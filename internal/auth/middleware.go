package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
)

// WorkspaceHeader selects the workspace for user (session) requests.
const WorkspaceHeader = "OD-Workspace"

// Resolver turns request credentials into a Principal. It never rejects a request;
// handlers call Require* to enforce what they need.
type Resolver struct {
	Pool   *pgxpool.Pool
	Tokens *Tokens
	// ClientIP extracts the caller address (honors proxies only when configured).
	ClientIP func(*http.Request) string

	mu        sync.Mutex
	lastTouch map[uuid.UUID]time.Time
}

func (rv *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := rv.resolve(r)
		if p != nil {
			r = r.WithContext(WithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}

func (rv *Resolver) resolve(r *http.Request) *Principal {
	h := r.Header.Get("Authorization")
	scheme, cred, ok := strings.Cut(h, " ")
	if !ok || cred == "" {
		return nil
	}
	ctx := r.Context()
	base := Principal{IP: rv.ClientIP(r), UserAgent: truncate(r.UserAgent(), 400), ResolvedAt: time.Now()}
	switch {
	case strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(cred, "od_live_"):
		p := base
		if !rv.resolveAPIKey(ctx, cred, &p) {
			return nil
		}
		return &p
	case strings.EqualFold(scheme, "Bearer"):
		c, err := rv.Tokens.Verify(cred, PurposeAccess)
		if err != nil {
			return nil
		}
		uid, err1 := uuid.Parse(c.Subject)
		sid, err2 := uuid.Parse(c.SessionID)
		if err1 != nil || err2 != nil {
			return nil
		}
		p := base
		p.Kind, p.UserID, p.SessionID, p.MFAVerified = KindUser, uid, sid, c.MFA
		// An access token works only while its session does: logout, revocation, a
		// password change or a suspension ends it at once, not when the token expires.
		wid, _ := uuid.Parse(r.Header.Get(WorkspaceHeader))
		var role *string
		var suspended *bool
		err = rv.Pool.QueryRow(ctx, `SELECT m.role, w.suspended_at IS NOT NULL FROM sessions s
			JOIN users u ON u.id = s.user_id AND u.suspended_at IS NULL
			LEFT JOIN members m ON m.user_id = s.user_id AND m.workspace_id = $3
			LEFT JOIN workspaces w ON w.id = m.workspace_id
			WHERE s.id = $1 AND s.user_id = $2 AND s.revoked_at IS NULL AND s.expires_at > now()`, sid, uid, wid).Scan(&role, &suspended)
		if err != nil {
			return nil
		}
		if role != nil && suspended != nil {
			p.WorkspaceID, p.Role, p.WorkspaceSuspended = wid, Role(*role), *suspended
		}
		return &p
	case strings.EqualFold(scheme, "Visitor"):
		p := base
		if !rv.resolveVisitor(ctx, cred, &p) {
			return nil
		}
		return &p
	}
	return nil
}

func (rv *Resolver) resolveAPIKey(ctx context.Context, key string, p *Principal) bool {
	var expires *time.Time
	err := rv.Pool.QueryRow(ctx, `SELECT k.id, k.workspace_id, k.scopes, k.expires_at, w.suspended_at IS NOT NULL FROM api_keys k
		JOIN workspaces w ON w.id = k.workspace_id
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL`, crypto.HashToken(key)).Scan(&p.APIKeyID, &p.WorkspaceID, &p.Scopes, &expires, &p.WorkspaceSuspended)
	if err != nil || (expires != nil && time.Now().After(*expires)) {
		return false
	}
	p.Kind = KindAPIKey
	if rv.shouldTouch(p.APIKeyID) {
		_, _ = rv.Pool.Exec(context.WithoutCancel(ctx), `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, p.APIKeyID)
	}
	return true
}

// VisitorSessionLive is the SQL condition (widget_sessions alias s) of a visitor session
// that still works: used within 90 days, and a verified one started within 30 days. A
// signed-in visitor's page verifies them again, so a fresh session follows without asking.
const VisitorSessionLive = `s.last_seen_at > now() - interval '90 days' AND (NOT s.verified OR s.created_at > now() - interval '30 days')`

func (rv *Resolver) resolveVisitor(ctx context.Context, token string, p *Principal) bool {
	var widgetID *uuid.UUID
	err := rv.Pool.QueryRow(ctx, `SELECT s.id, s.workspace_id, s.contact_id, s.verified, s.widget_id FROM widget_sessions s
		JOIN contacts c ON c.id = s.contact_id AND NOT c.blocked
		JOIN workspaces w ON w.id = s.workspace_id AND w.suspended_at IS NULL
		WHERE s.token_hash = $1 AND `+VisitorSessionLive, crypto.HashToken(token)).Scan(&p.VisitorID, &p.WorkspaceID, &p.ContactID, &p.Verified, &widgetID)
	if err != nil {
		return false
	}
	if widgetID != nil {
		p.WidgetID = *widgetID
	}
	p.Kind = KindVisitor
	if rv.shouldTouch(p.VisitorID) {
		_, _ = rv.Pool.Exec(context.WithoutCancel(ctx), `UPDATE widget_sessions SET last_seen_at = now() WHERE id = $1`, p.VisitorID)
	}
	return true
}

// shouldTouch rate-limits last-used writes to one per minute per credential.
func (rv *Resolver) shouldTouch(id uuid.UUID) bool {
	rv.mu.Lock()
	defer rv.mu.Unlock()
	if rv.lastTouch == nil {
		rv.lastTouch = map[uuid.UUID]time.Time{}
	}
	if t, ok := rv.lastTouch[id]; ok && time.Since(t) < time.Minute {
		return false
	}
	if len(rv.lastTouch) > 10000 {
		rv.lastTouch = map[uuid.UUID]time.Time{}
	}
	rv.lastTouch[id] = time.Now()
	return true
}

// Errors returned by Require*; the HTTP layer maps them to 401/403.
var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("you do not have permission to do that")
	ErrNoWorkspace     = errors.New("select a workspace with the OD-Workspace header")
	ErrMFARequired     = errors.New("this action requires two-factor authentication")
	// ErrWorkspaceSuspended: an instance admin suspended the workspace (contracts/admin.md).
	ErrWorkspaceSuspended = errors.New("this workspace is suspended")
)

// RequireUser demands a signed-in person (not an API key or visitor).
func RequireUser(ctx context.Context) (*Principal, error) {
	p := FromContext(ctx)
	if p == nil || p.Kind != KindUser {
		return nil, ErrUnauthenticated
	}
	if p.GrantID != uuid.Nil {
		return nil, ErrForbidden
	}
	return p, nil
}

// RequireWorkspace demands a workspace caller with at least minRole (users) or scope (API keys).
// A user acting through an OAuth grant needs both. Pass scope "" for routes API keys and
// OAuth grants may not call.
func RequireWorkspace(ctx context.Context, minRole Role, scope string) (*Principal, error) {
	p := FromContext(ctx)
	if p == nil || p.Kind == KindVisitor {
		return nil, ErrUnauthenticated
	}
	switch p.Kind {
	case KindUser:
		if p.WorkspaceID == uuid.Nil {
			return nil, ErrNoWorkspace
		}
		if p.WorkspaceSuspended {
			return nil, ErrWorkspaceSuspended
		}
		if !p.Role.AtLeast(minRole) {
			return nil, ErrForbidden
		}
		if p.GrantID != uuid.Nil && (scope == "" || !p.HasScope(scope)) {
			return nil, ErrForbidden
		}
	case KindAPIKey:
		if p.WorkspaceSuspended {
			return nil, ErrWorkspaceSuspended
		}
		if scope == "" || !p.HasScope(scope) {
			return nil, ErrForbidden
		}
	default:
		return nil, ErrUnauthenticated
	}
	return p, nil
}

// RequireVisitor demands a chat widget visitor session.
func RequireVisitor(ctx context.Context) (*Principal, error) {
	p := FromContext(ctx)
	if p == nil || p.Kind != KindVisitor {
		return nil, ErrUnauthenticated
	}
	return p, nil
}

// RequireMFA demands that a user session verified a second factor when the user has one.
// Used for owner-level operations (transfer, delete workspace, rotate secrets).
func RequireMFA(ctx context.Context, q db.Querier, p *Principal) error {
	if p.Kind != KindUser || p.MFAVerified {
		return nil
	}
	var enabled bool
	if err := q.QueryRow(ctx, `SELECT totp_enabled FROM users WHERE id = $1`, p.UserID).Scan(&enabled); err != nil {
		return err
	}
	if enabled {
		return ErrMFARequired
	}
	return nil
}

// RecentLoginWindow bounds how old a session may be for owner-level operations.
const RecentLoginWindow = 12 * time.Hour

// PassesStepUp reports whether p passes the owner-level gate without asking for anything:
// a user whose session verified a second factor when they have one, signed in within
// RecentLoginWindow. API keys never do.
func PassesStepUp(ctx context.Context, q db.Querier, p *Principal) (bool, error) {
	if p.Kind != KindUser {
		return false, nil
	}
	var created time.Time
	var verified, totp bool
	err := q.QueryRow(ctx, `SELECT s.created_at, s.mfa_verified, u.totp_enabled FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.user_id = $2 AND s.revoked_at IS NULL`, p.SessionID, p.UserID).Scan(&created, &verified, &totp)
	if db.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if totp && !verified && !p.MFAVerified {
		return false, nil
	}
	return time.Since(created) <= RecentLoginWindow, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
