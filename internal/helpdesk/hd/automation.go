package hd

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// Automation enums (contracts/api.md).
var (
	AutomationTriggers = []string{jobs.TriggerTicketCreated, jobs.TriggerCustomerReplied, jobs.TriggerAgentReplied, jobs.TriggerTicketUpdated}
	ConditionFields    = []string{"subject", "body", "from_email", "channel", "mailbox_id", "priority", "severity", "status", "tag", "sentiment", "ai_decide",
		"assignee_id", "team_id", "brand_id", "widget_id", "received_at"}
	ConditionOps = []string{"contains", "not_contains", "equals", "not_equals", "any", "none"}
	// PhraseFields take the phrase ops any and none: a list of phrases, one per line.
	PhraseFields = []string{"subject", "body", "from_email", "received_at", "tag"}
	ActionTypes  = []string{"set_status", "set_priority", "set_severity", "assign_user", "assign_team", "add_tag", "remove_tag", "send_reply", "add_note", "mark_spam", "delete_ticket", "merge_into_open_ticket", "ai_triage", "set_field", "set_reply_address"}
	// Sentiments are the values triage writes to custom.ai_sentiment (contracts/ai.md).
	Sentiments = []string{"positive", "neutral", "negative", "frustrated"}
)

const (
	MaxConditions = 20
	MaxActions    = 20
	// MaxAIDecideConditions bounds the ai_decide conditions of one automation.
	MaxAIDecideConditions = 3
	// MaxConditionValue bounds a condition value, and each phrase of an any or none list.
	MaxConditionValue = 500
	// MaxPhrases and MaxPhraseList bound the phrase list of an any or none condition.
	MaxPhrases    = 100
	MaxPhraseList = 5000
	// DefaultMergeWindowDays and MaxMergeWindowDays bound how recent the ticket a
	// merge_into_open_ticket action merges into must be (its value, in days).
	DefaultMergeWindowDays = 7
	MaxMergeWindowDays     = 30
)

type Condition struct {
	Field string `json:"field" maxLength:"60" doc:"subject, body, from_email, channel, mailbox_id, priority, severity, status, tag, sentiment, ai_decide, assignee_id, team_id, brand_id, widget_id, received_at, field.<key> (ticket custom field) or contact.field.<key>"`
	Op    string `json:"op" enum:"contains,not_contains,equals,not_equals,eq,ne,gt,gte,lt,lte,any,all,none,set,not_set" doc:"eq is an alias of equals, ne of not_equals; custom fields take the ops of their type"`
	Value string `json:"value" maxLength:"5000" doc:"up to 500 characters; any and none on subject, body, from_email, received_at and tag: up to 100 phrases, one per line"`
	// Question is the yes/no question of an ai_decide condition (contracts/ai.md "Automations").
	Question string `json:"question,omitempty" maxLength:"300" doc:"ai_decide only: a yes/no question about the ticket"`
}

type Action struct {
	Type  string `json:"type" enum:"set_status,set_priority,set_severity,assign_user,assign_team,add_tag,remove_tag,send_reply,add_note,mark_spam,delete_ticket,merge_into_open_ticket,ai_triage,set_field,set_reply_address"`
	Value string `json:"value" maxLength:"20000"`
	// Field is the ticket custom field key of set_field.
	Field string `json:"field,omitempty" maxLength:"40"`
	// SameSubject is merge_into_open_ticket's option: merge only into a ticket with the
	// same subject, once reply prefixes and ticket tags are dropped. Stored true or false.
	SameSubject *bool `json:"same_subject,omitempty" doc:"merge_into_open_ticket only: merge only into a ticket with the same subject (reply prefixes and ticket tags dropped, case ignored); default true"`
}

// MergeWindowDays is a merge_into_open_ticket action's window, in days.
func (a Action) MergeWindowDays() int {
	n, err := strconv.Atoi(a.Value)
	if err != nil || n < 1 || n > MaxMergeWindowDays {
		return DefaultMergeWindowDays
	}
	return n
}

// MergeSameSubject reports whether a merge_into_open_ticket action needs the same subject.
func (a Action) MergeSameSubject() bool {
	return a.SameSubject == nil || *a.SameSubject
}

// ConditionFieldKey splits "field.<key>" (ticket) and "contact.field.<key>" (contact) conditions.
func ConditionFieldKey(field string) (entity, key string, ok bool) {
	if k, found := strings.CutPrefix(field, "contact.field."); found {
		return domain.FieldEntityContact, k, true
	}
	if k, found := strings.CutPrefix(field, "field."); found {
		return domain.FieldEntityTicket, k, true
	}
	return "", "", false
}

// ValidateConditions checks conditions against the enums and the workspace.
func ValidateConditions(ctx context.Context, q db.Querier, wid uuid.UUID, cs []Condition) ([]Condition, error) {
	if len(cs) > MaxConditions {
		return nil, httpx.Validation("an automation can have at most 20 conditions")
	}
	out := make([]Condition, 0, len(cs))
	defs := map[string]domain.FieldDefs{}
	decides := 0
	for _, c := range cs {
		c.Field, c.Op = strings.TrimSpace(c.Field), strings.TrimSpace(c.Op)
		if c.Field == "ai_decide" {
			if decides++; decides > MaxAIDecideConditions {
				return nil, httpx.Validation("an automation can have at most 3 ai_decide conditions")
			}
			v, err := validDecide(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		c.Question = ""
		if entity, key, ok := ConditionFieldKey(c.Field); ok {
			if defs[entity] == nil {
				d, err := domain.LoadFieldDefs(ctx, q, wid, entity)
				if err != nil {
					return nil, err
				}
				defs[entity] = d
			}
			f, ok := defs[entity][key]
			if !ok {
				return nil, httpx.Validation(fmt.Sprintf("condition field %q is not a custom field of %ss", Truncate(c.Field, 60), entity))
			}
			v, err := domain.CheckFieldOperand(f, domain.NormalizeOp(c.Op), c.Value, false)
			if err != nil {
				return nil, err
			}
			c.Op, c.Value = domain.NormalizeOp(c.Op), v
			out = append(out, c)
			continue
		}
		switch c.Op {
		case "eq":
			c.Op = "equals"
		case "ne":
			c.Op = "not_equals"
		}
		if !slices.Contains(ConditionFields, c.Field) {
			return nil, httpx.Validation("condition field must be one of " + strings.Join(ConditionFields, ", "))
		}
		if !slices.Contains(ConditionOps, c.Op) {
			return nil, httpx.Validation("condition op must be one of " + strings.Join(ConditionOps, ", "))
		}
		if c.Op == "any" || c.Op == "none" {
			if !slices.Contains(PhraseFields, c.Field) {
				return nil, httpx.Validation("any and none conditions take " + strings.Join(PhraseFields, ", "))
			}
			v, err := phraseList(c.Value)
			if err != nil {
				return nil, err
			}
			c.Value = v
		} else if utf8.RuneCountInString(c.Value) > MaxConditionValue {
			return nil, httpx.Validation("condition values must be 500 characters or less")
		}
		exact := c.Op == "equals" || c.Op == "not_equals"
		switch c.Field {
		case "channel":
			if exact && !slices.Contains(domain.Channels, c.Value) {
				return nil, httpx.Validation("channel must be one of " + strings.Join(domain.Channels, ", "))
			}
		case "priority":
			if exact && !slices.Contains(domain.Priorities, c.Value) {
				return nil, httpx.Validation("priority must be one of " + strings.Join(domain.Priorities, ", "))
			}
		case "status":
			if exact && !slices.Contains(domain.Statuses, c.Value) {
				return nil, httpx.Validation("status must be one of " + strings.Join(domain.Statuses, ", "))
			}
		case "severity":
			if !exact || (c.Value != "none" && !slices.Contains(domain.Severities, c.Value)) {
				return nil, httpx.Validation("severity conditions use equals or not_equals with one of " + strings.Join(SeverityFilterValues, ", "))
			}
		case "sentiment":
			if !exact || !slices.Contains(Sentiments, c.Value) {
				return nil, httpx.Validation("sentiment conditions use equals or not_equals with one of " + strings.Join(Sentiments, ", "))
			}
		case "mailbox_id":
			if exact {
				id, err := ParseRef(c.Value, "mailbox_id")
				if err != nil {
					return nil, err
				}
				if err := CheckRef(ctx, q, wid, "mailbox", id); err != nil {
					return nil, err
				}
				c.Value = id.String()
			}
		case "assignee_id", "team_id", "brand_id", "widget_id":
			// The ticket's team, brand or widget: an id of the workspace, or "none".
			if !exact {
				return nil, httpx.Validation(c.Field + " conditions use equals or not_equals")
			}
			kind := strings.TrimSuffix(c.Field, "_id")
			if v := strings.TrimSpace(c.Value); v == "" || v == "none" {
				if kind == "brand" {
					return nil, httpx.Validation("brand_id conditions need a brand id")
				}
				c.Value = "none"
				break
			}
			id, err := ParseRef(c.Value, c.Field)
			if err != nil {
				return nil, err
			}
			if kind == "assignee" {
				if err := CheckMember(ctx, q, wid, id); err != nil {
					return nil, err
				}
			} else if err := CheckRef(ctx, q, wid, kind, id); err != nil {
				return nil, err
			}
			c.Value = id.String()
		case "received_at":
			// The exact address the customer wrote to.
			if !slices.Contains([]string{"equals", "not_equals", "contains", "any", "none"}, c.Op) {
				return nil, httpx.Validation("received_at conditions use equals, not_equals, contains, any or none")
			}
			c.Value = strings.ToLower(strings.TrimSpace(c.Value))
		}
		out = append(out, c)
	}
	return out, nil
}

// Phrases splits the value of an any or none condition: one phrase per line, each
// trimmed, empty lines dropped.
func Phrases(v string) []string {
	var out []string
	for _, p := range strings.Split(v, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// phraseList checks the phrase list of an any or none condition and returns it one
// phrase per line: 1 to MaxPhrases phrases of at most MaxConditionValue characters,
// MaxPhraseList characters in all.
func phraseList(v string) (string, error) {
	list := Phrases(v)
	if len(list) == 0 {
		return "", httpx.Validation("any and none conditions need at least one phrase, one per line")
	}
	if len(list) > MaxPhrases {
		return "", httpx.Validation("any and none conditions take at most 100 phrases")
	}
	for _, p := range list {
		if utf8.RuneCountInString(p) > MaxConditionValue {
			return "", httpx.Validation("each phrase must be 500 characters or less")
		}
	}
	out := strings.Join(list, "\n")
	if utf8.RuneCountInString(out) > MaxPhraseList {
		return "", httpx.Validation("a phrase list must be 5000 characters or less")
	}
	return out, nil
}

// ValidateActions checks actions and sanitizes reply and note bodies.
func ValidateActions(ctx context.Context, q db.Querier, wid uuid.UUID, as []Action) ([]Action, error) {
	if len(as) == 0 {
		return nil, httpx.Validation("an automation needs at least one action")
	}
	if len(as) > MaxActions {
		return nil, httpx.Validation("an automation can have at most 20 actions")
	}
	out := make([]Action, 0, len(as))
	for _, a := range as {
		a.Type = strings.TrimSpace(a.Type)
		switch a.Type {
		case "set_status":
			if !slices.Contains(domain.Statuses, a.Value) {
				return nil, httpx.Validation("set_status value must be one of " + strings.Join(domain.Statuses, ", "))
			}
		case "set_priority":
			if !slices.Contains(domain.Priorities, a.Value) {
				return nil, httpx.Validation("set_priority value must be one of " + strings.Join(domain.Priorities, ", "))
			}
		case "set_severity":
			a.Value = strings.TrimSpace(a.Value)
			if a.Value != "" && !slices.Contains(domain.Severities, a.Value) {
				return nil, httpx.Validation("set_severity value must be one of " + strings.Join(domain.Severities, ", ") + ", or empty to clear")
			}
		case "assign_user":
			id, err := ParseRef(a.Value, "assign_user value")
			if err != nil {
				return nil, err
			}
			if err := CheckMember(ctx, q, wid, id); err != nil {
				return nil, err
			}
			a.Value = id.String()
		case "assign_team":
			id, err := ParseRef(a.Value, "assign_team value")
			if err != nil {
				return nil, err
			}
			if err := CheckRef(ctx, q, wid, "team", id); err != nil {
				return nil, err
			}
			a.Value = id.String()
		case "add_tag", "remove_tag":
			name, err := CleanTagName(a.Value)
			if err != nil {
				return nil, err
			}
			a.Value = name
		case "send_reply", "add_note":
			a.Value = SanitizeRich(a.Value)
			if HTMLToText(a.Value) == "" {
				return nil, httpx.Validation(a.Type + " needs a message")
			}
		case "ai_triage", "delete_ticket":
			a.Value = ""
		case "set_reply_address":
			// An address the ticket replies from; skipped on tickets of another brand.
			id, err := ParseRef(a.Value, "set_reply_address value")
			if err != nil {
				return nil, err
			}
			if err := CheckRef(ctx, q, wid, "mailbox", id); err != nil {
				return nil, err
			}
			a.Value = id.String()
		case "set_field":
			a.Field = strings.TrimSpace(a.Field)
			defs, err := domain.LoadFieldDefs(ctx, q, wid, domain.FieldEntityTicket)
			if err != nil {
				return nil, err
			}
			f, ok := defs[a.Field]
			if !ok || f.Archived {
				return nil, httpx.Validation("set_field needs field, the key of an active ticket custom field")
			}
			v, err := domain.ParseFieldString(f, a.Value, domain.MemberChecker(ctx, q, wid))
			if err != nil {
				return nil, err
			}
			a.Value = FieldString(v)
		case "mark_spam":
			switch a.Value {
			case "", "true":
				a.Value = "true"
			case "false":
			default:
				return nil, httpx.Validation(`mark_spam value must be "true" or "false"`)
			}
		case "merge_into_open_ticket":
			// value: the window in days, 1..MaxMergeWindowDays, DefaultMergeWindowDays when empty.
			v := strings.TrimSpace(a.Value)
			if v == "" {
				v = strconv.Itoa(DefaultMergeWindowDays)
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > MaxMergeWindowDays {
				return nil, httpx.Validation(fmt.Sprintf("merge_into_open_ticket value must be a number of days from 1 to %d", MaxMergeWindowDays))
			}
			a.Value = strconv.Itoa(n)
			same := a.MergeSameSubject()
			a.SameSubject = &same
		default:
			return nil, httpx.Validation("action type must be one of " + strings.Join(ActionTypes, ", "))
		}
		if a.Type != "set_field" {
			a.Field = ""
		}
		if a.Type != "merge_into_open_ticket" {
			a.SameSubject = nil
		}
		out = append(out, a)
	}
	return out, nil
}

// ValidateActionTrigger checks that the actions can run on trigger: merge_into_open_ticket
// joins a new ticket to an earlier one, so it runs only when a ticket is created.
func ValidateActionTrigger(trigger string, as []Action) error {
	if trigger != jobs.TriggerTicketCreated && slices.ContainsFunc(as, func(a Action) bool { return a.Type == "merge_into_open_ticket" }) {
		return httpx.Validation("merge_into_open_ticket runs only on the ticket_created trigger")
	}
	return nil
}

// FieldString is the string form of a stored field value (set_field action values).
func FieldString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []string:
		return strings.Join(x, ",")
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, fmt.Sprint(e))
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v)
}

// validDecide checks an ai_decide condition: gt or lt, a probability strictly between 0
// and 1, and a plain text yes/no question. The question is data for the decision
// engine; markup and control characters are refused, whitespace is collapsed.
func validDecide(c Condition) (Condition, error) {
	if c.Op != "gt" && c.Op != "lt" {
		return c, httpx.Validation("ai_decide conditions use gt or lt")
	}
	p, err := strconv.ParseFloat(strings.TrimSpace(c.Value), 64)
	if err != nil || p < 0.01 || p > 0.99 {
		return c, httpx.Validation("ai_decide value must be a probability between 0.01 and 0.99")
	}
	c.Value = strconv.FormatFloat(p, 'f', -1, 64)
	q := strings.Join(strings.Fields(c.Question), " ")
	if n := utf8.RuneCountInString(q); n < 3 || n > 300 {
		return c, httpx.Validation("ai_decide needs a question of 3 to 300 characters")
	}
	if strings.ContainsAny(q, "<>`") || strings.IndexFunc(q, unicode.IsControl) >= 0 {
		return c, httpx.Validation("ai_decide questions are plain text without <, > or `")
	}
	c.Question = q
	return c, nil
}

// DecideThreshold is the probability an ai_decide condition compares against.
func (c Condition) DecideThreshold() float64 {
	p, _ := strconv.ParseFloat(c.Value, 64)
	return p
}
