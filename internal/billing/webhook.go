package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
)

const (
	webhookMaxBody   = 1 << 20
	webhookTolerance = 5 * time.Minute
)

func writeProblem(w http.ResponseWriter, e *httpx.Error) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}

// webhook receives Stripe events. It needs no session: the Stripe-Signature header is
// the credential. Only the event id and type are ever logged.
func (s *svc) webhook(w http.ResponseWriter, r *http.Request) {
	if !s.enabled() {
		writeProblem(w, errDisabled())
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookMaxBody))
	if err != nil {
		writeProblem(w, httpx.E(http.StatusRequestEntityTooLarge, "payload_too_large", "The event body is too large."))
		return
	}
	ev, err := webhook.ConstructEventWithOptions(payload, r.Header.Get("Stripe-Signature"), s.d.Cfg.StripeWebhookSecret,
		webhook.ConstructEventOptions{Tolerance: webhookTolerance, IgnoreAPIVersionMismatch: true})
	if err != nil {
		s.d.Log.Warn("stripe webhook refused", "reason", "invalid signature", "request_id", httpx.RequestIDFrom(r.Context()))
		writeProblem(w, httpx.E(http.StatusBadRequest, "invalid_signature", "The Stripe signature is missing or invalid."))
		return
	}
	if err := s.handleEvent(r.Context(), &ev); err != nil {
		s.d.Log.Error("stripe webhook failed", "event_id", ev.ID, "type", string(ev.Type), "err", err)
		writeProblem(w, httpx.E(http.StatusInternalServerError, "internal", "The event could not be applied; Stripe will retry."))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"received":true}`))
}

// capture is an analytics event sent after the transaction commits.
type capture struct {
	wid   uuid.UUID
	event string
	props map[string]any
}

// handleEvent applies one event exactly once: the stripe_events row is written in the
// same transaction as its effects, so a replay or a concurrent delivery is a no-op.
func (s *svc) handleEvent(ctx context.Context, ev *stripe.Event) error {
	var seen bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stripe_events WHERE id = $1)`, ev.ID).Scan(&seen); err != nil {
		return err
	}
	if seen {
		return nil
	}
	in, err := s.prepare(ctx, ev)
	if err != nil {
		return err
	}
	prices := s.pricesFor(ctx)
	var after []capture
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		after = nil
		tag, err := tx.Exec(ctx, `INSERT INTO stripe_events (id, type) VALUES ($1, $2) ON CONFLICT DO NOTHING`, ev.ID, string(ev.Type))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if in == nil {
			return nil
		}
		after, err = s.apply(ctx, tx, ev, in, prices)
		return err
	})
	if err != nil {
		return err
	}
	for _, c := range after {
		s.d.Analytics.Capture(c.wid.String(), c.wid.String(), c.event, c.props)
	}
	if in != nil && in.session != nil {
		// Off-session charges (auto-recharge) and the portal use the customer's default.
		s.ensureDefaultPM(ctx, in.customerID, in.setupPM)
	}
	return nil
}

// eventInput is what an event needs, read from the payload and from Stripe before the
// transaction opens.
type eventInput struct {
	metaWID    string
	customerID string
	sub        *stripe.Subscription // fresh from Stripe
	session    *stripe.CheckoutSession
	invoice    *stripe.Invoice
	charge     *stripe.Invoice // an invoice for a charge on the saved payment method (metadata.charge_id)
	setupPM    string          // the payment method a setup Checkout saved
	syncPM     bool            // the customer's payment methods may have changed
}

func (s *svc) prepare(ctx context.Context, ev *stripe.Event) (*eventInput, error) {
	in := &eventInput{}
	switch ev.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		var cs stripe.CheckoutSession
		if err := json.Unmarshal(ev.Data.Raw, &cs); err != nil {
			return nil, fmt.Errorf("decode checkout session: %w", err)
		}
		in.session = &cs
		in.metaWID = cs.Metadata["workspace_id"]
		if in.metaWID == "" {
			in.metaWID = cs.ClientReferenceID
		}
		if cs.Customer != nil {
			in.customerID = cs.Customer.ID
		}
		in.syncPM = true
		if cs.Mode == stripe.CheckoutSessionModeSubscription && cs.Subscription != nil && cs.Subscription.ID != "" {
			sub, err := s.fetchSubscription(ctx, cs.Subscription.ID, nil)
			if err != nil {
				return nil, err
			}
			in.sub = sub
		}
		if cs.Mode == stripe.CheckoutSessionModeSetup && cs.SetupIntent != nil && cs.SetupIntent.ID != "" {
			si, err := s.sc.V1SetupIntents.Retrieve(ctx, cs.SetupIntent.ID, nil)
			if err != nil {
				return nil, fmt.Errorf("retrieve setup intent: %s", stripeErr(err))
			}
			if si.PaymentMethod != nil {
				in.setupPM = si.PaymentMethod.ID
			}
		}
	case "customer.updated":
		var c stripe.Customer
		if err := json.Unmarshal(ev.Data.Raw, &c); err != nil {
			return nil, fmt.Errorf("decode customer: %w", err)
		}
		in.customerID, in.metaWID, in.syncPM = c.ID, c.Metadata["workspace_id"], true
	case "payment_method.attached", "payment_method.detached", "payment_method.updated", "payment_method.automatically_updated":
		var pm stripe.PaymentMethod
		if err := json.Unmarshal(ev.Data.Raw, &pm); err != nil {
			return nil, fmt.Errorf("decode payment method: %w", err)
		}
		if pm.Customer != nil {
			in.customerID = pm.Customer.ID
		} else if prev, ok := ev.Data.PreviousAttributes["customer"].(string); ok {
			// Detached: the customer it belonged to.
			in.customerID = prev
		}
		if in.customerID == "" {
			return nil, nil
		}
		in.syncPM = true
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		var sub stripe.Subscription
		if err := json.Unmarshal(ev.Data.Raw, &sub); err != nil {
			return nil, fmt.Errorf("decode subscription: %w", err)
		}
		fresh, err := s.fetchSubscription(ctx, sub.ID, &sub)
		if err != nil {
			return nil, err
		}
		in.sub = fresh
	case "invoice.paid", "invoice.payment_failed":
		var inv stripe.Invoice
		if err := json.Unmarshal(ev.Data.Raw, &inv); err != nil {
			return nil, fmt.Errorf("decode invoice: %w", err)
		}
		in.invoice = &inv
		if inv.Customer != nil {
			in.customerID = inv.Customer.ID
		}
		if inv.Metadata["charge_id"] != "" {
			// A top-up or auto-recharge charged by billing.
			in.charge, in.metaWID = &inv, inv.Metadata["workspace_id"]
			break
		}
		subID, meta := invoiceSubscription(&inv, ev.Data.Raw)
		in.metaWID = meta["workspace_id"]
		if subID != "" {
			sub, err := s.fetchSubscription(ctx, subID, nil)
			if err != nil {
				return nil, err
			}
			in.sub = sub
		}
	default:
		return nil, nil
	}
	if in.sub != nil {
		if in.metaWID == "" {
			in.metaWID = in.sub.Metadata["workspace_id"]
		}
		if in.customerID == "" && in.sub.Customer != nil {
			in.customerID = in.sub.Customer.ID
		}
	}
	return in, nil
}

// fetchSubscription re-reads a subscription from Stripe, so events applied out of order
// still store the latest state. A subscription Stripe no longer has falls back to the
// payload.
func (s *svc) fetchSubscription(ctx context.Context, id string, payload *stripe.Subscription) (*stripe.Subscription, error) {
	sub, err := s.sc.V1Subscriptions.Retrieve(ctx, id, nil)
	if err != nil {
		if isMissing(err) && payload != nil {
			return payload, nil
		}
		return nil, fmt.Errorf("retrieve subscription: %s", stripeErr(err))
	}
	return sub, nil
}

// invoiceSubscription reads the invoice's subscription id and metadata, in the current
// shape (parent.subscription_details) or the older top-level one.
func invoiceSubscription(inv *stripe.Invoice, raw json.RawMessage) (string, map[string]string) {
	if inv.Parent != nil && inv.Parent.SubscriptionDetails != nil {
		d := inv.Parent.SubscriptionDetails
		if d.Subscription != nil && d.Subscription.ID != "" {
			return d.Subscription.ID, d.Metadata
		}
	}
	var legacy struct {
		Subscription *stripe.Subscription `json:"subscription"`
	}
	if json.Unmarshal(raw, &legacy) == nil && legacy.Subscription != nil {
		return legacy.Subscription.ID, nil
	}
	return "", nil
}

// resolveWorkspace finds the workspace an event belongs to: metadata.workspace_id first,
// then the Stripe customer or subscription on record.
func resolveWorkspace(ctx context.Context, q db.Querier, in *eventInput) (uuid.UUID, bool, error) {
	if id, err := uuid.Parse(in.metaWID); err == nil {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workspaces WHERE id = $1)`, id).Scan(&ok); err != nil || ok {
			return id, ok, err
		}
	}
	var wid uuid.UUID
	var subID string
	if in.sub != nil {
		subID = in.sub.ID
	}
	err := q.QueryRow(ctx, `SELECT workspace_id FROM subscriptions WHERE ($1 <> '' AND stripe_customer_id = $1)
		OR ($2 <> '' AND stripe_subscription_id = $2) LIMIT 1`, in.customerID, subID).Scan(&wid)
	if db.IsNotFound(err) {
		return uuid.Nil, false, nil
	}
	return wid, err == nil, err
}

func (s *svc) apply(ctx context.Context, tx pgx.Tx, ev *stripe.Event, in *eventInput, prices Prices) ([]capture, error) {
	wid, ok, err := resolveWorkspace(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	if !ok {
		// A subscription that outlived its workspace (checkout finished after deletion).
		if id, err := uuid.Parse(in.metaWID); err == nil && in.sub != nil && liveStatus(string(in.sub.Status)) {
			_, err := s.d.Jobs.InsertTx(ctx, tx, jobs.CancelSubscriptionArgs{WorkspaceID: id, SubscriptionID: in.sub.ID},
				&river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 20})
			return nil, err
		}
		s.d.Log.Info("stripe event without a workspace", "event_id", ev.ID, "type", string(ev.Type))
		return nil, nil
	}
	if err := plan.Ensure(ctx, tx, wid); err != nil {
		return nil, err
	}
	if in.customerID != "" {
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET stripe_customer_id = $2 WHERE workspace_id = $1 AND stripe_customer_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM subscriptions WHERE stripe_customer_id = $2)`, wid, in.customerID); err != nil {
			return nil, err
		}
	}
	var out []capture
	changed := false
	if in.sub != nil {
		c, err := s.applySubscription(ctx, tx, wid, in.sub)
		if err != nil {
			return nil, err
		}
		changed = c.changed
		out = append(out, c.captures...)
	}
	if cs := in.session; cs != nil && cs.Mode == stripe.CheckoutSessionModePayment && cs.Metadata["kind"] == topUpKind &&
		cs.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid {
		usage, err := checkoutUSD(cs.Metadata)
		if err != nil {
			return nil, fmt.Errorf("checkout session %s: %w", cs.ID, err)
		}
		granted, err := plan.Grant(ctx, tx, wid, usage, "stripe_checkout", cs.ID)
		if err != nil {
			return nil, err
		}
		if granted {
			changed = true
			if err := rearmNotices(ctx, tx, wid); err != nil {
				return nil, err
			}
			data := map[string]any{"usd": usage.USD()}
			if err := audit.Log(ctx, tx, nil, wid, "billing.ai_top_up_purchased", "workspace", &wid, data); err != nil {
				return nil, err
			}
			out = append(out, capture{wid, "ai_top_up_purchased", data})
		}
	}
	if inv := in.charge; inv != nil && ev.Type == "invoice.paid" {
		// A failed charge is handled by whoever tried it (the request or the recharge job).
		chargeID, _ := uuid.Parse(inv.Metadata["charge_id"])
		caps, err := s.grantChargeTx(ctx, tx, wid, chargeID, inv.ID, inv.Metadata)
		if err != nil {
			return nil, err
		}
		changed = changed || len(caps) > 0
		out = append(out, caps...)
	}
	if in.syncPM {
		if err := enqueuePaymentMethodSync(ctx, s.d, tx, wid); err != nil {
			return nil, err
		}
	}
	if ev.Type == "invoice.payment_failed" && in.charge == nil && in.sub != nil {
		if err := s.notifyPaymentFailed(ctx, tx, wid); err != nil {
			return nil, err
		}
		if err := audit.Log(ctx, tx, nil, wid, "billing.payment_failed", "workspace", &wid, nil); err != nil {
			return nil, err
		}
	}
	if changed {
		if err := s.publish(ctx, tx, wid, prices); err != nil {
			return nil, err
		}
	}
	return out, nil
}

var stripeStatuses = map[string]bool{"trialing": true, "active": true, "past_due": true, "unpaid": true, "canceled": true,
	"incomplete": true, "incomplete_expired": true, "paused": true}

// liveStatus reports whether a subscription still bills (anything but ended).
func liveStatus(st string) bool { return st != "canceled" && st != "incomplete_expired" }

type subChange struct {
	changed  bool
	captures []capture
}

// applySubscription stores a subscription's state on the workspace. A workspace keeps
// its current paid subscription if a different one reports in.
func (s *svc) applySubscription(ctx context.Context, tx pgx.Tx, wid uuid.UUID, sub *stripe.Subscription) (subChange, error) {
	var cur struct {
		plan, status, interval string
		seats                  int
		subID                  *string
		periodEnd              *time.Time
		cancel                 bool
	}
	if err := tx.QueryRow(ctx, `SELECT plan, status, interval, seats, stripe_subscription_id, current_period_end, cancel_at_period_end
		FROM subscriptions WHERE workspace_id = $1 FOR UPDATE`, wid).
		Scan(&cur.plan, &cur.status, &cur.interval, &cur.seats, &cur.subID, &cur.periodEnd, &cur.cancel); err != nil {
		return subChange{}, err
	}
	wasPaid := paidPlan(cur.plan, cur.status) != ""
	if cur.subID != nil && *cur.subID != sub.ID && wasPaid {
		s.d.Log.Warn("ignoring a second subscription for a paid workspace", "workspace_id", wid)
		return subChange{}, nil
	}
	status := string(sub.Status)
	if !stripeStatuses[status] {
		return subChange{}, fmt.Errorf("unknown subscription status %q", status)
	}
	planName := plan.Free
	if plan.Paid(status) {
		// The item's price says which paid plan; without one, a paid plan on record stays.
		planName = plan.Pro
		if wasPaid {
			planName = cur.plan
		}
	}
	interval, seats, periodEnd := cur.interval, cur.seats, cur.periodEnd
	if sub.Items != nil && len(sub.Items.Data) > 0 {
		it := sub.Items.Data[0]
		if it.Price != nil && it.Price.ID != "" && planName != plan.Free {
			planName = s.planOf(it.Price.ID)
		}
		if it.Quantity > 0 {
			seats = int(it.Quantity)
		}
		if it.Price != nil && it.Price.Recurring != nil {
			if iv := string(it.Price.Recurring.Interval); iv == "month" || iv == "year" {
				interval = iv
			}
		}
		if it.CurrentPeriodEnd > 0 {
			t := time.Unix(it.CurrentPeriodEnd, 0).UTC()
			periodEnd = &t
		}
	}
	cancel := liveStatus(status) && (sub.CancelAtPeriodEnd || sub.CancelAt > 0)
	if _, err := tx.Exec(ctx, `UPDATE subscriptions SET plan = $2, status = $3, interval = $4, seats = $5, stripe_subscription_id = $6,
		current_period_end = $7, cancel_at_period_end = $8, updated_at = now() WHERE workspace_id = $1`,
		wid, planName, status, interval, seats, sub.ID, periodEnd, cancel); err != nil {
		return subChange{}, err
	}
	nowPaid := planName != plan.Free
	samePeriod := (periodEnd == nil) == (cur.periodEnd == nil) && (periodEnd == nil || periodEnd.Equal(*cur.periodEnd))
	c := subChange{changed: cur.plan != planName || cur.status != status || cur.interval != interval || cur.seats != seats ||
		cur.subID == nil || *cur.subID != sub.ID || cur.cancel != cancel || !samePeriod}
	if !c.changed {
		return c, nil
	}
	data := map[string]any{"status": status, "interval": interval, "seats": seats, "cancel_at_period_end": cancel}
	if nowPaid {
		data["plan"] = planName
	}
	action := "billing.updated"
	props := map[string]any{"plan": planName, "interval": interval, "seats": seats}
	switch {
	case nowPaid && wasPaid && cur.plan != planName:
		action = "billing.plan_changed"
		data["from"] = cur.plan
		c.captures = append(c.captures, capture{wid, "subscription_plan_changed", map[string]any{"from": cur.plan, "to": planName,
			"interval": interval, "seats": seats}})
	case nowPaid && !wasPaid:
		action = "billing.subscribed"
		c.captures = append(c.captures, capture{wid, "subscription_started", props})
	case wasPaid && !nowPaid:
		action = "billing.canceled"
		c.captures = append(c.captures, capture{wid, "subscription_canceled", map[string]any{"status": status, "interval": interval, "seats": seats}})
	}
	return c, audit.Log(ctx, tx, nil, wid, action, "workspace", &wid, data)
}

// notifyPaymentFailed emails the workspace's owners and admins.
func (s *svc) notifyPaymentFailed(ctx context.Context, tx pgx.Tx, wid uuid.UUID) error {
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1`, wid).Scan(&name); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT u.email FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND m.role IN ('owner', 'admin')`, wid)
	if err != nil {
		return err
	}
	to, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	m := emails.PaymentFailed(emails.SiteFrom(s.d.Cfg), name, wid.String())
	for _, addr := range to {
		if _, err := s.d.Jobs.InsertTx(ctx, tx, jobs.SystemEmailArgs{To: addr, Subject: m.Subject, HTML: m.HTML, Text: m.Text},
			&river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 8}); err != nil {
			return err
		}
	}
	return nil
}

var errNoItem = errors.New("subscription has no items")

// checkoutUSD is the AI usage a top-up Checkout bought: metadata.micro_usd, or ten dollars
// per metadata.top_ups on sessions started before amounts were free.
func checkoutUSD(meta map[string]string) (plan.Micro, error) {
	if v := meta["micro_usd"]; v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || plan.Micro(n) < MinTopUp || plan.Micro(n) > MaxTopUp {
			return 0, errors.New("invalid micro_usd metadata")
		}
		return plan.Micro(n), nil
	}
	n, err := strconv.Atoi(meta["top_ups"])
	if err != nil || n < 1 || n > 20 {
		return 0, errors.New("invalid top_ups metadata")
	}
	return plan.TopUpUSD * plan.Micro(n), nil
}
