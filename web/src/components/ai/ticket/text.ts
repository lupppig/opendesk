// Pure helpers for the ticket AI features: streamed text to editor HTML, and
// language detection results (custom.language) to "should we offer Translate".

import type { Message, Ticket } from "@/lib/api/types";

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

/** Plain streamed text to paragraphs: blank lines split paragraphs, single newlines become <br>. */
export function streamTextToHtml(text: string): string {
  const paras = text
    .replace(/\r\n/g, "\n")
    .split(/\n{2,}/)
    .map((p) => p.replace(/^\n+|\n+$/g, ""))
    .filter((p) => p.trim().length > 0);
  if (!paras.length) return "";
  return paras.map((p) => `<p>${esc(p).replace(/\n/g, "<br>")}</p>`).join("");
}

/** Base language subtag, lowercased: "pt-BR" -> "pt". */
export function baseLanguage(tag: string | null | undefined): string {
  return (tag ?? "").trim().toLowerCase().split(/[-_]/)[0] ?? "";
}

/** True when `tag` names a real language (not empty, undetermined or "no linguistic content"). */
export function isLanguage(tag: string | null | undefined): tag is string {
  const b = baseLanguage(tag);
  return !!b && b !== "und" && b !== "zxx";
}

/** True when `tag` names a real language that differs from `ours` (by base subtag). */
export function isForeign(tag: string | null | undefined, ours = "en"): boolean {
  return isLanguage(tag) && baseLanguage(tag) !== baseLanguage(ours);
}

/** "es" -> "Spanish". Falls back to the tag itself. */
export function languageName(tag: string, displayIn = "en"): string {
  try {
    const dn = new Intl.DisplayNames([displayIn], { type: "language" });
    return dn.of(tag) ?? tag;
  } catch {
    return tag;
  }
}

/** Languages a draft can be translated into from the composer (the EU's and other common ones). */
export const TRANSLATE_LANGUAGES = [
  "ar", "bg", "cs", "da", "de", "el", "en", "es", "et", "fi", "fr", "ga", "he", "hi", "hr", "hu", "id", "it", "ja", "ko",
  "lt", "lv", "mt", "nl", "no", "pl", "pt", "ro", "ru", "sk", "sl", "sv", "th", "tr", "uk", "vi", "zh",
];

/** The composer's translation targets: the customer's language first, then the others by name. */
export function translateTargets(customer: string | null, displayIn: string): string[] {
  const first = isLanguage(customer) ? customer : null;
  const rest = TRANSLATE_LANGUAGES.filter((l) => !first || baseLanguage(first) !== l)
    .map((l) => [l, languageName(l, displayIn)] as const)
    .sort((a, b) => a[1].localeCompare(b[1], displayIn))
    .map(([l]) => l);
  return first ? [first, ...rest] : rest;
}

function str(v: unknown): string | null {
  return typeof v === "string" && v.trim() ? v.trim() : null;
}

/** The customer's language for a ticket: ticket.custom.language. */
export function ticketLanguage(t: Pick<Ticket, "custom"> | null | undefined): string | null {
  return str(t?.custom?.language);
}

type WithCustom = { custom?: Record<string, unknown> | null };

/** A message's own detected language, else its ticket's. */
export function messageLanguage(m: Message, t: Pick<Ticket, "custom"> | null | undefined): string | null {
  return str((m as Message & WithCustom).custom?.language) ?? ticketLanguage(t);
}

/** Bot answers in chat carry `ai: true` (contracts/ai.md). */
export function isAiMessage(m: Message): boolean {
  return m.ai === true;
}

/** Merge a draft below existing content, or replace it. */
export function combineDraft(existing: string, next: string, mode: "replace" | "below"): string {
  if (mode === "replace" || !existing.trim()) return next;
  return `${existing}${next}`;
}
