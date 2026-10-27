// Spaces hooks: the list (saved optimistically, put back if refused), starter Spaces, and the
// data each workspace's rows read. Every workspace's data lives under its own cache keys
// (lib/queries.ts): a row of another workspace reads its teams, views and mail accounts with
// `inWorkspace`, and its numbers from the overview (GET /v1/me/overview), all kept live by the
// one socket that follows every workspace. Opening it moves the focus there: its data is
// already in the cache, so nothing reloads.

import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { inWorkspace } from "../api/client";
import { api } from "../api/endpoints";
import type { CollabStatus, MailAccount, MailCounts, MailFolder, Role, SidebarPrefs, Space, SpacesResponse, Team, TicketCounts, View, WorkspaceOverview } from "../api/types";
import { normalizePrefs } from "../collab/sidebarPrefs";
import { q, qk } from "../queries";
import { useSession } from "../session";
import { teamsOf } from "../ticketCache";
import { forWire, normalizeSpace, normalizeSpaces } from "./model";

// ─── The list ──────────────────────────────────────────────────────────────

/** The user's Spaces. `fresh`: read from the API, so a remembered id that names nothing can be let go. */
export function useSpaces(): { spaces: Space[] | undefined; fresh: boolean; isError: boolean } {
  const { data, isError, isSuccess, isPlaceholderData } = useQuery(q.spaces());
  return { spaces: data?.spaces, fresh: isSuccess && !isPlaceholderData, isError };
}

// Saves go out one at a time, and only the newest list: quick changes coalesce, an older
// save never lands after a newer one, and only the newest answer is applied.
let saveSeq = 0;
let inFlight: Promise<SpacesResponse> | null = null;
let queued: Space[] | null = null;

function putSpaces(list: Space[]): Promise<SpacesResponse> {
  queued = list;
  const run = async (): Promise<SpacesResponse> => {
    let last: SpacesResponse = { spaces: list, saved: true };
    while (queued) {
      const next = queued;
      queued = null;
      const r = await api.me.spaces.put(next.map(forWire));
      last = { spaces: normalizeSpaces(r?.spaces), saved: true };
    }
    return last;
  };
  inFlight = (inFlight ?? Promise.resolve(null)).catch(() => null).then(run);
  return inFlight;
}

/** Saves the whole list: shown at once, put back if the API refuses it. */
export function useSaveSpaces() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (list: Space[]) => putSpaces(list),
    onMutate: async (list) => {
      await qc.cancelQueries({ queryKey: qk.spaces });
      const prev = qc.getQueryData<SpacesResponse>(qk.spaces);
      qc.setQueryData<SpacesResponse>(qk.spaces, { spaces: list, saved: true });
      return { prev, seq: ++saveSeq };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev && ctx.seq === saveSeq) qc.setQueryData(qk.spaces, ctx.prev);
    },
    onSuccess: (r, _v, ctx) => {
      if (ctx?.seq === saveSeq) qc.setQueryData(qk.spaces, r);
    },
  });
}

// ─── Starter Spaces ────────────────────────────────────────────────────────

/** A workspace's starter Space (null: none; undefined: loading). */
export function useStarterSpace(wid: string | null) {
  return useQuery({ ...inWorkspace(wid, () => q.starter()), enabled: !!wid });
}

/** The starter Spaces of the given workspaces, by workspace id, once `enabled`. */
export function useStarterSpaces(wids: string[], enabled: boolean): Map<string, Space> {
  const results = useQueries({ queries: wids.map((wid) => ({ ...inWorkspace(wid, () => q.starter()), enabled, retry: false })) });
  const out = new Map<string, Space>();
  wids.forEach((wid, i) => {
    const s = results[i]?.data;
    if (s) out.set(wid, s);
  });
  return out;
}

/** Publishes the open workspace's starter Space (admins). Every item becomes the workspace's. */
export function useSaveStarter() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (s: Space) => normalizeSpace((await api.starterSpace.put(forWire(s)))?.space),
    onSuccess: (s) => qc.setQueryData(qk.starter, s),
  });
}

export function useRemoveStarter() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.starterSpace.remove(),
    onSuccess: () => qc.setQueryData(qk.starter, null),
  });
}

// ─── What each workspace's rows read ───────────────────────────────────────

/** Everything a workspace's rows in a Space read: the same shape for the open workspace and the others. */
export type WorkspaceData = {
  id: string;
  name: string;
  logo: string | null;
  /** The open workspace (its rows are the sidebar's own, with menus and waiting chats). */
  open: boolean;
  role: Role;
  /** Whether it has Collab; undefined while unknown. */
  collab: boolean | undefined;
  /** The member's SidebarPrefs there (every member has them, with or without Collab). */
  prefs: SidebarPrefs;
  counts: TicketCounts | undefined;
  teams: Team[] | undefined;
  myTeams: ReadonlySet<string>;
  views: View[] | undefined;
  accounts: MailAccount[] | undefined;
  mailCounts: MailCounts | undefined;
  folders: Record<string, MailFolder[] | undefined>;
};

export const canSeeTeam = (d: Pick<WorkspaceData, "myTeams">, t: Team) => !t.private || d.myTeams.has(t.id);

/** Every workspace's sidebar numbers, kept live by the socket that follows them all. */
export function useOverview(enabled = true): Map<string, WorkspaceOverview> {
  const several = useSession((s) => s.memberships.length > 1);
  const { data } = useQuery({ ...q.overview(), enabled: enabled && several });
  return new Map((data ?? []).map((o) => [o.id, o]));
}

/**
 * The data of the given workspaces, each read in its own cache keys. The open workspace's
 * numbers are its own live counts (the overview's until they load), the others' the overview's.
 * `folders`: the workspaces whose accounts' folders are needed (folder rows).
 */
export function useWorkspacesData(wids: string[], opts: { folders?: (wid: string) => boolean; enabled?: boolean } = {}): Map<string, WorkspaceData> {
  const uid = useSession((s) => s.user?.id ?? "");
  const focus = useSession((s) => s.workspaceId);
  const memberships = useSession((s) => s.memberships);
  const on = opts.enabled !== false;
  const overview = useOverview(on && wids.some((w) => w !== focus));
  const each = <T,>(make: () => T) => wids.map((w) => inWorkspace(w, make));
  const status = useQueries({ queries: each(() => ({ ...q.collabStatus(), enabled: on })) });
  const collabOf = (i: number): boolean | undefined => {
    const st = status[i];
    if (st?.data !== undefined) return (st.data as CollabStatus | null)?.enabled === true;
    const o = overview.get(wids[i]);
    if (o) return o.mail_counts !== null;
    return st?.isFetched ? false : undefined;
  };
  const counts = useQueries({ queries: wids.map((w) => inWorkspace(w, () => ({ ...q.counts(), enabled: on && w === focus }))) });
  const teams = useQueries({ queries: each(() => ({ ...q.teams(), enabled: on })) });
  const views = useQueries({ queries: each(() => ({ ...q.views(), enabled: on })) });
  const accounts = useQueries({ queries: wids.map((w, i) => inWorkspace(w, () => ({ ...q.mailAccounts(), enabled: on && collabOf(i) === true }))) });
  const mailCounts = useQueries({ queries: wids.map((w, i) => inWorkspace(w, () => ({ ...q.mailCounts(), enabled: on && w === focus && collabOf(i) === true }))) });
  const sidebar = useQueries({ queries: wids.map((w) => inWorkspace(w, () => ({ ...q.mailSidebar(), enabled: on }))) });
  const pairs = wids.flatMap((w, i) => (collabOf(i) && opts.folders?.(w) ? (accounts[i]?.data ?? []).map((a) => [w, a.id] as const) : []));
  const folders = useQueries({ queries: pairs.map(([w, a]) => inWorkspace(w, () => ({ ...q.mailFolders(a), enabled: on }))) });
  const out = new Map<string, WorkspaceData>();
  wids.forEach((w, i) => {
    const m = memberships.find((x) => x.workspace.id === w);
    if (!m) return;
    const o = overview.get(w);
    const collab = collabOf(i);
    const open = w === focus;
    const fs: Record<string, MailFolder[] | undefined> = {};
    pairs.forEach(([pw, a], j) => {
      if (pw === w) fs[a] = folders[j]?.data;
    });
    const tm = teams[i]?.data;
    out.set(w, {
      id: w,
      name: m.workspace.name,
      logo: m.workspace.avatar_url,
      open,
      role: m.role,
      collab,
      prefs: normalizePrefs(sidebar[i]?.data),
      counts: (open ? counts[i]?.data : undefined) ?? o?.ticket_counts ?? undefined,
      teams: tm,
      myTeams: new Set(teamsOf(tm ?? [], uid)),
      views: views[i]?.data,
      accounts: collab ? accounts[i]?.data : undefined,
      mailCounts: collab ? ((open ? mailCounts[i]?.data : undefined) ?? o?.mail_counts ?? undefined) : undefined,
      folders: fs,
    });
  });
  return out;
}

/** The open workspace's data, folders included (Favorites can name any folder). */
export function useOpenWorkspaceData(): WorkspaceData | null {
  const wid = useSession((s) => s.workspaceId);
  const data = useWorkspacesData(wid ? [wid] : [], { folders: () => true });
  return (wid && data.get(wid)) || null;
}
