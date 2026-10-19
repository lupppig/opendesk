// Settings > Plan and billing > Collab (contracts/billing.md "Collab"): the hidden
// collab plan, shown only to a workspace it is offered or given to, or that is on it.
// Subscribe opens Stripe Checkout; a Pro subscription switches in place, prorated.

import { Check, Mail } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Section } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Segmented } from "@/components/ui/tabs";
import { IconTile } from "@/components/settings2/bits";
import type { Billing } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";
import { useCanManageBilling, useChangePlan, useCollabCheckout, useFmt } from "./hooks";
import { formatMoney, formatPrice, hasSubscription, isComplimentary, yearlySavings } from "./model";

const BENEFITS = ["mail", "share", "limits", "ai"] as const;

export function CollabPlan({ b }: { b: Billing }) {
  const { t } = useTranslation("billing");
  const fmt = useFmt();
  const canManage = useCanManageBilling();
  const confirm = useConfirm();
  const checkout = useCollabCheckout();
  const change = useChangePlan();
  const month = b.prices.collab_month ?? null;
  const year = b.prices.collab_year ?? null;
  const [interval, setBillingInterval] = useState<"month" | "year">(month || !year ? "month" : "year");
  const price = interval === "year" ? year : month;
  const save = yearlySavings(month, year);
  const seats = Math.max(1, b.billed_seats);
  const available = !!b.collab?.available;
  const subscribed = hasSubscription(b);
  const onCollab = b.plan === "collab" && subscribed;
  const free = isComplimentary(b);
  const onPro = b.plan === "pro" && subscribed;
  const until = b.collab?.until ?? null;
  const subPrice = b.interval === "year" ? year : month;

  const switchToCollab = () =>
    confirm.show(
      subPrice ? t("collab.switchBody", { price: formatPrice(subPrice, fmt.locale), count: b.seats }) : t("collab.switchBodyNoPrice"),
      () => change.mutateAsync("collab"),
      { title: t("collab.switchTitle"), confirmLabel: t("collab.switch") },
    );
  const switchToPro = () =>
    confirm.show(t("collab.switchProBody"), () => change.mutateAsync("pro"), { title: t("collab.switchProTitle"), confirmLabel: t("collab.switchToPro") });

  // What the workspace can do from here: subscribe (free plan, or complimentary with an end
  // date), switch a Pro subscription, or switch back to Pro.
  const canSubscribe = canManage && available && !subscribed && (!free || !!until);
  const showPrice = available && ((!subscribed && !free) || onPro);

  return (
    <Section title={t("collab.title")} description={t("collab.description")} flush>
      <div className="grid gap-0 sm:grid-cols-[1fr_auto]">
        <div className="px-5 py-4">
          <div className="flex items-start gap-3.5">
            <IconTile tone="indigo" className="mt-0.5 size-10 rounded-lg [&_svg]:size-[18px]">
              <Mail strokeWidth={1.75} />
            </IconTile>
            <div className="min-w-0">
              {free ? (
                <>
                  <div className="text-[15px] font-semibold tracking-[-0.01em]">{until ? t("collab.freeUntil", { date: fmt.date(until) }) : t("collab.freeForever")}</div>
                  <p className="mt-1 text-[12.5px] leading-[18px] text-fg-3">{until ? t("collab.freeHint") : t("collab.freeHintForever")}</p>
                </>
              ) : onCollab ? (
                <>
                  <div className="label-caps">{t("collab.current")}</div>
                  <div className="mt-1 text-[15px] font-semibold tracking-[-0.01em]">{t("plans.collab")}</div>
                </>
              ) : (
                showPrice &&
                price && (
                  <>
                    <div className="flex items-baseline gap-1.5">
                      <span className="text-[28px] font-semibold leading-none tracking-[-0.03em] tabular-nums">{formatPrice(price, fmt.locale)}</span>
                      <span className="text-[12.5px] text-fg-3">
                        {t("upgrade.perSeat")} {interval === "year" ? t("upgrade.perYear") : t("upgrade.perMonth")}
                      </span>
                    </div>
                    {!onPro && (
                      <p className="mono mt-2 text-[11.5px] text-fg-4">
                        {t("upgrade.estimate", {
                          count: seats,
                          total: formatMoney(price.unit_amount * seats, price.currency, fmt.locale),
                          per: interval === "year" ? t("upgrade.perYear") : t("upgrade.perMonth"),
                        })}
                      </p>
                    )}
                  </>
                )
              )}
            </div>
          </div>

          {canSubscribe && month && year && (
            <Segmented
              className="mt-4"
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

          {canManage && (canSubscribe || (available && onPro) || onCollab) && (
            <div className="mt-4 flex flex-wrap items-center gap-3">
              {canSubscribe &&
                (price ? (
                  <Button variant={free ? "secondary" : "primary"} loading={checkout.isPending} onClick={() => checkout.mutate(interval)}>
                    {checkout.isPending ? t("upgrade.redirecting") : interval === "year" ? t("collab.subscribeYearly") : t("collab.subscribe")}
                  </Button>
                ) : (
                  <span className="text-[12.5px] text-fg-3">{t("collab.unavailable")}</span>
                ))}
              {available && onPro && (
                <Button variant="primary" loading={change.isPending} onClick={switchToCollab}>
                  {t("collab.switch")}
                </Button>
              )}
              {onCollab && (
                <Button loading={change.isPending} onClick={switchToPro}>
                  {t("collab.switchToPro")}
                </Button>
              )}
              {canSubscribe && price && <span className="text-[11.5px] text-fg-4">{t("upgrade.secure")}</span>}
            </div>
          )}
          {!canManage && !free && !onCollab && <p className="mt-3 text-[12.5px] text-fg-3">{t("plan.readOnly")}</p>}
        </div>
        <ul className="flex flex-col justify-center gap-2.5 border-t border-line-soft bg-sunken/50 px-5 py-4 sm:border-l sm:border-t-0 sm:pr-8">
          {BENEFITS.map((k) => (
            <li key={k} className="flex items-center gap-2.5 text-[12.5px] text-fg-2">
              <span className={cn("flex size-4 shrink-0 items-center justify-center rounded-xs", tone.green.chip)} aria-hidden>
                <Check className="size-2.5" strokeWidth={3} />
              </span>
              {t(`collab.benefit.${k}`)}
            </li>
          ))}
        </ul>
      </div>
    </Section>
  );
}
