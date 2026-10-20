package email

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jhillyerd/enmime/v2"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/ids"
)

// refMail is a message from `from` naming refs in References.
func refMail(from, to, subject, refs string) string {
	return "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nReferences: " + refs +
		"\r\nMessage-ID: <r" + crypto.RandomString(10) + "@c.test>\r\n\r\nHello\r\n"
}

func TestThreadingOurMessageIDMustProveItself(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	victim := customer()
	tk, ours := firstTicket(t, w, m, victim)
	n := itoa(tk.Number)
	attacker := "mallory-" + crypto.RandomString(5) + "@evil.test"

	// Ids shaped like ours but naming no message of this ticket: a stranger's mail opens
	// its own ticket.
	for name, ref := range map[string]string{
		"unknown m_ id":   "<m_" + ids.New().String() + ".t" + n + "@acme.test>",
		"forged a_ id":    "<a_" + ids.New().String() + ".t" + n + "@acme.test>",
		"m_ id, other #n": strings.Replace(ours, ".t"+n+"@", ".t"+itoa(tk.Number+1)+"@", 1),
	} {
		msg := w.messageFor(w.receive(m, refMail(attacker, m.Email, "Re: hello", ref)))
		if msg.TicketID == tk.ID {
			t.Errorf("%s: a stranger threaded into another customer's ticket", name)
		}
	}
	// The ticket's own contact still threads on the number.
	if w.messageFor(w.receive(m, refMail(victim, m.Email, "Re: hello", "<m_"+ids.New().String()+".t"+n+"@acme.test>"))).TicketID != tk.ID {
		t.Error("the contact's reply naming the ticket number did not thread")
	}

	// A provider re-stamped what we sent: our id still names the message, so anyone on the
	// conversation (a colleague in Cc) threads.
	reply := w.agentReply(tk.ID, "Second answer", "")
	mustExec(t, w.d, `UPDATE messages SET email_message_id = $1, delivery_status = 'sent' WHERE id = $2`, "<restamped"+crypto.RandomString(6)+"@mail.test>", reply)
	colleague := "colin-" + crypto.RandomString(5) + "@customer.test"
	if w.messageFor(w.receive(m, refMail(colleague, m.Email, "Re: hello", messageIDFor("m", reply, tk.Number, m)))).TicketID != tk.ID {
		t.Error("a reply naming our own message id did not thread")
	}

	// An auto-reply id we made threads; the same shape for another ticket does not.
	auto := w.s.autoMessageID(w.wid, tk.Number, m)
	if w.messageFor(w.receive(m, refMail(colleague, m.Email, "Re: hello", auto))).TicketID != tk.ID {
		t.Error("a reply to our auto-reply did not thread")
	}
	kind, id, _, _ := ourMessageID(auto)
	if kind != 'a' || w.s.verifyAutoID(w.wid, id, tk.Number+1) || w.s.verifyAutoID(ids.New(), id, tk.Number) {
		t.Error("an auto-reply id verified for another ticket or workspace")
	}
}

func TestThreadingNeverCrossesWorkspaces(t *testing.T) {
	w1, w2 := newWorld(t), newWorld(t)
	m1 := w1.mailbox(InboundForwarding, OutboundSystem, nil)
	m2 := w2.mailbox(InboundForwarding, OutboundSystem, nil)
	tk1, _ := firstTicket(t, w1, m1, customer())
	tk2, ours2 := firstTicket(t, w2, m2, customer())
	if tk1.Number != tk2.Number {
		t.Skipf("ticket numbers differ (%d, %d)", tk1.Number, tk2.Number)
	}
	attacker := "mallory-" + crypto.RandomString(5) + "@evil.test"
	// Another workspace's real message id, and an auto-reply id made for it, carry the same
	// ticket number but prove nothing here.
	for _, ref := range []string{ours2, w2.s.autoMessageID(w2.wid, tk2.Number, m2)} {
		msg := w1.messageFor(w1.receive(m1, refMail(attacker, m1.Email, "Re: hi", ref)))
		if msg.TicketID == tk1.ID {
			t.Errorf("%s threaded across workspaces", ref)
		}
	}
}

func TestReplyToIsNotIdentity(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	victim := customer()
	tk, _ := firstTicket(t, w, m, victim)
	attacker := "mallory-" + crypto.RandomString(5) + "@evil.test"

	// Reply-To naming the ticket's customer neither threads nor speaks as them.
	raw := "From: Mallory <" + attacker + ">\r\nReply-To: " + victim + "\r\nTo: " + m.Email + "\r\nSubject: Re: hello [#" + itoa(tk.Number) +
		"]\r\nMessage-ID: <rt" + crypto.RandomString(8) + "@evil.test>\r\n\r\nPlease change my address.\r\n"
	msg := w.messageFor(w.receive(m, raw))
	if msg.TicketID == tk.ID {
		t.Fatal("Reply-To threaded a stranger into the customer's ticket")
	}
	if msg.FromEmail != attacker {
		t.Fatalf("message from %q, want the From address", msg.FromEmail)
	}
	nt := w.ticket(msg.TicketID)
	if nt.Contact == nil || nt.Contact.Email == nil || *nt.Contact.Email != attacker {
		t.Fatalf("contact %+v", nt.Contact)
	}
	var replyTo string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT reply_to FROM messages WHERE id = $1`, msg.ID).Scan(&replyTo); err != nil || replyTo != victim {
		t.Fatalf("reply_to %q %v", replyTo, err)
	}

	// Replies go to that Reply-To only when it is at the From address's domain.
	var rcpts []string
	w.s.rawSend = func(_ context.Context, _ string, to []string, _ []byte) error { rcpts = to; return nil }
	send := func(ticketID uuid.UUID) []string {
		t.Helper()
		rcpts = nil
		mid := w.agentReply(ticketID, "On it", "")
		if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
			t.Fatal(err)
		}
		return rcpts
	}
	if got := send(nt.ID); len(got) != 1 || got[0] != attacker {
		t.Fatalf("reply to a foreign Reply-To went to %v", got)
	}
	system, person := "billing-system-"+crypto.RandomString(4)+"@corp.test", "jane-"+crypto.RandomString(4)+"@corp.test"
	raw = "From: " + system + "\r\nReply-To: Jane <" + person + ">\r\nTo: " + m.Email + "\r\nSubject: Invoice question\r\nMessage-ID: <sd" +
		crypto.RandomString(8) + "@corp.test>\r\n\r\nWhere is it?\r\n"
	same := w.messageFor(w.receive(m, raw))
	if got := send(same.TicketID); len(got) != 1 || got[0] != person {
		t.Fatalf("reply to a same-domain Reply-To went to %v", got)
	}
}

func TestDMARCTrustedAuthservOnly(t *testing.T) {
	trusted := []string{"mx.in.test"}
	cases := []struct {
		name    string
		headers string
		trusted []string
		want    authVerdict
	}{
		{"trusted fail", "Authentication-Results: mx.in.test; spf=fail; dmarc=fail header.from=x.test\r\n", trusted, authFail},
		{"untrusted fail", "Authentication-Results: other.test; dmarc=fail header.from=x.test\r\n", trusted, authUnknown},
		{"nothing trusted", "Authentication-Results: mx.in.test; dmarc=fail\r\n", nil, authUnknown},
		{"sender's copy below the server's", "Authentication-Results: mx.in.test; dmarc=fail (p=reject)\r\n" +
			"Authentication-Results: mx.in.test; dmarc=pass\r\n", trusted, authFail},
		{"untrusted on top", "Authentication-Results: relay.test; dmarc=pass\r\nAuthentication-Results: MX.IN.TEST 1; dmarc=fail\r\n", trusted, authFail},
		{"arc", "ARC-Authentication-Results: i=1; mx.in.test;\r\n dkim=none; dmarc=fail header.from=x.test\r\n", trusted, authFail},
		{"no dmarc", "Authentication-Results: mx.in.test; spf=pass\r\n", trusted, authUnknown},
		{"microsoft", "Authentication-Results: spf=fail (sender IP is 192.0.2.1) smtp.mailfrom=x.test; dmarc=fail action=none header.from=x.test\r\n",
			[]string{"", "mx.microsoft.com"}, authFail},
		{"no id, not trusted", "Authentication-Results: dmarc=fail header.from=x.test\r\n", trusted, authUnknown},
	}
	for _, c := range cases {
		raw := []byte(c.headers + "From: a@x.test\r\nSubject: s\r\n\r\nbody\r\n")
		if got := dmarcVerdict(raw, c.trusted); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	raw := "Authentication-Results: spoofed.example; dmarc=fail header.from=x.test\r\n" + rawMail(customer(), m.Email, "Hello", "")
	if w.ticket(w.messageFor(w.receive(m, raw)).TicketID).Spam {
		t.Fatal("an untrusted authserv-id decided the verdict")
	}
}

func TestDMARCFailOnThreadedMessage(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) { m.AIAnswers = true })
	from := customer()
	tk, ours := firstTicket(t, w, m, from)
	mustExec(t, w.d, `UPDATE tickets SET status = 'solved', solved_at = now() WHERE id = $1`, tk.ID)
	triggers := w.jobCount("helpdesk.ticket_trigger", "", "")

	raw := "Authentication-Results: mx.in.test; dmarc=fail header.from=customer.test\r\n" +
		"From: " + from + "\r\nTo: " + m.Email + "\r\nSubject: Re: hello\r\nIn-Reply-To: " + ours +
		"\r\nMessage-ID: <df" + crypto.RandomString(8) + "@spoof.test>\r\n\r\nWire the refund to this account.\r\n"
	msg := w.messageFor(w.receive(m, raw))
	if msg.TicketID != tk.ID {
		t.Fatal("the message should stay on its conversation")
	}
	var authFailed, noAuto bool
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT auth_failed, no_auto_reply FROM messages WHERE id = $1`, msg.ID).Scan(&authFailed, &noAuto); err != nil {
		t.Fatal(err)
	}
	if !authFailed || !noAuto {
		t.Fatalf("auth_failed %v no_auto_reply %v", authFailed, noAuto)
	}
	got := w.ticket(tk.ID)
	if got.Status != domain.StatusSolved || got.Spam {
		t.Fatalf("ticket status %s spam %v: only the message is flagged", got.Status, got.Spam)
	}
	if n := w.jobCount("helpdesk.ticket_trigger", "", ""); n != triggers {
		t.Fatal("customer_replied automations queued for an unauthenticated sender")
	}
	if n := w.jobCount("ai.email_answer", "", ""); n != 0 {
		t.Fatal("Ollie queued for an unauthenticated sender")
	}
	if n := w.count(`SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'status_changed' AND data->>'reason' = 'sender_unverified'`, tk.ID); n != 1 {
		t.Fatalf("status kept without a timeline entry (%d)", n)
	}
}

func TestAutomaticMailFlaggedAndAutomationRepliesMarked(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	flag := func(id uuid.UUID) bool {
		t.Helper()
		var v bool
		if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT no_auto_reply FROM messages WHERE inbound_email_id = $1`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for name, raw := range map[string]string{
		"list":           rawMail(customer(), m.Email, "News", "List-Unsubscribe: <https://list.test/u>\r\n"),
		"suppress":       rawMail(customer(), m.Email, "Hi", "X-Auto-Response-Suppress: All\r\n"),
		"someone's loop": rawMail(customer(), m.Email, "Hi", "X-Loop: helpdesk@vendor.test\r\n"),
		"no-reply":       rawMail("no-reply@vendor.test", m.Email, "Receipt", ""),
	} {
		if !flag(w.receive(m, raw)) {
			t.Errorf("%s: not flagged no_auto_reply", name)
		}
	}
	if flag(w.receive(m, rawMail(customer(), m.Email, "Question", ""))) {
		t.Error("a person's mail flagged no_auto_reply")
	}

	// An automation's email reply is marked automatic.
	tk, _ := firstTicket(t, w, m, customer())
	var raw []byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, b []byte) error { raw = b; return nil }
	var mid uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk.ID, Kind: "reply",
			Direction: "outbound", AuthorType: "system", BodyHTML: "<p>Thanks</p>", BodyText: "Thanks", To: []string{*tk.Contact.Email},
			DeliveryStatus: "pending", Trigger: "none"})
		mid = msg.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if env.GetHeader("Auto-Submitted") != "auto-generated" || env.GetHeader("X-Auto-Response-Suppress") != "All" {
		t.Errorf("automation reply not marked automatic: %q %q", env.GetHeader("Auto-Submitted"), env.GetHeader("X-Auto-Response-Suppress"))
	}
	if g := env.GetHeader("X-Loop"); strings.Contains(g, "@") || g != w.s.loopToken(m.ID) {
		t.Errorf("X-Loop %q", g)
	}
}

func TestLoopTokenRecognized(t *testing.T) {
	w := newWorld(t)
	a := w.mailbox(InboundForwarding, OutboundSystem, nil)
	b := w.mailbox(InboundForwarding, OutboundSystem, nil)
	id := w.receive(b, rawMail(customer(), b.Email, "x", "X-Loop: "+w.s.loopToken(a.ID)+"\r\n"))
	if st, reason := w.inbound(id); st != "ignored" || reason != "loop" {
		t.Fatalf("own loop token: %s %s", st, reason)
	}
	// Another workspace's token is not ours.
	other := w.s.loopToken(ids.New())
	if st, _ := w.inbound(w.receive(b, rawMail(customer(), b.Email, "x", "X-Loop: "+other+"\r\n"))); st != "processed" {
		t.Fatalf("a foreign loop token dropped the mail: %s", st)
	}
}

func TestForgedBounceIgnored(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	other := w.mailbox(InboundForwarding, OutboundSystem, nil)
	_, ours := firstTicket(t, w, m, customer())
	status := func() string {
		var s string
		if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT delivery_status FROM messages WHERE email_message_id = $1`, ours).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	dsn := fixture(t, "bounce.eml", "{{OUR}}", ours, "{{ID}}", crypto.RandomString(8))

	// A report with a sender, made to look like a bounce: not a bounce.
	forged := strings.Replace(dsn, "Return-Path: <>", "Return-Path: <mallory@evil.test>", 1)
	forged = strings.Replace(forged, "mailer-daemon@googlemail.com", "mallory@evil.test", 1)
	forged = strings.Replace(forged, "Auto-Submitted: auto-replied\n", "", 1)
	id := w.receive(m, forged)
	if st, reason := w.inbound(id); st != "processed" || reason == "bounce" {
		t.Fatalf("forged report: %s %s", st, reason)
	}
	if status() != "sent" {
		t.Fatal("a forged report marked our message bounced")
	}
	// The envelope sender the relay reports wins over a Return-Path in the message.
	raw := []byte(strings.ReplaceAll(strings.ReplaceAll(dsn, "\r\n", "\n"), "\n", "\r\n"))
	raw = bytes.Replace(raw, []byte("<bounce"), []byte("<bounce-env"), 1)
	sender := "mallory@evil.test"
	iid, _, err := w.s.ingest(w.ctx, m, raw, w.forwardAddr(m, ""), &sender)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.processInbound(w.ctx, w.wid, iid); err != nil {
		t.Fatal(err)
	}
	if status() != "sent" {
		t.Fatal("a report with a sender in the envelope marked our message bounced")
	}
	// A real report arriving at another mailbox does not touch this mailbox's message.
	w.receive(other, strings.Replace(dsn, "<bounce", "<bounce-other", 1))
	if status() != "sent" {
		t.Fatal("a report at another mailbox marked our message bounced")
	}
	// The real one at the sending mailbox does.
	w.receive(m, dsn)
	if status() != "bounced" {
		t.Fatal("a genuine bounce was not recorded")
	}
}

func TestBounceEnvelopeFromHTTP(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	srv := w.api(nil)
	post := func(raw, envFrom string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/inbound/email?to="+w.forwardAddr(m, ""), strings.NewReader(raw))
		req.Header.Set(InboundSignatureHeader, SignInbound("test", time.Now().Unix(), w.forwardAddr(m, ""), envFrom, []byte(raw)))
		req.Header.Set(EnvelopeFromHeader, envFrom)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	post("From: a@x.test\r\nSubject: s\r\nMessage-ID: <e1"+crypto.RandomString(6)+"@x.test>\r\n\r\nb\r\n", "")
	post("From: a@x.test\r\nSubject: s\r\nMessage-ID: <e2"+crypto.RandomString(6)+"@x.test>\r\n\r\nb\r\n", "a@x.test")
	rows, err := w.d.DB.Pool.Query(w.ctx, `SELECT envelope_from FROM inbound_emails WHERE mailbox_id = $1 ORDER BY received_at, message_id_hdr`, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[*string])
	if err != nil || len(got) != 2 || got[0] == nil || *got[0] != "" || got[1] == nil || *got[1] != "a@x.test" {
		t.Fatalf("envelope_from %v %v", got, err)
	}
}

func TestDedupeNeedsMatchingContent(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	other := w.mailbox(InboundForwarding, OutboundSystem, nil)
	from := customer()
	msgID := "<real" + crypto.RandomString(8) + "@customer.test>"
	mk := func(body string) string {
		return "From: " + from + "\r\nTo: " + m.Email + "\r\nSubject: Help\r\nDate: Sat, 10 Oct 2026 17:00:00 +0000\r\nMessage-ID: " + msgID + "\r\n\r\n" + body + "\r\n"
	}
	// Someone sends first with the id of a message they expect.
	w.receive(m, strings.Replace(mk("decoy"), "From: "+from, "From: mallory@evil.test", 1))
	// The real message still arrives.
	real := w.receive(m, mk("The real question."))
	if st, reason := w.inbound(real); st != "processed" {
		t.Fatalf("real message: %s %s", st, reason)
	}
	got := w.messageFor(real)
	if got.FromEmail != from || !strings.Contains(got.BodyText, "real question") {
		t.Fatalf("message %+v", got)
	}
	// Its redelivery is still the same message, at ingest and at another address.
	if _, dup, err := w.s.ingest(w.ctx, m, []byte(mk("The real question.")), "", nil); err != nil || !dup {
		t.Fatalf("redelivery at ingest: dup=%v err=%v", dup, err)
	}
	again := w.receive(other, mk("The real question."))
	if st, reason := w.inbound(again); st != "ignored" || reason != "duplicate" {
		t.Fatalf("same message at another address: %s %s", st, reason)
	}
}

func TestContentHash(t *testing.T) {
	base := "From: a@x.test\nDate: Sat, 10 Oct 2026 17:00:00 +0000\nSubject: Hi\nMessage-ID: <1@x.test>\n\nLine one\n\nLine two\n"
	same := []string{
		strings.ReplaceAll(base, "\n", "\r\n"),                                          // line endings
		strings.Replace(base, "Line one\n", "Line one  \n", 1) + "\n\n",                 // padding
		"Received: from relay\n" + strings.Replace(base, "<1@x.test>", "<2@x.test>", 1), // transport headers
	}
	for i, v := range same {
		if contentHash([]byte(v)) != contentHash([]byte(base)) {
			t.Errorf("variant %d hashed differently", i)
		}
	}
	for i, v := range []string{
		strings.Replace(base, "Line two", "Line 2", 1),
		strings.Replace(base, "a@x.test", "b@x.test", 1),
		strings.Replace(base, "Subject: Hi", "Subject: Hello", 1),
		strings.Replace(base, "17:00:00", "17:00:01", 1),
		strings.Replace(base, "\n\nLine one", "\n\n\nLine one", 1),
	} {
		if contentHash([]byte(v)) == contentHash([]byte(base)) {
			t.Errorf("change %d hashed the same", i)
		}
	}
}
