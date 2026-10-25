package email

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
)

// TicketMail is what Collab needs from ticket email (contracts/collab.md "Answering
// tickets"): a reply on a ticket ready to go out from a member's own address, its outcome
// recorded on the ticket, and an answer to it taken into the ticket like an email to the
// ticket's reply address.
type TicketMail struct{ s *service }

// NewTicketMail is the ticket email of the instance d.
func NewTicketMail(d *deps.Deps) *TicketMail { return &TicketMail{s: newService(d)} }

// ErrReplySettled: the reply is no longer waiting to go out (sent, failed, deleted or gone).
var ErrReplySettled = errNotSendable

// SendFailure is a refusal to send a reply that trying again does not change, and what the
// message's delivery_error says.
type SendFailure struct{ Msg string }

func (e *SendFailure) Error() string { return e.Msg }

// PersonalReply is an email reply of a ticket that goes out from a Collab account.
type PersonalReply struct {
	TicketID  uuid.UUID
	Number    int64
	AuthorID  *uuid.UUID
	AccountID uuid.UUID
	// FromEmail is the account's address asked for; ReplyTo the Reply-To asked for
	// ("address", "mailbox" or an address).
	FromEmail string
	ReplyTo   string
	// MailboxAddress is the ticket's reply address (its alias when the ticket was written to
	// one), "" when it has none.
	MailboxAddress string
	To, Cc, Bcc    []string
	Subject        string
	// MessageID is ours (the ticket format at FromEmail's domain), with brackets.
	MessageID  string
	InReplyTo  string
	References []string
	// HTML and Text are the reply as written, without a signature or layout.
	HTML, Text  string
	Attachments []OutAttachment
	// TicketMessageIDs are the Message-IDs of the ticket's emails, newest first, without
	// brackets: the conversation at the account they are in.
	TicketMessageIDs []string
}

// PersonalReply loads reply mid ready to send. ErrReplySettled when it is not waiting to
// go out; a *SendFailure when it cannot go.
func (t *TicketMail) PersonalReply(ctx context.Context, wid, mid uuid.UUID) (*PersonalReply, error) {
	s := t.s
	var r PersonalReply
	var kind, direction, status, bodyHTML, bodyText string
	var account *uuid.UUID
	var thread *uuid.UUID
	var deleted bool
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT ticket_id, kind, direction, delivery_status, body_html, body_text, to_emails, cc_emails, bcc_emails,
		author_user_id, collab_account_id, collab_reply_to, from_email, thread_id, deleted_at IS NOT NULL
		FROM messages WHERE workspace_id = $1 AND id = $2`, wid, mid).
		Scan(&r.TicketID, &kind, &direction, &status, &bodyHTML, &bodyText, &r.To, &r.Cc, &r.Bcc,
			&r.AuthorID, &account, &r.ReplyTo, &r.FromEmail, &thread, &deleted)
	if db.IsNotFound(err) {
		return nil, ErrReplySettled
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" || direction != "outbound" || kind != "reply" || thread != nil {
		return nil, ErrReplySettled
	}
	if deleted {
		if err := s.recordDelivery(ctx, wid, mid, r.TicketID, "failed", domain.DeletedBeforeSent, "", nil); err != nil {
			return nil, err
		}
		return nil, ErrReplySettled
	}
	if account == nil {
		return nil, &SendFailure{"The mail account this reply was to go from was removed."}
	}
	r.AccountID = *account
	ti, err := loadTicketInfo(ctx, s.d.DB.Pool, wid, r.TicketID)
	if db.IsNotFound(err) {
		return nil, &SendFailure{"The ticket no longer exists."}
	}
	if err != nil {
		return nil, err
	}
	r.Number = ti.number
	if len(dedupe(r.To)) == 0 {
		if ti.contactEmail == nil || *ti.contactEmail == "" {
			return nil, &SendFailure{"The customer has no email address."}
		}
		r.To = []string{*ti.contactEmail}
	}
	if r.To, err = replyDestination(ctx, s.d.DB.Pool, wid, r.TicketID, ti.contactEmail, r.To); err != nil {
		return nil, err
	}
	if m, err := s.sendingMailbox(ctx, wid, ti, nil); err == nil {
		r.MailboxAddress = strings.ToLower(firstNonEmpty(m.alias(ti.receivedAt), m.Email))
	} else if !isPermanent(err) {
		return nil, err
	}
	if r.InReplyTo, r.References, err = threadHeaders(ctx, s.d.DB.Pool, wid, r.TicketID, mid); err != nil {
		return nil, err
	}
	base := baseSubject(ti.subject)
	if base == "" {
		base = "Your request"
	}
	r.Subject = "Re: " + base
	domainPart := emailDomain(r.FromEmail)
	if domainPart == "" {
		domainPart = "opendesk.local"
	}
	r.MessageID = "<m_" + mid.String() + ".t" + itoa(ti.number) + "@" + domainPart + ">"
	// Recorded first so a fast answer threads even before the send returns.
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE messages SET email_message_id = $3 WHERE workspace_id = $1 AND id = $2 AND email_message_id IS NULL`,
		wid, mid, r.MessageID); err != nil && !db.IsUniqueViolation(err, "") {
		return nil, err
	}
	atts, html, err := s.outboundAttachments(ctx, wid, mid, bodyHTML)
	if err != nil {
		var se *sendError
		if errors.As(err, &se) && se.permanent {
			return nil, &SendFailure{se.msg}
		}
		return nil, err
	}
	r.HTML, r.Attachments = html, atts
	r.Text = bodyText
	if strings.TrimSpace(r.Text) == "" {
		r.Text = htmlToText(bodyHTML)
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT email_message_id FROM messages WHERE workspace_id = $1 AND ticket_id = $2
		AND email_message_id IS NOT NULL AND id <> $3 AND thread_id IS NULL ORDER BY created_at DESC LIMIT 50`, wid, r.TicketID, mid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id = strings.Trim(strings.TrimSpace(id), "<>"); id != "" {
			r.TicketMessageIDs = append(r.TicketMessageIDs, id)
		}
	}
	return &r, rows.Err()
}

func isPermanent(err error) bool {
	var se *sendError
	return errors.As(err, &se) && se.permanent
}

// StillSendable reports whether reply mid is still waiting to go out and not deleted.
func (t *TicketMail) StillSendable(ctx context.Context, wid, mid uuid.UUID) (bool, error) {
	return stillSendable(ctx, t.s.d.DB.Pool, wid, mid)
}

// RecordSent records that reply mid went out as fromName <fromEmail> to to, with the
// Message-ID stamped and html, the email as it went out, and publishes the change.
func (t *TicketMail) RecordSent(ctx context.Context, wid, mid, ticketID uuid.UUID, stamped, fromEmail, fromName string, to []string, html string) error {
	return t.s.recordDelivery(ctx, wid, mid, ticketID, "sent", "", stamped,
		&sentAs{fromEmail: strings.ToLower(fromEmail), fromName: fromName, to: dedupe(to), html: html})
}

// RecordFailed records that reply mid could not go out, with the reason shown on it.
func (t *TicketMail) RecordFailed(ctx context.Context, wid, mid, ticketID uuid.UUID, reason string) error {
	return t.s.recordDelivery(ctx, wid, mid, ticketID, "failed", reason, "", nil)
}

// TakeIn takes raw, an email that answers a reply of the ticket sent from a member's own
// address, into the ticket as an email to its reply address (contracts/api.md "Inbound
// email"): it threads by its headers, and a copy the ticket's address received too is a
// duplicate. A ticket with no address to receive at takes nothing.
func (t *TicketMail) TakeIn(ctx context.Context, wid, ticketID uuid.UUID, raw []byte) error {
	s := t.s
	ti, err := loadTicketInfo(ctx, s.d.DB.Pool, wid, ticketID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	m, err := s.sendingMailbox(ctx, wid, ti, nil)
	if isPermanent(err) {
		s.log.Info("ticket answer not taken in: no address", "workspace_id", wid, "ticket_id", ticketID)
		return nil
	}
	if err != nil {
		return err
	}
	_, _, err = s.ingest(ctx, m, raw, "", nil, viaMember)
	return err
}
