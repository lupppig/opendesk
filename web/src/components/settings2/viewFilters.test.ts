import { describe, expect, it } from "vitest";
import { cleanFilters, clearField, filterOp, hasFilter, joinList, splitList, withOp } from "./viewFilters";

describe("filterOp", () => {
  it("reads the operator from the key that is set", () => {
    expect(filterOp({ tag: "vip" }, "tag")).toBe("is");
    expect(filterOp({ tag_not: "Q&A Done" }, "tag")).toBe("not");
    expect(filterOp({ untagged: true }, "tag")).toBe("empty");
    expect(filterOp({}, "tag")).toBe("is");
    expect(filterOp({ team_not: "t1,t2" }, "team")).toBe("not");
    expect(filterOp({ team_id: "t1", team_not: "t2" }, "team")).toBe("is");
    expect(filterOp({ mailbox_not: "m1" }, "mailbox")).toBe("not");
    expect(filterOp({ channel_not: ["chat"] }, "channel")).toBe("not");
    expect(filterOp({ channel: "email" }, "channel")).toBe("is");
  });
});

describe("withOp", () => {
  it("carries labels between any of and none of, and drops them for empty", () => {
    const none = withOp({ status: ["open"], tag: "cancel/refund,ORDER-CHANGE/CANCEL" }, "tag", "not");
    expect(cleanFilters(none)).toEqual({ status: ["open"], tag_not: "cancel/refund,ORDER-CHANGE/CANCEL" });
    expect(cleanFilters(withOp(none, "tag", "is"))).toEqual({ status: ["open"], tag: "cancel/refund,ORDER-CHANGE/CANCEL" });
    expect(cleanFilters(withOp(none, "tag", "empty"))).toEqual({ status: ["open"], untagged: true });
    expect(cleanFilters(withOp({ untagged: true }, "tag", "is"))).toEqual({});
  });

  it("moves teams, mailboxes and channels between is and is not", () => {
    expect(cleanFilters(withOp({ team_id: "t1,t2" }, "team", "not"))).toEqual({ team_not: "t1,t2" });
    expect(cleanFilters(withOp({ team_not: "t1" }, "team", "is"))).toEqual({ team_id: "t1" });
    expect(cleanFilters(withOp({ mailbox_id: "m1" }, "mailbox", "not"))).toEqual({ mailbox_not: "m1" });
    expect(cleanFilters(withOp({ mailbox_not: "m1,m2" }, "mailbox", "is"))).toEqual({ mailbox_id: "m1" });
    expect(cleanFilters(withOp({ channel: "chat" }, "channel", "not"))).toEqual({ channel_not: ["chat"] });
    expect(cleanFilters(withOp({ channel_not: ["api", "chat"] }, "channel", "is"))).toEqual({ channel: "api" });
    expect(cleanFilters(withOp({}, "channel", "not"))).toEqual({});
  });
});

describe("clearField and hasFilter", () => {
  it("clear every operator's key of a row", () => {
    const f = { tag: "a", tag_not: "b", untagged: true, team_not: "t1", channel_not: ["chat" as const] };
    expect(hasFilter(f, "tag") && hasFilter(f, "team") && hasFilter(f, "channel")).toBe(true);
    expect(hasFilter(f, "mailbox")).toBe(false);
    expect(cleanFilters(clearField(clearField(f, "tag"), "team"))).toEqual({ channel_not: ["chat"] });
  });
});

describe("lists", () => {
  it("split and join comma-separated values", () => {
    expect(splitList(" a, ,b ")).toEqual(["a", "b"]);
    expect(splitList(undefined)).toEqual([]);
    expect(joinList(["a", "b"])).toBe("a,b");
    expect(joinList([])).toBeUndefined();
  });

  it("keeps the contacts and companies a view leaves out", () => {
    expect(cleanFilters({ contact_not: "c1,c2", company_not: "co1" })).toEqual({ contact_not: "c1,c2", company_not: "co1" });
    expect(cleanFilters({ contact_not: joinList([]), company_not: "" })).toEqual({});
  });

  it("saves a single label exactly as before", () => {
    expect(cleanFilters({ tag: "vip", tag_not: "", untagged: false, channel_not: [] })).toEqual({ tag: "vip" });
  });
});
