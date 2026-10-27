// Inbox categories in mock mode (contracts/collab.md "Categories"): the workspace's default and
// teams' sets, how an account's own choice resolves against them, how new mail is sorted, and
// Ollie's suggestions. Accounts and conversations live in handlers/collab.ts, which registers
// the account routes and calls `resolveCategories` and `sortMail`.

import type { MailAccountCategories, MailCategory, MailCategoryRule, MailCategorySet, MailCategorySettings, MailColor } from "@/lib/api/types";
import { MAX_CATEGORIES, MIN_CATEGORIES, PRIMARY, slugKey } from "@/lib/collab/categories";
import { LABEL_TONES } from "@/lib/collab/labelTones";
import { db, userRef, type WsData } from "../db";
import { del, get, HttpError, invalid, notFound, post, put } from "../router";
import { isRecord, nowIso, uuid } from "../util";

/** The built-in categories, as the server has them. */
export const PRESETS: MailCategory[] = [
  { key: PRIMARY, name: "Primary", color: "gray", description: "Mail from people that needs you: replies, questions, requests and conversations." },
  { key: "updates", name: "Updates", color: "blue", description: "Automatic notifications: receipts, orders, shipping, invoices, statements, account and security alerts, calendar and app notifications." },
  { key: "newsletters", name: "Newsletters", color: "violet", description: "Newsletters, digests and articles from lists you subscribed to." },
  { key: "promotions", name: "Promotions", color: "amber", description: "Marketing, offers, sales, discounts, event invitations from brands and cold sales outreach." },
  { key: "social", name: "Social", color: "teal", description: "Notifications from social networks, communities and forums." },
];

/** An account's own choice, kept on the mock account; `byAdmin`: an admin made it, so a lock leaves it. */
export type AccountChoice = { mode: MailAccountCategories["mode"]; own: MailCategory[]; ai: boolean; byAdmin: boolean };
export const defaultChoice = (): AccountChoice => ({ mode: "follow", own: [], ai: true, byAdmin: false });

type WorkspaceSet = MailCategorySet & { locked: boolean };
type TeamSet = MailCategorySet & { team_id: string };
type State = { workspace: WorkspaceSet | null; teams: TeamSet[] };

const states = new Map<string, State>();
const set = (enabled: boolean, categories: MailCategory[]): MailCategorySet => ({ enabled, categories: enabled ? categories : [], updated_at: nowIso(), updated_by: null });

function state(ws: WsData): State {
  let s = states.get(ws.workspace.id);
  if (!s) {
    // A demo default, like Gmail's tabs; ?mock_categories=none starts with nothing, to show the set-up.
    const none = new URLSearchParams(location.search).get("mock_categories") === "none";
    const gmail = PRESETS.filter((p) => p.key !== "social").map((p) => ({ ...p }));
    s = { workspace: none ? null : { ...set(true, gmail), updated_at: new Date(Date.now() - 9 * 86_400_000).toISOString(), updated_by: userRef(ws.members.find((m) => m.role === "owner")?.user_id), locked: false }, teams: [] };
    states.set(ws.workspace.id, s);
  }
  return s;
}

const roleOf = (ws: WsData) => ws.members.find((m) => m.user_id === db.meId)?.role ?? "viewer";
const isAdmin = (ws: WsData) => ["owner", "admin"].includes(roleOf(ws));
const requireAdmin = (ws: WsData) => {
  if (!isAdmin(ws)) throw new HttpError(403, "forbidden", "Only admins can change the workspace's categories.");
};
const inTeam = (ws: WsData, teamId: string, uid: string | null) => !!uid && !!ws.teams.find((t) => t.id === teamId)?.member_ids?.includes(uid);

/** What an account's inbox shows (contracts/collab.md "Who decides"), for the caller. */
export function resolveCategories(ws: WsData, choice: AccountChoice, ownerId: string | null): MailAccountCategories {
  const s = state(ws);
  const locked = !!s.workspace?.locked;
  const base = { mode: choice.mode, own: choice.own, ai: choice.ai, locked: locked && !isAdmin(ws) };
  if (choice.mode !== "follow" && (!locked || choice.byAdmin)) {
    const on = choice.mode === "custom" && choice.own.length >= MIN_CATEGORIES;
    return { ...base, enabled: on, categories: on ? choice.own : [], source: "account", team: null };
  }
  const team = s.teams.find((t) => inTeam(ws, t.team_id, ownerId));
  if (team) return { ...base, enabled: team.enabled, categories: team.categories, source: "team", team: { id: team.team_id, name: ws.teams.find((x) => x.id === team.team_id)?.name ?? "" } };
  if (s.workspace) return { ...base, enabled: s.workspace.enabled, categories: s.workspace.categories, source: "workspace", team: null };
  return { ...base, enabled: false, categories: [], source: "none", team: null };
}

/** A set as the server checks it: 2..8, Primary first, keys and names once each. */
export function parseCategories(v: unknown): MailCategory[] {
  if (!Array.isArray(v) || v.length < MIN_CATEGORIES || v.length > MAX_CATEGORIES) throw invalid(`categories must hold ${MIN_CATEGORIES} to ${MAX_CATEGORIES} categories`);
  const keys = new Set<string>();
  const names = new Set<string>();
  return v.map((raw, i) => {
    if (!isRecord(raw)) throw invalid("each category must be an object");
    const name = String(raw.name ?? "").trim();
    if (!name || name.length > 40) throw invalid("each category needs a name of 1 to 40 characters");
    let key = i === 0 ? PRIMARY : String(raw.key ?? "").trim() || slugKey(name) || "category";
    if (i > 0 && key === PRIMARY) throw invalid("primary must be the first category");
    for (let n = 2, k = key; keys.has(key); n++) key = `${k}-${n}`;
    if (names.has(name.toLowerCase())) throw invalid(`two categories are named ${name}`);
    keys.add(key);
    names.add(name.toLowerCase());
    const color = (LABEL_TONES as readonly string[]).includes(String(raw.color)) ? (raw.color as MailColor) : "gray";
    return { key, name, description: String(raw.description ?? "").trim().slice(0, 300), color };
  });
}

/** What a sender rule names: an address, or "@domain". */
export function parseSender(v: unknown): string {
  const s = String(v ?? "").trim().toLowerCase();
  if (!/^(@[^\s@]+\.[^\s@]+|[^\s@]+@[^\s@]+\.[^\s@]+)$/.test(s)) throw invalid("sender must be an address or @ and a domain");
  return s;
}

export const newRule = (sender: string, category: string): MailCategoryRule => ({ id: uuid(), sender, category, created_at: nowIso() });

/** The mail a conversation is sorted by: its first message from outside the account. */
export type SortInput = { from: string; name: string; subject: string; text: string };

/**
 * Sorting new mail, in the contract's order: sender rules, then (for the mock, standing in for
 * Ollie and the headers) the sender and subject. Only keys the account has; else Primary.
 */
export function sortMail(m: SortInput, categories: MailCategory[], rules: MailCategoryRule[]): string {
  const keys = new Set(categories.map((c) => c.key));
  const from = m.from.toLowerCase();
  const domain = from.slice(from.indexOf("@"));
  const rule = rules.find((r) => r.sender === from) ?? rules.find((r) => r.sender === domain);
  if (rule && keys.has(rule.category)) return rule.category;
  const hay = `${m.name} ${from} ${m.subject}`.toLowerCase();
  const pick = (...ks: string[]) => ks.find((k) => keys.has(k));
  if (/web drop|digest|newsletter|weekly|webdrop/.test(hay)) return pick("newsletters", "promotions", "updates") ?? PRIMARY;
  if (/% off|offer|sale\b|deal|discount/.test(hay)) return pick("promotions", "newsletters") ?? PRIMARY;
  if (/linkedin|twitter|facebook|instagram/.test(hay)) return pick("social", "updates") ?? PRIMARY;
  if (/no-?reply|notifications?@|billing@|receipt|invoice|pull request|github|stripe|linear|paypal|\[[^\]]+\]/.test(hay)) return pick("updates") ?? PRIMARY;
  // A category of one's own whose name the subject carries, as Ollie would read it.
  const named = categories.find((c) => c.key !== PRIMARY && !PRESETS.some((p) => p.key === c.key) && hay.includes(c.name.toLowerCase()));
  return named?.key ?? PRIMARY;
}

function settingsOut(ws: WsData): MailCategorySettings {
  const s = state(ws);
  const admin = isAdmin(ws);
  return {
    workspace: s.workspace,
    teams: s.teams
      .map((t, position) => ({ ...t, team_name: ws.teams.find((x) => x.id === t.team_id)?.name ?? "", position }))
      .filter((t) => admin || inTeam(ws, t.team_id, db.meId)),
    presets: PRESETS,
    sorting: ws.ai.enabled ? "ai" : "rules",
    suggest: ws.ai.enabled,
  };
}

/** Ollie's proposal: Primary, the built-ins the words call for, and a few of the inbox's own. */
function suggestion(about: string): MailCategory[] {
  const a = about.toLowerCase();
  const out: MailCategory[] = [PRESETS[0]];
  if (/sale|client|customer|deal|lead/.test(a)) {
    out.push({ key: "", name: "Clients", color: "green", description: "Mail from customers and their teams about work in progress, renewals and contracts." });
    out.push({ key: "", name: "Leads", color: "orange", description: "First messages from people and companies who are not customers yet." });
  } else if (/hir|recruit|job|candidate/.test(a)) {
    out.push({ key: "", name: "Candidates", color: "green", description: "Applications, replies from candidates and interview scheduling." });
  } else {
    out.push({ key: "", name: "Team", color: "indigo", description: "Mail from colleagues and partners you work with every week." });
  }
  out.push(PRESETS[1], PRESETS[2]);
  if (/invoice|bill|finance|account/.test(a)) out.push({ key: "", name: "Finance", color: "teal", description: "Invoices, receipts, payouts and statements." });
  else out.push(PRESETS[3]);
  return out;
}

/** The workspace and team routes; `changed` sorts the accounts again and tells whoever sees them. */
export function registerCollabCategories(deps: { requireCollab: (ws: WsData) => void; changed: (ws: WsData) => void }) {
  get("/v1/collab/categories", (ctx) => {
    deps.requireCollab(ctx.ws);
    return settingsOut(ctx.ws);
  });

  put("/v1/collab/categories/workspace", (ctx) => {
    deps.requireCollab(ctx.ws);
    requireAdmin(ctx.ws);
    const s = state(ctx.ws);
    const enabled = ctx.body.enabled === true;
    const categories = enabled ? parseCategories(ctx.body.categories) : [];
    const locked = typeof ctx.body.locked === "boolean" ? ctx.body.locked : (s.workspace?.locked ?? false);
    s.workspace = { ...set(enabled, categories), updated_by: userRef(db.meId), locked };
    deps.changed(ctx.ws);
    return settingsOut(ctx.ws);
  });

  del("/v1/collab/categories/workspace", (ctx) => {
    deps.requireCollab(ctx.ws);
    requireAdmin(ctx.ws);
    state(ctx.ws).workspace = null;
    deps.changed(ctx.ws);
    return settingsOut(ctx.ws);
  });

  put("/v1/collab/categories/teams/:teamId", (ctx) => {
    deps.requireCollab(ctx.ws);
    requireAdmin(ctx.ws);
    if (!ctx.ws.teams.some((t) => t.id === ctx.params.teamId)) throw notFound("Team");
    const s = state(ctx.ws);
    const enabled = ctx.body.enabled === true;
    const next: TeamSet = { ...set(enabled, enabled ? parseCategories(ctx.body.categories) : []), updated_by: userRef(db.meId), team_id: ctx.params.teamId };
    const i = s.teams.findIndex((t) => t.team_id === ctx.params.teamId);
    // A new team set goes last in the order.
    if (i >= 0) s.teams[i] = next;
    else s.teams.push(next);
    deps.changed(ctx.ws);
    return settingsOut(ctx.ws);
  });

  del("/v1/collab/categories/teams/:teamId", (ctx) => {
    deps.requireCollab(ctx.ws);
    requireAdmin(ctx.ws);
    const s = state(ctx.ws);
    s.teams = s.teams.filter((t) => t.team_id !== ctx.params.teamId);
    deps.changed(ctx.ws);
    return settingsOut(ctx.ws);
  });

  put("/v1/collab/categories/team-order", (ctx) => {
    deps.requireCollab(ctx.ws);
    requireAdmin(ctx.ws);
    const s = state(ctx.ws);
    const ids = Array.isArray(ctx.body.team_ids) ? ctx.body.team_ids.map(String) : [];
    if (ids.length !== s.teams.length || new Set(ids).size !== ids.length || !ids.every((id) => s.teams.some((t) => t.team_id === id))) throw invalid("team_ids must list every team with categories once");
    s.teams = ids.map((id) => s.teams.find((t) => t.team_id === id)!);
    deps.changed(ctx.ws);
    return settingsOut(ctx.ws);
  });

  post("/v1/collab/categories/suggest", async (ctx) => {
    deps.requireCollab(ctx.ws);
    if (roleOf(ctx.ws) === "viewer") throw new HttpError(403, "forbidden", "Viewers can read but not act.");
    if (!ctx.ws.ai.enabled) throw new HttpError(404, "ai_disabled", "AI is not on for this workspace.");
    const about = String(ctx.body.about ?? "");
    if (about.length > 500) throw invalid("about must be 500 characters or less");
    // Ollie takes a moment to read the inbox.
    await new Promise((r) => setTimeout(r, 700));
    return { categories: suggestion(about).map((c) => ({ ...c })) };
  });
}
