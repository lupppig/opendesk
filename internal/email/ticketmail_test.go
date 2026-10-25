package email

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/ids"
)

// collabAccount inserts a bare Collab account of the admin's, for replies sent from it.
func (w *world) collabAccount() (uuid.UUID, string) {
	w.t.Helper()
	id, addr := ids.New(), "adam-"+crypto.RandomString(5)+"@acme.test"
	mustExec(w.t, w.d, `INSERT INTO collab_accounts (id, workspace_id, owner_id, provider, email, name, display_name, visibility, credentials_enc, status)
		VALUES ($1, $2, $3, 'google', $4, $4, 'Adam', 'private', '\x00', 'active')`, id, w.wid, w.uid, addr)
	return id, addr
}

func TestPersonalReplyAndAnswer(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox("forwarding", "system", nil)
	in := w.receive(m, "From: Ada <ada@customer.test>\nTo: "+m.Email+"\nSubject: Where is my order? [#9]\nMessage-ID: <q-1@customer.test>\n\nIt has not arrived.\n")
	first := w.messageFor(in)
	account, addr := w.collabAccount()

	var mid uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: first.TicketID, Kind: "reply",
			Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, BodyHTML: "<p>On it</p>", BodyText: "On it",
			To: []string{"ada@customer.test"}, FromEmail: addr, FromName: "Adam", DeliveryStatus: "pending",
			CollabAccountID: &account, CollabReplyTo: "mailbox"})
		mid = msg.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.jobCount("collab.send_ticket_reply", "message_id", mid.String()); n != 1 {
		t.Fatalf("collab send jobs: %d", n)
	}
	if n := w.jobCount("email.send_message", "message_id", mid.String()); n != 0 {
		t.Fatalf("mailbox send jobs: %d", n)
	}

	tm := &TicketMail{s: w.s}
	r, err := tm.PersonalReply(w.ctx, w.wid, mid)
	if err != nil {
		t.Fatal(err)
	}
	tk := w.ticket(first.TicketID)
	wantID := "<m_" + mid.String() + ".t" + itoa(tk.Number) + "@acme.test>"
	switch {
	case r.AccountID != account || r.FromEmail != addr || r.ReplyTo != "mailbox":
		t.Fatalf("from %+v", r)
	case r.MailboxAddress != strings.ToLower(m.Email):
		t.Fatalf("mailbox address %q", r.MailboxAddress)
	case r.Subject != "Re: Where is my order?":
		t.Fatalf("subject %q", r.Subject)
	case r.MessageID != wantID:
		t.Fatalf("message id %q", r.MessageID)
	case r.InReplyTo != "<q-1@customer.test>":
		t.Fatalf("in-reply-to %q", r.InReplyTo)
	case len(r.To) != 1 || r.To[0] != "ada@customer.test":
		t.Fatalf("to %v", r.To)
	}
	if err := tm.RecordSent(w.ctx, w.wid, mid, r.TicketID, r.MessageID, addr, "Adam", r.To); err != nil {
		t.Fatal(err)
	}
	sent := w.message(mid)
	if sent.Delivery != "sent" || sent.FromEmail != addr || sent.MsgID == nil || *sent.MsgID != wantID {
		t.Fatalf("recorded %+v", sent)
	}
	var mailbox *uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT mailbox_id FROM messages WHERE id = $1`, mid).Scan(&mailbox); err != nil || mailbox != nil {
		t.Fatalf("a reply from the account has no mailbox: %v %v", mailbox, err)
	}
	if _, err := tm.PersonalReply(w.ctx, w.wid, mid); !errors.Is(err, ErrReplySettled) {
		t.Fatalf("sent twice: %v", err)
	}

	// Ada answers the account; the ticket takes it in, once.
	answer := []byte(strings.ReplaceAll("From: Ada <ada@customer.test>\nTo: "+addr+"\nSubject: Re: Where is my order?\n"+
		"Message-ID: <a-1@customer.test>\nIn-Reply-To: "+wantID+"\n\nThanks, it arrived.\n", "\n", "\r\n"))
	for range 2 {
		if err := tm.TakeIn(w.ctx, w.wid, r.TicketID, answer); err != nil {
			t.Fatal(err)
		}
	}
	var inbound uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id FROM inbound_emails WHERE mailbox_id = $1 AND message_id_hdr = '<a-1@customer.test>'`, m.ID).Scan(&inbound); err != nil {
		t.Fatal(err)
	}
	if err := w.s.processInbound(w.ctx, w.wid, inbound); err != nil {
		t.Fatal(err)
	}
	got := w.messageFor(inbound)
	if got.TicketID != r.TicketID || got.Direction != "inbound" || !strings.Contains(got.BodyText, "it arrived") {
		t.Fatalf("answer %+v", got)
	}
	if n := w.count(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND direction = 'inbound'`, r.TicketID); n != 2 {
		t.Fatalf("inbound messages: %d", n)
	}
	// The same email reaching the team's address too (a Cc, the Reply-To) is a duplicate.
	if _, dup, err := w.s.ingest(w.ctx, m, append([]byte("Delivered-To: "+m.Email+"\r\n"), answer...), w.forwardAddr(m, ""), nil, viaForward); err != nil || !dup {
		t.Fatalf("copy at the mailbox: dup %v, %v", dup, err)
	}
}

func TestPersonalReplyAccountRemoved(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox("forwarding", "system", nil)
	in := w.receive(m, "From: Ada <ada@customer.test>\nTo: "+m.Email+"\nSubject: Refund\nMessage-ID: <q-2@customer.test>\n\nPlease.\n")
	first := w.messageFor(in)
	account, addr := w.collabAccount()
	var mid uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: first.TicketID, Kind: "reply",
			Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, BodyHTML: "<p>Done</p>", BodyText: "Done",
			FromEmail: addr, DeliveryStatus: "pending", CollabAccountID: &account})
		mid = msg.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, w.d, `DELETE FROM collab_accounts WHERE id = $1`, account)
	var sf *SendFailure
	if _, err := (&TicketMail{s: w.s}).PersonalReply(w.ctx, w.wid, mid); !errors.As(err, &sf) {
		t.Fatalf("removed account: %v", err)
	}
}

func TestMailboxPersonalReplies(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox("forwarding", "system", nil)
	srv := w.api(w.admin)
	path := srv.URL + "/v1/mailboxes/" + m.ID.String()
	var out Mailbox
	if st := doJSON(t, "PATCH", path, map[string]any{"personal_replies": map[string]any{"enabled": false, "reply_to": "mailbox", "accounts": "domain"}}, &out); st != 200 {
		t.Fatalf("patch: %d", st)
	}
	pr := out.PersonalReplies
	if pr.Enabled == nil || *pr.Enabled || pr.ReplyTo == nil || *pr.ReplyTo != "mailbox" || pr.Accounts != PersonalDomain {
		t.Fatalf("saved %+v", pr)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.updated' AND data ? 'personal_replies'`, w.wid); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	// Back to the workspace's choices.
	if st := doJSON(t, "PATCH", path, map[string]any{"personal_replies": map[string]any{"enabled": nil, "reply_to": nil}}, &out); st != 200 {
		t.Fatalf("reset: %d", st)
	}
	if pr := out.PersonalReplies; pr.Enabled != nil || pr.ReplyTo != nil || pr.Accounts != PersonalAny {
		t.Fatalf("reset %+v", pr)
	}
	for _, bad := range []map[string]any{{"accounts": "everyone"}, {"reply_to": "customer"}} {
		if st := doJSON(t, "PATCH", path, map[string]any{"personal_replies": bad}, nil); st != 422 {
			t.Fatalf("%v: %d", bad, st)
		}
	}
}
