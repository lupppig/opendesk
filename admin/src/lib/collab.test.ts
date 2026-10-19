import { describe, expect, it } from "vitest";
import { untilFor, ymd } from "./collab";

describe("untilFor", () => {
  const now = new Date(2026, 9, 18, 10, 0, 0);

  it("has no end date by default", () => {
    expect(untilFor("none", "", now)).toEqual({ until: null });
  });

  it("adds the preset's days", () => {
    expect(untilFor("30", "", now)).toEqual({ until: new Date(now.getTime() + 30 * 86_400_000).toISOString() });
  });

  it("ends a chosen day at its end, local time", () => {
    const r = untilFor("date", "2026-12-31", now);
    expect(r).toEqual({ until: new Date(2026, 11, 31, 23, 59, 59).toISOString() });
  });

  it("refuses a malformed, impossible or past day", () => {
    expect(untilFor("date", "next week", now)).toHaveProperty("error");
    expect(untilFor("date", "2026-02-30", now)).toHaveProperty("error");
    expect(untilFor("date", "2026-10-17", now)).toHaveProperty("error");
    expect(untilFor("date", ymd(now), now)).toEqual({ until: new Date(2026, 9, 18, 23, 59, 59).toISOString() });
  });
});
