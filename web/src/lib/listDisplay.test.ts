import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// A fresh copy of the module, as on a new page load.
async function load() {
  vi.resetModules();
  return import("./listDisplay");
}

/** What a mounted component reads from the hook, after each change. */
async function mounted(mod: Awaited<ReturnType<typeof load>>) {
  const seen: string[] = [];
  function Probe() {
    seen.push(mod.useAssigneeDisplay());
    return null;
  }
  const el = document.createElement("div");
  const root = createRoot(el);
  await act(async () => root.render(createElement(Probe)));
  return { seen, unmount: () => act(async () => root.unmount()) };
}

describe("the list's assignee display", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.restoreAllMocks());

  it("shows the photo until something else is chosen, and ignores a value it does not know", async () => {
    expect((await mounted(await load())).seen.at(-1)).toBe("photo");
    localStorage.setItem("od-list-assignee", "avatar");
    expect((await mounted(await load())).seen.at(-1)).toBe("photo");
  });

  it("keeps the choice in this browser and reads it back on the next load", async () => {
    const mod = await load();
    const m = await mounted(mod);
    await act(async () => mod.setAssigneeDisplay("name"));
    expect(m.seen.at(-1)).toBe("name");
    expect(localStorage.getItem("od-list-assignee")).toBe("name");
    await m.unmount();
    expect((await mounted(await load())).seen.at(-1)).toBe("name");
    localStorage.setItem("od-list-assignee", "none");
    expect((await mounted(await load())).seen.at(-1)).toBe("none");
  });

  it("follows a change made in another tab", async () => {
    const mod = await load();
    const m = await mounted(mod);
    localStorage.setItem("od-list-assignee", "none");
    await act(async () => window.dispatchEvent(new StorageEvent("storage", { key: "od-list-assignee" })));
    expect(m.seen.at(-1)).toBe("none");
  });

  it("works for the page load when storage is blocked", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    const mod = await load();
    const m = await mounted(mod);
    expect(m.seen.at(-1)).toBe("photo");
    await act(async () => mod.setAssigneeDisplay("name"));
    expect(m.seen.at(-1)).toBe("name");
  });
});
