// The AI balance in mock mode (contracts/billing.md "Metered usage"): AI usage
// is charged in US dollars on each call's real cost, on the model the workspace
// chose. Amounts are kept as integer micro-dollars so every sum adds up; the API
// shows US dollars. Reserve checks the call's estimate before a model call, spend
// charges what the call really used. Shared by every AI handler.

import type { AIFeature, AIModel } from "@/lib/api/types";
import { OUT_OF_BALANCE_BELOW } from "@/components/billing/model";
import type { AiUsageRow, WsData } from "./db";
import { db } from "./db";
import { afterSpend, billingOut } from "./handlers/billing";
import { aiKeyOf, checkKeyOnUse, keyModels, OWN_DEFAULTS } from "./aiKey";
import { HttpError } from "./router";
import { nowIso } from "./util";

/** US dollars -> integer micro-dollars. */
export const toMicro = (usd: number) => Math.round(usd * 1e6);
/** Integer micro-dollars -> US dollars (up to 6 decimals). */
export const fromMicro = (micro: number) => Math.round(micro) / 1e6;
/** Rounds US dollars to the millionth, so 0.1 + 0.2 reads 0.3. */
export const roundUsd = (usd: number) => fromMicro(toMicro(usd));

/** The smallest estimate of any call (plan.MinBalance): below it nothing can start. */
export const MIN_BALANCE_USD = OUT_OF_BALANCE_BELOW;

/** A typical call's size (input / output tokens), `typicalTokens` in internal/ai. */
export const TYPICAL_TOKENS: Record<AIFeature, [number, number]> = {
  summarize: [3000, 300],
  rewrite: [800, 400],
  translate: [800, 500],
  triage: [1500, 150],
  decide: [1500, 100],
  answer_check: [1500, 0],
  copilot: [4000, 400],
  draft_reply: [4000, 500],
  answer: [5000, 400],
  answer_email: [5000, 500],
  playground: [5000, 400],
  kb_draft: [8000, 1500],
  assistant: [6000, 400],
  assistant_peer: [4000, 400],
};

/** What a call costs when no price gives it (contracts/billing.md "Typical costs"), in US dollars. */
export const FALLBACK_USD: Record<AIFeature, number> = {
  summarize: 0.01,
  rewrite: 0.01,
  translate: 0.01,
  triage: 0.01,
  decide: 0.01,
  answer_check: 0.001,
  copilot: 0.02,
  draft_reply: 0.03,
  answer: 0.03,
  answer_email: 0.03,
  playground: 0.03,
  kb_draft: 0.05,
  assistant: 0.02,
  assistant_peer: 0.01,
};

export const DEFAULT_MODEL = "anthropic/claude-sonnet-5.5";
export const FAST_MODEL = "anthropic/claude-haiku-5.5";
export const JEV = "jev";
/** Jev decisions are charged per input token; output is free. US dollars per 1M input tokens, markup included. */
const JEV_USD_PER_MTOK = 0.5;
/** opendesk Cloud's markup on provider prices (AI_USAGE_MARKUP). */
export const MARKUP = 1.5;

/** A draft reply's size, the basis of `typical_reply_usd`. */
const REPLY_TOKENS = TYPICAL_TOKENS.draft_reply;

function model(id: string, name: string, vendor: string, context_length: number, input: number, output: number): AIModel {
  const usd_per_mtok = { input: roundUsd(input * MARKUP), output: roundUsd(output * MARKUP) };
  return { id, name, vendor, context_length, usd_per_mtok, typical_reply_usd: fromMicro(Math.ceil(REPLY_TOKENS[0] * usd_per_mtok.input + REPLY_TOKENS[1] * usd_per_mtok.output)) };
}

/** The curated OpenRouter list (contracts/ai.md "Models"): provider prices per 1M tokens, shown with the 1.5 markup. */
export const MODELS: AIModel[] = [
  model(DEFAULT_MODEL, "Claude Sonnet 5.5", "Anthropic", 1_000_000, 2, 10),
  model(FAST_MODEL, "Claude Haiku 5.5", "Anthropic", 200_000, 0.1, 0.5),
  model("anthropic/claude-opus-5.5", "Claude Opus 5.5", "Anthropic", 1_000_000, 4, 20),
  model("openai/gpt-5.6-sol", "GPT-5.6 Sol", "OpenAI", 400_000, 2, 10),
  model("openai/gpt-5.6-luna", "GPT-5.6 Luna", "OpenAI", 400_000, 0.2, 1.2),
  model("google/gemini-3.8-flash", "Gemini 3.8 Flash", "Google", 1_048_576, 0.75, 3.75),
  model("deepseek/deepseek-v4.1-flash", "DeepSeek V4.1 Flash", "DeepSeek", 128_000, 0.15, 0.6),
  model("z-ai/glm-5.3-flash", "GLM-5.3 Flash", "Z.ai", 1_048_576, 0.048, 0.2),
];

/** A model's price per 1M tokens as the workspace pays it, or null when unknown. */
function priceOf(id: string): { input: number; output: number } | null {
  if (id === JEV) return { input: JEV_USD_PER_MTOK, output: 0 };
  return MODELS.find((m) => m.id === id)?.usd_per_mtok ?? null;
}

/** Micro-dollars for tokens at a price: tokens × US dollars per 1M tokens, rounded up. */
function costMicro(price: { input: number; output: number }, input: number, output: number): number {
  return Math.ceil(input * price.input + output * price.output - 1e-9);
}

/** What a typical call of `feature` costs on `model`, in US dollars (the feature's fallback when the price is unknown). */
export function typicalUsd(feature: AIFeature, model: string): number {
  const price = priceOf(model);
  if (!price) return FALLBACK_USD[feature];
  const [input, output] = TYPICAL_TOKENS[feature];
  return fromMicro(costMicro(price, input, output));
}

/** The models the workspace may choose: the instance's, or its own key's provider's. */
export function offeredModels(ws: WsData): { data: AIModel[]; default: string; fast: string } {
  const k = aiKeyOf(ws);
  if (!k) return { data: MODELS, default: DEFAULT_MODEL, fast: FAST_MODEL };
  const def = OWN_DEFAULTS[k.kind];
  return { data: keyModels(k), default: k.model || def?.[0] || "", fast: k.fast_model || def?.[1] || k.model };
}

/** Whether id is a model the workspace may choose. */
export function offersModel(ws: WsData, id: string): boolean {
  return offeredModels(ws).data.some((m) => m.id === id);
}

/** The workspace's effective models: its choice, else the defaults of its provider. */
export function effectiveModels(ws: WsData) {
  const offered = offeredModels(ws);
  const known = (id: string | undefined) => !!id && offered.data.some((m) => m.id === id);
  const model = known(ws.ai.model) ? ws.ai.model! : offered.default;
  const fast = known(ws.ai.fast_model) ? ws.ai.fast_model! : offered.fast;
  const widget = known(ws.ai.widget_model) ? ws.ai.widget_model! : model;
  return { model, fast, widget };
}

/** The model a feature runs on (contracts/ai.md "Models and prompts"). */
export function modelFor(ws: WsData, feature: AIFeature): string {
  const m = effectiveModels(ws);
  switch (feature) {
    case "triage":
    case "decide":
    case "answer_check":
      return JEV;
    case "rewrite":
    case "translate":
    case "assistant_peer":
      return m.fast;
    case "answer":
    case "answer_email":
    case "playground":
      return m.widget;
    default:
      return m.model;
  }
}

/** GET /v1/ai/config `costs`: a typical call of each feature on the workspace's chosen models, in US dollars. */
export function configCosts(ws: WsData): Partial<Record<AIFeature, number>> {
  const ownKey = !!aiKeyOf(ws);
  const out: Partial<Record<AIFeature, number>> = {};
  for (const f of Object.keys(TYPICAL_TOKENS) as AIFeature[]) {
    const m = modelFor(ws, f);
    // On the workspace's own key only Jev decisions are charged.
    if (ownKey && m !== JEV) continue;
    out[f] = typicalUsd(f, m);
  }
  return out;
}

/**
 * One metered call: a typical call's tokens × a length factor (a longer thread
 * or answer costs more), charged at the model's price, rounded up to the
 * micro-dollar. Unknown prices charge the feature's typical cost.
 */
export function meter(feature: AIFeature, model: string, random: () => number = Math.random): Omit<AiUsageRow, "user_id" | "at"> {
  const length = 0.35 + random() * 1.55;
  const [tin, tout] = TYPICAL_TOKENS[feature];
  const input_tokens = Math.round(tin * length);
  const price = priceOf(model);
  const output_tokens = model === JEV ? 0 : Math.round(tout * length);
  const micro_usd = price ? Math.max(1, costMicro(price, input_tokens, output_tokens)) : toMicro(FALLBACK_USD[feature]);
  return { feature, model, micro_usd, input_tokens, output_tokens, source: "instance" };
}

/** The estimate `reserve` needs the balance to cover: a typical call on the model, at least $0.01. */
export function estimateUsd(ws: WsData, feature: AIFeature): number {
  return Math.max(MIN_BALANCE_USD, typicalUsd(feature, modelFor(ws, feature)));
}

/** Throws 402 ai_balance_empty when the AI balance is below the call's estimate. A
 * workspace on its own key is never refused for LLM calls (Jev decisions still are),
 * but a key its provider rejects fails the call with 502. */
export function reserve(ws: WsData, feature: AIFeature) {
  if (aiKeyOf(ws) && modelFor(ws, feature) !== JEV) {
    checkKeyOnUse(ws);
    return;
  }
  const ai = billingOut(ws).ai; // remaining derived from the plan, allowance and top-ups
  if (ai.unlimited) return;
  if (toMicro(ai.remaining_usd) < toMicro(estimateUsd(ws, feature))) {
    throw new HttpError(402, "ai_balance_empty", "Your workspace's AI balance is too low for this. Add AI usage or upgrade.");
  }
}

/**
 * Records a successful call and returns the US dollars charged. A call that
 * costs more than the balance takes it to $0; the rest is not charged.
 */
export function spend(ws: WsData, feature: AIFeature, userId: string | null = db.meId, model = modelFor(ws, feature)): number {
  const row = meter(feature, model);
  if (aiKeyOf(ws) && model !== JEV) {
    // On the workspace's own key: recorded, $0.
    ws.aiUsage.push({ ...row, micro_usd: 0, source: "own_key", user_id: userId, at: nowIso() });
    return 0;
  }
  const ai = ws.billing.ai;
  let before = 0;
  if (!ai.unlimited) {
    before = billingOut(ws).ai.remaining_usd;
    row.micro_usd = Math.min(row.micro_usd, toMicro(before));
    ai.used_usd = fromMicro(toMicro(ai.used_usd) + row.micro_usd);
  } else {
    ai.used_usd = fromMicro(toMicro(ai.used_usd) + row.micro_usd);
  }
  ws.aiUsage.push({ ...row, user_id: userId, at: nowIso() });
  if (!ai.unlimited && row.micro_usd > 0) {
    // Auto-recharge and running-low notices (contracts/billing.md "Auto-recharge").
    afterSpend(ws, before, billingOut(ws).ai.remaining_usd);
  }
  return fromMicro(row.micro_usd);
}
