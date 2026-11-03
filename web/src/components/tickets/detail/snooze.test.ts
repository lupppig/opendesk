import { addDays, addHours, nextMonday, setHours, setMilliseconds, setMinutes, setSeconds, startOfHour } from "date-fns";
import { describe, expect, it } from "vitest";
import { DEFAULT_SNOOZE_PRESETS, MAX_SNOOZE_PRESETS, SNOOZE_TOMORROW, sameSnoozePresets, snoozeAt, snoozePresets } from "./snooze";
import type { SnoozePreset } from "@/lib/api/types";

const at = (iso: string, p: SnoozePreset, tz: string) => snoozeAt(p, new Date(iso), tz).toISOString();
const days = (n: number, time = "09:00"): SnoozePreset => ({ kind: "days", days: n, time });
const hours = (n: number): SnoozePreset => ({ kind: "hours", hours: n });
const week = (time = "09:00"): SnoozePreset => ({ kind: "week", time });

describe("snoozeAt", () => {
  it("gives the defaults the times the menu always offered, on the browser's clock", () => {
    // The menu used to keep the current seconds on 09:00 (09:00:30.250); it now snoozes until 09:00:00.
    const at9 = (d: Date) => setMilliseconds(setSeconds(setMinutes(setHours(d, 9), 0), 0), 0);
    for (const iso of ["2026-11-02T10:20:30.250Z", "2026-11-08T23:59:59.999Z", "2026-03-28T22:15:00Z", "2026-10-24T12:00:00Z", "2026-12-31T23:00:00Z"]) {
      const now = new Date(iso);
      const old = [startOfHour(addHours(now, 2)), startOfHour(addHours(now, 5)), at9(addDays(now, 1)), at9(nextMonday(now))];
      expect(DEFAULT_SNOOZE_PRESETS.map((p) => snoozeAt(p, now).getTime()), iso).toEqual(old.map((d) => d.getTime()));
    }
  });

  it("puts hours on the top of the hour at least that many hours away", () => {
    expect(at("2026-11-02T10:20:00Z", hours(1), "UTC")).toBe("2026-11-02T12:00:00.000Z");
    expect(at("2026-11-02T10:59:59.900Z", hours(1), "UTC")).toBe("2026-11-02T12:00:00.000Z");
    expect(at("2026-11-02T10:00:00Z", hours(4), "UTC")).toBe("2026-11-02T15:00:00.000Z");
    expect(at("2026-11-02T22:30:00Z", hours(24), "UTC")).toBe("2026-11-03T23:00:00.000Z");
    // A clock half an hour off UTC has its own top of the hour: 15:50 in Kolkata, in 1 hour, is 17:00 there.
    expect(at("2026-11-02T10:20:00Z", hours(1), "Asia/Kolkata")).toBe("2026-11-02T11:30:00.000Z");
  });

  it("counts days on the agent's clock, rolling over at their midnight", () => {
    // Monday 23:30 in Brussels: tomorrow is Tuesday.
    expect(at("2026-11-02T22:30:00Z", days(1), "Europe/Brussels")).toBe("2026-11-03T08:00:00.000Z");
    expect(at("2026-11-02T22:30:00Z", days(3), "Europe/Brussels")).toBe("2026-11-05T08:00:00.000Z");
    expect(at("2026-11-02T22:30:00Z", days(7), "Europe/Brussels")).toBe("2026-11-09T08:00:00.000Z");
    // Half an hour later it is Tuesday in Brussels but still Monday in UTC and in Los Angeles.
    expect(at("2026-11-02T23:30:00Z", days(1), "Europe/Brussels")).toBe("2026-11-04T08:00:00.000Z");
    expect(at("2026-11-02T23:30:00Z", days(1), "UTC")).toBe("2026-11-03T09:00:00.000Z");
    expect(at("2026-11-02T23:30:00Z", days(1), "America/Los_Angeles")).toBe("2026-11-03T17:00:00.000Z");
    // East of UTC, the day turns earlier.
    expect(at("2026-11-02T15:30:00Z", days(1, "08:30"), "Asia/Tokyo")).toBe("2026-11-03T23:30:00.000Z");
  });

  it("rolls over months and years, and leap days", () => {
    expect(at("2026-12-30T12:00:00Z", days(3), "UTC")).toBe("2027-01-02T09:00:00.000Z");
    expect(at("2028-02-28T12:00:00Z", days(1, "00:00"), "UTC")).toBe("2028-02-29T00:00:00.000Z");
    expect(at("2026-01-31T12:00:00Z", days(30, "23:59"), "UTC")).toBe("2026-03-02T23:59:00.000Z");
  });

  it("keeps the time of day across a clock change", () => {
    // Friday in Brussels; summer time ends on Sunday 25 October, so Monday's 09:00 is an hour later in UTC.
    expect(at("2026-10-23T10:00:00Z", days(3), "Europe/Brussels")).toBe("2026-10-26T08:00:00.000Z");
    expect(at("2026-10-23T10:00:00Z", days(1), "Europe/Brussels")).toBe("2026-10-24T07:00:00.000Z");
    // Summer time starts on Sunday 29 March.
    expect(at("2026-03-27T10:00:00Z", days(3), "Europe/Brussels")).toBe("2026-03-30T07:00:00.000Z");
    expect(at("2026-03-06T12:00:00Z", days(3), "America/New_York")).toBe("2026-03-09T13:00:00.000Z");
    // Hours are hours: 4 hours before the change lands 4 hours later, whatever the clock says.
    expect(at("2026-10-25T00:30:00Z", hours(1), "Europe/Brussels")).toBe("2026-10-25T02:00:00.000Z");
    // In the hour the clock repeats, the top of the hour is the later 02:00, still at least an hour away.
    expect(at("2026-10-24T23:30:00Z", hours(1), "Europe/Brussels")).toBe("2026-10-25T01:00:00.000Z");
  });

  it("moves a skipped time forward across a spring clock change", () => {
    expect(at("2027-03-13T17:00:00Z", days(1, "02:30"), "America/New_York")).toBe("2027-03-14T07:30:00.000Z");
    expect(at("2027-03-27T17:00:00Z", days(1, "02:30"), "Europe/Brussels")).toBe("2027-03-28T01:30:00.000Z");
    expect(at("2027-03-13T17:00:00Z", days(1, "03:30"), "America/New_York")).toBe("2027-03-14T07:30:00.000Z");
  });

  it("makes next week the coming Monday, a week on when today is Monday", () => {
    expect(at("2026-11-02T10:00:00Z", week(), "Europe/Brussels")).toBe("2026-11-09T08:00:00.000Z"); // Monday
    expect(at("2026-11-04T10:00:00Z", week("14:30"), "Europe/Brussels")).toBe("2026-11-09T13:30:00.000Z"); // Wednesday
    expect(at("2026-11-08T22:30:00Z", week(), "UTC")).toBe("2026-11-09T09:00:00.000Z"); // Sunday in UTC
    expect(at("2026-11-08T23:30:00Z", week(), "Europe/Brussels")).toBe("2026-11-16T08:00:00.000Z"); // already Monday in Brussels
  });

  it("snoozes the list's quick action until tomorrow at 09:00", () => {
    expect(at("2026-11-02T15:00:00Z", SNOOZE_TOMORROW, "Europe/Brussels")).toBe("2026-11-03T08:00:00.000Z");
  });
});

describe("snoozePresets", () => {
  it("offers the defaults until the workspace sets its own", () => {
    expect(snoozePresets(undefined)).toEqual(DEFAULT_SNOOZE_PRESETS);
    expect(snoozePresets({})).toEqual(DEFAULT_SNOOZE_PRESETS);
    expect(snoozePresets({ snooze_presets: [] })).toEqual(DEFAULT_SNOOZE_PRESETS);
    const lamptwist = [days(1), days(3), days(7)];
    expect(snoozePresets({ snooze_presets: lamptwist })).toEqual(lamptwist);
  });

  it("never offers more than the maximum", () => {
    const many = Array.from({ length: MAX_SNOOZE_PRESETS + 2 }, (_, i) => hours(i + 1));
    expect(snoozePresets({ snooze_presets: many })).toHaveLength(MAX_SNOOZE_PRESETS);
  });

  it("tells the same list from another", () => {
    expect(sameSnoozePresets([hours(1), hours(4), days(1), week()], DEFAULT_SNOOZE_PRESETS)).toBe(true);
    expect(sameSnoozePresets([hours(1), hours(4), days(1), week("10:00")], DEFAULT_SNOOZE_PRESETS)).toBe(false);
    expect(sameSnoozePresets([hours(4), hours(1), days(1), week()], DEFAULT_SNOOZE_PRESETS)).toBe(false);
    expect(sameSnoozePresets([hours(1)], DEFAULT_SNOOZE_PRESETS)).toBe(false);
  });
});
