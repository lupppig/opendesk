import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { api } from "../api/endpoints";
import type { WikiPage, WikiPagePatch } from "../api/types";
import { PageSaver, SAVE_DELAY, flushAll, resetSavers, saverFor } from "./saver";

const page = (over: Partial<WikiPage> = {}): WikiPage => ({
  id: "p1",
  parent_id: null,
  title: "Refunds",
  icon: "",
  body_html: "<p>v1</p>",
  position: 0,
  version: 1,
  updated_at: "2026-10-01T00:00:00Z",
  created_by_id: null,
  created_at: "2026-10-01T00:00:00Z",
  created_by: null,
  updated_by: null,
  breadcrumbs: [],
  children: [],
  ...over,
});

describe("wiki autosave", () => {
  let sent: WikiPagePatch[];
  let server: WikiPage;
  beforeEach(() => {
    vi.useFakeTimers();
    sent = [];
    server = page();
    vi.spyOn(api.wiki, "update").mockImplementation(async (_id, b) => {
      sent.push(b);
      if (b.version !== server.version) throw new ApiError({ status: 409, code: "page_changed", title: "Conflict" });
      server = { ...server, ...b, version: server.version + 1 };
      return server;
    });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    resetSavers();
  });

  it("waits for a pause in typing and sends one save with the version it edited", async () => {
    const onSaved = vi.fn();
    const s = new PageSaver(page(), { onSaved });
    s.edit({ body_html: "<p>v2</p>" });
    s.edit({ body_html: "<p>v3</p>" });
    expect(s.status).toBe("dirty");
    await vi.advanceTimersByTimeAsync(SAVE_DELAY - 10);
    expect(sent).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(20);
    expect(sent).toEqual([{ body_html: "<p>v3</p>", version: 1 }]);
    expect(s.status).toBe("saved");
    expect(s.version).toBe(2);
    expect(onSaved).toHaveBeenCalledOnce();
  });

  it("keeps typing made during a save and sends it next, never two at once", async () => {
    let release!: () => void;
    const s = new PageSaver(page());
    vi.mocked(api.wiki.update).mockImplementationOnce(async (_id, b) => {
      sent.push(b);
      await new Promise<void>((r) => (release = r));
      server = { ...server, ...b, version: 2 };
      return server;
    });
    s.edit({ title: "Refund policy" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    s.edit({ body_html: "<p>while saving</p>" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY * 3);
    expect(sent).toHaveLength(1);
    expect(s.status).toBe("saving");
    release();
    await vi.advanceTimersByTimeAsync(SAVE_DELAY + 10);
    expect(sent).toEqual([{ title: "Refund policy", version: 1 }, { body_html: "<p>while saving</p>", version: 2 }]);
    expect(s.status).toBe("saved");
  });

  it("stops at a conflict and keeps the typing, which can be saved over the newer version", async () => {
    const s = new PageSaver(page());
    server = { ...server, body_html: "<p>theirs</p>", version: 2 };
    s.edit({ body_html: "<p>mine</p>" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(s.status).toBe("conflict");
    expect(s.draft.body_html).toBe("<p>mine</p>");
    s.edit({ body_html: "<p>mine, more</p>" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY * 2);
    expect(sent).toHaveLength(1);
    s.overwrite(2);
    await vi.advanceTimersByTimeAsync(0);
    expect(sent[1]).toEqual({ title: "Refunds", icon: "", body_html: "<p>mine, more</p>", version: 2 });
    expect(s.status).toBe("saved");
    expect(server.body_html).toBe("<p>mine, more</p>");
  });

  it("takes someone else's version when nothing is pending", () => {
    const s = new PageSaver(page());
    s.rebase({ version: 4, title: "New", icon: "📘", body_html: "<p>x</p>" });
    expect(s.draft).toEqual({ title: "New", icon: "📘", body_html: "<p>x</p>" });
    expect(s.pending).toBe(false);
    expect(s.isNewer({ version: 4 })).toBe(false);
  });

  it("keeps unsent typing across navigation and flushes it on unload", async () => {
    const a = saverFor(page());
    a.edit({ body_html: "<p>unsent</p>" });
    // The page is opened again before the pause ends: same saver, typing intact.
    expect(saverFor(page())).toBe(a);
    expect(flushAll({ keepalive: true })).toBe(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(vi.mocked(api.wiki.update).mock.calls[0][2]).toEqual({ keepalive: true });
    expect(flushAll()).toBe(false);
    // Once saved, opening it again takes the page as loaded, in the same saver (asking
    // twice, as React's strict mode does, never leaves two savers for one page).
    await vi.advanceTimersByTimeAsync(0);
    const again = saverFor(page({ version: 5, body_html: "<p>newer</p>" }));
    expect(again).toBe(a);
    expect(again.version).toBe(5);
    expect(again.draft.body_html).toBe("<p>newer</p>");
  });

  it("retries after a failed save", async () => {
    vi.mocked(api.wiki.update).mockRejectedValueOnce(new ApiError({ status: 0, title: "Network error" }));
    const s = new PageSaver(page());
    s.edit({ title: "Offline edit" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(s.status).toBe("error");
    await vi.advanceTimersByTimeAsync(4100);
    expect(s.status).toBe("saved");
    expect(server.title).toBe("Offline edit");
  });

  it.each([
    [403, "forbidden"],
    [404, "deleted"],
  ] as const)("stops for good at a %i, keeping the typing to copy, and never retries", async (status, want) => {
    vi.mocked(api.wiki.update).mockImplementation(async (_id, b) => {
      sent.push(b);
      throw new ApiError({ status, code: status === 403 ? "forbidden" : "not_found", title: "No" });
    });
    const s = saverFor(page());
    s.edit({ body_html: "<p>typed</p>" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(s.status).toBe(want);
    expect(s.final).toBe(true);
    s.edit({ body_html: "<p>typed more</p>" });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(sent).toHaveLength(1);
    expect(s.status).toBe(want);
    expect(s.draft.body_html).toBe("<p>typed more</p>");
    // Closing the tab still warns (the text would be lost) but sends nothing.
    expect(flushAll({ keepalive: true })).toBe(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(sent).toHaveLength(1);
    // Opening the page again keeps it, so the text can still be copied.
    expect(saverFor(page())).toBe(s);
  });

  it("warns before the tab closes on any page of the app while typing is unsent", async () => {
    vi.mocked(api.wiki.update).mockRejectedValue(new ApiError({ status: 0, title: "Network error" }));
    const s = saverFor(page());
    s.edit({ title: "Offline" });
    await vi.advanceTimersByTimeAsync(SAVE_DELAY);
    expect(s.status).toBe("error");
    const e = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(e);
    expect(e.defaultPrevented).toBe(true);
    resetSavers();
    const idle = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(idle);
    expect(idle.defaultPrevented).toBe(false);
  });
});
