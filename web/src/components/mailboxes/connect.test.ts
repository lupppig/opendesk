import { afterEach, describe, expect, it } from "vitest";
import type { MailboxConnectOptions } from "@/lib/api/types";
import { config } from "@/lib/config";
import { apiOn, imapOn, syncOn } from "./connect";

const opts = (o: Partial<MailboxConnectOptions> & { api?: boolean } = {}): MailboxConnectOptions => ({
  google: { configured: true, oauth: true, api_sync: o.api ?? false },
  microsoft: { configured: true, oauth: true, api_sync: false },
  forwarding: { enabled: true, domain: "in.opendesk.sh" },
  system_sender: "noreply@opendesk.sh",
  ...o,
});

describe("what a workspace can receive with", () => {
  afterEach(() => {
    config.mailboxImap = true;
  });

  it("follows the instance without Collab", () => {
    config.mailboxImap = true;
    expect([imapOn(opts({ mailbox_imap: true, instance_imap: true })), syncOn(opts({ mailbox_imap: true, instance_imap: true }))]).toEqual([true, true]);
    config.mailboxImap = false;
    expect([imapOn(opts({ mailbox_imap: false, instance_imap: false })), syncOn(opts({ mailbox_imap: false, instance_imap: false }))]).toEqual([false, false]);
    expect(apiOn(opts(), "google")).toBe(false);
  });

  it("offers IMAP and reading directly to a workspace with Collab on an instance with IMAP off", () => {
    config.mailboxImap = false;
    const o = opts({ mailbox_imap: true, instance_imap: false, api: true });
    expect(imapOn(o)).toBe(true);
    expect(syncOn(o)).toBe(false);
    expect(apiOn(o, "google")).toBe(true);
    expect(apiOn(o, "microsoft")).toBe(false);
  });

  it("keeps the old answer for an API without the new fields", () => {
    config.mailboxImap = true;
    expect([imapOn(opts({ mailbox_imap: true })), syncOn(opts({ mailbox_imap: true }))]).toEqual([true, true]);
    expect(imapOn(undefined)).toBe(true);
  });
});
