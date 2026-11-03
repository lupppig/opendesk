// i18n: i18next with one JSON catalog per namespace and language under
// ./locales/<lng>/<ns>.json, as in the agent app (web/src/i18n). The panel
// ships the agent app's languages, English, French and German, and follows the
// browser's language like it does; any other language gets English. A key a
// translation does not have yet falls back to English (fallbackLng), so a new
// English string never shows a raw key. The three languages are bundled: the
// panel is small, and loading its language before the first paint keeps the
// strings read outside React (route titles, toasts) translated.

import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { NAMESPACES, resources } from "./resources";

export const LOCALES = ["en", "fr", "de"] as const;
export type Locale = (typeof LOCALES)[number];

/** Map a BCP 47 tag onto a panel language, or null. */
export function matchLocale(tag: string | null | undefined): Locale | null {
  if (!tag) return null;
  const base = tag.trim().replace("_", "-").toLowerCase().split("-")[0];
  return LOCALES.find((l) => l === base) ?? null;
}

/** The panel's language from the browser's, in order of preference; English when none is translated. */
export function pickLocale(browser?: readonly string[]): Locale {
  const langs = browser ?? (typeof navigator !== "undefined" ? (navigator.languages?.length ? navigator.languages : [navigator.language]) : []);
  return langs.map(matchLocale).find((l): l is Locale => !!l) ?? "en";
}

/**
 * The locale for Intl formatting: the browser's own tag when it speaks the
 * panel's language (fr-CA, de-CH keep their region), else the language. An
 * English panel formats in the browser's locale, as the agent app does.
 */
export function intlLocale(): string {
  const nav = typeof navigator !== "undefined" ? navigator : null;
  const lng = matchLocale(i18n.language) ?? "en";
  if (lng === "en") return nav?.language || "en-US";
  const browser = nav ? (nav.languages?.length ? nav.languages : [nav.language]) : [];
  return browser.find((tag) => matchLocale(tag) === lng) ?? lng;
}

/**
 * A label (a job state, a kind of sample data) placed inside a sentence.
 * English and French lowercase it; German keeps its capital, which its nouns need.
 */
export function inSentence(label: string): string {
  return i18n.resolvedLanguage === "de" ? label : label.toLowerCase();
}

/** "a, b and c" in the panel's language. */
export function listOf(items: string[]): string {
  return new Intl.ListFormat(intlLocale(), { type: "conjunction" }).format(items);
}

void i18n.use(initReactI18next).init({
  lng: pickLocale(),
  fallbackLng: "en",
  supportedLngs: [...LOCALES],
  ns: [...NAMESPACES],
  defaultNS: "common",
  resources,
  initAsync: false,
  interpolation: { escapeValue: false },
  returnNull: false,
  react: { useSuspense: false },
});

if (typeof document !== "undefined") document.documentElement.lang = i18n.language;
i18n.on("languageChanged", (lng) => {
  if (typeof document !== "undefined") document.documentElement.lang = lng;
});

export { i18n };
