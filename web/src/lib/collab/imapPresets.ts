// Server settings for common mail hosts, picked from the address's domain, so an
// IMAP account usually needs only the address and an app password.

import type { MailTls } from "../api/types";

export type ImapPreset = {
  id: string;
  name: string;
  domains: string[];
  imap_host: string;
  imap_port: number;
  imap_tls: MailTls;
  smtp_host: string;
  smtp_port: number;
  smtp_tls: MailTls;
  /** The host signs in with an app password, not the account's own. */
  appPassword: boolean;
};

export const IMAP_PRESETS: readonly ImapPreset[] = [
  { id: "gmail", name: "Gmail", domains: ["gmail.com", "googlemail.com"], imap_host: "imap.gmail.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp.gmail.com", smtp_port: 465, smtp_tls: "tls", appPassword: true },
  { id: "outlook", name: "Outlook.com", domains: ["outlook.com", "hotmail.com", "live.com", "msn.com"], imap_host: "outlook.office365.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp-mail.outlook.com", smtp_port: 587, smtp_tls: "starttls", appPassword: false },
  { id: "icloud", name: "iCloud Mail", domains: ["icloud.com", "me.com", "mac.com"], imap_host: "imap.mail.me.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp.mail.me.com", smtp_port: 587, smtp_tls: "starttls", appPassword: true },
  { id: "yahoo", name: "Yahoo Mail", domains: ["yahoo.com", "ymail.com", "rocketmail.com"], imap_host: "imap.mail.yahoo.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp.mail.yahoo.com", smtp_port: 465, smtp_tls: "tls", appPassword: true },
  { id: "aol", name: "AOL Mail", domains: ["aol.com"], imap_host: "imap.aol.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp.aol.com", smtp_port: 465, smtp_tls: "tls", appPassword: true },
  { id: "fastmail", name: "Fastmail", domains: ["fastmail.com", "fastmail.fm"], imap_host: "imap.fastmail.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp.fastmail.com", smtp_port: 465, smtp_tls: "tls", appPassword: true },
  { id: "zoho", name: "Zoho Mail", domains: ["zoho.com", "zohomail.com"], imap_host: "imap.zoho.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp.zoho.com", smtp_port: 465, smtp_tls: "tls", appPassword: true },
  { id: "gmx", name: "GMX", domains: ["gmx.com", "gmx.net", "gmx.de"], imap_host: "imap.gmx.com", imap_port: 993, imap_tls: "tls", smtp_host: "mail.gmx.com", smtp_port: 587, smtp_tls: "starttls", appPassword: false },
  { id: "yandex", name: "Yandex Mail", domains: ["yandex.com", "yandex.ru"], imap_host: "imap.yandex.com", imap_port: 993, imap_tls: "tls", smtp_host: "smtp.yandex.com", smtp_port: 465, smtp_tls: "tls", appPassword: true },
];

/** The preset for an address, by its domain; null for any other host. */
export function presetForEmail(email: string): ImapPreset | null {
  const domain = email.trim().toLowerCase().split("@")[1];
  if (!domain) return null;
  return IMAP_PRESETS.find((p) => p.domains.includes(domain)) ?? null;
}

/** A guess for a host nobody listed: imap.<domain> and smtp.<domain> over TLS. */
export function guessServers(email: string): Pick<ImapPreset, "imap_host" | "imap_port" | "imap_tls" | "smtp_host" | "smtp_port" | "smtp_tls"> | null {
  const domain = email.trim().toLowerCase().split("@")[1];
  if (!domain || !domain.includes(".")) return null;
  return { imap_host: `imap.${domain}`, imap_port: 993, imap_tls: "tls", smtp_host: `smtp.${domain}`, smtp_port: 465, smtp_tls: "tls" };
}

/** The usual port for a security mode, to follow a switch between TLS and STARTTLS. */
export function defaultPort(kind: "imap" | "smtp", tls: MailTls): number {
  if (kind === "imap") return tls === "tls" ? 993 : 143;
  return tls === "tls" ? 465 : 587;
}
