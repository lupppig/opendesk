package core

import (
	"context"
	"github.com/useopendesk/opendesk/internal/notifyprefs"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// svc carries the dependencies every core handler and worker uses.
type svc struct {
	d      *deps.Deps
	oauth  *oauthProviders
	hooks  *http.Client // SSRF-guarded client for webhook deliveries
	devURL bool         // development instance: webhooks may target http://localhost
	// webauthn is the passkey relying party derived from APP_URL; nil turns passkeys off.
	webauthn *webauthn.WebAuthn
	// push is the relay client (push = relay) and, on a relay, the instance key cache.
	push *pushSender
}

func newSvc(d *deps.Deps) *svc {
	dev := !d.Cfg.IsProduction()
	s := &svc{d: d, oauth: newOAuthProviders(d.Cfg), hooks: webhookClient(dev), devURL: dev, webauthn: newWebAuthn(d.Cfg)}
	s.push = s.newPushSender()
	notifyprefs.Configure(d.Cfg.NotifyEmail)
	return s
}

// reqInfo is what anonymous handlers need from the raw request.
type reqInfo struct {
	IP        string
	UserAgent string
	// Client is the app the request comes from: web, desktop or ios (sessions.client).
	Client string
	// Native marks a native request (contracts/native.md): OD-Client ios/<version> and
	// no Origin header. Only a native request receives a refresh token in a body.
	Native bool
	// Cookies are the refresh cookies the browser sent, one per signed-in account (and
	// the legacy single-account cookie). Native requests never read them.
	Cookies []presentedCookie
}

// ClientHeader names the calling app: ios/<version> or desktop/<version>.
const ClientHeader = "OD-Client"

// desktopUA is the user agent suffix of the desktop shell.
const desktopUA = "opendesk-desktop/"

type reqInfoKey struct{}

func (s *svc) withReqInfo(ctx huma.Context, next func(huma.Context)) {
	ri := reqInfo{}
	if r, _ := humachi.Unwrap(ctx); r != nil {
		ri = s.readReqInfo(r)
	}
	next(huma.WithValue(ctx, reqInfoKey{}, ri))
}

// readReqInfo reads the caller's address, user agent and client. Browsers always send
// Origin on POST, so a request with Origin is never native, whatever OD-Client says.
func (s *svc) readReqInfo(r *http.Request) reqInfo {
	ri := reqInfo{IP: httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(r), UserAgent: truncate(r.UserAgent(), 400), Client: "web"}
	od := r.Header.Get(ClientHeader)
	switch {
	case strings.HasPrefix(od, "ios/") && r.Header.Get("Origin") == "":
		ri.Client, ri.Native = "ios", true
	case strings.HasPrefix(od, "desktop/") || strings.Contains(r.UserAgent(), desktopUA):
		ri.Client = "desktop"
	}
	if !ri.Native {
		ri.Cookies = readRefreshCookies(r)
	}
	return ri
}

func requestInfo(ctx context.Context) reqInfo {
	ri, _ := ctx.Value(reqInfoKey{}).(reqInfo)
	return ri
}

// register adds an operation with the request-info middleware attached.
func register[I, O any](s *svc, api huma.API, op huma.Operation, h func(context.Context, *I) (*O, error)) {
	op.Middlewares = append(op.Middlewares, s.withReqInfo)
	huma.Register(api, op, h)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// normEmail validates and normalizes an address. It returns "" when invalid.
func normEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 {
		return ""
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || !strings.Contains(s, "@") {
		return ""
	}
	local, domain, _ := strings.Cut(s, "@")
	if local == "" || !strings.Contains(domain, ".") {
		return ""
	}
	return strings.ToLower(s)
}

// tx runs fn in a transaction on the pooled connection.
func (s *svc) tx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return s.d.DB.Tx(ctx, fn)
}

// requireRecentLogin demands that the caller's session started within RecentLoginWindow.
// Owner-level operations pair it with auth.RequireMFA.
func requireRecentLogin(ctx context.Context, q db.Querier, p *auth.Principal) error {
	if p.Kind != auth.KindUser {
		return auth.ErrForbidden
	}
	var created time.Time
	err := q.QueryRow(ctx, `SELECT created_at FROM sessions WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		p.SessionID, p.UserID).Scan(&created)
	if err != nil {
		if db.IsNotFound(err) {
			return httpx.E(401, "unauthenticated", auth.ErrUnauthenticated.Error())
		}
		return err
	}
	if time.Since(created) > RecentLoginWindow {
		return httpx.E(403, "reauth_required", "Sign in again to continue.")
	}
	return nil
}

// RecentLoginWindow bounds how old a session may be for owner-level operations.
const RecentLoginWindow = auth.RecentLoginWindow

// requireUserWorkspace demands a signed-in member with at least role (API keys refused).
func requireUserWorkspace(ctx context.Context, role auth.Role) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, role, "")
	if err != nil {
		return nil, err
	}
	if p.Kind != auth.KindUser {
		return nil, auth.ErrForbidden
	}
	return p, nil
}

// parseID parses a path id, answering 404 for malformed ids.
func parseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, httpx.NotFound(what)
	}
	return id, nil
}

// limit applies a rate limit and answers 429 when exceeded.
func (s *svc) limit(key string, n int, per time.Duration) error {
	if !s.d.Limiter.Allow(key, n, per) {
		return httpx.RateLimited()
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
