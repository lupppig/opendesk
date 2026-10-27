package resources

import (
	"context"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
)

var assignModes = []string{"none", "round_robin", "load_balanced"}

// AssignmentRule is the contract shape.
type AssignmentRule struct {
	TargetType string    `json:"target_type" enum:"mailbox,team"`
	TargetID   uuid.UUID `json:"target_id"`
	Mode       string    `json:"mode" enum:"none,round_robin,load_balanced"`
}

// Assignment is GET /v1/assignment.
type Assignment struct {
	Mode            string           `json:"mode" enum:"none,round_robin,load_balanced" doc:"Workspace default (settings.auto_assign)"`
	Rules           []AssignmentRule `json:"rules"`
	AutoAwayMinutes int              `json:"auto_away_minutes" doc:"0 = off"`
	NewTicketNotify string           `json:"new_ticket_notify" enum:"auto,everyone,leads,off" doc:"Who is notified of a new ticket nobody is assigned to (settings.new_ticket_notify)"`
}

type assignmentPatch struct {
	Mode            *string           `json:"mode,omitempty" enum:"none,round_robin,load_balanced"`
	Rules           *[]AssignmentRule `json:"rules,omitempty" maxItems:"200" doc:"Replaces every rule"`
	AutoAwayMinutes *int              `json:"auto_away_minutes,omitempty" minimum:"0" maximum:"1440" doc:"0 or 10..1440"`
	NewTicketNotify *string           `json:"new_ticket_notify,omitempty" enum:"auto,everyone,leads,off"`
}

// TriageSettings is GET /v1/triage.
type TriageSettings struct {
	AutoTriage        bool              `json:"auto_triage"`
	Fields            []string          `json:"fields" enum:"priority,severity,labels,team"`
	AssignAfterTriage bool              `json:"assign_after_triage"`
	Threshold         float64           `json:"threshold" doc:"Confidence at or above which a decision is applied; below it, a suggestion"`
	Engine            hd.DecisionEngine `json:"engine"`
	Available         bool              `json:"available" doc:"Triage can run: AI or a decision engine is configured, and AI is on for the workspace"`
}

type triagePatch struct {
	AutoTriage        *bool     `json:"auto_triage,omitempty"`
	Fields            *[]string `json:"fields,omitempty" maxItems:"4" enum:"priority,severity,labels,team"`
	AssignAfterTriage *bool     `json:"assign_after_triage,omitempty"`
	Threshold         *float64  `json:"threshold,omitempty" minimum:"0.5" maximum:"0.99"`
}

// BusinessHoursBody is the contract shape of /v1/business-hours.
type BusinessHoursBody struct {
	Enabled  bool                      `json:"enabled"`
	Timezone string                    `json:"timezone" doc:"IANA time zone (the workspace timezone)"`
	Schedule map[string][]hd.TimeRange `json:"schedule" doc:"mon..sun, up to 3 ranges each"`
	Holidays []hd.Holiday              `json:"holidays" maxItems:"100"`
}

func loadAssignment(ctx context.Context, q db.Querier, wid uuid.UUID) (Assignment, error) {
	st, err := hd.LoadSettings(ctx, q, wid)
	if err != nil {
		return Assignment{}, err
	}
	a := Assignment{Mode: st.AutoAssign, AutoAwayMinutes: st.AutoAwayMinutes, NewTicketNotify: st.NewTicketNotify(), Rules: []AssignmentRule{}}
	if !slices.Contains(assignModes, a.Mode) {
		a.Mode = "none"
	}
	rows, err := q.Query(ctx, `SELECT CASE WHEN mailbox_id IS NULL THEN 'team' ELSE 'mailbox' END, COALESCE(mailbox_id, team_id), mode
		FROM assignment_rules WHERE workspace_id = $1 ORDER BY created_at, COALESCE(mailbox_id, team_id)`, wid)
	if err != nil {
		return a, err
	}
	rules, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AssignmentRule, error) {
		var x AssignmentRule
		return x, r.Scan(&x.TargetType, &x.TargetID, &x.Mode)
	})
	if err != nil {
		return a, err
	}
	if rules != nil {
		a.Rules = rules
	}
	return a, nil
}

func loadTriage(ctx context.Context, d *deps.Deps, q db.Querier, wid uuid.UUID) (TriageSettings, error) {
	st, err := hd.LoadSettings(ctx, q, wid)
	if err != nil {
		return TriageSettings{}, err
	}
	tc := st.Triage()
	engine := hd.WorkspaceEngine(ctx, d.Cfg, wid)
	return TriageSettings{AutoTriage: tc.AutoTriage, Fields: tc.Fields, AssignAfterTriage: tc.AssignAfterTriage,
		Threshold: tc.Threshold, Engine: engine,
		Available: (d.Cfg.AIEnabled() || engine.Provider == "typesafe") && st.AI().Enabled}, nil
}

func loadHours(ctx context.Context, q db.Querier, wid uuid.UUID) (BusinessHoursBody, error) {
	st, err := hd.LoadSettings(ctx, q, wid)
	if err != nil {
		return BusinessHoursBody{}, err
	}
	b := st.Hours()
	tz := st.Timezone
	if tz == "" {
		tz = "UTC"
	}
	return BusinessHoursBody{Enabled: b.Enabled, Timezone: tz, Schedule: b.Schedule, Holidays: b.Holidays}, nil
}

func registerRouting(d *deps.Deps, api huma.API) {
	huma.Register(api, op("get-assignment", http.MethodGet, "/v1/assignment", "Get auto-assignment settings", "Assignment", 0),
		func(ctx context.Context, _ *struct{}) (*struct{ Body Assignment }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, err := loadAssignment(ctx, d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Assignment }{a}, nil
		})

	huma.Register(api, op("update-assignment", http.MethodPatch, "/v1/assignment", "Update auto-assignment settings", "Assignment", 0),
		func(ctx context.Context, in *struct{ Body assignmentPatch }) (*struct{ Body Assignment }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b := in.Body
			patch := map[string]any{}
			if b.Mode != nil {
				if !slices.Contains(assignModes, *b.Mode) {
					return nil, httpx.Validation("mode must be none, round_robin or load_balanced.")
				}
				patch["auto_assign"] = *b.Mode
			}
			if b.AutoAwayMinutes != nil {
				if m := *b.AutoAwayMinutes; m != 0 && (m < 10 || m > 1440) {
					return nil, httpx.Validation("auto_away_minutes must be 0 (off) or between 10 and 1440.")
				}
				patch["auto_away_minutes"] = *b.AutoAwayMinutes
			}
			if b.NewTicketNotify != nil {
				if !slices.Contains(hd.NewTicketNotifyModes, *b.NewTicketNotify) {
					return nil, httpx.Validation("new_ticket_notify must be auto, everyone, leads or off.")
				}
				patch["new_ticket_notify"] = *b.NewTicketNotify
			}
			var a Assignment
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if len(patch) > 0 {
					if _, err := tx.Exec(ctx, `UPDATE workspaces SET settings = settings || $2::jsonb, updated_at = now() WHERE id = $1`,
						p.WorkspaceID, patch); err != nil {
						return err
					}
				}
				if b.Rules != nil {
					if err := replaceRules(ctx, tx, p.WorkspaceID, *b.Rules); err != nil {
						return err
					}
				}
				var err error
				if a, err = loadAssignment(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "assignment.updated", "workspace", &p.WorkspaceID,
					map[string]any{"mode": a.Mode, "rules": len(a.Rules), "auto_away_minutes": a.AutoAwayMinutes,
						"new_ticket_notify": a.NewTicketNotify})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Assignment }{a}, nil
		})

	huma.Register(api, op("get-triage", http.MethodGet, "/v1/triage", "Get triage settings", "Triage", 0),
		func(ctx context.Context, _ *struct{}) (*struct{ Body TriageSettings }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			t, err := loadTriage(ctx, d, d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body TriageSettings }{t}, nil
		})

	huma.Register(api, op("update-triage", http.MethodPatch, "/v1/triage", "Update triage settings", "Triage", 0),
		func(ctx context.Context, in *struct{ Body triagePatch }) (*struct{ Body TriageSettings }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			patch := map[string]any{}
			if v := in.Body.AutoTriage; v != nil {
				patch["auto_triage"] = *v
			}
			if v := in.Body.AssignAfterTriage; v != nil {
				patch["assign_after_triage"] = *v
			}
			if v := in.Body.Threshold; v != nil {
				if *v < hd.MinTriageThreshold || *v > hd.MaxTriageThreshold {
					return nil, httpx.Validation("threshold must be between 0.5 and 0.99.")
				}
				patch["triage_threshold"] = math.Round(*v*100) / 100
			}
			if v := in.Body.Fields; v != nil {
				fields := []string{}
				for _, f := range *v {
					if !slices.Contains(hd.TriageFields, f) {
						return nil, httpx.Validation("fields may hold priority, severity, labels and team.")
					}
					if !slices.Contains(fields, f) {
						fields = append(fields, f)
					}
				}
				patch["triage_fields"] = fields
			}
			var t TriageSettings
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if len(patch) > 0 {
					if _, err := tx.Exec(ctx, `UPDATE workspaces SET settings = jsonb_set(settings, '{ai}',
						COALESCE(settings -> 'ai', '{}'::jsonb) || $2::jsonb), updated_at = now() WHERE id = $1`, p.WorkspaceID, patch); err != nil {
						return err
					}
				}
				var err error
				if t, err = loadTriage(ctx, d, tx, p.WorkspaceID); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "triage.updated", "workspace", &p.WorkspaceID,
					map[string]any{"auto_triage": t.AutoTriage, "fields": t.Fields, "assign_after_triage": t.AssignAfterTriage, "threshold": t.Threshold})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body TriageSettings }{t}, nil
		})

	huma.Register(api, op("get-business-hours", http.MethodGet, "/v1/business-hours", "Get business hours", "Business hours", 0),
		func(ctx context.Context, _ *struct{}) (*struct{ Body BusinessHoursBody }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b, err := loadHours(ctx, d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body BusinessHoursBody }{b}, nil
		})

	huma.Register(api, op("update-business-hours", http.MethodPut, "/v1/business-hours", "Set business hours", "Business hours", 0),
		func(ctx context.Context, in *struct{ Body BusinessHoursBody }) (*struct{ Body BusinessHoursBody }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if _, err := time.LoadLocation(in.Body.Timezone); err != nil || in.Body.Timezone == "" || in.Body.Timezone == "Local" {
				return nil, httpx.Validation("timezone must be an IANA time zone like Europe/Berlin.")
			}
			bh, err := hd.ValidateBusinessHours(hd.BusinessHours{Enabled: in.Body.Enabled, Schedule: in.Body.Schedule, Holidays: in.Body.Holidays})
			if err != nil {
				return nil, err
			}
			var out BusinessHoursBody
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE workspaces SET settings = settings || jsonb_build_object('business_hours', $2::jsonb, 'timezone', $3::text),
					updated_at = now() WHERE id = $1`, p.WorkspaceID, bh, in.Body.Timezone); err != nil {
					return err
				}
				var err error
				if out, err = loadHours(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "business_hours.updated", "workspace", &p.WorkspaceID,
					map[string]any{"enabled": out.Enabled, "timezone": out.Timezone, "holidays": len(out.Holidays)})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body BusinessHoursBody }{out}, nil
		})
}

// replaceRules swaps the assignment rules, checking every target belongs to the workspace.
func replaceRules(ctx context.Context, tx pgx.Tx, wid uuid.UUID, rules []AssignmentRule) error {
	if len(rules) > 200 {
		return httpx.Validation("At most 200 assignment rules.")
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if r.TargetType != "mailbox" && r.TargetType != "team" {
			return httpx.Validation("target_type must be mailbox or team.")
		}
		if !slices.Contains(assignModes, r.Mode) {
			return httpx.Validation("mode must be none, round_robin or load_balanced.")
		}
		k := r.TargetType + ":" + r.TargetID.String()
		if seen[k] {
			return httpx.Validation("Each mailbox and team takes one rule.")
		}
		seen[k] = true
		if err := hd.CheckRef(ctx, tx, wid, r.TargetType, r.TargetID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM assignment_rules WHERE workspace_id = $1`, wid); err != nil {
		return err
	}
	for _, r := range rules {
		var mb, tm *uuid.UUID
		id := r.TargetID
		if r.TargetType == "mailbox" {
			mb = &id
		} else {
			tm = &id
		}
		if _, err := tx.Exec(ctx, `INSERT INTO assignment_rules (workspace_id, mailbox_id, team_id, mode) VALUES ($1, $2, $3, $4)`,
			wid, mb, tm, r.Mode); err != nil {
			return err
		}
	}
	return nil
}
