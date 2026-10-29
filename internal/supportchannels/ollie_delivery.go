package supportchannels

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
)

type ollieDelivery struct {
	WorkspaceID, ConnectionID       uuid.UUID
	ChannelID, ThreadID, Provider   string
	CredentialsEnc                  []byte
	IncludeNotes, SummaryPosted     bool
	Personality, Length, SLAWarning string
	SentimentWarning                string
	Ticket                          domain.Ticket
}

// loadOllieDelivery applies the same eligibility to the model snapshot and delivery.
// With lock, every eligibility row stays stable through the provider call. NOWAIT
// avoids waiting behind writers that lock ticket, team and workspace in different
// orders; the caller rolls back and retries contention without making a post.
func loadOllieDelivery(ctx context.Context, q db.Querier, destinationID, ticketID uuid.UUID, summary, lock bool) (ollieDelivery, error) {
	var out ollieDelivery
	var teamID *uuid.UUID
	query := `SELECT h.workspace_id,c.id,COALESCE(h.external_channel_id,''),COALESCE(h.external_thread_id,''),c.provider,c.credentials_enc,
		d.include_notes,h.summary_posted_at IS NOT NULL,d.ollie_personality,d.ollie_length,h.sla_warning,h.sentiment_warning,k.team_id
		FROM support_channel_threads h
		JOIN tickets k ON k.workspace_id=h.workspace_id AND k.id=h.ticket_id
		JOIN support_channel_destinations d ON d.workspace_id=h.workspace_id AND d.id=h.destination_id
		JOIN support_channel_connections c ON c.workspace_id=h.workspace_id AND c.id=d.connection_id AND c.id=h.connection_id
		JOIN workspaces w ON w.id=h.workspace_id
		WHERE h.destination_id=$1 AND h.ticket_id=$2 AND d.enabled AND w.suspended_at IS NULL
		AND k.deleted_at IS NULL AND k.channel='email'
		AND (d.team_id IS NULL OR d.team_id=k.team_id)
		AND CASE WHEN $3 THEN d.ollie_summary ELSE d.ollie_warnings END`
	if lock {
		query += ` FOR SHARE OF k,d,c,w NOWAIT FOR UPDATE OF h NOWAIT`
	}
	err := q.QueryRow(ctx, query, destinationID, ticketID, summary).Scan(&out.WorkspaceID, &out.ConnectionID,
		&out.ChannelID, &out.ThreadID, &out.Provider, &out.CredentialsEnc, &out.IncludeNotes,
		&out.SummaryPosted, &out.Personality, &out.Length, &out.SLAWarning, &out.SentimentWarning, &teamID)
	if err != nil {
		return out, err
	}
	if out.ThreadID == "" {
		return out, errors.New("support channel thread is being created")
	}
	if teamID != nil {
		var private bool
		query := `SELECT private FROM teams WHERE workspace_id=$1 AND id=$2`
		if lock {
			query += ` FOR SHARE NOWAIT`
		}
		if err := q.QueryRow(ctx, query, out.WorkspaceID, *teamID).Scan(&private); err != nil {
			return out, err
		}
		if private {
			return out, pgx.ErrNoRows
		}
	}
	out.Ticket, err = domain.LoadTicket(ctx, q, out.WorkspaceID, ticketID)
	return out, err
}

func deliveryBusy(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "55P03"
}

// summarySnapshot ends before calling Ollie, so model latency holds no row locks.
func (s *Service) summarySnapshot(ctx context.Context, destinationID, ticketID uuid.UUID) (ollieDelivery, []domain.Message, error) {
	var d ollieDelivery
	var messages []domain.Message
	err := pgx.BeginTxFunc(ctx, s.d.DB.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		d, err = loadOllieDelivery(ctx, tx, destinationID, ticketID, true, false)
		if err != nil {
			return err
		}
		messages, err = domain.ListMessages(ctx, tx, d.WorkspaceID, ticketID, !d.IncludeNotes)
		return err
	})
	return d, messages, err
}

func ticketSLAWarning(t domain.Ticket, now time.Time) (warning, text string) {
	if t.SLA == nil || t.Status == domain.StatusSolved || t.Status == domain.StatusClosed {
		return "", ""
	}
	kind, due := "resolution", t.SLA.ResolutionDueAt
	if t.FirstRespondedAt == nil && t.SLA.FirstResponseDueAt != nil {
		kind, due = "first_response", t.SLA.FirstResponseDueAt
	}
	if due == nil || !due.Before(now.Add(slaWarningLead)) {
		return "", ""
	}
	stage := "due_soon"
	if !due.After(now) {
		stage = "breached"
	}
	return kind + ":" + stage + ":" + strconv.FormatInt(due.Unix(), 10), slaWarningText(kind, stage, *due, now)
}
