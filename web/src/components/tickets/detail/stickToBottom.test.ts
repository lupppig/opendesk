import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { FLOOR_PAD, latestMessageFloor, stickToBottom } from "./stickToBottom";

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

describe("stickToBottom with the latest message's floor", () => {
  beforeEach(() => {
    resized = [];
    vi.stubGlobal("ResizeObserver", FakeResizeObserver);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("opens on the latest message when history after it is taller than the box", () => {
    // End at 1600; the latest message starts at 1216, so the hold stops at 1200.
    const b = box(2000);
    stickToBottom(b.el, document.createElement("div"), () => 1200);
    expect(b.el.scrollTop).toBe(1200);
  });

  it("opens at the end when the latest message is in view there", () => {
    const b = box(2000);
    stickToBottom(b.el, document.createElement("div"), () => 1700);
    expect(b.gap()).toBe(0);
  });

  it("keeps the message in view while more history arrives below it", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"), () => 1200);
    b.size.scrollHeight = 2300;
    resize();
    s.follow();
    expect(b.el.scrollTop).toBe(1200);
    expect(s.pinned).toBe(true);
  });

  it("follows the message as content above it grows", () => {
    const b = box(2000);
    let floor = 1200;
    stickToBottom(b.el, document.createElement("div"), () => floor);
    b.size.scrollHeight = 2500;
    floor = 1700;
    resize();
    expect(b.el.scrollTop).toBe(1700);
  });

  it("holds the end once the agent scrolls down past the message", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"), () => 1200);
    b.scrollTo(1600);
    expect(s.pinned).toBe(true);
    b.size.scrollHeight = 2300;
    resize();
    expect(b.gap()).toBe(0);
  });

  it("lets go when the agent scrolls up from the message", () => {
    const b = box(2000);
    const s = stickToBottom(b.el, document.createElement("div"), () => 1200);
    b.scrollTo(500);
    expect(s.pinned).toBe(false);
    b.size.scrollHeight = 2300;
    resize();
    expect(b.el.scrollTop).toBe(500);
  });
});

describe("latestMessageFloor", () => {
  const rect = (top: number, height: number) => ({ top, bottom: top + height, height, left: 0, right: 0, width: 0, x: 0, y: top, toJSON: () => ({}) }) as DOMRect;
  function timeline(messages: [number, number][], clientHeight = 400, scrollTop = 0) {
    const el = document.createElement("div");
    const content = document.createElement("div");
    el.appendChild(content);
    Object.defineProperty(el, "clientHeight", { get: () => clientHeight });
    Object.defineProperty(el, "scrollTop", { get: () => scrollTop });
    el.getBoundingClientRect = () => rect(100, clientHeight);
    for (const [i, [top, height]] of messages.entries()) {
      const a = document.createElement("article");
      a.id = `m-${i}`;
      a.getBoundingClientRect = () => rect(100 + top - scrollTop, height);
      content.appendChild(a);
      const row = document.createElement("div");
      row.textContent = "history";
      content.appendChild(row);
    }
    return { el, content };
  }

  it("is null without a message", () => {
    const { el, content } = timeline([]);
    expect(latestMessageFloor(el, content)).toBeNull();
  });

  it("puts a message that fits near the top of the box", () => {
    const { el, content } = timeline([[0, 300], [1200, 200]], 400, 50);
    expect(latestMessageFloor(el, content)).toBe(1200 - FLOOR_PAD);
  });

  it("puts the end of a message taller than the box near its bottom", () => {
    const { el, content } = timeline([[1000, 900]]);
    expect(latestMessageFloor(el, content)).toBe(1900 - 400 + FLOOR_PAD);
  });
});
