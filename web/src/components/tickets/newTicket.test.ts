import { afterEach, describe, expect, it } from "vitest";
import type { Contact } from "@/lib/api/types";
import { createTicketBody, draftKey, emptyNewTicket, isDirty, newTicketDrafts, newTicketProblem, pageOnlyCount, type NewTicketDraft } from "./newTicket";

const contact = { id: "c1", name: "Ada Lovelace", email: "ada@example.com" } as Contact;
const draft = (over: Partial<NewTicketDraft> = {}): NewTicketDraft => ({ ...emptyNewTicket, ...over });
// What goes over the wire: keys left undefined are not sent.
const wire = (v: unknown) => JSON.parse(JSON.stringify(v));
const mine = draftKey("u1", "w1");
const otherWorkspace = draftKey("u1", "w2");
const otherPerson = draftKey("u2", "w1");

describe("the new ticket draft", () => {
  afterEach(() => [mine, otherWorkspace, otherPerson].forEach(newTicketDrafts.clear));

  it("hands what the dialog wrote to the page as is", () => {
    newTicketDrafts.patch(mine, { to: { contact }, subject: "Lamp flickers" });
    newTicketDrafts.patch(mine, { body: "<p>Hello</p>", kind: "note", priority: "high", assignee: null, team: "t1", mailbox: "m1" });
    // The page reads the same draft the dialog left.
    expect(newTicketDrafts.get(mine)).toEqual(
      draft({ to: { contact }, subject: "Lamp flickers", body: "<p>Hello</p>", kind: "note", priority: "high", assignee: null, team: "t1", mailbox: "m1" }),
    );
    // One draft per person and workspace: another account on this browser never gets it.
    expect(newTicketDrafts.get(otherWorkspace)).toEqual(emptyNewTicket);
    expect(newTicketDrafts.get(otherPerson)).toEqual(emptyNewTicket);
  });

  it("starts over once discarded or created", () => {
    newTicketDrafts.patch(mine, { subject: "Lamp flickers", tags: ["bug"] });
    newTicketDrafts.patch(otherWorkspace, { subject: "Other workspace" });
    newTicketDrafts.clear(mine);
    expect(newTicketDrafts.get(mine)).toEqual(emptyNewTicket);
    expect(newTicketDrafts.get(otherWorkspace).subject).toBe("Other workspace");
  });

  it("counts what was written, not the defaults picked for it", () => {
    expect(isDirty(draft({ mailbox: "m1", priority: "high", assignee: null, team: null, kind: "note", showCc: true }))).toBe(false);
    expect(isDirty(draft({ to: { email: "ada@example.com" } }))).toBe(true);
    expect(isDirty(draft({ subject: "x" }))).toBe(true);
    expect(isDirty(draft({ body: "<p>x</p>" }))).toBe(true);
    expect(isDirty(draft({ tags: ["bug"] }))).toBe(true);
    expect(isDirty(draft({ fields: { plan: "pro" } }))).toBe(true);
    expect(isDirty(draft({ cc: ["b@example.com"] }))).toBe(true);
  });

  it("counts what only the full page shows, for the dialog to say it goes along", () => {
    expect(pageOnlyCount(draft({ to: { contact }, subject: "x", body: "y", priority: "high", team: "t1", assignee: null }))).toBe(0);
    expect(pageOnlyCount(draft({ tags: ["bug", "hardware"], severity: "sev2", fields: { plan: "pro" }, cc: ["b@example.com"], bcc: ["c@example.com"] }))).toBe(6);
  });

  it("asks for a recipient, a subject, a message, then an address that can send", () => {
    expect(newTicketProblem(draft(), false)).toBe("needTo");
    expect(newTicketProblem(draft({ to: { contact }, subject: "  " }), false)).toBe("needSubject");
    expect(newTicketProblem(draft({ to: { contact }, subject: "Hi" }), false)).toBe("needBody");
    expect(newTicketProblem(draft({ to: { contact }, subject: "Hi", body: "<p>x</p>" }), true)).toBe("cannotSend");
    expect(newTicketProblem(draft({ to: { contact }, subject: "Hi", body: "<p>x</p>" }), false)).toBeNull();
  });
});

describe("createTicketBody", () => {
  const o = { me: "u-me", teamId: "t1", hasTeams: true };

  it("sends what the dialog sends: an email to a contact, from the address, assigned to me", () => {
    const d = draft({ to: { contact }, subject: "  Lamp flickers ", body: "<p>Hello</p>", mailbox: "m1" });
    expect(wire(createTicketBody(d, o))).toEqual({
      subject: "Lamp flickers",
      contact_id: "c1",
      channel: "email",
      mailbox_id: "m1",
      body_html: "<p>Hello</p>",
      kind: "reply",
      priority: "normal",
      assignee_id: "u-me",
      team_id: "t1",
      status: "pending",
    });
  });

  it("creates the contact from a typed address, and logs a note open with no address", () => {
    const d = draft({ to: { email: "new@example.com" }, subject: "Call notes", body: "<p>x</p>", kind: "note", assignee: null, priority: "low" });
    expect(wire(createTicketBody(d, { ...o, teamId: null }))).toEqual({
      subject: "Call notes",
      contact: { email: "new@example.com", name: "" },
      channel: "api",
      body_html: "<p>x</p>",
      kind: "note",
      priority: "low",
      status: "open",
    });
  });

  it("leaves the team out where the workspace has no teams", () => {
    expect(createTicketBody(draft({ to: { contact }, subject: "x", body: "y" }), { ...o, hasTeams: false }).team_id).toBeUndefined();
  });

  it("adds the page's labels, severity, fields, copies and files in the same request", () => {
    const d = draft({
      to: { contact },
      subject: "Lamp flickers",
      body: "<p>Hello</p>",
      mailbox: "m1",
      assignee: "u-2",
      severity: "sev2",
      tags: ["bug", "hardware"],
      fields: { plan: "pro" },
      cc: ["b@example.com"],
      bcc: ["c@example.com"],
    });
    expect(wire(createTicketBody(d, { ...o, attachmentIds: ["a1", "a2"] }))).toEqual({
      subject: "Lamp flickers",
      contact_id: "c1",
      channel: "email",
      mailbox_id: "m1",
      body_html: "<p>Hello</p>",
      kind: "reply",
      priority: "normal",
      severity: "sev2",
      assignee_id: "u-2",
      team_id: "t1",
      tags: ["bug", "hardware"],
      fields: { plan: "pro" },
      cc: ["b@example.com"],
      bcc: ["c@example.com"],
      attachment_ids: ["a1", "a2"],
      status: "pending",
    });
  });

  it("sends copies only with an emailed reply", () => {
    const d = draft({ to: { contact }, subject: "x", body: "y", mailbox: "m1", cc: ["b@example.com"], bcc: ["c@example.com"] });
    expect(createTicketBody({ ...d, kind: "note" }, o)).not.toHaveProperty("cc");
    expect(createTicketBody({ ...d, mailbox: undefined }, o)).not.toHaveProperty("bcc");
  });
});
