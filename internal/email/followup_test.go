package email

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jhillyerd/enmime/v2"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// chat starts a chat ticket for a visitor with email (empty for none) and a widget
// session last seen at seenAgo before now.
func (w *world) chat(email string, seenAgo string) (ticketID, widgetID uuid.UUID) {
	w.t.Helper()
	widgetID = ids.New()
	mustExec(w.t, w.d, `INSERT INTO widgets (id, workspace_id, brand_id, name, key) VALUES ($1, $2, $3, 'Site', $4)`,
		widgetID, w.wid, w.brand, "wk_"+crypto.RandomString(16))
	cid := ids.New()
	var mail *string
	if email != "" {
		mail = &email
	}
	mustExec(w.t, w.d, `INSERT INTO contacts (id, workspace_id, email, name) VALUES ($1, $2, $3, 'Vera Visitor')`, cid, w.wid, mail)
	mustExec(w.t, w.d, `INSERT INTO widget_sessions (workspace_id, contact_id, token_hash, widget_id, last_seen_at)
		VALUES ($1, $2, $3, $4, now() - $5::interval)`, w.wid, cid, []byte(crypto.RandomString(20)), widgetID, seenAgo)
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		t, err := domain.CreateTicket(w.ctx, tx, domain.NewTicket{WorkspaceID: w.wid, Subject: "Where is my order", Channel: domain.ChannelChat,
			ContactID: &cid, WidgetID: &widgetID})
		if err != nil {
			return err
		}
		ticketID = t.ID
		_, err = domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: t.ID, Kind: "reply",
			Direction: "inbound", AuthorType: "contact", AuthorContactID: &cid, BodyHTML: "<p>Hi</p>", BodyText: "Hi"})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return ticketID, widgetID
}

// chatReply posts a public reply on a chat ticket the way the helpdesk API does.
func (w *world) chatReply(ticketID uuid.UUID, body string, ai bool) uuid.UUID {
	w.t.Helper()
	nm := domain.NewMessage{WorkspaceID: w.wid, TicketID: ticketID, Kind: "reply", Direction: "outbound", AuthorType: "user",
		AuthorUserID: &w.uid, BodyHTML: "<p>" + body + "</p>", BodyText: body}
	if ai {
		nm.AuthorType, nm.AuthorUserID, nm.AI, nm.FromName = "system", nil, true, "Ollie"
	}
	var id uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		m, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, nm)
		id = m.ID
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) chatAPIReply(ticketID uuid.UUID, body string, ai bool) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		m, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: ticketID, Kind: "reply",
			Direction: "outbound", AuthorType: "api", AI: ai, BodyHTML: "<p>" + body + "</p>", BodyText: body})
		id = m.ID
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) followUps(mid uuid.UUID) []jobs.ChatFollowUpArgs {
	w.t.Helper()
	rows, err := w.d.DB.Pool.Query(w.ctx, `SELECT args FROM river_job WHERE kind = $1 AND args->>'message_id' = $2`,
		jobs.ChatFollowUpArgs{}.Kind(), mid.String())
	if err != nil {
		w.t.Fatal(err)
	}
	raws, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		w.t.Fatal(err)
	}
	out := []jobs.ChatFollowUpArgs{}
	for _, raw := range raws {
		var a jobs.ChatFollowUpArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func TestChatFollowUp(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.FromNameFormat = "{agent_first} at {brand}"
		m.SignatureHTML = "<p>Sent from {{brand.name}}</p>"
	})
	var sent [][]byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, raw []byte) error { sent = append(sent, raw); return nil }
	email := customer()
	tid, _ := w.chat(email, "10 minutes")

	// An agent's reply to an away visitor is queued, and a minute later sent by email.
	mid := w.chatReply(tid, "Your order ships today", false)
	due := w.followUps(mid)
	if len(due) != 1 {
		t.Fatalf("follow-ups %d", len(due))
	}
	if err := w.s.chatFollowUp(w.ctx, due[0]); err != nil {
		t.Fatal(err)
	}
	if got := w.message(mid).Delivery; got != "pending" {
		t.Fatalf("delivery %q", got)
	}
	if n := w.jobCount(jobs.SendMessageArgs{}.Kind(), "message_id", mid.String()); n != 1 {
		t.Fatalf("send jobs %d", n)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(sent[0]))
	if err != nil {
		t.Fatal(err)
	}
	if g := env.GetHeader("From"); g != `"Anna at Acme" <notifications@opendesk.test>` {
		t.Errorf("From %q", g)
	}
	if g := env.GetHeader("To"); !strings.Contains(g, email) {
		t.Errorf("To %q", g)
	}
	if !strings.Contains(env.HTML, "Your order ships today") || !strings.Contains(env.HTML, "Sent from Acme") {
		t.Errorf("body %s", env.HTML)
	}
	first := w.message(mid)
	if first.Delivery != "sent" || first.MsgID == nil {
		t.Fatalf("first %+v", first)
	}
	if tk := w.ticket(tid); tk.MailboxID == nil || *tk.MailboxID != m.ID {
		t.Fatalf("reply address %v", tk.MailboxID)
	}

	// The next one threads with it.
	mid2 := w.chatReply(tid, "Tracking number to follow", false)
	if err := w.s.chatFollowUp(w.ctx, w.followUps(mid2)[0]); err != nil {
		t.Fatal(err)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid2, false); err != nil {
		t.Fatal(err)
	}
	env, err = enmime.ReadEnvelope(bytes.NewReader(sent[1]))
	if err != nil {
		t.Fatal(err)
	}
	if g := env.GetHeader("In-Reply-To"); g != *first.MsgID {
		t.Errorf("In-Reply-To %q, want %q", g, *first.MsgID)
	}

	// The visitor's email answer lands in the chat.
	in := w.receive(m, rawMail("Vera <"+email+">", w.forwardAddr(m, ""), "Re: "+env.GetHeader("Subject"),
		"In-Reply-To: "+*first.MsgID+"\r\nReferences: "+*first.MsgID+"\r\n"))
	if got := w.messageFor(in).TicketID; got != tid {
		t.Fatalf("answer went to ticket %s, want the chat %s", got, tid)
	}

	// Back on the page before the minute is up: nothing is sent.
	mid3 := w.chatReply(tid, "Anything else?", false)
	mustExec(t, w.d, `UPDATE widget_sessions SET last_seen_at = now() + interval '1 second' WHERE contact_id = (SELECT contact_id FROM tickets WHERE id = $1)`, tid)
	if err := w.s.chatFollowUp(w.ctx, w.followUps(mid3)[0]); err != nil {
		t.Fatal(err)
	}
	if got := w.message(mid3).Delivery; got != "none" {
		t.Fatalf("seen visitor got an email: %q", got)
	}
}

func TestChatFollowUpSkips(t *testing.T) {
	w := newWorld(t)
	w.mailbox(InboundForwarding, OutboundSystem, nil)

	// On the page, no email, the AI's replies, and the widget setting off: nothing is queued.
	here, _ := w.chat(customer(), "10 seconds")
	if n := len(w.followUps(w.chatReply(here, "Hi", false))); n != 0 {
		t.Fatalf("connected visitor queued %d", n)
	}
	anon, _ := w.chat("", "10 minutes")
	if n := len(w.followUps(w.chatReply(anon, "Hi", false))); n != 0 {
		t.Fatalf("visitor without email queued %d", n)
	}
	away, widget := w.chat(customer(), "10 minutes")
	if n := len(w.followUps(w.chatReply(away, "Hi", true))); n != 0 {
		t.Fatalf("AI reply queued %d", n)
	}
	if n := len(w.followUps(w.chatAPIReply(away, "AI answer", true))); n != 0 {
		t.Fatalf("API AI reply queued %d", n)
	}
	mustExec(t, w.d, `UPDATE widgets SET settings = settings || '{"email_follow_up": false}' WHERE id = $1`, widget)
	if n := len(w.followUps(w.chatReply(away, "Hi", false))); n != 0 {
		t.Fatalf("setting off queued %d", n)
	}

	// A brand without an address sends nothing.
	w2 := newWorld(t)
	tid, _ := w2.chat(customer(), "10 minutes")
	mid := w2.chatReply(tid, "Hi", false)
	if err := w2.s.chatFollowUp(w2.ctx, w2.followUps(mid)[0]); err != nil {
		t.Fatal(err)
	}
	if got := w2.message(mid).Delivery; got != "none" {
		t.Fatalf("no address: %q", got)
	}
}

func TestChatFollowUpFromAPIKey(t *testing.T) {
	w := newWorld(t)
	w.mailbox(InboundForwarding, OutboundSystem, nil)
	tid, _ := w.chat(customer(), "10 minutes")
	mid := w.chatAPIReply(tid, "Your order is on its way", false)
	if n := len(w.followUps(mid)); n != 1 {
		t.Fatalf("API reply follow-ups %d", n)
	}
	if err := w.s.chatFollowUp(w.ctx, w.followUps(mid)[0]); err != nil {
		t.Fatal(err)
	}
	if got := w.message(mid).Delivery; got != "pending" {
		t.Fatalf("API reply delivery %q", got)
	}
	if n := w.jobCount(jobs.SendMessageArgs{}.Kind(), "message_id", mid.String()); n != 1 {
		t.Fatalf("send jobs %d", n)
	}
}
