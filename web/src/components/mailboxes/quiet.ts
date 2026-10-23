// When an address has gone quiet: no mail since it was added a day ago, or none for a week.
// Mail that stops arriving usually means forwarding was turned off or the connection broke,
// and nothing else would say so (contracts/api.md, Mailbox.last_received_at).

import type { Mailbox } from "@/lib/api/types";

export const QUIET_DAYS = 7;
const DAY = 24 * 60 * 60 * 1000;

export type Quiet = { never: true } | { never: false; days: number };

export function quietFor(m: Pick<Mailbox, "status" | "receiving" | "created_at" | "last_received_at">, now = Date.now()): Quiet | null {
  if (m.status !== "active" || m.receiving === "imap_off") return null;
  if (!m.last_received_at) return now - Date.parse(m.created_at) > DAY ? { never: true } : null;
  const days = Math.floor((now - Date.parse(m.last_received_at)) / DAY);
  return days >= QUIET_DAYS ? { never: false, days } : null;
}
