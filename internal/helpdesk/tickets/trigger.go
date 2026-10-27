package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/templates"
)

// MaxAutomationDepth bounds automation chains: changes made by automations re-run the
// ticket_updated automations in process at most this many times, and each automation
// fires at most once per chain. Automation writes never enqueue triggers.
const MaxAutomationDepth = 3

// TriggerWorker runs helpdesk.ticket_trigger jobs.
type TriggerWorker struct {
	river.WorkerDefaults[jobs.TicketTriggerArgs]
	D *deps.Deps
}

func (w *TriggerWorker) Work(ctx context.Context, job *river.Job[jobs.TicketTriggerArgs]) error {
	return RunTrigger(ctx, w.D, job.Args)
}

// RunTrigger applies SLA policies, automations, auto-assignment and the new ticket and
// reply notifications for one ticket change, in one transaction.
func RunTrigger(ctx context.Context, d *deps.Deps, a jobs.TicketTriggerArgs) error {
	// A suspended workspace runs no automations (contracts/admin.md).
	if suspended, err := domain.WorkspaceSuspended(ctx, d.DB.Pool, a.WorkspaceID); err != nil || suspended {
		return err
	}
	// Auto-triage runs first, outside the trigger transaction (it calls a model), so the
	// ticket_created automations see its tags, priority and sentiment.
	if h := hd.GetAIHooks(); a.Trigger == jobs.TriggerTicketCreated && h != nil && h.AutoTriage != nil {
		if err := h.AutoTriage(ctx, a.WorkspaceID, a.TicketID); err != nil {
			d.Log.Warn("auto triage skipped", "ticket_id", a.TicketID, "err", err)
		}
	}
	return d.DB.Tx(ctx, func(tx pgx.Tx) error {
		t, err := domain.LoadTicket(ctx, tx, a.WorkspaceID, a.TicketID)
		if db.IsNotFound(err) {
			return nil // deleted since
		}
		if err != nil {
			return err
		}
		run := &triggerRun{d: d, tx: tx, wid: a.WorkspaceID, tid: a.TicketID, fired: map[uuid.UUID]bool{}, messageID: a.MessageID}

		updated := func(field string) bool {
			return a.Trigger == jobs.TriggerTicketUpdated && slices.Contains(a.Changes, field)
		}
		if a.Trigger == jobs.TriggerTicketCreated || updated("priority") || updated("team_id") {
			if err := applySLA(ctx, tx, a.WorkspaceID, a.TicketID, a.Trigger == jobs.TriggerTicketCreated); err != nil {
				return err
			}
		}
		if !t.Spam {
			if err := run.automations(ctx, a.Trigger, a.MessageID, 0); err != nil {
				return err
			}
			if run.gone {
				return nil // deleted or merged into an earlier ticket: nothing else runs on it
			}
		}
		// A ticket that moved to another team unassigned is assigned in the new team's pool.
		if a.Trigger == jobs.TriggerTicketCreated || a.Trigger == jobs.TriggerCustomerReplied || updated("team_id") || run.teamMoved {
			if err := autoAssign(ctx, tx, d, a.WorkspaceID, a.TicketID, a.Trigger); err != nil {
				return err
			}
		}
		// After auto-assignment: only a ticket still nobody's is announced.
		if a.Trigger == jobs.TriggerTicketCreated {
			if err := notifyNewTicket(ctx, tx, d, a.WorkspaceID, a.TicketID, a.ActorUserID); err != nil {
				return err
			}
		}
		if a.Trigger == jobs.TriggerCustomerReplied {
			if err := notifyCustomerReplied(ctx, tx, d, a.WorkspaceID, a.TicketID, a.MessageID, a.ActorUserID); err != nil {
				return err
			}
		}
		if a.Trigger == jobs.TriggerThreadReplied {
			if err := notifyThreadReplied(ctx, tx, a.WorkspaceID, a.TicketID, a.MessageID); err != nil {
				return err
			}
		}
		return nil
	})
}

// applySLA picks the first enabled policy, by position, for the ticket's priority (or any
// priority) and its team (or any team), and sets the due dates from the ticket's creation.
// Business hours are the team's own, else the workspace's.
func applySLA(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID, created bool) error {
	var priority string
	var createdAt time.Time
	var current, team *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT priority, created_at, sla_policy_id, team_id FROM tickets WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
		wid, tid).Scan(&priority, &createdAt, &current, &team); err != nil {
		return err
	}
	var policyID *uuid.UUID
	var frMin, resMin int
	var inHours bool
	err := tx.QueryRow(ctx, `SELECT id, first_response_minutes, resolution_minutes, business_hours FROM sla_policies
		WHERE workspace_id = $1 AND enabled AND (priority = $2 OR priority IS NULL) AND (team_id = $3 OR team_id IS NULL)
		ORDER BY position, (priority IS NULL), (team_id IS NULL), created_at LIMIT 1`, wid, priority, team).Scan(&policyID, &frMin, &resMin, &inHours)
	if err != nil && !db.IsNotFound(err) {
		return err
	}
	if policyID == nil && current == nil {
		return nil
	}
	if policyID == nil {
		_, err = tx.Exec(ctx, `UPDATE tickets SET sla_policy_id = NULL, first_response_due_at = NULL, resolution_due_at = NULL,
			sla_first_response_breached_at = NULL, sla_resolution_breached_at = NULL, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wid, tid)
	} else {
		fr := createdAt.Add(time.Duration(frMin) * time.Minute)
		res := createdAt.Add(time.Duration(resMin) * time.Minute)
		if inHours {
			bh, loc, err := hd.HoursFor(ctx, tx, wid, team)
			if err != nil {
				return err
			}
			fr, res = bh.AddBusinessMinutes(createdAt, frMin, loc), bh.AddBusinessMinutes(createdAt, resMin, loc)
		}
		_, err = tx.Exec(ctx, `UPDATE tickets SET sla_policy_id = $3, first_response_due_at = $4, resolution_due_at = $5,
			sla_first_response_breached_at = CASE WHEN $4 > now() THEN NULL ELSE sla_first_response_breached_at END,
			sla_resolution_breached_at = CASE WHEN $5 > now() THEN NULL ELSE sla_resolution_breached_at END,
			updated_at = now() WHERE workspace_id = $1 AND id = $2`, wid, tid, *policyID, fr, res)
	}
	if err != nil {
		return err
	}
	return domain.PublishTicketUpdated(ctx, tx, wid, tid, []string{"sla"})
}

type automation struct {
	ID         uuid.UUID
	Name       string
	Conditions []hd.Condition
	Actions    []hd.Action
}

type triggerRun struct {
	d     *deps.Deps
	tx    pgx.Tx
	wid   uuid.UUID
	tid   uuid.UUID
	fired map[uuid.UUID]bool
	defs  map[string]domain.FieldDefs
	// decided caches ai_decide answers by question for the whole run (one call per run).
	decided map[string]float64
	asked   map[string]bool
	// teamMoved is set when an automation changed the ticket's team.
	teamMoved bool
	// messageID is the message the run is about, if any.
	messageID *uuid.UUID
	// gone is set when an automation deleted the ticket or merged it into another: nothing
	// else runs on it.
	gone bool
}

// automations evaluates the enabled automations for trigger in position order.
func (r *triggerRun) automations(ctx context.Context, trigger string, messageID *uuid.UUID, depth int) error {
	if depth > MaxAutomationDepth {
		return nil
	}
	rows, err := r.tx.Query(ctx, `SELECT id, name, conditions, actions FROM automations
		WHERE workspace_id = $1 AND trigger = $2 AND enabled ORDER BY position, created_at`, r.wid, trigger)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (automation, error) {
		var a automation
		var conds, acts []byte
		if err := row.Scan(&a.ID, &a.Name, &conds, &acts); err != nil {
			return a, err
		}
		_ = json.Unmarshal(conds, &a.Conditions)
		_ = json.Unmarshal(acts, &a.Actions)
		return a, nil
	})
	if err != nil || len(list) == 0 {
		return err
	}
	if err := r.decide(ctx, list); err != nil {
		return err
	}
	var changed []string
	for _, a := range list {
		if r.fired[a.ID] {
			continue
		}
		facts, err := r.facts(ctx, messageID)
		if err != nil {
			return err
		}
		facts.decided = r.decided
		if facts.spam || !matchAll(a.Conditions, facts) {
			continue
		}
		r.fired[a.ID] = true
		c, err := r.apply(ctx, a, facts)
		if err != nil {
			return err
		}
		changed = append(changed, c...)
		if _, err := r.tx.Exec(ctx, `UPDATE automations SET run_count = run_count + 1 WHERE workspace_id = $1 AND id = $2`, r.wid, a.ID); err != nil {
			return err
		}
		if r.gone {
			return nil
		}
	}
	if len(changed) == 0 {
		return nil
	}
	if slices.Contains(changed, "team_id") {
		r.teamMoved = true
	}
	if slices.Contains(changed, "priority") || slices.Contains(changed, "team_id") {
		if err := applySLA(ctx, r.tx, r.wid, r.tid, false); err != nil {
			return err
		}
	}
	return r.automations(ctx, jobs.TriggerTicketUpdated, messageID, depth+1)
}

// facts is what conditions are evaluated against.
type facts struct {
	subject, body, fromEmail, channel, mailboxID, priority, status string
	sentiment, severity                                            string
	assigneeID, teamID, brandID, widgetID, receivedAt              string
	decided                                                        map[string]float64
	tags                                                           []string
	spam                                                           bool
	contactEmail                                                   *string
	fields, contactFields                                          map[string]any
	defs                                                           map[string]domain.FieldDefs // by entity
}

func (r *triggerRun) facts(ctx context.Context, messageID *uuid.UUID) (facts, error) {
	var f facts
	var mailbox *uuid.UUID
	err := r.tx.QueryRow(ctx, `SELECT t.subject, t.channel, t.mailbox_id, t.priority, COALESCE(t.severity, 'none'), t.status, t.spam, c.email,
		COALESCE(t.custom->>'ai_sentiment', ''),
		COALESCE((SELECT array_agg(g.name) FROM ticket_tags tt JOIN tags g ON g.id = tt.tag_id WHERE tt.ticket_id = t.id), '{}'),
		t.fields, COALESCE(c.fields, '{}'),
		COALESCE(t.assignee_id::text, 'none'), COALESCE(t.team_id::text, 'none'), t.brand_id::text, COALESCE(t.widget_id::text, 'none'), t.received_at
		FROM tickets t LEFT JOIN contacts c ON c.id = t.contact_id AND c.workspace_id = t.workspace_id
		WHERE t.workspace_id = $1 AND t.id = $2`, r.wid, r.tid).
		Scan(&f.subject, &f.channel, &mailbox, &f.priority, &f.severity, &f.status, &f.spam, &f.contactEmail, &f.sentiment, &f.tags,
			&f.fields, &f.contactFields, &f.assigneeID, &f.teamID, &f.brandID, &f.widgetID, &f.receivedAt)
	if err != nil {
		return f, err
	}
	if r.defs == nil {
		r.defs = map[string]domain.FieldDefs{}
		for _, e := range []string{domain.FieldEntityTicket, domain.FieldEntityContact} {
			if r.defs[e], err = domain.LoadFieldDefs(ctx, r.tx, r.wid, e); err != nil {
				return f, err
			}
		}
	}
	f.defs = r.defs
	if mailbox != nil {
		f.mailboxID = mailbox.String()
	}
	// The body and sender are the triggering message's, else the latest customer message.
	var body, from string
	q := `SELECT CASE WHEN deleted_at IS NULL THEN body_text ELSE '' END, from_email FROM messages WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`
	args := []any{r.wid, r.tid, messageID}
	if messageID == nil {
		q = `SELECT body_text, from_email FROM messages WHERE workspace_id = $1 AND ticket_id = $2 AND kind = 'reply' AND deleted_at IS NULL
			ORDER BY (direction = 'inbound') DESC, created_at DESC LIMIT 1`
		args = args[:2]
	}
	if err := r.tx.QueryRow(ctx, q, args...).Scan(&body, &from); err != nil && !db.IsNotFound(err) {
		return f, err
	}
	f.body, f.fromEmail = body, from
	if f.fromEmail == "" && f.contactEmail != nil {
		f.fromEmail = *f.contactEmail
	}
	return f, nil
}

// matchAll reports whether every condition holds (an empty list always matches).
func matchAll(cs []hd.Condition, f facts) bool {
	for _, c := range cs {
		if !match(c, f) {
			return false
		}
	}
	return true
}

func match(c hd.Condition, f facts) bool {
	if entity, key, ok := hd.ConditionFieldKey(c.Field); ok {
		def, ok := f.defs[entity][key]
		if !ok {
			return false // the field was deleted since the automation was saved
		}
		values := f.fields
		if entity == domain.FieldEntityContact {
			values = f.contactFields
		}
		return domain.MatchField(def, domain.NormalizeOp(c.Op), c.Value, values[key])
	}
	if c.Field == "ai_decide" {
		p, ok := f.decided[c.Question]
		if !ok {
			return false // not answered: the automation does not run on a guess
		}
		if c.Op == "lt" {
			return p < c.DecideThreshold()
		}
		return p > c.DecideThreshold()
	}
	if c.Field == "tag" {
		if c.Op == "not_contains" || c.Op == "not_equals" || c.Op == "none" {
			// "tag not_equals x" holds when no tag equals x; "tag none" when no tag contains a phrase.
			for _, t := range f.tags {
				if !compare(c.Op, t, c.Value) {
					return false
				}
			}
			return true
		}
		for _, t := range f.tags {
			if compare(c.Op, t, c.Value) {
				return true
			}
		}
		return false
	}
	var v string
	switch c.Field {
	case "subject":
		v = f.subject
	case "body":
		v = f.body
	case "from_email":
		v = f.fromEmail
	case "channel":
		v = f.channel
	case "mailbox_id":
		v = f.mailboxID
	case "priority":
		v = f.priority
	case "severity":
		v = f.severity
	case "status":
		v = f.status
	case "sentiment":
		v = f.sentiment
	case "assignee_id":
		v = f.assigneeID
	case "team_id":
		v = f.teamID
	case "brand_id":
		v = f.brandID
	case "widget_id":
		v = f.widgetID
	case "received_at":
		v = f.receivedAt
	default:
		return false
	}
	return compare(c.Op, v, c.Value)
}

func compare(op, have, want string) bool {
	h, w := strings.ToLower(strings.TrimSpace(have)), strings.ToLower(strings.TrimSpace(want))
	switch op {
	case "contains":
		return strings.Contains(h, w)
	case "not_contains":
		return !strings.Contains(h, w)
	case "equals":
		return h == w
	case "not_equals":
		return h != w
	case "any", "none":
		// One phrase per line: any holds when one is contained, none when none is.
		found := slices.ContainsFunc(hd.Phrases(w), func(p string) bool { return strings.Contains(h, p) })
		return found == (op == "any")
	}
	return false
}

// apply runs an automation's actions, attributed to the automation, and returns the
// ticket fields it changed.
func (r *triggerRun) apply(ctx context.Context, a automation, f facts) ([]string, error) {
	actor := Actor{Type: "automation", Meta: map[string]any{"automation_id": a.ID.String(), "automation_name": a.Name}}
	// Deleting wins over the rest: no other step of the automation runs (no reply goes out
	// for a ticket about to vanish), and nothing else runs on the ticket after it.
	if slices.ContainsFunc(a.Actions, func(act hd.Action) bool { return act.Type == "delete_ticket" }) {
		r.gone = true
		return nil, deleteTicket(ctx, r.tx, r.wid, r.tid)
	}
	// Merging wins over the rest: when the ticket joins an earlier one, no other step of
	// the automation runs on it. With nothing to merge into, the other steps run as usual.
	if i := slices.IndexFunc(a.Actions, func(act hd.Action) bool { return act.Type == "merge_into_open_ticket" }); i >= 0 {
		merged, err := r.mergeIntoOpenTicket(ctx, actor, a.Actions[i])
		if err != nil {
			return nil, fmt.Errorf("automation %s: merge_into_open_ticket: %w", a.ID, err)
		}
		if merged {
			r.gone = true
			return nil, nil
		}
	}
	var patch Patch
	var changed []string
	for _, act := range a.Actions {
		switch act.Type {
		case "set_status":
			v := act.Value
			patch.Status = &v
		case "set_priority":
			v := act.Value
			patch.Priority = &v
		case "set_severity":
			patch.Severity = Opt[string]{Set: true, Value: act.Value, Null: act.Value == ""}
		case "assign_user":
			id, err := uuid.Parse(act.Value)
			if err != nil {
				continue
			}
			// A member removed since the automation was saved is skipped.
			if hd.CheckMember(ctx, r.tx, r.wid, id) != nil {
				continue
			}
			patch.Assignee = Opt[uuid.UUID]{Set: true, Value: id}
		case "assign_team":
			id, err := uuid.Parse(act.Value)
			if err != nil || hd.CheckRef(ctx, r.tx, r.wid, "team", id) != nil {
				continue
			}
			patch.Team = Opt[uuid.UUID]{Set: true, Value: id}
		case "set_reply_address":
			// An address removed since the automation was saved is skipped, and so is one
			// of another brand (applyPatch is lenient for automations).
			id, err := uuid.Parse(act.Value)
			if err != nil || hd.CheckRef(ctx, r.tx, r.wid, "mailbox", id) != nil {
				continue
			}
			patch.Mailbox = &id
		case "add_tag":
			patch.AddTags = append(patch.AddTags, act.Value)
			patch.CreateLabels = true
		case "set_field":
			def, ok := f.defs[domain.FieldEntityTicket][act.Field]
			if !ok || def.Archived {
				continue // removed or archived since the automation was saved
			}
			v, err := domain.ParseFieldString(def, act.Value, domain.MemberChecker(ctx, r.tx, r.wid))
			if err != nil {
				continue
			}
			if patch.Fields == nil {
				patch.Fields = map[string]any{}
			}
			patch.Fields[act.Field] = v
		case "remove_tag":
			patch.RemoveTags = append(patch.RemoveTags, act.Value)
		case "mark_spam":
			v := act.Value != "false"
			patch.Spam = &v
		case "send_reply", "add_note":
			if err := r.addMessage(ctx, act, f); err != nil {
				return nil, err
			}
			changed = append(changed, "messages")
		case "ai_triage":
			// The hook skips (nil, nil) when AI is off, out of AI balance or unavailable; an
			// error is a database failure of this transaction.
			if h := hd.GetAIHooks(); h != nil && h.Triage != nil {
				c, err := h.Triage(ctx, r.tx, r.wid, r.tid)
				if err != nil {
					return nil, fmt.Errorf("automation %s: ai_triage: %w", a.ID, err)
				}
				changed = append(changed, c...)
			}
		}
	}
	if !patch.empty() {
		c, err := applyPatch(ctx, r.tx, r.d, r.wid, r.tid, actor, patch, applyOpts{Lenient: true})
		if err != nil {
			return nil, fmt.Errorf("automation %s: %w", a.ID, err)
		}
		changed = append(changed, c...)
	}
	// Messages alone do not re-run ticket_updated automations.
	var fields []string
	for _, c := range changed {
		if c != "messages" {
			fields = append(fields, c)
		}
	}
	return fields, nil
}

func (r *triggerRun) addMessage(ctx context.Context, act hd.Action, f facts) error {
	kind := "note"
	if act.Type == "send_reply" {
		kind = "reply"
	}
	html := hd.SanitizeRich(act.Value)
	if kind == "reply" {
		// An automation's reply has no agent (contracts/templates.md).
		var err error
		if html, err = hd.FillReply(ctx, r.tx, r.d.Cfg, templates.Ref{WorkspaceID: r.wid, TicketID: &r.tid}, act.Value); err != nil {
			return err
		}
	}
	text := hd.HTMLToText(html)
	if text == "" {
		return nil
	}
	nm := domain.NewMessage{WorkspaceID: r.wid, TicketID: r.tid, Kind: kind, Direction: "outbound", AuthorType: "system",
		BodyHTML: html, BodyText: text, Subject: replySubject(f.subject), Trigger: "none"}
	if kind == "reply" && f.channel == domain.ChannelEmail {
		if f.contactEmail == nil || *f.contactEmail == "" {
			return nil
		}
		var mbEmail, mbName *string
		_ = r.tx.QueryRow(ctx, `SELECT mb.email, mb.display_name FROM tickets t JOIN mailboxes mb ON mb.id = t.mailbox_id
			WHERE t.workspace_id = $1 AND t.id = $2`, r.wid, r.tid).Scan(&mbEmail, &mbName)
		ok, err := r.mayAutoReply(ctx)
		if err != nil || !ok {
			return err
		}
		nm.To = []string{*f.contactEmail}
		nm.FromEmail, nm.FromName = str(mbEmail), str(mbName)
		nm.DeliveryStatus = "pending"
	}
	_, err := domain.AddMessage(ctx, r.tx, r.d.Jobs, nm)
	if errors.Is(err, domain.ErrCannotSend) {
		return nil // the ticket's mailbox has no way to send: nothing to answer with
	}
	return err
}

// Automation replies by email are automatic mail (Auto-Submitted: auto-generated). They
// never answer mail nothing automatic may answer (messages.no_auto_reply: automatic, bulk
// or list mail, a workspace's own address, a sender that failed authentication), and at
// most automationRepliesPerTicket go to one ticket, and automationRepliesPerContact to one
// contact's tickets, per automationReplyWindow, so two automatic systems cannot keep
// answering each other.
const (
	automationRepliesPerTicket  = 3
	automationRepliesPerContact = 10
	automationReplyWindow       = time.Hour
)

// mayAutoReply decides whether an automation may send an email reply on the run's ticket,
// judged by the message the run is about, else the ticket's latest inbound message.
func (r *triggerRun) mayAutoReply(ctx context.Context) (bool, error) {
	var noAuto bool
	err := r.tx.QueryRow(ctx, `SELECT no_auto_reply OR auth_failed FROM messages
		WHERE workspace_id = $1 AND ticket_id = $2 AND direction = 'inbound' AND kind = 'reply'
		ORDER BY COALESCE(id = $3, false) DESC, created_at DESC LIMIT 1`, r.wid, r.tid, r.messageID).Scan(&noAuto)
	if err != nil && !db.IsNotFound(err) {
		return false, err
	}
	if noAuto {
		r.d.Log.Info("automation reply skipped", "ticket_id", r.tid, "reason", "automatic_mail")
		return false, nil
	}
	var perTicket, perContact int
	err = r.tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE m.ticket_id = $2), count(*)
		FROM messages m JOIN tickets t ON t.id = m.ticket_id AND t.workspace_id = m.workspace_id
		WHERE m.workspace_id = $1 AND m.author_type = 'system' AND m.kind = 'reply' AND m.direction = 'outbound'
		AND m.created_at > now() - $3::interval
		AND (m.ticket_id = $2 OR t.contact_id = (SELECT contact_id FROM tickets WHERE workspace_id = $1 AND id = $2))`,
		r.wid, r.tid, fmt.Sprintf("%d seconds", int(automationReplyWindow.Seconds()))).Scan(&perTicket, &perContact)
	if err != nil {
		return false, err
	}
	if perTicket >= automationRepliesPerTicket || perContact >= automationRepliesPerContact {
		r.d.Log.Info("automation reply skipped", "ticket_id", r.tid, "reason", "rate_limited")
		return false, nil
	}
	return true, nil
}

// Assignment modes (settings.auto_assign and assignment_rules.mode).
const (
	AssignNone         = "none"
	AssignRoundRobin   = "round_robin"
	AssignLoadBalanced = "load_balanced"
)

// resolveAssignMode picks the mode for a ticket: its team's rule, else its mailbox's
// rule, else the workspace default.
func resolveAssignMode(ctx context.Context, q pgx.Tx, wid uuid.UUID, team, mailbox *uuid.UUID, def string) (string, error) {
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
	if def == AssignRoundRobin || def == AssignLoadBalanced {
		return def, nil
	}
	return AssignNone, nil
}

// autoAssign assigns an unassigned open ticket by the mode that applies to it
// (contracts/api.md "Assignment"). After Ollie triaged a new ticket and the workspace
// turned assign_after_triage on, the assignment is attributed to ai.
func autoAssign(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, tid uuid.UUID, trigger string) error {
	s, err := hd.LoadSettings(ctx, tx, wid)
	if err != nil {
		return err
	}
	var assignee, team, mailbox *uuid.UUID
	var status string
	var spam bool
	var custom map[string]any
	if err := tx.QueryRow(ctx, `SELECT assignee_id, team_id, mailbox_id, status, spam, custom FROM tickets
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`, wid, tid).
		Scan(&assignee, &team, &mailbox, &status, &spam, &custom); err != nil {
		if db.IsNotFound(err) {
			return nil
		}
		return err
	}
	if assignee != nil || spam || status == domain.StatusSolved || status == domain.StatusClosed {
		return nil
	}
	mode, err := resolveAssignMode(ctx, tx, wid, team, mailbox, s.AutoAssign)
	if err != nil {
		return err
	}
	actor := Actor{Type: "system", Meta: map[string]any{"via": mode}}
	if tc := s.Triage(); trigger == jobs.TriggerTicketCreated && tc.AutoTriage && tc.AssignAfterTriage && s.AI().Enabled {
		if _, triaged := custom["ai_triaged_at"]; triaged {
			if mode == AssignNone {
				mode = AssignLoadBalanced
			}
			actor = Actor{Type: "ai", Meta: map[string]any{"via": "triage", "mode": mode}}
		}
	}
	if mode == AssignNone {
		return nil
	}
	pool := team
	if pool == nil && mailbox != nil {
		var mbTeam *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT team_id FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *mailbox).Scan(&mbTeam); err != nil && !db.IsNotFound(err) {
			return err
		}
		pool = mbTeam
	}
	next, err := PickAssignee(ctx, tx, wid, pool, mode)
	if err != nil || next == nil {
		return err
	}
	_, err = applyPatch(ctx, tx, d, wid, tid, actor, Patch{Assignee: Opt[uuid.UUID]{Set: true, Value: *next}}, applyOpts{})
	return err
}

type candidate struct {
	ID   uuid.UUID
	Open int
}

// PickAssignee chooses the next agent of a pool (a team, or the whole workspace) and
// moves the pool's cursor. Eligible: owners, admins and agents whose presence is
// online and who are under their open ticket cap. nil when nobody is eligible.
func PickAssignee(ctx context.Context, tx pgx.Tx, wid uuid.UUID, team *uuid.UUID, mode string) (*uuid.UUID, error) {
	key := "workspace"
	q := `SELECT m.user_id, ` + domain.OpenTicketsSQL + ` FROM members m JOIN users u ON u.id = m.user_id JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.workspace_id = $1 AND m.role IN ('owner','admin','agent') AND ` + domain.PresenceSQL + ` = 'online'
		AND (m.max_open_tickets IS NULL OR ` + domain.OpenTicketsSQL + ` < m.max_open_tickets)`
	args := []any{wid}
	if team != nil {
		key = "team:" + team.String()
		// Observers see the team's tickets but are never auto-assigned.
		q += ` AND EXISTS (SELECT 1 FROM team_members tm JOIN teams tt ON tt.id = tm.team_id
			WHERE tm.user_id = m.user_id AND tm.team_id = $2 AND tt.workspace_id = $1 AND tm.role IN ('lead', 'member'))`
		args = append(args, *team)
	}
	rows, err := tx.Query(ctx, q+` ORDER BY m.user_id`, args...)
	if err != nil {
		return nil, err
	}
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (candidate, error) {
		var c candidate
		return c, r.Scan(&c.ID, &c.Open)
	})
	if err != nil || len(cands) == 0 {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO round_robin_cursors (workspace_id, pool) VALUES ($1, $2) ON CONFLICT DO NOTHING`, wid, key); err != nil {
		return nil, err
	}
	var last *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT last_user_id FROM round_robin_cursors WHERE workspace_id = $1 AND pool = $2 FOR UPDATE`, wid, key).Scan(&last); err != nil {
		return nil, err
	}
	next := chooseCandidate(cands, last, mode)
	if _, err := tx.Exec(ctx, `UPDATE round_robin_cursors SET last_user_id = $3, updated_at = now() WHERE workspace_id = $1 AND pool = $2`,
		wid, key, next); err != nil {
		return nil, err
	}
	return &next, nil
}

// chooseCandidate applies a mode to candidates sorted by id: round robin takes the
// first after last; load balanced takes the fewest open tickets, ties in round robin
// order.
func chooseCandidate(cands []candidate, last *uuid.UUID, mode string) uuid.UUID {
	start := 0
	if last != nil {
		for i, c := range cands {
			if c.ID.String() > last.String() {
				start = i
				break
			}
			if i == len(cands)-1 {
				start = 0
			}
		}
	}
	order := append(append([]candidate{}, cands[start:]...), cands[:start]...)
	if mode != AssignLoadBalanced {
		return order[0].ID
	}
	best := order[0]
	for _, c := range order[1:] {
		if c.Open < best.Open {
			best = c
		}
	}
	return best.ID
}

// notifyCustomerReplied tells the assignee about a customer's reply, or, on a ticket nobody
// has, the people a new ticket would tell, so a reply never waits unseen.
func notifyCustomerReplied(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, tid uuid.UUID, messageID, actor *uuid.UUID) error {
	var assignee *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT assignee_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, tid).Scan(&assignee); err != nil {
		return err
	}
	var recipients []uuid.UUID
	if assignee != nil {
		recipients = []uuid.UUID{*assignee}
	} else {
		var err error
		if recipients, _, _, err = unassignedAudience(ctx, tx, d, wid, tid, actor); err != nil || len(recipients) == 0 {
			return err
		}
	}
	data := map[string]any{}
	if messageID != nil {
		var preview string
		// A message deleted before this ran is announced without what it said.
		if err := tx.QueryRow(ctx, `SELECT CASE WHEN deleted_at IS NULL THEN body_text ELSE '' END FROM messages WHERE workspace_id = $1 AND id = $2`, wid, *messageID).Scan(&preview); err == nil {
			data["message_id"] = messageID.String()
			data["preview"] = hd.Excerpt(preview, 140)
		}
	}
	var cid uuid.UUID
	var cname string
	if err := tx.QueryRow(ctx, `SELECT c.id, c.name FROM tickets t JOIN contacts c ON c.id = t.contact_id AND c.workspace_id = t.workspace_id
		WHERE t.workspace_id = $1 AND t.id = $2`, wid, tid).Scan(&cid, &cname); err == nil {
		data["contact"] = map[string]any{"id": cid.String(), "name": cname}
	}
	for _, uid := range recipients {
		if err := hd.Notify(ctx, tx, wid, uid, hd.NotifyCustomerReplied, &tid, maps.Clone(data)); err != nil {
			return err
		}
	}
	return nil
}

// Who hears about a new unassigned ticket (contracts/api.md "Assignment").
const (
	// newTicketSmallTeam: in auto, up to this many people hear; more, and only the leads do.
	newTicketSmallTeam = 5
	newTicketMax       = 50
)

// notifyNewTicket tells people about a new ticket nobody is assigned to.
func notifyNewTicket(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, tid uuid.UUID, actor *uuid.UUID) error {
	recipients, channel, contact, err := unassignedAudience(ctx, tx, d, wid, tid, actor)
	if err != nil || len(recipients) == 0 {
		return err
	}
	data := map[string]any{"channel": channel}
	if contact != nil {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM contacts WHERE workspace_id = $1 AND id = $2`, wid, *contact).Scan(&name); err == nil {
			data["contact"] = map[string]any{"id": contact.String(), "name": name}
		}
	}
	for _, uid := range recipients {
		if err := hd.Notify(ctx, tx, wid, uid, hd.NotifyNewTicket, &tid, maps.Clone(data)); err != nil {
			return err
		}
	}
	return nil
}

// unassignedAudience is who hears about a ticket nobody is assigned to, by
// settings.new_ticket_notify, never actor. Spam, a solved or closed ticket, and one the
// answer bot is answering have none: the bot's handoff notifies (ai_handoff).
func unassignedAudience(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, tid uuid.UUID, actor *uuid.UUID) (recipients []uuid.UUID, channel string, contact *uuid.UUID, err error) {
	s, err := hd.LoadSettings(ctx, tx, wid)
	if err != nil {
		return nil, "", nil, err
	}
	mode := s.NewTicketNotify()
	if mode == hd.NewTicketNotifyOff {
		return nil, "", nil, nil
	}
	var assignee, team, mailbox *uuid.UUID
	var status string
	var spam bool
	var custom map[string]any
	err = tx.QueryRow(ctx, `SELECT assignee_id, team_id, mailbox_id, contact_id, channel, status, spam, custom FROM tickets
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, wid, tid).
		Scan(&assignee, &team, &mailbox, &contact, &channel, &status, &spam, &custom)
	if db.IsNotFound(err) {
		return nil, "", nil, nil
	}
	if err != nil {
		return nil, "", nil, err
	}
	if assignee != nil || spam || status == domain.StatusSolved || status == domain.StatusClosed {
		return nil, "", nil, nil
	}
	if v, _ := custom["ai_handoff"].(bool); v {
		return nil, "", nil, nil
	}
	if bot, err := botAnswers(ctx, tx, d, s, wid, team, mailbox, contact, channel); err != nil || bot {
		return nil, "", nil, err
	}
	// The ticket's team, else its address's team: the pool auto-assignment uses.
	pool := team
	if pool == nil && mailbox != nil {
		if err := tx.QueryRow(ctx, `SELECT team_id FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *mailbox).
			Scan(&pool); err != nil && !db.IsNotFound(err) {
			return nil, "", nil, err
		}
	}
	recipients, err = newTicketRecipients(ctx, tx, wid, pool, mode, actor)
	if err != nil || pool == nil || len(recipients) == 0 {
		return recipients, channel, contact, err
	}
	// Who turned the team's notifications off hears nothing of its tickets nobody has.
	rows, err := tx.Query(ctx, `SELECT user_id FROM team_members WHERE team_id = $1 AND user_id = ANY($2) AND NOT notify`, *pool, recipients)
	if err != nil {
		return nil, "", nil, err
	}
	muted, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, "", nil, err
	}
	recipients = slices.DeleteFunc(recipients, func(u uuid.UUID) bool { return slices.Contains(muted, u) })
	return recipients, channel, contact, nil
}

// newTicketRecipients picks who hears about a new ticket of pool, leaving out actor.
// auto: the pool's audience when it is at most newTicketSmallTeam people, else its leads.
// A pool with nobody to work it falls back to its leads, else the owners and admins.
func newTicketRecipients(ctx context.Context, tx pgx.Tx, wid uuid.UUID, pool *uuid.UUID, mode string, actor *uuid.UUID) ([]uuid.UUID, error) {
	// One more than needed, so leaving out the actor keeps the count right.
	get := func(f func(context.Context, pgx.Tx, uuid.UUID, *uuid.UUID, int) ([]uuid.UUID, error), limit int) ([]uuid.UUID, error) {
		list, err := f(ctx, tx, wid, pool, limit+1)
		if err != nil {
			return nil, err
		}
		list = slices.DeleteFunc(list, func(u uuid.UUID) bool { return actor != nil && u == *actor })
		return list[:min(len(list), limit)], nil
	}
	switch mode {
	case hd.NewTicketNotifyLeads:
		return get(hd.TicketLeads, newTicketMax)
	case hd.NewTicketNotifyEveryone:
		list, err := get(hd.TicketAudience, newTicketMax)
		if err != nil || len(list) > 0 {
			return list, err
		}
		return get(hd.TicketLeads, newTicketMax)
	}
	list, err := get(hd.TicketAudience, newTicketSmallTeam+1)
	if err != nil {
		return nil, err
	}
	if len(list) > 0 && len(list) <= newTicketSmallTeam {
		return list, nil
	}
	return get(hd.TicketLeads, newTicketSmallTeam)
}

// botAnswers reports whether the answer bot takes a new ticket on before a person does:
// a chat with widget answers on, or an email to an address whose bot sends its replies
// itself (ai_answers_mode send, not draft), within the hours it answers. It decides as
// the bots do (hd.EnqueueAIWidgetAnswer, internal/ai loadEmailConv).
func botAnswers(ctx context.Context, tx pgx.Tx, d *deps.Deps, s hd.Settings, wid uuid.UUID, team, mailbox, contact *uuid.UUID, channel string) (bool, error) {
	ai := s.AI()
	if d.Cfg == nil || !d.Cfg.AIEnabled() || !ai.Enabled {
		return false, nil
	}
	switch {
	case channel == domain.ChannelChat:
		return ai.WidgetAnswers, nil
	case channel != domain.ChannelEmail || mailbox == nil:
		return false, nil
	}
	var on, outside bool
	var mode, status string
	err := tx.QueryRow(ctx, `SELECT ai_answers, ai_answers_mode, ai_answers_outside_hours, status FROM mailboxes
		WHERE workspace_id = $1 AND id = $2`, wid, *mailbox).Scan(&on, &mode, &outside, &status)
	if db.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// draft: the bot only suggests a reply a person sends, so people hear now.
	if !on || status == "disabled" || mode != "send" {
		return false, nil
	}
	if outside {
		bh, loc, err := hd.HoursFor(ctx, tx, wid, team)
		if err != nil {
			return false, err
		}
		if bh.Enabled && hd.OpenAt(bh, loc, time.Now()) {
			return false, nil
		}
	}
	// The bot never answers the workspace's own members or addresses.
	if contact != nil {
		var own bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE(c.email, '') <> '' AND (
			EXISTS (SELECT 1 FROM members mb JOIN users u ON u.id = mb.user_id WHERE mb.workspace_id = $1 AND u.email = c.email::citext)
			OR EXISTS (SELECT 1 FROM mailboxes x WHERE x.workspace_id = $1 AND (x.email = c.email::citext OR lower(x.config->>'oauth_email') = lower(c.email))))
			FROM contacts c WHERE c.workspace_id = $1 AND c.id = $2`, wid, *contact).Scan(&own); err != nil && !db.IsNotFound(err) {
			return false, err
		}
		if own {
			return false, nil
		}
	}
	return true, nil
}

// notifyThreadReplied tells the assignee that someone outside the team answered one of the
// ticket's third-party threads (contracts/api.md "Threads").
func notifyThreadReplied(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID, messageID *uuid.UUID) error {
	var assignee *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT assignee_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, tid).Scan(&assignee); err != nil {
		return err
	}
	if assignee == nil || messageID == nil {
		return nil
	}
	var threadID *uuid.UUID
	var preview, fromName, fromEmail, subject string
	err := tx.QueryRow(ctx, `SELECT m.thread_id, m.body_text, m.from_name, m.from_email, COALESCE(th.subject, '') FROM messages m
		LEFT JOIN ticket_threads th ON th.id = m.thread_id
		WHERE m.workspace_id = $1 AND m.id = $2`, wid, *messageID).Scan(&threadID, &preview, &fromName, &fromEmail, &subject)
	if db.IsNotFound(err) || err == nil && threadID == nil {
		return nil
	}
	if err != nil {
		return err
	}
	data := map[string]any{"message_id": messageID.String(), "thread_id": threadID.String(), "subject": subject,
		"preview": hd.Excerpt(preview, 140), "from": map[string]any{"name": fromName, "email": fromEmail}}
	return hd.Notify(ctx, tx, wid, *assignee, hd.NotifyThreadReplied, &tid, data)
}

// decide asks the decision engine every ai_decide question of list not answered yet in
// this run, in one call. Unanswered questions stay absent, so their conditions fail.
func (r *triggerRun) decide(ctx context.Context, list []automation) error {
	var questions []string
	for _, a := range list {
		if r.fired[a.ID] {
			continue
		}
		for _, c := range a.Conditions {
			if c.Field == "ai_decide" && c.Question != "" && !r.asked[c.Question] && !slices.Contains(questions, c.Question) {
				questions = append(questions, c.Question)
			}
		}
	}
	if len(questions) == 0 {
		return nil
	}
	if r.asked == nil {
		r.asked, r.decided = map[string]bool{}, map[string]float64{}
	}
	for _, q := range questions {
		r.asked[q] = true
	}
	h := hd.GetAIHooks()
	if h == nil || h.Decide == nil {
		return nil
	}
	got, err := h.Decide(ctx, r.tx, r.wid, r.tid, questions)
	if err != nil {
		return err
	}
	for q, p := range got {
		r.decided[q] = p
	}
	return nil
}
