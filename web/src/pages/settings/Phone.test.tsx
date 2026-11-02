import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import { ConfirmProvider } from "@/components/ui/confirm";
import { TooltipProvider } from "@/components/ui/tooltip";
import { useSession } from "@/lib/session";
import { installMock } from "@/mock";
import { db } from "@/mock/db";
import { dispatch } from "@/mock/router";
import Phone, { missedCallAutomation } from "./Phone";

vi.mock("@tanstack/react-router", async (orig) => ({ ...(await orig<typeof import("@tanstack/react-router")>()), useNavigate: () => vi.fn() }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0].workspace.id;
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: { "OD-Workspace": ws } });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

const settle = () => act(async () => new Promise((r) => setTimeout(r, 30)));

/** Waits for the mock API's answers to render. */
async function until(el: HTMLElement, text: string) {
  for (let i = 0; i < 100 && !(el.textContent ?? "").includes(text); i++) await settle();
  expect(el.textContent).toContain(text);
}

describe("Phone settings", () => {
  installMock();
  let root: Root;
  let el: HTMLDivElement;

  beforeEach(() => {
    el = document.createElement("div");
    document.body.appendChild(el);
    root = createRoot(el);
    const ws = db.workspaces[0].workspace;
    useSession.setState({ status: "authed", workspaceId: ws.id, memberships: [{ workspace: ws, role: "admin" }] } as never);
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
  });

  const mount = async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    await act(async () =>
      root.render(
        <QueryClientProvider client={qc}>
          <TooltipProvider>
            <ConfirmProvider>
              <Phone />
            </ConfirmProvider>
          </TooltipProvider>
        </QueryClientProvider>,
      ),
    );
    await settle();
  };

  it("shows the connection's health and saves a placement change", async () => {
    await mount();
    await until(el, "Margin support line");
    const text = el.textContent ?? "";
    expect(text).toContain("Receiving calls");
    expect(text).toContain("Calls are sent to opendesk.");
    expect(text).toContain("Calls from Margin support line");
    const assign = el.querySelector<HTMLButtonElement>('[aria-label="Assign answered calls"]')!;
    expect(assign.getAttribute("aria-checked")).toBe("true");
    await act(async () => assign.click());
    for (let i = 0; i < 100 && db.workspaces[0].phone[0].assign_answered; i++) await settle();
    expect(db.workspaces[0].phone[0].assign_answered).toBe(false);
  });

  it("offers Aircall once disconnected", async () => {
    const saved = db.workspaces[0].phone;
    db.workspaces[0].phone = [];
    try {
      await mount();
      await until(el, "No phone system yet");
      await until(el, "Phone systems you can connect");
    } finally {
      db.workspaces[0].phone = saved;
    }
  });

  it("shows a Voys connection waiting for its first call, with the Freedom steps", async () => {
    const saved = db.workspaces[0].phone;
    db.workspaces[0].phone = [];
    try {
      const made = await call("POST", "/v1/phone/connections", { provider: "voys", credentials: { api_token: "demo" } });
      expect(made.status).toBe(201);
      await mount();
      await until(el, "Waiting for the first call");
      const text = el.textContent ?? "";
      expect(text).toContain("No call has come in yet. Add the webhook address below in Voys");
      expect(text).toContain("Webhook address, to add in Voys as a call notification");
      expect(text).toContain("open Integratie, then Gespreksnotificaties, and click Toevoegen");
      expect(el.querySelectorAll('[data-testid="webhook-steps"] li')).toHaveLength(5);
      expect(text).toContain(made.body.webhook_url);
      // Voys shows no cards, and keeps no lasting page to open.
      expect(text).not.toContain("Show the caller in Aircall");
      expect(el.querySelector('[aria-label="Make a new webhook address for Margin"]')).not.toBeNull();
    } finally {
      db.workspaces[0].phone = saved;
    }
  });

  it("builds the missed call example for the call_ended trigger", () => {
    const a = missedCallAutomation("Missed call", "Call back.");
    expect(a.trigger).toBe("call_ended");
    expect(a.enabled).toBe(false);
    expect(a.conditions.map((c) => `${c.field}=${c.value}`)).toEqual(["call_direction=inbound", "call_outcome=missed", "call_time=outside_business_hours"]);
    expect(a.actions.map((x) => x.type)).toEqual(["set_status", "add_note", "set_priority"]);
  });
});

describe("mock phone API", () => {
  installMock();

  it("refuses wrong credentials and a second connection", async () => {
    const saved = db.workspaces[0].phone;
    db.workspaces[0].phone = [];
    try {
      expect((await call("POST", "/v1/phone/connections", { provider: "aircall", credentials: { api_id: "nope", api_token: "x" } })).status).toBe(422);
      const ok = await call("POST", "/v1/phone/connections", { provider: "aircall", credentials: { api_id: "demo", api_token: "x" } });
      expect(ok.status).toBe(201);
      expect(ok.body.health_status).toBe("healthy");
      expect((await call("POST", "/v1/phone/connections", { provider: "aircall", credentials: { api_id: "demo", api_token: "x" } })).status).toBe(409);
      expect((await call("GET", "/v1/phone/lines")).body.data.length).toBeGreaterThan(0);
      expect((await call("DELETE", `/v1/phone/connections/${ok.body.id}`)).status).toBe(204);
    } finally {
      db.workspaces[0].phone = saved;
    }
  });

  it("connects Voys by its token and gives it a new webhook address on request", async () => {
    const saved = db.workspaces[0].phone;
    db.workspaces[0].phone = [];
    try {
      expect((await call("POST", "/v1/phone/connections", { provider: "voys", credentials: { api_token: "nope" } })).status).toBe(422);
      const ok = await call("POST", "/v1/phone/connections", { provider: "voys", credentials: { api_token: "demo" } });
      expect(ok.status).toBe(201);
      expect(ok.body).toMatchObject({ provider: "voys", health_status: "action_required", health_code: "webhook_missing", last_event_at: null, insight_cards: false });
      expect(ok.body.webhook_url).toMatch(new RegExp(`/v1/phone/hooks/voys/${ok.body.id}/[A-Za-z0-9_-]{43}$`));
      const rotated = await call("POST", `/v1/phone/connections/${ok.body.id}/rotate-webhook`);
      expect(rotated.status).toBe(200);
      expect(rotated.body.webhook_url).not.toBe(ok.body.webhook_url);
      expect((await call("GET", "/v1/phone/lines")).body.data.map((l: { provider: string }) => l.provider)).toEqual(["voys"]);
      const aircall = await call("POST", "/v1/phone/connections", { provider: "aircall", credentials: { api_id: "demo", api_token: "x" } });
      expect((await call("POST", `/v1/phone/connections/${aircall.body.id}/rotate-webhook`)).status).toBe(422);
    } finally {
      db.workspaces[0].phone = saved;
    }
  });

  it("seeds a phone ticket with an answered and a missed call", async () => {
    const ws = db.workspaces[0];
    const calls = ws.activities.filter((a) => a.type === "call");
    expect(calls.map((a) => a.data.outcome)).toEqual(["answered", "missed"]);
    const t = ws.tickets.find((x) => x.id === calls[0].ticket_id)!;
    expect(t.channel).toBe("phone");
    expect(t.status).toBe("open");
    expect(ws.automations.some((a) => a.trigger === "call_ended")).toBe(true);
  });
});
