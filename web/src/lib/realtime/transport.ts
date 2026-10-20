// The socket the app talks to. Production is a Phoenix Socket; mock mode swaps
// in an in-memory gateway with the same surface.

import { Socket } from "phoenix";

export type PushReply = { receive: (status: "ok" | "error" | "timeout", cb: (resp: unknown) => void) => PushReply };

export interface RtChannel {
  join(timeout?: number): PushReply;
  leave(timeout?: number): PushReply;
  on(event: string, cb: (payload: unknown) => void): number;
  off(event: string, ref?: number): void;
  push(event: string, payload: object, timeout?: number): PushReply;
  onError(cb: (reason?: unknown) => void): number | void;
  onClose(cb: (payload?: unknown) => void): number | void;
}

export interface RtSocket {
  connect(): void;
  disconnect(cb?: () => void, code?: number, reason?: string): void;
  onOpen(cb: () => void): unknown;
  onClose(cb: (e?: unknown) => void): unknown;
  onError(cb: (e?: unknown) => void): unknown;
  channel(topic: string, params?: object | (() => object)): RtChannel;
  isConnected(): boolean;
}

export type SocketFactory = (url: string, params: () => Record<string, string>) => RtSocket;

// The token goes in Sec-WebSocket-Protocol (authToken, read at every connect), never the
// URL, where proxies and access logs would keep it. Other params stay in the query.
let factory: SocketFactory = (url, params) =>
  new Socket(url, {
    params: () => {
      const { token: _token, ...rest } = params();
      return rest;
    },
    // phoenix.js reads authToken through closure(), so a function gives each connect the
    // current token; its types only say string.
    authToken: (() => params().token ?? "") as unknown as string,
    heartbeatIntervalMs: 25_000,
    reconnectAfterMs: (tries: number) => [1000, 2000, 5000, 10_000][tries - 1] ?? 15_000,
  }) as unknown as RtSocket;

export function setSocketFactory(f: SocketFactory) {
  factory = f;
}

export function createSocket(url: string, params: () => Record<string, string>): RtSocket {
  return factory(url, params);
}
