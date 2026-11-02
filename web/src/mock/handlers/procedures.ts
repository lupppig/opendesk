// The agent's procedures (contracts/agent.md "Procedures"): admin-only CRUD with
// the server's limits, and the procedure a playground or test answer follows,
// picked by matching the visitor's words against each procedure's "when" line.

import type { Procedure, ProcedureChannel, ProcedureRef } from "@/lib/api/types";
import { db, userRef, type WsData } from "../db";
import { botsOf } from "../bots";
import { publish } from "../gateway";
import { HttpError, del, get, invalid, notFound, patch, post, respond, type Ctx } from "../router";
import { DAY, ago, isRecord, nowIso, uuid } from "../util";
import { requireAiAdmin, terms } from "./ai";

export const MAX_PROCEDURES = 50;
const NAME_MAX = 80;
const WHEN_MIN = 3;
const WHEN_MAX = 300;
const INSTRUCTIONS_MAX = 8000;
const CHANNELS: ProcedureChannel[] = ["chat", "email"];

const lists = new WeakMap<WsData, Procedure[]>();

export function proceduresOf(ws: WsData): Procedure[] {
  let l = lists.get(ws);
  if (!l) {
    l = ws === db.workspaces[0] ? seed() : [];
    lists.set(ws, l);
  }
  return l;
}

function seed(): Procedure[] {
  const by = userRef(db.meId);
  const make = (name: string, when: string, instructions: string, days: number, extra: Partial<Procedure> = {}): Procedure => ({
    id: uuid(),
    name,
    when,
    instructions,
    enabled: true,
    channels: ["chat", "email"],
    bot_ids: [],
    created_at: ago(days * DAY),
    updated_at: ago(days * DAY),
    updated_by: by,
    ...extra,
  });
  return [
    make(
      "Refund requests",
      "The customer asks for a refund, or says they were charged twice",
      "1. Ask for the order number if they did not give it.\n2. Look the order up.\n3. Refunds within 30 days of purchase are fine: say the team will process it within 2 business days.\n4. Older orders, or more than $200: hand off to a person. Never promise a refund yourself.",
      12,
    ),
    make(
      "Report a bug",
      "The customer reports something broken in the app, such as an error message or a page that does not load",
      "Collect what they did, what they expected and what happened instead, with the browser and device. Thank them, say the team will look into it, and hand off.",
      5,
      { channels: ["chat"] },
    ),
    make(
      "Student discount",
      "A student or teacher asks about education pricing",
      "Students and teachers get 50% off the Pro plan with a school email. Point them to the education page and ask them to sign up with their school address.",
      30,
      { enabled: false },
    ),
  ];
}

// ─── Validation ────────────────────────────────────────────────────────────

function cleanName(v: unknown): string {
  const s = typeof v === "string" ? v.replace(/[\p{Cc}\p{Cf}]/gu, " ").replace(/\s+/g, " ").trim() : "";
  if (!s || [...s].length > NAME_MAX) throw invalid("name must be 1 to 80 characters");
  return s;
}

function cleanWhen(v: unknown): string {
  const s = typeof v === "string" ? v.replace(/[\s\p{Cc}\p{Cf}]+/gu, " ").trim() : "";
  const n = [...s].length;
  if (n < WHEN_MIN || n > WHEN_MAX) throw invalid("when must be 3 to 300 characters");
  return s;
}

function cleanInstructions(v: unknown): string {
  const s = typeof v === "string" ? v.replace(/\r\n/g, "\n").trim() : "";
  if (!s || [...s].length > INSTRUCTIONS_MAX) throw invalid("instructions must be 1 to 8000 characters");
  return s;
}

function cleanChannels(v: unknown): ProcedureChannel[] {
  if (!Array.isArray(v) || v.some((c) => !CHANNELS.includes(c as ProcedureChannel))) throw invalid("channels may be chat and email");
  return CHANNELS.filter((c) => v.includes(c));
}

function cleanBots(ws: WsData, v: unknown): string[] {
  if (!Array.isArray(v) || v.some((x) => typeof x !== "string")) throw invalid("bot_ids must be bots of this workspace");
  const ids = [...new Set(v as string[])];
  const known = new Set(botsOf(ws).map((b) => b.id));
  if (ids.some((id) => !known.has(id))) throw invalid("bot_ids must be bots of this workspace");
  return ids;
}

function audit(ws: WsData, action: string, p: Procedure, data: Record<string, unknown> = {}) {
  ws.audit.unshift({
    id: uuid(),
    actor: { type: "user", user: userRef(db.meId), api_key_id: null },
    action,
    entity_type: "ai_procedure",
    entity_id: p.id,
    data: { name: p.name, ...data },
    ip: "[IP]",
    created_at: nowIso(),
  });
}

function find(ctx: Ctx): Procedure {
  const p = proceduresOf(ctx.ws).find((x) => x.id === ctx.params.id);
  if (!p) throw notFound("Procedure");
  return p;
}

// ─── Answering ─────────────────────────────────────────────────────────────

/** The procedures a bot may pick in a channel: enabled, newest-updated first, at most 30. */
export function listedProcedures(ws: WsData, channel: ProcedureChannel, botId: string | null): Procedure[] {
  return proceduresOf(ws)
    .filter((p) => p.enabled && p.channels.includes(channel) && (!p.bot_ids.length || (botId !== null && p.bot_ids.includes(botId))))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    .slice(0, 30);
}

/** The listed procedure whose "when" line shares the most words with the message, if any shares two. */
export function followedProcedures(ws: WsData, text: string, botId: string | null): ProcedureRef[] {
  const words = new Set(terms(text));
  let best: { p: Procedure; hits: number } | null = null;
  for (const p of listedProcedures(ws, "chat", botId)) {
    const hits = new Set(terms(p.when).filter((w) => words.has(w))).size;
    if (hits >= 2 && (!best || hits > best.hits)) best = { p, hits };
  }
  return best ? [{ id: best.p.id, name: best.p.name }] : [];
}

/** Procedures that limit themselves to the bot, by name (bot deletion refuses while any does). */
export function proceduresUsingBot(ws: WsData, botId: string): string[] {
  return proceduresOf(ws)
    .filter((p) => p.bot_ids.includes(botId))
    .map((p) => p.name);
}

// ─── Routes ────────────────────────────────────────────────────────────────

export function registerProcedures() {
  get("/v1/ai/procedures", (ctx) => {
    requireAiAdmin(ctx.ws, "Only admins can see the agent's procedures");
    return { data: [...proceduresOf(ctx.ws)].sort((a, b) => a.created_at.localeCompare(b.created_at)) };
  });

  get("/v1/ai/procedures/:id", (ctx) => {
    requireAiAdmin(ctx.ws, "Only admins can see the agent's procedures");
    return find(ctx);
  });

  post("/v1/ai/procedures", (ctx) => {
    requireAiAdmin(ctx.ws, "Only admins can change the agent's procedures");
    const b = ctx.body;
    const list = proceduresOf(ctx.ws);
    const now = nowIso();
    const p: Procedure = {
      id: uuid(),
      name: cleanName(b.name),
      when: cleanWhen(b.when),
      instructions: cleanInstructions(b.instructions),
      enabled: b.enabled === undefined ? true : b.enabled === true,
      channels: b.channels === undefined ? ["chat", "email"] : cleanChannels(b.channels),
      bot_ids: b.bot_ids === undefined ? [] : cleanBots(ctx.ws, b.bot_ids),
      created_at: now,
      updated_at: now,
      updated_by: userRef(db.meId),
    };
    if (list.length >= MAX_PROCEDURES) throw new HttpError(422, "limit_reached", "A workspace can have at most 50 procedures.");
    list.push(p);
    audit(ctx.ws, "ai.procedure_created", p);
    publish(ctx.ws, "ai_procedure.created", { procedure: p });
    return respond(201, p);
  });

  patch("/v1/ai/procedures/:id", (ctx) => {
    requireAiAdmin(ctx.ws, "Only admins can change the agent's procedures");
    const p = find(ctx);
    const b = ctx.body;
    if (!isRecord(b)) throw invalid("Send the fields to change");
    const next: Procedure = { ...p };
    if ("name" in b) next.name = cleanName(b.name);
    if ("when" in b) next.when = cleanWhen(b.when);
    if ("instructions" in b) next.instructions = cleanInstructions(b.instructions);
    if ("enabled" in b) {
      if (typeof b.enabled !== "boolean") throw invalid("enabled must be true or false");
      next.enabled = b.enabled;
    }
    if ("channels" in b) next.channels = cleanChannels(b.channels);
    if ("bot_ids" in b) next.bot_ids = cleanBots(ctx.ws, b.bot_ids);
    const changes = (["name", "when", "instructions", "enabled", "channels", "bot_ids"] as const).filter((k) => JSON.stringify(next[k]) !== JSON.stringify(p[k]));
    if (!changes.length) return p;
    Object.assign(p, next, { updated_at: nowIso(), updated_by: userRef(db.meId) });
    audit(ctx.ws, "ai.procedure_updated", p, { changes });
    publish(ctx.ws, "ai_procedure.updated", { procedure: p });
    return p;
  });

  del("/v1/ai/procedures/:id", (ctx) => {
    requireAiAdmin(ctx.ws, "Only admins can change the agent's procedures");
    const p = find(ctx);
    lists.set(
      ctx.ws,
      proceduresOf(ctx.ws).filter((x) => x !== p),
    );
    audit(ctx.ws, "ai.procedure_deleted", p);
    publish(ctx.ws, "ai_procedure.deleted", { procedure_id: p.id });
    return respond(204);
  });
}
