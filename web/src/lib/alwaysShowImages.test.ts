import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const KEY = "od-always-show-images";

/** The module reads storage once on load, so each case loads a fresh copy. */
async function load() {
  vi.resetModules();
  return import("./alwaysShowImages");
}

describe("always show images preference", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("is off until switched on", async () => {
    expect((await load()).alwaysShowImages()).toBe(false);
  });

  it("is kept in this browser and read back on the next load", async () => {
    (await load()).setAlwaysShowImages(true);
    expect(localStorage.getItem(KEY)).toBe("on");
    expect((await load()).alwaysShowImages()).toBe(true);
    (await load()).setAlwaysShowImages(false);
    expect(localStorage.getItem(KEY)).toBeNull();
    expect((await load()).alwaysShowImages()).toBe(false);
  });

  it("ignores a value it did not write", async () => {
    localStorage.setItem(KEY, "yes");
    expect((await load()).alwaysShowImages()).toBe(false);
  });

  it("stays off, and still switches for the page, when storage is unavailable", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const m = await load();
    expect(m.alwaysShowImages()).toBe(false);
    m.setAlwaysShowImages(true);
    expect(m.alwaysShowImages()).toBe(true);
  });

  it("re-renders who reads it, here and when another tab changes it", async () => {
    const m = await load();
    const seen: boolean[] = [];
    function Probe() {
      seen.push(m.useAlwaysShowImages());
      return null;
    }
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(createElement(Probe)));
    await act(async () => m.setAlwaysShowImages(true));
    localStorage.removeItem(KEY);
    await act(async () => window.dispatchEvent(new StorageEvent("storage", { key: KEY })));
    expect(seen.at(-1)).toBe(false);
    expect(seen).toContain(true);
    act(() => root.unmount());
  });
});
