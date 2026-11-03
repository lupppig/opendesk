import { describe, expect, it } from "vitest";
import type { Message } from "@/lib/api/types";
import { baseLanguage, combineDraft, isAiMessage, isForeign, isLanguage, languageName, messageLanguage, streamTextToHtml, ticketLanguage, TRANSLATE_LANGUAGES, translateTargets } from "./text";

describe("streamTextToHtml", () => {
  it("splits paragraphs and keeps line breaks", () => {
    expect(streamTextToHtml("Hi Dana,\n\nThanks for this.\nMore soon.\n\n")).toBe("<p>Hi Dana,</p><p>Thanks for this.<br>More soon.</p>");
  });
  it("escapes markup and ignores empty input", () => {
    expect(streamTextToHtml("a <b> & c")).toBe("<p>a &lt;b&gt; &amp; c</p>");
    expect(streamTextToHtml("\n\n")).toBe("");
  });
  it("renders a partial stream mid-paragraph", () => {
    expect(streamTextToHtml("Hi Dana,\n\nThanks for")).toBe("<p>Hi Dana,</p><p>Thanks for</p>");
  });
});

describe("languages", () => {
  it("compares base subtags", () => {
    expect(baseLanguage("pt-BR")).toBe("pt");
    expect(isForeign("es")).toBe(true);
    expect(isForeign("en-GB")).toBe(false);
    expect(isForeign("de", "de-AT")).toBe(false);
    expect(isForeign("")).toBe(false);
    expect(isForeign("und")).toBe(false);
    expect(isForeign(null)).toBe(false);
  });
  it("names languages", () => {
    expect(languageName("es")).toBe("Spanish");
    expect(languageName("de")).toBe("German");
  });
  it("prefers the message's own language over the ticket's", () => {
    const m = { custom: { language: "fr" } } as unknown as Message;
    expect(messageLanguage(m, { custom: { language: "es" } })).toBe("fr");
    expect(messageLanguage({} as Message, { custom: { language: "es" } })).toBe("es");
    expect(ticketLanguage({ custom: {} })).toBeNull();
  });
  it("spots AI bot answers", () => {
    expect(isAiMessage({ ai: true } as unknown as Message)).toBe(true);
    expect(isAiMessage({} as Message)).toBe(false);
  });
});

describe("combineDraft", () => {
  it("replaces or appends", () => {
    expect(combineDraft("<p>a</p>", "<p>b</p>", "replace")).toBe("<p>b</p>");
    expect(combineDraft("<p>a</p>", "<p>b</p>", "below")).toBe("<p>a</p><p>b</p>");
    expect(combineDraft("", "<p>b</p>", "below")).toBe("<p>b</p>");
  });
});

describe("translateTargets", () => {
  it("knows a real language from an undetermined one", () => {
    expect(isLanguage("pt-BR")).toBe(true);
    expect(isLanguage("und")).toBe(false);
    expect(isLanguage("zxx")).toBe(false);
    expect(isLanguage(null)).toBe(false);
  });

  it("puts the customer's language first, then the others by name in the reader's language", () => {
    const fr = translateTargets("nl-BE", "fr");
    expect(fr[0]).toBe("nl-BE");
    expect(fr).not.toContain("nl");
    expect(fr).toHaveLength(TRANSLATE_LANGUAGES.length);
    expect(fr.indexOf("de")).toBeLessThan(fr.indexOf("en")); // allemand < anglais
    expect(translateTargets(null, "en").indexOf("de")).toBeGreaterThan(translateTargets(null, "en").indexOf("en")); // English < German
  });

  it("offers every language when the customer's is unknown", () => {
    expect(translateTargets("und", "fr")).toHaveLength(TRANSLATE_LANGUAGES.length);
    expect(translateTargets(null, "fr")).toContain("fr");
  });
});
