import { describe, expect, it } from "vitest";
import type { MailAccount, MailHistory } from "@/lib/api/types";
import { historyEnded, historyProgress, scopeOf, sinceOf } from "./HistorySection";
import { searchCoveredFrom } from "./MailNotices";

const history = (p: Partial<MailHistory> = {}): MailHistory => ({
  status: "running",
  since: null,
  include_spam_trash: false,
  requested_by: null,
  started_at: "2026-10-01T00:00:00Z",
  finished_at: null,
  reached: null,
  imported: 0,
  present: 0,
  too_large: 0,
  unreadable: 0,
  total: null,
  last_error: "",
  ...p,
});

describe("sinceOf", () => {
  const now = new Date("2026-10-23T12:00:00Z");
  it("goes back whole years, and all mail has no start", () => {
    expect(sinceOf("1y", now)).toBe("2025-10-23T12:00:00.000Z");
    expect(sinceOf("5y", now)).toBe("2021-10-23T12:00:00.000Z");
    expect(sinceOf("all", now)).toBeNull();
  });
});

describe("scopeOf", () => {
  it("reads the scope an import started with, and none for a date of its own", () => {
    const started = new Date("2026-10-01T00:00:00Z");
    expect(scopeOf(history({ since: null }))).toBe("all");
    expect(scopeOf(history({ since: sinceOf("1y", started) }))).toBe("1y");
    expect(scopeOf(history({ since: sinceOf("5y", started) }))).toBe("5y");
    expect(scopeOf(history({ since: "2024-03-14T00:00:00Z" }))).toBeNull();
  });
});

describe("historyProgress", () => {
  it("counts everything read against the total, and never passes it", () => {
    expect(historyProgress(history({ imported: 30, present: 10, too_large: 5, unreadable: 5, total: 100 }))).toBe(0.5);
    expect(historyProgress(history({ imported: 120, total: 100 }))).toBe(1);
  });
  it("has no progress without a total", () => {
    expect(historyProgress(history({ imported: 30 }))).toBeNull();
    expect(historyProgress(history({ imported: 30, total: 0 }))).toBeNull();
  });
});

describe("historyEnded", () => {
  it("lets a new import start when none runs", () => {
    expect(historyEnded(null)).toBe(true);
    expect(historyEnded(history({ status: "done" }))).toBe(true);
    expect(historyEnded(history({ status: "error" }))).toBe(true);
    expect(historyEnded(history({ status: "paused" }))).toBe(false);
    expect(historyEnded(history({ status: "limited" }))).toBe(false);
  });
});

describe("searchCoveredFrom", () => {
  const acc = (oldest: string | null) => ({ oldest_message_at: oldest }) as MailAccount;
  it("promises only what every account in view covers", () => {
    expect(searchCoveredFrom([acc("2021-01-01T00:00:00Z"), acc("2026-09-23T00:00:00Z"), acc(null)])).toBe("2026-09-23T00:00:00Z");
    expect(searchCoveredFrom([acc(null)])).toBeNull();
  });
});
