import { describe, expect, it } from "vitest";
import type { MailCounts, MailFolder, MailThread } from "../api/types";
import { countsAfter } from "./actions";

const folder = (id: string, role: MailFolder["role"], account = "a1") => ({ id, account_id: account, role }) as MailFolder;
const folders = [folder("in1", "inbox"), folder("tr1", "trash"), folder("sp1", "spam"), folder("ar1", "archive"), folder("lb1", null), folder("in2", "inbox", "a2")];
const thread = (over: Partial<MailThread>) => ({ id: "t1", account_id: "a1", unread: true, shared: false, folder_ids: ["in1"], ...over }) as MailThread;
const counts = (): MailCounts => ({ boxes: { inbox: 3, unread: 4, shared: 1, assigned: 0, drafts: 0 }, assigned: {}, accounts: { a1: 2, a2: 1 }, folders: { in1: 2, in2: 1, lb1: 1 } });

describe("countsAfter", () => {
  it("takes a conversation read on opening out of every number it was in", () => {
    const out = countsAfter(counts(), [thread({ folder_ids: ["in1", "lb1"] })], "read", folders);
    expect(out.boxes).toMatchObject({ inbox: 2, unread: 3 });
    expect(out.accounts).toEqual({ a1: 1, a2: 1 });
    expect(out.folders).toMatchObject({ in1: 1, lb1: 0 });
  });

  it("keeps an account at zero rather than dropping it", () => {
    const out = countsAfter({ ...counts(), accounts: { a1: 1 } }, [thread({})], "read", folders);
    expect(out.accounts.a1).toBe(0);
  });

  it("changes nothing for a conversation already read", () => {
    expect(countsAfter(counts(), [thread({ unread: false })], "read", folders)).toEqual(counts());
  });

  it("counts a conversation marked unread", () => {
    const out = countsAfter(counts(), [thread({ unread: false })], "unread", folders);
    expect(out.boxes).toMatchObject({ inbox: 4, unread: 5 });
    expect(out.accounts.a1).toBe(3);
  });

  it("takes an archived or trashed conversation out of the inbox", () => {
    const archived = countsAfter(counts(), [thread({})], "archive", folders);
    expect(archived.boxes).toMatchObject({ inbox: 2, unread: 4 });
    expect(archived.folders).toMatchObject({ in1: 1, ar1: 1 });
    const trashed = countsAfter(counts(), [thread({ shared: true })], "trash", folders);
    expect(trashed.boxes).toMatchObject({ inbox: 2, unread: 3, shared: 0 });
  });

  it("does not go below zero", () => {
    const out = countsAfter({ boxes: { inbox: 0, unread: 0, shared: 0, assigned: 0, drafts: 0 }, assigned: {}, accounts: {}, folders: {} }, [thread({})], "read", folders);
    expect(out.boxes.inbox).toBe(0);
    expect(out.accounts.a1).toBe(0);
  });
});

describe("countsAfter by address", () => {
  const withPeople = (over: Partial<MailThread>) => thread({ participants: [{ name: "", email: "Frank@example.com" }, { name: "", email: "me@acme.test" }], ...over });
  const own = { a1: ["me@acme.test", "support@acme.test"] };

  it("takes a read conversation off the alias it was written to and the addresses among its people", () => {
    const c = { ...counts(), addresses: { a1: { "support@acme.test": 2, "me@acme.test": 1 } } };
    const out = countsAfter(c, [withPeople({ to_address: "support@acme.test" })], "read", folders, undefined, own);
    expect(out.addresses).toEqual({ a1: { "support@acme.test": 1, "me@acme.test": 0 } });
  });

  it("counts an address that had nothing unread once a conversation is marked unread", () => {
    const c = { ...counts(), addresses: {} };
    const out = countsAfter(c, [withPeople({ unread: false, to_address: "support@acme.test" })], "unread", folders, undefined, own);
    expect(out.addresses).toEqual({ a1: { "support@acme.test": 1, "me@acme.test": 1 } });
  });

  it("leaves addresses alone without the counts by address", () => {
    expect(countsAfter(counts(), [withPeople({})], "read", folders, undefined, own).addresses).toBeUndefined();
  });
});
