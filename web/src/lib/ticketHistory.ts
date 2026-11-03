// Whether an open ticket's timeline shows its history (status, assignments, tags, rules,
// merges…) between the messages, per browser like the theme (the ticket's ⋯ menu): shown,
// the default, or hidden, which keeps the conversation and its calls only.

import { useSyncExternalStore } from "react";
import type { Activity } from "@/lib/api/types";

const KEY = "od-ticket-history";
const listeners = new Set<() => void>();

function read(): boolean {
  try {
    return localStorage.getItem(KEY) !== "hidden";
  } catch {
    return true;
  }
}

let shown = read();

export function setTicketHistoryShown(next: boolean) {
  shown = next;
  try {
    if (next) localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, "hidden");
  } catch {
    /* storage unavailable */
  }
  listeners.forEach((l) => l());
}

export function ticketHistoryShown(): boolean {
  return shown;
}

/** What the timeline shows of a ticket's activities: all of them, or, history hidden, its calls (part of the conversation). */
export function visibleActivities(activities: Activity[] | undefined, show: boolean): Activity[] | undefined {
  if (show || !activities) return activities;
  return activities.filter((a) => a.type === "call");
}

if (typeof window !== "undefined") {
  // Another tab changed it.
  window.addEventListener("storage", (e) => {
    if (e.key !== KEY && e.key !== null) return;
    shown = read();
    listeners.forEach((l) => l());
  });
}

// Stable across renders, so a reader subscribes once.
function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

function snapshot() {
  return shown;
}

export function useTicketHistoryShown(): boolean {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
