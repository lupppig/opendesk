import { describe, expect, it } from "vitest";
import endpointsSource from "@/lib/api/endpoints.ts?raw";
import { db } from "./db";
import { installMock } from "./index";
import { dispatch, routeList } from "./router";
import { seed } from "./seed";

async function call(method: string, path: string, body?: unknown) {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (db.session) headers.Authorization = `Bearer ${db.session.token}`;
  const r = await dispatch(`http://mock${path}`, { method, body: body === undefined ? undefined : JSON.stringify(body), headers });
  const text = await r.text();
  return { status: r.status, body: text ? JSON.parse(text) : null };
}

const CODE = "123456";

/** "METHOD /path/:param" for every call endpoints.ts makes, templates turned into params. */
function clientRoutes(): string[] {
  const out = new Set<string>();
  const norm = (p: string) => p.replace(/\$\{[^}]+\}/g, ":param");
  for (const m of endpointsSource.matchAll(/http\.(get|post|put|patch|del)<[^(]*>\(\s*[`"]([^`"]+)[`"]/g)) {
    const method = { get: "GET", post: "POST", put: "PUT", patch: "PATCH", del: "DELETE" }[m[1]]!;
    out.add(`${method} ${norm(m[2])}`);
  }
  for (const m of endpointsSource.matchAll(/authRequest<[^(]*>\(\s*[`"]([^`"]+)[`"]/g)) out.add(`POST ${norm(m[1])}`);
  return [...out];
}

describe("admin mock", () => {
  installMock();

  it("has a route for every call the client makes", () => {
    const mocked = new Set(routeList().map((r) => r.replace(/:\w+/g, ":param")));
    const calls = clientRoutes();
    expect(calls.length).toBeGreaterThan(30);
    expect(calls.filter((c) => !mocked.has(c))).toEqual([]);
    // And the refresh the client sends on its own.
    expect(mocked.has("POST /v1/admin/auth/refresh")).toBe(true);
  });

  it("serves the seeded instance on every list and page", async () => {
    const users = await call("GET", "/v1/admin/users");
    expect(users.status).toBe(200);
    expect(users.body.data).toHaveLength(50);
    expect(users.body.next_cursor).toBeTruthy();
    const next = await call("GET", `/v1/admin/users?cursor=${users.body.next_cursor}`);
    expect(next.body.data[0].id).not.toBe(users.body.data[0].id);

    const ws = await call("GET", "/v1/admin/workspaces?limit=100");
    expect(ws.body.data.length).toBeGreaterThan(55);
    expect(ws.body.data.map((w: { name: string }) => w.name)).toContain("Margin");
    const test = await call("GET", "/v1/admin/test-workspaces");
    expect(test.body.data.map((w: { workspace: { name: string } }) => w.workspace.name)).toContain("Acme demo");
    const jobs = await call("GET", "/v1/admin/jobs");
    expect(jobs.body.data.length).toBeGreaterThan(5);
    expect(jobs.body.data.every((j: { state: string; last_error: string }) => j.state === "retryable" && j.last_error)).toBe(true);
    const stats = await call("GET", "/v1/admin/jobs/stats");
    expect(stats.body.states.completed).toBeGreaterThan(50);
    const admins = await call("GET", "/v1/admin/admins");
    expect(admins.body.map((a: { role: string }) => a.role)).toEqual(["super", "super", "support", "support", "viewer"]);
    const audit = await call("GET", "/v1/admin/audit");
    expect(audit.body.data).toHaveLength(50);
    expect(audit.body.next_cursor).toBeTruthy();
    const summary = await call("GET", "/v1/admin/summary");
    expect(summary.body.users.total).toBeGreaterThan(150);
    expect(summary.body.daily).toHaveLength(30);
    const health = await call("GET", "/v1/admin/health");
    expect(health.body.checks.filter((c: { status: string }) => c.status !== "ok").map((c: { name: string }) => c.name)).toEqual(["Mailboxes"]);
    for (const path of ["/v1/admin/me", `/v1/admin/users/${users.body.data[0].id}`, `/v1/admin/workspaces/${ws.body.data[0].id}`]) expect((await call("GET", path)).status, path).toBe(200);
  });

  it("searches and filters", async () => {
    const maya = await call("GET", "/v1/admin/users?q=maya");
    expect(maya.body.data.map((u: { name: string }) => u.name)).toEqual(["Maya Chen"]);
    const byMember = await call("GET", "/v1/admin/workspaces?q=maya%40margin.app");
    expect(byMember.body.data.map((w: { name: string }) => w.name)).toEqual(["Margin"]);
    const suspended = await call("GET", "/v1/admin/workspaces?filter=suspended");
    expect(suspended.body.data.map((w: { name: string }) => w.name).sort()).toEqual(["Lumen Dental", "Quick Gift Rewards"]);
    const collab = (await call("GET", "/v1/admin/workspaces?filter=collab")).body.data.map((w: { name: string }) => w.name);
    expect(collab).toEqual(expect.arrayContaining(["Northwind Traders", "Copperleaf", "Vantage Legal", "Northwind pilot"]));
    const admins = await call("GET", "/v1/admin/users?filter=admins");
    expect(admins.body.data).toHaveLength(5);
    const failed = await call("GET", "/v1/admin/jobs?state=discarded&kind=admin.seed_test_workspace");
    expect(failed.body.data[0].last_error).toContain("context deadline exceeded");
  });

  it("applies actions and refuses what the API refuses", async () => {
    const victim = (await call("GET", "/v1/admin/users?q=sam%40margin.app")).body.data[0];
    expect((await call("POST", `/v1/admin/users/${victim.id}/suspend`, { reason: "" })).status).toBe(422);
    const s = await call("POST", `/v1/admin/users/${victim.id}/suspend`, { reason: "Testing" });
    expect(s.body.suspended_at).toBeTruthy();
    expect((await call("GET", "/v1/admin/users?filter=suspended")).body.data.map((u: { id: string }) => u.id)).toContain(victim.id);

    const kofi = db.users.find((u) => u.email === "kofi@opendesk.example")!;
    expect((await call("PATCH", `/v1/admin/admins/${kofi.id}`, { role: "super" })).status).toBe(403);
    expect((await call("PATCH", `/v1/admin/admins/${kofi.id}`, { role: "super", code: "000000" })).body.code).toBe("invalid_code");
    expect((await call("PATCH", `/v1/admin/admins/${kofi.id}`, { role: "super", code: CODE })).body.role).toBe("super");
    expect((await call("POST", "/v1/admin/admins", { email: "ben@opendesk.example", role: "viewer", code: CODE })).body.code).toBe("admin_mfa_setup_required");

    const job = (await call("GET", "/v1/admin/jobs")).body.data[0];
    expect((await call("POST", `/v1/admin/jobs/${job.id}/retry`)).body.state).toBe("available");
    expect((await call("POST", `/v1/admin/jobs/${job.id}/retry`)).status).toBe(409);

    const created = await call("POST", "/v1/admin/test-workspaces", {
      code: CODE,
      name: "QA demo",
      owner: { email: "owner-abcd@qa-demo.invalid" },
      password_days: 2,
      plan: "pro",
      sample: { size: "small", kinds: ["tickets"] },
      delete_after_days: 7,
      reason: "A test",
    });
    expect(created.status).toBe(201);
    expect(created.body.owner.password).toMatch(/^[A-Za-z2-9]{6}(-[A-Za-z2-9]{6}){3}$/);
    expect(created.body.workspace.sample_data.status).toBe("running");
    const wid = created.body.workspace.workspace.id;
    expect((await call("POST", `/v1/admin/workspaces/${wid}/sample-data`, { size: "small", kinds: ["kb"] })).body.code).toBe("seeding");
    expect((await call("POST", "/v1/admin/test-workspaces", { code: CODE, name: "X", owner: { email: "real@example.com" }, plan: "free", reason: "r" })).body.code).toBe("test_email_required");
    expect((await call("DELETE", `/v1/admin/workspaces/${wid}`, { code: CODE, confirm_name: "nope" })).body.code).toBe("confirm_name_mismatch");
    expect((await call("DELETE", `/v1/admin/workspaces/${wid}`, { code: CODE, confirm_name: "QA demo" })).status).toBe(204);
    expect((await call("GET", `/v1/admin/workspaces/${wid}`)).status).toBe(404);
    expect(db.users.some((u) => u.email === "owner-abcd@qa-demo.invalid")).toBe(false);

    const audit = (await call("GET", "/v1/admin/audit?limit=6")).body.data.map((e: { action: string }) => e.action);
    expect(audit).toEqual(["workspace.deleted", "test_workspace.created", "job.retried", "admin.role_changed", "user.suspended", expect.any(String)]);
  });

  it("holds the panel to the admin's role and session", async () => {
    const elena = db.users.find((u) => u.email === "elena@opendesk.example")!;
    db.session = { userId: elena.id, token: "viewer-token" };
    const w = db.workspaces[0];
    expect((await call("GET", `/v1/admin/workspaces/${w.id}`)).status).toBe(200);
    expect((await call("POST", `/v1/admin/workspaces/${w.id}/suspend`, { reason: "x" })).status).toBe(403);
    db.session = null;
    expect((await call("GET", "/v1/admin/users")).status).toBe(401);
    expect((await call("POST", "/v1/admin/auth/refresh")).status).toBe(401);
    const login = await call("POST", "/v1/admin/auth/login", { email: "ava@opendesk.example", password: "anything" });
    expect(login.status).toBe(200);
    expect((await call("POST", "/v1/admin/auth/mfa", { mfa_token: login.body.mfa_token, code: "000000" })).body.code).toBe("invalid_code");
    const done = await call("POST", "/v1/admin/auth/mfa", { mfa_token: login.body.mfa_token, code: CODE });
    expect(done.body.admin.permissions).toContain("admins.manage");
    expect((await call("POST", "/v1/admin/auth/login", { email: "maya@margin.app", password: "x" })).body.code).toBe("not_admin");
    expect((await call("POST", "/v1/admin/auth/login", { email: "nobody@example.com", password: "x" })).body.code).toBe("invalid_credentials");
  });

  it("seeds the same instance every time", () => {
    seed();
    const a = { users: db.users.map((u) => u.id), jobs: db.jobs.map((j) => j.id), audit: db.audit.length };
    seed();
    expect({ users: db.users.map((u) => u.id), jobs: db.jobs.map((j) => j.id), audit: db.audit.length }).toEqual(a);
  });
});
