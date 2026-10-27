// Mock routes for the workspace's AI provider (contracts/ai.md "Provider per
// workspace"): read, test, save and remove an own key, and the TypeSafe key for
// decisions. Keys are never kept, only their last 4 characters.

import type { AIDecisionsRoute, AIKeyKind, AIProvider, AIProviderKeyInput, AIProviderTest } from "@/lib/api/types";
import { aiKeyOf, decisionKeyOf, KEY_LABEL, OWN_DEFAULTS, OWN_MODELS, setAIKey, setDecisionKey, type MockAIKey } from "../aiKey";
import { db, userRef, type WsData } from "../db";
import { HttpError, del, get, invalid, post, put, respond, type Ctx } from "../router";
import { isRecord, nowIso, uuid } from "../util";
import { assertAiInstance, decisionEngine, requireAiAdmin } from "./ai";

const KINDS: AIKeyKind[] = ["openrouter", "openai", "anthropic"];

function view(ws: WsData): AIProvider {
  const k = aiKeyOf(ws);
  const key = k ? (({ failOnUse: _f, notified_at: _n, ...rest }) => rest)(k) : null;
  return { mode: k ? "own_key" : "instance", instance_provider: "openrouter", billing: ws.billing.billing_enabled, key, decisions: decisions(ws) };
}

/** Where decisions run: the TypeSafe key, else Jev on an OpenRouter key, else the instance's engine. */
function decisions(ws: WsData): AIDecisionsRoute {
  const key = decisionKeyOf(ws);
  if (key) return { runs_on: "typesafe_key", engine: "jev", key };
  if (aiKeyOf(ws)?.kind === "openrouter") return { runs_on: "provider_key", engine: "jev", key: null };
  const e = decisionEngine();
  if (e.provider === "none") return { runs_on: "none", engine: "none", key: null };
  return { runs_on: aiKeyOf(ws) && e.provider === "llm" ? "provider_key" : "instance", engine: e.provider === "llm" ? "llm" : "jev", key: null };
}

/** A TypeSafe key from the body; "bad" is rejected and "nofunds" is out of credits, like provider keys. */
function parseDecisionsKey(ctx: Ctx): string {
  const b = ctx.body;
  const secret = isRecord(b) && typeof b.api_key === "string" ? b.api_key.trim() : "";
  if (secret.length < 8 || secret.length > 500 || /[^\x21-\x7e]/.test(secret)) throw invalid("Enter the API key exactly as TypeSafe shows it.");
  return secret;
}

function testDecisionsKey(secret: string): AIProviderTest {
  const s = secret.toLowerCase();
  if (s.includes("bad")) return fail("invalid_key", "TypeSafe rejected this key. Check that it's correct and still active.");
  if (s.includes("nofunds")) return fail("no_funds", "The TypeSafe account behind this key is out of credits.");
  return { ok: true, models: [] };
}

/** Hosts a user-chosen endpoint may not point to (the server resolves and checks every address). */
function privateHost(host: string): boolean {
  const h = host.toLowerCase().replace(/^\[|\]$/g, "");
  return (
    h === "localhost" ||
    h.endsWith(".localhost") ||
    h.endsWith(".internal") ||
    /^(0|10|127)\./.test(h) ||
    /^169\.254\./.test(h) ||
    /^192\.168\./.test(h) ||
    /^172\.(1[6-9]|2\d|3[01])\./.test(h) ||
    /^100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\./.test(h) ||
    h === "::1" ||
    /^f[cd][0-9a-f]{2}:/.test(h) ||
    /^fe80:/.test(h)
  );
}

function parse(ctx: Ctx): AIProviderKeyInput {
  const b = ctx.body;
  if (!isRecord(b)) throw invalid("Body must be an object");
  const kind = b.kind as AIKeyKind;
  if (!KINDS.includes(kind)) throw invalid("kind must be openrouter, openai or anthropic.");
  const api_key = typeof b.api_key === "string" ? b.api_key.trim() : "";
  if (api_key.length < 8 || api_key.length > 500 || /[^\x21-\x7e]/.test(api_key)) throw invalid("Enter the API key exactly as the provider shows it.");
  const model = typeof b.model === "string" ? b.model.trim() : "";
  const fast_model = typeof b.fast_model === "string" ? b.fast_model.trim() : "";
  for (const id of [model, fast_model]) if (id && (id.length > 200 || /\s|[<>"]/.test(id))) throw invalid("Model ids are up to 200 characters, without spaces.");
  let base_url = "";
  if (kind === "openai") {
    const raw = typeof b.base_url === "string" ? b.base_url.trim() : "";
    let u: URL;
    try {
      u = new URL(raw);
    } catch {
      throw invalid("Enter a full URL starting with https://.");
    }
    if (u.username || u.password) throw invalid("The URL cannot contain a username or password.");
    if (u.protocol !== "https:") throw invalid("The URL must use https://.");
    if (privateHost(u.hostname)) throw invalid("The URL must point to a public internet address.");
    u.search = "";
    u.hash = "";
    base_url = u.toString().replace(/\/+$/, "");
    if (!model) throw invalid("Enter the model to use on this endpoint.");
  }
  return { kind, api_key, base_url, model, fast_model };
}

const fail = (reason: AIProviderTest["reason"], message: string, models: string[] = []): AIProviderTest => ({ ok: false, reason, message, models });

/** What the provider would say about the key: by its text, so every state can be tried. */
function test(b: AIProviderKeyInput): AIProviderTest {
  const label = KEY_LABEL[b.kind];
  const key = b.api_key.toLowerCase();
  if (key.includes("bad")) return fail("invalid_key", `${label} rejected this key. Check that it's correct and still active.`);
  if (key.includes("nofunds"))
    return fail("no_funds", b.kind === "openai" ? "The account behind this key is out of funds or quota." : `The ${label} account behind this key is out of credits.`);
  if (b.base_url?.includes("unreachable")) return fail("unreachable", "Couldn't reach the endpoint. Check the base URL and try again.");
  const models = OWN_MODELS[b.kind].map((m) => m.id).sort();
  for (const id of [b.model, b.fast_model]) if (id && !models.includes(id)) return fail("model_unavailable", `${label} doesn't list the model ${id}.`, models);
  return { ok: true, models };
}

function audit(ws: WsData, action: string, data: Record<string, unknown>) {
  ws.audit.unshift({
    id: uuid(),
    actor: { type: "user", user: userRef(db.meId), api_key_id: null },
    action,
    entity_type: "workspace",
    entity_id: ws.workspace.id,
    data,
    ip: "84.152.33.17",
    created_at: nowIso(),
  });
}

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function registerAiProvider() {
  get("/v1/ai/provider", (ctx) => {
    assertAiInstance();
    requireAiAdmin(ctx.ws);
    return view(ctx.ws);
  });

  post("/v1/ai/provider/test", async (ctx) => {
    assertAiInstance();
    requireAiAdmin(ctx.ws);
    const b = parse(ctx);
    await wait(500);
    return test(b);
  });

  put("/v1/ai/provider", async (ctx) => {
    assertAiInstance();
    requireAiAdmin(ctx.ws);
    const b = parse(ctx);
    await wait(500);
    const r = test(b);
    if (!r.ok) throw new HttpError(422, "provider_test_failed", r.message ?? "The key didn't work.");
    const replaced = !!aiKeyOf(ctx.ws);
    const def = OWN_DEFAULTS[b.kind];
    const now = nowIso();
    const k: MockAIKey = {
      kind: b.kind,
      base_url_host: b.base_url ? new URL(b.base_url).host : "",
      last4: b.api_key.slice(-4),
      model: b.model || def?.[0] || "",
      fast_model: b.fast_model || def?.[1] || b.model || "",
      status: "ok",
      status_at: null,
      created_by: userRef(db.meId),
      created_at: now,
      updated_at: now,
      failOnUse: b.api_key.toLowerCase().includes("revoked") ? "rejected" : undefined,
    };
    setAIKey(ctx.ws, k);
    audit(ctx.ws, replaced ? "ai.provider_key_replaced" : "ai.provider_key_set", { kind: b.kind, ...(k.base_url_host ? { base_url_host: k.base_url_host } : {}) });
    return view(ctx.ws);
  });

  post("/v1/ai/provider/decisions/test", async (ctx) => {
    assertAiInstance();
    requireAiAdmin(ctx.ws);
    const secret = parseDecisionsKey(ctx);
    await wait(400);
    return testDecisionsKey(secret);
  });

  put("/v1/ai/provider/decisions", async (ctx) => {
    assertAiInstance();
    requireAiAdmin(ctx.ws);
    const secret = parseDecisionsKey(ctx);
    await wait(400);
    const r = testDecisionsKey(secret);
    if (!r.ok) throw new HttpError(422, "provider_test_failed", r.message ?? "The key didn't work.");
    const replaced = !!decisionKeyOf(ctx.ws);
    const now = nowIso();
    setDecisionKey(ctx.ws, { last4: secret.slice(-4), status: "ok", status_at: null, created_by: userRef(db.meId), created_at: now, updated_at: now });
    audit(ctx.ws, replaced ? "ai.decisions_key_replaced" : "ai.decisions_key_set", { kind: "typesafe" });
    return view(ctx.ws);
  });

  del("/v1/ai/provider/decisions", (ctx) => {
    requireAiAdmin(ctx.ws);
    if (decisionKeyOf(ctx.ws)) {
      setDecisionKey(ctx.ws, null);
      audit(ctx.ws, "ai.decisions_key_removed", { kind: "typesafe" });
    }
    return respond(204);
  });

  del("/v1/ai/provider", (ctx) => {
    requireAiAdmin(ctx.ws);
    const k = aiKeyOf(ctx.ws);
    if (k) {
      setAIKey(ctx.ws, null);
      audit(ctx.ws, "ai.provider_key_removed", { kind: k.kind });
    }
    return respond(204);
  });
}
