package tickets_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/helpdesk/widget"
	"github.com/useopendesk/opendesk/internal/ids"
)

// privateTicket creates a ticket in a new private team led by the admin.
func privateTicket(t *testing.T, e *hdtest.Env, ws hdtest.WS) (domain.Ticket, uuid.UUID) {
	t.Helper()
	priv := addPrivateTeam(t, e, ws, map[uuid.UUID]string{ws.Admin.ID: hd.TeamLead})
	var tk domain.Ticket
	e.Do("POST", "/v1/tickets", ws.Admin.As(ws), map[string]any{"subject": "Breach", "body_html": "<p>x</p>", "kind": "note",
		"team_id": priv.String()}).Want(t, 201).JSON(t, &tk)
	return tk, priv
}

// An event whose data names a ticket in a private team goes to that team, whatever the
// caller passed; only ticket.hidden is published to the whole workspace.
func TestEventsScopedByTheirData(t *testing.T) {
	e, ws := newEnv(t)
	tk, priv := privateTicket(t, e, ws)
	public := createTicket(t, e, ws, map[string]any{"kind": "note"})
	idCopy := tk.ID
	cases := []struct {
		name    string
		data    any
		private bool
	}{
		{"ticket_id uuid", map[string]any{"ticket_id": tk.ID, "suggested_reply": map[string]any{"body": "x"}}, true},
		{"ticket_id pointer", map[string]any{"ticket_id": &idCopy}, true},
		{"ticket_id string", map[string]any{"ticket_id": tk.ID.String()}, true},
		{"ticket", map[string]any{"ticket": tk}, true},
		{"message", map[string]any{"message": domain.Message{ID: ids.New(), TicketID: tk.ID}}, true},
		{"notification", map[string]any{"notification": hd.Notification{ID: ids.New(), Ticket: &hd.NotificationTicket{ID: tk.ID}}}, true},
		{"bare ticket", tk, true},
		{"nil pointer", map[string]any{"ticket": (*domain.Ticket)(nil)}, false},
		{"public ticket", map[string]any{"ticket_id": public.ID}, false},
		{"no ticket", map[string]any{"tag": "x"}, false},
	}
	for _, c := range cases {
		var env events.Envelope
		err := pgx.BeginFunc(e.Ctx, e.D.DB.Pool, func(tx pgx.Tx) error {
			var err error
			env, err = events.Publish(e.Ctx, tx, events.Event{WorkspaceID: ws.ID, Type: "test.scoped", Data: c.data})
			return err
		})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var team *uuid.UUID
		e.Scalar(`SELECT team_id FROM events WHERE workspace_id = $1 AND seq = $2`, &team, ws.ID, env.Seq)
		teamTopic := slices.Contains(env.Topics, events.TeamTopic(ws.ID, priv))
		wsTopic := slices.Contains(env.Topics, events.WorkspaceTopic(ws.ID))
		if c.private && (team == nil || *team != priv || !teamTopic || wsTopic) {
			t.Fatalf("%s: team %v topics %v", c.name, team, env.Topics)
		}
		if !c.private && (team != nil || teamTopic || !wsTopic) {
			t.Fatalf("%s: scoped %v topics %v", c.name, team, env.Topics)
		}
	}
	// ticket.hidden names the ticket but reaches everyone.
	err := pgx.BeginFunc(e.Ctx, e.D.DB.Pool, func(tx pgx.Tx) error { return hd.PublishTicketHidden(e.Ctx, tx, ws.ID, tk.ID, priv) })
	if err != nil {
		t.Fatal(err)
	}
	var topics []string
	e.Scalar(`SELECT topics FROM events WHERE workspace_id = $1 AND type = 'ticket.hidden' ORDER BY seq DESC LIMIT 1`, &topics, ws.ID)
	if len(topics) != 1 || topics[0] != events.WorkspaceTopic(ws.ID) {
		t.Fatalf("ticket.hidden topics %v", topics)
	}
}

// A file on a private team's ticket is not found for others; only its uploader attaches an
// upload.
func TestPrivateTicketAttachments(t *testing.T) {
	e := hdtest.New(t, tickets.Register, widget.Register)
	ws := e.NewWorkspace()
	tk, _ := privateTicket(t, e, ws)
	var att domain.Attachment
	e.Upload("/v1/attachments", ws.Admin.As(ws), "notes.txt", []byte("secret notes")).Want(t, 201).JSON(t, &att)
	path := "/v1/attachments/" + att.ID.String()
	// Before it is attached it is only an upload of the workspace; another agent cannot send it.
	e.Do("POST", "/v1/tickets/"+createTicket(t, e, ws, map[string]any{"kind": "note"}).ID.String()+"/messages", ws.Agent.As(ws),
		map[string]any{"kind": "note", "body_html": "mine", "attachment_ids": []string{att.ID.String()}}).Want(t, 422)
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Admin.As(ws),
		map[string]any{"kind": "note", "body_html": "see file", "attachment_ids": []string{att.ID.String()}}).Want(t, 201)
	if r := e.Do("GET", path, ws.Admin.As(ws), nil); r.Status != 200 && r.Status != 302 {
		t.Fatalf("member download %d", r.Status)
	}
	e.Do("GET", path, ws.Agent.As(ws), nil).Want(t, 404)
	key := e.APIKey(ws, "tickets:read")
	e.Do("GET", path, hdtest.Auth{Bearer: key}, nil).Want(t, 404)
	// The assignee sees it.
	e.Exec(`UPDATE tickets SET assignee_id = $2 WHERE id = $1`, tk.ID, ws.Agent.ID)
	if r := e.Do("GET", path, ws.Agent.As(ws), nil); r.Status != 200 && r.Status != 302 {
		t.Fatalf("assignee download %d", r.Status)
	}
}

// Merging into a ticket of another, private team leaves the target unnamed on the source.
func TestMergeIntoPrivateTeamUnnamed(t *testing.T) {
	e, ws := newEnv(t)
	tk, priv := privateTicket(t, e, ws)
	src := createTicket(t, e, ws, map[string]any{"kind": "note"})
	e.Do("POST", "/v1/tickets/"+src.ID.String()+"/merge", ws.Admin.As(ws), map[string]any{"into_id": tk.ID.String()}).Want(t, 200)
	var into *string
	var number *float64
	e.Scalar(`SELECT data->>'into' FROM activities WHERE ticket_id = $1 AND type = 'merged'`, &into, src.ID)
	e.Scalar(`SELECT (data->>'into_number')::float FROM activities WHERE ticket_id = $1 AND type = 'merged'`, &number, src.ID)
	if into != nil || number != nil {
		t.Fatalf("source names the private target: %v %v", into, number)
	}
	// Within the same private team the target is named.
	a := createTicket(t, e, ws, map[string]any{"kind": "note"})
	b := createTicket(t, e, ws, map[string]any{"kind": "note"})
	e.Exec(`UPDATE tickets SET team_id = $2 WHERE id = ANY($1)`, []uuid.UUID{a.ID, b.ID}, priv)
	e.Do("POST", "/v1/tickets/"+a.ID.String()+"/merge", ws.Admin.As(ws), map[string]any{"into_id": b.ID.String()}).Want(t, 200)
	e.Scalar(`SELECT data->>'into' FROM activities WHERE ticket_id = $1 AND type = 'merged'`, &into, a.ID)
	if into == nil || *into != b.ID.String() {
		t.Fatalf("same team target unnamed: %v", into)
	}
}

// A reply goes out only from an address the caller may send from, and a ticket leaving a
// private team leaves its address.
func TestReplyAddressEnforced(t *testing.T) {
	e, ws := newEnv(t)
	priv := addPrivateTeam(t, e, ws, map[uuid.UUID]string{ws.Admin.ID: hd.TeamLead})
	public := addTeam(t, e, ws, ws.Agent.ID)
	mbPriv := addMailbox(t, e, ws)
	e.Exec(`UPDATE mailboxes SET team_id = $2 WHERE id = $1`, mbPriv, priv)
	mbOpen := addMailbox(t, e, ws)

	var tk domain.Ticket
	e.Do("POST", "/v1/tickets", ws.Admin.As(ws), map[string]any{"subject": "Order", "body_html": "<p>x</p>", "kind": "note",
		"mailbox_id": mbPriv.String(), "contact": map[string]any{"email": "c@example.com"}}).Want(t, 201).JSON(t, &tk)
	// Moved to a team everyone sees, still replying from the private team's address.
	e.Exec(`UPDATE tickets SET team_id = $2 WHERE id = $1`, tk.ID, public)
	msgs := "/v1/tickets/" + tk.ID.String() + "/messages"
	r := e.Do("POST", msgs, ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>hi</p>"}).Want(t, 403)
	if errCode(t, r) != "reply_address_forbidden" {
		t.Fatalf("code %s", r.Body)
	}
	// Nor does reading its signature through the template values.
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/variables?mailbox_id="+mbPriv.String(), ws.Agent.As(ws), nil).Want(t, 404)
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/variables?mailbox_id="+mbPriv.String(), ws.Admin.As(ws), nil).Want(t, 200)
	// Naming the ticket's current address does not get around it.
	r = e.Do("POST", msgs, ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>hi</p>", "mailbox_id": mbPriv.String()}).Want(t, 403)
	if errCode(t, r) != "reply_address_forbidden" {
		t.Fatalf("named address: %s", r.Body)
	}
	e.Do("POST", msgs, ws.Agent.As(ws), map[string]any{"kind": "note", "body_html": "<p>note</p>"}).Want(t, 201)
	e.Do("POST", msgs, ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>hi</p>", "mailbox_id": mbOpen.String()}).Want(t, 201)
	if got := getTicket(t, e, ws, tk.ID); got.MailboxID == nil || *got.MailboxID != mbOpen {
		t.Fatalf("reply address %v", got.MailboxID)
	}

	// Leaving the private team leaves its address for one the brand has.
	var tk2 domain.Ticket
	e.Do("POST", "/v1/tickets", ws.Admin.As(ws), map[string]any{"subject": "Leak", "body_html": "<p>x</p>", "kind": "note",
		"mailbox_id": mbPriv.String(), "contact": map[string]any{"email": "d@example.com"}}).Want(t, 201).JSON(t, &tk2)
	if tk2.TeamID == nil || *tk2.TeamID != priv {
		t.Fatalf("team %v", tk2.TeamID)
	}
	var got domain.Ticket
	e.Do("PATCH", "/v1/tickets/"+tk2.ID.String(), ws.Admin.As(ws), map[string]any{"team_id": nil}).Want(t, 200).JSON(t, &got)
	if got.MailboxID == nil || *got.MailboxID != mbOpen {
		t.Fatalf("still on the private team's address: %v", got.MailboxID)
	}

	// A brand change picks an address that is not another private team's.
	brand2 := ids.New()
	e.Exec(`INSERT INTO brands (id, workspace_id, name, slug) VALUES ($1, $2, 'Other', $3)`, brand2, ws.ID, "b-"+brand2.String()[24:])
	mbPriv2, mbOpen2 := addMailbox(t, e, ws), addMailbox(t, e, ws)
	e.Exec(`UPDATE mailboxes SET brand_id = $2, team_id = $3, created_at = now() - interval '1 day' WHERE id = $1`, mbPriv2, brand2, priv)
	e.Exec(`UPDATE mailboxes SET brand_id = $2 WHERE id = $1`, mbOpen2, brand2)
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"brand_id": brand2.String()}).Want(t, 200).JSON(t, &got)
	if got.MailboxID == nil || *got.MailboxID != mbOpen2 {
		t.Fatalf("brand switch picked %v, want %v", got.MailboxID, mbOpen2)
	}
}

// team_id takes a list mixing ids, mine and none; PATCH flags a ticket the caller can no
// longer see.
func TestTeamListFilterAndHiddenPatch(t *testing.T) {
	e, ws := newEnv(t)
	a, b := addTeam(t, e, ws, ws.Agent.ID), addTeam(t, e, ws)
	ta := createTicket(t, e, ws, map[string]any{"kind": "note", "team_id": a.String()})
	tb := createTicket(t, e, ws, map[string]any{"kind": "note", "team_id": b.String()})
	tn := createTicket(t, e, ws, map[string]any{"kind": "note"})
	agent := ws.Agent.As(ws)
	for q, want := range map[string][]uuid.UUID{
		"team_id=" + b.String() + ",none":          {tb.ID, tn.ID},
		"team_id=mine," + b.String():               {ta.ID, tb.ID},
		"team_id=mine":                             {ta.ID},
		"team_id=none,mine":                        {ta.ID, tn.ID},
		"team_id=" + a.String() + "," + a.String(): {ta.ID},
	} {
		got := ticketIDs(t, e.Do("GET", "/v1/tickets?"+q, agent, nil).Want(t, 200))
		if len(got) != len(want) {
			t.Fatalf("%s: %v", q, got)
		}
		for _, id := range want {
			if !slices.Contains(got, id.String()) {
				t.Fatalf("%s: %v", q, got)
			}
		}
	}
	e.Do("GET", "/v1/tickets?team_id=mine,nope", agent, nil).Want(t, 422)

	priv := addPrivateTeam(t, e, ws, map[uuid.UUID]string{ws.Admin.ID: hd.TeamLead})
	r := e.Do("PATCH", "/v1/tickets/"+tn.ID.String(), ws.Admin.As(ws), map[string]any{"team_id": priv.String()}).Want(t, 200)
	if r.Header.Get("OD-Ticket-Hidden") != "" {
		t.Fatal("a member was told the ticket is hidden")
	}
	key := e.APIKey(ws, "tickets:write", "tickets:read")
	r = e.Do("PATCH", "/v1/tickets/"+ta.ID.String(), hdtest.Auth{Bearer: key}, map[string]any{"team_id": priv.String()}).Want(t, 200)
	var got domain.Ticket
	r.JSON(t, &got)
	if r.Header.Get("OD-Ticket-Hidden") != "true" || got.TeamID == nil || *got.TeamID != priv {
		t.Fatalf("hidden after patch: %q %v", r.Header.Get("OD-Ticket-Hidden"), got.TeamID)
	}
}
