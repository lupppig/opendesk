// Agent-to-agent in mock mode (contracts/ai.md "Asking a teammate"): each member's own
// sharing settings, the log of asks made to them, and the check Ollie's ask_teammate runs
// (handlers/assistant.ts). Seeded per workspace: Priya answers everyone in summaries; Maya
// answers chosen people and a team, and has a few asks in her log.

import type { PeerAsk, PeerMode, PeerResult, PeerSettings } from "@/lib/api/types";
import { PEER_MODES, PEER_RULES_MAX, peerAccounts } from "@/lib/assistantPeer";
import { db, userRef, type WsData } from "../db";
import { get, HttpError, invalid, paginate, put, strArr, type Ctx } from "../router";
import { isRecord, nowIso, uuid } from "../util";
import { aiOff } from "./ai";
import { collabOn, ownMailAccounts } from "./collab";

type Stored = {
  enabled: boolean;
  share_with: "everyone" | "chosen";
  user_ids: string[];
  team_ids: string[];
  all_accounts: boolean;
  default_mode: PeerMode;
  choices: Record<string, PeerMode>;
  rules: string;
  updated_at: string | null;
};
type Ask = PeerAsk & { targetId: string };
type State = { settings: Record<string, Stored>; asks: Ask[] };

const states = new Map<string, State>();
const HOUR = 3_600_000;

const blank = (): Stored => ({ enabled: false, share_with: "everyone", user_ids: [], team_ids: [], all_accounts: true, default_mode: "summary", choices: {}, rules: "", updated_at: null });

const byEmail = (ws: WsData, email: string) => {
  const u = db.users.find((x) => x.email === email);
  return u && ws.members.some((m) => m.user_id === u.id) ? u : undefined;
};

function seed(ws: WsData): State {
  const st: State = { settings: {}, asks: [] };
  const priya = byEmail(ws, "priya@margin.app");
  if (priya) st.settings[priya.id] = { ...blank(), enabled: true, rules: "Share renewal dates and what customers asked for. Keep prices and anything personal to me.", updated_at: nowIso() };
  const me = db.meId;
  if (!ws.members.some((m) => m.user_id === me)) return st;
  const own = ownMailAccounts(ws, me);
  const work = own.find((a) => a.provider === "google");
  const personal = own.find((a) => a.provider === "imap");
  const team = ws.teams.find((t) => t.member_ids?.length);
  st.settings[me] = {
    ...blank(),
    enabled: true,
    share_with: "chosen",
    user_ids: priya && priya.id !== me ? [priya.id] : [],
    team_ids: team ? [team.id] : [],
    choices: { ...(work ? { [work.id]: "verbatim" as const } : {}), ...(personal ? { [personal.id]: "none" as const } : {}) },
    rules: "Share dates, decisions and next steps from customer threads. Never share prices, contract terms or anything from my personal account.",
    updated_at: new Date(Date.now() - 3 * 24 * HOUR).toISOString(),
  };
  const at = (h: number) => new Date(Date.now() - h * HOUR).toISOString();
  const log: [string, string, string, PeerResult, number, number][] = [
    ["jonas@margin.app", "When is the Northwind security review?", "Maya's mail says the Northwind security review is on Thursday at 2pm, with Tom Becker from their compliance team. She still owes them the filled-in questionnaire.", "answered", 2, 2],
    ["sam@margin.app", "What discount did Maya offer Halcyon for the renewal?", "Maya's sharing rules keep prices private, so I can't share that. Ask Maya directly.", "declined", 1, 26],
    ["priya@margin.app", "Has Acme confirmed the seat count for next year?", "Yes. Elena from Acme asked for a quote for 260 seats with the annual discount, and Maya said she would send it this week.", "answered", 1, 75],
  ];
  for (const [email, question, answer, result, sources, hours] of log) {
    const a = userRef(byEmail(ws, email)?.id);
    if (a && a.id !== me) st.asks.push({ id: uuid(), targetId: me, asker: a, question, answer, result, sources, created_at: at(hours) });
  }
  return st;
}

function state(ws: WsData): State {
  let s = states.get(ws.workspace.id);
  if (!s) {
    s = seed(ws);
    states.set(ws.workspace.id, s);
  }
  return s;
}

/** The workspace allows agent-to-agent: AI, Ollie and the admin's switch on, and Collab. */
function available(ws: WsData): boolean {
  return !aiOff() && ws.ai.enabled && ws.ai.assistant !== false && ws.ai.assistant_peer === true && collabOn(ws);
}

function view(ws: WsData, uid: string): PeerSettings {
  const s = state(ws).settings[uid] ?? blank();
  const users = s.user_ids
    .map((id) => userRef(id))
    .filter((u) => !!u)
    .map((u) => ({ id: u.id, name: u.name, email: u.email, avatar_url: u.avatar_url }));
  const teams = s.team_ids
    .map((id) => ws.teams.find((t) => t.id === id))
    .filter((t) => !!t)
    .map((t) => ({ id: t.id, name: t.name }));
  return {
    available: available(ws),
    enabled: s.enabled,
    share_with: s.share_with,
    audience: { users, teams },
    all_accounts: s.all_accounts,
    default_mode: s.default_mode,
    accounts: peerAccounts(ownMailAccounts(ws, uid), s.all_accounts, s.default_mode, s.choices),
    rules: s.rules,
    updated_at: s.updated_at,
  };
}

function requireCollab(ws: WsData) {
  if (!collabOn(ws)) throw new HttpError(402, "collab_required", "Collab is not on this workspace's plan.");
}

const isMode = (v: unknown): v is PeerMode => typeof v === "string" && (PEER_MODES as string[]).includes(v);

function parse(ws: WsData, uid: string, b: Record<string, unknown>): Stored {
  if (typeof b.enabled !== "boolean") throw invalid("enabled must be true or false");
  if (b.share_with !== "everyone" && b.share_with !== "chosen") throw invalid("share_with must be everyone or chosen");
  if (typeof b.all_accounts !== "boolean") throw invalid("all_accounts must be true or false");
  if (!isMode(b.default_mode)) throw invalid("default_mode must be verbatim, summary or none");
  const aud = isRecord(b.audience) ? b.audience : {};
  const userIds = [...new Set(strArr(aud.user_ids) ?? [])];
  const teamIds = [...new Set(strArr(aud.team_ids) ?? [])];
  if (userIds.some((id) => id === uid || !ws.members.some((m) => m.user_id === id))) throw invalid("audience.user_ids must be other members of this workspace");
  if (teamIds.some((id) => !ws.teams.some((t) => t.id === id))) throw invalid("audience.team_ids must be teams of this workspace");
  const own = new Set(ownMailAccounts(ws, uid).map((a) => a.id));
  const choices: Record<string, PeerMode> = {};
  for (const a of Array.isArray(b.accounts) ? b.accounts : []) {
    if (!isRecord(a) || typeof a.id !== "string" || !own.has(a.id)) throw invalid("accounts must be your own mail accounts");
    if (!isMode(a.mode)) throw invalid("each account's mode must be verbatim, summary or none");
    choices[a.id] = a.mode;
  }
  const rules = typeof b.rules === "string" ? b.rules.trim() : "";
  if (rules.length > PEER_RULES_MAX) throw invalid("rules must be 2000 characters or less");
  if (b.enabled && !available(ws)) throw new HttpError(409, "assistant_peer_off", "Your workspace has not allowed agent-to-agent. An admin can allow it in Settings, AI.");
  return { enabled: b.enabled, share_with: b.share_with, user_ids: userIds, team_ids: teamIds, all_accounts: b.all_accounts, default_mode: b.default_mode, choices, rules, updated_at: nowIso() };
}

function asksFor(ws: WsData, uid: string): PeerAsk[] {
  return state(ws)
    .asks.filter((a) => a.targetId === uid)
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
    .map(({ id, asker, question, answer, result, sources, created_at }) => ({ id, asker, question, answer, result, sources, created_at }));
}

export type PeerAnswer = { result: PeerResult; sources: number; answer: string };

/**
 * What ask_teammate gets from a teammate's Ollie: nothing unless the workspace allows it, the
 * teammate opted in, the asker is in their audience and an account shares something. Every
 * ask lands in the teammate's log.
 */
export function askTeammate(ws: WsData, askerId: string, targetId: string, question: string, answer: () => { text: string; sources: number }): PeerAnswer {
  const s = state(ws).settings[targetId];
  const shared = !!s && peerAccounts(ownMailAccounts(ws, targetId), s.all_accounts, s.default_mode, s.choices).some((a) => a.mode !== "none");
  const inTeam = (tid: string) => !!ws.teams.find((t) => t.id === tid)?.member_ids?.includes(askerId);
  const audience = !!s && (s.share_with === "everyone" || s.user_ids.includes(askerId) || s.team_ids.some(inTeam));
  let out: PeerAnswer = { result: "not_shared", sources: 0, answer: "" };
  if (available(ws) && s?.enabled && audience && shared) {
    const a = answer();
    out = { result: "answered", sources: a.sources, answer: a.text };
  }
  const asker = userRef(askerId);
  if (asker) state(ws).asks.push({ id: uuid(), targetId, asker, question, answer: out.answer, result: out.result, sources: out.sources, created_at: nowIso() });
  return out;
}

/** Whether Ollie is offered ask_teammate: the workspace allows agent-to-agent. */
export function peerAvailable(ws: WsData): boolean {
  return available(ws);
}

export function registerAssistantPeer() {
  get("/v1/me/assistant-peer", (ctx: Ctx) => {
    requireCollab(ctx.ws);
    return view(ctx.ws, db.meId);
  });
  put("/v1/me/assistant-peer", (ctx: Ctx) => {
    requireCollab(ctx.ws);
    if (!isRecord(ctx.body)) throw invalid("Body must be an object");
    state(ctx.ws).settings[db.meId] = parse(ctx.ws, db.meId, ctx.body);
    return view(ctx.ws, db.meId);
  });
  get("/v1/me/assistant-peer/asks", (ctx: Ctx) => {
    requireCollab(ctx.ws);
    return paginate(ctx, asksFor(ctx.ws, db.meId), 20);
  });
}
