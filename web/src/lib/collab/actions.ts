// What a thread action does to a conversation in the app before the provider hears
// of it (contracts/collab.md "Actions"), which lists it leaves, and the action that
// undoes it. Pure, so the optimistic update, the rollback and the Undo toast agree.

import type { CollabThreadQuery } from "../api/endpoints";
import type { MailFolder, MailThread, MailThreadAction } from "../api/types";

/** Actions whose toast offers Undo. */
export const UNDOABLE: ReadonlySet<MailThreadAction> = new Set<MailThreadAction>(["archive", "trash", "spam", "move"]);

/** The conversation as it reads once the action applied. */
export function applyAction(t: MailThread, action: MailThreadAction, folderId?: string): MailThread {
  switch (action) {
    case "read":
      return { ...t, unread: false };
    case "unread":
      return { ...t, unread: true };
    case "star":
      return { ...t, starred: true };
    case "unstar":
      return { ...t, starred: false };
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
export type ListPlace = { box: string | null; folderId: string | null; folderRole: MailFolder["role"] };

export function listPlace(q: CollabThreadQuery, folders: MailFolder[]): ListPlace {
  if (q.folder_id) return { box: null, folderId: q.folder_id, folderRole: folders.find((f) => f.id === q.folder_id)?.role ?? null };
  return { box: q.box ?? "inbox", folderId: null, folderRole: null };
}

/**
 * Whether a conversation leaves a list right away when the action applies. Read,
 * star and label changes leave rows where they are (the next refetch tidies up).
 */
export function leavesList(action: MailThreadAction, place: ListPlace, targetFolder?: string): boolean {
  const where = place.box ?? place.folderRole ?? "folder";
  switch (action) {
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
