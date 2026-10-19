// Collab access end dates: the presets the panel offers, and turning a choice
// into the RFC 3339 time the API takes (contracts/admin.md "Collab access").

export type UntilChoice = "none" | "14" | "30" | "90" | "365" | "date";

export const UNTIL_CHOICES: { value: UntilChoice; label: string }[] = [
  { value: "none", label: "No end date" },
  { value: "14", label: "For 2 weeks" },
  { value: "30", label: "For 30 days" },
  { value: "90", label: "For 90 days" },
  { value: "365", label: "For a year" },
  { value: "date", label: "Until a date" },
];

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
  if (!m) return { error: "Use a date like 2026-12-31." };
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const end = new Date(y, mo - 1, d, 23, 59, 59);
  if (end.getFullYear() !== y || end.getMonth() !== mo - 1 || end.getDate() !== d) return { error: "That day doesn't exist." };
  if (end.getTime() <= now.getTime()) return { error: "Pick a day in the future." };
  return { until: end.toISOString() };
}
