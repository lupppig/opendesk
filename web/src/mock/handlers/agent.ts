// The widget agent's report and playground (contracts/agent.md "Report",
// "Playground"). The report is deterministic per day; the playground answers
// from the last visitor message: order lookups, a confirm-first cancel action,
// handoffs, blocked topics, else the best KB articles and knowledge sources.

import type { AgentReport, AgentSource, Bot, BotDraft, PlaygroundDone, PlaygroundMessage, PlaygroundToolEvent, ProcedureRef } from "@/lib/api/types";
import { botsOf, defaultBot } from "../bots";
import { db, type WsData } from "../db";
import { articleUrl } from "../teams";
import { DEFAULT_MODEL, effectiveModels, fromMicro, offersModel, reserve, spend } from "../aiBalance";
import { chunk, eventStream, get, invalid, post, wantsStream, type Ctx } from "../router";
import { DAY, htmlToProse, isRecord, markdownToHtml } from "../util";
import { assertAi, requireAiAdmin, score, terms } from "./ai";
import { knowledgeDocs } from "./knowledge";
import { mcpTool } from "./mcp";
import { channelSplit } from "./emailAnswers";
import { followedProcedures } from "./procedures";

// ─── Report ────────────────────────────────────────────────────────────────

/** A deterministic PRNG keyed by text, so a day reads the same on every load. */
function prng(seed: string) {
  let h = 2166136261;
  for (const c of seed) h = Math.imul(h ^ c.charCodeAt(0), 16777619);
  return () => {
    h = Math.imul(h ^ (h >>> 15), 2246822507);
    h = Math.imul(h ^ (h >>> 13), 3266489909);
    return ((h ^= h >>> 16) >>> 0) / 4294967296;
  };
}

/** Visitor questions the agent handed off or couldn't answer, newest first. */
const GAPS: [string, number][] = [
  ["Can I pay with PayPal?", 14],
  ["Do you have a Linux app?", 11],
  ["Can I get an invoice with my company's VAT number on it?", 9],
  ["Is there a lifetime plan?", 7],
  ["Can two people edit the same note at the same time offline?", 6],
  ["Do you ship notebooks to Australia?", 5],
  ["Why was I charged twice this month?", 5],
  ["Can I move notes between workspaces?", 4],
  ["Is Margin HIPAA compliant?", 3],
  ["My order says delivered but I never got it", 3],
  ["Can I change the name on my account?", 2],
];

function reportRange(ctx: Ctx) {
  const today = new Date().toISOString().slice(0, 10);
  const from = ctx.query.get("from") ?? new Date(Date.now() - 29 * DAY).toISOString().slice(0, 10);
  const to = ctx.query.get("to") ?? today;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(from) || !/^\d{4}-\d{2}-\d{2}$/.test(to) || from > to) throw invalid("from and to must be YYYY-MM-DD, from before to");
  if ((Date.parse(to) - Date.parse(from)) / DAY > 366) throw invalid("Choose a range of at most a year");
  return { from, to, today };
}

export function agentReport(ws: WsData, from: string, to: string, today = new Date().toISOString().slice(0, 10)): AgentReport {
  const start = Date.parse(`${from}T00:00:00Z`);
  const end = Date.parse(`${to}T00:00:00Z`) + DAY;
  const empty: AgentReport = { from: new Date(start).toISOString(), to: new Date(end).toISOString(), conversations: 0, replies: 0, resolved: 0, handoffs: 0, ratings: { up: 0, down: 0 }, usd: 0, by_day: [], tools: [], gaps: [] };
  // Only workspaces that use the agent have history: Margin, or one that turned it on.
  if (ws !== db.workspaces[0] && !ws.ai.widget_answers) return empty;
  const r = empty;
  let micro = 0;
  for (let t = start; t < end; t += DAY) {
    const date = new Date(t).toISOString().slice(0, 10);
    if (date > today) break;
    const rnd = prng(`${ws.workspace.id}:${date}`);
    const dow = new Date(t).getUTCDay();
    const weekend = dow === 0 || dow === 6;
    const conversations = weekend ? 4 + Math.floor(rnd() * 6) : 14 + Math.floor(rnd() * 13);
    const replies = Math.round(conversations * (2.2 + rnd() * 1.2));
    const handoffs = Math.round(conversations * (0.12 + rnd() * 0.1));
    const resolved = Math.round((conversations - handoffs) * (0.55 + rnd() * 0.15));
    r.by_day.push({ date, conversations, replies, resolved, handoffs });
    r.conversations += conversations;
    r.replies += replies;
    r.resolved += resolved;
    r.handoffs += handoffs;
    r.ratings.up += Math.round(replies * (0.1 + rnd() * 0.05));
    r.ratings.down += Math.round(replies * rnd() * 0.03);
    // Replies on the widget model, plus a Jev relevance check per visitor message (micro-dollars).
    micro += Math.round(replies * (16_000 + rnd() * 9_000) + conversations * 750);
  }
  r.usd = fromMicro(micro);
  if (!r.conversations) return r;
  const c = r.conversations;
  r.tools = [
    { tool: "search_knowledge", calls: Math.round(c * 0.92), errors: 0 },
    { tool: "shop__lookup_order", calls: Math.round(c * 0.27), errors: Math.round(c * 0.006) },
    { tool: "handoff_to_human", calls: Math.round(r.handoffs * 0.6), errors: 0 },
    { tool: "shop__list_invoices", calls: Math.round(c * 0.08), errors: 0 },
    { tool: "shop__cancel_order", calls: Math.round(c * 0.025), errors: Math.min(1, Math.round(c * 0.002)) },
  ].sort((a, b) => b.calls - a.calls);
  const days = r.by_day.length;
  r.gaps = GAPS.slice(0, days < 7 ? 8 : days < 30 ? 10 : GAPS.length).map(([question, n]) => ({ question, count: Math.max(1, Math.round((n * days) / 30)) }));
  r.channels = channelSplit(ws, r);
  return r;
}

// ─── Playground ────────────────────────────────────────────────────────────

export type Visitor = { name?: string; email?: string; verified?: boolean; user_id?: string };
export type Overrides = {
  widget_model?: string;
  widget_blocked_topics?: string[];
  tone?: string;
  reply_language?: string;
  instructions?: string;
  /** Answer as this bot (contracts/bots.md); default the default bot. */
  bot_id?: string;
  /** Unsaved edits of that bot. */
  bot?: BotDraft;
};

const TONES = ["friendly", "professional", "playful", "concise", "warm", "custom"];

/** The bot a trial answers as, with the draft's edits (contracts/agent.md "Playground"). */
export function trialBot(ws: WsData, o: Overrides): Bot {
  const saved = o.bot_id ? botsOf(ws).find((b) => b.id === o.bot_id) : defaultBot(ws);
  if (!saved) throw invalid("bot_id must be a bot of this workspace");
  const d = o.bot ?? {};
  return { ...saved, name: d.name?.trim() || saved.name, role: d.role ?? saved.role, language: d.language ?? saved.language, personality: { ...saved.personality, ...d.personality } };
}

/** Checks the bot settings of a playground or test run body. */
export function checkTrialBot(ws: WsData, o: Overrides) {
  const b = trialBot(ws, o);
  if (!TONES.includes(b.personality.tone)) throw invalid(`settings.bot.personality.tone must be one of ${TONES.join(", ")}`);
  if (b.personality.instructions.length > 4000) throw invalid("settings.bot.personality.instructions must be 4000 characters or less");
}

const EMOJI_END: Record<string, string> = { some: " 🙂", lots: " ✨🙌" };

/** The bot's voice on a planned reply: its handoff line, greeting style and emoji. */
function voiced(p: Plan, bot: Bot): Plan {
  let text = p.text;
  const v = bot.personality;
  if (p.handoff && v.handoff_message.trim()) text = v.handoff_message.trim();
  if (v.tone === "professional") text = text.replace(/^Hi ([^!]+)! /, "Hello $1, ");
  if (v.tone === "playful") text = text.replace(/^Hi ([^!]+)! /, "Hey $1! ");
  if (v.reply_length === "detailed" && p.sources.length && !p.handoff) text += "\n\nIf any step doesn't match what you see, tell me where you're stuck and I'll walk you through it.";
  if (EMOJI_END[v.emoji]) {
    const paras = text.split("\n\n");
    paras[0] = paras[0].replace(/([.!?])(\s*\[\d\])?$/, (m) => m + EMOJI_END[v.emoji]);
    if (v.emoji === "lots") text = `👋 ${paras.join("\n\n")}`;
    else text = paras.join("\n\n");
  }
  return { ...p, text };
}
export type Plan = {
  tools: PlaygroundToolEvent[];
  sources: AgentSource[];
  text: string;
  handoff: boolean;
  action: PlaygroundDone["action"];
  /** The procedures the answer followed. */
  procedures: ProcedureRef[];
  /** A model call ran (handoffs on request don't call the model). */
  metered: boolean;
};

function parseBody(ctx: Ctx): { messages: PlaygroundMessage[]; settings: Overrides; visitor: Visitor } {
  const b = ctx.body;
  if (!Array.isArray(b.messages) || b.messages.length < 1 || b.messages.length > 40) throw invalid("messages must have 1 to 40 entries");
  const messages = b.messages.map((m: unknown, i: number) => {
    if (!isRecord(m) || (m.role !== "visitor" && m.role !== "agent") || typeof m.text !== "string") throw invalid(`messages[${i}] needs a role (visitor or agent) and text`);
    if (!m.text.trim()) throw invalid(`messages[${i}] is empty`);
    if (m.text.length > 4000) throw invalid("Each message can be at most 4,000 characters");
    return { role: m.role, text: m.text } as PlaygroundMessage;
  });
  if (messages[messages.length - 1].role !== "visitor") throw invalid("The last message must be the visitor's");
  const settings: Overrides = isRecord(b.settings) ? (b.settings as Overrides) : {};
  if (settings.widget_model !== undefined && settings.widget_model !== "" && !offersModel(ctx.ws, settings.widget_model)) throw invalid("widget_model must be empty or one of the offered models");
  if (settings.widget_blocked_topics !== undefined && (!Array.isArray(settings.widget_blocked_topics) || settings.widget_blocked_topics.some((x) => typeof x !== "string"))) throw invalid("widget_blocked_topics must be a list of topics");
  checkTrialBot(ctx.ws, settings);
  const visitor: Visitor = isRecord(b.visitor) ? (b.visitor as Visitor) : {};
  return { messages, settings, visitor };
}

/** The widget model: the draft override, else the workspace's widget_model, else model, else the default. */
export function widgetModel(ws: WsData, o: Overrides): string {
  if (o.widget_model) return o.widget_model;
  if (o.widget_model === "") return effectiveModels(ws).model;
  return effectiveModels(ws).widget || DEFAULT_MODEL;
}

/** Passages for a question: published articles and ready, enabled knowledge documents. */
export function retrieve(ws: WsData, query: string, limit = 3): { source: AgentSource; text: string; s: number }[] {
  const articles = ws.articles
    .filter((a) => a.status === "published")
    .map((a) => {
      // The passage is the article's own prose; the summary only helps it rank.
      const body = htmlToProse(a.body_html);
      return { source: { kind: "article", id: a.id, title: a.title, url: articleUrl(ws, a) } as AgentSource, text: body, s: score(query, a.title, `${a.excerpt} ${body}`) };
    })
    .filter((x) => x.s >= 0.35);
  const docs = knowledgeDocs(ws)
    .map(({ source, doc }) => {
      const text = source.snippet?.answer ?? "";
      const kind: AgentSource["kind"] = source.kind === "snippet" ? "snippet" : source.kind === "file" ? "file" : "page";
      return { source: { kind, id: doc.id, title: doc.title.replace(/ \| Margin Docs$/, ""), url: doc.url } as AgentSource, text, s: score(query, doc.title, text) * (kind === "snippet" ? 1.2 : 1) };
    })
    .filter((x) => x.s >= 0.4);
  return [...articles, ...docs].sort((a, b) => b.s - a.s).slice(0, limit);
}

function informative(text: string): string[] {
  return text
    .replace(/\s+/g, " ")
    .split(/(?<=[.!?])\s+/)
    .map((x) => x.trim())
    .filter((x) => x.length > 25 && x.length < 260);
}

function blockedTopic(text: string, topics: string[]): string | null {
  const lower = text.toLowerCase();
  const words = new Set(terms(text));
  for (const topic of topics) {
    const t = topic.toLowerCase().trim();
    if (!t) continue;
    const need = terms(t);
    if (lower.includes(t) || (need.length > 0 && need.every((w) => words.has(w)))) return topic;
  }
  return null;
}

const EMAIL = /[^\s@]+@[^\s@]+\.[^\s@]+/;
const HUMAN = /\b(human|real person|a person|someone|representative|live agent|an agent|support agent|talk to (the )?team|mensch|persona|humain)\b/i;

export function plan(ws: WsData, messages: PlaygroundMessage[], o: Overrides, visitor: Visitor): Plan {
  const bot = trialBot(ws, o);
  const p = voiced(planned(ws, messages, o, visitor), bot);
  return p.metered ? { ...p, procedures: followedProcedures(ws, messages[messages.length - 1].text, bot.id || null) } : p;
}

function planned(ws: WsData, messages: PlaygroundMessage[], o: Overrides, visitor: Visitor): Plan {
  const last = messages[messages.length - 1].text;
  const lower = last.toLowerCase();
  const first = (visitor.name ?? "").trim().split(/\s+/)[0] ?? "";
  const hi = first && messages.filter((m) => m.role === "visitor").length === 1 ? `Hi ${first}! ` : "";
  const bot = trialBot(ws, o);
  const concise = bot.personality.tone === "concise" || (o.tone ?? ws.ai.tone).toLowerCase().includes("concise");
  const blocked = [...(o.widget_blocked_topics ?? ws.ai.widget_blocked_topics ?? []), ...bot.personality.blocked_topics];
  const base: Plan = { tools: [], sources: [], text: "", handoff: false, action: null, procedures: [], metered: true };
  const orderNo = /#\s?(\d{3,})/.exec(last)?.[1] ?? /\border\s+(?:number\s+)?(\d{3,})/i.exec(last)?.[1] ?? "4821";
  const shop = (name: string) => (mcpTool(ws, "shop", name) ? `shop__${name}` : null);

  // A person, on request: no model call.
  const prevAgent = [...messages].reverse().find((m) => m.role === "agent")?.text ?? "";
  const gaveEmail = EMAIL.test(last) && /email/i.test(prevAgent);
  if (HUMAN.test(last) || gaveEmail) {
    const collect = ws.ai.widget_collect_email ?? true;
    if (collect && !visitor.email && !EMAIL.test(last)) {
      return { ...base, metered: false, text: `${hi}Happy to bring in someone from the team. What's the best email address for them to reach you, in case you leave the chat?` };
    }
    return { ...base, metered: false, handoff: true, text: `${hi}Of course. I've asked the team to take over, and someone will reply here soon. Our support hours are Monday to Friday, 8:00 to 18:00 CET.` };
  }

  const topic = blockedTopic(last, blocked);
  if (topic) {
    return { ...base, text: `${hi}Sorry, I can't help with ${topic.toLowerCase()}. I'm happy to help with anything else about ${ws.workspace.name}, or I can connect you with the team.` };
  }

  if (/\b(cancel|refund)\b/i.test(last) && /\b(order|purchase)\b|#\s?\d{3,}|\b(cancel|refund) (my|the|this|it)\b/i.test(last)) {
    const lookup = shop("lookup_order");
    const cancel = shop("cancel_order") ?? "shop__cancel_order";
    return {
      ...base,
      tools: lookup ? [{ name: lookup, status: "done", summary: `Order #4821: not shipped yet, $48.00` }] : [],
      text: `${hi}I found order #4821: the paper notebook bundle for $48.00, not shipped yet. I can cancel it and refund the card ending 4242. Please confirm below.`,
      action: { tool: cancel, title: "Cancel order", summary: "Cancel order #4821 and refund $48.00 to the card ending 4242" },
    };
  }

  if (/\b(order|tracking|track|shipment|shipped|package|parcel|delivery)\b|#\s?\d{3,}/i.test(lower)) {
    const lookup = shop("lookup_order");
    const tools: PlaygroundToolEvent[] = [{ name: "search_knowledge", status: "done", summary: `${retrieve(ws, last, 6).length || 2} passages` }];
    if (visitor.verified === false) {
      if (lookup) tools.push({ name: lookup, status: "error", summary: "Only for signed-in customers" });
      return { ...base, tools, text: `${hi}I can only share order details with signed-in customers. Please sign in to your account and open this chat again, or leave your email and the team will look into order #${orderNo} for you.` };
    }
    if (!lookup) {
      return { ...base, tools, text: `${hi}I can't look up orders from here yet. Would you like me to connect you with the team? They can check order #${orderNo} for you.` };
    }
    tools.push({ name: lookup, status: "done", summary: `Order #${orderNo}: shipped` });
    return {
      ...base,
      tools,
      text: `${hi}I checked order #${orderNo}. It shipped on October 9 with DHL and should arrive by **October 14**.\n\nYou can follow it with tracking number JD014600006281935. If it hasn't arrived by then, tell me and I'll bring in the team.`,
    };
  }

  const found = retrieve(ws, `${last} ${messages.length > 1 ? (messages[messages.length - 3]?.text ?? "") : ""}`);
  if (!found.length) {
    return { ...base, text: `${hi}I couldn't find an answer to that in our help center. Would you like me to connect you with someone from the team?` };
  }
  const [top, second] = found;
  const lead = top.source.kind === "snippet" ? top.text.replace(/\n+/g, " ") : (informative(top.text).slice(0, 2).join(" ") || `There's a guide for this: ${top.source.title}.`);
  const parts = [`${hi}${lead} [1]`];
  if (second && !concise) parts.push(`For more detail, see **${second.source.title}** [2].`);
  if (!concise) parts.push("Anything else I can help with?");
  return { ...base, sources: found.slice(0, second ? 2 : 1).map((f) => f.source), text: parts.join("\n\n") };
}

export function registerAgent() {
  get("/v1/ai/agent/report", (ctx) => {
    assertAi(ctx.ws);
    requireAiAdmin(ctx.ws, "Only admins can see the agent report");
    const { from, to, today } = reportRange(ctx);
    return agentReport(ctx.ws, from, to, today);
  });

  post("/v1/ai/agent/playground", (ctx) => {
    assertAi(ctx.ws);
    requireAiAdmin(ctx.ws, "Only admins can use the playground");
    const { messages, settings, visitor } = parseBody(ctx);
    reserve(ctx.ws, "playground");
    const p = plan(ctx.ws, messages, settings, visitor);
    const model = widgetModel(ctx.ws, settings);
    const finish = (): PlaygroundDone => ({
      text_html: markdownToHtml(p.text),
      sources: p.sources,
      handoff: p.handoff,
      action: p.action,
      procedures: p.procedures,
      usd: p.metered ? spend(ctx.ws, "playground", db.meId, model) : 0,
    });
    if (!wantsStream(ctx)) return finish();
    return eventStream(async (emit, wait) => {
      await wait(250);
      for (const t of p.tools) {
        emit("tool", { name: t.name, status: "running", summary: "" });
        await wait(400 + Math.random() * 500);
        emit("tool", t);
      }
      if (p.sources.length) emit("sources", { sources: p.sources });
      for (const text of chunk(p.text, 2)) {
        emit("delta", { text });
        await wait(30 + Math.random() * 15);
      }
      emit("done", finish());
    });
  });
}
