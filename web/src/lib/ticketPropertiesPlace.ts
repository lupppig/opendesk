// Where an open ticket's properties sit, per browser like the theme (Settings → Profile →
// Appearance): in the details column, the default, or in a bar above the conversation, the
// details column then starting with the customer (tickets/detail/TicketProperties.tsx).

import { useSyncExternalStore } from "react";

export type TicketPropertiesPlace = "column" | "bar";

const KEY = "od-ticket-properties";
const listeners = new Set<() => void>();

function read(): TicketPropertiesPlace {
  try {
    return localStorage.getItem(KEY) === "bar" ? "bar" : "column";
  } catch {
    return "column";
  }
}

let place = read();

export function setTicketPropertiesPlace(next: TicketPropertiesPlace) {
  place = next;
  try {
    if (next === "bar") localStorage.setItem(KEY, "bar");
    else localStorage.removeItem(KEY);
  } catch {
    /* storage unavailable */
  }
  listeners.forEach((l) => l());
}

export function ticketPropertiesPlace(): TicketPropertiesPlace {
  return place;
}

if (typeof window !== "undefined") {
  // Another tab changed it.
  window.addEventListener("storage", (e) => {
    if (e.key !== KEY && e.key !== null) return;
    place = read();
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
  return place;
}

export function useTicketPropertiesPlace(): TicketPropertiesPlace {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
