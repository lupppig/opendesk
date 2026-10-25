import { describe, expect, it } from "vitest";
import type { AssistantStep } from "@/components/assistant/api";
import { dispatch as streamDispatch, parseSse } from "@/components/assistant/testing";
import type { Page, PeerAsk, PeerSettings } from "@/lib/api/types";
import { peerInput } from "@/lib/assistantPeer";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, {
    method,
    body: body ? JSON.stringify(body) : undefined,
    headers: ws ? { "OD-Workspace": ws } : {},
  });
  return { status: r.status, body: await r.json() };
}

const settings = async () => (await call("GET", "/v1/me/assistant-peer")).body as PeerSettings;

describe("agent-to-agent (mock)", () => {
  installMock();

  it("seeds Maya's own settings with her own accounts only", async () => {
    const s = await settings();
    expect(s).toMatchObject({ available: true, enabled: true, share_with: "chosen", all_accounts: true, default_mode: "summary" });
    expect(s.audience.users.map((u) => u.name)).toEqual(["Priya Raman"]);
    expect(s.accounts.map((a) => a.mode).sort()).toEqual(["none", "verbatim"]);
    expect(s.accounts.some((a) => a.email === "sales@margin.app" || a.email === "priya@margin.app")).toBe(false);
  });

  it("keeps a full replace and refuses foreign ids", async () => {
    const s = await settings();
    const next = { ...peerInput(s), share_with: "everyone" as const, rules: "Dates only." };
    const r = await call("PUT", "/v1/me/assistant-peer", next);
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ share_with: "everyone", rules: "Dates only." });
    expect((await call("PUT", "/v1/me/assistant-peer", { ...next, accounts: [{ id: "00000000-0000-0000-0000-000000000000", mode: "verbatim" }] })).status).toBe(422);
    expect((await call("PUT", "/v1/me/assistant-peer", { ...next, audience: { user_ids: [], team_ids: ["nope"] } })).status).toBe(422);
  });

  it("lists the asks made to Maya, newest first", async () => {
    const r = (await call("GET", "/v1/me/assistant-peer/asks")).body as Page<PeerAsk>;
    expect(r.data.length).toBeGreaterThanOrEqual(2);
    expect(r.data.map((a) => a.result)).toContain("declined");
    expect([...r.data].sort((a, b) => b.created_at.localeCompare(a.created_at))).toEqual(r.data);
  });

  it("asks Priya's Ollie, and is not shared with by someone who did not opt in", async () => {
    const sid = ((await (await streamDispatch("POST", "/v1/assistant/sessions", {})).json()) as { id: string }).id;
    const evs = await parseSse(await streamDispatch("POST", `/v1/assistant/sessions/${sid}/messages`, { text: "Ask Priya what the Acme renewal date is" }));
    const steps = evs.filter((e) => e.event === "tool").map((e) => e.data as AssistantStep);
    expect(steps.at(-1)).toMatchObject({ tool: "ask_teammate", status: "done", label: "Asked Priya Raman's Ollie", peer: { result: "answered", sources: 3 } });
    expect(evs.map((e) => (e.event === "text" ? (e.data as { text: string }).text : "")).join("")).toContain("March 14");

    const sid2 = ((await (await streamDispatch("POST", "/v1/assistant/sessions", {})).json()) as { id: string }).id;
    const evs2 = await parseSse(await streamDispatch("POST", `/v1/assistant/sessions/${sid2}/messages`, { text: "ask Sam about the Halcyon contract" }));
    const last = evs2.filter((e) => e.event === "tool").at(-1)?.data as AssistantStep;
    expect(last).toMatchObject({ tool: "ask_teammate", peer: { result: "not_shared", sources: 0 } });
  });
});
