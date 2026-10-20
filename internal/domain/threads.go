package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/events"
)

// Thread statuses.
const (
	ThreadOpen   = "open"
	ThreadClosed = "closed"
)

// Thread is a third-party thread (contracts/api.md "Threads"): an email conversation on a
// ticket with someone outside the team. Its messages are notes with ThreadID set.
type Thread struct {
	ID        uuid.UUID  `json:"id"`
	TicketID  uuid.UUID  `json:"ticket_id"`
	Subject   string     `json:"subject"`
	To        []string   `json:"to"`
	Cc        []string   `json:"cc"`
	MailboxID *uuid.UUID `json:"mailbox_id"`
	Status    string     `json:"status" enum:"open,closed"`
	// IncludeHistory: the first message quoted the ticket's conversation with the customer.
	IncludeHistory bool      `json:"include_history"`
	CreatedBy      *UserRef  `json:"created_by"`
	MessageCount   int       `json:"message_count"`
	LastMessageAt  time.Time `json:"last_message_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const threadSelect = `SELECT th.id, th.ticket_id, th.subject, th.to_emails, th.cc_emails, th.mailbox_id, th.status, th.include_history,
	u.id, u.name, u.email, u.avatar_url,
	(SELECT count(*) FROM messages m WHERE m.thread_id = th.id),
	th.last_message_at, th.created_at, th.updated_at
FROM ticket_threads th LEFT JOIN users u ON u.id = th.created_by`

func scanThread(row pgx.Row) (Thread, error) {
	var t Thread
	var uID *uuid.UUID
	var uName, uEmail, uAvatar *string
	if err := row.Scan(&t.ID, &t.TicketID, &t.Subject, &t.To, &t.Cc, &t.MailboxID, &t.Status, &t.IncludeHistory,
		&uID, &uName, &uEmail, &uAvatar, &t.MessageCount, &t.LastMessageAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	if uID != nil {
		t.CreatedBy = &UserRef{ID: *uID, Name: deref(uName), Email: deref(uEmail), AvatarURL: Avatar(deref(uAvatar))}
	}
	if t.To == nil {
		t.To = []string{}
	}
	if t.Cc == nil {
		t.Cc = []string{}
	}
	return t, nil
}

// LoadThread loads one of a ticket's threads.
func LoadThread(ctx context.Context, q db.Querier, wid, ticketID, id uuid.UUID) (Thread, error) {
	return scanThread(q.QueryRow(ctx, threadSelect+` WHERE th.workspace_id = $1 AND th.ticket_id = $2 AND th.id = $3`, wid, ticketID, id))
}

// ListThreads returns a ticket's threads, oldest first.
func ListThreads(ctx context.Context, q db.Querier, wid, ticketID uuid.UUID) ([]Thread, error) {
	rows, err := q.Query(ctx, threadSelect+` WHERE th.workspace_id = $1 AND th.ticket_id = $2 ORDER BY th.created_at, th.id`, wid, ticketID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Thread, error) { return scanThread(r) })
	if out == nil {
		out = []Thread{}
	}
	return out, err
}

// PublishThread reloads a thread and publishes thread.created or thread.updated.
func PublishThread(ctx context.Context, tx pgx.Tx, wid, ticketID, id uuid.UUID, typ string) (Thread, error) {
	th, err := LoadThread(ctx, tx, wid, ticketID, id)
	if err != nil {
		return th, err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, TicketID: &ticketID,
		Data: map[string]any{"ticket_id": ticketID, "thread": th}})
	return th, err
}
