import { describe, expect, it } from "vitest";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";
import type { Contact, Mailbox, Message, Ticket } from "@/lib/api/types";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, {
    method,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    headers: ws ? { "OD-Workspace": ws } : {},
  });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

describe("mock new ticket", () => {
  installMock();

  it("creates the page's ticket in one request: labels, copies and files on the first message", async () => {
    const contact = ((await call("GET", "/v1/contacts?limit=5")).body.data as Contact[]).find((c) => c.email)!;
    const box = ((await call("GET", "/v1/mailboxes")).body.data as Mailbox[]).find((m) => m.status === "active" && m.sending !== "system_off")!;
    db.attachments.push({ id: "att-new-ticket", filename: "invoice.pdf", content_type: "application/pdf", size_bytes: 1200, inline: false, url: "https://example.com/invoice.pdf" });
    const created = await call("POST", "/v1/tickets", {
      subject: "Lamp flickers",
      contact_id: contact.id,
      channel: "email",
      mailbox_id: box.id,
      body_html: "<p>Hello</p>",
      kind: "reply",
      priority: "high",
      tags: ["bug"],
      cc: ["copy@example.com"],
      bcc: ["hidden@example.com"],
      attachment_ids: ["att-new-ticket"],
      status: "pending",
    });
    expect(created.status).toBe(201);
    const t = created.body as Ticket;
    // An existing label matches whatever its case.
    expect(t.tags.map((x) => x.name.toLowerCase())).toContain("bug");
    const msgs = (await call("GET", `/v1/tickets/${t.id}/messages`)).body.data as Message[];
    expect(msgs).toHaveLength(1);
    expect(msgs[0]).toMatchObject({ kind: "reply", cc: ["copy@example.com"], bcc: ["hidden@example.com"] });
    expect(msgs[0].attachments.map((a) => a.filename)).toEqual(["invoice.pdf"]);
  });

  it("keeps copies off a ticket that starts as a note", async () => {
    const contact = ((await call("GET", "/v1/contacts?limit=5")).body.data as Contact[]).find((c) => c.email)!;
    const created = await call("POST", "/v1/tickets", { subject: "Call notes", contact_id: contact.id, channel: "api", body_html: "<p>x</p>", kind: "note", cc: ["copy@example.com"], status: "open" });
    expect(created.status).toBe(201);
    const msgs = (await call("GET", `/v1/tickets/${created.body.id}/messages`)).body.data as Message[];
    expect(msgs[0]).toMatchObject({ kind: "note", cc: [] });
  });
});
