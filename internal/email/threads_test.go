package email

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jhillyerd/enmime/v2"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/ids"
)

// startThread opens a third-party thread on a ticket and writes its first message, the way
// the helpdesk API does.
func (w *world) startThread(ticketID uuid.UUID, m *mailboxRow, to, subject, body string, includeHistory ...bool) (uuid.UUID, uuid.UUID) {
	w.t.Helper()
	thread := ids.New()
	var mid uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(w.ctx, `INSERT INTO ticket_threads (id, workspace_id, ticket_id, subject, to_emails, mailbox_id, created_by, include_history)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, thread, w.wid, ticketID, subject, []string{to}, m.ID, w.uid, len(includeHistory) > 0 && includeHistory[0]); err != nil {
			return err
		}
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: ticketID, ThreadID: &thread,
			Kind: "note", Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, BodyHTML: "<p>" + body + "</p>", BodyText: body,
			Subject: subject, To: []string{to}, MailboxID: &m.ID, DeliveryStatus: "pending"})
		mid = msg.ID
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return thread, mid
}

func TestThirdPartyThread(t *testing.T) {
	w := newWorld(t)
	sink, port := startSink(t, w)
	m := w.mailbox(InboundIMAP, OutboundSMTP, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: 1, IMAPTLS: TLSImplicit, IMAPUsername: "u",
			SMTPHost: "127.0.0.1", SMTPPort: port, SMTPTLS: TLSStart, SMTPUsername: "support@acme.test"}
		c.Password = "imap-pass"
		c.SMTPPassword = "s3cret-pass"
	})
	from := customer()
	in := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", from, "{{TO}}", "", "{{ID}}", "abc"+crypto.RandomString(6)))
	tk := w.ticket(w.messageFor(in).TicketID)
	mustExec(t, w.d, `UPDATE tickets SET status = 'pending', assignee_id = $2 WHERE id = $1`, tk.ID, w.uid)
	before := w.ticket(tk.ID)

	// The team asks a partner: the note is emailed to them, from the ticket's address.
	partner := "ops-" + crypto.RandomString(5) + "@acquirer.test"
	thread, mid := w.startThread(tk.ID, m, partner, "Payout for order 1182", "Can you check the payout for order 1182?")
	// A footer or signature naming the customer is filled without them for a third party.
	mustExec(t, w.d, `INSERT INTO email_templates (workspace_id, brand_id, kind, options) VALUES ($1, $2, 'layout',
		'{"mode":"simple","footer_html":"<p>Sent to {{ contact.email }} about #{{ ticket.number }}</p>"}')`, w.wid, w.brand)
	mustExec(t, w.d, `UPDATE mailboxes SET signature_html = '<p>Acme support for {{ contact.name }}</p>' WHERE id = $1`, m.ID)
	if w.jobCount("email.send_message", "message_id", mid.String()) != 1 {
		t.Fatal("a thread message should queue its send")
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(sink.msgs) != 1 {
		t.Fatalf("sink got %d messages", len(sink.msgs))
	}
	got := sink.msgs[0]
	if len(got.rcpts) != 1 || got.rcpts[0] != partner {
		t.Fatalf("thread mail went to %v", got.rcpts)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(got.data))
	if err != nil {
		t.Fatal(err)
	}
	if s := env.GetHeader("Subject"); s != "Payout for order 1182" {
		t.Errorf("subject %q", s)
	}
	if env.GetHeader("In-Reply-To") != "" {
		t.Error("a new thread does not continue the customer's conversation")
	}
	if !strings.Contains(env.HTML, "check the payout") {
		t.Errorf("html %q", env.HTML)
	}
	if strings.Contains(env.HTML, "smoking") || strings.Contains(env.Text, "smoking") {
		t.Error("the customer's conversation must not reach the third party")
	}
	if strings.Contains(env.HTML, from) || strings.Contains(env.Text, from) || strings.Contains(env.HTML, "Carla") {
		t.Errorf("the customer's details must not reach the third party: %s", env.HTML)
	}
	if !strings.Contains(env.HTML, "Acme support for") {
		t.Errorf("the address's signature still goes: %s", env.HTML)
	}
	sent := w.message(mid)
	if sent.Delivery != "sent" || sent.MsgID == nil {
		t.Fatalf("delivery %s %v", sent.Delivery, sent.MsgID)
	}
	if n := w.count(`SELECT count(*) FROM messages WHERE id = $1 AND kind = 'note' AND thread_id = $2`, mid, thread); n != 1 {
		t.Fatal("the thread message is a note in the thread")
	}

	// The partner answers: the reply lands in the thread as a note, not as the customer's.
	reply := "From: Olga Ops <" + partner + ">\nTo: " + m.Email + "\nSubject: Re: Payout for order 1182\n" +
		"Date: Sun, 11 Oct 2026 10:00:00 +0000\nMessage-ID: <r" + crypto.RandomString(8) + "@acquirer.test>\n" +
		"In-Reply-To: " + *sent.MsgID + "\nReferences: " + *sent.MsgID + "\nMIME-Version: 1.0\n" +
		"Content-Type: text/plain; charset=utf-8\n\nThe payout went out this morning.\n"
	rin := w.receive(m, reply)
	if status, _ := w.inbound(rin); status != "processed" {
		t.Fatalf("inbound %s", status)
	}
	rm := w.messageFor(rin)
	if rm.TicketID != tk.ID || rm.Direction != "inbound" {
		t.Fatalf("reply went to %s (%s)", rm.TicketID, rm.Direction)
	}
	if n := w.count(`SELECT count(*) FROM messages WHERE id = $1 AND kind = 'note' AND thread_id = $2`, rm.ID, thread); n != 1 {
		t.Fatal("the reply is a note in the thread")
	}
	if n := w.count(`SELECT count(*) FROM contacts WHERE workspace_id = $1 AND email = $2`, w.wid, partner); n != 0 {
		t.Fatal("a third party does not become a contact")
	}
	if n := w.count(`SELECT count(*) FROM tickets WHERE workspace_id = $1`, w.wid); n != 1 {
		t.Fatalf("%d tickets: the reply opened a new one", n)
	}
	after := w.ticket(tk.ID)
	if after.Status != domain.StatusOpen {
		t.Errorf("status %s: the partner's answer opens a pending ticket", after.Status)
	}
	if after.MessageCount != before.MessageCount || !after.LastMessageAt.Equal(before.LastMessageAt) {
		t.Error("a thread reply is not part of the customer's conversation")
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'helpdesk.ticket_trigger' AND args->>'ticket_id' = $1 AND args->>'trigger' = 'thread_replied'`,
		tk.ID.String()); n != 1 {
		t.Fatalf("%d thread_replied triggers", n)
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'email.send_ticket_email' AND args->>'ticket_id' = $1`, tk.ID.String()); n != 0 {
		t.Fatal("nothing automatic answers a third party")
	}

	// A second message in the thread answers the partner's reply, inside the thread only.
	var mid2 uuid.UUID
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk.ID, ThreadID: &thread,
			Kind: "note", Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, BodyHTML: "<p>Thanks!</p>", BodyText: "Thanks!",
			Subject: "Payout for order 1182", To: []string{partner}, MailboxID: &m.ID, DeliveryStatus: "pending"})
		mid2 = msg.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid2, false); err != nil {
		t.Fatal(err)
	}
	env2, err := enmime.ReadEnvelope(bytes.NewReader(sink.msgs[1].data))
	if err != nil {
		t.Fatal(err)
	}
	if g := env2.GetHeader("In-Reply-To"); g != *rm.MsgID {
		t.Errorf("In-Reply-To %q, want the partner's reply %q", g, *rm.MsgID)
	}
	if s := env2.GetHeader("Subject"); s != "Re: Payout for order 1182" {
		t.Errorf("subject %q", s)
	}

	// The customer's own reply still threads into the conversation as before.
	cust := "From: Carla Customer <" + from + ">\nTo: " + m.Email + "\nSubject: Re: Printer is on fire\n" +
		"Date: Sun, 11 Oct 2026 11:00:00 +0000\nMessage-ID: <c" + crypto.RandomString(8) + "@mail.customer.test>\n" +
		"In-Reply-To: " + *w.messageFor(in).MsgID + "\nMIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\nAny news?\n"
	cm := w.messageFor(w.receive(m, cust))
	if n := w.count(`SELECT count(*) FROM messages WHERE id = $1 AND kind = 'reply' AND thread_id IS NULL`, cm.ID); n != 1 || cm.TicketID != tk.ID {
		t.Fatal("the customer's reply is a reply on the ticket")
	}
}

// Asked for, the first message quotes the conversation with the customer, never a note, and
// later messages in the thread do not repeat it.
func TestThirdPartyThreadWithHistory(t *testing.T) {
	w := newWorld(t)
	sink, port := startSink(t, w)
	m := w.mailbox(InboundIMAP, OutboundSMTP, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: 1, IMAPTLS: TLSImplicit, IMAPUsername: "u",
			SMTPHost: "127.0.0.1", SMTPPort: port, SMTPTLS: TLSStart, SMTPUsername: "support@acme.test"}
		c.Password = "imap-pass"
		c.SMTPPassword = "s3cret-pass"
	})
	in := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", customer(), "{{TO}}", "", "{{ID}}", "hist"+crypto.RandomString(6)))
	tk := w.ticket(w.messageFor(in).TicketID)
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		_, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk.ID, Kind: "note", Direction: "outbound",
			AuthorType: "user", AuthorUserID: &w.uid, BodyHTML: "<p>Internal: refund approved by finance</p>", BodyText: "Internal: refund approved by finance"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	partner := "ops-" + crypto.RandomString(5) + "@acquirer.test"
	thread, mid := w.startThread(tk.ID, m, partner, "Printer", "Have you seen this before?", true)
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(sink.msgs[0].data))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.HTML, "smoking") || !strings.Contains(env.Text, "smoking") {
		t.Error("the customer's conversation is quoted under the first message")
	}
	if strings.Contains(env.HTML, "refund approved") || strings.Contains(env.Text, "refund approved") {
		t.Error("a note never goes to the third party")
	}

	var mid2 uuid.UUID
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk.ID, ThreadID: &thread,
			Kind: "note", Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, BodyHTML: "<p>Any news?</p>", BodyText: "Any news?",
			Subject: "Printer", To: []string{partner}, MailboxID: &m.ID, DeliveryStatus: "pending"})
		mid2 = msg.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid2, false); err != nil {
		t.Fatal(err)
	}
	env2, err := enmime.ReadEnvelope(bytes.NewReader(sink.msgs[1].data))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env2.HTML, "smoking") {
		t.Error("only the first message quotes the conversation")
	}
}
