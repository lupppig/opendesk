import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  __resetClientForTests,
  ApiError,
  authRequest,
  configureClient,
  errorMessage,
  getAccessToken,
  http,
  noteActivity,
  onSession,
  parseError,
  refreshSession,
  setSession,
  setTransport,
} from "./client";
import type { AdminAuthResult } from "./types";

const me = {
  id: "u1",
  email: "ops@example.com",
  name: "Ops",
  role: "super" as const,
  permissions: ["read" as const],
  instance: { version: "1", commit: "abc", billing_enabled: false, ai_enabled: false, app_url: "", api_url: "", self_hosted: true },
};
const auth = (token: string, expires_in = 600): AdminAuthResult => ({ access_token: token, expires_in, admin: me });

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": status >= 400 ? "application/problem+json" : "application/json", ...headers } });

type Call = { url: string; init: RequestInit };

function fake(handler: (url: string, init: RequestInit, n: number) => Response | Promise<Response>) {
  const calls: Call[] = [];
  setTransport(async (url, init) => {
    calls.push({ url, init });
    return handler(url, init, calls.length);
  });
  return calls;
}

const headerOf = (c: Call, h: string) => (c.init.headers as Record<string, string> | undefined)?.[h];

beforeEach(() => {
  __resetClientForTests();
  configureClient({ baseUrl: "https://api.example.com/" });
});
afterEach(() => {
  vi.useRealTimers();
  __resetClientForTests();
});

describe("refresh", () => {
  it("shares one request between concurrent callers", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const calls = fake(async () => {
      await gate;
      return json(200, auth("t1"));
    });
    const a = refreshSession();
    const b = refreshSession();
    release();
    const [ra, rb] = await Promise.all([a, b]);
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("https://api.example.com/v1/admin/auth/refresh");
    expect(calls[0].init.credentials).toBe("include");
    expect(calls[0].init.method).toBe("POST");
    expect(ra?.access_token).toBe("t1");
    expect(rb).toBe(ra);
    expect(getAccessToken()).toBe("t1");
  });

  it("ends the session on 401 and tells listeners why", async () => {
    setSession(auth("old"));
    const seen: (string | null)[] = [];
    onSession((r, reason) => seen.push(r ? r.access_token : (reason?.code ?? "none")));
    fake(() => json(401, { status: 401, code: "unauthenticated" }));
    expect(await refreshSession()).toBeNull();
    expect(getAccessToken()).toBeNull();
    expect(seen).toEqual(["unauthenticated"]);
  });

  it("keeps the token when the API can't answer", async () => {
    setSession(auth("keep"));
    fake(() => {
      throw new TypeError("Failed to fetch");
    });
    await expect(refreshSession()).rejects.toMatchObject({ code: "network_error" });
    expect(getAccessToken()).toBe("keep");
  });
});

describe("data requests", () => {
  it("send the bearer token and no credentials", async () => {
    setSession(auth("tok"));
    const calls = fake(() => json(200, { ok: true }));
    await http.get("/v1/admin/summary");
    expect(headerOf(calls[0], "Authorization")).toBe("Bearer tok");
    expect(calls[0].init.credentials).toBe("omit");
  });

  it("refresh once on 401 and retry with the new token", async () => {
    setSession(auth("stale"));
    const calls = fake((url) => {
      if (url.endsWith("/v1/admin/auth/refresh")) return json(200, auth("fresh"));
      const bearer = headerOf(calls[calls.length - 1], "Authorization");
      return bearer === "Bearer fresh" ? json(200, { users: 1 }) : json(401, { status: 401, code: "unauthenticated" });
    });
    await expect(http.get<{ users: number }>("/v1/admin/summary")).resolves.toEqual({ users: 1 });
    expect(calls.map((c) => c.url.replace("https://api.example.com", ""))).toEqual(["/v1/admin/summary", "/v1/admin/auth/refresh", "/v1/admin/summary"]);
  });

  it("share one refresh when several requests hit 401 together", async () => {
    setSession(auth("stale"));
    let refreshes = 0;
    const calls = fake(async (url, init) => {
      if (url.endsWith("/v1/admin/auth/refresh")) {
        refreshes++;
        await new Promise((r) => setTimeout(r, 5));
        return json(200, auth("fresh"));
      }
      const bearer = (init.headers as Record<string, string>).Authorization;
      return bearer === "Bearer fresh" ? json(200, { url }) : json(401, { status: 401, code: "unauthenticated" });
    });
    await Promise.all([http.get("/v1/admin/users"), http.get("/v1/admin/workspaces"), http.get("/v1/admin/jobs")]);
    expect(refreshes).toBe(1);
    expect(calls.filter((c) => !c.url.endsWith("/refresh"))).toHaveLength(6);
  });

  it("do not refresh on a wrong step-up code", async () => {
    setSession(auth("tok"));
    const calls = fake(() => json(401, { status: 401, code: "invalid_code", detail: "Wrong code" }));
    await expect(http.post("/v1/admin/users/u2/mfa/disable", { code: "000000" })).rejects.toMatchObject({ status: 401, code: "invalid_code" });
    expect(calls).toHaveLength(1);
    expect(getAccessToken()).toBe("tok");
  });

  it("give up when the refresh fails, and end the session", async () => {
    setSession(auth("stale"));
    const ends: string[] = [];
    onSession((r, reason) => !r && ends.push(reason?.code ?? ""));
    const calls = fake((url) => (url.endsWith("/refresh") ? json(401, { status: 401, code: "unauthenticated" }) : json(401, { status: 401, code: "unauthenticated" })));
    await expect(http.get("/v1/admin/me")).rejects.toMatchObject({ status: 401 });
    expect(calls).toHaveLength(2);
    expect(ends).toEqual(["unauthenticated"]);
  });

  it("refresh first when the token is about to expire", async () => {
    setSession(auth("short", 10));
    const calls = fake((url) => (url.endsWith("/refresh") ? json(200, auth("long")) : json(200, {})));
    await http.get("/v1/admin/health");
    expect(calls[0].url).toMatch(/\/refresh$/);
    expect(headerOf(calls[1], "Authorization")).toBe("Bearer long");
  });

  it("encode the query and the JSON body", async () => {
    setSession(auth("tok"));
    const calls = fake(() => new Response(null, { status: 204 }));
    await http.get("/v1/admin/users", { q: "a b", filter: undefined, cursor: null, limit: 50 });
    await http.del("/v1/admin/workspaces/w1", { code: "123456", confirm_name: "Acme" });
    expect(calls[0].url).toBe("https://api.example.com/v1/admin/users?q=a+b&limit=50");
    expect(calls[1].init.method).toBe("DELETE");
    expect(calls[1].init.body).toBe(JSON.stringify({ code: "123456", confirm_name: "Acme" }));
    expect(headerOf(calls[1], "Content-Type")).toBe("application/json");
  });
});

describe("auth requests", () => {
  it("send credentials and never refresh", async () => {
    const calls = fake(() => json(401, { status: 401, code: "invalid_credentials" }));
    await expect(authRequest("/v1/admin/auth/login", { email: "a@b.c", password: "x" })).rejects.toMatchObject({ code: "invalid_credentials" });
    expect(calls).toHaveLength(1);
    expect(calls[0].init.credentials).toBe("include");
  });
});

describe("refresh another tab answered first", () => {
  it("tries again with the cookie that tab received", async () => {
    vi.useFakeTimers();
    const calls = fake((_url, _init, n) => (n === 1 ? json(409, { status: 409, code: "refresh_in_progress" }) : json(200, auth("after"))));
    const r = refreshSession();
    await vi.advanceTimersByTimeAsync(1_000);
    expect((await r)?.access_token).toBe("after");
    expect(calls).toHaveLength(2);
    expect(getAccessToken()).toBe("after");
  });

  it("keeps the session when the conflict outlasts the retries", async () => {
    vi.useFakeTimers();
    setSession(auth("current"));
    const ended = vi.fn();
    onSession((s) => s === null && ended());
    const calls = fake(() => json(409, { status: 409, code: "refresh_in_progress" }));
    const r = refreshSession();
    const done = expect(r).rejects.toMatchObject({ code: "refresh_in_progress" });
    await vi.advanceTimersByTimeAsync(3_000);
    await done;
    expect(calls).toHaveLength(3);
    expect(ended).not.toHaveBeenCalled();
    expect(getAccessToken()).toBe("current");
  });
});

describe("proactive refresh", () => {
  it("refreshes before expiry only when the admin was active", async () => {
    vi.useFakeTimers();
    const calls = fake(() => json(200, auth("next")));
    setSession(auth("first", 600));
    await vi.advanceTimersByTimeAsync(600_000);
    expect(calls).toHaveLength(0);

    setSession(auth("second", 600));
    noteActivity(Date.now() + 1);
    await vi.advanceTimersByTimeAsync(541_000);
    expect(calls).toHaveLength(1);
    expect(getAccessToken()).toBe("next");
  });
});

describe("errors", () => {
  it("parse problem+json with the request id from the body or the header", async () => {
    const a = await parseError(json(409, { status: 409, code: "sole_owner", title: "Conflict", detail: "Owns a workspace", request_id: "req_body" }));
    expect(a).toBeInstanceOf(ApiError);
    expect(a).toMatchObject({ status: 409, code: "sole_owner", detail: "Owns a workspace", requestId: "req_body" });
    const b = await parseError(json(500, { status: 500, title: "Internal" }, { "x-request-id": "req_hdr" }));
    expect(b).toMatchObject({ code: "server_error", requestId: "req_hdr" });
    const c = await parseError(new Response("<html>", { status: 502, headers: { "content-type": "text/html" } }));
    expect(c).toMatchObject({ status: 502, code: "server_error", requestId: null });
  });

  it("read as plain messages", () => {
    expect(errorMessage(new ApiError({ status: 0 }))).toMatch(/reach the API/);
    expect(errorMessage(new ApiError({ status: 429 }))).toMatch(/Too many attempts/);
    expect(errorMessage(new ApiError({ status: 422, errors: [{ message: "reason is required" }] }))).toBe("reason is required");
    expect(errorMessage(new ApiError({ status: 403 }))).toMatch(/role/);
    expect(errorMessage("nope")).toBe("Something went wrong");
  });
});
