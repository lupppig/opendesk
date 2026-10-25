package email

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
)

func customer() string { return "carla-" + crypto.RandomString(6) + "@customer.test" }

func TestProcessNewTicketPlain(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	id := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8)))
	if st, _ := w.inbound(id); st != "processed" {
		t.Fatalf("status %s", st)
	}
	msg := w.messageFor(id)
	if msg.Direction != "inbound" || msg.FromEmail != from {
		t.Fatalf("message %+v", msg)
	}
	if !strings.Contains(msg.BodyText, "smoking this morning") || strings.Contains(msg.BodyText, "Head of Printing") {
		t.Fatalf("signature not stripped: %q", msg.BodyText)
	}
	if msg.FullHTML == "" || !strings.Contains(msg.FullHTML, "Head of Printing") {
		t.Fatalf("full_html should keep the original: %q", msg.FullHTML)
	}
	tk := w.ticket(msg.TicketID)
	if tk.Subject != "Printer is on fire" || tk.Channel != "email" || tk.MailboxID == nil || *tk.MailboxID != m.ID {
		t.Fatalf("ticket %+v", tk)
	}
	if tk.Contact == nil || tk.Contact.Email == nil || *tk.Contact.Email != from || tk.Contact.Name != "Carla Customer" {
		t.Fatalf("contact %+v", tk.Contact)
	}
	for _, typ := range []string{"ticket.created", "message.created", "contact.created"} {
		if w.eventCount(typ) != 1 {
			t.Fatalf("want one %s event, got %d", typ, w.eventCount(typ))
		}
	}
	if w.jobCount("helpdesk.ticket_trigger", "", "") != 1 {
		t.Fatal("ticket trigger not queued")
	}
	// The same message again is deduped at ingest.
	raw := fixture(t, "new_plain.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", "x")
	if _, dup, _ := w.s.ingest(w.ctx, m, []byte(raw), "", nil, viaMailbox); dup {
		t.Fatal("different id should not be a dup")
	}
	if _, dup, _ := w.s.ingest(w.ctx, m, []byte(raw), "", nil, viaMailbox); !dup {
		t.Fatal("redelivery should dedupe")
	}
}

// firstTicket opens a ticket from a customer and adds an agent reply with a known
// Message-ID, returning the ticket and that id.
func firstTicket(t *testing.T, w *world, m *mailboxRow, from string) (domain.Ticket, string) {
	t.Helper()
	id := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8)))
	msg := w.messageFor(id)
	tk := w.ticket(msg.TicketID)
	reply := w.agentReply(tk.ID, "Please unplug it right away.", "")
	ours := messageIDFor("m", reply, tk.Number, m)
	mustExec(t, w.d, `UPDATE messages SET email_message_id = $1, delivery_status = 'sent', mailbox_id = $3 WHERE id = $2`, ours, reply, m.ID)
	return tk, ours
}

func TestThreadingGmailReply(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	tk, ours := firstTicket(t, w, m, from)
	id := w.receive(m, fixture(t, "gmail_reply.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8),
		"{{IRT}}", ours, "{{N}}", itoa(tk.Number)))
	msg := w.messageFor(id)
	t.Logf("gmail html=%s\ntext=%q", msg.BodyHTML, msg.BodyText)
	if msg.TicketID != tk.ID {
		t.Fatal("reply opened a new ticket")
	}
	if !strings.Contains(msg.BodyHTML, "<b>worked</b>") || strings.Contains(msg.BodyHTML, "unplug it right away") || strings.Contains(msg.BodyHTML, "wrote:") {
		t.Fatalf("gmail quote not stripped: %s", msg.BodyHTML)
	}
	if strings.Contains(msg.BodyText, "wrote") || !strings.Contains(msg.BodyText, "unplugging it worked") {
		t.Fatalf("text: %q", msg.BodyText)
	}
	if !strings.Contains(msg.FullHTML, "unplug it right away") {
		t.Fatal("full_html lost the quote")
	}
	if msg.InReplyTo != ours || len(msg.References) != 2 {
		t.Fatalf("threading headers: %q %v", msg.InReplyTo, msg.References)
	}
	if got := w.ticket(tk.ID).MessageCount; got != 3 {
		t.Fatalf("message_count %d", got)
	}
}

func TestThreadingOutlookAndApple(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	tk, ours := firstTicket(t, w, m, from)
	repl := []string{"{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{IRT}}", ours, "{{N}}", itoa(tk.Number)}

	id := w.receive(m, fixture(t, "outlook_reply.eml", append(repl, "{{ID}}", crypto.RandomString(8))...))
	msg := w.messageFor(id)
	t.Logf("outlook html=%s\ntext=%q", msg.BodyHTML, msg.BodyText)
	if msg.TicketID != tk.ID {
		t.Fatal("outlook reply not threaded")
	}
	for _, bad := range []string{"invoice was sent", "From:", "<script", "onclick", "alert"} {
		if strings.Contains(msg.BodyHTML, bad) {
			t.Fatalf("outlook body contains %q: %s", bad, msg.BodyHTML)
		}
	}
	if !strings.HasPrefix(msg.BodyHTML, "<style>p{margin:0}</style>") || !strings.Contains(msg.BodyHTML, "font-family: Aptos") {
		t.Fatalf("outlook styles not kept: %s", msg.BodyHTML)
	}
	if !strings.Contains(msg.BodyHTML, "That makes sense") || !strings.Contains(msg.BodyText, "Olaf") || strings.Contains(msg.BodyText, "Monday") {
		t.Fatalf("outlook body: %s / %q", msg.BodyHTML, msg.BodyText)
	}

	id = w.receive(m, fixture(t, "apple_reply.eml", append(repl, "{{ID}}", crypto.RandomString(8))...))
	msg = w.messageFor(id)
	t.Logf("apple html=%s\ntext=%q", msg.BodyHTML, msg.BodyText)
	if msg.TicketID != tk.ID {
		t.Fatal("apple reply not threaded")
	}
	if strings.Contains(msg.BodyHTML, "ships tomorrow") || strings.Contains(msg.BodyHTML, "wrote:") || !strings.Contains(msg.BodyHTML, "see you then") {
		t.Fatalf("apple body: %s", msg.BodyHTML)
	}
	if msg.FullHTML == "" {
		t.Fatal("has_quoted should be true")
	}
}

func TestThreadingPlusAddressAndSubjectTag(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	tk, _ := firstTicket(t, w, m, from)
	plus := w.forwardAddr(m, "t"+itoa(tk.Number))
	// No References, only the +t<n> address.
	raw := "From: " + from + "\r\nTo: " + plus + "\r\nSubject: Another thing\r\nMessage-ID: <p" + crypto.RandomString(6) + "@c.test>\r\n\r\nAlso the scanner.\r\n"
	msg := w.messageFor(w.receive(m, raw))
	if msg.TicketID != tk.ID {
		t.Fatal("plus address did not thread")
	}
	// Subject tag from the contact threads.
	raw = "From: " + from + "\r\nTo: support@acme.test\r\nSubject: Re: whatever [#" + itoa(tk.Number) + "]\r\nMessage-ID: <s" + crypto.RandomString(6) + "@c.test>\r\n\r\nOne more.\r\n"
	if w.messageFor(w.receive(m, raw)).TicketID != tk.ID {
		t.Fatal("subject tag did not thread")
	}
	// The same tag from someone else opens a new ticket.
	other := customer()
	raw = "From: " + other + "\r\nTo: " + plus + "\r\nSubject: Re: whatever [#" + itoa(tk.Number) + "]\r\nMessage-ID: <o" + crypto.RandomString(6) + "@c.test>\r\n\r\nLet me in.\r\n"
	msg = w.messageFor(w.receive(m, raw))
	if msg.TicketID == tk.ID {
		t.Fatal("a stranger's subject tag must not reach another customer's ticket")
	}
}

func TestReplyToClosedTicketOpensNew(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	tk, ours := firstTicket(t, w, m, from)
	mustExec(t, w.d, `UPDATE tickets SET status = 'closed' WHERE id = $1`, tk.ID)
	id := w.receive(m, fixture(t, "gmail_reply.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8),
		"{{IRT}}", ours, "{{N}}", itoa(tk.Number)))
	msg := w.messageFor(id)
	if msg.TicketID == tk.ID {
		t.Fatal("reply to a closed ticket must open a new one")
	}
	nt := w.ticket(msg.TicketID)
	if nt.Custom["previous_ticket_id"] != tk.ID.String() || nt.Subject != "Printer is on fire" {
		t.Fatalf("new ticket %+v", nt)
	}
}

func TestInlineImageAndSanitize(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	id := w.receive(m, fixture(t, "inline_image.eml", "{{FROM}}", customer(), "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8)))
	msg := w.messageFor(id)
	t.Logf("inline html=%s", msg.BodyHTML)
	var attID uuid.UUID
	var inline bool
	var cid string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id, inline, content_id FROM attachments WHERE message_id = $1`, msg.ID).Scan(&attID, &inline, &cid); err != nil {
		t.Fatal(err)
	}
	if !inline || cid != "shot1@customer.test" {
		t.Fatalf("inline=%v cid=%q", inline, cid)
	}
	if !strings.Contains(msg.BodyHTML, "http://api.test/v1/attachments/"+attID.String()+"?exp=") {
		t.Fatalf("cid not rewritten: %s", msg.BodyHTML)
	}
	for _, bad := range []string{"cid:", "onerror", "javascript:"} {
		if strings.Contains(msg.BodyHTML, bad) {
			t.Fatalf("body contains %q: %s", bad, msg.BodyHTML)
		}
	}
	if !strings.Contains(msg.BodyHTML, `target="_blank"`) || !strings.Contains(msg.BodyHTML, "noopener") || !strings.Contains(msg.BodyHTML, "nofollow") {
		t.Fatalf("links not hardened: %s", msg.BodyHTML)
	}
}

func TestAttachmentStored(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	id := w.receive(m, fixture(t, "attachment.eml", "{{FROM}}", customer(), "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8)))
	msg := w.messageFor(id)
	var name, ct, key string
	var size int64
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT filename, content_type, storage_key, size_bytes FROM attachments WHERE message_id = $1`, msg.ID).
		Scan(&name, &ct, &key, &size); err != nil {
		t.Fatal(err)
	}
	if name != "contract.pdf" || ct != "application/pdf" || size == 0 || !strings.HasPrefix(key, "workspaces/"+w.wid.String()+"/attachments/") {
		t.Fatalf("attachment %q %q %q %d", name, ct, key, size)
	}
	data, err := w.s.readObject(w.ctx, key, 1<<20)
	if err != nil || !strings.HasPrefix(string(data), "%PDF") {
		t.Fatalf("stored object: %v %q", err, data)
	}
	loaded, err := domain.LoadMessage(w.ctx, w.d.DB.Pool, w.wid, msg.ID)
	if err != nil || len(loaded.Attachments) != 1 {
		t.Fatalf("message attachments: %v %+v", err, loaded.Attachments)
	}
}

func TestActiveAttachmentStoredAsText(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	raw := "From: " + customer() + "\r\nTo: " + w.forwardAddr(m, "") + "\r\nSubject: Page\r\nMessage-ID: <act" + crypto.RandomString(6) + "@a.test>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nSee attached.\r\n" +
		"--b\r\nContent-Type: image/png\r\nContent-Disposition: attachment; filename=\"page.png\"\r\n\r\n<html><script>alert(1)</script></html>\r\n" +
		"--b--\r\n"
	msg := w.messageFor(w.receive(m, raw))
	var ct string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT content_type FROM attachments WHERE message_id = $1`, msg.ID).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if ct != "text/plain; charset=utf-8" {
		t.Fatalf("an HTML file claiming to be an image is stored as %q", ct)
	}
}

func TestNoiseIgnored(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	id := w.receive(m, fixture(t, "auto_reply.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8)))
	if st, reason := w.inbound(id); st != "ignored" || reason != "auto_submitted" {
		t.Fatalf("auto reply: %s %s", st, reason)
	}
	// From our own mailbox: a loop.
	raw := "From: " + m.Email + "\r\nTo: x@y.test\r\nSubject: hi\r\nMessage-ID: <own" + crypto.RandomString(5) + "@a.test>\r\n\r\nbody\r\n"
	if st, reason := w.inbound(w.receive(m, raw)); st != "ignored" || reason != "from_own_mailbox" {
		t.Fatalf("own: %s %s", st, reason)
	}
	// Bulk precedence.
	raw = "From: news@shop.test\r\nTo: x@y.test\r\nPrecedence: bulk\r\nSubject: Sale\r\nMessage-ID: <b" + crypto.RandomString(5) + "@a.test>\r\n\r\nbody\r\n"
	if st, reason := w.inbound(w.receive(m, raw)); st != "ignored" || reason != "precedence" {
		t.Fatalf("bulk: %s %s", st, reason)
	}
	// Google Group redistributing the support address is not noise.
	raw = "From: " + from + "\r\nTo: " + m.Email + "\r\nPrecedence: list\r\nList-Id: <" + strings.Replace(m.Email, "@", ".", 1) + ">\r\nSubject: Help\r\nMessage-ID: <g" + crypto.RandomString(5) + "@a.test>\r\n\r\nbody\r\n"
	if st, _ := w.inbound(w.receive(m, raw)); st != "processed" {
		t.Fatalf("own group list mail should be processed, got %s", st)
	}
	// Blocked contact.
	blocked := customer()
	mustExec(t, w.d, `INSERT INTO contacts (workspace_id, email, name, blocked) VALUES ($1, $2, 'B', true)`, w.wid, blocked)
	raw = "From: " + blocked + "\r\nTo: x@y.test\r\nSubject: hi\r\nMessage-ID: <bl" + crypto.RandomString(5) + "@a.test>\r\n\r\nbody\r\n"
	if st, reason := w.inbound(w.receive(m, raw)); st != "ignored" || reason != "blocked_contact" {
		t.Fatalf("blocked: %s %s", st, reason)
	}
	if n := w.count(`SELECT count(*) FROM tickets WHERE workspace_id = $1`, w.wid); n != 1 {
		t.Fatalf("only the group mail should open a ticket, got %d", n)
	}
}

func TestBounceRecorded(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	tk, ours := firstTicket(t, w, m, customer())
	id := w.receive(m, fixture(t, "bounce.eml", "{{OUR}}", ours, "{{ID}}", crypto.RandomString(8)))
	if st, reason := w.inbound(id); st != "ignored" || reason != "bounce" {
		t.Fatalf("bounce: %s %s", st, reason)
	}
	var status, detail string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT delivery_status, delivery_error FROM messages WHERE email_message_id = $1`, ours).Scan(&status, &detail); err != nil {
		t.Fatal(err)
	}
	if status != "bounced" || !strings.Contains(detail, "5.1.1") || !strings.Contains(detail, "nobody@customer.test") {
		t.Fatalf("bounce not recorded: %s %q", status, detail)
	}
	if w.eventCount("message.updated") != 1 {
		t.Fatal("message.updated not published")
	}
	if n := w.count(`SELECT count(*) FROM tickets WHERE workspace_id = $1`, w.wid); n != 1 || w.ticket(tk.ID).ID != tk.ID {
		t.Fatal("bounce must not open a ticket")
	}
}

func TestDMARCFailMarksSpam(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.AutoReplyEnabled, m.AutoReplyHTML = true, "<p>Thanks</p>"
	})
	id := w.receive(m, fixture(t, "dmarc_fail.eml", "{{FROM}}", customer(), "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8)))
	msg := w.messageFor(id)
	if !w.ticket(msg.TicketID).Spam {
		t.Fatal("dmarc=fail should mark spam")
	}
	if strings.Contains(msg.BodyHTML, "<script") || strings.Contains(msg.BodyHTML, "<style") {
		t.Fatalf("not sanitized: %s", msg.BodyHTML)
	}
	if w.jobCount("email.send_ticket_email", "", "") != 0 {
		t.Fatal("no auto-reply to spam")
	}
}

func TestAutoReplyRateLimited(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.AutoReplyEnabled, m.AutoReplySubject, m.AutoReplyHTML = true, "We got it: {{ticket.subject}}", "<p>Hi {{contact.name}}, ticket {{ticket.number}}.</p>"
	})
	from := customer()
	for i := 0; i < 2; i++ {
		raw := "From: Zed <" + from + ">\r\nTo: x@y.test\r\nSubject: Question " + itoa(int64(i)) + "\r\nMessage-ID: <ar" + crypto.RandomString(6) + "@a.test>\r\n\r\nbody\r\n"
		w.receive(m, raw)
	}
	if n := w.jobCount("email.send_ticket_email", "", ""); n != 1 {
		t.Fatalf("want one auto-reply per sender per window, got %d", n)
	}
	// The subject and body are filled when the email goes out.
	sent := w.sendQueued(t)
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	subject, html := sent[0].GetHeader("Subject"), sent[0].HTML
	if sent[0].GetHeader("Auto-Submitted") == "" || !strings.HasPrefix(subject, "We got it: Question 0 [#") || !strings.Contains(html, "Hi Zed") {
		t.Fatalf("auto reply %q %q", subject, html)
	}
}

// Imported history (contracts/imports.md "CSV") keeps its Message-IDs: a live reply threads
// into the imported ticket, and the first mailbox sync's copy of an imported email is
// already seen.
func TestImportedHistoryThreadsAndDedupes(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	id := crypto.RandomString(8)
	asked, answered := "<"+id+"@mail.customer.test>", "<answer"+id+"@acme.test>"
	var tk domain.Ticket
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		contact, _, err := domain.UpsertContactByEmail(w.ctx, tx, w.wid, from, "Carla Customer")
		if err != nil {
			return err
		}
		created := time.Now().Add(-48 * time.Hour).UTC()
		tk, err = domain.CreateTicket(w.ctx, tx, domain.NewTicket{WorkspaceID: w.wid, Subject: "Printer is on fire", Channel: domain.ChannelEmail,
			MailboxID: &m.ID, ContactID: &contact, Status: domain.StatusSolved, CreatedAt: created, Imported: true})
		if err != nil {
			return err
		}
		for _, nm := range []domain.NewMessage{
			{Direction: "inbound", AuthorType: "contact", AuthorContactID: &contact, FromEmail: from, EmailMessageID: &asked, CreatedAt: created},
			{Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, EmailMessageID: &answered, InReplyTo: asked,
				References: []string{asked}, CreatedAt: created.Add(time.Hour)},
		} {
			nm.WorkspaceID, nm.TicketID, nm.Kind, nm.BodyHTML, nm.BodyText, nm.Imported = w.wid, tk.ID, "reply", "<p>Hi</p>", "Hi", true
			if _, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, nm); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The first sync reads the customer's email again: it is the imported one.
	copyID := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", id))
	if status, reason := w.inbound(copyID); status != "ignored" || reason != "duplicate" {
		t.Fatalf("copy of an imported email: %s %s", status, reason)
	}

	// A reply to the imported answer continues the imported ticket.
	replyID := w.receive(m, fixture(t, "gmail_reply.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8),
		"{{IRT}}", answered, "{{N}}", "999999"))
	if msg := w.messageFor(replyID); msg.TicketID != tk.ID {
		t.Fatal("a reply to imported history opened a new ticket")
	}
	if got := w.ticket(tk.ID); got.Status != domain.StatusOpen || got.MessageCount != 3 {
		t.Fatalf("imported ticket after the reply: %s, %d messages", got.Status, got.MessageCount)
	}
}
