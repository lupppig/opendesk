package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
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
	"github.com/pquerna/otp/totp"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/useopendesk/opendesk/internal/analytics"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/natsx"
	"github.com/useopendesk/opendesk/internal/plan"
	"github.com/useopendesk/opendesk/internal/storage"
)

const (
	panelOrigin  = "http://admin.test"
	goodPassword = "correct horse battery staple 42"
)

var migrateOnce sync.Once

type env struct {
	t   *testing.T
	d   *deps.Deps
	srv *httptest.Server
}

// newEnv is the core and admin areas on a real Postgres (OPENDESK_TEST_DATABASE_URL), with
// billing on so limit overrides apply.
func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("OPENDESK_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set OPENDESK_TEST_DATABASE_URL to run database tests")
	}
	ctx := context.Background()
	var merr error
	migrateOnce.Do(func() { merr = db.Migrate(ctx, dsn) })
	if merr != nil {
		t.Fatalf("migrate: %v", merr)
	}
	database, err := db.Open(ctx, dsn, dsn)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	nc, err := natsx.Connect(natsx.Options{StoreDir: t.TempDir(), Name: "admin-test"}, log)
	if err != nil {
		t.Fatal(err)
	}
	box, _ := crypto.NewBox([]byte("development-only-credentials-key"))
	cfg := &config.Config{Env: "development", APIURL: "http://api.test", AppURL: "http://app.test", AdminURL: panelOrigin,
		GatewayURL: "ws://gw.test/socket", AuthSecret: []byte("test-auth-secret-test-auth-secret-0000"), SignupEnabled: true,
		StripeSecretKey: "sk_test_admin", CORSOrigins: []string{"http://app.test"}}
	d := &deps.Deps{Cfg: cfg, Log: log, DB: database, NATS: nc, Analytics: analytics.New("", "", log), Box: box,
		Tokens: auth.NewTokens(cfg.AuthSecret), Captcha: captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Disabled: true}),
		Limiter: httpx.NewLimiter(), Plan: plan.New(true, 1.5)}
	if d.Storage, err = storage.New(ctx, storage.Options{Driver: "local", Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	domain.Configure(cfg.APIURL, cfg.AuthSecret)
	if d.Jobs, err = river.NewClient[pgx.Tx](riverpgxv5.New(database.Pool), &river.Config{}); err != nil {
		t.Fatal(err)
	}
	httpx.Install(log)
	r := chi.NewRouter()
	r.Use(httpx.CORS(cfg.CORSOrigins, cfg.AdminOrigin()))
	r.Use((&auth.Resolver{Pool: database.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}).Middleware)
	api := humachi.New(r, huma.DefaultConfig("test", "0"))
	core.Register(d, api, r)
	Register(d, api, r)
	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		srv.Close()
		nc.Close()
		database.Close()
	})
	return &env{t: t, d: d, srv: srv}
}

type client struct {
	e         *env
	jar       http.CookieJar
	token     string
	workspace string
	origin    string
}

func (e *env) client(origin string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, jar: jar, origin: origin}
}

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
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

func (c *client) do(method, path string, body any) resp {
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
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.workspace != "" {
		req.Header.Set(auth.WorkspaceHeader, c.workspace)
	}
	for _, ck := range c.jar.Cookies(req.URL) {
		req.AddCookie(ck)
	}
	res, err := http.DefaultClient.Do(req)
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

func (c *client) cookie(name, path string) string {
	req, _ := http.NewRequest(http.MethodGet, c.e.srv.URL+path, nil)
	for _, ck := range c.jar.Cookies(req.URL) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

func want(t *testing.T, what string, r resp, status int, code ...string) {
	t.Helper()
	if r.Status != status || (len(code) > 0 && r.str("code") != code[0]) {
		t.Fatalf("%s: %d %s, want %d %v", what, r.Status, r.Raw, status, code)
	}
}

// person registers an account through the app and returns its signed-in app client.
type person struct {
	id     uuid.UUID
	email  string
	app    *client
	secret string
}

func (e *env) person(tag string) *person {
	e.t.Helper()
	c := e.client("http://app.test")
	email := tag + "-" + crypto.RandomString(8) + "@example.com"
	r := c.do("POST", "/v1/auth/register", map[string]any{"email": email, "password": goodPassword, "name": "Test " + tag})
	want(e.t, "register", r, 200)
	c.token = r.str("access_token")
	return &person{id: uuid.MustParse(r.str("user", "id")), email: email, app: c}
}

// twoFactor turns on TOTP for p directly.
func (e *env) twoFactor(p *person) {
	e.t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "opendesk", AccountName: p.email})
	if err != nil {
		e.t.Fatal(err)
	}
	sealed, _ := e.d.Box.Seal([]byte(key.Secret()))
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE users SET totp_secret_enc = $2, totp_enabled = true WHERE id = $1`, p.id, sealed); err != nil {
		e.t.Fatal(err)
	}
	p.secret = key.Secret()
}

// code is a current authenticator code of p. Each step is accepted once, so it forgets
// the last consumed step first.
func (e *env) code(p *person) string {
	e.t.Helper()
	if _, err := e.d.DB.Pool.Exec(context.Background(), `UPDATE users SET totp_last_step = 0 WHERE id = $1`, p.id); err != nil {
		e.t.Fatal(err)
	}
	c, _ := totp.GenerateCode(p.secret, time.Now())
	return c
}

func (e *env) grant(p *person, role Role) {
	e.t.Helper()
	err := e.d.DB.Tx(context.Background(), func(tx pgx.Tx) error {
		_, _, err := Grant(context.Background(), tx, p.email, role, CLIActor)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// signIn signs p in to the panel and returns the panel client.
func (e *env) signIn(p *person) *client {
	e.t.Helper()
	c := e.client(panelOrigin)
	lr := c.do("POST", "/v1/admin/auth/login", map[string]any{"email": p.email, "password": goodPassword})
	want(e.t, "admin login", lr, 200)
	mr := c.do("POST", "/v1/admin/auth/mfa", map[string]any{"mfa_token": lr.str("mfa_token"), "code": e.code(p)})
	want(e.t, "admin mfa", mr, 200)
	c.token = mr.str("access_token")
	return c
}

func TestAdminSignIn(t *testing.T) {
	e := newEnv(t)
	owner := e.person("owner")

	// Only the panel's origin may sign in, and only an admin.
	app := e.client("http://app.test")
	want(t, "app origin", app.do("POST", "/v1/admin/auth/login", map[string]any{"email": owner.email, "password": goodPassword}), 403, "origin_refused")
	panel := e.client(panelOrigin)
	want(t, "wrong password", panel.do("POST", "/v1/admin/auth/login", map[string]any{"email": owner.email, "password": "nope nope nope"}), 401, "invalid_credentials")
	want(t, "unknown email", panel.do("POST", "/v1/admin/auth/login", map[string]any{"email": "nobody-" + crypto.RandomString(6) + "@example.com", "password": goodPassword}), 401, "invalid_credentials")
	want(t, "not an admin", panel.do("POST", "/v1/admin/auth/login", map[string]any{"email": owner.email, "password": goodPassword}), 403, "not_admin")

	// An admin without two-factor authentication sets it up first.
	e.grant(owner, RoleSuper)
	want(t, "no 2fa", panel.do("POST", "/v1/admin/auth/login", map[string]any{"email": owner.email, "password": goodPassword}), 403, "admin_mfa_setup_required")

	e.twoFactor(owner)
	lr := panel.do("POST", "/v1/admin/auth/login", map[string]any{"email": owner.email, "password": goodPassword})
	want(t, "login", lr, 200)
	want(t, "wrong code", panel.do("POST", "/v1/admin/auth/mfa", map[string]any{"mfa_token": lr.str("mfa_token"), "code": "000000"}), 401, "invalid_code")
	mr := panel.do("POST", "/v1/admin/auth/mfa", map[string]any{"mfa_token": lr.str("mfa_token"), "code": e.code(owner)})
	want(t, "mfa", mr, 200)
	if mr.Body["refresh_token"] != nil || panel.cookie(RefreshCookie, "/v1/admin/auth/refresh") == "" {
		t.Fatalf("the refresh token is a cookie only: %s", mr.Raw)
	}
	if strings.Contains(mr.Header.Get("Set-Cookie"), "Domain=") || !strings.Contains(mr.Header.Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("cookie attributes: %s", mr.Header.Get("Set-Cookie"))
	}
	panel.token = mr.str("access_token")
	me := panel.do("GET", "/v1/admin/me", nil)
	want(t, "me", me, 200)
	if me.str("role") != "super" || me.str("email") != owner.email {
		t.Fatalf("me: %s", me.Raw)
	}

	// The two kinds of token never cross.
	want(t, "app token on admin route", owner.app.do("GET", "/v1/admin/me", nil), 401)
	cross := e.client("http://app.test")
	cross.token = panel.token
	want(t, "admin token on app route", cross.do("GET", "/v1/me", nil), 401)

	// CORS on /v1/admin/* names only the panel.
	if got := app.do("GET", "/v1/admin/me", nil).Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("app origin allowed on admin routes: %q", got)
	}
	if got := panel.do("GET", "/v1/admin/me", nil).Header.Get("Access-Control-Allow-Origin"); got != panelOrigin {
		t.Fatalf("panel origin not allowed: %q", got)
	}

	// Refresh rotates; the old cookie inside the grace window does not end the session.
	old := panel.cookie(RefreshCookie, "/v1/admin/auth/refresh")
	rr := panel.do("POST", "/v1/admin/auth/refresh", nil)
	want(t, "refresh", rr, 200)
	if panel.cookie(RefreshCookie, "/v1/admin/auth/refresh") == old {
		t.Fatal("refresh did not rotate")
	}
	replay := e.client(panelOrigin)
	replay.jar.SetCookies(mustURL(e.srv.URL+"/v1/admin/auth/refresh"), []*http.Cookie{{Name: RefreshCookie, Value: old, Path: "/v1/admin/auth"}})
	want(t, "replay in grace", replay.do("POST", "/v1/admin/auth/refresh", nil), 409, "refresh_in_progress")
	panel.token = rr.str("access_token")

	// Losing the admin role ends access at once.
	err := e.d.DB.Tx(context.Background(), func(tx pgx.Tx) error { return Revoke(context.Background(), tx, owner.id, CLIActor) })
	if err != nil {
		t.Fatal(err)
	}
	want(t, "revoked admin", panel.do("GET", "/v1/admin/me", nil), 401)
	want(t, "revoked refresh", panel.do("POST", "/v1/admin/auth/refresh", nil), 401)

	audit := e.signInAs(t, RoleViewer).do("GET", "/v1/admin/audit?limit=100", nil)
	for _, a := range []string{`"login"`, `"login.failed"`, `"admin.granted"`, `"admin.revoked"`} {
		if !strings.Contains(string(audit.Raw), a) {
			t.Errorf("admin audit lacks %s", a)
		}
	}
}

// signInAs makes a new admin with role and signs them in.
func (e *env) signInAs(t *testing.T, role Role) *client {
	t.Helper()
	p := e.person(string(role))
	e.twoFactor(p)
	e.grant(p, role)
	return e.signIn(p)
}

func TestAdminWorkspaceActions(t *testing.T) {
	e := newEnv(t)
	owner := e.person("owner")
	r := owner.app.do("POST", "/v1/workspaces", map[string]any{"name": "Acme " + crypto.RandomString(4)})
	want(t, "create workspace", r, 201)
	wid, name := r.str("id"), r.str("name")
	owner.app.workspace = wid

	staff := e.person("staff")
	e.twoFactor(staff)
	e.grant(staff, RoleSuper)
	panel := e.signIn(staff)
	viewer := e.signInAs(t, RoleViewer)

	list := panel.do("GET", "/v1/admin/workspaces?q="+owner.email, nil)
	want(t, "search by member email", list, 200)
	if !strings.Contains(string(list.Raw), wid) {
		t.Fatalf("search: %s", list.Raw)
	}
	w := panel.do("GET", "/v1/admin/workspaces/"+wid, nil)
	want(t, "get workspace", w, 200)
	if w.str("name") != name || w.str("plan") != "free" {
		t.Fatalf("workspace: %s", w.Raw)
	}

	// Limits replace the plan's.
	lim := panel.do("PUT", "/v1/admin/workspaces/"+wid+"/limits", map[string]any{"max_mailboxes": 5, "max_members": 0, "monthly_ai_usd": 25})
	want(t, "limits", lim, 200)
	ent := lim.Body["entitlements"].(map[string]any)
	if ent["max_mailboxes"] != 5.0 || ent["max_members"] != 0.0 || ent["monthly_ai_usd"] != 25.0 {
		t.Fatalf("entitlements: %v", ent)
	}
	g := panel.do("POST", "/v1/admin/workspaces/"+wid+"/ai-grants", map[string]any{"amount_usd": 10, "note": "Launch week credit"})
	want(t, "ai grant", g, 200)
	if g.Body["ai"].(map[string]any)["purchased_usd"] != 10.0 {
		t.Fatalf("ai: %v", g.Body["ai"])
	}
	want(t, "viewer cannot suspend", viewer.do("POST", "/v1/admin/workspaces/"+wid+"/suspend", map[string]any{"reason": "x"}), 403)

	// Suspended: members get workspace_suspended and see why in their list.
	want(t, "suspend", panel.do("POST", "/v1/admin/workspaces/"+wid+"/suspend", map[string]any{"reason": "Phishing replies"}), 200)
	want(t, "member route", owner.app.do("GET", "/v1/workspace", nil), 403, "workspace_suspended")
	me := owner.app.do("GET", "/v1/me", nil)
	want(t, "me", me, 200)
	if !strings.Contains(string(me.Raw), `"suspended":true`) {
		t.Fatalf("membership not marked: %s", me.Raw)
	}
	if ok, _ := domain.WorkspaceSuspended(context.Background(), e.d.DB.Pool, uuid.MustParse(wid)); !ok {
		t.Fatal("WorkspaceSuspended is false")
	}
	want(t, "unsuspend", panel.do("POST", "/v1/admin/workspaces/"+wid+"/unsuspend", nil), 200)
	want(t, "member route again", owner.app.do("GET", "/v1/workspace", nil), 200)

	// The workspace's own audit log shows the operator acted.
	audit := owner.app.do("GET", "/v1/audit-logs?limit=100", nil)
	if !strings.Contains(string(audit.Raw), `"workspace.suspended"`) || !strings.Contains(string(audit.Raw), `"instance_admin"`) {
		t.Fatalf("workspace audit: %s", audit.Raw)
	}

	// Deleting needs the code and the exact name.
	want(t, "delete without code", panel.do("DELETE", "/v1/admin/workspaces/"+wid, map[string]any{"confirm_name": name}), 403, "mfa_required")
	want(t, "delete wrong name", panel.do("DELETE", "/v1/admin/workspaces/"+wid, map[string]any{"confirm_name": "nope", "code": e.code(staff)}), 422, "confirm_name_mismatch")
	want(t, "delete", panel.do("DELETE", "/v1/admin/workspaces/"+wid, map[string]any{"confirm_name": name, "code": e.code(staff)}), 204)
	want(t, "gone", panel.do("GET", "/v1/admin/workspaces/"+wid, nil), 404)

	for _, p := range []string{"/v1/admin/summary", "/v1/admin/health", "/v1/admin/jobs/stats", "/v1/admin/jobs?state=available", "/v1/admin/admins"} {
		want(t, p, viewer.do("GET", p, nil), 200)
	}
}

func TestAdminUserActions(t *testing.T) {
	e := newEnv(t)
	user := e.person("user")
	e.twoFactor(user)
	staff := e.person("staff")
	e.twoFactor(staff)
	e.grant(staff, RoleSuper)
	panel := e.signIn(staff)
	support := e.signInAs(t, RoleSupport)

	u := panel.do("GET", "/v1/admin/users/"+user.id.String(), nil)
	want(t, "get user", u, 200)
	if u.str("email") != user.email || len(u.Body["sessions"].([]any)) != 1 {
		t.Fatalf("user: %s", u.Raw)
	}

	// Suspended: no new session, and the app's refresh is refused.
	want(t, "suspend self", panel.do("POST", "/v1/admin/users/"+staff.id.String()+"/suspend", map[string]any{"reason": "x"}), 409)
	want(t, "suspend", support.do("POST", "/v1/admin/users/"+user.id.String()+"/suspend", map[string]any{"reason": "Spam"}), 200)
	login := e.client("http://app.test").do("POST", "/v1/auth/login", map[string]any{"email": user.email, "password": goodPassword})
	want(t, "login with mfa token", login, 200)
	mfa := e.client("http://app.test").do("POST", "/v1/auth/mfa", map[string]any{"mfa_token": login.str("mfa_token"), "code": e.code(user)})
	want(t, "suspended sign-in", mfa, 403, "account_suspended")
	want(t, "suspended refresh", user.app.do("POST", "/v1/auth/refresh", map[string]any{}), 401)
	want(t, "unsuspend", support.do("POST", "/v1/admin/users/"+user.id.String()+"/unsuspend", nil), 200)

	// Turning off 2FA is super's, with a step-up code, and the user is told.
	want(t, "support cannot", support.do("POST", "/v1/admin/users/"+user.id.String()+"/mfa/disable", map[string]any{"code": "123456"}), 403, "forbidden")
	want(t, "no code", panel.do("POST", "/v1/admin/users/"+user.id.String()+"/mfa/disable", map[string]any{}), 403, "mfa_required")
	off := panel.do("POST", "/v1/admin/users/"+user.id.String()+"/mfa/disable", map[string]any{"code": e.code(staff)})
	want(t, "mfa disable", off, 200)
	if off.Body["totp_enabled"] != false {
		t.Fatalf("still on: %s", off.Raw)
	}
	var subject string
	if err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT args->>'subject' FROM river_job WHERE kind = 'mail.system' AND args->>'to' = $1
		ORDER BY id DESC LIMIT 1`, user.email).Scan(&subject); err != nil || !strings.Contains(subject, "Two-factor") {
		t.Fatalf("no notice email: %q %v", subject, err)
	}

	jobs := panel.do("GET", "/v1/admin/jobs?state=available&kind=mail.system", nil)
	want(t, "jobs", jobs, 200)
	if strings.Contains(string(jobs.Raw), "settings/security") {
		t.Fatalf("job args leak the email: %s", jobs.Raw)
	}

	// Deleting needs the code and the exact email; admins are revoked first.
	want(t, "delete wrong email", panel.do("DELETE", "/v1/admin/users/"+user.id.String(), map[string]any{"code": e.code(staff), "confirm_email": "x@example.com"}), 422)
	want(t, "delete", panel.do("DELETE", "/v1/admin/users/"+user.id.String(), map[string]any{"code": e.code(staff), "confirm_email": user.email}), 204)
	want(t, "gone", panel.do("GET", "/v1/admin/users/"+user.id.String(), nil), 404)

	// The last super admin stays.
	adm := panel.do("GET", "/v1/admin/admins", nil)
	want(t, "admins", adm, 200)
	want(t, "revoke self", panel.do("DELETE", "/v1/admin/admins/"+staff.id.String(), map[string]any{"code": e.code(staff)}), 409)
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
