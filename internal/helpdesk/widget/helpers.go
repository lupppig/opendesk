package widget

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Rate limits for visitor traffic.
const (
	sessionLimit  = 20 // per IP per minute
	visitorWrites = 30 // per session per minute
	uploadLimit   = 10 // per session per minute
)

type ipKey struct{}

// withClientIP is an operation middleware that puts the caller address in the context
// for routes that run before any credential exists (session creation).
func (h *handler) withClientIP(ctx huma.Context, next func(huma.Context)) {
	r, _ := humachi.Unwrap(ctx)
	next(huma.WithValue(ctx, ipKey{}, httpx.ClientIP(h.d.Cfg.TrustProxy, h.d.Cfg.ProxyHops)(r)))
}

func clientIP(ctx context.Context) string {
	if p := auth.FromContext(ctx); p != nil && p.IP != "" {
		return p.IP
	}
	s, _ := ctx.Value(ipKey{}).(string)
	return s
}

// checkOrigin enforces the widget's allowed_origins: when the list is set, the browser
// Origin must be one of them.
func checkOrigin(w hd.WidgetSettings, origin string) error {
	if len(w.AllowedOrigins) == 0 {
		return nil
	}
	origin = strings.ToLower(strings.TrimRight(strings.TrimSpace(origin), "/"))
	if origin != "" && slices.Contains(w.AllowedOrigins, origin) {
		return nil
	}
	return httpx.E(http.StatusForbidden, "origin_not_allowed", "This site is not allowed to use the chat widget.")
}

// visitor resolves the visitor, checks their session's widget is enabled and the
// request origin is one it allows.
func (h *handler) visitor(ctx context.Context, origin string) (*auth.Principal, error) {
	p, err := auth.RequireVisitor(ctx)
	if err != nil {
		return nil, err
	}
	g, err := loadVisitorWidget(ctx, h.d.DB.Pool, p)
	if err != nil {
		return nil, err
	}
	if !g.Settings.Enabled {
		return nil, errWidgetDisabled
	}
	if err := checkOrigin(g.Settings, origin); err != nil {
		return nil, err
	}
	return p, nil
}

func (h *handler) allowWrite(p *auth.Principal, bucket string, limit int) error {
	if !h.d.Limiter.Allow("widget:"+bucket+":"+p.VisitorID.String(), limit, time.Minute) {
		return httpx.RateLimited()
	}
	return nil
}

// publicMessage is the visitor-facing form of a message (domain.PublicMessage): no
// recipient lists, delivery internals, sender checks or agent email addresses.
func publicMessage(m domain.Message) domain.Message {
	m = domain.PublicMessage(m)
	if m.Author.Type != "contact" {
		m.FromEmail = ""
	}
	return m
}

// writeProblem writes an RFC 9457 body from a raw handler.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "title": http.StatusText(status), "code": code,
		"detail": detail, "request_id": httpx.RequestIDFrom(r.Context())})
}

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil
}
