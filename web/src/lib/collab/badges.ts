// The numbers a sidebar row or tab can show: for mail, unread conversations (GET /v1/collab/counts)
// and every conversation (MailFolder.total); for tickets, the row's count and its stats (GET
// /v1/tickets/counts). Pure, so the sidebar, its menus and the folder bar count alike.

import type { MailAccount, MailCounts, MailFolder, TicketCounts } from "../api/types";
import { folderById } from "../folders";
import { folderWithRole, type TabBox } from "./boxes";
import type { BadgeNumbers } from "./sidebarPrefs";

export type RowNumbers = { unread?: number; total?: number };

type FolderMap = Record<string, MailFolder[] | undefined>;

const sum = (xs: (number | undefined)[]): number | undefined => (xs.some((x) => x === undefined) ? undefined : xs.reduce<number>((a, x) => a + (x ?? 0), 0));

/** One account's inbox: its unread conversations and every conversation in it. */
export function accountNumbers(a: MailAccount, folders: MailFolder[] | undefined, counts: MailCounts | undefined): RowNumbers {
  return { unread: counts?.accounts[a.id] ?? a.unread, total: folderWithRole(folders, "inbox")?.total };
}

/** One address of an account (its own or an alias): the unread conversations in the inbox with it on a message. */
export function addressNumbers(accountId: string, email: string, counts: MailCounts | undefined): RowNumbers {
  return { unread: counts ? (counts.addresses?.[accountId]?.[email.toLowerCase()] ?? 0) : undefined };
}

/** All inboxes: unread across every account, and the sum of the shown accounts' inbox totals. */
export function allInboxNumbers(shown: MailAccount[], folders: FolderMap, counts: MailCounts | undefined): RowNumbers {
  return { unread: counts?.boxes.inbox, total: shown.length ? sum(shown.map((a) => folderWithRole(folders[a.id], "inbox")?.total)) : undefined };
}

/** A folder's unread conversations: the live counts (which leave out folders without any) once loaded. */
export function folderUnread(f: MailFolder, counts: MailCounts | undefined): number {
  return counts ? (counts.folders[f.id] ?? 0) : f.unread;
}

/** A folder or label: its unread conversations and every conversation in it. */
export function folderNumbers(f: MailFolder, counts: MailCounts | undefined): RowNumbers {
  return { unread: folderUnread(f, counts), total: f.total };
}

/** Shared with me and Assigned to me: one count, the conversations of theirs still in an inbox. */
export function listBoxNumbers(box: "shared" | "assigned", counts: MailCounts | undefined): RowNumbers {
  const n = counts?.boxes[box];
  return { unread: n, total: n };
}

/** Assigned to me within one account. */
export function accountAssignedNumbers(accountId: string, counts: MailCounts | undefined): RowNumbers {
  const n = counts ? (counts.assigned?.[accountId] ?? 0) : undefined;
  return { unread: n, total: n };
}

/**
 * A box tab in the folder bar, over the accounts in scope: Inbox, Unread and Drafts read the
 * live counts across accounts; the rest add up the accounts' folders with that role (unknown
 * when an account has no such folder, like Gmail's Archive).
 */
export function boxTabNumbers(box: TabBox, scope: MailAccount[], folders: FolderMap, counts: MailCounts | undefined, spansAll: boolean): RowNumbers {
  const role = box === "unread" ? null : box;
  const roleFolders = role ? scope.map((a) => folderWithRole(folders[a.id], role)) : [];
  const total = role && scope.length ? sum(roleFolders.map((f) => f?.total)) : undefined;
  let unread: number | undefined;
  if (box === "inbox") unread = spansAll ? counts?.boxes.inbox : sum(scope.map((a) => counts?.accounts[a.id] ?? a.unread));
  else if (box === "unread") return spansAll ? { unread: counts?.boxes.unread, total: counts?.boxes.unread } : {};
  else if (box === "drafts" && spansAll) unread = counts?.boxes.drafts;
  else if (role && scope.length) unread = sum(roleFolders.map((f) => (f ? folderUnread(f, counts) : undefined)));
  return { unread, total };
}

/**
 * A ticket row's numbers by its row id (ticket_folder:<id>, team:<id>, team:mine, view:<id>): its
 * usual count (a team's unassigned tickets), a team's open tickets, and the row's stats.
 * Nothing while the counts load.
 */
export function ticketRowNumbers(counts: TicketCounts | undefined, rowId: string): BadgeNumbers {
  if (!counts) return {};
  const i = rowId.indexOf(":");
  const kind = rowId.slice(0, i);
  const id = rowId.slice(i + 1);
  const out: BadgeNumbers = { ...counts.stats?.[rowId] };
  if (kind === "ticket_folder") {
    const f = folderById(id);
    out.count = f ? f.count(counts) : undefined;
  } else if (kind === "team") {
    const c = id === "mine" ? counts.my_teams : counts.teams?.[id];
    out.count = c?.unassigned;
    out.open = c?.open;
  } else if (kind === "view") out.count = counts.views?.[id];
  return out;
}
