import { afterEach, describe, expect, it } from "vitest";
import type { Mailbox, MailboxCause } from "@/lib/api/types";
import { config } from "@/lib/config";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";
import catalogue from "../mailCauses.json";
import { mailCause, refusal } from "./mailhost";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: ws ? { "OD-Workspace": ws } : {} });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

// mailCauses.json is the API's catalogue (internal/email/mailcause), checked by its Go test.
describe("mock mail causes", () => {
  it("is the API's catalogue", () => {
    const codes = catalogue.map((c) => c.code);
    expect(new Set(codes).size).toBe(codes.length);
    for (const c of catalogue as MailboxCause[]) {
      expect(c.code).toMatch(/^[a-z0-9_]+$/);
      expect(c.title.length).toBeGreaterThan(0);
      expect(c.fix.length).toBeGreaterThan(0);
      expect(typeof c.retryable).toBe("boolean");
      expect(c.title + c.fix).not.toContain("—");
    }
    expect(codes).toContain("google_app_password_required");
    expect(() => mailCause("google_app_password")).toThrow();
  });

  it("answers refusals with catalogue entries", () => {
    const cases: [string, string, boolean, string][] = [
      ["gmail", "my-account-password", false, "google_app_password_required"],
      ["yahoo", "short", false, "yahoo_app_password_required"],
      ["aol", "short", true, "aol_app_password_required"],
      ["microsoft365", "wrong", true, "microsoft_smtp_auth_disabled"],
      ["outlook", "wrong", false, "microsoft_basic_auth_disabled"],
      ["other", "wrong", false, "auth_refused"],
    ];
    for (const [host, pw, smtpOnly, code] of cases) {
      const r = refusal(host, pw, smtpOnly);
      expect(r?.cause).toEqual(catalogue.find((c) => c.code === code));
      expect(r?.inbound.ok).toBe(smtpOnly);
    }
    expect(refusal("gmail", "abcdefghijklmnop", false)).toBeNull();
    expect(mailCause("microsoft_smtp_auth_disabled").fix).toContain("phasing out password sign-in for SMTP AUTH");
  });
});

describe("mock mailbox sending", () => {
  installMock();

  it("moves a forwarding mailbox to its own SMTP server and back", async () => {
    const created = await call("POST", "/v1/mailboxes", { name: "Sales", email: "sales@sending.example", inbound_kind: "forwarding", outbound_kind: "system" });
    expect(created.status).toBe(201);
    const url = `/v1/mailboxes/${(created.body as Mailbox).id}`;
    const smtp = (pw?: string) => ({
      outbound_kind: "smtp",
      verify: true,
      config: { smtp_host: "smtp.sending.example", smtp_port: 587, smtp_tls: "starttls", smtp_username: "sales@sending.example" },
      ...(pw ? { smtp_password: pw } : {}),
    });

    expect((await call("PATCH", url, smtp())).status).toBe(422);
    expect((await call("PATCH", url, { ...smtp("pw-123456"), config: { smtp_host: "10.0.0.5" } })).status).toBe(422);
    const refused = await call("PATCH", url, smtp("wrong"));
    expect(refused.status).toBe(422);
    expect(refused.body).toMatchObject({ code: "mailbox_connection_failed", cause: { code: "auth_refused" }, inbound: { ok: true }, outbound: { ok: false } });
    expect(((await call("GET", url)).body as Mailbox).outbound_kind).toBe("system");

    const ok = await call("PATCH", url, smtp("pw-123456"));
    expect(ok.status).toBe(200);
    expect((ok.body as Mailbox).outbound_kind).toBe("smtp");
    expect((ok.body as Mailbox).config.smtp_host).toBe("smtp.sending.example");
    // The stored password is kept when none is sent.
    const kept = await call("PATCH", url, { verify: true, config: { smtp_port: 465, smtp_tls: "tls" } });
    expect(kept.body).toMatchObject({ outbound_kind: "smtp" });
    const test = await call("POST", `${url}/test`);
    expect(test.body).toMatchObject({ outbound: { ok: true } });

    const back = await call("PATCH", url, { outbound_kind: "system" });
    expect((back.body as Mailbox).config.smtp_host).toBeUndefined();
    // No password is left to sign in with.
    expect((await call("PATCH", url, { ...smtp(), verify: false })).status).toBe(422);
  });
});

describe("mock mailbox receiving", () => {
  installMock();
  afterEach(() => {
    config.mailboxImap = true;
  });
  const list = async () => (await call("GET", "/v1/mailboxes")).body.data as Mailbox[];

  it("seeds one mailbox of each kind", async () => {
    const kinds = new Set((await list()).map((m) => `${m.inbound_kind}+${m.outbound_kind}`));
    for (const k of ["google+google", "forwarding+system", "forwarding+smtp", "forwarding+google", "forwarding+microsoft", "imap+smtp"]) expect(kinds).toContain(k);
    expect((await list()).every((m) => m.receiving === "ok")).toBe(true);
  });

  it("connects Google to send only by default, or to sync", async () => {
    const start = (email: string, body: Record<string, unknown> = {}) => call("POST", "/v1/mailboxes/oauth/google/start", { name: "Desk", email, ...body });
    const sendOnly = await start("desk@acme.io");
    expect(sendOnly.status).toBe(200);
    const id = new URL(sendOnly.body.url, "http://x").searchParams.get("connected");
    const m = (await list()).find((x) => x.id === id)!;
    expect(m).toMatchObject({ inbound_kind: "forwarding", outbound_kind: "google", receiving: "ok" });
    expect(m.forwarding_address).toMatch(/@in\.opendesk\.sh$/);

    const synced = await start("sync@acme.io", { receive: "sync" });
    const s = (await list()).find((x) => x.id === new URL(synced.body.url, "http://x").searchParams.get("connected"))!;
    expect(s).toMatchObject({ inbound_kind: "google", outbound_kind: "google" });
    expect((await start("other@acme.io", { receive: "pop3" })).status).toBe(422);
  });

  it("reads a Google inbox directly only where the workspace has Collab", async () => {
    const start = (email: string) => call("POST", "/v1/mailboxes/oauth/google/start", { name: "Desk", email, receive: "api" });
    const refused = await start("direct@acme.io");
    expect(refused.status).toBe(422);
    expect(refused.body.code).toBe("api_sync_unavailable");
    localStorage.setItem("od.mock.apiSync", "on");
    try {
      expect((await call("GET", "/v1/mailboxes/connect-options")).body.google.api_sync).toBe(true);
      config.mailboxImap = false;
      const ok = await start("direct@acme.io");
      const m = (await list()).find((x) => x.id === new URL(ok.body.url, "http://x").searchParams.get("connected"))!;
      expect(m).toMatchObject({ inbound_kind: "google", outbound_kind: "google", receiving: "ok" });
      expect(m.config).toMatchObject({ oauth_grant: "api", oauth_client: "collab" });
    } finally {
      localStorage.removeItem("od.mock.apiSync");
    }
  });

  it("tidies up in Gmail only for a mailbox that reads it directly", async () => {
    localStorage.setItem("od.mock.apiSync", "on");
    try {
      const ok = await call("POST", "/v1/mailboxes/oauth/google/start", { name: "Desk", email: "tidy@acme.io", receive: "api" });
      const id = new URL(ok.body.url, "http://x").searchParams.get("connected")!;
      const on = await call("PATCH", `/v1/mailboxes/${id}`, { config: { provider_mark_read: true, provider_label: true } });
      expect(on.status).toBe(200);
      expect(on.body.config).toMatchObject({ oauth_grant: "api", provider_mark_read: true, provider_label: true });
      const off = await call("PATCH", `/v1/mailboxes/${id}`, { config: { provider_label: false } });
      expect(off.body.config.provider_label).toBeUndefined();
      const forwarding = (await list()).find((m) => m.inbound_kind === "forwarding")!;
      expect((await call("PATCH", `/v1/mailboxes/${forwarding.id}`, { config: { provider_archive_on_solve: true } })).status).toBe(422);
    } finally {
      localStorage.removeItem("od.mock.apiSync");
    }
  });

  it("refuses receiving over IMAP while it is off, and switches to forwarding", async () => {
    config.mailboxImap = false;
    const mbs = await list();
    const support = mbs.find((m) => m.email === "support@margin.app")!;
    const partners = mbs.find((m) => m.email === "partners@margin.app")!;
    expect(support.receiving).toBe("imap_off");
    expect(partners.receiving).toBe("imap_off");
    expect(mbs.filter((m) => m.inbound_kind === "forwarding").every((m) => m.receiving === "ok")).toBe(true);

    const imap = await call("POST", "/v1/mailboxes", {
      name: "Ops",
      email: "ops@imap-off.example",
      inbound_kind: "imap",
      outbound_kind: "smtp",
      config: { imap_host: "imap.imap-off.example", imap_username: "ops", smtp_host: "smtp.imap-off.example", smtp_username: "ops" },
      password: "pw",
    });
    expect(imap.status).toBe(422);
    expect(imap.body).toMatchObject({ code: "imap_off", detail: "Receiving over IMAP is off on this instance. Forward the address to opendesk instead." });
    expect((await call("POST", "/v1/mailboxes/oauth/google/start", { name: "Ops", email: "ops2@acme.io", receive: "sync" })).body.code).toBe("imap_off");
    const billing = mbs.find((m) => m.email === "billing@margin.app")!;
    expect((await call("PATCH", `/v1/mailboxes/${billing.id}`, { inbound_kind: "imap" })).body.code).toBe("imap_off");

    // Switching keeps how replies are sent.
    const switched = await call("PATCH", `/v1/mailboxes/${partners.id}`, { inbound_kind: "forwarding" });
    expect(switched.status).toBe(200);
    expect(switched.body).toMatchObject({ inbound_kind: "forwarding", outbound_kind: "smtp", receiving: "ok", status: "active", last_error: "" });
    expect((switched.body as Mailbox).config.imap_host).toBeUndefined();
    expect((switched.body as Mailbox).config.smtp_host).toBe("smtp.fastmail.com");

    // Reconnecting a sync while IMAP is off connects it to send only.
    await call("POST", "/v1/mailboxes/oauth/google/start", { mailbox_id: support.id, name: support.name, email: support.email });
    expect(((await call("GET", `/v1/mailboxes/${support.id}`)).body as Mailbox)).toMatchObject({ inbound_kind: "forwarding", outbound_kind: "google", receiving: "ok" });
  });
});
