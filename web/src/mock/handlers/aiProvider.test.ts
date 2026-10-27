import { afterEach, describe, expect, it } from "vitest";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";
import { setAIKey, setDecisionKey } from "../aiKey";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: ws ? { "OD-Workspace": ws } : {} });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

const OR_KEY = "sk-or-v1-0123456789abcdefWXYZ";

describe("mock AI provider (own key)", () => {
  installMock();
  afterEach(() => {
    setAIKey(db.workspaces[0], null);
    setDecisionKey(db.workspaces[0], null);
  });

  it("starts on the instance provider", async () => {
    const r = await call("GET", "/v1/ai/provider");
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ mode: "instance", key: null });
  });

  it("tests a key without saving it and explains failures", async () => {
    const ok = await call("POST", "/v1/ai/provider/test", { kind: "openrouter", api_key: OR_KEY });
    expect(ok.body.ok).toBe(true);
    expect(ok.body.models.length).toBeGreaterThan(3);
    expect((await call("GET", "/v1/ai/provider")).body.key).toBeNull();

    const bad = await call("POST", "/v1/ai/provider/test", { kind: "openrouter", api_key: "sk-or-bad-12345678" });
    expect(bad.body).toMatchObject({ ok: false, reason: "invalid_key" });
    expect(bad.body.message).not.toMatch(/—/);
    const funds = await call("POST", "/v1/ai/provider/test", { kind: "anthropic", api_key: "sk-ant-nofunds-1234" });
    expect(funds.body).toMatchObject({ ok: false, reason: "no_funds" });
  });

  it("refuses private, loopback and plain http endpoints", async () => {
    for (const base_url of ["http://api.example.com/v1", "https://127.0.0.1/v1", "https://10.1.2.3/v1", "https://169.254.169.254/v1", "https://localhost:8080/v1", "https://u:p@api.example.com/v1"]) {
      const r = await call("PUT", "/v1/ai/provider", { kind: "openai", api_key: "sk-test-12345678", base_url, model: "qwen-3.5-32b" });
      expect(r.status, base_url).toBe(422);
    }
    expect((await call("GET", "/v1/ai/provider")).body.key).toBeNull();
  });

  it("saves a key, shows only its last 4 characters, runs AI at $0 on the key and removes it", async () => {
    const saved = await call("PUT", "/v1/ai/provider", { kind: "openrouter", api_key: OR_KEY });
    expect(saved.status).toBe(200);
    expect(JSON.stringify(saved.body)).not.toContain("0123456789abcdef");
    expect(saved.body).toMatchObject({ mode: "own_key", key: { kind: "openrouter", last4: "WXYZ", status: "ok", model: "anthropic/claude-sonnet-5.5" } });
    const ws = db.workspaces[0];
    expect(ws.audit[0].action).toBe("ai.provider_key_set");

    const cfg = (await call("GET", "/v1/ai/config")).body;
    expect(cfg).toMatchObject({ own_key: true, provider: "openrouter" });
    const models = (await call("GET", "/v1/ai/models")).body;
    expect(models.data.some((m: { id: string }) => m.id === "mistralai/mistral-large-3")).toBe(true);
    expect(models.data.every((m: { usd_per_mtok: unknown; typical_reply_usd: unknown }) => m.usd_per_mtok === null && m.typical_reply_usd === null)).toBe(true);
    // Jev decisions run on the OpenRouter key too: nothing is priced.
    expect(Object.keys(cfg.costs)).toEqual([]);
    expect(cfg.decisions_own_key).toBe(true);
    expect((await call("GET", "/v1/ai/provider")).body.decisions).toMatchObject({ runs_on: "provider_key", engine: "jev", key: null });
    expect((await call("PATCH", "/v1/ai/settings", { model: "claude-sonnet-5-5" })).status).toBe(422);
    expect((await call("PATCH", "/v1/ai/settings", { model: "mistralai/mistral-large-3" })).status).toBe(200);

    // No AI balance left: calls on the key still run, recorded at $0.
    const used = ws.billing.ai.used_usd;
    const spent = ws.billing.ai.monthly_usd + ws.billing.ai.purchased_usd;
    ws.billing.ai.used_usd = spent;
    const before = ws.aiUsage.length;
    const r = await call("POST", "/v1/ai/rewrite", { html: "<p>thanks for waiting</p>", mode: "friendlier" });
    expect(r.status).toBeLessThan(300);
    const row = ws.aiUsage[ws.aiUsage.length - 1];
    expect(ws.aiUsage.length).toBe(before + 1);
    expect(row).toMatchObject({ source: "own_key", micro_usd: 0 });
    expect(ws.billing.ai.used_usd).toBe(spent);
    ws.billing.ai.used_usd = used;
    const usage = (await call("GET", "/v1/billing/usage")).body;
    if (usage.by_model) {
      expect(usage.own_key_calls).toBeGreaterThan(0);
      expect(usage.by_model.some((m: { source: string }) => m.source === "own_key")).toBe(true);
    }
    await call("PATCH", "/v1/ai/settings", { model: "" });

    // Replace, then remove: back to the AI balance.
    expect((await call("PUT", "/v1/ai/provider", { kind: "anthropic", api_key: "sk-ant-api03-abcd9876" })).body.key.last4).toBe("9876");
    expect(ws.audit[0].action).toBe("ai.provider_key_replaced");
    expect((await call("DELETE", "/v1/ai/provider")).status).toBe(204);
    expect(ws.audit[0].action).toBe("ai.provider_key_removed");
    expect((await call("GET", "/v1/ai/config")).body.own_key).toBe(false);
  });

  it("fails like an outage when the provider rejects the key, and tells admins once", async () => {
    await call("PUT", "/v1/ai/provider", { kind: "openrouter", api_key: "sk-or-revoked-1234" });
    const ws = db.workspaces[0];
    const notes = () => ws.notifications.filter((n) => n.type === "ai_key_failed").length;
    const n0 = notes();
    const r = await call("POST", "/v1/ai/rewrite", { html: "<p>hi</p>", mode: "shorter" });
    expect(r.status).toBe(502);
    expect(r.body.code).toBe("ai_key_rejected");
    expect(notes()).toBe(n0 + 1);
    expect((await call("POST", "/v1/ai/rewrite", { html: "<p>hi</p>", mode: "shorter" })).status).toBe(502);
    expect(notes()).toBe(n0 + 1);
    const p = (await call("GET", "/v1/ai/provider")).body;
    expect(p.key.status).toBe("rejected");
    expect(p.key.status_at).toBeTruthy();
  });

  it("runs decisions on a TypeSafe key, which wins over the provider key", async () => {
    const ws = db.workspaces[0];
    expect((await call("GET", "/v1/ai/provider")).body.decisions).toMatchObject({ runs_on: "instance", engine: "jev", key: null });
    expect((await call("POST", "/v1/ai/provider/decisions/test", { api_key: "ts-bad-key-1234" })).body).toMatchObject({ ok: false, reason: "invalid_key" });
    expect((await call("PUT", "/v1/ai/provider/decisions", { api_key: "ts-nofunds-1234" })).status).toBe(422);

    await call("PUT", "/v1/ai/provider", { kind: "anthropic", api_key: "sk-ant-0123456789" });
    expect((await call("GET", "/v1/ai/config")).body.costs.triage).toBeGreaterThan(0);
    const saved = await call("PUT", "/v1/ai/provider/decisions", { api_key: "ts-own-key-ABCD" });
    expect(saved.status).toBe(200);
    expect(JSON.stringify(saved.body)).not.toContain("ts-own-key");
    expect(saved.body.decisions).toMatchObject({ runs_on: "typesafe_key", engine: "jev", key: { last4: "ABCD", status: "ok" } });
    expect(ws.audit[0].action).toBe("ai.decisions_key_set");
    const cfg = (await call("GET", "/v1/ai/config")).body;
    expect(cfg.decisions_own_key).toBe(true);
    expect(cfg.costs.triage).toBeUndefined();

    expect((await call("DELETE", "/v1/ai/provider/decisions")).status).toBe(204);
    expect(ws.audit[0].action).toBe("ai.decisions_key_removed");
    expect((await call("GET", "/v1/ai/provider")).body.decisions.runs_on).toBe("instance");
  });
});
