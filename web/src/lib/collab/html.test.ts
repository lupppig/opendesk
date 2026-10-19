import { describe, expect, it } from "vitest";
import { isBlankHtml, mentionIds } from "./html";
import { presetForEmail } from "./imapPresets";

describe("html helpers", () => {
  it("finds mentions", () => {
    const html = '<p>Hi <span data-type="mention" class="mention" data-id="u1" data-label="Ada">@Ada</span> and <span class="mention" data-id="u2">@Bo</span> <span data-id="u3">x</span> <span class="mention" data-id="u1">@Ada</span></p>';
    expect(mentionIds(html)).toEqual(["u1", "u2"]);
  });
  it("knows blank editor HTML", () => {
    expect(isBlankHtml("<p></p><p>&nbsp;</p>")).toBe(true);
    expect(isBlankHtml('<p><img src="x"></p>')).toBe(false);
    expect(isBlankHtml("<p>hi</p>")).toBe(false);
  });
  it("picks presets by domain", () => {
    expect(presetForEmail("Ada@Gmail.com")?.imap_host).toBe("imap.gmail.com");
    expect(presetForEmail("ada@acme.com")).toBeNull();
  });
});
