// The one HTTP client for the admin API (contracts/admin.md).
//
// - The access token lives in this module's memory only: never in storage.
// - The refresh token is the HttpOnly admin refresh cookie; only the auth calls
//   send credentials.
// - Data calls carry `Authorization: Bearer`. A 401 from one triggers a single
//   shared refresh (the server rotates the cookie on use, so two parallel
//   refreshes would present an already-rotated token), then one retry.
// - The token is refreshed shortly before it expires when the operator has been
//   active since it was issued, so an idle panel lets its session lapse.

import type { AdminAuthResult } from "./types";

export type Problem = {
  status: number;
  title?: string;
  detail?: string;
  code?: string;
  request_id?: string;
  errors?: { message?: string; location?: string; value?: unknown }[];
};

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly title: string;
  readonly detail: string;
  readonly errors: Problem["errors"];
  readonly requestId: string | null;

  constructor(p: Problem, requestId: string | null = null) {
    super(p.detail || p.title || `Request failed (${p.status})`);
    this.name = "ApiError";
    this.status = p.status;
    this.code = p.code || codeForStatus(p.status);
    this.title = p.title ?? "";
    this.detail = p.detail ?? "";
    this.errors = p.errors;
    this.requestId = (typeof p.request_id === "string" && p.request_id) || requestId;
  }
}

export function codeForStatus(status: number): string {
  switch (status) {
    case 400:
    case 422:
      return "validation_failed";
    case 401:
      return "unauthenticated";
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

export function isApiError(e: unknown, code?: string): e is ApiError {
  return e instanceof ApiError && (code === undefined || e.code === code);
}

/** A human message for any thrown value, for toasts and inline errors. */
export function errorMessage(e: unknown, fallback = "Something went wrong"): string {
  if (e instanceof ApiError) {
    if (e.code === "network_error") return "Can't reach the API. Check your connection.";
    if (e.code === "rate_limited") return "Too many attempts. Wait a moment and try again.";
    if (e.code === "forbidden" && !e.detail) return "Your admin role doesn't allow this.";
    const field = e.errors?.find((x) => x.message)?.message;
    return e.detail || field || e.title || fallback;
  }
  if (e instanceof Error && e.message) return e.message;
  return fallback;
}

/** Reads a problem+json error from a response; anything else becomes a bare status. */
export async function parseError(res: Response): Promise<ApiError> {
  let p: Problem = { status: res.status };
  try {
    const ct = res.headers.get("content-type") ?? "";
    if (ct.includes("json")) {
      const body = (await res.json()) as unknown;
      if (body && typeof body === "object") p = { ...(body as Problem), status: res.status };
    }
  } catch {
    /* the body was not JSON */
  }
  return new ApiError(p, res.headers.get("x-request-id"));
}

// ─── State ─────────────────────────────────────────────────────────────────

export type Transport = (input: string, init: RequestInit) => Promise<Response>;
let transport: Transport = (input, init) => fetch(input, init);
export function setTransport(t: Transport) {
  transport = t;
}

const state = {
  baseUrl: "",
  accessToken: null as string | null,
  expiresAt: 0,
  issuedAt: 0,
  lastActivity: 0,
  timer: undefined as ReturnType<typeof setTimeout> | undefined,
};

/** Refresh this long before the access token expires. */
export const REFRESH_AHEAD_MS = 60_000;

/**
 * Waits before trying a refresh again that another tab answered first (409
 * refresh_in_progress): by then its new cookie is in the browser's jar.
 */
export const REFRESH_RETRY_MS = [1_000, 2_000];

type SessionListener = (r: AdminAuthResult | null, reason?: ApiError) => void;
const listeners = new Set<SessionListener>();
/** Called with every new session (sign-in, refresh) and with null when it ends. */
export function onSession(l: SessionListener) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

export function configureClient(opts: { baseUrl: string }) {
  state.baseUrl = opts.baseUrl.replace(/\/+$/, "");
}

export function apiUrl(path: string) {
  return `${state.baseUrl}${path}`;
}

export function getAccessToken() {
  return state.accessToken;
}

/** The operator did something (a key, a click): a reason to keep the session alive. */
export function noteActivity(now = Date.now()) {
  state.lastActivity = now;
}

/** How long since the operator last did anything; pollers pause once it is long. */
export function idleMs(now = Date.now()) {
  return state.lastActivity ? now - state.lastActivity : Number.POSITIVE_INFINITY;
}

function schedule() {
  clearTimeout(state.timer);
  state.timer = undefined;
  if (!state.accessToken) return;
  const wait = Math.max(5_000, state.expiresAt - Date.now() - REFRESH_AHEAD_MS);
  state.timer = setTimeout(() => {
    state.timer = undefined;
    // Idle since the token was issued: let it lapse. The next request refreshes
    // first, while the server still holds the session (an hour after the last refresh).
    if (state.lastActivity > state.issuedAt) void refreshSession().catch(() => {});
  }, wait);
}

/** Adopt a session (sign-in, refresh), or end it with null. */
export function setSession(r: AdminAuthResult | null, reason?: ApiError) {
  if (r) {
    state.accessToken = r.access_token;
    state.issuedAt = Date.now();
    state.expiresAt = state.issuedAt + Math.max(1, r.expires_in) * 1000;
  } else {
    state.accessToken = null;
    state.expiresAt = 0;
    state.issuedAt = 0;
  }
  schedule();
  listeners.forEach((l) => l(r, reason));
}

let refreshing: Promise<AdminAuthResult | null> | null = null;

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

async function postRefresh(): Promise<Response> {
  try {
    return await transport(apiUrl("/v1/admin/auth/refresh"), {
      method: "POST",
      credentials: "include",
      headers: { Accept: "application/json" },
    });
  } catch {
    throw new ApiError({ status: 0, title: "Network error" });
  }
}

/**
 * Exchanges the refresh cookie for a new access token. Concurrent callers share
 * one request; when another tab rotated the cookie a moment earlier (409), it
 * tries again with the cookie that tab received. Resolves null when there is no
 * live session (401, or 403 for a suspended account or a revoked admin); throws
 * ApiError when the API could not answer (network, 5xx), keeping the current token.
 */
export function refreshSession(): Promise<AdminAuthResult | null> {
  if (refreshing) return refreshing;
  refreshing = (async () => {
    try {
      let res = await postRefresh();
      for (const wait of REFRESH_RETRY_MS) {
        if (res.status !== 409) break;
        await sleep(wait);
        res = await postRefresh();
      }
      if (res.status === 401 || res.status === 403) {
        const err = await parseError(res);
        setSession(null, err);
        return null;
      }
      if (!res.ok) throw await parseError(res);
      const body = (await res.json()) as AdminAuthResult;
      setSession(body);
      return body;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

// ─── Requests ──────────────────────────────────────────────────────────────

export type Query = Record<string, string | number | boolean | null | undefined>;

export type RequestOptions = {
  query?: Query;
  body?: unknown;
  signal?: AbortSignal;
};

/** 401 codes that are the answer itself (a wrong code or password), not an ended session. */
const ANSWER_CODES = new Set(["invalid_code", "invalid_credentials", "invalid_mfa_token"]);

export function buildQuery(query: Query | undefined): string {
  if (!query) return "";
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === "") continue;
    p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
}

async function read<T>(res: Response): Promise<T> {
  if (res.status === 204 || res.headers.get("content-length") === "0") return undefined as T;
  const ct = res.headers.get("content-type") ?? "";
  if (!ct.includes("json")) return undefined as T;
  return (await res.json()) as T;
}

function encode(body: unknown, headers: Record<string, string>): BodyInit | undefined {
  if (body === undefined) return undefined;
  headers["Content-Type"] = "application/json";
  return JSON.stringify(body);
}

/** A data call: bearer auth, refresh ahead of expiry, refresh and retry once on 401. */
export async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  if (state.accessToken && state.expiresAt - Date.now() < 30_000) {
    // A failed refresh here is not fatal: the request answers 401 and the retry path decides.
    await refreshSession().catch(() => null);
  }

  const send = async () => {
    const headers: Record<string, string> = { Accept: "application/json" };
    const body = encode(opts.body, headers);
    if (state.accessToken) headers.Authorization = `Bearer ${state.accessToken}`;
    try {
      return await transport(apiUrl(path) + buildQuery(opts.query), { method, headers, body, credentials: "omit", signal: opts.signal });
    } catch (e) {
      if ((e as Error)?.name === "AbortError") throw e;
      throw new ApiError({ status: 0, title: "Network error" });
    }
  };

  let res = await send();
  if (res.status === 401) {
    const err = await parseError(res);
    if (ANSWER_CODES.has(err.code)) throw err;
    const refreshed = await refreshSession();
    if (!refreshed) throw err;
    res = await send();
    if (res.status === 401) {
      const again = await parseError(res);
      if (!ANSWER_CODES.has(again.code)) setSession(null, again);
      throw again;
    }
  }
  if (!res.ok) throw await parseError(res);
  return read<T>(res);
}

/** A sign-in call: the cookie, no bearer, no refresh. */
export async function authRequest<T>(path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const payload = encode(body, headers);
  if (state.accessToken) headers.Authorization = `Bearer ${state.accessToken}`;
  let res: Response;
  try {
    res = await transport(apiUrl(path), { method: "POST", headers, body: payload, credentials: "include" });
  } catch {
    throw new ApiError({ status: 0, title: "Network error" });
  }
  if (!res.ok) throw await parseError(res);
  return read<T>(res);
}

export const http = {
  get: <T>(path: string, query?: Query, signal?: AbortSignal) => request<T>("GET", path, { query, signal }),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, { body }),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, { body }),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, { body }),
  del: <T = void>(path: string, body?: unknown) => request<T>("DELETE", path, { body }),
};

/** Test hook: reset module state between tests. */
export function __resetClientForTests() {
  clearTimeout(state.timer);
  Object.assign(state, { baseUrl: "", accessToken: null, expiresAt: 0, issuedAt: 0, lastActivity: 0, timer: undefined });
  refreshing = null;
  listeners.clear();
  transport = (input, init) => fetch(input, init);
}
