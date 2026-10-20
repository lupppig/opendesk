package tickets_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/helpdesk/widget"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

type threadCreated struct {
	Thread  domain.Thread  `json:"thread"`
	Message domain.Message `json:"message"`
}

func TestThirdPartyThreadsAPI(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	tk := createTicket(t, e, ws, map[string]any{"channel": "email", "mailbox_id": mb.String(), "kind": "note",
		"contact": map[string]any{"email": "carla@customer.test", "name": "Carla"}})
	base := "/v1/tickets/" + tk.ID.String() + "/threads"
	agent := ws.Agent.As(ws)

	// Recipients and a subject are required; viewers cannot start one.
	e.Do("POST", base, agent, map[string]any{"to": []string{}, "subject": "Payout", "body_html": "<p>Hi</p>"}).Want(t, 422)
	e.Do("POST", base, agent, map[string]any{"to": []string{"ops@acquirer.test"}, "subject": "  ", "body_html": "<p>Hi</p>"}).Want(t, 422)
	e.Do("POST", base, agent, map[string]any{"to": []string{"not an address"}, "subject": "Payout", "body_html": "<p>Hi</p>"}).Want(t, 422)
	e.Do("POST", base, agent, map[string]any{"to": []string{"ops@acquirer.test"}, "subject": "Payout", "body_html": ""}).Want(t, 422)
	e.Do("POST", base, ws.Viewer.As(ws), map[string]any{"to": []string{"ops@acquirer.test"}, "subject": "Payout", "body_html": "<p>Hi</p>"}).Want(t, 403)

	var created threadCreated
	e.Do("POST", base, agent, map[string]any{"to": []string{"Ops@Acquirer.test"}, "cc": []string{"lead@acquirer.test"},
		"subject": "Payout for order 1182", "body_html": "<p>Can you check the payout?</p>"}).Want(t, 201).JSON(t, &created)
	th, msg := created.Thread, created.Message
	if th.Status != domain.ThreadOpen || th.Subject != "Payout for order 1182" || len(th.To) != 1 || th.To[0] != "ops@acquirer.test" ||
		len(th.Cc) != 1 || th.MailboxID == nil || *th.MailboxID != mb || th.MessageCount != 1 || th.CreatedBy == nil {
		t.Fatalf("thread %+v", th)
	}
	if msg.Kind != "note" || msg.Direction != "outbound" || msg.ThreadID == nil || *msg.ThreadID != th.ID ||
		msg.DeliveryStatus != "pending" || len(msg.To) != 1 || msg.To[0] != "ops@acquirer.test" {
		t.Fatalf("message %+v", msg)
	}
	if n := len(e.Jobs(jobs.SendMessageArgs{}.Kind(), ws.ID)); n != 1 {
		t.Fatalf("%d sends queued", n)
	}

	// The thread's message is in the ticket's messages, marked with its thread.
	var msgs struct {
		Data []domain.Message `json:"data"`
	}
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/messages", agent, nil).Want(t, 200).JSON(t, &msgs)
	found := false
	for _, m := range msgs.Data {
		if m.ID == msg.ID && m.ThreadID != nil && *m.ThreadID == th.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("thread message missing from the ticket's messages")
	}

	// Writing in the thread sends to its recipients again.
	var m2 domain.Message
	e.Do("POST", base+"/"+th.ID.String()+"/messages", agent, map[string]any{"body_html": "<p>Any news?</p>"}).Want(t, 201).JSON(t, &m2)
	if m2.ThreadID == nil || *m2.ThreadID != th.ID || len(m2.To) != 1 || len(m2.Cc) != 1 {
		t.Fatalf("second message %+v", m2)
	}
	e.Do("POST", base+"/"+ids.New().String()+"/messages", agent, map[string]any{"body_html": "<p>x</p>"}).Want(t, 404)

	// Close and reopen.
	var closed domain.Thread
	e.Do("PATCH", base+"/"+th.ID.String(), agent, map[string]any{"status": "closed"}).Want(t, 200).JSON(t, &closed)
	if closed.Status != domain.ThreadClosed {
		t.Fatalf("status %s", closed.Status)
	}
	var list struct {
		Data []domain.Thread `json:"data"`
	}
	e.Do("GET", base, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 1 || list.Data[0].Status != domain.ThreadClosed || list.Data[0].MessageCount != 2 {
		t.Fatalf("threads %+v", list.Data)
	}
	var acts struct {
		Data []domain.Activity `json:"data"`
	}
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/activities", agent, nil).Want(t, 200).JSON(t, &acts)
	seen := map[string]bool{}
	for _, a := range acts.Data {
		seen[a.Type] = true
	}
	if !seen["thread_started"] || !seen["thread_closed"] {
		t.Fatalf("activities %v", seen)
	}
	// Writing in a closed thread opens it again.
	e.Do("POST", base+"/"+th.ID.String()+"/messages", agent, map[string]any{"body_html": "<p>One more thing</p>"}).Want(t, 201)
	e.Do("GET", base, agent, nil).Want(t, 200).JSON(t, &list)
	if list.Data[0].Status != domain.ThreadOpen {
		t.Fatal("a new message opens the thread")
	}

	// The ticket's own conversation is untouched by the thread.
	after := getTicket(t, e, ws, tk.ID)
	if after.MessageCount != tk.MessageCount {
		t.Fatalf("message_count %d, was %d", after.MessageCount, tk.MessageCount)
	}

	// Threads move with their messages when the ticket is merged.
	other := createTicket(t, e, ws, map[string]any{"channel": "email", "mailbox_id": mb.String(), "kind": "note",
		"contact": map[string]any{"email": "carla@customer.test", "name": "Carla"}})
	_, since := e.Events(ws.ID, 0)
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/merge", agent, map[string]any{"into_id": other.ID.String()}).Want(t, 200)
	if types, _ := e.Events(ws.ID, since); !slices.Contains(types, "thread.updated") {
		t.Fatalf("merge events %v: the moved thread is announced on the target", types)
	}
	e.Do("GET", "/v1/tickets/"+other.ID.String()+"/threads", agent, nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 1 || list.Data[0].ID != th.ID {
		t.Fatalf("threads after merge %+v", list.Data)
	}
}

func TestThreadForwardsHistoryAndAttachments(t *testing.T) {
	// Uploads are served by the widget area.
	e := hdtest.New(t, tickets.Register, widget.Register)
	ws := e.NewWorkspace()
	mb := addMailbox(t, e, ws)
	tk := createTicket(t, e, ws, map[string]any{"channel": "email", "mailbox_id": mb.String(), "kind": "note",
		"contact": map[string]any{"email": "carla@customer.test", "name": "Carla"}})
	agent := ws.Agent.As(ws)
	upload := func(name string) domain.Attachment {
		var a domain.Attachment
		e.Upload("/v1/attachments", agent, name, []byte("file "+name)).Want(t, 201).JSON(t, &a)
		return a
	}
	// One file in the conversation with the customer, one in an internal note.
	public, internal := upload("receipt.pdf"), upload("internal.txt")
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", agent, map[string]any{"kind": "reply", "body_html": "<p>Here is the receipt.</p>",
		"attachment_ids": []string{public.ID.String()}}).Want(t, 201)
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", agent, map[string]any{"kind": "note", "body_html": "<p>Internal.</p>",
		"attachment_ids": []string{internal.ID.String()}}).Want(t, 201)
	base := "/v1/tickets/" + tk.ID.String() + "/threads"
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"to": []string{"ops@acquirer.test"}, "subject": "Payout", "body_html": "<p>See attached.</p>"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	// Nothing of the customer's goes along unless asked for.
	var plain threadCreated
	e.Do("POST", base, agent, body(nil)).Want(t, 201).JSON(t, &plain)
	if plain.Thread.IncludeHistory || len(plain.Message.Attachments) != 0 {
		t.Fatalf("plain thread %+v, %d attachments", plain.Thread, len(plain.Message.Attachments))
	}

	// A note's attachment, or another ticket's, is not the customer's conversation.
	e.Do("POST", base, agent, body(map[string]any{"forward_attachment_ids": []string{internal.ID.String()}})).Want(t, 422)
	other := createTicket(t, e, ws, map[string]any{"kind": "note"})
	e.Do("POST", "/v1/tickets/"+other.ID.String()+"/threads", agent, body(map[string]any{"forward_attachment_ids": []string{public.ID.String()}})).Want(t, 422)

	// Nor is a file of mail that failed its sender checks, which the timeline holds back.
	spoofed := upload("new-bank-details.pdf")
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		m, err := domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: ws.ID, TicketID: tk.ID, Kind: "reply", Direction: "inbound",
			AuthorType: "contact", BodyHTML: "<p>Our new bank details.</p>", BodyText: "Our new bank details.",
			FromEmail: "carla@customer.test", FromName: "Carla", AuthFailed: true})
		if err != nil {
			return err
		}
		_, err = tx.Exec(e.Ctx, `UPDATE attachments SET message_id = $2 WHERE id = $1`, spoofed.ID, m.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.Do("POST", base, agent, body(map[string]any{"forward_attachment_ids": []string{spoofed.ID.String()}})).Want(t, 422)

	var fwd threadCreated
	e.Do("POST", base, agent, body(map[string]any{"include_history": true, "forward_attachment_ids": []string{public.ID.String()}})).
		Want(t, 201).JSON(t, &fwd)
	if !fwd.Thread.IncludeHistory || len(fwd.Message.Attachments) != 1 || fwd.Message.Attachments[0].Filename != "receipt.pdf" ||
		fwd.Message.Attachments[0].ID == public.ID {
		t.Fatalf("forwarded thread %+v, attachments %+v", fwd.Thread, fwd.Message.Attachments)
	}
	// A saved reply's tags are filled without the customer's or the ticket's details.
	var filled threadCreated
	e.Do("POST", base, agent, body(map[string]any{"body_html": "<p>Hi from {{ workspace.name }} about {{ contact.name }} {{ ticket.number }}.</p>"})).
		Want(t, 201).JSON(t, &filled)
	if b := filled.Message.BodyHTML; strings.Contains(b, "{{") || strings.Contains(b, "Carla") || strings.Contains(b, strconv.FormatInt(tk.Number, 10)+".") {
		t.Fatalf("filled body %q", b)
	}

	// The copy shares the stored file and leaves the customer's message as it was.
	var shared, onReply int
	e.Scalar(`SELECT count(*) FROM attachments WHERE storage_key = (SELECT storage_key FROM attachments WHERE id = $1)`, &shared, public.ID)
	e.Scalar(`SELECT count(*) FROM attachments a JOIN messages m ON m.id = a.message_id WHERE a.id = $1 AND m.kind = 'reply'`, &onReply, public.ID)
	if shared != 2 || onReply != 1 {
		t.Fatalf("shared %d, still on the reply %d", shared, onReply)
	}
}

func TestThreadReplyNotifiesAssignee(t *testing.T) {
	e, ws := newEnv(t)
	mb := addMailbox(t, e, ws)
	tk := createTicket(t, e, ws, map[string]any{"channel": "email", "mailbox_id": mb.String(), "kind": "note",
		"contact": map[string]any{"email": "carla@customer.test", "name": "Carla"}})
	e.Exec(`UPDATE tickets SET assignee_id = $2 WHERE id = $1`, tk.ID, ws.Agent.ID)
	var created threadCreated
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/threads", ws.Agent.As(ws), map[string]any{"to": []string{"ops@acquirer.test"},
		"subject": "Payout", "body_html": "<p>Can you check?</p>"}).Want(t, 201).JSON(t, &created)
	runTriggers(t, e, ws.ID)

	// Mail that failed sender checks lands in the thread but leaves a ticket on hold as it is.
	e.Exec(`UPDATE tickets SET status = 'on_hold' WHERE id = $1`, tk.ID)
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		_, err := domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: ws.ID, TicketID: tk.ID, ThreadID: &created.Thread.ID,
			Kind: "note", Direction: "inbound", AuthorType: "contact", BodyHTML: "<p>Pay to this new account.</p>", BodyText: "Pay to this new account.",
			FromEmail: "ops@acquirer.test", FromName: "Olga Ops", AuthFailed: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if st := getTicket(t, e, ws, tk.ID).Status; st != domain.StatusOnHold {
		t.Fatalf("status %s after unverified mail", st)
	}

	err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		_, err := domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: ws.ID, TicketID: tk.ID, ThreadID: &created.Thread.ID,
			Kind: "note", Direction: "inbound", AuthorType: "contact", BodyHTML: "<p>It went out today.</p>", BodyText: "It went out today.",
			FromEmail: "ops@acquirer.test", FromName: "Olga Ops"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	runTriggers(t, e, ws.ID)
	var typ, preview, from string
	e.Scalar(`SELECT type FROM notifications WHERE workspace_id = $1 AND user_id = $2 ORDER BY created_at DESC LIMIT 1`, &typ, ws.ID, ws.Agent.ID)
	e.Scalar(`SELECT data->>'preview' FROM notifications WHERE workspace_id = $1 AND user_id = $2 AND type = 'thread_replied' ORDER BY created_at DESC LIMIT 1`, &preview, ws.ID, ws.Agent.ID)
	e.Scalar(`SELECT data->'from'->>'name' FROM notifications WHERE workspace_id = $1 AND user_id = $2 AND type = 'thread_replied' ORDER BY created_at DESC LIMIT 1`, &from, ws.ID, ws.Agent.ID)
	if typ != "thread_replied" || preview != "It went out today." || from != "Olga Ops" {
		t.Fatalf("notification %q %q %q", typ, preview, from)
	}
	var n int
	e.Scalar(`SELECT count(*) FROM notifications WHERE workspace_id = $1 AND type = 'customer_replied'`, &n, ws.ID)
	if n != 0 {
		t.Fatal("a thread reply is not a customer reply")
	}
}
