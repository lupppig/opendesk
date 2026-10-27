// Package routing picks the team of a new ticket (contracts/teams.md "Routing"): the
// address's or widget's team, then the first matching routing rule.
package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
)

// Input describes the first message of a ticket that is about to be created.
type Input struct {
	WorkspaceID uuid.UUID
	Channel     string // email | chat | api
	BrandID     uuid.UUID
	MailboxID   *uuid.UUID // the address it arrived at
	ReceivedAt  string     // the exact address written to
	WidgetID    *uuid.UUID
	Topic       string // the widget topic id the visitor picked
	FromEmail   string
	Subject     string
	Body        string // plain text
	PageURL     string
	ContactID   *uuid.UUID
	// TeamID is the team the caller asked for (API); it wins over the address's team.
	TeamID *uuid.UUID
}

// Result is the team the ticket goes to and why. TeamID nil means no team.
type Result struct {
	TeamID *uuid.UUID
	Routed *domain.TicketRouted
	// Steps lists the rules that were tried, for POST /v1/routing/test.
	Steps []Step
}

// Step is one rule tried by Resolve.
type Step = RoutingStep

type RoutingStep struct {
	RuleID  uuid.UUID `json:"rule_id"`
	Name    string    `json:"name"`
	Matched any       `json:"matched"` // true, false or "unknown"
}

// Unknown is Step.Matched for a rule whose outcome depends on an ai_decide condition.
const Unknown = "unknown"

// Via values of domain.TicketRouted set here.
const (
	ViaAddress = "address"
	ViaWidget  = "widget"
	ViaTopic   = "topic"
	ViaRule    = "rule"
	ViaAPI     = "api"
)

// Resolve applies step 1 (address, widget, topic, API) and step 2 (routing rules). It reads
// only; the caller stores the result with domain.CreateTicket and counts the rule's run.
//
// ai_decide conditions need a model call about a stored ticket, so they are not answered
// here: a rule that holds only if one of them does is not matched, and its step reads
// "unknown". Automations answer ai_decide once the ticket exists.
func Resolve(ctx context.Context, q db.Querier, in Input) (Result, error) {
	var res Result
	team, via, topicLabel, err := firstStep(ctx, q, in)
	if err != nil {
		return res, err
	}
	if team != nil {
		res.TeamID = team
		res.Routed = &domain.TicketRouted{Via: via}
		if via == ViaTopic {
			res.Routed.TopicID, res.Routed.TopicLabel = in.Topic, topicLabel
		}
	}
	rules, err := loadRules(ctx, q, in.WorkspaceID, true)
	if err != nil {
		return res, err
	}
	if len(rules) == 0 {
		return res, nil
	}
	f := &facts{in: in}
	for _, r := range rules {
		m, err := f.rule(ctx, q, r)
		if err != nil {
			return res, err
		}
		step := Step{RuleID: r.ID, Name: r.Name, Matched: m == yes}
		if m == unknown {
			step.Matched = Unknown
		}
		res.Steps = append(res.Steps, step)
		if m == yes {
			id, ruleID := r.TeamID, r.ID
			res.TeamID = &id
			res.Routed = &domain.TicketRouted{Via: ViaRule, RuleID: &ruleID, RuleName: r.Name}
			break
		}
	}
	return res, nil
}

// Count records a match of the rule Resolve returned in Result.Routed.RuleID, in the
// transaction that creates the ticket.
func Count(ctx context.Context, q db.Querier, ruleID uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE routing_rules SET run_count = run_count + 1 WHERE id = $1`, ruleID)
	return err
}

// RuleID returns the matched rule of a result, or nil when no rule decided the team.
func (r Result) RuleID() *uuid.UUID {
	if r.Routed == nil || r.Routed.Via != ViaRule {
		return nil
	}
	return r.Routed.RuleID
}

// firstStep is the address's, topic's, widget's or caller's team, and the topic's label
// when the topic decided it.
func firstStep(ctx context.Context, q db.Querier, in Input) (*uuid.UUID, string, string, error) {
	switch in.Channel {
	case domain.ChannelChat:
		if in.WidgetID == nil {
			return nil, "", "", nil
		}
		var widgetTeam *uuid.UUID
		var topicsRaw []byte
		err := q.QueryRow(ctx, `SELECT team_id, COALESCE(settings->'topics', '[]') FROM widgets WHERE workspace_id = $1 AND id = $2`,
			in.WorkspaceID, *in.WidgetID).Scan(&widgetTeam, &topicsRaw)
		if db.IsNotFound(err) {
			return nil, "", "", nil
		}
		if err != nil {
			return nil, "", "", fmt.Errorf("load widget: %w", err)
		}
		if in.Topic != "" {
			var topics []struct {
				ID     string  `json:"id"`
				Label  string  `json:"label"`
				TeamID *string `json:"team_id"`
			}
			_ = json.Unmarshal(topicsRaw, &topics)
			for _, t := range topics {
				if t.ID != in.Topic || t.TeamID == nil {
					continue
				}
				if id, err := uuid.Parse(*t.TeamID); err == nil {
					ok, err := teamExists(ctx, q, in.WorkspaceID, id)
					if err != nil {
						return nil, "", "", err
					}
					if ok {
						return &id, ViaTopic, t.Label, nil
					}
				}
				break
			}
		}
		if widgetTeam != nil {
			return widgetTeam, ViaWidget, "", nil
		}
		return nil, "", "", nil
	case domain.ChannelEmail:
		team, err := mailboxTeam(ctx, q, in)
		return team, ViaAddress, "", err
	default:
		if in.TeamID != nil {
			return in.TeamID, ViaAPI, "", nil
		}
		team, err := mailboxTeam(ctx, q, in)
		return team, ViaAddress, "", err
	}
}

// mailboxTeam is the team of the alias the message was written to, else the mailbox's. A
// private team's address delivers every alias to that team.
func mailboxTeam(ctx context.Context, q db.Querier, in Input) (*uuid.UUID, error) {
	if in.MailboxID == nil {
		return nil, nil
	}
	var team *uuid.UUID
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT at.id FROM teams at
			WHERE at.workspace_id = m.workspace_id AND at.id::text = m.alias_teams ->> $3
			AND NOT EXISTS (SELECT 1 FROM teams pt WHERE pt.id = m.team_id AND pt.private)), m.team_id)
		FROM mailboxes m WHERE m.workspace_id = $1 AND m.id = $2`,
		in.WorkspaceID, *in.MailboxID, strings.ToLower(strings.TrimSpace(in.ReceivedAt))).Scan(&team)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load mailbox team: %w", err)
	}
	return team, nil
}

func teamExists(ctx context.Context, q db.Querier, wid, id uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&ok)
	return ok, err
}

// outcome is a rule's or condition's answer: ai_decide makes it unknown.
type outcome int

const (
	no outcome = iota
	yes
	unknown
)

// facts is the new ticket the conditions are evaluated against. Contact fields load on
// first use.
type facts struct {
	in            Input
	contactLoaded bool
	contactFields map[string]any
	defs          domain.FieldDefs
	textLower     map[string]string
}

func (f *facts) rule(ctx context.Context, q db.Querier, r RoutingRule) (outcome, error) {
	if len(r.Conditions) == 0 {
		return no, nil
	}
	matchAny := r.Match == MatchAny
	sawUnknown := false
	for _, c := range r.Conditions {
		o, err := f.condition(ctx, q, c)
		if err != nil {
			return no, err
		}
		switch {
		case o == unknown:
			sawUnknown = true
		case matchAny && o == yes:
			return yes, nil
		case !matchAny && o == no:
			return no, nil
		}
	}
	if sawUnknown {
		return unknown, nil
	}
	if matchAny {
		return no, nil
	}
	return yes, nil
}

func (f *facts) condition(ctx context.Context, q db.Querier, c RoutingCondition) (outcome, error) {
	if c.Field == FieldAIDecide {
		return unknown, nil
	}
	if key, ok := strings.CutPrefix(c.Field, contactFieldPrefix); ok {
		if err := f.loadContact(ctx, q); err != nil {
			return no, err
		}
		def, ok := f.defs[key]
		if !ok {
			return no, nil // the field was deleted since the rule was saved
		}
		return boolOutcome(domain.MatchField(def, domain.NormalizeOp(c.Op), c.Value, f.contactFields[key])), nil
	}
	have, ok := f.text(c.Field)
	if !ok {
		return no, nil
	}
	return boolOutcome(f.compareText(c.Field, c.Op, have, c.Value)), nil
}

// compareText normalizes each ticket field once. Inbound email bodies can be large, and
// routing may compare the same body against thousands of conditions.
func (f *facts) compareText(field, op, have, want string) bool {
	lower, ok := f.textLower[field]
	if !ok {
		lower = strings.ToLower(strings.TrimSpace(have))
		if f.textLower == nil {
			f.textLower = make(map[string]string)
		}
		f.textLower[field] = lower
	}
	return compareLower(op, lower, strings.ToLower(strings.TrimSpace(want)))
}

func (f *facts) loadContact(ctx context.Context, q db.Querier) error {
	if f.contactLoaded {
		return nil
	}
	f.contactLoaded = true
	defs, err := domain.LoadFieldDefs(ctx, q, f.in.WorkspaceID, domain.FieldEntityContact)
	if err != nil {
		return err
	}
	f.defs = defs
	f.contactFields = map[string]any{}
	if f.in.ContactID == nil {
		return nil
	}
	err = q.QueryRow(ctx, `SELECT COALESCE(fields, '{}') FROM contacts WHERE workspace_id = $1 AND id = $2`,
		f.in.WorkspaceID, *f.in.ContactID).Scan(&f.contactFields)
	if err != nil && !db.IsNotFound(err) {
		return fmt.Errorf("load contact fields: %w", err)
	}
	if f.contactFields == nil {
		f.contactFields = map[string]any{}
	}
	return nil
}

// text is the value of a plain condition field.
func (f *facts) text(field string) (string, bool) {
	in := f.in
	switch field {
	case "received_at":
		return in.ReceivedAt, true
	case "mailbox_id":
		return idString(in.MailboxID), true
	case "brand_id":
		if in.BrandID == uuid.Nil {
			return "", true
		}
		return in.BrandID.String(), true
	case "widget_id":
		return idString(in.WidgetID), true
	case "channel":
		return in.Channel, true
	case "from_email":
		return in.FromEmail, true
	case "from_domain":
		return emailDomain(in.FromEmail), true
	case "subject":
		return in.Subject, true
	case "body":
		return in.Body, true
	case "page_url":
		return in.PageURL, true
	case "topic":
		return in.Topic, true
	}
	return "", false
}

// compareLower compares values that have already been trimmed and lowercased.
func compareLower(op, h, w string) bool {
	switch op {
	case "equals":
		return h == w
	case "not_equals":
		return h != w
	case "contains":
		return strings.Contains(h, w)
	case "not_contains":
		return !strings.Contains(h, w)
	case "starts_with":
		return strings.HasPrefix(h, w)
	}
	return false
}

func boolOutcome(b bool) outcome {
	if b {
		return yes
	}
	return no
}

func idString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func emailDomain(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return strings.ToLower(strings.TrimSpace(addr[i+1:]))
	}
	return ""
}

// loadRules lists the workspace's rules in evaluation order.
func loadRules(ctx context.Context, q db.Querier, wid uuid.UUID, enabledOnly bool) ([]RoutingRule, error) {
	rows, err := q.Query(ctx, ruleSelect+` WHERE workspace_id = $1 AND (NOT $2 OR enabled) ORDER BY position, created_at, id`, wid, enabledOnly)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RoutingRule, error) { return scanRule(r) })
}
