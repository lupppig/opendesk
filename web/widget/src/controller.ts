// Everything the widget does that isn't drawing: the session, conversations,
// sending (optimistic, with retry), the agent's live replies, actions,
// ratings, and applying realtime events.

import { WidgetApi, WidgetError, type Fetcher } from "./api";
import { isAiAnswer } from "./ai";
import { pageUrl, readBotRef, resolveConfig } from "./config";
import { t } from "./i18n";
import { dropLive, liveMessage, reduceLive } from "./live";
import { plainText } from "./markdown";
import { Live, type SocketFactory } from "./realtime";
import { chime } from "./sound";
import { load, save } from "./storage";
import { Store } from "./store";
import type { AIAction, Attachment, Envelope, LiveEvent, Message, Rating, SessionIdentity, UiMessage, WidgetConfig } from "./types";

export type Env = {
  base: string;
  key: string;
  fetcher: Fetcher;
  socketFactory: SocketFactory;
  /** Keep the visitor token across reloads (off for the dashboard preview). */
  persist?: boolean;
  store?: Store;
};
export type EventName = "message" | "open" | "close";
type Listener = (payload?: unknown) => void;

export const NEW = "__new";
const TYPING_MS = 5000;
/** A reply still writing with no event for this long lost its end (a dropped connection). */
const LIVE_IDLE_MS = 30_000;
/** An ended reply waits this long for its message before the thread is fetched again. */
const LIVE_DONE_MS = 10_000;
const FADE_MS = 200;

let localSeq = 0;

export class Controller {
  readonly api: WidgetApi;
  readonly store: Store;
  private live: Live | null = null;
  private identity: SessionIdentity = {};
  private listeners: Record<EventName, Set<Listener>> = { message: new Set(), open: new Set(), close: new Set() };
  private typingTimers: Record<string, ReturnType<typeof setTimeout>> = {};
  private sweepTimer: ReturnType<typeof setInterval> | null = null;
  private storageKey: string;
  private persist: boolean;
  /** Called with screen-reader text: once per completed reply, never per streamed chunk. */
  announce: (text: string) => void = () => {};

  constructor(private env: Env) {
    this.api = new WidgetApi(env.base, env.key, env.fetcher);
    this.store = env.store ?? new Store();
    this.persist = env.persist !== false;
    this.storageKey = `od_widget_${env.key}`;
    this.api.visitorToken = this.persist ? load(this.storageKey) : null;
  }

  private get s() {
    return this.store.get();
  }
  private set(patch: Parameters<Store["set"]>[0]) {
    this.store.set(patch);
  }
  private remember(token: string | null) {
    if (this.persist) save(this.storageKey, token);
  }

  on(name: EventName, cb: Listener) {
    this.listeners[name]?.add(cb);
  }

  private emit(name: EventName, payload?: unknown) {
    this.listeners[name].forEach((cb) => {
      try {
        cb(payload);
      } catch {
        /* a host callback must not break the widget */
      }
    });
  }

  /** Load the config (or use the given one) and start a session. */
  async boot(config?: WidgetConfig) {
    try {
      this.setConfig(config ?? (await this.api.config()));
      await this.startSession();
    } catch (e) {
      this.set({ error: e instanceof Error ? e.message : t("unavailable") });
    } finally {
      this.set({ booting: false });
    }
  }

  setConfig(raw: Partial<WidgetConfig>) {
    this.set({ config: resolveConfig(raw) });
  }

  private async session() {
    try {
      return await this.api.session(this.identity);
    } catch (e) {
      // A stale or revoked visitor token: start over as a new visitor.
      if (e instanceof WidgetError && (e.status === 401 || e.status === 403 || e.status === 404) && this.api.visitorToken) {
        this.api.visitorToken = null;
        this.remember(null);
        return this.api.session(this.identity);
      }
      throw e;
    }
  }

  async startSession() {
    const s = await this.session();
    this.remember(s.visitor_token);
    this.set({ session: s, error: null });
    this.live?.stop();
    this.live = new Live(
      this.env.socketFactory,
      // Already the widget endpoint (…/socket/widget); used exactly as returned.
      s.gateway_url,
      s.ws_token,
      s.contact.id,
      async () => {
        const fresh = await this.session();
        this.set({ session: fresh });
        return fresh.ws_token;
      },
      {
        onEvent: (e) => this.onEvent(e),
        onTyping: (p) => this.onTyping(p.ticket_id, p.user?.name ?? t("support")),
        onLive: (e) => this.onLive(e),
        onReconnect: () => void this.catchUp(),
        sendTyping: (id) => this.api.typing(id),
      },
    );
    this.live.start();
    if (!this.sweepTimer) this.sweepTimer = setInterval(() => this.sweep(), 4000);
    await this.loadConversations();
  }

  async identify(identity: SessionIdentity) {
    this.identity = { ...this.identity, ...identity };
    if (this.s.booting) return; // boot picks it up
    try {
      await this.startSession();
    } catch (e) {
      this.set({ error: e instanceof Error ? e.message : t("unavailable") });
    }
  }

  /** After a dropped connection, fetch what arrived while we were away. */
  private async catchUp() {
    await this.loadConversations();
    const id = this.s.active;
    if (id) await this.refresh(id);
  }

  async loadConversations() {
    try {
      const list = await this.api.conversations();
      list.sort((a, b) => Date.parse(b.last_message_at) - Date.parse(a.last_message_at));
      this.set({ conversations: list });
    } catch {
      this.set((s) => ({ conversations: s.conversations ?? [] }));
    }
  }

  open() {
    if (this.s.open) return;
    this.set({ open: true, unread: 0 });
    this.emit("open");
  }
  close() {
    if (!this.s.open) return;
    this.set({ open: false });
    this.emit("close");
  }
  toggle() {
    if (this.s.open) this.close();
    else this.open();
  }

  home() {
    this.set({ view: "home", active: null });
    void this.loadConversations();
  }

  /** A new conversation; with `topic`, already about one of config.topics (the home screen's topic list). */
  newConversation(topic: string | null = null) {
    this.set((s) => ({ view: "thread", active: null, topic, queued: null, messages: { ...s.messages, [NEW]: s.messages[NEW] ?? [] } }));
  }

  /** A suggestion chip: start a conversation with that text, once the email and topic steps are done. */
  async startWith(text: string) {
    this.newConversation();
    this.set({ queued: text });
    await this.flushQueued();
  }

  /** The widget has topics and the new conversation has none yet: ask before the first message. */
  needsTopic(): boolean {
    const { config, active, topic } = this.s;
    return !active && !topic && !!config?.topics?.length;
  }

  /** Pick the new conversation's topic (null asks again); a waiting suggestion goes out with it. */
  async pickTopic(id: string | null) {
    if (this.s.active) return;
    this.set({ topic: id });
    await this.flushQueued();
  }

  private async flushQueued() {
    const text = this.s.queued;
    if (!text || this.needsEmail() || this.needsTopic()) return;
    this.set({ queued: null });
    await this.send(text, []);
  }

  async openConversation(id: string) {
    this.set({ view: "thread", active: id });
    await this.refresh(id);
  }

  private async refresh(id: string) {
    try {
      const msgs = await this.api.messages(id);
      this.applyMessages(id, msgs, true);
    } catch {
      this.set((s) => ({ messages: { ...s.messages, [id]: s.messages[id] ?? [] } }));
    }
  }

  /** Merge server messages into a thread and let them replace any live preview. */
  private applyMessages(tid: string, msgs: Message[], create: boolean) {
    this.set((s) => {
      let live = s.live;
      let swapped = s.swapped;
      for (const m of msgs) {
        const r = liveMessage(live, m);
        live = r.state;
        if (r.replaced) swapped = { ...swapped, [m.id]: true };
      }
      const patch: Partial<typeof s> = { live, swapped };
      if (s.messages[tid] || create) patch.messages = { ...s.messages, [tid]: mergeServer(s.messages[tid] ?? [], msgs) };
      // The server caught up with optimistic ratings and action decisions.
      let ratings = s.ratings;
      let actions = s.actions;
      for (const m of msgs) {
        if (m.id in ratings && (m.ai_rating ?? null) === ratings[m.id]) {
          ratings = { ...ratings };
          delete ratings[m.id];
        }
        const a = m.ai_action;
        if (a && a.status !== "pending" && actions[a.id]) {
          actions = { ...actions };
          delete actions[a.id];
        }
      }
      patch.ratings = ratings;
      patch.actions = actions;
      return patch;
    });
  }

  /** "Talk to a person": hand the conversation from the AI agent to the team. */
  async handoff(id: string) {
    if (this.s.handoff[id] === "pending" || this.s.handoff[id] === "done") return;
    this.set((s) => ({ handoff: { ...s.handoff, [id]: "pending" } }));
    try {
      await this.api.handoff(id);
      this.markHandedOff(id);
      this.announce(t("handoffDone"));
    } catch {
      this.set((s) => ({ handoff: { ...s.handoff, [id]: "failed" } }));
    }
  }

  private markHandedOff(id: string) {
    this.set((s) => {
      const awaiting = { ...s.awaiting };
      delete awaiting[id];
      return {
        handoff: { ...s.handoff, [id]: "done" },
        awaiting,
        conversations: (s.conversations ?? []).map((c) => (c.id === id ? { ...c, ai_handoff: true } : c)),
      };
    });
  }

  /** Confirm or decline an action card. Optimistic; `message.updated` brings the final status. */
  async decide(convId: string, action: AIAction, decision: "confirm" | "decline") {
    const cur = this.s.actions[action.id];
    if (cur?.busy || cur?.status) return;
    this.set((s) => ({ actions: { ...s.actions, [action.id]: { busy: decision } } }));
    try {
      await this.api.decide(convId, action.id, decision);
      this.set((s) => ({
        actions: { ...s.actions, [action.id]: { status: decision === "confirm" ? "confirmed" : "declined" } },
        // The agent continues with a new reply either way.
        awaiting: { ...s.awaiting, [convId]: Date.now() },
      }));
    } catch (e) {
      if (e instanceof WidgetError && e.status === 409) {
        // No longer pending (expired, or decided elsewhere): show what the server says.
        this.set((s) => {
          const actions = { ...s.actions };
          delete actions[action.id];
          return { actions };
        });
        void this.refresh(convId);
      } else this.set((s) => ({ actions: { ...s.actions, [action.id]: { error: true } } }));
    }
  }

  /** Rate an agent reply; the same rating again clears it. */
  async rate(convId: string, m: Message, rating: Rating) {
    const was = m.id in this.s.ratings ? this.s.ratings[m.id] : (m.ai_rating ?? null);
    const next = was === rating ? null : rating;
    this.set((s) => ({ ratings: { ...s.ratings, [m.id]: next } }));
    try {
      await this.api.rate(convId, m.id, next);
    } catch {
      this.set((s) => {
        if (s.ratings[m.id] !== next) return {};
        const ratings = { ...s.ratings };
        delete ratings[m.id];
        return { ratings };
      });
    }
  }

  async captureEmail(name: string, email: string) {
    this.identity = { ...this.identity, name: name || undefined, email };
    const s = await this.session();
    this.remember(s.visitor_token);
    this.set({ session: s });
    await this.flushQueued();
  }

  needsEmail(): boolean {
    const { config, session } = this.s;
    return !!config?.require_email && !!session && !session.contact.email;
  }

  typing() {
    const { active } = this.s;
    if (active) this.live?.typing(active);
  }

  async send(text: string, files: File[]) {
    const body = text.trim();
    if (!body && !files.length) return;
    const { active, session } = this.s;
    const key = active ?? NEW;
    const local: UiMessage = {
      id: `local-${++localSeq}`,
      ticket_id: key,
      kind: "reply",
      direction: "inbound",
      author: { type: "contact", user: null, contact: session?.contact ?? null },
      body_html: "",
      body_text: body,
      attachments: files.map((f, i) => ({
        id: `local-file-${localSeq}-${i}`,
        filename: f.name,
        content_type: f.type,
        size_bytes: f.size,
        inline: false,
        url: f.type.startsWith("image/") || f.type.startsWith("audio/") ? URL.createObjectURL(f) : "",
      })),
      created_at: new Date().toISOString(),
      local: "pending",
      localFiles: files,
    };
    this.set((s) => ({ messages: { ...s.messages, [key]: [...(s.messages[key] ?? []), local] } }));
    await this.deliver(key, local);
  }

  async retry(key: string, localId: string) {
    const m = this.s.messages[key]?.find((x) => x.id === localId);
    if (!m) return;
    this.patchLocal(key, localId, { local: "pending" });
    await this.deliver(key, m);
  }

  private patchLocal(key: string, id: string, patch: Partial<UiMessage>) {
    this.set((s) => ({ messages: { ...s.messages, [key]: (s.messages[key] ?? []).map((m) => (m.id === id ? { ...m, ...patch } : m)) } }));
  }

  private async deliver(key: string, local: UiMessage) {
    // A sent message ends the typing burst: the next keystroke signals again.
    this.live?.typingDone();
    const page = this.s.preview ? undefined : pageUrl();
    try {
      const uploaded: Attachment[] = [];
      for (const f of local.localFiles ?? []) uploaded.push(await this.api.upload(f));
      const ids = uploaded.map((a) => a.id);
      if (key === NEW) {
        const { conversation, message } = await this.api.start(local.body_text, ids.length ? ids : undefined, page, this.s.topic ?? undefined);
        this.set((s) => {
          const rest = { ...s.messages };
          delete rest[NEW];
          const thread = message ? [message] : [];
          return {
            active: s.active === null && s.view === "thread" ? conversation.id : s.active,
            messages: { ...rest, [conversation.id]: mergeServer(s.messages[conversation.id] ?? [], thread) },
            conversations: [conversation, ...(s.conversations ?? []).filter((c) => c.id !== conversation.id)],
            awaiting: { ...s.awaiting, [conversation.id]: Date.now() },
          };
        });
        if (!message) void this.openConversation(conversation.id);
      } else {
        const msg = await this.api.send(key, local.body_text, ids.length ? ids : undefined, page);
        this.set((s) => ({
          messages: { ...s.messages, [key]: upsert((s.messages[key] ?? []).filter((m) => m.id !== local.id), msg) },
          awaiting: { ...s.awaiting, [key]: Date.now() },
        }));
        this.touchConversation(key, msg);
      }
    } catch {
      this.patchLocal(key, local.id, { local: "failed" });
    }
  }

  private touchConversation(id: string, m: Message) {
    this.set((s) => {
      const list = s.conversations ?? [];
      const c = list.find((x) => x.id === id);
      if (!c) return {};
      const next = { ...c, last_message_at: m.created_at, last_message_preview: m.body_text.slice(0, 140) };
      return { conversations: [next, ...list.filter((x) => x.id !== id)] };
    });
  }

  private onTyping(ticketId: string, name: string) {
    this.set((s) => ({ typing: { ...s.typing, [ticketId]: name } }));
    clearTimeout(this.typingTimers[ticketId]);
    this.typingTimers[ticketId] = setTimeout(() => this.clearTyping(ticketId), TYPING_MS);
  }

  private clearTyping(ticketId: string) {
    clearTimeout(this.typingTimers[ticketId]);
    this.set((s) => {
      if (!(ticketId in s.typing)) return {};
      const next = { ...s.typing };
      delete next[ticketId];
      return { typing: next };
    });
  }

  private clearAwaiting(tid: string) {
    this.set((s) => {
      if (!(tid in s.awaiting)) return {};
      const next = { ...s.awaiting };
      delete next[tid];
      return { awaiting: next };
    });
  }

  /** A `"live"` push: the agent's reply as it is generated. */
  onLive(e: LiveEvent) {
    const before = this.s.live;
    const tid = e.data?.ticket_id;
    this.set((s) => ({
      live: reduceLive(s.live, e, Date.now(), (mid) => Object.values(s.messages).some((list) => list.some((m) => m.id === mid))),
    }));
    if (e.type !== "ai.reply.ended" || this.s.live === before || typeof tid !== "string") return;
    this.clearAwaiting(tid);
    if (e.data.handoff) this.markHandedOff(tid);
    const r = this.s.live.replies[e.data.reply_id];
    if (r?.phase === "leaving") setTimeout(() => this.set((s) => ({ live: dropLive(s.live, { id: r.id }) })), FADE_MS);
  }

  /** Drop previews whose end or message was missed; fetch the thread when a message didn't arrive. */
  private sweep() {
    const now = Date.now();
    const { live, active } = this.s;
    const stale = Object.values(live.replies).filter((r) => (r.phase === "done" ? r.at < now - LIVE_DONE_MS : r.at < now - LIVE_IDLE_MS));
    if (!stale.length) return;
    this.set((s) => ({ live: dropLive(s.live, { idleBefore: now - LIVE_IDLE_MS, doneBefore: now - LIVE_DONE_MS }) }));
    if (active && stale.some((r) => r.ticket_id === active)) void this.refresh(active);
  }

  onEvent(e: Envelope) {
    // A reply the team took back (deleted, or turned into a note) leaves the conversation.
    if (e.type === "message.withdrawn") {
      const tid = e.data.ticket_id as string | undefined;
      const mid = e.data.message_id as string | undefined;
      if (tid && mid && this.s.messages[tid]) this.set((s) => ({ messages: { ...s.messages, [tid]: s.messages[tid].filter((m) => m.id !== mid) } }));
      return;
    }
    if (e.type !== "message.created" && e.type !== "message.updated") return;
    const m = e.data.message as Message | undefined;
    const tid = (e.data.ticket_id as string | undefined) ?? m?.ticket_id;
    if (!m || !tid || m.kind === "note") return;
    const s = this.s;
    this.applyMessages(tid, [m], false);
    if (e.type === "message.updated") return;
    if (s.conversations && !s.conversations.some((c) => c.id === tid)) void this.loadConversations();
    else this.touchConversation(tid, m);
    if (m.direction !== "outbound") return;
    this.clearTyping(tid);
    this.clearAwaiting(tid);
    const viewing = s.open && s.view === "thread" && s.active === tid;
    if (!s.open) this.set((st) => ({ unread: st.unread + 1 }));
    else if (!viewing) void this.loadConversations();
    // An agent's reply read on screen: tell the API the visitor is here, so it is not emailed too.
    if (viewing && !s.preview && m.author.type === "user" && typeof document !== "undefined" && !document.hidden) {
      void this.api.seen(tid).catch(() => {});
    }
    if (s.config?.sound && !s.preview && typeof document !== "undefined" && document.hidden) chime();
    const ai = isAiAnswer(m);
    const who = m.author.user?.name ?? (ai ? readBotRef(m.author.bot)?.name || s.config?.bot?.name || t("ai") : s.config?.workspace_name || t("support"));
    const text = (m.body_text || plainText(m.body_html.replace(/<[^>]+>/g, " "))).slice(0, 200);
    this.announce(t("newMessage", { name: who, text }));
    this.emit("message", { conversation_id: tid, body: m.body_text, from: who });
  }

  shutdown() {
    this.stop();
    // The host page signed its user out: end the session on the server too, so the
    // token stops working even where a copy of it survives.
    void this.api.endSession().catch(() => {});
    this.remember(null);
    this.api.visitorToken = null;
  }

  /** Stop the connection and timers (the preview's destroy). */
  stop() {
    this.live?.stop();
    this.live = null;
    if (this.sweepTimer) clearInterval(this.sweepTimer);
    this.sweepTimer = null;
    Object.values(this.typingTimers).forEach(clearTimeout);
  }
}

function upsert(list: UiMessage[], m: Message): UiMessage[] {
  const i = list.findIndex((x) => x.id === m.id);
  if (i >= 0) return list.map((x, j) => (j === i ? m : x));
  return [...list, m].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
}

/** Merge server messages in, dropping a pending local copy the echo replaces. */
function mergeServer(list: UiMessage[], incoming: Message[]): UiMessage[] {
  let out = list;
  for (const m of incoming) {
    if (m.direction === "inbound") {
      const dup = out.find((x) => x.local === "pending" && x.body_text === m.body_text);
      if (dup && !out.some((x) => x.id === m.id)) out = out.filter((x) => x !== dup);
    }
    out = upsert(out, m);
  }
  return out;
}
