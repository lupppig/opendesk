package billing

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stripe/stripe-go/v87"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/plan"
)

type urlBody struct {
	Body struct {
		URL string `json:"url"`
	}
}

func newURL(u string) *urlBody {
	out := &urlBody{}
	out.Body.URL = u
	return out
}

// UsageReport is the GET /v1/billing/usage body.
type UsageReport struct {
	From       time.Time     `json:"from"`
	To         time.Time     `json:"to"`
	TotalUSD   float64       `json:"total_usd"`
	TotalCalls int           `json:"total_calls"`
	ByFeature  []FeatureUse  `json:"by_feature"`
	ByDay      []DayUse      `json:"by_day"`
	ByMember   []MemberUsage `json:"by_member"`
	ByModel    []ModelUse    `json:"by_model"`
	// OwnKeyCalls counts calls made on the workspace's own AI provider key ($0 each).
	OwnKeyCalls int `json:"own_key_calls"`
}

type FeatureUse struct {
	Feature string  `json:"feature"`
	USD     float64 `json:"usd"`
	Calls   int     `json:"calls"`
}

type DayUse struct {
	Date      string             `json:"date" doc:"YYYY-MM-DD, UTC"`
	USD       float64            `json:"usd"`
	Calls     int                `json:"calls"`
	ByFeature map[string]float64 `json:"by_feature" doc:"US dollars per feature that day"`
}

type MemberUsage struct {
	User  *domain.UserRef `json:"user" doc:"null for calls without a user (widget agent, automations)"`
	USD   float64         `json:"usd"`
	Calls int             `json:"calls"`
}

type ModelUse struct {
	Model        string  `json:"model"`
	USD          float64 `json:"usd"`
	Calls        int     `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	// Source is who paid: "instance" (the instance's provider, charged to the AI balance)
	// or "own_key" (the workspace's key).
	Source string `json:"source" enum:"instance,own_key"`
}

// requireBillingAdmin is the gate for routes that open Stripe: a signed-in owner or admin.
func requireBillingAdmin(ctx context.Context) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, "")
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if p.Kind != auth.KindUser {
		return nil, httpx.Forbidden()
	}
	return p, nil
}

func (s *svc) registerAPI(api huma.API) {
	tags := []string{"Billing"}

	huma.Register(api, huma.Operation{OperationID: "get-billing", Method: http.MethodGet, Path: "/v1/billing",
		Summary: "Get the workspace plan, limits, AI balance and prices", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body Billing }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b, err := s.load(ctx, s.d.DB.Pool, p.WorkspaceID, s.pricesFor(ctx))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Billing }{b}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-billing-checkout", Method: http.MethodPost, Path: "/v1/billing/checkout",
		Summary: "Start a Stripe Checkout for a paid plan", Tags: tags},
		func(ctx context.Context, in *struct {
			Body *struct {
				Interval string `json:"interval,omitempty" enum:"month,year" default:"month"`
				Plan     string `json:"plan,omitempty" enum:"pro,collab" default:"pro" doc:"collab only when the plan is offered to the workspace"`
			}
		}) (*urlBody, error) {
			if !s.enabled() {
				return nil, errDisabled()
			}
			p, err := requireBillingAdmin(ctx)
			if err != nil {
				return nil, err
			}
			planName, interval := plan.Pro, "month"
			if in.Body != nil {
				if in.Body.Plan == plan.Collab {
					planName = plan.Collab
				}
				if in.Body.Interval == "year" {
					interval = "year"
				}
			}
			if err := plan.Ensure(ctx, s.d.DB.Pool, p.WorkspaceID); err != nil {
				return nil, httpx.Wrap(err)
			}
			var curPlan, curStatus, access string
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT plan, status, collab_access FROM subscriptions WHERE workspace_id = $1`, p.WorkspaceID).
				Scan(&curPlan, &curStatus, &access); err != nil {
				return nil, httpx.Wrap(err)
			}
			if planName == plan.Collab && !collabOpen(access) {
				return nil, errPlanUnavailable()
			}
			price := s.planPrice(planName, interval)
			if price == "" {
				if planName == plan.Collab && !s.collabPriced() {
					return nil, errPlanNotPriced()
				}
				if interval == "year" {
					return nil, httpx.E(422, "interval_unavailable", "Yearly billing is not available.")
				}
				return nil, httpx.E(422, "interval_unavailable", "Monthly billing is not available.")
			}
			if !s.d.Limiter.Allow("billing:checkout:"+p.WorkspaceID.String(), 30, time.Hour) {
				return nil, httpx.RateLimited()
			}
			switch paidPlan(curPlan, curStatus) {
			case plan.Pro:
				return nil, httpx.E(409, "already_subscribed", "This workspace is already on Pro. Manage the subscription in the billing portal.")
			case plan.Collab:
				return nil, httpx.E(409, "already_subscribed", "This workspace already has a subscription. Switch plans or manage it on the billing page.")
			}
			cus, err := s.customer(ctx, p.WorkspaceID)
			if err != nil {
				return nil, s.wrapStripe(err)
			}
			seats, err := plan.BilledSeats(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			wid := p.WorkspaceID.String()
			meta := map[string]string{"workspace_id": wid, "plan": planName}
			params := &stripe.CheckoutSessionCreateParams{
				Mode:                stripe.String(string(stripe.CheckoutSessionModeSubscription)),
				Customer:            stripe.String(cus),
				ClientReferenceID:   stripe.String(wid),
				LineItems:           []*stripe.CheckoutSessionCreateLineItemParams{{Price: stripe.String(price), Quantity: stripe.Int64(int64(seats))}},
				SuccessURL:          stripe.String(s.appURL("checkout", "success")),
				CancelURL:           stripe.String(s.appURL("checkout", "canceled")),
				AllowPromotionCodes: stripe.Bool(true),
				AdaptivePricing:     &stripe.CheckoutSessionCreateAdaptivePricingParams{Enabled: stripe.Bool(true)},
				Metadata:            meta,
				SubscriptionData:    &stripe.CheckoutSessionCreateSubscriptionDataParams{Metadata: meta},
			}
			forBusiness(params)
			sess, err := s.sc.V1CheckoutSessions.Create(ctx, params)
			if err != nil {
				return nil, s.wrapStripe(err)
			}
			return newURL(sess.URL), nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-billing-portal", Method: http.MethodPost, Path: "/v1/billing/portal",
		Summary: "Open the Stripe Customer Portal", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*urlBody, error) {
			if !s.enabled() {
				return nil, errDisabled()
			}
			p, err := requireBillingAdmin(ctx)
			if err != nil {
				return nil, err
			}
			var cus *string
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT stripe_customer_id FROM subscriptions WHERE workspace_id = $1`, p.WorkspaceID).
				Scan(&cus); err != nil && !db.IsNotFound(err) {
				return nil, httpx.Wrap(err)
			}
			if cus == nil {
				return nil, httpx.E(409, "no_billing_account", "This workspace has no billing account yet. Upgrade or add an AI top-up first.")
			}
			sess, err := s.sc.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
				Customer: cus, ReturnURL: stripe.String(s.d.Cfg.AppURL + "/settings/billing")})
			if err != nil {
				return nil, s.wrapStripe(err)
			}
			return newURL(sess.URL), nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-top-up-checkout", Method: http.MethodPost, Path: "/v1/billing/top-up/checkout",
		Summary: "Start a Stripe Checkout for AI usage", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				AmountUSD *float64 `json:"amount_usd,omitempty" minimum:"5" maximum:"5000" doc:"AI usage to buy, in US dollars (rounded to cents), charged dollar for dollar"`
				TopUps    *int     `json:"top_ups,omitempty" minimum:"1" maximum:"20" doc:"Deprecated: $10 of AI usage each. Use amount_usd"`
			}
		}) (*urlBody, error) {
			if !s.enabled() {
				return nil, errDisabled()
			}
			p, err := requireBillingAdmin(ctx)
			if err != nil {
				return nil, err
			}
			var usd plan.Micro
			switch {
			case in.Body.AmountUSD != nil:
				if usd, err = topUpAmount(*in.Body.AmountUSD, "amount_usd"); err != nil {
					return nil, err
				}
			case in.Body.TopUps != nil:
				usd = plan.TopUpUSD * plan.Micro(*in.Body.TopUps)
			default:
				return nil, httpx.Validation("amount_usd is required.")
			}
			if s.d.Cfg.StripePriceTopUp == "" {
				return nil, errTopUpUnavailable()
			}
			if !s.d.Limiter.Allow("billing:checkout:"+p.WorkspaceID.String(), 30, time.Hour) {
				return nil, httpx.RateLimited()
			}
			price := s.pricesFor(ctx).AITopUp
			if price == nil || price.UnitAmount <= 0 {
				return nil, httpx.E(http.StatusBadGateway, "billing_unavailable", "The billing provider is unavailable. Try again in a moment.")
			}
			cus, err := s.customer(ctx, p.WorkspaceID)
			if err != nil {
				return nil, s.wrapStripe(err)
			}
			wid := p.WorkspaceID.String()
			meta := map[string]string{"workspace_id": wid, "kind": topUpKind, "micro_usd": strconv.FormatInt(int64(usd), 10)}
			// Any amount: priced on the top-up price's product, dollar for dollar.
			line := &stripe.CheckoutSessionCreateLineItemPriceDataParams{Currency: stripe.String(price.Currency),
				UnitAmount: stripe.Int64(amountFor(usd, price)), TaxBehavior: stripe.String("exclusive")}
			if price.Product != "" {
				line.Product = stripe.String(price.Product)
			} else {
				line.ProductData = &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripe.String("AI usage top-up")}
			}
			text := "AI usage top-up, " + usdText(usd)
			params := &stripe.CheckoutSessionCreateParams{
				Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
				Customer:          stripe.String(cus),
				ClientReferenceID: stripe.String(wid),
				LineItems:         []*stripe.CheckoutSessionCreateLineItemParams{{PriceData: line, Quantity: stripe.Int64(1)}},
				SuccessURL:        stripe.String(s.appURL("top_up", "success")),
				CancelURL:         stripe.String(s.appURL("top_up", "canceled")),
				AdaptivePricing:   &stripe.CheckoutSessionCreateAdaptivePricingParams{Enabled: stripe.Bool(true)},
				InvoiceCreation: &stripe.CheckoutSessionCreateInvoiceCreationParams{Enabled: stripe.Bool(true),
					InvoiceData: &stripe.CheckoutSessionCreateInvoiceCreationInvoiceDataParams{Description: stripe.String(text),
						Metadata: map[string]string{"workspace_id": wid, "kind": topUpKind}}},
				Metadata: meta,
				// Save the card for off-session use, so auto-recharge and one-click top-ups can charge it.
				PaymentIntentData: &stripe.CheckoutSessionCreatePaymentIntentDataParams{Metadata: meta, Description: stripe.String(text),
					SetupFutureUsage: stripe.String("off_session")},
			}
			forBusiness(params)
			sess, err := s.sc.V1CheckoutSessions.Create(ctx, params)
			if err != nil {
				return nil, s.wrapStripe(err)
			}
			return newURL(sess.URL), nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-billing-usage", Method: http.MethodGet, Path: "/v1/billing/usage",
		Summary: "AI usage in US dollars by feature, day, member and model", Tags: tags},
		func(ctx context.Context, in *struct {
			From string `query:"from" doc:"RFC 3339 time or YYYY-MM-DD (default: start of this month, UTC)"`
			To   string `query:"to" doc:"RFC 3339 time (exclusive) or YYYY-MM-DD (that day included) (default: now)"`
		}) (*struct{ Body UsageReport }, error) {
			// AI usage is recorded on every instance, so self-hosted admins see it too.
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			now := time.Now().UTC()
			from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			to := now
			if in.From != "" {
				if from, err = parseTime(in.From); err != nil {
					return nil, httpx.Validation("from must be an RFC 3339 time or a YYYY-MM-DD date.")
				}
			}
			if in.To != "" {
				if to, err = parseTime(in.To); err != nil {
					return nil, httpx.Validation("to must be an RFC 3339 time or a YYYY-MM-DD date.")
				}
				// A date names a whole day, so "to" includes it (as in /v1/reports/overview).
				if _, dateErr := time.Parse(time.DateOnly, in.To); dateErr == nil {
					to = to.AddDate(0, 0, 1)
				}
			}
			if !to.After(from) || to.Sub(from) > 367*24*time.Hour {
				return nil, httpx.Validation("to must be after from, at most a year apart.")
			}
			r, err := usageReport(ctx, s.d.DB.Pool, p.WorkspaceID, from, to)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body UsageReport }{r}, nil
		})
}

func parseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	return time.Parse(time.DateOnly, v)
}

func usageReport(ctx context.Context, q db.Querier, wid uuid.UUID, from, to time.Time) (UsageReport, error) {
	r := UsageReport{From: from, To: to, ByFeature: []FeatureUse{}, ByDay: []DayUse{}, ByMember: []MemberUsage{}, ByModel: []ModelUse{}}
	const where = ` FROM ai_usage a WHERE a.workspace_id = $1 AND a.created_at >= $2 AND a.created_at < $3`
	var total plan.Micro
	rows, err := q.Query(ctx, `SELECT a.feature, sum(a.micro_usd)::bigint, count(*)::int`+where+` GROUP BY a.feature ORDER BY 2 DESC, 1`, wid, from, to)
	if err != nil {
		return r, err
	}
	if r.ByFeature, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (FeatureUse, error) {
		var f FeatureUse
		var m plan.Micro
		err := row.Scan(&f.Feature, &m, &f.Calls)
		f.USD = m.USD()
		total += m
		return f, err
	}); err != nil {
		return r, err
	}
	r.TotalUSD = total.USD()
	for _, f := range r.ByFeature {
		r.TotalCalls += f.Calls
	}
	rows, err = q.Query(ctx, `SELECT to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') d, a.feature, sum(a.micro_usd)::bigint, count(*)::int`+where+
		` GROUP BY d, a.feature ORDER BY d, a.feature`, wid, from, to)
	if err != nil {
		return r, err
	}
	type dayFeature struct {
		day, feature string
		usd          plan.Micro
		calls        int
	}
	parts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (dayFeature, error) {
		var x dayFeature
		return x, row.Scan(&x.day, &x.feature, &x.usd, &x.calls)
	})
	if err != nil {
		return r, err
	}
	dayUSD := map[string]plan.Micro{}
	for _, x := range parts {
		if n := len(r.ByDay); n == 0 || r.ByDay[n-1].Date != x.day {
			r.ByDay = append(r.ByDay, DayUse{Date: x.day, ByFeature: map[string]float64{}})
		}
		d := &r.ByDay[len(r.ByDay)-1]
		dayUSD[x.day] += x.usd
		d.USD = dayUSD[x.day].USD()
		d.Calls += x.calls
		d.ByFeature[x.feature] = x.usd.USD()
	}
	rows, err = q.Query(ctx, `SELECT u.id, u.name, u.email, u.avatar_url, sum(a.micro_usd)::bigint, count(*)::int
		FROM ai_usage a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.workspace_id = $1 AND a.created_at >= $2 AND a.created_at < $3
		GROUP BY u.id, u.name, u.email, u.avatar_url ORDER BY 5 DESC LIMIT 500`, wid, from, to)
	if err != nil {
		return r, err
	}
	r.ByMember, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (MemberUsage, error) {
		var m MemberUsage
		var id *uuid.UUID
		var name, email, avatar *string
		var usd plan.Micro
		if err := row.Scan(&id, &name, &email, &avatar, &usd, &m.Calls); err != nil {
			return m, err
		}
		m.USD = usd.USD()
		if id != nil {
			m.User = &domain.UserRef{ID: *id, Name: *name, Email: *email, AvatarURL: domain.Avatar(*avatar)}
		}
		return m, nil
	})
	if err != nil {
		return r, err
	}
	rows, err = q.Query(ctx, `SELECT a.model, a.source, sum(a.micro_usd)::bigint, count(*)::int, sum(a.input_tokens)::bigint, sum(a.output_tokens)::bigint`+where+
		` GROUP BY a.model, a.source ORDER BY 3 DESC, 4 DESC, 1 LIMIT 100`, wid, from, to)
	if err != nil {
		return r, err
	}
	r.ByModel, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (ModelUse, error) {
		var m ModelUse
		var usd plan.Micro
		err := row.Scan(&m.Model, &m.Source, &usd, &m.Calls, &m.InputTokens, &m.OutputTokens)
		m.USD = usd.USD()
		return m, err
	})
	if err != nil {
		return r, err
	}
	err = q.QueryRow(ctx, `SELECT count(*)::int`+where+` AND a.source = 'own_key'`, wid, from, to).Scan(&r.OwnKeyCalls)
	return r, err
}

func errPlanUnavailable() *httpx.Error {
	return httpx.E(http.StatusForbidden, "plan_unavailable", "This plan is not available to this workspace.")
}

func errPlanNotPriced() *httpx.Error {
	return httpx.E(422, "plan_unavailable", "This plan cannot be bought on this instance yet.")
}

func (s *svc) appURL(key, value string) string {
	return s.d.Cfg.AppURL + "/settings/billing?" + key + "=" + value
}

// forBusiness sets up a Checkout Session as a business purchase: the company's legal
// name and billing address are required and its tax ID is collected, so they go on the
// invoice. Tax is not computed at checkout (contracts/billing.md "Tax").
func forBusiness(p *stripe.CheckoutSessionCreateParams) {
	p.BillingAddressCollection = stripe.String(string(stripe.CheckoutSessionBillingAddressCollectionRequired))
	p.NameCollection = &stripe.CheckoutSessionCreateNameCollectionParams{
		Business: &stripe.CheckoutSessionCreateNameCollectionBusinessParams{Enabled: stripe.Bool(true), Optional: stripe.Bool(false)}}
	p.TaxIDCollection = &stripe.CheckoutSessionCreateTaxIDCollectionParams{Enabled: stripe.Bool(true)}
	// Saves what the buyer enters on the workspace's existing customer, for later invoices.
	p.CustomerUpdate = &stripe.CheckoutSessionCreateCustomerUpdateParams{Address: stripe.String("auto"), Name: stripe.String("auto")}
}

// wrapStripe logs a Stripe failure (without request details) and answers 502.
func (s *svc) wrapStripe(err error) error {
	var se *stripe.Error
	if !errors.As(err, &se) {
		return httpx.Wrap(err)
	}
	s.d.Log.Error("stripe request failed", "err", stripeErr(err))
	return httpx.E(http.StatusBadGateway, "billing_unavailable", "The billing provider is unavailable. Try again in a moment.")
}

// customer returns the workspace's Stripe customer, creating it on first use with the
// workspace name, the owner's email and metadata.workspace_id.
func (s *svc) customer(ctx context.Context, wid uuid.UUID) (string, error) {
	if err := plan.Ensure(ctx, s.d.DB.Pool, wid); err != nil {
		return "", err
	}
	var cus *string
	var name, email string
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT s.stripe_customer_id, w.name, COALESCE((SELECT u.email FROM members m JOIN users u ON u.id = m.user_id
			WHERE m.workspace_id = w.id AND m.role = 'owner' LIMIT 1), '')
		FROM workspaces w JOIN subscriptions s ON s.workspace_id = w.id WHERE w.id = $1`, wid).Scan(&cus, &name, &email); err != nil {
		return "", err
	}
	if cus != nil {
		return *cus, nil
	}
	params := &stripe.CustomerCreateParams{Name: stripe.String(name), Metadata: map[string]string{"workspace_id": wid.String()}}
	if email != "" {
		params.Email = stripe.String(email)
	}
	params.IdempotencyKey = stripe.String("od-customer-" + wid.String())
	c, err := s.sc.V1Customers.Create(ctx, params)
	if err != nil {
		return "", err
	}
	// A concurrent request may have stored one first; keep whichever won.
	var id string
	err = s.d.DB.Pool.QueryRow(ctx, `UPDATE subscriptions SET stripe_customer_id = COALESCE(stripe_customer_id, $2), updated_at = now()
		WHERE workspace_id = $1 RETURNING stripe_customer_id`, wid, c.ID).Scan(&id)
	return id, err
}
