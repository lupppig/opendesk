import { describe, expect, it } from "vitest";
import type { PeerSettings } from "@/lib/api/types";
import { effectiveMode, peerAccounts, peerInput, peerOutcome, sharedAccountCount, withAccountMode, withAllAccounts, withDefaultMode } from "./assistantPeer";

const own = [
  { id: "a1", email: "maya@margin.app", name: "Work", provider: "google" as const },
  { id: "a2", email: "maya.chen@gmail.com", name: "Personal", provider: "google" as const },
];

function settings(over: Partial<PeerSettings> = {}): PeerSettings {
  const base: PeerSettings = {
    available: true,
    enabled: true,
    share_with: "chosen",
    audience: { users: [{ id: "u1", name: "Priya Raman", email: "priya@margin.app" }], teams: [{ id: "t1", name: "Billing" }] },
    all_accounts: true,
    default_mode: "summary",
    accounts: peerAccounts(own, true, "summary", { a2: "none" }),
    rules: "No prices.",
    updated_at: null,
  };
  return { ...base, ...over };
}

describe("effective modes", () => {
  it("uses the account's choice, else the default for all accounts, else nothing", () => {
    expect(effectiveMode(true, "summary", "verbatim")).toBe("verbatim");
    expect(effectiveMode(true, "summary", undefined)).toBe("summary");
    expect(effectiveMode(false, "summary", undefined)).toBe("none");
  });
  it("marks explicit choices", () => {
    const s = settings();
    expect(s.accounts.map((a) => [a.id, a.mode, a.chosen])).toEqual([
      ["a1", "summary", false],
      ["a2", "none", true],
    ]);
    expect(sharedAccountCount(s)).toBe(1);
  });
});

describe("changes", () => {
  it("narrowing to chosen accounts drops the ones without a choice", () => {
    const s = withAllAccounts(settings(), false);
    expect(s.accounts.map((a) => a.mode)).toEqual(["none", "none"]);
    expect(sharedAccountCount(s)).toBe(0);
    const back = withAllAccounts(s, true);
    expect(back.accounts.map((a) => a.mode)).toEqual(["summary", "none"]);
  });
  it("a new default reaches only accounts without a choice", () => {
    const s = withDefaultMode(settings(), "verbatim");
    expect(s.accounts.map((a) => a.mode)).toEqual(["verbatim", "none"]);
  });
  it("setting an account's mode makes it a choice", () => {
    const s = withAccountMode(settings(), "a1", "verbatim");
    expect(s.accounts[0]).toMatchObject({ mode: "verbatim", chosen: true });
  });
});

describe("peerInput", () => {
  it("sends ids and only the explicit account choices", () => {
    expect(peerInput(withAccountMode(settings(), "a1", "verbatim"))).toEqual({
      enabled: true,
      share_with: "chosen",
      audience: { user_ids: ["u1"], team_ids: ["t1"] },
      all_accounts: true,
      default_mode: "summary",
      accounts: [
        { id: "a1", mode: "verbatim" },
        { id: "a2", mode: "none" },
      ],
      rules: "No prices.",
    });
  });
});

describe("peerOutcome", () => {
  it("counts sources only for answers", () => {
    expect(peerOutcome("answered", 3)).toEqual({ key: "answered", count: 3 });
    expect(peerOutcome("declined", 2)).toEqual({ key: "declined", count: 0 });
  });
});
