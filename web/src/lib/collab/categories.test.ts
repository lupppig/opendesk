import { describe, expect, it } from "vitest";
import type { MailAccount, MailAccountCategories, MailCategory, MailCounts } from "../api/types";
import { accountCategories, categoryProblem, categoryTabs, categoryTone, ruleSender, slugKey, withKeys } from "./categories";

const cat = (key: string, name = key[0].toUpperCase() + key.slice(1), color: MailCategory["color"] = "gray"): MailCategory => ({ key, name, description: "", color });

function account(id: string, categories: MailCategory[] | null): MailAccount {
  const c: MailAccountCategories = {
    enabled: !!categories,
    categories: categories ?? [],
    source: categories ? "workspace" : "none",
    team: null,
    mode: "follow",
    own: [],
    ai: true,
    locked: false,
  };
  return { id, categories: c } as MailAccount;
}

const counts = (p: Partial<MailCounts>): MailCounts => ({ boxes: { inbox: 0, unread: 0, shared: 0, assigned: 0, drafts: 0, needs_action: 0 }, accounts: {}, folders: {}, assigned: {}, ...p });

describe("categoryTabs", () => {
  it("is empty while no account in view is split", () => {
    expect(categoryTabs([account("a", null), account("b", null)])).toEqual([]);
  });

  it("joins the accounts' categories by key, Primary first, in the order they first appear, with the first account's name", () => {
    const work = account("w", [cat("primary", "Important", "blue"), cat("updates"), cat("clients", "Clients", "green")]);
    const sales = account("s", [cat("primary"), cat("leads"), cat("updates", "Robots")]);
    const tabs = categoryTabs([work, sales]);
    expect(tabs.map((t) => t.key)).toEqual(["primary", "updates", "clients", "leads"]);
    expect(tabs[0].name).toBe("Important");
    expect(tabs[1].name).toBe("Updates");
  });

  it("sums each tab's unread conversations, and counts an account that is not split under Primary", () => {
    const work = account("w", [cat("primary"), cat("updates")]);
    const sales = account("s", [cat("primary"), cat("updates"), cat("newsletters")]);
    const personal = account("p", null);
    const c = counts({ accounts: { w: 9, s: 9, p: 4 }, categories: { w: { primary: 2, updates: 3 }, s: { primary: 1, newsletters: 5 } } });
    const tabs = categoryTabs([work, sales, personal], c);
    expect(Object.fromEntries(tabs.map((t) => [t.key, t.unread]))).toEqual({ primary: 2 + 1 + 4, updates: 3, newsletters: 5 });
  });

  it("leaves out an account with one category", () => {
    expect(categoryTabs([account("a", [cat("primary")])])).toEqual([]);
  });
});

describe("withKeys", () => {
  it("keeps Primary's key on the first, makes the others from their names and never repeats one", () => {
    const out = withKeys([cat("", "Important"), cat("", "Sales leads"), cat("", "Sales  leads!"), cat("primary", "Not primary"), cat("updates")]);
    expect(out.map((c) => c.key)).toEqual(["primary", "sales-leads", "sales-leads-2", "category", "updates"]);
  });

  it("falls back to a plain key for a name with no letters or digits", () => {
    expect(withKeys([cat("primary"), cat("", "★★")])[1].key).toBe("category");
  });
});

describe("slugKey", () => {
  it("lower-cases, joins words with dashes and stops at 36 characters", () => {
    expect(slugKey("  Clients & Partners ")).toBe("clients-partners");
    expect(slugKey("x".repeat(50))).toHaveLength(36);
  });
});

describe("categoryProblem", () => {
  it("needs 2 to 8 categories", () => {
    expect(categoryProblem([cat("primary")])).toBe("count");
    expect(categoryProblem(Array.from({ length: 9 }, (_, i) => cat(`c${i}`)))).toBe("count");
    expect(categoryProblem([cat("primary"), cat("updates")])).toBeNull();
  });

  it("needs a name of up to 40 characters on each", () => {
    expect(categoryProblem([cat("primary"), cat("x", "   ")])).toBe("name");
    expect(categoryProblem([cat("primary"), cat("x", "y".repeat(41))])).toBe("name");
  });

  it("refuses two names that differ only in case or spaces", () => {
    expect(categoryProblem([cat("primary", "Primary"), cat("x", " primary ")])).toBe("repeat");
  });
});

describe("ruleSender", () => {
  it("names the first participant who is not one of the account's addresses, in lower case", () => {
    const own = new Set(["me@margin.app"]);
    expect(ruleSender([{ email: "ME@margin.app" }, { email: "Ana@Acme.com" }], own)).toBe("ana@acme.com");
    expect(ruleSender([{ email: "me@margin.app" }], own)).toBeNull();
    expect(ruleSender([{ email: "" }], own)).toBeNull();
  });
});

describe("accountCategories and categoryTone", () => {
  it("offers an account's categories only while its inbox is split", () => {
    expect(accountCategories(account("a", [cat("primary"), cat("updates")])).map((c) => c.key)).toEqual(["primary", "updates"]);
    expect(accountCategories(account("a", null))).toEqual([]);
    expect(accountCategories(undefined)).toEqual([]);
  });

  it("reads an unknown colour as grey", () => {
    expect(categoryTone({ color: "violet" })).toBe("violet");
    expect(categoryTone({ color: "chartreuse" as MailCategory["color"] })).toBe("gray");
  });
});
