# Billing, plans and AI usage

Billing exists only on opendesk Cloud. It is **on when `STRIPE_SECRET_KEY` is set**. Without
it (every self-hosted instance by default) a workspace reports plan `self_hosted`, has no
limits and unlimited AI (the instance brings its own AI key); usage is still recorded.
Same code, same API.

## Plans

| plan | price | mailboxes | members | AI usage / month |
| --- | --- | --- | --- | --- |
| `free` | $0, per workspace, forever | 1 | 1 | $0.50 per workspace |
| `pro` | $18 per seat per month (Stripe recurring per-seat price `STRIPE_PRICE_PRO`; optional yearly `STRIPE_PRICE_PRO_YEARLY`) | unlimited | unlimited | $10 per paid seat, pooled |
| `collab` (hidden, see "Collab") | $38 per seat per month (Stripe recurring per-seat price `STRIPE_PRICE_COLLAB`; optional yearly `STRIPE_PRICE_COLLAB_YEARLY`) | unlimited | unlimited | $20 per paid seat, pooled; plus Collab |
| `self_hosted` | free | unlimited | unlimited | unlimited (own key) |

- Every feature is on every plan. Plans differ only in the limits above and the import file
  limits below. The one deliberate
  exception is Collab (members' own mail accounts, contracts/collab.md), which only the
  `collab` plan has.
- An address's aliases (up to 20 each, contracts/teams.md "Aliases") are not mailboxes and are
  never counted: each alias has its own name, signature, team and brand on every plan.
- Seats (Stripe quantity) = members whose role is owner, admin or agent. **Viewers are free.**
  The quantity is synced to Stripe whenever members change (prorated).
- On free, "members" counts all members plus pending invitations; the owner is the 1.
- Downgrade (subscription ends): existing mailboxes and members stay; creating new ones is
  refused until the workspace is within the free limits. Nothing is deleted.
- `past_due` keeps Pro (or Collab) while Stripe retries; `unpaid`/`canceled` fall back to free.
- AI top-ups: one-time purchases of **any amount from $5 to $5,000** of AI usage, dollar for
  dollar, priced on the product of `STRIPE_PRICE_AI_TOPUP` (or the older name
  `STRIPE_PRICE_CREDITS`), a $10 price that sets the rate. They never expire and are spent
  after the monthly allowance. Available on free and pro. See "AI top-ups".
- Constants live in `internal/plan` (single source). Prices are Stripe objects, never hard-coded.

## Imports

Imports (contracts/imports.md) copy a source's attachments within the plan's file limits. Every
file is also at most 25 MB, as for any attachment, on every plan.

| plan | files per message | one message's files | all imported files in the workspace |
| --- | --- | --- | --- |
| `free` | 100 | 100 MB | 1 GB |
| `pro`, `collab` | 1,000 | 1 GB | 50 GB per paid seat, pooled |
| `self_hosted` | unlimited | unlimited | unlimited |

- `plan.Entitlements` carries them (`ImportFilesPerMessage`, `ImportMessageBytes`, `ImportBytes`; 0 =
  unlimited); the numbers live in `internal/plan`. Complimentary Collab counts billed seats.
- The workspace's total is the size of every kept file an import copied (`attachments.imported`),
  across imports and runs; undoing an import frees what it removed. Each run reads the plan again,
  so an upgrade applies to the next run of an import in progress.
- Files past a limit are left out and listed in the import's `problems`; on free the problem says a
  paid plan raises the limit.

## Collab

The `collab` plan is Pro with a larger AI allowance plus Collab (contracts/collab.md). It is
**hidden**: no workspace sees it, its prices or its card until an instance admin opens it to
that workspace (contracts/admin.md "Collab access"), as `subscriptions.collab_access`:

| access | the workspace |
| --- | --- |
| `none` (default) | cannot see or buy the collab plan; `prices.collab_month`/`collab_year` are left out of `Billing` |
| `offered` | may subscribe (`POST /v1/billing/checkout {plan: "collab"}`) or switch a Pro subscription to it (`POST /v1/billing/change-plan`) |
| `complimentary` | has the collab plan without a subscription, until `collab_until` (or until changed): plan `collab`, unlimited mailboxes and members, $20 of AI per billed seat (`plan.BilledSeats`, pooled), and Collab. It may still subscribe to pay for it |

- The plan of a Stripe subscription comes from its item's price: a collab price (monthly or
  yearly) is `collab`, any other `pro`. Seats sync, `past_due`, cancellation and the fall back to
  free work as on Pro. A plan change in the Customer Portal arrives by webhook the same way.
- `Billing.collab.available` is true when access is `offered` or `complimentary` and a collab price
  is configured. Without a collab price the plan cannot be bought (`422 plan_unavailable`).
- Removing access does not cancel a subscription already on the collab plan; the workspace just
  cannot subscribe or switch to it again. Complimentary access that ends leaves the workspace on
  its own plan (free, or its Pro subscription).
- **Collab storage**: the mail Collab stores (contracts/collab.md "Storage") is allowed 25 GB per paid
  seat, pooled (`plan.CollabStorageBytesPerSeat`; complimentary Collab counts billed seats), as
  `plan.Entitlements.CollabStorageBytes`. It is a fair-use bound, not a feature limit: history imports
  bring any amount of older mail within it and wait at it, and past twice it new mail waits at the
  provider. An instance admin's override replaces it (`0` = unlimited). Without billing it is unlimited.
- `plan.Entitlements.Collab` decides whether a workspace has Collab: the collab plan (paid or
  complimentary) with billing on; every workspace with billing off, unless `COLLAB=false`.
  Collab access is stored with billing off too, and has no effect there.

## Metered usage

AI usage is charged in **US dollars** on each call's real cost, at the instance's markup
(`AI_USAGE_MARKUP`, or the older name `AI_CREDIT_MARKUP`; default `1.5`; read only when
billing is on, so a self-hosted instance records what its provider charged):

```
charge (USD) = provider cost in USD × AI_USAGE_MARKUP, rounded up to $0.000001
```

- Amounts are kept as integer **micro-dollars** (millionths of a dollar, `plan.Micro`): balances,
  allowances, grants, usage rows and recharge amounts. The API reports US dollars as numbers
  with up to 6 decimals, in fields named `usd` or ending in `_usd`.
- The provider cost is the call's `usage.cost` from OpenRouter (chat, Jev decisions). When a
  provider reports no cost (direct Anthropic, an OpenAI-compatible server, direct TypeSafe), the cost
  is the call's tokens at the model's OpenRouter price when known, else the call is charged the
  feature's typical cost below.
- The charge depends on the model the workspace chose (`settings.ai`, contracts/ai.md "Models"):
  a pricier model, a longer thread or a longer answer costs more; a short one less.
  `GET /v1/ai/models` returns each model's price per million tokens as the workspace pays it.
- Embeddings (indexing articles and tickets) are not charged.
- **Own key** (contracts/ai.md "Provider per workspace"): a workspace that brings its own provider key
  pays its provider directly. Its LLM calls charge **$0** and are never refused for the AI balance
  (`Reserve` is skipped); each still writes a usage row with `micro_usd = 0` and
  `source = "own_key"`. Jev decisions charge $0 the same way when they run on the workspace's own
  OpenRouter key or its own TypeSafe key (contracts/ai.md "Provider per workspace"); otherwise they run
  on the instance and are charged as usual. The widget agent's
  monthly cap counts the AI balance spent, so it does not limit calls on the workspace's key.

### Starting a call

`Reserve` refuses a call (`402 ai_balance_empty`, the model is not called) when the remaining
balance is below the call's **estimate**:

```
estimate = (prompt tokens × input price + min(max output tokens, 4,000) × output price) × markup
           at least $0.01 (plan.MinBalance)
```

- Prompt tokens are counted from the prompt (about four characters a token, tool definitions
  included); max output tokens is the call's limit, counted up to 4,000. Prices are the chosen
  model's, so a pricier model needs more balance to start.
- When the model's price is unknown, the estimate is the feature's typical cost below.
- Jev decisions are estimated at the feature's typical cost.
- If a call ends up costing more than the remaining balance, the balance goes to $0 and the
  rest is not charged.

### Typical costs

What a call typically costs with the default models (`anthropic/claude-sonnet-5.5`,
`anthropic/claude-haiku-5.5` for the fast tier) and a typical thread. It is charged only when
neither the provider nor the model's price gives the cost, and it is the estimate for a model
whose price is unknown. `GET /v1/ai/config` `costs` prices the same typical calls on the
workspace's chosen models.

| feature | key | typical cost |
| --- | --- | --- |
| Summarize a ticket | `summarize` | $0.01 |
| Rewrite (tone, length, fix grammar) | `rewrite` | $0.01 |
| Translate a message or draft | `translate` | $0.01 |
| Auto-triage (tags, priority, severity, team, sentiment, language): one call, every question batched | `triage` | $0.01 |
| `ai_decide` automation conditions (all of one trigger run in one call) | `decide` | $0.01 |
| Widget agent relevance check (Jev) | `answer_check` | $0.001 |
| Sort new Collab mail into categories (up to 10 conversations in one call, contracts/collab.md "Categories") | `mail_categorize` | $0.002 |
| Suggest categories for an inbox | `mail_categories` | $0.01 |
| Ask about a ticket (copilot) | `copilot` | $0.02 |
| Draft a reply | `draft_reply` | $0.03 |
| Widget agent reply (each model turn of a reply, tool turns included) | `answer` | $0.03 |
| Agent email answer or suggested reply (each model turn, tool turns included) | `answer_email` | $0.03 |
| Agent playground reply, and each agent test answered or graded in a test run | `playground` | $0.03 |
| Draft a KB article from tickets | `kb_draft` | $0.05 |
| Ollie, the assistant (each model turn of a run) | `assistant` | $0.02 |
| A teammate's Ollie answering an ask (one call, paid by the workspace, attributed to the asker) | `assistant_peer` | $0.01 |

A typical call's size (`internal/ai` `typicalTokens`, input / output tokens): summarize 3,000 / 300,
rewrite 800 / 400, translate 800 / 500, triage 1,500 / 150, decide 1,500 / 100, copilot 4,000 / 400,
draft_reply 4,000 / 500, answer 5,000 / 400, answer_email 5,000 / 500, playground 5,000 / 400,
kb_draft 8,000 / 1,500, assistant 6,000 / 400, assistant_peer 4,000 / 400, mail_categorize 3,000 / 100,
mail_categories 3,000 / 500. A model's `typical_reply_usd` is a draft reply of this size.

Jev decisions cost a fraction of an LLM call (per input token, output free), so triage and the
widget relevance check use only a little. Usage rows record decision calls with model `jev`.
Usage rows (`ai_usage`) store the feature, model, token counts, the amount charged (`micro_usd`), the
provider cost in USD and `source` (`instance`, or `own_key` for calls on the workspace's own key); the
provider cost is internal and never returned by the API.

Go API (`internal/plan`, `d.Plan`):
- `d.Plan.Entitlements(ctx, q, wid)`, `d.Plan.CheckMailbox`, `d.Plan.CheckMember` (return
  `*plan.LimitError` → `httpx.Wrap` → `402 plan_limit`).
- `d.Plan.Reserve(ctx, q, wid, estimate)` before a model call (`plan.ErrNoBalance` → `402 ai_balance_empty`),
  then `d.Plan.Spend(ctx, pool, wid, plan.Usage{..., CostUSD})` after it succeeds. Failed calls cost nothing.
  A call on the workspace's own key skips `Reserve` and spends with `plan.Usage{..., OwnKey: true}`:
  recorded (`source = "own_key"`), $0, balance untouched. Features go through `internal/ai`, which
  computes the estimate and decides this per workspace (`ai.Service.Reserve` for callers outside it,
  such as the assistant, estimates a typical call of the feature on the workspace's model).
- `plan.Grant(ctx, tx, wid, amount, source, externalID)` is idempotent per external id.

## Endpoints (owner area: billing)

| method | path | who | notes |
| --- | --- | --- | --- |
| GET | `/v1/billing` | member | `Billing` below |
| POST | `/v1/billing/checkout` | owner/admin | `{interval: "month"\|"year", plan?: "pro"\|"collab"}` (default pro) → `{url}` Stripe Checkout (subscription, quantity = billed seats, a business purchase (see "Tax"), promotion codes, `client_reference_id` = workspace id, `metadata.plan`). `collab` needs Collab access offered or complimentary (`403 plan_unavailable`) |
| POST | `/v1/billing/change-plan` | owner/admin | `{plan: "pro"\|"collab"}` → `Billing`: swaps an active, trialing or past_due subscription's price to the other plan's price of the same interval, same seats, prorated (`create_prorations`, Stripe idempotency key). `409 no_subscription`, `409 already_on_plan`; `collab` needs access offered or complimentary (`403 plan_unavailable`) |
| POST | `/v1/billing/portal` | owner/admin | `{url}` Stripe Customer Portal (payment method, invoices, cancel, tax ids) |
| POST | `/v1/billing/top-up` | owner/admin | `{amount_usd: 5..5000}` → `TopUpResult`: buys AI usage now with the saved payment method. See "AI top-ups" |
| POST | `/v1/billing/top-up/checkout` | owner/admin | `{amount_usd: 5..5000}` (or the older `{top_ups: 1..20}`, $10 each) → `{url}` one-time Checkout for AI usage, a business purchase; saves the card for off-session use (`setup_future_usage: off_session`) |
| PATCH | `/v1/billing/auto-recharge` | owner/admin | `{enabled?, threshold_usd?: 0.01..1000, amount_usd?: 5..5000, monthly_limit_usd?: 5..50000}` (US dollars, rounded to cents) → `Billing`. See "Auto-recharge" |
| POST | `/v1/billing/payment-method` | owner/admin | `{url}` Stripe Checkout in setup mode: saves a card and makes it the customer's default. Returns to `?payment_method=success\|canceled` |
| GET | `/v1/billing/invoices` | owner/admin | `{data: Invoice[]}`: the 24 most recent invoices, newest first. See "Payment method and invoices" |
| GET | `/v1/billing/usage` | admin | `?from&to` → AI usage by feature, by day and by member; also on self-hosted |
| POST | `/v1/billing/stripe/webhook` | Stripe | signature checked with `STRIPE_WEBHOOK_SECRET`; idempotent via `stripe_events` |

All billing routes answer `404 billing_disabled` on an instance without Stripe, except
`GET /v1/billing`, which returns the self-hosted entitlements, and `GET /v1/billing/usage`.

```ts
interface Billing {
  plan: "free" | "pro" | "collab" | "self_hosted"   // collab: the collab plan, paid or complimentary
  billing_enabled: boolean
  status: "none" | "trialing" | "active" | "past_due" | "unpaid" | "canceled" | "incomplete" | "incomplete_expired" | "paused"
  interval: "month" | "year"
  seats: number            // billed seats in Stripe
  billed_seats: number     // current owners+admins+agents (what the next sync will set)
  current_period_end: string | null
  cancel_at_period_end: boolean
  limits: { max_mailboxes: number; max_members: number }   // 0 = unlimited
  usage: { mailboxes: number; members: number }
  ai: AIBalance
  prices: { pro_month: Price | null; pro_year: Price | null; ai_top_up: Price | null
    collab_month?: Price; collab_year?: Price }  // read from Stripe, cached; collab_* only with Collab access offered or complimentary
  collab: {
    access: "none" | "offered" | "complimentary"  // an instance admin's Collab access (see "Collab")
    until: string | null   // complimentary access ends then; null: until changed
    enabled: boolean       // the workspace has Collab now (plan.Entitlements.Collab)
    available: boolean     // it may subscribe or switch to collab: access offered or complimentary, and a collab price is configured
  }
  auto_recharge: AutoRecharge | null   // null when billing is disabled
  payment_method: PaymentMethod | null // what top-ups and auto-recharge charge; null when none is saved
}
// Every *_usd value is US dollars, a number with up to 6 decimals.
interface AIBalance {
  unlimited: boolean         // self-hosted: no allowance, nothing refused
  monthly_usd: number        // the monthly AI usage allowance ($0.50 free; $10 × paid seats on pro; $20 × paid seats on collab)
  used_usd: number           // used of the allowance this period (with unlimited AI: all usage this period)
  purchased_usd: number      // AI usage bought and not yet spent; never expires, spent after the allowance
  remaining_usd: number      // what is left of the allowance + purchased_usd
  period_end: string         // the allowance resets at the start of each calendar month, UTC
}
interface AutoRecharge {
  enabled: boolean           // off by default
  threshold_usd: number      // recharge when the AI balance falls below it (default 5)
  amount_usd: number         // AI usage each recharge adds, 5..5000 (default 25)
  monthly_limit_usd: number  // most recharged per calendar month, UTC, 5..50000 (default 250)
  charged_this_month_usd: number  // recharged this month, paid or still being paid
  recharges_this_month: number
  low_at_usd: number         // an AI balance below this reads as running low (see below)
  last_recharge: { at: string; usd: number /* AI usage added */; amount: number; currency: string } | null  // amount charged, in minor units
  retry: { at: string; code: "card_declined" } | null  // a recharge the bank declined for now, tried again at `at`
  failure: { code: "card_declined" | "authentication_required" | "no_payment_method" | "payment_failed" | "unavailable"; at: string } | null
}
interface Price { id: string; unit_amount: number; currency: string; interval: "month" | "year" | null; usd?: number /* ai_top_up: the AI usage unit_amount buys (10) */ }
interface PaymentMethod { brand: string /* visa, mastercard, ... or a type: link, sepa_debit */; last4: string; exp_month?: number; exp_year?: number; wallet?: string /* apple_pay, google_pay, link */ }
interface TopUpResult { status: "paid" | "processing"; usd: number; billing: Billing }
interface Invoice {
  id: string; number: string; created: string
  total: number; currency: string          // minor units
  status: "paid" | "open" | "uncollectible"
  kind: "subscription" | "top_up" | "auto_recharge" | "other"
  description: string
  url: string        // Stripe's page for the invoice: view, download or pay it
  pdf_url: string
}
```

Stripe webhook events handled: `checkout.session.completed`, `checkout.session.async_payment_succeeded`,
`customer.subscription.created`, `customer.subscription.updated`, `customer.subscription.deleted`,
`invoice.paid` (the subscription, and charges on the saved payment method: `metadata.charge_id`),
`invoice.payment_failed` (the subscription only), `customer.updated`, `payment_method.attached`,
`payment_method.detached`, `payment_method.updated` and `payment_method.automatically_updated` (they
refresh `payment_method`). Each change publishes `billing.updated` (agents visibility, data = `Billing`)
and is audit-logged (`billing.subscribed`, `billing.canceled`, `billing.plan_changed` (`{plan, from, ...}`, between
pro and collab), `billing.ai_top_up_purchased` (`{usd}`),
`billing.ai_recharged` (`{usd}`), `billing.auto_recharge_updated` (with the changed fields),
`billing.auto_recharge_failed` (with `reason`)).

```ts
// GET /v1/billing/usage?from&to (RFC 3339 or YYYY-MM-DD; default this month, UTC; at most a year).
// A YYYY-MM-DD `to` includes that day; an RFC 3339 `to` is exclusive. The response `from`/`to` are RFC 3339 (`to` exclusive).
interface UsageReport {
  from: string; to: string; total_usd: number; total_calls: number
  by_feature: { feature: string; usd: number; calls: number }[]
  by_day: { date: string /* YYYY-MM-DD UTC */; usd: number; calls: number; by_feature: Record<string, number> /* usd */ }[] // days with usage only
  by_member: { user: UserRef | null /* null: widget agent, automations */; usd: number; calls: number }[]
  by_model: { model: string; usd: number; calls: number; input_tokens: number; output_tokens: number; source: "instance" | "own_key" }[] // one row per model and source
  own_key_calls: number // calls on the workspace's own provider key ($0 each)
}
// Every usd value is US dollars, a number with up to 6 decimals.
```

- Checkout returns to `APP_URL/settings/billing?checkout=success|canceled`, AI top-ups to
  `?top_up=success|canceled`. The plan changes when the webhook lands, not on the redirect:
  wait for `billing.updated` (or refetch `GET /v1/billing`).
- `billed_seats` changes with members; the Stripe quantity (`seats`) follows within seconds
  (debounced, prorated) and is reconciled hourly.
- Deleting a workspace cancels its subscription immediately.
- Mailbox OAuth connect past the limit redirects to `/settings/mailboxes?error=plan_limit`.

## AI top-ups

Any amount from $5 to $5,000 of AI usage, rounded to cents, charged dollar for dollar at the
`STRIPE_PRICE_AI_TOPUP` rate (its `unit_amount` buys `Price.usd`, $10) and priced on its product.

- **With the saved payment method** (`POST /v1/billing/top-up`): the buyer is present, so the
  bank may ask them to confirm. The charge is an invoice (below), paid at once.
  `402 authentication_required` means the bank wants the buyer to confirm: the web app carries
  on in Stripe Checkout with the same amount. `402 card_declined`: nothing was charged and the
  invoice is voided. `409 payment_method_required` without a saved card.
- **In Stripe Checkout** (`POST /v1/billing/top-up/checkout`): a business purchase that saves
  the card. The AI usage is granted on `checkout.session.completed` (or
  `async_payment_succeeded`) when paid, from `metadata.micro_usd`, once per session.

### Charges on the saved payment method

Top-ups paid in the app and auto-recharges are Stripe invoices that billing creates on the
customer (`collection_method: charge_automatically`, `auto_advance: false`, pending invoice
items excluded), with one line on the top-up product, then finalizes and pays with the saved
payment method. So every charge has an invoice with the customer's business details, and none
is a bare payment. The invoice metadata is `{workspace_id, kind: "top_up" | "auto_recharge",
charge_id, micro_usd}`; `charge_id` is the `ai_auto_recharges` row, and every Stripe request
for it carries an idempotency key derived from it, so a retried request or job never creates
or pays anything twice. The AI usage is granted with `plan.Grant(..., invoice id)` by
whichever sees the invoice paid first: the request or job that paid it, or `invoice.paid`.
An unpaid invoice is voided when the charge is given up, so nobody pays it later.

## Payment method and invoices

`Billing.payment_method` is the payment method charges use: the customer's default, else
the most recently saved one. Billing keeps only its brand, last four digits, expiry and
wallet, refreshed by the River job `billing.sync_payment_method`
(`jobs.SyncPaymentMethodArgs{workspace_id}`) after every completed Checkout and every
`customer.updated` or `payment_method.*` event, which includes the card networks' automatic
updates. The job also makes a saved card the customer's default when it has none.

`GET /v1/billing/invoices` lists the 24 most recent invoices from Stripe, newest first:
the subscription, top-ups and auto-recharges. Drafts and voided invoices (a declined charge)
are left out. The Customer Portal has the full history.

## Tax

opendesk Cloud is sold to businesses for business use (the terms at opendesk.sh/terms say
so, and that prices exclude taxes). Every Checkout that takes money (the subscription and
AI top-ups) requires the company's legal name and billing address and collects its tax ID.
They are saved on the Stripe customer, so they appear on every invoice. Tax is not computed at checkout:
prices carry `tax_behavior: exclusive`, and Stripe Tax stays off.

## Auto-recharge

Off by default. When on, a charged AI call that leaves the workspace's AI balance below
`threshold_usd` (default $5) queues the River job `billing.auto_recharge`
(`jobs.AutoRechargeArgs{workspace_id}`, unique per workspace, so one runs at a time). The job:

- does nothing when the balance is back at or above the threshold, or when the monthly limit
  has less than $5 left: recharges paid or pending this month (UTC) count against
  `monthly_limit_usd`. Near the limit a recharge adds only what the limit has left;
- charges `amount_usd` off-session as an invoice (see "Charges on the saved payment method")
  and grants it with `plan.Grant(..., "stripe_auto_recharge", invoice id)`;
- on a **soft decline** (insufficient funds, try again later, the issuer unavailable, a
  processing error) keeps auto-recharge on and tries the same invoice again after 1 hour, 6
  hours and 18 hours, on the payment method saved by then. `retry` says when. After the last try
  it fails as below.

Defaults: add $25 when the balance falls below $5, at most $250 a month. Each recharge must
bring the balance back above the threshold (`amount_usd > threshold_usd`) and fit the limit
(`monthly_limit_usd >= amount_usd`).

When a payment fails for good (card declined, authentication required, no saved payment method,
AI top-ups unavailable), the invoice is voided, auto-recharge turns itself off and records
`failure`; owners and admins get an `auto_recharge_failed` notification (emailed by their
notification preferences, on by default; `contracts/api.md` "Notification preferences and emails");
it is audit-logged and `billing.updated` is published. Turning it back on clears `failure`; if the
balance is already below the threshold, a recharge starts right away.

Turning it on needs a saved payment method: `409 payment_method_required` otherwise (the web app
offers "Add a payment method", which opens `POST /v1/billing/payment-method`). Top-up Checkouts
save the card (`setup_future_usage: off_session`), subscription Checkouts save it with the
subscription, and after any completed Checkout the saved card becomes the customer's default when it
has none.

Running low (auto-recharge off, or at its monthly limit):

- **low**: the AI balance falls below `low_at_usd` = the threshold, but never more than a fifth of
  the monthly allowance (at least $0.05); with auto-recharge on, `low_at_usd` is the threshold.
  Owners and admins get one `ai_balance_low` notification (`data {remaining_usd}`).
- **out**: the AI balance falls below $0.01 (`plan.MinBalance`, the smallest estimate of any call:
  no metered call can start; the widget agent goes silent). One `ai_balance_out` notification
  (`data {remaining_usd}`), emailed like any notification (on by default).
- Each is sent once a month at most; any grant (top-up, recharge) re-arms both.

Errors: `402 plan_limit` (`detail` names the limit), `402 ai_balance_empty` (the AI balance does not
cover the call's estimate, "Starting a call"), `404 billing_disabled`,
`409 already_subscribed` (checkout while on a paid plan: use the portal, or change plan), `409 no_billing_account`
(portal before any purchase), `422 interval_unavailable` (no yearly price),
`403 plan_unavailable` (the collab plan is not offered to the workspace), `422 plan_unavailable` (no collab
price configured), `409 no_subscription` and `409 already_on_plan` (change plan),
`422 top_up_unavailable` (no AI top-up price configured), `409 payment_method_required` (turning
auto-recharge on, or a top-up with the saved card, without a saved payment method),
`402 card_declined` and `402 authentication_required` (a top-up with the saved card, see
"AI top-ups"), `422 recharge_too_small` (a recharge would not lift the balance above the
threshold), `422 monthly_limit_too_small` (the monthly limit is below one recharge),
`502 billing_unavailable` (Stripe unreachable).
The web app shows an upgrade prompt for both 402 codes.
