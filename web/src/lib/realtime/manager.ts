// One socket per signed-in tab. Joins workspace:<wid> and user:<uid> for events,
// and ticket:<wid>:<id> for presence and typing on the open ticket (and, quietly,
// the tickets visible in the list). With Together (contracts/api.md "Workspaces
// together") the token covers the person's other workspaces too: the socket joins
// each one's workspace and private team channels, tracks seq per workspace, and
// applies every event to its own workspace's cache.

import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import { getWorkspaceId, inWorkspace } from "../api/client";
import { api } from "../api/endpoints";
import type { Envelope, Notification, Page, Ticket } from "../api/types";
import { keyBody, keyWorkspace, qk } from "../queries";
import { config } from "../config";
import { setLiveCheck } from "../session";
import { applyEnvelope } from "./apply";
import { syncDiff, syncState, type PresenceDiff, type PresenceState } from "./presence";
import { SeqSync } from "./seq";
import { applyLive, clearLive, clearPresence, noteCustomerTyping, noteTyping, setPresence, useRealtime } from "./store";
import { createSocket, type RtChannel, type RtSocket } from "./transport";

/** Query roots that realtime events keep current (see apply.ts). */
const LIVE_ROOTS = new Set(["tickets", "contacts", "companies", "notifications", "members", "kb"]);

export type ManagerOptions = {
  qc: QueryClient;
  /** The focus workspace. */
  workspaceId: string;
  userId: string;
  /** Follow every workspace the person is in (Together), not only the focus. */
  together?: boolean;
  /** A notification for this person, in workspace `workspaceId`. */
  onNotification?: (n: Notification, workspaceId: string) => void;
  onTicketCreated?: (t: Ticket, workspaceId: string) => void;
  onSelfMembershipChanged?: () => void;
};

type TicketMode = "active" | "watch";
/** team is the private team the channel was joined with (null: none). */
type TicketEntry = { ch: RtChannel; mode: TicketMode; state: PresenceState; team: string | null; wid: string; id: string };

/** One followed workspace: its channel, seq and private team channels. */
type Followed = { wid: string; sync: SeqSync; ch: RtChannel | null; joined: boolean; teamChannels: Map<string, RtChannel> };

const MAX_WATCHED = 30;
const TOKEN_REFRESH_MS = 4 * 60_000;

type WsClaims = { wid?: unknown; teams?: unknown; workspaces?: unknown };

function claims(token: string): WsClaims {
  const part = token.split(".")[1];
  if (!part) return {};
  try {
    const b64 = part.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(part.length / 4) * 4, "=");
    return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)))) as WsClaims;
  } catch {
    return {};
  }
}

const strings = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);

/**
 * The private teams a ws token may join (its `teams` claim). The payload is read
 * without checking the signature: the gateway checks it, this only picks topics.
 */
export function tokenTeams(token: string): string[] {
  return strings(claims(token).teams);
}

/**
 * Every workspace a ws token covers, with the private teams it may join in each: its
 * own (`wid`, `teams`) and the others of its `workspaces` claim (contracts/tokens.md).
 */
export function tokenWorkspaces(token: string): Map<string, string[]> {
  const c = claims(token);
  const out = new Map<string, string[]>();
  if (c.workspaces && typeof c.workspaces === "object") {
    for (const [wid, e] of Object.entries(c.workspaces as Record<string, unknown>)) {
      if (e && typeof e === "object") out.set(wid, strings((e as { teams?: unknown }).teams));
    }
  }
  if (typeof c.wid === "string") out.set(c.wid, strings(c.teams));
  return out;
}

export class RealtimeManager {
  private socket: RtSocket | null = null;
  private token = "";
  private tokenTimer: ReturnType<typeof setInterval> | null = null;
  private stopped = false;
  private followed = new Map<string, Followed>();
  /** The workspaces the current token covers, with their private teams. */
  private covered = new Map<string, string[]>();
  private tickets = new Map<string, TicketEntry>();
  private active: string | null = null;
  private watched = new Set<string>();
  /** Watch lists by who asked (the inbox list, waiting chats), merged into `watched`. */
  private watchSources = new Map<string, string[]>();
  private lastTyping = 0;
  private everOpened = false;
  private unsubscribeCache: (() => void) | null = null;
  private user: RtChannel | null = null;

  constructor(private opts: ManagerOptions) {}

  /** The focus workspace: tickets joined for presence are its. */
  get focus() {
    return this.opts.workspaceId;
  }

  /** Move the focus (Together keeps one manager across workspaces). */
  setFocus(wid: string) {
    if (wid === this.opts.workspaceId) return;
    this.opts.workspaceId = wid;
    this.active = null;
    this.watched = new Set();
    this.watchSources.clear();
    this.reconcileTickets();
  }

  /** Whether events keep this workspace's cache current right now. */
  isLive(wid: string) {
    const f = this.followed.get(wid);
    return !!f && f.joined && f.sync.last !== null && useRealtime.getState().status === "live";
  }

  /** The workspaces the socket follows. */
  following(): string[] {
    return [...this.followed.keys()];
  }

  private teamsOf(wid: string) {
    return this.covered.get(wid) ?? [];
  }

  private apply(wid: string, env: Envelope, viaUser = false) {
    const { onNotification, onTicketCreated } = this.opts;
    applyEnvelope(this.opts.qc, env, {
      me: this.opts.userId,
      viaUser,
      privateTeams: () => this.teamsOf(wid),
      onNotification: onNotification && ((n) => onNotification(n, wid)),
      onTicketCreated: onTicketCreated && ((t) => onTicketCreated(t, wid)),
      onSelfMembershipChanged: this.opts.onSelfMembershipChanged,
    });
  }

  private async fetchToken(): Promise<{ token: string; url: string }> {
    // The token is asked for in the focus workspace; once the session has moved on
    // (switched account, left or deleted it, logged out) there is nothing to ask for.
    const wid = getWorkspaceId();
    if (this.stopped || !wid || (!this.opts.together && wid !== this.opts.workspaceId)) throw new Error("realtime: workspace changed");
    const r = await api.realtimeToken(!!this.opts.together);
    this.token = r.token;
    const covered = tokenWorkspaces(r.token);
    // Separate: the focus alone, whatever the token says.
    if (!this.opts.together) for (const k of [...covered.keys()]) if (k !== this.opts.workspaceId) covered.delete(k);
    this.covered = covered;
    this.reconcileWorkspaces();
    this.reconcileTickets();
    return r;
  }

  async start() {
    setLiveCheck((wid) => this.isLive(wid));
    useRealtime.setState({ status: "connecting" });
    let url = config.gatewayUrl;
    try {
      const r = await this.fetchToken();
      if (r.url && !window.__OPENDESK__?.gatewayUrl && !import.meta.env.VITE_GATEWAY_URL) url = r.url;
    } catch {
      if (this.stopped) return;
      useRealtime.setState({ status: "offline" });
      setTimeout(() => !this.stopped && void this.start(), 5000);
      return;
    }
    if (this.stopped) return;

    const socket = createSocket(url, () => ({ token: this.token }));
    this.socket = socket;
    socket.onOpen(() => {
      useRealtime.setState({ status: "live" });
      this.everOpened = true;
    });
    const dropped = () => {
      if (this.stopped) return;
      useRealtime.setState({ status: this.everOpened ? "reconnecting" : "connecting" });
      // Tokens only need to be valid at connect; fetch a fresh one for the retry.
      this.fetchToken().catch(() => useRealtime.setState({ status: "offline" }));
    };
    socket.onClose(dropped);
    socket.onError(dropped);
    socket.connect();
    this.tokenTimer = setInterval(() => void this.fetchToken().catch(() => {}), TOKEN_REFRESH_MS);

    const user = socket.channel(`user:${this.opts.userId}`);
    user.on("event", (p) => {
      const env = p as Envelope;
      // Only the workspaces this socket follows: in Separate, the focus alone.
      const f = this.followed.get(env.workspace_id);
      if (!f) return;
      // Notifications are applied from here only (the workspace copy carries no topics).
      if (env.type === "notification.created") return this.apply(f.wid, env, true);
      if (f.sync.hasSeen(env.id)) return;
      f.sync.markSeen(env.id);
      this.apply(f.wid, env);
    });
    user.join();
    this.user = user;

    this.reconcileWorkspaces();
    this.reconcileTickets();
    // A ticket's team arrives with its data (or changes): rejoin its channel with it.
    this.unsubscribeCache = this.opts.qc.getQueryCache().subscribe((e) => {
      const wid = keyWorkspace(e.query.queryKey);
      if (!wid || e.type !== "updated") return;
      const key = keyBody(e.query.queryKey);
      if (key[0] !== "tickets" || key[1] !== "detail") return;
      const entry = this.tickets.get(`${wid}:${String(key[2])}`);
      if (entry && entry.team !== this.privateTeamOf(wid, entry.id)) this.reconcileTickets();
    });
  }

  /** Join the workspaces the token covers, and leave the ones it no longer does. */
  private reconcileWorkspaces() {
    if (!this.socket) return;
    for (const [wid, f] of this.followed) {
      if (this.covered.has(wid)) continue;
      f.ch?.leave();
      for (const ch of f.teamChannels.values()) ch.leave();
      this.followed.delete(wid);
    }
    for (const wid of this.covered.keys()) {
      let f = this.followed.get(wid);
      if (!f) {
        f = this.follow(wid);
        this.followed.set(wid, f);
      }
      this.reconcileTeams(f);
    }
  }

  private follow(wid: string): Followed {
    const qc = this.opts.qc;
    const f: Followed = {
      wid,
      joined: false,
      ch: null,
      teamChannels: new Map(),
      sync: new SeqSync({
        // Replays of a workspace go to that workspace, whichever has the focus.
        fetchSince: (since) => inWorkspace(wid, () => api.events(since)),
        apply: (env) => this.apply(wid, env),
        resync: () => void qc.invalidateQueries({ queryKey: qk.scope(wid) }),
      }),
    };
    const ch = this.socket!.channel(`workspace:${wid}`);
    ch.on("event", (p) => {
      const env = p as Envelope;
      if (env.workspace_id === wid) f.sync.receive(env);
    });
    ch.join().receive("ok", () => {
      if (!f.joined) {
        f.joined = true;
        const joinedAt = Date.now();
        if (f.sync.last === null) {
          inWorkspace(wid, () => api.events(0, 1))
            .then((page) => {
              if (f.sync.last !== null) return;
              f.sync.baseline(page.latest_seq);
              // Lists loaded before the join can miss events published in between,
              // which the channel never delivers: refetch those once.
              void qc.invalidateQueries({
                predicate: (q) =>
                  keyWorkspace(q.queryKey) === wid &&
                  LIVE_ROOTS.has(String(keyBody(q.queryKey)[0])) &&
                  q.state.dataUpdatedAt > 0 &&
                  q.state.dataUpdatedAt < joinedAt,
              });
            })
            .catch(() => {});
        }
      } else {
        // A rejoin after a dropped connection: pick up what we missed.
        void f.sync.catchUp();
      }
    });
    f.ch = ch;
    return f;
  }

  /**
   * A private team's ticket events skip workspace:<wid> (it gets a stand-in) and come on
   * the team's channel. They are applied as they arrive; seq stays the workspace's.
   */
  private reconcileTeams(f: Followed) {
    if (!this.socket) return;
    const want = new Set(this.teamsOf(f.wid));
    for (const [tid, ch] of f.teamChannels) {
      if (!want.has(tid)) {
        ch.leave();
        f.teamChannels.delete(tid);
      }
    }
    for (const tid of want) {
      if (f.teamChannels.has(tid)) continue;
      const ch = this.socket.channel(`team:${f.wid}:${tid}`);
      ch.on("event", (p) => {
        const env = p as Envelope;
        if (env.workspace_id !== f.wid || f.sync.hasSeen(env.id)) return;
        f.sync.markSeen(env.id);
        this.apply(f.wid, env);
      });
      ch.join();
      f.teamChannels.set(tid, ch);
    }
  }

  /**
   * The ticket's private team when the token may join it: a private team's presence,
   * typing and live previews reach only sockets that joined its ticket with team_id.
   */
  private privateTeamOf(wid: string, id: string): string | null {
    const qc = this.opts.qc;
    return inWorkspace(wid, () => {
      let team: string | null = null;
      const own = qc.getQueryData<Ticket>(qk.ticket(id));
      if (own) team = own.team_id ?? null;
      else {
        for (const [, data] of qc.getQueriesData<InfiniteData<Page<Ticket>>>({ queryKey: qk.ticketLists })) {
          const hit = data?.pages?.flatMap((p) => p.data ?? []).find((t) => t.id === id);
          if (hit) {
            team = hit.team_id ?? null;
            break;
          }
        }
      }
      return team && this.teamsOf(wid).includes(team) ? team : null;
    });
  }

  stop() {
    this.stopped = true;
    setLiveCheck(() => false);
    if (this.tokenTimer) clearInterval(this.tokenTimer);
    this.unsubscribeCache?.();
    this.unsubscribeCache = null;
    for (const e of this.tickets.values()) {
      e.ch.leave();
      clearPresence(e.id);
      clearLive(e.id);
    }
    this.tickets.clear();
    for (const f of this.followed.values()) {
      f.ch?.leave();
      for (const ch of f.teamChannels.values()) ch.leave();
    }
    this.followed.clear();
    this.user?.leave();
    this.user = null;
    this.socket?.disconnect();
    this.socket = null;
    useRealtime.setState({ status: "idle" });
  }

  setActiveTicket(id: string | null) {
    this.active = id;
    useRealtime.setState({ active: id });
    this.reconcileTickets();
  }

  /** Tickets whose presence to follow, per source; waiting chats come first when over the cap. */
  setWatched(ids: string[], source = "list") {
    this.watchSources.set(source, ids);
    const merged = [...(this.watchSources.get("waiting") ?? []), ...[...this.watchSources].filter(([k]) => k !== "waiting").flatMap(([, v]) => v)];
    this.watched = new Set(merged.slice(0, MAX_WATCHED));
    this.reconcileTickets();
  }

  private activeEntry() {
    return this.active ? this.tickets.get(`${this.focus}:${this.active}`) : undefined;
  }

  /** viewing | replying | idle for the open ticket. */
  setPresenceState(state: "viewing" | "replying" | "idle") {
    const e = this.activeEntry();
    if (e?.mode === "active") e.ch.push("presence", { state });
  }

  /**
   * Tells the other agents on the open ticket (the socket) and, on a chat with a
   * contact, its visitor (the API, which checks the ticket) that a reply is being written.
   */
  typing(contactId?: string | null) {
    if (!this.active) return;
    const now = Date.now();
    if (now - this.lastTyping < 2500) return;
    this.lastTyping = now;
    const e = this.activeEntry();
    if (e?.mode !== "active") return;
    e.ch.push("typing", {});
    if (contactId) void api.tickets.typing(e.id, { workspace: e.wid }).catch(() => {});
  }

  /** Presence follows the focus workspace's open and listed tickets. */
  private reconcileTickets() {
    if (!this.socket) return;
    const wid = this.focus;
    const desired = new Map<string, TicketMode>();
    if (this.covered.has(wid)) {
      for (const id of this.watched) desired.set(id, "watch");
      if (this.active) desired.set(this.active, "active");
    }
    for (const [key, e] of this.tickets) {
      if (e.wid !== wid || desired.get(e.id) !== e.mode || e.team !== this.privateTeamOf(e.wid, e.id)) {
        e.ch.leave();
        this.tickets.delete(key);
        clearPresence(e.id);
        if (e.wid !== wid || !desired.has(e.id)) clearLive(e.id);
      }
    }
    for (const [id, mode] of desired) {
      if (!this.tickets.has(`${wid}:${id}`)) this.joinTicket(wid, id, mode);
    }
  }

  private joinTicket(wid: string, id: string, mode: TicketMode) {
    if (!this.socket) return;
    const team = this.privateTeamOf(wid, id);
    const params: Record<string, unknown> = mode === "watch" ? { watch: true } : { state: "viewing" };
    if (team) params.team_id = team;
    const ch = this.socket.channel(`ticket:${wid}:${id}`, params);
    const entry: TicketEntry = { ch, mode, state: {}, team, wid, id };
    this.tickets.set(`${wid}:${id}`, entry);
    ch.on("presence_state", (p) => {
      entry.state = syncState(entry.state, p as PresenceState);
      setPresence(id, entry.state);
    });
    ch.on("presence_diff", (p) => {
      entry.state = syncDiff(entry.state, p as PresenceDiff);
      setPresence(id, entry.state);
    });
    if (mode === "active") {
      ch.on("typing", (p) => {
        const t = p as { user_id: string; name: string };
        if (t.user_id !== this.opts.userId) noteTyping(id, t);
      });
      // The widget agent writing a reply: a preview only, replaced by message.created.
      ch.on("live", (p) => {
        const l = p as { type?: string; data?: Record<string, unknown> };
        if (typeof l?.type !== "string" || !l.data || typeof l.data !== "object") return;
        const tid = typeof l.data.ticket_id === "string" ? l.data.ticket_id : id;
        if (tid !== id) return;
        applyLive(id, l.type, l.data);
      });
      // contact_id comes from the visitor's token, so only the ticket's own contact counts.
      ch.on("customer_typing", (p) => {
        const contactId = (p as { contact_id?: string }).contact_id;
        const ticket = inWorkspace(wid, () => this.opts.qc.getQueryData<Ticket>(qk.ticket(id)));
        if (contactId && ticket?.contact?.id === contactId) noteCustomerTyping(id);
      });
    }
    ch.join().receive("ok", () => {
      if (mode === "active") ch.push("presence", { state: "viewing" });
    });
  }
}

let current: RealtimeManager | null = null;

export function startRealtime(opts: ManagerOptions) {
  current?.stop();
  current = new RealtimeManager(opts);
  void current.start();
  return current;
}

export function stopRealtime() {
  current?.stop();
  current = null;
}

export function realtime(): RealtimeManager | null {
  return current;
}
