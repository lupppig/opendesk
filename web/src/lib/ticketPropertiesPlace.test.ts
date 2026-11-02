import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const KEY = "od-ticket-properties";

/** The module reads storage once on load, so each case loads a fresh copy. */
async function load() {
  vi.resetModules();
  return import("./ticketPropertiesPlace");
}

describe("ticket properties place preference", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("keeps the properties in the details column until chosen", async () => {
    const { ticketPropertiesPlace } = await load();
    expect(ticketPropertiesPlace()).toBe("column");
  });

  it("is kept in this browser and read back on the next load", async () => {
    (await load()).setTicketPropertiesPlace("bar");
    expect(localStorage.getItem(KEY)).toBe("bar");
    expect((await load()).ticketPropertiesPlace()).toBe("bar");
    (await load()).setTicketPropertiesPlace("column");
    expect(localStorage.getItem(KEY)).toBeNull();
    expect((await load()).ticketPropertiesPlace()).toBe("column");
  });

  it("ignores a value it did not write", async () => {
    localStorage.setItem(KEY, "top");
    expect((await load()).ticketPropertiesPlace()).toBe("column");
  });

  it("stays in the column, and still switches for the page, when storage is unavailable", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const m = await load();
    expect(m.ticketPropertiesPlace()).toBe("column");
    m.setTicketPropertiesPlace("bar");
    expect(m.ticketPropertiesPlace()).toBe("bar");
  });

  it("re-renders who reads it, here and when another tab changes it", async () => {
    const m = await load();
    const seen: string[] = [];
    function Probe() {
      seen.push(m.useTicketPropertiesPlace());
      return null;
    }
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(createElement(Probe)));
    await act(async () => m.setTicketPropertiesPlace("bar"));
    localStorage.removeItem(KEY);
    await act(async () => window.dispatchEvent(new StorageEvent("storage", { key: KEY })));
    expect(seen).toContain("bar");
    expect(seen.at(-1)).toBe("column");
    act(() => root.unmount());
  });
});
