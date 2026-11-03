package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/httpx"
)

func writeTools() []*tool {
	changeProps := map[string]any{
		"status":        enum("New status.", statuses...),
		"priority":      enum("New priority.", priorities...),
		"severity":      enum("New severity: sev1 critical, sev2 major, sev3 minor, sev4 minimal, or none to clear.", append(append([]string{}, severities...), "none")...),
		"assignee":      str(`Assign to "me", "none" to unassign, or a teammate's name, email or user id.`),
		"team":          str(`Move to a team by name or id, or "none" to remove the team.`),
		"add_labels":    arr("Label names to add.", str("")),
		"remove_labels": arr("Label names to remove.", str("")),
	}
	singleProps := merge(map[string]any{
		"ticket": str("The ticket number like #123, or its id."),
		"fields": map[string]any{"type": "object", "description": "Custom field values by key; null clears a field. Use list_labels_and_fields for keys and option ids."},
	}, changeProps)
	bulkChanges := obj(merge(map[string]any{}, changeProps))
	return []*tool{
		{
			Name: "update_ticket", Write: true, MinRole: auth.RoleAgent,
			Description: "Change one ticket: status, priority, severity, assignee, team, labels or custom fields. Needs the user's approval, which the panel asks for; just call it.",
			Schema:      obj(singleProps, "ticket"),
			Active:      "Preparing a ticket change", Done: "Updated the ticket",
			Propose: proposeUpdate, Apply: applyUpdate,
		},
		{
			Name: "bulk_update_tickets", Write: true, MinRole: auth.RoleAgent,
			Description: fmt.Sprintf("Apply the same change to many tickets: the given ticket_ids, or every ticket matching filter (at most %d per approval). "+
				"Needs the user's approval, which shows the exact tickets.", BulkCap),
			Schema: obj(map[string]any{
				"ticket_ids": arr("Ticket numbers like #123 or ids.", str("")),
				"filter":     obj(ticketFilterProps()),
				"changes":    bulkChanges,
			}, "changes"),
			Active: "Preparing a bulk change", Done: "Updated tickets",
			Propose: proposeBulk, Apply: applyBulk,
		},
		{
			Name: "add_note", Write: true, MinRole: auth.RoleAgent,
			Description: "Add an internal note (never shown to the customer) to a ticket. Markdown body. Needs the user's approval.",
			Schema:      obj(map[string]any{"ticket": str("The ticket number like #123, or its id."), "body": str("The note, in light markdown.")}, "ticket", "body"),
			Active:      "Preparing a note", Done: "Added a note",
			Propose: proposeNote, Apply: applyMessage("note"),
		},
		{
			Name: "draft_reply", Write: true, MinRole: auth.RoleAgent,
			Description: "Write a reply to the customer on a ticket. The user sees the full draft and sends it by approving; nothing is sent without that. " +
				"Optionally set the status after sending (e.g. pending while waiting on the customer). Markdown body, no signature.",
			Schema: obj(map[string]any{
				"ticket": str("The ticket number like #123, or its id."),
				"body":   str("The reply, in light markdown."),
				"status": enum("Status to set after sending.", "open", "pending", "on_hold", "solved"),
			}, "ticket", "body"),
			Active: "Drafting a reply", Done: "Sent the reply",
			Propose: proposeReply, Apply: applyMessage("reply"),
		},
		{
			Name: "create_kb_draft", Write: true, MinRole: auth.RoleAgent,
			Description: "Create a knowledge base article as an unpublished draft. Markdown body. Needs the user's approval.",
			Schema:      obj(map[string]any{"title": str("Article title."), "body": str("Article body in light markdown.")}, "title", "body"),
			Active:      "Drafting an article", Done: "Created a draft article",
			Propose: proposeKB, Apply: applyKB,
		},
		{
			Name: "create_view", Write: true, MinRole: auth.RoleAgent,
			Description: "Save a personal ticket view with filters. Needs the user's approval.",
			Schema: obj(map[string]any{
				"name": str("View name."),
				"filters": obj(map[string]any{
					"status":   arr("", enum("", statuses...)),
					"priority": arr("", enum("", priorities...)),
					"assignee": str(`"me", "none" or a teammate.`),
					"label":    str("Label name."),
					"channel":  enum("", "email", "chat", "api", "portal", "phone"),
					"query":    str("Full-text search."),
				}),
			}, "name", "filters"),
			Active: "Preparing a view", Done: "Created a view",
			Propose: proposeView, Apply: applyView,
		},
		{
			Name: "create_automation_draft", Write: true, MinRole: auth.RoleAdmin,
			Description: "Create an automation, switched off, for an admin to review and enable. Needs the user's approval.",
			Schema: obj(map[string]any{
				"name":    str("Automation name."),
				"trigger": enum("When it runs.", "ticket_created", "customer_replied", "agent_replied", "ticket_updated", "call_ended"),
				"conditions": arr("All must match.", obj(map[string]any{
					"field": str("subject, body, from_email, received_at, channel, mailbox_id, priority, severity, status, tag, sentiment or field.<key>; on call_ended also call_outcome (answered, missed, voicemail), call_direction (inbound, outbound), call_missed_reason, call_time (business_hours, outside_business_hours), call_line (equals: a line id from GET /v1/phone/lines; contains: words of its name) or call_tag"),
					"op":    enum("any and none on subject, body, from_email, received_at or tag: the text contains any, or none, of the phrases in value.", "contains", "not_contains", "equals", "not_equals", "gt", "gte", "lt", "lte", "any", "all", "none", "set", "not_set"),
					"value": str("The value to compare; for any and none on a text field, up to 100 phrases, one per line."),
				}, "field", "op", "value")),
				"actions": arr("What it does.", obj(map[string]any{
					"type":  enum("", "set_status", "set_priority", "set_severity", "assign_user", "assign_team", "add_tag", "remove_tag", "add_note", "ai_triage", "set_field"),
					"value": str("The value: a status, priority, severity, user id, team id, label name, note text or field value."),
					"field": str("The custom field key, for set_field."),
				}, "type", "value")),
			}, "name", "trigger", "actions"),
			Active: "Preparing an automation", Done: "Created an automation",
			Propose: proposeAutomation, Apply: applyAutomation,
		},
	}
}

// --- ticket changes -------------------------------------------------------------------

type ticketChanges struct {
	Status       string         `json:"status,omitempty"`
	Priority     string         `json:"priority,omitempty"`
	Severity     string         `json:"severity,omitempty"`
	Assignee     string         `json:"assignee,omitempty"`
	Team         string         `json:"team,omitempty"`
	AddLabels    []string       `json:"add_labels,omitempty"`
	RemoveLabels []string       `json:"remove_labels,omitempty"`
	Fields       map[string]any `json:"fields,omitempty"`
}

// patchArgs is a resolved PATCH body, stored with the action and sent as is.
type patchArgs struct {
	Status       string         `json:"status,omitempty"`
	Priority     string         `json:"priority,omitempty"`
	Severity     string         `json:"severity,omitempty" doc:"sev1..sev4, or none to clear"`
	AssigneeSet  bool           `json:"assignee_set,omitempty"`
	AssigneeID   string         `json:"assignee_id,omitempty"`
	AssigneeName string         `json:"assignee_name,omitempty"`
	TeamSet      bool           `json:"team_set,omitempty"`
	TeamID       string         `json:"team_id,omitempty"`
	TeamName     string         `json:"team_name,omitempty"`
	AddTags      []string       `json:"add_tags,omitempty"`
	RemoveTags   []string       `json:"remove_tags,omitempty"`
	Fields       map[string]any `json:"fields,omitempty"`
}

func (p patchArgs) body() map[string]any {
	b := map[string]any{}
	if p.Status != "" {
		b["status"] = p.Status
	}
	if p.Priority != "" {
		b["priority"] = p.Priority
	}
	switch p.Severity {
	case "":
	case "none":
		b["severity"] = nil
	default:
		b["severity"] = p.Severity
	}
	if p.AssigneeSet {
		if p.AssigneeID == "" {
			b["assignee_id"] = nil
		} else {
			b["assignee_id"] = p.AssigneeID
		}
	}
	if p.TeamSet {
		if p.TeamID == "" {
			b["team_id"] = nil
		} else {
			b["team_id"] = p.TeamID
		}
	}
	if len(p.AddTags) > 0 {
		b["add_tags"] = p.AddTags
	}
	if len(p.RemoveTags) > 0 {
		b["remove_tags"] = p.RemoveTags
	}
	if len(p.Fields) > 0 {
		b["fields"] = p.Fields
	}
	return b
}

func cleanLabels(in []string) []string {
	var out []string
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" || utf8.RuneCountInString(l) > 50 || contains(out, l) {
			continue
		}
		out = append(out, l)
	}
	return out
}

func (rc *runCtx) resolveChanges(ctx context.Context, c ticketChanges, allowFields bool) (patchArgs, error) {
	var p patchArgs
	if c.Status != "" {
		if err := validEnum([]string{c.Status}, statuses, "status"); err != nil {
			return p, err
		}
		p.Status = c.Status
	}
	if c.Priority != "" {
		if err := validEnum([]string{c.Priority}, priorities, "priority"); err != nil {
			return p, err
		}
		p.Priority = c.Priority
	}
	if c.Severity != "" && !rc.noSeverity {
		if err := validEnum([]string{c.Severity}, append(append([]string{}, severities...), "none"), "severity"); err != nil {
			return p, err
		}
		p.Severity = c.Severity
	}
	if strings.TrimSpace(c.Assignee) != "" {
		id, name, err := rc.resolveAssignee(ctx, c.Assignee)
		if err != nil {
			return p, err
		}
		p.AssigneeSet, p.AssigneeID, p.AssigneeName = true, id, name
	}
	if strings.TrimSpace(c.Team) != "" {
		id, name, err := rc.resolveTeam(ctx, c.Team)
		if err != nil {
			return p, err
		}
		p.TeamSet, p.TeamID, p.TeamName = true, id, name
	}
	p.AddTags, p.RemoveTags = cleanLabels(c.AddLabels), cleanLabels(c.RemoveLabels)
	if allowFields && len(c.Fields) > 0 {
		if len(c.Fields) > 20 {
			return p, httpx.Validation("at most 20 fields at once")
		}
		p.Fields = c.Fields
	}
	if len(p.body()) == 0 {
		return p, httpx.Validation("there is nothing to change")
	}
	return p, nil
}

func show(v any) string {
	switch x := v.(type) {
	case nil:
		return "empty"
	case string:
		if x == "" {
			return "empty"
		}
		return x
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
}

func labelNames(ts []tagRef) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

// diff describes a patch against a ticket's current values. For many tickets
// (t == nil) the from side is "current".
func (p patchArgs) diff(t *ticket) []Change {
	var out []Change
	from := func(get func(ticket) string) string {
		if t == nil {
			return "current"
		}
		return get(*t)
	}
	if p.Status != "" {
		out = append(out, Change{Field: "Status", From: from(func(t ticket) string { return t.Status }), To: p.Status})
	}
	if p.Priority != "" {
		out = append(out, Change{Field: "Priority", From: from(func(t ticket) string { return t.Priority }), To: p.Priority})
	}
	if p.Severity != "" {
		out = append(out, Change{Field: "Severity", From: from(func(t ticket) string { return severityText(t.Severity) }), To: p.Severity})
	}
	if p.AssigneeSet {
		out = append(out, Change{Field: "Assignee", From: from(func(t ticket) string {
			if t.Assignee == nil {
				return "Unassigned"
			}
			return t.Assignee.Name
		}), To: p.AssigneeName})
	}
	if p.TeamSet {
		out = append(out, Change{Field: "Team", From: from(func(t ticket) string {
			switch {
			case t.TeamID == nil:
				return "No team"
			case t.teamName != "":
				return t.teamName
			}
			return *t.TeamID
		}), To: p.TeamName})
	}
	if len(p.AddTags)+len(p.RemoveTags) > 0 {
		var to []string
		for _, l := range p.AddTags {
			to = append(to, "+"+l)
		}
		for _, l := range p.RemoveTags {
			to = append(to, "−"+l)
		}
		out = append(out, Change{Field: "Labels", From: from(func(t ticket) string {
			if len(t.Tags) == 0 {
				return "none"
			}
			return strings.Join(labelNames(t.Tags), ", ")
		}), To: strings.Join(to, ", ")})
	}
	keys := make([]string, 0, len(p.Fields))
	for k := range p.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, Change{Field: "Field " + k, From: from(func(t ticket) string { return show(t.Fields[k]) }), To: show(p.Fields[k])})
	}
	return out
}

type updateArgs struct {
	TicketID string    `json:"ticket_id"`
	Number   int64     `json:"number"`
	Patch    patchArgs `json:"patch"`
}

func proposeUpdate(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
	a, err := decode[struct {
		Ticket string `json:"ticket"`
		ticketChanges
	}](in)
	if err != nil {
		return nil, Preview{}, err
	}
	t, err := rc.getTicket(ctx, a.Ticket)
	if err != nil {
		return nil, Preview{}, err
	}
	p, err := rc.resolveChanges(ctx, a.ticketChanges, true)
	if err != nil {
		return nil, Preview{}, err
	}
	// Values the ticket already has are not changes.
	if p.Status == t.Status {
		p.Status = ""
	}
	if p.Priority == t.Priority {
		p.Priority = ""
	}
	if p.Severity == severityText(t.Severity) {
		p.Severity = ""
	}
	if p.AssigneeSet && ((t.Assignee == nil && p.AssigneeID == "") || (t.Assignee != nil && t.Assignee.ID == p.AssigneeID)) {
		p.AssigneeSet = false
	}
	if p.TeamSet && ((t.TeamID == nil && p.TeamID == "") || (t.TeamID != nil && *t.TeamID == p.TeamID)) {
		p.TeamSet = false
	}
	changes := p.diff(&t)
	if len(changes) == 0 {
		return nil, Preview{}, httpx.Validation(fmt.Sprintf("#%d already has those values; nothing to change", t.Number))
	}
	pv := Preview{Title: fmt.Sprintf("Update #%d", t.Number), Summary: t.Subject, Changes: changes, Targets: []Ref{t.ref()}}
	return updateArgs{TicketID: t.ID, Number: t.Number, Patch: p}, pv, nil
}

func applyUpdate(ctx context.Context, rc *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
	a, err := decode[updateArgs](raw)
	if err != nil {
		return output{}, err
	}
	var t ticket
	if err := w.do(ctx, http.MethodPatch, "/v1/tickets/"+a.TicketID, nil, a.Patch.body(), &t, ""); err != nil {
		return output{}, err
	}
	if rc != nil {
		rc.describe(ctx, &t)
	}
	return output{Content: block("update_ticket", "Updated: "+t.line()), Summary: fmt.Sprintf("Updated #%d", t.Number), Refs: []Ref{t.ref()}}, nil
}

type bulkArgs struct {
	IDs     []string  `json:"ids"`
	Targets []Ref     `json:"targets"`
	Patch   patchArgs `json:"patch"`
}

func proposeBulk(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
	a, err := decode[struct {
		TicketIDs []string      `json:"ticket_ids"`
		Filter    *ticketFilter `json:"filter"`
		Changes   ticketChanges `json:"changes"`
	}](in)
	if err != nil {
		return nil, Preview{}, err
	}
	p, err := rc.resolveChanges(ctx, a.Changes, false)
	if err != nil {
		return nil, Preview{}, err
	}
	var ts []ticket
	total := 0
	switch {
	case len(a.TicketIDs) > 0:
		if len(a.TicketIDs) > BulkCap {
			return nil, Preview{}, httpx.Validation(fmt.Sprintf("at most %d tickets per approval", BulkCap))
		}
		for _, raw := range a.TicketIDs {
			t, err := rc.getTicket(ctx, raw)
			if err != nil {
				return nil, Preview{}, err
			}
			ts = append(ts, t)
		}
		total = len(ts)
	case a.Filter != nil && !a.Filter.empty():
		if ts, total, err = rc.findTickets(ctx, *a.Filter, BulkCap, 1000); err != nil {
			return nil, Preview{}, err
		}
	default:
		return nil, Preview{}, httpx.Validation("give ticket_ids or a filter")
	}
	if len(ts) == 0 {
		return nil, Preview{}, httpx.Validation("no tickets match")
	}
	args := bulkArgs{Patch: p}
	seen := map[string]bool{}
	for _, t := range ts {
		if seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		args.IDs = append(args.IDs, t.ID)
		args.Targets = append(args.Targets, t.ref())
	}
	pv := Preview{Title: "Update " + plural(len(args.IDs), "ticket", "tickets"), Changes: p.diff(nil), Targets: args.Targets, Total: total}
	if total > len(args.IDs) {
		pv.Summary = fmt.Sprintf("%d tickets match; this changes the first %d. Ask again for the rest.", total, len(args.IDs))
	}
	return args, pv, nil
}

func applyBulk(ctx context.Context, _ *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
	a, err := decode[bulkArgs](raw)
	if err != nil {
		return output{}, err
	}
	var res struct {
		Updated int `json:"updated"`
	}
	if err := w.do(ctx, http.MethodPost, "/v1/tickets/bulk", nil, map[string]any{"ids": a.IDs, "patch": a.Patch.body()}, &res, key); err != nil {
		return output{}, err
	}
	sum := "Updated " + plural(res.Updated, "ticket", "tickets")
	return output{Content: block("bulk_update_tickets", sum+" of "+plural(len(a.IDs), "ticket", "tickets")+" requested."), Summary: sum, Refs: a.Targets}, nil
}

// --- messages ------------------------------------------------------------------------

type messageArgs struct {
	TicketID string  `json:"ticket_id"`
	Number   int64   `json:"number"`
	Subject  string  `json:"subject"`
	BodyHTML string  `json:"body_html"`
	Status   *string `json:"status,omitempty"`
}

func proposeMessage(ctx context.Context, rc *runCtx, in json.RawMessage, kind string) (any, Preview, error) {
	a, err := decode[struct {
		Ticket string `json:"ticket"`
		Body   string `json:"body"`
		Status string `json:"status"`
	}](in)
	if err != nil {
		return nil, Preview{}, err
	}
	body := strings.TrimSpace(a.Body)
	if kind == "reply" {
		body = ai.TidyReply(body)
	}
	if body == "" {
		return nil, Preview{}, httpx.Validation("the body is empty")
	}
	if utf8.RuneCountInString(body) > 20000 {
		return nil, Preview{}, httpx.Validation("the body is too long")
	}
	t, err := rc.getTicket(ctx, a.Ticket)
	if err != nil {
		return nil, Preview{}, err
	}
	args := messageArgs{TicketID: t.ID, Number: t.Number, Subject: t.Subject, BodyHTML: ai.MarkdownToHTML(body)}
	pv := Preview{Title: fmt.Sprintf("Add a note to #%d", t.Number), Summary: t.Subject, Body: body, Targets: []Ref{t.ref()}}
	if kind == "reply" {
		to := "the customer"
		if t.Contact != nil {
			to = t.Contact.Name
			if t.Contact.Email != nil && *t.Contact.Email != "" {
				to += " <" + *t.Contact.Email + ">"
			}
		}
		pv.Title = fmt.Sprintf("Send a reply on #%d", t.Number)
		pv.Summary = "To " + to + ": " + t.Subject
		pv.External = true
		if a.Status != "" {
			if err := validEnum([]string{a.Status}, statuses, "status"); err != nil {
				return nil, Preview{}, err
			}
			st := a.Status
			args.Status = &st
			pv.Changes = []Change{{Field: "Status", From: t.Status, To: st}}
		}
	}
	return args, pv, nil
}

func proposeNote(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
	return proposeMessage(ctx, rc, in, "note")
}
func proposeReply(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
	return proposeMessage(ctx, rc, in, "reply")
}

func applyMessage(kind string) func(context.Context, *runCtx, *caller, json.RawMessage, string) (output, error) {
	return func(ctx context.Context, _ *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
		a, err := decode[messageArgs](raw)
		if err != nil {
			return output{}, err
		}
		body := map[string]any{"kind": kind, "body_html": a.BodyHTML}
		if a.Status != nil {
			body["status"] = *a.Status
		}
		if err := w.do(ctx, http.MethodPost, "/v1/tickets/"+a.TicketID+"/messages", nil, body, nil, key); err != nil {
			return output{}, err
		}
		ref := Ref{Kind: "ticket", ID: a.TicketID, Number: a.Number, Label: a.Subject}
		what := "Added a note to"
		if kind == "reply" {
			what = "Sent the reply on"
		}
		sum := fmt.Sprintf("%s #%d", what, a.Number)
		return output{Content: block(kind, sum), Summary: sum, Refs: []Ref{ref}}, nil
	}
}

// --- knowledge base, views, automations ---------------------------------------------

type kbArgs struct {
	Title    string `json:"title"`
	BodyHTML string `json:"body_html"`
}

func proposeKB(_ context.Context, _ *runCtx, in json.RawMessage) (any, Preview, error) {
	a, err := decode[struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}](in)
	if err != nil {
		return nil, Preview{}, err
	}
	title := cleanTitle(a.Title)
	if title == "" || utf8.RuneCountInString(a.Title) > 200 {
		return nil, Preview{}, httpx.Validation("the title must be 1 to 200 characters")
	}
	body := strings.TrimSpace(a.Body)
	if body == "" || utf8.RuneCountInString(body) > 50000 {
		return nil, Preview{}, httpx.Validation("the body must be 1 to 50,000 characters")
	}
	return kbArgs{Title: title, BodyHTML: ai.MarkdownToHTML(body)},
		Preview{Title: "Create a draft article", Summary: title, Body: body, Changes: []Change{{Field: "Status", From: "new", To: "draft"}}}, nil
}

func applyKB(ctx context.Context, _ *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
	a, err := decode[kbArgs](raw)
	if err != nil {
		return output{}, err
	}
	var art struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := w.do(ctx, http.MethodPost, "/v1/kb/articles", nil, map[string]any{"title": a.Title, "body_html": a.BodyHTML, "status": "draft"}, &art, key); err != nil {
		return output{}, err
	}
	return output{Content: block("create_kb_draft", "Created the draft article "+art.Title+"; id "+art.ID), Summary: "Created a draft",
		Refs: []Ref{{Kind: "article", ID: art.ID, Label: art.Title}}}, nil
}

type viewArgs struct {
	Name    string         `json:"name"`
	Filters map[string]any `json:"filters"`
}

func proposeView(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
	a, err := decode[struct {
		Name    string `json:"name"`
		Filters struct {
			Status   []string `json:"status"`
			Priority []string `json:"priority"`
			Assignee string   `json:"assignee"`
			Label    string   `json:"label"`
			Channel  string   `json:"channel"`
			Query    string   `json:"query"`
		} `json:"filters"`
	}](in)
	if err != nil {
		return nil, Preview{}, err
	}
	name := cleanTitle(a.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return nil, Preview{}, httpx.Validation("the view name must be 1 to 100 characters")
	}
	f := a.Filters
	if err := validEnum(f.Status, statuses, "status"); err != nil {
		return nil, Preview{}, err
	}
	if err := validEnum(f.Priority, priorities, "priority"); err != nil {
		return nil, Preview{}, err
	}
	filters := map[string]any{}
	var changes []Change
	if len(f.Status) > 0 {
		filters["status"] = f.Status
		changes = append(changes, Change{Field: "Status", From: "any", To: strings.Join(f.Status, ", ")})
	}
	if len(f.Priority) > 0 {
		filters["priority"] = f.Priority
		changes = append(changes, Change{Field: "Priority", From: "any", To: strings.Join(f.Priority, ", ")})
	}
	if strings.TrimSpace(f.Assignee) != "" {
		v := strings.ToLower(strings.TrimSpace(f.Assignee))
		label := f.Assignee
		switch v {
		case "me", "none":
			filters["assignee"] = v
		default:
			id, n, err := rc.resolveAssignee(ctx, f.Assignee)
			if err != nil {
				return nil, Preview{}, err
			}
			if id == "" {
				filters["assignee"] = "none"
			} else {
				filters["assignee"] = id
			}
			label = n
		}
		changes = append(changes, Change{Field: "Assignee", From: "any", To: label})
	}
	if l := strings.TrimSpace(f.Label); l != "" {
		filters["tag"] = l
		changes = append(changes, Change{Field: "Label", From: "any", To: l})
	}
	if f.Channel != "" {
		filters["channel"] = f.Channel
		changes = append(changes, Change{Field: "Channel", From: "any", To: f.Channel})
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		filters["q"] = clip(q, 200)
		changes = append(changes, Change{Field: "Search", From: "none", To: q})
	}
	if len(filters) == 0 {
		return nil, Preview{}, httpx.Validation("a view needs at least one filter")
	}
	return viewArgs{Name: name, Filters: filters}, Preview{Title: "Create the view " + name, Summary: "A personal view", Changes: changes}, nil
}

func applyView(ctx context.Context, _ *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
	a, err := decode[viewArgs](raw)
	if err != nil {
		return output{}, err
	}
	var v struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := w.do(ctx, http.MethodPost, "/v1/views", nil, map[string]any{"name": a.Name, "filters": a.Filters, "shared": false}, &v, key); err != nil {
		return output{}, err
	}
	return output{Content: block("create_view", "Created the view "+v.Name+"; id "+v.ID), Summary: "Created " + v.Name,
		Refs: []Ref{{Kind: "view", ID: v.ID, Label: v.Name}}}, nil
}

type automationArgs struct {
	Name       string           `json:"name"`
	Trigger    string           `json:"trigger"`
	Conditions []map[string]any `json:"conditions"`
	Actions    []map[string]any `json:"actions"`
}

var triggers = []string{"ticket_created", "customer_replied", "agent_replied", "ticket_updated", "call_ended"}

func proposeAutomation(_ context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
	a, err := decode[struct {
		Name       string `json:"name"`
		Trigger    string `json:"trigger"`
		Conditions []struct {
			Field string `json:"field"`
			Op    string `json:"op"`
			Value string `json:"value"`
		} `json:"conditions"`
		Actions []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
			Field string `json:"field"`
		} `json:"actions"`
	}](in)
	if err != nil {
		return nil, Preview{}, err
	}
	name := cleanTitle(a.Name)
	if name == "" {
		return nil, Preview{}, httpx.Validation("the automation needs a name")
	}
	if err := validEnum([]string{a.Trigger}, triggers, "trigger"); err != nil {
		return nil, Preview{}, err
	}
	if len(a.Actions) == 0 || len(a.Actions) > 10 || len(a.Conditions) > 20 {
		return nil, Preview{}, httpx.Validation("an automation has 1 to 10 actions and at most 20 conditions")
	}
	args := automationArgs{Name: name, Trigger: a.Trigger, Conditions: []map[string]any{}, Actions: []map[string]any{}}
	changes := []Change{{Field: "When", From: "", To: strings.ReplaceAll(a.Trigger, "_", " ")}}
	for _, c := range a.Conditions {
		if rc.noSeverity && c.Field == "severity" {
			return nil, Preview{}, httpx.Validation("This workspace does not use severity.")
		}
		args.Conditions = append(args.Conditions, map[string]any{"field": c.Field, "op": c.Op, "value": c.Value})
		op, value := strings.ReplaceAll(c.Op, "_", " "), c.Value
		if (c.Op == "any" || c.Op == "none") && !strings.Contains(c.Field, "field.") {
			// A phrase list, one per line, reads as one line in the preview.
			var phrases []string
			for _, p := range strings.Split(value, "\n") {
				if p = strings.TrimSpace(p); p != "" {
					phrases = append(phrases, p)
				}
			}
			op, value = "contains "+c.Op+" of", clip(strings.Join(phrases, ", "), 200)
		}
		changes = append(changes, Change{Field: "If", From: "", To: strings.TrimSpace(c.Field + " " + op + " " + value)})
	}
	for _, x := range a.Actions {
		if rc.noSeverity && x.Type == "set_severity" {
			continue
		}
		m := map[string]any{"type": x.Type, "value": x.Value}
		if x.Field != "" {
			m["field"] = x.Field
		}
		args.Actions = append(args.Actions, m)
		changes = append(changes, Change{Field: "Then", From: "", To: strings.TrimSpace(strings.ReplaceAll(x.Type, "_", " ") + " " + x.Field + " " + clip(x.Value, 120))})
	}
	if len(args.Actions) == 0 {
		return nil, Preview{}, httpx.Validation("The automation needs an action this workspace uses.")
	}
	changes = append(changes, Change{Field: "Enabled", From: "", To: "off, until an admin turns it on"})
	return args, Preview{Title: "Create the automation " + name, Summary: "Created switched off", Changes: changes}, nil
}

func applyAutomation(ctx context.Context, _ *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
	a, err := decode[automationArgs](raw)
	if err != nil {
		return output{}, err
	}
	var au struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := w.do(ctx, http.MethodPost, "/v1/automations", nil, map[string]any{"name": a.Name, "trigger": a.Trigger,
		"conditions": a.Conditions, "actions": a.Actions, "enabled": false}, &au, key); err != nil {
		return output{}, err
	}
	return output{Content: block("create_automation_draft", "Created the automation "+au.Name+" (off); id "+au.ID), Summary: "Created " + au.Name,
		Refs: []Ref{{Kind: "automation", ID: au.ID, Label: au.Name}}}, nil
}
