package billing

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stripe/stripe-go/v87"

	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/plan"
)

// registerPlansAPI adds switching a subscription between the paid plans.
func (s *svc) registerPlansAPI(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "change-billing-plan", Method: http.MethodPost, Path: "/v1/billing/change-plan",
		Summary: "Switch the subscription between Pro and Collab", Tags: []string{"Billing"}},
		func(ctx context.Context, in *struct {
			Body struct {
				Plan string `json:"plan" enum:"pro,collab" doc:"collab only when the plan is offered to the workspace"`
			}
		}) (*struct{ Body Billing }, error) {
			if !s.enabled() {
				return nil, errDisabled()
			}
			p, err := requireBillingAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if !s.d.Limiter.Allow("billing:change-plan:"+p.WorkspaceID.String(), 20, time.Hour) {
				return nil, httpx.RateLimited()
			}
			b, err := s.changePlan(ctx, p.WorkspaceID, in.Body.Plan)
			if err != nil {
				return nil, err
			}
			return &struct{ Body Billing }{b}, nil
		})
}

// changePlan swaps the subscription item's price to the target plan's price of the same
// interval, prorated, keeping the seats. The change is stored at once (the webhook that
// follows finds it applied).
func (s *svc) changePlan(ctx context.Context, wid uuid.UUID, target string) (Billing, error) {
	if err := plan.Ensure(ctx, s.d.DB.Pool, wid); err != nil {
		return Billing{}, httpx.Wrap(err)
	}
	var cur struct {
		plan, status, interval, access string
		subID                          *string
		changes                        int
	}
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT plan, status, interval, collab_access, stripe_subscription_id,
			(SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'billing.plan_changed')
		FROM subscriptions WHERE workspace_id = $1`, wid).
		Scan(&cur.plan, &cur.status, &cur.interval, &cur.access, &cur.subID, &cur.changes); err != nil {
		return Billing{}, httpx.Wrap(err)
	}
	from := paidPlan(cur.plan, cur.status)
	if from == "" || cur.subID == nil {
		return Billing{}, httpx.E(409, "no_subscription", "This workspace has no subscription to change. Subscribe first.")
	}
	if from == target {
		return Billing{}, httpx.E(409, "already_on_plan", "This workspace is already on this plan.")
	}
	if target == plan.Collab && !collabOpen(cur.access) {
		return Billing{}, errPlanUnavailable()
	}
	price := s.planPrice(target, cur.interval)
	if price == "" {
		if target == plan.Collab && !s.collabPriced() {
			return Billing{}, errPlanNotPriced()
		}
		return Billing{}, httpx.E(422, "interval_unavailable", "This plan is not available with "+cur.interval+"ly billing.")
	}
	sub, err := s.sc.V1Subscriptions.Retrieve(ctx, *cur.subID, nil)
	if isMissing(err) {
		return Billing{}, httpx.E(409, "no_subscription", "This workspace has no subscription to change. Subscribe first.")
	}
	if err != nil {
		return Billing{}, s.wrapStripe(err)
	}
	if !plan.Paid(string(sub.Status)) {
		return Billing{}, httpx.E(409, "no_subscription", "This workspace has no subscription to change. Subscribe first.")
	}
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return Billing{}, httpx.Wrap(errNoItem)
	}
	item := sub.Items.Data[0]
	if item.Price == nil || item.Price.ID != price {
		params := &stripe.SubscriptionItemUpdateParams{Price: stripe.String(price), Quantity: stripe.Int64(max(item.Quantity, 1)),
			ProrationBehavior: stripe.String("create_prorations")}
		// One key per intended change: a retried or doubled request swaps the price once, and
		// the next change (after this one is recorded) gets a new key.
		params.IdempotencyKey = stripe.String(fmt.Sprintf("od-plan-%s-%s-%d", item.ID, price, cur.changes))
		if _, err := s.sc.V1SubscriptionItems.Update(ctx, item.ID, params); err != nil {
			return Billing{}, s.wrapStripe(err)
		}
		if sub, err = s.sc.V1Subscriptions.Retrieve(ctx, *cur.subID, nil); err != nil {
			return Billing{}, s.wrapStripe(err)
		}
	}
	prices := s.pricesFor(ctx)
	var b Billing
	var after []capture
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		c, err := s.applySubscription(ctx, tx, wid, sub)
		if err != nil {
			return err
		}
		after = c.captures
		if c.changed {
			if err := s.publish(ctx, tx, wid, prices); err != nil {
				return err
			}
		}
		b, err = s.load(ctx, tx, wid, prices)
		return err
	})
	if err != nil {
		return Billing{}, httpx.Wrap(err)
	}
	for _, c := range after {
		s.d.Analytics.Capture(c.wid.String(), c.wid.String(), c.event, c.props)
	}
	return b, nil
}
