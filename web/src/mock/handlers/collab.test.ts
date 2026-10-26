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

  it("counts each address of an account with aliases like the list's to=", async () => {
    const accounts = (await call("GET", "/v1/collab/accounts")).body as { id: string; identities: { email: string; primary: boolean }[] }[];
    const a = accounts.find((x) => x.identities.some((i) => !i.primary))!;
    const counts = (await call("GET", "/v1/collab/counts")).body as { addresses: Record<string, Record<string, number>> };
    expect(Object.keys(counts.addresses)).toEqual([a.id]);
    for (const i of a.identities) {
      const list = (await call("GET", `/v1/collab/threads?box=inbox&account_id=${a.id}&to=${encodeURIComponent(i.email)}`)).body.data as { unread: boolean }[];
      expect(counts.addresses[a.id][i.email] ?? 0).toBe(list.filter((t) => t.unread).length);
    }
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

  it("lists a Gmail account's send-as addresses and sends from the one a message was written to", async () => {
    const accounts = (await call("GET", "/v1/collab/accounts")).body as { id: string; provider: string; email: string; identities: { id: string | null; email: string; can_send: boolean; primary: boolean }[] }[];
    const gmail = accounts.find((a) => a.provider === "google")!;
    expect(gmail.identities[0].primary).toBe(true);
    expect(gmail.identities.map((i) => i.email)).toContain("support@margin.app");
    expect(gmail.identities.find((i) => i.email === "press@margin.app")?.can_send).toBe(false);
    const toAlias = (await call("GET", `/v1/collab/threads?box=inbox&account_id=${gmail.id}&to=support@margin.app`)).body.data as { id: string; to_address: string | null }[];
    expect(toAlias.length).toBe(1);
    expect(toAlias[0].to_address).toBe("support@margin.app");
    const detail = (await call("GET", `/v1/collab/threads/${toAlias[0].id}`)).body;
    expect(detail.messages[0].own_address).toBe("support@margin.app");
    const sent = (await call("POST", "/v1/collab/messages", { account_id: gmail.id, mode: "reply", message_id: detail.messages[0].id, to: ["priya@cobalt.dev"], subject: "", body_html: "<p>On it</p>" })).body;
    expect(sent.from.email).toBe("support@margin.app");
    expect((await call("POST", "/v1/collab/messages", { account_id: gmail.id, mode: "new", to: ["x@example.com"], subject: "s", body_html: "b", from_email: "press@margin.app" })).status).toBe(422);
    expect((await call("POST", `/v1/collab/accounts/${gmail.id}/identities`, { email: "new@margin.app" })).body.code).toBe("identities_from_provider");
    // An address's signature is kept in opendesk; Get from Gmail brings Gmail's back.
    const support = gmail.identities.find((i) => i.email === "support@margin.app")!;
    const gmailSig = (support as unknown as { signature_html: string }).signature_html;
    const edited = (await call("PATCH", `/v1/collab/accounts/${gmail.id}/identities/${support.id}`, { signature_html: "<p>Ours</p>" })).body;
    expect(edited.identities.find((i: { email: string }) => i.email === "support@margin.app")).toMatchObject({ signature_html: "<p>Ours</p>", signature_sync: "pull" });
    const pulled = (await call("POST", `/v1/collab/accounts/${gmail.id}/identities/${support.id}/signature/pull`)).body;
    expect(pulled.identities.find((i: { email: string }) => i.email === "support@margin.app").signature_html).toBe(gmailSig);
    const ms = accounts.find((a) => a.provider === "microsoft")!;
    const added = (await call("POST", `/v1/collab/accounts/${ms.id}/identities`, { email: "deals@margin.app", display_name: "Deals" })).body;
    expect(added.identities.map((i: { email: string }) => i.email)).toContain("deals@margin.app");
  });

  it("fills a From name and signature that name whoever sends, and keeps them from Gmail", async () => {
    type Ident = { id: string | null; email: string; display_name: string; from_name: string; signature_html: string; filled_signature_html: string | null; display_name_local: boolean; signature_local: boolean };
    const accounts = (await call("GET", "/v1/collab/accounts")).body as { id: string; provider: string; email: string; identities: Ident[] }[];
    const sales = accounts.find((a) => a.provider === "microsoft")!;
    const own = sales.identities[0];
    expect(own.display_name).toBe("{agent} at Margin Sales");
    expect(own.from_name).toMatch(/ at Margin Sales$/);
    expect(own.from_name).not.toContain("{");
    expect(own.filled_signature_html).toContain("Head of support, Margin Sales");
    const sent = (await call("POST", "/v1/collab/messages", { account_id: sales.id, mode: "new", to: ["x@example.com"], subject: "s", body_html: "<p>b</p>" })).body;
    expect(sent.from.name).toBe(own.from_name);

    const gmail = accounts.find((a) => a.provider === "google")!;
    const support = gmail.identities.find((i) => i.email === "support@margin.app")!;
    const edited = (await call("PATCH", `/v1/collab/accounts/${gmail.id}/identities/${support.id}`, { display_name: "{agent} at Margin Support" })).body;
    const now = edited.identities.find((i: Ident) => i.email === "support@margin.app") as Ident;
    expect(now).toMatchObject({ display_name: "{agent} at Margin Support", display_name_local: true });
    expect(now.from_name).toMatch(/ at Margin Support$/);
    expect((await call("PATCH", `/v1/collab/accounts/${gmail.id}/identities/${support.id}`, { display_name: "{agent} for {team}" })).status).toBe(422);
    expect((await call("PATCH", `/v1/collab/accounts/${gmail.id}`, { signature_html: "<p>{{ ticket.subject }}</p>" })).status).toBe(422);
    const pulled = (await call("POST", `/v1/collab/accounts/${gmail.id}/identities/${support.id}/display-name/pull`)).body;
    expect(pulled.identities.find((i: Ident) => i.email === "support@margin.app").display_name).toBe("Margin Support");

    const preview = (await call("POST", `/v1/collab/accounts/${gmail.id}/identities/preview`, { email: "support@margin.app", display_name: "{agent_first} from {workspace}", signature_html: "<p>{{ mailbox.name }}</p>" })).body;
    expect(preview.from).toMatch(/^\S+ from .+ <support@margin\.app>$/);
    expect(preview.signature_html).toContain(preview.from_name);
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
