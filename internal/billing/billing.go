package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stripe/stripe-go/v87"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
)

// Billing is the GET /v1/billing body and the billing.updated event data.
type Billing struct {
	Plan              string      `json:"plan" enum:"free,pro,collab,self_hosted"`
	BillingEnabled    bool        `json:"billing_enabled"`
	Status            string      `json:"status" enum:"none,trialing,active,past_due,unpaid,canceled,incomplete,incomplete_expired,paused"`
	Interval          string      `json:"interval" enum:"month,year"`
	Seats             int         `json:"seats" doc:"Billed seats in Stripe"`
	BilledSeats       int         `json:"billed_seats" doc:"Current owners, admins and agents: what the next sync sets"`
	CurrentPeriodEnd  *time.Time  `json:"current_period_end"`
	CancelAtPeriodEnd bool        `json:"cancel_at_period_end"`
	Limits            Limits      `json:"limits"`
	Usage             UsageCounts `json:"usage"`
	AI                AIBalance   `json:"ai" doc:"The AI balance, in US dollars (up to 6 decimals)"`
	Prices            Prices      `json:"prices"`
	// Collab is the workspace's Collab (contracts/billing.md "Collab").
	Collab CollabBilling `json:"collab"`
	// AutoRecharge is null when billing is disabled.
	AutoRecharge *AutoRecharge `json:"auto_recharge"`
	// PaymentMethod is what top-ups and auto-recharge charge; null when none is saved.
	PaymentMethod *PaymentMethod `json:"payment_method"`
}

// Limits are the plan limits; zero means unlimited.
type Limits struct {
	MaxMailboxes int `json:"max_mailboxes"`
	MaxMembers   int `json:"max_members"`
}

// UsageCounts is what counts against Limits. Members include pending invitations.
type UsageCounts struct {
	Mailboxes int `json:"mailboxes"`
	Members   int `json:"members"`
}

// AIBalance is Billing.ai: the monthly allowance and what is left, in US dollars.
type AIBalance struct {
	Unlimited    bool      `json:"unlimited"`
	MonthlyUSD   float64   `json:"monthly_usd" doc:"The monthly AI usage allowance"`
	UsedUSD      float64   `json:"used_usd" doc:"Used of the monthly allowance this period (with unlimited AI: all usage this period)"`
	PurchasedUSD float64   `json:"purchased_usd" doc:"Top-ups left; they never expire and are spent after the allowance"`
	RemainingUSD float64   `json:"remaining_usd"`
	PeriodEnd    time.Time `json:"period_end"`
}

// CollabBilling is Billing.collab.
type CollabBilling struct {
	// Access is the instance admin's Collab access; the collab plan is hidden without it.
	Access string     `json:"access" enum:"none,offered,complimentary"`
	Until  *time.Time `json:"until" doc:"Complimentary access ends then; null: until an instance admin changes it"`
	// Enabled is whether the workspace has Collab now (plan.Entitlements.Collab).
	Enabled bool `json:"enabled"`
	// Available is whether the workspace may subscribe or switch to the collab plan.
	Available bool `json:"available"`
}

type Prices struct {
	ProMonth *Price `json:"pro_month"`
	ProYear  *Price `json:"pro_year"`
	AITopUp  *Price `json:"ai_top_up"`
	// CollabMonth and CollabYear are present only for a workspace the collab plan is
	// offered to (or given complimentary): the plan is hidden from everyone else.
	CollabMonth *Price `json:"collab_month,omitempty"`
	CollabYear  *Price `json:"collab_year,omitempty"`
}

type Price struct {
	ID         string  `json:"id"`
	UnitAmount int64   `json:"unit_amount"`
	Currency   string  `json:"currency"`
	Interval   *string `json:"interval" enum:"month,year"`
	USD        float64 `json:"usd,omitempty" doc:"ai_top_up: the AI usage unit_amount buys, in US dollars"`
	// Product is the price's Stripe product: top-ups of any amount are priced on it.
	Product string `json:"-"`
}

// stripeBackendURL points the Stripe client somewhere else (tests use a local fake).
var stripeBackendURL string

// svc carries what billing handlers and workers share.
type svc struct {
	d      *deps.Deps
	sc     *stripe.Client
	prices priceCache
}

func newSvc(d *deps.Deps) *svc {
	s := &svc{d: d}
	if d.Cfg.BillingEnabled() {
		cfg := &stripe.BackendConfig{
			HTTPClient:        &http.Client{Timeout: 30 * time.Second},
			LeveledLogger:     &stripe.LeveledLogger{Level: stripe.LevelNull},
			MaxNetworkRetries: stripe.Int64(2),
		}
		backend := stripeBackendURL
		if backend == "" {
			backend = d.Cfg.StripeAPIURL // development only (config refuses it in production)
		}
		if backend != "" {
			cfg.URL = stripe.String(backend)
			cfg.MaxNetworkRetries = stripe.Int64(0)
		}
		s.sc = stripe.NewClient(d.Cfg.StripeSecretKey, stripe.WithBackends(stripe.NewBackendsWithConfig(cfg)))
	}
	return s
}

func (s *svc) enabled() bool { return s.sc != nil }

func errDisabled() *httpx.Error {
	return httpx.E(http.StatusNotFound, "billing_disabled", "Billing is not enabled on this instance.")
}

// priceCache keeps the Stripe prices for 10 minutes.
type priceCache struct {
	mu      sync.Mutex
	at      time.Time
	value   Prices
	fetched bool
}

const priceTTL = 10 * time.Minute

// pricesFor returns the configured prices, from Stripe at most every 10 minutes. On a
// Stripe failure it serves the last good value (or nulls) and retries after a minute.
func (s *svc) pricesFor(ctx context.Context) Prices {
	if !s.enabled() {
		return Prices{}
	}
	s.prices.mu.Lock()
	defer s.prices.mu.Unlock()
	if s.prices.fetched && time.Since(s.prices.at) < priceTTL {
		return s.prices.value
	}
	cfg := s.d.Cfg
	var out Prices
	var failed bool
	get := func(id string, usage plan.Micro) *Price {
		if id == "" {
			return nil
		}
		p, err := s.sc.V1Prices.Retrieve(ctx, id, nil)
		if err != nil {
			failed = true
			s.d.Log.Warn("stripe price", "price", id, "err", stripeErr(err))
			return nil
		}
		out := &Price{ID: p.ID, UnitAmount: p.UnitAmount, Currency: string(p.Currency), USD: usage.USD()}
		if p.Product != nil {
			out.Product = p.Product.ID
		}
		if p.Recurring != nil {
			iv := string(p.Recurring.Interval)
			out.Interval = &iv
		}
		return out
	}
	out.ProMonth = get(cfg.StripePricePro, 0)
	out.ProYear = get(cfg.StripePriceProYear, 0)
	out.AITopUp = get(cfg.StripePriceTopUp, plan.TopUpUSD)
	out.CollabMonth = get(cfg.StripePriceCollab, 0)
	out.CollabYear = get(cfg.StripePriceCollabYear, 0)
	s.prices.at = time.Now()
	if failed {
		if s.prices.fetched {
			out = s.prices.value
		}
		s.prices.at = s.prices.at.Add(time.Minute - priceTTL)
	}
	s.prices.value, s.prices.fetched = out, true
	return out
}

// stripeErr describes a Stripe error without request details.
func stripeErr(err error) string {
	var se *stripe.Error
	if errors.As(err, &se) {
		if se.HTTPStatusCode == http.StatusUnauthorized || se.HTTPStatusCode == http.StatusForbidden {
			// The message echoes part of the key.
			return fmt.Sprintf("%s: authentication failed (status %d)", se.Type, se.HTTPStatusCode)
		}
		return string(se.Type) + "/" + string(se.Code) + ": " + se.Msg
	}
	return err.Error()
}

func isMissing(err error) bool {
	var se *stripe.Error
	return errors.As(err, &se) && (se.Code == stripe.ErrorCodeResourceMissing || se.HTTPStatusCode == http.StatusNotFound)
}

// load builds the Billing shape. prices come from the cache (pass them in when q is a tx,
// so no Stripe call happens while holding locks).
func (s *svc) load(ctx context.Context, q db.Querier, wid uuid.UUID, prices Prices) (Billing, error) {
	b := Billing{Prices: prices, Interval: "month", Status: "none"}
	e, err := s.d.Plan.Entitlements(ctx, q, wid)
	if err != nil {
		return b, err
	}
	b.Plan, b.BillingEnabled = e.Plan, e.BillingEnabled
	b.Collab = CollabBilling{Access: e.CollabAccess, Until: e.CollabUntil, Enabled: e.Collab}
	if b.Collab.Access == "" {
		b.Collab.Access = plan.CollabNone
	}
	if collabOpen(e.CollabAccess) {
		b.Collab.Available = e.BillingEnabled && s.collabPriced()
	} else {
		// The collab plan is hidden: its prices never reach a workspace it is not offered to.
		b.Prices.CollabMonth, b.Prices.CollabYear = nil, nil
	}
	b.Limits = Limits{MaxMailboxes: e.MaxMailboxes, MaxMembers: e.MaxMembers}
	if e.BillingEnabled {
		if err := q.QueryRow(ctx, `SELECT status, interval, seats, current_period_end, cancel_at_period_end
			FROM subscriptions WHERE workspace_id = $1`, wid).
			Scan(&b.Status, &b.Interval, &b.Seats, &b.CurrentPeriodEnd, &b.CancelAtPeriodEnd); err != nil {
			return b, err
		}
	} else {
		b.Status = e.Status
	}
	if b.BilledSeats, err = plan.BilledSeats(ctx, q, wid); err != nil {
		return b, err
	}
	if err := q.QueryRow(ctx, `SELECT (SELECT count(*) FROM mailboxes WHERE workspace_id = $1),
		(SELECT count(*) FROM members WHERE workspace_id = $1)
		+ (SELECT count(*) FROM invitations WHERE workspace_id = $1 AND accepted_at IS NULL AND expires_at > now())`, wid).
		Scan(&b.Usage.Mailboxes, &b.Usage.Members); err != nil {
		return b, err
	}
	c, err := s.d.Plan.Balance(ctx, q, wid)
	if err != nil {
		return b, err
	}
	b.AI = AIBalance{Unlimited: c.Unlimited, MonthlyUSD: c.Monthly.USD(), UsedUSD: c.IncludedUsed.USD(),
		PurchasedUSD: c.Purchased.USD(), RemainingUSD: c.Remaining.USD(), PeriodEnd: c.PeriodEnd}
	if c.Unlimited {
		// Unlimited workspaces do not draw on the allowance; report what was used.
		var used plan.Micro
		if err := q.QueryRow(ctx, `SELECT COALESCE(sum(micro_usd), 0)::bigint FROM ai_usage WHERE workspace_id = $1 AND created_at >= $2`,
			wid, c.PeriodStart).Scan(&used); err != nil {
			return b, err
		}
		b.AI.UsedUSD = used.USD()
	}
	if e.BillingEnabled {
		if b.AutoRecharge, err = loadAutoRecharge(ctx, q, wid, e.MonthlyUSD); err != nil {
			return b, err
		}
		if b.PaymentMethod, err = loadPaymentMethod(ctx, q, wid); err != nil {
			return b, err
		}
	}
	return b, nil
}

// collabOpen reports whether Collab access lets a workspace buy or switch to the collab plan.
func collabOpen(access string) bool {
	return access == plan.CollabOffered || access == plan.CollabComplimentary
}

// collabPriced reports whether the collab plan has a Stripe price on this instance.
func (s *svc) collabPriced() bool {
	return s.d.Cfg.StripePriceCollab != "" || s.d.Cfg.StripePriceCollabYear != ""
}

// paidPlan is the plan of a workspace's Stripe subscription while it grants it (pro or
// collab), or "" without one. Complimentary Collab is not a subscription.
func paidPlan(planName, status string) string {
	if (planName == plan.Pro || planName == plan.Collab) && plan.Paid(status) {
		return planName
	}
	return ""
}

// planPrice is the Stripe price of a paid plan for an interval; "" when not configured.
func (s *svc) planPrice(planName, interval string) string {
	cfg := s.d.Cfg
	switch {
	case planName == plan.Collab && interval == "year":
		return cfg.StripePriceCollabYear
	case planName == plan.Collab:
		return cfg.StripePriceCollab
	case interval == "year":
		return cfg.StripePriceProYear
	}
	return cfg.StripePricePro
}

// planOf is the paid plan a subscription item's price stands for: a collab price (monthly
// or yearly) is the collab plan, any other the pro plan.
func (s *svc) planOf(priceID string) string {
	cfg := s.d.Cfg
	if priceID != "" && (priceID == cfg.StripePriceCollab || priceID == cfg.StripePriceCollabYear) {
		return plan.Collab
	}
	return plan.Pro
}

// Publisher publishes billing.updated for a workspace from outside billing (the instance
// admin's Collab access). It keeps its own Stripe price cache.
type Publisher struct{ s *svc }

// NewPublisher returns a Publisher for d.
func NewPublisher(d *deps.Deps) *Publisher { return &Publisher{s: newSvc(d)} }

// Prices reads the prices to publish with; call it before opening the transaction.
func (p *Publisher) Prices(ctx context.Context) Prices { return p.s.pricesFor(ctx) }

// Publish sends billing.updated with the workspace's current Billing in tx.
func (p *Publisher) Publish(ctx context.Context, tx pgx.Tx, wid uuid.UUID, prices Prices) error {
	return p.s.publish(ctx, tx, wid, prices)
}

// publish sends billing.updated with the current Billing in tx.
func (s *svc) publish(ctx context.Context, tx pgx.Tx, wid uuid.UUID, prices Prices) error {
	b, err := s.load(ctx, tx, wid, prices)
	if err != nil {
		return err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "billing.updated", Data: b})
	return err
}

// seatSyncDelay debounces bursts of member changes into one Stripe update.
const seatSyncDelay = 10 * time.Second

var uniqueStates = []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending,
	rivertype.JobStateRunning, rivertype.JobStateRetryable, rivertype.JobStateScheduled}

func seatSyncOpts() *river.InsertOpts {
	return &river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 10, ScheduledAt: time.Now().Add(seatSyncDelay),
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: uniqueStates}}
}

// EnqueueSeatSync queues a Stripe seat sync for wid in tx. Call it whenever members are
// added, removed or change role. It does nothing when billing is disabled.
func EnqueueSeatSync(ctx context.Context, d *deps.Deps, tx pgx.Tx, wid uuid.UUID) error {
	if !d.Cfg.BillingEnabled() || d.Jobs == nil {
		return nil
	}
	_, err := d.Jobs.InsertTx(ctx, tx, jobs.SyncSeatsArgs{WorkspaceID: wid}, seatSyncOpts())
	return err
}

// EnqueueCancel queues the immediate cancellation of wid's Stripe subscription. Call it
// in the transaction that deletes the workspace, before the delete. It does nothing when
// billing is disabled or the workspace never had a Stripe customer.
func EnqueueCancel(ctx context.Context, d *deps.Deps, tx pgx.Tx, wid uuid.UUID) error {
	if !d.Cfg.BillingEnabled() || d.Jobs == nil {
		return nil
	}
	var cus, sub *string
	err := tx.QueryRow(ctx, `SELECT stripe_customer_id, stripe_subscription_id FROM subscriptions WHERE workspace_id = $1`, wid).Scan(&cus, &sub)
	if db.IsNotFound(err) || (err == nil && cus == nil && sub == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	args := jobs.CancelSubscriptionArgs{WorkspaceID: wid}
	if cus != nil {
		args.CustomerID = *cus
	}
	if sub != nil {
		args.SubscriptionID = *sub
	}
	_, err = d.Jobs.InsertTx(ctx, tx, args, &river.InsertOpts{Queue: jobs.QueueDefault, MaxAttempts: 20})
	return err
}
