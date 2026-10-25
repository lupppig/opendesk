package core

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Workspaces together (contracts/api.md "Workspaces together"): a person in several
// workspaces works them side by side. Every request still targets one workspace; these
// two routes are the person's own: their settings for the arrangement, and one answer
// with each workspace's sidebar numbers, each computed as that workspace's own routes
// would for them (their role there, that workspace's private teams).

// Together modes and layouts.
const (
	ModeTogether   = "together"
	ModeSeparate   = "separate"
	LayoutGrouped  = "grouped"
	LayoutMixed    = "mixed"
	collapsedAll   = "everywhere"
	maxPrefIDs     = 100
	maxPrefItems   = 500
	maxPrefItemLen = 120
)

// itemIDRe is a sidebar item id qualified by its workspace: `<wid>/<kind>:<id>`, or
// `everywhere/<list>` for the Everywhere lists.
var itemIDRe = regexp.MustCompile(`^(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|everywhere)/[a-z_]{1,32}(?::[A-Za-z0-9_-]{1,64})?$`)

// WorkspacePrefItems are the sidebar items a person hid and the order they put them in.
type WorkspacePrefItems struct {
	Hidden []string `json:"hidden" required:"false" maxItems:"500" doc:"Item ids, qualified by workspace: <workspace id>/<kind>:<id>"`
	Order  []string `json:"order" required:"false" maxItems:"500" doc:"Item ids in the order the person put them; unknown ids are ignored"`
}

// WorkspacePrefs is a person's Together settings. Workspace ids in it only arrange the
// sidebar: a workspace the person is not in is ignored, never shown.
type WorkspacePrefs struct {
	Mode       string             `json:"mode" enum:"together,separate" doc:"together (the default) shows every workspace in one sidebar; separate shows one at a time"`
	ModeSet    bool               `json:"mode_set" readOnly:"true" doc:"Whether the person chose the mode (false: the default applies)"`
	Layout     string             `json:"layout" enum:"grouped,mixed" doc:"grouped: a section per workspace; mixed: sections combined across workspaces"`
	Order      []uuid.UUID        `json:"order" maxItems:"100" doc:"Workspace ids in sidebar order; the rest follow in joining order"`
	Hidden     []uuid.UUID        `json:"hidden" maxItems:"100" doc:"Workspaces left out of the sidebar and the Everywhere lists"`
	Collapsed  []string           `json:"collapsed" maxItems:"101" doc:"Folded sidebar groups: workspace ids, or everywhere"`
	Items      WorkspacePrefItems `json:"items"`
	Everywhere bool               `json:"everywhere" doc:"Show the Everywhere lists (assigned to me and all mail, across workspaces)"`
	UpdatedAt  *time.Time         `json:"updated_at,omitempty" readOnly:"true"`
}

type workspacePrefsInput struct {
	Mode       *string             `json:"mode,omitempty" doc:"together or separate; empty goes back to the default"`
	Layout     *string             `json:"layout,omitempty" enum:"grouped,mixed"`
	Order      *[]uuid.UUID        `json:"order,omitempty" maxItems:"100"`
	Hidden     *[]uuid.UUID        `json:"hidden,omitempty" maxItems:"100"`
	Collapsed  *[]string           `json:"collapsed,omitempty" maxItems:"101"`
	Items      *WorkspacePrefItems `json:"items,omitempty"`
	Everywhere *bool               `json:"everywhere,omitempty"`
}

func defaultWorkspacePrefs() WorkspacePrefs {
	return WorkspacePrefs{Mode: ModeTogether, Layout: LayoutGrouped, Order: []uuid.UUID{}, Hidden: []uuid.UUID{},
		Collapsed: []string{}, Items: WorkspacePrefItems{Hidden: []string{}, Order: []string{}}, Everywhere: true}
}

func loadWorkspacePrefs(ctx context.Context, q db.Querier, uid uuid.UUID) (WorkspacePrefs, error) {
	out := defaultWorkspacePrefs()
	var mode *string
	var items []byte
	var updated time.Time
	err := q.QueryRow(ctx, `SELECT mode, layout, workspace_order, hidden_workspaces, collapsed, items, everywhere, updated_at
		FROM user_workspace_prefs WHERE user_id = $1`, uid).
		Scan(&mode, &out.Layout, &out.Order, &out.Hidden, &out.Collapsed, &items, &out.Everywhere, &updated)
	if db.IsNotFound(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if mode != nil {
		out.Mode, out.ModeSet = *mode, true
	}
	_ = json.Unmarshal(items, &out.Items)
	out.Items.Hidden = orEmpty(out.Items.Hidden)
	out.Items.Order = orEmpty(out.Items.Order)
	out.UpdatedAt = &updated
	return out, nil
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
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
	if in.Mode != nil {
		switch *in.Mode {
		case "":
			out.Mode, out.ModeSet = ModeTogether, false
		case ModeTogether, ModeSeparate:
			out.Mode, out.ModeSet = *in.Mode, true
		default:
			return out, httpx.Validation("mode must be together or separate.")
		}
	}
	if in.Layout != nil {
		if *in.Layout != LayoutGrouped && *in.Layout != LayoutMixed {
			return out, httpx.Validation("layout must be grouped or mixed.")
		}
		out.Layout = *in.Layout
	}
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
	if in.Collapsed != nil {
		if len(*in.Collapsed) > maxPrefIDs+1 {
			return out, httpx.Validation("collapsed lists at most 101 groups.")
		}
		for _, c := range *in.Collapsed {
			if _, err := uuid.Parse(c); c != collapsedAll && (err != nil || len(c) != 36) {
				return out, httpx.Validation("collapsed takes workspace ids and everywhere.")
			}
		}
		out.Collapsed = uniqueIDs(*in.Collapsed)
	}
	if in.Items != nil {
		for _, list := range [][]string{in.Items.Hidden, in.Items.Order} {
			if len(list) > maxPrefItems {
				return out, httpx.Validation("items lists at most 500 ids.")
			}
			for _, id := range list {
				if len(id) > maxPrefItemLen || !itemIDRe.MatchString(id) {
					return out, httpx.Validation("Item ids look like <workspace id>/view:<id>.")
				}
			}
		}
		out.Items = WorkspacePrefItems{Hidden: uniqueIDs(orEmpty(in.Items.Hidden)), Order: uniqueIDs(orEmpty(in.Items.Order))}
	}
	if in.Everywhere != nil {
		out.Everywhere = *in.Everywhere
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

func (s *svc) registerTogether(api huma.API) {
	tags := []string{"Account"}

	register(s, api, huma.Operation{OperationID: "get-workspace-prefs", Method: http.MethodGet, Path: "/v1/me/workspace-prefs",
		Summary: "Get how your workspaces show together", Tags: tags},
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
		Summary: "Change how your workspaces show together", Tags: tags,
		Description: "Fields left out keep their value. mode \"\" goes back to the default."},
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
			var mode *string
			if next.ModeSet {
				mode = &next.Mode
			}
			items, _ := json.Marshal(next.Items)
			var updated time.Time
			err = s.d.DB.Pool.QueryRow(ctx, `INSERT INTO user_workspace_prefs
				(user_id, mode, layout, workspace_order, hidden_workspaces, collapsed, items, everywhere, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
				ON CONFLICT (user_id) DO UPDATE SET mode = EXCLUDED.mode, layout = EXCLUDED.layout,
					workspace_order = EXCLUDED.workspace_order, hidden_workspaces = EXCLUDED.hidden_workspaces,
					collapsed = EXCLUDED.collapsed, items = EXCLUDED.items, everywhere = EXCLUDED.everywhere, updated_at = now()
				RETURNING updated_at`,
				p.UserID, mode, next.Layout, next.Order, next.Hidden, next.Collapsed, items, next.Everywhere).Scan(&updated)
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
