import { describe, expect, it } from "vitest";
import { quietFor } from "./quiet";

const now = Date.parse("2026-10-22T12:00:00Z");
const iso = (daysAgo: number) => new Date(now - daysAgo * 86_400_000).toISOString();
const box = (over: Partial<Parameters<typeof quietFor>[0]>) => ({ status: "active" as const, receiving: "ok" as const, created_at: iso(30), last_received_at: iso(1), ...over });

describe("quietFor", () => {
  it("is quiet after a week without mail", () => {
    expect(quietFor(box({ last_received_at: iso(1) }), now)).toBeNull();
    expect(quietFor(box({ last_received_at: iso(9) }), now)).toEqual({ never: false, days: 9 });
  });
  it("waits a day before saying mail never came", () => {
    expect(quietFor(box({ last_received_at: null, created_at: iso(0.5) }), now)).toBeNull();
    expect(quietFor(box({ last_received_at: null, created_at: iso(2) }), now)).toEqual({ never: true });
  });
  it("says nothing for a mailbox that is off or not receiving", () => {
    expect(quietFor(box({ status: "disabled", last_received_at: iso(30) }), now)).toBeNull();
    expect(quietFor(box({ receiving: "imap_off", last_received_at: iso(30) }), now)).toBeNull();
  });
});
