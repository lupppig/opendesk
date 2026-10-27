// The ticket timeline's layout, per browser like the theme (Settings → Profile → Appearance):
// off, every message takes the timeline's width; on, the customer's messages sit at the
// start and the team's at the end, like a chat (messageSide in tickets/detail/messageFrame.ts).

import { useSyncExternalStore } from "react";

const KEY = "od-conversation-layout";
const listeners = new Set<() => void>();

function read(): boolean {
  try {
    return localStorage.getItem(KEY) === "on";
  } catch {
    return false;
  }
}

let enabled = read();

export function setConversationLayout(on: boolean) {
  enabled = on;
  try {
    if (on) localStorage.setItem(KEY, "on");
    else localStorage.removeItem(KEY);
  } catch {
    /* storage unavailable */
  }
  listeners.forEach((l) => l());
}

export function conversationLayoutOn(): boolean {
  return enabled;
}

if (typeof window !== "undefined") {
  // Another tab changed it.
  window.addEventListener("storage", (e) => {
    if (e.key !== KEY && e.key !== null) return;
    enabled = read();
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
  return enabled;
}

export function useConversationLayout(): boolean {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
