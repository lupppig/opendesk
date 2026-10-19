// Renders the real route tree in jsdom against a fake API: the sign-in page for
// a visitor without a session, and the signed-in shell with every page.

import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { __resetClientForTests, configureClient, setTransport } from "@/lib/api/client";
import { queryClient } from "@/lib/queryClient";
import { __resetSessionForTests } from "@/lib/session";
import { routeTree } from "@/router";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const me = {
  id: "u1",
  email: "ops@example.com",
  name: "Ops",
  role: "viewer",
  permissions: ["read"],
  instance: { version: "1.2.3", commit: "abcdef1", billing_enabled: true, ai_enabled: true, app_url: "https://app.example.com", api_url: "https://api.example.com", self_hosted: false },
};

const page = <T,>(data: T[]) => ({ data, next_cursor: null });
const now = new Date().toISOString();

const fixtures: Record<string, unknown> = {
  "/v1/admin/summary": {
    users: { total: 1200, new_7d: 12, new_30d: 80, active_7d: 400 },
    workspaces: { total: 300, new_7d: 3, new_30d: 20, active_7d: 120, suspended: 1 },
    billing: { enabled: true, paid_workspaces: 40, billed_seats: 210, past_due: 2 },
    ai: { month_charged_usd: 123.45, month_provider_cost_usd: 60.12, month_calls: 9000 },
    daily: Array.from({ length: 30 }, (_, i) => ({ date: `2026-09-${String(i + 1).padStart(2, "0")}`, signups: i % 5, workspaces: i % 3, ai_charged_usd: i * 0.37 })),
  },
  "/v1/admin/health": {
    version: "1.2.3",
    commit: "abcdef1",
    role: "all",
    started_at: now,
    database: { ok: true, latency_ms: 1.2, schema_version: 631, dirty: false, pool: { total: 4, idle: 3, in_use: 1, max: 20 } },
    nats: { ok: true, embedded: true },
    relay: { unpublished: 0, oldest_unpublished_s: null },
    jobs: { available: 1, running: 0, retryable: 2, scheduled: 3, discarded_24h: 0, oldest_available_s: 4, by_queue: [{ queue: "default", available: 1, running: 0, retryable: 2 }] },
    mail: { driver: "ses", inbound_failed_24h: 0, inbound_pending: 0, mailboxes_in_error: 1 },
    webhooks: { failing_24h: 0 },
    config: { billing: true, ai_provider: "openrouter", decisions_provider: "jev", embeddings: "openai", storage: "s3", signup_enabled: true, mailbox_imap: false, mailbox_system_send: false, push: "direct", help_domain: "" },
    updates: {
      check: true,
      current: "v1.2.3",
      latest: { version: "v1.3.0", url: "https://github.com/useopendesk/opendesk/releases/tag/v1.3.0", published_at: now },
      available: true,
      major: false,
      checked_at: now,
      error: "",
      previous_version: "v1.2.2",
      running_since: now,
      auto: { reported: true, enabled: false, last_run_at: null, last_result: "none", last_detail: "" },
    },
    checks: [
      { name: "Database", status: "ok", detail: "" },
      { name: "Mailboxes", status: "warn", detail: "1 mailbox in error" },
    ],
  },
  "/v1/admin/workspaces": page([{ id: "w1", name: "Acme", slug: "acme", avatar_url: null, members: 3, plan: "pro", status: "active", suspended_at: null, last_activity_at: now, created_at: now }]),
  "/v1/admin/test-workspaces": page([
    {
      workspace: { id: "w9", name: "Acme demo", slug: "acme-demo" },
      owner: { id: "u9", email: "owner-x7k2@acme-demo.invalid", name: "Alex", test_account: true, password_expires_at: now },
      created_by: { id: "u1", email: "ops@example.com" },
      reason: "Sales demo",
      plan: "pro",
      test_expires_at: null,
      sample_data: { status: "running", size: "realistic", kinds: ["tickets", "kb"], counts: null, batches: 1, started_at: now, seeded_at: null, error: "" },
      members: 4,
      suspended_at: null,
      created_at: now,
    },
  ]),
  "/v1/admin/users": page([{ id: "u2", email: "a@acme.test", name: "Ann", avatar_url: null, email_verified: true, totp_enabled: false, has_password: true, workspace_count: 1, admin_role: null, suspended_at: null, last_seen_at: now, created_at: now }]),
  "/v1/admin/jobs/stats": { states: { retryable: 2, discarded: 0 }, kinds: [{ kind: "email.send", available: 1, running: 0, retryable: 2, discarded_24h: 0 }] },
  "/v1/admin/jobs": page([{ id: 7, kind: "email.send", queue: "default", state: "retryable", attempt: 2, max_attempts: 25, args: { message_id: "m1" }, last_error: "smtp: 421", errors: 2, created_at: now, scheduled_at: now, attempted_at: now, finalized_at: null }]),
  "/v1/admin/admins": [{ user: { id: "u1", email: "ops@example.com", name: "Ops" }, role: "viewer", granted_by: null, created_at: now }],
  "/v1/admin/audit": page([{ id: "a1", actor: { type: "cli", id: null, email: "cli" }, action: "admin.granted", target_type: "user", target_id: "u1", data: { role: "viewer" }, ip: "", created_at: now }]),
  "/v1/admin/workspaces/w1": {
    id: "w1",
    name: "Acme",
    slug: "acme",
    avatar_url: null,
    members: 1,
    plan: "pro",
    status: "active",
    suspended_at: now,
    suspended_reason: "Spam reports",
    last_activity_at: now,
    created_at: now,
    members_list: [{ user: { id: "u2", email: "a@acme.test", name: "Ann" }, role: "owner", created_at: now }],
    pending_invitations: 1,
    counts: { tickets: 10, open_tickets: 2, contacts: 5, kb_articles: 1, api_keys: 0, webhooks: 0, brands: 1 },
    mailboxes: [{ id: "m1", name: "Support", email: "help@acme.test", inbound_kind: "forward", outbound_kind: "smtp", status: "error", last_error: "auth failed", last_synced_at: null, created_at: now }],
    entitlements: { max_mailboxes: 3, max_members: 0, monthly_ai_usd: 10, unlimited_ai: false },
    limits: { max_mailboxes: null, max_members: null, monthly_ai_usd: 10 },
    subscription: { plan: "pro", status: "active", interval: "month", seats: 3, billed_seats: 3, current_period_end: now, cancel_at_period_end: false, stripe_customer_id: "cus_1", stripe_subscription_id: "sub_1" },
    collab: { access: "complimentary", until: now, note: "Beta", enabled: true, accounts: 2 },
    ai: { remaining_usd: 4.2, monthly_usd: 10, included_used_usd: 5.8, purchased_usd: 0, unlimited: false, period_start: now, period_end: now, month: { charged_usd: 5.8, provider_cost_usd: 2.1, calls: 300, by_feature: [{ feature: "drafts", charged_usd: 5.8, provider_cost_usd: 2.1, calls: 300 }] } },
    recent_audit: [{ id: "x", actor: { type: "system", user: null, api_key_id: null }, action: "workspace.suspended", entity_type: "workspace", entity_id: "w1", data: { actor: "instance_admin" }, ip: "", created_at: now }],
  },
  "/v1/admin/users/u2": {
    id: "u2",
    email: "a@acme.test",
    name: "Ann",
    avatar_url: null,
    email_verified: false,
    totp_enabled: true,
    has_password: true,
    workspace_count: 1,
    admin_role: null,
    suspended_at: null,
    last_seen_at: now,
    created_at: now,
    suspended_reason: "",
    identities: [{ provider: "google", email: "a@acme.test", created_at: now }],
    passkeys: 1,
    memberships: [{ workspace: { id: "w1", name: "Acme", slug: "acme" }, role: "owner", created_at: now }],
    sessions: [{ id: "s1", client: "web", ip: "203.0.113.4", user_agent: "Mozilla/5.0 (Macintosh) Chrome/130 Safari/537", mfa_verified: true, created_at: now, last_used_at: now }],
  },
};

let signedIn = false;
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

beforeAll(() => {
  // jsdom lacks these; radix and motion read them.
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false })) as unknown as typeof window.matchMedia;
});

let root: Root | null = null;
let host: HTMLDivElement | null = null;

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null;
  queryClient.clear();
  __resetClientForTests();
  __resetSessionForTests();
});

async function render(path: string) {
  __resetClientForTests();
  __resetSessionForTests();
  configureClient({ baseUrl: "https://api.example.com" });
  setTransport(async (url) => {
    const u = new URL(url);
    if (u.pathname === "/v1/admin/auth/refresh") return signedIn ? json(200, { access_token: "t", expires_in: 600, admin: me }) : json(401, { status: 401, code: "unauthenticated" });
    const body = fixtures[u.pathname];
    return body ? json(200, body) : json(404, { status: 404, code: "not_found" });
  });
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }), context: { queryClient }, defaultPendingMs: 0 });
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<RouterProvider router={router} />);
  });
  // Let lazy routes and queries settle.
  for (let i = 0; i < 30; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 10));
    });
  }
  return { router, text: () => document.body.textContent ?? "" };
}

describe("the panel", () => {
  it("asks a visitor without a session to sign in", async () => {
    signedIn = false;
    const { router, text } = await render("/workspaces");
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toMatchObject({ next: "/workspaces" });
    expect(text()).toContain("Sign in to the admin panel");
    expect(text()).toContain("Admin");
  });

  const pages: [string, string][] = [
    ["/", "AI charged this month"],
    ["/workspaces", "Acme"],
    ["/workspaces/w1", "Spam reports"],
    ["/test-workspaces", "Adding realistic data"],
    ["/users", "Ann"],
    ["/users/u2", "Chrome on macOS"],
    ["/jobs", "email.send"],
    ["/health", "1 mailbox in error"],
    ["/health", "opendesk-ctl upgrade v1.3.0"],
    ["/audit", "admin.granted"],
    ["/admins", "ops@example.com"],
    ["/nowhere", "Page not found"],
  ];
  for (const [path, expected] of pages) {
    it(`renders ${path} for a signed-in admin`, async () => {
      signedIn = true;
      const { text } = await render(path);
      expect(text()).toContain(expected);
    });
  }

  it("disables actions a viewer can't take, naming the role", async () => {
    signedIn = true;
    await render("/admins");
    const add = [...document.querySelectorAll("button")].find((b) => b.textContent?.includes("Add an admin"));
    expect(add?.disabled).toBe(true);
    expect(add?.parentElement?.getAttribute("aria-label")).toContain("Needs the Super admin role");
  });
});
