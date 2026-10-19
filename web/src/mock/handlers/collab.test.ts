import { describe, expect, it } from "vitest";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: ws ? { "OD-Workspace": ws } : {} });
  return { status: r.status, body: r.status === 204 || r.status === 202 ? null : await r.json() };
}

describe("mock Collab", () => {
  installMock();

  it("lists accounts, boxes and counts, and archives with an undo", async () => {
    expect((await call("GET", "/v1/collab")).body.enabled).toBe(true);
    const accounts = (await call("GET", "/v1/collab/accounts")).body as { id: string; provider: string }[];
    expect(accounts.length).toBeGreaterThanOrEqual(2);
    const inbox = (await call("GET", "/v1/collab/threads?box=inbox")).body.data as { id: string; account_id: string }[];
    expect(inbox.length).toBeGreaterThan(0);
    const t = inbox[0];
    const archived = (await call("POST", "/v1/collab/threads/actions", { thread_ids: [t.id], action: "archive" })).body.threads[0];
    expect(archived.id).toBe(t.id);
    const after = (await call("GET", "/v1/collab/threads?box=inbox")).body.data as { id: string }[];
    expect(after.some((x) => x.id === t.id)).toBe(false);
    await call("POST", "/v1/collab/threads/actions", { thread_ids: [t.id], action: "inbox" });
    expect(((await call("GET", "/v1/collab/threads?box=inbox")).body.data as { id: string }[]).some((x) => x.id === t.id)).toBe(true);
    const counts = (await call("GET", "/v1/collab/counts")).body;
    expect(typeof counts.boxes.inbox).toBe("number");
  });

  it("labels Outlook categories, refuses labels on IMAP, and keeps the sidebar prefs", async () => {
    const accounts = (await call("GET", "/v1/collab/accounts")).body as { id: string; provider: string }[];
    const ms = accounts.find((a) => a.provider === "microsoft")!;
    const t = ((await call("GET", `/v1/collab/threads?box=all&account_id=${ms.id}`)).body.data as { id: string }[])[0];
    const folders = (await call("GET", `/v1/collab/accounts/${ms.id}/folders`)).body as { id: string; role: string | null; kind: string }[];
    const category = folders.find((f) => !f.role && f.kind === "label")!;
    const labelled = (await call("POST", "/v1/collab/threads/actions", { thread_ids: [t.id], action: "label", folder_id: category.id })).body;
    expect(labelled.threads[0].folder_ids).toContain(category.id);
    const coloured = (await call("PATCH", `/v1/collab/folders/${category.id}`, { color: "blue" })).body;
    expect(coloured.color).toBe("#0078d4");
    expect((await call("PATCH", `/v1/collab/folders/${category.id}`, { name: "Renamed" })).body.code).toBe("rename_unsupported");
    const imap = accounts.find((a) => a.provider === "imap")!;
    const it2 = ((await call("GET", `/v1/collab/threads?box=all&account_id=${imap.id}`)).body.data as { id: string }[])[0];
    const imapFolders = (await call("GET", `/v1/collab/accounts/${imap.id}/folders`)).body as { id: string; role: string | null }[];
    const user = imapFolders.find((f) => !f.role)!;
    expect((await call("POST", "/v1/collab/threads/actions", { thread_ids: [it2.id], action: "label", folder_id: user.id })).body.code).toBe("labels_unsupported");
    const saved = (await call("PUT", "/v1/collab/sidebar", { order: ["mail", "inbox"], hidden: ["section:views"], pinned: [{ kind: "box", id: "assigned" }], expanded_accounts: [], mark_read_on_open: false, unified_inbox: true })).body;
    expect(saved.mark_read_on_open).toBe(false);
    expect((await call("GET", "/v1/collab/sidebar")).body.hidden).toEqual(["section:views"]);
    // Numbers and tabs: kept by a client that leaves them out.
    await call("PUT", "/v1/collab/sidebar", { ...saved, badges: { "section:mail": "total" }, mail_tabs: ["inbox", "spam"] });
    const kept = (await call("PUT", "/v1/collab/sidebar", { order: ["mail"], hidden: [], pinned: [], expanded_accounts: [], mark_read_on_open: true, unified_inbox: true })).body;
    expect(kept.badges).toEqual({ "section:mail": "total" });
    expect(kept.mail_tabs).toEqual(["inbox", "spam"]);
  });

  it("saves, sends and discards drafts", async () => {
    const drafts = (await call("GET", "/v1/collab/threads?box=drafts")).body.data as { id: string }[];
    expect(drafts.length).toBeGreaterThanOrEqual(2);
    const inbox = (await call("GET", "/v1/collab/threads?box=inbox")).body.data as { id: string }[];
    const detail = (await call("GET", `/v1/collab/threads/${inbox[0].id}`)).body;
    const last = detail.messages[detail.messages.length - 1];
    const d = await call("POST", "/v1/collab/drafts", { account_id: detail.account.id, mode: "reply", message_id: last.id, to: [], subject: "", body_html: "<p>Half</p>" });
    expect(d.status).toBe(201);
    expect(d.body.draft_id).toBe(d.body.id);
    expect(d.body.in_reply_to_id).toBe(last.id);
    const again = await call("PUT", `/v1/collab/drafts/${d.body.id}`, { to: [last.from.email], subject: "Re: x", body_html: "<p>Whole</p>" });
    expect(again.body.body_html).toBe("<p>Whole</p>");
    const sent = await call("POST", `/v1/collab/drafts/${d.body.id}/send`);
    expect(sent.body.draft).toBe(false);
    const n = await call("POST", "/v1/collab/drafts", { account_id: detail.account.id, mode: "new", to: [], subject: "Later", body_html: "<p>x</p>" });
    expect((await call("DELETE", `/v1/collab/drafts/${n.body.id}`)).status).toBe(204);
    expect((await call("GET", `/v1/collab/threads/${n.body.thread_id}`)).status).toBe(404);
  });

  it("sends a reply into its conversation", async () => {
    const inbox = (await call("GET", "/v1/collab/threads?box=inbox")).body.data as { id: string; account_id: string }[];
    const detail = (await call("GET", `/v1/collab/threads/${inbox[0].id}`)).body;
    const last = detail.messages[detail.messages.length - 1];
    const sent = await call("POST", "/v1/collab/messages", { account_id: detail.account.id, mode: "reply", message_id: last.id, to: [last.from.email], subject: `Re: ${detail.thread.subject}`, body_html: "<p>Thanks</p>" });
    expect(sent.status).toBe(201);
    expect(sent.body.thread_id).toBe(inbox[0].id);
  });
});
