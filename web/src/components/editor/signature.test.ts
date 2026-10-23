import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import Image from "@tiptap/extension-image";
import { afterEach, describe, expect, it } from "vitest";
import { replaceSignature, SignatureBlock, signatureBlockHtml, withoutSignature } from "./signature";

// A signature as Gmail keeps it: a layout table, a sized logo, lines split by <br>.
const GMAIL =
  '<div dir="ltr"><div>Thanks,</div><table cellpadding="0" cellspacing="0" border="0" style="border-collapse: collapse"><tbody><tr>' +
  '<td valign="top" style="padding-right: 12px"><img src="https://example.com/logo.png" width="96" height="38" alt="Northwind"></td>' +
  '<td valign="top"><b>Ada Lovelace</b><br><span style="color: #6b7280">Founder &amp; CEO</span></td></tr></tbody></table></div>';

let editors: Editor[] = [];
afterEach(() => {
  editors.forEach((e) => e.destroy());
  editors = [];
});

// The node view is React's; the schema and the HTML are what is tested here.
const Block = SignatureBlock.extend({ addNodeView: undefined });

function editor(content: string) {
  const e = new Editor({ extensions: [StarterKit, Image, Block], content });
  editors.push(e);
  return e;
}

describe("signature block", () => {
  it("keeps a designed signature as one block and writes it out as stored", () => {
    const e = editor(`<p>Hi</p><p></p>${signatureBlockHtml(GMAIL)}`);
    const blocks: string[] = [];
    e.state.doc.descendants((n) => {
      if (n.type.name === "signatureBlock") blocks.push(n.attrs.html as string);
    });
    expect(blocks).toEqual([GMAIL]);
    // No paragraph took the name and title apart or glued them together.
    expect(e.getText()).not.toContain("Ada Lovelace");
    expect(e.getHTML()).toBe(`<p>Hi</p><p></p><div class="od-signature">${GMAIL}</div>`);
  });

  it("opens a saved draft with its signature intact", () => {
    const sent = editor(`<p>Draft</p>${signatureBlockHtml(GMAIL)}`).getHTML();
    expect(editor(sent).getHTML()).toBe(sent);
  });

  it("opens a Gmail draft's signature as a block", () => {
    const e = editor(`<p>From Gmail</p><div dir="ltr" class="gmail_signature"><b>Ada</b><br>Founder</div>`);
    expect(e.getHTML()).toBe(`<p>From Gmail</p><div class="od-signature"><div dir="ltr" class="gmail_signature"><b>Ada</b><br>Founder</div></div>`);
  });

  it("drops anything active from a pasted block", () => {
    const e = editor(`<div class="od-signature"><img src="x" onerror="alert(1)"><script>alert(2)</script><b>Ada</b></div>`);
    const html = e.getHTML();
    expect(html).not.toMatch(/onerror|script/);
    expect(html).toContain("<b>Ada</b>");
  });
});

describe("replaceSignature", () => {
  const body = `<p>Hello</p><div class="od-signature"><p>Old</p></div><blockquote><div class="od-signature"><p>Quoted</p></div></blockquote>`;

  it("swaps the composer's block, not a quoted one", () => {
    expect(replaceSignature(body, GMAIL)).toBe(`<p>Hello</p><div class="od-signature">${GMAIL}</div><blockquote><div class="od-signature"><p>Quoted</p></div></blockquote>`);
  });

  it("removes the block for an account without a signature", () => {
    expect(replaceSignature(body, "")).toBe(`<p>Hello</p><blockquote><div class="od-signature"><p>Quoted</p></div></blockquote>`);
  });

  it("leaves a body whose signature was taken out", () => {
    expect(replaceSignature("<p>Hello</p>", GMAIL)).toBeNull();
  });

  it("finds what the person wrote", () => {
    expect(withoutSignature(`<p></p><p></p>${signatureBlockHtml(GMAIL)}`)).toBe("<p></p><p></p>");
  });
});
