package email

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/jobs"
)

type chatFollowUpWorker struct {
	river.WorkerDefaults[jobs.ChatFollowUpArgs]
	s *service
}

func (w *chatFollowUpWorker) Work(ctx context.Context, job *river.Job[jobs.ChatFollowUpArgs]) error {
	return w.s.chatFollowUp(ctx, job.Args)
}

// chatFollowUp turns an agent's chat reply into an email when the visitor has not been
// back since it was posted (contracts/teams.md "Chat follow-up"). The message is queued
// like an email reply: it goes out from the ticket's reply address, else the brand's
// address for its team, else the brand's default address, with the address's From name
// format and signature, and threads with the follow-ups before it. Without such an
// address nothing is sent.
func (s *service) chatFollowUp(ctx context.Context, a jobs.ChatFollowUpArgs) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var kind, direction, authorType, status string
		var ai bool
		var created time.Time
		var ticketMailbox, team *uuid.UUID
		var brand uuid.UUID
		var deleted bool
		err := tx.QueryRow(ctx, `SELECT m.kind, m.direction, m.author_type, m.ai, m.delivery_status, m.created_at, m.deleted_at IS NOT NULL,
			t.mailbox_id, t.team_id, t.brand_id
			FROM messages m JOIN tickets t ON t.id = m.ticket_id AND t.workspace_id = m.workspace_id
			WHERE m.workspace_id = $1 AND m.id = $2 AND m.ticket_id = $3 AND t.deleted_at IS NULL FOR UPDATE OF m`,
			a.WorkspaceID, a.MessageID, a.TicketID).
			Scan(&kind, &direction, &authorType, &ai, &status, &created, &deleted, &ticketMailbox, &team, &brand)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if kind != "reply" || direction != "outbound" || (authorType != "user" && authorType != "api") || ai || status != "none" || deleted {
			return nil
		}
		due, err := domain.ChatFollowUpDue(ctx, tx, a.WorkspaceID, a.TicketID, created)
		if err != nil || !due {
			return err
		}
		mailbox, err := followUpMailbox(ctx, tx, a.WorkspaceID, ticketMailbox, brand, team)
		if err != nil || mailbox == nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE messages SET delivery_status = 'pending', mailbox_id = $3
			WHERE workspace_id = $1 AND id = $2 AND delivery_status = 'none'`, a.WorkspaceID, a.MessageID, *mailbox); err != nil {
			return err
		}
		_, err = s.d.Jobs.InsertTx(ctx, tx, jobs.SendMessageArgs{WorkspaceID: a.WorkspaceID, MessageID: a.MessageID},
			&river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 8})
		return err
	})
}

// followUpMailbox is the address a chat follow-up goes out from: the ticket's reply
// address, else the brand's address whose default team is the ticket's, else the brand's
// default address that is not another private team's; nil when the brand has none.
func followUpMailbox(ctx context.Context, q db.Querier, wid uuid.UUID, ticketMailbox *uuid.UUID, brand uuid.UUID, team *uuid.UUID) (*uuid.UUID, error) {
	if ticketMailbox != nil {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1 AND id = $2 AND status <> 'disabled')`,
			wid, *ticketMailbox).Scan(&ok); err != nil {
			return nil, err
		}
		if ok {
			return ticketMailbox, nil
		}
	}
	if team != nil {
		id, err := domain.TeamReplyMailbox(ctx, q, wid, brand, *team)
		if err != nil || id != nil {
			return id, err
		}
	}
	return domain.BrandMailboxFor(ctx, q, wid, brand, team)
}
