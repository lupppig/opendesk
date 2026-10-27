package hd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Team filter values besides a team id.
const (
	TeamMine = "mine"
	TeamNone = "none"
)

// SeverityFilterValues are the severities a filter may name; "none" is no severity.
var SeverityFilterValues = append(append([]string{}, domain.Severities...), "none")

// TicketFilters is a saved view's filter set (contracts/api.md TicketFilters).
// Assignee is "me", "none" or a user id; TeamID "mine" (the caller's teams), "none" or a
// team id. Label names never contain a comma, so Tag and TagNot are comma-separated lists
// like TeamID. Each *Not field leaves out the tickets its values would match.
type TicketFilters struct {
	Status    []string `json:"status,omitempty" enum:"open,pending,on_hold,solved,closed"`
	Priority  []string `json:"priority,omitempty" enum:"low,normal,high,urgent"`
	Severity  []string `json:"severity,omitempty" enum:"sev1,sev2,sev3,sev4,none" doc:"none: tickets without a severity"`
	Assignee  string   `json:"assignee,omitempty"`
	TeamID    string   `json:"team_id,omitempty" doc:"A team id, mine (the caller's teams) or none"`
	TeamNot   string   `json:"team_not,omitempty" doc:"Comma-separated team ids, mine and none: tickets of none of them"`
	MailboxID string   `json:"mailbox_id,omitempty"`
	// MailboxNot leaves out tickets whose reply address is one of these mailboxes.
	MailboxNot string `json:"mailbox_not,omitempty" doc:"Comma-separated mailbox ids: tickets whose reply address is none of them"`
	// ContactNot and CompanyNot leave out tickets from these customers: the ticket's contact,
	// or the company that contact belongs to.
	ContactNot string `json:"contact_not,omitempty" doc:"Comma-separated contact ids: tickets whose contact is none of them"`
	CompanyNot string `json:"company_not,omitempty" doc:"Comma-separated company ids: tickets whose contact belongs to none of them"`
	// ReceivedAt is the address a ticket was first written to: a mailbox's address or an alias.
	ReceivedAt string   `json:"received_at,omitempty" maxLength:"254" doc:"Tickets first written to this address (a mailbox's address or one of its aliases)"`
	BrandID    string   `json:"brand_id,omitempty"`
	WidgetID   string   `json:"widget_id,omitempty"`
	Tag        string   `json:"tag,omitempty" doc:"Comma-separated label names: tickets carrying any of them"`
	TagNot     string   `json:"tag_not,omitempty" doc:"Comma-separated label names: tickets carrying none of them"`
	Untagged   bool     `json:"untagged,omitempty" doc:"Tickets carrying no label"`
	Channel    string   `json:"channel,omitempty" enum:"email,chat,api,portal"`
	ChannelNot []string `json:"channel_not,omitempty" enum:"email,chat,api,portal" doc:"Tickets on none of these channels"`
	Q          string   `json:"q,omitempty" maxLength:"200"`
	// Fields are custom field filters, ANDed.
	Fields []FieldFilter `json:"fields,omitempty" maxItems:"20"`
}

// Normalize trims values and validates every field against the contract enums and the
// workspace's own rows.
func (f *TicketFilters) Normalize(ctx context.Context, q db.Querier, wid uuid.UUID) error {
	var err error
	if f.Status, err = enumList(f.Status, domain.Statuses, "status"); err != nil {
		return err
	}
	if f.Priority, err = enumList(f.Priority, domain.Priorities, "priority"); err != nil {
		return err
	}
	if f.Severity, err = enumList(f.Severity, SeverityFilterValues, "severity"); err != nil {
		return err
	}
	f.Assignee = strings.TrimSpace(f.Assignee)
	switch f.Assignee {
	case "", "me", "none":
	default:
		id, err := ParseRef(f.Assignee, "assignee")
		if err != nil {
			return err
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE workspace_id = $1 AND user_id = $2)`, wid, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return httpx.Validation("assignee is not a member of this workspace")
		}
		f.Assignee = id.String()
	}
	// team_id and team_not are comma-separated lists of team ids, mine and none.
	if f.TeamID, err = normTeams(ctx, q, wid, f.TeamID, "team_id"); err != nil {
		return err
	}
	if f.TeamNot, err = normTeams(ctx, q, wid, f.TeamNot, "team_not"); err != nil {
		return err
	}
	for _, ref := range []struct {
		v           *string
		kind, field string
	}{{&f.MailboxNot, "mailbox", "mailbox_not"}, {&f.ContactNot, "contact", "contact_not"}, {&f.CompanyNot, "company", "company_not"}} {
		if *ref.v, err = normRefs(ctx, q, wid, *ref.v, ref.kind, ref.field); err != nil {
			return err
		}
	}
	for _, ref := range []struct {
		v    *string
		kind string
	}{{&f.MailboxID, "mailbox"}, {&f.BrandID, "brand"}, {&f.WidgetID, "widget"}} {
		*ref.v = strings.TrimSpace(*ref.v)
		if *ref.v == "" {
			continue
		}
		id, err := ParseRef(*ref.v, ref.kind+"_id")
		if err != nil {
			return err
		}
		if err := CheckRef(ctx, q, wid, ref.kind, id); err != nil {
			return err
		}
		*ref.v = id.String()
	}
	f.ReceivedAt = strings.ToLower(strings.TrimSpace(f.ReceivedAt))
	if len(f.ReceivedAt) > 254 {
		return httpx.Validation("received_at must be 254 characters or less")
	}
	for _, l := range []struct {
		v     *string
		field string
	}{{&f.Tag, "tag"}, {&f.TagNot, "tag_not"}} {
		names, err := SplitLabels(*l.v, l.field)
		if err != nil {
			return err
		}
		*l.v = strings.Join(names, ",")
	}
	f.Channel = strings.TrimSpace(f.Channel)
	if f.Channel != "" && !slices.Contains(domain.Channels, f.Channel) {
		return httpx.Validation("channel must be one of " + strings.Join(domain.Channels, ", "))
	}
	if f.ChannelNot, err = enumList(f.ChannelNot, domain.Channels, "channel_not"); err != nil {
		return err
	}
	f.Q = strings.TrimSpace(f.Q)
	if len([]rune(f.Q)) > 200 {
		return httpx.Validation("q must be 200 characters or less")
	}
	if len(f.Fields) > 0 {
		defs, err := domain.LoadFieldDefs(ctx, q, wid, domain.FieldEntityTicket)
		if err != nil {
			return err
		}
		if f.Fields, err = NormalizeFieldFilters(defs, f.Fields); err != nil {
			return err
		}
	} else {
		f.Fields = nil
	}
	return nil
}

// normTeams validates a comma-separated list of team ids, mine and none, dropping repeats.
func normTeams(ctx context.Context, q db.Querier, wid uuid.UUID, raw, field string) (string, error) {
	var teams []string
	for _, part := range strings.Split(raw, ",") {
		v := strings.TrimSpace(part)
		switch {
		case v == "":
			continue
		case v != TeamMine && v != TeamNone:
			id, err := ParseRef(v, field)
			if err != nil {
				return "", err
			}
			if err := CheckRef(ctx, q, wid, "team", id); err != nil {
				return "", err
			}
			v = id.String()
		}
		if !slices.Contains(teams, v) {
			teams = append(teams, v)
		}
	}
	if len(teams) > 50 {
		return "", httpx.Validation(field + " takes at most 50 values")
	}
	return strings.Join(teams, ","), nil
}

// normRefs validates a comma-separated list of ids of the workspace's rows of kind,
// dropping repeats.
func normRefs(ctx context.Context, q db.Querier, wid uuid.UUID, raw, kind, field string) (string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		id, err := ParseRef(v, field)
		if err != nil {
			return "", err
		}
		if err := CheckRef(ctx, q, wid, kind, id); err != nil {
			return "", err
		}
		if !slices.Contains(out, id.String()) {
			out = append(out, id.String())
		}
	}
	if len(out) > 50 {
		return "", httpx.Validation(field + " takes at most 50 values")
	}
	return strings.Join(out, ","), nil
}

// SplitLabels splits a comma-separated list of label names (a label name never contains a
// comma), trimming each and dropping blanks and repeats in any case.
func SplitLabels(raw, field string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		v := strings.TrimSpace(part)
		if v == "" || slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, v) }) {
			continue
		}
		if len([]rune(v)) > 50 {
			return nil, httpx.Validation("label must be 50 characters or less")
		}
		out = append(out, v)
	}
	if len(out) > 50 {
		return nil, httpx.Validation(field + " takes at most 50 labels")
	}
	return out, nil
}

// RenameLabel replaces from with to (case-insensitive) in a comma-separated list of label
// names, keeping each name once. It reports whether the list named from.
func RenameLabel(list, from, to string) (string, bool) {
	names, _ := SplitLabels(list, "")
	hit := false
	var out []string
	for _, n := range names {
		if strings.EqualFold(n, from) {
			n, hit = to, true
		}
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, n) }) {
			out = append(out, n)
		}
	}
	if !hit {
		return list, false
	}
	return strings.Join(out, ","), true
}

func enumList(in, allowed []string, field string) ([]string, error) {
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || slices.Contains(out, v) {
			continue
		}
		if !slices.Contains(allowed, v) {
			return nil, httpx.Validation(field + " must be one of " + strings.Join(allowed, ", "))
		}
		out = append(out, v)
	}
	return out, nil
}

// SplitCSV splits a comma separated query value.
func SplitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ViewVisibleSQL is the predicate for the views aliased alias that a caller sees in their
// lists, with the caller's user id bound as $n (NULL for API keys): shared views, their own
// personal views and the views of their teams.
func ViewVisibleSQL(alias string, n int) string {
	return fmt.Sprintf(`((%[1]s.team_id IS NULL AND (%[1]s.user_id IS NULL OR %[1]s.user_id = $%[2]d))
		OR %[1]s.team_id IN (SELECT vt.team_id FROM team_members vt WHERE vt.user_id = $%[2]d))`, alias, n)
}
