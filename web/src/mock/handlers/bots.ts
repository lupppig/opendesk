// Bots (contracts/bots.md): members read, admins write. Validation mirrors the API.

import type { Bot, BotPersonality } from "@/lib/api/types";
import { lookProblem, readLook, type BotLook } from "@/lib/botlook";
import { MAX_BOTS, botOut, botsOf, defaultBot, newBot, sortedBots } from "../bots";
import { db, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, forbidden, get, invalid, notFound, patch, post, put, respond, type Ctx } from "../router";
import { isAdmin } from "../teams";
import { hasControl, isRecord, nowIso, oneLine, uuid } from "../util";
import { readUpload } from "./pictures";
import { proceduresUsingBot } from "./procedures";

const TONES = ["friendly", "professional", "playful", "concise", "warm", "custom"] as const;
const LENGTHS = ["short", "medium", "detailed"] as const;
const EMOJI = ["none", "some", "lots"] as const;

function requireAdmin(ws: WsData) {
  if (!isAdmin(ws, db.meId)) throw forbidden("Only admins can change bots");
}

function audit(ws: WsData, action: string, id: string, data: Record<string, unknown> = {}) {
  ws.audit.unshift({
    id: uuid(),
    actor: { type: "user", user: userRef(db.meId), api_key_id: null },
    action,
    entity_type: "bot",
    entity_id: id,
    data,
    ip: "84.152.33.17",
    created_at: nowIso(),
  });
}

function plain(field: string, v: unknown, max: number, multiline = false): string {
  if (typeof v !== "string") throw invalid(`${field} must be text`);
  const s = multiline ? v.replace(/\r\n/g, "\n").trim() : oneLine(v);
  if (/[<>]/.test(s) || hasControl(s, multiline)) throw invalid(`${field} can't contain < or > or control characters`);
  if (s.length > max) throw invalid(`${field} must be ${max} characters or less`);
  return s;
}

function cleanName(v: unknown): string {
  const s = plain("name", v, 40);
  if (!s) throw invalid("name is required");
  return s;
}

function cleanLanguage(v: unknown): string {
  if (typeof v !== "string") throw invalid("language must be text");
  const s = v.trim();
  if (s && !/^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}$/.test(s)) throw invalid("language must be a BCP 47 tag like en, de or pt-BR, or empty");
  return s;
}

function cleanLook(v: unknown, current: Bot | null): BotLook {
  const problem = lookProblem(v);
  if (problem) throw invalid(problem);
  const look = readLook(v);
  if (look.kind === "image" && !current?.image_url) throw invalid("look.kind image needs an uploaded picture: PUT /v1/bots/{id}/image");
  return look;
}

/** Merges a partial personality, validating each key, like PATCH /v1/bots/{id}. */
function applyPersonality(p: BotPersonality, raw: unknown): BotPersonality {
  if (raw === undefined) return p;
  if (!isRecord(raw)) throw invalid("personality must be an object");
  const next = { ...p, blocked_topics: [...p.blocked_topics] };
  const pick = <T extends string>(key: string, list: readonly T[]): T | undefined => {
    const v = raw[key];
    if (v === undefined) return undefined;
    if (typeof v !== "string" || !list.includes(v as T)) throw invalid(`personality.${key} must be one of ${list.join(", ")}`);
    return v as T;
  };
  next.tone = pick("tone", TONES) ?? next.tone;
  next.reply_length = pick("reply_length", LENGTHS) ?? next.reply_length;
  next.emoji = pick("emoji", EMOJI) ?? next.emoji;
  if (raw.custom_tone !== undefined) next.custom_tone = plain("personality.custom_tone", raw.custom_tone, 200);
  if (raw.greeting !== undefined) next.greeting = plain("personality.greeting", raw.greeting, 300, true);
  if (raw.handoff_message !== undefined) next.handoff_message = plain("personality.handoff_message", raw.handoff_message, 300, true);
  if (raw.sign_off !== undefined) next.sign_off = plain("personality.sign_off", raw.sign_off, 200, true);
  if (raw.instructions !== undefined) {
    if (typeof raw.instructions !== "string") throw invalid("personality.instructions must be text");
    const s = raw.instructions.replace(/\r\n/g, "\n").trim();
    if (s.length > 4000) throw invalid("personality.instructions must be 4000 characters or less");
    if (hasControl(s, true)) throw invalid("personality.instructions contains characters that are not allowed");
    next.instructions = s;
  }
  if (raw.blocked_topics !== undefined) {
    if (!Array.isArray(raw.blocked_topics)) throw invalid("personality.blocked_topics must be a list");
    const seen = new Set<string>();
    const out: string[] = [];
    for (const t of raw.blocked_topics) {
      const s = plain("personality.blocked_topics", t, 100);
      if (s.length < 2) throw invalid("personality.blocked_topics must be 2 to 100 characters each");
      if (seen.has(s.toLowerCase())) continue;
      seen.add(s.toLowerCase());
      out.push(s);
    }
    if (out.length > 20) throw invalid("personality.blocked_topics can have at most 20 topics");
    next.blocked_topics = out;
  }
  if (next.tone === "custom" && !next.custom_tone) throw invalid("personality.custom_tone is required when the tone is custom");
  return next;
}

function find(ctx: Ctx): Bot {
  const b = botsOf(ctx.ws).find((x) => x.id === ctx.params.id);
  if (!b) throw notFound("Bot");
  return b;
}

function changed(ws: WsData, b: Bot, type: "bot.created" | "bot.updated") {
  publish(ws, type, { bot: botOut(ws, b) });
}

function room(ws: WsData) {
  if (botsOf(ws).length >= MAX_BOTS) throw new HttpError(422, "limit_reached", `A workspace can have at most ${MAX_BOTS} bots.`);
}

const list = (ws: WsData) => ({
  data: sortedBots(ws).map((b) => botOut(ws, b)),
  next_cursor: null,
});

export function registerBots() {
  get("/v1/bots", (ctx) => list(ctx.ws));
  get("/v1/bots/:id", (ctx) => botOut(ctx.ws, find(ctx)));

  post("/v1/bots", (ctx) => {
    requireAdmin(ctx.ws);
    room(ctx.ws);
    const b = newBot(cleanName(ctx.body.name), {
      position: Math.max(0, ...botsOf(ctx.ws).map((x) => x.position)) + 1,
    });
    if (ctx.body.role !== undefined) b.role = plain("role", ctx.body.role, 60);
    if (ctx.body.language !== undefined) b.language = cleanLanguage(ctx.body.language);
    if (ctx.body.look !== undefined) b.look = cleanLook(ctx.body.look, null);
    b.personality = applyPersonality(b.personality, ctx.body.personality);
    botsOf(ctx.ws).push(b);
    changed(ctx.ws, b, "bot.created");
    audit(ctx.ws, "bot.created", b.id, { name: b.name });
    return respond(201, botOut(ctx.ws, b));
  });

  patch("/v1/bots/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const b = find(ctx);
    const keys: string[] = [];
    if (ctx.body.name !== undefined) {
      const n = cleanName(ctx.body.name);
      if (n !== b.name) {
        b.name = n;
        keys.push("name");
      }
    }
    if (ctx.body.role !== undefined) {
      const r = plain("role", ctx.body.role, 60);
      if (r !== b.role) {
        b.role = r;
        keys.push("role");
      }
    }
    if (ctx.body.language !== undefined) {
      const l = cleanLanguage(ctx.body.language);
      if (l !== b.language) {
        b.language = l;
        keys.push("language");
      }
    }
    if (ctx.body.look !== undefined) {
      const l = cleanLook(ctx.body.look, b);
      if (JSON.stringify(l) !== JSON.stringify(b.look)) {
        b.look = l;
        keys.push("look");
      }
    }
    const p = applyPersonality(b.personality, ctx.body.personality);
    for (const k of Object.keys(p) as (keyof BotPersonality)[]) if (JSON.stringify(p[k]) !== JSON.stringify(b.personality[k])) keys.push(`personality.${k}`);
    b.personality = p;
    if (keys.length) {
      b.updated_at = nowIso();
      changed(ctx.ws, b, "bot.updated");
      audit(ctx.ws, "bot.updated", b.id, { changes: keys });
    }
    return botOut(ctx.ws, b);
  });

  del("/v1/bots/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const b = find(ctx);
    if (b.is_default) throw new HttpError(409, "bot_is_default", "The default bot cannot be deleted. Make another bot the default first.");
    const usage = botOut(ctx.ws, b).usage;
    const names = [...usage.widgets.map((w) => w.name), ...usage.mailboxes.map((m) => m.email), ...proceduresUsingBot(ctx.ws, b.id).map((n) => `the ${n} procedure`)];
    if (names.length) {
      const shown = names.slice(0, 3).join(", ");
      const more = names.length > 3 ? ` and ${names.length - 3} more` : "";
      throw new HttpError(409, "bot_in_use", `${b.name} still answers on ${shown}${more}. Pick another bot there first.`);
    }
    ctx.ws.bots = botsOf(ctx.ws).filter((x) => x !== b);
    publish(ctx.ws, "bot.deleted", { bot_id: b.id });
    audit(ctx.ws, "bot.deleted", b.id, { name: b.name });
    return respond(204);
  });

  post("/v1/bots/:id/duplicate", (ctx) => {
    requireAdmin(ctx.ws);
    room(ctx.ws);
    const src = find(ctx);
    const name = `${src.name.slice(0, 35)} copy`;
    const copy = newBot(name, {
      role: src.role,
      language: src.language,
      look: {
        ...src.look,
        fill: { ...src.look.fill },
        kind: src.look.kind === "image" ? "built" : src.look.kind,
      },
      personality: {
        ...src.personality,
        blocked_topics: [...src.personality.blocked_topics],
      },
      position: src.position + 1,
    });
    for (const x of botsOf(ctx.ws)) if (x.position > src.position) x.position++;
    botsOf(ctx.ws).push(copy);
    changed(ctx.ws, copy, "bot.created");
    audit(ctx.ws, "bot.duplicated", copy.id, { from: src.id });
    return respond(201, botOut(ctx.ws, copy));
  });

  post("/v1/bots/:id/default", (ctx) => {
    requireAdmin(ctx.ws);
    const b = find(ctx);
    const prev = defaultBot(ctx.ws);
    if (prev !== b) {
      prev.is_default = false;
      b.is_default = true;
      changed(ctx.ws, prev, "bot.updated");
      changed(ctx.ws, b, "bot.updated");
      audit(ctx.ws, "bot.default_changed", b.id, { previous: prev.id });
    }
    return botOut(ctx.ws, b);
  });

  post("/v1/bots/reorder", (ctx) => {
    requireAdmin(ctx.ws);
    const ids = ctx.body.ids;
    const all = botsOf(ctx.ws);
    if (!Array.isArray(ids) || ids.length !== all.length || new Set(ids).size !== ids.length || !ids.every((id) => all.some((b) => b.id === id)))
      throw invalid("ids must list every bot of the workspace once");
    ids.forEach((id, i) => {
      const b = all.find((x) => x.id === id)!;
      if (b.position !== i) {
        b.position = i;
        changed(ctx.ws, b, "bot.updated");
      }
    });
    audit(ctx.ws, "bot.reordered", ids[0] as string);
    return list(ctx.ws);
  });

  put("/v1/bots/:id/image", async (ctx) => {
    requireAdmin(ctx.ws);
    const b = find(ctx);
    b.image_url = await readUpload(ctx);
    b.look = { ...b.look, kind: "image" };
    b.updated_at = nowIso();
    changed(ctx.ws, b, "bot.updated");
    audit(ctx.ws, "bot.image_updated", b.id);
    return botOut(ctx.ws, b);
  });

  del("/v1/bots/:id/image", (ctx) => {
    requireAdmin(ctx.ws);
    const b = find(ctx);
    b.image_url = null;
    if (b.look.kind === "image") b.look = { ...b.look, kind: "built" };
    b.updated_at = nowIso();
    changed(ctx.ws, b, "bot.updated");
    audit(ctx.ws, "bot.image_removed", b.id);
    return botOut(ctx.ws, b);
  });
}

/** A bot_id for a widget or a mailbox: null, or a bot of this workspace. */
export function checkBotId(ws: WsData, v: unknown): string | null {
  if (v === null || v === "") return null;
  if (typeof v !== "string" || !botsOf(ws).some((b) => b.id === v)) throw invalid("bot_id must be a bot of this workspace");
  return v;
}
