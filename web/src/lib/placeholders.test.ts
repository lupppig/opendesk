import { describe, expect, it } from "vitest";
import { findPlaceholders, isPlaceholder, ownText } from "./placeholders";

describe("findPlaceholders", () => {
  it("finds the notes a draft leaves for the agent", () => {
    const html =
      "<p>Hi Jane,</p><p>We refunded <strong>[confirm the refund amount]</strong> to your card. It reaches you in [add the delay].</p><p>Best,<br>[Prénom]</p>";
    expect(findPlaceholders(html)).toEqual(["[confirm the refund amount]", "[add the delay]", "[Prénom]"]);
  });

  it("names each once, trimmed", () => {
    expect(findPlaceholders("<p>[ order number ] and again [order number]</p>")).toEqual(["[order number]"]);
  });

  it("leaves markdown links and images alone", () => {
    expect(findPlaceholders("<p>See [Refund policy](https://acme.test/refunds) and ![logo](https://acme.test/l.png).</p>")).toEqual([]);
  });

  it("leaves references alone", () => {
    const refs = ["[#123]", "[#ABC-12]", "[@anna]", "[Ticket #4521]", "[ACM-7]", "[INV/2026/0042]", "[v2]", "[1]", "[12]"];
    expect(findPlaceholders(`<p>${refs.join(" ")}</p>`)).toEqual([]);
  });

  it("leaves cuts, checkboxes and inline stand-ins alone", () => {
    expect(findPlaceholders("<p>[…] [...] [ ] [x] [cid:image001.png@01DA] [image: Logo] [mailto:a@b.test]</p>")).toEqual([]);
  });

  it("leaves quoted text, code and links alone", () => {
    const html = [
      "<p>Thanks!</p>",
      "<blockquote><p>On Monday you wrote: please [fill this in]</p></blockquote>",
      "<pre><code>items[index name]</code></pre>",
      "<p>Run <code>[your command]</code>.</p>",
      '<p><a href="https://acme.test">[Open the tracking page]</a></p>',
      "<p>&gt; [quoted in plain text]</p>",
    ].join("");
    expect(findPlaceholders(html)).toEqual([]);
  });

  it("does not join brackets across paragraphs or lines", () => {
    expect(findPlaceholders("<p>Prices [from</p><p>today] apply</p>")).toEqual([]);
    expect(findPlaceholders("<p>Prices [from<br>today] apply</p>")).toEqual([]);
  });

  it("does not take a bracket inside a longer one, nor a very long one", () => {
    expect(findPlaceholders("<p>[[double]]</p>")).toEqual(["[double]"]);
    expect(findPlaceholders(`<p>[${"a".repeat(301)}]</p>`)).toEqual([]);
  });

  it("is empty for an empty reply", () => {
    expect(findPlaceholders("")).toEqual([]);
    expect(findPlaceholders("<p></p>")).toEqual([]);
  });
});

describe("isPlaceholder", () => {
  it("takes words with a number in a phrase", () => {
    expect(isPlaceholder("add the tracking link for order 1234")).toBe(true);
    expect(isPlaceholder("confirm the 2 refunds")).toBe(true);
    expect(isPlaceholder("date")).toBe(true);
  });
});

describe("ownText", () => {
  it("keeps a line per paragraph and line break", () => {
    expect(ownText("<p>a</p><p>b<br>c</p>").split("\n").filter(Boolean)).toEqual(["a", "b", "c"]);
  });
});
