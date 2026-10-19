// Soft tinted pills for the states the panel shows: plan, subscription status,
// suspension, admin role, mailbox status, health checks and job states.

import { Badge } from "@/components/ui/badge";
import type { AdminRole, HealthStatus, JobState, Plan } from "@/lib/api/types";
import { humanize } from "@/lib/format";
import { ROLE_LABEL } from "@/lib/permissions";
import type { Tone } from "@/lib/tones";

const PLAN: Record<Plan, { label: string; tone: Tone }> = {
  free: { label: "Free", tone: "gray" },
  pro: { label: "Pro", tone: "blue" },
  collab: { label: "Collab", tone: "indigo" },
  self_hosted: { label: "Self-hosted", tone: "teal" },
};

export function PlanPill({ plan }: { plan: Plan }) {
  const p = PLAN[plan] ?? { label: humanize(plan), tone: "gray" as Tone };
  return <Badge tone={p.tone}>{p.label}</Badge>;
}

/** Stripe subscription statuses. Empty (no subscription) shows nothing. */
export function SubscriptionPill({ status }: { status: string }) {
  if (!status) return null;
  const t: Tone =
    status === "active" ? "green" : status === "trialing" ? "blue" : status === "past_due" || status === "unpaid" ? "amber" : status === "canceled" || status === "incomplete_expired" ? "gray" : "gray";
  return <Badge tone={t}>{humanize(status)}</Badge>;
}

export function SuspendedPill() {
  return (
    <Badge tone="red" dot>
      Suspended
    </Badge>
  );
}

const ROLE_TONE: Record<AdminRole, Tone> = { viewer: "gray", support: "blue", super: "indigo" };

export function AdminRolePill({ role }: { role: AdminRole }) {
  return <Badge tone={ROLE_TONE[role]}>{ROLE_LABEL[role]}</Badge>;
}

export function MailboxStatusPill({ status }: { status: "active" | "error" | "disabled" }) {
  return status === "active" ? <Badge tone="ok">Active</Badge> : status === "error" ? <Badge tone="danger">Error</Badge> : <Badge tone="gray">Disabled</Badge>;
}

export function CheckPill({ status }: { status: HealthStatus }) {
  return status === "ok" ? <Badge tone="ok">OK</Badge> : status === "warn" ? <Badge tone="warn">Warning</Badge> : <Badge tone="danger">Failing</Badge>;
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
  return <Badge tone={JOB_TONE[state] ?? "gray"}>{humanize(state)}</Badge>;
}

/** A test workspace (contracts/admin.md "Test workspaces"). */
export function TestPill() {
  return <Badge tone="violet">Test</Badge>;
}

/** A yes/no fact: "2FA on" in green, "2FA off" in grey (or amber when it should be on). */
export function FlagPill({ on, yes, no, warnOff }: { on: boolean; yes: string; no: string; warnOff?: boolean }) {
  return on ? <Badge tone="green">{yes}</Badge> : <Badge tone={warnOff ? "amber" : "gray"}>{no}</Badge>;
}
