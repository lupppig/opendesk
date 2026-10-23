package routing

import (
	"context"
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

const tag = "Routing"

var errLimit = httpx.E(http.StatusUnprocessableEntity, "limit_reached", "A workspace can have at most 200 routing rules.")

type routingRuleBody struct {
	Name       *string             `json:"name,omitempty" maxLength:"200"`
	Enabled    *bool               `json:"enabled,omitempty"`
	Match      *string             `json:"match,omitempty" enum:"all,any"`
	Conditions *[]RoutingCondition `json:"conditions,omitempty" maxItems:"20"`
	TeamID     *string             `json:"team_id,omitempty" doc:"The team matching tickets go to"`
}

// apply merges b into r and validates the result against the workspace.
func (b routingRuleBody) apply(ctx context.Context, q db.Querier, wid uuid.UUID, r *RoutingRule) error {
	if b.Name != nil {
		n, err := cleanName(*b.Name)
		if err != nil {
			return err
		}
		r.Name = n
	}
	if r.Name == "" {
		return httpx.Validation("name is required")
	}
	if b.Enabled != nil {
		r.Enabled = *b.Enabled
	}
	if b.Match != nil {
		if *b.Match != MatchAll && *b.Match != MatchAny {
			return httpx.Validation(`match must be "all" or "any"`)
		}
		r.Match = *b.Match
	}
	if b.Conditions != nil {
		cs, err := ValidateConditions(ctx, q, wid, *b.Conditions)
		if err != nil {
			return err
		}
		r.Conditions = cs
	} else if len(r.Conditions) == 0 {
		return httpx.Validation("a routing rule needs at least one condition")
	}
	if b.TeamID != nil {
		id, err := uuid.Parse(strings.TrimSpace(*b.TeamID))
		if err != nil {
			return httpx.Validation("team_id is not a valid id")
		}
		r.TeamID = id
	}
	if r.TeamID == uuid.Nil {
		return httpx.Validation("team_id is required")
	}
	if b.TeamID != nil {
		return checkRow(ctx, q, wid, "teams", r.TeamID, "team")
	}
	return nil
}

// changed publishes routing_rule.<verb> and audit-logs it.
func changed(ctx context.Context, tx pgx.Tx, p *auth.Principal, verb string, r RoutingRule) error {
	if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "routing_rule." + verb,
		Data: map[string]any{"routing_rule": r}}); err != nil {
		return err
	}
	return audit.Log(ctx, tx, p, p.WorkspaceID, "routing_rule."+verb, "routing_rule", &r.ID, map[string]any{
		"name": r.Name, "enabled": r.Enabled, "match": r.Match, "team_id": r.TeamID, "condition_count": len(r.Conditions)})
}

// Register adds the routing rule routes. They are workspace settings: admins, and API keys
// with settings:read or settings:write.
func Register(d *deps.Deps, api huma.API) {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{tag}, DefaultStatus: status}
	}

	huma.Register(api, op("list-routing-rules", http.MethodGet, "/v1/routing-rules", "List routing rules", 0),
		func(ctx context.Context, _ *struct{}) (*struct{ Body httpx.Page[RoutingRule] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			rules, err := loadRules(ctx, d.DB.Pool, p.WorkspaceID, false)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if rules == nil {
				rules = []RoutingRule{}
			}
			return &struct{ Body httpx.Page[RoutingRule] }{httpx.Page[RoutingRule]{Data: rules}}, nil
		})

	huma.Register(api, op("create-routing-rule", http.MethodPost, "/v1/routing-rules", "Create a routing rule", http.StatusCreated),
		func(ctx context.Context, in *struct{ Body routingRuleBody }) (*struct{ Body RoutingRule }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			r := RoutingRule{ID: ids.New(), Enabled: true, Match: MatchAll}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				// One writer at a time per workspace keeps the limit and positions exact.
				if _, err := tx.Exec(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR UPDATE`, p.WorkspaceID); err != nil {
					return err
				}
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(max(position) + 1, 0) FROM routing_rules WHERE workspace_id = $1`,
					p.WorkspaceID).Scan(&n, &r.Position); err != nil {
					return err
				}
				if n >= MaxRules {
					return errLimit
				}
				if err := in.Body.apply(ctx, tx, p.WorkspaceID, &r); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `INSERT INTO routing_rules (id, workspace_id, name, conditions, match, team_id, enabled, position)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at, updated_at`,
					r.ID, p.WorkspaceID, r.Name, r.Conditions, r.Match, r.TeamID, r.Enabled, r.Position).Scan(&r.CreatedAt, &r.UpdatedAt); err != nil {
					return err
				}
				return changed(ctx, tx, p, "created", r)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body RoutingRule }{r}, nil
		})

	huma.Register(api, op("get-routing-rule", http.MethodGet, "/v1/routing-rules/{id}", "Get a routing rule", 0),
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body RoutingRule }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Routing rule")
			if err != nil {
				return nil, err
			}
			r, err := loadRule(ctx, d.DB.Pool, p.WorkspaceID, id, false)
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Routing rule")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body RoutingRule }{r}, nil
		})

	huma.Register(api, op("update-routing-rule", http.MethodPatch, "/v1/routing-rules/{id}", "Update a routing rule", 0),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body routingRuleBody
		}) (*struct{ Body RoutingRule }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Routing rule")
			if err != nil {
				return nil, err
			}
			var r RoutingRule
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var err error
				r, err = loadRule(ctx, tx, p.WorkspaceID, id, true)
				if db.IsNotFound(err) {
					return httpx.NotFound("Routing rule")
				}
				if err != nil {
					return err
				}
				if err := in.Body.apply(ctx, tx, p.WorkspaceID, &r); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `UPDATE routing_rules SET name = $3, conditions = $4, match = $5, team_id = $6, enabled = $7,
					updated_at = now() WHERE workspace_id = $1 AND id = $2 RETURNING updated_at`,
					p.WorkspaceID, id, r.Name, r.Conditions, r.Match, r.TeamID, r.Enabled).Scan(&r.UpdatedAt); err != nil {
					return err
				}
				return changed(ctx, tx, p, "updated", r)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body RoutingRule }{r}, nil
		})

	huma.Register(api, op("delete-routing-rule", http.MethodDelete, "/v1/routing-rules/{id}", "Delete a routing rule", http.StatusNoContent),
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Routing rule")
			if err != nil {
				return nil, err
			}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var name string
				err := tx.QueryRow(ctx, `DELETE FROM routing_rules WHERE workspace_id = $1 AND id = $2 RETURNING name`, p.WorkspaceID, id).Scan(&name)
				if db.IsNotFound(err) {
					return httpx.NotFound("Routing rule")
				}
				if err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "routing_rule.deleted",
					Data: map[string]any{"routing_rule_id": id}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "routing_rule.deleted", "routing_rule", &id, map[string]any{"name": name})
			})
			return nil, httpx.Wrap(err)
		})

	huma.Register(api, op("reorder-routing-rules", http.MethodPost, "/v1/routing-rules/reorder", "Reorder routing rules", 0),
		func(ctx context.Context, in *struct {
			Body struct {
				IDs []string `json:"ids" maxItems:"200" doc:"Every rule of the workspace, in the order they are tried"`
			}
		}) (*struct{ Body httpx.Page[RoutingRule] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var out []RoutingRule
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				list, err := reorder(ctx, tx, p, in.Body.IDs)
				out = list
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if out == nil {
				out = []RoutingRule{}
			}
			return &struct{ Body httpx.Page[RoutingRule] }{httpx.Page[RoutingRule]{Data: out}}, nil
		})

	huma.Register(api, op("test-routing", http.MethodPost, "/v1/routing/test", "Test routing",
		0), func(ctx context.Context, in *struct{ Body routingTestBody }) (*struct{ Body RoutingTestResult }, error) {
		p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
		if err != nil {
			return nil, httpx.Wrap(err)
		}
		out, err := runTest(ctx, d.DB.Pool, p.WorkspaceID, strings.ToLower(strings.TrimSpace(d.Cfg.InboundDomain)), in.Body)
		if err != nil {
			return nil, httpx.Wrap(err)
		}
		return &struct{ Body RoutingTestResult }{out}, nil
	})
}

// reorder sets the positions of every rule of the workspace to the order of raw.
func reorder(ctx context.Context, tx pgx.Tx, p *auth.Principal, raw []string) ([]RoutingRule, error) {
	order := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, httpx.Validation("ids must be routing rule ids")
		}
		if slices.Contains(order, id) {
			return nil, httpx.Validation("ids must not repeat")
		}
		order = append(order, id)
	}
	rows, err := tx.Query(ctx, ruleSelect+` WHERE workspace_id = $1 ORDER BY position, created_at, id FOR UPDATE`, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	rules, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RoutingRule, error) { return scanRule(r) })
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]RoutingRule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}
	if len(order) != len(rules) {
		return nil, httpx.Validation("ids must list every routing rule of the workspace once")
	}
	out := make([]RoutingRule, 0, len(order))
	moved := 0
	for i, id := range order {
		r, ok := byID[id]
		if !ok {
			return nil, httpx.Validation("ids must list every routing rule of the workspace once")
		}
		if r.Position != i {
			if err := tx.QueryRow(ctx, `UPDATE routing_rules SET position = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2
				RETURNING updated_at`, p.WorkspaceID, id, i).Scan(&r.UpdatedAt); err != nil {
				return nil, err
			}
			r.Position = i
			moved++
			if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "routing_rule.updated",
				Data: map[string]any{"routing_rule": r}}); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	if moved > 0 {
		if err := audit.Log(ctx, tx, p, p.WorkspaceID, "routing_rule.reordered", "routing_rule", nil, map[string]any{"moved": moved}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type routingTestBody struct {
	Channel  string `json:"channel" enum:"email,chat"`
	To       string `json:"to,omitempty" maxLength:"320" doc:"email: the address written to (a mailbox address, alias or forwarding address)"`
	From     string `json:"from,omitempty" maxLength:"320"`
	Subject  string `json:"subject,omitempty" maxLength:"500"`
	Body     string `json:"body,omitempty" maxLength:"20000"`
	WidgetID string `json:"widget_id,omitempty" doc:"chat: the widget"`
	Topic    string `json:"topic,omitempty" maxLength:"100" doc:"chat: the topic id the visitor picked"`
	PageURL  string `json:"page_url,omitempty" maxLength:"2000"`
}

// RoutingTestResult is where a message like the test body would go.
type RoutingTestResult struct {
	TeamID     *uuid.UUID           `json:"team_id"`
	BrandID    uuid.UUID            `json:"brand_id"`
	MailboxID  *uuid.UUID           `json:"mailbox_id"`
	Routed     *domain.TicketRouted `json:"routed"`
	Steps      []Step               `json:"steps"`
	AssignMode string               `json:"assign_mode" enum:"none,round_robin,load_balanced"`
}

// runTest resolves the test message without writing anything.
func runTest(ctx context.Context, q db.Querier, wid uuid.UUID, inboundDomain string, b routingTestBody) (RoutingTestResult, error) {
	var out RoutingTestResult
	in := Input{WorkspaceID: wid, Channel: b.Channel, FromEmail: strings.ToLower(strings.TrimSpace(b.From)),
		Subject: b.Subject, Body: b.Body, PageURL: strings.TrimSpace(b.PageURL), Topic: strings.TrimSpace(b.Topic)}
	switch b.Channel {
	case domain.ChannelEmail:
		to := strings.ToLower(strings.Trim(strings.TrimSpace(b.To), "<>"))
		in.ReceivedAt = to
		if to != "" {
			// The address itself or an alias; else the forwarding address of a mailbox, which
			// stands for the mailbox's own address.
			token := ""
			if local, dom, ok := strings.Cut(to, "@"); ok && inboundDomain != "" && dom == inboundDomain {
				token, _, _ = strings.Cut(local, "+")
			}
			var id uuid.UUID
			var email string
			var direct bool
			err := q.QueryRow(ctx, `SELECT id, lower(email::text), lower(email::text) = $2 OR $2 = ANY(SELECT lower(a) FROM unnest(aliases) a)
				FROM mailboxes WHERE workspace_id = $1
				AND (lower(email::text) = $2 OR $2 = ANY(SELECT lower(a) FROM unnest(aliases) a) OR ($3 <> '' AND forwarding_token = $3))
				ORDER BY 3 DESC, created_at LIMIT 1`, wid, to, token).Scan(&id, &email, &direct)
			if err != nil && !db.IsNotFound(err) {
				return out, err
			}
			if err == nil {
				in.MailboxID = &id
				if !direct {
					in.ReceivedAt = email
				}
			}
		}
	case domain.ChannelChat:
		if b.WidgetID != "" {
			id, err := uuid.Parse(strings.TrimSpace(b.WidgetID))
			if err != nil {
				return out, httpx.Validation("widget_id is not a valid id")
			}
			if err := checkRow(ctx, q, wid, "widgets", id, "widget"); err != nil {
				return out, err
			}
			in.WidgetID = &id
		}
	default:
		return out, httpx.Validation(`channel must be "email" or "chat"`)
	}
	var aliasBrand *uuid.UUID
	if in.MailboxID != nil && in.ReceivedAt != "" {
		b, err := domain.AddressBrand(ctx, q, wid, *in.MailboxID, in.ReceivedAt)
		if err != nil {
			return out, err
		}
		aliasBrand = b
	}
	brand, err := domain.ResolveBrand(ctx, q, wid, aliasBrand, in.MailboxID, in.WidgetID)
	if err != nil {
		return out, err
	}
	in.BrandID = brand
	if in.FromEmail != "" {
		var cid uuid.UUID
		err := q.QueryRow(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND email = $2`, wid, in.FromEmail).Scan(&cid)
		if err != nil && !db.IsNotFound(err) {
			return out, err
		}
		if err == nil {
			in.ContactID = &cid
		}
	}
	res, err := Resolve(ctx, q, in)
	if err != nil {
		return out, err
	}
	out.TeamID, out.BrandID, out.MailboxID, out.Routed, out.Steps = res.TeamID, brand, in.MailboxID, res.Routed, res.Steps
	if out.Steps == nil {
		out.Steps = []Step{}
	}
	out.AssignMode, err = assignMode(ctx, q, wid, res.TeamID, in.MailboxID)
	return out, err
}

// assignMode is the assignment method a ticket with this team and address gets: the
// team's rule, else the address's, else the workspace default (contracts/api.md "Assignment").
func assignMode(ctx context.Context, q db.Querier, wid uuid.UUID, team, mailbox *uuid.UUID) (string, error) {
	if team != nil || mailbox != nil {
		var mode string
		err := q.QueryRow(ctx, `SELECT mode FROM assignment_rules WHERE workspace_id = $1 AND (team_id = $2 OR mailbox_id = $3)
			ORDER BY (team_id IS NULL) LIMIT 1`, wid, team, mailbox).Scan(&mode)
		if err == nil {
			return mode, nil
		}
		if !db.IsNotFound(err) {
			return "", err
		}
	}
	s, err := hd.LoadSettings(ctx, q, wid)
	if err != nil {
		return "", err
	}
	if s.AutoAssign == "round_robin" || s.AutoAssign == "load_balanced" {
		return s.AutoAssign, nil
	}
	return "none", nil
}
