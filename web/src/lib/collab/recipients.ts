// Recipients, subjects and quoting for replies and forwards from a mail account.
// Reply all is computed here, in the browser: everyone on the replied message except
// the account's own address (contracts/collab.md, POST /v1/collab/messages).

import type { MailAddress, MailMessage } from "../api/types";
import { escapeHtml } from "./html";

export type ComposeMode = "new" | "reply" | "reply_all" | "forward";

const EMAIL = /^[^\s@,;<>"]+@[^\s@,;<>"]+\.[^\s@,;<>"]+$/;

export function isEmail(s: string): boolean {
  return EMAIL.test(s.trim());
}

export const normEmail = (s: string) => s.trim().toLowerCase();

/** "Ada Lovelace <ada@example.com>", or the bare address. */
export function formatAddress(a: MailAddress): string {
  return a.name && a.name !== a.email ? `${a.name} <${a.email}>` : a.email;
}

/** The addresses typed or pasted into a field: "a@x.com, Ada <ada@y.com>; b@z.com". Invalid parts are returned apart. */
export function parseAddressList(text: string): { valid: string[]; invalid: string[] } {
  const valid: string[] = [];
  const invalid: string[] = [];
  for (const raw of text.split(/[,;\n]+/)) {
    const part = raw.trim();
    if (!part) continue;
    const angle = /<([^<>]+)>\s*$/.exec(part);
    const candidate = (angle ? angle[1] : part).trim();
    // A pasted list separated by spaces: "a@x.com b@y.com".
    const words = angle ? [candidate] : candidate.split(/\s+/);
    for (const w of words) {
      if (isEmail(w)) valid.push(w);
      else if (w) invalid.push(w);
    }
  }
  return { valid: dedupe(valid), invalid };
}

/** Unique addresses, case-insensitive, keeping the first spelling and order. */
export function dedupe(list: string[]): string[] {
  const seen = new Set<string>();
  return list.filter((e) => {
    const k = normEmail(e);
    if (!k || seen.has(k)) return false;
    seen.add(k);
    return true;
  });
}

/** The message a reply answers: the newest one that is not a draft. */
export function replyTarget(messages: MailMessage[]): MailMessage | undefined {
  for (let i = messages.length - 1; i >= 0; i--) if (!messages[i].draft) return messages[i];
  return messages[messages.length - 1];
}

/**
 * Who a reply goes to. Reply: the message's Reply-To, else its sender; when the
 * account sent it, its recipients again. Reply all: those plus everyone on To and
 * Cc, without the account's own address. Forward and new: nobody yet.
 */
export function replyRecipients(m: MailMessage, ownEmail: string, mode: ComposeMode): { to: string[]; cc: string[] } {
  if (mode === "new" || mode === "forward") return { to: [], cc: [] };
  const own = normEmail(ownEmail);
  const notOwn = (e: string) => normEmail(e) !== own && isEmail(e);
  const fromUs = normEmail(m.from.email) === own;
  const primary = fromUs ? m.to.map((a) => a.email) : (m.reply_to.length ? m.reply_to : [m.from]).map((a) => a.email);
  if (mode === "reply") {
    const to = dedupe(primary.filter(notOwn));
    // Replying to our own message with nobody else on it: back to ourselves is never wanted.
    return { to, cc: [] };
  }
  const to = dedupe([...primary, ...(fromUs ? [] : m.to.map((a) => a.email))].filter(notOwn));
  const inTo = new Set(to.map(normEmail));
  const cc = dedupe(m.cc.map((a) => a.email).filter((e) => notOwn(e) && !inTo.has(normEmail(e))));
  return { to, cc };
}

/** "Re: Subject" or "Fwd: Subject", without stacking prefixes. */
export function replySubject(subject: string, mode: ComposeMode): string {
  const s = subject.trim();
  if (mode === "new") return s;
  if (mode === "forward") return /^(fwd?|fw):\s/i.test(s) ? s : `Fwd: ${s}`;
  return /^re:\s/i.test(s) ? s : `Re: ${s}`;
}

/** Whether "reply all" would reach more people than "reply". */
export function hasReplyAll(m: MailMessage, ownEmail: string): boolean {
  const one = replyRecipients(m, ownEmail, "reply");
  const all = replyRecipients(m, ownEmail, "reply_all");
  return all.to.length + all.cc.length > one.to.length + one.cc.length;
}

/** The header block a forward starts with, above the original message (its HTML was sanitized by the server). */
export function forwardHeader(m: MailMessage, labels: { title: string; from: string; date: string; subject: string; to: string }, date: string): string {
  const row = (k: string, v: string) => `${escapeHtml(k)}: ${escapeHtml(v)}<br>`;
  return `<p>---------- ${escapeHtml(labels.title)} ----------<br>${row(labels.from, formatAddress(m.from))}${row(labels.date, date)}${row(labels.subject, m.subject)}${row(labels.to, m.to.map(formatAddress).join(", "))}</p>`;
}

/** Participants as a short line: first names, the account's own address as "me". */
export function participantsLine(people: MailAddress[], ownEmails: Set<string>, me: string, max = 3): string {
  const names: string[] = [];
  for (const p of people) {
    const own = ownEmails.has(normEmail(p.email));
    const label = own ? me : (p.name || p.email).split(/[\s@]/)[0] || p.email;
    if (!names.includes(label)) names.push(label);
  }
  if (names.length <= max) return names.join(", ");
  return `${names.slice(0, max).join(", ")} +${names.length - max}`;
}
