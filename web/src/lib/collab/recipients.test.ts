import { describe, expect, it } from "vitest";
import type { MailMessage } from "../api/types";
import { dedupe, hasReplyAll, parseAddressList, replyRecipients, replySubject, replyTarget } from "./recipients";

const msg = (p: Partial<MailMessage>): MailMessage => ({
  id: "m1",
  thread_id: "t1",
  account_id: "a1",
  message_id: "x@y",
  from: { name: "Ada", email: "ada@acme.com" },
  to: [{ name: "Me", email: "me@ours.com" }],
  cc: [],
  bcc: [],
  reply_to: [],
  subject: "Hello",
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

describe("replyRecipients", () => {
  it("replies to the sender", () => {
    expect(replyRecipients(msg({}), "me@ours.com", "reply")).toEqual({ to: ["ada@acme.com"], cc: [] });
  });

  it("prefers Reply-To over From", () => {
    const m = msg({ reply_to: [{ name: "", email: "support@acme.com" }] });
    expect(replyRecipients(m, "me@ours.com", "reply").to).toEqual(["support@acme.com"]);
  });

  it("reply all keeps everyone except the account's own address, case-insensitively", () => {
    const m = msg({
      to: [
        { name: "Me", email: "ME@ours.com" },
        { name: "Bob", email: "bob@acme.com" },
      ],
      cc: [
        { name: "Cy", email: "cy@acme.com" },
        { name: "Bob again", email: "Bob@acme.com" },
        { name: "Me", email: "me@ours.com" },
      ],
    });
    expect(replyRecipients(m, "me@ours.com", "reply_all")).toEqual({ to: ["ada@acme.com", "bob@acme.com"], cc: ["cy@acme.com"] });
  });

  it("replying to our own message goes to its recipients again", () => {
    const m = msg({ from: { name: "Me", email: "me@ours.com" }, to: [{ name: "Ada", email: "ada@acme.com" }], cc: [{ name: "Cy", email: "cy@acme.com" }] });
    expect(replyRecipients(m, "me@ours.com", "reply")).toEqual({ to: ["ada@acme.com"], cc: [] });
    expect(replyRecipients(m, "me@ours.com", "reply_all")).toEqual({ to: ["ada@acme.com"], cc: ["cy@acme.com"] });
  });

  it("reply all leaves out every one of the account's addresses, its aliases included", () => {
    const m = msg({
      from: { name: "Ada", email: "ada@acme.com" },
      to: [{ name: "Sales", email: "Sales@ours.com" }],
      cc: [
        { name: "Me", email: "me@ours.com" },
        { name: "Cy", email: "cy@acme.com" },
      ],
    });
    expect(replyRecipients(m, ["me@ours.com", "sales@ours.com"], "reply_all")).toEqual({ to: ["ada@acme.com"], cc: ["cy@acme.com"] });
    // Sent from an alias: a reply goes to its recipients again.
    const sent = msg({ from: { name: "Sales", email: "sales@ours.com" }, to: [{ name: "Ada", email: "ada@acme.com" }] });
    expect(replyRecipients(sent, ["me@ours.com", "sales@ours.com"], "reply")).toEqual({ to: ["ada@acme.com"], cc: [] });
  });

  it("forward and new start empty", () => {
    expect(replyRecipients(msg({}), "me@ours.com", "forward")).toEqual({ to: [], cc: [] });
    expect(replyRecipients(msg({}), "me@ours.com", "new")).toEqual({ to: [], cc: [] });
  });

  it("knows when reply all adds people", () => {
    expect(hasReplyAll(msg({}), "me@ours.com")).toBe(false);
    expect(hasReplyAll(msg({ cc: [{ name: "", email: "cy@acme.com" }] }), "me@ours.com")).toBe(true);
  });
});

describe("subjects and parsing", () => {
  it("prefixes once", () => {
    expect(replySubject("Hello", "reply")).toBe("Re: Hello");
    expect(replySubject("RE: Hello", "reply_all")).toBe("RE: Hello");
    expect(replySubject("Hello", "forward")).toBe("Fwd: Hello");
    expect(replySubject("Fw: Hello", "forward")).toBe("Fw: Hello");
    expect(replySubject(" Hello ", "new")).toBe("Hello");
  });

  it("parses pasted lists", () => {
    expect(parseAddressList("Ada <ada@acme.com>, bob@acme.com; nope, cy@acme.com dee@acme.com")).toEqual({
      valid: ["ada@acme.com", "bob@acme.com", "cy@acme.com", "dee@acme.com"],
      invalid: ["nope"],
    });
    expect(dedupe(["a@x.com", "A@x.com", "b@x.com"])).toEqual(["a@x.com", "b@x.com"]);
  });

  it("replies to the newest message that is not a draft", () => {
    const a = msg({ id: "a" });
    const b = msg({ id: "b", draft: true });
    expect(replyTarget([a, b])?.id).toBe("a");
  });
});
