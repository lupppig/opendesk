package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/useopendesk/opendesk/internal/analytics"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/natsx"
	"github.com/useopendesk/opendesk/internal/storage"
)

// testEnv is a core API backed by a real Postgres (OPENDESK_TEST_DATABASE_URL).
type testEnv struct {
	t   *testing.T
	d   *deps.Deps
	s   *svc
	srv *httptest.Server
}

var migrateOnce sync.Once

func newEnv(t *testing.T) *testEnv {
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
	nc, err := natsx.Connect(natsx.Options{StoreDir: t.TempDir(), Name: "core-test"}, log)
	if err != nil {
		t.Fatal(err)
	}
	box, _ := crypto.NewBox([]byte("development-only-credentials-key"))
	cfg := &config.Config{Env: "development", APIURL: "http://api.test", AppURL: "http://app.test", ServeWeb: true, GatewayURL: "ws://gw.test/socket",
		AuthSecret: []byte("test-auth-secret-test-auth-secret-0000"), SignupEnabled: true}
	d := &deps.Deps{Cfg: cfg, Log: log, DB: database, NATS: nc, Mailer: nil, Analytics: analytics.New("", "", log), Box: box,
		Tokens: auth.NewTokens(cfg.AuthSecret), Captcha: captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Disabled: true}),
		Limiter: httpx.NewLimiter()}
	if d.Storage, err = storage.New(ctx, storage.Options{Driver: "local", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	domain.Configure(cfg.APIURL, cfg.AuthSecret)
	d.Jobs, err = river.NewClient[pgx.Tx](riverpgxv5.New(database.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	httpx.Install(log)
	r := chi.NewRouter()
	resolver := &auth.Resolver{Pool: database.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}
	r.Use(resolver.Middleware)
	api := humachi.New(r, huma.DefaultConfig("test", "0"))
	Register(d, api, r)
	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		srv.Close()
		nc.Close()
		database.Close()
	})
	return &testEnv{t: t, d: d, s: newSvc(d), srv: srv}
}

// client is a browser-like caller with a cookie jar.
type client struct {
	e         *testEnv
	jar       http.CookieJar
	token     string
	workspace string
}

func (e *testEnv) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, jar: jar}
}

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

func (c *client) do(method, path string, body any, hdr ...string) resp {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.workspace != "" {
		req.Header.Set(auth.WorkspaceHeader, c.workspace)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	// The test server is http, but the refresh cookie is path-scoped like production.
	for _, ck := range c.jar.Cookies(req.URL) {
		req.AddCookie(ck)
	}
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	c.jar.SetCookies(req.URL, res.Cookies())
	out := resp{Status: res.StatusCode, Raw: raw, Header: res.Header}
	if len(raw) > 0 && strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

func (c *client) cookie(name string) string {
	u := c.e.srv.URL + "/v1/auth/refresh"
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	for _, ck := range c.jar.Cookies(req.URL) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// accountCookie returns the name and value of the client's account refresh cookie (the
// first, when it holds several).
func (c *client) accountCookie() (string, string) {
	for _, ck := range c.cookies() {
		if strings.HasPrefix(ck.Name, RefreshCookiePrefix) {
			return ck.Name, ck.Value
		}
	}
	return "", ""
}

// cookies lists the cookies the client sends to the auth routes.
func (c *client) cookies() []*http.Cookie {
	req, _ := http.NewRequest(http.MethodGet, c.e.srv.URL+"/v1/auth/refresh", nil)
	return c.jar.Cookies(req.URL)
}

func (r resp) str(path ...string) string {
	var v any = r.Body
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[p]
	}
	s, _ := v.(string)
	return s
}

func uniqueEmail(t *testing.T, tag string) string {
	return tag + "-" + crypto.RandomString(8) + "@example.com"
}

const goodPassword = "correct horse battery staple 42"

// signup registers a user and returns a signed-in client.
func (e *testEnv) signup(tag string) (*client, string) {
	e.t.Helper()
	c := e.client()
	email := uniqueEmail(e.t, tag)
	r := c.do("POST", "/v1/auth/register", map[string]any{"email": email, "password": goodPassword, "name": "Test " + strings.ToUpper(tag[:1]) + tag[1:]})
	if r.Status != 200 {
		e.t.Fatalf("register: %d %s", r.Status, r.Raw)
	}
	c.token = r.str("access_token")
	return c, email
}

// workspace creates a workspace and selects it on c.
func (c *client) newWorkspace(name string) string {
	c.e.t.Helper()
	r := c.do("POST", "/v1/workspaces", map[string]any{"name": name})
	if r.Status != 201 {
		c.e.t.Fatalf("create workspace: %d %s", r.Status, r.Raw)
	}
	c.workspace = r.str("id")
	return c.workspace
}

// lastMail returns the newest queued system email to addr.
func (e *testEnv) lastMail(addr string) (subject, text string) {
	e.t.Helper()
	var raw []byte
	err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT args FROM river_job WHERE kind = 'mail.system' AND args->>'to' = $1
		ORDER BY id DESC LIMIT 1`, addr).Scan(&raw)
	if err != nil {
		e.t.Fatalf("no mail to %s: %v", addr, err)
	}
	var m struct{ Subject, Text string }
	_ = json.Unmarshal(raw, &m)
	return m.Subject, m.Text
}

func tokenFromText(text, marker string) string {
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	rest := text[i+len(marker):]
	if j := strings.IndexAny(rest, " \n&"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
