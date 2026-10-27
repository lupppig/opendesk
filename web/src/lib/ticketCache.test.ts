import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { TicketListQuery } from "./api/endpoints";
import type { Ticket } from "./api/types";
import { qk } from "./queries";
import { applyEnvelope } from "./realtime/apply";
import { env, ticket } from "./test/fixtures";
import { patchTicketEverywhere, placeTicket, ticketMatches, type TicketPages } from "./ticketCache";

const pages = (...lists: Ticket[][]): TicketPages => ({
  pages: lists.map((data, i) => ({ data, next_cursor: i < lists.length - 1 ? `c${i}` : null })),
  pageParams: lists.map((_, i) => (i === 0 ? null : `c${i - 1}`)),
});
const ids = (d: TicketPages | undefined) => d?.pages.flatMap((p) => p.data.map((t) => t.id));

const mine: TicketListQuery = { status: ["open", "pending", "on_hold"], assignee: "me" };
const open: TicketListQuery = { status: ["open"] };

describe("ticketMatches", () => {
  it("checks status, assignee, spam and tag", () => {
    const t = ticket({ assignee: { id: "u1", name: "Me", email: "me@example.com", avatar_url: "" }, tags: [{ id: "g", name: "VIP" } as Ticket["tags"][number]] });
    expect(ticketMatches(mine, t, "u1")).toBe(true);
    expect(ticketMatches(mine, t, "u2")).toBe(false);
    expect(ticketMatches({ ...open, tag: "vip" }, t, "u1")).toBe(true);
    expect(ticketMatches(open, { ...t, spam: true }, "u1")).toBe(false);
    expect(ticketMatches({ spam: true }, { ...t, spam: true }, "u1")).toBe(true);
    expect(ticketMatches({ assignee: "none" }, t, "u1")).toBe(false);
  });

  it("filters by severity, with none for unset", () => {
    const t = ticket({ severity: "sev2" });
    expect(ticketMatches({ severity: ["sev1", "sev2"] }, t, "u1")).toBe(true);
    expect(ticketMatches({ severity: ["sev1"] }, t, "u1")).toBe(false);
    expect(ticketMatches({ severity: ["none"] }, ticket({ severity: null }), "u1")).toBe(true);
    expect(ticketMatches({ severity: ["none"] }, t, "u1")).toBe(false);
  });

  it("filters by the address a ticket was written to, ignoring case", () => {
    const t = ticket({ received_at: "projects@acme.test" });
    expect(ticketMatches({ received_at: "Projects@acme.test" }, t, "u1")).toBe(true);
    expect(ticketMatches({ received_at: "support@acme.test" }, t, "u1")).toBe(false);
  });

  it("filters by any of several labels, none of them, or no label", () => {
    const tag = (name: string) => ({ id: name, name }) as Ticket["tags"][number];
    const t = ticket({ tags: [tag("ORDER-CHANGE/CANCEL"), tag("Q&A Done")] });
    expect(ticketMatches({ tag: "cancel/refund,order-change/cancel" }, t, "u1")).toBe(true);
    expect(ticketMatches({ tag: "cancel/refund" }, t, "u1")).toBe(false);
    expect(ticketMatches({ tag_not: "q&a done" }, t, "u1")).toBe(false);
    expect(ticketMatches({ tag_not: "cancel/refund" }, t, "u1")).toBe(true);
    expect(ticketMatches({ untagged: true }, t, "u1")).toBe(false);
    expect(ticketMatches({ untagged: true }, ticket({ tags: [] }), "u1")).toBe(true);
    const views = [{ id: "v1", name: "To handle", shared: true, position: 0, filters: { tag_not: "Q&A Done", team_not: "t1" } }];
    expect(ticketMatches({ view_id: "v1" }, t, "u1", views)).toBe(false);
    expect(ticketMatches({ view_id: "v1" }, ticket({ tags: [], team_id: null }), "u1", views)).toBe(true);
  });

  it("leaves out excluded teams, mailboxes and channels", () => {
    const t = ticket({ team_id: "t1", mailbox_id: "m1", channel: "email" });
    expect(ticketMatches({ team_not: "t1,t2" }, t, "u1")).toBe(false);
    expect(ticketMatches({ team_not: "t2" }, t, "u1")).toBe(true);
    expect(ticketMatches({ team_not: "t2" }, ticket({ team_id: null }), "u1")).toBe(true);
    expect(ticketMatches({ team_not: "none" }, ticket({ team_id: null }), "u1")).toBe(false);
    expect(ticketMatches({ team_not: "mine" }, t, "u1")).toBeNull();
    expect(ticketMatches({ team_not: "mine" }, t, "u1", undefined, ["t1"])).toBe(false);
    expect(ticketMatches({ mailbox_not: "m2,m1" }, t, "u1")).toBe(false);
    expect(ticketMatches({ mailbox_not: "m2" }, t, "u1")).toBe(true);
    expect(ticketMatches({ channel_not: ["chat", "email"] }, t, "u1")).toBe(false);
    expect(ticketMatches({ channel_not: ["chat"] }, t, "u1")).toBe(true);
  });

  it("leaves out tickets of excluded contacts, and asks the server about excluded companies", () => {
    const contact = (id: string) => ({ id, name: id, email: `${id}@customer.test`, avatar_url: null });
    const t = ticket({ contact: contact("c1") });
    expect(ticketMatches({ contact_not: "c2,c1" }, t, "u1")).toBe(false);
    expect(ticketMatches({ contact_not: "c2" }, t, "u1")).toBe(true);
    expect(ticketMatches({ contact_not: "c1" }, ticket({ contact: null }), "u1")).toBe(true);
    // A ticket carries its contact but not the contact's company.
    expect(ticketMatches({ company_not: "co1" }, t, "u1")).toBeNull();
    expect(ticketMatches({ company_not: "co1", contact_not: "c1" }, t, "u1")).toBe(false);
    expect(ticketMatches({ company_not: "co1" }, ticket({ contact: null }), "u1")).toBe(true);
    const views = [{ id: "v1", name: "Not Ann", shared: true, position: 0, filters: { contact_not: "c1" } }];
    expect(ticketMatches({ view_id: "v1" }, t, "u1", views)).toBe(false);
    expect(ticketMatches({ view_id: "v1" }, ticket({ contact: contact("c2") }), "u1", views)).toBe(true);
  });

  it("can't decide full-text or unknown views", () => {
    expect(ticketMatches({ q: "refund" }, ticket(), "u1")).toBeNull();
    expect(ticketMatches({ view_id: "v1" }, ticket(), "u1", [])).toBeNull();
  });
});

describe("placeTicket", () => {
  it("inserts in sort order (newest first by default)", () => {
    const a = ticket({ id: "a", number: 1, last_message_at: "2026-10-01T12:00:00Z" });
    const c = ticket({ id: "c", number: 3, last_message_at: "2026-10-01T10:00:00Z" });
    const b = ticket({ id: "b", number: 2, last_message_at: "2026-10-01T11:00:00Z" });
    expect(ids(placeTicket(pages([a, c]), b, open))).toEqual(["a", "b", "c"]);
  });

  it("moves an updated ticket to its new position without duplicating it", () => {
    const a = ticket({ id: "a", last_message_at: "2026-10-01T12:00:00Z" });
    const b = ticket({ id: "b", last_message_at: "2026-10-01T11:00:00Z" });
    const bNew = { ...b, last_message_at: "2026-10-01T13:00:00Z" };
    expect(ids(placeTicket(pages([a, b]), bNew, open))).toEqual(["b", "a"]);
  });

  it("leaves a ticket that sorts past the loaded window to a later page", () => {
    const a = ticket({ id: "a", last_message_at: "2026-10-01T12:00:00Z" });
    const b = ticket({ id: "b", last_message_at: "2026-10-01T11:00:00Z" });
    const old = ticket({ id: "old", last_message_at: "2026-09-01T00:00:00Z" });
    const twoPages = pages([a], [b]);
    twoPages.pages[1].next_cursor = "more";
    expect(ids(placeTicket(twoPages, old, open))).toEqual(["a", "b"]);
  });

  it("sorts by due date ascending", () => {
    const sla = (d: string) => ({ policy_id: "p", first_response_due_at: d, resolution_due_at: null }) as Ticket["sla"];
    const soon = ticket({ id: "soon", sla: sla("2026-10-01T11:00:00Z") });
    const later = ticket({ id: "later", sla: sla("2026-10-02T11:00:00Z") });
    const none = ticket({ id: "none" });
    const q: TicketListQuery = { sort: "due" };
    expect(ids(placeTicket(pages([later, none]), soon, q))).toEqual(["soon", "later", "none"]);
  });
});

describe("patchTicketEverywhere and applyEnvelope", () => {
  it("moves a ticket between folder lists when it is assigned", () => {
    const qc = new QueryClient();
    const t = ticket({ id: "x" });
    qc.setQueryData(qk.tickets(open), pages([t]));
    qc.setQueryData(qk.tickets(mine), pages([]));
    const assigned = { ...t, assignee: { id: "u1", name: "Me", email: "me@example.com", avatar_url: "" } };
    patchTicketEverywhere(qc, assigned, "u1");
    expect(ids(qc.getQueryData(qk.tickets(mine)))).toEqual(["x"]);
    expect(ids(qc.getQueryData(qk.tickets(open)))).toEqual(["x"]);
    patchTicketEverywhere(qc, { ...assigned, status: "solved" }, "u1");
    expect(ids(qc.getQueryData(qk.tickets(mine)))).toEqual([]);
    expect(ids(qc.getQueryData(qk.tickets(open)))).toEqual([]);
    expect(qc.getQueryData<Ticket>(qk.ticket("x"))?.status).toBe("solved");
  });

  it("removes deleted tickets from every list", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.tickets(open), pages([ticket({ id: "x" }), ticket({ id: "y" })]));
    applyEnvelope(qc, env(1, { type: "ticket.deleted", data: { ticket_id: "x" } }), { me: "u1" });
    expect(ids(qc.getQueryData(qk.tickets(open)))).toEqual(["y"]);
  });

  it("merges a partial chat message into an existing copy", () => {
    const qc = new QueryClient();
    const full = { id: "m1", ticket_id: "x", body_html: "<p>hi</p>", to: ["a@b.c"], attachments: [], author: { type: "user", user: { id: "u1", name: "Me" }, contact: null } };
    qc.setQueryData(qk.messages("x"), [full]);
    applyEnvelope(qc, env(2, { type: "message.updated", data: { ticket_id: "x", message: { id: "m1", ticket_id: "x", delivery_status: "sent" } } }), { me: "u1" });
    const [m] = qc.getQueryData<Record<string, unknown>[]>(qk.messages("x"))!;
    expect(m.to).toEqual(["a@b.c"]);
    expect(m.delivery_status).toBe("sent");
  });

  it("keeps only the signed-in user's notifications", () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.notifications, []);
    applyEnvelope(qc, env(3, { type: "notification.created", topics: ["user:u2"], data: { notification: { id: "n1" } } }), { me: "u1" });
    expect(qc.getQueryData<unknown[]>(qk.notifications)).toEqual([]);
    applyEnvelope(qc, env(4, { type: "notification.created", topics: ["user:u1"], data: { notification: { id: "n2" } } }), { me: "u1" });
    expect(qc.getQueryData<{ id: string }[]>(qk.notifications)?.map((n) => n.id)).toEqual(["n2"]);
  });
});

describe("detail aliases", () => {
  it("updates a detail view cached under the ticket number", () => {
    const qc = new QueryClient();
    const t = ticket({ id: "x", number: 1072 });
    qc.setQueryData(qk.ticket("1072"), t);
    patchTicketEverywhere(qc, { ...t, status: "pending" }, "u1");
    expect(qc.getQueryData<Ticket>(qk.ticket("1072"))?.status).toBe("pending");
    expect(qc.getQueryData(qk.ticket("1073"))).toBeUndefined();
  });
});
