import { describe, expect, it } from "vitest";
import type { ContactRef, Message, UserRef } from "@/lib/api/types";
import { messageAuthor, messageFrame, messageSide, sideClass } from "./messageFrame";

type Shape = Pick<Message, "kind" | "direction" | "author" | "thread_id">;

const user: UserRef = { id: "u1", name: "Maya Chen", email: "maya@example.com", avatar_url: null };
const contact: ContactRef = { id: "c1", name: "Dana", email: "dana@example.com", avatar_url: null };

function msg(over: Partial<Shape> & { type?: Message["author"]["type"] } = {}): Shape {
  const { type = "contact", ...rest } = over;
  return {
    kind: "reply",
    direction: "inbound",
    thread_id: null,
    author: { type, user: type === "user" ? user : null, contact: type === "contact" ? contact : null },
    ...rest,
  };
}

describe("messageAuthor", () => {
  it("reads a contact's inbound message as the customer's", () => {
    expect(messageAuthor(msg())).toBe("customer");
    expect(messageAuthor(msg({ type: "api" }))).toBe("customer");
  });
  it("reads every outbound reply as the team's: agents, the AI agent, automations and the API", () => {
    expect(messageAuthor(msg({ type: "user", direction: "outbound" }))).toBe("team");
    expect(messageAuthor(msg({ type: "system", direction: "outbound" }))).toBe("team");
    expect(messageAuthor(msg({ type: "api", direction: "outbound" }))).toBe("team");
  });
  it("reads a message a member wrote as the team's", () => {
    expect(messageAuthor(msg({ type: "user" }))).toBe("team");
  });
  it("reads a note as a note, whoever wrote it", () => {
    expect(messageAuthor(msg({ type: "user", kind: "note", direction: "outbound" }))).toBe("note");
    expect(messageAuthor(msg({ type: "system", kind: "note", direction: "outbound" }))).toBe("note");
  });
  it("reads a side conversation message as the thread's, both ways", () => {
    expect(messageAuthor(msg({ type: "user", kind: "note", direction: "outbound", thread_id: "th1" }))).toBe("thread");
    expect(messageAuthor(msg({ kind: "note", thread_id: "th1" }))).toBe("thread");
  });
});

describe("messageFrame", () => {
  it("gives each author its own surface and border", () => {
    expect(messageFrame("customer", false)).toBe("border-line bg-surface");
    expect(messageFrame("team", false)).toBe("border-reply-line bg-reply");
    expect(messageFrame("note", false)).toBe("border-dashed border-note-line bg-note");
  });
  it("keeps the three apart", () => {
    const frames = new Set((["customer", "team", "note"] as const).map((a) => messageFrame(a, false)));
    expect(frames.size).toBe(3);
  });
  it("marks a failed delivery in red and keeps the team's surface", () => {
    const f = messageFrame("team", true);
    expect(f).toContain("border-tone-red-line");
    expect(f).toContain("bg-reply");
    expect(f).not.toContain("border-reply-line");
  });
  it("frames a side conversation in cyan", () => {
    expect(messageFrame("thread", false)).toContain("border-tone-cyan-line");
    expect(messageFrame("thread", false)).toContain("bg-surface");
  });
  it("never lets a note look failed", () => {
    expect(messageFrame("note", true)).toBe("border-dashed border-note-line bg-note");
  });
});

describe("messageSide", () => {
  it("puts the customer's messages on the customer's side", () => {
    expect(messageSide(msg())).toBe("customer");
    expect(messageSide(msg({ type: "api" }))).toBe("customer");
  });
  it("puts mail that came in from a system, such as a bounce, on the customer's side", () => {
    expect(messageSide(msg({ type: "system" }))).toBe("customer");
  });
  it("puts every reply we send on the team's: agents, Ollie and bots, automations, auto-replies and the API", () => {
    expect(messageSide(msg({ type: "user", direction: "outbound" }))).toBe("team");
    expect(messageSide(msg({ type: "system", direction: "outbound" }))).toBe("team");
    expect(messageSide(msg({ type: "api", direction: "outbound" }))).toBe("team");
  });
  it("puts internal notes on the team's side, whoever wrote them", () => {
    expect(messageSide(msg({ type: "user", kind: "note", direction: "outbound" }))).toBe("team");
    expect(messageSide(msg({ type: "system", kind: "note", direction: "outbound" }))).toBe("team");
  });
  it("splits a side conversation between what we wrote and what the third party answered", () => {
    expect(messageSide(msg({ type: "user", kind: "note", direction: "outbound", thread_id: "th1" }))).toBe("team");
    expect(messageSide(msg({ kind: "note", thread_id: "th1" }))).toBe("customer");
  });
});

describe("sideClass", () => {
  it("leaves every card full width when the layout is off", () => {
    expect(sideClass("customer", false)).toBeUndefined();
    expect(sideClass("team", false)).toBeUndefined();
  });
  it("narrows both sides to the same width, from a wide enough timeline only", () => {
    for (const side of ["customer", "team"] as const) {
      const c = sideClass(side, true)!;
      expect(c).toContain("@md:w-(--conversation-card)");
      expect(c.split(" ").every((x) => x.startsWith("@md:"))).toBe(true);
    }
  });
  it("sends the customer to the start and the team to the end", () => {
    expect(sideClass("customer", true)).toContain("@md:me-auto");
    expect(sideClass("team", true)).toContain("@md:ms-auto");
  });
});
