// The order a member last chose for a ticket list (a folder, a view, a team inbox), remembered
// per member, per workspace and per list in this browser. A list opened without a sort in its
// URL (the sidebar, a shortcut, a notification) comes back in that order; a sort in the URL
// wins. Choosing the default order again forgets it.

import { parseInboxSearch, type InboxSearch } from "./folders";
import { useSession } from "./session";

export type ListSort = Pick<InboxSearch, "sort" | "order">;

/** The list a route shows, from its params. */
export type ListRef = { folder?: string; viewId?: string; teamId?: string };

/** A list's id within its workspace: `folder:<id>`, `view:<id>` or `team:<id>`. */
export function listId(l: ListRef): string | null {
  if (l.viewId) return `view:${l.viewId}`;
  if (l.teamId) return `team:${l.teamId}`;
  if (l.folder) return `folder:${l.folder}`;
  return null;
}

export const listSortKey = (workspaceId: string, uid: string, list: string) => `od-list-sort:${workspaceId}:${uid}:${list}`;

/** The sort as the list's own default leaves it out: newest message first, and each sort's default order. */
export function normalSort(s: ListSort): ListSort {
  const sort = s.sort === "last_message_at" ? undefined : s.sort;
  const order = s.order === ((sort ?? "last_message_at") === "due" ? "asc" : "desc") ? undefined : s.order;
  return { ...(sort ? { sort } : {}), ...(order ? { order } : {}) };
}

function storageKey(l: ListRef): string | null {
  const { user, workspaceId } = useSession.getState();
  const id = listId(l);
  return user?.id && workspaceId && id ? listSortKey(workspaceId, user.id, id) : null;
}

/** The sort this member last chose for the list, or null for the default. */
export function rememberedSort(l: ListRef): ListSort | null {
  const key = storageKey(l);
  if (!key) return null;
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return null;
    const stored = JSON.parse(raw) as Record<string, unknown> | null;
    if (!stored || typeof stored !== "object") return null;
    const { sort, order } = parseInboxSearch({ sort: stored.sort, order: stored.order });
    const s = normalSort({ sort, order });
    return s.sort || s.order ? s : null;
  } catch {
    return null;
  }
}

/** Remembers the sort chosen for the list; the default forgets it. */
export function rememberSort(l: ListRef, s: ListSort) {
  const key = storageKey(l);
  if (!key) return;
  const n = normalSort(s);
  try {
    if (n.sort || n.order) localStorage.setItem(key, JSON.stringify(n));
    else localStorage.removeItem(key);
  } catch {
    /* storage unavailable */
  }
}

/**
 * The search to open the list with when its URL names no sort and the member chose one:
 * the URL's own with the remembered sort. Null leaves the URL as it is.
 */
export function withRememberedSort<S extends InboxSearch>(l: ListRef, search: S): S | null {
  if (search.sort || search.order) return null;
  const s = rememberedSort(l);
  return s ? { ...search, ...s } : null;
}
