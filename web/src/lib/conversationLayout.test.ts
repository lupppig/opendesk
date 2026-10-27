import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const KEY = "od-conversation-layout";

/** The module reads storage once on load, so each case loads a fresh copy. */
async function load() {
  vi.resetModules();
  return import("./conversationLayout");
}

describe("conversation layout preference", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("is off until chosen", async () => {
    const { conversationLayoutOn } = await load();
    expect(conversationLayoutOn()).toBe(false);
  });

  it("is kept in this browser and read back on the next load", async () => {
    const first = await load();
    first.setConversationLayout(true);
    expect(localStorage.getItem(KEY)).toBe("on");
    expect((await load()).conversationLayoutOn()).toBe(true);
    (await load()).setConversationLayout(false);
    expect(localStorage.getItem(KEY)).toBeNull();
    expect((await load()).conversationLayoutOn()).toBe(false);
  });

  it("ignores a value it did not write", async () => {
    localStorage.setItem(KEY, "yes");
    expect((await load()).conversationLayoutOn()).toBe(false);
  });

  it("stays off, and still switches for the page, when storage is unavailable", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const m = await load();
    expect(m.conversationLayoutOn()).toBe(false);
    m.setConversationLayout(true);
    expect(m.conversationLayoutOn()).toBe(true);
  });

  it("re-renders who reads it, here and when another tab changes it", async () => {
    const m = await load();
    const seen: boolean[] = [];
    function Probe() {
      seen.push(m.useConversationLayout());
      return null;
    }
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(createElement(Probe)));
    await act(async () => m.setConversationLayout(true));
    localStorage.removeItem(KEY);
    await act(async () => window.dispatchEvent(new StorageEvent("storage", { key: KEY })));
    expect(seen.at(-1)).toBe(false);
    expect(seen).toContain(true);
    act(() => root.unmount());
  });
});
