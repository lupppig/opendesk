// How wide an open ticket's conversation is, per browser like the theme (Settings → Profile →
// Appearance): limited, the default, keeps the messages and the reply box to a readable
// measure centred in the pane; full lets them take the pane's whole width, for a wide screen
// (the conversation layout's cards then grow with it, keeping their offset).

import { useSyncExternalStore } from "react";

export type ConversationWidth = "limited" | "full";

const KEY = "od-conversation-width";
const listeners = new Set<() => void>();

function read(): ConversationWidth {
  try {
    return localStorage.getItem(KEY) === "full" ? "full" : "limited";
  } catch {
    return "limited";
  }
}

let width = read();

export function setConversationWidth(next: ConversationWidth) {
  width = next;
  try {
    if (next === "full") localStorage.setItem(KEY, "full");
    else localStorage.removeItem(KEY);
  } catch {
    /* storage unavailable */
  }
  listeners.forEach((l) => l());
}

export function conversationWidth(): ConversationWidth {
  return width;
}

/** The cap of the conversation's column (the timeline, the reply box): a readable measure, or none. */
export function conversationMaxWidth(w: ConversationWidth): string {
  return w === "full" ? "max-w-none" : "max-w-[820px]";
}

if (typeof window !== "undefined") {
  // Another tab changed it.
  window.addEventListener("storage", (e) => {
    if (e.key !== KEY && e.key !== null) return;
    width = read();
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
  return width;
}

export function useConversationWidth(): ConversationWidth {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
