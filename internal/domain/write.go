package domain

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// Jobs is the River client; writes enqueue follow-up work inside the same transaction.
type Jobs = *river.Client[pgx.Tx]

// UpsertContactByEmail finds or creates the contact for an email address. A name is
// filled in only when the contact has none; untrusted names are cleaned, not refused.
func UpsertContactByEmail(ctx context.Context, tx pgx.Tx, wid uuid.UUID, email, name string) (uuid.UUID, bool, error) {
	return upsertContactByEmail(ctx, tx, wid, email, name, true)
}

// ImportContactByEmail is UpsertContactByEmail for imported history: a new contact is
// not announced (contracts/imports.md), and a phone is filled in when it has none.
func ImportContactByEmail(ctx context.Context, tx pgx.Tx, wid uuid.UUID, email, name, phone string) (uuid.UUID, bool, error) {
	id, created, err := upsertContactByEmail(ctx, tx, wid, email, name, false)
	if err != nil || strings.TrimSpace(phone) == "" {
		return id, created, err
	}
	_, err = tx.Exec(ctx, `UPDATE contacts SET phone = $3 WHERE workspace_id = $1 AND id = $2 AND phone = ''`,
		wid, id, truncateRunes(strings.TrimSpace(phone), 40))
	return id, created, err
}

func upsertContactByEmail(ctx context.Context, tx pgx.Tx, wid uuid.UUID, email, name string, announce bool) (uuid.UUID, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if name = displayname.Clean(name, displayname.Person); name == "" {
		name = displayname.FromEmail(email)
	}
	var id uuid.UUID
	var created bool
	err := tx.QueryRow(ctx, `INSERT INTO contacts (id, workspace_id, email, name) VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id, email) WHERE email IS NOT NULL
		DO UPDATE SET name = CASE WHEN contacts.name = '' THEN EXCLUDED.name ELSE contacts.name END
		RETURNING id, (xmax = 0)`, ids.New(), wid, email, name).Scan(&id, &created)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("upsert contact: %w", err)
	}
	if created && announce {
		c, err := LoadContact(ctx, tx, wid, id)
		if err != nil {
			return uuid.Nil, false, err
		}
		if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "contact.created", Data: map[string]any{"contact": c}}); err != nil {
			return uuid.Nil, false, err
		}
	}
	return id, created, nil
}

// PhoneDigitsSQL is the digits of contacts.phone that calls match on: a trunk "(0)"
// written after the country code ("+32 (0)470 ...") is dropped before the digits are kept,
// as phone.NormalizeNumber does on the call's side. It is the expression of
// contacts_phone_digits_idx, so the index serves the lookup: change both together.
const PhoneDigitsSQL = `regexp_replace(regexp_replace(phone, '\(0\)', '', 'g'), '[^0-9]', '', 'g')`

// PhoneContact is the contact a phone number belongs to.
type PhoneContact struct {
	ID uuid.UUID
	// Created: the number was new, and the contact was made for it.
	Created bool
	// Blocked: the team blocked the contact; nothing they send reaches a ticket.
	Blocked bool
}

// ContactByPhone finds the contact a phone number belongs to, or creates one named name
// (else the number), and announces it (contracts/phone.md "Contacts"). number is E.164
// ("+3247...") or national digits; contacts match on the number's digits, written with or
// without a 00 prefix. Callers of one number are serialized for the transaction, so two
// calls from a new number make one contact.
func ContactByPhone(ctx context.Context, tx pgx.Tx, wid uuid.UUID, number, name string) (PhoneContact, error) {
	digits := strings.TrimPrefix(number, "+")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return PhoneContact{}, fmt.Errorf("contact by phone: not a number")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "contact-phone:"+wid.String()+":"+digits); err != nil {
		return PhoneContact{}, err
	}
	var found PhoneContact
	err := tx.QueryRow(ctx, `SELECT id, blocked FROM contacts WHERE workspace_id = $1 AND phone <> ''
		AND `+PhoneDigitsSQL+` = ANY($2) ORDER BY updated_at DESC, id LIMIT 1`,
		wid, []string{digits, "00" + digits}).Scan(&found.ID, &found.Blocked)
	if err == nil {
		return found, nil
	}
	if !db.IsNotFound(err) {
		return PhoneContact{}, fmt.Errorf("contact by phone: %w", err)
	}
	if name = displayname.Clean(name, displayname.Person); name == "" {
		name = number
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, workspace_id, name, phone) VALUES ($1, $2, $3, $4)`,
		id, wid, name, truncateRunes(number, 40)); err != nil {
		return PhoneContact{}, fmt.Errorf("create contact: %w", err)
	}
	c, err := LoadContact(ctx, tx, wid, id)
	if err != nil {
		return PhoneContact{}, err
	}
	if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "contact.created", Data: map[string]any{"contact": c}}); err != nil {
		return PhoneContact{}, err
	}
	return PhoneContact{ID: id, Created: true}, nil
}

type NewTicket struct {
	WorkspaceID uuid.UUID
	Subject     string
	Channel     string
	Status      string // default open
	Priority    string // default normal
	Severity    *string
	MailboxID   *uuid.UUID
	ContactID   *uuid.UUID
	AssigneeID  *uuid.UUID
	TeamID      *uuid.UUID
	// RequiredSkillIDs is the route's workspace-scoped expertise requirements.
	RequiredSkillIDs []uuid.UUID
	Custom           map[string]any
	Fields           map[string]any // custom field values, validated by the caller
	// BrandID defaults to the brand of MailboxID, else of WidgetID, else the default brand.
	BrandID  *uuid.UUID
	WidgetID *uuid.UUID
	// OriginMailboxID and ReceivedAt are where the first message arrived (email).
	OriginMailboxID *uuid.UUID
	ReceivedAt      string
	PageURL         string
	// Routed says why the ticket has its team; nil when it has none.
	Routed *TicketRouted
	// Imported writes history (contracts/imports.md): the ticket is dated CreatedAt and
	// published to no one; the importer announces its progress instead.
	Imported  bool
	CreatedAt time.Time
}

// ResolveBrand returns the brand a new ticket belongs to (see NewTicket.BrandID).
func ResolveBrand(ctx context.Context, q db.Querier, wid uuid.UUID, brandID, mailboxID, widgetID *uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT COALESCE(
		(SELECT b.id FROM brands b WHERE b.workspace_id = $1 AND b.id = $2),
		(SELECT m.brand_id FROM mailboxes m WHERE m.workspace_id = $1 AND m.id = $3),
		(SELECT g.brand_id FROM widgets g WHERE g.workspace_id = $1 AND g.id = $4),
		(SELECT b.id FROM brands b WHERE b.workspace_id = $1 AND b.is_default))`, wid, brandID, mailboxID, widgetID).Scan(&id)
	if err != nil {
		return id, fmt.Errorf("resolve brand: %w", err)
	}
	return id, nil
}

// CreateTicket inserts a ticket and publishes ticket.created. Add the first message with
// AddMessage(Trigger: jobs.TriggerTicketCreated) so automations see its body.
func CreateTicket(ctx context.Context, tx pgx.Tx, nt NewTicket) (Ticket, error) {
	if nt.Status == "" {
		nt.Status = StatusOpen
	}
	if nt.Priority == "" {
		nt.Priority = "normal"
	}
	if nt.Custom == nil {
		nt.Custom = map[string]any{}
	}
	if nt.Fields == nil {
		nt.Fields = map[string]any{}
	}
	subject := truncateRunes(strings.TrimSpace(nt.Subject), 500)
	var number int64
	if err := tx.QueryRow(ctx, `UPDATE workspaces SET ticket_seq = ticket_seq + 1 WHERE id = $1 RETURNING ticket_seq`,
		nt.WorkspaceID).Scan(&number); err != nil {
		return Ticket{}, fmt.Errorf("next ticket number: %w", err)
	}
	brandID, err := ResolveBrand(ctx, tx, nt.WorkspaceID, nt.BrandID, nt.MailboxID, nt.WidgetID)
	if err != nil {
		return Ticket{}, err
	}
	if nt.TeamID == nil {
		nt.Routed = nil
	}
	var created *time.Time // now, unless imported history has its own time
	if nt.Imported && !nt.CreatedAt.IsZero() && nt.CreatedAt.Before(time.Now()) {
		at := nt.CreatedAt.UTC()
		created = &at
	}
	id := ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO tickets (id, workspace_id, number, subject, status, priority, channel,
		mailbox_id, contact_id, assignee_id, team_id, custom, fields, severity,
		brand_id, widget_id, origin_mailbox_id, received_at, page_url, routed, created_at, updated_at, last_message_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
			COALESCE($21, now()), COALESCE($21, now()), COALESCE($21, now()))`,
		id, nt.WorkspaceID, number, subject, nt.Status, nt.Priority, nt.Channel, nt.MailboxID, nt.ContactID, nt.AssigneeID, nt.TeamID, nt.Custom, nt.Fields, nt.Severity,
		brandID, nt.WidgetID, nt.OriginMailboxID, strings.ToLower(strings.TrimSpace(nt.ReceivedAt)), truncateRunes(nt.PageURL, 500), nt.Routed, created)
	if err != nil {
		return Ticket{}, fmt.Errorf("insert ticket: %w", err)
	}
	requiredSkills := slices.Clone(nt.RequiredSkillIDs)
	slices.SortFunc(requiredSkills, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	requiredSkills = slices.Compact(requiredSkills)
	if len(requiredSkills) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_skills (workspace_id, ticket_id, skill_id)
			SELECT $1, $2, unnest($3::uuid[])`, nt.WorkspaceID, id, requiredSkills); err != nil {
			return Ticket{}, fmt.Errorf("insert ticket skills: %w", err)
		}
	}
	t, err := LoadTicket(ctx, tx, nt.WorkspaceID, id)
	if err != nil {
		return Ticket{}, err
	}
	if nt.Imported {
		return t, nil
	}
	topics := []string{}
	if t.Assignee != nil {
		topics = append(topics, events.UserTopic(t.Assignee.ID))
	}
	if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: nt.WorkspaceID, Type: "ticket.created", Topics: topics,
		TicketID: &t.ID, Data: map[string]any{"ticket": t}}); err != nil {
		return Ticket{}, err
	}
	return t, nil
}

type NewMessage struct {
	WorkspaceID     uuid.UUID
	TicketID        uuid.UUID
	Kind            string // reply | note
	Direction       string // inbound | outbound
	AuthorType      string // contact | user | system | api
	AuthorUserID    *uuid.UUID
	AuthorContactID *uuid.UUID
	BodyHTML        string // already sanitized by the caller
	BodyText        string
	FullHTML        string // inbound: the full original including quoted history
	Subject         string
	FromEmail       string
	FromName        string
	To, Cc, Bcc     []string
	EmailMessageID  *string
	InReplyTo       string
	References      []string
	InboundEmailID  *uuid.UUID
	// MailboxID is the address the message is sent from (outbound) or arrived at (inbound).
	MailboxID     *uuid.UUID
	AttachmentIDs []uuid.UUID // previously uploaded, unattached attachments to link
	// DeliveryStatus: "pending" queues an email send for outbound replies on email tickets.
	DeliveryStatus string
	// Trigger overrides the job trigger: "" derives it, "none" skips automations.
	Trigger   string
	CreatedAt time.Time
	// AI marks a reply written by the widget AI agent (messages.ai).
	AI bool
	// AIAction is an action card the visitor confirms (contracts/agent.md).
	AIAction *AIAction
	// BotID is the bot an AI reply is sent as (contracts/bots.md), or the bot the API key
	// that writes it speaks as.
	BotID *uuid.UUID
	// APIKeyID is the API key that writes the message, nil for anyone else.
	APIKeyID *uuid.UUID
	// Inbound email (contracts/api.md "Inbound email"). ReplyTo is the Reply-To it named when
	// that differs from FromEmail; AuthFailed marks a From that failed DMARC; NoAutoReply
	// keeps automatic answers away from it; FormSender is the trusted contact form address
	// it came through.
	ReplyTo     string
	AuthFailed  bool
	NoAutoReply bool
	FormSender  string
	// ReceivedAt is the address of ours an inbound email was written to (its mailbox's
	// address or an alias).
	ReceivedAt string
	// NoReopen keeps the ticket's status when an inbound reply would reopen it, and
	// records that it was kept (status_changed with reason "sender_unverified").
	NoReopen bool
	// Imported writes history (contracts/imports.md): dated CreatedAt, nothing sent, no
	// status change, no events, automations or follow-ups. The importer sets the status.
	Imported bool
	// ThreadID puts a note in a third-party thread (contracts/api.md "Threads"). An
	// outbound one with DeliveryStatus "pending" is emailed to the thread's recipients;
	// an inbound one is a reply from them.
	ThreadID *uuid.UUID
	// CollabAccountID sends an outbound email reply from the author's own Collab address
	// (FromEmail) instead of a mailbox, with CollabReplyTo as its Reply-To ("address",
	// "mailbox" or an address; contracts/collab.md "Answering tickets").
	CollabAccountID *uuid.UUID
	CollabReplyTo   string
}

// ErrCannotSend answers an email reply from a mailbox that sends through the platform
// mailer while this instance has that off (MAILBOX_SYSTEM_SEND): it sends nothing until
// it connects Google, Microsoft or SMTP.
var ErrCannotSend = httpx.E(http.StatusUnprocessableEntity, "mailbox_cannot_send",
	"This mailbox has no way to send email yet. Connect Google or Microsoft, or add SMTP, in its settings.")

// checkCanSend refuses a reply from a mailbox that cannot send (ErrCannotSend). A ticket
// without a mailbox is checked when it is sent.
func checkCanSend(ctx context.Context, tx pgx.Tx, wid uuid.UUID, mailbox *uuid.UUID) error {
	if systemSending || mailbox == nil {
		return nil
	}
	var kind string
	err := tx.QueryRow(ctx, `SELECT outbound_kind FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *mailbox).Scan(&kind)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == "system" {
		return ErrCannotSend
	}
	return nil
}

// AddMessage inserts a message, updates the ticket's activity fields, publishes events and
// queues the email send and ticket triggers, all in tx.
func AddMessage(ctx context.Context, tx pgx.Tx, jc Jobs, nm NewMessage) (Message, error) {
	var channel, status string
	var firstResponded *time.Time
	var contactID, ticketMailbox *uuid.UUID
	var brandID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT channel, status, first_responded_at, contact_id, brand_id, mailbox_id FROM tickets
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL FOR UPDATE`, nm.WorkspaceID, nm.TicketID).
		Scan(&channel, &status, &firstResponded, &contactID, &brandID, &ticketMailbox); err != nil {
		return Message{}, fmt.Errorf("lock ticket: %w", err)
	}
	if nm.Imported {
		nm.DeliveryStatus, nm.Trigger = "none", "none"
	}
	if nm.ThreadID != nil && nm.Kind != "note" {
		return Message{}, fmt.Errorf("a thread message is a note")
	}
	sendThread := nm.ThreadID != nil && nm.Direction == "outbound" && nm.DeliveryStatus == "pending"
	if sendThread {
		if err := checkCanSend(ctx, tx, nm.WorkspaceID, nm.MailboxID); err != nil {
			return Message{}, err
		}
	}
	if nm.Kind == "reply" && nm.Direction == "outbound" && channel == ChannelEmail && nm.DeliveryStatus == "pending" && nm.CollabAccountID == nil {
		from := nm.MailboxID
		if from == nil {
			from = ticketMailbox
		}
		if err := checkCanSend(ctx, tx, nm.WorkspaceID, from); err != nil {
			return Message{}, err
		}
	}
	if nm.DeliveryStatus == "" {
		nm.DeliveryStatus = "none"
	}
	if nm.CreatedAt.IsZero() {
		nm.CreatedAt = time.Now().UTC()
	}
	for _, s := range []*[]string{&nm.To, &nm.Cc, &nm.Bcc, &nm.References} {
		if *s == nil {
			*s = []string{}
		}
	}
	id := ids.New()
	_, err := tx.Exec(ctx, `INSERT INTO messages (id, workspace_id, ticket_id, kind, direction, author_type, author_user_id,
		author_contact_id, body_html, body_text, full_html, subject, from_email, from_name, to_emails, cc_emails, bcc_emails,
		email_message_id, in_reply_to, "references", inbound_email_id, delivery_status, created_at, ai, ai_action, mailbox_id,
		reply_to, auth_failed, no_auto_reply, form_sender, bot_id, received_at, thread_id, collab_account_id, collab_reply_to, api_key_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36)`,
		id, nm.WorkspaceID, nm.TicketID, nm.Kind, nm.Direction, nm.AuthorType, nm.AuthorUserID, nm.AuthorContactID,
		nm.BodyHTML, nm.BodyText, nm.FullHTML, truncateRunes(nm.Subject, 500), nm.FromEmail, nm.FromName, nm.To, nm.Cc, nm.Bcc,
		nm.EmailMessageID, nm.InReplyTo, nm.References, nm.InboundEmailID, nm.DeliveryStatus, nm.CreatedAt, nm.AI, nm.AIAction, nm.MailboxID,
		truncateRunes(nm.ReplyTo, 320), nm.AuthFailed, nm.NoAutoReply, truncateRunes(nm.FormSender, 320), nm.BotID,
		truncateRunes(strings.ToLower(strings.TrimSpace(nm.ReceivedAt)), 320), nm.ThreadID, nm.CollabAccountID, truncateRunes(nm.CollabReplyTo, 320), nm.APIKeyID)
	if err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}
	if len(nm.AttachmentIDs) > 0 {
		// An agent's or visitor's upload goes only on their own message.
		if _, err := tx.Exec(ctx, `UPDATE attachments SET message_id = $1 WHERE workspace_id = $2 AND id = ANY($3) AND message_id IS NULL
			AND (uploaded_by IS NULL OR uploaded_by IS NOT DISTINCT FROM $4)
			AND (uploaded_by_contact_id IS NULL OR uploaded_by_contact_id IS NOT DISTINCT FROM $5)`,
			id, nm.WorkspaceID, nm.AttachmentIDs, nm.AuthorUserID, nm.AuthorContactID); err != nil {
			return Message{}, fmt.Errorf("link attachments: %w", err)
		}
	}

	// Ticket bookkeeping. Notes do not move the conversation forward.
	var changes []string
	if nm.Kind == "reply" {
		preview := truncateRunes(strings.Join(strings.Fields(nm.BodyText), " "), 200)
		customer := nm.Direction == "inbound"
		newStatus := status
		kept := false
		if customer && !nm.Imported && (status == StatusPending || status == StatusSolved || status == StatusOnHold) {
			if nm.NoReopen {
				kept = true
			} else {
				newStatus = StatusOpen
			}
		}
		setFirst := !customer && nm.AuthorType == "user" && firstResponded == nil
		_, err := tx.Exec(ctx, `UPDATE tickets SET last_message_at = CASE WHEN $8 THEN GREATEST(last_message_at, $3) ELSE $3 END, last_message_preview = $4,
			message_count = message_count + 1, updated_at = CASE WHEN $8 THEN GREATEST(updated_at, $3) ELSE now() END,
			last_customer_message_at = CASE WHEN $5 THEN $3 ELSE last_customer_message_at END,
			waiting_since = CASE WHEN $5 THEN COALESCE(waiting_since, $3) ELSE NULL END,
			unanswered_count = CASE WHEN $5 THEN unanswered_count + 1 ELSE 0 END,
			status = $6, solved_at = CASE WHEN $6 = 'open' THEN NULL ELSE solved_at END,
			snoozed_until = CASE WHEN $6 = 'on_hold' THEN snoozed_until ELSE NULL END,
			first_responded_at = CASE WHEN $7 THEN $3 ELSE first_responded_at END
			WHERE workspace_id = $1 AND id = $2`, nm.WorkspaceID, nm.TicketID, nm.CreatedAt, preview, customer, newStatus, setFirst, nm.Imported)
		if err != nil {
			return Message{}, fmt.Errorf("update ticket: %w", err)
		}
		if newStatus != status {
			changes = append(changes, "status")
			if _, err := LogActivity(ctx, tx, nm.WorkspaceID, nm.TicketID, "contact", nil, "status_changed",
				map[string]any{"from": status, "to": newStatus}); err != nil {
				return Message{}, err
			}
		}
		if kept {
			// The reply did not reopen the ticket: the timeline says so, and why.
			if _, err := LogActivity(ctx, tx, nm.WorkspaceID, nm.TicketID, "system", nil, "status_changed",
				map[string]any{"from": status, "to": status, "reason": "sender_unverified", "message_id": id.String()}); err != nil {
				return Message{}, err
			}
		}
		if setFirst {
			changes = append(changes, "first_responded_at")
		}
	} else if !nm.Imported {
		if _, err := tx.Exec(ctx, `UPDATE tickets SET updated_at = now() WHERE workspace_id = $1 AND id = $2`, nm.WorkspaceID, nm.TicketID); err != nil {
			return Message{}, err
		}
		if nm.ThreadID != nil {
			// A new message opens the thread again. A reply from the other side means the
			// team has something to act on: a ticket waiting on the customer or on hold is
			// open again. It is not a customer reply, so SLA and response times stay. Mail
			// that failed sender checks is kept but changes nothing, as for a customer's.
			if _, err := tx.Exec(ctx, `UPDATE ticket_threads SET status = 'open', last_message_at = GREATEST(last_message_at, $3), updated_at = now()
				WHERE workspace_id = $1 AND id = $2`, nm.WorkspaceID, *nm.ThreadID, nm.CreatedAt); err != nil {
				return Message{}, fmt.Errorf("update thread: %w", err)
			}
			if nm.Direction == "inbound" && !nm.AuthFailed && (status == StatusPending || status == StatusOnHold) {
				if _, err := tx.Exec(ctx, `UPDATE tickets SET status = 'open', snoozed_until = NULL WHERE workspace_id = $1 AND id = $2`,
					nm.WorkspaceID, nm.TicketID); err != nil {
					return Message{}, err
				}
				changes = append(changes, "status")
				if _, err := LogActivity(ctx, tx, nm.WorkspaceID, nm.TicketID, "system", nil, "status_changed",
					map[string]any{"from": status, "to": StatusOpen, "reason": "thread_replied", "message_id": id.String()}); err != nil {
					return Message{}, err
				}
			}
		}
	}

	msg, err := LoadMessage(ctx, tx, nm.WorkspaceID, id)
	if err != nil {
		return Message{}, err
	}
	if nm.Imported {
		return msg, nil
	}
	ev := events.Event{WorkspaceID: nm.WorkspaceID, Type: "message.created", TicketID: &nm.TicketID,
		Data: map[string]any{"ticket_id": nm.TicketID, "message": msg}}
	if nm.Kind == "reply" && channel == ChannelChat && contactID != nil {
		ev.Visibility = events.VisibilityPublic
		ev.Topics = []string{events.ContactTopic(nm.WorkspaceID, *contactID, brandID)}
		ev.Data = map[string]any{"ticket_id": nm.TicketID, "message": PublicMessage(msg)}
	}
	if _, err := events.Publish(ctx, tx, ev); err != nil {
		return Message{}, err
	}
	// A reply going out ends the ticket's shared draft, whoever or whatever sends it.
	if nm.Kind == "reply" && nm.Direction == "outbound" {
		if err := ClearTicketDraft(ctx, tx, nm.WorkspaceID, nm.TicketID); err != nil {
			return Message{}, err
		}
	}
	if nm.ThreadID != nil {
		if _, err := PublishThread(ctx, tx, nm.WorkspaceID, nm.TicketID, *nm.ThreadID, "thread.updated"); err != nil {
			return Message{}, err
		}
	}
	if len(changes) > 0 || nm.Kind == "reply" {
		if err := PublishTicketUpdated(ctx, tx, nm.WorkspaceID, nm.TicketID, append(changes, "last_message_at")); err != nil {
			return Message{}, err
		}
	}

	if sendThread || nm.Kind == "reply" && nm.Direction == "outbound" && channel == ChannelEmail && nm.DeliveryStatus == "pending" {
		var args river.JobArgs = jobs.SendMessageArgs{WorkspaceID: nm.WorkspaceID, MessageID: id}
		if nm.CollabAccountID != nil && !sendThread {
			// Sent through the member's own account (contracts/collab.md "Answering tickets").
			args = jobs.CollabSendTicketReplyArgs{WorkspaceID: nm.WorkspaceID, MessageID: id}
		}
		if _, err := jc.InsertTx(ctx, tx, args, &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 8}); err != nil {
			return Message{}, fmt.Errorf("queue send: %w", err)
		}
	}
	if nm.Kind == "reply" && nm.Direction == "outbound" && channel == ChannelChat && (nm.AuthorType == "user" || nm.AuthorType == "api") && !nm.AI && contactID != nil {
		if err := queueChatFollowUp(ctx, tx, jc, nm.WorkspaceID, nm.TicketID, id, time.Now()); err != nil {
			return Message{}, fmt.Errorf("queue chat follow-up: %w", err)
		}
	}
	trigger := nm.Trigger
	if trigger == "" {
		switch {
		case nm.Kind == "reply" && nm.Direction == "inbound":
			trigger = jobs.TriggerCustomerReplied
		case nm.Kind == "reply" && nm.AuthorType == "user":
			trigger = jobs.TriggerAgentReplied
		case nm.ThreadID != nil && nm.Direction == "inbound":
			trigger = jobs.TriggerThreadReplied
		default:
			trigger = "none"
		}
	}
	if trigger != "none" {
		if _, err := jc.InsertTx(ctx, tx, jobs.TicketTriggerArgs{WorkspaceID: nm.WorkspaceID, TicketID: nm.TicketID,
			Trigger: trigger, MessageID: &id, ActorUserID: nm.AuthorUserID}, nil); err != nil {
			return Message{}, fmt.Errorf("queue trigger: %w", err)
		}
	}
	return msg, nil
}

// PublishTicketUpdated reloads the ticket and publishes ticket.updated with the changed fields.
func PublishTicketUpdated(ctx context.Context, tx pgx.Tx, wid, ticketID uuid.UUID, changes []string) error {
	t, err := LoadTicket(ctx, tx, wid, ticketID)
	if err != nil {
		return err
	}
	var topics []string
	if t.Assignee != nil {
		topics = append(topics, events.UserTopic(t.Assignee.ID))
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "ticket.updated", Topics: topics,
		TicketID: &t.ID, Data: map[string]any{"ticket": t, "changes": changes}})
	return err
}

// LogActivity records a timeline entry and publishes activity.created.
func LogActivity(ctx context.Context, tx pgx.Tx, wid, ticketID uuid.UUID, actorType string, actorUserID *uuid.UUID,
	typ string, data map[string]any) (Activity, error) {
	if data == nil {
		data = map[string]any{}
	}
	a := Activity{ID: ids.New(), TicketID: ticketID, Actor: ActivityActor{Type: actorType}, Type: typ, Data: data, CreatedAt: time.Now().UTC()}
	if _, err := tx.Exec(ctx, `INSERT INTO activities (id, workspace_id, ticket_id, actor_type, actor_user_id, type, data, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, a.ID, wid, ticketID, actorType, actorUserID, typ, data, a.CreatedAt); err != nil {
		return a, fmt.Errorf("insert activity: %w", err)
	}
	if actorUserID != nil {
		u, err := LoadUserRef(ctx, tx, *actorUserID)
		if err != nil {
			return a, err
		}
		a.Actor.User = &u
	}
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "activity.created", TicketID: &ticketID,
		Data: map[string]any{"ticket_id": ticketID, "activity": a}})
	return a, err
}

// UpdateActivity replaces a timeline entry's data and publishes activity.updated. Only
// entries that follow something still changing are updated: a call's (contracts/phone.md).
func UpdateActivity(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID, data map[string]any) (Activity, error) {
	if data == nil {
		data = map[string]any{}
	}
	a := Activity{ID: id, Data: data}
	var actorUser *uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE activities SET data = $3 WHERE workspace_id = $1 AND id = $2
		RETURNING ticket_id, actor_type, actor_user_id, type, created_at`, wid, id, data).
		Scan(&a.TicketID, &a.Actor.Type, &actorUser, &a.Type, &a.CreatedAt)
	if err != nil {
		return a, fmt.Errorf("update activity: %w", err)
	}
	if actorUser != nil {
		u, err := LoadUserRef(ctx, tx, *actorUser)
		if err != nil {
			return a, err
		}
		a.Actor.User = &u
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "activity.updated", TicketID: &a.TicketID,
		Data: map[string]any{"ticket_id": a.TicketID, "activity": a}})
	return a, err
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// PublishMessageUpdated publishes message.updated for a message in tx. A reply on a chat
// ticket is public and reaches the visitor's widget too, like message.created. A deleted
// message (or a note sent as a reply) goes to the team alone, as its marker, without what it
// said: the visitor's widget no longer has it.
func PublishMessageUpdated(ctx context.Context, tx pgx.Tx, wid, messageID uuid.UUID) error {
	msg, err := LoadMessage(ctx, tx, wid, messageID)
	if err != nil {
		return err
	}
	ev := MessageUpdatedEvent(wid, msg)
	if msg.DeletedAt == nil && msg.Kind == "reply" {
		var channel string
		var contactID *uuid.UUID
		var brandID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT channel, contact_id, brand_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, msg.TicketID).
			Scan(&channel, &contactID, &brandID); err != nil {
			return err
		}
		if channel == ChannelChat && contactID != nil {
			ev.Visibility = events.VisibilityPublic
			ev.Topics = []string{events.ContactTopic(wid, *contactID, brandID)}
			ev.Data = map[string]any{"ticket_id": msg.TicketID, "message": PublicMessage(msg)}
		}
	}
	_, err = events.Publish(ctx, tx, ev)
	return err
}

// DeletedBeforeSent is the delivery error of a reply deleted before it went out: it is
// marked failed then, and sending it skips it. One already being handed to the mailbox goes
// out and is recorded as sent.
const DeletedBeforeSent = "Deleted before it was sent."

// MessageUpdatedEvent is message.updated for the team: a deleted message as its marker
// (Tombstone), never what it said.
func MessageUpdatedEvent(wid uuid.UUID, msg Message) events.Event {
	return events.Event{WorkspaceID: wid, Type: "message.updated", TicketID: &msg.TicketID,
		Data: map[string]any{"ticket_id": msg.TicketID, "message": Tombstone(msg)}}
}

// ClearTicketDraft removes the ticket's shared draft (contracts/api.md "Drafts"), if any, and
// tells everyone it is gone.
func ClearTicketDraft(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM ticket_drafts WHERE workspace_id = $1 AND ticket_id = $2`, wid, tid)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "ticket_draft.updated", TicketID: &tid,
		Data: map[string]any{"ticket_id": tid, "draft": nil}})
	return err
}
