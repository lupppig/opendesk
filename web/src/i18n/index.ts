// i18n: i18next with one JSON catalog per namespace and language under
// ./locales/<lng>/<ns>.json. English is bundled; another language loads a
// namespace at a time, when something asks for it: a public page loads the
// namespaces it shows (setLocale), the agent app loads its catalogs before its
// first route renders (loadAgentCatalogs, from the root route), and a component
// reading a namespace that is not there yet loads it (useTranslation). Boot
// itself loads only `common`, so a help center or CSAT visitor never downloads
// the agent app's catalogs.
//
// The agent app ships English, French and German (AGENT_LOCALES), following the
// browser's language. A key a translation does not have yet falls back to
// English (fallbackLng), so a new English string never shows a raw key. The customer-facing surfaces (help center, CSAT,
// widget) ship every SUPPORTED language, picked from the visitor's browser with
// the workspace default as the fallback (see pickLocale).

import i18n, { type BackendModule, type ReadCallback } from "i18next";
import { initReactI18next } from "react-i18next";
import { en, NAMESPACES, type Namespace } from "./resources";

export const SUPPORTED = ["en", "es", "fr", "de", "pt", "it", "nl", "pl", "ja", "zh-CN", "ko", "tr", "az", "ar"] as const;
export type Locale = (typeof SUPPORTED)[number];
export const RTL: ReadonlySet<string> = new Set(["ar"]);

/** Native names, for language pickers. */
export const LOCALE_NAMES: Record<Locale, string> = {
  en: "English",
  es: "Español",
  fr: "Français",
  de: "Deutsch",
  pt: "Português",
  it: "Italiano",
  nl: "Nederlands",
  pl: "Polski",
  ja: "日本語",
  "zh-CN": "简体中文",
  ko: "한국어",
  tr: "Türkçe",
  az: "Azərbaycan dili",
  ar: "العربية",
};

/** Map any BCP 47 tag onto a supported locale, or null. */
export function matchLocale(tag: string | null | undefined): Locale | null {
  if (!tag) return null;
  const t = tag.trim().replace("_", "-").toLowerCase();
  const exact = SUPPORTED.find((l) => l.toLowerCase() === t);
  if (exact) return exact;
  const base = t.split("-")[0];
  if (base === "zh") return t.includes("hant") || t.endsWith("-tw") || t.endsWith("-hk") ? null : "zh-CN";
  return SUPPORTED.find((l) => l.toLowerCase() === base) ?? null;
}

/**
 * The language for a customer-facing page: an explicit `?lang=` first, then the
 * visitor's browser languages in order, then the workspace default, then English.
 */
export function pickLocale(opts: { explicit?: string | null; browser?: readonly string[]; workspaceDefault?: string | null } = {}): Locale {
  const browser = opts.browser ?? (typeof navigator !== "undefined" ? navigator.languages ?? [navigator.language] : []);
  return matchLocale(opts.explicit) ?? browser.map(matchLocale).find((x): x is Locale => !!x) ?? matchLocale(opts.workspaceDefault) ?? "en";
}

/** The languages the agent app is translated into. */
export const AGENT_LOCALES = ["en", "fr", "de"] as const;
export type AgentLocale = (typeof AGENT_LOCALES)[number];

/** The agent app's language from the browser's; a language it is not translated into gets English. */
export function pickAgentLocale(browser?: readonly string[]): AgentLocale {
  const lng = pickLocale({ browser });
  return (AGENT_LOCALES as readonly string[]).includes(lng) ? (lng as AgentLocale) : "en";
}

const loaders = import.meta.glob<{ default: Record<string, unknown> }>(["./locales/*/*.json", "!./locales/en/*.json"]);

const lazyBackend: BackendModule = {
  type: "backend",
  init() {},
  read(lng: string, ns: string, cb: ReadCallback) {
    const load = loaders[`./locales/${lng}/${ns}.json`];
    if (!load) return cb(null, {});
    load().then(
      (m) => cb(null, m.default),
      (e: unknown) => cb(e instanceof Error ? e : new Error(String(e)), null),
    );
  },
};

void i18n
  .use(lazyBackend)
  .use(initReactI18next)
  .init({
    lng: pickAgentLocale(),
    fallbackLng: "en",
    supportedLngs: [...SUPPORTED],
    // Only what every page reads loads at boot; the rest load on demand (see the top).
    ns: ["common"],
    defaultNS: "common",
    resources: { en },
    partialBundledLanguages: true,
    interpolation: { escapeValue: false },
    returnNull: false,
    react: { useSuspense: false },
  });

const agentLoads = new Map<string, Promise<void>>();

/**
 * Load the agent app's catalogs in its language, all at once, so its pages and
 * the strings read outside React (`i18n.t`) render translated from the first
 * paint. English is bundled. A namespace that fails to load stays English and
 * is tried again on the next call.
 */
export function loadAgentCatalogs(lng: string = pickAgentLocale()): Promise<void> {
  if (lng === "en") return Promise.resolve();
  let pending = agentLoads.get(lng);
  if (!pending) {
    pending = Promise.allSettled(
      NAMESPACES.filter((ns) => !i18n.hasResourceBundle(lng, ns) && loaders[`./locales/${lng}/${ns}.json`]).map(async (ns) => {
        const m = await loaders[`./locales/${lng}/${ns}.json`]();
        i18n.addResourceBundle(lng, ns, m.default, true, false);
      }),
    ).then((results) => {
      if (results.some((r) => r.status === "rejected")) agentLoads.delete(lng);
    });
    agentLoads.set(lng, pending);
  }
  return pending;
}

/**
 * A label (a field's entity, a view's name, a rewrite mode) placed inside a
 * sentence. English and French lowercase it; German keeps its capital, which
 * its nouns need.
 */
export function inSentence(label: string): string {
  return i18n.resolvedLanguage === "de" ? label : label.toLowerCase();
}

/** Public routes, which load only the namespaces they show. */
export function isPublicPath(pathname: string): boolean {
  return /^\/(help|csat)(\/|$)/.test(pathname);
}

// The language a customer-facing page chose, so Intl formatting follows the
// page (an English page for a German browser formats dates in English).
let pageLocale: string | null = null;
let localeVersion = 0;

/** Switch language for the page (customer surfaces) and set <html lang dir>. */
export async function setLocale(lng: Locale, ns: Namespace[] = ["common"]): Promise<void> {
  const version = ++localeVersion;
  pageLocale = lng;
  await i18n.loadLanguages(lng);
  await i18n.loadNamespaces(ns);
  if (version !== localeVersion) return;
  await i18n.changeLanguage(lng);
  if (version !== localeVersion) return;
  if (typeof document !== "undefined") {
    document.documentElement.lang = lng;
    document.documentElement.dir = RTL.has(lng) ? "rtl" : "ltr";
  }
}

/** Restore the browser's agent language when leaving a public page. */
export function resetLocale() {
  ++localeVersion;
  pageLocale = null;
  const lng = pickAgentLocale();
  if (i18n.language !== lng) void i18n.changeLanguage(lng);
  if (typeof document !== "undefined") {
    document.documentElement.lang = lng;
    document.documentElement.dir = "ltr";
  }
}

resetLocale();

/** The language a customer-facing page chose, or null in the agent app. */
export function pageLanguage(): string | null {
  return pageLocale;
}

/**
 * The active locale for Intl formatting: the page language on public pages, the
 * agent app's language in the app. A browser locale of that same language
 * (matchLocale) wins, so fr-CA or fr-CH keeps its own number and date formats.
 * An English agent app formats with the browser's locale, whatever its language.
 */
export function intlLocale(): string {
  const nav = typeof navigator !== "undefined" ? navigator : null;
  const active = pageLocale ?? (i18n.language && i18n.language !== "en" ? i18n.language : null);
  if (!active) return nav?.language || "en";
  const browser = nav ? (nav.languages?.length ? nav.languages : [nav.language]) : [];
  const lng = matchLocale(active);
  return browser.find((tag) => !!tag && matchLocale(tag) === lng) ?? active;
}

export { i18n };
