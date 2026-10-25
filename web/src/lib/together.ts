// Workspaces together (contracts/api.md "Workspaces together"): a person in several
// workspaces sees them side by side in one sidebar, one bell and the Everywhere lists,
// or one at a time (Separate, the switcher alone). Pages still work in one workspace,
// the focus; opening anything of another moves the focus there, without a reload and
// without clearing the cache (its keys carry their workspace, lib/queries.ts).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, type NavigateOptions } from "@tanstack/react-router";
import { useCallback, useMemo } from "react";
import { api } from "./api/endpoints";
import type { Membership, WorkspacePrefs, WorkspacePrefsInput } from "./api/types";
import { q, qk } from "./queries";
import { selectWorkspace, useSession } from "./session";

export const EVERYWHERE = "everywhere";

export const DEFAULT_PREFS: WorkspacePrefs = {
  mode: "together",
  mode_set: false,
  layout: "grouped",
  order: [],
  hidden: [],
  collapsed: [],
  items: { hidden: [], order: [] },
  everywhere: true,
};

// ─── Pure helpers ──────────────────────────────────────────────────────────

/** Together shows only for a person in two or more workspaces who did not choose Separate. */
export function isTogether(prefs: Pick<WorkspacePrefs, "mode">, workspaces: number): boolean {
  return workspaces >= 2 && prefs.mode === "together";
}

/** Workspaces in the person's order: the ones they placed first, the rest in joining order. */
export function orderWorkspaces<M extends { workspace: { id: string } }>(list: M[], order: string[]): M[] {
  const rank = new Map(order.map((id, i) => [id, i]));
  return list
    .map((m, i) => ({ m, i, r: rank.get(m.workspace.id) }))
    .sort((a, b) => (a.r ?? Infinity) - (b.r ?? Infinity) || a.i - b.i)
    .map((x) => x.m);
}

/** The workspaces the sidebar shows: ordered, without the hidden ones, the focus always kept. */
export function shownWorkspaces<M extends { workspace: { id: string } }>(list: M[], prefs: Pick<WorkspacePrefs, "order" | "hidden">, focus: string | null): M[] {
  const hidden = new Set(prefs.hidden);
  return orderWorkspaces(list, prefs.order).filter((m) => !hidden.has(m.workspace.id) || m.workspace.id === focus);
}

/** The order after moving workspace `id` by `delta` places (keyboard and drag both land here). */
export function moveWorkspace(ids: string[], id: string, delta: number): string[] {
  const i = ids.indexOf(id);
  if (i < 0) return ids;
  const j = Math.max(0, Math.min(ids.length - 1, i + delta));
  if (i === j) return ids;
  const next = ids.slice();
  next.splice(i, 1);
  next.splice(j, 0, id);
  return next;
}

/** A sidebar item's id, qualified by its workspace (contracts/api.md): `<wid>/view:<id>`. */
export const itemId = (wid: string, kind: string, id?: string) => `${wid}/${kind}${id ? `:${id}` : ""}`;

export function toggleIn(list: string[], x: string, on?: boolean): string[] {
  const has = list.includes(x);
  const want = on ?? !has;
  if (want === has) return list;
  return want ? [...list, x] : list.filter((y) => y !== x);
}

export type Tagged<T> = { workspaceId: string; row: T };

/**
 * Rows from several workspaces in one list, newest activity first. Each workspace's own
 * order is kept for ties, and a workspace's row never takes another's place.
 */
export function mergeByActivity<T>(lists: { workspaceId: string; rows: T[] }[], at: (row: T) => string | null | undefined, limit = Infinity): Tagged<T>[] {
  const all: (Tagged<T> & { t: number; i: number; w: number })[] = [];
  lists.forEach((l, w) =>
    l.rows.forEach((row, i) => {
      const s = at(row);
      const t = s ? Date.parse(s) : NaN;
      all.push({ workspaceId: l.workspaceId, row, t: Number.isNaN(t) ? -Infinity : t, i, w });
    }),
  );
  all.sort((a, b) => b.t - a.t || a.w - b.w || a.i - b.i);
  return all.slice(0, limit).map(({ workspaceId, row }) => ({ workspaceId, row }));
}

/** One or two letters for a workspace without a logo. */
export function workspaceInitials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (!words.length) return "?";
  const first = Array.from(words[0])[0] ?? "?";
  const second = words.length > 1 ? (Array.from(words[1])[0] ?? "") : "";
  return (first + second).toUpperCase();
}

// ─── Prefs: cached per person so the first paint already has them ─────────

const prefsKey = (uid: string) => `od-together:${uid}`;

function storedPrefs(uid: string | undefined): WorkspacePrefs | undefined {
  if (!uid) return undefined;
  try {
    const raw = localStorage.getItem(prefsKey(uid));
    return raw ? { ...DEFAULT_PREFS, ...(JSON.parse(raw) as Partial<WorkspacePrefs>) } : undefined;
  } catch {
    return undefined;
  }
}

function storePrefs(uid: string | undefined, p: WorkspacePrefs) {
  if (!uid) return;
  try {
    localStorage.setItem(prefsKey(uid), JSON.stringify(p));
  } catch {
    /* storage unavailable */
  }
}

export function useWorkspacePrefs(): WorkspacePrefs {
  const uid = useSession((s) => s.user?.id);
  const { data } = useQuery({ ...q.workspacePrefs(), initialData: () => storedPrefs(uid), initialDataUpdatedAt: 0 });
  return data ?? DEFAULT_PREFS;
}

/** Whether Together is on for this person, and the workspaces it shows. */
export function useTogether() {
  const prefs = useWorkspacePrefs();
  const memberships = useSession((s) => s.memberships);
  const focus = useSession((s) => s.workspaceId);
  return useMemo(() => {
    const active = isTogether(prefs, memberships.length);
    const ordered = orderWorkspaces(memberships, prefs.order);
    const shown = active ? shownWorkspaces(memberships, prefs, focus) : memberships.filter((m) => m.workspace.id === focus);
    return { active, prefs, ordered, shown, focus };
  }, [prefs, memberships, focus]);
}

/** Change the settings: applied at once, put back when the server refuses. */
export function useUpdateWorkspacePrefs() {
  const qc = useQueryClient();
  const uid = useSession((s) => s.user?.id);
  return useMutation({
    mutationFn: (b: WorkspacePrefsInput) => api.me.putWorkspacePrefs(b),
    onMutate: async (b) => {
      await qc.cancelQueries({ queryKey: qk.workspacePrefs });
      const before = qc.getQueryData<WorkspacePrefs>(qk.workspacePrefs) ?? DEFAULT_PREFS;
      const { mode, ...rest } = b;
      const next: WorkspacePrefs = { ...before, ...rest, ...(mode === undefined ? {} : mode === "" ? { mode: "together", mode_set: false } : { mode, mode_set: true }) };
      qc.setQueryData(qk.workspacePrefs, next);
      storePrefs(uid, next);
      return { before };
    },
    onError: (_e, _b, ctx) => {
      if (!ctx) return;
      qc.setQueryData(qk.workspacePrefs, ctx.before);
      storePrefs(uid, ctx.before);
    },
    onSuccess: (p) => {
      qc.setQueryData(qk.workspacePrefs, p);
      storePrefs(uid, p);
    },
  });
}

/** The workspace's membership, when the person is in it. */
export function useMembership(wid: string | null | undefined): Membership | undefined {
  return useSession((s) => s.memberships.find((m) => m.workspace.id === wid));
}

/**
 * Open a page of workspace `wid`: the focus moves there first (no reload; what the cache
 * holds of it shows at once), then the app navigates.
 */
export function useOpenInWorkspace() {
  const navigate = useNavigate();
  return useCallback(
    (wid: string, to: NavigateOptions) => {
      if (useSession.getState().workspaceId !== wid) selectWorkspace(wid);
      void navigate(to);
    },
    [navigate],
  );
}
