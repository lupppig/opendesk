package billing

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/stripe/stripe-go/v87"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
)

// syncSeatsWorker sets the Stripe quantity of an active subscription to the billed seats.
type syncSeatsWorker struct {
	river.WorkerDefaults[jobs.SyncSeatsArgs]
	s *svc
}

func (w *syncSeatsWorker) Work(ctx context.Context, job *river.Job[jobs.SyncSeatsArgs]) error {
	s, wid := w.s, job.Args.WorkspaceID
	if !s.enabled() {
		return nil
	}
	var subID *string
	var planName, status string
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT stripe_subscription_id, plan, status FROM subscriptions WHERE workspace_id = $1`, wid).
		Scan(&subID, &planName, &status)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if subID == nil || paidPlan(planName, status) == "" {
		return nil
	}
	// Members can change while Stripe is called; check again until the count holds.
	for range 3 {
		billed, err := plan.BilledSeats(ctx, s.d.DB.Pool, wid)
		if err != nil {
			return err
		}
		sub, err := s.sc.V1Subscriptions.Retrieve(ctx, *subID, nil)
		if isMissing(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("retrieve subscription: %s", stripeErr(err))
		}
		if !plan.Paid(string(sub.Status)) {
			return nil
		}
		if sub.Items == nil || len(sub.Items.Data) == 0 {
			return errNoItem
		}
		item := sub.Items.Data[0]
		if int(item.Quantity) == billed {
			return nil
		}
		params := &stripe.SubscriptionItemUpdateParams{Quantity: stripe.Int64(int64(billed)),
			ProrationBehavior: stripe.String("create_prorations")}
		if _, err := s.sc.V1SubscriptionItems.Update(ctx, item.ID, params); err != nil {
			return fmt.Errorf("update subscription quantity: %s", stripeErr(err))
		}
		prices := s.pricesFor(ctx)
		if err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE subscriptions SET seats = $3, updated_at = now() WHERE workspace_id = $1 AND stripe_subscription_id = $2`,
				wid, *subID, billed)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			if err := audit.Log(ctx, tx, nil, wid, "billing.seats_synced", "workspace", &wid,
				map[string]any{"from": item.Quantity, "to": billed}); err != nil {
				return err
			}
			return s.publish(ctx, tx, wid, prices)
		}); err != nil {
			return err
		}
	}
	return nil
}

// reconcileWorker queues a seat sync for every paid workspace and prunes old webhook ids.
type reconcileWorker struct {
	river.WorkerDefaults[jobs.ReconcileBillingArgs]
	s *svc
}

func (w *reconcileWorker) Work(ctx context.Context, _ *river.Job[jobs.ReconcileBillingArgs]) error {
	s := w.s
	if !s.enabled() {
		return nil
	}
	// Stripe retries an event for up to three days; a month of ids is plenty.
	if _, err := s.d.DB.Pool.Exec(ctx, `DELETE FROM stripe_events WHERE received_at < now() - interval '30 days'`); err != nil {
		return err
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT workspace_id FROM subscriptions
		WHERE plan IN ('pro', 'collab') AND status IN ('active', 'trialing', 'past_due') AND stripe_subscription_id IS NOT NULL`)
	if err != nil {
		return err
	}
	wids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil || len(wids) == 0 {
		return err
	}
	params := make([]river.InsertManyParams, 0, len(wids))
	for _, id := range wids {
		params = append(params, river.InsertManyParams{Args: jobs.SyncSeatsArgs{WorkspaceID: id}, InsertOpts: seatSyncOpts()})
	}
	_, err = s.d.Jobs.InsertMany(ctx, params)
	return err
}

// cancelWorker cancels a deleted workspace's subscriptions immediately.
type cancelWorker struct {
	river.WorkerDefaults[jobs.CancelSubscriptionArgs]
	s *svc
}

func (w *cancelWorker) Work(ctx context.Context, job *river.Job[jobs.CancelSubscriptionArgs]) error {
	s, a := w.s, job.Args
	if !s.enabled() {
		return nil
	}
	ids := map[string]bool{}
	if a.SubscriptionID != "" {
		ids[a.SubscriptionID] = true
	}
	if a.CustomerID != "" {
		// Also catch a subscription that started while the workspace was being deleted.
		for sub, err := range s.sc.V1Subscriptions.List(ctx, &stripe.SubscriptionListParams{Customer: stripe.String(a.CustomerID)}).All(ctx) {
			if err != nil {
				if isMissing(err) {
					break
				}
				return fmt.Errorf("list subscriptions: %s", stripeErr(err))
			}
			ids[sub.ID] = true
		}
	}
	for id := range ids {
		sub, err := s.sc.V1Subscriptions.Retrieve(ctx, id, nil)
		if isMissing(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("retrieve subscription: %s", stripeErr(err))
		}
		if !liveStatus(string(sub.Status)) {
			continue
		}
		if _, err := s.sc.V1Subscriptions.Cancel(ctx, id, nil); err != nil && !isMissing(err) {
			return fmt.Errorf("cancel subscription: %s", stripeErr(err))
		}
		s.d.Log.Info("subscription canceled for a deleted workspace", "workspace_id", a.WorkspaceID)
	}
	return nil
}
