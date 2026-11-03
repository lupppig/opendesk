// Soft tinted pills for the states the panel shows: plan, subscription status,
// suspension, admin role, mailbox status, health checks and job states.

import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import type { AdminRole, HealthStatus, JobState, Plan } from "@/lib/api/types";
import { humanize } from "@/lib/format";
import { ROLE_LABEL } from "@/lib/permissions";
import type { Tone } from "@/lib/tones";

const PLAN_TONE: Record<Plan, Tone> = { free: "gray", pro: "blue", collab: "indigo", self_hosted: "teal" };

export function PlanPill({ plan }: { plan: Plan }) {
  const { t } = useTranslation();
  return <Badge tone={PLAN_TONE[plan] ?? "gray"}>{t(`plan.${plan}`, { defaultValue: humanize(plan) })}</Badge>;
}

/** Stripe subscription statuses. Empty (no subscription) shows nothing. */
export function SubscriptionPill({ status }: { status: string }) {
  const { t } = useTranslation();
  if (!status) return null;
  const tn: Tone =
    status === "active" ? "green" : status === "trialing" ? "blue" : status === "past_due" || status === "unpaid" ? "amber" : status === "canceled" || status === "incomplete_expired" ? "gray" : "gray";
  return <Badge tone={tn}>{t(`subscription.${status}` as never, { defaultValue: humanize(status) })}</Badge>;
}

export function SuspendedPill() {
  const { t } = useTranslation();
  return (
    <Badge tone="red" dot>
      {t("suspended")}
    </Badge>
  );
}

const ROLE_TONE: Record<AdminRole, Tone> = { viewer: "gray", support: "blue", super: "indigo" };

export function AdminRolePill({ role }: { role: AdminRole }) {
  const { t } = useTranslation();
  return <Badge tone={ROLE_TONE[role]}>{t(ROLE_LABEL[role])}</Badge>;
}

export function MailboxStatusPill({ status }: { status: "active" | "error" | "disabled" }) {
  const { t } = useTranslation();
  return status === "active" ? <Badge tone="ok">{t("mailbox.active")}</Badge> : status === "error" ? <Badge tone="danger">{t("mailbox.error")}</Badge> : <Badge tone="gray">{t("mailbox.disabled")}</Badge>;
}

export function CheckPill({ status }: { status: HealthStatus }) {
  const { t } = useTranslation();
  return status === "ok" ? <Badge tone="ok">{t("check.ok")}</Badge> : status === "warn" ? <Badge tone="warn">{t("check.warn")}</Badge> : <Badge tone="danger">{t("check.fail")}</Badge>;
}

export const JOB_TONE: Record<JobState, Tone> = {
  available: "blue",
  running: "violet",
  retryable: "amber",
  scheduled: "cyan",
  pending: "gray",
  completed: "green",
  cancelled: "gray",
  discarded: "red",
};

export function JobStatePill({ state }: { state: JobState }) {
  const { t } = useTranslation();
  return <Badge tone={JOB_TONE[state] ?? "gray"}>{t(`jobState.${state}`, { defaultValue: humanize(state) })}</Badge>;
}

/** A test workspace (contracts/admin.md "Test workspaces"). */
export function TestPill() {
  const { t } = useTranslation();
  return <Badge tone="violet">{t("test")}</Badge>;
}

/** A yes/no fact: "2FA on" in green, "2FA off" in grey (or amber when it should be on). */
export function FlagPill({ on, yes, no, warnOff }: { on: boolean; yes: string; no: string; warnOff?: boolean }) {
  return on ? <Badge tone="green">{yes}</Badge> : <Badge tone={warnOff ? "amber" : "gray"}>{no}</Badge>;
}
