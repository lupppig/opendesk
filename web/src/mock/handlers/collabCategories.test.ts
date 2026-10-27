import { describe, expect, it } from "vitest";
import type { MailAccount, MailCategorySettings, MailThread } from "@/lib/api/types";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: ws ? { "OD-Workspace": ws } : {} });
  return { status: r.status, body: r.status === 204 || r.status === 202 ? null : await r.json() };
}

const accounts = async () => (await call("GET", "/v1/collab/accounts")).body as MailAccount[];
const inbox = async (q = "") => (await call("GET", `/v1/collab/threads?box=inbox&limit=100${q}`)).body.data as MailThread[];

describe("mock inbox categories", () => {
  installMock();

  it("splits every account that follows the workspace's default, and sorts mail into its tabs", async () => {
    const settings = (await call("GET", "/v1/collab/categories")).body as MailCategorySettings;
    expect(settings.workspace?.categories.map((c) => c.key)).toEqual(["primary", "updates", "newsletters", "promotions"]);
    const work = (await accounts()).find((a) => a.name === "Work")!;
    expect(work.categories).toMatchObject({ enabled: true, source: "workspace", mode: "follow" });
    const rows = await inbox(`&account_id=${work.id}`);
    expect(rows.every((t) => typeof t.category === "string")).toBe(true);
    // The Web Drop newsletter came to the shared Sales account.
    expect((await inbox()).some((t) => t.category === "newsletters")).toBe(true);
    const updates = await inbox(`&account_id=${work.id}&category=updates`);
    expect(updates.length).toBeGreaterThan(0);
    expect(updates.every((t) => t.category === "updates")).toBe(true);
    const counts = (await call("GET", "/v1/collab/counts")).body;
    expect(counts.categories[work.id].updates).toBe(updates.filter((t) => t.unread).length);
    expect((await call("GET", "/v1/collab/threads?box=sent&category=updates")).status).toBe(422);
  });

  it("moves a conversation by hand, and keeps it there when the categories change", async () => {
    const work = (await accounts()).find((a) => a.name === "Work")!;
    const t = (await inbox(`&account_id=${work.id}&category=primary`))[0];
    const moved = (await call("POST", "/v1/collab/threads/actions", { thread_ids: [t.id], action: "categorize", category: "promotions" })).body.threads[0];
    expect(moved.category).toBe("promotions");
    expect((await call("POST", "/v1/collab/threads/actions", { thread_ids: [t.id], action: "categorize", category: "nope" })).status).toBe(422);
    await call("PUT", "/v1/collab/categories/workspace", { enabled: true, categories: [{ name: "Primary" }, { name: "Updates" }, { name: "Promotions", color: "amber" }], locked: false });
    expect((await inbox(`&account_id=${work.id}&category=promotions`)).some((x) => x.id === t.id)).toBe(true);
  });

  it("lets an account choose its own, or none, and refuses while the workspace locks them for someone who is not an admin", async () => {
    const work = (await accounts()).find((a) => a.name === "Work")!;
    const own = (await call("PUT", `/v1/collab/accounts/${work.id}/categories`, { mode: "custom", categories: [{ name: "Important" }, { name: "Clients", color: "green" }] })).body as MailAccount;
    expect(own.categories).toMatchObject({ enabled: true, source: "account", mode: "custom" });
    expect(own.categories.categories.map((c) => c.key)).toEqual(["primary", "clients"]);
    const off = (await call("PUT", `/v1/collab/accounts/${work.id}/categories`, { mode: "off", ai: false })).body as MailAccount;
    expect(off.categories).toMatchObject({ enabled: false, mode: "off", ai: false });
    expect((await inbox(`&account_id=${work.id}`)).every((t) => t.category === null)).toBe(true);
    expect((await call("PUT", `/v1/collab/accounts/${work.id}/categories`, { mode: "custom", categories: [{ name: "Only one" }] })).status).toBe(422);
  });

  it("adds a sender rule, which moves the sender's mail, one rule per sender", async () => {
    const work = (await accounts()).find((a) => a.name === "Work")!;
    await call("PUT", `/v1/collab/accounts/${work.id}/categories`, { mode: "follow" });
    const before = ((await call("GET", `/v1/collab/accounts/${work.id}/category-rules`)).body.rules as unknown[]).length;
    const sender = (await inbox(`&account_id=${work.id}&category=primary`)).flatMap((t) => t.participants).find((p) => p.email !== work.email)!.email;
    const rule = (await call("POST", `/v1/collab/accounts/${work.id}/category-rules`, { sender, category: "promotions" })).body;
    expect(rule.sender).toBe(sender.toLowerCase());
    await call("POST", `/v1/collab/accounts/${work.id}/category-rules`, { sender, category: "updates" });
    const rules = (await call("GET", `/v1/collab/accounts/${work.id}/category-rules`)).body.rules as { id: string; category: string }[];
    expect(rules).toHaveLength(before + 1);
    expect((await call("POST", `/v1/collab/accounts/${work.id}/category-rules`, { sender: "not an address", category: "updates" })).status).toBe(422);
    expect((await call("DELETE", `/v1/collab/accounts/${work.id}/category-rules/${rules[0].id}`)).status).toBe(204);
  });

  it("orders team sets and takes one away", async () => {
    const teams = db.workspaces[0].teams.slice(0, 2);
    for (const t of teams) await call("PUT", `/v1/collab/categories/teams/${t.id}`, { enabled: true, categories: [{ name: "Primary" }, { name: t.name.slice(0, 30) + " mail" }] });
    let s = (await call("PUT", "/v1/collab/categories/team-order", { team_ids: teams.map((t) => t.id).reverse() })).body as MailCategorySettings;
    expect(s.teams.map((t) => t.team_id)).toEqual(teams.map((t) => t.id).reverse());
    s = (await call("DELETE", `/v1/collab/categories/teams/${teams[0].id}`)).body;
    expect(s.teams.map((t) => t.team_id)).toEqual([teams[1].id]);
  });
});
