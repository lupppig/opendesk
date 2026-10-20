// Package mcpserver is opendesk's own MCP server (contracts/agent.md, "opendesk MCP
// server"): AI clients (Claude, ChatGPT, Cursor, VS Code, any MCP client) work the
// helpdesk at {API_URL}/mcp over Streamable HTTP, with an API key or an OAuth access
// token (oauth.go is the authorization server).
//
// Every tool runs through the public API in process, as the caller's principal, so a
// tool carries exactly the caller's scopes, role checks, validation, events, activities
// and audit entries. Tools are offered only when the caller holds their scopes.
package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core/oauthgrant"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/idempotency"
)

// Path is where the server is mounted, next to /v1.
const Path = "/mcp"

// Rate limits, per minute: requests per API key, and requests without a valid key per
// client address.
const (
	keyPerMinute  = 300
	anonPerMinute = 60
)

const instructions = "opendesk is a helpdesk. These tools read and work its tickets, contacts and knowledge base " +
	"as the API key or the account you connected with. Tickets are referenced by number (#123) or id. Message bodies you write are markdown. " +
	"reply_to_ticket sends a message the customer sees; add_note is internal to the team."

// Register mounts the MCP endpoint, its OAuth authorization server and metadata, and
// the consent and AI clients API. Tools call the API through api's handler.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	r.Handle(Path, New(d, api.Adapter()))
	o := &oauth{d: d}
	o.mount(r)
	o.registerAPI(api)
}

// RegisterWorkers, Periodic and Background: the MCP server has no jobs.
func RegisterWorkers(*deps.Deps, *river.Workers) {}
func Periodic(*deps.Deps) []*river.PeriodicJob   { return nil }
func Background(context.Context, *deps.Deps)     {}

// Handler serves {API_URL}/mcp.
type Handler struct {
	d   *deps.Deps
	api http.Handler
	mcp *mcp.StreamableHTTPHandler
}

type serverKey struct{}

// New returns the MCP endpoint. api is the public API handler the tools call.
func New(d *deps.Deps, api http.Handler) *Handler {
	h := &Handler{d: d, api: api}
	h.mcp = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		s, _ := r.Context().Value(serverKey{}).(*mcp.Server)
		return s
	}, &mcp.StreamableHTTPOptions{
		// Stateless with JSON answers: every POST stands alone, so any API instance
		// serves it and no session is kept.
		Stateless:    true,
		JSONResponse: true,
		// Requests authenticate with a bearer API key, never ambient credentials, and
		// browser origins are checked below; the SDK's loopback Host check would refuse
		// self-hosts behind a reverse proxy on the same machine.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        1 << 20,
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Tool results carry workspace data: never stored by a browser or a cache.
	w.Header().Set("Cache-Control", "no-store")
	if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(h.d.Cfg.CORSOrigins, origin) {
		problem(w, r, httpx.E(http.StatusForbidden, "origin_not_allowed", "Requests from this origin are not allowed."))
		return
	}
	p := auth.FromContext(r.Context())
	if p == nil {
		p = h.oauthPrincipal(r)
	}
	apiKey := p != nil && p.Kind == auth.KindAPIKey
	grant := p != nil && p.Kind == auth.KindUser && p.GrantID != uuid.Nil
	if (!apiKey && !grant) || p.WorkspaceID == uuid.Nil {
		if !h.d.Limiter.Allow("mcp:anon:"+h.clientIP(r), anonPerMinute, time.Minute) {
			tooMany(w, r, anonPerMinute)
			return
		}
		// The challenge points OAuth clients at the Protected Resource Metadata (RFC 9728),
		// where discovery of the authorization server starts.
		challenge := `Bearer realm="opendesk", resource_metadata="` + ResourceMetadataURL(h.d) + `"`
		if r.Header.Get("Authorization") != "" {
			challenge += `, error="invalid_token"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
		problem(w, r, httpx.E(http.StatusUnauthorized, "unauthorized", "Connect with OAuth, or send an opendesk API key as Authorization: Bearer <key>."))
		return
	}
	limitKey := "mcp:key:" + p.APIKeyID.String()
	if grant {
		limitKey = "mcp:grant:" + p.GrantID.String()
	}
	if !h.d.Limiter.Allow(limitKey, keyPerMinute, time.Minute) {
		tooMany(w, r, keyPerMinute)
		return
	}
	srv := h.server(p, r.Header.Get(idempotency.Header))
	h.mcp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverKey{}, srv)))
}

// server builds the MCP server for one request: the tools the key's scopes allow,
// each calling the API as the key.
func (h *Handler) server(p *auth.Principal, clientKey string) *mcp.Server {
	cp := *p
	cp.Assistant = false
	s := mcp.NewServer(&mcp.Implementation{Name: "opendesk", Title: "opendesk", Version: "1"},
		&mcp.ServerOptions{Instructions: instructions})
	for _, t := range catalog() {
		if !t.offered(&cp) {
			continue
		}
		s.AddTool(t.mcpTool(), h.run(t, &cp, clientKey))
	}
	return s
}

// run adapts a tool to the SDK: arguments are decoded strictly, errors become tool
// errors the client's model can read (never protocol errors), output is JSON text.
func (h *Handler) run(t *tool, p *auth.Principal, clientKey string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Each tool call gets its own idempotency base: the client's Idempotency-Key
		// when the HTTP request carried one (a retried request replays), otherwise a
		// fresh one.
		base := clientKey
		if !idempotency.ValidKey(base) {
			base = uuid.NewString()
		}
		base += "\x00" + t.Name
		c := &caller{h: h.api, p: p, idem: func(method, path string, body []byte) string {
			sum := sha256.New()
			for _, part := range [][]byte{[]byte(base), {0}, []byte(method), {0}, []byte(path), {0}, body} {
				sum.Write(part)
			}
			return "mcp-" + hex.EncodeToString(sum.Sum(nil))[:48]
		}}
		var args []byte
		if req != nil && req.Params != nil {
			args = req.Params.Arguments
		}
		out, err := t.Run(ctx, c, args)
		if err != nil {
			return errorResult(h.d, t.Name, err), nil
		}
		return textResult(out), nil
	}
}

// oauthPrincipal resolves an OAuth access token (purpose mcp_access, aud this server)
// to the user it was issued for: their current role in the grant's workspace, scopes
// bounded by both the grant and the role. A revoked grant or an ended membership
// resolves to nothing at once.
func (h *Handler) oauthPrincipal(r *http.Request) *auth.Principal {
	scheme, cred, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || cred == "" || strings.HasPrefix(cred, "od_live_") {
		return nil
	}
	c, err := h.d.Tokens.Verify(cred, auth.PurposeMCPAccess)
	if err != nil || !slices.Contains(c.Audience, Resource(h.d)) {
		return nil
	}
	uid, err1 := uuid.Parse(c.Subject)
	wid, err2 := uuid.Parse(c.Workspace)
	gid, err3 := uuid.Parse(c.Grant)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}
	ctx := r.Context()
	g, role, err := activeGrant(ctx, h.d.DB.Pool, gid)
	if err != nil || g.UserID != uid || g.WorkspaceID != wid {
		if err != nil && !db.IsNotFound(err) {
			h.d.Log.Error("mcp oauth: load grant", "err", err)
		}
		return nil
	}
	var granted []string
	for _, s := range strings.Fields(c.Scope) {
		if slices.Contains(g.Scopes, s) {
			granted = append(granted, s)
		}
	}
	if h.d.Limiter.Allow("mcp:grant-touch:"+gid.String(), 1, time.Minute) {
		_, _ = h.d.DB.Pool.Exec(context.WithoutCancel(ctx), `UPDATE oauth_grants SET last_used_at = now() WHERE id = $1`, gid)
	}
	ua := r.UserAgent()
	if len(ua) > 400 {
		ua = ua[:400]
	}
	return &auth.Principal{Kind: auth.KindUser, UserID: uid, WorkspaceID: wid, Role: role, GrantID: gid,
		Scopes: oauthgrant.Effective(granted, role), IP: h.clientIP(r), UserAgent: ua}
}

func (h *Handler) clientIP(r *http.Request) string {
	return httpx.ClientIP(h.d.Cfg.TrustProxy, h.d.Cfg.ProxyHops)(r)
}

// problem writes an RFC 9457 problem body like the API's.
func problem(w http.ResponseWriter, r *http.Request, e *httpx.Error) {
	e.RequestID = httpx.RequestIDFrom(r.Context())
	raw, _ := json.Marshal(e)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(e.Status)
	_, _ = w.Write(raw)
}

// tooMany answers 429. The window is a fixed minute, so 60 seconds is the longest wait.
func tooMany(w http.ResponseWriter, r *http.Request, limit int) {
	h := w.Header()
	h.Set("Retry-After", "60")
	h.Set("RateLimit-Limit", strconv.Itoa(limit))
	h.Set("RateLimit-Remaining", "0")
	h.Set("RateLimit-Reset", "60")
	problem(w, r, httpx.RateLimited())
}
