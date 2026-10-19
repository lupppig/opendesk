import { describe, expect, it } from "vitest";
import type { AdminSampleCounts } from "./api/types";
import { batchLine, countsLine, deleteAfterDays, expiryLabel, isTestEmail, kindAdds, orderKinds, passwordLabel, suggestEmail } from "./testWorkspaces";

const counts = (c: Partial<AdminSampleCounts>): AdminSampleCounts => ({
  members: 0,
  teams: 0,
  contacts: 0,
  companies: 0,
  tickets: 0,
  messages: 0,
  chats: 0,
  articles: 0,
  collections: 0,
  mail_accounts: 0,
  mail_threads: 0,
  views: 0,
  saved_replies: 0,
  ...c,
});

describe("test workspaces", () => {
  const now = Date.parse("2026-10-19T12:00:00Z");

  it("suggests a reserved .invalid owner address from the name", () => {
    const e = suggestEmail("Acme Démo, Inc.", () => 0);
    expect(e).toBe("owner-aaaa@acme-demo-inc.invalid");
    expect(isTestEmail(e)).toBe(true);
    expect(suggestEmail("!!!", () => 0)).toBe("owner-aaaa@demo.invalid");
    expect(isTestEmail("someone@example.com")).toBe(false);
  });

  it("reads expiry as kept, a date, soon or overdue", () => {
    expect(expiryLabel(null, now)).toEqual({ label: "Kept", tone: "gray" });
    expect(expiryLabel("2026-10-19T11:00:00Z", now).tone).toBe("red");
    expect(expiryLabel("2026-10-20T12:00:00Z", now)).toEqual({ label: "Deletes in 1 day", tone: "amber" });
    expect(expiryLabel("2026-11-30T12:00:00Z", now).label).toBe("Deletes Nov 30");
    expect(passwordLabel(null, now)).toBeNull();
    expect(passwordLabel("2026-10-18T12:00:00Z", now)?.label).toBe("Password expired");
  });

  it("lists what sample data added, without zeros", () => {
    expect(countsLine(counts({ tickets: 120, messages: 640, companies: 1, articles: 14 }))).toBe("120 tickets, 640 messages, 1 company, 14 articles");
    expect(countsLine(counts({ companies: 3 }))).toBe("3 companies");
    expect(countsLine(null)).toBe("");
  });

  it("keeps kinds in the generator's order and names a batch", () => {
    expect(orderKinds(["kb", "tickets", "kb"])).toEqual(["tickets", "kb"]);
    expect(batchLine("small", ["kb", "tickets"])).toBe("Small batch of tickets and knowledge base");
    expect(deleteAfterDays("keep")).toBeNull();
    expect(deleteAfterDays("30")).toBe(30);
  });

  it("says what a kind adds at a size", () => {
    expect(kindAdds("tickets", "large")).toContain("5,000 open");
    expect(kindAdds("collab", "small")).toBe("1 demo mail account with 20 threads, never connected to a provider");
    expect(kindAdds("history", "realistic")).toContain("last 90 days");
  });
});
