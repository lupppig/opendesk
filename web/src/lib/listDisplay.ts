import { useSyncExternalStore } from "react";

// How the ticket list shows who a ticket is assigned to, per browser. Teammates on a
// ticket right now always show live (green ring and a label), whatever this says.

/** photo: the assignee's avatar (the default). name: their first name in a pill. none: nothing. */
export type AssigneeDisplay = "photo" | "name" | "none";

const KEY = "od-list-assignee";
const listeners = new Set<() => void>();

function read(): AssigneeDisplay {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "photo" || v === "name" || v === "none") return v;
  } catch {
    /* storage unavailable */
  }
  return "photo";
}

let current: AssigneeDisplay = read();

export function setAssigneeDisplay(v: AssigneeDisplay) {
  current = v;
  try {
    localStorage.setItem(KEY, v);
  } catch {
    /* storage unavailable: kept for this page load */
  }
  listeners.forEach((l) => l());
}

if (typeof window !== "undefined") {
  window.addEventListener("storage", (e) => {
    if (e.key !== KEY) return;
    current = read();
    listeners.forEach((l) => l());
  });
}

// Stable across renders, so each row subscribes once.
function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

function snapshot() {
  return current;
}

/** The list's assignee display, kept in sync across tabs. */
export function useAssigneeDisplay(): AssigneeDisplay {
  return useSyncExternalStore(subscribe, snapshot);
}
