---
title: Billing
description: Plans, seats, AI usage in US dollars, AI top-ups, auto-recharge and Stripe checkout on opendesk Cloud.
---

**Cloud only.** Billing exists only on opendesk Cloud. See [Cloud plans and billing](/guides/billing/). On an instance without Stripe configured, every route below returns `404 billing_disabled`, except `GET /v1/billing`, which returns the self-hosted entitlements (plan `self_hosted`, no limits and unlimited AI), and `GET /v1/billing/usage`.

| Method | Path | Who | Description |
| --- | --- | --- | --- |
| `GET` | `/v1/billing` | member | The workspace's plan, limits, usage and AI balance |
| `POST` | `/v1/billing/checkout` | owner, admin | `{ interval: "month" \| "year" }` → `{ url }` to Stripe Checkout |
| `POST` | `/v1/billing/portal` | owner, admin | `{ url }` to the Stripe customer portal |
| `POST` | `/v1/billing/top-up` | owner, admin | `{ amount_usd: 5..5000 }` → `TopUpResult`: adds AI usage now, charged to the saved payment method. See [AI top-ups](#ai-top-ups) |
| `POST` | `/v1/billing/top-up/checkout` | owner, admin | `{ amount_usd: 5..5000 }` → `{ url }` to a one-time Checkout for AI usage |
| `PATCH` | `/v1/billing/auto-recharge` | owner, admin | `{ enabled?, threshold_usd?, amount_usd?, monthly_limit_usd? }` → the billing object. See [Auto-recharge](#auto-recharge) |
| `POST` | `/v1/billing/payment-method` | owner, admin | `{ url }` to Stripe Checkout in setup mode, to save a card |
| `GET` | `/v1/billing/invoices` | owner, admin | `{ data: Invoice[] }`: the 24 most recent invoices. See [Invoices](#invoices) |
| `GET` | `/v1/billing/usage?from=&to=` | admin | AI usage by feature, day, member and model; also on self-hosted |

Checkout returns to `/settings/billing?checkout=success|canceled`, an AI top-up to `?top_up=success|canceled`, and saving a card to `?payment_method=success|canceled`. The plan and balance change when Stripe confirms the payment, not on the redirect: wait for `billing.updated`, or fetch `GET /v1/billing` again.

## The billing object

Every amount ending in `_usd`, or named `usd`, is US dollars, a number with up to 6 decimals.

```ts
interface Billing {
  plan: "free" | "pro" | "self_hosted";
  billing_enabled: boolean;
  status: "none" | "trialing" | "active" | "past_due" | "unpaid" | "canceled" | "incomplete" | "incomplete_expired" | "paused";
  interval: "month" | "year";
  seats: number;          // billed seats in Stripe
  billed_seats: number;   // current owners, admins and agents
  current_period_end: string | null;
  cancel_at_period_end: boolean;
  limits: { max_mailboxes: number; max_members: number };   // 0 = unlimited
  usage: { mailboxes: number; members: number };
  ai: {
    unlimited: boolean;       // self-hosted: no allowance, nothing refused
    monthly_usd: number;      // the monthly allowance: $0.50 on Free, $10 per paid seat on Pro
    used_usd: number;         // used of the allowance this period
    purchased_usd: number;    // AI usage added and not yet spent; it never expires and is spent after the allowance
    remaining_usd: number;    // what is left of the allowance, plus purchased_usd
    period_end: string;       // the allowance resets at the start of each calendar month, UTC
  };
  prices: { pro_month: Price | null; pro_year: Price | null; ai_top_up: Price | null };
  auto_recharge: AutoRecharge | null;   // null when billing is off
  payment_method: PaymentMethod | null; // what top-ups and auto-recharge charge; null when none is saved
}

interface Price {
  id: string;
  unit_amount: number;      // in minor units, such as cents
  currency: string;
  interval: "month" | "year" | null;
  usd?: number;             // ai_top_up: the AI usage unit_amount buys (10)
}

interface PaymentMethod {
  brand: string;            // visa, mastercard, amex, ..., or a type such as link or sepa_debit
  last4: string;
  exp_month?: number;
  exp_year?: number;
  wallet?: string;          // apple_pay, google_pay or link, when paid through one
}

interface AutoRecharge {
  enabled: boolean;                // off by default
  threshold_usd: number;           // recharge when the AI balance falls below this (default 5)
  amount_usd: number;              // AI usage each recharge adds, 5..5000 (default 25)
  monthly_limit_usd: number;       // the most added per calendar month, UTC, 5..50000 (default 250)
  charged_this_month_usd: number;  // recharged this month, paid or still being paid
  recharges_this_month: number;
  low_at_usd: number;              // a balance below this reads as running low
  last_recharge: { at: string; usd: number; amount: number; currency: string } | null;  // amount charged, in minor units
  retry: { at: string; code: "card_declined" } | null;  // a declined recharge that is tried again at `at`
  failure: { code: "card_declined" | "authentication_required" | "no_payment_method" | "payment_failed" | "unavailable"; at: string } | null;
}
```

## Plans and limits

- Every feature is on every plan. Plans differ only in limits.
- Seats are members with the owner, admin or agent role. **Viewers are free.**
- When a limit is reached, creating another mailbox or member returns `402 plan_limit`, with the limit named in `detail`. Nothing existing is removed.

## AI usage

- AI usage is charged in US dollars on each call's real cost: the provider's cost at the model's list price, times 1.5 on opendesk Cloud, kept to $0.000001. The charge depends on the model the workspace chose (see [Models](/api/resources/ai/#models)) and on the size of the request.
- A call starts only when the balance covers its estimate: the prompt tokens at the model's input price plus the call's maximum output tokens (counted up to 4,000) at its output price, times the markup, and at least $0.01. Otherwise the route returns `402 ai_balance_empty` and the model is not called. If a call ends up costing more than what is left, the balance goes to $0.
- The allowance is spent first, then AI usage added with top-ups, which never expires. A failed call costs nothing. Search indexing is free.
- Calls on the workspace's own provider key cost $0 and are never refused for the balance. Jev decisions are charged unless they run on the workspace's own OpenRouter or TypeSafe key ([AI](/api/resources/ai/)).

## AI top-ups

A top-up adds any amount from $5 to $5,000 of AI usage, rounded to cents, dollar for dollar. Every payment gets an invoice. `422 top_up_unavailable` means the instance offers no AI top-ups.

With the saved payment method, `POST /v1/billing/top-up` with `{ "amount_usd": 40 }` charges it now:

```ts
interface TopUpResult {
  status: "paid" | "processing";  // processing: the payment method settles later; billing.updated follows
  usd: number;
  billing: Billing;
}
```

- `402 authentication_required`: the bank wants the buyer to confirm the payment. Nothing was charged; open a Checkout for the same amount instead.
- `402 card_declined`: nothing was charged. Try another card.
- `409 payment_method_required`: there is no saved payment method.

`POST /v1/billing/top-up/checkout` with `{ "amount_usd": 40 }` opens a one-time Checkout instead, which saves the card for later top-ups and auto-recharge. The older `{ "top_ups": 4 }`, $10 each, still works.

## Auto-recharge

Off by default. When on, a charged AI call that leaves the balance below `threshold_usd` adds `amount_usd` of AI usage, charged to the saved payment method with an invoice, until `monthly_limit_usd` has been added in the calendar month (UTC). Near the limit a recharge adds only what is left of it, if that is at least $5. The defaults add $25 when the balance falls below $5, at most $250 a month.

`PATCH /v1/billing/auto-recharge` takes:

| Field | Values |
| --- | --- |
| `enabled` | `true` or `false` |
| `threshold_usd` | 0.01 to 1,000, rounded to cents |
| `amount_usd` | 5 to 5,000, rounded to cents |
| `monthly_limit_usd` | 5 to 50,000, rounded to cents |

- `409 payment_method_required`: turning it on needs a saved card. `POST /v1/billing/payment-method` saves one.
- `422 recharge_too_small`: each recharge must bring the balance back above the threshold, so `amount_usd` must be more than `threshold_usd`.
- `422 monthly_limit_too_small`: `monthly_limit_usd` must be at least `amount_usd`.
- When the bank declines a recharge for now (insufficient funds, try again later), auto-recharge stays on, sets `retry`, and tries again after 1 hour, 6 hours and 18 hours, with the payment method saved by then.
- When a payment fails for good, or after the last retry, auto-recharge turns itself off and records `failure`. Owners and admins get an `auto_recharge_failed` notification and an email. Turning it back on clears `failure`, and recharges right away if the balance is below the threshold.

Owners and admins get an `ai_balance_low` notification (`{ remaining_usd }`) when the balance falls below `low_at_usd`: the threshold, but never more than a fifth of the monthly allowance (at least $0.05). They get `ai_balance_out` (`{ remaining_usd }`), and an email, when it falls below $0.01. Each is sent at most once a month, and any top-up or recharge resets both.

## Invoices

`billing.payment_method` is the card, or other payment method, that top-ups and auto-recharge charge. It follows the customer's default in Stripe, including the card networks' automatic updates; `POST /v1/billing/payment-method` saves another one.

`GET /v1/billing/invoices` lists the 24 most recent invoices, newest first. Drafts and voided invoices are left out; the customer portal has the full history.

```ts
interface Invoice {
  id: string;
  number: string;
  created: string;
  total: number;            // in minor units
  currency: string;
  status: "paid" | "open" | "uncollectible";
  kind: "subscription" | "top_up" | "auto_recharge" | "other";
  description: string;
  url: string;              // Stripe's page to view, download or pay it
  pdf_url: string;
}
```

## Usage report

`GET /v1/billing/usage?from=&to=` (RFC 3339 or `YYYY-MM-DD`; this month by default, at most a year). A `YYYY-MM-DD` `to` includes that day.

```ts
interface UsageReport {
  from: string;
  to: string;               // exclusive
  total_usd: number;
  total_calls: number;
  by_feature: { feature: string; usd: number; calls: number }[];
  by_day: { date: string; usd: number; calls: number; by_feature: Record<string, number> }[];   // days with usage only; by_feature in US dollars
  by_member: { user: UserRef | null; usd: number; calls: number }[];   // null: the AI agent and automations
  by_model: { model: string; usd: number; calls: number; input_tokens: number; output_tokens: number; source: "instance" | "own_key" }[];
  own_key_calls: number;    // calls on the workspace's own provider key, $0 each
}
```

Changes publish a `billing.updated` event with the billing object.
