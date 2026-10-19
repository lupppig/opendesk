import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserWindow } from "electron";

const setBadgeCount = vi.fn();
const createFromBitmap = vi.fn((bitmap: Buffer, opts: unknown) => ({ bitmap, opts }));

vi.mock("electron", () => ({
  app: { setBadgeCount },
  nativeImage: { createFromBitmap },
  Notification: class {
    static isSupported() {
      return false;
    }
  },
}));

function fakeWindow() {
  return { setOverlayIcon: vi.fn() } as unknown as BrowserWindow;
}

async function freshNotify() {
  vi.resetModules();
  return import("./notify");
}

const originalPlatform = process.platform;

afterEach(() => {
  Object.defineProperty(process, "platform", { value: originalPlatform });
  setBadgeCount.mockClear();
  createFromBitmap.mockClear();
});

describe("setBadge on macOS/Linux", () => {
  it("sets the dock/taskbar badge count", async () => {
    Object.defineProperty(process, "platform", { value: "darwin" });
    const { setBadge } = await freshNotify();
    setBadge(3, null);
    expect(setBadgeCount).toHaveBeenCalledWith(3);
  });

  it("skips the call when the count hasn't changed", async () => {
    Object.defineProperty(process, "platform", { value: "darwin" });
    const { setBadge } = await freshNotify();
    setBadge(3, null);
    setBadge(3, null);
    expect(setBadgeCount).toHaveBeenCalledTimes(1);
  });

  it("calls again once the count actually changes", async () => {
    Object.defineProperty(process, "platform", { value: "darwin" });
    const { setBadge } = await freshNotify();
    setBadge(3, null);
    setBadge(5, null);
    expect(setBadgeCount).toHaveBeenNthCalledWith(2, 5);
  });
});

describe("setBadge on Windows", () => {
  it("sets an overlay icon with a tooltip when the count is positive", async () => {
    Object.defineProperty(process, "platform", { value: "win32" });
    const { setBadge } = await freshNotify();
    const win = fakeWindow();
    setBadge(7, win);
    expect(win.setOverlayIcon).toHaveBeenCalledTimes(1);
    const [icon, tooltip] = (win.setOverlayIcon as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(icon).not.toBeNull();
    expect(tooltip).toBe("7 unread");
    expect(setBadgeCount).not.toHaveBeenCalled();
  });

  it("clears the overlay icon when the count drops to zero", async () => {
    Object.defineProperty(process, "platform", { value: "win32" });
    const { setBadge } = await freshNotify();
    const win = fakeWindow();
    setBadge(2, win);
    setBadge(0, win);
    expect(win.setOverlayIcon).toHaveBeenLastCalledWith(null, "");
  });

  it("draws the overlay dot once and reuses it across calls", async () => {
    Object.defineProperty(process, "platform", { value: "win32" });
    const { setBadge } = await freshNotify();
    setBadge(1, fakeWindow());
    setBadge(2, fakeWindow());
    expect(createFromBitmap).toHaveBeenCalledTimes(1);
  });
});
