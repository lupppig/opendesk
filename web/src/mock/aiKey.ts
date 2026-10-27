// A workspace's own AI provider key in mock mode (contracts/ai.md "Provider per
// workspace"). Only what the API shows is kept: the kind, the base URL host, the
// last 4 characters and the default models. Keys behave by their text so every
// state can be tried: "bad" is rejected, "nofunds" is out of funds, and "revoked"
// passes the test but is rejected on the first AI call.

import type { AIDecisionsKey, AIKeyKind, AIModel, AIProviderKey, NotificationType } from "@/lib/api/types";
import { db, type WsData } from "./db";
import { publish } from "./gateway";
import { HttpError } from "./router";
import { nowIso, uuid } from "./util";

export type MockAIKey = AIProviderKey & { failOnUse?: "rejected" | "no_funds"; notified_at?: number };

const keys = new WeakMap<WsData, MockAIKey>();

export function aiKeyOf(ws: WsData): MockAIKey | null {
  return keys.get(ws) ?? null;
}

export function setAIKey(ws: WsData, k: MockAIKey | null) {
  if (k) keys.set(ws, k);
  else keys.delete(ws);
}

/** The workspace's own TypeSafe key for decisions; behaves by its text like a provider key. */
const decisionKeys = new WeakMap<WsData, AIDecisionsKey>();

export function decisionKeyOf(ws: WsData): AIDecisionsKey | null {
  return decisionKeys.get(ws) ?? null;
}

export function setDecisionKey(ws: WsData, k: AIDecisionsKey | null) {
  if (k) decisionKeys.set(ws, k);
  else decisionKeys.delete(ws);
}

/** Decisions run on the workspace's own credentials: its TypeSafe key, or Jev on its OpenRouter key. */
export function decisionsOnOwnKey(ws: WsData): boolean {
  return !!decisionKeyOf(ws) || aiKeyOf(ws)?.kind === "openrouter";
}

const m = (id: string, name: string, vendor: string, context_length: number | null = null): AIModel => ({ id, name, vendor, context_length, usd_per_mtok: null, typical_reply_usd: null });

/** The models each provider lists for a key (no prices: the workspace pays its provider). */
export const OWN_MODELS: Record<AIKeyKind, AIModel[]> = {
  openrouter: [
    m("anthropic/claude-sonnet-5.5", "Claude Sonnet 5.5", "Anthropic", 1_000_000),
    m("anthropic/claude-haiku-5.5", "Claude Haiku 5.5", "Anthropic", 200_000),
    m("anthropic/claude-opus-5.5", "Claude Opus 5.5", "Anthropic", 1_000_000),
    m("openai/gpt-5.6-sol", "GPT-5.6 Sol", "OpenAI", 400_000),
    m("openai/gpt-5.6-luna", "GPT-5.6 Luna", "OpenAI", 400_000),
    m("google/gemini-3.8-flash", "Gemini 3.8 Flash", "Google", 1_048_576),
    m("deepseek/deepseek-v4.1-flash", "DeepSeek V4.1 Flash", "DeepSeek", 128_000),
    m("z-ai/glm-5.3-flash", "GLM-5.3 Flash", "Z.ai", 1_048_576),
    m("meta-llama/llama-4.1-maverick", "Llama 4.1 Maverick", "Meta", 1_000_000),
    m("mistralai/mistral-large-3", "Mistral Large 3", "Mistral", 256_000),
    m("qwen/qwen-3.5-max", "Qwen 3.5 Max", "Qwen", 256_000),
  ],
  anthropic: [
    m("claude-sonnet-5-5", "Claude Sonnet 5.5", "Anthropic", 1_000_000),
    m("claude-haiku-4-5-20251001", "Claude Haiku 4.5", "Anthropic", 200_000),
    m("claude-opus-5-5", "Claude Opus 5.5", "Anthropic", 1_000_000),
  ],
  openai: [m("llama-4.1-70b-instruct", "llama-4.1-70b-instruct", ""), m("qwen-3.5-32b", "qwen-3.5-32b", ""), m("mistral-large-3", "mistral-large-3", "")],
};

/** A provider's usual default and fast models; an OpenAI-compatible endpoint has none. */
export const OWN_DEFAULTS: Partial<Record<AIKeyKind, [string, string]>> = {
  openrouter: ["anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5"],
  anthropic: ["claude-sonnet-5-5", "claude-haiku-4-5-20251001"],
};

/** The models a key may use: the provider's list plus the key's own defaults. */
export function keyModels(k: MockAIKey): AIModel[] {
  const list = [...OWN_MODELS[k.kind]];
  for (const id of [k.model, k.fast_model]) if (id && !list.some((x) => x.id === id)) list.unshift(m(id, id, ""));
  return list;
}

export const KEY_LABEL: Record<AIKeyKind, string> = { openrouter: "OpenRouter", anthropic: "Anthropic", openai: "The endpoint" };

/**
 * Before a call on the key: a key the provider rejects fails the call like an
 * outage (502), never falling back to the AI balance, and tells admins at most hourly.
 */
export function checkKeyOnUse(ws: WsData) {
  const k = aiKeyOf(ws);
  if (!k) return;
  if (k.failOnUse && k.status === "ok") {
    k.status = k.failOnUse;
    k.status_at = nowIso();
  }
  if (k.status === "ok") return;
  if (!k.notified_at || Date.now() - k.notified_at > 3_600_000) {
    k.notified_at = Date.now();
    const role = ws.members.find((x) => x.user_id === db.meId)?.role;
    if (role === "owner" || role === "admin") {
      const n = { id: uuid(), type: "ai_key_failed" as NotificationType, ticket: null, data: { provider: k.kind, reason: k.status }, read_at: null, created_at: nowIso() };
      ws.notifications.unshift(n);
      publish(ws, "notification.created", { notification: n }, { topics: [`user:${db.meId}`] });
    }
  }
  if (k.status === "no_funds") {
    throw new HttpError(502, "ai_key_no_funds", "The AI provider account behind this workspace's key is out of funds. An admin can fix it in AI settings.");
  }
  throw new HttpError(502, "ai_key_rejected", "The AI provider rejected this workspace's key. An admin can fix it in AI settings.");
}
