// Package plan holds workspace entitlements: plan limits and the AI balance.
//
// Limits and the AI balance are enforced only when billing is enabled (opendesk Cloud, Stripe
// configured). A self-hosted instance gets Unlimited entitlements, and AI usage is still
// recorded so admins can see it.
package plan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
)

const (
	Free = "free"
	Pro  = "pro"
	// Collab is Pro plus Collab, members' own mail accounts (contracts/collab.md).
	Collab = "collab"
	// SelfHosted is reported when billing is disabled.
	SelfHosted = "self_hosted"
)

// Collab access an instance admin gives a workspace (contracts/admin.md "Collab access").
const (
	// CollabNone: the collab plan is not offered (it is hidden).
	CollabNone = "none"
	// CollabOffered: the workspace may subscribe to the collab plan.
	CollabOffered = "offered"
	// CollabComplimentary: the workspace has the collab plan without paying for it.
	CollabComplimentary = "complimentary"
)

// Plan numbers. Prices live in Stripe; these are the limits the API enforces.
const (
	FreeMaxMailboxes = 1
	FreeMaxMembers   = 1
	// FreeMonthlyUSD is the AI usage allowance on the free plan, per workspace per month.
	FreeMonthlyUSD = Micro(500_000) // $0.50
	// ProUSDPerSeat is the monthly AI usage allowance per paid seat, pooled across the workspace.
	ProUSDPerSeat = Micro(10_000_000) // $10
	// CollabUSDPerSeat is the collab plan's monthly AI usage allowance per seat, pooled.
	CollabUSDPerSeat = Micro(20_000_000) // $20
	// TopUpUSD is the AI usage one top-up (STRIPE_PRICE_AI_TOPUP) adds: its price, dollar
	// for dollar, when the Stripe price is $10.
	TopUpUSD = Micro(10_000_000) // $10
	// MinBalance is the smallest balance any metered call needs to start: every call's
	// estimate is at least this much. Below it a workspace is out of AI balance.
	MinBalance = Micro(10_000) // $0.01
)

// Import file limits (contracts/billing.md "Imports"): what imports copy of one message's
// files, and of a workspace's in all. Every file is also at most 25 MB, as for any attachment.
const (
	FreeImportFilesPerMessage       = 100
	FreeImportMessageBytes    int64 = 100 << 20 // 100 MB
	FreeImportBytes           int64 = 1 << 30   // 1 GB per workspace
	PaidImportFilesPerMessage       = 1000
	PaidImportMessageBytes    int64 = 1 << 30  // 1 GB
	PaidImportBytesPerSeat    int64 = 50 << 30 // 50 GB per paid seat, pooled
)

// CollabStorageBytesPerSeat is the collab plan's fair-use allowance for the mail Collab
// stores (contracts/collab.md "Storage"), per paid seat, pooled across the workspace.
const CollabStorageBytesPerSeat int64 = 25 << 30 // 25 GB

// Micro is an amount of US dollars in millionths of a dollar. Balances, allowances and
// usage rows are kept in Micro; the API reports dollars as numbers with up to 6 decimals.
type Micro int64

// USD returns m as a number of dollars.
func (m Micro) USD() float64 { return float64(m) / 1e6 }

// FromUSD turns a number of dollars into Micro, rounded to the nearest millionth.
func FromUSD(usd float64) Micro { return Micro(math.Round(usd * 1e6)) }

// FromCost turns a provider cost in USD into what the workspace pays at the given
// markup, rounded up to a millionth of a dollar. A call with any cost is at least one.
func FromCost(usd, markup float64) Micro {
	if usd <= 0 {
		return 0
	}
	if markup < 1 {
		markup = 1
	}
	// The tolerance keeps float noise (3000.0000000001) from rounding a whole amount up.
	m := Micro(math.Ceil(usd*markup*1e6 - 1e-6))
	return max(m, 1)
}

// Typical is what a call of each AI feature typically costs with the default models
// and a typical thread (contracts/billing.md "Metered usage"). It is charged when
// neither the provider nor the model's price gives the cost, and it is the estimate to
// start a call whose model price is unknown.
var Typical = map[string]Micro{
	"summarize":       10_000, // $0.01
	"rewrite":         10_000,
	"translate":       10_000,
	"triage":          10_000, // every triage decision in one call (Jev, or the AI model)
	"decide":          10_000, // the ai_decide automation conditions of one trigger run, in one call
	"answer_check":    1_000,  // the widget agent's relevance check on Jev
	"mail_categorize": 2_000,  // sorting up to 10 new Collab conversations into categories, in one call
	"mail_categories": 10_000, // Ollie's suggested categories for an inbox
	"draft_reply":     30_000,
	"answer":          30_000, // one model turn of a widget agent reply
	"answer_email":    30_000, // one model turn of an agent email answer or suggested reply
	"playground":      30_000, // one model turn of an agent playground reply
	"kb_draft":        50_000, // knowledge base article draft from tickets
	"copilot":         20_000, // free-form question about a ticket
	"assistant":       20_000, // one model turn of the assistant (Ollie)
	"assistant_peer":  10_000, // one answer of a teammate's Ollie, paid by the asker's workspace
}

var (
	// ErrLimit means a plan limit was reached; the API answers 402 plan_limit.
	ErrLimit = errors.New("plan limit reached")
	// ErrNoBalance means the workspace's AI balance cannot cover a call; 402 ai_balance_empty.
	ErrNoBalance = errors.New("AI balance too low")
)

// LimitError says which limit was reached.
type LimitError struct {
	Resource string // "mailboxes" | "members"
	Limit    int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("the %s plan allows %d %s", Free, e.Limit, e.Resource)
}
func (e *LimitError) Unwrap() error { return ErrLimit }

// Entitlements is what a workspace may use right now.
type Entitlements struct {
	Plan           string `json:"plan" enum:"free,pro,collab,self_hosted"`
	BillingEnabled bool   `json:"billing_enabled"`
	Status         string `json:"status"`
	// Collab reports whether the workspace has Collab (contracts/collab.md "Who has it").
	Collab bool `json:"collab"`
	// CollabAccess and CollabUntil are the instance admin's Collab access (billing on only).
	CollabAccess string     `json:"collab_access" enum:"none,offered,complimentary"`
	CollabUntil  *time.Time `json:"collab_until"`
	// Zero means unlimited.
	MaxMailboxes int `json:"max_mailboxes"`
	MaxMembers   int `json:"max_members"`
	// MonthlyUSD is the monthly AI usage allowance; zero with UnlimitedAI.
	MonthlyUSD  Micro `json:"-"`
	UnlimitedAI bool  `json:"unlimited_ai"`
	// ImportFilesPerMessage and ImportMessageBytes bound what an import copies of one
	// message's files; ImportBytes, what imports copy into the workspace in all. Zero
	// means unlimited.
	ImportFilesPerMessage int   `json:"import_files_per_message"`
	ImportMessageBytes    int64 `json:"import_message_bytes"`
	ImportBytes           int64 `json:"import_bytes"`
	// CollabStorageBytes is the allowance for the mail Collab stores (contracts/collab.md
	// "Storage"). Zero means unlimited.
	CollabStorageBytes int64 `json:"collab_storage_bytes"`
}

// Service reads and enforces entitlements. A nil *Service behaves like a self-hosted
// instance: no limits, unlimited AI.
type Service struct {
	Billing bool
	// CollabOff turns Collab off on this instance (COLLAB=false), whatever the plan.
	CollabOff bool
	// Markup multiplies a call's provider cost into what the workspace pays
	// (AI_USAGE_MARKUP); below 1 counts as 1.
	Markup float64
	// OnLimit, when set, is told about every refused mailbox or member (analytics).
	OnLimit func(wid uuid.UUID, resource string)
	// OnSpend, when set, is told the balance before and after every call Spend charged
	// (billing: auto-recharge and running-low notices). It runs after the charge commits
	// and never fails the call.
	OnSpend func(ctx context.Context, wid uuid.UUID, before, after Micro)
}

func (s *Service) enabled() bool { return s != nil && s.Billing }

func (s *Service) limitHit(wid uuid.UUID, le *LimitError) error {
	if s != nil && s.OnLimit != nil {
		s.OnLimit(wid, le.Resource)
	}
	return le
}

// lock serializes limit checks per workspace for the rest of q's transaction, so two
// concurrent creates cannot both pass a limit. Outside a transaction it is a no-op.
func lock(ctx context.Context, q db.Querier, wid uuid.UUID) error {
	_, err := q.Exec(ctx, `SELECT 1 FROM subscriptions WHERE workspace_id = $1 FOR UPDATE`, wid)
	return err
}

func New(billingEnabled bool, markup float64) *Service {
	return &Service{Billing: billingEnabled, Markup: markup}
}

// Ensure creates the subscription and AI balance rows for a workspace if they are missing.
func Ensure(ctx context.Context, q db.Querier, wid uuid.UUID) error {
	if _, err := q.Exec(ctx, `INSERT INTO subscriptions (workspace_id) VALUES ($1) ON CONFLICT DO NOTHING`, wid); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `INSERT INTO ai_balances (workspace_id) VALUES ($1) ON CONFLICT DO NOTHING`, wid)
	return err
}

// Paid reports whether a subscription status grants the paid plan. past_due keeps access
// while Stripe retries payment.
func Paid(status string) bool {
	switch status {
	case "active", "trialing", "past_due":
		return true
	}
	return false
}

// Entitlements returns the workspace's current limits.
func (s *Service) Entitlements(ctx context.Context, q db.Querier, wid uuid.UUID) (Entitlements, error) {
	if !s.enabled() {
		return Entitlements{Plan: SelfHosted, Status: "active", UnlimitedAI: true, Collab: s == nil || !s.CollabOff, CollabAccess: CollabNone}, nil
	}
	if err := Ensure(ctx, q, wid); err != nil {
		return Entitlements{}, err
	}
	var planName, status string
	var seats int
	var o Overrides
	e := Entitlements{BillingEnabled: true}
	if err := q.QueryRow(ctx, `SELECT plan, status, seats, max_mailboxes_override, max_members_override, monthly_micro_usd_override,
		collab_storage_override, collab_access, collab_until FROM subscriptions WHERE workspace_id = $1`, wid).
		Scan(&planName, &status, &seats, &o.MaxMailboxes, &o.MaxMembers, &o.MonthlyUSD, &o.CollabStorageBytes, &e.CollabAccess, &e.CollabUntil); err != nil {
		return Entitlements{}, err
	}
	e.Plan, e.Status = Free, status
	switch {
	case planName == Collab && Paid(status):
		e.Plan, e.Collab = Collab, true
		e.MonthlyUSD = CollabUSDPerSeat * Micro(max(seats, 1))
		e.paidImports(seats)
		e.CollabStorageBytes = CollabStorageBytesPerSeat * int64(max(seats, 1))
	case planName == Pro && Paid(status):
		e.Plan = Pro
		e.MonthlyUSD = ProUSDPerSeat * Micro(max(seats, 1))
		e.paidImports(seats)
	default:
		e.MaxMailboxes, e.MaxMembers, e.MonthlyUSD = FreeMaxMailboxes, FreeMaxMembers, FreeMonthlyUSD
		e.ImportFilesPerMessage, e.ImportMessageBytes, e.ImportBytes = FreeImportFilesPerMessage, FreeImportMessageBytes, FreeImportBytes
	}
	// Complimentary Collab is the collab plan without a subscription, until it ends.
	if e.Plan != Collab && ComplimentaryActive(e.CollabAccess, e.CollabUntil, time.Now()) {
		billed, err := BilledSeats(ctx, q, wid)
		if err != nil {
			return Entitlements{}, err
		}
		e.Plan, e.Collab = Collab, true
		e.MaxMailboxes, e.MaxMembers = 0, 0
		e.MonthlyUSD = CollabUSDPerSeat * Micro(billed)
		e.paidImports(billed)
		e.CollabStorageBytes = CollabStorageBytesPerSeat * int64(billed)
	}
	if s.CollabOff {
		e.Collab = false
	}
	o.apply(&e)
	return e, nil
}

// paidImports sets the import file limits of a paid plan with seats.
func (e *Entitlements) paidImports(seats int) {
	e.ImportFilesPerMessage, e.ImportMessageBytes = PaidImportFilesPerMessage, PaidImportMessageBytes
	e.ImportBytes = PaidImportBytesPerSeat * int64(max(seats, 1))
}

// ComplimentaryActive reports whether complimentary Collab access is in effect at now.
func ComplimentaryActive(access string, until *time.Time, now time.Time) bool {
	return access == CollabComplimentary && (until == nil || until.After(now))
}

// CollabAccess is an instance admin's Collab access for one workspace.
type CollabAccess struct {
	Access string     // CollabNone, CollabOffered or CollabComplimentary
	Until  *time.Time // complimentary access ends then; nil = until changed
	Note   string
}

// LoadCollabAccess reads a workspace's Collab access.
func LoadCollabAccess(ctx context.Context, q db.Querier, wid uuid.UUID) (CollabAccess, error) {
	if err := Ensure(ctx, q, wid); err != nil {
		return CollabAccess{}, err
	}
	var a CollabAccess
	err := q.QueryRow(ctx, `SELECT collab_access, collab_until, collab_note FROM subscriptions WHERE workspace_id = $1`, wid).
		Scan(&a.Access, &a.Until, &a.Note)
	return a, err
}

// SetCollabAccess replaces a workspace's Collab access. Until is kept only for
// complimentary access.
func SetCollabAccess(ctx context.Context, q db.Querier, wid uuid.UUID, a CollabAccess) error {
	if err := Ensure(ctx, q, wid); err != nil {
		return err
	}
	switch a.Access {
	case CollabNone, CollabOffered, CollabComplimentary:
	default:
		return fmt.Errorf("plan: unknown collab access %q", a.Access)
	}
	if a.Access != CollabComplimentary {
		a.Until = nil
	}
	_, err := q.Exec(ctx, `UPDATE subscriptions SET collab_access = $2, collab_until = $3, collab_note = $4, updated_at = now()
		WHERE workspace_id = $1`, wid, a.Access, a.Until, a.Note)
	return err
}

// ErrCollab means the workspace does not have Collab; the API answers 402 collab_required.
var ErrCollab = errors.New("Collab is not on this workspace's plan")

// RequireCollab returns ErrCollab unless the workspace has Collab.
func (s *Service) RequireCollab(ctx context.Context, q db.Querier, wid uuid.UUID) error {
	e, err := s.Entitlements(ctx, q, wid)
	if err != nil {
		return err
	}
	if !e.Collab {
		return ErrCollab
	}
	return nil
}

// Overrides are an instance admin's limits for one workspace (contracts/admin.md); nil
// keeps the plan's. They apply on every plan and only while billing is on.
type Overrides struct {
	// MaxMailboxes and MaxMembers replace the plan's limits; 0 is unlimited.
	MaxMailboxes *int
	MaxMembers   *int
	// MonthlyUSD replaces the monthly AI usage allowance.
	MonthlyUSD *Micro
	// CollabStorageBytes replaces the Collab storage allowance; 0 is unlimited.
	CollabStorageBytes *int64
}

func (o Overrides) apply(e *Entitlements) {
	if o.MaxMailboxes != nil {
		e.MaxMailboxes = *o.MaxMailboxes
	}
	if o.MaxMembers != nil {
		e.MaxMembers = *o.MaxMembers
	}
	if o.MonthlyUSD != nil {
		e.MonthlyUSD = *o.MonthlyUSD
	}
	if o.CollabStorageBytes != nil {
		e.CollabStorageBytes = *o.CollabStorageBytes
	}
}

// LoadOverrides reads a workspace's limit overrides.
func LoadOverrides(ctx context.Context, q db.Querier, wid uuid.UUID) (Overrides, error) {
	if err := Ensure(ctx, q, wid); err != nil {
		return Overrides{}, err
	}
	var o Overrides
	err := q.QueryRow(ctx, `SELECT max_mailboxes_override, max_members_override, monthly_micro_usd_override, collab_storage_override
		FROM subscriptions WHERE workspace_id = $1`, wid).Scan(&o.MaxMailboxes, &o.MaxMembers, &o.MonthlyUSD, &o.CollabStorageBytes)
	return o, err
}

// SetOverrides replaces a workspace's limit overrides.
func SetOverrides(ctx context.Context, q db.Querier, wid uuid.UUID, o Overrides) error {
	if err := Ensure(ctx, q, wid); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE subscriptions SET max_mailboxes_override = $2, max_members_override = $3,
		monthly_micro_usd_override = $4, collab_storage_override = $5, updated_at = now() WHERE workspace_id = $1`,
		wid, o.MaxMailboxes, o.MaxMembers, o.MonthlyUSD, o.CollabStorageBytes)
	return err
}

// CheckMailbox refuses a new mailbox past the plan limit.
func (s *Service) CheckMailbox(ctx context.Context, q db.Querier, wid uuid.UUID) error {
	e, err := s.Entitlements(ctx, q, wid)
	if err != nil || e.MaxMailboxes == 0 {
		return err
	}
	if err := lock(ctx, q, wid); err != nil {
		return err
	}
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM mailboxes WHERE workspace_id = $1`, wid).Scan(&n); err != nil {
		return err
	}
	if n >= e.MaxMailboxes {
		return s.limitHit(wid, &LimitError{Resource: "mailboxes", Limit: e.MaxMailboxes})
	}
	return nil
}

// CheckMember refuses a new member or invitation past the plan limit. Pending
// invitations count, so a limit cannot be passed by inviting many people at once.
func (s *Service) CheckMember(ctx context.Context, q db.Querier, wid uuid.UUID) error {
	e, err := s.Entitlements(ctx, q, wid)
	if err != nil || e.MaxMembers == 0 {
		return err
	}
	if err := lock(ctx, q, wid); err != nil {
		return err
	}
	n, err := memberCount(ctx, q, wid, true)
	if err != nil {
		return err
	}
	if n >= e.MaxMembers {
		return s.limitHit(wid, &LimitError{Resource: "members", Limit: e.MaxMembers})
	}
	return nil
}

func memberCount(ctx context.Context, q db.Querier, wid uuid.UUID, withInvites bool) (int, error) {
	var n int
	sql := `SELECT (SELECT count(*) FROM members WHERE workspace_id = $1)`
	if withInvites {
		sql += ` + (SELECT count(*) FROM invitations WHERE workspace_id = $1 AND accepted_at IS NULL AND expires_at > now())`
	}
	err := q.QueryRow(ctx, sql, wid).Scan(&n)
	return n, err
}

// BilledSeats is the Stripe quantity for a workspace: owners, admins and agents.
// Viewers are free.
func BilledSeats(ctx context.Context, q db.Querier, wid uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1 AND role <> 'viewer'`, wid).Scan(&n)
	return max(n, 1), err
}

// Balance is a workspace's AI balance for the current period, in Micro.
type Balance struct {
	Plan         string
	Unlimited    bool
	Monthly      Micro
	IncludedUsed Micro
	Purchased    Micro
	Remaining    Micro
	PeriodStart  time.Time
	PeriodEnd    time.Time
}

func periodStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Balance returns the AI balance, rolling the monthly allowance over if a new period began.
func (s *Service) Balance(ctx context.Context, q db.Querier, wid uuid.UUID) (Balance, error) {
	e, err := s.Entitlements(ctx, q, wid)
	if err != nil {
		return Balance{}, err
	}
	if err := Ensure(ctx, q, wid); err != nil {
		return Balance{}, err
	}
	ps := periodStart(time.Now())
	c := Balance{Plan: e.Plan, Unlimited: e.UnlimitedAI, Monthly: e.MonthlyUSD, PeriodStart: ps, PeriodEnd: ps.AddDate(0, 1, 0)}
	if err := q.QueryRow(ctx, `UPDATE ai_balances SET
			included_used_micro_usd = CASE WHEN period_start < $2 THEN 0 ELSE included_used_micro_usd END,
			period_start = GREATEST(period_start, $2)
		WHERE workspace_id = $1 RETURNING included_used_micro_usd, purchased_micro_usd`, wid, ps).
		Scan(&c.IncludedUsed, &c.Purchased); err != nil {
		return Balance{}, err
	}
	c.Remaining = max(c.Monthly-c.IncludedUsed, 0) + c.Purchased
	return c, nil
}

// Usage describes one AI call for Spend.
type Usage struct {
	UserID       *uuid.UUID
	TicketID     *uuid.UUID
	Feature      string // a key of Typical
	Model        string
	InputTokens  int
	OutputTokens int
	// CostUSD is the provider's cost of the call; 0 when unknown, which charges the
	// feature's typical cost.
	CostUSD float64
	// OwnKey marks a call made on the workspace's own provider key: it is recorded
	// (source own_key) and charges nothing.
	OwnKey bool
}

// Charge is what u costs the workspace at the service's markup.
func (s *Service) Charge(u Usage) (Micro, error) {
	typical, ok := Typical[u.Feature]
	if !ok {
		return 0, fmt.Errorf("plan: unknown AI feature %q", u.Feature)
	}
	if u.CostUSD > 0 {
		return s.Price(u.CostUSD), nil
	}
	return typical, nil
}

// Price is what a provider cost in USD costs the workspace at the service's markup.
func (s *Service) Price(usd float64) Micro { return FromCost(usd, s.markup()) }

// markup is read only when billing is on: a self-hosted instance records what its
// provider charged.
func (s *Service) markup() float64 {
	if !s.enabled() || s.Markup < 1 {
		return 1
	}
	return s.Markup
}

// Reserve checks that a call can start before the model is called: the balance must
// cover the call's estimate, and never less than MinBalance. It does not spend.
func (s *Service) Reserve(ctx context.Context, q db.Querier, wid uuid.UUID, estimate Micro) error {
	c, err := s.Balance(ctx, q, wid)
	if err != nil {
		return err
	}
	if !c.Unlimited && c.Remaining < max(estimate, MinBalance) {
		return ErrNoBalance
	}
	return nil
}

// Spend charges a completed AI call on its real cost: the monthly allowance first, then
// purchased top-ups. A call that costs more than what is left takes the balance to zero.
// The row lock makes concurrent spends safe. Usage is always recorded. It returns what
// was charged. A call on the workspace's own key (u.OwnKey) is recorded and charges 0;
// callers skip Reserve for it, since no balance is needed.
func (s *Service) Spend(ctx context.Context, pool db.Querier, wid uuid.UUID, u Usage) (Micro, error) {
	cost, err := s.Charge(u)
	if err != nil {
		return 0, err
	}
	if u.OwnKey {
		_, err := pool.Exec(ctx, `INSERT INTO ai_usage (workspace_id, user_id, feature, micro_usd, model, input_tokens, output_tokens, ticket_id, cost_usd, source)
			VALUES ($1, $2, $3, 0, $4, $5, $6, $7, $8, 'own_key')`, wid, u.UserID, u.Feature, u.Model, u.InputTokens, u.OutputTokens, u.TicketID, u.CostUSD)
		return 0, err
	}
	e, err := s.Entitlements(ctx, pool, wid)
	if err != nil {
		return 0, err
	}
	charged := cost
	var before Micro
	balanced := false
	err = pgx.BeginFunc(ctx, txStarter{pool}, func(tx pgx.Tx) error {
		if !e.UnlimitedAI {
			if err := Ensure(ctx, tx, wid); err != nil {
				return err
			}
			ps := periodStart(time.Now())
			var used, purchased Micro
			if err := tx.QueryRow(ctx, `SELECT CASE WHEN period_start < $2 THEN 0 ELSE included_used_micro_usd END, purchased_micro_usd
				FROM ai_balances WHERE workspace_id = $1 FOR UPDATE`, wid, ps).Scan(&used, &purchased); err != nil {
				return err
			}
			fromIncluded := min(max(e.MonthlyUSD-used, 0), cost)
			fromPurchased := min(cost-fromIncluded, max(purchased, 0))
			charged = fromIncluded + fromPurchased
			before, balanced = max(e.MonthlyUSD-used, 0)+max(purchased, 0), true
			if _, err := tx.Exec(ctx, `UPDATE ai_balances SET period_start = GREATEST(period_start, $2),
				included_used_micro_usd = $3, purchased_micro_usd = purchased_micro_usd - $4, updated_at = now() WHERE workspace_id = $1`,
				wid, ps, used+fromIncluded, fromPurchased); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO ai_usage (workspace_id, user_id, feature, micro_usd, model, input_tokens, output_tokens, ticket_id, cost_usd)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, wid, u.UserID, u.Feature, charged, u.Model, u.InputTokens, u.OutputTokens, u.TicketID, u.CostUSD)
		return err
	})
	if err == nil && balanced && charged > 0 && s != nil && s.OnSpend != nil {
		s.OnSpend(ctx, wid, before, before-charged)
	}
	return charged, err
}

// Grant adds purchased AI usage once per external id (a Stripe checkout session or
// PaymentIntent).
func Grant(ctx context.Context, tx pgx.Tx, wid uuid.UUID, amount Micro, source, externalID string) (bool, error) {
	if err := Ensure(ctx, tx, wid); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO ai_balance_grants (workspace_id, micro_usd, source, external_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (external_id) DO NOTHING`, wid, amount, source, externalID)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE ai_balances SET purchased_micro_usd = purchased_micro_usd + $2, updated_at = now() WHERE workspace_id = $1`, wid, amount)
	return err == nil, err
}

// txStarter lets Spend open a transaction on a pool, or a savepoint inside a tx.
type txStarter struct{ q db.Querier }

func (t txStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	if b, ok := t.q.(interface {
		Begin(context.Context) (pgx.Tx, error)
	}); ok {
		return b.Begin(ctx)
	}
	return nil, errors.New("plan: querier cannot begin a transaction")
}
