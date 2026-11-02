import { describe, expect, it } from "vitest";
import type { Bot, PlaygroundDone, Procedure } from "@/lib/api/types";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: ws ? { "OD-Workspace": ws } : {} });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

const events = (type: string, since = 0) => db.workspaces[0].events.slice(since).filter((e) => e.type === type);
const list = async () => (await call("GET", "/v1/ai/procedures")).body.data as Procedure[];

describe("mock procedures", () => {
  installMock();

  it("seeds procedures, validates and saves new ones", async () => {
    expect((await list()).map((p) => p.name)).toContain("Refund requests");
    const bad = [
      { name: "", when: "A refund", instructions: "x" },
      { name: "Refunds", when: "ab", instructions: "x" },
      { name: "Refunds", when: "A refund", instructions: " " },
      { name: "Refunds", when: "A refund", instructions: "x", channels: ["sms"] },
      { name: "Refunds", when: "A refund", instructions: "x", bot_ids: ["not-a-bot"] },
    ];
    for (const b of bad) expect((await call("POST", "/v1/ai/procedures", b)).status, JSON.stringify(b)).toBe(422);

    const since = db.workspaces[0].events.length;
    const made = await call("POST", "/v1/ai/procedures", { name: "  Linear   issues ", when: "A bug needs an engineer", instructions: "Create the issue." });
    expect(made.status).toBe(201);
    expect(made.body).toMatchObject({ name: "Linear issues", enabled: true, channels: ["chat", "email"], bot_ids: [] });
    expect(events("ai_procedure.created", since)).toHaveLength(1);

    const patched = await call("PATCH", `/v1/ai/procedures/${made.body.id}`, { enabled: false, channels: [] });
    expect(patched.body).toMatchObject({ enabled: false, channels: [] });
    expect(events("ai_procedure.updated", since)).toHaveLength(1);

    expect((await call("DELETE", `/v1/ai/procedures/${made.body.id}`)).status).toBe(204);
    expect(events("ai_procedure.deleted", since)).toHaveLength(1);
    expect((await list()).some((p) => p.id === made.body.id)).toBe(false);
  });

  it("follows the matching procedure in the playground, and keeps a bot it names", async () => {
    const refund = (await list()).find((p) => p.name === "Refund requests")!;
    const done = (await call("POST", "/v1/ai/agent/playground", { messages: [{ role: "visitor", text: "I was charged twice, can I get a refund?" }] })).body as PlaygroundDone;
    expect(done.procedures).toEqual([{ id: refund.id, name: refund.name }]);

    const bot = (await call("POST", "/v1/bots", { name: "Pip" })).body as Bot;
    const scoped = await call("POST", "/v1/ai/procedures", { name: "Pip only", when: "Pip's topic", instructions: "x", bot_ids: [bot.id] });
    expect(scoped.status).toBe(201);
    const busy = await call("DELETE", `/v1/bots/${bot.id}`);
    expect(busy.status).toBe(409);
    expect(busy.body.detail).toContain("Pip only procedure");
  });

  it("lets automations run a procedure, never on ticket_updated", async () => {
    const proc = (await list())[0];
    const action = [{ type: "run_procedure", value: proc.id }];
    expect((await call("POST", "/v1/automations", { name: "Loop", trigger: "ticket_updated", actions: action })).status).toBe(422);
    expect((await call("POST", "/v1/automations", { name: "Unknown", trigger: "ticket_created", actions: [{ type: "run_procedure", value: crypto.randomUUID() }] })).status).toBe(422);
    const made = await call("POST", "/v1/automations", { name: "Linear", trigger: "customer_replied", actions: action });
    expect(made.status).toBe(201);
    expect((await call("PATCH", `/v1/automations/${made.body.id}`, { trigger: "ticket_updated" })).status).toBe(422);
  });
});
