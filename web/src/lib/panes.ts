import { useCallback, useSyncExternalStore } from "react";
import { useSession } from "./session";

// Pane widths a person dragged (the sidebar, the ticket list, the ticket's details), remembered
// per person in this browser. No width stored means the layout's own default, so a reset is
// the layout as it ships. A stored width still gives way to the window: each pane takes at most
// a share of the room it sits in, and grows back once the window is wide again.

export type Pane = "nav" | "list" | "details";

export type PaneLimits = {
  min: number;
  max: number;
  /** The largest part of the room the pane sits in that it may take, 0..1. */
  share: number;
};

// The room: the window for the sidebar, the inbox for the list, the open ticket for its details.
export const paneLimits: Record<Pane, PaneLimits> = {
  nav: { min: 200, max: 360, share: 0.3 },
  list: { min: 280, max: 640, share: 0.5 },
  details: { min: 260, max: 560, share: 0.5 },
};

/** One arrow press moves the edge this far; with shift, `BIG_STEP`. */
export const STEP = 16;
export const BIG_STEP = 64;

export const paneKey = (pane: Pane, uid: string) => `od-pane:${pane}:${uid}`;

/** The widest the pane may be with `room` pixels around it (the window or its row). */
export function maxWidth(pane: Pane, room?: number): number {
  const { min, max, share } = paneLimits[pane];
  if (room === undefined || !Number.isFinite(room) || room <= 0) return max;
  return Math.max(min, Math.min(max, Math.floor(room * share)));
}

export function clampWidth(pane: Pane, px: number, room?: number): number {
  return Math.min(maxWidth(pane, room), Math.max(paneLimits[pane].min, Math.round(px)));
}

/** The width as CSS, held within the pane's share of the room: `vw` for the window, `%` for its parent. */
export function paneCss(pane: Pane, px: number, room: "vw" | "%"): string {
  const { min, share } = paneLimits[pane];
  return `clamp(${min}px, ${px}px, ${share * 100}${room})`;
}

/** Writes the width a drag shows (`--pane-<pane>-live`, over the saved one), or lifts it with null. */
export function setLiveWidth(el: HTMLElement | null | undefined, pane: Pane, css: string | null) {
  if (css === null) el?.style.removeProperty(`--pane-${pane}-live`);
  else el?.style.setProperty(`--pane-${pane}-live`, css);
}

/**
 * The width a key press gives, or null for a key the handle leaves alone. `grow` is 1 when
 * the pane sits left of its handle on screen (ArrowRight widens it), -1 when it sits right.
 */
export function keyWidth(key: string, shift: boolean, current: number, pane: Pane, room: number | undefined, grow: 1 | -1): number | null {
  const step = shift ? BIG_STEP : STEP;
  switch (key) {
    case "ArrowRight":
      return clampWidth(pane, current + step * grow, room);
    case "ArrowLeft":
      return clampWidth(pane, current - step * grow, room);
    case "Home":
      return paneLimits[pane].min;
    case "End":
      return maxWidth(pane, room);
    default:
      return null;
  }
}

// ─── Storage: read once per person and pane, shared by every reader ────────

const cache = new Map<string, number | null>();
const listeners = new Set<() => void>();

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

function readStored(pane: Pane, uid: string): number | null {
  try {
    const raw = localStorage.getItem(paneKey(pane, uid));
    const px = raw === null ? NaN : Number(raw);
    return Number.isFinite(px) && px > 0 ? clampWidth(pane, px) : null;
  } catch {
    return null;
  }
}

/** The width this person gave the pane, or null for the layout's default. */
export function paneWidth(pane: Pane, uid: string): number | null {
  const k = paneKey(pane, uid);
  if (!cache.has(k)) cache.set(k, uid ? readStored(pane, uid) : null);
  return cache.get(k) ?? null;
}

/** Remembers a width for the pane (null goes back to the default). */
export function setPaneWidth(pane: Pane, uid: string, px: number | null) {
  const value = px === null ? null : clampWidth(pane, px);
  cache.set(paneKey(pane, uid), value);
  if (uid) {
    try {
      if (value === null) localStorage.removeItem(paneKey(pane, uid));
      else localStorage.setItem(paneKey(pane, uid), String(value));
    } catch {
      /* storage unavailable */
    }
  }
  listeners.forEach((l) => l());
}

/** Forgets what was read, so the next read comes from storage again (tests). */
export function resetPaneCache() {
  cache.clear();
}

/** The signed-in person's width for a pane, and a setter that remembers it. */
export function usePaneWidth(pane: Pane): [number | null, (px: number | null) => void] {
  const uid = useSession((s) => s.user?.id ?? "");
  const width = useSyncExternalStore(subscribe, () => paneWidth(pane, uid));
  const set = useCallback((px: number | null) => setPaneWidth(pane, uid, px), [pane, uid]);
  return [width, set];
}
