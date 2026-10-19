package domain

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/jobs"
)

// ProviderTidyAttempts is how often a provider tidy-up is tried, with River's backoff,
// before it is given up; it never affects the ticket.
const ProviderTidyAttempts = 8

// QueueProviderArchive archives a ticket's emails at Gmail or Microsoft when it is solved
// or closed (contracts/api.md "Reading the inbox directly"): only when one of its emails
// came in through a support mailbox that reads its inbox through the provider's API and
// has provider_archive_on_solve on. Call it in the transaction that changes the status.
func QueueProviderArchive(ctx context.Context, tx pgx.Tx, jc Jobs, wid, ticketID uuid.UUID) error {
	var due bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages ms
		JOIN mailboxes mb ON mb.workspace_id = ms.workspace_id AND mb.id::text = ms.provider_ref->>'mailbox_id'
		WHERE ms.workspace_id = $1 AND ms.ticket_id = $2 AND ms.direction = 'inbound' AND ms.provider_ref IS NOT NULL
		AND mb.config->>'oauth_grant' = 'api' AND mb.config->>'provider_archive_on_solve' = 'true')`, wid, ticketID).Scan(&due)
	if err != nil || !due {
		return err
	}
	_, err = jc.InsertTx(ctx, tx, jobs.ProviderTidyArgs{WorkspaceID: wid, TicketID: ticketID, Action: jobs.ProviderArchive},
		&river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: ProviderTidyAttempts})
	return err
}
