package tickets_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// customerMail opens a ticket the way the email pipeline does: the contact of from (none
// when from is empty), the ticket and the customer's first message, with its ticket_created
// trigger queued but not run. edit adjusts the ticket and the message before they are written.
func customerMail(t *testing.T, e *hdtest.Env, ws hdtest.WS, mb uuid.UUID, from, subject, body string, edit ...func(*domain.NewTicket, *domain.NewMessage)) uuid.UUID {
	t.Helper()
	var tid uuid.UUID
	err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		nt := domain.NewTicket{WorkspaceID: ws.ID, Subject: subject, Channel: domain.ChannelEmail, MailboxID: &mb}
		nm := domain.NewMessage{WorkspaceID: ws.ID, Kind: "reply", Direction: "inbound", AuthorType: "contact",
			BodyHTML: "<p>" + body + "</p>", BodyText: body, FromEmail: from, MailboxID: &mb, Trigger: jobs.TriggerTicketCreated}
		if from != "" {
			cid, _, err := domain.UpsertContactByEmail(e.Ctx, tx, ws.ID, from, "")
			if err != nil {
				return err
			}
			nt.ContactID, nm.AuthorContactID = &cid, &cid
		}
		for _, f := range edit {
			f(&nt, &nm)
		}
		tk, err := domain.CreateTicket(e.Ctx, tx, nt)
		if err != nil {
			return err
		}
		tid, nm.TicketID = tk.ID, tk.ID
		_, err = domain.AddMessage(e.Ctx, tx, e.D.Jobs, nm)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tid
}

type mergeState struct {
	Into   *uuid.UUID
	Status string
}

func mergedInto(t *testing.T, e *hdtest.Env, id uuid.UUID) mergeState {
	t.Helper()
	var s mergeState
	if err := e.D.DB.Pool.QueryRow(e.Ctx, `SELECT merged_into_id, status FROM tickets WHERE id = $1`, id).Scan(&s.Into, &s.Status); err != nil {
		t.Fatal(err)
	}
	return s
}

func mergeAction(days string, sameSubject bool) map[string]any {
	return map[string]any{"type": "merge_into_open_ticket", "value": days, "same_subject": sameSubject}
}

func TestAutomationMergesIntoOpenTicket(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	pre := addAutomation(t, e, ws, jobs.TriggerTicketCreated, 0, []map[string]string{}, []map[string]string{{"type": "add_tag", "value": "inbox"}})
	merge := addAutomation(t, e, ws, jobs.TriggerTicketCreated, 1, []map[string]string{},
		[]map[string]any{mergeAction("7", true), {"type": "add_tag", "value": "fresh"}})
	after := addAutomation(t, e, ws, jobs.TriggerTicketCreated, 2, []map[string]string{}, []map[string]string{{"type": "add_tag", "value": "seen"}})
	updated := addAutomation(t, e, ws, jobs.TriggerTicketUpdated, 0,
		[]map[string]string{{"field": "body", "op": "contains", "value": "again"}}, []map[string]string{{"type": "set_priority", "value": "high"}})

	// The first mail has nothing to join: the merge automation's other steps run.
	first := customerMail(t, e, ws, mb, "carla@customer.test", "Order 1042 not delivered", "Where is my parcel?")
	runTriggers(t, e, ws.ID)
	if s := mergedInto(t, e, first); s.Into != nil || s.Status != domain.StatusOpen {
		t.Fatalf("first ticket %+v", s)
	}
	if got := tagNames(getTicket(t, e, ws, first)); !slices.Equal(got, []string{"fresh", "inbox", "seen"}) {
		t.Fatalf("first ticket tags %v", got)
	}
	e.Do("PATCH", "/v1/tickets/"+first.String(), ws.Agent.As(ws), map[string]any{"status": "pending", "assignee_id": ws.Agent.ID.String()}).Want(t, 200)
	runTriggers(t, e, ws.ID)

	// The customer writes again, without the thread headers: the new ticket joins the first.
	second := customerMail(t, e, ws, mb, "carla@customer.test", "RE:  order 1042 NOT delivered [#41]", "Writing again")
	runTriggers(t, e, ws.ID)
	if s := mergedInto(t, e, second); s.Into == nil || *s.Into != first || s.Status != domain.StatusClosed {
		t.Fatalf("second ticket %+v, want merged into %s", s, first)
	}
	if n := countRows(t, e, `SELECT count(*) FROM messages WHERE ticket_id = $1 AND body_text = 'Writing again'`, first); n != 1 {
		t.Fatalf("moved messages %d", n)
	}
	if n := countRows(t, e, `SELECT count(*) FROM messages WHERE ticket_id = $1`, second); n != 0 {
		t.Fatalf("%d messages left on the merged ticket", n)
	}
	// As for a reply: the ticket is open again and its assignee hears of it.
	got := getTicket(t, e, ws, first)
	if got.Status != domain.StatusOpen || got.MessageCount != 2 {
		t.Fatalf("first ticket after the merge %+v", got)
	}
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'customer_replied' AND user_id = $2`, first, ws.Agent.ID); n != 1 {
		t.Fatalf("customer_replied notifications %d", n)
	}
	// Both timelines name the automation.
	var names []string
	e.Scalar(`SELECT array_agg(data->>'automation_name' ORDER BY ticket_id = $1) FROM activities WHERE ticket_id = ANY($2) AND type = 'merged' AND actor_type = 'automation'`,
		&names, first, []uuid.UUID{first, second})
	if !slices.Equal(names, []string{"auto ticket_created", "auto ticket_created"}) {
		t.Fatalf("merged activities %v", names)
	}
	if n := countRows(t, e, `SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'status_changed' AND actor_type = 'automation' AND data->>'to' = 'open'`, first); n != 1 {
		t.Fatalf("reopened activities %d", n)
	}
	var into string
	e.Scalar(`SELECT data->>'into' FROM activities WHERE ticket_id = $1 AND type = 'merged'`, &into, second)
	if into != first.String() {
		t.Fatalf("merged into %q", into)
	}

	// Nothing else runs on the merged ticket: not the merge automation's other steps, the
	// automations after it, a ticket_updated chain, auto-assignment or the new ticket notice.
	if got := tagNames(getTicket(t, e, ws, second)); !slices.Equal(got, []string{"inbox"}) {
		t.Fatalf("merged ticket tags %v", got)
	}
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'new_ticket'`, second); n != 0 {
		t.Fatalf("new ticket notifications for a merged ticket %d", n)
	}
	var assignee *uuid.UUID
	e.Scalar(`SELECT assignee_id FROM tickets WHERE id = $1`, &assignee, second)
	if assignee != nil {
		t.Fatal("merged ticket auto-assigned")
	}
	var runs []int64
	e.Scalar(`SELECT array_agg(run_count ORDER BY array_position($2::uuid[], id)) FROM automations WHERE id = ANY($1)`, &runs,
		[]uuid.UUID{pre, merge, after, updated}, []uuid.UUID{pre, merge, after, updated})
	if !slices.Equal(runs, []int64{2, 2, 1, 0}) {
		t.Fatalf("run counts %v", runs)
	}

	// A third mail joins the live ticket, never the one merged away.
	third := customerMail(t, e, ws, mb, "carla@customer.test", "Fwd: Order 1042 not delivered", "Again")
	runTriggers(t, e, ws.ID)
	if s := mergedInto(t, e, third); s.Into == nil || *s.Into != first {
		t.Fatalf("third ticket %+v, want merged into %s", s, first)
	}
	if s := mergedInto(t, e, first); s.Into != nil || s.Status != domain.StatusOpen {
		t.Fatalf("first ticket merged away %+v", s)
	}
}

func TestAutomationMergeSkips(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	addAutomation(t, e, ws, jobs.TriggerTicketCreated, 0, []map[string]string{}, []map[string]any{mergeAction("7", true)})
	private := addTeam(t, e, ws, ws.Agent.ID)
	e.Exec(`UPDATE teams SET private = true WHERE id = $1`, private)
	public := addTeam(t, e, ws, ws.Agent.ID)
	const subject = "Invoice 77 is wrong"

	cases := []struct {
		name  string
		merge bool
		// earlier writes the contact's earlier ticket (none when nil); later the new one.
		earlier func(from string) uuid.UUID
		later   func(nt *domain.NewTicket, nm *domain.NewMessage)
	}{
		{name: "open", merge: true},
		{name: "on hold", merge: true, earlier: withStatus(t, e, ws, mb, "on_hold")},
		{name: "solved", earlier: withStatus(t, e, ws, mb, "solved")},
		{name: "closed", earlier: withStatus(t, e, ws, mb, "closed")},
		{name: "spam", earlier: withSQL(t, e, ws, mb, `UPDATE tickets SET spam = true WHERE id = $1`)},
		{name: "deleted", earlier: withSQL(t, e, ws, mb, `UPDATE tickets SET deleted_at = now() WHERE id = $1`)},
		{name: "outside the window", earlier: withSQL(t, e, ws, mb, `UPDATE tickets SET created_at = now() - interval '8 days', last_message_at = now() - interval '8 days' WHERE id = $1`)},
		{name: "old but replied on lately", merge: true, earlier: withSQL(t, e, ws, mb, `UPDATE tickets SET created_at = now() - interval '40 days', last_message_at = now() - interval '6 days' WHERE id = $1`)},
		{name: "another subject", earlier: func(from string) uuid.UUID { return customerMail(t, e, ws, mb, from, "Invoice 78 is wrong", "x") }},
		{name: "another contact", earlier: func(string) uuid.UUID { return customerMail(t, e, ws, mb, "someone.else@customer.test", subject, "x") }},
		{name: "another channel", earlier: func(from string) uuid.UUID {
			return customerMail(t, e, ws, mb, from, subject, "x", func(nt *domain.NewTicket, nm *domain.NewMessage) {
				nt.Channel, nt.MailboxID, nm.MailboxID = domain.ChannelAPI, nil, nil
			})
		}},
		{name: "chat conversations stay separate", earlier: func(from string) uuid.UUID {
			return customerMail(t, e, ws, mb, from, subject, "x", func(nt *domain.NewTicket, nm *domain.NewMessage) {
				nt.Channel, nt.MailboxID, nm.MailboxID = domain.ChannelChat, nil, nil
			})
		}, later: func(nt *domain.NewTicket, nm *domain.NewMessage) {
			nt.Channel, nt.MailboxID, nm.MailboxID = domain.ChannelChat, nil, nil
		}},
		{name: "API tickets stay separate", earlier: func(from string) uuid.UUID {
			return customerMail(t, e, ws, mb, from, subject, "x", func(nt *domain.NewTicket, nm *domain.NewMessage) {
				nt.Channel, nt.MailboxID, nm.MailboxID = domain.ChannelAPI, nil, nil
			})
		}, later: func(nt *domain.NewTicket, nm *domain.NewMessage) {
			nt.Channel, nt.MailboxID, nm.MailboxID = domain.ChannelAPI, nil, nil
		}},
		{name: "a private team's ticket", earlier: func(from string) uuid.UUID {
			return customerMail(t, e, ws, mb, from, subject, "x", func(nt *domain.NewTicket, _ *domain.NewMessage) { nt.TeamID = &private })
		}},
		{name: "into a private team's ticket from it", merge: true, earlier: func(from string) uuid.UUID {
			return customerMail(t, e, ws, mb, from, subject, "x", func(nt *domain.NewTicket, _ *domain.NewMessage) { nt.TeamID = &private })
		}, later: func(nt *domain.NewTicket, _ *domain.NewMessage) { nt.TeamID = &private }},
		{name: "a public team's ticket", merge: true, earlier: func(from string) uuid.UUID {
			return customerMail(t, e, ws, mb, from, subject, "x", func(nt *domain.NewTicket, _ *domain.NewMessage) { nt.TeamID = &public })
		}},
		{name: "mail that failed sender checks", later: func(_ *domain.NewTicket, nm *domain.NewMessage) { nm.AuthFailed = true }},
	}
	for i, c := range cases {
		from := hdtest.Fmt("customer%d@customer.test", i)
		var earlier uuid.UUID
		if c.earlier != nil {
			earlier = c.earlier(from)
		} else {
			earlier = customerMail(t, e, ws, mb, from, subject, "First")
		}
		runTriggers(t, e, ws.ID)
		var edits []func(*domain.NewTicket, *domain.NewMessage)
		if c.later != nil {
			edits = append(edits, c.later)
		}
		later := customerMail(t, e, ws, mb, from, "Re: "+subject, "Second", edits...)
		runTriggers(t, e, ws.ID)
		s := mergedInto(t, e, later)
		if c.merge && (s.Into == nil || *s.Into != earlier) {
			t.Errorf("%s: %+v, want merged into %s", c.name, s, earlier)
		}
		if !c.merge && (s.Into != nil || s.Status != domain.StatusOpen) {
			t.Errorf("%s: merged %+v", c.name, s)
		}
	}

	// Tickets without a contact, or started by the team, never merge.
	for range 2 {
		customerMail(t, e, ws, mb, "", subject, "Anonymous")
	}
	runTriggers(t, e, ws.ID)
	if n := countRows(t, e, `SELECT count(*) FROM tickets WHERE workspace_id = $1 AND contact_id IS NULL AND merged_into_id IS NOT NULL`, ws.ID); n != 0 {
		t.Fatalf("%d tickets without a contact merged", n)
	}
	customerMail(t, e, ws, mb, "agent.started@customer.test", subject, "First")
	runTriggers(t, e, ws.ID)
	out := createTicket(t, e, ws, map[string]any{"subject": subject, "channel": "email", "mailbox_id": mb.String(),
		"contact": map[string]any{"email": "agent.started@customer.test"}})
	runTriggers(t, e, ws.ID)
	if s := mergedInto(t, e, out.ID); s.Into != nil {
		t.Fatalf("a ticket the team started merged %+v", s)
	}
}

func withStatus(t *testing.T, e *hdtest.Env, ws hdtest.WS, mb uuid.UUID, status string) func(string) uuid.UUID {
	return withSQL(t, e, ws, mb, `UPDATE tickets SET status = '`+status+`' WHERE id = $1`)
}

// withSQL writes the contact's earlier ticket, then changes it with sql.
func withSQL(t *testing.T, e *hdtest.Env, ws hdtest.WS, mb uuid.UUID, sql string) func(string) uuid.UUID {
	return func(from string) uuid.UUID {
		id := customerMail(t, e, ws, mb, from, "Invoice 77 is wrong", "First")
		runTriggers(t, e, ws.ID)
		e.Exec(sql, id)
		return id
	}
}

func TestAutomationMergeAnySubject(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	addAutomation(t, e, ws, jobs.TriggerTicketCreated, 0, []map[string]string{}, []map[string]any{mergeAction("2", false)})
	older := customerMail(t, e, ws, mb, "dan@customer.test", "Broken lamp", "x")
	newer := customerMail(t, e, ws, mb, "dan@customer.test", "Return label", "x")
	runTriggers(t, e, ws.ID)
	// Without same_subject, the most recent earlier ticket of the contact is joined.
	latest := customerMail(t, e, ws, mb, "dan@customer.test", "Something else entirely", "x")
	runTriggers(t, e, ws.ID)
	if s := mergedInto(t, e, newer); s.Into == nil || *s.Into != older {
		t.Fatalf("newer %+v, want merged into %s", s, older)
	}
	if s := mergedInto(t, e, latest); s.Into == nil || *s.Into != older {
		t.Fatalf("latest %+v, want merged into %s", s, older)
	}
	// The window is the action's: three days ago is out of a two-day window.
	e.Exec(`UPDATE tickets SET created_at = now() - interval '3 days', last_message_at = now() - interval '3 days' WHERE id = $1`, older)
	late := customerMail(t, e, ws, mb, "dan@customer.test", "Broken lamp", "x")
	runTriggers(t, e, ws.ID)
	if s := mergedInto(t, e, late); s.Into != nil {
		t.Fatalf("merged outside the window %+v", s)
	}
}
