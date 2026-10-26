// Notification preferences (contracts/api.md "Notification preferences and emails"):
// the defaults, the pause, the desktop decision and the optimistic patch. Pure, so
// the settings page, the desktop bridge and the mock share one reading of them.

import type {
  NotificationChannels,
  NotificationGroup,
  NotificationPacing,
  NotificationPreferences,
  NotificationPreferencesPatch,
  NotificationType,
  NotificationTypePrefs,
} from "./api/types";

const on: NotificationChannels = { email: true, push: true, desktop: true };

/** Each type's group and the channels it follows until the person sets them. */
export const NOTIFICATION_DEFAULTS: Record<NotificationType, { group: NotificationGroup; defaults: NotificationChannels }> = {
  assigned: { group: "tickets", defaults: on },
  // A new ticket nobody is assigned to (contracts/api.md "Assignment").
  new_ticket: { group: "tickets", defaults: on },
  mentioned: { group: "tickets", defaults: on },
  customer_replied: { group: "tickets", defaults: on },
  thread_replied: { group: "tickets", defaults: on },
  sla_breached: { group: "tickets", defaults: on },
  ai_handoff: { group: "tickets", defaults: on },
  team_joined: { group: "team", defaults: { email: false, push: false, desktop: true } },
  // Collab mail (contracts/collab.md "Notifications"): everywhere by default, like the ticket ones.
  collab_assigned: { group: "mail", defaults: on },
  collab_mention: { group: "mail", defaults: on },
  ai_cap_reached: { group: "ai_billing", defaults: { email: true, push: false, desktop: true } },
  ai_key_failed: { group: "ai_billing", defaults: { email: true, push: false, desktop: true } },
  ai_balance_low: { group: "ai_billing", defaults: { email: true, push: false, desktop: true } },
  ai_balance_out: { group: "ai_billing", defaults: { email: true, push: false, desktop: true } },
  auto_recharge_failed: { group: "ai_billing", defaults: { email: true, push: false, desktop: true } },
  import_finished: { group: "workspace", defaults: on },
};

/** The order rows are listed in, within and across groups. */
export const NOTIFICATION_ORDER: NotificationType[] = [
  "assigned",
  "new_ticket",
  "mentioned",
  "customer_replied",
  "thread_replied",
  "sla_breached",
  "ai_handoff",
  "team_joined",
  "collab_assigned",
  "collab_mention",
  "ai_cap_reached",
  "ai_key_failed",
  "ai_balance_low",
  "ai_balance_out",
  "auto_recharge_failed",
  "import_finished",
];

/** Types that may be emailed sooner than the cooldown. */
export const URGENT_TYPES: ReadonlySet<NotificationType> = new Set<NotificationType>([
  "mentioned",
  "sla_breached",
  "ai_handoff",
  "ai_balance_out",
  "auto_recharge_failed",
  "ai_key_failed",
]);

/** The pacing opendesk Cloud uses, for when the server sends none. */
export const DEFAULT_PACING: NotificationPacing = {
  delay_minutes: 5,
  cooldown_minutes: [15, 30, 60],
  quiet_reset_minutes: 120,
  urgent_gap_minutes: 10,
  urgent_per_day: 6,
  per_hour: 4,
  per_day: 24,
  active_minutes: 5,
};

/** The numbers the settings page explains pacing with. */
export function pacingSummary(p: NotificationPacing | undefined, delayFallback?: number) {
  const pacing = p ?? { ...DEFAULT_PACING, delay_minutes: delayFallback ?? DEFAULT_PACING.delay_minutes };
  const cooldowns = pacing.cooldown_minutes.filter((n) => Number.isFinite(n) && n > 0);
  return {
    delay: pacing.delay_minutes,
    /** The longest gap between emails while activity continues; 0 when there is no cooldown. */
    maxGap: cooldowns.length ? Math.max(...cooldowns) : 0,
    urgentGap: pacing.urgent_gap_minutes,
    perHour: pacing.per_hour,
    perDay: pacing.per_day,
  };
}

/** A gap in words: "hour", "2 hours" or "45 minutes" (the i18n key under notifications.gap and its count). */
export type GapWords = { key: "hour" | "hours" | "minutes"; count: number };

export function gapWords(minutes: number): GapWords {
  if (minutes === 60) return { key: "hour", count: 1 };
  if (minutes > 60 && minutes % 60 === 0) return { key: "hours", count: minutes / 60 };
  return { key: "minutes", count: minutes };
}

/**
 * Which sentences the settings page explains pacing with, and their numbers:
 * the "Right away" hint (with or without a cooldown), the limits line and the
 * urgent tooltip.
 */
export function pacingCopy(p: NotificationPacing | undefined, delayFallback?: number) {
  const s = pacingSummary(p, delayFallback);
  return {
    instant: s.maxGap > 0 ? { key: "instantPaced" as const, delay: s.delay, gap: gapWords(s.maxGap) } : { key: "instantHint" as const, delay: s.delay, gap: null },
    limits: { perHour: s.perHour, perDay: s.perDay },
    urgent: gapWords(s.urgentGap),
  };
}

export const NOTIFICATION_GROUPS: NotificationGroup[] = ["tickets", "team", "mail", "ai_billing", "workspace"];

/** Groups only owners and admins receive. */
export const ADMIN_GROUPS: ReadonlySet<NotificationGroup> = new Set<NotificationGroup>(["ai_billing", "workspace"]);

/** The longest pause the API takes. */
export const MAX_PAUSE_MS = 30 * 86_400_000;

/** True while a pause is running. */
export function isPaused(pausedUntil: string | null | undefined, now = Date.now()): boolean {
  if (!pausedUntil) return false;
  const t = Date.parse(pausedUntil);
  return Number.isFinite(t) && t > now;
}

/**
 * Whether the desktop app shows an OS notification of this type: its `desktop`
 * channel is on and nothing is paused. Without preferences loaded, the defaults
 * apply (desktop is on for every type).
 */
export function wantsDesktop(prefs: NotificationPreferences | undefined, type: NotificationType, now = Date.now()): boolean {
  if (!prefs) return NOTIFICATION_DEFAULTS[type]?.defaults.desktop ?? true;
  if (isPaused(prefs.paused_until, now)) return false;
  const row = prefs.types.find((t) => t.type === type);
  return row ? row.desktop : (NOTIFICATION_DEFAULTS[type]?.defaults.desktop ?? true);
}

export type PauseChoice = "hour" | "tomorrow" | "week";

/** The hour "until tomorrow morning" ends at, in local time. */
export const MORNING_HOUR = 9;

/** When a pause chosen now ends: in an hour, tomorrow at 9:00 local time, or in a week. */
export function pauseEnd(choice: PauseChoice, now: Date = new Date()): Date {
  if (choice === "hour") return new Date(now.getTime() + 3_600_000);
  if (choice === "week") return new Date(now.getTime() + 7 * 86_400_000);
  const d = new Date(now);
  d.setDate(d.getDate() + 1);
  d.setHours(MORNING_HOUR, 0, 0, 0);
  return d;
}

/** The preferences with a patch applied, as the server will answer it (for optimistic updates). */
export function applyPreferencesPatch(prefs: NotificationPreferences, patch: NotificationPreferencesPatch): NotificationPreferences {
  const next: NotificationPreferences = { ...prefs };
  if (patch.email_cadence !== undefined) next.email_cadence = patch.email_cadence;
  if (patch.digest_hour !== undefined) next.digest_hour = patch.digest_hour;
  if (patch.timezone !== undefined) next.timezone = patch.timezone;
  if (patch.paused_until !== undefined) next.paused_until = patch.paused_until === "" ? null : patch.paused_until;
  if (patch.types?.length) {
    const changes = new Map(patch.types.map((t) => [t.type, t]));
    next.types = prefs.types.map((row) => {
      const c = changes.get(row.type);
      if (!c) return row;
      const out: NotificationTypePrefs = { ...row };
      if (c.email !== undefined) out.email = c.email;
      if (c.push !== undefined) out.push = c.push;
      if (c.desktop !== undefined) out.desktop = c.desktop;
      return out;
    });
  }
  if (patch.teams?.length) {
    const changes = new Map(patch.teams.map((t) => [t.id, t.notify]));
    next.teams = prefs.teams.map((row) => (changes.has(row.id) ? { ...row, notify: changes.get(row.id)! } : row));
  }
  return next;
}

/** The rows by group, in the fixed order; empty groups are left out. Unknown types go last in their group. */
export function groupTypes(types: NotificationTypePrefs[]): { group: NotificationGroup; types: NotificationTypePrefs[] }[] {
  const rank = (t: NotificationType) => {
    const i = NOTIFICATION_ORDER.indexOf(t);
    return i < 0 ? NOTIFICATION_ORDER.length : i;
  };
  return NOTIFICATION_GROUPS.map((group) => ({
    group,
    types: types.filter((t) => t.group === group).sort((a, b) => rank(a.type) - rank(b.type)),
  })).filter((g) => g.types.length > 0);
}

/** The browser's IANA time zone, or UTC when it can't tell. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/** Why notifications are not reaching someone outside the app, most important first; null when nothing holds them. */
export type NotificationHold =
  | { kind: "unsubscribed"; since: string }
  | { kind: "paused"; until: string }
  | { kind: "emailOff" };

/**
 * What stops notifications here right now: an unsubscribe link (email from this workspace),
 * a pause (everything outside the app), or the email schedule set to off. Each has a one-click undo.
 */
export function notificationHolds(p: Pick<NotificationPreferences, "unsubscribed_at" | "paused_until" | "email_cadence">, now = Date.now()): NotificationHold[] {
  const out: NotificationHold[] = [];
  if (p.unsubscribed_at) out.push({ kind: "unsubscribed", since: p.unsubscribed_at });
  if (p.paused_until && Date.parse(p.paused_until) > now) out.push({ kind: "paused", until: p.paused_until });
  if (p.email_cadence === "off") out.push({ kind: "emailOff" });
  return out;
}
