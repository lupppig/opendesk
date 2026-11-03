// Collab access end dates: the presets the panel offers, and turning a choice
// into the RFC 3339 time the API takes (contracts/admin.md "Collab access").

import { i18n } from "@/i18n";

export type UntilChoice = "none" | "14" | "30" | "90" | "365" | "date";

/** The choices, in order; each label is `collab.until.<choice>` in the workspaces catalog. */
export const UNTIL_CHOICES: UntilChoice[] = ["none", "14", "30", "90", "365", "date"];

const YMD = /^(\d{4})-(\d{2})-(\d{2})$/;

/** A local calendar day as YYYY-MM-DD. */
export function ymd(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/**
 * The end of complimentary access for a choice: null for no end date, an ISO
 * time for a preset (now plus the days) or a date (the end of that local day),
 * or an error when the date is not a real day in the future.
 */
export function untilFor(choice: UntilChoice, date: string, now = new Date()): { until: string | null } | { error: string } {
  if (choice === "none") return { until: null };
  if (choice !== "date") return { until: new Date(now.getTime() + Number(choice) * 86_400_000).toISOString() };
  const m = YMD.exec(date.trim());
  if (!m) return { error: i18n.t("workspaces:collab.dateError.format") };
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const end = new Date(y, mo - 1, d, 23, 59, 59);
  if (end.getFullYear() !== y || end.getMonth() !== mo - 1 || end.getDate() !== d) return { error: i18n.t("workspaces:collab.dateError.noSuchDay") };
  if (end.getTime() <= now.getTime()) return { error: i18n.t("workspaces:collab.dateError.past") };
  return { until: end.toISOString() };
}
