package sample_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

	"github.com/useopendesk/opendesk/internal/analytics"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/collab"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/plan"
	"github.com/useopendesk/opendesk/internal/sample"
	"github.com/useopendesk/opendesk/internal/storage"
)

// The harness: a real Postgres (TEST_DATABASE_URL, default opendesk_sample on
// localhost:55432), the core, helpdesk and Collab routes behind the real auth middleware,
// billing off. Workspaces are created through core's own route, so they have its defaults
// (brand, widget, labels, views, SLA policy).

const defaultDSN = "postgres://opendesk:opendesk@localhost:55432/opendesk_sample?sslmode=disable"

var secret = []byte("test-only-auth-secret-0123456789abcdef")

var (
	once   sync.Once
	shared *env
	setup  error
)

type env struct {
	d   *deps.Deps
	srv *httptest.Server
}

func testEnv(t *testing.T) *env {
	t.Helper()
	once.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			dsn = defaultDSN
		}
		ctx := context.Background()
		if setup = db.Migrate(ctx, dsn); setup != nil {
			return
		}
		var database *db.DB
		if database, setup = db.Open(ctx, dsn, dsn); setup != nil {
			return
		}
		dir, err := os.MkdirTemp("", "sampletest-files-")
		if err != nil {
			setup = err
			return
		}
		st, err := storage.New(ctx, storage.Options{Driver: "local", Dir: dir})
		if err != nil {
			setup = err
			return
		}
		box, _ := crypto.NewBox([]byte("development-only-credentials-key"))
		jc, err := river.NewClient[pgx.Tx](riverpgxv5.New(database.Pool), &river.Config{})
		if err != nil {
			setup = err
			return
		}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		if os.Getenv("SAMPLETEST_LOG") != "" {
			log = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}
		cfg := &config.Config{Env: "development", Role: "all", APIURL: "http://api.test", AppURL: "http://app.test", ServeWeb: true,
			GatewayURL: "ws://gateway.test/socket", AuthSecret: secret, MailFrom: "opendesk <notifications@opendesk.test>", MailDriver: "log",
			Collab: true, CollabIMAP: true}
		d := &deps.Deps{Cfg: cfg, Log: log, DB: database, Jobs: jc, Storage: st, Box: box, Tokens: auth.NewTokens(secret),
			Limiter: httpx.NewLimiter(), Plan: plan.New(false, 1), Analytics: analytics.New("", "", log),
			Captcha: captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Disabled: true})}
		domain.Configure(cfg.APIURL, secret)
		httpx.Install(log)
		r := chi.NewRouter()
		r.Use((&auth.Resolver{Pool: database.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}).Middleware)
		api := humachi.New(r, huma.DefaultConfig("sample test", "0"))
		core.Register(d, api, r)
		helpdesk.Register(d, api, r)
		collab.Register(d, api, r)
		shared = &env{d: d, srv: httptest.NewServer(r)}
	})
	if setup != nil {
		t.Skipf("sample tests need Postgres (TEST_DATABASE_URL): %v", setup)
	}
	return shared
}

type workspace struct {
	e     *env
	id    uuid.UUID
	owner uuid.UUID
	token string
}

// newWorkspace signs up an owner and creates their workspace through core.
func (e *env) newWorkspace(t *testing.T, name string) *workspace {
	t.Helper()
	ctx := context.Background()
	w := &workspace{e: e, owner: ids.New()}
	email := "owner-" + strings.ToLower(crypto.RandomString(8)) + "@example.com"
	if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO users (id, email, name, email_verified_at) VALUES ($1, $2, 'Olivia Owner', now())`, w.owner, email); err != nil {
		t.Fatal(err)
	}
	tok, _, err := e.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAccess, SessionID: ids.New().String()}, w.owner.String(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w.token = tok
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	w.do(t, http.MethodPost, "/v1/workspaces", map[string]any{"name": name}, http.StatusCreated, &out)
	w.id = out.ID
	return w
}

func (w *workspace) do(t *testing.T, method, path string, body any, want int, out any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.e.srv.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Content-Type", "application/json")
	if w.id != uuid.Nil {
		req.Header.Set(auth.WorkspaceHeader, w.id.String())
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
}

// all pages through a list route and returns every row.
func (w *workspace) all(t *testing.T, path string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	cursor := ""
	for {
		u, _ := url.Parse(path)
		q := u.Query()
		q.Set("limit", "100")
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		u.RawQuery = q.Encode()
		var page struct {
			Data       []map[string]any `json:"data"`
			NextCursor *string          `json:"next_cursor"`
		}
		w.do(t, http.MethodGet, u.String(), nil, http.StatusOK, &page)
		rows = append(rows, page.Data...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			return rows
		}
		cursor = *page.NextCursor
	}
}

func (w *workspace) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := w.e.d.DB.Pool.QueryRow(context.Background(), sql, append([]any{w.id}, args...)...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// mailJobs are the job kinds that send mail, call a model or reach a provider: none may be
// queued for sample data.
var mailJobs = []string{"email.send_message", "email.send_ticket_email", "email.chat_follow_up", "mail.system",
	"core.notification_email", "helpdesk.ticket_trigger", "ai.embed", "ai.widget_answer", "ai.email_answer", "ai.summarize",
	"push.notification", "collab.sync_account", "collab.apply", "webhooks.dispatch"}

func TestSeedSmall(t *testing.T) {
	e := testEnv(t)
	ctx := context.Background()
	w := e.newWorkspace(t, "Northwind Demo")
	started := time.Now()
	jobsBefore := time.Now().Add(-time.Second)

	got, err := sample.Seed(ctx, e.d, w.id, sample.Options{Size: sample.SizeSmall, Seed: 42, OwnerID: w.owner})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("small: %+v in %s", got, time.Since(started).Round(time.Millisecond))
	want := sample.Counts{Members: 3, Teams: 2, Contacts: 30, Companies: 6, Tickets: 40, Chats: 5, Articles: 6, Collections: 2,
		MailAccounts: 1, MailThreads: 20}
	want.Messages = got.Messages
	if got != want {
		t.Fatalf("counts %+v, want %+v", got, want)
	}
	if got.Messages < got.Tickets+got.Chats {
		t.Fatalf("only %d messages for %d tickets", got.Messages, got.Tickets+got.Chats)
	}

	// Visible through the normal list routes.
	tickets := w.all(t, "/v1/tickets")
	if len(tickets) != 45 {
		t.Fatalf("tickets list has %d, want 45", len(tickets))
	}
	statuses := map[string]int{}
	channels := map[string]int{}
	for _, tk := range tickets {
		statuses[tk["status"].(string)]++
		channels[tk["channel"].(string)]++
	}
	for _, s := range []string{"open", "pending", "solved"} {
		if statuses[s] == 0 {
			t.Errorf("no %s tickets: %v", s, statuses)
		}
	}
	if channels["email"] == 0 || channels["chat"] != 5 {
		t.Errorf("channels %v", channels)
	}
	contacts := w.all(t, "/v1/contacts")
	if len(contacts) != 30 {
		t.Fatalf("contacts list has %d, want 30", len(contacts))
	}
	for _, c := range contacts {
		if email, _ := c["email"].(string); !strings.HasSuffix(email, ".invalid") {
			t.Fatalf("contact email %q is not on .invalid", email)
		}
	}
	if arts := w.all(t, "/v1/kb/articles"); len(arts) != 6 {
		t.Fatalf("kb lists %d articles, want 6", len(arts))
	}
	var accounts []map[string]any
	w.do(t, http.MethodGet, "/v1/collab/accounts", nil, http.StatusOK, &accounts)
	if len(accounts) != 1 || accounts[0]["status"] != "active" {
		t.Fatalf("collab accounts %v", accounts)
	}
	if threads := w.all(t, "/v1/collab/threads?box=all&account_id="+accounts[0]["id"].(string)); len(threads) < 15 {
		t.Fatalf("collab lists %d threads", len(threads))
	}
	if n := w.count(t, `SELECT count(*) FROM collab_threads WHERE workspace_id = $1`); n != 20 {
		t.Fatalf("%d collab threads stored, want 20", n)
	}
	if n := w.count(t, `SELECT count(*) FROM collab_messages WHERE workspace_id = $1 AND draft`); n < 2 {
		t.Errorf("%d collab drafts", n)
	}

	// Time: inside the window, relative to now, with fresh activity today.
	if n := w.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND (created_at < now() - interval '31 days' OR created_at > now())`); n != 0 {
		t.Errorf("%d tickets outside the window", n)
	}
	if n := w.count(t, `SELECT count(*) FROM messages WHERE workspace_id = $1 AND created_at > now()`); n != 0 {
		t.Errorf("%d messages in the future", n)
	}
	if n := w.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND created_at > now() - interval '1 day'`); n < 5 {
		t.Errorf("only %d tickets today", n)
	}
	if n := w.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND channel = 'chat' AND status = 'open'
		AND assignee_id IS NULL AND waiting_since > now() - interval '1 hour'`); n < 2 {
		t.Errorf("%d chats waiting, want 2", n)
	}
	if n := w.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND channel <> 'chat' AND status = 'open' AND waiting_since IS NOT NULL`); n == 0 {
		t.Error("no tickets awaiting a reply")
	}
	if n := w.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND csat_score IS NOT NULL AND status IN ('solved', 'closed')`); n == 0 {
		t.Error("no CSAT on solved tickets")
	}
	if n := w.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND sla_policy_id IS NOT NULL AND status IN ('open', 'pending', 'on_hold')
		AND (sla_first_response_breached_at IS NOT NULL OR sla_resolution_breached_at IS NOT NULL OR resolution_due_at < now())`); n == 0 {
		t.Error("no overdue tickets")
	}
	if n := w.count(t, `SELECT count(*) FROM messages WHERE workspace_id = $1 AND kind = 'note' AND body_html LIKE '%data-id="%'`); n == 0 {
		t.Error("no notes with mentions")
	}
	if n := w.count(t, `SELECT count(*) FROM ticket_tags tt JOIN tickets t ON t.id = tt.ticket_id WHERE t.workspace_id = $1`); n == 0 {
		t.Error("no labels on tickets")
	}
	if n := w.count(t, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND fields ? 'plan'`); n == 0 {
		t.Error("no contact fields")
	}

	// Never deliverable, never sent.
	if n := w.count(t, `SELECT count(*) FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1 AND u.id <> $2
		AND (u.email NOT LIKE '%.example.invalid' OR u.password_hash IS NOT NULL OR u.email_verified_at IS NULL)`, w.owner); n != 0 {
		t.Errorf("%d generated teammates are deliverable, have a password or are unverified", n)
	}
	for _, q := range []string{
		`SELECT count(*) FROM contacts WHERE workspace_id = $1 AND (email IS NULL OR email NOT LIKE '%.invalid')`,
		`SELECT count(*) FROM companies WHERE workspace_id = $1 AND domain NOT LIKE '%.invalid'`,
		`SELECT count(*) FROM collab_accounts WHERE workspace_id = $1 AND (email NOT LIKE '%.invalid' OR NOT (config->>'demo')::boolean)`,
		`SELECT count(*) FROM collab_messages WHERE workspace_id = $1 AND (from_addr->>'email' NOT LIKE '%.invalid'
			OR EXISTS (SELECT 1 FROM jsonb_array_elements(to_addrs || cc_addrs) a WHERE a->>'email' NOT LIKE '%.invalid'))`,
		`SELECT count(*) FROM messages WHERE workspace_id = $1 AND delivery_status = 'pending'`,
		`SELECT count(*) FROM contacts WHERE workspace_id = $1 AND avatar_retry_at IS NOT NULL`,
		`SELECT count(*) FROM companies WHERE workspace_id = $1 AND avatar_retry_at IS NOT NULL`,
		`SELECT count(*) FROM messages WHERE workspace_id = $1 AND from_email <> '' AND from_email NOT LIKE '%.invalid' AND author_type = 'contact'`,
	} {
		if n := w.count(t, q); n != 0 {
			t.Errorf("%d rows for %s", n, q)
		}
	}
	var queued int
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE created_at >= $1 AND kind = ANY($2)`, jobsBefore, mailJobs).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Errorf("%d jobs that send, call a model or reach a provider were queued", queued)
	}

	// A second batch adds as much again.
	again, err := sample.Seed(ctx, e.d, w.id, sample.Options{Size: sample.SizeSmall, Seed: 43, OwnerID: w.owner})
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if again.Contacts != 30 || again.Members != 3 || again.Tickets != 40 || again.MailAccounts != 1 || again.Teams != 0 {
		t.Fatalf("second batch %+v", again)
	}
	if n := w.count(t, `SELECT count(*) FROM tickets WHERE workspace_id = $1`); n != 90 {
		t.Errorf("%d tickets after two batches, want 90", n)
	}
	if n := w.count(t, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND email LIKE '%+2@%'`); n != 30 {
		t.Errorf("%d second-batch contacts, want 30", n)
	}
}

func TestSeedKinds(t *testing.T) {
	e := testEnv(t)
	w := e.newWorkspace(t, "Kinds Demo")
	got, err := sample.Seed(context.Background(), e.d, w.id, sample.Options{Size: sample.SizeSmall, Kinds: []sample.Kind{sample.KindKB, sample.KindChat}, Seed: 7, OwnerID: w.owner})
	if err != nil {
		t.Fatal(err)
	}
	if got.Articles != 6 || got.Chats != 5 || got.Tickets != 0 || got.Members != 0 || got.MailAccounts != 0 {
		t.Fatalf("counts %+v", got)
	}
	if _, err := sample.Seed(context.Background(), e.d, w.id, sample.Options{Size: "huge"}); err == nil {
		t.Fatal("an unknown size was accepted")
	}
	if _, err := sample.Seed(context.Background(), e.d, w.id, sample.Options{Kinds: []sample.Kind{"pets"}}); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
}

// The same seed writes the same people.
func TestSeedDeterministic(t *testing.T) {
	e := testEnv(t)
	names := func(seed int64) []string {
		w := e.newWorkspace(t, "Same Demo")
		if _, err := sample.Seed(context.Background(), e.d, w.id, sample.Options{Size: sample.SizeSmall, Kinds: []sample.Kind{sample.KindTeam, sample.KindTickets},
			Seed: seed, OwnerID: w.owner}); err != nil {
			t.Fatal(err)
		}
		rows, err := e.d.DB.Pool.Query(context.Background(), `SELECT c.name || ' ' || split_part(c.email::text, '@', 1) || ' ' || COALESCE(co.name, '')
			FROM contacts c LEFT JOIN companies co ON co.id = c.company_id WHERE c.workspace_id = $1 ORDER BY 1`, w.id)
		if err != nil {
			t.Fatal(err)
		}
		out, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b, c := names(99), names(99), names(100)
	if strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatal("the same seed gave different contacts")
	}
	if strings.Join(a, "|") == strings.Join(c, "|") {
		t.Fatal("another seed gave the same contacts")
	}
}

func TestSeedRealistic(t *testing.T) {
	if testing.Short() {
		t.Skip("realistic volume")
	}
	e := testEnv(t)
	w := e.newWorkspace(t, "Realistic Demo")
	started := time.Now()
	got, err := sample.Seed(context.Background(), e.d, w.id, sample.Options{Size: sample.SizeRealistic, Seed: 1, OwnerID: w.owner})
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(started)
	t.Logf("realistic: %+v in %s", got, took.Round(time.Millisecond))
	if got.Tickets < 400 || got.Chats != 40 || got.Contacts != 300 || got.Members != 8 || got.Teams != 4 || got.MailThreads != 120 ||
		got.Views == 0 || got.SavedReplies == 0 || got.Articles != 25 {
		t.Fatalf("counts %+v", got)
	}
	if took > 2*time.Minute {
		t.Fatalf("realistic took %s", took)
	}
	// Reports see a curve: tickets on most days of the window, fewer on weekends.
	if n := w.count(t, `SELECT count(DISTINCT created_at::date) FROM tickets WHERE workspace_id = $1`); n < 60 {
		t.Errorf("tickets on only %d days", n)
	}
	var weekday, weekend float64
	if err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT
		count(*) FILTER (WHERE extract(isodow FROM created_at) < 6) / 5.0, count(*) FILTER (WHERE extract(isodow FROM created_at) >= 6) / 2.0
		FROM tickets WHERE workspace_id = $1`, w.id).Scan(&weekday, &weekend); err != nil {
		t.Fatal(err)
	}
	if weekend >= weekday {
		t.Errorf("weekend days (%.0f) as busy as weekdays (%.0f)", weekend, weekday)
	}
	var overview map[string]any
	w.do(t, http.MethodGet, "/v1/reports/overview", nil, http.StatusOK, &overview)
	if overview["created"].(float64) == 0 || overview["solved"].(float64) == 0 || overview["csat_count"].(float64) == 0 {
		t.Errorf("report overview %v", overview)
	}
}

// TestSeedLarge measures the large size; it runs only with SAMPLE_LARGE=1.
func TestSeedLarge(t *testing.T) {
	if os.Getenv("SAMPLE_LARGE") == "" {
		t.Skip("set SAMPLE_LARGE=1 to seed the large size")
	}
	e := testEnv(t)
	w := e.newWorkspace(t, "Large Demo")
	started := time.Now()
	got, err := sample.Seed(context.Background(), e.d, w.id, sample.Options{Size: sample.SizeLarge, Seed: 5, OwnerID: w.owner})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("large: %+v in %s", got, time.Since(started).Round(time.Millisecond))
	if got.Tickets < 5000 || got.Chats != 300 || got.Contacts != 3000 || got.Members != 25 || got.MailThreads != 600 || got.Articles != 80 {
		t.Fatalf("counts %+v", got)
	}
}
