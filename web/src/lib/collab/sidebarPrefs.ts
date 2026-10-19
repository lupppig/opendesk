// Each member's sidebar (contracts/collab.md SidebarPrefs): section order, what is
// hidden, favorites, expanded accounts and two switches. Pure, so the sidebar, the
// customize dialog and the optimistic save read the prefs the same way. Ids the app
// does not know are kept as they are (another client may know them) and ignored here.

import type { MailAccount, MailFolder, SidebarBadge, SidebarPin, SidebarPrefs, SidebarSectionId } from "../api/types";
import { hasBox, isTabBox, type TabBox } from "./boxes";

export const SECTION_IDS: readonly SidebarSectionId[] = ["favorites", "inbox", "mail", "teams", "views", "workspace"];
export const MAX_PINS = 30;
export const MAX_TABS = 30;
export const MAX_BADGES = 300;
/** The folder bar of someone who never arranged it. */
export const DEFAULT_MAIL_TABS: readonly string[] = ["inbox", "starred", "sent", "drafts", "archive"];

export function defaultSidebarPrefs(): SidebarPrefs {
  return {
    order: [...SECTION_IDS],
    hidden: [],
    pinned: [],
    expanded_accounts: [],
    mark_read_on_open: true,
    unified_inbox: true,
    badges: {},
    mail_tabs: [...DEFAULT_MAIL_TABS],
  };
}

const BADGES: readonly SidebarBadge[] = ["unread", "total", "count", "awaiting", "urgent", "overdue", "open", "none"];
const isBadge = (v: unknown): v is SidebarBadge => typeof v === "string" && (BADGES as readonly string[]).includes(v);

function readBadges(v: unknown): Record<string, SidebarBadge> | null {
  if (!v || typeof v !== "object" || Array.isArray(v)) return null;
  const out: Record<string, SidebarBadge> = {};
  for (const [k, x] of Object.entries(v)) if (isBadge(x)) out[k] = x;
  return out;
}

/** Prefs with every field present (an older or partial answer reads as the defaults). */
export function normalizePrefs(p: Partial<SidebarPrefs> | null | undefined): SidebarPrefs {
  const d = defaultSidebarPrefs();
  if (!p) return d;
  return {
    order: Array.isArray(p.order) ? p.order : d.order,
    hidden: Array.isArray(p.hidden) ? p.hidden : d.hidden,
    pinned: Array.isArray(p.pinned) ? p.pinned.filter((x) => x && typeof x.id === "string" && typeof x.kind === "string") : d.pinned,
    expanded_accounts: Array.isArray(p.expanded_accounts) ? p.expanded_accounts : d.expanded_accounts,
    mark_read_on_open: typeof p.mark_read_on_open === "boolean" ? p.mark_read_on_open : d.mark_read_on_open,
    unified_inbox: typeof p.unified_inbox === "boolean" ? p.unified_inbox : d.unified_inbox,
    badges: readBadges(p.badges) ?? d.badges,
    mail_tabs: Array.isArray(p.mail_tabs) ? p.mail_tabs.filter((x): x is string => typeof x === "string") : d.mail_tabs,
  };
}

const isSection = (id: string): id is SidebarSectionId => (SECTION_IDS as readonly string[]).includes(id);

/** Every known section once, in the saved order; a section the saved order lacks goes where the default puts it. */
export function sectionOrder(p: SidebarPrefs): SidebarSectionId[] {
  const out: SidebarSectionId[] = [];
  for (const id of p.order) if (isSection(id) && !out.includes(id)) out.push(id);
  SECTION_IDS.forEach((id, i) => {
    if (out.includes(id)) return;
    // After the nearest earlier default section that is already placed.
    let at = 0;
    for (let j = i - 1; j >= 0; j--) {
      const k = out.indexOf(SECTION_IDS[j]);
      if (k >= 0) {
        at = k + 1;
        break;
      }
    }
    out.splice(at, 0, id);
  });
  return out;
}

/** The visible sections in order. */
export function visibleSections(p: SidebarPrefs): SidebarSectionId[] {
  return sectionOrder(p).filter((id) => !isHidden(p, `section:${id}`));
}

export function isHidden(p: SidebarPrefs, id: string): boolean {
  return p.hidden.includes(id);
}

export function setHidden(p: SidebarPrefs, id: string, hidden: boolean): SidebarPrefs {
  const has = p.hidden.includes(id);
  if (hidden === has) return p;
  return { ...p, hidden: hidden ? [...p.hidden, id] : p.hidden.filter((x) => x !== id) };
}

/** Moves a section one place up (-1) or down (1) among all sections; unknown saved ids stay at the end. */
export function moveSection(p: SidebarPrefs, id: SidebarSectionId, dir: -1 | 1): SidebarPrefs {
  const order = sectionOrder(p);
  const i = order.indexOf(id);
  const j = i + dir;
  if (i < 0 || j < 0 || j >= order.length) return p;
  [order[i], order[j]] = [order[j], order[i]];
  const unknown = p.order.filter((x) => !isSection(x));
  return { ...p, order: [...order, ...unknown] };
}

const samePin = (a: SidebarPin, b: SidebarPin) => a.kind === b.kind && a.id === b.id;

export function isPinned(p: SidebarPrefs, pin: SidebarPin): boolean {
  return p.pinned.some((x) => samePin(x, pin));
}

/** Pins or unpins; a new favorite goes last, and the list stops at 30. */
export function togglePin(p: SidebarPrefs, pin: SidebarPin): SidebarPrefs {
  if (isPinned(p, pin)) return { ...p, pinned: p.pinned.filter((x) => !samePin(x, pin)) };
  if (p.pinned.length >= MAX_PINS) return p;
  return { ...p, pinned: [...p.pinned, pin] };
}

export function movePin(p: SidebarPrefs, pin: SidebarPin, dir: -1 | 1): SidebarPrefs {
  const i = p.pinned.findIndex((x) => samePin(x, pin));
  const j = i + dir;
  if (i < 0 || j < 0 || j >= p.pinned.length) return p;
  const next = p.pinned.slice();
  [next[i], next[j]] = [next[j], next[i]];
  return { ...p, pinned: next };
}

/** Shows or folds an account's folders in the sidebar (folded by default). */
export function toggleExpanded(p: SidebarPrefs, accountId: string): SidebarPrefs {
  const has = p.expanded_accounts.includes(accountId);
  return { ...p, expanded_accounts: has ? p.expanded_accounts.filter((x) => x !== accountId) : [...p.expanded_accounts, accountId] };
}

/** Favorites whose target still exists, in order; one that is gone is skipped, not removed. */
export function livePins<T>(p: SidebarPrefs, resolve: (pin: SidebarPin) => T | null | undefined): { pin: SidebarPin; target: T }[] {
  const out: { pin: SidebarPin; target: T }[] = [];
  for (const pin of p.pinned) {
    const target = resolve(pin);
    if (target) out.push({ pin, target });
  }
  return out;
}

/** Hidden-id helpers, so callers never spell the prefixes. */
export const hid = {
  section: (id: SidebarSectionId) => `section:${id}`,
  box: (box: string) => `box:${box}`,
  account: (id: string) => `account:${id}`,
  /** A folder id, or a special box name to hide it in every account. */
  folder: (idOrBox: string) => `folder:${idOrBox}`,
  view: (id: string) => `view:${id}`,
  team: (id: string) => `team:${id}`,
  ticketFolder: (id: string) => `ticket_folder:${id}`,
};

// ── Numbers beside rows ──────────────────────────────────────────────────

/** Mail rows count conversations; ticket rows (inbox folders, views) and team rows count tickets. */
export type BadgeKind = "mail" | "ticket" | "team";
export type MailBadge = "unread" | "total" | "none";
export type TicketBadge = "count" | "awaiting" | "urgent" | "overdue" | "open" | "none";

const CHOICES: Record<BadgeKind, readonly SidebarBadge[]> = {
  mail: ["unread", "total", "none"],
  ticket: ["count", "awaiting", "urgent", "overdue", "none"],
  team: ["count", "open", "awaiting", "urgent", "overdue", "none"],
};

/** The numbers a kind of row can show, in menu order; the first is its default. */
export function badgeChoices(kind: "mail"): readonly MailBadge[];
export function badgeChoices(kind: "ticket" | "team"): readonly TicketBadge[];
export function badgeChoices(kind: BadgeKind): readonly (MailBadge | TicketBadge)[];
export function badgeChoices(kind: BadgeKind): readonly (MailBadge | TicketBadge)[] {
  return CHOICES[kind] as readonly (MailBadge | TicketBadge)[];
}

/** The word for a number (a key under customize.badge): count reads as Unread, Tickets or Unassigned by the row. */
export function badgeWord(v: SidebarBadge, kind: BadgeKind): "unread" | "total" | "tickets" | "unassigned" | "open" | "awaiting" | "urgent" | "overdue" | "none" {
  const k = asKind(v, kind);
  if (k !== "count") return k;
  return kind === "mail" ? "unread" : kind === "team" ? "unassigned" : "tickets";
}

/** A value read for a kind of row: one the row has no such number for reads as its default (unread, or the count). */
function asKind(v: SidebarBadge, kind: BadgeKind): MailBadge | TicketBadge {
  return CHOICES[kind].includes(v) ? (v as MailBadge | TicketBadge) : kind === "mail" ? "unread" : "count";
}

type Sections = SidebarSectionId | readonly SidebarSectionId[];

/**
 * The number a row shows: its own choice, else that of the first of its sections with one
 * (a favorite reads Favorites, then the section it comes from), else `fallback`: what it
 * always showed (unread for mail rows, the count for ticket rows, unless the row never had one).
 */
export function badgeFor(p: SidebarPrefs, rowId: string, sections: Sections, kind: "mail", fallback?: MailBadge): MailBadge;
export function badgeFor(p: SidebarPrefs, rowId: string, sections: Sections, kind: "ticket" | "team", fallback?: TicketBadge): TicketBadge;
export function badgeFor(p: SidebarPrefs, rowId: string, sections: Sections, kind: BadgeKind, fallback?: MailBadge | TicketBadge): MailBadge | TicketBadge;
export function badgeFor(p: SidebarPrefs, rowId: string, sections: Sections, kind: BadgeKind, fallback?: MailBadge | TicketBadge): MailBadge | TicketBadge {
  const own = p.badges[rowId];
  if (own) return asKind(own, kind);
  for (const s of typeof sections === "string" ? [sections] : sections) {
    const v = p.badges[`section:${s}`];
    if (v) return asKind(v, kind);
  }
  return fallback ?? (kind === "mail" ? "unread" : "count");
}

/** A folder bar tab's number: its own choice (the same key as the sidebar row), else unread on Inbox and none elsewhere. */
export function tabBadge(p: SidebarPrefs, tabId: string): MailBadge {
  const key = tabId.startsWith("folder:") ? tabId : `box:${tabId}`;
  const own = p.badges[key];
  if (own) return asKind(own, "mail") as MailBadge;
  return tabId === "inbox" ? "unread" : "none";
}

/** Sets a row's or section's number; null goes back to following its section (or the default). */
export function setBadge(p: SidebarPrefs, key: string, value: SidebarBadge | null): SidebarPrefs {
  if ((p.badges[key] ?? null) === value) return p;
  const next = { ...p.badges };
  if (value === null) delete next[key];
  else {
    if (!(key in next) && Object.keys(next).length >= MAX_BADGES) return p;
    next[key] = value;
  }
  return { ...p, badges: next };
}

/** Every number a row may have, by the choice that shows it (contracts/collab.md SidebarPrefs.badges). */
export type BadgeNumbers = Partial<Record<Exclude<SidebarBadge, "none">, number | null>>;

/** What a chosen number reads as: nothing, or the count to show (the caller decides whether zero shows). */
export function pickCount(badge: MailBadge | TicketBadge, n: BadgeNumbers): number | undefined {
  const v = badge === "none" ? undefined : n[badge];
  return v === null || v === undefined ? undefined : v;
}

// ── The folder bar's tabs ────────────────────────────────────────────────

export type MailTab = { id: string; kind: "box"; box: TabBox } | { id: string; kind: "folder"; folder: MailFolder };

/** The scope the folder bar shows: one account and its folders, or none (every account). */
export type TabScope = { account: Pick<MailAccount, "id" | "provider">; folders: MailFolder[] | undefined } | null;

export const tabId = {
  box: (box: TabBox) => box as string,
  folder: (id: string) => `folder:${id}`,
};

/**
 * The tabs to show, in order: every box tab (for one account, those its provider has), and the
 * folder tabs of the account in scope. Folders of other accounts and ids of things gone are skipped.
 */
export function tabsFor(p: SidebarPrefs, scope: TabScope): MailTab[] {
  const out: MailTab[] = [];
  for (const id of p.mail_tabs) {
    if (isTabBox(id)) {
      if (!scope || hasBox(scope.account, scope.folders, id)) out.push({ id, kind: "box", box: id });
      continue;
    }
    if (!id.startsWith("folder:") || !scope) continue;
    const folder = scope.folders?.find((f) => f.id === id.slice(7) && f.account_id === scope.account.id);
    if (folder) out.push({ id, kind: "folder", folder });
  }
  return out;
}

export function isTab(p: SidebarPrefs, id: string): boolean {
  return p.mail_tabs.includes(id);
}

/** Shows or removes a tab; a new tab goes last, and the bar stops at 30. */
export function toggleTab(p: SidebarPrefs, id: string): SidebarPrefs {
  if (isTab(p, id)) return { ...p, mail_tabs: p.mail_tabs.filter((x) => x !== id) };
  if (p.mail_tabs.length >= MAX_TABS) return p;
  return { ...p, mail_tabs: [...p.mail_tabs, id] };
}

/**
 * Moves a tab one place left (-1) or right (1) past the next tab that shows here
 * (`visible`, all of them by default), so tabs of other accounts never swallow a move.
 */
export function moveTab(p: SidebarPrefs, id: string, dir: -1 | 1, visible?: readonly string[]): SidebarPrefs {
  const tabs = p.mail_tabs.slice();
  const i = tabs.indexOf(id);
  if (i < 0) return p;
  let j = i + dir;
  while (j >= 0 && j < tabs.length && visible && !visible.includes(tabs[j])) j += dir;
  if (j < 0 || j >= tabs.length) return p;
  tabs.splice(i, 1);
  tabs.splice(j, 0, id);
  return { ...p, mail_tabs: tabs };
}

export function resetTabs(p: SidebarPrefs): SidebarPrefs {
  return { ...p, mail_tabs: [...DEFAULT_MAIL_TABS] };
}
