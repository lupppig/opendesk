// Package billing is the billing area. Register wires its HTTP routes, RegisterWorkers its
// River workers, Periodic its scheduled jobs and Background its long-running loops.
//
// Billing owns Stripe: Checkout, the Customer Portal, AI top-ups, auto-recharge, the
// Stripe webhook and seat sync. It is on only when STRIPE_SECRET_KEY is set (contracts/billing.md);
// otherwise GET /v1/billing reports self-hosted entitlements, every other billing route
// answers 404 billing_disabled and no worker runs.
package billing

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// WebhookPath is the Stripe webhook endpoint.
const WebhookPath = "/v1/billing/stripe/webhook"

// Register adds this area's routes. Prefer huma operations; use r only for raw HTTP
// (redirects, raw bodies, streaming).
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	s := newSvc(d)
	s.registerAPI(api)
	s.registerRechargeAPI(api)
	s.registerPaymentsAPI(api)
	s.registerPlansAPI(api)
	// Raw route: the signature is computed over the exact body bytes.
	r.Post(WebhookPath, s.webhook)
	if s.enabled() && d.Plan != nil {
		d.Plan.OnLimit = func(wid uuid.UUID, resource string) {
			d.Analytics.Capture(wid.String(), wid.String(), "plan_limit_hit", map[string]any{"resource": resource})
		}
		d.Plan.OnSpend = s.onSpend
	}
}

// RegisterWorkers adds this area's River workers (none when billing is disabled).
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	if !d.Cfg.BillingEnabled() {
		return
	}
	s := newSvc(d)
	river.AddWorker(w, &syncSeatsWorker{s: s})
	river.AddWorker(w, &reconcileWorker{s: s})
	river.AddWorker(w, &cancelWorker{s: s})
	river.AddWorker(w, &autoRechargeWorker{s: s})
	river.AddWorker(w, &syncPaymentMethodWorker{s: s})
	// AI calls charged in workers (widget agent, triage) also trigger auto-recharge.
	if d.Plan != nil {
		d.Plan.OnSpend = s.onSpend
	}
}

// Periodic returns this area's scheduled jobs: an hourly seat reconciliation.
func Periodic(d *deps.Deps) []*river.PeriodicJob {
	if !d.Cfg.BillingEnabled() {
		return nil
	}
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return jobs.ReconcileBillingArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}

// Background runs until ctx is done, in the worker role only. Billing has no loops.
func Background(ctx context.Context, d *deps.Deps) {}
