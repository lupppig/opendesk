import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Notification, NotificationPreferences } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { queryClient } from "@/lib/queryClient";
import { notifyDesktop } from "./useDesktopBridge";

const assigned: Notification = {
  id: "n1",
  type: "assigned",
  ticket: { id: "t1", number: 12, subject: "Refund" },
  data: {},
  read_at: null,
  created_at: "2026-10-19T09:00:00Z",
};

class FakeNotification {
  static permission: NotificationPermission = "granted";
  static shown: FakeNotification[] = [];
  onclick: (() => void) | null = null;
  close = vi.fn();
  constructor(
    public title: string,
    public options: NotificationOptions,
  ) {
    FakeNotification.shown.push(this);
  }
}

let focused = false;

beforeEach(() => {
  focused = false;
  FakeNotification.permission = "granted";
  FakeNotification.shown = [];
  vi.stubGlobal("document", { hasFocus: () => focused });
  vi.stubGlobal("window", { Notification: FakeNotification, focus: vi.fn() });
});

afterEach(() => {
  vi.unstubAllGlobals();
  queryClient.removeQueries({ queryKey: qk.notificationPreferences });
});

describe("browser notifications", () => {
  it("shows one while the page is in the background, and opens the ticket on click", () => {
    const open = vi.fn();
    notifyDesktop(assigned, open);
    expect(FakeNotification.shown).toHaveLength(1);
    const note = FakeNotification.shown[0];
    expect(note.options).toMatchObject({ body: "#12 Refund", tag: "n1" });
    note.onclick?.();
    expect(open).toHaveBeenCalledWith("/inbox/mine/t1");
    expect(note.close).toHaveBeenCalled();
  });

  it("leaves a focused page to its toasts", () => {
    focused = true;
    notifyDesktop(assigned, vi.fn());
    expect(FakeNotification.shown).toHaveLength(0);
  });

  it("waits for the person's permission", () => {
    FakeNotification.permission = "default";
    notifyDesktop(assigned, vi.fn());
    expect(FakeNotification.shown).toHaveLength(0);
  });

  it("follows the type's desktop channel", () => {
    queryClient.setQueryData<Partial<NotificationPreferences>>(qk.notificationPreferences, {
      paused_until: null,
      types: [{ type: "assigned", group: "tickets", email: true, push: true, desktop: false, defaults: { email: true, push: true, desktop: true }, urgent: false }],
    });
    notifyDesktop(assigned, vi.fn());
    expect(FakeNotification.shown).toHaveLength(0);
  });
});
