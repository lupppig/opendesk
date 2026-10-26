// Collab hooks: the feature gate, accounts, the sidebar prefs (saved optimistically)
// and thread actions with rollback.

import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { api } from "../api/endpoints";
import type { MailAccount, MailCounts, MailFolder, MailThread, MailThreadAction, SidebarPrefs } from "../api/types";
import { qk, q, removeFromList, upsertInList } from "../queries";
import { atLeast, useRole } from "../session";
import { applyAction, countsAfter } from "./actions";
import { cachedFolders, cachedThreads, dropFromLists, mapThreads, patchThreads, restore, snapshotThreads } from "./cache";
import { aliasesOf, ownEmails } from "./identities";
import { normalizePrefs } from "./sidebarPrefs";

/** Whether Collab is on for this workspace. Off (and nothing changes) until the API says it is on. */
export function useCollab() {
  const { data, isFetched } = useQuery(q.collabStatus());
  return { enabled: data?.enabled === true, status: data ?? null, isFetched };
}

/** Accounts the caller sees, theirs first; nothing is fetched while Collab is off. */
export function useMailAccounts() {
  const { enabled } = useCollab();
  return useQuery({ ...q.mailAccounts(), enabled });
}

/** Accounts for settings: admins also get the shared accounts of people who left, to manage or disconnect. */
export function useSettingsAccounts() {
  const { enabled } = useCollab();
  const admin = atLeast(useRole(), "admin");
  const mine = useQuery({ ...q.mailAccounts(), enabled: enabled && !admin });
  const managed = useQuery({ ...q.mailAccountsManage(), enabled: enabled && admin });
  return admin ? managed : mine;
}

/** Puts an account into both account lists (or takes it out). */
export function storeAccount(qc: ReturnType<typeof useQueryClient>, a: MailAccount | { id: string; removed: true }) {
  for (const key of [qk.mailAccounts, qk.mailAccountsManage]) {
    if ("removed" in a) removeFromList(qc, key, a.id);
    else upsertInList(qc, key, a);
  }
}

/** Every account's folders, by account id. */
export function useAllFolders(accounts: MailAccount[] | undefined, enabled = true): Record<string, MailFolder[] | undefined> {
  const ids = (accounts ?? []).map((a) => a.id);
  return useQueries({
    queries: ids.map((id) => ({ ...q.mailFolders(id), enabled })),
    combine: (results) => Object.fromEntries(ids.map((id, i) => [id, results[i]?.data])) as Record<string, MailFolder[] | undefined>,
  });
}

export function useSidebarPrefs(enabled: boolean): SidebarPrefs {
  const { data } = useQuery({ ...q.mailSidebar(), enabled });
  return useMemo(() => normalizePrefs(data), [data]);
}

/** Saves the sidebar prefs: shown at once, put back if the server refuses. */
// Sidebar saves go out one at a time, and only the newest prefs: quick clicks coalesce,
// an older save can never land after a newer one, and only the newest answer is applied.
let saveSeq = 0;
let inFlight: Promise<SidebarPrefs> | null = null;
let queued: SidebarPrefs | null = null;

function putSidebarPrefs(p: SidebarPrefs): Promise<SidebarPrefs> {
  queued = p;
  const run = async (): Promise<SidebarPrefs> => {
    let last: SidebarPrefs = p;
    while (queued) {
      const next = queued;
      queued = null;
      last = await api.collab.sidebar.put(next);
    }
    return last;
  };
  inFlight = (inFlight ?? Promise.resolve(p)).catch(() => p).then(run);
  return inFlight;
}

export function useSaveSidebarPrefs() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (p: SidebarPrefs) => putSidebarPrefs(p),
    onMutate: async (p) => {
      await qc.cancelQueries({ queryKey: qk.mailSidebar });
      const prev = qc.getQueryData<SidebarPrefs>(qk.mailSidebar);
      qc.setQueryData(qk.mailSidebar, p);
      return { prev, seq: ++saveSeq };
    },
    onError: (_e, _p, ctx) => {
      if (ctx?.prev && ctx.seq === saveSeq) qc.setQueryData(qk.mailSidebar, ctx.prev);
    },
    onSuccess: (p, _v, ctx) => {
      if (ctx?.seq === saveSeq) qc.setQueryData(qk.mailSidebar, p);
    },
  });
}

/** Whether the caller can act in Collab at all (viewers read only). */
export function useCanAct(): boolean {
  return atLeast(useRole(), "agent");
}

export type ThreadActionVars = { ids: string[]; action: MailThreadAction; folderId?: string };

/** Applies an action to conversations: in the cache (and the sidebar's numbers) at once, then the API; a refusal puts everything back. */
export function useThreadAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: ThreadActionVars) => api.collab.threads.action({ thread_ids: v.ids, action: v.action, folder_id: v.folderId }),
    onMutate: async (v) => {
      await Promise.all([qc.cancelQueries({ queryKey: qk.mailThreadLists }), qc.cancelQueries({ queryKey: qk.mailCounts })]);
      const snap = snapshotThreads(qc, v.ids);
      const ids = new Set(v.ids);
      // The sidebar's numbers follow at once (a conversation opened reads as read there too).
      const before = cachedThreads(qc, ids);
      const addresses = Object.fromEntries((qc.getQueryData<MailAccount[]>(qk.mailAccounts) ?? []).filter((a) => aliasesOf(a).length).map((a) => [a.id, ownEmails(a)]));
      qc.setQueryData<MailCounts>(qk.mailCounts, (c) => (c ? countsAfter(c, before, v.action, cachedFolders(qc), v.folderId, addresses) : c));
      mapThreads(qc, ids, (t) => applyAction(t, v.action, v.folderId));
      dropFromLists(qc, ids, v.action, v.folderId);
      return { snap };
    },
    onError: (_e, _v, ctx) => {
      if (ctx) restore(qc, ctx.snap);
    },
    onSuccess: (r: { threads: MailThread[] }) => patchThreads(qc, r?.threads ?? []),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: qk.mailCounts });
    },
  });
}
