// Which cards of a URL panel the agent folded or opened (contracts/integrations.md "The http
// answer", `collapsed`). A card starts the way the panel asks. Once the agent toggles it, their
// choice holds for that ticket and panel while the app runs: through the panel's refresh and its
// re-fetches, a panel folded and opened again, a visit to another ticket and back. A card is known
// by its `id`, or by its title without one.
//
// Kept in memory only, never stored: after a reload the panel's own choice applies again, and it
// knows better by then which order is the one to read (the newest, the late one).

import { useCallback, useState, useSyncExternalStore } from "react";

/** How many ticket panels keep their agent's choices; the one touched longest ago goes first. */
export const MAX_FOLD_SCOPES = 200;

const scopes = new Map<string, Map<string, boolean>>();
const listeners = new Set<() => void>();

/** The choices of one panel on one ticket. */
export function foldScope(ticketId: string, panelId: string): string {
  return `${ticketId}\u0000${panelId}`;
}

/** What the agent chose for a card, or undefined while they have not toggled it. */
export function chosenOpen(scope: string, card: string): boolean | undefined {
  return scopes.get(scope)?.get(card);
}

export function chooseOpen(scope: string, card: string, open: boolean) {
  const cards = scopes.get(scope) ?? new Map<string, boolean>();
  scopes.delete(scope); // re-inserted last: the most recently touched
  cards.set(card, open);
  scopes.set(scope, cards);
  for (const old of scopes.keys()) {
    if (scopes.size <= MAX_FOLD_SCOPES) break;
    scopes.delete(old);
  }
  listeners.forEach((l) => l());
}

export function __resetFoldsForTests() {
  scopes.clear();
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

/**
 * Whether a card shows open, and the toggle: what the panel asks (`requested`) until the agent
 * chooses. Without a scope (settings' preview), the choice lasts as long as the card is shown.
 */
export function useCardFold(scope: string | undefined, card: string, requested: boolean): [boolean, () => void] {
  const [local, setLocal] = useState<boolean>();
  const read = () => (scope === undefined ? undefined : chosenOpen(scope, card));
  const chosen = useSyncExternalStore(subscribe, read, read);
  const open = (scope === undefined ? local : chosen) ?? requested;
  const toggle = useCallback(() => {
    if (scope === undefined) setLocal(!open);
    else chooseOpen(scope, card, !open);
  }, [scope, card, open]);
  return [open, toggle];
}

/**
 * Each block's identity in an answer: a card's `id`, else its title, with a count when two
 * cards share one ("Order #1042", "Order #1042\u00002"); other blocks by their place.
 */
export function blockKeys(blocks: readonly { type: string; id?: string; title?: string }[]): string[] {
  const seen = new Map<string, number>();
  return blocks.map((b, i) => {
    if (b.type !== "card") return `#${i}`;
    const base = b.id ? `id:${b.id}` : `title:${b.title ?? ""}`;
    const n = (seen.get(base) ?? 0) + 1;
    seen.set(base, n);
    return n === 1 ? base : `${base}\u0000${n}`;
  });
}
