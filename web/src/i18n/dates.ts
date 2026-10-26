// Calendar dates in the agent app's language: the date-fns locale for month and
// weekday names, and the short patterns each language writes a date in
// ("Mar 4" in English, "4 mars" in French, "4. März" in German).

import { de, fr } from "date-fns/locale";
import type { Locale } from "date-fns";
import { AGENT_LOCALES, type AgentLocale, i18n } from "./index";

export type DatePattern = "dayMonth" | "dayMonthYear" | "weekdayDayMonth" | "field";

const LOCALES: Record<string, Locale> = { fr, de };

const PATTERNS: Record<AgentLocale, Record<DatePattern, string>> = {
  en: { dayMonth: "MMM d", dayMonthYear: "MMM d, yyyy", weekdayDayMonth: "EEE, MMM d", field: "d MMM yyyy" },
  fr: { dayMonth: "d MMM", dayMonthYear: "d MMM yyyy", weekdayDayMonth: "EEE d MMM", field: "d MMM yyyy" },
  de: { dayMonth: "d. MMM", dayMonthYear: "d. MMM yyyy", weekdayDayMonth: "EEE, d. MMM", field: "d. MMM yyyy" },
};

function lang(lng?: string): string {
  return (lng ?? i18n.resolvedLanguage ?? i18n.language ?? "en").split("-")[0];
}

/** The date-fns locale for the language, or undefined for English. */
export function dateLocale(lng?: string): Locale | undefined {
  return LOCALES[lang(lng)];
}

/** The date-fns pattern for the language. */
export function datePattern(pattern: DatePattern, lng?: string): string {
  const l = lang(lng);
  return PATTERNS[(AGENT_LOCALES as readonly string[]).includes(l) ? (l as AgentLocale) : "en"][pattern];
}
