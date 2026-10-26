// Several workspaces (contracts/api.md "Several workspaces"): a person in several
// workspaces works one at a time, the focus, and reaches the others through Spaces, the
// one bell and the Everywhere lists. Opening anything of another workspace moves the
// focus there, without a reload and without clearing the cache (its keys carry their
// workspace, lib/queries.ts). The switcher lists workspaces in the person's order, with
// the ones they hid folded away.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, type NavigateOptions } from "@tanstack/react-router";
import { useCallback, useMemo } from "react";
import { api } from "./api/endpoints";
import type { Membership, WorkspacePrefs, WorkspacePrefsInput } from "./api/types";
import { q, qk } from "./queries";
import { selectWorkspace, useSession } from "./session";

export const DEFAULT_PREFS: WorkspacePrefs = { order: [], hidden: [] };

// ─── Pure helpers ──────────────────────────────────────────────────────────

/** Workspaces in the person's order: the ones they placed first, the rest in joining order. */
export function orderWorkspaces<M extends { workspace: { id: string } }>(list: M[], order: string[]): M[] {
  const rank = new Map(order.map((id, i) => [id, i]));
  return list
    .map((m, i) => ({ m, i, r: rank.get(m.workspace.id) }))
    .sort((a, b) => (a.r ?? Infinity) - (b.r ?? Infinity) || a.i - b.i)
    .map((x) => x.m);
}

/** The workspaces shown in the switcher, the bell and the Everywhere lists: ordered, without the hidden ones, the focus always kept. */
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

const prefsKey = (uid: string) => `od-workspaces:${uid}`;

function storedPrefs(uid: string | undefined): WorkspacePrefs | undefined {
  if (!uid) return undefined;
  try {
    const raw = localStorage.getItem(prefsKey(uid));
    if (!raw) return undefined;
    const p = JSON.parse(raw) as Partial<WorkspacePrefs>;
    return { order: Array.isArray(p.order) ? p.order : [], hidden: Array.isArray(p.hidden) ? p.hidden : [] };
  } catch {
    return undefined;
  }
}

function storePrefs(uid: string | undefined, p: WorkspacePrefs) {
  if (!uid) return;
  try {
    localStorage.setItem(prefsKey(uid), JSON.stringify({ order: p.order, hidden: p.hidden }));
  } catch {
    /* storage unavailable */
  }
}

export function useWorkspacePrefs(): WorkspacePrefs {
  const uid = useSession((s) => s.user?.id);
  const several = useSession((s) => s.memberships.length > 1);
  const { data } = useQuery({ ...q.workspacePrefs(), enabled: several, initialData: () => storedPrefs(uid), initialDataUpdatedAt: 0 });
  return data ?? DEFAULT_PREFS;
}

/**
 * The person's workspaces as they arranged them: all of them in order, the shown ones
 * (the hidden ones left out, the focus kept), and whether they are in several (the one
 * bell, the Everywhere lists and the socket that follows every workspace).
 */
export function useWorkspaces() {
  const prefs = useWorkspacePrefs();
  const memberships = useSession((s) => s.memberships);
  const focus = useSession((s) => s.workspaceId);
  return useMemo(() => {
    const several = memberships.length > 1;
    const ordered = orderWorkspaces(memberships, prefs.order);
    const shown = several ? shownWorkspaces(memberships, prefs, focus) : memberships.filter((m) => m.workspace.id === focus);
    return { several, prefs, ordered, shown, focus };
  }, [prefs, memberships, focus]);
}

/** Change the arrangement: applied at once, put back when the server refuses. */
export function useUpdateWorkspacePrefs() {
  const qc = useQueryClient();
  const uid = useSession((s) => s.user?.id);
  return useMutation({
    mutationFn: (b: WorkspacePrefsInput) => api.me.putWorkspacePrefs(b),
    onMutate: async (b) => {
      await qc.cancelQueries({ queryKey: qk.workspacePrefs });
      const before = qc.getQueryData<WorkspacePrefs>(qk.workspacePrefs) ?? DEFAULT_PREFS;
      const next: WorkspacePrefs = { ...before, ...b };
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
 * holds of it shows at once), then the app navigates. A picked Space stays picked.
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
