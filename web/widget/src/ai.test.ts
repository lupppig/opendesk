import { describe, expect, it } from "vitest";
import { aiHandling, handedOff, isAiAnswer, keyBot, latestAiAnswer, teamReplied } from "./ai";
import type { Message } from "./types";

const author = (type: Message["author"]["type"]): Message["author"] => ({ type, user: type === "user" ? { id: "u", name: "Priya", avatar_url: "" } : null, contact: null });
const m = (id: string, type: Message["author"]["type"], dir: Message["direction"], ai?: boolean) => ({ id, author: author(type), direction: dir, ai });

describe("AI agent replies", () => {
  it("detects system messages marked ai", () => {
    expect(isAiAnswer(m("1", "system", "outbound", true))).toBe(true);
    expect(isAiAnswer(m("1", "system", "outbound"))).toBe(false);
    expect(isAiAnswer(m("1", "user", "outbound", true))).toBe(false);
  });

  it("handles a conversation until a teammate replies or it is handed off, with no reply cap", () => {
    const q = m("q", "contact", "inbound");
    const ai = (i: number) => m(`ai${i}`, "system", "outbound", true);
    const many = [q, ai(1), q, ai(2), q, ai(3), q, ai(4), q];
    expect(aiHandling({ aiAnswers: false, msgs: many })).toBe(true);
    expect(aiHandling({ aiAnswers: true, msgs: [] })).toBe(true);
    expect(aiHandling({ aiAnswers: false, msgs: [q] })).toBe(false);
    expect(aiHandling({ aiAnswers: true, msgs: [...many, m("h", "user", "outbound")] })).toBe(false);
    expect(aiHandling({ aiAnswers: true, msgs: many, conversation: { ai_handoff: true, status: "open" } })).toBe(false);
    expect(aiHandling({ aiAnswers: true, msgs: many, handedOff: true })).toBe(false);
    expect(aiHandling({ aiAnswers: true, msgs: many, conversation: { status: "solved" } })).toBe(false);
    expect(teamReplied([q, m("h", "user", "outbound")])).toBe(true);
    expect(latestAiAnswer(many)?.id).toBe("ai4");
    expect(handedOff({ ai_handoff: true })).toBe(true);
    expect(handedOff(undefined)).toBe(false);
  });
});

describe("replies an API key writes as a bot", () => {
  const look = { kind: "built", shape: "circle", fill: { type: "solid", color: "#22c55e", to: "#16a34a" }, eyes: "dots", mouth: "smile", accessory: "none", accent: "#f59e0b", blush: false, background: "none", emoji: "" };
  const julia = { id: "b1", name: "Julia", avatar_url: "https://api.test/v1/public/bots/b1/avatar.png", look } as unknown as Message["author"]["bot"];
  const withBot = (type: Message["author"]["type"], ai?: boolean) => ({ ...m("k", type, "outbound", ai), author: { ...author(type), bot: julia } });

  it("show the bot the key speaks as", () => {
    expect(keyBot(withBot("api"))).toMatchObject({ id: "b1", name: "Julia", look: { shape: "circle" } });
  });

  it("are not the AI agent's answers, and other replies show no bot", () => {
    expect(isAiAnswer(withBot("api"))).toBe(false);
    expect(keyBot(m("k", "api", "outbound"))).toBeNull();
    expect(keyBot(withBot("system", true))).toBeNull();
    expect(keyBot(withBot("user"))).toBeNull();
  });
});
