// Package hdtest is the integration test harness for the helpdesk packages. It runs the
// real middleware, huma API and Postgres (TEST_DATABASE_URL, defaulting to the local
// helpdesk database). Each test creates its own workspaces, so packages run in parallel.
package hdtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/natsx"
	"github.com/useopendesk/opendesk/internal/storage"
)

const defaultDSN = "postgres://opendesk:opendesk@localhost:55432/opendesk_helpdesk?sslmode=disable"

// Secret is the AUTH_SECRET used by tests.
var Secret = []byte("test-only-auth-secret-0123456789abcdef")

var (
	once    sync.Once
	shared  *deps.Deps
	initErr error
	filesAt string
)

// Main runs a package's tests, then removes the files they stored, and exits.
// Every package that uses Deps or New calls it from TestMain.
func Main(m *testing.M) {
	code := m.Run()
	if filesAt != "" {
		_ = os.RemoveAll(filesAt)
	}
	os.Exit(code)
}

// Deps returns process-wide dependencies against the test database (migrated once).
func Deps(t testing.TB) *deps.Deps {
	t.Helper()
	once.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			dsn = defaultDSN
		}
		ctx := context.Background()
		if initErr = db.Migrate(ctx, dsn); initErr != nil {
			return
		}
		var database *db.DB
		if database, initErr = db.Open(ctx, dsn, dsn); initErr != nil {
			return
		}
		dir, err := os.MkdirTemp("", "hdtest-files-")
		if err != nil {
			initErr = err
			return
		}
		filesAt = dir
		st, err := storage.New(ctx, storage.Options{Driver: "local", Dir: dir})
		if err != nil {
			initErr = err
			return
		}
		box, err := crypto.NewBox([]byte("development-only-credentials-key"))
		if err != nil {
			initErr = err
			return
		}
		jc, err := river.NewClient[pgx.Tx](riverpgxv5.New(database.Pool), &river.Config{})
		if err != nil {
			initErr = err
			return
		}
		cfg := &config.Config{Env: "development", Role: "all", APIURL: "http://api.test", AppURL: "http://app.test", ServeWeb: true,
			GatewayURL: "ws://gateway.test/socket", AuthSecret: Secret, CredentialsKey: []byte("development-only-credentials-key")}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		if os.Getenv("HDTEST_LOG") != "" {
			log = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}
		shared = &deps.Deps{Cfg: cfg, Log: log, DB: database, Jobs: jc, Storage: st, Box: box,
			Tokens: auth.NewTokens(Secret), Limiter: httpx.NewLimiter()}
		domain.Configure(cfg.APIURL, Secret)
	})
	if initErr != nil {
		t.Fatalf("hdtest: %v", initErr)
	}
	return shared
}

// Env is one test server.
type Env struct {
	T   testing.TB
	D   *deps.Deps
	Srv *httptest.Server
	Ctx context.Context
}

// RegisterFunc matches the subpackages' Register.
type RegisterFunc func(d *deps.Deps, api huma.API, r chi.Router)

// New starts a server with the given areas registered behind the real middleware.
func New(t testing.TB, regs ...RegisterFunc) *Env {
	t.Helper()
	d := Deps(t)
	httpx.Install(d.Log)
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.SecurityHeaders(false), httpx.CORS([]string{"http://app.test"}))
	resolver := &auth.Resolver{Pool: d.DB.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}
	r.Use(resolver.Middleware)
	api := humachi.New(r, huma.DefaultConfig("opendesk test", "test"))
	for _, reg := range regs {
		reg(d, api, r)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &Env{T: t, D: d, Srv: srv, Ctx: context.Background()}
}

// User is a seeded account with a ready access token.
type User struct {
	ID    uuid.UUID
	Email string
	Name  string
	Token string
}

// WS is a seeded workspace with one member per role, its default brand (slug = the
// workspace slug, help center off) and that brand's widget.
type WS struct {
	ID        uuid.UUID
	Slug      string
	WidgetKey string
	BrandID   uuid.UUID
	WidgetID  uuid.UUID
	Owner     User
	Admin     User
	Agent     User
	Viewer    User
}

// Auth selects the credentials of a request.
type Auth struct {
	Bearer    string
	Workspace uuid.UUID
	Visitor   string
	Header    map[string]string
}

// As returns user credentials with the workspace header.
func (u User) As(ws WS) Auth { return Auth{Bearer: u.Token, Workspace: ws.ID} }

// NewWorkspace creates a workspace with an owner, admin, agent and viewer.
func (e *Env) NewWorkspace() WS {
	e.T.Helper()
	ws := WS{ID: ids.New()}
	ws.Slug = "ws-" + crypto.RandomString(10)
	ws.WidgetKey = "wk_" + crypto.RandomString(20)
	e.Exec(`INSERT INTO workspaces (id, name, slug, widget_key) VALUES ($1, $2, $3, $4)`, ws.ID, "Acme "+ws.Slug, ws.Slug, ws.WidgetKey)
	ws.BrandID, ws.WidgetID = ids.New(), ids.New()
	e.Exec(`INSERT INTO brands (id, workspace_id, name, slug, is_default) VALUES ($1, $2, $3, $4, true)`, ws.BrandID, ws.ID, "Acme "+ws.Slug, ws.Slug)
	e.Exec(`INSERT INTO widgets (id, workspace_id, brand_id, name, key) VALUES ($1, $2, $3, 'Website', $4)`, ws.WidgetID, ws.ID, ws.BrandID, ws.WidgetKey)
	ws.Owner = e.AddUser(ws, auth.RoleOwner, "Olivia Owner")
	ws.Admin = e.AddUser(ws, auth.RoleAdmin, "Adam Admin")
	ws.Agent = e.AddUser(ws, auth.RoleAgent, "Anna Agent")
	ws.Viewer = e.AddUser(ws, auth.RoleViewer, "Victor Viewer")
	return ws
}

// AddUser creates a user and adds them to ws with role.
func (e *Env) AddUser(ws WS, role auth.Role, name string) User {
	e.T.Helper()
	u := User{ID: ids.New(), Name: name}
	u.Email = strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "." + crypto.RandomString(6) + "@example.com"
	e.Exec(`INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, u.ID, u.Email, name)
	e.Exec(`INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, ws.ID, u.ID, string(role))
	// A live session that started a day ago: older than the recent sign-in owner-level
	// operations need (SignedIn makes a fresh one).
	sid := ids.New()
	e.Exec(`INSERT INTO sessions (id, user_id, refresh_hash, created_at, expires_at) VALUES ($1, $2, $3, now() - interval '1 day', now() + interval '1 day')`,
		sid, u.ID, []byte(sid.String()))
	tok, _, err := e.D.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAccess, SessionID: sid.String()}, u.ID.String(), time.Hour)
	if err != nil {
		e.T.Fatal(err)
	}
	u.Token = tok
	return u
}

// SignedIn returns u with a token on a stored session that started now: what owner-level
// operations check (a recent sign-in; a second factor when mfa is true).
func (e *Env) SignedIn(u User, mfa bool) User {
	e.T.Helper()
	sid := ids.New()
	e.Exec(`INSERT INTO sessions (id, user_id, refresh_hash, mfa_verified, expires_at) VALUES ($1, $2, $3, $4, now() + interval '1 day')`,
		sid, u.ID, []byte(sid.String()), mfa)
	tok, _, err := e.D.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAccess, SessionID: sid.String()}, u.ID.String(), time.Hour)
	if err != nil {
		e.T.Fatal(err)
	}
	u.Token = tok
	return u
}

// APIKey creates an API key for ws with scopes and returns its secret.
func (e *Env) APIKey(ws WS, scopes ...string) string {
	e.T.Helper()
	secret := "od_live_" + crypto.RandomToken(32)
	e.Exec(`INSERT INTO api_keys (id, workspace_id, name, prefix, key_hash, scopes) VALUES ($1,$2,'test',$3,$4,$5)`,
		ids.New(), ws.ID, secret[:12], crypto.HashToken(secret), scopes)
	return secret
}

// SetSettings merges keys into workspaces.settings.
func (e *Env) SetSettings(ws WS, settings map[string]any) {
	e.T.Helper()
	e.Exec(`UPDATE workspaces SET settings = settings || $2 WHERE id = $1`, ws.ID, settings)
}

// SetWidgetSettings merges keys into the settings of a widget (the workspace's default
// widget when id is uuid.Nil).
func (e *Env) SetWidgetSettings(ws WS, id uuid.UUID, settings map[string]any) {
	e.T.Helper()
	if id == uuid.Nil {
		id = ws.WidgetID
	}
	e.Exec(`UPDATE widgets SET settings = settings || $3 WHERE workspace_id = $1 AND id = $2`, ws.ID, id, settings)
}

// Exec runs SQL and fails the test on error.
func (e *Env) Exec(sql string, args ...any) {
	e.T.Helper()
	if _, err := e.D.DB.Pool.Exec(e.Ctx, sql, args...); err != nil {
		e.T.Fatalf("exec %q: %v", sql, err)
	}
}

// Scalar scans a single-row query into dst.
func (e *Env) Scalar(sql string, dst any, args ...any) {
	e.T.Helper()
	if err := e.D.DB.Pool.QueryRow(e.Ctx, sql, args...).Scan(dst); err != nil {
		e.T.Fatalf("query %q: %v", sql, err)
	}
}

// Resp is a response.
type Resp struct {
	Status int
	Body   []byte
	Header http.Header
}

// JSON decodes the body into v.
func (r *Resp) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %d %s: %v", r.Status, r.Body, err)
	}
}

// Map decodes the body into a map.
func (r *Resp) Map(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	r.JSON(t, &m)
	return m
}

// Do sends a JSON request. body may be nil, a string, []byte or anything JSON-able.
func (e *Env) Do(method, path string, a Auth, body any) *Resp {
	e.T.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.T.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.Srv.URL+path, rd)
	if err != nil {
		e.T.Fatal(err)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return e.send(req, a)
}

// Upload posts a multipart form with one file field "file".
func (e *Env) Upload(path string, a Auth, filename string, content []byte) *Resp {
	e.T.Helper()
	return e.UploadWith(http.MethodPost, path, a, filename, content)
}

// UploadWith sends a multipart "file" with method.
func (e *Env) UploadWith(method, path string, a Auth, filename string, content []byte) *Resp {
	e.T.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		e.T.Fatal(err)
	}
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, err := http.NewRequest(method, e.Srv.URL+path, &buf)
	if err != nil {
		e.T.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return e.send(req, a)
}

func (e *Env) send(req *http.Request, a Auth) *Resp {
	e.T.Helper()
	switch {
	case a.Visitor != "":
		req.Header.Set("Authorization", "Visitor "+a.Visitor)
	case a.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+a.Bearer)
	}
	if a.Workspace != uuid.Nil {
		req.Header.Set(auth.WorkspaceHeader, a.Workspace.String())
	}
	for k, v := range a.Header {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return &Resp{Status: res.StatusCode, Body: b, Header: res.Header}
}

// Want fails the test unless the status matches, printing the body.
func (r *Resp) Want(t testing.TB, status int) *Resp {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d, want %d: %s", r.Status, status, r.Body)
	}
	return r
}

// Jobs removes and returns the args of queued jobs of kind for the workspace.
func (e *Env) Jobs(kind string, wid uuid.UUID) []json.RawMessage {
	e.T.Helper()
	rows, err := e.D.DB.Pool.Query(e.Ctx, `DELETE FROM river_job WHERE kind = $1 AND args->>'workspace_id' = $2 RETURNING args`,
		kind, wid.String())
	if err != nil {
		e.T.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[json.RawMessage])
	if err != nil {
		e.T.Fatal(err)
	}
	return out
}

// Events returns the event types published for the workspace since seq, and the latest seq.
func (e *Env) Events(wid uuid.UUID, since int64) ([]string, int64) {
	e.T.Helper()
	rows, err := e.D.DB.Pool.Query(e.Ctx, `SELECT type, seq FROM events WHERE workspace_id = $1 AND seq > $2 ORDER BY seq`, wid, since)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var types []string
	latest := since
	for rows.Next() {
		var typ string
		var seq int64
		if err := rows.Scan(&typ, &seq); err != nil {
			e.T.Fatal(err)
		}
		types = append(types, typ)
		latest = seq
	}
	return types, latest
}

// Fmt is fmt.Sprintf, for building paths.
func Fmt(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// Live connects the test dependencies to an in-process NATS for the rest of the test and
// returns a function giving the next live event published for the workspace
// (od.live.<wid>), or nil when none arrives within half a second.
func (e *Env) Live(wid uuid.UUID) func() *events.LiveEvent {
	e.T.Helper()
	nc, err := natsx.Connect(natsx.Options{Name: "hdtest"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		e.T.Fatal(err)
	}
	sub, err := nc.SubscribeSync(events.LiveSubjectPrefix + wid.String())
	if err != nil {
		e.T.Fatal(err)
	}
	prev := e.D.NATS
	e.D.NATS = nc
	e.T.Cleanup(func() {
		e.D.NATS = prev
		nc.Close()
	})
	return func() *events.LiveEvent {
		msg, err := sub.NextMsg(500 * time.Millisecond)
		if err != nil {
			return nil
		}
		var ev events.LiveEvent
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			e.T.Fatal(err)
		}
		return &ev
	}
}
