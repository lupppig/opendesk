// The Collab parts of the TanStack Query cache: patching conversations everywhere
// they are listed, taking them out of lists, snapshots for rollback, and the
// realtime events (contracts/collab.md "Events": ids only, so lists refetch).

import type { InfiniteData, QueryClient, QueryKey } from "@tanstack/react-query";
import type { CollabThreadQuery } from "../api/endpoints";
import type { CollabChangedEvent, MailFolder, MailThread, MailThreadDetail, Page } from "../api/types";
import { qk } from "../queries";
import { leavesList, listPlace } from "./actions";
import type { MailThreadAction } from "../api/types";

type ThreadPages = InfiniteData<Page<MailThread>, string | null>;

/** Every cached folder, across accounts. */
export function cachedFolders(qc: QueryClient): MailFolder[] {
  return qc.getQueriesData<MailFolder[]>({ queryKey: ["collab", "folders"] }).flatMap(([, d]) => d ?? []);
}

/** Replaces conversations by id in every list and open thread. */
export function patchThreads(qc: QueryClient, threads: MailThread[]) {
  if (!threads.length) return;
  const byId = new Map(threads.map((t) => [t.id, t]));
  for (const [key, data] of qc.getQueriesData<ThreadPages>({ queryKey: qk.mailThreadLists })) {
    if (!data) continue;
    let changed = false;
    const pages = data.pages.map((p) => ({
      ...p,
      data: p.data.map((t) => {
        const n = byId.get(t.id);
        if (!n) return t;
        changed = true;
        return n;
      }),
    }));
    if (changed) qc.setQueryData<ThreadPages>(key, { ...data, pages });
  }
  for (const t of threads) {
    qc.setQueryData<MailThreadDetail>(qk.mailThread(t.id), (old) => (old ? { ...old, thread: t } : old));
  }
}

/** Maps conversations in every list and open thread through fn. */
export function mapThreads(qc: QueryClient, ids: Set<string>, fn: (t: MailThread) => MailThread) {
  for (const [key, data] of qc.getQueriesData<ThreadPages>({ queryKey: qk.mailThreadLists })) {
    if (!data) continue;
    if (!data.pages.some((p) => p.data.some((t) => ids.has(t.id)))) continue;
    qc.setQueryData<ThreadPages>(key, { ...data, pages: data.pages.map((p) => ({ ...p, data: p.data.map((t) => (ids.has(t.id) ? fn(t) : t)) })) });
  }
  for (const id of ids) qc.setQueryData<MailThreadDetail>(qk.mailThread(id), (old) => (old ? { ...old, thread: fn(old.thread) } : old));
}

/** Takes conversations out of the lists they leave when the action applies. */
export function dropFromLists(qc: QueryClient, ids: Set<string>, action: MailThreadAction, folderId?: string) {
  const folders = cachedFolders(qc);
  for (const [key, data] of qc.getQueriesData<ThreadPages>({ queryKey: qk.mailThreadLists })) {
    if (!data) continue;
    const query = (key[2] ?? {}) as CollabThreadQuery;
    if (!leavesList(action, listPlace(query, folders), folderId)) continue;
    if (!data.pages.some((p) => p.data.some((t) => ids.has(t.id)))) continue;
    qc.setQueryData<ThreadPages>(key, { ...data, pages: data.pages.map((p) => ({ ...p, data: p.data.filter((t) => !ids.has(t.id)) })) });
  }
}

export type Snapshot = [QueryKey, unknown][];

/** Thread lists and the given open threads, to put back when an action fails. */
export function snapshotThreads(qc: QueryClient, ids: Iterable<string>): Snapshot {
  const out: Snapshot = qc.getQueriesData({ queryKey: qk.mailThreadLists });
  for (const id of ids) out.push([qk.mailThread(id), qc.getQueryData(qk.mailThread(id))]);
  return out;
}

export function restore(qc: QueryClient, snap: Snapshot) {
  for (const [key, data] of snap) qc.setQueryData(key, data);
}

let listTimer: ReturnType<typeof setTimeout> | null = null;
const pendingThreads = new Set<string>();
let pendingFolders: Set<string | null> = new Set();

/** Coalesces bursts of events (a sync, a bulk action) into one refetch of what is shown. */
function flush(qc: QueryClient) {
  listTimer = null;
  void qc.invalidateQueries({ queryKey: qk.mailThreadLists });
  void qc.invalidateQueries({ queryKey: qk.mailCounts });
  for (const id of pendingThreads) void qc.invalidateQueries({ queryKey: qk.mailThread(id), exact: true });
  pendingThreads.clear();
  for (const acc of pendingFolders) void qc.invalidateQueries({ queryKey: acc ? qk.mailFolders(acc) : ["collab", "folders"] });
  pendingFolders = new Set();
}

/** Applies a Collab event; returns true when it was one. */
export function applyCollabEvent(qc: QueryClient, type: string, data: Record<string, unknown>): boolean {
  if (type === "collab.changed") {
    const d = data as Partial<CollabChangedEvent>;
    for (const id of d.thread_ids ?? []) pendingThreads.add(id);
    if (d.folders) pendingFolders.add(d.account_id ?? null);
    if (!listTimer) listTimer = setTimeout(() => flush(qc), 200);
    return true;
  }
  if (type === "collab.accounts") {
    const id = typeof data.account_id === "string" ? data.account_id : null;
    void qc.invalidateQueries({ queryKey: qk.mailAccounts });
    void qc.invalidateQueries({ queryKey: qk.collabStatus });
    // An open conversation carries its account's addresses and signatures (the reply's From): they follow.
    void qc.invalidateQueries({ queryKey: ["collab", "thread"], predicate: (query) => !id || (query.state.data as MailThreadDetail | undefined)?.account.id === id });
    pendingFolders.add(id);
    if (!listTimer) listTimer = setTimeout(() => flush(qc), 200);
    return true;
  }
  return false;
}
