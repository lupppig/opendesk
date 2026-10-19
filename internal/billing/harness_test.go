package billing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
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
	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/useopendesk/opendesk/internal/analytics"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/billing"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/natsx"
	"github.com/useopendesk/opendesk/internal/plan"
)

const whSecret = "whsec_test_secret"

var migrateOnce sync.Once

type env struct {
	run  string // suffix that keeps event ids unique across runs on one database
	t    *testing.T
	d    *deps.Deps
	srv  *httptest.Server
	fake *fakeStripe
}

func dsn(t *testing.T) string {
	for _, k := range []string{"BILLING_TEST_DATABASE_URL", "OPENDESK_TEST_DATABASE_URL"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	t.Skip("set BILLING_TEST_DATABASE_URL to run billing tests")
	return ""
}

// newEnv serves core, email and billing against Postgres. With stripe set, billing is on
// and talks to a local fake Stripe.
func newEnv(t *testing.T, stripeOn bool) *env {
	t.Helper()
	url := dsn(t)
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
	if os.Getenv("BILLINGTEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	nc, err := natsx.Connect(natsx.Options{StoreDir: t.TempDir(), Name: "billing-test"}, log)
	if err != nil {
		t.Fatal(err)
	}
	box, _ := crypto.NewBox([]byte("development-only-credentials-key"))
	cfg := &config.Config{Env: "development", Role: "all", APIURL: "http://api.test", AppURL: "http://app.test", ServeWeb: true,
		GatewayURL: "ws://gw.test/socket", AuthSecret: []byte("test-auth-secret-test-auth-secret-0000"), SignupEnabled: true,
		InboundDomain: "in.test", MailFrom: "opendesk <notifications@opendesk.test>", MailboxSystemSend: true}
	e := &env{t: t, run: crypto.RandomString(8)}
	if stripeOn {
		e.fake = newFakeStripe()
		t.Cleanup(e.fake.srv.Close)
		billing.SetStripeBackend(e.fake.srv.URL)
		cfg.StripeSecretKey, cfg.StripeWebhookSecret = "sk_test_123", whSecret
		cfg.StripePricePro, cfg.StripePriceProYear, cfg.StripePriceTopUp = "price_pro", "price_pro_y", "price_ai_topup"
		cfg.StripePriceCollab, cfg.StripePriceCollabYear = "price_collab", "price_collab_y"
	}
	d := &deps.Deps{Cfg: cfg, Log: log, DB: database, NATS: nc, Analytics: analytics.New("", "", log), Box: box,
		Tokens: auth.NewTokens(cfg.AuthSecret), Captcha: captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Disabled: true}),
		Limiter: httpx.NewLimiter(), Plan: plan.New(cfg.BillingEnabled(), 1.5)}
	d.Jobs, err = river.NewClient[pgx.Tx](riverpgxv5.New(database.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	domain.Configure(cfg.APIURL, cfg.AuthSecret)
	httpx.Install(log)
	r := chi.NewRouter()
	r.Use(httpx.CORS([]string{cfg.AppURL}))
	r.Use((&auth.Resolver{Pool: database.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}).Middleware)
	api := humachi.New(r, huma.DefaultConfig("test", "0"))
	core.Register(d, api, r)
	email.Register(d, api, r)
	billing.Register(d, api, r)
	e.srv = httptest.NewServer(r)
	e.d = d
	t.Cleanup(func() {
		e.srv.Close()
		nc.Close()
		database.Close()
	})
	return e
}

type client struct {
	e         *env
	token     string
	workspace string
	email     string
}

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
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
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.workspace != "" {
		req.Header.Set(auth.WorkspaceHeader, c.workspace)
	}
	return c.e.send(req)
}

func (e *env) send(req *http.Request) resp {
	e.t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

func (r resp) str(path ...string) string {
	var v any = r.Body
	for _, p := range path {
		m, _ := v.(map[string]any)
		v = m[p]
	}
	s, _ := v.(string)
	return s
}

func (r resp) num(path ...string) float64 {
	var v any = r.Body
	for _, p := range path {
		m, _ := v.(map[string]any)
		v = m[p]
	}
	f, _ := v.(float64)
	return f
}

func (e *env) signup(tag string) *client {
	e.t.Helper()
	c := &client{e: e, email: tag + "-" + strings.ToLower(crypto.RandomString(8)) + "@example.com"}
	r := c.do("POST", "/v1/auth/register", map[string]any{"email": c.email, "password": "correct horse battery staple 42", "name": "Test " + tag})
	if r.Status != 200 {
		e.t.Fatalf("register: %d %s", r.Status, r.Raw)
	}
	c.token = r.str("access_token")
	return c
}

func (c *client) newWorkspace(name string) uuid.UUID {
	c.e.t.Helper()
	r := c.do("POST", "/v1/workspaces", map[string]any{"name": name})
	if r.Status != 201 {
		c.e.t.Fatalf("create workspace: %d %s", r.Status, r.Raw)
	}
	c.workspace = r.str("id")
	return uuid.MustParse(c.workspace)
}

func (c *client) addMailbox(addr string) resp {
	return c.do("POST", "/v1/mailboxes", map[string]any{"name": "Support", "email": addr, "display_name": "Support",
		"inbound_kind": "forwarding", "outbound_kind": "system"})
}

func (c *client) invite(addr, role string) resp {
	return c.do("POST", "/v1/invitations", map[string]any{"email": addr, "role": role})
}

// inviteToken reads the invitation link from the queued system email.
func (e *env) inviteToken(addr string) string {
	e.t.Helper()
	var raw []byte
	if err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT args FROM river_job WHERE kind = 'mail.system' AND args->>'to' = $1
		ORDER BY id DESC LIMIT 1`, addr).Scan(&raw); err != nil {
		e.t.Fatalf("no mail to %s: %v", addr, err)
	}
	var m struct{ Text string }
	_ = json.Unmarshal(raw, &m)
	i := strings.Index(m.Text, "/invite?token=")
	rest := m.Text[i+len("/invite?token="):]
	if j := strings.IndexAny(rest, " \n&"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// event signs and posts a Stripe event to the webhook.
func (e *env) event(id, typ string, object map[string]any) resp {
	e.t.Helper()
	return e.postSigned(eventPayload(id+"_"+e.run, typ, object), whSecret, time.Now())
}

func eventPayload(id, typ string, object map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"id": id, "object": "event", "type": typ, "api_version": stripe.APIVersion,
		"created": time.Now().Unix(), "data": map[string]any{"object": object}})
	return b
}

func (e *env) postSigned(payload []byte, secret string, at time.Time) resp {
	e.t.Helper()
	sp := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: at})
	req, _ := http.NewRequest("POST", e.srv.URL+billing.WebhookPath, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", sp.Header)
	return e.send(req)
}

func (e *env) scalar(sql string, args ...any) any {
	e.t.Helper()
	var v any
	if err := e.d.DB.Pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		e.t.Fatalf("query %q: %v", sql, err)
	}
	return v
}

func (e *env) count(sql string, args ...any) int64 {
	e.t.Helper()
	v := e.scalar(sql, args...)
	n, _ := v.(int64)
	return n
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.Status != status || (code != "" && r.str("code") != code) {
		t.Fatalf("want %d %s, got %d %s", status, code, r.Status, r.Raw)
	}
}

func mustReq(t *testing.T, url string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func jsonInto(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
