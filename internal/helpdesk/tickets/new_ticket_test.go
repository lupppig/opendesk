package tickets_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// newTicketRecipients is who got the new_ticket notification of a ticket, by user id.
func newTicketRecipients(t *testing.T, e *hdtest.Env, ws hdtest.WS, tid uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := e.D.DB.Pool.Query(e.Ctx, `SELECT user_id FROM notifications WHERE workspace_id = $1 AND ticket_id = $2
		AND type = 'new_ticket' ORDER BY user_id`, ws.ID, tid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func sameUsers(got []uuid.UUID, want ...uuid.UUID) bool {
	a, b := slices.Clone(got), slices.Clone(want)
	cmp := func(x, y uuid.UUID) int { return slices.Compare(x[:], y[:]) }
	slices.SortFunc(a, cmp)
	slices.SortFunc(b, cmp)
	return slices.Equal(a, b)
}

// apiTicket creates a ticket with an API key (no person created it) and runs its triggers.
func apiTicket(t *testing.T, e *hdtest.Env, ws hdtest.WS, key string, body map[string]any) domain.Ticket {
	t.Helper()
	b := map[string]any{"kind": "note", "subject": "Help needed", "body_html": "<p>Hello there</p>"}
	for k, v := range body {
		b[k] = v
	}
	var tk domain.Ticket
	e.Do("POST", "/v1/tickets", hdtest.Auth{Bearer: key}, b).Want(t, 201).JSON(t, &tk)
	runTriggers(t, e, ws.ID)
	return tk
}

// customerTicket creates a ticket the way the email pipeline and the widget do: a ticket
// and the customer's first message, with its ticket_created trigger.
func customerTicket(t *testing.T, e *hdtest.Env, ws hdtest.WS, nt domain.NewTicket) uuid.UUID {
	t.Helper()
	var tid uuid.UUID
	err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		cid, _, err := domain.UpsertContactByEmail(e.Ctx, tx, ws.ID, "sam.ortiz@customer.test", "Sam Ortiz")
		if err != nil {
			return err
		}
		nt.WorkspaceID, nt.ContactID = ws.ID, &cid
		if nt.Subject == "" {
			nt.Subject = "Cannot connect"
		}
		tk, err := domain.CreateTicket(e.Ctx, tx, nt)
		if err != nil {
			return err
		}
		tid = tk.ID
		_, err = domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: ws.ID, TicketID: tk.ID, Kind: "reply",
			Direction: "inbound", AuthorType: "contact", AuthorContactID: &cid, BodyHTML: "<p>Hi</p>", BodyText: "Hi",
			FromEmail: "sam.ortiz@customer.test", MailboxID: nt.MailboxID, Trigger: jobs.TriggerTicketCreated})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	runTriggers(t, e, ws.ID)
	return tid
}

// withAI turns on the instance's AI for one test.
func withAI(t *testing.T, e *hdtest.Env) {
	t.Helper()
	prev := e.D.Cfg.AIProvider
	e.D.Cfg.AIProvider = "anthropic"
	t.Cleanup(func() { e.D.Cfg.AIProvider = prev })
}

func TestNewTicketNotifiesASoloOwner(t *testing.T) {
	e, ws := newEnv(t)
	// A workspace of one person (and a viewer, who is never notified).
	e.Exec(`DELETE FROM members WHERE workspace_id = $1 AND user_id IN ($2, $3)`, ws.ID, ws.Admin.ID, ws.Agent.ID)
	key := e.APIKey(ws, "tickets:write", "tickets:read")
	tk := apiTicket(t, e, ws, key, nil)
	if got := newTicketRecipients(t, e, ws, tk.ID); !sameUsers(got, ws.Owner.ID) {
		t.Fatalf("recipients %v, want the owner", got)
	}
	var channel string
	e.Scalar(`SELECT data->>'channel' FROM notifications WHERE ticket_id = $1 AND type = 'new_ticket'`, &channel, tk.ID)
	if channel != "api" {
		t.Fatalf("data.channel %q", channel)
	}

	// An inbound email names its customer.
	mb := addMailbox(t, e, ws)
	tid := customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelEmail, MailboxID: &mb})
	if got := newTicketRecipients(t, e, ws, tid); !sameUsers(got, ws.Owner.ID) {
		t.Fatalf("email recipients %v", got)
	}
	var name string
	e.Scalar(`SELECT data->'contact'->>'name' FROM notifications WHERE ticket_id = $1 AND type = 'new_ticket'`, &name, tid)
	if name != "Sam Ortiz" {
		t.Fatalf("data.contact.name %q", name)
	}

	// The owner creating a ticket themself hears nothing.
	tk = createTicketAs(t, e, ws, ws.Owner, map[string]any{"kind": "note"})
	runTriggers(t, e, ws.ID)
	if got := newTicketRecipients(t, e, ws, tk.ID); len(got) != 0 {
		t.Fatalf("the creator was notified: %v", got)
	}
}

func createTicketAs(t *testing.T, e *hdtest.Env, ws hdtest.WS, u hdtest.User, body map[string]any) domain.Ticket {
	t.Helper()
	body["subject"], body["body_html"] = "Help needed", "<p>Hello there</p>"
	var tk domain.Ticket
	e.Do("POST", "/v1/tickets", u.As(ws), body).Want(t, 201).JSON(t, &tk)
	return tk
}

func TestNewTicketLeavesOutItsCreatorAndAssignedTickets(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"kind": "note"}) // by the agent
	runTriggers(t, e, ws.ID)
	if got := newTicketRecipients(t, e, ws, tk.ID); !sameUsers(got, ws.Owner.ID, ws.Admin.ID) {
		t.Fatalf("recipients %v, want the owner and the admin", got)
	}

	// Auto-assignment gave it to someone: they hear "assigned", nobody hears new_ticket.
	e.SetSettings(ws, map[string]any{"auto_assign": "round_robin"})
	tk = createTicket(t, e, ws, map[string]any{"kind": "note"})
	runTriggers(t, e, ws.ID)
	if got := newTicketRecipients(t, e, ws, tk.ID); len(got) != 0 {
		t.Fatalf("an assigned ticket was announced to %v", got)
	}
	if a := getTicket(t, e, ws, tk.ID).Assignee; a == nil {
		t.Fatal("not auto-assigned")
	}
	e.SetSettings(ws, map[string]any{"auto_assign": "none"})

	// Assigned on creation.
	tk = createTicket(t, e, ws, map[string]any{"kind": "note", "assignee_id": ws.Admin.ID.String()})
	runTriggers(t, e, ws.ID)
	if got := newTicketRecipients(t, e, ws, tk.ID); len(got) != 0 {
		t.Fatalf("an assigned ticket was announced to %v", got)
	}
}

func TestNewTicketSettings(t *testing.T) {
	e, ws := newEnv(t)
	key := e.APIKey(ws, "tickets:write", "tickets:read")
	var extra []uuid.UUID
	for _, n := range []string{"Bea Agent", "Cal Agent", "Dee Agent", "Eli Agent"} {
		extra = append(extra, e.AddUser(ws, auth.RoleAgent, n).ID)
	}
	everyone := append([]uuid.UUID{ws.Owner.ID, ws.Admin.ID, ws.Agent.ID}, extra...)

	// auto with more than 5 people: only the owners and admins (no team).
	tk := apiTicket(t, e, ws, key, nil)
	if got := newTicketRecipients(t, e, ws, tk.ID); !sameUsers(got, ws.Owner.ID, ws.Admin.ID) {
		t.Fatalf("auto: %v", got)
	}
	e.SetSettings(ws, map[string]any{"new_ticket_notify": "everyone"})
	tk = apiTicket(t, e, ws, key, nil)
	if got := newTicketRecipients(t, e, ws, tk.ID); !sameUsers(got, everyone...) {
		t.Fatalf("everyone: %v", got)
	}
	e.SetSettings(ws, map[string]any{"new_ticket_notify": "off"})
	tk = apiTicket(t, e, ws, key, nil)
	if got := newTicketRecipients(t, e, ws, tk.ID); len(got) != 0 {
		t.Fatalf("off: %v", got)
	}

	// A team of six with one lead: auto tells the lead, leads tells the lead, a small
	// team hears in full.
	team := addTeam(t, e, ws, everyone[2:]...)
	e.Exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'lead'), ($1, $3, 'observer')`, team, ws.Owner.ID, ws.Admin.ID)
	e.SetSettings(ws, map[string]any{"new_ticket_notify": "auto"})
	tk = apiTicket(t, e, ws, key, map[string]any{"team_id": team.String()})
	if got := newTicketRecipients(t, e, ws, tk.ID); !sameUsers(got, ws.Owner.ID) {
		t.Fatalf("auto, big team: %v", got)
	}
	e.SetSettings(ws, map[string]any{"new_ticket_notify": "leads"})
	tk = apiTicket(t, e, ws, key, map[string]any{"team_id": team.String()})
	if got := newTicketRecipients(t, e, ws, tk.ID); !sameUsers(got, ws.Owner.ID) {
		t.Fatalf("leads: %v", got)
	}
	e.Exec(`DELETE FROM team_members WHERE team_id = $1 AND user_id IN ($2, $3)`, team, extra[2], extra[3])
	e.SetSettings(ws, map[string]any{"new_ticket_notify": "auto"})
	tk = apiTicket(t, e, ws, key, map[string]any{"team_id": team.String()})
	if got := newTicketRecipients(t, e, ws, tk.ID); !sameUsers(got, ws.Owner.ID, ws.Agent.ID, extra[0], extra[1]) {
		t.Fatalf("auto, small team (observer left out): %v", got)
	}
}

func TestNewTicketWaitsForTheBot(t *testing.T) {
	e, ws := newEnv(t)
	withAI(t, e)
	e.SetSettings(ws, map[string]any{"ai": map[string]any{"enabled": true, "widget_answers": true}})
	// The widget's answer bot takes the chat: people hear at its handoff.
	tid := customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelChat, WidgetID: &ws.WidgetID})
	if got := newTicketRecipients(t, e, ws, tid); len(got) != 0 {
		t.Fatalf("a chat the bot answers was announced to %v", got)
	}
	e.SetSettings(ws, map[string]any{"ai": map[string]any{"enabled": true, "widget_answers": false}})
	tid = customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelChat, WidgetID: &ws.WidgetID})
	if got := newTicketRecipients(t, e, ws, tid); len(got) != 3 {
		t.Fatalf("a chat without the bot: %v", got)
	}

	// An address whose bot sends its own replies waits; one that only drafts does not.
	mb := addMailbox(t, e, ws)
	e.Exec(`UPDATE mailboxes SET ai_answers = true, ai_answers_mode = 'send' WHERE id = $1`, mb)
	tid = customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelEmail, MailboxID: &mb})
	if got := newTicketRecipients(t, e, ws, tid); len(got) != 0 {
		t.Fatalf("an email the bot answers was announced to %v", got)
	}
	e.Exec(`UPDATE mailboxes SET ai_answers_mode = 'draft' WHERE id = $1`, mb)
	tid = customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelEmail, MailboxID: &mb})
	if got := newTicketRecipients(t, e, ws, tid); len(got) != 3 {
		t.Fatalf("a drafted email: %v", got)
	}
	// Outside hours only: within open hours the team hears now. Every day is open.
	day := []map[string]string{{"start": "00:00", "end": "24:00"}}
	e.SetSettings(ws, map[string]any{"timezone": "UTC", "business_hours": map[string]any{"enabled": true, "holidays": []any{},
		"schedule": map[string]any{"mon": day, "tue": day, "wed": day, "thu": day, "fri": day, "sat": day, "sun": day}}})
	e.Exec(`UPDATE mailboxes SET ai_answers_mode = 'send', ai_answers_outside_hours = true WHERE id = $1`, mb)
	tid = customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelEmail, MailboxID: &mb})
	if got := newTicketRecipients(t, e, ws, tid); len(got) != 3 {
		t.Fatalf("within hours: %v", got)
	}

	// AI off for the workspace: nothing waits.
	e.SetSettings(ws, map[string]any{"ai": map[string]any{"enabled": false, "widget_answers": true}})
	tid = customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelChat, WidgetID: &ws.WidgetID})
	if got := newTicketRecipients(t, e, ws, tid); len(got) != 3 {
		t.Fatalf("AI off: %v", got)
	}
}

func TestNewTicketSkipsSpamAndHandoffs(t *testing.T) {
	e, ws := newEnv(t)
	key := e.APIKey(ws, "tickets:write", "tickets:read")
	var b map[string]any
	for _, mark := range []string{`spam = true`, `custom = custom || '{"ai_handoff": true}'`, `status = 'solved'`} {
		e.Do("POST", "/v1/tickets", hdtest.Auth{Bearer: key}, map[string]any{"kind": "note", "subject": "Win a prize",
			"body_html": "<p>x</p>"}).Want(t, 201).JSON(t, &b)
		id := uuid.MustParse(b["id"].(string))
		e.Exec(`UPDATE tickets SET `+mark+` WHERE id = $1`, id)
		runTriggers(t, e, ws.ID)
		if got := newTicketRecipients(t, e, ws, id); len(got) != 0 {
			t.Fatalf("%s: announced to %v", mark, got)
		}
	}
}

// customerReply adds a customer's reply to a ticket and runs its triggers.
func customerReply(t *testing.T, e *hdtest.Env, ws hdtest.WS, tid uuid.UUID) {
	t.Helper()
	err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		cid, _, err := domain.UpsertContactByEmail(e.Ctx, tx, ws.ID, "sam.ortiz@customer.test", "Sam Ortiz")
		if err != nil {
			return err
		}
		_, err = domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: ws.ID, TicketID: tid, Kind: "reply",
			Direction: "inbound", AuthorType: "contact", AuthorContactID: &cid, BodyHTML: "<p>Any news?</p>", BodyText: "Any news?",
			FromEmail: "sam.ortiz@customer.test"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	runTriggers(t, e, ws.ID)
}

func repliedRecipients(t *testing.T, e *hdtest.Env, ws hdtest.WS, tid uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := e.D.DB.Pool.Query(e.Ctx, `SELECT user_id FROM notifications WHERE workspace_id = $1 AND ticket_id = $2
		AND type = 'customer_replied'`, ws.ID, tid)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCustomerReplyOnAnUnassignedTicketNotifies(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	tid := customerTicket(t, e, ws, domain.NewTicket{Channel: domain.ChannelEmail, MailboxID: &mb})

	// Nobody has it: the reply reaches the people the new ticket did.
	customerReply(t, e, ws, tid)
	if got := repliedRecipients(t, e, ws, tid); !sameUsers(got, newTicketRecipients(t, e, ws, tid)...) || len(got) == 0 {
		t.Fatalf("unassigned reply recipients %v", got)
	}
	var preview string
	e.Scalar(`SELECT data->>'preview' FROM notifications WHERE ticket_id = $1 AND type = 'customer_replied' LIMIT 1`, &preview, tid)
	if preview != "Any news?" {
		t.Fatalf("preview %q", preview)
	}

	// Once someone has it, only they hear.
	e.Exec(`DELETE FROM notifications WHERE ticket_id = $1`, tid)
	e.Exec(`UPDATE tickets SET assignee_id = $2 WHERE id = $1`, tid, ws.Agent.ID)
	customerReply(t, e, ws, tid)
	if got := repliedRecipients(t, e, ws, tid); !sameUsers(got, ws.Agent.ID) {
		t.Fatalf("assigned reply recipients %v", got)
	}

	// With new ticket notifications off, an unassigned reply is quiet too.
	e.Exec(`DELETE FROM notifications WHERE ticket_id = $1`, tid)
	e.Exec(`UPDATE tickets SET assignee_id = NULL WHERE id = $1`, tid)
	e.SetSettings(ws, map[string]any{"new_ticket_notify": "off"})
	customerReply(t, e, ws, tid)
	if got := repliedRecipients(t, e, ws, tid); len(got) != 0 {
		t.Fatalf("notified with notifications off: %v", got)
	}
}
