package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// env is the middleware in front of test handlers, on a real Postgres
// (OPENDESK_TEST_DATABASE_URL).
type env struct {
	t      *testing.T
	db     *db.DB
	srv    *httptest.Server
	tokens *auth.Tokens
	calls  atomic.Int64
	fails  atomic.Int64
	gate   chan struct{}
	inside chan struct{}
}

var migrateOnce sync.Once

func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("OPENDESK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set OPENDESK_TEST_DATABASE_URL to run database tests")
	}
	ctx := context.Background()
	var merr error
	migrateOnce.Do(func() { merr = db.Migrate(ctx, url) })
	if merr != nil {
		t.Fatalf("migrate: %v", merr)
	}
	database, err := db.Open(ctx, url, url)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	box, _ := crypto.NewBox([]byte("development-only-credentials-key"))
	secret := []byte("test-auth-secret-test-auth-secret-0000")
	e := &env{t: t, db: database, tokens: auth.NewTokens(secret), gate: make(chan struct{}), inside: make(chan struct{}, 1)}

	r := chi.NewRouter()
	r.Use(httpx.RequestID, (&auth.Resolver{Pool: database.Pool, Tokens: e.tokens, ClientIP: httpx.ClientIP(false, 0)}).Middleware,
		New(database.Pool, box, secret, log).Middleware)
	r.Post("/v1/things", func(w http.ResponseWriter, r *http.Request) {
		n := e.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", fmt.Sprintf("/v1/things/%d", n))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"n": n, "echo": string(body), "secret_marker": "plaintext-marker"})
	})
	r.Post("/v1/flaky", func(w http.ResponseWriter, r *http.Request) {
		if e.fails.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		n := e.calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"n":%d}`, n)
	})
	r.Post("/v1/slow", func(w http.ResponseWriter, r *http.Request) {
		e.calls.Add(1)
		e.inside <- struct{}{}
		<-e.gate
		w.WriteHeader(http.StatusCreated)
	})
	r.Post("/v1/big", func(w http.ResponseWriter, r *http.Request) {
		e.calls.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte("x"), MaxResponseBody+10))
	})
	r.Post("/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		e.calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: done\ndata: {}\n\n")
		_ = http.NewResponseController(w).Flush()
	})
	r.Post("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) { e.calls.Add(1); w.WriteHeader(http.StatusOK) })
	e.srv = httptest.NewServer(r)
	t.Cleanup(func() {
		e.srv.Close()
		database.Close()
	})
	return e
}

// caller is a principal: a user with a workspace selected, or an API key.
type caller struct {
	bearer    string
	workspace string
	userID    uuid.UUID
}

func (e *env) user(ws uuid.UUID) caller {
	e.t.Helper()
	ctx := context.Background()
	var uid uuid.UUID
	if err := e.db.Pool.QueryRow(ctx, `INSERT INTO users (email, name) VALUES ($1, 'Idem') RETURNING id`,
		"idem-"+crypto.RandomString(8)+"@example.com").Scan(&uid); err != nil {
		e.t.Fatal(err)
	}
	if ws != uuid.Nil {
		if _, err := e.db.Pool.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'agent')`, ws, uid); err != nil {
			e.t.Fatal(err)
		}
	}
	tok, _, err := e.tokens.Sign(auth.Claims{Purpose: auth.PurposeAccess, SessionID: uuid.NewString()}, uid.String(), time.Minute)
	if err != nil {
		e.t.Fatal(err)
	}
	c := caller{bearer: tok, userID: uid}
	if ws != uuid.Nil {
		c.workspace = ws.String()
	}
	return c
}

func (e *env) apiKey(ws uuid.UUID) caller {
	e.t.Helper()
	key := "od_live_" + crypto.RandomToken(24)
	if _, err := e.db.Pool.Exec(context.Background(), `INSERT INTO api_keys (workspace_id, name, prefix, key_hash, scopes)
		VALUES ($1, 'idem', 'od_live_', $2, '{tickets:write}')`, ws, crypto.HashToken(key)); err != nil {
		e.t.Fatal(err)
	}
	return caller{bearer: key}
}

func (e *env) workspace() uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	slug := "idem-" + strings.ToLower(crypto.RandomString(10))
	if err := e.db.Pool.QueryRow(context.Background(), `INSERT INTO workspaces (name, slug, widget_key) VALUES ('Idem', $1, $1) RETURNING id`,
		slug).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

type result struct {
	status int
	header http.Header
	body   []byte
}

func (r result) code() string {
	var p struct{ Code string }
	_ = json.Unmarshal(r.body, &p)
	return p.Code
}

func (e *env) post(c caller, path, key, body string, hdr ...string) result {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.workspace != "" {
		req.Header.Set(auth.WorkspaceHeader, c.workspace)
	}
	if key != "-" {
		req.Header.Set(Header, key)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return result{status: res.StatusCode, header: res.Header, body: raw}
}

func TestReplayReturnsStoredResponse(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	for _, c := range []caller{e.user(ws), e.apiKey(ws)} {
		key := uuid.NewString()
		before := e.calls.Load()
		first := e.post(c, "/v1/things", key, `{"a":1}`)
		if first.status != 201 || first.header.Get(ReplayedHeader) != "" {
			t.Fatalf("first: %d %q %s", first.status, first.header.Get(ReplayedHeader), first.body)
		}
		again := e.post(c, "/v1/things", key, `{"a":1}`)
		if again.status != 201 || again.header.Get(ReplayedHeader) != "true" {
			t.Fatalf("replay: %d %q %s", again.status, again.header.Get(ReplayedHeader), again.body)
		}
		if !bytes.Equal(first.body, again.body) || again.header.Get("Location") != first.header.Get("Location") ||
			again.header.Get("Content-Type") != "application/json" {
			t.Fatalf("replay differs: %s / %s, %q", first.body, again.body, again.header.Get("Location"))
		}
		if got := e.calls.Load() - before; got != 1 {
			t.Fatalf("handler ran %d times, want 1", got)
		}
	}
}

func TestBodyStoredSealed(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	c := e.user(ws)
	key := uuid.NewString()
	if r := e.post(c, "/v1/things", key, `{"a":1}`); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var enc, hash []byte
	var wsCol uuid.UUID
	if err := e.db.Pool.QueryRow(context.Background(), `SELECT body_enc, request_hash, workspace_id FROM idempotency_keys
		WHERE user_id = $1 AND key = $2`, c.userID, key).Scan(&enc, &hash, &wsCol); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("plaintext-marker")) || len(enc) == 0 {
		t.Fatal("response body is stored in plaintext")
	}
	if len(hash) != 32 || wsCol != ws {
		t.Fatalf("hash %d bytes, workspace %s", len(hash), wsCol)
	}
}

func TestKeyReusedForDifferentRequest(t *testing.T) {
	e := newEnv(t)
	c := e.user(e.workspace())
	key := uuid.NewString()
	if r := e.post(c, "/v1/things", key, `{"a":1}`); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	for _, r := range []result{
		e.post(c, "/v1/things", key, `{"a":2}`),
		e.post(c, "/v1/things?x=1", key, `{"a":1}`),
		e.post(c, "/v1/flaky", key, `{"a":1}`),
	} {
		if r.status != 422 || r.code() != "idempotency_key_reused" {
			t.Fatalf("reused: %d %s", r.status, r.body)
		}
	}
}

func TestScopedPerPrincipalAndWorkspace(t *testing.T) {
	e := newEnv(t)
	ws1, ws2 := e.workspace(), e.workspace()
	u := e.user(ws1)
	if _, err := e.db.Pool.Exec(context.Background(), `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'agent')`, ws2, u.userID); err != nil {
		t.Fatal(err)
	}
	u2 := u
	u2.workspace = ws2.String()
	noWS := u
	noWS.workspace = ""
	key := uuid.NewString()
	before := e.calls.Load()
	for _, c := range []caller{u, u2, noWS, e.user(ws1), e.apiKey(ws1)} {
		if r := e.post(c, "/v1/things", key, `{"a":1}`); r.status != 201 || r.header.Get(ReplayedHeader) != "" {
			t.Fatalf("%d %q %s", r.status, r.header.Get(ReplayedHeader), r.body)
		}
	}
	if got := e.calls.Load() - before; got != 5 {
		t.Fatalf("handler ran %d times, want 5 independent requests", got)
	}
	if r := e.post(noWS, "/v1/things", key, `{"a":1}`); r.header.Get(ReplayedHeader) != "true" {
		t.Fatalf("no-workspace key not replayed: %d", r.status)
	}
}

func TestInvalidKey(t *testing.T) {
	e := newEnv(t)
	c := e.user(e.workspace())
	for _, k := range []string{"", strings.Repeat("k", 256), "tab\tkey", "ключ"} {
		r := e.post(c, "/v1/things", k, `{}`)
		if r.status != 422 || r.code() != "validation_failed" || r.header.Get("Content-Type") != "application/problem+json" {
			t.Fatalf("key %q: %d %s", k, r.status, r.body)
		}
	}
	if r := e.post(c, "/v1/things", strings.Repeat("k", 255), `{}`); r.status != 201 {
		t.Fatalf("255-char key: %d %s", r.status, r.body)
	}
}

func TestServerErrorReleasesKey(t *testing.T) {
	e := newEnv(t)
	c := e.apiKey(e.workspace())
	key := uuid.NewString()
	if r := e.post(c, "/v1/flaky", key, `{}`); r.status != 500 {
		t.Fatalf("first: %d", r.status)
	}
	r := e.post(c, "/v1/flaky", key, `{}`)
	if r.status != 201 || r.header.Get(ReplayedHeader) != "" {
		t.Fatalf("retry after 5xx: %d %q", r.status, r.header.Get(ReplayedHeader))
	}
	if r := e.post(c, "/v1/flaky", key, `{}`); r.header.Get(ReplayedHeader) != "true" {
		t.Fatalf("success not stored: %d", r.status)
	}
}

func TestConcurrentRequestInProgress(t *testing.T) {
	e := newEnv(t)
	c := e.user(e.workspace())
	key := uuid.NewString()
	done := make(chan result, 1)
	go func() { done <- e.post(c, "/v1/slow", key, `{}`) }()
	select {
	case <-e.inside:
	case <-time.After(10 * time.Second):
		t.Fatal("first request never reached the handler")
	}
	r := e.post(c, "/v1/slow", key, `{}`)
	if r.status != 409 || r.code() != "idempotency_in_progress" {
		t.Fatalf("concurrent: %d %s", r.status, r.body)
	}
	close(e.gate)
	if first := <-done; first.status != 201 {
		t.Fatalf("first: %d", first.status)
	}
	if r := e.post(c, "/v1/slow", key, `{}`); r.status != 201 || r.header.Get(ReplayedHeader) != "true" {
		t.Fatalf("after finish: %d %q", r.status, r.header.Get(ReplayedHeader))
	}
}

func TestNotStored(t *testing.T) {
	e := newEnv(t)
	ws := e.workspace()
	c := e.user(ws)
	cases := []struct {
		path string
		hdr  []string
	}{
		{"/v1/big", nil},
		{"/v1/stream", nil},
		{"/v1/things", []string{"Accept", "text/event-stream"}},
		{"/v1/auth/login", nil},
	}
	for _, tc := range cases {
		key := uuid.NewString()
		before := e.calls.Load()
		e.post(c, tc.path, key, `{}`, tc.hdr...)
		r := e.post(c, tc.path, key, `{}`, tc.hdr...)
		if r.header.Get(ReplayedHeader) != "" || e.calls.Load()-before != 2 {
			t.Fatalf("%s was stored", tc.path)
		}
		var n int
		_ = e.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n)
		if n != 0 {
			t.Fatalf("%s left %d rows", tc.path, n)
		}
	}
	// No header, or no principal: not tracked.
	before := e.calls.Load()
	e.post(c, "/v1/things", "-", `{}`)
	e.post(c, "/v1/things", "-", `{}`)
	anon := caller{}
	key := uuid.NewString()
	e.post(anon, "/v1/things", key, `{}`)
	if r := e.post(anon, "/v1/things", key, `{}`); r.header.Get(ReplayedHeader) != "" || e.calls.Load()-before != 4 {
		t.Fatal("untracked request was replayed")
	}
}

func TestExpiredAndAbandonedKeys(t *testing.T) {
	e := newEnv(t)
	c := e.user(e.workspace())
	ctx := context.Background()

	key := uuid.NewString()
	e.post(c, "/v1/things", key, `{"a":1}`)
	if _, err := e.db.Pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours' WHERE key = $1`, key); err != nil {
		t.Fatal(err)
	}
	if r := e.post(c, "/v1/things", key, `{"other":true}`); r.status != 201 || r.header.Get(ReplayedHeader) != "" {
		t.Fatalf("expired key: %d %q %s", r.status, r.header.Get(ReplayedHeader), r.body)
	}

	// An in-progress claim whose request never finished is released after a while.
	stale := uuid.NewString()
	if _, err := e.db.Pool.Exec(ctx, `INSERT INTO idempotency_keys (user_id, workspace_id, key, method, path, request_hash, created_at)
		VALUES ($1, $2, $3, 'POST', '/v1/things', '\x00', now() - interval '10 minutes')`, c.userID, uuid.MustParse(c.workspace), stale); err != nil {
		t.Fatal(err)
	}
	if r := e.post(c, "/v1/things", stale, `{}`); r.status != 201 || r.header.Get(ReplayedHeader) != "" {
		t.Fatalf("abandoned key: %d %s", r.status, r.body)
	}
}
