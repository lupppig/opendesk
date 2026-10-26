package core

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Several workspaces (contracts/api.md "Several workspaces"): a person in several
// workspaces orders and hides them in the switcher, sees every workspace's sidebar
// numbers in one answer (Spaces, the bell), and follows them all on one socket. Every
// request still targets one workspace; these routes are the person's own, and each
// workspace's numbers are computed as that workspace's own routes would for them
// (their role there, that workspace's private teams).

const maxPrefIDs = 100

// WorkspacePrefs is how a person arranges their workspaces. Workspace ids in it only
// arrange the switcher: a workspace the person is not in is ignored, never shown.
type WorkspacePrefs struct {
	Order     []uuid.UUID `json:"order" maxItems:"100" doc:"Workspace ids in switcher order; the rest follow in joining order"`
	Hidden    []uuid.UUID `json:"hidden" maxItems:"100" doc:"Workspaces folded away in the switcher and left out of the bell and the Everywhere lists"`
	UpdatedAt *time.Time  `json:"updated_at,omitempty" readOnly:"true"`
}

type workspacePrefsInput struct {
	Order  *[]uuid.UUID `json:"order,omitempty" maxItems:"100"`
	Hidden *[]uuid.UUID `json:"hidden,omitempty" maxItems:"100"`
}

func defaultWorkspacePrefs() WorkspacePrefs {
	return WorkspacePrefs{Order: []uuid.UUID{}, Hidden: []uuid.UUID{}}
}

func loadWorkspacePrefs(ctx context.Context, q db.Querier, uid uuid.UUID) (WorkspacePrefs, error) {
	out := defaultWorkspacePrefs()
	var updated time.Time
	err := q.QueryRow(ctx, `SELECT workspace_order, hidden_workspaces, updated_at FROM user_workspace_prefs WHERE user_id = $1`, uid).
		Scan(&out.Order, &out.Hidden, &updated)
	if db.IsNotFound(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.UpdatedAt = &updated
	return out, nil
}

// uniqueIDs keeps the first of each id, in order.
func uniqueIDs[T comparable](in []T) []T {
	out := make([]T, 0, len(in))
	for _, x := range in {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// applyWorkspacePrefs validates a change and applies it to cur.
func applyWorkspacePrefs(cur WorkspacePrefs, in workspacePrefsInput) (WorkspacePrefs, error) {
	out := cur
	if in.Order != nil {
		if len(*in.Order) > maxPrefIDs {
			return out, httpx.Validation("order lists at most 100 workspaces.")
		}
		out.Order = uniqueIDs(*in.Order)
	}
	if in.Hidden != nil {
		if len(*in.Hidden) > maxPrefIDs {
			return out, httpx.Validation("hidden lists at most 100 workspaces.")
		}
		out.Hidden = uniqueIDs(*in.Hidden)
	}
	return out, nil
}

// WorkspaceOverview is one workspace's entry in the caller's overview.
type WorkspaceOverview struct {
	ID        uuid.UUID     `json:"id"`
	Name      string        `json:"name"`
	Slug      string        `json:"slug"`
	AvatarURL domain.Avatar `json:"avatar_url"`
	Role      string        `json:"role" enum:"owner,admin,agent,viewer"`
	Suspended bool          `json:"suspended"`
	// UnreadNotifications are the caller's own unread notifications there.
	UnreadNotifications int `json:"unread_notifications"`
	// TicketCounts is GET /v1/tickets/counts as that workspace answers the caller; null while suspended.
	TicketCounts any `json:"ticket_counts" doc:"GET /v1/tickets/counts for this workspace; null while it is suspended"`
	// MailCounts is GET /v1/collab/counts there; null without Collab.
	MailCounts any `json:"mail_counts" doc:"GET /v1/collab/counts for this workspace; null without Collab"`
}

type overviewBody struct {
	Workspaces []WorkspaceOverview `json:"workspaces"`
}

// workspacePrincipal is the caller as a request with OD-Workspace set to m's workspace
// would resolve them: that workspace, their role there, nothing cached from another.
func workspacePrincipal(p *auth.Principal, m CoreMembership) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: p.UserID, SessionID: p.SessionID, MFAVerified: p.MFAVerified,
		WorkspaceID: m.Workspace.ID, Role: auth.Role(m.Role), WorkspaceSuspended: m.Suspended, IP: p.IP, UserAgent: p.UserAgent}
}

func (s *svc) overview(ctx context.Context, p *auth.Principal) ([]WorkspaceOverview, error) {
	ms, err := loadMemberships(ctx, s.d.DB.Pool, p.UserID)
	if err != nil {
		return nil, err
	}
	unread := map[uuid.UUID]int{}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT n.workspace_id, count(*) FROM notifications n
		JOIN members m ON m.workspace_id = n.workspace_id AND m.user_id = n.user_id
		WHERE n.user_id = $1 AND n.read_at IS NULL GROUP BY n.workspace_id`, p.UserID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var wid uuid.UUID
		var n int
		if err := rows.Scan(&wid, &n); err != nil {
			rows.Close()
			return nil, err
		}
		unread[wid] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]WorkspaceOverview, 0, len(ms))
	for _, m := range ms {
		e := WorkspaceOverview{ID: m.Workspace.ID, Name: m.Workspace.Name, Slug: m.Workspace.Slug, AvatarURL: m.Workspace.AvatarURL,
			Role: m.Role, Suspended: m.Suspended}
		if !m.Suspended {
			e.UnreadNotifications = unread[m.Workspace.ID]
			wp := workspacePrincipal(p, m)
			if f := s.d.Overview.TicketCounts; f != nil {
				if e.TicketCounts, err = f(ctx, wp); err != nil {
					return nil, err
				}
			}
			if f := s.d.Overview.MailCounts; f != nil {
				if e.MailCounts, err = f(ctx, wp); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *svc) registerWorkspaceOverview(api huma.API) {
	tags := []string{"Account"}

	register(s, api, huma.Operation{OperationID: "get-workspace-prefs", Method: http.MethodGet, Path: "/v1/me/workspace-prefs",
		Summary: "Get how your workspaces are arranged", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body WorkspacePrefs }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := loadWorkspacePrefs(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body WorkspacePrefs }{out}, nil
		})

	register(s, api, huma.Operation{OperationID: "put-workspace-prefs", Method: http.MethodPut, Path: "/v1/me/workspace-prefs",
		Summary: "Change how your workspaces are arranged", Tags: tags,
		Description: "Fields left out keep their value."},
		func(ctx context.Context, in *struct{ Body workspacePrefsInput }) (*struct{ Body WorkspacePrefs }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			cur, err := loadWorkspacePrefs(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			next, err := applyWorkspacePrefs(cur, in.Body)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var updated time.Time
			err = s.d.DB.Pool.QueryRow(ctx, `INSERT INTO user_workspace_prefs (user_id, workspace_order, hidden_workspaces, updated_at)
				VALUES ($1, $2, $3, now())
				ON CONFLICT (user_id) DO UPDATE SET workspace_order = EXCLUDED.workspace_order,
					hidden_workspaces = EXCLUDED.hidden_workspaces, updated_at = now()
				RETURNING updated_at`, p.UserID, next.Order, next.Hidden).Scan(&updated)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			next.UpdatedAt = &updated
			return &struct{ Body WorkspacePrefs }{next}, nil
		})

	register(s, api, huma.Operation{OperationID: "get-overview", Method: http.MethodGet, Path: "/v1/me/overview",
		Summary: "Get every workspace's sidebar numbers", Tags: tags,
		Description: "One entry per workspace you are in, in joining order. Each workspace's numbers are what its own routes answer you: your role there and its private teams. No OD-Workspace header."},
		func(ctx context.Context, _ *struct{}) (*struct{ Body overviewBody }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := s.overview(ctx, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body overviewBody }{overviewBody{Workspaces: list}}, nil
		})
}

// realtimeWorkspaces is the ws token's workspaces claim for a person: the other
// workspaces they are in (p's is the token's own wid) that are not suspended, with their
// role and the private teams they may see in each, in joining order within the budget.
func (s *svc) realtimeWorkspaces(ctx context.Context, p *auth.Principal) (map[string]auth.WSWorkspace, error) {
	ms, err := loadMemberships(ctx, s.d.DB.Pool, p.UserID)
	if err != nil {
		return nil, err
	}
	out := map[string]auth.WSWorkspace{}
	teams := 0
	for _, m := range ms {
		if m.Suspended || m.Workspace.ID == p.WorkspaceID || len(out) >= auth.WSMaxWorkspaces {
			continue
		}
		visible, err := domain.VisibleTeams(ctx, s.d.DB.Pool, workspacePrincipal(p, m))
		if err != nil {
			return nil, err
		}
		if teams+len(visible) > auth.WSMaxTeams {
			continue
		}
		teams += len(visible)
		e := auth.WSWorkspace{Role: m.Role}
		for _, t := range visible {
			e.Teams = append(e.Teams, t.String())
		}
		out[m.Workspace.ID.String()] = e
	}
	return out, nil
}
