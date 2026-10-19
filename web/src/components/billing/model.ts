// Pure billing helpers: money, limits, the AI balance and usage series. No React.

import type { AIBalance, AIFeature, AutoRecharge, Billing, BillingStatus, BillingUsage, PaymentMethod, Price } from "@/lib/api/types";

/** Fraction digits a currency uses (USD 2, JPY 0). Stripe amounts are in those minor units. */
export function currencyDigits(currency: string, locale = "en"): number {
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency: currency.toUpperCase() }).resolvedOptions().maximumFractionDigits ?? 2;
  } catch {
    return 2;
  }
}

/** "$19", "$19.50", "€190", "¥2,000". Whole amounts drop the decimals. */
export function formatMoney(minor: number, currency: string, locale = "en"): string {
  const digits = currencyDigits(currency, locale);
  const major = minor / 10 ** digits;
  const whole = Number.isInteger(major);
  try {
    return new Intl.NumberFormat(locale, {
      style: "currency",
      currency: currency.toUpperCase(),
      minimumFractionDigits: whole ? 0 : digits,
      maximumFractionDigits: digits,
    }).format(major);
  } catch {
    return `${major} ${currency.toUpperCase()}`;
  }
}

export function formatPrice(p: Price, locale = "en", quantity = 1): string {
  return formatMoney(p.unit_amount * quantity, p.currency, locale);
}

/** Percent saved by paying yearly, or null when the prices can't be compared. */
export function yearlySavings(month: Price | null, year: Price | null): number | null {
  if (!month || !year || month.currency !== year.currency || month.unit_amount <= 0) return null;
  const pct = Math.round((1 - year.unit_amount / (month.unit_amount * 12)) * 100);
  return pct > 0 ? pct : null;
}

export type LimitKind = "mailboxes" | "members";

export type LimitState = {
  /** The plan caps this at all. */
  limited: boolean;
  used: number;
  max: number;
  /** Creating one more would be refused. */
  atLimit: boolean;
  /** Already past the cap (after a downgrade). */
  over: boolean;
};

export function limitState(b: Billing | null | undefined, kind: LimitKind): LimitState {
  const max = b ? (kind === "mailboxes" ? b.limits.max_mailboxes : b.limits.max_members) : 0;
  const used = b ? b.usage[kind] : 0;
  const limited = !!b && max > 0;
  return { limited, used, max, atLimit: limited && used >= max, over: limited && used > max };
}

/** Rounds US dollars to the millionth, the unit AI usage is kept in, so sums add up. */
export function roundUsd(n: number): number {
  return Math.round(n * 1e6) / 1e6;
}

export type BalanceBreakdown = {
  unlimited: boolean;
  /** The monthly allowance, in US dollars. */
  monthly: number;
  /** Monthly allowance spent so far (capped at the allowance). */
  monthlyUsed: number;
  monthlyLeft: number;
  /** AI top-ups still available. */
  purchased: number;
  remaining: number;
  /** 0..1 of the allowance spent. */
  ratio: number;
  low: boolean;
};

/** Below this an AI balance reads as running low when the server gives no `low_at_usd`: $5, at most a fifth of the allowance, at least $0.05. */
export function defaultLowAt(monthly: number): number {
  return Math.max(0.05, Math.min(DEFAULT_THRESHOLD_USD, monthly / 5));
}

/** `lowAt` is Billing.auto_recharge.low_at_usd. */
export function balanceBreakdown(c: AIBalance, lowAt?: number): BalanceBreakdown {
  const monthlyUsed = Math.min(c.used_usd, c.monthly_usd);
  const monthlyLeft = roundUsd(Math.max(0, c.monthly_usd - c.used_usd));
  const ratio = c.monthly_usd > 0 ? monthlyUsed / c.monthly_usd : 0;
  return {
    unlimited: c.unlimited,
    monthly: c.monthly_usd,
    monthlyUsed,
    monthlyLeft,
    purchased: c.purchased_usd,
    remaining: c.remaining_usd,
    ratio,
    low: !c.unlimited && c.remaining_usd < (lowAt ?? defaultLowAt(c.monthly_usd)),
  };
}

// ─── Running low and auto-recharge (contracts/billing.md "Auto-recharge") ──

/** Below this balance no metered call can start (the smallest estimate): Ollie is paused. */
export const OUT_OF_BALANCE_BELOW = 0.01;
/** The AI usage the top-up price's `unit_amount` buys, when the price does not say. */
export const TOP_UP_USD = 10;
/** Auto-recharge's default threshold. */
export const DEFAULT_THRESHOLD_USD = 5;
/** One purchase or recharge adds this much AI usage, at least and at most. */
export const MIN_TOP_UP_USD = 5;
export const MAX_TOP_UP_USD = 5000;
/** The amounts offered first; any other is typed. */
export const TOP_UP_PRESETS = [10, 25, 50, 100, 250] as const;
/** The amount picked first when nothing else says. */
export const DEFAULT_TOP_UP_USD = 25;

export type BalanceAlert = "out" | "low";

/** Out (Ollie paused) or running low; null when there's nothing to say. */
export function balanceAlert(b: Billing | null | undefined): BalanceAlert | null {
  if (!b || !b.billing_enabled || b.ai.unlimited) return null;
  if (b.ai.remaining_usd < OUT_OF_BALANCE_BELOW) return "out";
  return balanceBreakdown(b.ai, b.auto_recharge?.low_at_usd).low ? "low" : null;
}

/** Auto-recharge is on with room left under its monthly limit, so a low balance refills itself. */
export function rechargeCovers(ar: AutoRecharge | null | undefined): boolean {
  return !!ar?.enabled && roundUsd(ar.monthly_limit_usd - ar.charged_this_month_usd) >= MIN_TOP_UP_USD;
}

/** The app-wide banner: shown when the AI balance is out or low and auto-recharge won't refill it. */
export function balanceBanner(b: Billing | null | undefined): BalanceAlert | null {
  const alert = balanceAlert(b);
  if (!alert || rechargeCovers(b?.auto_recharge)) return null;
  return alert;
}

/** What `usd` of AI usage costs at the top-up price: the amount in minor units, formatted. */
export function topUpQuote(topUp: Price | null | undefined, usd: number, locale = "en"): { usd: number; price: string | null; amount: number | null } {
  if (!topUp) return { usd, price: null, amount: null };
  const amount = Math.round((usd * topUp.unit_amount) / (topUp.usd ?? TOP_UP_USD));
  return { usd, price: formatMoney(amount, topUp.currency, locale), amount };
}

/** The price is dollar for dollar in US dollars, so "$50" says both what's paid and what's added. */
export function dollarForDollar(topUp: Price | null | undefined): boolean {
  return !!topUp && topUp.currency.toLowerCase() === "usd" && topUp.unit_amount === Math.round((topUp.usd ?? TOP_UP_USD) * 100);
}

/** Each recharge must lift the balance back above the threshold (else 422 recharge_too_small). */
export function rechargeTooSmall(amountUsd: number, thresholdUsd: number): boolean {
  return amountUsd <= thresholdUsd;
}

/** A typed amount in US dollars ("$1,250.50", "40"), rounded to cents; null when it isn't one. */
export function parseUsd(text: string): number | null {
  const t = text.trim().replace(/^\$/, "").replace(/[,\s]/g, "");
  if (!/^\d+(\.\d{0,2})?$/.test(t)) return null;
  return Math.round(Number(t) * 100) / 100;
}

/** A top-up amount the server accepts. */
export function validTopUp(usd: number | null): usd is number {
  return usd !== null && usd >= MIN_TOP_UP_USD && usd <= MAX_TOP_UP_USD;
}

const BRANDS: Record<string, string> = {
  visa: "Visa", mastercard: "Mastercard", amex: "American Express", discover: "Discover", diners: "Diners Club", jcb: "JCB",
  unionpay: "UnionPay", cartes_bancaires: "Cartes Bancaires", eftpos_au: "eftpos", link: "Link", sepa_debit: "SEPA Direct Debit",
  us_bank_account: "Bank account", bacs_debit: "Bacs Direct Debit",
};

/** "Visa •••• 4242", "Link". */
export function paymentMethodLabel(pm: PaymentMethod): string {
  const brand = BRANDS[pm.brand] ?? pm.brand.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase());
  return pm.last4 ? `${brand} •••• ${pm.last4}` : brand;
}

/** "08/30", or null when the method doesn't expire. */
export function paymentMethodExpiry(pm: PaymentMethod): string | null {
  if (!pm.exp_month || !pm.exp_year) return null;
  return `${String(pm.exp_month).padStart(2, "0")}/${String(pm.exp_year % 100).padStart(2, "0")}`;
}

/** The card expires before the end of next month: worth updating before a charge fails. */
export function expiresSoon(pm: PaymentMethod, now = new Date()): boolean {
  if (!pm.exp_month || !pm.exp_year) return false;
  const months = (pm.exp_year - now.getUTCFullYear()) * 12 + (pm.exp_month - (now.getUTCMonth() + 1));
  return months <= 1;
}

/** The settings with a pending patch applied (what the card shows while a save is in flight). */
export function withPatch(ar: AutoRecharge, patch: Partial<AutoRecharge>): AutoRecharge {
  const next = { ...ar, ...patch };
  if (patch.enabled) next.failure = null;
  // With auto-recharge on, low is the threshold; turning it off waits for the server's figure.
  if (next.enabled && (patch.threshold_usd !== undefined || patch.enabled)) next.low_at_usd = next.threshold_usd;
  return next;
}

const PAID_STATUSES: BillingStatus[] = ["active", "trialing", "past_due"];

/** The workspace has a Stripe subscription to manage. */
export function hasSubscription(b: Billing): boolean {
  if (!b.billing_enabled) return false;
  if (b.plan === "pro") return b.status !== "none";
  // Complimentary Collab is the collab plan without a subscription.
  return b.plan === "collab" && PAID_STATUSES.includes(b.status);
}

/** Collab is on for free: complimentary access from an instance admin, without a subscription. */
export function isComplimentary(b: Billing): boolean {
  return b.plan === "collab" && b.collab?.access === "complimentary" && !hasSubscription(b);
}

/** The Collab plan card shows: the plan is offered (or given) to the workspace, or it is on it. */
export function showCollab(b: Billing): boolean {
  return b.billing_enabled && (!!b.collab?.available || b.plan === "collab");
}

export function isSelfHosted(b: Billing): boolean {
  return b.plan === "self_hosted" || !b.billing_enabled;
}

// ─── Usage series ──────────────────────────────────────────────────────────

export const FEATURES: AIFeature[] = ["draft_reply", "answer", "copilot", "summarize", "rewrite", "translate", "triage", "kb_draft"];

/** Every day from `from` to `to` (YYYY-MM-DD, inclusive), in UTC. */
export function daysBetween(from: string, to: string): string[] {
  const out: string[] = [];
  const end = Date.parse(`${to}T00:00:00Z`);
  for (let t = Date.parse(`${from}T00:00:00Z`); t <= end && out.length < 400; t += 86_400_000) out.push(new Date(t).toISOString().slice(0, 10));
  return out;
}

export type Stack = { key: AIFeature | "other"; usd: number };
export type StackedDay = { date: string; total: number; stacks: Stack[] };

/**
 * One bar per day, stacked by the top `top` features over the range plus
 * "other". Missing days are zero. Series order follows the range totals, so
 * a feature keeps its shade across every bar.
 */
export function stackDays(u: BillingUsage, range: { from: string; to: string }, top = 3): { series: (AIFeature | "other")[]; days: StackedDay[] } {
  const ranked = [...u.by_feature].filter((f) => f.usd > 0).sort((a, b) => b.usd - a.usd);
  const head = ranked.slice(0, top).map((f) => f.feature);
  const hasOther = ranked.length > top;
  const series: (AIFeature | "other")[] = hasOther ? [...head, "other"] : head;
  const byDate = new Map(u.by_day.map((d) => [d.date, d]));
  const days = daysBetween(range.from.slice(0, 10), range.to.slice(0, 10)).map((date) => {
    const d = byDate.get(date);
    const total = d?.usd ?? 0;
    const per = d?.by_feature ?? {};
    const stacks: Stack[] = head.map((f) => ({ key: f, usd: per[f] ?? 0 }));
    if (hasOther) {
      const known = stacks.reduce((s, x) => s + x.usd, 0);
      stacks.push({ key: "other", usd: roundUsd(Math.max(0, total - known)) });
    } else if (!d?.by_feature && head.length === 1) {
      stacks[0].usd = total;
    }
    return { date, total, stacks };
  });
  return { series, days };
}
