package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/auth"
)

// Limits on what a tool returns, so an answer fits a model's context.
const (
	maxSearchTickets = 50
	threadMessages   = 50
	messageChars     = 4000
	threadChars      = 60000
	maxBodyChars     = 20000
)

func ticketTools() []*tool {
	filterProps := map[string]any{
		"query":          strN("Full-text search over subjects and messages, or a ticket number like #123.", 1, 200),
		"status":         arr("Only these statuses.", enum("", statuses...), 5),
		"priority":       arr("Only these priorities.", enum("", priorities...), 4),
		"severity":       arr("Only these severities; none for tickets without one.", enum("", append(append([]string{}, severities...), "none")...), 5),
		"assignee":       str(`"none" for unassigned tickets, or a teammate's user id or email (an email needs the settings:read scope).`),
		"team":           str("A team id, or a team name (a name needs the settings:read scope)."),
		"label":          strN("A label name.", 1, 50),
		"channel":        enum("Only this channel.", channels...),
		"contact_id":     str("Only this contact's tickets (an id from search_contacts)."),
		"created_after":  str("Only tickets created at or after this day or time: YYYY-MM-DD or RFC 3339."),
		"created_before": str("Only tickets created before the end of this day, or before this RFC 3339 time."),
		"sort":           enum("Order: last_message_at (newest activity first, the default), created_at (newest first), priority (most urgent first) or due (next SLA target first).", "last_message_at", "created_at", "priority", "due"),
		"limit":          integer("How many tickets to return.", 1, maxSearchTickets, 20),
		"cursor":         str("next_cursor from a previous search with the same filters, for the next page. Not used with created_after or created_before."),
	}
	changeProps := map[string]any{
		"ticket":        str(ticketArg),
		"status":        enum("New status. on_hold waits on a third party; solved and closed end the conversation.", statuses...),
		"priority":      enum("New priority.", priorities...),
		"severity":      enum("New severity (sev1 most severe), or none to clear it.", append(append([]string{}, severities...), "none")...),
		"assignee":      str(`Assign to a teammate by user id or email (an email needs the settings:read scope), or "none" to unassign.`),
		"team":          str(`Move to a team by id or name (a name needs the settings:read scope), or "none" to remove the team.`),
		"add_labels":    arr("Label names to add.", strN("", 1, 50), 50),
		"remove_labels": arr("Label names to remove.", strN("", 1, 50), 50),
	}
	return []*tool{
		{
			Name: "search_tickets", Title: "Search tickets", Scopes: []string{auth.ScopeTicketsRead}, ReadOnly: true, Idempotent: true,
			Description: "Find tickets by text, status, priority, severity, assignee, team, label, channel, contact or creation date. " +
				"Returns up to 50 tickets (default 20) with their key fields, and next_cursor when there are more. Spam is excluded.",
			Schema: obj(filterProps),
			Run:    searchTickets,
		},
		{
			Name: "get_ticket", Title: "Get a ticket", Scopes: []string{auth.ScopeTicketsRead}, ReadOnly: true, Idempotent: true,
			Description: "Read one ticket: its fields, team, brand, the address the customer wrote to (received_at) and the one replies go out from " +
				"(reply_from), SLA, labels and its thread as plain text, customer messages, team replies and internal notes " +
				"(kind note) in order. Long threads keep the latest 50 messages.",
			Schema: obj(map[string]any{"ticket": str(ticketArg)}, "ticket"),
			Run:    getTicket,
		},
		{
			Name: "reply_to_ticket", Title: "Reply to a ticket", Scopes: []string{auth.ScopeTicketsWrite}, OpenWorld: true,
			Description: "Send a public reply on a ticket. The customer receives it (by email for email tickets). Write the body in markdown, without a signature. " +
				"Optionally set the ticket's status after sending, for example pending while waiting on the customer.",
			Schema: obj(map[string]any{
				"ticket": str(ticketArg),
				"body":   strN("The reply, in markdown.", 1, maxBodyChars),
				"status": enum("Status to set after the reply is sent.", statuses...),
				"cc":     arr("Extra email addresses to copy (email tickets).", str(""), 20),
			}, "ticket", "body"),
			Run: replyToTicket,
		},
		{
			Name: "draft_reply", Title: "Draft a reply for the team", Scopes: []string{auth.ScopeTicketsWrite}, Idempotent: true,
			Description: "Stage a reply on a ticket without sending it. Nothing reaches the customer: the draft appears in the composer of every " +
				"teammate who opens the ticket, for one of them to review, edit and send as their own reply. Staging again replaces the draft; " +
				"a reply going out clears it. Write the body in markdown, without a signature. Prefer this over reply_to_ticket when a person should check the answer first.",
			Schema: obj(map[string]any{
				"ticket": str(ticketArg),
				"body":   strN("The draft reply, in markdown.", 1, maxBodyChars),
			}, "ticket", "body"),
			Run: draftReply,
		},
		{
			Name: "add_note", Title: "Add an internal note", Scopes: []string{auth.ScopeTicketsWrite},
			Description: "Add an internal note to a ticket. Notes are visible to the team only, never to the customer. Markdown body.",
			Schema: obj(map[string]any{
				"ticket": str(ticketArg),
				"body":   strN("The note, in markdown.", 1, maxBodyChars),
			}, "ticket", "body"),
			Run: addNote,
		},
		{
			Name: "update_ticket", Title: "Update a ticket", Scopes: []string{auth.ScopeTicketsWrite}, Destructive: true, Idempotent: true,
			Description: "Change a ticket's status, priority, severity, assignee, team or labels. Only the fields you pass change. Returns the updated ticket.",
			Schema:      obj(changeProps, "ticket"),
			Run:         updateTicket,
		},
		{
			Name: "create_ticket", Title: "Create a ticket", Scopes: []string{auth.ScopeTicketsWrite}, OpenWorld: true,
			Description: "Create a ticket for a contact by email (the contact is created when new). The body is the first message: " +
				"a reply (default) or an internal note. With mailbox_id the ticket is an email ticket and a reply is emailed to the contact; " +
				"without it the ticket is recorded on the api channel and nothing is sent.",
			Schema: obj(map[string]any{
				"email":      strN("The contact's email address.", 3, 254),
				"name":       strN("The contact's name, used when the contact is new.", 1, 200),
				"subject":    strN("The ticket subject.", 1, 500),
				"body":       strN("The first message, in markdown.", 1, maxBodyChars),
				"kind":       enum("Whether the body is a reply to the contact (default) or an internal note.", "reply", "note"),
				"mailbox_id": str("Send from this mailbox: makes an email ticket and emails a reply to the contact."),
				"status":     enum("Initial status (default open).", statuses...),
				"priority":   enum("Initial priority (default normal).", priorities...),
				"severity":   enum("Initial severity.", severities...),
				"assignee":   str("Assign to a teammate by user id or email (an email needs the settings:read scope)."),
				"team":       str("A team id, or a team name (a name needs the settings:read scope)."),
				"labels":     arr("Label names.", strN("", 1, 50), 50),
			}, "email", "subject"),
			Run: createTicket,
		},
	}
}

// --- reads ---------------------------------------------------------------------------

type ticketFilter struct {
	Query         string   `json:"query"`
	Status        []string `json:"status"`
	Priority      []string `json:"priority"`
	Severity      []string `json:"severity"`
	Assignee      string   `json:"assignee"`
	Team          string   `json:"team"`
	Label         string   `json:"label"`
	Channel       string   `json:"channel"`
	ContactID     string   `json:"contact_id"`
	CreatedAfter  string   `json:"created_after"`
	CreatedBefore string   `json:"created_before"`
	Sort          string   `json:"sort"`
	Limit         int      `json:"limit"`
	Cursor        string   `json:"cursor"`
}

type ticketList struct {
	Tickets    []outTicket `json:"tickets"`
	Count      int         `json:"count"`
	NextCursor *string     `json:"next_cursor,omitempty"`
	Note       string      `json:"note,omitempty"`
}

// scanMax bounds how many tickets a date-filtered search reads.
const scanMax = 500

func searchTickets(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	f, err := decode[ticketFilter](raw)
	if err != nil {
		return nil, err
	}
	if err := allOf(f.Status, "status", statuses...); err != nil {
		return nil, err
	}
	if err := allOf(f.Priority, "priority", priorities...); err != nil {
		return nil, err
	}
	if err := allOf(f.Severity, "severity", append(append([]string{}, severities...), "none")...); err != nil {
		return nil, err
	}
	if err := oneOf(f.Channel, "channel", channels...); err != nil {
		return nil, err
	}
	if err := oneOf(f.Sort, "sort", "last_message_at", "created_at", "priority", "due"); err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > maxSearchTickets {
		limit = maxSearchTickets
	}
	q := url.Values{}
	if s := strings.TrimSpace(f.Query); s != "" {
		q.Set("q", clip(s, 200))
	}
	if len(f.Status) > 0 {
		q.Set("status", strings.Join(f.Status, ","))
	}
	if len(f.Priority) > 0 {
		q.Set("priority", strings.Join(f.Priority, ","))
	}
	if len(f.Severity) > 0 {
		q.Set("severity", strings.Join(f.Severity, ","))
	}
	if strings.TrimSpace(f.Assignee) != "" {
		id, err := resolveAssignee(ctx, c, f.Assignee)
		if err != nil {
			return nil, err
		}
		if id == "" {
			id = "none"
		}
		q.Set("assignee", id)
	}
	if strings.TrimSpace(f.Team) != "" {
		id, err := resolveTeam(ctx, c, f.Team)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, invalid(`team must be a team id or name`)
		}
		q.Set("team_id", id)
	}
	if l := strings.TrimSpace(f.Label); l != "" {
		q.Set("tag", l)
	}
	if f.Channel != "" {
		q.Set("channel", f.Channel)
	}
	if f.ContactID != "" {
		id, err := idRef(f.ContactID, "contact_id")
		if err != nil {
			return nil, err
		}
		q.Set("contact_id", id)
	}
	after, err := day(f.CreatedAfter, "created_after", false)
	if err != nil {
		return nil, err
	}
	before, err := day(f.CreatedBefore, "created_before", true)
	if err != nil {
		return nil, err
	}
	if f.Sort != "" {
		q.Set("sort", f.Sort)
	}

	if after == nil && before == nil {
		q.Set("limit", strconv.Itoa(limit))
		if f.Cursor != "" {
			q.Set("cursor", clip(f.Cursor, 500))
		}
		var pg page[apiTicket]
		if err := c.get(ctx, "/v1/tickets", q, &pg); err != nil {
			return nil, err
		}
		out := ticketList{Tickets: []outTicket{}, NextCursor: pg.NextCursor}
		for _, t := range pg.Data {
			out.Tickets = append(out.Tickets, compactTicket(t, false))
		}
		out.Count = len(out.Tickets)
		c.nameAll(ctx, out.Tickets)
		return out, nil
	}

	// Date filters: newest created first, read until the window is passed.
	if f.Sort != "" && f.Sort != "created_at" {
		return nil, invalid("created_after and created_before sort by created_at")
	}
	q.Set("sort", "created_at")
	q.Set("limit", "100")
	out := ticketList{Tickets: []outTicket{}}
	scanned := 0
	for scanned < scanMax {
		var pg page[apiTicket]
		if err := c.get(ctx, "/v1/tickets", q, &pg); err != nil {
			return nil, err
		}
		stop := false
		for _, t := range pg.Data {
			scanned++
			if after != nil && t.CreatedAt.Before(*after) {
				stop = true
				break
			}
			if before != nil && !t.CreatedAt.Before(*before) {
				continue
			}
			if len(out.Tickets) == limit {
				out.Note = "more tickets match; narrow the dates or filters"
				stop = true
				break
			}
			out.Tickets = append(out.Tickets, compactTicket(t, false))
		}
		if stop || pg.NextCursor == nil || len(pg.Data) == 0 {
			break
		}
		q.Set("cursor", *pg.NextCursor)
	}
	if scanned >= scanMax && out.Note == "" {
		out.Note = "searched the newest " + strconv.Itoa(scanMax) + " tickets in the range; narrow the dates for older ones"
	}
	out.Count = len(out.Tickets)
	c.nameAll(ctx, out.Tickets)
	return out, nil
}

type apiMessage struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Direction string `json:"direction"`
	Author    struct {
		Type    string      `json:"type"`
		User    *userRef    `json:"user"`
		Contact *contactRef `json:"contact"`
		Bot     *struct {
			Name string `json:"name"`
		} `json:"bot"`
	} `json:"author"`
	BodyText    string   `json:"body_text"`
	FromEmail   string   `json:"from_email"`
	FromName    string   `json:"from_name"`
	To          []string `json:"to"`
	Cc          []string `json:"cc"`
	Attachments []struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	} `json:"attachments"`
	DeliveryStatus string    `json:"delivery_status"`
	AI             bool      `json:"ai"`
	CreatedAt      time.Time `json:"created_at"`
	// A deleted message, or a note sent as a reply: a marker the model never reads.
	DeletedAt     *time.Time `json:"deleted_at"`
	SentAsReplyID *string    `json:"sent_as_reply_id"`
}

type outMessage struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Direction   string    `json:"direction"`
	Author      outAuthor `json:"author"`
	At          time.Time `json:"at"`
	Text        string    `json:"text"`
	To          []string  `json:"to,omitempty"`
	Cc          []string  `json:"cc,omitempty"`
	Attachments []string  `json:"attachments,omitempty"`
	Delivery    string    `json:"delivery_status,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"`
}

type outAuthor struct {
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	AI    bool   `json:"ai,omitempty"`
}

type threadOut struct {
	Ticket          outTicket    `json:"ticket"`
	Messages        []outMessage `json:"messages"`
	EarlierOmitted  int          `json:"earlier_messages_omitted,omitempty"`
	TextBudgetSpent bool         `json:"text_truncated,omitempty"`
}

func getTicket(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Ticket string `json:"ticket"`
	}](raw)
	if err != nil {
		return nil, err
	}
	ref, err := ticketRef(a.Ticket)
	if err != nil {
		return nil, err
	}
	var t apiTicket
	if err := c.get(ctx, "/v1/tickets/"+ref, nil, &t); err != nil {
		return nil, err
	}
	var msgs struct {
		Data []apiMessage `json:"data"`
	}
	if err := c.get(ctx, "/v1/tickets/"+t.ID+"/messages", nil, &msgs); err != nil {
		return nil, err
	}
	out := threadOut{Ticket: compactTicket(t, true), Messages: []outMessage{}}
	c.name(ctx, &out.Ticket)
	c.replyAddress(ctx, &out.Ticket)
	// Deleted messages and notes sent as replies stay out, whoever asks (admins read
	// them in the app, never through a model).
	list := slices.DeleteFunc(msgs.Data, func(m apiMessage) bool { return m.DeletedAt != nil || m.SentAsReplyID != nil })
	if len(list) > threadMessages {
		out.EarlierOmitted = len(list) - threadMessages
		list = list[len(list)-threadMessages:]
	}
	// The newest messages matter most: spend the text budget from the end.
	budget := threadChars
	rendered := make([]outMessage, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		m := list[i]
		om := outMessage{ID: m.ID, Kind: m.Kind, Direction: m.Direction, At: m.CreatedAt, Author: authorOf(m)}
		if m.Kind == "reply" && m.Direction == "outbound" {
			om.To, om.Cc = m.To, m.Cc
			if m.DeliveryStatus != "none" {
				om.Delivery = m.DeliveryStatus
			}
		}
		for _, at := range m.Attachments {
			om.Attachments = append(om.Attachments, at.Filename)
		}
		n := min(messageChars, budget)
		text := strings.TrimSpace(m.BodyText)
		if utf8.RuneCountInString(text) > n {
			text, om.Truncated = clip(text, n), true
		}
		budget -= utf8.RuneCountInString(text)
		if budget <= 0 {
			budget = 0
			out.TextBudgetSpent = true
		}
		om.Text = text
		rendered[i] = om
	}
	out.Messages = rendered
	return out, nil
}

func authorOf(m apiMessage) outAuthor {
	a := outAuthor{Type: m.Author.Type, AI: m.AI}
	switch {
	case m.Author.Contact != nil:
		a.Name = m.Author.Contact.Name
		if m.Author.Contact.Email != nil {
			a.Email = *m.Author.Contact.Email
		}
	case m.Author.User != nil:
		a.Name, a.Email = m.Author.User.Name, m.Author.User.Email
	case m.AI || m.Author.Bot != nil:
		// The AI's replies are its bot's (contracts/bots.md), else Ollie's; so are those of
		// an API key that speaks as a bot.
		a.Name = ai.AssistantName
		if m.Author.Bot != nil && m.Author.Bot.Name != "" {
			a.Name = m.Author.Bot.Name
		}
	default:
		a.Name = m.FromName
		if m.Direction == "inbound" {
			a.Email = m.FromEmail
		}
	}
	return a
}

// --- writes --------------------------------------------------------------------------

type messageResult struct {
	MessageID    string `json:"message_id"`
	TicketID     string `json:"ticket_id"`
	TicketNumber int64  `json:"ticket_number,omitempty"`
	Kind         string `json:"kind"`
	Status       string `json:"status,omitempty"`
	Delivery     string `json:"delivery_status,omitempty"`
}

func markdownBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", invalid("body is empty")
	}
	if utf8.RuneCountInString(body) > maxBodyChars {
		return "", invalid("body is longer than 20,000 characters")
	}
	return ai.MarkdownToHTML(body), nil
}

func postMessage(ctx context.Context, c *caller, ticket, kind, body string, status string, cc []string) (any, error) {
	ref, err := ticketRef(ticket)
	if err != nil {
		return nil, err
	}
	html, err := markdownBody(body)
	if err != nil {
		return nil, err
	}
	req := map[string]any{"kind": kind, "body_html": html}
	if status != "" {
		if err := oneOf(status, "status", statuses...); err != nil {
			return nil, err
		}
		req["status"] = status
	}
	if len(cc) > 0 {
		req["cc"] = cc
	}
	var m struct {
		ID             string `json:"id"`
		TicketID       string `json:"ticket_id"`
		Kind           string `json:"kind"`
		DeliveryStatus string `json:"delivery_status"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/tickets/"+ref+"/messages", nil, req, &m); err != nil {
		return nil, err
	}
	out := messageResult{MessageID: m.ID, TicketID: m.TicketID, Kind: m.Kind, Status: status}
	if m.DeliveryStatus != "none" {
		out.Delivery = m.DeliveryStatus
	}
	if n, ok := strings.CutPrefix(ref, "%23"); ok {
		out.TicketNumber, _ = strconv.ParseInt(n, 10, 64)
	}
	return out, nil
}

func replyToTicket(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Ticket string   `json:"ticket"`
		Body   string   `json:"body"`
		Status string   `json:"status"`
		Cc     []string `json:"cc"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if len(a.Cc) > 20 {
		return nil, invalid("at most 20 cc addresses")
	}
	return postMessage(ctx, c, a.Ticket, "reply", a.Body, a.Status, a.Cc)
}

type draftResult struct {
	TicketID     string `json:"ticket_id"`
	TicketNumber int64  `json:"ticket_number,omitempty"`
	By           string `json:"by"`
	Note         string `json:"note"`
}

func draftReply(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Ticket string `json:"ticket"`
		Body   string `json:"body"`
	}](raw)
	if err != nil {
		return nil, err
	}
	ref, err := ticketRef(a.Ticket)
	if err != nil {
		return nil, err
	}
	html, err := markdownBody(a.Body)
	if err != nil {
		return nil, err
	}
	var d struct {
		TicketID string `json:"ticket_id"`
		By       struct {
			Name string `json:"name"`
		} `json:"by"`
	}
	if err := c.do(ctx, http.MethodPut, "/v1/tickets/"+ref+"/draft", nil, map[string]any{"body_html": html}, &d); err != nil {
		return nil, err
	}
	out := draftResult{TicketID: d.TicketID, By: d.By.Name, Note: "staged, not sent; a teammate reviews and sends it from the ticket"}
	if n, ok := strings.CutPrefix(ref, "%23"); ok {
		out.TicketNumber, _ = strconv.ParseInt(n, 10, 64)
	}
	return out, nil
}

func addNote(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Ticket string `json:"ticket"`
		Body   string `json:"body"`
	}](raw)
	if err != nil {
		return nil, err
	}
	return postMessage(ctx, c, a.Ticket, "note", a.Body, "", nil)
}

func cleanLabels(in []string) ([]string, error) {
	if len(in) > 50 {
		return nil, invalid("at most 50 labels")
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" || seen[strings.ToLower(l)] {
			continue
		}
		if utf8.RuneCountInString(l) > 50 {
			return nil, invalid("a label name is at most 50 characters")
		}
		seen[strings.ToLower(l)] = true
		out = append(out, l)
	}
	return out, nil
}

func updateTicket(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Ticket       string   `json:"ticket"`
		Status       string   `json:"status"`
		Priority     string   `json:"priority"`
		Severity     string   `json:"severity"`
		Assignee     string   `json:"assignee"`
		Team         string   `json:"team"`
		AddLabels    []string `json:"add_labels"`
		RemoveLabels []string `json:"remove_labels"`
	}](raw)
	if err != nil {
		return nil, err
	}
	ref, err := ticketRef(a.Ticket)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if a.Status != "" {
		if err := oneOf(a.Status, "status", statuses...); err != nil {
			return nil, err
		}
		body["status"] = a.Status
	}
	if a.Priority != "" {
		if err := oneOf(a.Priority, "priority", priorities...); err != nil {
			return nil, err
		}
		body["priority"] = a.Priority
	}
	switch a.Severity {
	case "":
	case "none":
		body["severity"] = nil
	default:
		if err := oneOf(a.Severity, "severity", severities...); err != nil {
			return nil, err
		}
		body["severity"] = a.Severity
	}
	if strings.TrimSpace(a.Assignee) != "" {
		id, err := resolveAssignee(ctx, c, a.Assignee)
		if err != nil {
			return nil, err
		}
		body["assignee_id"] = nullable(id)
	}
	if strings.TrimSpace(a.Team) != "" {
		id, err := resolveTeam(ctx, c, a.Team)
		if err != nil {
			return nil, err
		}
		body["team_id"] = nullable(id)
	}
	add, err := cleanLabels(a.AddLabels)
	if err != nil {
		return nil, err
	}
	remove, err := cleanLabels(a.RemoveLabels)
	if err != nil {
		return nil, err
	}
	if len(add) > 0 {
		body["add_tags"] = add
	}
	if len(remove) > 0 {
		body["remove_tags"] = remove
	}
	if len(body) == 0 {
		return nil, invalid("nothing to change: pass status, priority, severity, assignee, team, add_labels or remove_labels")
	}
	var t apiTicket
	if err := c.do(ctx, http.MethodPatch, "/v1/tickets/"+ref, nil, body, &t); err != nil {
		return nil, err
	}
	o := compactTicket(t, false)
	c.name(ctx, &o)
	return o, nil
}

func createTicket(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Email     string   `json:"email"`
		Name      string   `json:"name"`
		Subject   string   `json:"subject"`
		Body      string   `json:"body"`
		Kind      string   `json:"kind"`
		MailboxID string   `json:"mailbox_id"`
		Status    string   `json:"status"`
		Priority  string   `json:"priority"`
		Severity  string   `json:"severity"`
		Assignee  string   `json:"assignee"`
		Team      string   `json:"team"`
		Labels    []string `json:"labels"`
	}](raw)
	if err != nil {
		return nil, err
	}
	email := strings.TrimSpace(a.Email)
	if email == "" || !strings.Contains(email, "@") || len(email) > 254 {
		return nil, invalid("email must be an email address")
	}
	subject := strings.TrimSpace(a.Subject)
	if subject == "" || utf8.RuneCountInString(subject) > 500 {
		return nil, invalid("subject must be 1 to 500 characters")
	}
	contact := map[string]any{"email": email}
	if n := strings.TrimSpace(a.Name); n != "" {
		contact["name"] = clip(n, 200)
	}
	body := map[string]any{"subject": subject, "contact": contact}
	if strings.TrimSpace(a.Body) != "" {
		html, err := markdownBody(a.Body)
		if err != nil {
			return nil, err
		}
		body["body_html"] = html
	}
	if err := oneOf(a.Kind, "kind", "reply", "note"); err != nil {
		return nil, err
	}
	if a.Kind != "" {
		body["kind"] = a.Kind
	}
	if a.MailboxID != "" {
		id, err := idRef(a.MailboxID, "mailbox_id")
		if err != nil {
			return nil, err
		}
		body["mailbox_id"] = id
	}
	for field, v := range map[string]struct {
		val     string
		allowed []string
	}{"status": {a.Status, statuses}, "priority": {a.Priority, priorities}, "severity": {a.Severity, severities}} {
		if v.val == "" {
			continue
		}
		if err := oneOf(v.val, field, v.allowed...); err != nil {
			return nil, err
		}
		body[field] = v.val
	}
	if strings.TrimSpace(a.Assignee) != "" {
		id, err := resolveAssignee(ctx, c, a.Assignee)
		if err != nil {
			return nil, err
		}
		if id != "" {
			body["assignee_id"] = id
		}
	}
	if strings.TrimSpace(a.Team) != "" {
		id, err := resolveTeam(ctx, c, a.Team)
		if err != nil {
			return nil, err
		}
		if id != "" {
			body["team_id"] = id
		}
	}
	labels, err := cleanLabels(a.Labels)
	if err != nil {
		return nil, err
	}
	if len(labels) > 0 {
		body["tags"] = labels
	}
	var t apiTicket
	if err := c.do(ctx, http.MethodPost, "/v1/tickets", nil, body, &t); err != nil {
		return nil, err
	}
	o := compactTicket(t, false)
	c.name(ctx, &o)
	return o, nil
}

func nullable(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// --- references ----------------------------------------------------------------------

// resolveAssignee turns "none", a user id, an email or a name into a user id ("" for
// unassigned). Emails and names are looked up through the members list, which needs
// the settings:read scope.
func resolveAssignee(ctx context.Context, c *caller, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	switch strings.ToLower(v) {
	case "none", "unassigned", "nobody", "null":
		return "", nil
	case "me", "myself":
		return "", invalid(`an API key is not a teammate: pass a user id or email instead of "me"`)
	}
	if id, err := uuid.Parse(v); err == nil {
		return id.String(), nil
	}
	type member struct {
		User userRef `json:"user"`
	}
	var all []member
	q := url.Values{"limit": {"100"}}
	for range 10 {
		var pg page[member]
		if err := c.get(ctx, "/v1/members", q, &pg); err != nil {
			if statusOf(err) == http.StatusForbidden {
				return "", invalid("looking up a teammate by email or name needs the settings:read scope; pass their user id instead")
			}
			return "", err
		}
		all = append(all, pg.Data...)
		if pg.NextCursor == nil {
			break
		}
		q.Set("cursor", *pg.NextCursor)
	}
	var hits []member
	for _, m := range all {
		if strings.EqualFold(m.User.Email, v) || strings.EqualFold(m.User.Name, v) {
			return m.User.ID, nil
		}
		if !strings.Contains(v, "@") && strings.Contains(strings.ToLower(m.User.Name), strings.ToLower(v)) {
			hits = append(hits, m)
		}
	}
	if len(hits) == 1 {
		return hits[0].User.ID, nil
	}
	if len(hits) > 1 {
		return "", invalid("more than one teammate matches " + strconv.Quote(clip(v, 100)) + "; use their email or user id")
	}
	return "", invalid("no teammate matches " + strconv.Quote(clip(v, 100)))
}

// resolveTeam turns "none", a team id or a team name into a team id ("" for none).
func resolveTeam(ctx context.Context, c *caller, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if strings.EqualFold(v, "none") {
		return "", nil
	}
	if id, err := uuid.Parse(v); err == nil {
		return id.String(), nil
	}
	var pg page[struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}]
	if err := c.get(ctx, "/v1/teams", url.Values{"limit": {"100"}}, &pg); err != nil {
		if statusOf(err) == http.StatusForbidden {
			return "", invalid("looking up a team by name needs the settings:read scope; pass the team id instead")
		}
		return "", err
	}
	for _, t := range pg.Data {
		if strings.EqualFold(t.Name, v) {
			return t.ID, nil
		}
	}
	return "", invalid("no team is named " + strconv.Quote(clip(v, 100)))
}
