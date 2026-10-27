import { describe, expect, it } from "vitest";
import type { MailAccount, MailCounts, MailFolder, TicketCounts } from "../api/types";
import { accountNumbers, allInboxNumbers, boxTabNumbers, folderNumbers, ticketRowNumbers } from "./badges";
import {
  badgeChoices,
  badgeFor,
  badgeWord,
  DEFAULT_MAIL_TABS,
  defaultSidebarPrefs,
  hid,
  livePins,
  moveSection,
  sectionsOffered,
  movePin,
  moveTab,
  normalizePrefs,
  pickCount,
  resetTabs,
  sectionOrder,
  setBadge,
  setHidden,
  tabBadge,
  tabsFor,
  toggleExpanded,
  togglePin,
  toggleTab,
  visibleSections,
} from "./sidebarPrefs";

const folder = (id: string, account_id: string, role: MailFolder["role"], extra: Partial<MailFolder> = {}): MailFolder => ({
  id,
  account_id,
  name: id,
  path: id,
  role,
  kind: "folder",
  unread: 0,
  total: 0,
  color: null,
  can_rename: !role,
  ...extra,
});
const account = (id: string, provider: MailAccount["provider"], unread = 0) => ({ id, provider, unread, name: id, email: `${id}@x.test` }) as MailAccount;

describe("sidebar prefs", () => {
  it("reads a partial answer as the defaults", () => {
    const p = normalizePrefs({ order: ["inbox"] });
    expect(p.mark_read_on_open).toBe(true);
    expect(p.unified_inbox).toBe(true);
    expect(p.hidden).toEqual([]);
    expect(p.badges).toEqual({});
    expect(p.mail_tabs).toEqual(DEFAULT_MAIL_TABS);
  });

  it("keeps saved numbers and tabs, dropping values it does not know", () => {
    const p = normalizePrefs({ badges: { "box:inbox": "total", "view:v": "huge" } as never, mail_tabs: ["spam", 3 as never] });
    expect(p.badges).toEqual({ "box:inbox": "total" });
    expect(p.mail_tabs).toEqual(["spam"]);
    expect(normalizePrefs({ mail_tabs: [] }).mail_tabs).toEqual([]);
  });

  it("places sections missing from the saved order where the default puts them, ignoring unknown ids", () => {
    const p = { ...defaultSidebarPrefs(), order: ["views", "future", "inbox"] };
    expect(sectionOrder(p)).toEqual(["favorites", "views", "workspace", "inbox", "mail", "teams"]);
  });

  it("hides sections", () => {
    const p = setHidden(defaultSidebarPrefs(), hid.section("teams"), true);
    expect(visibleSections(p)).toEqual(["favorites", "inbox", "mail", "views", "workspace"]);
    expect(setHidden(p, hid.section("teams"), false).hidden).toEqual([]);
  });

  it("moves sections and keeps unknown ids", () => {
    const p = { ...defaultSidebarPrefs(), order: ["favorites", "mail", "inbox", "teams", "views", "workspace", "future"] };
    const moved = moveSection(p, "inbox", -1);
    expect(moved.order).toEqual(["favorites", "inbox", "mail", "teams", "views", "workspace", "future"]);
    expect(moveSection(p, "favorites", -1)).toBe(p);
  });

  it("offers every section but Mail without Collab, and moves past it", () => {
    expect(sectionsOffered(true)).toEqual(["favorites", "inbox", "mail", "teams", "views", "workspace"]);
    expect(sectionsOffered(false)).toEqual(["favorites", "inbox", "teams", "views", "workspace"]);
    const p = { ...defaultSidebarPrefs(), order: ["favorites", "inbox", "mail", "teams", "views", "workspace"] };
    expect(visibleSections(p, false)).toEqual(["favorites", "inbox", "teams", "views", "workspace"]);
    // Teams goes above Inbox in one move; Mail, which the member does not see, keeps its place.
    expect(moveSection(p, "teams", -1, false).order).toEqual(["favorites", "teams", "mail", "inbox", "views", "workspace"]);
    expect(moveSection(p, "inbox", 1, false).order).toEqual(["favorites", "teams", "mail", "inbox", "views", "workspace"]);
    expect(moveSection(p, "workspace", 1, false)).toBe(p);
  });

  it("pins, reorders and skips favorites that are gone", () => {
    let p = togglePin(defaultSidebarPrefs(), { kind: "view", id: "v1" });
    p = togglePin(p, { kind: "account", id: "a1" });
    p = movePin(p, { kind: "account", id: "a1" }, -1);
    expect(p.pinned.map((x) => x.id)).toEqual(["a1", "v1"]);
    const live = livePins(p, (pin) => (pin.id === "v1" ? "View" : null));
    expect(live).toEqual([{ pin: { kind: "view", id: "v1" }, target: "View" }]);
    expect(togglePin(p, { kind: "view", id: "v1" }).pinned).toEqual([{ kind: "account", id: "a1" }]);
  });

  it("expands and folds accounts", () => {
    const p = toggleExpanded(defaultSidebarPrefs(), "a1");
    expect(p.expanded_accounts).toEqual(["a1"]);
    expect(toggleExpanded(p, "a1").expanded_accounts).toEqual([]);
  });
});

describe("numbers beside rows", () => {
  it("reads the row, then its sections in order, then the default", () => {
    let p = defaultSidebarPrefs();
    expect(badgeFor(p, "account:a", "mail", "mail")).toBe("unread");
    expect(badgeFor(p, "view:v", "views", "ticket")).toBe("count");
    expect(badgeFor(p, "folder:sent", "mail", "mail", "none")).toBe("none");
    p = setBadge(p, "section:mail", "total");
    expect(badgeFor(p, "folder:sent", "mail", "mail", "none")).toBe("total");
    expect(badgeFor(p, "account:a", "mail", "mail")).toBe("total");
    p = setBadge(p, "account:a", "none");
    expect(badgeFor(p, "account:a", "mail", "mail")).toBe("none");
    // A favorite reads Favorites first, then the section it comes from.
    expect(badgeFor(p, "folder:f", ["favorites", "mail"], "mail")).toBe("total");
    p = setBadge(p, "section:favorites", "unread");
    expect(badgeFor(p, "folder:f", ["favorites", "mail"], "mail")).toBe("unread");
  });

  it("reads a value meant for the other kind of row sensibly", () => {
    const p = { ...defaultSidebarPrefs(), badges: { "section:favorites": "total", "view:v": "unread", "folder:f": "count" } as const };
    expect(badgeFor(p, "team:t", ["favorites", "teams"], "ticket")).toBe("count");
    expect(badgeFor(p, "view:v", "views", "ticket")).toBe("count");
    expect(badgeFor(p, "folder:f", "mail", "mail")).toBe("unread");
  });

  it("lets ticket rows show their stats, and team rows their open tickets too", () => {
    expect(badgeChoices("mail")).toEqual(["unread", "total", "none"]);
    expect(badgeChoices("ticket")).toEqual(["count", "awaiting", "urgent", "overdue", "none"]);
    expect(badgeChoices("team")).toEqual(["count", "open", "awaiting", "urgent", "overdue", "none"]);
    let p = setBadge(defaultSidebarPrefs(), "section:teams", "open");
    expect(badgeFor(p, "team:t", "teams", "team")).toBe("open");
    expect(badgeFor(p, "team:t", ["favorites", "teams"], "team")).toBe("open");
    p = setBadge(setBadge(p, "team:t", "overdue"), "view:v", "awaiting");
    expect(badgeFor(p, "team:t", "teams", "team")).toBe("overdue");
    expect(badgeFor(p, "view:v", "views", "ticket")).toBe("awaiting");
    // A value the row has no number for reads as its default.
    p = setBadge(setBadge(p, "view:w", "open"), "box:inbox", "urgent");
    expect(badgeFor(p, "view:w", "views", "ticket")).toBe("count");
    expect(badgeFor(p, "box:inbox", "mail", "mail")).toBe("unread");
  });

  it("names each number by its row", () => {
    expect(badgeWord("count", "ticket")).toBe("tickets");
    expect(badgeWord("count", "team")).toBe("unassigned");
    expect(badgeWord("count", "mail")).toBe("unread");
    expect(badgeWord("open", "team")).toBe("open");
    expect(badgeWord("open", "ticket")).toBe("tickets");
    expect(badgeWord("awaiting", "ticket")).toBe("awaiting");
    expect(badgeWord("none", "team")).toBe("none");
  });

  it("reads a ticket row's numbers from the counts", () => {
    const counts: TicketCounts = {
      unassigned: 4, mine: 2, open: 7, pending: 1, on_hold: 2, solved: 9, spam: 1,
      views: { v: 5 },
      teams: { t: { open: 6, unassigned: 3 } },
      my_teams: { open: 8, unassigned: 4 },
      stats: {
        "ticket_folder:pending": { awaiting: 1, urgent: 0, overdue: 2 },
        "team:t": { awaiting: 2, urgent: 1, overdue: 0 },
        "team:mine": { awaiting: 3, urgent: 1, overdue: 1 },
        "view:v": { awaiting: 0, urgent: 5, overdue: 4 },
      },
    };
    expect(ticketRowNumbers(undefined, "view:v")).toEqual({});
    expect(ticketRowNumbers(counts, "ticket_folder:pending")).toEqual({ count: 3, awaiting: 1, urgent: 0, overdue: 2 });
    expect(ticketRowNumbers(counts, "team:t")).toEqual({ count: 3, open: 6, awaiting: 2, urgent: 1, overdue: 0 });
    expect(ticketRowNumbers(counts, "team:mine")).toEqual({ count: 4, open: 8, awaiting: 3, urgent: 1, overdue: 1 });
    expect(ticketRowNumbers(counts, "view:v")).toEqual({ count: 5, awaiting: 0, urgent: 5, overdue: 4 });
    // An older server without stats: the count alone.
    expect(ticketRowNumbers({ ...counts, stats: undefined }, "view:v")).toEqual({ count: 5 });
    expect(pickCount("overdue", ticketRowNumbers(counts, "view:v"))).toBe(4);
    expect(pickCount("open", ticketRowNumbers(counts, "team:t"))).toBe(6);
    expect(pickCount("awaiting", ticketRowNumbers({ ...counts, stats: undefined }, "view:v"))).toBeUndefined();
  });

  it("sets and clears a choice, and stops at 300", () => {
    const p = setBadge(defaultSidebarPrefs(), "box:inbox", "total");
    expect(p.badges).toEqual({ "box:inbox": "total" });
    expect(setBadge(p, "box:inbox", "total")).toBe(p);
    expect(setBadge(p, "box:inbox", null).badges).toEqual({});
    const full = { ...defaultSidebarPrefs(), badges: Object.fromEntries(Array.from({ length: 300 }, (_, i) => [`view:${i}`, "none" as const])) };
    expect(setBadge(full, "view:new", "none")).toBe(full);
    expect(setBadge(full, "view:1", "count").badges["view:1"]).toBe("count");
  });

  it("gives tabs unread on Inbox and nothing elsewhere, unless chosen", () => {
    let p = defaultSidebarPrefs();
    expect(tabBadge(p, "inbox")).toBe("unread");
    expect(tabBadge(p, "sent")).toBe("none");
    expect(tabBadge(p, "folder:f")).toBe("none");
    p = setBadge(setBadge(p, "folder:f", "total"), "box:sent", "unread");
    expect(tabBadge(p, "folder:f")).toBe("total");
    expect(tabBadge(p, "sent")).toBe("unread");
  });

  it("picks the number, and nothing for none", () => {
    expect(pickCount("unread", { unread: 3, total: 9 })).toBe(3);
    expect(pickCount("total", { unread: 3, total: 9 })).toBe(9);
    expect(pickCount("none", { unread: 3, total: 9 })).toBeUndefined();
    expect(pickCount("count", { count: null })).toBeUndefined();
  });

  it("counts inboxes, folders and box tabs", () => {
    const a = account("a", "imap", 2);
    const b = account("b", "google", 1);
    const folders = {
      a: [folder("ai", "a", "inbox", { total: 10, unread: 2 }), folder("as", "a", "sent", { total: 4 })],
      b: [folder("bi", "b", "inbox", { total: 5, unread: 1 }), folder("bs", "b", "sent", { total: 6, unread: 1 })],
    };
    const counts: MailCounts = { boxes: { inbox: 3, unread: 7, shared: 0, assigned: 0, drafts: 2, needs_action: 0 }, accounts: { a: 2, b: 1 }, folders: { bs: 1 }, assigned: {} };
    expect(accountNumbers(a, folders.a, counts)).toEqual({ unread: 2, total: 10 });
    expect(allInboxNumbers([a, b], folders, counts)).toEqual({ unread: 3, total: 15 });
    expect(allInboxNumbers([a], folders, counts).total).toBe(10);
    expect(allInboxNumbers([a, b], { a: folders.a }, counts).total).toBeUndefined();
    // The live counts leave out folders without unread conversations.
    expect(folderNumbers(folders.a[1], counts)).toEqual({ unread: 0, total: 4 });
    expect(boxTabNumbers("sent", [a, b], folders, counts, true)).toEqual({ unread: 1, total: 10 });
    expect(boxTabNumbers("inbox", [a], folders, counts, false)).toEqual({ unread: 2, total: 10 });
    expect(boxTabNumbers("archive", [b], folders, counts, false).total).toBeUndefined();
    expect(boxTabNumbers("unread", [a, b], folders, counts, true)).toEqual({ unread: 7, total: 7 });
  });
});

describe("folder bar tabs", () => {
  const imap = account("a", "imap");
  const gmail = account("g", "google");
  const imapFolders = [folder("in", "a", "inbox"), folder("sent", "a", "sent"), folder("acme", "a", null)];

  it("shows the default tabs everywhere, and only what the provider has for one account", () => {
    const p = defaultSidebarPrefs();
    expect(tabsFor(p, null).map((t) => t.id)).toEqual(["inbox", "starred", "sent", "drafts", "archive"]);
    expect(tabsFor(p, { account: imap, folders: imapFolders }).map((t) => t.id)).toEqual(["inbox", "starred", "sent"]);
    // Gmail archives without a folder.
    expect(tabsFor(p, { account: gmail, folders: [] }).map((t) => t.id)).toEqual(["inbox", "starred", "archive"]);
    // Before the folders load, every box shows.
    expect(tabsFor(p, { account: imap, folders: undefined })).toHaveLength(5);
  });

  it("shows a folder tab only with its account in scope", () => {
    const p = { ...defaultSidebarPrefs(), mail_tabs: ["inbox", "folder:acme", "folder:gone", "spam", "nonsense"] };
    expect(tabsFor(p, null).map((t) => t.id)).toEqual(["inbox", "spam"]);
    const tabs = tabsFor(p, { account: imap, folders: imapFolders });
    expect(tabs.map((t) => t.id)).toEqual(["inbox", "folder:acme"]);
    expect(tabs[1]).toMatchObject({ kind: "folder", folder: { id: "acme" } });
    expect(tabsFor(p, { account: gmail, folders: [folder("acme", "a", null), folder("gs", "g", "spam")] }).map((t) => t.id)).toEqual(["inbox", "spam"]);
  });

  it("toggles, moves past hidden tabs and resets", () => {
    let p = toggleTab(defaultSidebarPrefs(), "folder:acme");
    expect(p.mail_tabs.at(-1)).toBe("folder:acme");
    p = toggleTab(p, "starred");
    expect(p.mail_tabs).toEqual(["inbox", "sent", "drafts", "archive", "folder:acme"]);
    expect(moveTab(p, "sent", -1).mail_tabs).toEqual(["sent", "inbox", "drafts", "archive", "folder:acme"]);
    expect(moveTab(p, "inbox", -1)).toBe(p);
    // Drafts and Archive do not show here: one move passes them.
    expect(moveTab(p, "folder:acme", -1, ["inbox", "sent", "folder:acme"]).mail_tabs).toEqual(["inbox", "folder:acme", "sent", "drafts", "archive"]);
    expect(moveTab(p, "sent", 1, ["inbox", "sent", "folder:acme"]).mail_tabs).toEqual(["inbox", "drafts", "archive", "folder:acme", "sent"]);
    expect(resetTabs(p).mail_tabs).toEqual(DEFAULT_MAIL_TABS);
    const full = { ...defaultSidebarPrefs(), mail_tabs: Array.from({ length: 30 }, (_, i) => `folder:${i}`) };
    expect(toggleTab(full, "inbox")).toBe(full);
  });
});
