// Collab mail boxes, folders and the list query a mail page reads (contracts/collab.md
// "Endpoints"). Pure, so the sidebar, the route loader and the page share one reading.

import type { CollabThreadQuery } from "../api/endpoints";
import type { MailAccount, MailBox, MailFolder, MailFolderRole } from "../api/types";

export const MAIL_BOXES: readonly MailBox[] = ["inbox", "unread", "starred", "sent", "drafts", "archive", "trash", "spam", "all", "shared", "assigned"];

/** The route's box: a box, or "folder" when the `folder` search names one. */
export type MailRouteBox = MailBox | "folder";

export function isMailBox(v: unknown): v is MailBox {
  return typeof v === "string" && (MAIL_BOXES as readonly string[]).includes(v);
}

export function isMailRouteBox(v: unknown): v is MailRouteBox {
  return v === "folder" || isMailBox(v);
}

/** The rows each account shows before its own folders, in order. */
export const ACCOUNT_BOXES = ["inbox", "starred", "sent", "drafts", "archive", "spam", "trash"] as const satisfies readonly MailBox[];
export type AccountBox = (typeof ACCOUNT_BOXES)[number];

/** The box a special folder stands for. */
export function boxForRole(role: MailFolderRole | null): AccountBox | null {
  switch (role) {
    case "inbox":
    case "starred":
    case "sent":
    case "drafts":
    case "archive":
    case "spam":
    case "trash":
      return role;
    default:
      return null;
  }
}

export type MailSearch = { account?: string; folder?: string; q?: string };

export function parseMailSearch(raw: Record<string, unknown>): MailSearch {
  const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);
  return { account: str(raw.account), folder: str(raw.folder), q: str(raw.q) };
}

/** The thread list query for a route: a folder replaces the box. */
export function mailListQuery(box: string, s: MailSearch): CollabThreadQuery {
  const q: CollabThreadQuery = {};
  if (s.folder) q.folder_id = s.folder;
  else {
    q.box = isMailBox(box) ? box : "inbox";
    if (s.account) q.account_id = s.account;
  }
  if (s.q) q.q = s.q;
  return q;
}

/** A user folder or label, with its children nested by path. Missing parents get a node without a folder. */
export type FolderNode = { key: string; name: string; path: string; folder: MailFolder | null; children: FolderNode[] };

/** Folders and labels people made (not the special ones), nested by "/" in their path and sorted by name. */
export function buildFolderTree(folders: MailFolder[]): FolderNode[] {
  const root: FolderNode = { key: "", name: "", path: "", folder: null, children: [] };
  const byPath = new Map<string, FolderNode>([["", root]]);
  const user = folders.filter((f) => !f.role).sort((a, b) => a.path.localeCompare(b.path));
  for (const f of user) {
    const parts = (f.path || f.name).split("/").filter(Boolean);
    if (!parts.length) continue;
    let parent = root;
    let path = "";
    parts.forEach((part, i) => {
      path = path ? `${path}/${part}` : part;
      let node = byPath.get(path);
      if (!node) {
        node = { key: `${f.account_id}:${path}`, name: part, path, folder: null, children: [] };
        byPath.set(path, node);
        parent.children.push(node);
      }
      if (i === parts.length - 1) {
        node.folder = f;
        node.name = f.name || part;
      }
      parent = node;
    });
  }
  const sort = (nodes: FolderNode[]) => {
    nodes.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
    nodes.forEach((n) => sort(n.children));
  };
  sort(root.children);
  return root.children;
}

/** Every node of a tree, depth first, with its depth. */
export function flattenTree(nodes: FolderNode[], depth = 0): { node: FolderNode; depth: number }[] {
  return nodes.flatMap((n) => [{ node: n, depth }, ...flattenTree(n.children, depth + 1)]);
}

/**
 * A tree's folders without some (those already tabs), depth first: a folder under one that is
 * left out shows its whole path, unindented, so it never hangs under nothing.
 */
export function flattenWithout(nodes: FolderNode[], skip: (f: MailFolder) => boolean): { folder: MailFolder; depth: number; label: string; key: string }[] {
  const out: { folder: MailFolder; depth: number; label: string; key: string }[] = [];
  const walk = (list: FolderNode[], parentDepth: number | null) => {
    for (const n of list) {
      if (n.folder && !skip(n.folder)) {
        const depth = parentDepth === null ? 0 : parentDepth + 1;
        out.push({ folder: n.folder, depth, label: parentDepth === null && n.path.includes("/") ? n.path : n.name, key: n.key });
        walk(n.children, depth);
      } else walk(n.children, null);
    }
  };
  walk(nodes, null);
  return out;
}

/** The special folder of an account with a role, when the provider has one. */
export function folderWithRole(folders: MailFolder[] | undefined, role: MailFolderRole): MailFolder | undefined {
  return folders?.find((f) => f.role === role);
}

/** The boxes the folder bar can show as tabs, in their usual order. */
export const TAB_BOXES = ["inbox", "unread", "starred", "sent", "drafts", "archive", "spam", "trash", "all"] as const satisfies readonly MailBox[];
export type TabBox = (typeof TAB_BOXES)[number];

export function isTabBox(v: unknown): v is TabBox {
  return typeof v === "string" && (TAB_BOXES as readonly string[]).includes(v);
}

/** Whether an account has a box: the provider lists the folder; Inbox, Starred, Unread and All mail always; Archive on Gmail. */
export function hasBox(account: Pick<MailAccount, "provider">, folders: MailFolder[] | undefined, box: MailBox): boolean {
  if (box === "inbox" || box === "starred" || box === "unread" || box === "all" || box === "shared" || box === "assigned") return true;
  if (box === "archive" && account.provider === "google") return true;
  return !folders || !!folderWithRole(folders, box);
}

/** Accounts in the order the API gives them (theirs first). */
export function accountName(a: Pick<MailAccount, "name" | "email">): string {
  return a.name || a.email;
}
