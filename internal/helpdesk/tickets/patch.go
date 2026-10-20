package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// Actor is who a change is attributed to on the timeline.
type Actor struct {
	Type   string // user | api | automation | system | contact
	UserID *uuid.UUID
	// Meta is merged into each activity's data (the automation that acted).
	Meta map[string]any
}

func actorOf(p *auth.Principal) Actor {
	if p.Kind == auth.KindUser && p.Assistant {
		uid := p.UserID
		return Actor{Type: "ai", UserID: &uid}
	}
	if p.Kind == auth.KindUser {
		uid := p.UserID
		return Actor{Type: "user", UserID: &uid}
	}
	return Actor{Type: "api"}
}

var systemActor = Actor{Type: "system"}

// Patch is a set of ticket changes. Nil and unset fields are left alone.
type Patch struct {
	Subject  *string
	Status   *string
	Priority *string
	Severity Opt[string] // null or "" clears
	Contact  Opt[uuid.UUID]
	Assignee Opt[uuid.UUID]
	Team     Opt[uuid.UUID]
	// Mailbox is the reply address: an address of the ticket's brand.
	Mailbox *uuid.UUID
	// Brand moves the ticket to another brand; the reply address follows it.
	Brand      *uuid.UUID
	Snooze     Opt[time.Time]
	Spam       *bool
	Tags       *[]string // replaces the tag set, by name
	AddTags    []string
	RemoveTags []string
	Custom     map[string]any // shallow merge; a null value removes the key
	Fields     map[string]any // custom field values, merged; null clears
	// CreateLabels lets AddTags/Tags create missing labels (settings.label_creation).
	CreateLabels bool
}

func (p Patch) empty() bool {
	return p.Subject == nil && p.Status == nil && p.Priority == nil && !p.Severity.Set && !p.Contact.Set && !p.Assignee.Set && !p.Team.Set && !p.Snooze.Set &&
		p.Mailbox == nil && p.Brand == nil && p.Spam == nil && p.Tags == nil && len(p.AddTags) == 0 && len(p.RemoveTags) == 0 && p.Custom == nil && p.Fields == nil
}

// checkCaller applies the checks that depend on who asks: the reply address must be one
// the caller may send from (an address of a private team is for its members only).
func (p Patch) checkCaller(ctx context.Context, q db.Querier, pr *auth.Principal, tid uuid.UUID) error {
	if p.Mailbox == nil {
		return nil
	}
	if err := hd.CheckRef(ctx, q, pr.WorkspaceID, "mailbox", *p.Mailbox); err != nil {
		return err
	}
	ok, err := domain.CanSendFrom(ctx, q, pr, *p.Mailbox)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.E(403, "forbidden", "Only members of its team can send from this address.")
	}
	return nil
}

type ticketState struct {
	Number   int64
	Subject  string
	Status   string
	Priority string
	Severity *string
	Contact  *uuid.UUID
	Assignee *uuid.UUID
	Team     *uuid.UUID
	Snoozed  *time.Time
	Spam     bool
	Custom   map[string]any
	Fields   map[string]any
	Channel  string
	Brand    uuid.UUID
	Mailbox  *uuid.UUID
	Routed   *domain.TicketRouted
}

func lockTicket(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID) (ticketState, error) {
	var s ticketState
	err := tx.QueryRow(ctx, `SELECT number, subject, status, priority, severity, contact_id, assignee_id, team_id, snoozed_until, spam, custom, fields,
		channel, brand_id, mailbox_id, routed
		FROM tickets WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`, wid, tid).
		Scan(&s.Number, &s.Subject, &s.Status, &s.Priority, &s.Severity, &s.Contact, &s.Assignee, &s.Team, &s.Snoozed, &s.Spam, &s.Custom, &s.Fields,
			&s.Channel, &s.Brand, &s.Mailbox, &s.Routed)
	if err != nil {
		if db.IsNotFound(err) {
			return s, httpx.NotFound("Ticket")
		}
		return s, err
	}
	if s.Custom == nil {
		s.Custom = map[string]any{}
	}
	if s.Fields == nil {
		s.Fields = map[string]any{}
	}
	return s, nil
}

// applyOpts controls follow-up work of a patch.
type applyOpts struct {
	// Enqueue queues the ticket_updated trigger (automations do not, to avoid loops).
	Enqueue bool
	// RequireFields refuses solving or closing while a required_on_solve field is empty
	// (people and API keys; automations, merges and system jobs are not held back).
	RequireFields bool
	// Lenient skips unknown or not applicable labels and archived or removed fields
	// instead of refusing (automations).
	Lenient bool
}

// applyPatch validates and applies p in tx: it logs an activity per change, publishes
// ticket.updated, notifies a new assignee, requests CSAT on solve and queues the
// ticket_updated trigger. It returns the changed field names.
func applyPatch(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, tid uuid.UUID, actor Actor, p Patch, o applyOpts) ([]string, error) {
	cur, err := lockTicket(ctx, tx, wid, tid)
	if err != nil {
		return nil, err
	}
	// Snoozing puts the ticket on hold; any other status ends the snooze, so a solved
	// or reopened ticket is not woken up later.
	if p.Snooze.Set && !p.Snooze.Null && p.Status == nil {
		onHold := domain.StatusOnHold
		p.Status = &onHold
	}
	if p.Status != nil && *p.Status != domain.StatusOnHold && !p.Snooze.Set && cur.Snoozed != nil {
		p.Snooze = Opt[time.Time]{Set: true, Null: true}
	}
	next := cur
	var changes []string
	type act struct {
		typ  string
		data map[string]any
	}
	var acts []act

	if p.Subject != nil {
		s := strings.Join(strings.Fields(*p.Subject), " ")
		if s == "" {
			return nil, httpx.Validation("subject is required")
		}
		s = hd.Truncate(s, 500)
		if s != cur.Subject {
			next.Subject = s
			changes = append(changes, "subject")
			acts = append(acts, act{"subject_changed", map[string]any{"from": cur.Subject, "to": s}})
		}
	}
	if p.Status != nil {
		if !slices.Contains(domain.Statuses, *p.Status) {
			return nil, httpx.Validation("status must be one of " + strings.Join(domain.Statuses, ", "))
		}
		if *p.Status != cur.Status {
			next.Status = *p.Status
			changes = append(changes, "status")
			acts = append(acts, act{"status_changed", map[string]any{"from": cur.Status, "to": *p.Status}})
		}
	}
	if p.Priority != nil {
		if !slices.Contains(domain.Priorities, *p.Priority) {
			return nil, httpx.Validation("priority must be one of " + strings.Join(domain.Priorities, ", "))
		}
		if *p.Priority != cur.Priority {
			next.Priority = *p.Priority
			changes = append(changes, "priority")
			acts = append(acts, act{"priority_changed", map[string]any{"from": cur.Priority, "to": *p.Priority}})
		}
	}
	if p.Severity.Set {
		var to *string
		if v := strings.TrimSpace(p.Severity.Value); !p.Severity.Null && v != "" {
			if !slices.Contains(domain.Severities, v) {
				return nil, httpx.Validation("severity must be one of " + strings.Join(domain.Severities, ", ") + ", or null")
			}
			to = &v
		}
		if !sameStr(cur.Severity, to) {
			next.Severity = to
			changes = append(changes, "severity")
			acts = append(acts, act{"severity_changed", map[string]any{"from": strOrNil(cur.Severity), "to": strOrNil(to)}})
		}
	}
	if p.Contact.Set {
		if p.Contact.Null {
			return nil, httpx.Validation("contact_id cannot be null")
		}
		to := p.Contact.Value
		if !sameID(cur.Contact, &to) {
			// A live chat is the visitor's own conversation in the widget: it stays theirs.
			if cur.Channel == domain.ChannelChat {
				return nil, httpx.Validation("a live chat stays with the visitor who started it")
			}
			toName, err := contactLabel(ctx, tx, wid, to)
			if db.IsNotFound(err) {
				return nil, httpx.Validation("contact not found in this workspace")
			} else if err != nil {
				return nil, err
			}
			var fromName string
			if cur.Contact != nil {
				if fromName, err = contactLabel(ctx, tx, wid, *cur.Contact); err != nil && !db.IsNotFound(err) {
					return nil, err
				}
			}
			next.Contact = &to
			changes = append(changes, "contact_id")
			acts = append(acts, act{"contact_changed", map[string]any{
				"from": contactActivityRef(cur.Contact, fromName),
				"to":   contactActivityRef(&to, toName),
			}})
		}
	}
	if p.Assignee.Set {
		to := p.Assignee.Ptr()
		if to != nil {
			if err := hd.CheckMember(ctx, tx, wid, *to); err != nil {
				return nil, err
			}
		}
		next.Assignee = to
	}
	teamChanged := false
	if p.Team.Set {
		to := p.Team.Ptr()
		if to != nil {
			if err := hd.CheckRef(ctx, tx, wid, "team", *to); err != nil {
				return nil, err
			}
		}
		if !sameID(cur.Team, to) {
			next.Team, teamChanged = to, true
			if next.Routed, err = routedFor(ctx, tx, actor, to); err != nil {
				return nil, err
			}
		}
	}
	// The assignee of a private team's ticket must be on the team: naming someone else is
	// refused (automations skip it); moving the ticket into a team its assignee is not on
	// unassigns it.
	if next.Assignee != nil && (teamChanged || !sameID(cur.Assignee, next.Assignee)) {
		if err := hd.CheckAssigneeOnTeam(ctx, tx, wid, next.Team, next.Assignee); err != nil {
			named := p.Assignee.Set && !sameID(cur.Assignee, next.Assignee)
			if named && !o.Lenient {
				return nil, err
			}
			next.Assignee = nil
			if named && cur.Assignee != nil && hd.CheckAssigneeOnTeam(ctx, tx, wid, next.Team, cur.Assignee) == nil {
				next.Assignee = cur.Assignee
			}
		}
	}
	if !sameID(cur.Assignee, next.Assignee) {
		changes = append(changes, "assignee_id")
		acts = append(acts, act{"assigned", map[string]any{"from": idOrNil(cur.Assignee), "to": idOrNil(next.Assignee)}})
	}

	// Brand and reply address (contracts/teams.md "Reply address").
	if p.Brand != nil && *p.Brand != cur.Brand {
		if err := hd.CheckRef(ctx, tx, wid, "brand", *p.Brand); err != nil {
			if !o.Lenient {
				return nil, err
			}
			p.Brand = nil
		}
	}
	if p.Brand != nil && *p.Brand != cur.Brand {
		next.Brand = *p.Brand
		changes = append(changes, "brand_id")
		acts = append(acts, act{"brand_changed", map[string]any{"from": cur.Brand.String(), "to": next.Brand.String()}})
		if p.Mailbox == nil {
			inBrand, err := mailboxInBrand(ctx, tx, wid, next.Brand, cur.Mailbox)
			if err != nil {
				return nil, err
			}
			if !inBrand {
				m, err := replyMailbox(ctx, tx, wid, next.Brand, next.Team)
				if err != nil {
					return nil, err
				}
				if m == nil && cur.Channel == domain.ChannelEmail && !o.Lenient {
					return nil, httpx.Validation("this brand has no address to reply to this email ticket from")
				}
				if m != nil || cur.Channel != domain.ChannelEmail {
					next.Mailbox = m
				}
			}
		}
	}
	if p.Mailbox != nil && !sameID(cur.Mailbox, p.Mailbox) {
		inBrand, err := mailboxInBrand(ctx, tx, wid, next.Brand, p.Mailbox)
		if err != nil {
			return nil, err
		}
		switch {
		case inBrand:
			next.Mailbox = p.Mailbox
		case !o.Lenient:
			return nil, httpx.Validation("mailbox_id must be an address of the ticket's brand")
		}
	}
	mailboxData := map[string]any{}
	if teamChanged && p.Mailbox == nil && sameID(cur.Mailbox, next.Mailbox) && cur.Mailbox != nil {
		// The reply address follows the team when it was the old team's (or no team's)
		// and the brand has an address of the new team. A ticket leaving a private team
		// also leaves that team's address when the brand has another it may use.
		var mbTeam *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT team_id FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *cur.Mailbox).Scan(&mbTeam); err != nil && !db.IsNotFound(err) {
			return nil, err
		}
		var m *uuid.UUID
		if next.Team != nil && (mbTeam == nil || sameID(mbTeam, cur.Team)) {
			if m, err = domain.TeamReplyMailbox(ctx, tx, wid, next.Brand, *next.Team); err != nil {
				return nil, err
			}
		}
		if m == nil && mbTeam != nil && !sameID(mbTeam, next.Team) {
			private, err := hd.TeamPrivate(ctx, tx, wid, mbTeam)
			if err != nil {
				return nil, err
			}
			if private {
				if m, err = replyMailbox(ctx, tx, wid, next.Brand, next.Team); err != nil {
					return nil, err
				}
			}
		}
		if m != nil && *m != *cur.Mailbox {
			next.Mailbox = m
			mailboxData = map[string]any{"mailbox_from": cur.Mailbox.String(), "mailbox_to": m.String()}
		}
	}
	if !sameID(cur.Mailbox, next.Mailbox) {
		changes = append(changes, "mailbox_id")
		if len(mailboxData) == 0 {
			acts = append(acts, act{"mailbox_changed", map[string]any{"from": idOrNil(cur.Mailbox), "to": idOrNil(next.Mailbox)}})
		}
	}
	if teamChanged {
		changes = append(changes, "team_id")
		data := map[string]any{"from": idOrNil(cur.Team), "to": idOrNil(next.Team), "via": routedVia(next.Routed, actor)}
		for k, v := range mailboxData {
			data[k] = v
		}
		acts = append(acts, act{"team_changed", data})
	}
	if p.Snooze.Set {
		to := p.Snooze.Ptr()
		if to != nil {
			if to.Before(time.Now().Add(-time.Minute)) {
				return nil, httpx.Validation("snoozed_until must be in the future")
			}
			u := to.UTC()
			to = &u
		}
		if !sameTime(cur.Snoozed, to) {
			next.Snoozed = to
			changes = append(changes, "snoozed_until")
			if to != nil {
				acts = append(acts, act{"snoozed", map[string]any{"until": to.Format(time.RFC3339)}})
			} else {
				acts = append(acts, act{"unsnoozed", map[string]any{}})
			}
		}
	}
	if p.Spam != nil && *p.Spam != cur.Spam {
		next.Spam = *p.Spam
		changes = append(changes, "spam")
		if *p.Spam {
			acts = append(acts, act{"marked_spam", map[string]any{}})
		} else {
			acts = append(acts, act{"unmarked_spam", map[string]any{}})
		}
	}
	if p.Custom != nil {
		merged := map[string]any{}
		for k, v := range cur.Custom {
			merged[k] = v
		}
		var keys []string
		for k, v := range p.Custom {
			k = strings.TrimSpace(k)
			if k == "" || len(k) > 64 {
				return nil, httpx.Validation("custom keys must be 1..64 characters")
			}
			keys = append(keys, k)
			if v == nil {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		raw, _ := json.Marshal(merged)
		if len(merged) > 50 || len(raw) > 16<<10 {
			return nil, httpx.Validation("custom fields are limited to 50 keys and 16 KB")
		}
		old, _ := json.Marshal(cur.Custom)
		if string(old) != string(raw) {
			next.Custom = merged
			slices.Sort(keys)
			changes = append(changes, "custom")
			acts = append(acts, act{"custom_updated", map[string]any{"keys": keys}})
		}
	}

	var defs domain.FieldDefs
	if p.Fields != nil || (o.RequireFields && p.Status != nil && closing(next.Status) && !closing(cur.Status)) {
		if defs, err = domain.LoadFieldDefs(ctx, tx, wid, domain.FieldEntityTicket); err != nil {
			return nil, err
		}
	}
	if p.Fields != nil {
		patch := p.Fields
		if o.Lenient {
			patch = map[string]any{}
			for k, v := range p.Fields {
				if f, ok := defs[k]; ok && !f.Archived {
					patch[k] = v
				}
			}
		}
		merged, fc, err := domain.MergeFields(defs, cur.Fields, patch, domain.MemberChecker(ctx, tx, wid))
		if err != nil {
			return nil, err
		}
		if len(fc) > 0 {
			next.Fields = merged
			changes = append(changes, "fields")
			for _, c := range fc {
				acts = append(acts, act{"field_changed", map[string]any{"key": c.Key, "name": c.Name, "from": c.From, "to": c.To}})
			}
		}
	}
	if o.RequireFields && p.Status != nil && closing(next.Status) && !closing(cur.Status) {
		if err := domain.CheckRequiredOnSolve(defs, next.Fields); err != nil {
			return nil, err
		}
	}

	// Tags: replace, add, remove (by name).
	tagChanged := false
	if p.Tags != nil || len(p.AddTags) > 0 || len(p.RemoveTags) > 0 {
		added, removed, err := domain.ApplyTags(ctx, tx, wid, domain.TicketLabels, tid, domain.TagOps{Replace: p.Tags,
			Add: p.AddTags, Remove: p.RemoveTags, Create: p.CreateLabels, Lenient: o.Lenient})
		if err != nil {
			return nil, err
		}
		for _, n := range added {
			acts = append(acts, act{"tagged", map[string]any{"tag": n}})
		}
		for _, n := range removed {
			acts = append(acts, act{"untagged", map[string]any{"tag": n}})
		}
		if len(added)+len(removed) > 0 {
			tagChanged = true
			changes = append(changes, "tags")
		}
	}

	if len(changes) == 0 {
		return nil, nil
	}
	// A field someone (or something) set no longer needs Ollie's suggestion for it.
	if pending := dropSuggestions(next.Custom, changes); pending != nil {
		next.Custom = pending
		if !slices.Contains(changes, "custom") {
			changes = append(changes, "custom")
		}
	}
	if len(changes) > 1 || !tagChanged {
		_, err = tx.Exec(ctx, `UPDATE tickets SET subject = $3, status = $4, priority = $5, contact_id = $6, assignee_id = $7, team_id = $8,
			snoozed_until = $9, spam = $10, custom = $11, fields = $12, severity = $13,
			solved_at = CASE WHEN $4 IN ('solved', 'closed') THEN COALESCE(solved_at, now()) ELSE NULL END,
			closed_at = CASE WHEN $4 = 'closed' THEN COALESCE(closed_at, now()) ELSE NULL END,
			brand_id = $14, mailbox_id = $15, routed = $16,
			updated_at = now()
			WHERE workspace_id = $1 AND id = $2`,
			wid, tid, next.Subject, next.Status, next.Priority, next.Contact, next.Assignee, next.Team, next.Snoozed, next.Spam, next.Custom, next.Fields, next.Severity,
			next.Brand, next.Mailbox, next.Routed)
	} else {
		_, err = tx.Exec(ctx, `UPDATE tickets SET updated_at = now() WHERE workspace_id = $1 AND id = $2`, wid, tid)
	}
	if err != nil {
		return nil, fmt.Errorf("update ticket: %w", err)
	}
	for _, a := range acts {
		for k, v := range actor.Meta {
			a.data[k] = v
		}
		if _, err := domain.LogActivity(ctx, tx, wid, tid, actor.Type, actor.UserID, a.typ, a.data); err != nil {
			return nil, err
		}
	}
	if teamChanged && next.Team != nil {
		private, err := hd.TeamPrivate(ctx, tx, wid, next.Team)
		if err != nil {
			return nil, err
		}
		if private {
			// Clients outside the team drop the ticket; its own events now go to the team.
			if err := hd.PublishTicketHidden(ctx, tx, wid, tid, *next.Team); err != nil {
				return nil, err
			}
		}
	}
	if err := domain.PublishTicketUpdated(ctx, tx, wid, tid, changes); err != nil {
		return nil, err
	}
	if slices.Contains(changes, "assignee_id") && next.Assignee != nil && !sameID(actor.UserID, next.Assignee) {
		data := map[string]any{"by": idOrNil(actor.UserID), "actor_type": actor.Type}
		if err := hd.Notify(ctx, tx, wid, *next.Assignee, hd.NotifyAssigned, &tid, data); err != nil {
			return nil, err
		}
	}
	if slices.Contains(changes, "status") && next.Status == domain.StatusSolved {
		if err := requestCSAT(ctx, tx, d, wid, tid); err != nil {
			return nil, err
		}
		if err := hd.EnqueueAIEmbed(ctx, tx, d.Jobs, d.Cfg, wid, "ticket", tid); err != nil {
			return nil, err
		}
	}
	if slices.Contains(changes, "status") && closing(next.Status) && !closing(cur.Status) {
		// The mailbox's own copy leaves the Gmail or Outlook inbox too, when it asks for that.
		if err := domain.QueueProviderArchive(ctx, tx, d.Jobs, wid, tid); err != nil {
			return nil, err
		}
	}
	if o.Enqueue {
		if _, err := d.Jobs.InsertTx(ctx, tx, jobs.TicketTriggerArgs{WorkspaceID: wid, TicketID: tid, Trigger: jobs.TriggerTicketUpdated,
			ActorUserID: actor.UserID, Changes: changes}, nil); err != nil {
			return nil, fmt.Errorf("queue trigger: %w", err)
		}
	}
	return changes, nil
}

// ApplyPatch applies a person-initiated ticket change. It runs the same
// validation, events, notifications, CSAT and automation triggers as PATCH
// /v1/tickets/{id}.
func ApplyPatch(ctx context.Context, tx pgx.Tx, d *deps.Deps, principal *auth.Principal, tid uuid.UUID, patch Patch) ([]string, error) {
	if err := patch.checkCaller(ctx, tx, principal, tid); err != nil {
		return nil, err
	}
	return applyPatch(ctx, tx, d, principal.WorkspaceID, tid, actorOf(principal), patch,
		applyOpts{Enqueue: true, RequireFields: true})
}

// closing reports a solved or closed status.
func closing(status string) bool {
	return status == domain.StatusSolved || status == domain.StatusClosed
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func idOrNil(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// contactLabel is how the timeline names a contact: their name, else their email.
func contactLabel(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID) (string, error) {
	var label string
	err := tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(name, ''), email::text, '') FROM contacts WHERE workspace_id = $1 AND id = $2`, wid, id).Scan(&label)
	return label, err
}

func contactActivityRef(id *uuid.UUID, name string) any {
	if id == nil {
		return nil
	}
	return map[string]any{"id": id.String(), "name": name}
}

func sameStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func strOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// suggestionFor maps a changed ticket field to the key of Ollie's pending suggestion
// for it (custom.ai_suggestions, contracts/ai.md).
var suggestionFor = map[string]string{"priority": "priority", "severity": "severity", "tags": "labels", "team_id": "team", "spam": "spam"}

// dropSuggestions returns custom without the suggestions of the changed fields, or nil
// when there is nothing to drop. custom is not modified.
func dropSuggestions(custom map[string]any, changes []string) map[string]any {
	sugg, _ := custom[domain.CustomAISuggestions].(map[string]any)
	if len(sugg) == 0 {
		return nil
	}
	var drop []string
	for _, c := range changes {
		if k, ok := suggestionFor[c]; ok {
			if _, pending := sugg[k]; pending {
				drop = append(drop, k)
			}
		}
	}
	if len(drop) == 0 {
		return nil
	}
	rest := map[string]any{}
	for k, v := range sugg {
		if !slices.Contains(drop, k) {
			rest[k] = v
		}
	}
	out := make(map[string]any, len(custom))
	for k, v := range custom {
		out[k] = v
	}
	if len(rest) == 0 {
		delete(out, domain.CustomAISuggestions)
	} else {
		out[domain.CustomAISuggestions] = rest
	}
	return out
}

// routedFor says why a ticket changed team, for the actor who moved it (team nil: none).
func routedFor(ctx context.Context, q db.Querier, actor Actor, team *uuid.UUID) (*domain.TicketRouted, error) {
	if team == nil {
		return nil, nil
	}
	switch actor.Type {
	case "automation":
		return &domain.TicketRouted{Via: "automation"}, nil
	case "api":
		return &domain.TicketRouted{Via: "api"}, nil
	case "ai":
		if via, _ := actor.Meta["via"].(string); via == "triage" || actor.UserID == nil {
			return &domain.TicketRouted{Via: "triage"}, nil
		}
	}
	r := &domain.TicketRouted{Via: "agent"}
	if actor.UserID != nil {
		u, err := domain.LoadUserRef(ctx, q, *actor.UserID)
		if err != nil {
			return nil, err
		}
		r.By = &u
	}
	return r, nil
}

// routedVia is the via of a team_changed activity.
func routedVia(r *domain.TicketRouted, actor Actor) string {
	if r != nil {
		return r.Via
	}
	switch actor.Type {
	case "automation":
		return "automation"
	case "api":
		return "api"
	}
	return "agent"
}

// mailboxInBrand reports whether the workspace's mailbox belongs to brand (false for nil).
func mailboxInBrand(ctx context.Context, q db.Querier, wid, brand uuid.UUID, mailbox *uuid.UUID) (bool, error) {
	if mailbox == nil {
		return false, nil
	}
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1 AND id = $2 AND brand_id = $3)`,
		wid, *mailbox, brand).Scan(&ok)
	return ok, err
}

// replyMailbox is where a ticket of team in brand replies from by default: the brand's
// address of the team, else the brand's default address (nil when it has none).
func replyMailbox(ctx context.Context, q db.Querier, wid, brand uuid.UUID, team *uuid.UUID) (*uuid.UUID, error) {
	if team != nil {
		m, err := domain.TeamReplyMailbox(ctx, q, wid, brand, *team)
		if err != nil || m != nil {
			return m, err
		}
	}
	return domain.BrandMailboxFor(ctx, q, wid, brand, team)
}
