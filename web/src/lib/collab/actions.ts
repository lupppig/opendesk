// What a thread action does to a conversation in the app before the provider hears
// of it (contracts/collab.md "Actions"), which lists it leaves, and the action that
// undoes it. Pure, so the optimistic update, the rollback and the Undo toast agree.

import type { CollabThreadQuery } from "../api/endpoints";
import type { MailCounts, MailFolder, MailThread, MailThreadAction } from "../api/types";

/** Actions whose toast offers Undo. */
export const UNDOABLE: ReadonlySet<MailThreadAction> = new Set<MailThreadAction>(["archive", "trash", "spam", "move"]);

/** The conversation as it reads once the action applied. */
export function applyAction(t: MailThread, action: MailThreadAction, folderId?: string, category?: string): MailThread {
  switch (action) {
    case "categorize":
      return category ? { ...t, category } : t;
    case "read":
      return { ...t, unread: false };
    case "unread":
      return { ...t, unread: true };
    case "star":
      return { ...t, starred: true };
    case "unstar":
      return { ...t, starred: false };
    case "done":
      return { ...t, needs_action: false };
    case "needs_action":
      return { ...t, needs_action: true };
    case "label":
    case "move":
      return folderId && !t.folder_ids.includes(folderId) ? { ...t, folder_ids: [...t.folder_ids, folderId] } : t;
    case "unlabel":
      return folderId ? { ...t, folder_ids: t.folder_ids.filter((x) => x !== folderId) } : t;
    default:
      return t;
  }
}

/** Where a list is: a box, or a folder with the role it has (null for a user folder). */
export type ListPlace = { box: string | null; folderId: string | null; folderRole: MailFolder["role"]; category?: string };

export function listPlace(q: CollabThreadQuery, folders: MailFolder[]): ListPlace {
  if (q.folder_id) return { box: null, folderId: q.folder_id, folderRole: folders.find((f) => f.id === q.folder_id)?.role ?? null };
  return { box: q.box ?? "inbox", folderId: null, folderRole: null, category: q.category };
}

/**
 * Whether a conversation leaves a list right away when the action applies. Read,
 * star and label changes leave rows where they are (the next refetch tidies up).
 */
export function leavesList(action: MailThreadAction, place: ListPlace, targetFolder?: string, category?: string): boolean {
  const where = place.box ?? place.folderRole ?? "folder";
  switch (action) {
    case "categorize":
      return !!place.category && place.category !== category;
    case "archive":
      return where === "inbox" || where === "unread";
    case "trash":
      return where !== "trash";
    case "spam":
      return where !== "spam";
    case "not_spam":
      return where === "spam";
    case "inbox":
      return where === "archive" || where === "trash" || where === "spam";
    case "move":
      if (place.folderId) return place.folderId !== targetFolder;
      return where === "inbox" || where === "unread";
    case "unlabel":
      return !!place.folderId && place.folderId === targetFolder;
    case "done":
      return where === "needs_action";
    default:
      return false;
  }
}

/**
 * The action that puts a conversation back: archive, trash and spam return it to
 * the inbox; a move returns it to the folder it was moved from, else the inbox.
 */
export function inverseAction(action: MailThreadAction, from: ListPlace): { action: MailThreadAction; folderId?: string } | null {
  switch (action) {
    case "archive":
    case "trash":
      return { action: "inbox" };
    case "spam":
      return { action: "not_spam" };
    case "move":
      return from.folderId && !from.folderRole ? { action: "move", folderId: from.folderId } : { action: "inbox" };
    case "read":
      return { action: "unread" };
    case "unread":
      return { action: "read" };
    case "star":
      return { action: "unstar" };
    case "unstar":
      return { action: "star" };
    default:
      return null;
  }
}

/** A conversation's folders once a moving action applied, by the roles of its account's folders. */
function foldersAfter(t: MailThread, action: MailThreadAction, folders: MailFolder[], folderId?: string): string[] {
  const role = (r: MailFolder["role"]) => folders.filter((f) => f.account_id === t.account_id && f.role === r).map((f) => f.id);
  const [inbox, trash, spam] = [role("inbox"), role("trash"), role("spam")];
  const without = (ids: string[]) => t.folder_ids.filter((id) => !ids.includes(id));
  const add = (list: string[], ids: string[]) => [...list, ...ids.filter((id) => !list.includes(id))];
  switch (action) {
    case "archive":
      return add(without(inbox), role("archive"));
    case "trash":
      return add(without([...inbox, ...spam]), trash);
    case "spam":
      return add(without(inbox), spam);
    case "inbox":
    case "not_spam":
      return add(without([...trash, ...spam]), inbox);
    case "move":
      return folderId ? add(without(inbox), [folderId]) : t.folder_ids;
    default:
      return applyAction(t, action, folderId).folder_ids;
  }
}

/**
 * The sidebar's numbers once the action applied to the conversations (as cached), so a
 * conversation read on opening leaves its counts at once; the refetch after the action
 * settles has the last word. `addresses` are the addresses of each account with aliases, lower
 * case. Assigned to me is left as it is.
 */
export function countsAfter(
  counts: MailCounts,
  threads: MailThread[],
  action: MailThreadAction,
  folders: MailFolder[],
  folderId?: string,
  addresses: Record<string, string[]> = {},
  category?: string,
): MailCounts {
  const roles = new Map(folders.map((f) => [f.id, f.role]));
  const out: MailCounts = { assigned: { ...counts.assigned }, boxes: { ...counts.boxes }, accounts: { ...counts.accounts }, folders: { ...counts.folders } };
  if (counts.addresses) out.addresses = Object.fromEntries(Object.entries(counts.addresses).map(([id, m]) => [id, { ...m }]));
  if (counts.categories) out.categories = Object.fromEntries(Object.entries(counts.categories).map(([id, m]) => [id, { ...m }]));
  // A zero is kept: an account missing from the counts falls back to its own (older) number.
  const bump = (rec: Record<string, number>, key: string, d: number) => {
    rec[key] = Math.max(0, (rec[key] ?? 0) + d);
  };
  const count = (t: MailThread, sign: 1 | -1) => {
    const has = (r: MailFolder["role"]) => t.folder_ids.some((id) => roles.get(id) === r);
    const inInbox = has("inbox");
    if (t.unread) {
      if (inInbox) {
        out.boxes.inbox = Math.max(0, out.boxes.inbox + sign);
        bump(out.accounts, t.account_id, sign);
        // Its account's addresses on it: the alias it was written to, and those among its people (the list's to=).
        const own = addresses[t.account_id];
        if (own && out.addresses) {
          const on = new Set([t.to_address ?? "", ...t.participants.map((x) => x.email)].map((e) => e.toLowerCase()));
          const byAddress = (out.addresses[t.account_id] ??= {});
          for (const e of own) if (on.has(e)) bump(byAddress, e, sign);
        }
        // Its inbox tab, when its account's inbox is split.
        if (t.category && out.categories?.[t.account_id]) bump(out.categories[t.account_id], t.category, sign);
      }
      if (!has("trash") && !has("spam")) out.boxes.unread = Math.max(0, out.boxes.unread + sign);
      for (const id of t.folder_ids) bump(out.folders, id, sign);
    }
    if (t.shared && inInbox) out.boxes.shared = Math.max(0, out.boxes.shared + sign);
    if (t.needs_action && inInbox) out.boxes.needs_action = Math.max(0, out.boxes.needs_action + sign);
  };
  for (const t of threads) {
    const next = { ...applyAction(t, action, folderId, category), folder_ids: foldersAfter(t, action, folders, folderId) };
    count(t, -1);
    count(next, 1);
  }
  return out;
}
