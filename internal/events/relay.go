package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/jobs"
)

// Relay moves committed events from the outbox to NATS and queues webhook dispatch.
// Several relays can run at once; rows are claimed with SKIP LOCKED.
type Relay struct {
	Pool *pgxpool.Pool // direct (unpooled) connection: LISTEN needs a session
	NATS *nats.Conn
	Jobs *river.Client[pgx.Tx]
	Log  *slog.Logger
}

func (r *Relay) Run(ctx context.Context) {
	wake := make(chan struct{}, 1)
	go r.listen(ctx, wake)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		for {
			n, err := r.flush(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				r.Log.Error("event relay", "err", err)
				break
			}
			if n == 0 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-tick.C:
		}
	}
}

func (r *Relay) listen(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		conn, err := r.Pool.Acquire(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if _, err = conn.Exec(ctx, "LISTEN od_events"); err == nil {
			for {
				if _, err = conn.Conn().WaitForNotification(ctx); err != nil {
					break
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
		conn.Release()
		if ctx.Err() == nil {
			time.Sleep(time.Second)
		}
	}
}

func (r *Relay) flush(ctx context.Context) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, r.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, seq, workspace_id, type, visibility, topics, data, created_at FROM events
			WHERE published_at IS NULL ORDER BY created_at, seq LIMIT 200 FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		batch, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Envelope, error) {
			var e Envelope
			err := row.Scan(&e.ID, &e.Seq, &e.WorkspaceID, &e.Type, &e.Visibility, &e.Topics, &e.Data, &e.OccurredAt)
			return e, err
		})
		if err != nil || len(batch) == 0 {
			return err
		}
		mirrored, err := supportChannelWorkspaces(ctx, tx, batch)
		if err != nil {
			return err
		}
		params := make([]river.InsertManyParams, 0, len(batch))
		for _, e := range batch {
			payload, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if err := r.NATS.Publish(SubjectPrefix+e.WorkspaceID.String(), payload); err != nil {
				return err
			}
			params = append(params, river.InsertManyParams{
				Args:       jobs.WebhookDispatchArgs{WorkspaceID: e.WorkspaceID, EventSeq: e.Seq},
				InsertOpts: &river.InsertOpts{Queue: jobs.QueueWebhooks},
			})
			if supportChannelEvent(e.Type) && mirrored[e.WorkspaceID] {
				params = append(params, river.InsertManyParams{Args: jobs.SupportChannelDispatchArgs{
					WorkspaceID: e.WorkspaceID, EventSeq: e.Seq,
				}})
			}
			if _, err := tx.Exec(ctx, `UPDATE events SET published_at = now() WHERE workspace_id = $1 AND seq = $2`, e.WorkspaceID, e.Seq); err != nil {
				return err
			}
		}
		if err := r.NATS.Flush(); err != nil {
			return err
		}
		if _, err := r.Jobs.InsertManyTx(ctx, tx, params); err != nil {
			return err
		}
		n = len(batch)
		return nil
	})
	return n, err
}

// supportChannelWorkspaces returns the workspaces in batch that mirror tickets to a
// support channel (contracts/support-channels.md), so only they get dispatch jobs.
// It runs one indexed query per batch, and none when the batch has no ticket events.
func supportChannelWorkspaces(ctx context.Context, tx pgx.Tx, batch []Envelope) (map[uuid.UUID]bool, error) {
	var wids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, e := range batch {
		if supportChannelEvent(e.Type) && !seen[e.WorkspaceID] {
			seen[e.WorkspaceID] = true
			wids = append(wids, e.WorkspaceID)
		}
	}
	out := map[uuid.UUID]bool{}
	if len(wids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT workspace_id FROM support_channel_destinations WHERE workspace_id = ANY($1) AND enabled`, wids)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	for _, wid := range found {
		out[wid] = true
	}
	return out, nil
}

func supportChannelEvent(typ string) bool {
	switch typ {
	case "ticket.created", "ticket.updated", "message.created", "message.updated":
		return true
	default:
		return false
	}
}

// Disconnect asks the gateway to close a user's sockets (all, or one session's). Until
// they expire, the gateway also refuses that user's (or session's) ws tokens issued at or
// before the time the message carries (contracts/realtime.md "Connect").
func Disconnect(nc *nats.Conn, userID string, sessionID string) {
	_ = nc.Publish(DisconnectSubject, disconnectPayload(userID, sessionID, time.Now()))
}

// disconnectPayload is the od.control.disconnect body: `at` is the revocation time in ms.
func disconnectPayload(userID, sessionID string, at time.Time) []byte {
	payload, _ := json.Marshal(struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
		At        int64  `json:"at"`
	}{userID, sessionID, at.UnixMilli()})
	return payload
}

// LiveSubjectPrefix + workspace id is the NATS subject for live previews: short-lived
// events that are never stored or replayed (contracts/realtime.md "Live events").
const LiveSubjectPrefix = "od.live."

// LiveEvent is a live preview about one chat ticket, pushed by the gateway to the
// visitor's contact topic and to agents watching the ticket. TeamID is the ticket's
// private team: the gateway pushes the preview only to agents whose token lists it.
type LiveEvent struct {
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	TicketID    uuid.UUID  `json:"ticket_id"`
	ContactID   uuid.UUID  `json:"contact_id"`
	TeamID      *uuid.UUID `json:"team_id,omitempty"`
	// BrandID is the chat's brand: the visitor's widget sockets of another brand never get it.
	BrandID *uuid.UUID `json:"brand_id,omitempty"`
	Type    string     `json:"type"`
	Data    any        `json:"data"`
}

// PrivateTeam returns the ticket's team when that team is private, else nil.
func PrivateTeam(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, wid, ticketID uuid.UUID) (*uuid.UUID, error) {
	var team *uuid.UUID
	err := q.QueryRow(ctx, `SELECT tm.id FROM tickets t JOIN teams tm ON tm.id = t.team_id AND tm.private
		WHERE t.workspace_id = $1 AND t.id = $2`, wid, ticketID).Scan(&team)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return team, err
}

// PublishLive sends a live preview. It is best effort: a missed preview is replaced by
// the stored event that follows it.
func PublishLive(nc *nats.Conn, ev LiveEvent) {
	if nc == nil {
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = nc.Publish(LiveSubjectPrefix+ev.WorkspaceID.String(), payload)
}
