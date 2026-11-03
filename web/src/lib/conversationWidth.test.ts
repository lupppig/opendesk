import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const KEY = "od-conversation-width";

/** The module reads storage once on load, so each case loads a fresh copy. */
async function load() {
  vi.resetModules();
  return import("./conversationWidth");
}

describe("conversation width preference", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("is limited to a readable measure until full width is chosen", async () => {
    const m = await load();
    expect(m.conversationWidth()).toBe("limited");
    expect(m.conversationMaxWidth("limited")).toBe("max-w-[820px]");
    expect(m.conversationMaxWidth("full")).toBe("max-w-none");
  });

  it("is kept in this browser and read back on the next load", async () => {
    (await load()).setConversationWidth("full");
    expect(localStorage.getItem(KEY)).toBe("full");
    expect((await load()).conversationWidth()).toBe("full");
    (await load()).setConversationWidth("limited");
    expect(localStorage.getItem(KEY)).toBeNull();
    expect((await load()).conversationWidth()).toBe("limited");
  });

  it("ignores a value it did not write", async () => {
    localStorage.setItem(KEY, "wide");
    expect((await load()).conversationWidth()).toBe("limited");
  });

  it("stays limited, and still switches for the page, when storage is unavailable", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const m = await load();
    expect(m.conversationWidth()).toBe("limited");
    m.setConversationWidth("full");
    expect(m.conversationWidth()).toBe("full");
  });

  it("re-renders who reads it, here and when another tab changes it", async () => {
    const m = await load();
    const seen: string[] = [];
    function Probe() {
      seen.push(m.useConversationWidth());
      return null;
    }
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(createElement(Probe)));
    await act(async () => m.setConversationWidth("full"));
    localStorage.removeItem(KEY);
    await act(async () => window.dispatchEvent(new StorageEvent("storage", { key: KEY })));
    expect(seen.at(-1)).toBe("limited");
    expect(seen).toContain("full");
    act(() => root.unmount());
  });
});
