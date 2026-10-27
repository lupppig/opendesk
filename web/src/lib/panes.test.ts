import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BIG_STEP, clampWidth, keyWidth, maxWidth, paneCss, paneKey, paneLimits, paneWidth, resetPaneCache, setPaneWidth, STEP } from "./panes";

beforeEach(() => {
  localStorage.clear();
  resetPaneCache();
});
afterEach(() => vi.restoreAllMocks());

describe("pane widths", () => {
  it("holds a width within the pane's limits", () => {
    expect(clampWidth("list", 100)).toBe(paneLimits.list.min);
    expect(clampWidth("list", 5000)).toBe(paneLimits.list.max);
    expect(clampWidth("list", 401.6)).toBe(402);
  });

  it("gives way to a narrow room, never below the pane's minimum", () => {
    expect(maxWidth("list", 1000)).toBe(500);
    expect(maxWidth("list", 3000)).toBe(paneLimits.list.max);
    expect(maxWidth("list", 400)).toBe(paneLimits.list.min);
    expect(maxWidth("list", undefined)).toBe(paneLimits.list.max);
    expect(clampWidth("details", 520, 800)).toBe(400);
  });

  it("writes the width as CSS that the window can still shrink", () => {
    expect(paneCss("nav", 300, "vw")).toBe("clamp(200px, 300px, 30vw)");
    expect(paneCss("list", 420, "%")).toBe("clamp(280px, 420px, 50%)");
  });
});

describe("keyboard", () => {
  it("steps with the arrows, further with shift, the way the pane sits", () => {
    expect(keyWidth("ArrowRight", false, 400, "list", undefined, 1)).toBe(400 + STEP);
    expect(keyWidth("ArrowLeft", false, 400, "list", undefined, 1)).toBe(400 - STEP);
    expect(keyWidth("ArrowRight", true, 400, "list", undefined, 1)).toBe(400 + BIG_STEP);
    // A pane right of its handle widens as the handle moves left.
    expect(keyWidth("ArrowLeft", false, 300, "details", undefined, -1)).toBe(300 + STEP);
    expect(keyWidth("ArrowRight", false, 300, "details", undefined, -1)).toBe(300 - STEP);
  });

  it("goes to the ends with Home and End, within the room", () => {
    expect(keyWidth("Home", false, 400, "list", 1200, 1)).toBe(paneLimits.list.min);
    expect(keyWidth("End", false, 400, "list", 1200, 1)).toBe(600);
    expect(keyWidth("ArrowRight", true, 590, "list", 1200, 1)).toBe(600);
    expect(keyWidth("ArrowLeft", false, 285, "list", 1200, 1)).toBe(paneLimits.list.min);
  });

  it("leaves other keys alone", () => {
    expect(keyWidth("Enter", false, 400, "list", undefined, 1)).toBeNull();
    expect(keyWidth("ArrowUp", false, 400, "list", undefined, 1)).toBeNull();
  });
});

describe("remembering", () => {
  it("keeps a width per person and per pane", () => {
    setPaneWidth("list", "u1", 450);
    setPaneWidth("details", "u1", 340);
    expect(localStorage.getItem(paneKey("list", "u1"))).toBe("450");
    resetPaneCache();
    expect(paneWidth("list", "u1")).toBe(450);
    expect(paneWidth("details", "u1")).toBe(340);
    expect(paneWidth("list", "u2")).toBeNull();
    expect(paneWidth("nav", "u1")).toBeNull();
  });

  it("forgets the width on a reset", () => {
    setPaneWidth("nav", "u1", 300);
    setPaneWidth("nav", "u1", null);
    expect(localStorage.getItem(paneKey("nav", "u1"))).toBeNull();
    expect(paneWidth("nav", "u1")).toBeNull();
  });

  it("reads back stored widths within the limits, and ignores what is not one", () => {
    localStorage.setItem(paneKey("list", "u1"), "9000");
    localStorage.setItem(paneKey("nav", "u1"), "wide");
    localStorage.setItem(paneKey("details", "u1"), "-4");
    expect(paneWidth("list", "u1")).toBe(paneLimits.list.max);
    expect(paneWidth("nav", "u1")).toBeNull();
    expect(paneWidth("details", "u1")).toBeNull();
  });

  it("works without storage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(paneWidth("list", "u1")).toBeNull();
    expect(() => setPaneWidth("list", "u1", 420)).not.toThrow();
    expect(paneWidth("list", "u1")).toBe(420);
  });

  it("stores nothing without a signed-in person", () => {
    setPaneWidth("list", "", 420);
    expect(localStorage.length).toBe(0);
  });
});
