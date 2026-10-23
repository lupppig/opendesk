// Each alias as its own identity (contracts/teams.md "Aliases"): its From name, signature and
// brand, and whether replies go out From it, from Gmail's send-as list where the connection
// may read it, else an admin's word.

import type { AliasSendAs, AliasSettings, Mailbox, ProviderSendAs } from "@/lib/api/types";
import type { WsData } from "./db";
import { HttpError, invalid } from "./router";

const SEND_AS: AliasSendAs[] = ["verified", "unverified", "unknown"];

/** The provider whose sign-in the mailbox holds, if any. */
function signedIn(m: Mailbox): "google" | "microsoft" | null {
  if (m.inbound_kind === "google" || m.inbound_kind === "microsoft") return m.inbound_kind;
  if (m.outbound_kind === "google" || m.outbound_kind === "microsoft") return m.outbound_kind;
  return null;
}

/** A Google connection that may read Gmail's send-as list: a sync or api grant, not send-only. */
export function readsSendAs(m: Mailbox): boolean {
  return signedIn(m) === "google" && m.inbound_kind === "google" && m.config.oauth_grant !== "send";
}

/** Gmail's send-as list as the mock's Google account has it, by mailbox. */
const gmailLists = new Map<string, ProviderSendAs["addresses"]>();
export function setGmailSendAs(m: Mailbox, list: ProviderSendAs["addresses"]) {
  gmailLists.set(m.id, list);
}

/** Every alias has an entry; removed aliases lose theirs; Gmail's list decides where it is read. */
export function normalizeAliases(m: Mailbox) {
  const cur = m.alias_settings ?? {};
  const next: Record<string, AliasSettings> = {};
  for (const a of m.aliases) next[a] = cur[a] ?? { display_name: "", signature_html: "", brand_id: null, send_as: "unknown", send_as_by: "" };
  m.alias_settings = next;
  if (signedIn(m) !== "google") {
    m.provider_send_as = null;
    return;
  }
  const ps: ProviderSendAs = m.provider_send_as ?? { readable: readsSendAs(m), checked_at: null, addresses: [] };
  ps.readable = readsSendAs(m);
  m.provider_send_as = ps;
  if (!ps.readable || !ps.checked_at) return;
  for (const a of m.aliases) {
    const listed = ps.addresses.find((x) => x.email === a);
    next[a] = { ...next[a], send_as: listed?.verified ? "verified" : "unverified", send_as_by: "provider" };
  }
}

/** Reads the (mock) Gmail list now, as POST /v1/mailboxes/{id}/send-as/check does. */
export function checkSendAs(m: Mailbox, now: string) {
  if (!readsSendAs(m)) throw new HttpError(422, "send_as_unreadable", "Google does not share the send-as list with this connection. Add the addresses as aliases by hand.");
  m.provider_send_as = { readable: true, checked_at: now, addresses: (gmailLists.get(m.id) ?? []).filter((x) => x.email !== m.email) };
  normalizeAliases(m);
}

/** PATCH alias_settings: replaces names and brands; a signature where given; send_as only where it changes. */
export function writeAliasSettings(ws: WsData, m: Mailbox, raw: unknown) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw invalid("alias_settings must map aliases to their settings");
  const cur = m.alias_settings ?? {};
  const next: Record<string, AliasSettings> = {};
  for (const a of m.aliases) next[a] = { display_name: "", signature_html: "", brand_id: null, send_as: cur[a]?.send_as ?? "unknown", send_as_by: cur[a]?.send_as_by ?? "" };
  for (const [k, v] of Object.entries(raw as Record<string, Record<string, unknown>>)) {
    const a = k.trim().toLowerCase();
    if (!m.aliases.includes(a)) throw invalid(`alias_settings: ${a} is not an alias of this mailbox`);
    const name = typeof v.display_name === "string" ? v.display_name.trim() : "";
    if (name.length > 64) throw invalid("Keep an alias's name under 64 characters");
    let brand = typeof v.brand_id === "string" && v.brand_id ? v.brand_id : null;
    if (brand && !ws.brands.some((b) => b.id === brand)) throw invalid("alias_settings names a brand that does not exist in this workspace");
    if (brand === m.brand_id) brand = null;
    const st: AliasSettings = { ...next[a], display_name: name, signature_html: typeof v.signature_html === "string" ? v.signature_html : (cur[a]?.signature_html ?? ""), brand_id: brand };
    if (typeof v.send_as === "string" && v.send_as !== st.send_as) {
      if (!SEND_AS.includes(v.send_as as AliasSendAs)) throw invalid('send_as must be "verified", "unverified" or "unknown"');
      if (readsSendAs(m)) throw invalid("Gmail's own send-as list decides which aliases this mailbox sends as. Add the address in Gmail, then check again.");
      st.send_as = v.send_as as AliasSendAs;
      st.send_as_by = st.send_as === "unknown" ? "" : "admin";
    }
    next[a] = st;
  }
  m.alias_settings = next;
}
