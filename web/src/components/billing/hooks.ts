import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { intlLocale } from "@/i18n";
import { formatUsd } from "@/lib/format";
import { isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AutoRechargePatch, Billing, PaidPlan } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { toast } from "@/components/ui/toast";
import { limitState, withPatch, type LimitKind } from "./model";

export function useBilling() {
  return useQuery(q.billing());
}

/** Owners and admins manage billing; everyone else reads. */
export function useCanManageBilling() {
  return atLeast(useRole(), "admin");
}

export function useLimit(kind: LimitKind) {
  // Counts move with every invite and mailbox, so don't trust an old copy here.
  const { data } = useQuery({ ...q.billing(), staleTime: 5_000 });
  return { billing: data, ...limitState(data, kind) };
}

/** Leaves for a Stripe-hosted page. Stays "pending" through the navigation. */
function useRedirect<A>(fn: (a: A) => Promise<{ url: string }>) {
  return useMutation({
    mutationFn: async (a: A) => {
      const { url } = await fn(a);
      location.assign(url);
      // Keep the button busy while the browser leaves.
      await new Promise(() => {});
    },
    onError: (e) => toast.error(e),
  });
}

export const useCheckout = () => useRedirect((interval: "month" | "year") => api.billing.checkout(interval));
/** Stripe Checkout for the collab plan (offered to the workspace by an instance admin). */
export const useCollabCheckout = () => useRedirect((interval: "month" | "year") => api.billing.checkout(interval, "collab"));
export const usePortal = () => useRedirect((_: void) => api.billing.portal());

/** Switches the subscription between Pro and Collab, prorated; the page updates in place. */
export function useChangePlan() {
  const qc = useQueryClient();
  const { t } = useTranslation("billing");
  return useMutation({
    mutationFn: (plan: PaidPlan) => api.billing.changePlan(plan),
    onSuccess: (b) => {
      qc.setQueryData(qk.billing, b);
      toast.success(t("toast.switched", { plan: t(`plans.${b.plan}`) }), { description: t("toast.switchedBody") });
    },
  });
}
/** Stripe Checkout for `usd` of AI usage. */
export const useBuyTopUps = () => useRedirect((usd: number) => api.billing.topUpCheckout(usd));

/**
 * Buys `usd` of AI usage with the saved payment method, in place. When the bank wants the
 * buyer to confirm the payment, it carries on in Stripe Checkout with the same amount.
 */
export function useTopUp() {
  const qc = useQueryClient();
  const { t } = useTranslation("billing");
  return useMutation({
    mutationFn: (usd: number) => api.billing.topUp(usd),
    onSuccess: (r) => {
      qc.setQueryData(qk.billing, r.billing);
      void qc.invalidateQueries({ queryKey: qk.billingInvoices });
      if (r.status === "paid") toast.success(t("toast.topUpAdded", { usd: formatUsd(r.usd, { trim: true }) }), { description: t("toast.topUpBody") });
      else toast.info(t("toast.topUpPending"));
    },
    onError: async (e, usd) => {
      if (!isApiError(e, "authentication_required")) {
        toast.error(e);
        return;
      }
      toast.info(t("toast.confirmInCheckout"));
      try {
        location.assign((await api.billing.topUpCheckout(usd)).url);
      } catch (err) {
        toast.error(err);
      }
    },
  });
}
/** Stripe Checkout in setup mode: saves a card for auto-recharge. */
export const useAddPaymentMethod = () => useRedirect((_: void) => api.billing.paymentMethod());

/** The settings to turn on once the customer is back from adding a payment method. */
export const RESUME_AUTO_RECHARGE_KEY = "od-auto-recharge-resume";

/**
 * Saves auto-recharge settings optimistically: the billing cache changes at
 * once and rolls back on error. `payment_method_required` is left to the
 * caller (it asks for a card instead of showing a toast).
 */
export function useAutoRecharge() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: AutoRechargePatch) => api.billing.autoRecharge(patch),
    onMutate: async (patch) => {
      await qc.cancelQueries({ queryKey: qk.billing, exact: true });
      const prev = qc.getQueryData<Billing>(qk.billing);
      if (prev?.auto_recharge) qc.setQueryData<Billing>(qk.billing, { ...prev, auto_recharge: withPatch(prev.auto_recharge, patch) });
      return { prev };
    },
    onError: (e, _patch, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.billing, ctx.prev);
      if (!isApiError(e, "payment_method_required")) toast.error(e);
    },
    onSuccess: (b) => qc.setQueryData(qk.billing, b),
  });
}

/** Locale-aware number and date formatting for billing figures. */
export function useFmt() {
  const locale = intlLocale();
  const nf = new Intl.NumberFormat(locale);
  const thisYear = new Date().getFullYear();
  return {
    locale,
    num: (n: number) => nf.format(n),
    /** US dollars of AI usage: "$0.50"; `precise` keeps small amounts, "$0.0042". */
    usd: formatUsd,
    date: (iso: string | null | undefined) => {
      if (!iso) return "";
      const d = new Date(iso);
      return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", ...(d.getFullYear() !== thisYear ? { year: "numeric" } : {}) }).format(d);
    },
    day: (ymd: string, weekday = false) =>
      new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", timeZone: "UTC", ...(weekday ? { weekday: "short" } : {}) }).format(new Date(`${ymd}T00:00:00Z`)),
  };
}
