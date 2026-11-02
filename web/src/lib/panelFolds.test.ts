import { afterEach, describe, expect, it } from "vitest";
import { MAX_FOLD_SCOPES, __resetFoldsForTests, blockKeys, chooseOpen, chosenOpen, foldScope } from "./panelFolds";

afterEach(() => __resetFoldsForTests());

describe("panel folds", () => {
  it("knows a card by its id, else its title, counting cards that share one, and other blocks by their place", () => {
    expect(
      blockKeys([
        { type: "card", title: "Order #1042" },
        { type: "note" },
        { type: "card", title: "Order #1042" },
        { type: "card", id: "so-42", title: "Sales order S00042" },
        { type: "card", id: "so-42", title: "Sales order S00042 (copy)" },
        { type: "fields", title: "Order #1042" },
      ]),
    ).toEqual(["title:Order #1042", "#1", "title:Order #1042\u00002", "id:so-42", "id:so-42\u00002", "#5"]);
    // An id never meets a title of the same text.
    expect(blockKeys([{ type: "card", id: "Order #1042", title: "A" }, { type: "card", title: "Order #1042" }])).toEqual(["id:Order #1042", "title:Order #1042"]);
  });

  it("keeps a choice per ticket and panel", () => {
    chooseOpen(foldScope("t1", "p1"), "title:Order #1042", false);
    expect(chosenOpen(foldScope("t1", "p1"), "title:Order #1042")).toBe(false);
    expect(chosenOpen(foldScope("t1", "p2"), "title:Order #1042")).toBeUndefined();
    expect(chosenOpen(foldScope("t2", "p1"), "title:Order #1042")).toBeUndefined();
    chooseOpen(foldScope("t1", "p1"), "title:Order #1042", true);
    expect(chosenOpen(foldScope("t1", "p1"), "title:Order #1042")).toBe(true);
  });

  it("forgets the panels touched longest ago past its bound", () => {
    chooseOpen(foldScope("t0", "p"), "c", false);
    for (let i = 1; i <= MAX_FOLD_SCOPES; i++) {
      chooseOpen(foldScope(`t${i}`, "p"), "c", false);
      if (i === 1) chooseOpen(foldScope("t0", "p"), "c", true); // t0 touched again: t1 is now the oldest
    }
    expect(chosenOpen(foldScope("t0", "p"), "c")).toBe(true);
    expect(chosenOpen(foldScope("t1", "p"), "c")).toBeUndefined();
    expect(chosenOpen(foldScope("t2", "p"), "c")).toBe(false);
    expect(chosenOpen(foldScope(`t${MAX_FOLD_SCOPES}`, "p"), "c")).toBe(false);
  });
});
