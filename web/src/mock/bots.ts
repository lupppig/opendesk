// Bots in mock mode (contracts/bots.md): the workspace's customer-facing AI identities.

import type { Bot, BotPersonality, BotRef, Ticket, WidgetBot } from "@/lib/api/types";
import { OLLIE_LOOK, renderBotSvg, type BotLook } from "@/lib/botlook";
import type { WsData } from "./db";
import { nowIso, uuid } from "./util";

export const MAX_BOTS = 50;

export function defaultPersonality(): BotPersonality {
  return {
    tone: "friendly",
    custom_tone: "",
    instructions: "",
    reply_length: "short",
    emoji: "none",
    greeting: "",
    handoff_message: "",
    blocked_topics: [],
    sign_off: "",
  };
}

/** The look as an image URL, for places that show a picture (the API serves a PNG). */
export function lookUrl(look: BotLook): string {
  return `data:image/svg+xml;utf8,${encodeURIComponent(renderBotSvg(look))}`;
}

export function newBot(name: string, extra: Partial<Bot> = {}): Bot {
  const now = nowIso();
  const look = { ...OLLIE_LOOK, fill: { ...OLLIE_LOOK.fill } };
  return {
    id: uuid(),
    name,
    role: "",
    language: "",
    look,
    image_url: null,
    avatar_url: lookUrl(look),
    personality: defaultPersonality(),
    is_default: false,
    position: 0,
    usage: { widgets: [], mailboxes: [] },
    created_at: now,
    updated_at: now,
    ...extra,
  };
}

/** The workspace's bots, its default one made on first use. */
export function botsOf(ws: WsData): Bot[] {
  if (!ws.bots?.length) ws.bots = [newBot("Ollie", { is_default: true })];
  return ws.bots;
}

export function defaultBot(ws: WsData): Bot {
  const list = botsOf(ws);
  return list.find((b) => b.is_default) ?? list[0];
}

/** A bot as the API answers it: avatar_url follows the look, usage is computed. */
export function botOut(ws: WsData, b: Bot): Bot {
  const answers = (id: string | null | undefined) => (id ? id === b.id : b.is_default);
  return {
    ...b,
    look: { ...b.look, fill: { ...b.look.fill } },
    personality: { ...b.personality, blocked_topics: [...b.personality.blocked_topics] },
    avatar_url: b.look.kind === "image" && b.image_url ? b.image_url : lookUrl(b.look),
    usage: {
      widgets: ws.widgets.filter((w) => answers(w.bot_id)).map((w) => ({ id: w.id, name: w.name, brand_id: w.brand_id })),
      mailboxes: ws.mailboxes.filter((m) => answers(m.bot_id)).map((m) => ({ id: m.id, name: m.name, email: m.email })),
      // A key speaks as its own bot only: one without a bot is not the default's.
      api_keys: ws.apiKeys.filter((k) => k.bot_id === b.id).map((k) => ({ id: k.id, name: k.name })),
    },
  };
}

export function sortedBots(ws: WsData): Bot[] {
  return [...botsOf(ws)].sort((a, b) => a.position - b.position || Number(b.is_default) - Number(a.is_default) || a.created_at.localeCompare(b.created_at));
}

const byId = (ws: WsData, id: string | null | undefined) => (id ? botsOf(ws).find((b) => b.id === id) : undefined);

export function botForWidget(ws: WsData, widgetId: string | null | undefined): Bot {
  return byId(ws, ws.widgets.find((w) => w.id === widgetId)?.bot_id) ?? defaultBot(ws);
}

export function botForMailbox(ws: WsData, mailboxId: string | null | undefined): Bot {
  return byId(ws, ws.mailboxes.find((m) => m.id === mailboxId)?.bot_id) ?? defaultBot(ws);
}

/** The bot a ticket's AI replies are sent as: its widget's on chat, its mailbox's on email. */
export function botForTicket(ws: WsData, t: Pick<Ticket, "channel" | "widget_id" | "mailbox_id"> | null | undefined): Bot {
  if (!t) return defaultBot(ws);
  return t.channel === "email" ? botForMailbox(ws, t.mailbox_id) : botForWidget(ws, t.widget_id);
}

export function botRef(ws: WsData, b: Bot): BotRef {
  const out = botOut(ws, b);
  return { id: b.id, name: b.name, avatar_url: out.avatar_url, look: out.look };
}

/** A message's bot: the one it was sent as, else one of that name, else the default. */
export function botRefFor(ws: WsData, id: string | undefined, name: string): BotRef {
  const b = byId(ws, id) ?? botsOf(ws).find((x) => x.name === name) ?? defaultBot(ws);
  return { ...botRef(ws, b), name: name || b.name };
}

export function widgetBot(ws: WsData, widgetId: string | null | undefined): WidgetBot {
  const b = botForWidget(ws, widgetId);
  const out = botOut(ws, b);
  return { id: b.id, name: b.name, role: b.role, avatar_url: out.avatar_url, look: out.look, greeting: b.personality.greeting };
}
