import { describe, expect, it } from "vitest";
import type { Billing, BillingUsage } from "@/lib/api/types";
import type { AutoRecharge } from "@/lib/api/types";
import {
  balanceAlert,
  balanceBanner,
  balanceBreakdown,
  daysBetween,
  defaultLowAt,
  dollarForDollar,
  expiresSoon,
  formatMoney,
  hasSubscription,
  isComplimentary,
  limitState,
  parseUsd,
  paymentMethodExpiry,
  paymentMethodLabel,
  rechargeTooSmall,
  roundUsd,
  showCollab,
  stackDays,
  topUpQuote,
  validTopUp,
  withPatch,
  yearlySavings,
} from "./model";

const price = (unit_amount: number, interval: "month" | "year" | null, currency = "usd") => ({ id: "p", unit_amount, currency, interval });

describe("money", () => {
  it("formats minor units and drops whole decimals", () => {
    expect(formatMoney(1900, "usd")).toBe("$19");
    expect(formatMoney(1950, "usd")).toBe("$19.50");
    expect(formatMoney(2000, "jpy")).toBe("¥2,000");
    expect(formatMoney(19000, "eur", "de")).toMatch(/190\s?€/);
  });

  it("computes yearly savings", () => {
    expect(yearlySavings(price(1900, "month"), price(19000, "year"))).toBe(17);
    expect(yearlySavings(price(1900, "month"), price(22800, "year"))).toBeNull();
    expect(yearlySavings(price(1900, "month"), null)).toBeNull();
    expect(yearlySavings(price(1900, "month"), price(19000, "year", "eur"))).toBeNull();
  });
});

const billing = (over: Partial<Billing> = {}): Billing => ({
  plan: "free",
  billing_enabled: true,
  status: "none",
  interval: "month",
  seats: 0,
  billed_seats: 1,
  current_period_end: null,
  cancel_at_period_end: false,
  limits: { max_mailboxes: 1, max_members: 1 },
  usage: { mailboxes: 1, members: 2 },
  ai: { unlimited: false, monthly_usd: 0.5, used_usd: 0.38, purchased_usd: 0, remaining_usd: 0.12, period_end: "2026-11-01T00:00:00Z" },
  prices: { pro_month: null, pro_year: null, ai_top_up: null },
  ...over,
});

describe("limits", () => {
  it("reports at and over the cap", () => {
    expect(limitState(billing(), "mailboxes")).toMatchObject({ limited: true, atLimit: true, over: false });
    expect(limitState(billing(), "members")).toMatchObject({ limited: true, atLimit: true, over: true });
  });
  it("treats 0 as unlimited", () => {
    expect(limitState(billing({ limits: { max_mailboxes: 0, max_members: 0 } }), "members")).toMatchObject({ limited: false, atLimit: false });
    expect(limitState(undefined, "members").limited).toBe(false);
  });
});

describe("AI balance", () => {
  it("splits the allowance from top-ups", () => {
    const c = balanceBreakdown({ unlimited: false, monthly_usd: 10, used_usd: 12, purchased_usd: 18, remaining_usd: 18, period_end: "" });
    expect(c).toMatchObject({ monthlyUsed: 10, monthlyLeft: 0, ratio: 1, purchased: 18, low: false });
    expect(balanceBreakdown(billing().ai).low).toBe(false);
    // Free: low below a fifth of the $0.50 allowance.
    expect(balanceBreakdown({ ...billing().ai, remaining_usd: 0.09 }).low).toBe(true);
  });

  it("keeps micro-dollar sums exact", () => {
    expect(balanceBreakdown({ unlimited: false, monthly_usd: 0.5, used_usd: 0.3, purchased_usd: 0, remaining_usd: 0.2, period_end: "" }).monthlyLeft).toBe(0.2);
    expect(roundUsd(0.1 + 0.2)).toBe(0.3);
    expect(defaultLowAt(0.5)).toBe(0.1);
    expect(defaultLowAt(30)).toBe(5);
    expect(defaultLowAt(0)).toBe(0.05);
  });
});

describe("usage series", () => {
  it("fills days and stacks the top features plus other", () => {
    const u: BillingUsage = {
      from: "2026-10-01T00:00:00Z",
      to: "2026-10-04T00:00:00Z",
      total_usd: 0.2,
      total_calls: 9,
      by_feature: [
        { feature: "draft_reply", usd: 0.09, calls: 3 },
        { feature: "summarize", usd: 0.05, calls: 5 },
        { feature: "copilot", usd: 0.04, calls: 2 },
        { feature: "rewrite", usd: 0.02, calls: 2 },
      ],
      by_day: [
        { date: "2026-10-01", usd: 0.12, calls: 5, by_feature: { draft_reply: 0.06, summarize: 0.03, copilot: 0.02, rewrite: 0.01 } },
        { date: "2026-10-03", usd: 0.08, calls: 4, by_feature: { draft_reply: 0.03, summarize: 0.02, copilot: 0.02, rewrite: 0.01 } },
      ],
      by_member: [],
    };
    const { series, days } = stackDays(u, { from: "2026-10-01", to: "2026-10-03" });
    expect(series).toEqual(["draft_reply", "summarize", "copilot", "other"]);
    expect(days.map((d) => d.date)).toEqual(["2026-10-01", "2026-10-02", "2026-10-03"]);
    expect(days[0].stacks.map((s) => s.usd)).toEqual([0.06, 0.03, 0.02, 0.01]);
    expect(days[1].total).toBe(0);
  });

  it("lists every day inclusive", () => {
    expect(daysBetween("2026-02-27", "2026-03-01")).toEqual(["2026-02-27", "2026-02-28", "2026-03-01"]);
  });
});

const ar = (over: Partial<AutoRecharge> = {}): AutoRecharge => ({
  enabled: false,
  threshold_usd: 2,
  amount_usd: 25,
  monthly_limit_usd: 100,
  charged_this_month_usd: 0,
  recharges_this_month: 0,
  low_at_usd: 0.1,
  last_recharge: null,
  retry: null,
  failure: null,
  ...over,
});
const topUp = { id: "price_ai_top_up", unit_amount: 1000, currency: "usd", interval: null, usd: 10 };

describe("running low", () => {
  const ai = (remaining_usd: number) => ({ ...billing().ai, remaining_usd });

  it("reads out below $0.01 and low below low_at_usd", () => {
    expect(balanceAlert(billing({ ai: ai(0.009), auto_recharge: ar() }))).toBe("out");
    expect(balanceAlert(billing({ ai: ai(0.09), auto_recharge: ar() }))).toBe("low");
    expect(balanceAlert(billing({ ai: ai(0.1), auto_recharge: ar() }))).toBeNull();
    // Without auto_recharge: $2, at most a fifth of the allowance.
    expect(balanceAlert(billing({ ai: ai(0.05) }))).toBe("low");
    expect(balanceAlert(billing({ ai: { ...ai(0), unlimited: true } }))).toBeNull();
    expect(balanceAlert(billing({ billing_enabled: false, ai: ai(0) }))).toBeNull();
    expect(balanceBreakdown(ai(1.5), 2).low).toBe(true);
  });

  it("stays quiet while auto-recharge will refill", () => {
    const low = { ai: ai(1.5) };
    expect(balanceBanner(billing({ ...low, auto_recharge: ar({ enabled: true, low_at_usd: 2 }) }))).toBeNull();
    // Less than $5 left under the monthly limit: no recharge can come.
    expect(balanceBanner(billing({ ...low, auto_recharge: ar({ enabled: true, low_at_usd: 2, charged_this_month_usd: 95.01 }) }))).toBe("low");
    expect(balanceBanner(billing({ ...low, auto_recharge: ar({ enabled: true, low_at_usd: 2, charged_this_month_usd: 95 }) }))).toBeNull();
    expect(balanceBanner(billing({ ai: ai(0.001), auto_recharge: ar({ failure: { code: "card_declined", at: "" } }) }))).toBe("out");
  });
});

describe("auto-recharge", () => {
  it("quotes any amount at the top-up price", () => {
    expect(topUpQuote(topUp, 30)).toEqual({ usd: 30, price: "$30", amount: 3000 });
    expect(topUpQuote(topUp, 42.5)).toEqual({ usd: 42.5, price: "$42.50", amount: 4250 });
    expect(topUpQuote({ ...topUp, currency: "eur", unit_amount: 920 }, 50)).toMatchObject({ amount: 4600 });
    expect(topUpQuote(null, 20)).toEqual({ usd: 20, price: null, amount: null });
    expect(dollarForDollar(topUp)).toBe(true);
    expect(dollarForDollar({ ...topUp, currency: "eur" })).toBe(false);
  });

  it("reads typed amounts", () => {
    expect(parseUsd("40")).toBe(40);
    expect(parseUsd(" $1,250.5 ")).toBe(1250.5);
    expect(parseUsd("12.345")).toBeNull();
    expect(parseUsd("ten")).toBeNull();
    expect(parseUsd("")).toBeNull();
    expect(validTopUp(4.99)).toBe(false);
    expect(validTopUp(5)).toBe(true);
    expect(validTopUp(5000)).toBe(true);
    expect(validTopUp(5000.01)).toBe(false);
    expect(validTopUp(null)).toBe(false);
  });

  it("names the card", () => {
    expect(paymentMethodLabel({ brand: "visa", last4: "4242" })).toBe("Visa •••• 4242");
    expect(paymentMethodLabel({ brand: "link", last4: "" })).toBe("Link");
    expect(paymentMethodExpiry({ brand: "visa", last4: "4242", exp_month: 8, exp_year: 2030 })).toBe("08/30");
    expect(expiresSoon({ brand: "visa", last4: "1", exp_month: 11, exp_year: 2026 }, new Date("2026-10-14T00:00:00Z"))).toBe(true);
    expect(expiresSoon({ brand: "visa", last4: "1", exp_month: 12, exp_year: 2026 }, new Date("2026-10-14T00:00:00Z"))).toBe(false);
  });

  it("refuses a recharge that would not clear the threshold", () => {
    expect(rechargeTooSmall(10, 10)).toBe(true);
    expect(rechargeTooSmall(10, 9.99)).toBe(false);
  });

  it("applies a pending patch the way the server will", () => {
    const failed = ar({ failure: { code: "card_declined", at: "2026-10-01T00:00:00Z" } });
    expect(withPatch(failed, { enabled: true })).toMatchObject({ enabled: true, failure: null, low_at_usd: 2 });
    expect(withPatch(ar({ enabled: true, low_at_usd: 2 }), { threshold_usd: 4 }).low_at_usd).toBe(4);
    expect(withPatch(ar(), { threshold_usd: 4 }).low_at_usd).toBe(0.1);
  });
});

describe("collab", () => {
  const base = { billing_enabled: true, status: "none", prices: { pro_month: null, pro_year: null, ai_top_up: null } } as unknown as Billing;
  const b = (over: Partial<Billing>) => ({ ...base, ...over }) as Billing;
  const access = (a: "none" | "offered" | "complimentary", available = a !== "none") => ({ access: a, until: null, enabled: false, available });

  it("hides the plan without access", () => {
    expect(showCollab(b({ plan: "free", collab: access("none") }))).toBe(false);
    expect(showCollab(b({ plan: "pro", status: "active" }))).toBe(false);
    expect(showCollab(b({ plan: "free", collab: access("offered") }))).toBe(true);
    expect(showCollab(b({ plan: "self_hosted", billing_enabled: false, collab: access("none") }))).toBe(false);
  });

  it("tells a paid collab subscription from complimentary access", () => {
    expect(hasSubscription(b({ plan: "pro", status: "active" }))).toBe(true);
    expect(hasSubscription(b({ plan: "collab", status: "active", collab: access("offered") }))).toBe(true);
    expect(hasSubscription(b({ plan: "collab", status: "canceled", collab: access("complimentary") }))).toBe(false);
    expect(isComplimentary(b({ plan: "collab", status: "none", collab: access("complimentary") }))).toBe(true);
    expect(isComplimentary(b({ plan: "collab", status: "active", collab: access("complimentary") }))).toBe(false);
    expect(isComplimentary(b({ plan: "free", collab: access("complimentary") }))).toBe(false);
  });
});
