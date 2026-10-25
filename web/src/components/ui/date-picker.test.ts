import { describe, expect, it } from "vitest";
import { normalTime, parseTime } from "./date-picker";

describe("parseTime", () => {
  it("reads 24-hour times", () => {
    expect(parseTime("9:00")).toEqual([9, 0]);
    expect(parseTime("21:30")).toEqual([21, 30]);
    expect(parseTime("0930")).toEqual([9, 30]);
    expect(parseTime("24:00")).toBeNull();
  });
  it("reads 12-hour times", () => {
    expect(parseTime("9:00 AM")).toEqual([9, 0]);
    expect(parseTime("9:30 PM")).toEqual([21, 30]);
    expect(parseTime("9pm")).toEqual([21, 0]);
    expect(parseTime("12 am")).toEqual([0, 0]);
    expect(parseTime("12:15 p.m.")).toEqual([12, 15]);
    expect(parseTime("13:00 PM")).toBeNull();
    expect(parseTime("0:30 AM")).toBeNull();
  });
  it("stores HH:mm whichever clock was typed", () => {
    expect(normalTime("9:05 pm")).toBe("21:05");
    expect(normalTime("7:00")).toBe("07:00");
  });
});
