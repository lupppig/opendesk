package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/deps"
)

// tool is one MCP tool: its schema, the scopes a key needs to be offered it, and how it
// runs (through the caller, as the key).
type tool struct {
	Name        string
	Title       string
	Description string
	Schema      map[string]any
	// Scopes are all required for the tool to be offered.
	Scopes []string
	// Hints for the client (MCP tool annotations).
	ReadOnly, Destructive, Idempotent, OpenWorld bool
	Run                                          func(ctx context.Context, c *caller, args json.RawMessage) (any, error)
}

func (t *tool) offered(p *auth.Principal) bool {
	for _, s := range t.Scopes {
		if !p.HasScope(s) {
			return false
		}
	}
	return true
}

func (t *tool) mcpTool() *mcp.Tool {
	destructive, openWorld := t.Destructive, t.OpenWorld
	return &mcp.Tool{
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		InputSchema: t.Schema,
		Annotations: &mcp.ToolAnnotations{
			Title:           t.Title,
			ReadOnlyHint:    t.ReadOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  t.Idempotent,
			OpenWorldHint:   &openWorld,
		},
	}
}

// catalog is every tool, in the order clients list them (contracts/agent.md).
func catalog() []*tool {
	return slices.Concat(ticketTools(), contactTools(), knowledgeTools(), wikiTools(), reportTools(), collabTools())
}

// --- results -------------------------------------------------------------------------

func textResult(v any) *mcp.CallToolResult {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimSpace(buf.String())}}}
}

// errorResult is a tool error the client's model can read and act on. API errors
// carry their problem detail (written for API callers); anything else is a fixed
// message, logged without arguments.
func errorResult(d *deps.Deps, name string, err error) *mcp.CallToolResult {
	type body struct {
		Error  string `json:"error"`
		Code   string `json:"code,omitempty"`
		Status int    `json:"status,omitempty"`
	}
	var b body
	var ae *apiError
	var ie *errInput
	switch {
	case errors.As(err, &ie):
		b = body{Error: ie.msg, Code: "invalid_arguments"}
	case errors.As(err, &ae) && ae.Status < 500:
		b = body{Error: ae.Detail, Code: ae.Code, Status: ae.Status}
		if ae.Status == 403 {
			b.Error = "This connection is not allowed to do that. " + ae.Detail
		}
	default:
		d.Log.Error("mcp tool failed", "tool", name, "err", err)
		b = body{Error: "Something went wrong on our side. Try again in a moment.", Code: "internal"}
	}
	res := textResult(b)
	res.IsError = true
	return res
}

// --- arguments -----------------------------------------------------------------------

// decode reads tool arguments strictly: unknown members are refused like API bodies.
func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, invalid("the arguments are not valid: " + strings.TrimPrefix(err.Error(), "json: "))
	}
	return v, nil
}

func oneOf(v, field string, allowed ...string) error {
	if v == "" {
		return nil
	}
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return invalid(field + " must be one of " + strings.Join(allowed, ", "))
}

func allOf(vs []string, field string, allowed ...string) error {
	for _, v := range vs {
		if err := oneOf(v, field, allowed...); err != nil {
			return err
		}
	}
	return nil
}

// day parses YYYY-MM-DD or RFC 3339; endOfDay moves a bare day to the next midnight.
func day(s, field string, endOfDay bool) (*time.Time, error) {
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
	return nil, invalid(field + " must be YYYY-MM-DD or an RFC 3339 time")
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return strings.TrimSpace(string(r[:n])) + " […]"
}

// --- schema helpers ------------------------------------------------------------------

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strN(desc string, minLen, maxLen int) map[string]any {
	return map[string]any{"type": "string", "description": desc, "minLength": minLen, "maxLength": maxLen}
}

func enum(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}

func arr(desc string, items map[string]any, maxItems int) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": items, "maxItems": maxItems}
}

func integer(desc string, lo, hi, def int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": lo, "maximum": hi, "default": def}
}

var (
	statuses   = []string{"open", "pending", "on_hold", "solved", "closed"}
	priorities = []string{"low", "normal", "high", "urgent"}
	severities = []string{"sev1", "sev2", "sev3", "sev4"}
	channels   = []string{"email", "chat", "api", "portal"}
)

const ticketArg = "The ticket: its number like #123 (or 123), or its id."

// --- API shapes ----------------------------------------------------------------------

type page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type userRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type contactRef struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Email *string `json:"email"`
}

type tagRef struct {
	Name string `json:"name"`
}

// apiTicket is the part of the API Ticket the tools read.
type apiTicket struct {
	ID               string         `json:"id"`
	Number           int64          `json:"number"`
	Subject          string         `json:"subject"`
	Status           string         `json:"status"`
	Priority         string         `json:"priority"`
	Severity         *string        `json:"severity"`
	Channel          string         `json:"channel"`
	MailboxID        *string        `json:"mailbox_id"`
	Contact          *contactRef    `json:"contact"`
	Assignee         *userRef       `json:"assignee"`
	TeamID           *string        `json:"team_id"`
	BrandID          string         `json:"brand_id"`
	ReceivedAt       string         `json:"received_at"`
	Tags             []tagRef       `json:"tags"`
	Fields           map[string]any `json:"fields"`
	SLA              *apiSLA        `json:"sla"`
	FirstRespondedAt *time.Time     `json:"first_responded_at"`
	SolvedAt         *time.Time     `json:"solved_at"`
	SnoozedUntil     *time.Time     `json:"snoozed_until"`
	LastMessageAt    time.Time      `json:"last_message_at"`
	Preview          string         `json:"last_message_preview"`
	MessageCount     int            `json:"message_count"`
	Spam             bool           `json:"spam"`
	CSAT             *struct {
		Score   int    `json:"score"`
		Comment string `json:"comment"`
	} `json:"csat"`
	Custom    map[string]any `json:"custom"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type apiSLA struct {
	FirstResponseDueAt *time.Time `json:"first_response_due_at"`
	ResolutionDueAt    *time.Time `json:"resolution_due_at"`
	Breached           bool       `json:"breached"`
}

// --- compact outputs -----------------------------------------------------------------

type outPerson struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type outSLA struct {
	FirstResponseDueAt *time.Time `json:"first_response_due_at,omitempty"`
	ResolutionDueAt    *time.Time `json:"resolution_due_at,omitempty"`
	Breached           bool       `json:"breached"`
}

type outTicket struct {
	ID       string     `json:"id"`
	Number   int64      `json:"number"`
	Subject  string     `json:"subject"`
	Status   string     `json:"status"`
	Priority string     `json:"priority"`
	Severity *string    `json:"severity,omitempty"`
	Channel  string     `json:"channel"`
	Contact  *outPerson `json:"contact,omitempty"`
	Assignee *outPerson `json:"assignee,omitempty"`
	TeamID   *string    `json:"team_id,omitempty"`
	Team     string     `json:"team,omitempty"`
	BrandID  string     `json:"brand_id,omitempty"`
	Brand    string     `json:"brand,omitempty"`
	// ReceivedAt is the address the customer wrote to; ReplyFrom the address replies go out from.
	ReceivedAt    string         `json:"received_at,omitempty"`
	ReplyFrom     *outAddress    `json:"reply_from,omitempty"`
	Labels        []string       `json:"labels,omitempty"`
	SLA           *outSLA        `json:"sla,omitempty"`
	Fields        map[string]any `json:"fields,omitempty"`
	Spam          bool           `json:"spam,omitempty"`
	CSAT          any            `json:"csat,omitempty"`
	Summary       string         `json:"ai_summary,omitempty"`
	SnoozedUntil  *time.Time     `json:"snoozed_until,omitempty"`
	MessageCount  int            `json:"message_count"`
	LastMessage   string         `json:"last_message_preview,omitempty"`
	LastMessageAt time.Time      `json:"last_message_at"`
	CreatedAt     time.Time      `json:"created_at"`
	SolvedAt      *time.Time     `json:"solved_at,omitempty"`
}

type outAddress struct {
	MailboxID string `json:"mailbox_id"`
	Email     string `json:"email,omitempty"`
}

// compactTicket keeps the fields an AI client acts on. full adds custom fields, CSAT
// and the stored summary.
func compactTicket(t apiTicket, full bool) outTicket {
	o := outTicket{ID: t.ID, Number: t.Number, Subject: t.Subject, Status: t.Status, Priority: t.Priority, Severity: t.Severity,
		Channel: t.Channel, TeamID: t.TeamID, BrandID: t.BrandID, ReceivedAt: t.ReceivedAt, Spam: t.Spam, SnoozedUntil: t.SnoozedUntil,
		MessageCount: t.MessageCount, LastMessage: clip(t.Preview, 200), LastMessageAt: t.LastMessageAt, CreatedAt: t.CreatedAt, SolvedAt: t.SolvedAt}
	if t.MailboxID != nil {
		o.ReplyFrom = &outAddress{MailboxID: *t.MailboxID}
	}
	if t.Contact != nil {
		o.Contact = &outPerson{ID: t.Contact.ID, Name: t.Contact.Name}
		if t.Contact.Email != nil {
			o.Contact.Email = *t.Contact.Email
		}
	}
	if t.Assignee != nil {
		o.Assignee = &outPerson{ID: t.Assignee.ID, Name: t.Assignee.Name, Email: t.Assignee.Email}
	}
	for _, tg := range t.Tags {
		o.Labels = append(o.Labels, tg.Name)
	}
	if t.SLA != nil {
		o.SLA = &outSLA{FirstResponseDueAt: t.SLA.FirstResponseDueAt, ResolutionDueAt: t.SLA.ResolutionDueAt, Breached: t.SLA.Breached}
		if t.FirstRespondedAt != nil {
			o.SLA.FirstResponseDueAt = nil
		}
	}
	if full {
		if len(t.Fields) > 0 {
			o.Fields = t.Fields
		}
		if t.CSAT != nil {
			o.CSAT = t.CSAT
		}
		if s, ok := t.Custom["ai_summary"].(map[string]any); ok {
			if text, ok := s["text"].(string); ok {
				o.Summary = clip(text, 1000)
			}
		}
	}
	return o
}

// --- names ---------------------------------------------------------------------------

// refNames are the team and brand names of the caller's workspace, read once per tool
// call. The lists need settings:read for an API key; without it ticket outputs keep the
// ids only.
type refNames struct {
	loaded        bool
	teams, brands map[string]string
}

// listNames reads a list route's {id, name} pairs, from a page or a bare array.
func listNames(ctx context.Context, c *caller, path string) map[string]string {
	var raw json.RawMessage
	if err := c.get(ctx, path, url.Values{"limit": {"100"}}, &raw); err != nil {
		return nil
	}
	type named struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var list []named
	if err := json.Unmarshal(raw, &list); err != nil {
		var pg page[named]
		if json.Unmarshal(raw, &pg) != nil {
			return nil
		}
		list = pg.Data
	}
	out := make(map[string]string, len(list))
	for _, n := range list {
		out[n.ID] = n.Name
	}
	return out
}

// name fills in the team and brand names of tickets.
func (c *caller) name(ctx context.Context, ts ...*outTicket) {
	if c.names == nil {
		c.names = &refNames{}
	}
	n := c.names
	if !n.loaded {
		n.loaded = true
		n.teams = listNames(ctx, c, "/v1/teams")
		n.brands = listNames(ctx, c, "/v1/brands")
	}
	for _, t := range ts {
		if t.TeamID != nil {
			t.Team = n.teams[*t.TeamID]
		}
		t.Brand = n.brands[t.BrandID]
	}
}

// nameAll fills in the team and brand names of a list of tickets.
func (c *caller) nameAll(ctx context.Context, ts []outTicket) {
	if len(ts) == 0 {
		return
	}
	ptrs := make([]*outTicket, len(ts))
	for i := range ts {
		ptrs[i] = &ts[i]
	}
	c.name(ctx, ptrs...)
}

// replyAddress fills in the email address replies go out from, when the caller may read it.
func (c *caller) replyAddress(ctx context.Context, t *outTicket) {
	if t.ReplyFrom == nil {
		return
	}
	var m struct {
		Email string `json:"email"`
	}
	if err := c.get(ctx, "/v1/mailboxes/"+url.PathEscape(t.ReplyFrom.MailboxID), nil, &m); err == nil {
		t.ReplyFrom.Email = m.Email
	}
}
