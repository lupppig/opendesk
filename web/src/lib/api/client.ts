// The one HTTP client for the public API. Access tokens live in memory only,
// one per signed-in account; each account's refresh token is an HttpOnly cookie
// the browser sends to /v1/auth/refresh (contracts/tokens.md). A 401 triggers
// one shared refresh of that account, then the request is retried once.

import type { TokenResponse } from "./types";

export type Problem = {
  status: number;
  title?: string;
  detail?: string;
  code?: string;
  errors?: { message?: string; location?: string; value?: unknown }[];
};

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly title: string;
  readonly detail: string;
  readonly errors: Problem["errors"];
  readonly requestId: string | null;
  /** The full problem body, including extension members (e.g. link_token). */
  readonly body: Record<string, unknown>;

  constructor(p: Problem, requestId: string | null = null) {
    super(p.detail || p.title || `Request failed (${p.status})`);
    this.name = "ApiError";
    this.status = p.status;
    this.code = p.code ?? codeForStatus(p.status);
    this.title = p.title ?? "";
    this.detail = p.detail ?? "";
    this.errors = p.errors;
    this.requestId = requestId;
    this.body = { ...(p as Record<string, unknown>) };
  }
}

function codeForStatus(status: number): string {
  switch (status) {
    case 400:
    case 422:
      return "validation_failed";
    case 401:
      return "unauthorized";
    case 403:
      return "forbidden";
    case 404:
      return "not_found";
    case 409:
      return "conflict";
    case 429:
      return "rate_limited";
    case 0:
      return "network_error";
    default:
      return "server_error";
  }
}

// Plan limits: any 402 plan_limit / ai_balance_empty is announced so one shared
// upgrade dialog can explain it, wherever the request came from.
type LimitListener = (e: ApiError) => void;
const limitListeners = new Set<LimitListener>();
export function onPlanLimit(l: LimitListener) {
  limitListeners.add(l);
  return () => limitListeners.delete(l);
}
export function isPlanLimit(e: unknown): e is ApiError {
  return e instanceof ApiError && e.status === 402 && (e.code === "plan_limit" || e.code === "ai_balance_empty");
}
function announce(e: ApiError) {
  if (isPlanLimit(e)) limitListeners.forEach((l) => l(e));
  return e;
}

export function isApiError(e: unknown, code?: string): e is ApiError {
  return e instanceof ApiError && (code === undefined || e.code === code);
}

/** A human message for any thrown value, for toasts and inline errors. */
export function errorMessage(e: unknown, fallback = "Something went wrong"): string {
  if (e instanceof ApiError) {
    if (e.code === "network_error") return "Can't reach the server. Check your connection.";
    if (e.code === "rate_limited") return "Too many attempts. Wait a moment and try again.";
    const field = e.errors?.find((x) => x.message)?.message;
    return e.detail || field || e.title || fallback;
  }
  if (e instanceof Error && e.message) return e.message;
  return fallback;
}

// Transport: fetch by default; mock mode swaps it for an in-memory handler.
export type Transport = (input: string, init: RequestInit) => Promise<Response>;
let transport: Transport = (input, init) => fetch(input, init);
export function setTransport(t: Transport) {
  transport = t;
}

type ClientState = {
  baseUrl: string;
  /** The active account's access token. */
  accessToken: string | null;
  expiresAt: number;
  workspaceId: string | null;
  /** The active account (user id), once known. */
  userId: string | null;
};

const state: ClientState = { baseUrl: "", accessToken: null, expiresAt: 0, workspaceId: null, userId: null };

type Token = { token: string; expiresAt: number };
/** Access tokens of the signed-in accounts that are not active, by user id. */
const parked = new Map<string, Token>();

type SessionListener = (r: TokenResponse | null) => void;
const sessionListeners = new Set<SessionListener>();
/** Called with the active account's session after every refresh, and with null when it ends. */
export function onSession(l: SessionListener) {
  sessionListeners.add(l);
  return () => sessionListeners.delete(l);
}

type AccountListener = (userId: string | null, r: TokenResponse | null) => void;
const accountListeners = new Set<AccountListener>();
/** Called for every account's refresh: its new session, or null when it ended. */
export function onAccountSession(l: AccountListener) {
  accountListeners.add(l);
  return () => accountListeners.delete(l);
}

export function configureClient(opts: { baseUrl: string }) {
  state.baseUrl = opts.baseUrl;
}
export function setAccessToken(token: string | null, expiresIn = 900) {
  state.accessToken = token;
  state.expiresAt = token ? Date.now() + expiresIn * 1000 : 0;
}
export function getAccessToken() {
  return state.accessToken;
}

/**
 * Make another signed-in account the one requests go out as. Its access token,
 * when the client holds one, comes back from memory; the previous account's is
 * kept for switching back.
 */
export function setActiveAccount(userId: string | null) {
  if (userId === state.userId) return;
  if (state.userId && state.accessToken) parked.set(state.userId, { token: state.accessToken, expiresAt: state.expiresAt });
  state.userId = userId;
  const t = userId ? parked.get(userId) : undefined;
  if (userId) parked.delete(userId);
  state.accessToken = t?.token ?? null;
  state.expiresAt = t?.expiresAt ?? 0;
}
export function getActiveAccount() {
  return state.userId;
}
/** Forget an account's access token (it signed out). */
export function forgetAccount(userId: string) {
  parked.delete(userId);
  if (state.userId === userId) setAccessToken(null);
}
/** Forget every account's access token. */
export function forgetAllAccounts() {
  parked.clear();
  state.userId = null;
  setAccessToken(null);
}

function tokenFor(userId: string | null): Token | null {
  if (userId === state.userId) return state.accessToken ? { token: state.accessToken, expiresAt: state.expiresAt } : null;
  return userId ? (parked.get(userId) ?? null) : null;
}

function storeToken(userId: string | null, token: string | null, expiresIn = 900) {
  if (userId === state.userId || state.userId === null) {
    if (state.userId === null && userId) state.userId = userId;
    setAccessToken(token, expiresIn);
  } else if (userId) {
    if (token) parked.set(userId, { token, expiresAt: Date.now() + expiresIn * 1000 });
    else parked.delete(userId);
  }
}
export function setWorkspaceId(id: string | null) {
  state.workspaceId = id;
}
/** The focus workspace: the one pages work in and requests go to by default. */
export function getWorkspaceId() {
  return state.workspaceId;
}

// Every request targets exactly one workspace (contracts/tokens.md "Requests"). It is the
// focus workspace unless something on the stack runs inside `inWorkspace`: a query of
// another workspace's cache key, a realtime event of another workspace, a sidebar group.
// Only the synchronous part of `fn` sees it, which is where requests read it: `raw`
// takes the workspace before its first await.
let ambient: string | null = null;

/** Run fn with requests and query keys aimed at workspace `wid` (synchronously). */
export function inWorkspace<T>(wid: string | null | undefined, fn: () => T): T {
  if (!wid) return fn();
  const prev = ambient;
  ambient = wid;
  try {
    return fn();
  } finally {
    ambient = prev;
  }
}

/** The workspace a request or query key made right now belongs to. */
export function scopeWorkspace(): string | null {
  return ambient ?? state.workspaceId;
}

export function apiUrl(path: string) {
  return `${state.baseUrl}${path}`;
}

const refreshing = new Map<string, Promise<TokenResponse | null>>();

/**
 * Exchanges an account's refresh cookie for a new access token: the active
 * account by default, or, before any account is known, the one this browser
 * used most recently. Concurrent callers for one account share one request,
 * because the server rotates the refresh token on use and a second parallel
 * refresh would present an already-rotated token.
 */
export function refreshSession(userId: string | null = state.userId): Promise<TokenResponse | null> {
  const key = userId ?? "";
  const running = refreshing.get(key);
  if (running) return running;
  const p = (async () => {
    try {
      const res = await transport(apiUrl("/v1/auth/refresh"), {
        method: "POST",
        credentials: "include",
        headers: userId ? { Accept: "application/json", "Content-Type": "application/json" } : { Accept: "application/json" },
        body: userId ? JSON.stringify({ user_id: userId }) : undefined,
      });
      if (!res.ok) {
        const active = userId === state.userId;
        storeToken(userId, null);
        accountListeners.forEach((l) => l(userId, null));
        if (active) sessionListeners.forEach((l) => l(null));
        return null;
      }
      const body = (await res.json()) as TokenResponse;
      const uid = body.user?.id ?? userId;
      storeToken(uid, body.access_token, body.expires_in);
      accountListeners.forEach((l) => l(uid, body));
      if (uid === state.userId) sessionListeners.forEach((l) => l(body));
      return body;
    } catch {
      // Network failure: keep whatever token we had; the caller surfaces the error.
      return null;
    } finally {
      refreshing.delete(key);
    }
  })();
  refreshing.set(key, p);
  return p;
}

export type RequestOptions = {
  query?: Record<string, string | number | boolean | null | undefined | string[]>;
  body?: unknown;
  headers?: Record<string, string>;
  /**
   * OD-Workspace: true (default) sends the workspace in scope (see inWorkspace), false
   * none, and an id that workspace (with `as`, one of that account's).
   */
  workspace?: boolean | string;
  /** Send the bearer token and refresh on 401. Default true. */
  auth?: boolean;
  /** Attach an Idempotency-Key (POST creates). Reused on the retry after refresh. */
  idempotent?: boolean;
  /** Send as this signed-in account (user id) rather than the active one. */
  as?: string;
  signal?: AbortSignal;
  /** Let the request outlive the page (sent while it unloads). Bodies over KEEPALIVE_MAX go without. */
  keepalive?: boolean;
};

/** The largest JSON body (bytes) sent with keepalive; browsers allow 64 KB for all of a page's keepalive requests together. */
export const KEEPALIVE_MAX = 60_000;

/** The size of a JSON body in bytes, as it goes over the wire. */
export function jsonBytes(body: unknown): number {
  return new TextEncoder().encode(JSON.stringify(body)).length;
}

const NO_REFRESH = new Set([
  "/v1/auth/login",
  "/v1/auth/register",
  "/v1/auth/mfa",
  "/v1/auth/refresh",
  "/v1/auth/accounts",
  "/v1/auth/oauth/exchange",
  "/v1/auth/oauth/link",
  "/v1/auth/password/forgot",
  "/v1/auth/password/reset",
]);

export function buildQuery(query: RequestOptions["query"]): string {
  if (!query) return "";
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === "") continue;
    if (Array.isArray(v)) {
      if (v.length) p.set(k, v.join(","));
    } else p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
}

function newIdempotencyKey() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

async function parseError(res: Response): Promise<ApiError> {
  let p: Problem = { status: res.status };
  try {
    const ct = res.headers.get("content-type") ?? "";
    if (ct.includes("json")) p = { ...(await res.json()), status: res.status };
  } catch {
    /* body was not JSON */
  }
  return new ApiError(p, res.headers.get("x-request-id"));
}

export async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await raw(method, path, opts);
  if (res.status === 204 || res.headers.get("content-length") === "0") return undefined as T;
  const ct = res.headers.get("content-type") ?? "";
  if (!ct.includes("json")) return undefined as T;
  return (await res.json()) as T;
}

/** Sends a request with auth, refresh and retry; throws ApiError unless 2xx. */
async function raw(method: string, path: string, opts: RequestOptions = {}): Promise<Response> {
  const useAuth = opts.auth !== false;
  const idemKey = opts.idempotent ? newIdempotencyKey() : undefined;
  // A request belongs to the account (and workspace) it started under, even if
  // the person switches while it is in flight.
  let account = opts.as ?? state.userId;
  const workspaceId =
    typeof opts.workspace === "string" ? opts.workspace : opts.as && opts.as !== state.userId ? null : scopeWorkspace();

  // Refresh ahead of expiry rather than eating a 401 on every 15-minute boundary.
  const tok = tokenFor(account);
  if (useAuth && tok && tok.expiresAt - Date.now() < 20_000) {
    const r = await refreshSession(account);
    account ??= r?.user?.id ?? state.userId;
  }

  const send = async () => {
    const headers: Record<string, string> = { Accept: "application/json", ...opts.headers };
    let body: BodyInit | undefined;
    if (opts.body instanceof FormData) body = opts.body;
    else if (opts.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(opts.body);
    }
    const bearer = useAuth ? tokenFor(account)?.token : undefined;
    if (bearer) headers.Authorization = `Bearer ${bearer}`;
    if (opts.workspace !== false && workspaceId) headers["OD-Workspace"] = workspaceId;
    if (idemKey) headers["Idempotency-Key"] = idemKey;
    try {
      return await transport(apiUrl(path) + buildQuery(opts.query), {
        method,
        headers,
        body,
        credentials: "include",
        signal: opts.signal,
        keepalive: opts.keepalive && (typeof body !== "string" || new TextEncoder().encode(body).length <= KEEPALIVE_MAX) ? true : undefined,
      });
    } catch (e) {
      if ((e as Error)?.name === "AbortError") throw e;
      throw new ApiError({ status: 0, title: "Network error" });
    }
  };

  let res = await send();
  if (res.status === 401 && useAuth && !NO_REFRESH.has(path)) {
    // Only a missing or expired session is worth a refresh; a wrong password or
    // code (401 invalid_credentials / invalid_code) is the answer itself.
    const err = await parseError(res);
    if (err.code !== "unauthenticated" && err.code !== "unauthorized") throw announce(err);
    const refreshed = await refreshSession(account);
    if (!refreshed) throw announce(err);
    account ??= refreshed.user?.id ?? state.userId;
    res = await send();
  }
  if (!res.ok) throw announce(await parseError(res));
  return res;
}

export type StreamHandlers = {
  /** Each `delta` chunk's text, in order. */
  onDelta?: (text: string, soFar: string) => void;
  signal?: AbortSignal;
};

/**
 * POST with `Accept: text/event-stream`: calls onDelta for every `event: delta`
 * {text} and resolves with the `event: done` JSON. A server that answers with
 * plain JSON instead is treated as one final result.
 */
export async function stream<T>(path: string, body: unknown, h: StreamHandlers = {}): Promise<T> {
  const res = await raw("POST", path, { body, headers: { Accept: "text/event-stream" }, signal: h.signal });
  const ct = res.headers.get("content-type") ?? "";
  if (!ct.includes("text/event-stream") || !res.body) return (await res.json()) as T;
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  let soFar = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (value) buf += value;
    let sep: number;
    while ((sep = buf.search(/\r?\n\r?\n/)) >= 0) {
      const frame = buf.slice(0, sep);
      buf = buf.slice(sep).replace(/^\r?\n\r?\n/, "");
      const ev = parseFrame(frame);
      if (!ev) continue;
      if (ev.event === "delta") {
        const text = (JSON.parse(ev.data) as { text?: string }).text ?? "";
        soFar += text;
        h.onDelta?.(text, soFar);
      } else if (ev.event === "done") {
        void reader.cancel().catch(() => {});
        return JSON.parse(ev.data) as T;
      } else if (ev.event === "error") {
        const p = JSON.parse(ev.data) as Problem;
        throw announce(new ApiError({ ...p, status: p.status || 500 }));
      }
    }
    if (done) break;
  }
  throw new ApiError({ status: 0, title: "The stream ended early" });
}

/**
 * POST with `Accept: text/event-stream` and hand every event to `onEvent`, in
 * order, until the stream ends. An `error` event throws its problem; an error
 * before the stream starts is the normal ApiError of that status.
 */
export async function streamEvents(
  path: string,
  body: unknown,
  onEvent: (event: string, data: unknown) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await raw("POST", path, { body, headers: { Accept: "text/event-stream" }, signal });
  const ct = res.headers.get("content-type") ?? "";
  if (!ct.includes("text/event-stream") || !res.body) {
    onEvent("result", await res.json());
    return;
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (value) buf += value;
    let sep: number;
    while ((sep = buf.search(/\r?\n\r?\n/)) >= 0) {
      const frame = buf.slice(0, sep);
      buf = buf.slice(sep).replace(/^\r?\n\r?\n/, "");
      const ev = parseFrame(frame);
      if (!ev) continue;
      const data: unknown = JSON.parse(ev.data);
      if (ev.event === "error") {
        const p = data as Problem;
        throw announce(new ApiError({ ...p, status: p.status || 500 }));
      }
      onEvent(ev.event, data);
    }
    if (done) return;
  }
}

export function parseFrame(frame: string): { event: string; data: string } | null {
  let event = "message";
  const data: string[] = [];
  for (const line of frame.split(/\r?\n/)) {
    if (line.startsWith(":")) continue;
    const i = line.indexOf(":");
    const field = i < 0 ? line : line.slice(0, i);
    const value = i < 0 ? "" : line.slice(i + 1).replace(/^ /, "");
    if (field === "event") event = value;
    else if (field === "data") data.push(value);
  }
  return data.length ? { event, data: data.join("\n") } : null;
}

export const http = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>("GET", path, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("POST", path, { ...opts, body }),
  create: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>("POST", path, { ...opts, body, idempotent: true }),
  patch: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("PATCH", path, { ...opts, body }),
  put: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("PUT", path, { ...opts, body }),
  del: <T = void>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("DELETE", path, { ...opts, body }),
};

/** Test hook: reset module state between tests. */
export function __resetClientForTests() {
  state.accessToken = null;
  state.expiresAt = 0;
  state.workspaceId = null;
  state.userId = null;
  state.baseUrl = "";
  ambient = null;
  parked.clear();
  refreshing.clear();
  sessionListeners.clear();
  accountListeners.clear();
  limitListeners.clear();
  transport = (input, init) => fetch(input, init);
}
