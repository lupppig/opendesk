// Display formatting. Dates and numbers go through Intl with the active locale
// (`intlLocale()`: the page language on customer-facing pages, the browser's
// locale in the agent app), so a German visitor reads "11. Okt." and 24-hour
// times while an American agent reads "Oct 11" and "2:05 PM". In the agent app,
// the signed-in person's clock (`setClock`, from their profile) picks 12 or 24
// hour times; "auto" keeps the locale's own. Customer-facing pages always use
// the page language's clock.

import { intlLocale, pageLanguage } from "@/i18n";
import type { TimeFormat } from "@/lib/api/types";

const cache = new Map<string, Intl.DateTimeFormat | Intl.NumberFormat | Intl.RelativeTimeFormat>();
function memo<T extends Intl.DateTimeFormat | Intl.NumberFormat | Intl.RelativeTimeFormat>(key: string, make: () => T): T {
  let f = cache.get(key) as T | undefined;
  if (!f) {
    f = make();
    cache.set(key, f);
  }
  return f;
}

let agentClock: TimeFormat = "auto";

/** The agent app's clock for times of day: "12h", "24h", or "auto" for the locale's own. */
export function setClock(tf: TimeFormat | null | undefined) {
  agentClock = tf === "12h" || tf === "24h" ? tf : "auto";
}

/** The hour cycle times of day follow: the person's clock in the agent app, the locale's on customer pages. */
function hourCycle(): Intl.DateTimeFormatOptions["hourCycle"] | undefined {
  if (pageLanguage() || agentClock === "auto") return undefined;
  return agentClock === "12h" ? "h12" : "h23";
}

function dtf(opts: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const loc = intlLocale();
  const hc = opts.hour ? hourCycle() : undefined;
  const o = hc ? { ...opts, hourCycle: hc } : opts;
  return memo(`d|${loc}|${JSON.stringify(o)}`, () => new Intl.DateTimeFormat(loc, o));
}
function nf(opts: Intl.NumberFormatOptions = {}): Intl.NumberFormat {
  const loc = intlLocale();
  return memo(`n|${loc}|${JSON.stringify(opts)}`, () => new Intl.NumberFormat(loc, opts));
}
function rtf(opts: Intl.RelativeTimeFormatOptions): Intl.RelativeTimeFormat {
  const loc = intlLocale();
  return memo(`r|${loc}|${JSON.stringify(opts)}`, () => new Intl.RelativeTimeFormat(loc, opts));
}

const isEnglish = () => intlLocale().toLowerCase().startsWith("en");

function sameYear(d: Date, now = new Date()) {
  return d.getFullYear() === now.getFullYear();
}
function startOfDay(d: Date) {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}
function capitalize(s: string) {
  return s ? s.charAt(0).toLocaleUpperCase(intlLocale()) + s.slice(1) : s;
}

/** Compact age for dense lists: "now", "5m", "2h", "3d", then a short date. */
export function relShort(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  const s = Math.round((now - t) / 1000);
  const m = Math.round(s / 60);
  const h = Math.round(m / 60);
  const d = Math.round(h / 24);
  if (d < 7) {
    if (isEnglish()) {
      if (s < 45) return "now";
      if (m < 60) return `${m}m`;
      if (h < 24) return `${h}h`;
      return `${d}d`;
    }
    const f = rtf({ style: "narrow", numeric: "auto" });
    if (s < 45) return f.format(0, "second");
    if (m < 60) return f.format(-m, "minute");
    if (h < 24) return f.format(-h, "hour");
    return f.format(-d, "day");
  }
  return shortDate(iso);
}

/** "5 minutes ago", "2 days ago", in the active language. */
export function relLong(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "";
  const s = Math.round((now - Date.parse(iso)) / 1000);
  if (s < 45) return isEnglish() ? "just now" : rtf({ numeric: "auto" }).format(0, "second");
  const f = rtf({ numeric: "always" });
  const m = Math.round(s / 60);
  if (m < 60) return f.format(-Math.max(1, m), "minute");
  const h = Math.round(m / 60);
  if (h < 24) return f.format(-h, "hour");
  const d = Math.round(h / 24);
  if (d < 30) return f.format(-d, "day");
  const mo = Math.round(d / 30);
  if (mo < 12) return f.format(-mo, "month");
  return f.format(-Math.round(d / 365), "year");
}

/** "Sat, Oct 11, 2026, 2:05 PM" (en-US) or "Sa., 11. Okt. 2026, 14:05" (de). */
export function fullDate(iso: string | null | undefined): string {
  if (!iso) return "";
  return dtf({ weekday: "short", month: "short", day: "numeric", year: "numeric", hour: "numeric", minute: "2-digit" }).format(new Date(iso));
}

/** "October 11, 2026" / "11. Oktober 2026": a calm date for public pages. */
export function longDate(iso: string | null | undefined): string {
  if (!iso) return "";
  return dtf({ month: "long", day: "numeric", year: "numeric" }).format(new Date(iso));
}

/** "Today", "Yesterday", "Monday, Oct 6", or "Oct 6, 2024". */
export function dayLabel(iso: string, now = Date.now()): string {
  const d = new Date(iso);
  const diff = Math.round((startOfDay(new Date(now)) - startOfDay(d)) / 86_400_000);
  if (diff === 0 || diff === 1) return capitalize(rtf({ numeric: "auto" }).format(-diff, "day"));
  return sameYear(d, new Date(now))
    ? dtf({ weekday: "long", month: "short", day: "numeric" }).format(d)
    : dtf({ month: "short", day: "numeric", year: "numeric" }).format(d);
}

/** Time of day, on the 12 or 24 hour clock (the person's choice in the agent app, else the locale's). */
export function clock(iso: string): string {
  return dtf({ hour: "numeric", minute: "2-digit" }).format(new Date(iso));
}

/** Weekday and time of day: "Mon 9:00 AM" / "Mon 09:00", for times within the coming week. */
export function weekdayClock(d: Date | string): string {
  return dtf({ weekday: "short", hour: "numeric", minute: "2-digit" }).format(new Date(d));
}

/** Whether times of day read on the 12-hour clock (the person's choice in the agent app, else the locale's). */
export function twelveHour(): boolean {
  const hc = dtf({ hour: "numeric", minute: "2-digit" }).resolvedOptions().hourCycle;
  return hc === "h12" || hc === "h11";
}

/** A stored "HH:mm" time of day on the reader's clock: "9:00 AM" / "09:00". "24:00", the end of a day, reads "12:00 AM" on a 12-hour clock. */
export function timeOfDay(hhmm: string): string {
  const m = /^(\d{2}):(\d{2})$/.exec(hhmm);
  if (!m || !twelveHour()) return hhmm;
  return dtf({ hour: "numeric", minute: "2-digit" }).format(new Date(2000, 0, 1, Number(m[1]) % 24, Number(m[2])));
}

/** "Oct 6", or "Oct 6, 2024" outside the current year. */
export function shortDate(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  return sameYear(d) ? dtf({ month: "short", day: "numeric" }).format(d) : dtf({ month: "short", day: "numeric", year: "numeric" }).format(d);
}

/** "Jun 2021": how far back something goes. */
export function monthYear(iso: string | null | undefined): string {
  if (!iso) return "";
  return dtf({ month: "short", year: "numeric" }).format(new Date(iso));
}

/** 135 -> "2h 15m", 2880 -> "2d". */
export function minutes(n: number | null | undefined): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return "–";
  const m = Math.round(Math.abs(n));
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  if (h < 24) return rm ? `${h}h ${rm}m` : `${h}h`;
  const d = Math.floor(h / 24);
  const rh = h % 24;
  return rh ? `${d}d ${rh}h` : `${d}d`;
}

export function bytes(n: number): string {
  if (n < 1024) return `${number(n)} B`;
  if (n < 1024 * 1024) return `${number(n / 1024, { maximumFractionDigits: n < 10 * 1024 ? 1 : 0 })} KB`;
  if (n < 1024 * 1024 * 1024) return `${number(n / 1024 / 1024, { maximumFractionDigits: 1, minimumFractionDigits: 1 })} MB`;
  return `${number(n / 1024 / 1024 / 1024, { maximumFractionDigits: 1, minimumFractionDigits: 1 })} GB`;
}

/** English-only pluralizer for agent-app copy; customer pages use i18n plurals. */
export function plural(n: number, one: string, many = `${one}s`) {
  return `${number(n)} ${n === 1 ? one : many}`;
}

/** 1234 -> "1.2K" (en), "1234" -> "1234" in locales that don't abbreviate thousands. */
export function compact(n: number): string {
  return nf({ notation: "compact", maximumFractionDigits: 1 }).format(n);
}

/** Grouped number in the active locale: 12345.6 -> "12,345.6" / "12.345,6". */
export function number(n: number, opts?: Intl.NumberFormatOptions): string {
  return nf(opts).format(n);
}

/** Money from minor units (Stripe amounts): currency(1900, "usd") -> "$19.00"; whole amounts drop the decimals. */
export function currency(minorUnits: number, code: string, opts: { exact?: boolean } = {}): string {
  const cur = code.toUpperCase();
  const digits = memo(`c|${cur}`, () => new Intl.NumberFormat("en", { style: "currency", currency: cur })).resolvedOptions().maximumFractionDigits ?? 2;
  const value = minorUnits / 10 ** digits;
  const whole = Number.isInteger(value) && !opts.exact;
  return nf({ style: "currency", currency: cur, ...(whole ? { minimumFractionDigits: 0, maximumFractionDigits: 0 } : {}) }).format(value);
}

/** 0.874 -> "87%" (or "87 %" in fr/de). */
export function percent(ratio: number, digits = 0): string {
  return nf({ style: "percent", maximumFractionDigits: digits }).format(ratio);
}

/**
 * US dollars of AI usage (balances, allowances, per-call costs, prices). Amounts
 * are kept to the millionth of a dollar.
 * - default: cents, "$0.50", "$10.00", "$1,234.56";
 * - `precise`: small amounts keep three significant digits, "$0.0195",
 *   "$0.0042", "$0.000975", and a tiny non-zero amount reads "<$0.0001";
 *   from $1 up it is cents again;
 * - `tiny`: cents, but an amount under a cent keeps its digits like `precise`;
 *   for totals and breakdowns, "$1.61", "$0.35", "$0.0042";
 * - `trim`: whole dollars without cents, for prices and presets, "$3", "$0.30", "$1.125".
 */
export function formatUsd(n: number, opts: { precise?: boolean; tiny?: boolean; trim?: boolean } = {}): string {
  const usd = (min: number, max: number) => nf({ style: "currency", currency: "USD", minimumFractionDigits: min, maximumFractionDigits: max });
  const a = Math.abs(n);
  if (opts.trim) return usd(Number.isInteger(n) ? 0 : 2, 4).format(n);
  const precise = opts.precise || (opts.tiny && a < 0.01);
  if (!precise || a === 0 || a >= 1) return usd(2, 2).format(n);
  if (a < 0.0001) return `<${usd(0, 4).format(0.0001)}`;
  return usd(2, Math.min(6, Math.max(2, 2 - Math.floor(Math.log10(a))))).format(n);
}
