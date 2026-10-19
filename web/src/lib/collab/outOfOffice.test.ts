import { describe, expect, it } from "vitest";
import type { MailOutOfOffice } from "@/lib/api/types";
import { isAway, joinDay, outOfOfficeBody, outOfOfficeForm, outOfOfficeProblem } from "./outOfOffice";

const ooo = (p: Partial<MailOutOfOffice> = {}): MailOutOfOffice => ({
  enabled: true,
  start: null,
  end: null,
  subject: "Away",
  message_html: "<p>Back soon</p>",
  contacts_only: false,
  internal_only: false,
  ...p,
});

describe("isAway", () => {
  const now = Date.parse("2026-10-18T12:00:00Z");
  it("is on while enabled and inside its dates", () => {
    expect(isAway({ out_of_office: ooo(), out_of_office_sync: "two_way" }, now)).toBe(true);
    expect(isAway({ out_of_office: ooo({ start: "2026-10-18T00:00:00Z", end: "2026-10-20T00:00:00Z" }), out_of_office_sync: "read_only" }, now)).toBe(true);
  });
  it("is off when turned off, not started, ended or not synced", () => {
    expect(isAway({ out_of_office: ooo({ enabled: false }), out_of_office_sync: "two_way" }, now)).toBe(false);
    expect(isAway({ out_of_office: ooo({ start: "2026-10-19T00:00:00Z" }), out_of_office_sync: "two_way" }, now)).toBe(false);
    expect(isAway({ out_of_office: ooo({ end: "2026-10-18T11:00:00Z" }), out_of_office_sync: "two_way" }, now)).toBe(false);
    expect(isAway({ out_of_office: null, out_of_office_sync: "none" }, now)).toBe(false);
  });
});

describe("the out of office form", () => {
  it("round-trips local days and times", () => {
    const start = joinDay("2026-12-20", "09:30")!;
    const f = outOfOfficeForm(ooo({ start, end: null }));
    expect(f.startDay).toBe("2026-12-20");
    expect(f.startTime).toBe("09:30");
    expect(f.endDay).toBe("");
    expect(outOfOfficeBody(f)).toMatchObject({ enabled: true, start, end: null, subject: "Away" });
  });
  it("needs the end after the start, and both for Outlook", () => {
    const f = outOfOfficeForm(ooo());
    expect(outOfOfficeProblem({ ...f, startDay: "2026-12-20", endDay: "2026-12-19" }, "google")).toBe("detail.ooo.endBeforeStart");
    expect(outOfOfficeProblem({ ...f, startDay: "2026-12-20" }, "google")).toBeNull();
    expect(outOfOfficeProblem({ ...f, startDay: "2026-12-20" }, "microsoft")).toBe("detail.ooo.needBoth");
    expect(outOfOfficeProblem({ ...f, enabled: false, startDay: "2026-12-20" }, "microsoft")).toBeNull();
  });
});
