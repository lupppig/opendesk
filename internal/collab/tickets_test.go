package collab

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// emailTicket opens an email ticket from ada@customer.test with her first email on it and
// returns it with that email's Message-ID.
func (w *world) emailTicket(subject string) (domain.Ticket, string) {
	w.t.Helper()
	var t domain.Ticket
	first := "<first-" + ids.New().String() + "@customer.test>"
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		contact, _, err := domain.UpsertContactByEmail(w.ctx, tx, w.wid, "ada@customer.test", "Ada Customer")
		if err != nil {
			return err
		}
		if t, err = domain.CreateTicket(w.ctx, tx, domain.NewTicket{WorkspaceID: w.wid, Subject: subject, Channel: domain.ChannelEmail,
			ContactID: &contact}); err != nil {
			return err
		}
		_, err = domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: t.ID, Kind: "reply",
			Direction: "inbound", AuthorType: "contact", AuthorContactID: &contact, BodyHTML: "<p>Help</p>", BodyText: "Help",
			Subject: subject, FromEmail: "ada@customer.test", EmailMessageID: &first, Trigger: "none"})
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return t, first
}

// personalReply adds a pending reply by p from the account's address, as the reply API does.
func (w *world) personalReply(p *auth.Principal, ticket uuid.UUID, from TicketFrom) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		m, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: ticket, Kind: "reply",
			Direction: "outbound", AuthorType: "user", AuthorUserID: &p.UserID, BodyHTML: "<p>On it</p>", BodyText: "On it",
			To: []string{"ada@customer.test"}, FromEmail: from.Email, FromName: from.Name, DeliveryStatus: "pending",
			CollabAccountID: &from.AccountID, CollabReplyTo: from.ReplyTo})
		id = m.ID
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func statusOf(err error) int {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.GetStatus()
	}
	if errors.Is(err, auth.ErrForbidden) {
		return http.StatusForbidden
	}
	return 0
}

func TestTicketSenderChecks(t *testing.T) {
	w := newWorld(t)
	ticket, _ := w.emailTicket("Where is my order?")
	mine := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})

	from, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if from.Email != strings.ToLower(mine.Email) || from.ReplyTo != ReplyToAddress || from.Name != "Box" {
		t.Fatalf("default sender: %+v", from)
	}
	if from, err = TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, mine.Email, "Help@Acme.test"); err != nil || from.ReplyTo != "help@acme.test" {
		t.Fatalf("custom reply-to: %+v %v", from, err)
	}
	if _, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, "", "not an address"); statusOf(err) != http.StatusUnprocessableEntity {
		t.Fatalf("bad reply-to: %v", err)
	}
	if _, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, "someone@else.test", ""); statusOf(err) != http.StatusUnprocessableEntity {
		t.Fatalf("an address of nobody's: %v", err)
	}
	// A private account is its owner's alone, admins included.
	for _, p := range []*auth.Principal{w.agent2, w.owner} {
		if _, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, p, ticket.ID, mine.ID, "", ""); statusOf(err) != http.StatusNotFound {
			t.Fatalf("someone else's private account: %v", err)
		}
	}
	if _, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.viewer, ticket.ID, mine.ID, "", ""); statusOf(err) != http.StatusForbidden {
		t.Fatalf("viewer: %v", err)
	}
	ollie := *w.agent
	ollie.ViaAssistant = true
	if _, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, &ollie, ticket.ID, mine.ID, "", ""); statusOf(err) != http.StatusForbidden {
		t.Fatalf("assistant: %v", err)
	}
	// A shared account works for its audience.
	shared := w.account(w.owner, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	if _, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent2, ticket.ID, shared.ID, "", ""); err != nil {
		t.Fatalf("shared account: %v", err)
	}
	mustExec(t, w.d, `UPDATE workspaces SET settings = settings || '{"collab_ticket_replies": false, "collab_ticket_reply_to": "mailbox"}' WHERE id = $1`, w.wid)
	if _, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, "", ""); !errors.Is(err, ErrTicketRepliesOff) {
		t.Fatalf("setting off: %v", err)
	}
	mustExec(t, w.d, `UPDATE workspaces SET settings = settings || '{"collab_ticket_replies": true}' WHERE id = $1`, w.wid)
	if from, err = TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, "", ""); err != nil || from.ReplyTo != ReplyToMailbox {
		t.Fatalf("workspace reply-to: %+v %v", from, err)
	}
}

func TestTicketReplySentFromAccount(t *testing.T) {
	w := newWorld(t)
	fake := &fakeProvider{}
	w.useFake(fake)
	ticket, first := w.emailTicket("Where is my order?")
	mine := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	mustExec(t, w.d, `UPDATE collab_accounts SET signature_html = '<p>Adam, Acme</p>' WHERE id = $1`, mine.ID)

	from, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, "", "help@acme.test")
	if err != nil {
		t.Fatal(err)
	}
	mid := w.personalReply(w.agent, ticket.ID, from)
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.send_ticket_reply' AND args->>'message_id' = $1`, mid.String()); n != 1 {
		t.Fatalf("collab send jobs: %d", n)
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'email.send_message' AND args->>'message_id' = $1`, mid.String()); n != 0 {
		t.Fatalf("mailbox send jobs: %d", n)
	}
	if err := w.s.sendTicketReply(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.sent) != 1 {
		t.Fatalf("sent %d", len(fake.sent))
	}
	pm, err := email.ParseMail(fake.sent[0])
	if err != nil {
		t.Fatal(err)
	}
	wantID := "<m_" + mid.String() + ".t" + itoa(ticket.Number) + "@acme.test>"
	switch {
	case !strings.EqualFold(pm.FromAddr, mine.Email):
		t.Fatalf("from %q", pm.FromAddr)
	case pm.Subject != "Re: Where is my order?":
		t.Fatalf("subject %q", pm.Subject)
	case pm.MessageID != wantID:
		t.Fatalf("message id %q, want %q", pm.MessageID, wantID)
	case pm.InReplyTo != first:
		t.Fatalf("in-reply-to %q", pm.InReplyTo)
	case !strings.Contains(string(fake.sent[0]), "Reply-To: help@acme.test") && !strings.Contains(string(fake.sent[0]), "Reply-To: <help@acme.test>"):
		t.Fatalf("no reply-to in %s", fake.sent[0])
	case !strings.Contains(pm.HTML, "Adam, Acme"):
		t.Fatalf("no signature in %s", pm.HTML)
	}
	var status, fromEmail string
	var mailbox, account *uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT delivery_status, from_email, mailbox_id, collab_account_id FROM messages WHERE id = $1`, mid).
		Scan(&status, &fromEmail, &mailbox, &account); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || !strings.EqualFold(fromEmail, mine.Email) || mailbox != nil || account == nil || *account != mine.ID {
		t.Fatalf("recorded %s %s %v %v", status, fromEmail, mailbox, account)
	}
	msg, err := domain.LoadMessage(w.ctx, w.d.DB.Pool, w.wid, mid)
	if err != nil || msg.FromAccount == nil || msg.FromAccount.ReplyTo != "help@acme.test" || domain.PublicMessage(msg).FromAccount != nil {
		t.Fatalf("from_account: %+v %v", msg.FromAccount, err)
	}
	// In the account's Sent, in a conversation linked to the ticket.
	var thread uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT t.id FROM collab_messages m JOIN collab_threads t ON t.id = m.thread_id
		WHERE m.account_id = $1 AND m.message_id = $2 AND t.ticket_id = $3`, mine.ID, strings.Trim(wantID, "<>"), ticket.ID).Scan(&thread); err != nil {
		t.Fatalf("stored and linked: %v", err)
	}
	if tk, err := threadTicket(w.ctx, w.d.DB.Pool, w.agent, thread); err != nil || tk == nil || tk.Number != ticket.Number {
		t.Fatalf("thread ticket: %+v %v", tk, err)
	}

	// The customer answers the address: the sync hands the answer to the ticket.
	answer := "From: Ada Customer <ada@customer.test>\r\nTo: " + mine.Email + "\r\nSubject: Re: Where is my order?\r\n" +
		"Message-ID: <answer-1@customer.test>\r\nIn-Reply-To: " + wantID + "\r\nReferences: " + first + " " + wantID + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\nContent-Type: text/plain\r\n\r\nThanks, it arrived.\r\n"
	p, err := w.s.newPass(w.ctx, w.d.DB.Pool, mine)
	if err != nil {
		t.Fatal(err)
	}
	inbox := w.folder(mine, RoleInbox)
	var stored uuid.UUID
	if err := p.batch(w.ctx, func(tx pgx.Tx) error {
		var err error
		stored, err = p.insert(w.ctx, tx, &newMessage{RemoteID: "answer-1", Raw: []byte(answer), State: state{Unread: true, FolderIDs: []uuid.UUID{inbox.ID}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.ticket_answer' AND args->>'message_id' = $1 AND args->>'ticket_id' = $2`,
		stored.String(), ticket.ID.String()); n != 1 {
		t.Fatalf("answer jobs: %d", n)
	}
	// Mail the account sent itself that names the reply is not an answer.
	own := strings.Replace(strings.Replace(answer, "Ada Customer <ada@customer.test>", mine.Email, 1), "answer-1@", "own-1@", 1)
	if err := p.batch(w.ctx, func(tx pgx.Tx) error {
		_, err := p.insert(w.ctx, tx, &newMessage{RemoteID: "own-1", Raw: []byte(own)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.ticket_answer' AND args->>'ticket_id' = $1`, ticket.ID.String()); n != 1 {
		t.Fatalf("answer jobs after own mail: %d", n)
	}
}

func TestTicketReplyFailsWithoutAccount(t *testing.T) {
	w := newWorld(t)
	w.useFake(&fakeProvider{})
	ticket, _ := w.emailTicket("Refund")
	mine := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	from, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, mine.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Shared with the agent, then taken away before it went out.
	shared := w.account(w.owner, ProviderGoogle, VisibilityShared, Audience{UserIDs: []uuid.UUID{w.agent.UserID}})
	sharedFrom, err := TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, shared.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	gone := w.personalReply(w.agent, ticket.ID, from)
	unshared := w.personalReply(w.agent, ticket.ID, sharedFrom)
	mustExec(t, w.d, `DELETE FROM collab_accounts WHERE id = $1`, mine.ID)
	mustExec(t, w.d, `DELETE FROM collab_shares WHERE account_id = $1`, shared.ID)
	for _, mid := range []uuid.UUID{gone, unshared} {
		if err := w.s.sendTicketReply(w.ctx, w.wid, mid, false); err != nil {
			t.Fatal(err)
		}
		var status, reason string
		if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT delivery_status, delivery_error FROM messages WHERE id = $1`, mid).Scan(&status, &reason); err != nil {
			t.Fatal(err)
		}
		if status != "failed" || reason == "" {
			t.Fatalf("reply %s: %s %q", mid, status, reason)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// replyAddress adds a support address at domain with the given personal_replies and makes it the
// ticket's reply address.
func (w *world) replyAddress(ticket uuid.UUID, domain, rules string) uuid.UUID {
	w.t.Helper()
	id := ids.New()
	var brand uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id FROM brands WHERE workspace_id = $1 AND is_default`, w.wid).Scan(&brand); err != nil {
		w.t.Fatal(err)
	}
	mustExec(w.t, w.d, `INSERT INTO mailboxes (id, workspace_id, name, email, display_name, inbound_kind, forwarding_token, outbound_kind, brand_id, personal_replies)
		VALUES ($1, $2, 'Support', $3, 'Acme Support', 'forwarding', $4, 'system', $5, $6)`, id, w.wid, "support-"+id.String()[24:]+"@"+domain,
		"fw-"+id.String(), brand, rules)
	mustExec(w.t, w.d, `UPDATE tickets SET mailbox_id = $2 WHERE id = $1`, ticket, id)
	return id
}

func TestTicketRulesPerMailbox(t *testing.T) {
	w := newWorld(t)
	w.useFake(&fakeProvider{})
	ticket, _ := w.emailTicket("Shipping")
	mine := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	shared := w.account(w.owner, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	send := func(a *accountRow) (TicketFrom, error) {
		return TicketSender(w.ctx, w.d, w.d.DB.Pool, w.agent, ticket.ID, a.ID, "", "")
	}

	// Off for this address, though on for the workspace.
	mb := w.replyAddress(ticket.ID, "acme.test", `{"enabled": false}`)
	if _, err := send(mine); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "Acme Support") {
		t.Fatalf("off for the address: %v", err)
	}
	// On for this address, though off for the workspace, answering to the address by default.
	mustExec(t, w.d, `UPDATE workspaces SET settings = settings || '{"collab_ticket_replies": false}' WHERE id = $1`, w.wid)
	mustExec(t, w.d, `UPDATE mailboxes SET personal_replies = '{"enabled": true, "reply_to": "mailbox"}' WHERE id = $1`, mb)
	from, err := send(mine)
	if err != nil || from.ReplyTo != ReplyToMailbox {
		t.Fatalf("on for the address: %+v %v", from, err)
	}
	// Shared accounts only.
	mustExec(t, w.d, `UPDATE mailboxes SET personal_replies = '{"enabled": true, "accounts": "shared"}' WHERE id = $1`, mb)
	if _, err := send(mine); statusOf(err) != http.StatusUnprocessableEntity {
		t.Fatalf("private account where shared only: %v", err)
	}
	if _, err := send(shared); err != nil {
		t.Fatalf("shared account: %v", err)
	}
	// Addresses at the address's own domains only.
	mustExec(t, w.d, `UPDATE mailboxes SET personal_replies = '{"enabled": true, "accounts": "domain"}' WHERE id = $1`, mb)
	if _, err := send(mine); err != nil {
		t.Fatalf("same domain: %v", err)
	}
	other := w.replyAddress(ticket.ID, "elsewhere.test", `{"enabled": true, "accounts": "domain"}`)
	if _, err := send(mine); statusOf(err) != http.StatusUnprocessableEntity || !strings.Contains(err.Error(), "elsewhere.test") {
		t.Fatalf("another domain: %v", err)
	}

	// The rules are the ticket's current address's, checked again when it goes out.
	mustExec(t, w.d, `UPDATE tickets SET mailbox_id = $2 WHERE id = $1`, ticket.ID, mb)
	from, err = send(mine)
	if err != nil {
		t.Fatal(err)
	}
	mid := w.personalReply(w.agent, ticket.ID, from)
	mustExec(t, w.d, `UPDATE tickets SET mailbox_id = $2 WHERE id = $1`, ticket.ID, other)
	if err := w.s.sendTicketReply(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT delivery_status, delivery_error FROM messages WHERE id = $1`, mid).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(reason, "elsewhere.test") {
		t.Fatalf("moved to an address that refuses it: %s %q", status, reason)
	}
}
