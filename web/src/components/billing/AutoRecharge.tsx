// Settings > Plan and billing > Auto-recharge (contracts/billing.md "Auto-recharge").
// The switch saves at once; stepper edits save together after a short pause.
// Both are optimistic (the billing cache changes first and rolls back on error).

import { useQueryClient } from "@tanstack/react-query";
import { CreditCard, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Stepper } from "@/components/settings2/Stepper";
import { isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AutoRechargePatch, Billing } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { clock } from "@/lib/format";
import { qk } from "@/lib/queries";
import { tone, type Tone } from "@/lib/tones";
import { RESUME_AUTO_RECHARGE_KEY, useAddPaymentMethod, useAutoRecharge, useCanManageBilling, useFmt } from "./hooks";
import { dollarForDollar, formatMoney, MAX_TOP_UP_USD, MIN_TOP_UP_USD, topUpQuote, withPatch } from "./model";

const SAVE_DELAY = 600;

export function AutoRechargeSection({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const qc = useQueryClient();
  const canManage = useCanManageBilling();
  const save = useAutoRecharge();
  const addCard = useAddPaymentMethod();
  const [needsCard, setNeedsCard] = useState(false);
  const [draft, setDraft] = useState<AutoRechargePatch>({});
  const pending = useRef<AutoRechargePatch>({});
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Leaving the page within the pause still saves the last edit.
  useEffect(
    () => () => {
      clearTimeout(timer.current);
      const p = pending.current;
      if (Object.keys(p).length === 0) return;
      pending.current = {};
      void api.billing
        .autoRecharge(p)
        .then((next) => qc.setQueryData(qk.billing, next))
        .catch(() => qc.invalidateQueries({ queryKey: qk.billing, exact: true }));
    },
    [qc],
  );

  const ar = b.auto_recharge;
  const topUp = b.prices.ai_top_up;
  if (!ar || !topUp || !canManage) return null;
  const v = withPatch(ar, draft);

  const flush = (extra: AutoRechargePatch = {}) => {
    clearTimeout(timer.current);
    const p = { ...pending.current, ...extra };
    pending.current = {};
    setDraft({});
    if (Object.keys(p).length === 0) return;
    save.mutate(p, {
      onError: (e) => {
        if (isApiError(e, "payment_method_required")) setNeedsCard(true);
      },
    });
  };
  const edit = (patch: AutoRechargePatch) => {
    pending.current = { ...pending.current, ...patch };
    setDraft(pending.current);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => flush(), SAVE_DELAY);
  };
  const toggle = (enabled: boolean) => {
    if (!enabled) setNeedsCard(false);
    flush({ enabled });
  };
  const goAddCard = () => {
    try {
      sessionStorage.setItem(
        RESUME_AUTO_RECHARGE_KEY,
        JSON.stringify({ enabled: true, threshold_usd: v.threshold_usd, amount_usd: v.amount_usd, monthly_limit_usd: v.monthly_limit_usd }),
      );
    } catch {
      /* private mode: the customer turns it on again after adding the card */
    }
    addCard.mutate();
  };

  const quote = topUpQuote(topUp, v.amount_usd, fmt.locale);
  const same = dollarForDollar(topUp);
  // One recharge must lift the balance back above the threshold, and fit the monthly limit,
  // so each bounds the next.
  const maxThreshold = Math.max(0.01, Math.min(1000, Math.round((v.amount_usd - 0.01) * 100) / 100));
  const minAmount = Math.min(MAX_TOP_UP_USD, Math.max(MIN_TOP_UP_USD, Math.floor(v.threshold_usd) + 1));
  const maxAmount = Math.min(MAX_TOP_UP_USD, v.monthly_limit_usd);
  const usd = (n: number) => fmt.usd(n, { trim: true });
  const last = ar.last_recharge;
  const failure = !v.enabled ? ar.failure : null;
  const retry = v.enabled ? ar.retry : null;
  const retryText = (iso: string) => {
    const d = new Date(iso);
    const time = clock(iso);
    return d.toDateString() === new Date().toDateString()
      ? t("autoRecharge.retryToday", { time })
      : t("autoRecharge.retryLater", { date: fmt.date(iso), time });
  };

  return (
    <div id="auto-recharge" className="scroll-mt-6">
      <Section
        title={t("autoRecharge.title")}
        description={
          v.enabled
            ? t("autoRecharge.summary", { usd: usd(v.amount_usd), threshold: fmt.usd(v.threshold_usd), limit: usd(v.monthly_limit_usd) })
            : t("autoRecharge.description")
        }
        actions={
          <label className="flex cursor-pointer items-center gap-2 text-[12px] text-fg-3">
            {v.enabled ? t("autoRecharge.on") : t("autoRecharge.off")}
            <Switch checked={v.enabled} onCheckedChange={toggle} aria-label={t("autoRecharge.toggle")} />
          </label>
        }
      >
        {failure && (
          <Callout
            hue="red"
            icon={<CreditCard className="size-3.5" />}
            title={t("autoRecharge.failedTitle", { date: fmt.date(failure.at) })}
            body={t(`autoRecharge.failure.${failure.code}`)}
            action={
              failure.code !== "unavailable" ? (
                <Button size="sm" loading={addCard.isPending} onClick={goAddCard}>
                  {t("autoRecharge.addCard")}
                </Button>
              ) : undefined
            }
          />
        )}
        {retry && (
          <Callout
            hue="amber"
            icon={<CreditCard className="size-3.5" />}
            title={t("autoRecharge.retryTitle")}
            body={retryText(retry.at)}
            action={
              <Button size="sm" loading={addCard.isPending} onClick={() => addCard.mutate()}>
                {t("autoRecharge.updateCard")}
              </Button>
            }
          />
        )}
        {needsCard && !v.enabled && !failure && (
          <Callout
            hue="amber"
            icon={<CreditCard className="size-3.5" />}
            title={t("autoRecharge.needCardTitle")}
            body={t("autoRecharge.needCardBody")}
            action={
              <Button size="sm" variant="primary" loading={addCard.isPending} onClick={goAddCard}>
                {t("autoRecharge.addCard")}
              </Button>
            }
          />
        )}
        <Row label={t("autoRecharge.threshold")} description={t("autoRecharge.thresholdHint")}>
          <Stepper
            value={v.threshold_usd}
            onChange={(threshold_usd) => edit({ threshold_usd })}
            min={0.01}
            max={maxThreshold}
            step={1}
            decimals={2}
            prefix="$"
            aria-label={t("autoRecharge.threshold")}
            decLabel={t("autoRecharge.thresholdDec")}
            incLabel={t("autoRecharge.thresholdInc")}
            className="w-[140px]"
          />
        </Row>
        <Row
          label={t("autoRecharge.amount")}
          description={same || !quote.price ? t("autoRecharge.amountHint") : t("autoRecharge.amountQuote", { usd: usd(v.amount_usd), price: quote.price })}
        >
          <Stepper
            value={v.amount_usd}
            onChange={(amount_usd) => edit({ amount_usd })}
            min={minAmount}
            max={maxAmount}
            step={5}
            prefix="$"
            aria-label={t("autoRecharge.amount")}
            decLabel={t("autoRecharge.amountDec")}
            incLabel={t("autoRecharge.amountInc")}
            className="w-[140px]"
          />
        </Row>
        <Row label={t("autoRecharge.limit")} description={t("autoRecharge.limitHint")}>
          <Stepper
            value={v.monthly_limit_usd}
            onChange={(monthly_limit_usd) => edit({ monthly_limit_usd })}
            min={Math.max(MIN_TOP_UP_USD, v.amount_usd)}
            max={50_000}
            step={25}
            prefix="$"
            aria-label={t("autoRecharge.limit")}
            decLabel={t("autoRecharge.limitDec")}
            incLabel={t("autoRecharge.limitInc")}
            className="w-[150px]"
          />
        </Row>
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-3 text-[12px]">
          <span className="flex min-w-0 items-center gap-2 text-fg-3">
            <RefreshCw className="size-3 shrink-0 text-fg-4" aria-hidden />
            <span className="truncate">
              {last
                ? t("autoRecharge.last", { date: fmt.date(last.at), usd: usd(last.usd), price: formatMoney(last.amount, last.currency, fmt.locale) })
                : v.enabled
                  ? t("autoRecharge.none")
                  : t("autoRecharge.offHint")}
            </span>
          </span>
          {v.enabled && (
            <span className="mono shrink-0 text-[11.5px] tabular-nums text-fg-4">
              {t("autoRecharge.thisMonth", { used: usd(v.charged_this_month_usd), limit: usd(v.monthly_limit_usd) })}
            </span>
          )}
        </div>
      </Section>
    </div>
  );
}

function Callout({ hue, icon, title, body, action }: { hue: Tone; icon: ReactNode; title: ReactNode; body: ReactNode; action?: ReactNode }) {
  return (
    <div role="status" className={cn("-mx-4 flex animate-rise-in items-start gap-3 border-b px-4 py-3 motion-reduce:animate-none", tone[hue].soft, tone[hue].line)}>
      <span className={cn("mt-[2px] flex shrink-0", tone[hue].text)}>{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="text-[12.5px] font-medium">{title}</div>
        <div className="mt-0.5 text-[12px] leading-[18px] text-fg-3">{body}</div>
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  );
}
