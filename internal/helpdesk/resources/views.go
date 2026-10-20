package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// View is the contract shape. A personal view (shared=false, no team) belongs to its
// creator; a team view (team_id) is shared with that team's members.
type View struct {
	ID       uuid.UUID        `json:"id"`
	Name     string           `json:"name"`
	Shared   bool             `json:"shared"`
	Filters  hd.TicketFilters `json:"filters"`
	Position int              `json:"position"`
	TeamID   *uuid.UUID       `json:"team_id" doc:"Shared with this team's members; shared is then false"`

	owner *uuid.UUID
}

type viewWriteBody struct {
	Name     *string           `json:"name,omitempty" maxLength:"100"`
	Filters  *hd.TicketFilters `json:"filters,omitempty"`
	Shared   *bool             `json:"shared,omitempty"`
	Position *int              `json:"position,omitempty" minimum:"0" maximum:"100000"`
	TeamID   Nullable[string]  `json:"team_id,omitempty" doc:"Share with a team's members (admins and the team's leads); null makes it a personal or shared view again"`
}

type viewOut struct{ Body View }

func scanView(r pgx.Row) (View, error) {
	var v View
	var raw []byte
	if err := r.Scan(&v.ID, &v.Name, &v.owner, &raw, &v.Position, &v.TeamID); err != nil {
		return v, err
	}
	v.Shared = v.owner == nil && v.TeamID == nil
	_ = json.Unmarshal(raw, &v.Filters)
	return v, nil
}

const viewSelect = `SELECT v.id, v.name, v.user_id, v.filters, v.position, v.team_id FROM views v`

// visibleView loads a view the caller can see: shared, their own personal view, a view of
// their team, or (for admins and the team's leads, who manage them) any team view.
func visibleView(ctx context.Context, q db.Querier, p *auth.Principal, id uuid.UUID, lock bool) (View, error) {
	sql := viewSelect + ` WHERE v.workspace_id = $1 AND v.id = $2 AND (` + hd.ViewVisibleSQL("v", 3) + `
		OR (v.team_id IS NOT NULL AND ($4 OR v.team_id IN (SELECT lt.team_id FROM team_members lt WHERE lt.user_id = $3 AND lt.role = 'lead'))))`
	if lock {
		sql += ` FOR UPDATE`
	}
	v, err := scanView(q.QueryRow(ctx, sql, p.WorkspaceID, id, viewer(p), teamManager(p)))
	if db.IsNotFound(err) {
		return v, httpx.NotFound("View")
	}
	return v, err
}

// canManageTeamView allows admins (and API keys with settings:write) and the team's leads.
func canManageTeamView(ctx context.Context, q db.Querier, p *auth.Principal, team uuid.UUID) error {
	if teamManager(p) {
		return nil
	}
	if p.Kind == auth.KindUser {
		role, err := hd.TeamRole(ctx, q, p.WorkspaceID, team, p.UserID)
		if err != nil {
			return err
		}
		if role == hd.TeamLead {
			return nil
		}
	}
	return httpx.E(403, "forbidden", "Only admins and the team's leads manage its views.")
}

// canWrite checks the caller may write v as it is (or becomes).
func canWrite(ctx context.Context, q db.Querier, p *auth.Principal, v View) error {
	if v.TeamID != nil {
		return canManageTeamView(ctx, q, p, *v.TeamID)
	}
	return canManage(p, v.Shared)
}

// viewer is the user id personal views are matched against (nil for API keys).
func viewer(p *auth.Principal) *uuid.UUID {
	if p.Kind == auth.KindUser {
		return &p.UserID
	}
	return nil
}

// canManage checks the caller may write a view that is (or becomes) shared or personal.
func canManage(p *auth.Principal, shared bool) error {
	if shared {
		if p.Kind == auth.KindUser && !p.Role.AtLeast(auth.RoleAdmin) {
			return httpx.Forbidden()
		}
		if p.Kind == auth.KindAPIKey && !p.HasScope(auth.ScopeSettingsWrite) {
			return httpx.Forbidden()
		}
		return nil
	}
	if p.Kind != auth.KindUser {
		return httpx.E(403, "forbidden", "API keys can only manage shared views")
	}
	return nil
}

// publishView announces a view to who can see it: a personal view to its owner alone.
func publishView(ctx context.Context, tx pgx.Tx, wid uuid.UUID, typ string, v View, data any) error {
	if v.owner != nil {
		_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Users: []uuid.UUID{*v.owner}, Data: data})
		return err
	}
	scope, err := privateTeam(ctx, tx, wid, v.TeamID)
	if err != nil {
		return err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, TeamID: scope, Data: data})
	return err
}

// applyViewTeam sets the view's team from the body: a team view is not shared and has no
// owner.
func applyViewTeam(ctx context.Context, q db.Querier, p *auth.Principal, b viewWriteBody, v *View) error {
	if !b.TeamID.Set {
		return nil
	}
	if b.TeamID.Null || strings.TrimSpace(b.TeamID.Value) == "" {
		if v.TeamID != nil {
			v.TeamID = nil
			v.Shared = b.Shared != nil && *b.Shared
			v.owner = nil
			// A team's lead manages its views, not the workspace's shared ones.
			if err := canManage(p, v.Shared); err != nil {
				return err
			}
			if !v.Shared {
				if p.Kind != auth.KindUser {
					return httpx.E(403, "forbidden", "API keys can only manage shared views")
				}
				v.owner = &p.UserID
			}
		}
		return nil
	}
	id, err := hd.ParseRef(b.TeamID.Value, "team_id")
	if err != nil {
		return err
	}
	if err := hd.CheckRef(ctx, q, p.WorkspaceID, "team", id); err != nil {
		return err
	}
	if b.Shared != nil && *b.Shared {
		return httpx.Validation("a team view is not shared: send shared false with team_id")
	}
	if err := canManageTeamView(ctx, q, p, id); err != nil {
		return err
	}
	v.TeamID, v.Shared, v.owner = &id, false, nil
	return nil
}

func registerViews(d *deps.Deps, api huma.API) {
	huma.Register(api, op("list-views", http.MethodGet, "/v1/views", "List views", "Views", 0),
		func(ctx context.Context, in *struct {
			TeamID string `query:"team_id" doc:"Only this team's views; admins and the team's leads see them all, to manage them"`
		}) (*struct{ Body httpx.Page[View] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if strings.TrimSpace(in.TeamID) != "" {
				team, err := hd.ParseRef(in.TeamID, "team_id")
				if err != nil {
					return nil, err
				}
				manage := canManageTeamView(ctx, d.DB.Pool, p, team) == nil
				rows, err := d.DB.Pool.Query(ctx, viewSelect+` WHERE v.workspace_id = $1 AND v.team_id = $2
					AND ($3 OR v.team_id IN (SELECT mt.team_id FROM team_members mt WHERE mt.user_id = $4))
					ORDER BY v.position, lower(v.name), v.id`, p.WorkspaceID, team, manage, viewer(p))
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (View, error) { return scanView(r) })
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				return &struct{ Body httpx.Page[View] }{list(out)}, nil
			}
			rows, err := d.DB.Pool.Query(ctx, viewSelect+` WHERE v.workspace_id = $1 AND `+hd.ViewVisibleSQL("v", 2)+`
				ORDER BY v.position, lower(v.name), v.id`, p.WorkspaceID, viewer(p))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (View, error) { return scanView(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[View] }{list(out)}, nil
		})

	huma.Register(api, op("create-view", http.MethodPost, "/v1/views", "Create a view", "Views", http.StatusCreated),
		func(ctx context.Context, in *struct{ Body viewWriteBody }) (*viewOut, error) {
			shared := in.Body.Shared != nil && *in.Body.Shared
			team := in.Body.TeamID.Set && !in.Body.TeamID.Null && strings.TrimSpace(in.Body.TeamID.Value) != ""
			minRole, scope := auth.RoleAgent, ""
			if shared || team {
				scope = auth.ScopeSettingsWrite
			}
			if shared {
				minRole = auth.RoleAdmin
			}
			p, err := auth.RequireWorkspace(ctx, minRole, scope)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !team {
				if err := canManage(p, shared); err != nil {
					return nil, err
				}
			}
			if in.Body.Name == nil {
				return nil, httpx.Validation("name is required")
			}
			name, err := cleanName("name", *in.Body.Name, 100)
			if err != nil {
				return nil, err
			}
			v := View{ID: ids.New(), Name: name, Shared: shared}
			if !shared && !team {
				v.owner = &p.UserID
			}
			if in.Body.Filters != nil {
				v.Filters = *in.Body.Filters
			}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := applyViewTeam(ctx, tx, p, in.Body, &v); err != nil {
					return err
				}
				if err := v.Filters.Normalize(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				if in.Body.Position != nil {
					v.Position = *in.Body.Position
				} else if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position) + 1, 0) FROM views v WHERE v.workspace_id = $1 AND `+hd.ViewVisibleSQL("v", 2),
					p.WorkspaceID, viewer(p)).Scan(&v.Position); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO views (id, workspace_id, user_id, name, filters, position, team_id) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
					v.ID, p.WorkspaceID, v.owner, v.Name, v.Filters, v.Position, v.TeamID); err != nil {
					return err
				}
				return publishView(ctx, tx, p.WorkspaceID, "view.created", v, map[string]any{"view": v})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &viewOut{v}, nil
		})

	huma.Register(api, op("get-view", http.MethodGet, "/v1/views/{id}", "Get a view", "Views", 0),
		func(ctx context.Context, in *idPath) (*viewOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "View")
			if err != nil {
				return nil, err
			}
			v, err := visibleView(ctx, d.DB.Pool, p, id, false)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &viewOut{v}, nil
		})

	huma.Register(api, op("update-view", http.MethodPatch, "/v1/views/{id}", "Update a view", "Views", 0),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body viewWriteBody
		}) (*viewOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "View")
			if err != nil {
				return nil, err
			}
			var v View
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var err error
				if v, err = visibleView(ctx, tx, p, id, true); err != nil {
					return err
				}
				if err := canWrite(ctx, tx, p, v); err != nil {
					return err
				}
				if err := applyViewTeam(ctx, tx, p, in.Body, &v); err != nil {
					return err
				}
				if in.Body.Shared != nil && *in.Body.Shared != v.Shared && v.TeamID == nil && !in.Body.TeamID.Set {
					// Changing who sees a view is a shared-view operation either way.
					if err := canManage(p, true); err != nil {
						return err
					}
					if !*in.Body.Shared {
						if err := canManage(p, false); err != nil {
							return err
						}
					}
					v.Shared = *in.Body.Shared
					if v.Shared {
						v.owner = nil
					} else {
						v.owner = &p.UserID
					}
				}
				if in.Body.Name != nil {
					if v.Name, err = cleanName("name", *in.Body.Name, 100); err != nil {
						return err
					}
				}
				if in.Body.Filters != nil {
					v.Filters = *in.Body.Filters
					if err := v.Filters.Normalize(ctx, tx, p.WorkspaceID); err != nil {
						return err
					}
				}
				if in.Body.Position != nil {
					v.Position = *in.Body.Position
				}
				if _, err := tx.Exec(ctx, `UPDATE views SET name = $3, filters = $4, position = $5, user_id = $6, team_id = $7, updated_at = now()
					WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id, v.Name, v.Filters, v.Position, v.owner, v.TeamID); err != nil {
					return err
				}
				return publishView(ctx, tx, p.WorkspaceID, "view.updated", v, map[string]any{"view": v})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &viewOut{v}, nil
		})

	huma.Register(api, op("delete-view", http.MethodDelete, "/v1/views/{id}", "Delete a view", "Views", http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "View")
			if err != nil {
				return nil, err
			}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				v, err := visibleView(ctx, tx, p, id, true)
				if err != nil {
					return err
				}
				if err := canWrite(ctx, tx, p, v); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM views WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id); err != nil {
					return err
				}
				return publishView(ctx, tx, p.WorkspaceID, "view.deleted", v, map[string]any{"view_id": id})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}
