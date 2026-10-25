package auth

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleAgent  Role = "agent"
	RoleViewer Role = "viewer"
)

var roleRank = map[Role]int{RoleViewer: 1, RoleAgent: 2, RoleAdmin: 3, RoleOwner: 4}

// AtLeast reports whether r is min or higher.
func (r Role) AtLeast(min Role) bool { return roleRank[r] >= roleRank[min] }

// Valid reports a known role.
func (r Role) Valid() bool { _, ok := roleRank[r]; return ok }

// API key scopes.
const (
	ScopeTicketsRead   = "tickets:read"
	ScopeTicketsWrite  = "tickets:write"
	ScopeContactsRead  = "contacts:read"
	ScopeContactsWrite = "contacts:write"
	ScopeKBRead        = "kb:read"
	ScopeKBWrite       = "kb:write"
	ScopeSettingsRead  = "settings:read"
	ScopeSettingsWrite = "settings:write"
	ScopeRealtime      = "realtime"
)

var AllScopes = []string{ScopeTicketsRead, ScopeTicketsWrite, ScopeContactsRead, ScopeContactsWrite,
	ScopeKBRead, ScopeKBWrite, ScopeSettingsRead, ScopeSettingsWrite, ScopeRealtime}

type Kind string

const (
	KindUser    Kind = "user"
	KindAPIKey  Kind = "api_key"
	KindVisitor Kind = "visitor"
)

// Principal is the authenticated caller of a request.
type Principal struct {
	Kind        Kind
	UserID      uuid.UUID // users
	SessionID   uuid.UUID // users
	MFAVerified bool
	APIKeyID    uuid.UUID // api keys
	Scopes      []string  // api keys
	ContactID   uuid.UUID // visitors
	VisitorID   uuid.UUID // visitors: widget_sessions.id
	WidgetID    uuid.UUID // visitors: the widget the session started in
	WorkspaceID uuid.UUID // zero until resolved from OD-Workspace or the credential
	Role        Role      // users, once the workspace is resolved
	// WorkspaceSuspended: the resolved workspace is suspended; Require* refuse it.
	WorkspaceSuspended bool
	Verified           bool // visitors: identity signed by the customer's backend
	IP                 string
	UserAgent          string
	// Assistant marks a request the assistant makes for this user after they approved
	// it: it runs with the user's own role and checks, and is attributed to ai on
	// behalf of the user.
	Assistant bool
	// ViaAssistant marks every request the assistant makes for this user, reads included.
	// It changes no attribution; routes that are the person's alone (Collab mail, what they
	// share with teammates' assistant) refuse it.
	ViaAssistant bool
	// GrantID is set when a user acts through an AI client connected to the MCP server
	// with OAuth (contracts/agent.md). Scopes then bound the request like an API key's,
	// on top of the user's role, and account routes are refused.
	GrantID uuid.UUID
	// HiddenTeams caches the private teams this caller may not see (domain.HiddenTeams).
	HiddenTeams       []uuid.UUID
	HiddenTeamsLoaded bool
	// ResolvedAt is when the credential began to be resolved, before any lookup: what
	// the request reads from here on is at least as recent as this.
	ResolvedAt time.Time
}

func (p *Principal) HasScope(s string) bool { return slices.Contains(p.Scopes, s) }

// ActorUserID returns the user id for attribution, or nil for non-users.
func (p *Principal) ActorUserID() *uuid.UUID {
	if p.Kind == KindUser {
		return &p.UserID
	}
	return nil
}

// ActorAPIKeyID returns the API key id for attribution, or nil.
func (p *Principal) ActorAPIKeyID() *uuid.UUID {
	if p.Kind == KindAPIKey {
		return &p.APIKeyID
	}
	return nil
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the caller, or nil when the request is anonymous.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}
