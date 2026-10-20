package tickets_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

func TestCreateAndGet(t *testing.T) {
	e, ws := newEnv(t)
	_, seq := e.Events(ws.ID, 0)
	tk := createTicket(t, e, ws, map[string]any{"subject": "Broken login", "contact": map[string]any{"email": "Jane@Example.com", "name": "Jane Doe"},
		"tags": []string{"Login", "vip"}, "priority": "high", "body_html": `<p>Hi <script>alert(1)</script>there</p>`})
	if tk.Channel != "api" || tk.Priority != "high" || tk.Contact == nil || *tk.Contact.Email != "jane@example.com" || tk.Contact.Name != "Jane Doe" {
		t.Fatalf("unexpected ticket %+v", tk)
	}
	if len(tk.Tags) != 2 || tk.MessageCount != 1 || tk.FirstRespondedAt == nil {
		t.Fatalf("tags/messages: %+v", tk)
	}
	types, _ := e.Events(ws.ID, seq)
	for _, want := range []string{"contact.created", "ticket.created", "tag.created", "message.created", "ticket.updated"} {
		if !slices.Contains(types, want) {
			t.Errorf("missing event %s in %v", want, types)
		}
	}
	var msgs struct{ Data []domain.Message }
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &msgs)
	if len(msgs.Data) != 1 || strings.Contains(msgs.Data[0].BodyHTML, "script") || msgs.Data[0].BodyText != "Hi there" ||
		msgs.Data[0].DeliveryStatus != "none" || msgs.Data[0].Author.User == nil {
		t.Fatalf("message: %+v", msgs.Data)
	}
	if n := len(e.Jobs(jobs.TicketTriggerArgs{}.Kind(), ws.ID)); n != 1 {
		t.Fatalf("want one ticket_created trigger, got %d", n)
	}

	for _, ref := range []string{tk.ID.String(), fmt.Sprintf("%d", tk.Number), fmt.Sprintf("%%23%d", tk.Number)} {
		var got domain.Ticket
		e.Do("GET", "/v1/tickets/"+ref, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &got)
		if got.ID != tk.ID {
			t.Fatalf("ref %s resolved to %s", ref, got.ID)
		}
	}
	e.Do("GET", "/v1/tickets/999999", ws.Agent.As(ws), nil).Want(t, 404)
	e.Do("GET", "/v1/tickets/not-a-ticket", ws.Agent.As(ws), nil).Want(t, 404)

	// Viewers read only.
	e.Do("POST", "/v1/tickets", ws.Viewer.As(ws), map[string]any{"subject": "x", "body_html": "y"}).Want(t, 403)
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Viewer.As(ws), map[string]any{"status": "solved"}).Want(t, 403)
	// Unauthenticated.
	e.Do("GET", "/v1/tickets", hdtest.Auth{}, nil).Want(t, 401)

	// Another workspace's credentials never see this ticket.
	other := e.NewWorkspace()
	for _, path := range []string{"/v1/tickets/" + tk.ID.String(), fmt.Sprintf("/v1/tickets/%d", tk.Number),
		"/v1/tickets/" + tk.ID.String() + "/messages", "/v1/tickets/" + tk.ID.String() + "/activities",
		"/v1/messages/" + msgs.Data[0].ID.String() + "/original"} {
		res := e.Do("GET", path, other.Owner.As(other), nil)
		if path == fmt.Sprintf("/v1/tickets/%d", tk.Number) && res.Status == 200 {
			var got domain.Ticket
			res.JSON(t, &got)
			if got.ID == tk.ID {
				t.Fatal("cross-workspace number lookup leaked")
			}
			continue
		}
		res.Want(t, 404)
	}
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), other.Owner.As(other), map[string]any{"status": "solved"}).Want(t, 404)
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", other.Owner.As(other), map[string]any{"kind": "note", "body_html": "x"}).Want(t, 404)
	e.Do("DELETE", "/v1/tickets/"+tk.ID.String(), other.Owner.As(other), nil).Want(t, 404)
	key := e.APIKey(other, auth.ScopeTicketsRead, auth.ScopeTicketsWrite)
	e.Do("GET", "/v1/tickets/"+tk.ID.String(), hdtest.Auth{Bearer: key}, nil).Want(t, 404)
	// A workspace header the user is not a member of resolves no workspace.
	e.Do("GET", "/v1/tickets/"+tk.ID.String(), hdtest.Auth{Bearer: other.Owner.Token, Workspace: ws.ID}, nil).Want(t, 400)
}

func TestTrustedAssistantCreateUsesNormalTicketPath(t *testing.T) {
	e, ws := newEnv(t)
	teamID := addTeam(t, e, ws, ws.Agent.ID)
	p := &auth.Principal{Kind: auth.KindUser, UserID: ws.Agent.ID, WorkspaceID: ws.ID, Role: auth.RoleAgent, Assistant: true}
	var tk domain.Ticket
	err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		var err error
		tk, err = tickets.CreateTicket(e.Ctx, tx, e.D, p, tickets.CreateInput{
			Subject: "Printer is offline", BodyHTML: "<p>Third floor</p>", Kind: "note", Channel: domain.ChannelAPI, TeamID: &teamID,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Channel != domain.ChannelAPI || tk.TeamID == nil || *tk.TeamID != teamID || tk.Subject != "Printer is offline" {
		t.Fatalf("ticket = %+v", tk)
	}
	var authorType, kind, body string
	var authorID uuid.UUID
	if err := e.D.DB.Pool.QueryRow(e.Ctx, `SELECT author_type,author_user_id,kind,body_text FROM messages WHERE ticket_id=$1`, tk.ID).
		Scan(&authorType, &authorID, &kind, &body); err != nil {
		t.Fatal(err)
	}
	if authorType != "user" || authorID != ws.Agent.ID || kind != "note" || body != "Third floor" {
		t.Fatalf("message = %s %s %s %q", authorType, authorID, kind, body)
	}
	if n := len(e.Jobs(jobs.TicketTriggerArgs{}.Kind(), ws.ID)); n != 1 {
		t.Fatalf("want one ticket-created trigger, got %d", n)
	}
}

func TestCreateValidation(t *testing.T) {
	e, ws := newEnv(t)
	other := e.NewWorkspace()
	otherMailbox := addMailbox(t, e, other)
	cases := []map[string]any{
		{"subject": "x", "body_html": "y", "assignee_id": other.Agent.ID.String()},
		{"subject": "x", "body_html": "y", "assignee_id": ws.Viewer.ID.String()},
		{"subject": "x", "body_html": "y", "mailbox_id": otherMailbox.String(), "contact": map[string]any{"email": "a@b.co"}},
		{"subject": "x", "body_html": "y", "channel": "email"},
		{"subject": "x", "body_html": "y", "contact": map[string]any{"email": "not an email"}},
		{"subject": "x", "body_html": "y", "contact": map[string]any{"email": "a@b.co", "name": "visit https://evil.example"}},
		{"subject": "x", "body_html": "y", "team_id": ids.New().String()},
		{"subject": "x", "body_html": "y", "attachment_ids": []string{ids.New().String()}},
	}
	for i, c := range cases {
		res := e.Do("POST", "/v1/tickets", ws.Agent.As(ws), c)
		if res.Status != 422 {
			t.Errorf("case %d: status %d %s", i, res.Status, res.Body)
		}
	}
	// API keys need tickets:write.
	ro := e.APIKey(ws, auth.ScopeTicketsRead)
	e.Do("POST", "/v1/tickets", hdtest.Auth{Bearer: ro}, map[string]any{"subject": "x", "body_html": "y"}).Want(t, 403)
	e.Do("GET", "/v1/tickets", hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeContactsRead)}, nil).Want(t, 403)
	rw := e.APIKey(ws, auth.ScopeTicketsWrite)
	var tk domain.Ticket
	e.Do("POST", "/v1/tickets", hdtest.Auth{Bearer: rw}, map[string]any{"subject": "From the API", "body_html": "<p>hi</p>", "kind": "note"}).Want(t, 201).JSON(t, &tk)
	var msgs struct{ Data []domain.Message }
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/messages", hdtest.Auth{Bearer: ro}, nil).Want(t, 200).JSON(t, &msgs)
	if msgs.Data[0].Author.Type != "api" || msgs.Data[0].Kind != "note" || tk.MessageCount != 0 {
		t.Fatalf("api note: %+v %+v", msgs.Data[0], tk)
	}
}

func TestEmailTicketQueuesSend(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	tk := createTicket(t, e, ws, map[string]any{"mailbox_id": mb.String(), "contact": map[string]any{"email": "buyer@example.com"},
		"cc": []string{"Boss@Example.com"}})
	if tk.Channel != "email" || *tk.MailboxID != mb {
		t.Fatalf("channel %s", tk.Channel)
	}
	var msgs struct{ Data []domain.Message }
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), nil).Want(t, 200).JSON(t, &msgs)
	m := msgs.Data[0]
	if m.DeliveryStatus != "pending" || len(m.To) != 1 || m.To[0] != "buyer@example.com" || m.Cc[0] != "boss@example.com" || !strings.HasPrefix(m.FromEmail, "support-") {
		t.Fatalf("email message: %+v", m)
	}
	if n := len(e.Jobs(jobs.SendMessageArgs{}.Kind(), ws.ID)); n != 1 {
		t.Fatalf("want one send job, got %d", n)
	}
	// A note-only email ticket without a contact is allowed and sends nothing.
	note := createTicket(t, e, ws, map[string]any{"mailbox_id": mb.String(), "kind": "note", "body_html": "<p>internal</p>"})
	if note.Channel != "email" || note.Contact != nil {
		t.Fatalf("note ticket %+v", note)
	}
	if n := len(e.Jobs(jobs.SendMessageArgs{}.Kind(), ws.ID)); n != 0 {
		t.Fatalf("note queued %d sends", n)
	}
	// A reply without a contact email is refused.
	e.Do("POST", "/v1/tickets/"+note.ID.String()+"/messages", ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "hi"}).Want(t, 422)
	// Agent reply on the email ticket queues a send.
	var reply domain.Message
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>Following up</p>",
		"bcc": []string{"audit@example.com"}}).Want(t, 201).JSON(t, &reply)
	if reply.DeliveryStatus != "pending" || reply.Bcc[0] != "audit@example.com" || reply.Subject != "Re: Help needed" {
		t.Fatalf("reply %+v", reply)
	}
	if n := len(e.Jobs(jobs.SendMessageArgs{}.Kind(), ws.ID)); n != 1 {
		t.Fatalf("want one send job, got %d", n)
	}
}

func TestPatch(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{})
	e.Jobs(jobs.TicketTriggerArgs{}.Kind(), ws.ID)
	_, seq := e.Events(ws.ID, 0)
	path := "/v1/tickets/" + tk.ID.String()

	var got domain.Ticket
	e.Do("PATCH", path, ws.Admin.As(ws), map[string]any{"status": "solved", "priority": "urgent", "assignee_id": ws.Agent.ID.String(),
		"tags": []string{"Billing"}, "custom": map[string]any{"plan": "pro", "seats": 5}, "subject": "  New   subject "}).Want(t, 200).JSON(t, &got)
	if got.Status != "solved" || got.SolvedAt == nil || got.Priority != "urgent" || got.Assignee == nil || got.Assignee.ID != ws.Agent.ID ||
		len(got.Tags) != 1 || got.Tags[0].Name != "Billing" || got.Custom["plan"] != "pro" || got.Subject != "New subject" {
		t.Fatalf("patched %+v", got)
	}
	types, _ := e.Events(ws.ID, seq)
	if !slices.Contains(types, "ticket.updated") || !slices.Contains(types, "notification.created") || !slices.Contains(types, "activity.created") {
		t.Fatalf("events %v", types)
	}
	var topics []string
	e.Scalar(`SELECT topics FROM events WHERE workspace_id = $1 AND type = 'ticket.updated' ORDER BY seq DESC LIMIT 1`, &topics, ws.ID)
	if !slices.Contains(topics, "user:"+ws.Agent.ID.String()) {
		t.Fatalf("ticket.updated topics %v", topics)
	}
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE user_id = $1 AND type = 'assigned' AND ticket_id = $2`, ws.Agent.ID, tk.ID); n != 1 {
		t.Fatalf("assigned notifications %d", n)
	}
	var acts struct{ Data []domain.Activity }
	e.Do("GET", path+"/activities", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &acts)
	var kinds []string
	for _, a := range acts.Data {
		kinds = append(kinds, a.Type)
		if a.Actor.Type != "user" || a.Actor.User == nil || a.Actor.User.ID != ws.Admin.ID {
			t.Fatalf("actor %+v", a.Actor)
		}
	}
	for _, want := range []string{"status_changed", "priority_changed", "assigned", "tagged", "custom_updated", "subject_changed"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("missing activity %s in %v", want, kinds)
		}
	}
	triggers := e.Jobs(jobs.TicketTriggerArgs{}.Kind(), ws.ID)
	if len(triggers) != 1 || !strings.Contains(string(triggers[0]), `"ticket_updated"`) || !strings.Contains(string(triggers[0]), `"priority"`) {
		t.Fatalf("triggers %s", triggers)
	}

	// Self-assignment notifies nobody; null unassigns; reopening clears solved_at.
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]any{"assignee_id": ws.Agent.ID.String()}).Want(t, 200)
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]any{"assignee_id": ws.Admin.ID.String()}).Want(t, 200)
	got = domain.Ticket{}
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]any{"assignee_id": nil, "status": "open", "custom": map[string]any{"plan": nil}}).Want(t, 200).JSON(t, &got)
	if got.Assignee != nil || got.SolvedAt != nil || got.Status != "open" || got.Custom["plan"] != nil || got.Custom["seats"] == nil {
		t.Fatalf("after unassign %+v", got)
	}
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'assigned'`, tk.ID); n != 2 {
		t.Fatalf("assigned notifications %d, want 2 (agent by admin, admin by agent)", n)
	}
	// Closing sets closed_at; snooze; spam.
	until := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]any{"snoozed_until": until, "spam": true, "tags": []string{}}).Want(t, 200).JSON(t, &got)
	if got.SnoozedUntil == nil || !got.Spam || len(got.Tags) != 0 {
		t.Fatalf("snooze/spam %+v", got)
	}
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]any{"status": "closed"}).Want(t, 200)
	var closedAt *time.Time
	e.Scalar(`SELECT closed_at FROM tickets WHERE id = $1`, &closedAt, tk.ID)
	if closedAt == nil {
		t.Fatal("closed_at not set")
	}

	// Validation.
	for _, body := range []map[string]any{
		{"assignee_id": ws.Viewer.ID.String()}, {"assignee_id": e.NewWorkspace().Agent.ID.String()}, {"team_id": ids.New().String()},
		{"snoozed_until": "2001-01-01T00:00:00Z"}, {"subject": "   "}, {"tags": []string{"<b>"}}, {"assignee_id": "nope"},
	} {
		if res := e.Do("PATCH", path, ws.Agent.As(ws), body); res.Status != 422 {
			t.Errorf("%v: %d %s", body, res.Status, res.Body)
		}
	}
	e.Do("PATCH", path, ws.Agent.As(ws), map[string]any{"status": "bogus"}).Want(t, 422)

	// API key with tickets:write is attributed as api.
	key := e.APIKey(ws, auth.ScopeTicketsWrite)
	e.Do("PATCH", path, hdtest.Auth{Bearer: key}, map[string]any{"priority": "low"}).Want(t, 200)
	var actor string
	e.Scalar(`SELECT actor_type FROM activities WHERE ticket_id = $1 AND type = 'priority_changed' ORDER BY created_at DESC LIMIT 1`, &actor, tk.ID)
	if actor != "api" {
		t.Fatalf("actor %s", actor)
	}
}

func TestPatchContact(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	tk := createTicket(t, e, ws, map[string]any{
		"mailbox_id": mb.String(), "contact": map[string]any{"email": "old@example.com", "name": "Old Contact"},
	})

	newContactID := ids.New()
	e.Exec(`INSERT INTO contacts (id, workspace_id, email, name) VALUES ($1, $2, $3, $4)`, newContactID, ws.ID, "new@example.com", "New Contact")
	_, seq := e.Events(ws.ID, 0)

	var got domain.Ticket
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"contact_id": newContactID.String()}).
		Want(t, 200).JSON(t, &got)
	if got.Contact == nil || got.Contact.ID != newContactID || got.Contact.Name != "New Contact" || got.Contact.Email == nil || *got.Contact.Email != "new@example.com" {
		t.Fatalf("contact was not changed: %+v", got.Contact)
	}
	var n int
	e.Scalar(`SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'contact_changed'
		AND data->'from'->>'id' = $2 AND data->'to'->>'id' = $3
		AND data->'from'->>'name' = 'Old Contact' AND data->'to'->>'name' = 'New Contact'`,
		&n, tk.ID, tk.Contact.ID.String(), newContactID.String())
	if n != 1 {
		t.Fatalf("contact_changed activity count %d", n)
	}
	e.Scalar(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'ticket.updated'
		AND data->'changes' @> '["contact_id"]'::jsonb AND data->'ticket'->'contact'->>'id' = $2`, &n, ws.ID, newContactID.String())
	if n != 1 {
		t.Fatalf("ticket.updated contact change count %d", n)
	}
	types, _ := e.Events(ws.ID, seq)
	if !slices.Contains(types, "ticket.updated") || !slices.Contains(types, "activity.created") {
		t.Fatalf("contact change events %v", types)
	}

	// Existing messages keep their original recipient; later replies use the new contact.
	var messages struct{ Data []domain.Message }
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), nil).Want(t, 200).JSON(t, &messages)
	if len(messages.Data) != 1 || len(messages.Data[0].To) != 1 || messages.Data[0].To[0] != "old@example.com" {
		t.Fatalf("existing message recipient changed: %+v", messages.Data)
	}
	var reply domain.Message
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>Following up</p>"}).
		Want(t, 201).JSON(t, &reply)
	if len(reply.To) != 1 || reply.To[0] != "new@example.com" {
		t.Fatalf("reply recipient %+v", reply.To)
	}

	// Admins can also change the contact; viewers cannot.
	adminContactID := ids.New()
	e.Exec(`INSERT INTO contacts (id, workspace_id, email, name) VALUES ($1, $2, $3, $4)`, adminContactID, ws.ID, "admin@example.com", "Admin Contact")
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Admin.As(ws), map[string]any{"contact_id": adminContactID.String()}).Want(t, 200)
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Viewer.As(ws), map[string]any{"contact_id": newContactID.String()}).Want(t, 403)

	other := e.NewWorkspace()
	foreign := createTicket(t, e, other, map[string]any{"contact": map[string]any{"email": "foreign@example.com"}})
	for _, body := range []map[string]any{
		{"contact_id": foreign.Contact.ID.String()},
		{"contact_id": ids.New().String()},
		{"contact_id": nil},
	} {
		if res := e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), body); res.Status != 422 {
			t.Errorf("%v: status %d %s", body, res.Status, res.Body)
		}
	}
	got = getTicket(t, e, ws, tk.ID)
	if got.Contact == nil || got.Contact.ID != adminContactID {
		t.Fatalf("invalid or unauthorized patch changed contact: %+v", got.Contact)
	}

	// Setting the contact it already has records nothing.
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"contact_id": adminContactID.String()}).Want(t, 200)
	if n := countRows(t, e, `SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'contact_changed'`, tk.ID); n != 2 {
		t.Fatalf("contact_changed activities %d", n)
	}
	// An API key needs tickets:write; bulk never changes a contact.
	read := e.APIKey(ws, "tickets:read")
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), hdtest.Auth{Bearer: read}, map[string]any{"contact_id": newContactID.String()}).Want(t, 403)
	e.Do("POST", "/v1/tickets/bulk", ws.Agent.As(ws), map[string]any{"ids": []string{tk.ID.String()},
		"patch": map[string]any{"contact_id": newContactID.String()}}).Want(t, 422)
	// A live chat stays with its visitor, whose widget lists it by contact.
	e.Exec(`UPDATE tickets SET channel = 'chat' WHERE id = $1`, tk.ID)
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Agent.As(ws), map[string]any{"contact_id": newContactID.String()}).Want(t, 422)
	// A ticket of a private team the caller is not on is not there at all.
	hidden, _ := privateTicket(t, e, ws)
	e.Do("PATCH", "/v1/tickets/"+hidden.ID.String(), ws.Agent.As(ws), map[string]any{"contact_id": newContactID.String()}).Want(t, 404)
}

func TestBulkDeleteMerge(t *testing.T) {
	e, ws := newEnv(t)
	a := createTicket(t, e, ws, map[string]any{"subject": "A", "tags": []string{"one"}})
	b := createTicket(t, e, ws, map[string]any{"subject": "B", "tags": []string{"two"}})
	c := createTicket(t, e, ws, map[string]any{"subject": "C"})

	var out struct {
		Data    []domain.Ticket
		Updated int
	}
	e.Do("POST", "/v1/tickets/bulk", ws.Agent.As(ws), map[string]any{"ids": []string{a.ID.String(), b.ID.String()},
		"patch": map[string]any{"status": "pending", "assignee_id": ws.Agent.ID.String()}}).Want(t, 200).JSON(t, &out)
	if out.Updated != 2 || len(out.Data) != 2 || out.Data[0].Status != "pending" {
		t.Fatalf("bulk %+v", out)
	}
	// All or nothing: a foreign id refuses the whole batch.
	other := e.NewWorkspace()
	foreign := createTicket(t, e, other, map[string]any{})
	e.Do("POST", "/v1/tickets/bulk", ws.Agent.As(ws), map[string]any{"ids": []string{c.ID.String(), foreign.ID.String()},
		"patch": map[string]any{"status": "solved"}}).Want(t, 404)
	if getTicket(t, e, ws, c.ID).Status != "open" {
		t.Fatal("partial bulk applied")
	}
	e.Do("POST", "/v1/tickets/bulk", ws.Agent.As(ws), map[string]any{"ids": []string{c.ID.String()}}).Want(t, 422)

	// Merge b into a.
	inbound(t, e, ws, b.ID, "customer follow up")
	var merged domain.Ticket
	e.Do("POST", "/v1/tickets/"+b.ID.String()+"/merge", ws.Agent.As(ws), map[string]any{"into_id": fmt.Sprintf("#%d", a.Number)}).Want(t, 200).JSON(t, &merged)
	if merged.ID != a.ID || merged.MessageCount != 3 || len(merged.Tags) != 2 || merged.LastMessagePreview != "customer follow up" {
		t.Fatalf("merged %+v", merged)
	}
	src := getTicket(t, e, ws, b.ID)
	if src.Status != "closed" || src.MessageCount != 0 {
		t.Fatalf("source %+v", src)
	}
	if n := countRows(t, e, `SELECT count(*) FROM messages WHERE ticket_id = $1`, b.ID); n != 0 {
		t.Fatalf("source still has %d messages", n)
	}
	if n := countRows(t, e, `SELECT count(*) FROM activities WHERE type = 'merged' AND ticket_id = ANY($1)`, []uuid.UUID{a.ID, b.ID}); n != 2 {
		t.Fatalf("merged activities %d", n)
	}
	e.Do("POST", "/v1/tickets/"+b.ID.String()+"/merge", ws.Agent.As(ws), map[string]any{"into_id": a.ID.String()}).Want(t, 409)
	e.Do("POST", "/v1/tickets/"+a.ID.String()+"/merge", ws.Agent.As(ws), map[string]any{"into_id": a.ID.String()}).Want(t, 422)
	e.Do("POST", "/v1/tickets/"+c.ID.String()+"/merge", ws.Agent.As(ws), map[string]any{"into_id": foreign.ID.String()}).Want(t, 422)

	// Delete is soft and publishes ticket.deleted.
	_, seq := e.Events(ws.ID, 0)
	e.Do("DELETE", "/v1/tickets/"+c.ID.String(), ws.Agent.As(ws), nil).Want(t, 204)
	e.Do("GET", "/v1/tickets/"+c.ID.String(), ws.Agent.As(ws), nil).Want(t, 404)
	if n := countRows(t, e, `SELECT count(*) FROM tickets WHERE id = $1 AND deleted_at IS NOT NULL`, c.ID); n != 1 {
		t.Fatal("not soft deleted")
	}
	types, _ := e.Events(ws.ID, seq)
	if !slices.Contains(types, "ticket.deleted") {
		t.Fatalf("events %v", types)
	}
	var del struct{ Deleted int }
	e.Do("POST", "/v1/tickets/bulk", ws.Agent.As(ws), map[string]any{"ids": []string{a.ID.String()}, "delete": true}).Want(t, 200).JSON(t, &del)
	if del.Deleted != 1 {
		t.Fatalf("bulk delete %+v", del)
	}
}
