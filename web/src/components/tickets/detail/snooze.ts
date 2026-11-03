// The times a ticket's snooze menu offers (settings.snooze_presets), worked out on the agent's
// own clock when the menu opens, like a date picked by hand in it.

import type { TFunction } from "i18next";
import type { SnoozePreset, WorkspaceSettings } from "@/lib/api/types";

/** What the menu offers when the workspace sets nothing: in 1 hour, in 4 hours, tomorrow at 09:00, next week (Monday) at 09:00. */
export const DEFAULT_SNOOZE_PRESETS: readonly SnoozePreset[] = [
  { kind: "hours", hours: 1 },
  { kind: "hours", hours: 4 },
  { kind: "days", days: 1, time: "09:00" },
  { kind: "week", time: "09:00" },
];
export const MAX_SNOOZE_PRESETS = 8;
export const SNOOZE_MAX_HOURS = 168;
export const SNOOZE_MAX_DAYS = 365;
/** The list's quick snooze ("Snooze until tomorrow"): tomorrow at 09:00, whatever the menu offers. */
export const SNOOZE_TOMORROW: SnoozePreset = { kind: "days", days: 1, time: "09:00" };

/** The workspace's snooze times, or the defaults when it sets none. */
export function snoozePresets(settings: WorkspaceSettings | undefined): readonly SnoozePreset[] {
  const p = settings?.snooze_presets;
  return p && p.length ? p.slice(0, MAX_SNOOZE_PRESETS) : DEFAULT_SNOOZE_PRESETS;
}

/** Whether two lists offer the same times in the same order. */
export function sameSnoozePresets(a: readonly SnoozePreset[], b: readonly SnoozePreset[]): boolean {
  const key = (p: SnoozePreset) => (p.kind === "hours" ? `h${p.hours}` : p.kind === "days" ? `d${p.days}@${p.time}` : `w@${p.time}`);
  return a.length === b.length && a.every((p, i) => key(p) === key(b[i]));
}

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const formats = new Map<string, Intl.DateTimeFormat>();

/** The wall clock in timeZone at an instant. */
function wall(at: number, timeZone: string) {
  let f = formats.get(timeZone);
  if (!f) {
    f = new Intl.DateTimeFormat("en-US", { timeZone, hourCycle: "h23", weekday: "short", year: "numeric", month: "numeric", day: "numeric", hour: "numeric", minute: "numeric", second: "numeric" });
    formats.set(timeZone, f);
  }
  const p: Record<string, string> = {};
  for (const x of f.formatToParts(new Date(at))) p[x.type] = x.value;
  return { y: +p.year, mo: +p.month, d: +p.day, h: +p.hour % 24, mi: +p.minute, s: +p.second, wd: WEEKDAYS.indexOf(p.weekday) };
}

/** How far timeZone's clock is ahead of UTC at an instant, in milliseconds. */
function offset(at: number, timeZone: string): number {
  const w = wall(at, timeZone);
  return Date.UTC(w.y, w.mo - 1, w.d, w.h, w.mi, w.s) - (at - (((at % 1000) + 1000) % 1000));
}

/** The instant timeZone's clock reads a date and time (a day past the month's end rolls over); a time a clock change skips lands after it. */
function zoned(y: number, mo: number, d: number, h: number, mi: number, timeZone: string): Date {
  const local = Date.UTC(y, mo - 1, d, h, mi);
  const first = local - offset(local, timeZone);
  const second = local - offset(first, timeZone);
  // In a skipped hour the offsets alternate: choose the instant after the gap.
  return new Date(second + offset(second, timeZone) < local ? Math.max(first, second) : second);
}

function hhmm(time: string): [number, number] {
  const [h, m] = time.split(":").map(Number);
  return [h || 0, m || 0];
}

/** When a snooze time falls, from now, on timeZone's clock (the browser's by default). */
export function snoozeAt(p: SnoozePreset, now: Date = new Date(), timeZone: string = Intl.DateTimeFormat().resolvedOptions().timeZone): Date {
  const t = now.getTime();
  if (p.kind === "hours") {
    // The top of the hour at least that many hours away: "in 1 hour" at 10:20 is 12:00.
    const later = t + (p.hours + 1) * 3_600_000;
    const w = wall(later, timeZone);
    return new Date(later - (w.mi * 60 + w.s) * 1000 - (((later % 1000) + 1000) % 1000));
  }
  const today = wall(t, timeZone);
  const [h, mi] = hhmm(p.time);
  // Next week is the coming Monday, a week on when today is one.
  const days = p.kind === "days" ? p.days : (8 - today.wd) % 7 || 7;
  return zoned(today.y, today.mo, today.d + days, h, mi, timeZone);
}

/** How the menu names a snooze time: "In 4 hours", "Tomorrow", "In 3 days", "Next week". */
export function snoozeLabel(p: SnoozePreset, t: TFunction<"inbox">): string {
  if (p.kind === "hours") return t("snooze.inHours", { count: p.hours });
  if (p.kind === "week") return t("snooze.week");
  return p.days === 1 ? t("snooze.tomorrow") : t("snooze.inDays", { count: p.days });
}
