// Live updates over the gateway's contact:<wid>:<contact_id> topic. Tokens
// live five minutes and only need to be valid at connect, so every reconnect
// first asks the API for a fresh session (and ws_token).

import { Socket } from "phoenix";
import type { Envelope, LiveEvent } from "./types";

export type RtChannelLike = {
  join(): { receive(status: string, cb: (r: unknown) => void): unknown };
  on(event: string, cb: (p: unknown) => void): unknown;
  push(event: string, payload: object): unknown;
  leave(): unknown;
};
export type RtSocketLike = {
  connect(): void;
  disconnect(): void;
  onOpen(cb: () => void): unknown;
  onError(cb: () => void): unknown;
  onClose(cb: () => void): unknown;
  channel(topic: string, params?: object): RtChannelLike;
};
export type SocketFactory = (url: string, params: () => Record<string, string>) => RtSocketLike;

// The token goes in Sec-WebSocket-Protocol (authToken, read at every connect), never the
// URL, where the customer site's proxies and logs would keep it.
export const phoenixFactory: SocketFactory = (url, params) =>
  new Socket(url, {
    params: () => {
      const { token: _token, ...rest } = params();
      return rest;
    },
    // phoenix.js reads authToken through closure(), so a function gives each connect the
    // current token; its types only say string.
    authToken: (() => params().token ?? "") as unknown as string,
    reconnectAfterMs: (tries: number) => [1000, 2000, 5000, 10000][tries - 1] ?? 15000,
  }) as unknown as RtSocketLike;

/** The `wid` claim of a JWT payload, read without verifying (the gateway verifies). */
export function claim(token: string, name: string): string | null {
  try {
    const part = token.split(".")[1];
    if (!part) return null;
    const b64 = part.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(part.length / 4) * 4, "=");
    const json = JSON.parse(atob(b64)) as Record<string, unknown>;
    const v = json[name];
    return typeof v === "string" ? v : null;
  } catch {
    return null;
  }
}

export type LiveHandlers = {
  onEvent: (e: Envelope) => void;
  onTyping: (p: { ticket_id: string; user?: { id: string; name: string } }) => void;
  /** The agent's reply as it is generated (contracts/realtime.md "Live events"). */
  onLive?: (e: LiveEvent) => void;
  onStatus?: (live: boolean) => void;
  /** The socket came back after a drop. */
  onReconnect?: () => void;
};

export class Live {
  private socket: RtSocketLike | null = null;
  private channel: RtChannelLike | null = null;
  private token: string;
  private refreshing = false;
  private lastTyping = 0;

  constructor(
    private factory: SocketFactory,
    private url: string,
    token: string,
    private contactId: string,
    private refresh: () => Promise<string>,
    private h: LiveHandlers,
  ) {
    this.token = token;
  }

  start() {
    const wid = claim(this.token, "wid");
    if (!wid || !this.url) return;
    const socket = this.factory(this.url, () => ({ token: this.token }));
    this.socket = socket;
    let wasDown = false;
    socket.onOpen(() => {
      this.h.onStatus?.(true);
      if (wasDown) this.h.onReconnect?.();
      wasDown = false;
    });
    const dropped = () => {
      wasDown = true;
      this.h.onStatus?.(false);
      if (this.refreshing) return;
      this.refreshing = true;
      this.refresh()
        .then((t) => (this.token = t))
        .catch(() => {})
        .finally(() => (this.refreshing = false));
    };
    socket.onError(dropped);
    socket.onClose(dropped);
    socket.connect();
    const ch = socket.channel(`contact:${wid}:${this.contactId}`, {});
    ch.on("event", (p) => this.h.onEvent(p as Envelope));
    ch.on("typing", (p) => this.h.onTyping(p as { ticket_id: string; user?: { id: string; name: string } }));
    ch.on("live", (p) => {
      if (p && typeof p === "object" && typeof (p as LiveEvent).type === "string" && (p as LiveEvent).data) this.h.onLive?.(p as LiveEvent);
    });
    ch.join();
    this.channel = ch;
  }

  typing(ticketId: string) {
    const now = Date.now();
    if (!this.channel || now - this.lastTyping < 3000) return;
    this.lastTyping = now;
    this.channel.push("typing", { ticket_id: ticketId });
  }

  typingDone() {
    this.lastTyping = 0;
  }

  stop() {
    this.channel?.leave();
    this.socket?.disconnect();
    this.channel = null;
    this.socket = null;
  }
}
