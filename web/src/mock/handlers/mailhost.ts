// Mock of connecting a mailbox: the connect options, provider detection for a small
// table of domains, and the cause-and-fix answers a refused sign-in gets.
//
// Sign-in can be hidden per provider for demos with
// localStorage["od.mock.mailboxOAuth"] = "google" | "microsoft" | "none" | "google,microsoft".
//
// Receiving over IMAP follows the app's runtime config (VITE_MAILBOX_IMAP, on by
// default), and localStorage["od.mock.mailboxImap"] = "off" | "on" overrides it for a
// demo of an instance that receives by forwarding only (reload after setting it).
//
// Sending through opendesk is on; localStorage["od.mock.systemSend"] = "off" turns it off,
// as on opendesk Cloud, where every mailbox sends through Google, Microsoft or SMTP.
//
// Reading a Google or Microsoft inbox directly (a workspace with Collab) is off;
// localStorage["od.mock.apiSync"] = "on" offers it.

import type { Mailbox, MailDetection, MailEndpoint, MailHost, MailboxCause, MailboxTestLeg, PasswordAuth } from "@/lib/api/types";
import { config } from "@/lib/config";
import { MAIL_HOST_LABELS } from "@/lib/mailHost";
import { HttpError } from "../router";
import catalogue from "../mailCauses.json";

const OAUTH_KEY = "od.mock.mailboxOAuth";
const IMAP_KEY = "od.mock.mailboxImap";
const SYSTEM_SEND_KEY = "od.mock.systemSend";
const API_SYNC_KEY = "od.mock.apiSync";

/** Whether the workspace may read Google and Microsoft inboxes directly (it has Collab). */
export function mockApiSyncOn(): boolean {
  try {
    return localStorage.getItem(API_SYNC_KEY) === "on";
  } catch {
    return false;
  }
}

/** Applies the localStorage override of IMAP receiving to the app's config, before anything renders. */
export function applyMockImap(): void {
  let raw: string | null;
  try {
    raw = localStorage.getItem(IMAP_KEY);
  } catch {
    raw = null;
  }
  if (raw === "off") config.mailboxImap = false;
  if (raw === "on") config.mailboxImap = true;
}

/** Whether this instance receives over IMAP: the same switch the app reads. */
export function mockImapOn(): boolean {
  return config.mailboxImap;
}

export const IMAP_OFF_DETAIL = "Receiving over IMAP is off on this instance. Forward the address to opendesk instead.";
export const imapOff = () => new HttpError(422, "imap_off", IMAP_OFF_DETAIL);

/** The read-only receiving state the API reports. */
export function receivingOf(m: Pick<Mailbox, "inbound_kind"> & { config?: Mailbox["config"] }): NonNullable<Mailbox["receiving"]> {
  return m.inbound_kind === "forwarding" || m.config?.oauth_grant === "api" || mockImapOn() ? "ok" : "imap_off";
}

/** Whether mailboxes may send through opendesk. */
export function mockSystemSendOn(): boolean {
  try {
    return localStorage.getItem(SYSTEM_SEND_KEY) !== "off";
  } catch {
    return true;
  }
}

export const systemSendOff = () =>
  new HttpError(422, "system_send_off", "Sending through opendesk is off on this instance. Connect Google or Microsoft, or add SMTP.");
export const cannotSend = () =>
  new HttpError(422, "mailbox_cannot_send", "This mailbox has no way to send email yet. Connect Google or Microsoft, or add SMTP, in its settings.");

/** The read-only sending state the API reports. */
export function sendingOf(m: Pick<Mailbox, "outbound_kind">): NonNullable<Mailbox["sending"]> {
  return m.outbound_kind !== "system" || mockSystemSendOn() ? "ok" : "system_off";
}

/** Brings every mailbox's receiving and sending state up to date (the switches can change under them in tests). */
export function syncReceiving(mailboxes: Mailbox[]): void {
  for (const m of mailboxes) {
    m.receiving = receivingOf(m);
    m.sending = sendingOf(m);
  }
}

export function mockOAuthEnabled(p: "google" | "microsoft"): boolean {
  let raw: string | null;
  try {
    raw = localStorage.getItem(OAUTH_KEY);
  } catch {
    raw = null;
  }
  if (raw === null) return true;
  return raw
    .split(",")
    .map((x) => x.trim())
    .includes(p);
}

export function connectOptions(forwardingDomain: string) {
  return {
    google: { configured: true, oauth: mockOAuthEnabled("google"), api_sync: mockApiSyncOn() },
    microsoft: { configured: true, oauth: mockOAuthEnabled("microsoft"), api_sync: mockApiSyncOn() },
    forwarding: { enabled: true, domain: forwardingDomain },
    system_sender: "noreply@opendesk.sh",
    system_send: mockSystemSendOn(),
    mailbox_imap: mockImapOn(),
    instance_imap: mockImapOn(),
  };
}

const ep = (host: string, port: number, security: MailEndpoint["security"] = port === 587 ? "starttls" : "tls"): MailEndpoint => ({ host, port, security });

type Known = { host: MailHost; imap: string; smtp: string; smtpPort?: number; url?: string };
const KNOWN: Record<string, Known> = {
  "gmail.com": { host: "gmail", imap: "imap.gmail.com", smtp: "smtp.gmail.com", url: "https://myaccount.google.com/apppasswords" },
  "googlemail.com": { host: "gmail", imap: "imap.gmail.com", smtp: "smtp.gmail.com", url: "https://myaccount.google.com/apppasswords" },
  "outlook.com": { host: "outlook", imap: "outlook.office365.com", smtp: "smtp-mail.outlook.com", smtpPort: 587 },
  "hotmail.com": { host: "outlook", imap: "outlook.office365.com", smtp: "smtp-mail.outlook.com", smtpPort: 587 },
  "yahoo.com": { host: "yahoo", imap: "imap.mail.yahoo.com", smtp: "smtp.mail.yahoo.com", url: "https://login.yahoo.com/account/security" },
  "aol.com": { host: "aol", imap: "imap.aol.com", smtp: "smtp.aol.com", url: "https://login.aol.com/account/security" },
  "icloud.com": { host: "icloud", imap: "imap.mail.me.com", smtp: "smtp.mail.me.com", smtpPort: 587, url: "https://account.apple.com/account/manage" },
  "me.com": { host: "icloud", imap: "imap.mail.me.com", smtp: "smtp.mail.me.com", smtpPort: 587, url: "https://account.apple.com/account/manage" },
  "fastmail.com": { host: "fastmail", imap: "imap.fastmail.com", smtp: "smtp.fastmail.com", url: "https://app.fastmail.com/settings/security/apps" },
  "zohomail.com": { host: "zoho", imap: "imap.zoho.com", smtp: "smtp.zoho.com", url: "https://accounts.zoho.com/home#security/app_password" },
  "yandex.com": { host: "yandex", imap: "imap.yandex.com", smtp: "smtp.yandex.com", url: "https://id.yandex.com/security/app-passwords" },
  "gmx.com": { host: "gmx", imap: "imap.gmx.com", smtp: "mail.gmx.com", smtpPort: 587 },
  "proton.me": { host: "proton", imap: "", smtp: "" },
  "protonmail.com": { host: "proton", imap: "", smtp: "" },
};

// Custom domains, as MX and autoconfig would find them.
const MX: Record<string, { host: MailHost; source: MailDetection["source"] }> = {
  "acme.io": { host: "google_workspace", source: "mx" },
  "margin.app": { host: "google_workspace", source: "mx" },
  "contoso.com": { host: "microsoft365", source: "mx" },
  "northwind.dev": { host: "zoho", source: "mx" },
};
const WORKSPACE = KNOWN["gmail.com"];
const M365: Known = { host: "microsoft365", imap: "outlook.office365.com", smtp: "smtp.office365.com", smtpPort: 587 };
const ZOHO = KNOWN["zohomail.com"];

function passwordAuth(h: MailHost | ""): PasswordAuth {
  switch (h) {
    case "gmail":
    case "google_workspace":
    case "yahoo":
    case "aol":
    case "icloud":
    case "fastmail":
    case "yandex":
      return "app_password";
    case "microsoft365":
    case "outlook":
      return "oauth_only";
    case "proton":
      return "unsupported";
  }
  return "password";
}

function oauthFor(h: MailHost | ""): MailDetection["oauth"] {
  if (h === "gmail" || h === "google_workspace") return "google";
  if (h === "microsoft365" || h === "outlook") return "microsoft";
  return null;
}

export function detect(email: string): MailDetection {
  const domain = email.split("@")[1]?.toLowerCase() ?? "";
  const out = (host: MailHost | "", source: MailDetection["source"], k: Known | null): MailDetection => ({
    domain,
    host,
    label: host ? MAIL_HOST_LABELS[host] : "",
    source,
    settings: k && k.imap ? { imap: ep(k.imap, 993), smtp: ep(k.smtp, k.smtpPort ?? 465) } : null,
    password_auth: passwordAuth(host),
    app_password_url: k?.url ?? "",
    oauth: oauthFor(host),
  });
  const known = KNOWN[domain];
  if (known) return out(known.host, "known", known);
  const mx = MX[domain];
  if (mx) return out(mx.host, mx.source, mx.host === "google_workspace" ? WORKSPACE : mx.host === "zoho" ? ZOHO : M365);
  // Any other domain whose name hints at its own server: found through autoconfig.
  if (/^(mail|hosting|example)\./.test(domain) || domain.endsWith(".example")) return out("other", "autoconfig", { host: "other", imap: `imap.${domain}`, smtp: `smtp.${domain}`, smtpPort: 587 });
  return out("", "none", null);
}

// The API's cause catalogue (internal/email/mailcause), written by its Go test: the
// mock answers with the same codes, titles and fixes.
const CATALOGUE: readonly MailboxCause[] = catalogue;

/** The catalogue entry for a code; an unknown code is a mistake in the mock. */
export function mailCause(code: string): MailboxCause {
  const c = CATALOGUE.find((x) => x.code === code);
  if (!c) throw new Error(`mock: no mail cause ${code}`);
  return c;
}

const refusedLeg = (detail: string): MailboxTestLeg => ({ ok: false, error: "The server refused the sign-in.", detail });
const okLeg: MailboxTestLeg = { ok: true, error: "" };

/**
 * A refused check, or null when the mock lets it through. The password "wrong" is
 * refused anywhere; Google wants a 16-letter app password, Yahoo and AOL a 16-character
 * one. smtpOnly: only the SMTP server is signed in to (forwarding in, SMTP out).
 */
export function refusal(host: string, password: string, smtpOnly: boolean): { inbound: MailboxTestLeg; outbound: MailboxTestLeg; cause: MailboxCause } | null {
  const google = host === "gmail" || host === "google_workspace";
  const microsoft = host === "microsoft365" || host === "outlook";
  const wrong = password === "wrong";
  const notAppPassword = google && !/^[a-z]{16}$/i.test(password);
  const yahooPlain = (host === "yahoo" || host === "aol") && password.length !== 16;
  if (!wrong && !notAppPassword && !yahooPlain) return null;
  if (notAppPassword)
    return {
      inbound: smtpOnly ? okLeg : refusedLeg("[AUTHENTICATIONFAILED] Application-specific password required: https://support.google.com/accounts/answer/185833"),
      outbound: refusedLeg("534 5.7.9 Application-specific password required."),
      cause: mailCause("google_app_password_required"),
    };
  if (yahooPlain)
    return {
      inbound: smtpOnly ? okLeg : refusedLeg("[AUTHENTICATIONFAILED] Invalid credentials (Failure)"),
      outbound: refusedLeg("535 5.7.0 (#AUTH005) Too many bad auth attempts."),
      cause: mailCause(host === "aol" ? "aol_app_password_required" : "yahoo_app_password_required"),
    };
  if (microsoft)
    return smtpOnly
      ? {
          inbound: okLeg,
          outbound: refusedLeg("535 5.7.139 Authentication unsuccessful, SmtpClientAuthentication is disabled for the Mailbox."),
          cause: mailCause("microsoft_smtp_auth_disabled"),
        }
      : {
          inbound: refusedLeg("AUTHENTICATE failed."),
          outbound: refusedLeg("535 5.7.139 Authentication unsuccessful, basic authentication is disabled."),
          cause: mailCause("microsoft_basic_auth_disabled"),
        };
  return {
    inbound: smtpOnly ? okLeg : refusedLeg("[AUTHENTICATIONFAILED] Invalid credentials (Failure)"),
    outbound: refusedLeg("535 5.7.8 Username and Password not accepted."),
    cause: mailCause("auth_refused"),
  };
}

export const authRefusedCause = mailCause("auth_refused");
