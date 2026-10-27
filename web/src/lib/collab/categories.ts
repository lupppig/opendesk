// Inbox categories (contracts/collab.md "Categories"): the tabs above an inbox, worked
// out from the accounts in view, and the editor's helpers. Pure, so the tabs, the
// counts and the editor agree.

import type { MailAccount, MailCategory, MailCounts } from "../api/types";
import { isTone, type Tone } from "../tones";

export const PRIMARY = "primary";
export const MAX_CATEGORIES = 8;
export const MIN_CATEGORIES = 2;

/** A tab: a category key with the first in-scope account's name and colour for it. */
export type CategoryTab = MailCategory & { unread: number };

/**
 * The tabs of an inbox spanning these accounts: the union of their categories by key,
 * Primary first, then in the order they first appear. None when no account is split.
 */
export function categoryTabs(accounts: MailAccount[], counts?: MailCounts): CategoryTab[] {
  const split = accounts.filter((a) => a.categories?.enabled && a.categories.categories.length > 1);
  if (!split.length) return [];
  const byKey = new Map<string, CategoryTab>();
  for (const a of split) {
    for (const c of a.categories.categories) {
      const tab = byKey.get(c.key) ?? { ...c, unread: 0 };
      tab.unread += counts?.categories?.[a.id]?.[c.key] ?? 0;
      byKey.set(c.key, tab);
    }
  }
  // Accounts not split show all their inbox under Primary.
  const primary = byKey.get(PRIMARY);
  if (primary) for (const a of accounts) if (!split.includes(a)) primary.unread += counts?.accounts[a.id] ?? 0;
  return [...byKey.values()].sort((x, y) => (x.key === PRIMARY ? -1 : y.key === PRIMARY ? 1 : 0));
}

/** A category's tone; an unknown colour reads as grey. */
export const categoryTone = (c: Pick<MailCategory, "color">): Tone => (isTone(c.color) ? c.color : "gray");

/** The categories a conversation of this account may move to. */
export function accountCategories(a: MailAccount | undefined): MailCategory[] {
  return a?.categories?.enabled ? a.categories.categories : [];
}

/** A key from a name, as the server makes it. */
export function slugKey(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 36);
}

/** Keys for categories added in the editor (left empty, the server makes them). */
export function withKeys(list: MailCategory[]): MailCategory[] {
  const seen = new Set<string>();
  return list.map((c, i) => {
    let key = i === 0 ? PRIMARY : c.key || slugKey(c.name) || "category";
    if (i > 0 && key === PRIMARY) key = "category";
    for (let n = 2, base = key; seen.has(key); n++) key = `${base}-${n}`;
    seen.add(key);
    return { ...c, key };
  });
}

/** What is wrong with a set before it is saved, as a message key of collab categories.problem, or null. */
export function categoryProblem(list: MailCategory[]): "count" | "name" | "repeat" | null {
  if (list.length < MIN_CATEGORIES || list.length > MAX_CATEGORIES) return "count";
  const names = new Set<string>();
  for (const c of list) {
    const n = c.name.trim().toLowerCase();
    if (!n || n.length > 40) return "name";
    if (names.has(n)) return "repeat";
    names.add(n);
  }
  return null;
}

/** The sender a rule would name for a conversation: its latest sender that is not one of ours. */
export function ruleSender(participants: { email: string }[], own: Set<string>): string | null {
  const p = participants.find((x) => x.email && !own.has(x.email.toLowerCase()));
  return p ? p.email.toLowerCase() : null;
}
