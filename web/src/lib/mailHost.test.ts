import { describe, expect, it } from "vitest";
import { mailboxHost, offersNoTls, readsDirectly, receivesOverImap, sendOnly, signedInProvider } from "./mailHost";

describe("offersNoTls", () => {
  it("offers no encryption only for local and private hosts", () => {
    for (const h of ["localhost", "mailpit", "mail.local", "127.0.0.1", "10.0.0.5", "172.20.1.1", "192.168.1.10", "::1", "[fd00::1]"]) expect(offersNoTls(h), h).toBe(true);
    for (const h of ["", "smtp.office365.com", "imap.gmail.com", "fcm.example.com", "8.8.8.8", "172.32.0.1"]) expect(offersNoTls(h), h).toBe(false);
  });
});

describe("how a mailbox connects", () => {
  const base = { email: "support@acme.io", config: {} } as const;
  it("tells a send-only connection from a sync", () => {
    const sendOnlyGoogle = { ...base, inbound_kind: "forwarding", outbound_kind: "google" } as const;
    const syncGoogle = { ...base, inbound_kind: "google", outbound_kind: "google" } as const;
    const forwarding = { ...base, inbound_kind: "forwarding", outbound_kind: "system" } as const;
    const imap = { ...base, inbound_kind: "imap", outbound_kind: "smtp" } as const;
    expect([sendOnlyGoogle, syncGoogle, forwarding, imap].map(sendOnly)).toEqual([true, false, false, false]);
    expect([sendOnlyGoogle, syncGoogle, forwarding, imap].map(receivesOverImap)).toEqual([false, true, false, true]);
    expect([sendOnlyGoogle, syncGoogle, forwarding, imap].map(signedInProvider)).toEqual(["google", "google", null, null]);
    expect(mailboxHost(sendOnlyGoogle)).toBe("google_workspace");
    expect(mailboxHost({ ...base, inbound_kind: "forwarding", outbound_kind: "microsoft" })).toBe("microsoft365");
    expect(mailboxHost(forwarding)).toBeNull();
  });

  it("tells reading the inbox directly from syncing over IMAP", () => {
    const direct = { ...base, inbound_kind: "microsoft", outbound_kind: "microsoft", config: { oauth_grant: "api" } } as const;
    const sync = { ...base, inbound_kind: "google", outbound_kind: "google", config: { oauth_grant: "sync" } } as const;
    const sendOnlyApi = { ...base, inbound_kind: "forwarding", outbound_kind: "google", config: { oauth_grant: "api" } } as const;
    expect([direct, sync, sendOnlyApi, { ...base, inbound_kind: "google", outbound_kind: "google" } as const].map(readsDirectly)).toEqual([true, false, false, false]);
    expect(signedInProvider(direct)).toBe("microsoft");
  });
});
