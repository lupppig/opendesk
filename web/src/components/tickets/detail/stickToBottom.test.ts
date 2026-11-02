import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { stickToBottom } from "./stickToBottom";

/** ResizeObserver callbacks, run by hand: jsdom lays nothing out. */
let resized: (() => void)[] = [];
class FakeResizeObserver {
  constructor(private cb: () => void) {}
  observe() {
    resized.push(this.cb);
  }
  disconnect() {
    resized = resized.filter((cb) => cb !== this.cb);
  }
}
const resize = () => resized.forEach((cb) => cb());

/** A scrolling box whose sizes the test sets; scrollTop clamps like a browser's. */
function box(scrollHeight: number, clientHeight = 400) {
  const el = document.createElement("div");
  const size = { scrollHeight, clientHeight, top: 0 };
  Object.defineProperty(el, "scrollHeight", { get: () => size.scrollHeight });
  Object.defineProperty(el, "clientHeight", { get: () => size.clientHeight });
  Object.defineProperty(el, "scrollTop", {
    get: () => size.top,
    set: (v: number) => {
      size.top = Math.max(0, Math.min(v, size.scrollHeight - size.clientHeight));
    },
  });
  /** The agent scrolls to `top` with the wheel. */
  const scrollTo = (top: number) => {
    el.dispatchEvent(new Event("wheel"));
    el.scrollTop = top;
    el.dispatchEvent(new Event("scroll"));
  };
  /** The scroll moves to `top` on its own (the layout, the browser clamping it). */
  const moved = (top: number) => {
    el.scrollTop = top;
    el.dispatchEvent(new Event("scroll"));
  };
  return { el, size, scrollTo, moved, gap: () => size.scrollHeight - size.top - size.clientHeight };
}

describe("stickToBottom", () => {
  beforeEach(() => {
    resized = [];
    vi.stubGlobal("ResizeObserver", FakeResizeObserver);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("opens at the end", () => {
    const b = box(2000);
    stickToBottom(b.el, document.createElement("div"));
    expect(b.gap()).toBe(0);
  });

  it("stays at the end while emails and images grow, and the reply box takes room", () => {
    const b = box(2000);
    stickToBottom(b.el, document.createElement("div"));
    b.size.scrollHeight = 2600;
    resize();
    expect(b.gap()).toBe(0);
    b.size.clientHeight = 250;
    resize();
    expect(b.gap()).toBe(0);
  });

  it("stays held when content shrinks and the browser pulls the scroll back", () => {
    const b = box(2000);
    stickToBottom(b.el, document.createElement("div"));
    b.size.scrollHeight = 1500;
    b.moved(b.el.scrollTop);
    b.size.scrollHeight = 1900;
    resize();
    expect(b.gap()).toBe(0);
  });

  it("stays held when an email's frame shrinks and grows again before the scroll event", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    b.size.scrollHeight = 1000;
    // The browser clamps the scroll to the shorter content.
    b.el.scrollTop = 1600;
    b.size.scrollHeight = 2500;
    b.moved(b.el.scrollTop);
    expect(s.pinned).toBe(true);
    resize();
    expect(b.gap()).toBe(0);
  });

  it("lets go when the agent scrolls up over an email's frame, where no wheel reaches the box", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    b.moved(900);
    expect(s.pinned).toBe(false);
  });

  it("lets go when the agent scrolls up with the keyboard while content grows", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp" }));
    b.size.scrollHeight = 2300;
    b.moved(1200);
    expect(s.pinned).toBe(false);
  });

  it("lets go once the agent scrolls up, and holds again back at the end", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    b.scrollTo(600);
    expect(s.pinned).toBe(false);
    b.size.scrollHeight = 2600;
    resize();
    expect(b.el.scrollTop).toBe(600);
    b.scrollTo(2600 - 400 - 20);
    expect(s.pinned).toBe(true);
    b.size.scrollHeight = 3000;
    resize();
    expect(b.gap()).toBe(0);
  });

  it("keeps a nudge within the end's slack held", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    b.scrollTo(1600 - 40);
    expect(s.pinned).toBe(true);
  });

  it("follows new items only while held", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    b.size.scrollHeight = 2200;
    s.follow();
    expect(b.gap()).toBe(0);
    b.scrollTo(100);
    b.size.scrollHeight = 2400;
    s.follow();
    expect(b.el.scrollTop).toBe(100);
  });

  it("lets go for a jump to a message further up", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    s.release();
    b.size.scrollHeight = 2600;
    resize();
    expect(b.el.scrollTop).toBe(1600);
    expect(s.pinned).toBe(false);
  });

  it("stops listening when stopped", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"));
    s.stop();
    expect(resized).toEqual([]);
    b.scrollTo(100);
    expect(s.pinned).toBe(true);
  });
});
