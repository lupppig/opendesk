// The Collab parts of the TanStack Query cache: patching conversations everywhere
// they are listed, taking them out of lists, snapshots for rollback, and the
// realtime events (contracts/collab.md "Events": ids only, so lists refetch).

import type { InfiniteData, QueryClient, QueryKey } from "@tanstack/react-query";
import type { CollabThreadQuery } from "../api/endpoints";
import type { CollabChangedEvent, MailFolder, MailThread, MailThreadDetail, Page } from "../api/types";
import { inWorkspace, scopeWorkspace } from "../api/client";
import { keyBody, qk } from "../queries";
import { leavesList, listPlace } from "./actions";
import type { MailThreadAction } from "../api/types";

type ThreadPages = InfiniteData<Page<MailThread>, string | null>;

/** Every cached folder, across accounts. */
export function cachedFolders(qc: QueryClient): MailFolder[] {
  return qc.getQueriesData<MailFolder[]>({ queryKey: qk.of("collab", "folders") }).flatMap(([, d]) => d ?? []);
}

/** The cached copies of conversations, from their open thread or any list. */
export function cachedThreads(qc: QueryClient, ids: Set<string>): MailThread[] {
  const found = new Map<string, MailThread>();
  for (const id of ids) {
    const t = qc.getQueryData<MailThreadDetail>(qk.mailThread(id))?.thread;
    if (t) found.set(id, t);
  }
  for (const [, data] of qc.getQueriesData<ThreadPages>({ queryKey: qk.mailThreadLists })) {
    for (const p of data?.pages ?? []) for (const t of p.data) if (ids.has(t.id) && !found.has(t.id)) found.set(t.id, t);
  }
  return [...found.values()];
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
    const query = (keyBody(key)[2] ?? {}) as CollabThreadQuery;
    if (!leavesList(action, listPlace(query, folders), folderId)) continue;
    if (!data.pages.some((p) => p.data.some((t) => ids.has(t.id)))) continue;
    qc.setQueryData<ThreadPages>(key, { ...data, pages: data.pages.map((p) => ({ ...p, data: p.data.filter((t) => !ids.has(t.id)) })) });
  }
}

export type Snapshot = [QueryKey, unknown][];

/** Thread lists, the counts and the given open threads, to put back when an action fails. */
export function snapshotThreads(qc: QueryClient, ids: Iterable<string>): Snapshot {
  const out: Snapshot = qc.getQueriesData({ queryKey: qk.mailThreadLists });
  out.push([qk.mailCounts, qc.getQueryData(qk.mailCounts)]);
  for (const id of ids) out.push([qk.mailThread(id), qc.getQueryData(qk.mailThread(id))]);
  return out;
}

export function restore(qc: QueryClient, snap: Snapshot) {
  for (const [key, data] of snap) qc.setQueryData(key, data);
}

// Pending work per workspace: events of several workspaces can arrive in one burst
// (several workspaces), and each flush runs in its own workspace's keys.
type Pending = { timer: ReturnType<typeof setTimeout> | null; threads: Set<string>; folders: Set<string | null> };
const pending = new Map<string, Pending>();

function pendingFor(wid: string): Pending {
  let p = pending.get(wid);
  if (!p) pending.set(wid, (p = { timer: null, threads: new Set(), folders: new Set() }));
  return p;
}

/** Coalesces bursts of events (a sync, a bulk action) into one refetch of what is shown. */
function flush(qc: QueryClient, wid: string) {
  const p = pending.get(wid);
  pending.delete(wid);
  if (!p) return;
  inWorkspace(wid, () => {
    void qc.invalidateQueries({ queryKey: qk.mailThreadLists });
    void qc.invalidateQueries({ queryKey: qk.mailCounts });
    // A person's conversations may be any of them.
    void qc.invalidateQueries({ queryKey: qk.mailPeople });
    void qc.invalidateQueries({ queryKey: qk.of("collab", "everywhere") });
    for (const id of p.threads) void qc.invalidateQueries({ queryKey: qk.mailThread(id), exact: true });
    for (const acc of p.folders) void qc.invalidateQueries({ queryKey: acc ? qk.mailFolders(acc) : qk.of("collab", "folders") });
  });
  // The sidebar's numbers of every workspace come in one answer.
  void qc.invalidateQueries({ queryKey: qk.overview });
}

function schedule(qc: QueryClient, wid: string, p: Pending) {
  if (!p.timer) p.timer = setTimeout(() => flush(qc, wid), 200);
}

/** Applies a Collab event of the workspace in scope; returns true when it was one. */
export function applyCollabEvent(qc: QueryClient, type: string, data: Record<string, unknown>): boolean {
  const wid = scopeWorkspace() ?? "none";
  if (type === "collab.changed") {
    const d = data as Partial<CollabChangedEvent>;
    const p = pendingFor(wid);
    for (const id of d.thread_ids ?? []) p.threads.add(id);
    if (d.folders) p.folders.add(d.account_id ?? null);
    schedule(qc, wid, p);
    return true;
  }
  if (type === "collab.accounts") {
    const id = typeof data.account_id === "string" ? data.account_id : null;
    void qc.invalidateQueries({ queryKey: qk.mailAccounts });
    void qc.invalidateQueries({ queryKey: qk.collabStatus });
    // An open conversation carries its account's addresses and signatures (the reply's From): they follow.
    void qc.invalidateQueries({ queryKey: qk.of("collab", "thread"), predicate: (query) => !id || (query.state.data as MailThreadDetail | undefined)?.account.id === id });
    const p = pendingFor(wid);
    p.folders.add(id);
    schedule(qc, wid, p);
    return true;
  }
  return false;
}
