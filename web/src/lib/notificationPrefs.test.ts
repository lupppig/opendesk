import { describe, expect, it } from "vitest";
import type { NotificationPreferences, NotificationTypePrefs } from "./api/types";
import { applyPreferencesPatch, DEFAULT_PACING, gapWords, groupTypes, isPaused, NOTIFICATION_DEFAULTS, NOTIFICATION_ORDER, notificationHolds, pacingCopy, pauseEnd, URGENT_TYPES, wantsDesktop } from "./notificationPrefs";

const row = (type: NotificationTypePrefs["type"], over: Partial<NotificationTypePrefs> = {}): NotificationTypePrefs => {
  const { group, defaults } = NOTIFICATION_DEFAULTS[type];
  return { type, group, defaults, urgent: URGENT_TYPES.has(type), ...defaults, ...over };
};

const prefs = (over: Partial<NotificationPreferences> = {}): NotificationPreferences => ({
  email_cadence: "instant",
  digest_hour: 9,
  timezone: "UTC",
  paused_until: null,
  email: "maya@margin.app",
  email_verified: true,
  delay_minutes: 5,
  pacing: DEFAULT_PACING,
  unsubscribed_at: null,
  types: [row("assigned"), row("mentioned", { desktop: false }), row("team_joined")],
  teams: [{ id: "t1", name: "Billing", notify: true }, { id: "t2", name: "Legal", notify: true }],
  ...over,
});

const NOW = Date.parse("2026-10-13T09:42:00Z");

describe("wantsDesktop", () => {
  it("follows the defaults until the preferences load", () => {
    expect(wantsDesktop(undefined, "assigned", NOW)).toBe(true);
    expect(wantsDesktop(undefined, "team_joined", NOW)).toBe(true);
    expect(wantsDesktop(undefined, "ai_balance_low", NOW)).toBe(true);
  });
  it("follows the type's desktop channel", () => {
    expect(wantsDesktop(prefs(), "assigned", NOW)).toBe(true);
    expect(wantsDesktop(prefs(), "mentioned", NOW)).toBe(false);
  });
  it("is quiet while paused and speaks again once the pause is over", () => {
    expect(wantsDesktop(prefs({ paused_until: "2026-10-13T10:00:00Z" }), "assigned", NOW)).toBe(false);
    expect(wantsDesktop(prefs({ paused_until: "2026-10-13T09:00:00Z" }), "assigned", NOW)).toBe(true);
  });
  it("uses the default for a type the list leaves out", () => {
    expect(wantsDesktop(prefs(), "sla_breached", NOW)).toBe(true);
  });
});

describe("isPaused", () => {
  it("is on only for a time in the future", () => {
    expect(isPaused(null, NOW)).toBe(false);
    expect(isPaused("not a time", NOW)).toBe(false);
    expect(isPaused("2026-10-13T09:41:59Z", NOW)).toBe(false);
    expect(isPaused("2026-10-13T09:42:01Z", NOW)).toBe(true);
  });
});

describe("pauseEnd", () => {
  const now = new Date(2026, 9, 13, 22, 15); // local time, late evening
  it("pauses for an hour or a week from now", () => {
    expect(pauseEnd("hour", now).getTime() - now.getTime()).toBe(3_600_000);
    expect(pauseEnd("week", now).getTime() - now.getTime()).toBe(7 * 86_400_000);
  });
  it("ends tomorrow morning at 9:00 local time", () => {
    const end = pauseEnd("tomorrow", now);
    expect([end.getFullYear(), end.getMonth(), end.getDate(), end.getHours(), end.getMinutes()]).toEqual([2026, 9, 14, 9, 0]);
  });
  it("means the next day even just after midnight, across a month end", () => {
    const end = pauseEnd("tomorrow", new Date(2026, 9, 31, 0, 30));
    expect([end.getMonth(), end.getDate(), end.getHours()]).toEqual([10, 1, 9]);
  });
});

describe("applyPreferencesPatch", () => {
  it("changes only what the patch names", () => {
    const p = prefs();
    const next = applyPreferencesPatch(p, { types: [{ type: "assigned", email: false }] });
    expect(next.types[0]).toMatchObject({ type: "assigned", email: false, push: true, desktop: true });
    expect(next.types[1]).toBe(p.types[1]);
    expect(next.email_cadence).toBe("instant");
    expect(p.types[0].email).toBe(true);
  });
  it("sets the schedule and the pause, and an empty pause resumes", () => {
    const paused = applyPreferencesPatch(prefs(), { email_cadence: "daily", digest_hour: 7, timezone: "Europe/Budapest", paused_until: "2026-10-14T07:00:00Z" });
    expect(paused).toMatchObject({ email_cadence: "daily", digest_hour: 7, timezone: "Europe/Budapest", paused_until: "2026-10-14T07:00:00Z" });
    expect(applyPreferencesPatch(paused, { paused_until: "" }).paused_until).toBeNull();
  });
  it("turns one team off and leaves the others", () => {
    const p = prefs();
    const next = applyPreferencesPatch(p, { teams: [{ id: "t2", notify: false }] });
    expect(next.teams.map((t) => t.notify)).toEqual([true, false]);
    expect(next.teams[0]).toBe(p.teams[0]);
  });
});

describe("new_ticket", () => {
  it("is a ticket notification right after assigned, emailed, pushed and on the desktop", () => {
    expect(NOTIFICATION_DEFAULTS.new_ticket).toEqual({ group: "tickets", defaults: { email: true, push: true, desktop: true } });
    expect(NOTIFICATION_ORDER.indexOf("new_ticket")).toBe(NOTIFICATION_ORDER.indexOf("assigned") + 1);
    expect(URGENT_TYPES.has("new_ticket")).toBe(false);
    expect(wantsDesktop(undefined, "new_ticket", NOW)).toBe(true);
  });
});

describe("groupTypes", () => {
  it("groups in a fixed order and leaves empty groups out", () => {
    const g = groupTypes([row("team_joined"), row("mentioned"), row("assigned")]);
    expect(g.map((x) => x.group)).toEqual(["tickets", "team"]);
    expect(g[0].types.map((t) => t.type)).toEqual(["assigned", "mentioned"]);
  });
});

describe("pacing copy", () => {
  it("puts gaps in words", () => {
    expect(gapWords(60)).toEqual({ key: "hour", count: 1 });
    expect(gapWords(120)).toEqual({ key: "hours", count: 2 });
    expect(gapWords(90)).toEqual({ key: "minutes", count: 90 });
    expect(gapWords(10)).toEqual({ key: "minutes", count: 10 });
  });
  it("explains Cloud's pacing with its longest cooldown and urgent gap", () => {
    const c = pacingCopy(DEFAULT_PACING);
    expect(c.instant).toEqual({ key: "instantPaced", delay: 5, gap: { key: "hour", count: 1 } });
    expect(c.limits).toEqual({ perHour: 4, perDay: 24 });
    expect(c.urgent).toEqual({ key: "minutes", count: 10 });
  });
  it("follows a server's own numbers", () => {
    const c = pacingCopy({ ...DEFAULT_PACING, delay_minutes: 2, cooldown_minutes: [5, 15, 30, 120], urgent_gap_minutes: 5, per_hour: 12, per_day: 100 });
    expect(c.instant).toEqual({ key: "instantPaced", delay: 2, gap: { key: "hours", count: 2 } });
    expect(c.limits).toEqual({ perHour: 12, perDay: 100 });
    expect(c.urgent).toEqual({ key: "minutes", count: 5 });
  });
  it("drops the cooldown sentence without one, and uses the defaults before the server sends pacing", () => {
    expect(pacingCopy({ ...DEFAULT_PACING, cooldown_minutes: [] }).instant).toEqual({ key: "instantHint", delay: 5, gap: null });
    expect(pacingCopy(undefined, 3).instant).toMatchObject({ key: "instantPaced", delay: 3 });
  });
  it("marks the urgent types", () => {
    expect([...URGENT_TYPES].sort()).toEqual(["ai_balance_out", "ai_handoff", "ai_key_failed", "auto_recharge_failed", "mentioned", "sla_breached"]);
  });
});

describe("notificationHolds", () => {
  it("lists what stops notifications, most important first, with nothing when all is on", () => {
    expect(notificationHolds(prefs(), NOW)).toEqual([]);
    const all = notificationHolds(prefs({ unsubscribed_at: "2026-10-12T08:00:00Z", paused_until: "2026-10-13T12:00:00Z", email_cadence: "off" }), NOW);
    expect(all.map((h) => h.kind)).toEqual(["unsubscribed", "paused", "emailOff"]);
  });
  it("ignores a pause that has ended", () => {
    expect(notificationHolds(prefs({ paused_until: "2026-10-13T09:00:00Z" }), NOW)).toEqual([]);
  });
});
