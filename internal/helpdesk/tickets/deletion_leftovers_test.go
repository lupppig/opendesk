package tickets_test

import (
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
)

// TestDeletedMessageLeavesNothingBehind checks a deleted customer message's text leaves its
// notifications' previews, a pending reply is marked not sent, and replayed events (GET
// /v1/events) show it as the marker it is now.
func TestDeletedMessageLeavesNothingBehind(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note", "contact": map[string]any{"email": "c@example.com"}})
	e.Do("PATCH", "/v1/tickets/"+tk.ID.String(), ws.Admin.As(ws), map[string]any{"assignee_id": ws.Admin.ID.String()}).Want(t, 200)
	runTriggers(t, e, ws.ID)
	secret := "my card is 4242 4242 4242 4242"
	msg := inbound(t, e, ws, tk.ID, secret)
	runTriggers(t, e, ws.ID)
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'customer_replied' AND data->>'preview' LIKE '%4242%'`, tk.ID); n != 1 {
		t.Fatalf("customer_replied previews %d", n)
	}
	e.Do("DELETE", "/v1/tickets/"+tk.ID.String()+"/messages/"+msg.ID.String(), ws.Admin.As(ws), nil).Want(t, 204)
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND data::text LIKE '%4242%'`, tk.ID); n != 0 {
		t.Fatal("a notification still quotes the deleted message")
	}
	if n := countRows(t, e, `SELECT count(*) FROM notifications WHERE ticket_id = $1 AND type = 'customer_replied'`, tk.ID); n != 1 {
		t.Fatal("the notification itself should stay, without its preview")
	}

	evs, _, _, err := events.Since(e.Ctx, e.D.DB.Pool, ws.ID, 0, 1000, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored := false
	for _, ev := range evs {
		stored = stored || strings.Contains(string(ev.Data), "4242")
	}
	if !stored {
		t.Fatal("expected the message's own message.created to hold its text before redaction")
	}
	if err := domain.RedactReplay(e.Ctx, e.D.DB.Pool, ws.ID, evs); err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if strings.Contains(string(ev.Data), "4242") {
			t.Fatalf("replayed %s carries the deleted text: %s", ev.Type, ev.Data)
		}
	}

	// A reply deleted before it went out is marked not sent, so a restore doesn't leave it waiting.
	var reply domain.Message
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>Oops</p>"}).
		Want(t, 201).JSON(t, &reply)
	e.Exec(`UPDATE messages SET delivery_status = 'pending' WHERE id = $1`, reply.ID)
	path := "/v1/tickets/" + tk.ID.String() + "/messages/" + reply.ID.String()
	e.Do("DELETE", path, ws.Admin.As(ws), nil).Want(t, 204)
	var restored domain.Message
	e.Do("POST", path+"/restore", ws.Admin.As(ws), nil).Want(t, 200).JSON(t, &restored)
	if restored.DeliveryStatus != "failed" || restored.DeliveryError != domain.DeletedBeforeSent {
		t.Fatalf("restored delivery %q %q", restored.DeliveryStatus, restored.DeliveryError)
	}
}

// TestPurgeRemovesWhatWasMadeFromIt checks purge also empties Ollie's suggested reply to the
// message, an action card's summary and arguments, and the ticket's AI summary.
func TestPurgeRemovesWhatWasMadeFromIt(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note"})
	msg := inbound(t, e, ws, tk.ID, "my password is hunter2")
	e.Exec(`INSERT INTO ai_suggested_replies (id, workspace_id, ticket_id, message_id, body_html) VALUES (gen_random_uuid(), $1, $2, $3, '<p>About hunter2</p>')`,
		ws.ID, tk.ID, msg.ID)
	e.Exec(`INSERT INTO ai_widget_actions (workspace_id, ticket_id, message_id, server_id, tool, title, summary, args, expires_at)
		VALUES ($1, $2, $3, gen_random_uuid(), 'reset', 'Reset', 'Reset hunter2', 'hunter2'::bytea, now() + interval '1 hour')`, ws.ID, tk.ID, msg.ID)
	e.Exec(`UPDATE tickets SET custom = custom || '{"ai_summary": {"text": "Customer shared hunter2"}}' WHERE id = $1`, tk.ID)
	path := "/v1/tickets/" + tk.ID.String() + "/messages/" + msg.ID.String()
	e.Do("DELETE", path, ws.Admin.As(ws), nil).Want(t, 204)
	if n := countRows(t, e, `SELECT count(*) FROM ai_widget_actions WHERE message_id = $1 AND status = 'expired'`, msg.ID); n != 1 {
		t.Fatal("an action card of a deleted message still waits on the visitor")
	}
	e.Do("POST", path+"/purge", ws.Admin.As(ws), nil).Want(t, 200)
	if n := countRows(t, e, `SELECT count(*) FROM ai_suggested_replies WHERE message_id = $1 AND (body_html LIKE '%hunter2%' OR status = 'pending')`, msg.ID); n != 0 {
		t.Fatal("the suggested reply to a purged message stays")
	}
	if n := countRows(t, e, `SELECT count(*) FROM ai_widget_actions WHERE message_id = $1 AND (summary LIKE '%hunter2%' OR position('hunter2'::bytea in args) > 0)`, msg.ID); n != 0 {
		t.Fatal("the action card of a purged message stays")
	}
	if n := countRows(t, e, `SELECT count(*) FROM tickets WHERE id = $1 AND custom ? 'ai_summary'`, tk.ID); n != 0 {
		t.Fatal("the ticket's AI summary stays after a purge")
	}
}

// TestDeletedReplyLeavesSentCopies checks the kept copies of emails sent after a deleted
// reply (their quoted conversation may hold it) are dropped, and earlier ones stay.
func TestDeletedReplyLeavesSentCopies(t *testing.T) {
	e, ws := newEnv(t)
	tk := createTicket(t, e, ws, map[string]any{"channel": "api", "kind": "note", "contact": map[string]any{"email": "ada" + "@example.com"}})
	var before, after domain.Message
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>Hello</p>"}).
		Want(t, 201).JSON(t, &before)
	msg := inbound(t, e, ws, tk.ID, "my card is 4242 4242 4242 4242")
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", ws.Agent.As(ws), map[string]any{"kind": "reply", "body_html": "<p>Got it</p>"}).
		Want(t, 201).JSON(t, &after)
	e.Exec(`UPDATE messages SET full_html = '<p>Hello</p>' WHERE id = $1`, before.ID)
	e.Exec(`UPDATE messages SET full_html = '<p>Got it</p><blockquote>my card is 4242 4242 4242 4242</blockquote>' WHERE id = $1`, after.ID)

	e.Do("DELETE", "/v1/tickets/"+tk.ID.String()+"/messages/"+msg.ID.String(), ws.Admin.As(ws), nil).Want(t, 204)
	if n := countRows(t, e, `SELECT count(*) FROM messages WHERE ticket_id = $1 AND full_html LIKE '%4242%'`, tk.ID); n != 0 {
		t.Fatal("a sent copy still quotes the deleted message")
	}
	if n := countRows(t, e, `SELECT count(*) FROM messages WHERE id = $1 AND full_html = '<p>Hello</p>'`, before.ID); n != 1 {
		t.Fatal("a copy sent before the deleted message should stay")
	}
	got, err := domain.LoadMessage(e.Ctx, e.D.DB.Pool, ws.ID, after.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HasQuoted {
		t.Fatal("the later reply should no longer offer its sent copy")
	}
}
