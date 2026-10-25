// The signed-in accounts, the active one, its workspaces and the selected one.
// Resolved once at boot (refresh cookie, then /v1/me) so nothing under the app
// gates its first render on it again.
//
// A browser can hold several accounts at once (contracts/tokens.md). Only ids
// are persisted (the active account, each account's last workspace); access
// tokens stay in the API client's memory, refresh tokens in HttpOnly cookies.
// Switching accounts clears the whole query cache, so data from one account
// never renders under another. Switching workspace within an account keeps it:
// every workspace key starts with its workspace (lib/queries.ts).

import { create } from "zustand";
import { api } from "./api/endpoints";
import {
  forgetAccount,
  forgetAllAccounts,
  getActiveAccount,
  http,
  onAccountSession,
  refreshSession,
  setAccessToken,
  setActiveAccount,
  setWorkspaceId,
} from "./api/client";
import type { Account, Me, Membership, Role, TokenResponse, User, Workspace } from "./api/types";
import { setClock } from "./format";
import { qk } from "./queries";
import { queryClient } from "./queryClient";
import { workspaceTarget } from "./workspaceParam";

export type SessionStatus = "loading" | "anon" | "authed";

type SessionState = {
  status: SessionStatus;
  /** The active account. */
  user: User | null;
  memberships: Membership[];
  workspaceId: string | null;
  /** Every account signed in to in this browser, the active one included, in the order they were added. */
  accounts: Account[];
};

/** At most this many accounts per browser (the API answers 409 too_many_accounts beyond it). */
export const MAX_ACCOUNTS = 5;

const anonState = (): SessionState => ({ status: "anon", user: null, memberships: [], workspaceId: null, accounts: [] });

export const useSession = create<SessionState>(() => ({
  status: "loading",
  user: null,
  memberships: [],
  workspaceId: null,
  accounts: [],
}));

// Times of day follow the active account's clock.
useSession.subscribe((s) => setClock(s.user?.time_format));

// ─── Persistence: ids only ─────────────────────────────────────────────────

const wsKey = (uid: string) => `od-ws:${uid}`;
const ACTIVE_KEY = "od-account";

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    /* storage unavailable */
  }
}

const rememberedWorkspace = (uid: string) => read(wsKey(uid));
const remember = (uid: string, wid: string) => write(wsKey(uid), wid);

function pickWorkspace(uid: string, memberships: Membership[], prefer?: string | null): string | null {
  const ids = memberships.map((m) => m.workspace.id);
  for (const c of [prefer, rememberedWorkspace(uid)]) if (c && ids.includes(c)) return c;
  return ids[0] ?? null;
}

// ─── Accounts list ─────────────────────────────────────────────────────────

function accountOf(user: User, memberships: Membership[]): Account {
  return {
    user: { id: user.id, name: user.name, email: user.email, avatar_url: user.avatar_url },
    memberships: memberships.map((m) => ({
      role: m.role,
      workspace: { id: m.workspace.id, name: m.workspace.name, slug: m.workspace.slug, avatar_url: m.workspace.avatar_url },
    })),
  };
}

/** The list with the active account's entry taken from its live /v1/me. */
function withActive(list: Account[], user: User | null, memberships: Membership[]): Account[] {
  if (!user) return list;
  const mine = accountOf(user, memberships);
  return list.some((a) => a.user.id === user.id) ? list.map((a) => (a.user.id === user.id ? mine : a)) : [...list, mine];
}

let accountsSeq = 0;

/** Re-read the accounts this browser holds (never rotates a token). */
export async function loadAccounts(): Promise<Account[]> {
  const seq = ++accountsSeq;
  try {
    const { accounts } = await api.auth.accounts();
    if (seq !== accountsSeq) return useSession.getState().accounts;
    const s = useSession.getState();
    // An account whose session ended elsewhere disappears from the list; its token goes too.
    for (const a of s.accounts) if (!accounts.some((x) => x.user.id === a.user.id) && a.user.id !== s.user?.id) forgetAccount(a.user.id);
    const next = s.status === "authed" ? withActive(accounts, s.user, s.memberships) : accounts;
    useSession.setState({ accounts: next });
    return next;
  } catch {
    return useSession.getState().accounts;
  }
}

/** The accounts and their workspaces in switcher order (the order quick switch numbers them). */
export function switcherEntries(s: Pick<SessionState, "accounts" | "user" | "memberships">): { account: Account; membership: Account["memberships"][number] }[] {
  const list = s.user ? withActive(s.accounts, s.user, s.memberships) : s.accounts;
  return list.flatMap((account) => account.memberships.map((membership) => ({ account, membership })));
}

// ─── Boot and sign-in ──────────────────────────────────────────────────────

async function loadMe(prefer?: string | null): Promise<SessionState> {
  const me = await api.me.get();
  return applyMe(me, prefer ?? useSession.getState().workspaceId);
}

function applyMe(me: Me, prefer?: string | null): SessionState {
  const wid = pickWorkspace(me.user.id, me.memberships, prefer);
  setWorkspaceId(wid);
  if (wid) remember(me.user.id, wid);
  write(ACTIVE_KEY, me.user.id);
  const prev = useSession.getState();
  const next: SessionState = {
    status: "authed",
    user: me.user,
    memberships: me.memberships,
    workspaceId: wid,
    accounts: withActive(prev.accounts, me.user, me.memberships),
  };
  useSession.setState(next);
  return next;
}

let booting: Promise<unknown> | null = null;
const current = () => useSession.getState();

/** Resolve the session once: the remembered account's refresh cookie (or the most recent one), then /v1/me. */
export function bootSession(): Promise<SessionState> {
  if (!booting) {
    booting = (async () => {
      const stored = read(ACTIVE_KEY);
      let r: TokenResponse | null = null;
      if (stored) {
        setActiveAccount(stored);
        r = await refreshSession(stored);
      }
      if (!r) {
        setActiveAccount(null);
        r = await refreshSession(null);
      }
      if (!r) {
        useSession.setState(anonState());
        return;
      }
      setActiveAccount(r.user.id);
      try {
        await loadMe();
        void loadAccounts();
      } catch {
        useSession.setState(anonState());
      }
    })();
  }
  // Guards always see the live state (an account switch changes it after boot).
  return booting.then(current);
}

/**
 * After login, register, MFA or OAuth exchange. The account joins the others in
 * this browser (or replaces its own earlier session) and becomes the active one.
 */
export async function signIn(tok: TokenResponse, preferWorkspace?: string | null) {
  const prev = getActiveAccount();
  if (prev && prev !== tok.user.id) queryClient.clear();
  setActiveAccount(tok.user.id);
  setAccessToken(tok.access_token, tok.expires_in);
  const p = loadMe(preferWorkspace);
  booting = p;
  const s = await p;
  void loadAccounts();
  return s;
}

/** Re-read memberships (after creating or joining a workspace). */
export function reloadSession(preferWorkspace?: string | null) {
  const p = loadMe(preferWorkspace);
  booting = p;
  return p;
}

/** Whether realtime keeps a workspace's cache current (set by the realtime manager). */
let liveCheck: (wid: string) => boolean = () => false;
export function setLiveCheck(fn: (wid: string) => boolean) {
  liveCheck = fn;
}

/**
 * Move the focus to another workspace of the active account. The cache stays: its keys
 * carry their workspace (lib/queries.ts), so nothing of one is read for another. What
 * realtime did not keep current is refetched in the background while it shows.
 */
export function selectWorkspace(id: string) {
  const { user, memberships, workspaceId } = useSession.getState();
  if (!user || !memberships.some((m) => m.workspace.id === id)) return;
  setWorkspaceId(id);
  remember(user.id, id);
  useSession.setState({ workspaceId: id });
  if (id !== workspaceId && !liveCheck(id)) void queryClient.invalidateQueries({ queryKey: qk.scope(id) });
}

/**
 * Opens the workspace a link names (`?ws=`, contracts/api.md "Core notes"): in the
 * active account when it is a member, else in another account signed in to in
 * this browser that is. Anyone else's workspace is ignored.
 */
export async function openLinkedWorkspace(ws: string): Promise<void> {
  let target = workspaceTarget(ws, useSession.getState());
  if (target.kind === "none") {
    await loadAccounts();
    target = workspaceTarget(ws, useSession.getState());
  }
  if (target.kind === "select") {
    selectWorkspace(ws);
  } else if (target.kind === "account") {
    await switchAccount(target.userId, ws);
  }
}

/**
 * Make another signed-in account active, optionally in one of its workspaces.
 * The target's /v1/me is read with its own token before anything changes; then
 * the query cache is cleared and the session flips in one step. Resolves null
 * when that account's session has ended (it is dropped from the list).
 */
export async function switchAccount(userId: string, workspaceId?: string | null): Promise<SessionState | null> {
  const s = useSession.getState();
  if (s.user?.id === userId) {
    if (workspaceId && workspaceId !== s.workspaceId) {
      selectWorkspace(workspaceId);
    }
    return useSession.getState();
  }
  let me: Me;
  try {
    if (!(await refreshSession(userId))) {
      dropAccount(userId);
      return null;
    }
    me = await http.get<Me>("/v1/me", { workspace: false, as: userId });
  } catch {
    return null;
  }
  queryClient.clear();
  setActiveAccount(userId);
  const next = applyMe(me, workspaceId);
  booting = Promise.resolve();
  return next;
}

function dropAccount(userId: string) {
  forgetAccount(userId);
  useSession.setState((s) => ({ accounts: s.accounts.filter((a) => a.user.id !== userId) }));
}

export function updateUser(u: User) {
  useSession.setState((s) => ({ user: u, accounts: withActive(s.accounts, u, s.memberships) }));
}

export function updateWorkspace(w: Workspace) {
  useSession.setState((s) => {
    const memberships = s.memberships.map((m) => (m.workspace.id === w.id ? { ...m, workspace: w } : m));
    return { memberships, accounts: withActive(s.accounts, s.user, memberships) };
  });
}

/** End every session locally (all accounts). */
export function clearSession() {
  accountsSeq++;
  forgetAllAccounts();
  setWorkspaceId(null);
  write(ACTIVE_KEY, null);
  booting = Promise.resolve();
  useSession.setState(anonState());
}

/**
 * The active account is gone (signed out, deleted, its session ended). Another
 * signed-in account takes over when there is one; otherwise the browser is
 * signed out. Resolves the state that follows.
 */
export async function leaveActiveAccount(): Promise<SessionState> {
  const s = useSession.getState();
  const uid = s.user?.id;
  // Started before the token goes (a session that ended elsewhere fails them, and says so).
  void runBeforeSignOut();
  if (uid) forgetAccount(uid);
  accountsSeq++;
  // The server's list is the truth (an account may have ended elsewhere, or not loaded yet).
  let others: Account[];
  try {
    others = (await api.auth.accounts()).accounts;
  } catch {
    others = s.accounts;
  }
  others = others.filter((a) => a.user.id !== uid);
  useSession.setState({ accounts: others });
  for (const a of others) {
    const next = await switchAccount(a.user.id);
    if (next) return next;
  }
  clearSession();
  queryClient.clear();
  return useSession.getState();
}

// Work that must finish under the account before it signs out (a reply held for its undo
// window goes out first, with that account's credentials).
type LeaveHook = () => Promise<unknown>;
const leaveHooks = new Set<LeaveHook>();
/** Runs `h` before an account signs out, is deleted, or leaves this browser; awaited when it can be. */
export function beforeSignOut(h: LeaveHook): () => void {
  leaveHooks.add(h);
  return () => {
    leaveHooks.delete(h);
  };
}
/** Runs every beforeSignOut hook. Each starts at once, while the account's token is still held. */
export function runBeforeSignOut(): Promise<unknown> {
  return Promise.allSettled([...leaveHooks].map((h) => h()));
}

/**
 * Log out of the active account in this browser; the next signed-in account
 * takes over. With `all`, log out of every account here.
 */
export async function signOut(opts: { all?: boolean } = {}): Promise<SessionState> {
  const uid = useSession.getState().user?.id;
  await runBeforeSignOut();
  try {
    await api.auth.logout(opts.all ? { all: true } : uid ? { user_id: uid } : {});
  } catch {
    /* the session ends locally either way */
  }
  if (opts.all) {
    clearSession();
    queryClient.clear();
    return useSession.getState();
  }
  return leaveActiveAccount();
}

// A refresh that fails mid-session ends that account; the active one hands over
// to another signed-in account, or the browser is signed out.
type FallbackListener = (s: SessionState) => void;
const fallbackListeners = new Set<FallbackListener>();
/** Called after the active account's session ended on its own and another account (or nobody) took over. */
export function onAccountFallback(l: FallbackListener): () => void {
  fallbackListeners.add(l);
  return () => {
    fallbackListeners.delete(l);
  };
}

let leaving = false;
onAccountSession((uid, r) => {
  const s = useSession.getState();
  if (r) {
    if (uid === s.user?.id) useSession.setState({ user: r.user, accounts: withActive(s.accounts, r.user, s.memberships) });
    return;
  }
  if (!uid || s.status !== "authed") return;
  if (uid !== s.user?.id) {
    dropAccount(uid);
    return;
  }
  if (leaving) return;
  leaving = true;
  void leaveActiveAccount()
    .then((next) => fallbackListeners.forEach((l) => l(next)))
    .finally(() => {
      leaving = false;
    });
});

export function currentMembership(s: SessionState = useSession.getState()): Membership | null {
  return s.memberships.find((m) => m.workspace.id === s.workspaceId) ?? null;
}

export function useWorkspace(): Workspace | null {
  return useSession((s) => s.memberships.find((m) => m.workspace.id === s.workspaceId)?.workspace ?? null);
}

export function useRole(): Role {
  return useSession((s) => s.memberships.find((m) => m.workspace.id === s.workspaceId)?.role ?? "viewer");
}

export function useUser(): User {
  const u = useSession((s) => s.user);
  if (!u) throw new Error("useUser outside an authenticated route");
  return u;
}

const rank: Record<Role, number> = { viewer: 0, agent: 1, admin: 2, owner: 3 };
export function atLeast(role: Role, min: Role) {
  return rank[role] >= rank[min];
}
