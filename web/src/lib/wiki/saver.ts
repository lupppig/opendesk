// Autosave for wiki pages (contracts/wiki.md "Editing together"). One saver per open
// page holds what was typed and sends it a moment after typing stops, one request at a
// time, each with the version it edited. It lives outside React, so a save started as
// the person navigates away still finishes, and opening the page again picks it up; and
// while any saver holds unsent typing, closing the tab sends it and the browser asks first,
// wherever in the app the person is.

import { ApiError, inWorkspace, isApiError, scopeWorkspace } from "../api/client";
import { api } from "../api/endpoints";
import type { WikiNode, WikiPage, WikiPagePatch } from "../api/types";

export type Draft = { title: string; icon: string; body_html: string };

/**
 * saved: nothing left to send. dirty: typed, waiting for the pause. saving: a request is
 * out. error: the last one failed and is retried. conflict: someone else saved first.
 * forbidden: the person may not edit the page (403). deleted: the page is gone (404).
 * The last two are final: nothing is sent again, and the typing stays to be copied.
 */
export type SaveStatus = "saved" | "dirty" | "saving" | "error" | "conflict" | "forbidden" | "deleted";

/** Statuses where the saver stops and waits for the person (conflict) or for good. */
const halted = (s: SaveStatus) => s === "conflict" || s === "forbidden" || s === "deleted";

/** How long after the last keystroke a save goes out. */
export const SAVE_DELAY = 800;
const RETRY_DELAY = 4000;

export type SaverHooks = {
  /** A save answered: the page as stored. */
  onSaved?: (page: WikiPage) => void;
};

const fields = ["title", "icon", "body_html"] as const;

function changes(from: Draft, to: Draft): Partial<Draft> {
  const out: Partial<Draft> = {};
  for (const f of fields) if (from[f] !== to[f]) out[f] = to[f];
  return out;
}

export class PageSaver {
  readonly id: string;
  readonly workspace: string | null;
  version: number;
  /** What the server holds, as this saver last sent or loaded it. */
  base: Draft;
  /** What the person sees. */
  draft: Draft;
  status: SaveStatus = "saved";
  lastSavedAt: number | null = null;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private inflight: Promise<void> | null = null;
  /** Send every field on the next save (keeping what was typed over a newer version). */
  private force = false;
  private listeners = new Set<() => void>();
  hooks: SaverHooks;

  constructor(page: Pick<WikiPage, "id" | "version" | "title" | "icon" | "body_html">, hooks: SaverHooks = {}, workspace = scopeWorkspace()) {
    this.id = page.id;
    this.workspace = workspace;
    this.version = page.version;
    this.base = { title: page.title, icon: page.icon, body_html: page.body_html };
    this.draft = { ...this.base };
    this.hooks = hooks;
  }

  subscribe(fn: () => void) {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private emit() {
    for (const l of this.listeners) l();
  }

  /** There is typing the server has not stored yet. */
  get pending(): boolean {
    return this.force || Object.keys(changes(this.base, this.draft)).length > 0;
  }

  get busy(): boolean {
    return this.inflight !== null || this.pending;
  }

  /** Saving stopped for good (forbidden, deleted): what is pending can only be copied. */
  get final(): boolean {
    return this.status === "forbidden" || this.status === "deleted";
  }

  edit(patch: Partial<Draft>) {
    const next = { ...this.draft, ...patch };
    if (!Object.keys(changes(this.draft, next)).length) return;
    this.draft = next;
    if (!halted(this.status) && this.status !== "saving") this.status = this.pending ? "dirty" : "saved";
    this.schedule(SAVE_DELAY);
    this.emit();
  }

  private schedule(ms: number) {
    clearTimeout(this.timer);
    if (halted(this.status)) return;
    this.timer = setTimeout(() => void this.flush(), ms);
  }

  /** Sends what is pending now. `keepalive` lets it finish while the page unloads. */
  async flush(opts: { keepalive?: boolean } = {}): Promise<void> {
    clearTimeout(this.timer);
    if (halted(this.status)) return;
    if (this.inflight) {
      await this.inflight;
      return this.flush(opts);
    }
    const patch: WikiPagePatch = this.force ? { ...this.draft } : changes(this.base, this.draft);
    if (!Object.keys(patch).length) {
      if (this.status !== "saved") {
        this.status = "saved";
        this.emit();
      }
      return;
    }
    const sending = { ...this.draft };
    this.status = "saving";
    this.emit();
    this.inflight = (async () => {
      try {
        const page = await inWorkspace(this.workspace, () => api.wiki.update(this.id, { ...patch, version: this.version }, opts));
        this.version = page.version;
        this.base = sending;
        this.force = false;
        this.lastSavedAt = Date.now();
        this.status = this.pending ? "dirty" : "saved";
        this.hooks.onSaved?.(page);
      } catch (e) {
        this.status = isApiError(e, "page_changed") ? "conflict" : statusOf(e) === 403 ? "forbidden" : statusOf(e) === 404 ? "deleted" : "error";
      } finally {
        this.inflight = null;
      }
      if (this.status === "dirty") this.schedule(SAVE_DELAY);
      if (this.status === "error") this.schedule(RETRY_DELAY);
      this.emit();
    })();
    return this.inflight;
  }

  /** Someone else saved and nothing here is pending: take their page as it is. */
  rebase(page: Pick<WikiPage, "version" | "title" | "icon" | "body_html">) {
    clearTimeout(this.timer);
    this.version = page.version;
    this.force = false;
    this.base = { title: page.title, icon: page.icon, body_html: page.body_html };
    this.draft = { ...this.base };
    this.status = "saved";
    this.emit();
  }

  /** After a conflict: keep what was typed and save it over the newer version. */
  overwrite(latestVersion: number) {
    this.version = latestVersion;
    this.force = true;
    this.status = "dirty";
    void this.flush();
  }

  /** Whether a change the server announced is newer than what this saver knows. */
  isNewer(node: Pick<WikiNode, "version">): boolean {
    return node.version > this.version;
  }

  dispose() {
    clearTimeout(this.timer);
    this.listeners.clear();
  }
}

const statusOf = (e: unknown) => (e instanceof ApiError ? e.status : 0);

const savers = new Map<string, PageSaver>();

/**
 * The saver of a page, one per page: the one still holding unsent typing (or a conflict)
 * keeps it, so nothing typed is lost across navigation; an idle one takes page when page
 * is newer. Asking twice (React's strict mode does) gives the same saver.
 */
export function saverFor(page: WikiPage, hooks: SaverHooks = {}): PageSaver {
  const wid = scopeWorkspace();
  const k = `${wid}:${page.id}`;
  const cur = savers.get(k);
  if (cur) {
    if (!cur.busy && cur.status === "saved" && cur.isNewer(page)) cur.rebase(page);
    cur.hooks = hooks;
    return cur;
  }
  const s = new PageSaver(page, hooks, wid);
  savers.set(k, s);
  return s;
}

/** The open saver of a page in the workspace in scope, if any. */
export function openSaver(id: string): PageSaver | undefined {
  return savers.get(`${scopeWorkspace()}:${id}`);
}

export function forgetSaver(id: string) {
  const k = `${scopeWorkspace()}:${id}`;
  savers.get(k)?.dispose();
  savers.delete(k);
}

/**
 * Sends everything pending, as the page unloads. True while something is still unsent,
 * including typing a final status keeps (closing the tab would lose it).
 */
export function flushAll(opts: { keepalive?: boolean } = {}): boolean {
  let unsent = false;
  for (const s of savers.values()) {
    if (s.busy) {
      unsent = true;
      if (!s.final) void s.flush(opts);
    }
  }
  return unsent;
}

if (typeof window !== "undefined") {
  // Closing or reloading the tab, from any page of the app, sends what is unsent and asks first.
  window.addEventListener("beforeunload", (e) => {
    if (flushAll({ keepalive: true })) e.preventDefault();
  });
}

/** Test hook. */
export function resetSavers() {
  for (const s of savers.values()) s.dispose();
  savers.clear();
}
