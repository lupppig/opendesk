package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/integrations"
)

// output is a tool's answer: Content for the model (always inside a data block),
// Summary and Refs for the panel.
type output struct {
	Content string
	Summary string
	Refs    []Ref
	// Label replaces the tool's Done label; Peer is an ask of a teammate's Ollie.
	Label string
	Peer  *PeerStep
}

// Preview is exactly what an approval card shows before a write runs.
type Preview struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary,omitempty"`
	Changes []Change `json:"changes,omitempty"`
	// Body is the text a note, reply or article will carry (markdown).
	Body    string `json:"body,omitempty"`
	Targets []Ref  `json:"targets,omitempty"`
	// Total is how many tickets matched a bulk filter (Targets holds the capped set).
	Total int `json:"total,omitempty"`
	// External marks a change a customer will see (a reply).
	External bool `json:"external,omitempty"`
	// System names the connected tool the change runs in ("Linear").
	System string `json:"system,omitempty"`
	// Arguments is everything the change runs with, as indented JSON: a connected
	// tool's arguments as they are sent, or api_change's request. Changes and Body may
	// summarize; this is complete.
	Arguments string `json:"arguments,omitempty"`
	// Omitted counts the fields Changes and Body leave out; Clipped marks a value
	// they show cut short.
	Omitted int  `json:"omitted,omitempty"`
	Clipped bool `json:"clipped,omitempty"`
}

// Change is one field of a diff.
type Change struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// runCtx is one run's view of the workspace, as the calling user.
type runCtx struct {
	svc  *Service
	p    *auth.Principal
	read *caller
	page *PageContext
	// members is loaded once per run, for assignee names.
	members []member
	// ext are the workspace's connected tools, loaded once per run.
	ext      map[string]*tool
	extOrder []string
	// names are the workspace's team and brand names, loaded once per run.
	names *refNames
	// auto are the built-in changes the workspace lets run without asking, as the run
	// started (nil until loaded); each one is checked again just before it runs.
	auto []string
}

type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	// Write tools run only after an approval.
	Write   bool
	MinRole auth.Role
	// Labels for the panel while running and when done.
	Active, Done string
	// Labels, when set, names the panel labels from the call's arguments ("" keeps the
	// fixed ones).
	Labels func(ctx context.Context, rc *runCtx, in json.RawMessage) (active, done string)
	Read   func(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error)
	// Propose validates and resolves a write's arguments and describes it. It reads
	// only; the stored args are what Apply later runs.
	Propose func(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error)
	Apply   func(ctx context.Context, rc *runCtx, w *caller, args json.RawMessage, key string) (output, error)
	// ext is the connected tool behind a <slug>__<tool> name.
	ext *integrations.Tool
}

// BulkCap is the most tickets one approved bulk action changes.
const BulkCap = 50

// --- schema helpers ----------------------------------------------------------------

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func enum(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}
func arr(desc string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": items}
}
func integer(desc string, lo, hi int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": lo, "maximum": hi}
}

var statuses = []string{"open", "pending", "on_hold", "solved", "closed"}
var priorities = []string{"low", "normal", "high", "urgent"}

var severities = []string{"sev1", "sev2", "sev3", "sev4"}

// severityText is a severity, or "none".
func severityText(s *string) string {
	if s == nil {
		return "none"
	}
	return *s
}

func ticketFilterProps() map[string]any {
	return map[string]any{
		"query":          str("Full-text search over subjects and messages, or #number."),
		"status":         arr("Statuses to include.", enum("", statuses...)),
		"priority":       arr("Priorities to include.", enum("", priorities...)),
		"assignee":       str(`"me", "none" for unassigned, or a teammate's name, email or user id.`),
		"label":          str("A label name."),
		"channel":        enum("Channel.", "email", "chat", "api", "portal"),
		"created_after":  str("Only tickets created at or after this time: RFC 3339 or YYYY-MM-DD."),
		"created_before": str("Only tickets created before this time: RFC 3339 or YYYY-MM-DD."),
		"sla":            enum(`"breached" for tickets past an SLA target, "due_soon" for targets in the next hour.`, "breached", "due_soon"),
		"team":           str(`A team name or id, "mine" for the user's teams, or "none" for tickets without a team.`),
		"view_id":        str("Apply a saved view's filters."),
	}
}

// --- API shapes the tools read ----------------------------------------------------

type userRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type member struct {
	User userRef `json:"user"`
	Role string  `json:"role"`
}

type tagRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ticket struct {
	ID       string  `json:"id"`
	Number   int64   `json:"number"`
	Subject  string  `json:"subject"`
	Status   string  `json:"status"`
	Priority string  `json:"priority"`
	Severity *string `json:"severity"`
	Channel  string  `json:"channel"`
	Contact  *struct {
		ID    string  `json:"id"`
		Name  string  `json:"name"`
		Email *string `json:"email"`
	} `json:"contact"`
	Assignee *userRef `json:"assignee"`
	TeamID   *string  `json:"team_id"`
	BrandID  string   `json:"brand_id"`
	// ReceivedAt is the address the customer wrote to; MailboxID the one replies go out from.
	ReceivedAt string         `json:"received_at"`
	MailboxID  *string        `json:"mailbox_id"`
	Tags       []tagRef       `json:"tags"`
	Fields     map[string]any `json:"fields"`
	SLA        *struct {
		FirstResponseDueAt *time.Time `json:"first_response_due_at"`
		ResolutionDueAt    *time.Time `json:"resolution_due_at"`
		Breached           bool       `json:"breached"`
	} `json:"sla"`
	FirstRespondedAt *time.Time     `json:"first_responded_at"`
	LastMessageAt    time.Time      `json:"last_message_at"`
	Preview          string         `json:"last_message_preview"`
	MessageCount     int            `json:"message_count"`
	CreatedAt        time.Time      `json:"created_at"`
	Custom           map[string]any `json:"custom"`
	// Filled in by runCtx.describe: names the model reads instead of ids.
	teamName, brandName, replyFrom string
}

type page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

func (t ticket) ref() Ref {
	return Ref{Kind: "ticket", ID: t.ID, Number: t.Number, Label: t.Subject}
}

// nextDue is the earliest open SLA target, and whether it has passed.
func (t ticket) nextDue() (*time.Time, bool) {
	if t.SLA == nil {
		return nil, false
	}
	var due *time.Time
	if t.FirstRespondedAt == nil && t.SLA.FirstResponseDueAt != nil {
		due = t.SLA.FirstResponseDueAt
	}
	if t.SLA.ResolutionDueAt != nil && (due == nil || t.SLA.ResolutionDueAt.Before(*due)) {
		due = t.SLA.ResolutionDueAt
	}
	return due, t.SLA.Breached || (due != nil && due.Before(time.Now()))
}

func (t ticket) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d [%s, %s, %s] %q", t.Number, t.Status, t.Priority, t.Channel, t.Subject)
	if t.Severity != nil {
		fmt.Fprintf(&b, "; severity: %s", *t.Severity)
	}
	if t.Contact != nil {
		fmt.Fprintf(&b, "; customer: %s", t.Contact.Name)
		if t.Contact.Email != nil && *t.Contact.Email != "" {
			fmt.Fprintf(&b, " <%s>", *t.Contact.Email)
		}
	}
	if t.Assignee != nil {
		fmt.Fprintf(&b, "; assignee: %s", t.Assignee.Name)
	} else {
		b.WriteString("; unassigned")
	}
	switch {
	case t.teamName != "":
		fmt.Fprintf(&b, "; team: %s", t.teamName)
	case t.TeamID != nil:
		fmt.Fprintf(&b, "; team id: %s", *t.TeamID)
	}
	if t.brandName != "" {
		fmt.Fprintf(&b, "; brand: %s", t.brandName)
	}
	if t.ReceivedAt != "" {
		fmt.Fprintf(&b, "; written to: %s", t.ReceivedAt)
	}
	if t.replyFrom != "" {
		fmt.Fprintf(&b, "; replies from: %s", t.replyFrom)
	}
	if len(t.Tags) > 0 {
		names := make([]string, 0, len(t.Tags))
		for _, tg := range t.Tags {
			names = append(names, tg.Name)
		}
		fmt.Fprintf(&b, "; labels: %s", strings.Join(names, ", "))
	}
	fmt.Fprintf(&b, "; created %s; last message %s", t.CreatedAt.UTC().Format(time.RFC3339), t.LastMessageAt.UTC().Format(time.RFC3339))
	if due, breached := t.nextDue(); due != nil {
		if breached {
			fmt.Fprintf(&b, "; SLA breached (due %s)", due.UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(&b, "; SLA due %s", due.UTC().Format(time.RFC3339))
		}
	}
	fmt.Fprintf(&b, "; id %s", t.ID)
	return b.String()
}

// --- helpers ------------------------------------------------------------------------

func decode[T any](in json.RawMessage) (T, error) {
	var v T
	if len(in) == 0 {
		in = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(in, &v); err != nil {
		return v, httpx.Validation("the tool arguments are not valid: " + err.Error())
	}
	return v, nil
}

func parseTime(s string, endOfDay bool) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if endOfDay {
			t = t.Add(24 * time.Hour)
		}
		return &t, nil
	}
	return nil, httpx.Validation("times must be RFC 3339 or YYYY-MM-DD: " + s)
}

func (rc *runCtx) loadMembers(ctx context.Context) ([]member, error) {
	if rc.members != nil {
		return rc.members, nil
	}
	var pg page[member]
	if err := rc.read.get(ctx, "/v1/members", nil, &pg); err != nil {
		return nil, err
	}
	rc.members = pg.Data
	return rc.members, nil
}

// resolveAssignee turns "me", "none", a name, an email or an id into a user id
// ("" for unassigned).
func (rc *runCtx) resolveAssignee(ctx context.Context, raw string) (id string, name string, err error) {
	v := strings.TrimSpace(raw)
	switch strings.ToLower(v) {
	case "":
		return "", "", httpx.Validation("assignee is empty")
	case "none", "unassigned", "nobody", "null":
		return "", "Unassigned", nil
	case "me", "myself":
		v = rc.p.UserID.String()
	}
	ms, err := rc.loadMembers(ctx)
	if err != nil {
		return "", "", err
	}
	lv := strings.ToLower(v)
	var hits []member
	for _, m := range ms {
		if m.User.ID == v || strings.EqualFold(m.User.Email, v) || strings.EqualFold(m.User.Name, v) {
			return m.User.ID, m.User.Name, nil
		}
		if strings.Contains(strings.ToLower(m.User.Name), lv) {
			hits = append(hits, m)
		}
	}
	if len(hits) == 1 {
		return hits[0].User.ID, hits[0].User.Name, nil
	}
	if len(hits) > 1 {
		return "", "", httpx.Validation(fmt.Sprintf("%q matches more than one teammate; use their email", raw))
	}
	return "", "", httpx.Validation(fmt.Sprintf("no teammate matches %q", raw))
}

func (rc *runCtx) getTicket(ctx context.Context, raw string) (ticket, error) {
	ref, err := ticketRef(raw)
	if err != nil {
		return ticket{}, err
	}
	var t ticket
	if err = rc.read.get(ctx, "/v1/tickets/"+ref, nil, &t); err != nil {
		return t, err
	}
	rc.describe(ctx, &t)
	return t, nil
}

// refNames are the team and brand names of the workspace, as the user may list them.
type refNames struct {
	teams, brands map[string]string
}

// loadNames reads the team and brand lists once per run. A list the user may not read
// leaves its names empty; tickets then show ids.
func (rc *runCtx) loadNames(ctx context.Context) *refNames {
	if rc.names != nil {
		return rc.names
	}
	n := &refNames{teams: map[string]string{}, brands: map[string]string{}}
	var teams page[struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}]
	if rc.read.get(ctx, "/v1/teams", url.Values{"limit": {"100"}}, &teams) == nil {
		for _, t := range teams.Data {
			n.teams[t.ID] = t.Name
		}
	}
	var raw json.RawMessage
	if rc.read.get(ctx, "/v1/brands", url.Values{"limit": {"100"}}, &raw) == nil {
		type named struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		var list []named
		if json.Unmarshal(raw, &list) != nil {
			var pg page[named]
			_ = json.Unmarshal(raw, &pg)
			list = pg.Data
		}
		for _, b := range list {
			n.brands[b.ID] = b.Name
		}
	}
	rc.names = n
	return n
}

// describe fills in a ticket's team and brand names. The brand is named only when the
// workspace has more than one.
func (rc *runCtx) describe(ctx context.Context, ts ...*ticket) {
	n := rc.loadNames(ctx)
	for _, t := range ts {
		if t.TeamID != nil {
			t.teamName = n.teams[*t.TeamID]
		}
		if len(n.brands) > 1 {
			t.brandName = n.brands[t.BrandID]
		}
	}
}

// replyAddress fills in the address a ticket's replies go out from, when the user may
// read the mailbox.
func (rc *runCtx) replyAddress(ctx context.Context, t *ticket) {
	if t.MailboxID == nil || !uuidOK(*t.MailboxID) {
		return
	}
	var m struct {
		Email string `json:"email"`
	}
	if rc.read.get(ctx, "/v1/mailboxes/"+*t.MailboxID, nil, &m) == nil {
		t.replyFrom = m.Email
	}
}

// resolveTeam turns "none", a team id or a team name into a team id ("" for none) and
// its name.
func (rc *runCtx) resolveTeam(ctx context.Context, raw string) (id string, name string, err error) {
	v := strings.TrimSpace(raw)
	switch strings.ToLower(v) {
	case "":
		return "", "", httpx.Validation("team is empty")
	case "none", "no team", "null":
		return "", "No team", nil
	}
	n := rc.loadNames(ctx)
	if _, ok := n.teams[v]; ok {
		return v, n.teams[v], nil
	}
	for tid, tname := range n.teams {
		if strings.EqualFold(tname, v) {
			return tid, tname, nil
		}
	}
	return "", "", httpx.Validation(fmt.Sprintf("no team is named %q; list_views lists the teams", raw))
}

type ticketFilter struct {
	Query         string   `json:"query"`
	Status        []string `json:"status"`
	Priority      []string `json:"priority"`
	Assignee      string   `json:"assignee"`
	Label         string   `json:"label"`
	Channel       string   `json:"channel"`
	CreatedAfter  string   `json:"created_after"`
	CreatedBefore string   `json:"created_before"`
	SLA           string   `json:"sla"`
	ViewID        string   `json:"view_id"`
	Team          string   `json:"team"`
}

func (f ticketFilter) empty() bool {
	return f.Query == "" && len(f.Status) == 0 && len(f.Priority) == 0 && f.Assignee == "" && f.Label == "" &&
		f.Channel == "" && f.CreatedAfter == "" && f.CreatedBefore == "" && f.SLA == "" && f.ViewID == "" && f.Team == ""
}

// findTickets lists tickets matching f through GET /v1/tickets, applying the time and
// SLA filters the list route does not have. It scans at most scanMax tickets and
// returns up to limit plus the number that matched in the scan.
func (rc *runCtx) findTickets(ctx context.Context, f ticketFilter, limit, scanMax int) ([]ticket, int, error) {
	q := url.Values{}
	if s := strings.TrimSpace(f.Query); s != "" {
		q.Set("q", s)
	}
	if len(f.Status) > 0 {
		q.Set("status", strings.Join(f.Status, ","))
	}
	if len(f.Priority) > 0 {
		q.Set("priority", strings.Join(f.Priority, ","))
	}
	if f.Assignee != "" {
		switch strings.ToLower(strings.TrimSpace(f.Assignee)) {
		case "me":
			q.Set("assignee", "me")
		case "none", "unassigned":
			q.Set("assignee", "none")
		default:
			id, _, err := rc.resolveAssignee(ctx, f.Assignee)
			if err != nil {
				return nil, 0, err
			}
			q.Set("assignee", id)
		}
	}
	if f.Label != "" {
		q.Set("tag", f.Label)
	}
	if f.Channel != "" {
		q.Set("channel", f.Channel)
	}
	if f.ViewID != "" {
		id, err := idRef(f.ViewID, "view_id")
		if err != nil {
			return nil, 0, err
		}
		q.Set("view_id", id)
	}
	if team := strings.TrimSpace(f.Team); team != "" {
		switch strings.ToLower(team) {
		case "mine", "my teams":
			q.Set("team_id", "mine")
		default:
			id, _, err := rc.resolveTeam(ctx, team)
			if err != nil {
				return nil, 0, err
			}
			if id == "" {
				id = "none"
			}
			q.Set("team_id", id)
		}
	}
	after, err := parseTime(f.CreatedAfter, false)
	if err != nil {
		return nil, 0, err
	}
	before, err := parseTime(f.CreatedBefore, true)
	if err != nil {
		return nil, 0, err
	}
	if after != nil || before != nil {
		q.Set("sort", "created_at")
	}
	q.Set("limit", "100")
	var out []ticket
	matched, scanned := 0, 0
	for scanned < scanMax {
		var pg page[ticket]
		if err := rc.read.get(ctx, "/v1/tickets", q, &pg); err != nil {
			return nil, 0, err
		}
		stop := false
		for _, t := range pg.Data {
			scanned++
			if after != nil && t.CreatedAt.Before(*after) {
				stop = true // sorted by created_at desc: everything after is older
				break
			}
			if before != nil && !t.CreatedAt.Before(*before) {
				continue
			}
			if f.SLA != "" {
				due, breached := t.nextDue()
				if t.Status == "solved" || t.Status == "closed" {
					continue
				}
				if f.SLA == "breached" && !breached {
					continue
				}
				if f.SLA == "due_soon" && (breached || due == nil || due.After(time.Now().Add(time.Hour))) {
					continue
				}
			}
			matched++
			if len(out) < limit {
				rc.describe(ctx, &t)
				out = append(out, t)
			}
		}
		if stop || pg.NextCursor == nil || len(pg.Data) == 0 {
			break
		}
		q.Set("cursor", *pg.NextCursor)
	}
	return out, matched, nil
}

func block(tool string, body string) string {
	return ai.DataBlock("tool_output", []string{"tool", tool}, body)
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n])) + " […]"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func validEnum(vals []string, allowed []string, field string) error {
	for _, v := range vals {
		if !slices.Contains(allowed, v) {
			return httpx.Validation(field + " must be one of " + strings.Join(allowed, ", "))
		}
	}
	return nil
}

func uuidOK(s string) bool { _, err := uuid.Parse(s); return err == nil }
