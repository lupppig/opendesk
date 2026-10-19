// Settings > Plan and billing (contracts/billing.md): the plan, seats, limits,
// the AI balance (US dollars) and usage. Stripe Checkout and the billing portal are hosted by
// Stripe; this page only starts them and reflects billing.updated live.

import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { Link, useNavigate, useRouterState, useSearch } from "@tanstack/react-router";
import { ArrowUpRight, Check, CreditCard, Gauge, KeyRound, Server, Sparkles } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Section, SettingsPage } from "@/components/layout";
import { presets, RangePicker } from "@/components/reports/RangePicker";
import { Avatar } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { RESUME_AUTO_RECHARGE_KEY, useBilling, useCanManageBilling, useCheckout, useFmt, usePortal } from "@/components/billing/hooks";
import { AutoRechargeSection } from "@/components/billing/AutoRecharge";
import { PaymentAndInvoices } from "@/components/billing/Invoices";
import { TopUpPanel } from "@/components/billing/TopUp";
import { api } from "@/lib/api/endpoints";
import { Meter } from "@/components/billing/Meter";
import { balanceBreakdown, formatMoney, formatPrice, hasSubscription, isComplimentary, isSelfHosted, limitState, showCollab, stackDays, yearlySavings } from "@/components/billing/model";
import { CollabPlan } from "@/components/billing/CollabPlan";
import { UsageChart, UsageDayTable, UsageLegend, shadeOf } from "@/components/billing/UsageChart";
import { isApiError } from "@/lib/api/client";
import type { AutoRechargePatch, Billing, BillingStatus, ReportsRange } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { cn } from "@/lib/cn";
import { AI_TONE, SERIES, tone, toneFor, toneVar, type Tone } from "@/lib/tones";
import { IconTile, SummaryStrip } from "@/components/settings2/bits";
import { modelName, VendorMark } from "@/components/ai/ModelPicker";
import { useAI } from "@/lib/ai";

export default function BillingPage() {
  const { t } = useTranslation("billing");
  const { data: b, isPending, isError, refetch } = useBilling();
  const confirming = useCheckoutReturn(b);
  useScrollToHash(!isPending);

  return (
    <SettingsPage title={t("page.title")} description={b && isSelfHosted(b) ? t("page.descriptionSelfHosted") : t("page.description")}>
      {isPending ? (
        <PageSkeleton />
      ) : isError || !b ? (
        <Section>
          <EmptyState lit={3} title={t("errors.load")} description={t("errors.loadBody")} action={<Button onClick={() => void refetch()}>{t("retry")}</Button>} />
        </Section>
      ) : isSelfHosted(b) ? (
        <>
          <SelfHosted b={b} />
          <Usage />
        </>
      ) : (
        <>
          {confirming && <Notice icon={<Spinner className="size-3.5" />} title={t("notice.confirming")} body={t("notice.confirmingBody")} />}
          <Notices b={b} />
          <PlanCard b={b} />
          {showCollab(b) && <CollabPlan b={b} />}
          {b.plan === "free" && <Upgrade b={b} />}
          {b.plan === "free" && <Limits b={b} />}
          <AiBalance b={b} />
          <AutoRechargeSection b={b} />
          <PaymentAndInvoices b={b} />
          <Usage />
        </>
      )}
    </SettingsPage>
  );
}

// ─── Returning from Stripe ─────────────────────────────────────────────────

/** Handles ?checkout= and ?top_up=, then waits for billing.updated to confirm. */
function useCheckoutReturn(b: Billing | undefined) {
  const { t } = useTranslation("billing");
  const search = useSearch({ from: "/app/settings/billing" });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const topUpBase = useRef<number | null>(null);
  const handled = useRef(false);

  useEffect(() => {
    if (handled.current || (!search.checkout && !search.top_up && !search.payment_method)) return;
    handled.current = true;
    if (search.checkout === "success") setConfirming(true);
    else if (search.checkout === "canceled") toast.info(t("toast.checkoutCancel"));
    if (search.top_up === "canceled") toast.info(t("toast.checkoutCancel"));
    if (search.top_up === "success") {
      topUpBase.current = qc.getQueryData<Billing>(qk.billing)?.ai.purchased_usd ?? 0;
      toast.info(t("toast.topUpPending"));
    }
    if (search.payment_method) resumeAutoRecharge(search.payment_method === "success", qc, t);
    void qc.invalidateQueries({ queryKey: qk.billing });
    void navigate({ to: "/settings/billing", search: {}, replace: true });
  }, [search.checkout, search.top_up, search.payment_method, navigate, qc, t]);

  // The webhook may land after the customer is back; billing.updated (or a refetch) confirms it.
  useEffect(() => {
    if (!b) return;
    if (confirming && b.plan === "pro") {
      setConfirming(false);
      toast.success(t("toast.pro"), { description: t("toast.proBody") });
    }
    if (confirming && b.plan === "collab" && hasSubscription(b)) {
      setConfirming(false);
      toast.success(t("toast.collab"), { description: t("toast.collabBody") });
    }
    if (topUpBase.current !== null && b.ai.purchased_usd > topUpBase.current) {
      topUpBase.current = null;
      toast.success(t("toast.topUp"), { description: t("toast.topUpBody") });
    }
  }, [b, confirming, t]);

  useEffect(() => {
    if (!confirming) return;
    const poll = setInterval(() => void qc.invalidateQueries({ queryKey: qk.billing, exact: true }), 4000);
    const stop = setTimeout(() => setConfirming(false), 45_000);
    return () => {
      clearInterval(poll);
      clearTimeout(stop);
    };
  }, [confirming, qc]);

  return confirming;
}

/** Back from saving a card: turn on auto-recharge with the settings chosen before leaving. */
function resumeAutoRecharge(saved: boolean, qc: QueryClient, t: TFunction<"billing">) {
  let patch: AutoRechargePatch | null;
  try {
    patch = JSON.parse(sessionStorage.getItem(RESUME_AUTO_RECHARGE_KEY) ?? "null") as AutoRechargePatch | null;
    sessionStorage.removeItem(RESUME_AUTO_RECHARGE_KEY);
  } catch {
    patch = null;
  }
  if (!saved) {
    toast.info(t("toast.cardCanceled"));
    return;
  }
  if (!patch) {
    toast.success(t("toast.cardSaved"), { description: t("toast.cardSavedBody") });
    return;
  }
  api.billing
    .autoRecharge({ ...patch, enabled: true })
    .then((b) => {
      qc.setQueryData(qk.billing, b);
      toast.success(t("toast.autoRechargeOn"), { description: t("toast.cardSavedBody") });
    })
    .catch((e: unknown) => toast.error(e));
}

/** Scrolls to #ai-balance or #auto-recharge (the app banner links there) once the page has rendered. */
function useScrollToHash(ready: boolean) {
  const hash = useRouterState({ select: (s) => s.location.hash });
  useEffect(() => {
    if (!ready || !hash) return;
    const el = document.getElementById(hash);
    if (!el) return;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    el.scrollIntoView({ block: "start", behavior: reduce ? "auto" : "smooth" });
  }, [ready, hash]);
}

// ─── Pieces ────────────────────────────────────────────────────────────────

function PageSkeleton() {
  return (
    <div className="flex flex-col gap-8" aria-hidden>
      {[132, 96, 220].map((h, i) => (
        <div key={i} className="rounded-lg border border-line p-4" style={{ height: h }}>
          <Skeleton className="h-2.5 w-24" />
          <Skeleton className="mt-4 h-5 w-32" />
          <Skeleton className="mt-3 h-2.5 w-[60%]" />
        </div>
      ))}
    </div>
  );
}

function Notice({ icon, title, body, action, hue }: { icon: ReactNode; title: ReactNode; body: ReactNode; action?: ReactNode; hue?: Tone }) {
  return (
    <div
      role="status"
      className={cn("flex animate-rise-in items-start gap-3 rounded-lg border px-4 py-3", hue ? cn(tone[hue].soft, tone[hue].line) : "border-line bg-sunken/60")}
    >
      <span className={cn("mt-[3px] flex shrink-0", hue ? tone[hue].text : "text-fg-3")}>{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium">{title}</div>
        <div className="mt-0.5 text-[12.5px] leading-[18px] text-fg-3">{body}</div>
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  );
}

function Notices({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const canManage = useCanManageBilling();
  const portal = usePortal();
  const portalButton = (label: string) =>
    canManage && hasSubscription(b) ? (
      <Button size="sm" loading={portal.isPending} onClick={() => portal.mutate()}>
        {label}
      </Button>
    ) : undefined;
  return (
    <>
      {b.status === "past_due" && (
        <Notice
          hue="amber"
          icon={<span className="block size-1.5 translate-y-[3px] animate-pulse-soft rounded-full bg-tone-amber-solid motion-reduce:animate-none" />}
          title={t("notice.pastDueTitle")}
          body={t("notice.pastDueBody")}
          action={portalButton(t("notice.updatePayment"))}
        />
      )}
      {b.cancel_at_period_end && b.current_period_end && (
        <Notice
          icon={<span className="block size-1.5 translate-y-[3px] rounded-full bg-fg-4" />}
          title={t("notice.cancelTitle", { date: fmt.date(b.current_period_end) })}
          body={t("notice.cancelBody")}
          action={portalButton(t("notice.resume"))}
        />
      )}
    </>
  );
}

const statusTone: Partial<Record<BillingStatus, "ok" | "warn" | "danger" | "neutral">> = {
  active: "ok",
  trialing: "ok",
  past_due: "warn",
  unpaid: "danger",
  incomplete: "warn",
};

function Fact({ label, value, hint }: { label: ReactNode; value: ReactNode; hint?: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="label-caps mb-1">{label}</div>
      <div className="mono truncate text-[13px] text-fg">{value}</div>
      {hint && <div className="mt-1 text-[11.5px] leading-4 text-fg-4">{hint}</div>}
    </div>
  );
}

function PlanCard({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const canManage = useCanManageBilling();
  const portal = usePortal();
  const pro = b.plan === "pro";
  const collab = b.plan === "collab";
  const free = isComplimentary(b);
  // The subscription's facts: always on Pro (as before), on Collab while it is paid for.
  const paid = pro || (collab && hasSubscription(b));
  const price = collab
    ? ((b.interval === "year" ? b.prices.collab_year : b.prices.collab_month) ?? null)
    : b.interval === "year"
      ? b.prices.pro_year
      : b.prices.pro_month;
  const summary = pro ? t("plan.proSummary") : collab ? t("plan.collabSummary") : t("plan.freeSummary");

  return (
    <Section flush>
      <div className="flex items-start justify-between gap-4 px-5 pb-4 pt-4">
        <div className="flex min-w-0 items-start gap-3.5">
          <IconTile tone={pro ? "blue" : collab ? "indigo" : "gray"} className="mt-0.5 size-10 rounded-lg [&_svg]:size-[18px]">
            <CreditCard strokeWidth={1.75} />
          </IconTile>
          <div className="min-w-0">
          <div className="label-caps">{t("plan.current")}</div>
          <div className="mt-1.5 flex items-center gap-2.5">
            <span className="text-[22px] font-semibold leading-7 tracking-[-0.03em]">{t(`plans.${b.plan}`)}</span>
            {free && <Badge tone="indigo">{t("collab.free")}</Badge>}
            {paid && b.status !== "none" && (
              <Badge tone={b.cancel_at_period_end ? "neutral" : (statusTone[b.status] ?? "neutral")} dot>
                {b.cancel_at_period_end && b.current_period_end ? t("status.ending", { date: fmt.date(b.current_period_end) }) : t(`status.${b.status as Exclude<BillingStatus, "none">}`)}
              </Badge>
            )}
          </div>
          <p className="mt-1 text-[12.5px] text-fg-3">{summary}</p>
          </div>
        </div>
        {canManage && hasSubscription(b) && (
          <Button icon={<CreditCard className="size-3.5 text-fg-3" />} iconRight={<ArrowUpRight className="size-3 text-fg-4" />} loading={portal.isPending} onClick={() => portal.mutate()}>
            {t("plan.manage")}
          </Button>
        )}
      </div>
      {paid && (
        <div className="grid grid-cols-1 gap-4 border-t border-line-soft bg-sunken/50 px-5 py-3.5 sm:grid-cols-3">
          <Fact
            label={t("plan.seats")}
            value={t("plan.seatsValue", { count: b.seats })}
            hint={b.billed_seats !== b.seats ? t("plan.seatsNext", { count: b.billed_seats }) : t("plan.seatsHint")}
          />
          <Fact
            label={t("plan.price")}
            value={
              price
                ? b.interval === "year"
                  ? t("plan.perSeatYear", { price: formatPrice(price, fmt.locale) })
                  : t("plan.perSeatMonth", { price: formatPrice(price, fmt.locale) })
                : "–"
            }
          />
          <Fact
            label={b.cancel_at_period_end ? t("plan.endsLabel") : t("plan.renewsLabel")}
            value={b.current_period_end ? fmt.date(b.current_period_end) : "–"}
            hint={canManage ? t("plan.manageHint") : t("plan.readOnly")}
          />
        </div>
      )}
      {!paid && !canManage && <div className="border-t border-line-soft px-5 py-3 text-[12.5px] text-fg-3">{t("plan.readOnly")}</div>}
    </Section>
  );
}

function Upgrade({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const canManage = useCanManageBilling();
  const checkout = useCheckout();
  const month = b.prices.pro_month;
  const year = b.prices.pro_year;
  const [interval, setBillingInterval] = useState<"month" | "year">("month");
  const save = yearlySavings(month, year);
  const price = interval === "year" ? year : month;
  const seats = Math.max(1, b.billed_seats);
  if (!canManage) return null;

  return (
    <Section title={t("upgrade.title")} description={t("upgrade.description")} flush>
      <div className="grid gap-0 sm:grid-cols-[1fr_auto]">
        <div className="px-5 py-4">
          {year && (
            <Segmented
              value={interval}
              onChange={setBillingInterval}
              options={[
                { value: "month", label: t("upgrade.monthly") },
                {
                  value: "year",
                  label: (
                    <span className="flex items-center gap-1.5">
                      {t("upgrade.yearly")}
                      {save && <span className={cn("mono rounded-xs px-1 text-[10.5px] font-normal", tone.green.chip)}>{t("upgrade.save", { pct: save })}</span>}
                    </span>
                  ),
                },
              ]}
            />
          )}
          {price && (
            <div className="mt-4 flex items-baseline gap-1.5">
              <span className="text-[28px] font-semibold leading-none tracking-[-0.03em] tabular-nums">{formatPrice(price, fmt.locale)}</span>
              <span className="text-[12.5px] text-fg-3">
                {t("upgrade.perSeat")} {interval === "year" ? t("upgrade.perYear") : t("upgrade.perMonth")}
              </span>
            </div>
          )}
          {price && (
            <p className="mono mt-2 text-[11.5px] text-fg-4">
              {t("upgrade.estimate", {
                count: seats,
                total: formatMoney(price.unit_amount * seats, price.currency, fmt.locale),
                per: interval === "year" ? t("upgrade.perYear") : t("upgrade.perMonth"),
              })}
            </p>
          )}
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button variant="primary" size="md" loading={checkout.isPending} onClick={() => checkout.mutate(interval)}>
              {checkout.isPending ? t("upgrade.redirecting") : interval === "year" ? t("upgrade.ctaYearly") : t("upgrade.cta")}
            </Button>
            <span className="text-[11.5px] text-fg-4">{t("upgrade.secure")}</span>
          </div>
        </div>
        <ul className="flex flex-col justify-center gap-2.5 border-t border-line-soft bg-sunken/50 px-5 py-4 sm:border-l sm:border-t-0 sm:pr-8">
          {(["mailboxes", "members", "ai", "same"] as const).map((k) => (
            <li key={k} className="flex items-center gap-2.5 text-[12.5px] text-fg-2">
              <BenefitIcon ai={k === "ai"} />
              {t(`upgrade.benefit.${k}`)}
            </li>
          ))}
        </ul>
      </div>
    </Section>
  );
}

function Limits({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const rows = [
    { kind: "mailboxes" as const, label: t("limits.mailboxes"), hint: undefined },
    { kind: "members" as const, label: t("limits.members"), hint: t("limits.membersHint") },
  ];
  return (
    <Section title={t("limits.title")} description={t("limits.description")}>
      {rows.map((r) => {
        const l = limitState(b, r.kind);
        return (
          <div key={r.kind} className="flex items-center gap-4 border-b border-line-soft py-3.5 last:border-0">
            <div className="min-w-0 flex-1">
              <div className="text-[13px] font-medium">{r.label}</div>
              <div className="mt-0.5 text-[12.5px] leading-[18px] text-fg-3">{l.over ? t("limits.over") : r.hint}</div>
            </div>
            <Meter value={l.used} max={l.max} label={`${r.label}: ${t("limits.of", { used: l.used, max: l.max })}`} className="hidden h-1.5 w-40 sm:block" />
            <span className={cn("mono w-24 whitespace-nowrap text-right text-[12.5px] tabular-nums", l.over ? "text-tone-red" : l.atLimit ? "text-tone-amber" : "text-fg-2")}>
              {!l.limited ? t("limits.unlimited") : l.over ? t("limits.overOf", { used: l.used, max: l.max }) : t("limits.of", { used: l.used, max: l.max })}
            </span>
          </div>
        );
      })}
    </Section>
  );
}

function AiBalance({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const canManage = useCanManageBilling();
  const c = balanceBreakdown(b.ai, b.auto_recharge?.low_at_usd);

  return (
    <div id="ai-balance" className="scroll-mt-6">
      <Section title={t("balance.title")} description={t("balance.description")} flush>
        <div className="grid gap-5 px-5 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
          <div className="min-w-0">
            <div className="flex items-center gap-3">
              <IconTile tone={AI_TONE}>
                <Sparkles strokeWidth={1.75} />
              </IconTile>
              <div className="flex items-baseline gap-2">
                <span className="text-[28px] font-semibold leading-none tracking-[-0.03em] tabular-nums">{fmt.usd(c.remaining)}</span>
                <span className="text-[12.5px] text-fg-3">{t("balance.remaining")}</span>
                {c.low && <span className="size-1.5 self-center animate-pulse-soft rounded-full bg-tone-amber-solid motion-reduce:animate-none" aria-hidden />}
              </div>
            </div>
            <div className="mt-4">
              <div className="mb-1.5 flex items-baseline justify-between gap-3 text-[12px]">
                <span className="text-fg-2">{t("balance.monthly")}</span>
                <span className="mono text-fg-3 tabular-nums">{t("balance.monthlyUsed", { used: fmt.usd(c.monthlyUsed), total: fmt.usd(c.monthly) })}</span>
              </div>
              <Meter value={c.monthlyUsed} max={c.monthly} label={t("balance.meterLabel", { used: fmt.usd(c.monthlyUsed), total: fmt.usd(c.monthly) })} className="h-1.5" />
              <div className="mono mt-1.5 text-[11px] text-fg-4">{t("balance.resets", { date: fmt.date(b.ai.period_end) })}</div>
            </div>
            {c.low && <p className="mt-3 text-[12.5px] text-tone-amber">{t("balance.low")}</p>}
          </div>
          <div className="flex gap-6 sm:flex-col sm:gap-3 sm:border-l sm:border-line-soft sm:pl-6">
            <Fact label={t("balance.monthlyLeft")} value={fmt.usd(c.monthlyLeft)} hint={t("balance.monthlyLeftHint")} />
            <Fact label={t("balance.purchased")} value={fmt.usd(c.purchased)} hint={t("balance.purchasedHint")} />
          </div>
        </div>
        <p className="flex items-start gap-2 border-t border-line-soft px-5 py-2.5 text-[12px] leading-[18px] text-fg-3">
          <Gauge className="mt-[2px] size-3.5 shrink-0 text-fg-4" strokeWidth={1.75} aria-hidden />
          <span>
            {t("balance.usageBased")}{" "}
            <Link to="/settings/ai" className="text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
              {t("balance.chooseModels")}
            </Link>
          </span>
        </p>
        {canManage && b.prices.ai_top_up && <TopUpPanel b={b} />}
      </Section>
    </div>
  );
}

function BenefitIcon({ ai }: { ai?: boolean }) {
  return (
    <span className={cn("flex size-4 shrink-0 items-center justify-center rounded-xs", ai ? tone[AI_TONE].chip : tone.green.chip)} aria-hidden>
      {ai ? <Sparkles className="size-2.5" strokeWidth={2} /> : <Check className="size-2.5" strokeWidth={3} />}
    </span>
  );
}

function SelfHosted({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  return (
    <>
      <Section flush>
        <div className="flex items-start gap-4 px-5 py-5">
          <IconTile tone="green" className="size-10 rounded-lg [&_svg]:size-[18px]">
            <Server strokeWidth={1.75} />
          </IconTile>
          <div className="min-w-0 flex-1">
            <div className="text-[15px] font-semibold tracking-[-0.01em]">{t("selfHosted.title")}</div>
            <p className="mt-1 max-w-[560px] text-[12.5px] leading-[19px] text-fg-3">{t("selfHosted.body")}</p>
            <ul className="mt-3.5 flex flex-wrap gap-x-5 gap-y-2">
              {(["mailboxes", "members", "ai"] as const).map((k) => (
                <li key={k} className="flex items-center gap-2 text-[12.5px] text-fg-2">
                  <BenefitIcon ai={k === "ai"} />
                  {t(`selfHosted.${k}`)}
                </li>
              ))}
            </ul>
          </div>
        </div>
      </Section>
      <SummaryStrip
        className="-mt-4"
        items={[
          { label: t("selfHosted.usedMailboxes"), value: fmt.num(b.usage.mailboxes), hint: t("selfHosted.noLimit") },
          { label: t("selfHosted.usedMembers"), value: fmt.num(b.usage.members), hint: t("selfHosted.noLimit") },
          {
            label: t("selfHosted.usedAi"),
            value: fmt.usd(b.ai.used_usd, { tiny: true }),
            tone: AI_TONE,
            hint: b.ai.period_end ? t("balance.resets", { date: fmt.date(b.ai.period_end) }) : t("selfHosted.noLimit"),
          },
        ]}
      />
    </>
  );
}

// ─── Usage ─────────────────────────────────────────────────────────────────

function Usage() {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const canManage = useCanManageBilling();
  const [range, setRange] = useState<ReportsRange>(() => presets()[1].range);
  const [asTable, setAsTable] = useState(false);
  const { data, isPending, isError, error, refetch } = useQuery({ ...q.billingUsage(range), enabled: canManage, retry: (n, e) => !isApiError(e, "billing_disabled") && n < 2 });
  const stacked = useMemo(() => (data ? stackDays(data, range) : null), [data, range]);
  const metered = !!useAI().config?.metered;
  const { data: models } = useQuery({ ...q.aiModels(), enabled: canManage && metered });

  if (!canManage) return null;
  // Usage is served on every instance (self-hosted too); an older server may answer 404 billing_disabled.
  if (isError && isApiError(error, "billing_disabled")) return null;

  const total = data?.total_usd ?? 0;
  return (
    <Section
      title={t("usage.title")}
      description={metered ? `${t("usage.description")} ${t("usage.metered")}` : t("usage.description")}
      actions={<RangePicker value={range} onChange={setRange} />}
      flush
    >
      {isPending ? (
        <div className="px-5 py-5" aria-hidden>
          <Skeleton className="h-2.5 w-28" />
          <Skeleton className="mt-5 h-[180px] w-full rounded-md" />
        </div>
      ) : isError || !data || !stacked ? (
        <EmptyState compact lit={3} title={t("usage.error")} action={<Button onClick={() => void refetch()}>{t("retry")}</Button>} />
      ) : total === 0 && !data.total_calls ? (
        <EmptyState compact lit={0} title={t("usage.empty")} description={t("usage.emptyBody")} />
      ) : (
        <>
          <div className="px-5 pb-3 pt-4">
            <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
              <div className="flex items-baseline gap-5">
                <div>
                  <div className="label-caps mb-1">{t("usage.total")}</div>
                  <div className="text-[20px] font-semibold leading-6 tracking-[-0.02em] tabular-nums">{fmt.usd(total, { tiny: true })}</div>
                </div>
                <div>
                  <div className="label-caps mb-1">{t("usage.calls")}</div>
                  <div className="text-[20px] font-semibold leading-6 tracking-[-0.02em] tabular-nums text-fg-3">{fmt.num(data.total_calls)}</div>
                </div>
                {!!data.own_key_calls && (
                  <div title={t("usage.ownKeyHint")}>
                    <div className="label-caps mb-1">{t("usage.ownKey")}</div>
                    <div className="text-[20px] font-semibold leading-6 tracking-[-0.02em] tabular-nums text-fg-3">{fmt.num(data.own_key_calls)}</div>
                  </div>
                )}
              </div>
              <div className="flex items-center gap-3">
                <UsageLegend series={stacked.series} />
                <Button size="xs" variant="ghost" onClick={() => setAsTable((v) => !v)}>
                  {asTable ? t("usage.chart") : t("usage.table")}
                </Button>
              </div>
            </div>
            {asTable ? <UsageDayTable days={stacked.days} series={stacked.series} /> : <UsageChart days={stacked.days} series={stacked.series} />}
          </div>
          <div className="grid border-t border-line-soft md:grid-cols-2">
            <Breakdown
              title={t("usage.byFeature")}
              rows={data.by_feature.map((f) => ({
                key: f.feature,
                label: t(`features.${f.feature}`),
                calls: f.calls,
                usd: f.usd,
                color: shadeOf(stacked.series, stacked.series.includes(f.feature) ? f.feature : "other"),
              }))}
              total={total}
            />
            <Breakdown
              title={t("usage.byMember")}
              className="border-t border-line-soft md:border-l md:border-t-0"
              rows={data.by_member.map((m) => ({
                key: m.user?.id ?? "widget",
                label: m.user?.name ?? t("usage.widget"),
                icon: m.user ? (
                  <Avatar name={m.user.name} src={m.user.avatar_url} size="xs" />
                ) : (
                  <span className={cn("flex size-4 items-center justify-center rounded-xs", tone.teal.chip)}>
                    <Sparkles className="size-2.5" />
                  </span>
                ),
                color: toneVar(m.user ? toneFor(m.user.name) : "teal"),
                calls: m.calls,
                usd: m.usd,
              }))}
              total={total}
            />
          </div>
          {data.by_model && data.by_model.length > 0 && (
            <Breakdown
              title={t("usage.byModel")}
              className="border-t border-line-soft"
              rows={data.by_model.map((m, i) => {
                const known = models?.data.find((x) => x.id === m.model);
                const vendor = known?.vendor || (m.model.includes("/") ? m.model.split("/")[0] : "");
                return {
                  key: `${m.source ?? "instance"}:${m.model}`,
                  label: m.model === "jev" ? t("usage.decisionModel") : modelName(m.model, models?.data),
                  icon: <VendorMark vendor={m.model === "jev" ? "" : vendor} className="size-4 text-[9px]" />,
                  color: toneVar(SERIES[i % SERIES.length]),
                  calls: m.calls,
                  usd: m.usd,
                  ownKey: m.source === "own_key",
                };
              })}
              total={total}
            />
          )}
        </>
      )}
    </Section>
  );
}

function Breakdown({
  title,
  rows,
  total,
  className,
}: {
  title: string;
  rows: { key: string; label: string; icon?: ReactNode; calls: number; usd: number; color?: string; ownKey?: boolean }[];
  total: number;
  className?: string;
}) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const max = Math.max(0, ...rows.map((r) => r.usd));
  return (
    <div className={cn("min-w-0 px-5 py-3.5", className)}>
      <table className="w-full text-[12.5px]">
        <caption className="mb-1 text-left">
          <span className="label-caps">{title}</span>
        </caption>
        <thead>
          <tr className="text-left">
            <th className="sr-only">{title}</th>
            <th className="w-16 pb-1 text-right text-[10.5px] font-normal text-fg-4">{t("usage.calls")}</th>
            <th className="w-16 pb-1 text-right text-[10.5px] font-normal text-fg-4">{t("usage.total")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.slice(0, 8).map((r) => (
            <tr key={r.key} title={r.ownKey ? `${r.label}: ${t("usage.ownKeyHint")}` : `${r.label}: ${total ? Math.round((r.usd / total) * 100) : 0}%`}>
              <td className="max-w-0 py-1.5 pr-3">
                <span className="flex min-w-0 items-center gap-2">
                  {r.icon}
                  <span className="truncate text-fg-2">{r.label}</span>
                </span>
                <div className="mt-1 h-[3px] overflow-hidden rounded-full bg-hover">
                  <div
                    className="h-full rounded-full transition-[width] duration-500 ease-out motion-reduce:transition-none"
                    style={{ width: `${max > 0 ? (r.usd / max) * 100 : 0}%`, background: r.color ?? toneVar(SERIES[0]) }}
                  />
                </div>
              </td>
              <td className="mono py-1.5 text-right align-top tabular-nums text-fg-4">{fmt.num(r.calls)}</td>
              <td className="mono whitespace-nowrap py-1.5 text-right align-top tabular-nums">
                {r.ownKey && r.usd === 0 ? (
                  <span className="inline-flex items-center gap-1 text-fg-3">
                    <KeyRound className="size-3" strokeWidth={1.75} aria-hidden />
                    {t("usage.ownKeyTag")}
                  </span>
                ) : (
                  fmt.usd(r.usd, { tiny: true })
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
