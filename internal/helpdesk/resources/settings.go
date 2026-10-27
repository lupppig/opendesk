package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

const maxMinutes = 525600 // one year

// SlaPolicy is the contract shape.
type SlaPolicy struct {
	ID                   uuid.UUID `json:"id"`
	Name                 string    `json:"name"`
	Priority             *string   `json:"priority" enum:"low,normal,high,urgent"`
	FirstResponseMinutes int       `json:"first_response_minutes"`
	ResolutionMinutes    int       `json:"resolution_minutes"`
	BusinessHours        bool      `json:"business_hours" doc:"Count only open business hours: the team's own, else the workspace's"`
	Enabled              bool      `json:"enabled"`
	Position             int       `json:"position"`
	// TeamID limits the policy to the tickets of one team; nil applies to any team.
	TeamID *uuid.UUID `json:"team_id" doc:"Only this team's tickets; null for any"`
}

type slaPolicyWriteBody struct {
	Name                 *string          `json:"name,omitempty" maxLength:"200"`
	Priority             Nullable[string] `json:"priority,omitempty" doc:"low, normal, high, urgent, or null for any priority"`
	FirstResponseMinutes *int             `json:"first_response_minutes,omitempty" minimum:"1" maximum:"525600"`
	ResolutionMinutes    *int             `json:"resolution_minutes,omitempty" minimum:"1" maximum:"525600"`
	BusinessHours        *bool            `json:"business_hours,omitempty"`
	Enabled              *bool            `json:"enabled,omitempty"`
	Position             *int             `json:"position,omitempty" minimum:"0" maximum:"100000"`
	TeamID               Nullable[string] `json:"team_id,omitempty" doc:"A team id, or null for any team"`
}

func (b *slaPolicyWriteBody) apply(ctx context.Context, q db.Querier, wid uuid.UUID, s *SlaPolicy) error {
	if b.TeamID.Set {
		if b.TeamID.Null || strings.TrimSpace(b.TeamID.Value) == "" {
			s.TeamID = nil
		} else {
			id, err := hd.ParseRef(b.TeamID.Value, "team_id")
			if err != nil {
				return err
			}
			if err := hd.CheckRef(ctx, q, wid, "team", id); err != nil {
				return err
			}
			s.TeamID = &id
		}
	}
	if b.Name != nil {
		n, err := cleanName("name", *b.Name, 200)
		if err != nil {
			return err
		}
		s.Name = n
	}
	if s.Name == "" {
		return httpx.Validation("name is required")
	}
	if b.Priority.Set {
		if b.Priority.Null || b.Priority.Value == "" {
			s.Priority = nil
		} else if !slices.Contains(domain.Priorities, b.Priority.Value) {
			return httpx.Validation("priority must be one of " + strings.Join(domain.Priorities, ", ") + ", or null")
		} else {
			s.Priority = hd.Ptr(b.Priority.Value)
		}
	}
	if b.FirstResponseMinutes != nil {
		s.FirstResponseMinutes = *b.FirstResponseMinutes
	}
	if b.ResolutionMinutes != nil {
		s.ResolutionMinutes = *b.ResolutionMinutes
	}
	for _, m := range []int{s.FirstResponseMinutes, s.ResolutionMinutes} {
		if m < 1 || m > maxMinutes {
			return httpx.Validation("first_response_minutes and resolution_minutes must be between 1 and 525600")
		}
	}
	if b.BusinessHours != nil {
		s.BusinessHours = *b.BusinessHours
	}
	if b.Enabled != nil {
		s.Enabled = *b.Enabled
	}
	if b.Position != nil {
		s.Position = *b.Position
	}
	return nil
}

const slaSelect = `SELECT id, name, priority, first_response_minutes, resolution_minutes, business_hours, enabled, position, team_id FROM sla_policies`

func scanSLA(r pgx.Row) (SlaPolicy, error) {
	var s SlaPolicy
	err := r.Scan(&s.ID, &s.Name, &s.Priority, &s.FirstResponseMinutes, &s.ResolutionMinutes, &s.BusinessHours, &s.Enabled, &s.Position, &s.TeamID)
	return s, err
}

// Automation is the contract shape.
type Automation struct {
	ID         uuid.UUID      `json:"id"`
	Name       string         `json:"name"`
	Trigger    string         `json:"trigger" enum:"ticket_created,customer_replied,agent_replied,ticket_updated"`
	Conditions []hd.Condition `json:"conditions"`
	Actions    []hd.Action    `json:"actions"`
	Enabled    bool           `json:"enabled"`
	Position   int            `json:"position"`
	RunCount   int64          `json:"run_count"`
}

type automationWriteBody struct {
	Name       *string         `json:"name,omitempty" maxLength:"200"`
	Trigger    *string         `json:"trigger,omitempty" enum:"ticket_created,customer_replied,agent_replied,ticket_updated"`
	Conditions *[]hd.Condition `json:"conditions,omitempty"`
	Actions    *[]hd.Action    `json:"actions,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"`
	Position   *int            `json:"position,omitempty" minimum:"0" maximum:"100000"`
}

func (b *automationWriteBody) apply(ctx context.Context, q db.Querier, wid uuid.UUID, a *Automation) error {
	if b.Name != nil {
		n, err := cleanName("name", *b.Name, 200)
		if err != nil {
			return err
		}
		a.Name = n
	}
	if a.Name == "" {
		return httpx.Validation("name is required")
	}
	if b.Trigger != nil {
		a.Trigger = *b.Trigger
	}
	if !slices.Contains(hd.AutomationTriggers, a.Trigger) {
		return httpx.Validation("trigger must be one of " + strings.Join(hd.AutomationTriggers, ", "))
	}
	var err error
	if b.Conditions != nil {
		if a.Conditions, err = hd.ValidateConditions(ctx, q, wid, *b.Conditions); err != nil {
			return err
		}
	}
	if b.Actions != nil {
		a.Actions = *b.Actions
	}
	if a.Actions, err = hd.ValidateActions(ctx, q, wid, a.Actions); err != nil {
		return err
	}
	if err := hd.ValidateActionTrigger(a.Trigger, a.Actions); err != nil {
		return err
	}
	if a.Conditions == nil {
		a.Conditions = []hd.Condition{}
	}
	if b.Enabled != nil {
		a.Enabled = *b.Enabled
	}
	if b.Position != nil {
		a.Position = *b.Position
	}
	return nil
}

const automationSelect = `SELECT id, name, trigger, conditions, actions, enabled, position, run_count FROM automations`

func scanAutomation(r pgx.Row) (Automation, error) {
	var a Automation
	var conds, acts []byte
	if err := r.Scan(&a.ID, &a.Name, &a.Trigger, &conds, &acts, &a.Enabled, &a.Position, &a.RunCount); err != nil {
		return a, err
	}
	_ = json.Unmarshal(conds, &a.Conditions)
	_ = json.Unmarshal(acts, &a.Actions)
	if a.Conditions == nil {
		a.Conditions = []hd.Condition{}
	}
	if a.Actions == nil {
		a.Actions = []hd.Action{}
	}
	return a, nil
}

func registerSLA(d *deps.Deps, api huma.API) {
	huma.Register(api, op("list-sla-policies", http.MethodGet, "/v1/sla-policies", "List SLA policies", "SLA policies", 0),
		func(ctx context.Context, in *struct {
			TeamID string `query:"team_id" doc:"Only this team's policies"`
		}) (*struct{ Body httpx.Page[SlaPolicy] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var teamID *uuid.UUID
			if strings.TrimSpace(in.TeamID) != "" {
				id, err := hd.ParseRef(in.TeamID, "team_id")
				if err != nil {
					return nil, err
				}
				teamID = &id
			}
			rows, err := d.DB.Pool.Query(ctx, slaSelect+` WHERE workspace_id = $1 AND ($2::uuid IS NULL OR team_id = $2)
				ORDER BY position, created_at, id`, p.WorkspaceID, teamID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (SlaPolicy, error) { return scanSLA(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[SlaPolicy] }{list(out)}, nil
		})

	huma.Register(api, op("create-sla-policy", http.MethodPost, "/v1/sla-policies", "Create an SLA policy", "SLA policies", http.StatusCreated),
		func(ctx context.Context, in *struct{ Body slaPolicyWriteBody }) (*struct{ Body SlaPolicy }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s := SlaPolicy{ID: ids.New(), Enabled: true, Position: -1}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := in.Body.apply(ctx, tx, p.WorkspaceID, &s); err != nil {
					return err
				}
				if s.Position < 0 {
					if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position) + 1, 0) FROM sla_policies WHERE workspace_id = $1`, p.WorkspaceID).Scan(&s.Position); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `INSERT INTO sla_policies (id, workspace_id, name, priority, first_response_minutes,
					resolution_minutes, enabled, position, business_hours, team_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, s.ID, p.WorkspaceID, s.Name, s.Priority,
					s.FirstResponseMinutes, s.ResolutionMinutes, s.Enabled, s.Position, s.BusinessHours, s.TeamID); err != nil {
					return err
				}
				return settingsChanged(ctx, tx, p, "sla_policy", "created", s.ID, map[string]any{"sla_policy": s}, slaAudit(s))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body SlaPolicy }{s}, nil
		})

	huma.Register(api, op("get-sla-policy", http.MethodGet, "/v1/sla-policies/{id}", "Get an SLA policy", "SLA policies", 0),
		func(ctx context.Context, in *idPath) (*struct{ Body SlaPolicy }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "SLA policy")
			if err != nil {
				return nil, err
			}
			s, err := scanSLA(d.DB.Pool.QueryRow(ctx, slaSelect+` WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id))
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("SLA policy")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body SlaPolicy }{s}, nil
		})

	huma.Register(api, op("update-sla-policy", http.MethodPatch, "/v1/sla-policies/{id}", "Update an SLA policy", "SLA policies", 0),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body slaPolicyWriteBody
		}) (*struct{ Body SlaPolicy }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "SLA policy")
			if err != nil {
				return nil, err
			}
			var s SlaPolicy
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var err error
				s, err = scanSLA(tx.QueryRow(ctx, slaSelect+` WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, p.WorkspaceID, id))
				if db.IsNotFound(err) {
					return httpx.NotFound("SLA policy")
				}
				if err != nil {
					return err
				}
				if err := in.Body.apply(ctx, tx, p.WorkspaceID, &s); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE sla_policies SET name = $3, priority = $4, first_response_minutes = $5,
					resolution_minutes = $6, enabled = $7, position = $8, business_hours = $9, team_id = $10, updated_at = now()
					WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, id, s.Name, s.Priority, s.FirstResponseMinutes, s.ResolutionMinutes, s.Enabled, s.Position, s.BusinessHours, s.TeamID); err != nil {
					return err
				}
				return settingsChanged(ctx, tx, p, "sla_policy", "updated", s.ID, map[string]any{"sla_policy": s}, slaAudit(s))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body SlaPolicy }{s}, nil
		})

	huma.Register(api, op("delete-sla-policy", http.MethodDelete, "/v1/sla-policies/{id}", "Delete an SLA policy", "SLA policies", http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			return nil, deleteSetting(ctx, d, in.ID, "sla_policies", "sla_policy", "SLA policy")
		})
}

func slaAudit(s SlaPolicy) map[string]any {
	return map[string]any{"name": s.Name, "priority": s.Priority, "first_response_minutes": s.FirstResponseMinutes,
		"resolution_minutes": s.ResolutionMinutes, "enabled": s.Enabled, "team_id": s.TeamID}
}

// settingsChanged publishes <kind>.<verb> and writes the audit entry in tx.
func settingsChanged(ctx context.Context, tx pgx.Tx, p *auth.Principal, kind, verb string, id uuid.UUID, data any, auditData map[string]any) error {
	if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: kind + "." + verb, Data: data}); err != nil {
		return err
	}
	return audit.Log(ctx, tx, p, p.WorkspaceID, kind+"."+verb, kind, &id, auditData)
}

// deleteSetting removes an admin-managed row (table is a trusted constant).
func deleteSetting(ctx context.Context, d *deps.Deps, rawID, table, kind, label string) error {
	p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
	if err != nil {
		return httpx.Wrap(err)
	}
	id, err := hd.ParseID(rawID, label)
	if err != nil {
		return err
	}
	err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var name string
		err := tx.QueryRow(ctx, `DELETE FROM `+table+` WHERE workspace_id = $1 AND id = $2 RETURNING name`, p.WorkspaceID, id).Scan(&name)
		if db.IsNotFound(err) {
			return httpx.NotFound(label)
		}
		if err != nil {
			return err
		}
		return settingsChanged(ctx, tx, p, kind, "deleted", id, map[string]any{kind + "_id": id}, map[string]any{"name": name})
	})
	return httpx.Wrap(err)
}

func registerAutomations(d *deps.Deps, api huma.API) {
	huma.Register(api, op("list-automations", http.MethodGet, "/v1/automations", "List automations", "Automations", 0),
		func(ctx context.Context, in *struct {
			Trigger string `query:"trigger" doc:"Only automations for this trigger"`
		}) (*struct{ Body httpx.Page[Automation] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			rows, err := d.DB.Pool.Query(ctx, automationSelect+` WHERE workspace_id = $1 AND ($2 = '' OR trigger = $2)
				ORDER BY position, created_at, id`, p.WorkspaceID, in.Trigger)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Automation, error) { return scanAutomation(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[Automation] }{list(out)}, nil
		})

	huma.Register(api, op("create-automation", http.MethodPost, "/v1/automations", "Create an automation", "Automations", http.StatusCreated),
		func(ctx context.Context, in *struct{ Body automationWriteBody }) (*struct{ Body Automation }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a := Automation{ID: ids.New(), Enabled: true, Position: -1}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := in.Body.apply(ctx, tx, p.WorkspaceID, &a); err != nil {
					return err
				}
				if a.Position < 0 {
					if err := tx.QueryRow(ctx, `SELECT COALESCE(max(position) + 1, 0) FROM automations WHERE workspace_id = $1`, p.WorkspaceID).Scan(&a.Position); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `INSERT INTO automations (id, workspace_id, name, trigger, conditions, actions, enabled, position)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, a.ID, p.WorkspaceID, a.Name, a.Trigger, a.Conditions, a.Actions, a.Enabled, a.Position); err != nil {
					return err
				}
				return settingsChanged(ctx, tx, p, "automation", "created", a.ID, map[string]any{"automation": a}, automationAudit(a))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Automation }{a}, nil
		})

	huma.Register(api, op("get-automation", http.MethodGet, "/v1/automations/{id}", "Get an automation", "Automations", 0),
		func(ctx context.Context, in *idPath) (*struct{ Body Automation }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Automation")
			if err != nil {
				return nil, err
			}
			a, err := scanAutomation(d.DB.Pool.QueryRow(ctx, automationSelect+` WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id))
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Automation")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Automation }{a}, nil
		})

	huma.Register(api, op("update-automation", http.MethodPatch, "/v1/automations/{id}", "Update an automation", "Automations", 0),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body automationWriteBody
		}) (*struct{ Body Automation }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Automation")
			if err != nil {
				return nil, err
			}
			var a Automation
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var err error
				a, err = scanAutomation(tx.QueryRow(ctx, automationSelect+` WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, p.WorkspaceID, id))
				if db.IsNotFound(err) {
					return httpx.NotFound("Automation")
				}
				if err != nil {
					return err
				}
				if err := in.Body.apply(ctx, tx, p.WorkspaceID, &a); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE automations SET name = $3, trigger = $4, conditions = $5, actions = $6, enabled = $7,
					position = $8, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, id, a.Name, a.Trigger, a.Conditions, a.Actions, a.Enabled, a.Position); err != nil {
					return err
				}
				return settingsChanged(ctx, tx, p, "automation", "updated", a.ID, map[string]any{"automation": a}, automationAudit(a))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Automation }{a}, nil
		})

	huma.Register(api, op("delete-automation", http.MethodDelete, "/v1/automations/{id}", "Delete an automation", "Automations", http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			return nil, deleteSetting(ctx, d, in.ID, "automations", "automation", "Automation")
		})
}

// automationAudit records the shape of a change, not reply bodies.
func automationAudit(a Automation) map[string]any {
	types := make([]string, 0, len(a.Actions))
	for _, x := range a.Actions {
		types = append(types, x.Type)
	}
	return map[string]any{"name": a.Name, "trigger": a.Trigger, "enabled": a.Enabled,
		"condition_count": len(a.Conditions), "action_types": types}
}
