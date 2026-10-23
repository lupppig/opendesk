import { describe, expect, it } from "vitest";
import type { MailIdentity, MailMessage } from "../api/types";
import { collabFromName, defaultIdentity, fromLabel, identitiesOf, identitySignature, ownEmails, replyIdentity, sendableIdentities } from "./identities";

const id = (p: Partial<MailIdentity>): MailIdentity => ({
  id: "x",
  email: "x@ours.com",
  display_name: "",
  signature_html: "",
  reply_to: "",
  primary: false,
  default: false,
  source: "provider",
  verification: "accepted",
  can_send: true,
  signature_sync: "two_way",
  display_name_sync: "two_way",
  signature_synced_at: null,
  ...p,
});

const account = {
  email: "me@ours.com",
  display_name: "Mia",
  signature_html: "<p>Mia</p>",
  default_from: "sales@ours.com",
  identities: [
    id({ id: null, email: "me@ours.com", display_name: "Mia", primary: true, source: "account" }),
    id({ id: "s", email: "sales@ours.com", display_name: "Ours Sales", default: true }),
    id({ id: "p", email: "new@ours.com", verification: "pending", can_send: false }),
  ],
};

const msg = (p: Partial<MailMessage>): MailMessage => ({
  id: "m1",
  thread_id: "t1",
  account_id: "a1",
  message_id: "x@acme.com",
  from: { name: "Ada", email: "ada@acme.com" },
  to: [{ name: "Mia", email: "me@ours.com" }],
  cc: [],
  bcc: [],
  reply_to: [],
  subject: "Hi",
  snippet: "",
  body_html: "",
  body_text: "",
  sent_at: "2026-10-01T10:00:00Z",
  unread: false,
  starred: false,
  draft: false,
  folder_ids: [],
  attachments: [],
  sent_by: null,
  ...p,
});

describe("identities", () => {
  it("lists the account's addresses, and only the confirmed ones can send", () => {
    expect(ownEmails(account)).toEqual(["me@ours.com", "sales@ours.com", "new@ours.com"]);
    expect(sendableIdentities(account).map((i) => i.email)).toEqual(["me@ours.com", "sales@ours.com"]);
    // An older shape without the list: its own address.
    expect(identitiesOf({ email: "Me@ours.com", display_name: "Mia", signature_html: "" }).map((i) => i.email)).toEqual(["me@ours.com"]);
  });

  it("starts new mail from the default", () => {
    expect(defaultIdentity(account).email).toBe("sales@ours.com");
    expect(defaultIdentity({ ...account, default_from: "new@ours.com" }).email).toBe("sales@ours.com");
  });

  it("replies from the address the message was written to", () => {
    expect(replyIdentity(account, msg({ own_address: "sales@ours.com" })).email).toBe("sales@ours.com");
    expect(replyIdentity(account, msg({ to: [{ name: "", email: "SALES@ours.com" }] })).email).toBe("sales@ours.com");
    expect(replyIdentity(account, msg({})).email).toBe("me@ours.com");
    // Written to an address that cannot send yet: the default.
    expect(replyIdentity(account, msg({ to: [{ name: "", email: "new@ours.com" }], own_address: "new@ours.com" })).email).toBe("sales@ours.com");
  });

  it("names an address with the account's From name when it has none", () => {
    expect(fromLabel(account.identities[1], account)).toBe("Ours Sales <sales@ours.com>");
    expect(fromLabel(id({ email: "help@ours.com" }), account)).toBe("Mia <help@ours.com>");
  });

  it("shows the From name and signature filled for you", () => {
    const shared = id({ email: "support@ours.com", display_name: "{agent} at Ours Support", from_name: "Maya Chen at Ours Support", signature_html: "<p>{{ agent.name }}</p>", filled_signature_html: "<p>Maya Chen</p>" });
    expect(fromLabel(shared, account)).toBe("Maya Chen at Ours Support <support@ours.com>");
    expect(identitySignature(shared)).toBe("<p>Maya Chen</p>");
    expect(identitySignature(id({ signature_html: "<p>Plain</p>", filled_signature_html: null }))).toBe("<p>Plain</p>");
    expect(collabFromName("{agent} at {mailbox}", { agent: "Maya Chen", account: "Support", workspace: "Ours" })).toBe("Maya Chen at Support");
  });
});
