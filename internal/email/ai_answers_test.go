package email

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
)

// aiWorld is a world whose instance has AI configured.
func aiWorld(t *testing.T) *world {
	w := newWorld(t)
	d := *w.d
	cfg := *w.d.Cfg
	cfg.AIProvider = "anthropic"
	d.Cfg = &cfg
	w.d, w.s = &d, newService(&d)
	return w
}

func TestAIAnswersQueued(t *testing.T) {
	w := aiWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	mail := func(extra string, from string) string {
		return "From: Carla <" + from + ">\r\nTo: " + m.Email + "\r\n" + extra + "Subject: Where is my order\r\nMessage-ID: <" +
			crypto.RandomString(10) + "@customer.test>\r\n\r\nHi, where is my order?\r\n"
	}
	queued := func() int { return w.jobCount("ai.email_answer", "", "") }

	// Off by default: nothing is queued.
	w.receive(m, mail("", customer()))
	if n := queued(); n != 0 {
		t.Fatalf("queued %d with email answers off", n)
	}

	mustExec(t, w.d, `UPDATE mailboxes SET ai_answers = true WHERE id = $1`, m.ID)
	m, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	id := w.receive(m, mail("", customer()))
	if n := queued(); n != 1 {
		t.Fatalf("queued %d for a customer email", n)
	}
	msg := w.messageFor(id)
	if w.jobCount("ai.email_answer", "message_id", msg.ID.String()) != 1 {
		t.Fatal("job not for the new message")
	}

	// Lists, bulk mail, no-reply senders and automatic mail are never answered, even
	// when inbound keeps them as tickets.
	for name, raw := range map[string]string{
		"list-id":       mail("List-Id: <news.shop.test>\r\n", customer()),
		"unsubscribe":   mail("List-Unsubscribe: <mailto:u@shop.test>\r\n", customer()),
		"own list":      mail("Precedence: list\r\nList-Id: <"+strings.Replace(m.Email, "@", ".", 1)+">\r\n", customer()),
		"no-reply":      mail("", "no-reply-"+crypto.RandomString(4)+"@shop.test"),
		"noreply":       mail("", "noreply@"+crypto.RandomString(4)+".test"),
		"suppress":      mail("X-Auto-Response-Suppress: All\r\n", customer()),
		"null sender":   mail("Return-Path: <>\r\n", customer()),
		"out of office": strings.Replace(mail("", customer()), "Where is my order", "Out of office: Where is my order", 1),
	} {
		before := queued()
		id := w.receive(m, raw)
		if st, _ := w.inbound(id); st != "processed" && st != "ignored" {
			t.Fatalf("%s: status %s", name, st)
		}
		if queued() != before {
			t.Fatalf("%s: queued an answer", name)
		}
	}

	// Auto-replies are dropped by inbound and never reach the agent.
	before := queued()
	id = w.receive(m, mail("Auto-Submitted: auto-replied\r\n", customer()))
	if st, reason := w.inbound(id); st != "ignored" || reason != "auto_submitted" || queued() != before {
		t.Fatalf("auto reply %s %s", st, reason)
	}
	// Spam (DMARC failure) is not answered either.
	id = w.receive(m, mail("Authentication-Results: mx.test; dmarc=fail header.from=shop.test\r\n", customer()))
	if queued() != before {
		t.Fatal("answered spam")
	}
	_ = id

	// A customer reply on the ticket queues again.
	from := customer()
	first := w.receive(m, mail("", from))
	tk := w.messageFor(first).TicketID
	before = queued()
	raw := "From: " + from + "\r\nTo: " + m.Email + "\r\nSubject: Re: Where is my order [#" + itoa(w.ticket(tk).Number) +
		"]\r\nMessage-ID: <" + crypto.RandomString(10) + "@customer.test>\r\n\r\nAny news?\r\n"
	if got := w.messageFor(w.receive(m, raw)).TicketID; got != tk {
		t.Fatal("reply did not thread")
	}
	if queued() != before+1 {
		t.Fatal("customer reply not queued")
	}
}

func TestAIAnswersSetting(t *testing.T) {
	w := newWorld(t) // AI off on this instance
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	srv := w.api(w.admin)
	var out Mailbox
	if st := doJSON(t, "GET", srv.URL+"/v1/mailboxes/"+m.ID.String(), nil, &out); st != 200 || out.AIAnswers || out.AIAnswersMode != "draft" || out.AIAnswersOutsideHours {
		t.Fatalf("defaults %d %+v", st, out)
	}
	if st := doJSON(t, "PATCH", srv.URL+"/v1/mailboxes/"+m.ID.String(), map[string]any{"ai_answers": true}, nil); st != 422 {
		t.Fatalf("enabled with AI off: %d", st)
	}

	w2 := aiWorld(t)
	m2 := w2.mailbox(InboundForwarding, OutboundSystem, nil)
	srv2 := w2.api(w2.admin)
	if st := doJSON(t, "PATCH", srv2.URL+"/v1/mailboxes/"+m2.ID.String(), map[string]any{"ai_answers": true, "ai_answers_mode": "send",
		"ai_answers_outside_hours": true}, &out); st != 200 || !out.AIAnswers || out.AIAnswersMode != "send" || !out.AIAnswersOutsideHours {
		t.Fatalf("enable %d %+v", st, out)
	}
	if st := doJSON(t, "PATCH", srv2.URL+"/v1/mailboxes/"+m2.ID.String(), map[string]any{"ai_answers_mode": "maybe"}, nil); st != 422 {
		t.Fatalf("bad mode %d", st)
	}
	var data string
	if err := w2.d.DB.Pool.QueryRow(w2.ctx, `SELECT data::text FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.updated'
		ORDER BY created_at DESC LIMIT 1`, w2.wid).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, `"ai_answers"`) || !strings.Contains(data, `"send"`) {
		t.Fatalf("audit %s", data)
	}
	// Workspace AI off refuses too.
	mustExec(t, w2.d, `UPDATE workspaces SET settings = jsonb_set(settings, '{ai}', '{"enabled": false}') WHERE id = $1`, w2.wid)
	doJSON(t, "PATCH", srv2.URL+"/v1/mailboxes/"+m2.ID.String(), map[string]any{"ai_answers": false}, nil)
	if st := doJSON(t, "PATCH", srv2.URL+"/v1/mailboxes/"+m2.ID.String(), map[string]any{"ai_answers": true}, nil); st != 422 {
		t.Fatalf("enabled with workspace AI off: %d", st)
	}
}

func TestAIReplySentAsBot(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	in := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", customer(), "{{TO}}", "", "{{ID}}", "bot"+crypto.RandomString(6)))
	tk := w.messageFor(in).TicketID
	var sent []byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, raw []byte) error {
		sent = raw
		return nil
	}
	var mid uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk, Kind: "reply", Direction: "outbound",
			AuthorType: "system", BodyHTML: "<p>Hi Carla, it shipped.</p>", BodyText: "Hi Carla, it shipped.", AI: true, FromName: "Ollie",
			Trigger: "none", DeliveryStatus: "pending"})
		mid = msg.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	raw := string(sent)
	if !strings.Contains(raw, "Ollie at Acme Support") || !strings.Contains(raw, "Auto-Submitted: auto-generated") || !strings.Contains(raw, "In-Reply-To:") {
		t.Fatalf("headers %s", raw)
	}
	if got := w.message(mid); got.Delivery != "sent" {
		t.Fatalf("delivery %s", got.Delivery)
	}
}

// A reply an API key writes as a bot (contracts/api.md "API keys") goes out like the AI
// agent's: the bot is {agent} in the address's sender name, and the email is automatic.
// A key that speaks as no bot sends as before.
func TestAPIKeyReplySentAsBot(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	mustExec(t, w.d, `UPDATE mailboxes SET from_name_format = '{agent} - LampTwist Support' WHERE id = $1`, m.ID)
	in := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", customer(), "{{TO}}", "", "{{ID}}", "key"+crypto.RandomString(6)))
	tk := w.messageFor(in).TicketID
	bot := uuid.New()
	mustExec(t, w.d, `INSERT INTO bots (id, workspace_id, name, look) VALUES ($1, $2, 'Julia', '{}')`, bot, w.wid)
	var sent []byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, raw []byte) error {
		sent = raw
		return nil
	}
	send := func(botID *uuid.UUID, fromName string) string {
		t.Helper()
		var mid uuid.UUID
		err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
			msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk, Kind: "reply", Direction: "outbound",
				AuthorType: "api", BodyHTML: "<p>It shipped.</p>", BodyText: "It shipped.", FromName: fromName, BotID: botID,
				Trigger: "none", DeliveryStatus: "pending"})
			mid = msg.ID
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
			t.Fatal(err)
		}
		if got := w.message(mid); got.Delivery != "sent" {
			t.Fatalf("delivery %s", got.Delivery)
		}
		return string(sent)
	}
	raw := send(&bot, "Julia")
	if !strings.Contains(raw, "Julia - LampTwist Support") || !strings.Contains(raw, "Auto-Submitted: auto-generated") {
		t.Fatalf("as a bot: %s", raw)
	}
	raw = send(nil, "Acme Support")
	head, _, _ := strings.Cut(raw, "\r\n\r\n")
	if strings.Contains(head, "Julia") || strings.Contains(head, "Auto-Submitted") {
		t.Fatalf("as itself: %s", head)
	}
}
