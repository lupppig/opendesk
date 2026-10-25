// In-memory backend for the widget demo (demo.html, ?od_mock) and the
// dashboard preview. The demo bundles it only when __OD_WIDGET_MOCK__ is true
// (WIDGET_MOCK=1 or the dev server); production widget builds drop it.
//
// It simulates the AI agent the way the API does (contracts/agent.md): live
// events streamed word by word on the contact topic, a tool call, an action
// card the visitor confirms, ratings and a handoff to a person.

import { OLLIE_LOOK, type BotLook } from "../../src/lib/botlook";
import type { Fetcher } from "./api";
import type { RtChannelLike, RtSocketLike, SocketFactory } from "./realtime";
import type { AIAction, Attachment, BotIdentity, BotRef, Conversation, Message, WidgetConfig } from "./types";

const WID = "00000000-0000-4000-8000-00000000d3e0";
const HELP = "https://margin.opendeskhelp.com/a";

type Copy = { q1: string; a1: string; q2: string; a2: string; subject: string; aiSso: string; s1: string; s2: string; human: string };
const COPY: Record<string, Copy> = {
  en: {
    subject: "Exporting notebooks to PDF",
    q1: "Hi! Is there a way to export a whole notebook to PDF, not just single pages?",
    a1: "<p>Yes. Open the notebook, then choose <strong>File → Export → PDF</strong> and pick <em>Entire notebook</em>.</p>",
    q2: "Perfect, that did it. Thanks!",
    a2: "Glad that worked! Let us know if anything else comes up.",
    aiSso:
      "Sign-in loops with SSO usually mean the identity provider's **signing certificate** changed [1]. An admin can upload the new certificate under **Settings → Security → SSO** [2], then everyone can sign in again.\n\nWhile that's sorted, admins can still sign in with email and password.",
    s1: "Set up SAML single sign-on",
    s2: "Rotate your SSO certificate",
    human: "Hi, Priya here from the Margin team. I've picked this up and I'm on it now.",
  },
  ja: {
    subject: "ノートをPDFで書き出す",
    q1: "こんにちは。ページ単位ではなく、ノート全体をPDFで書き出す方法はありますか？",
    a1: "<p>はい。ノートを開いて <strong>ファイル → 書き出し → PDF</strong> を選び、<em>ノート全体</em> を指定してください。</p>",
    q2: "できました。ありがとうございます！",
    a2: "うまくいってよかったです。ほかにも何かあればお気軽にどうぞ。",
    aiSso: "SSOでサインインが繰り返される場合、IDプロバイダーの**署名証明書**が更新された可能性があります [1]。管理者が **設定 → セキュリティ → SSO** から新しい証明書をアップロードすると [2]、再びサインインできるようになります。",
    s1: "SAMLシングルサインオンを設定する",
    s2: "SSO証明書を更新する",
    human: "Marginサポートのプリヤです。こちらで引き継ぎました。",
  },
  ar: {
    subject: "تصدير الدفاتر بصيغة PDF",
    q1: "مرحبًا! هل يمكن تصدير دفتر كامل بصيغة PDF، وليس صفحات منفردة فقط؟",
    a1: "<p>نعم. افتح الدفتر، ثم اختر <strong>ملف ← تصدير ← PDF</strong> وحدّد <em>الدفتر بالكامل</em>.</p>",
    q2: "ممتاز، نجح الأمر. شكرًا!",
    a2: "يسعدنا ذلك! أخبرنا إن احتجت إلى أي شيء آخر.",
    aiSso: "تكرار تسجيل الدخول عبر SSO يعني غالبًا أن **شهادة التوقيع** لدى مزوّد الهوية قد تغيّرت [1]. يمكن للمسؤول رفع الشهادة الجديدة من **الإعدادات ← الأمان ← SSO** [2]، وبعدها يستطيع الجميع تسجيل الدخول مجددًا.",
    s1: "إعداد تسجيل الدخول الموحّد SAML",
    s2: "تحديث شهادة SSO",
    human: "مرحبًا، معك بريا من فريق Margin. تسلّمت محادثتك وأعمل عليها الآن.",
  },
};

const ORDER = {
  question: "Can you send order #4821 to my new address? It's 22 Baker Street, London.",
  lookup: "I found order **#4821**: a Margin Pro notebook set, not shipped yet, so the address can still change.\n\nShall I send it to **22 Baker Street, London** instead?",
  action: { title: "Change the delivery address", summary: "Send order #4821 to 22 Baker Street, London instead of 9 Elm Road.", tool: "Orders" },
  confirmed: "Done. Order **#4821** will now ship to 22 Baker Street, London. You'll get an updated confirmation email in a minute.",
  declined: "No problem, I've left order #4821 as it was. Anything else I can help with?",
};

const OLLIE: BotIdentity = { id: "00000000-0000-4000-8000-0000000b0700", name: "Ollie", role: "", avatar_url: "", look: OLLIE_LOOK, greeting: "" };
const PIP_LOOK: BotLook = {
  kind: "built",
  shape: "squircle",
  fill: { type: "gradient", color: "#9ff0e3", to: "#0b7f73" },
  eyes: "happy",
  mouth: "smile",
  accessory: "antenna",
  accent: "#f5a524",
  blush: true,
  background: "none",
  emoji: "",
};
const PIP: BotIdentity = {
  id: "00000000-0000-4000-8000-0000000b0701",
  name: "Pip",
  role: "Support assistant",
  avatar_url: "",
  look: PIP_LOOK,
  greeting: "Hi, I'm Pip.\nAsk me about orders, delivery or your account.",
};
const FOX: BotIdentity = { ...PIP, id: "00000000-0000-4000-8000-0000000b0702", name: "Fox", role: "", greeting: "", look: { ...PIP_LOOK, kind: "emoji", emoji: "🦊", fill: { type: "solid", color: "#ffe2a6", to: "#c46f06" }, background: "soft" } };
const refOf = (b: BotIdentity): BotRef => ({ id: b.id, name: b.name, avatar_url: b.avatar_url, look: b.look });

/** Demo widget configs by key (?key= on demo.html). */
export function demoConfig(key: string, params = new URLSearchParams()): WidgetConfig {
  const base: WidgetConfig = {
    workspace_name: "Margin",
    brand_color: null,
    greeting: "Hi there. How can we help?",
    help_center_url: "https://margin.opendeskhelp.com",
    require_email: false,
    locale: params.get("od_ws_lang") ?? "en",
    avatar_url: null,
    launcher_icon_url: null,
    theme: "auto",
    position: "right",
    offset_x: 20,
    offset_y: 20,
    launcher: "icon",
    launcher_label: "",
    radius: "soft",
    title: "",
    subtitle: "",
    suggestions: ["Where is my order?", "Change my delivery address", "I can't sign in with SSO", "Talk to a person"],
    sound: true,
    voice_messages: params.get("od_voice") !== "0",
    voice_max_seconds: Number(params.get("od_voice_max")) || 60,
    team: [
      { name: "Priya", avatar_url: null },
      { name: "Tomás", avatar_url: null },
      { name: "Mei", avatar_url: null },
    ],
    ai_answers: params.get("od_bot") !== "0",
    bot: OLLIE,
    topics: [],
    open: params.get("od_closed") === null,
    offline_message: "",
    intro: "",
    chat_welcome: params.get("od_welcome") !== "0",
    language: params.get("od_fixed_lang") ?? "",
    brand: { name: "Margin", color: null },
  };
  const topics = [
    { id: "orders", label: "Orders and delivery" },
    { id: "billing", label: "Billing and refunds" },
    { id: "account", label: "Account and sign-in" },
    { id: "other", label: "Something else" },
  ];
  const variants: Record<string, Partial<WidgetConfig>> = {
    wk_brand: { brand_color: "#3b5bdb" },
    wk_email: { require_email: true },
    wk_left: { position: "left", offset_x: 32, offset_y: 28, brand_color: "#0f766e" },
    wk_label: { launcher: "label", launcher_label: "", brand_color: "#7c3aed" },
    wk_round: { radius: "round", launcher: "label", launcher_label: "Ask us", brand_color: "#e8590c" },
    wk_square: { radius: "square", brand_color: "#111827" },
    wk_dark: { theme: "dark", brand_color: "#a3e635", title: "Margin support", subtitle: "Ask Ollie anything, day or night." },
    wk_light: { theme: "light" },
    wk_human: { ai_answers: false, suggestions: [] },
    wk_bot: { bot: PIP, brand_color: "#0f766e" },
    wk_emoji: { bot: FOX },
    wk_topics: { topics },
    wk_files: { attachment_extensions: [".png", ".jpg", ".jpeg", ".pdf"] },
    wk_nofiles: { attachments: false, attachment_extensions: [] },
    wk_closed: { open: false, topics },
    wk_offline: {
      open: false,
      offline_message: "We're offline for the weekend.\nLeave a message and we'll reply on Monday morning.",
      intro: "Ask about orders, delivery or returns.\nLeave your email and we'll write back.",
    },
    wk_press: {
      workspace_name: "Pressroom",
      title: "",
      brand_color: "#e11d48",
      brand: { name: "Pressroom", color: "#e11d48" },
      suggestions: ["Custom domains", "Import subscribers"],
      topics: [
        { id: "publishing", label: "Publishing help" },
        { id: "payouts", label: "Payments and payouts" },
      ],
    },
  };
  return { ...base, ...(variants[key] ?? {}) };
}

export type MockOptions = {
  /** Language of the seeded content (?od_lang=). */
  lang?: string;
  /** "demo": one past conversation; "preview": the dashboard preview's showcase conversation; "empty": none. */
  seed?: "demo" | "preview" | "empty";
  /** Answer GET /v1/widget/config with this (demo pages); the preview passes its config to boot. */
  config?: (key: string) => WidgetConfig;
  /** Network latency for API calls, ms. */
  latency?: number;
  /**
   * The bot replies and live events name. Default: the served config's bot.
   * null names none, so the widget shows its configured bot (the dashboard
   * preview, which follows unsaved edits).
   */
  bot?: BotRef | null;
};

export type Mock = {
  fetch: Fetcher;
  socket: SocketFactory;
  /** The preview's showcase: stream the agent's reply in the seeded conversation (once). */
  playShowcase(): void;
  /** Id of the seeded preview conversation. */
  showcaseId: string;
  /** The topic_id of the last conversation started (null: none was sent). */
  readonly lastTopic: string | null;
  /** The team takes back its latest reply in the latest conversation (deleted, or made a note): it leaves the visitor's chat. */
  takeBack(): boolean;
  dispose(): void;
};

export function createMock(o: MockOptions = {}): Mock {
  const copy = COPY[o.lang ?? ""] ?? COPY.en;
  const contact = { id: "00000000-0000-4000-8000-0000000c0de0", email: null as string | null, name: "", avatar_url: "" };
  const agent = { id: "u_priya", name: "Priya Raman", avatar_url: "" };
  const latency = o.latency ?? 120;
  let served: WidgetConfig | null = null;
  const bot = (): BotRef | null => (o.bot !== undefined ? o.bot : refOf(served?.bot ?? OLLIE));
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const later = (ms: number, f: () => void) => {
    const id = setTimeout(() => {
      timers.delete(id);
      f();
    }, ms);
    timers.add(id);
  };
  const ago = (m: number) => new Date(Date.now() - m * 60_000).toISOString();
  const b64 = (x: object) => btoa(JSON.stringify(x)).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
  const wsToken = () => `${b64({ alg: "HS256", typ: "JWT" })}.${b64({ purpose: "widget_ws", sub: contact.id, wid: WID, exp: Date.now() / 1000 + 300 })}.mock`;
  let n = 0;
  const uid = () => {
    const h = (++n).toString(16).padStart(4, "0") + Math.random().toString(16).slice(2, 10);
    return `${h.slice(0, 8)}-0000-4000-8000-${Date.now().toString(16).slice(-12).padStart(12, "0")}`;
  };

  // Uploaded files, attached to the message that names them.
  const uploads: Record<string, Attachment> = {};
  const attached = (ids: unknown) => (Array.isArray(ids) ? ids.map((id) => uploads[String(id)]).filter(Boolean) : []);

  function msg(ticket_id: string, dir: "inbound" | "outbound", text: string, minsAgo = 0, html = ""): Message {
    return {
      id: uid(),
      ticket_id,
      kind: "reply",
      direction: dir,
      author: dir === "inbound" ? { type: "contact", user: null, contact } : { type: "user", user: agent, contact: null },
      body_html: html,
      body_text: text,
      attachments: [],
      created_at: ago(minsAgo),
      ai: false,
      ai_action: null,
      ai_rating: null,
    };
  }

  /** The agent's posted message: markdown to simple HTML, markers removed, cited sources listed under it. */
  function aiMsg(tid: string, md: string, sources: { title: string; url: string }[] = [], action: Omit<AIAction, "id" | "status"> | null = null, minsAgo = 0): Message {
    const clean = md.replace(/\s*\[\d+\]/g, "");
    const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    const html =
      clean
        .split(/\n{2,}/)
        .map((p) => `<p>${esc(p).replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")}</p>`)
        .join("") + (sources.length ? `<ul>${sources.map((s) => `<li><a href="${s.url}">${esc(s.title)}</a></li>`).join("")}</ul>` : "");
    return {
      ...msg(tid, "outbound", clean.replace(/\*\*/g, ""), minsAgo, html),
      author: { type: "system", user: null, contact: null, bot: bot() },
      ai: true,
      ai_action: action ? { ...action, id: uid(), status: "pending" } : null,
    };
  }

  const conversations: Conversation[] = [];
  const messages: Record<string, Message[]> = {};
  const showcaseId = uid();

  if (o.seed === "preview") {
    const q = msg(showcaseId, "inbound", "Hi! Can I still change where my order ships?", 3);
    const a = aiMsg(
      showcaseId,
      "Yes, as long as it hasn't shipped yet [1]. Tell me the order number and the new address, and I'll take care of it.",
      [{ title: "Changing a delivery address", url: `${HELP}/change-address` }],
      null,
      2.8,
    );
    const q2 = msg(showcaseId, "inbound", ORDER.question, 1);
    conversations.push({ id: showcaseId, subject: "Changing my delivery address", status: "open", last_message_preview: ORDER.question, last_message_at: q2.created_at });
    messages[showcaseId] = [q, a, q2];
  } else if (o.seed !== "empty") {
    const cv = uid();
    conversations.push({ id: cv, subject: copy.subject, status: "solved", last_message_preview: copy.a2, last_message_at: ago(60 * 26) });
    messages[cv] = [msg(cv, "inbound", copy.q1, 60 * 27), msg(cv, "outbound", "", 60 * 26.5, copy.a1), msg(cv, "inbound", copy.q2, 60 * 26.2), msg(cv, "outbound", copy.a2, 60 * 26)];
  }

  // One fake channel per topic; the fetch handler pushes into it.
  const channels: { topic: string; handlers: Record<string, ((p: unknown) => void)[]> }[] = [];
  const broadcast = (event: string, payload: unknown) => {
    for (const ch of channels) (ch.handlers[event] ?? []).forEach((h) => h(payload));
  };
  const publish = (type: "message.created" | "message.updated", m: Message) =>
    broadcast("event", { id: uid(), seq: n, type, data: { ticket_id: m.ticket_id, message: m }, topics: [] });
  const live = (type: string, data: Record<string, unknown>) => broadcast("live", { type, data });
  const conv = (id: string) => conversations.find((c) => c.id === id);

  function post(m: Message) {
    (messages[m.ticket_id] ??= []).push(m);
    const c = conv(m.ticket_id);
    if (c) {
      c.last_message_at = m.created_at;
      c.last_message_preview = m.body_text.slice(0, 140);
    }
    publish("message.created", m);
  }

  /**
   * Stream one agent reply: started, optional tool steps, the text word by
   * word, then ended and the posted message (as the API does).
   */
  function stream(
    tid: string,
    md: string,
    opts: { tool?: string; delay?: number; sources?: { title: string; url: string }[]; action?: Omit<AIAction, "id" | "status">; handoff?: boolean } = {},
  ) {
    const reply = uid();
    let at = opts.delay ?? 700;
    later(at, () => live("ai.reply.started", { reply_id: reply, ticket_id: tid, bot: bot() }));
    if (opts.tool) {
      at += 500;
      later(at, () => live("ai.reply.tool", { reply_id: reply, ticket_id: tid, tool: opts.tool, status: "running" }));
      at += 1300;
      later(at, () => live("ai.reply.tool", { reply_id: reply, ticket_id: tid, tool: opts.tool, status: "done" }));
    }
    at += 400;
    // Words with their trailing space, in small uneven chunks like a model's.
    const words = md.match(/\S+\s*/g) ?? [];
    for (let i = 0; i < words.length; ) {
      const size = 1 + ((i * 7) % 3);
      const chunk = words.slice(i, i + size).join("");
      i += size;
      at += 45 + ((i * 13) % 60);
      later(at, () => live("ai.reply.delta", { reply_id: reply, ticket_id: tid, text: chunk }));
    }
    at += 250;
    const m = aiMsg(tid, md, opts.sources ?? [], opts.action ?? null);
    later(at, () => {
      live("ai.reply.ended", { reply_id: reply, ticket_id: tid, message_id: m.id, handoff: !!opts.handoff, action_id: m.ai_action?.id ?? null });
      if (opts.handoff) {
        const c = conv(tid);
        if (c) c.ai_handoff = true;
      }
    });
    later(at + 120, () => post(m));
    if (opts.handoff) humanReply(tid, at + 3500);
  }

  function humanReply(tid: string, at = 1500) {
    later(at, () => broadcast("typing", { ticket_id: tid, user: { id: agent.id, name: "Priya" } }));
    later(at + 2600, () => post(msg(tid, "outbound", copy.human)));
  }

  /** The agent's turn after a visitor message, picked by what they asked. */
  function agentTurn(tid: string, text: string) {
    const c = conv(tid);
    if (!c || c.ai_handoff || messages[tid]?.some((m) => m.author.type === "user")) return;
    if (/person|human|someone|agent|real/i.test(text)) {
      stream(tid, "Of course. I've asked the team to join; a person will reply right here.", { handoff: true });
    } else if (/order|address|deliver|ship|#\d+/i.test(text)) {
      if (/#?\d{4}|street|road|avenue/i.test(text)) stream(tid, ORDER.lookup, { tool: "Order lookup", action: ORDER.action, delay: 900 });
      else stream(tid, "Happy to help with that. What's your order number, and where should it go?", { tool: "Order lookup" });
    } else if (/sso|sign|log ?in|saml|ログイン|サインイン|تسجيل/i.test(text)) {
      stream(tid, copy.aiSso, {
        tool: "Knowledge search",
        sources: [
          { title: copy.s1, url: `${HELP}/saml-sso` },
          { title: copy.s2, url: `${HELP}/rotate-sso-certificate` },
        ],
      });
    } else {
      stream(tid, "Thanks for the message! I can look up **orders**, change a delivery address, or point you to the right guide [1]. What do you need?", {
        sources: [{ title: "Getting started with Margin", url: `${HELP}/getting-started` }],
      });
    }
  }

  const params = typeof location === "undefined" ? new URLSearchParams() : new URLSearchParams(location.search);
  const botOn = () => params.get("od_bot") !== "0";
  const replies = [
    "Thanks for reaching out! Let me take a look at that for you.",
    "Good question. You can find that under Settings → Workspace. Want me to walk you through it?",
    "I've passed this on to our engineers and will update you here as soon as I hear back.",
  ];
  let replyIdx = 0;
  function teamTurn(tid: string) {
    later(900, () => broadcast("typing", { ticket_id: tid, user: { id: agent.id, name: "Priya" } }));
    later(3200, () => broadcast("typing", { ticket_id: tid, user: { id: agent.id, name: "Priya" } }));
    later(4600, () => post(msg(tid, "outbound", replies[replyIdx++ % replies.length])));
  }

  let lastTopic: string | null = null;
  const json = (body: unknown, status = 200) => new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  const notFound = () => json({ status: 404, title: "Not found", code: "not_found" }, 404);

  const fetcher: Fetcher = async (url, init) => {
    await new Promise((r) => setTimeout(r, latency));
    const u = new URL(url, typeof location === "undefined" ? "https://mock.invalid" : location.href);
    const path = u.pathname;
    const method = (init.method ?? "GET").toUpperCase();
    const body = typeof init.body === "string" ? (JSON.parse(init.body) as Record<string, unknown>) : {};

    if (path === "/v1/widget/config") {
      served = (o.config ?? ((k) => demoConfig(k, params)))(u.searchParams.get("key") ?? "");
      return json(served);
    }
    if (path === "/v1/widget/session") {
      if (typeof body.email === "string") contact.email = body.email;
      if (typeof body.name === "string") contact.name = body.name;
      return json({ visitor_token: "od_vis_mock", contact: { ...contact }, ws_token: wsToken(), gateway_url: "wss://mock.invalid/socket/widget" });
    }
    if (path === "/v1/widget/conversations" && method === "GET") return json({ data: conversations, next_cursor: null });
    if (path === "/v1/widget/conversations" && method === "POST") {
      // topic_id: one of WidgetConfig.topics; ids are 1..40 of A-Z a-z 0-9 _ - (contracts/api.md).
      if (body.topic_id !== undefined && (typeof body.topic_id !== "string" || !/^[A-Za-z0-9_-]{1,40}$/.test(body.topic_id))) {
        return json({ status: 422, title: "Unprocessable Entity", code: "validation_failed", detail: "topic_id is not a topic of this widget" }, 422);
      }
      lastTopic = typeof body.topic_id === "string" ? body.topic_id : null;
      const cid = uid();
      const text = String(body.body ?? "");
      const c: Conversation = { id: cid, subject: text.slice(0, 60) || "New conversation", status: "open", last_message_preview: text, last_message_at: ago(0) };
      conversations.unshift(c);
      const first = { ...msg(cid, "inbound", text), attachments: attached(body.attachment_ids) };
      messages[cid] = [first];
      if (botOn()) agentTurn(cid, text);
      else teamTurn(cid);
      return json({ conversation: c, message: first }, 201);
    }
    let m = /^\/v1\/widget\/conversations\/([^/]+)\/typing$/.exec(path);
    if (m && method === "POST") return conv(m[1]) ? json(null, 204) : notFound();
    m = /^\/v1\/widget\/conversations\/([^/]+)\/handoff$/.exec(path);
    if (m && method === "POST") {
      const c = conv(m[1]);
      if (!c) return notFound();
      if (!c.ai_handoff) {
        c.ai_handoff = true;
        humanReply(c.id);
      }
      return json(null, 204);
    }
    m = /^\/v1\/widget\/conversations\/([^/]+)\/actions\/([^/]+)$/.exec(path);
    if (m && method === "POST") {
      const [, cid, aid] = m;
      const am = messages[cid]?.find((x) => x.ai_action?.id === aid);
      if (!am?.ai_action) return notFound();
      if (am.ai_action.status !== "pending") return json({ status: 409, title: "Conflict", code: "action_not_pending" }, 409);
      const confirm = body.decision === "confirm";
      am.ai_action = { ...am.ai_action, status: confirm ? "confirmed" : "declined" };
      later(300, () => publish("message.updated", am));
      stream(cid, confirm ? ORDER.confirmed : ORDER.declined, confirm ? { tool: "Orders", delay: 500 } : { delay: 500 });
      return json(null, 204);
    }
    m = /^\/v1\/widget\/conversations\/([^/]+)\/messages\/([^/]+)\/rating$/.exec(path);
    if (m && method === "POST") {
      const rm = messages[m[1]]?.find((x) => x.id === m![2] && x.ai);
      if (!rm) return notFound();
      rm.ai_rating = body.rating === "up" || body.rating === "down" ? body.rating : null;
      later(200, () => publish("message.updated", rm));
      return json(null, 204);
    }
    m = /^\/v1\/widget\/conversations\/([^/]+)\/messages$/.exec(path);
    if (m) {
      const cid = m[1];
      if (method === "GET") return json({ data: messages[cid] ?? [], next_cursor: null });
      const text = String(body.body ?? "");
      const reply = { ...msg(cid, "inbound", text), attachments: attached(body.attachment_ids) };
      // Writing again expires a pending action (contracts/agent.md).
      for (const x of messages[cid] ?? [])
        if (x.ai_action?.status === "pending") {
          x.ai_action = { ...x.ai_action, status: "expired" };
          later(150, () => publish("message.updated", x));
        }
      (messages[cid] ??= []).push(reply);
      const c = conv(cid);
      if (c) {
        c.last_message_at = reply.created_at;
        c.last_message_preview = text;
      }
      if (c && !c.ai_handoff && botOn() && !messages[cid].some((x) => x.author.type === "user")) agentTurn(cid, text);
      else teamTurn(cid);
      return json(reply, 201);
    }
    if (path === "/v1/widget/attachments" && init.body instanceof FormData) {
      const f = init.body.get("file") as File;
      const a: Attachment = { id: uid(), filename: f.name, content_type: f.type, size_bytes: f.size, inline: false, url: URL.createObjectURL(f) };
      uploads[a.id] = a;
      return json(a, 201);
    }
    return notFound();
  };

  const socket: SocketFactory = () => {
    const opens: (() => void)[] = [];
    const sock: RtSocketLike = {
      connect: () => later(50, () => opens.forEach((f) => f())),
      disconnect: () => {
        channels.length = 0;
      },
      onOpen: (cb) => opens.push(cb),
      onError: () => {},
      onClose: () => {},
      channel(topic) {
        const ch = { topic, handlers: {} as Record<string, ((p: unknown) => void)[]> };
        channels.push(ch);
        const reply = { receive: (status: string, cb: (r: unknown) => void) => (status === "ok" && later(30, () => cb({})), reply) };
        const api: RtChannelLike = {
          join: () => reply,
          on: (event, cb) => ((ch.handlers[event] ??= []).push(cb), 0),
          push: () => reply,
          leave: () => {
            const i = channels.indexOf(ch);
            if (i >= 0) channels.splice(i, 1);
          },
        };
        return api;
      },
    };
    return sock;
  };

  let played = false;
  return {
    get lastTopic() {
      return lastTopic;
    },
    fetch: fetcher,
    socket,
    showcaseId,
    takeBack() {
      const latest = [...conversations].sort((a, b) => Date.parse(b.last_message_at) - Date.parse(a.last_message_at))[0];
      const list = latest ? messages[latest.id] : undefined;
      const i = list ? list.map((m) => m.direction).lastIndexOf("outbound") : -1;
      if (!latest || !list || i < 0) return false;
      const [gone] = list.splice(i, 1);
      const last = list[list.length - 1];
      latest.last_message_preview = last ? last.body_text.slice(0, 140) : "";
      broadcast("event", { id: uid(), seq: n, type: "message.withdrawn", data: { ticket_id: latest.id, message_id: gone.id }, topics: [] });
      return true;
    },
    playShowcase() {
      if (played) return;
      played = true;
      stream(showcaseId, ORDER.lookup, { tool: "Order lookup", action: ORDER.action, delay: 500 });
    },
    dispose() {
      timers.forEach(clearTimeout);
      timers.clear();
      channels.length = 0;
    },
  };
}

// The demo page's backend (main.tsx imports these in mock builds).
let demo: Mock | null = null;
const demoMock = () =>
  (demo ??= createMock({ lang: typeof location === "undefined" ? "" : (new URLSearchParams(location.search).get("od_lang") ?? ""), seed: "demo" }));
export const mockFetch: Fetcher = (url, init) => demoMock().fetch(url, init);
export const mockSocket: SocketFactory = (url, params) => demoMock().socket(url, params);
/** The team takes back its latest reply in the demo (the proof flow dispatches `opendesk-mock:take-back`). */
export const mockTakeBack = () => demoMock().takeBack();
