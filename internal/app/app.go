// Package app wires dependencies and runs the api and worker roles.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/useopendesk/opendesk/internal/admin"
	"github.com/useopendesk/opendesk/internal/adminui"
	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/ai/assistant"
	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/ai/knowledge"
	"github.com/useopendesk/opendesk/internal/ai/mcp"
	"github.com/useopendesk/opendesk/internal/analytics"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/billing"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/collab"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/core/mcpserver"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/helpsite"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/idempotency"
	"github.com/useopendesk/opendesk/internal/integrations"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/mailer"
	"github.com/useopendesk/opendesk/internal/natsx"
	"github.com/useopendesk/opendesk/internal/plan"
	"github.com/useopendesk/opendesk/internal/push"
	"github.com/useopendesk/opendesk/internal/storage"
	"github.com/useopendesk/opendesk/internal/supportchannels"
	"github.com/useopendesk/opendesk/internal/version"
	"github.com/useopendesk/opendesk/internal/webui"
)

type area struct {
	register        func(*deps.Deps, huma.API, chi.Router)
	registerWorkers func(*deps.Deps, *river.Workers)
	periodic        func(*deps.Deps) []*river.PeriodicJob
	background      func(context.Context, *deps.Deps)
}

var areas = []area{
	{core.Register, core.RegisterWorkers, core.Periodic, core.Background},
	{email.Register, email.RegisterWorkers, email.Periodic, email.Background},
	{helpdesk.Register, helpdesk.RegisterWorkers, helpdesk.Periodic, helpdesk.Background},
	{billing.Register, billing.RegisterWorkers, billing.Periodic, billing.Background},
	{ai.Register, ai.RegisterWorkers, ai.Periodic, ai.Background},
	{integrations.Register, integrations.RegisterWorkers, integrations.Periodic, integrations.Background},
	{supportchannels.Register, supportchannels.RegisterWorkers, supportchannels.Periodic, supportchannels.Background},
	{assistant.Register, assistant.RegisterWorkers, assistant.Periodic, assistant.Background},
	{knowledge.Register, knowledge.RegisterWorkers, knowledge.Periodic, knowledge.Background},
	{bots.Register, bots.RegisterWorkers, bots.Periodic, bots.Background},
	{mcp.Register, mcp.RegisterWorkers, mcp.Periodic, mcp.Background},
	{mcpserver.Register, mcpserver.RegisterWorkers, mcpserver.Periodic, mcpserver.Background},
	{admin.Register, admin.RegisterWorkers, admin.Periodic, admin.Background},
	{collab.Register, collab.RegisterWorkers, collab.Periodic, collab.Background},
}

// turnstileHostnames are the hosts that render the Turnstile widget: the web app (login,
// registration, password reset; the desktop app loads it too) and the admin panel.
func turnstileHostnames(cfg *config.Config) []string {
	if captcha.IsTestSecret(cfg.TurnstileSecret) {
		return nil
	}
	var hosts []string
	for _, raw := range []string{cfg.AppURL, cfg.AdminURL} {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" && !slices.Contains(hosts, u.Hostname()) {
			hosts = append(hosts, u.Hostname())
		}
	}
	return hosts
}

// NewLogger returns a JSON logger in production and a text logger in development.
func NewLogger(cfg *config.Config) *slog.Logger {
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
}

// Build connects everything. The caller closes the returned cleanup.
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger) (*deps.Deps, func(), error) {
	database, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabaseURLDirect)
	if err != nil {
		return nil, nil, err
	}
	nc, err := natsx.Connect(natsx.Options{URL: cfg.NATSURL, Token: cfg.NATSToken, Listen: cfg.NATSListen,
		StoreDir: cfg.NATSStoreDir, Name: "opendesk-" + cfg.Role}, log)
	if err != nil {
		database.Close()
		return nil, nil, err
	}
	store, err := storage.New(ctx, storage.Options{Driver: cfg.StorageDriver, Dir: cfg.DataDir + "/files", Bucket: cfg.S3Bucket,
		Region: cfg.S3Region, Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, ForcePathStyle: cfg.S3ForcePathStyle})
	if err != nil {
		return nil, nil, err
	}
	m, err := mailer.New(ctx, mailer.Options{Driver: cfg.MailDriver, From: cfg.MailFrom, Host: cfg.SMTPHost, Port: cfg.SMTPPort,
		Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, TLS: cfg.SMTPTLS, Region: cfg.SESRegion, LogBodies: cfg.MailLogBodies}, log)
	if err != nil {
		return nil, nil, err
	}
	box, err := crypto.NewBox(cfg.CredentialsKey)
	if err != nil {
		return nil, nil, err
	}
	turnstile := captcha.NewTurnstileFromConfig(captcha.TurnstileConfig{Secret: cfg.TurnstileSecret, Disabled: cfg.TurnstileSecret == "",
		Hostnames: turnstileHostnames(cfg)})
	d := &deps.Deps{Cfg: cfg, Log: log, DB: database, NATS: nc, Storage: store, Mailer: m,
		Analytics: analytics.New(cfg.PosthogKey, cfg.PosthogHost, log), Box: box, Tokens: auth.NewTokens(cfg.AuthSecret),
		Captcha: turnstile, Limiter: httpx.NewLimiter(), Plan: plan.New(cfg.BillingEnabled(), cfg.AIUsageMarkup)}
	d.Plan.CollabOff = !cfg.Collab
	d.Limiter.UseDB(database.Pool, log)
	domain.Configure(cfg.APIURL, cfg.AuthSecret)
	domain.ConfigureSending(cfg.MailboxSystemSend)
	if cfg.APNsKey != "" {
		if d.APNs, err = push.NewAPNs(cfg.APNsKey, cfg.APNsKeyID, cfg.APNsTeamID, cfg.APNsTopic); err != nil {
			return nil, nil, err
		}
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &events.PruneWorker{Pool: database.Direct, Retention: cfg.EventRetention})
	periodic := []*river.PeriodicJob{events.PrunePeriodic()}
	for _, a := range areas {
		a.registerWorkers(d, workers)
		periodic = append(periodic, a.periodic(d)...)
	}
	rc := &river.Config{Workers: workers, Logger: slog.New(minLevel{log.Handler(), slog.LevelInfo}).With("component", "river"), JobTimeout: 5 * time.Minute}
	if cfg.Role != "api" {
		rc.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 20},
			jobs.QueueEmail:    {MaxWorkers: 20},
			jobs.QueueWebhooks: {MaxWorkers: 20},
		}
		rc.PeriodicJobs = periodic
	}
	d.Jobs, err = river.NewClient[pgx.Tx](riverpgxv5.New(database.Direct), rc)
	if err != nil {
		return nil, nil, fmt.Errorf("river: %w", err)
	}
	// Browsers can always be pushed (Web Push needs no outside service), so push jobs are
	// always enqueued for users with a phone or a subscribed browser.
	hd.ConfigurePush(d.Jobs)
	cleanup := func() {
		d.Analytics.Close()
		nc.Close()
		database.Close()
	}
	return d, cleanup, nil
}

// Router builds the HTTP handler: middleware, the huma API, areas and the web app.
func Router(d *deps.Deps) http.Handler {
	cfg := d.Cfg
	httpx.Install(d.Log)
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.Logger(d.Log), httpx.SecurityHeaders(strings.HasPrefix(cfg.APIURL, "https://")),
		httpx.CORS(cfg.CORSOrigins, cfg.AdminOrigin()))
	// Help centers answer on their own hosts and at /help/<slug>, before the API's
	// sessions are read (contracts/teams.md "Help center addresses").
	r.Use(helpsite.Middleware(d))
	resolver := &auth.Resolver{Pool: d.DB.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(cfg.TrustProxy, cfg.ProxyHops)}
	r.Use(resolver.Middleware, httpx.CredentialLimit(d.Limiter, httpx.ClientIP(cfg.TrustProxy, cfg.ProxyHops)), idempotency.New(d.DB.Pool, d.Box, cfg.AuthSecret, d.Log).Middleware)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 404, "title": "Not Found", "code": "not_found",
			"detail": "No route matches " + r.Method + " " + r.URL.Path})
	})
	mountHealth(r, d)
	mountSecurityTxt(r, cfg)

	// The API version, not the build: a public spec never says which release runs here.
	hc := huma.DefaultConfig("opendesk API", "1")
	hc.Info.Description = "The opendesk public API. The opendesk web app uses exactly this API."
	hc.OpenAPIPath = "/v1/openapi"
	hc.DocsPath = "/docs"
	hc.SchemasPath = "/v1/schemas"
	// No "$schema" member on responses: clients send objects back as PATCH bodies, and
	// bodies refuse unknown members. The schemas stay at /v1/schemas and in the spec.
	hc.CreateHooks = nil
	hc.Servers = []*huma.Server{{URL: cfg.APIURL}}
	hc.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {Type: "http", Scheme: "bearer", Description: "Access token (JWT) or API key (od_live_...)"},
	}
	hc.Security = []map[string][]string{{"bearer": {}}}
	keepUploadsInMemory()
	api := humachi.New(r, hc)

	for _, a := range areas {
		a.register(d, api, r)
	}

	if cfg.ServeWeb {
		// The instance admin panel (contracts/admin.md), before the web app's catch-all.
		panel := adminui.Handler(adminui.RuntimeConfig{APIURL: cfg.APIURL, AppURL: cfg.AppURL, TurnstileSiteKey: cfg.TurnstileSiteKey,
			EnvLabel: os.Getenv("ADMIN_ENV_LABEL")}, adminCSP(cfg))
		r.Handle("/admin", panel)
		r.Handle("/admin/*", panel)
		csp := webCSP(cfg)
		r.Handle("/*", webui.Handler(webui.RuntimeConfig{APIURL: cfg.APIURL, GatewayURL: cfg.GatewayURL,
			PosthogKey: os.Getenv("WEB_POSTHOG_KEY"), PosthogHost: os.Getenv("WEB_POSTHOG_HOST"),
			TurnstileSiteKey: cfg.TurnstileSiteKey, SelfHosted: true, MailboxOAuth: cfg.MailboxOAuthProviders(),
			MailboxIMAP: cfg.MailboxIMAP, HelpDomain: cfg.HelpDomain}, csp))
	}
	return r
}

// uploadMemory covers the largest upload body (an import CSV: 50 MB and its
// multipart envelope). Every route caps its body below this.
const uploadMemory = 64 << 20

// keepUploadsInMemory has the multipart parser keep uploaded files in memory
// instead of temp files: every upload handler reads the whole file anyway, and
// the API runs on a read-only root filesystem.
func keepUploadsInMemory() { humachi.MultipartMaxMemory = uploadMemory }

func webCSP(cfg *config.Config) string {
	connect := []string{"'self'"}
	for _, u := range []string{cfg.APIURL, cfg.GatewayURL} {
		if o := cspOrigin(u); o != "" {
			connect = append(connect, o)
		}
	}
	connect = append(connect, "https://*.posthog.com", "https://challenges.cloudflare.com")
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' https://challenges.cloudflare.com",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connect, " "),
		"frame-src https://challenges.cloudflare.com",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
}

// adminCSP is the admin panel's policy: the API and Turnstile, nothing else.
func adminCSP(cfg *config.Config) string {
	connect := []string{"'self'"}
	if o := cspOrigin(cfg.APIURL); o != "" {
		connect = append(connect, o)
	}
	connect = append(connect, "https://challenges.cloudflare.com")
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' https://challenges.cloudflare.com",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connect, " "),
		"frame-src https://challenges.cloudflare.com",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"object-src 'none'",
	}, "; ")
}

// cspOrigin is a URL's scheme and host. A CSP source with a path matches only that
// exact path, which would block the socket at <gateway>/socket/websocket.
func cspOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Serve runs the configured role until ctx is canceled.
func Serve(ctx context.Context, d *deps.Deps) error {
	cfg := d.Cfg
	errc := make(chan error, 2)

	// Build the router before starting workers, so a registration failure exits cleanly.
	var handler http.Handler
	if cfg.Role != "worker" {
		handler = Router(d)
	}

	if cfg.Role != "api" {
		if err := d.Jobs.Start(ctx); err != nil {
			return fmt.Errorf("start river: %w", err)
		}
		relay := &events.Relay{Pool: d.DB.Direct, NATS: d.NATS.Conn, Jobs: d.Jobs, Log: d.Log.With("component", "relay")}
		go relay.Run(ctx)
		for _, a := range areas {
			go a.background(ctx, d)
		}
		d.Log.Info("worker started")
	}

	if cfg.Role == "worker" {
		// The worker serves only health checks, so orchestrators can probe it.
		handler = healthRouter(d)
	}
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		d.Log.Info("http listening", "addr", cfg.HTTPAddr, "version", version.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if cfg.Role != "api" {
		_ = d.Jobs.Stop(shutdown)
	}
	return nil
}

// minLevel drops records below a level (River's debug output is very chatty).
type minLevel struct {
	slog.Handler
	min slog.Level
}

func (h minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h minLevel) WithAttrs(a []slog.Attr) slog.Handler {
	return minLevel{h.Handler.WithAttrs(a), h.min}
}
func (h minLevel) WithGroup(n string) slog.Handler { return minLevel{h.Handler.WithGroup(n), h.min} }

func mountHealth(r chi.Router, d *deps.Deps) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Pool.Ping(ctx); err != nil || !d.NATS.IsConnected() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func healthRouter(d *deps.Deps) http.Handler {
	r := chi.NewRouter()
	mountHealth(r, d)
	return r
}
