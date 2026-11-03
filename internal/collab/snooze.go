package collab

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Snooze (contracts/collab.md "Snooze") works as a ticket's: a conversation of an account
// that tracks handling leaves Needs action until a time, then comes back to it. New mail
// from someone else wakes it sooner; marking it done or needs action ends it. It is kept in
// opendesk only and never changes the provider's message state.

// snoozeWakeBatch bounds the conversations one wake transaction takes.
const snoozeWakeBatch = 500

// snoozeThreads snoozes the conversations until until, in tx: out of Needs action now.
func snoozeThreads(ctx context.Context, tx pgx.Tx, wid uuid.UUID, threadIDs []uuid.UUID, until time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE collab_threads SET snoozed_until = $3, needs_action = false, updated_at = now()
		WHERE workspace_id = $1 AND id = ANY($2)`, wid, threadIDs, until)
	return err
}

// snoozeWakeArgs is the periodic job that wakes conversations whose snooze has passed.
type snoozeWakeArgs struct{}

func (snoozeWakeArgs) Kind() string { return "collab.snooze_wake" }

type snoozeWakeWorker struct {
	river.WorkerDefaults[snoozeWakeArgs]
	s *service
}

func (w *snoozeWakeWorker) Work(ctx context.Context, _ *river.Job[snoozeWakeArgs]) error {
	return w.s.wakeSnoozed(ctx)
}

// wakeSnoozed ends every passed snooze: the conversation is back in Needs action when its
// account still tracks handling, and everyone who sees it hears collab.changed.
func (s *service) wakeSnoozed(ctx context.Context) error {
	for {
		n := 0
		err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `WITH due AS (
					SELECT id FROM collab_threads WHERE snoozed_until IS NOT NULL AND snoozed_until <= now()
					ORDER BY snoozed_until LIMIT $1 FOR UPDATE SKIP LOCKED)
				UPDATE collab_threads t SET snoozed_until = NULL, updated_at = now(),
					needs_action = (a.visibility = 'shared' AND a.handling_enabled)
				FROM due, collab_accounts a WHERE t.id = due.id AND a.id = t.account_id
				RETURNING t.workspace_id, t.account_id, t.id`, snoozeWakeBatch)
			if err != nil {
				return err
			}
			type key struct{ wid, aid uuid.UUID }
			woken := map[key][]uuid.UUID{}
			var order []key
			for rows.Next() {
				var k key
				var tid uuid.UUID
				if err := rows.Scan(&k.wid, &k.aid, &tid); err != nil {
					rows.Close()
					return err
				}
				if _, ok := woken[k]; !ok {
					order = append(order, k)
				}
				woken[k] = append(woken[k], tid)
				n++
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, k := range order {
				if err := s.publishChanged(ctx, tx, k.wid, &k.aid, woken[k], false); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil || n < snoozeWakeBatch {
			return err
		}
	}
}
