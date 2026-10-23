import { describe, expect, it } from "vitest";
import { isForward, splitQuotedHtml, splitQuotedText } from "./quote";

describe("splitQuotedHtml", () => {
  it("cuts Gmail's quote and keeps the signature", () => {
    const html =
      `<div dir="ltr"><div>Hi Matthew,</div><div><br></div><div>Any questions?</div><br><div>Jade Neser<br><b>Growth @ Ramp</b></div><br><br><br></div>` +
      `<br><div class="gmail_quote gmail_quote_container"><div dir="ltr" class="gmail_attr">On Mon, Oct 19, 2026 at 8:05 am, Jade Neser wrote:<br></div><blockquote class="gmail_quote">Matthew, still interested?</blockquote></div>`;
    const s = splitQuotedHtml(html)!;
    expect(s.body).toContain("Growth @ Ramp");
    expect(s.body).not.toContain("wrote:");
    expect(s.body).not.toMatch(/(<br>)+<\/div>$/);
    expect(s.quoted).toContain("On Mon, Oct 19, 2026");
    expect(s.quoted).toContain("still interested?");
  });

  it("cuts a plain attribution line with no client markup", () => {
    const html = `<p>Sounds good.</p><p>Best,<br>Jade</p><p></p><p>On Mon, Oct 19, 2026 at 8:05 am, Jade Neser wrote:</p><p>Matthew,</p><p>Haven't heard from you.</p>`;
    const s = splitQuotedHtml(html)!;
    expect(s.body).toBe("<p>Sounds good.</p><p>Best,<br>Jade</p>");
    expect(s.quoted).toContain("Haven't heard from you.");
  });

  it("cuts an attribution that is a line inside a block", () => {
    const s = splitQuotedHtml(`<div>Thanks!<br><br>On Tue, 3 Mar 2026, Ana &lt;ana@x.io&gt; wrote:<br>&gt; earlier</div>`)!;
    expect(s.body).toBe("<div>Thanks!</div>");
    expect(s.quoted).toContain("earlier");
  });

  it("cuts Outlook's header block", () => {
    const html = `<p>Yes, Friday works.</p><div id="appendonsend"></div><hr><div><b>From:</b> Sam &lt;sam@x.io&gt;<br><b>Sent:</b> Monday<br><b>Subject:</b> Plan</div><p>Can we meet?</p>`;
    const s = splitQuotedHtml(html)!;
    expect(s.body).toContain("Friday works");
    expect(s.body).not.toContain("Sent:");
    expect(s.quoted).toContain("Can we meet?");
  });

  it("cuts a trailing quote", () => {
    const s = splitQuotedHtml(`<p>Agreed.</p><blockquote><p>Shall we ship it?</p></blockquote>`)!;
    expect(s.body).toBe("<p>Agreed.</p>");
    expect(s.quoted).toContain("ship it");
  });

  it("leaves a message without history alone", () => {
    expect(splitQuotedHtml(`<p>Hello there.</p><blockquote>a quote I meant</blockquote><p>More after it.</p>`)).toBeNull();
  });

  it("leaves a message that is only a quote alone", () => {
    expect(splitQuotedHtml(`<div class="gmail_quote">On Mon, A wrote:<blockquote>x</blockquote></div>`)).toBeNull();
  });

  it("keeps the leading style sheets on both halves", () => {
    const s = splitQuotedHtml(`<style>p{color:red}</style><p>New</p><div class="gmail_quote">old</div>`)!;
    expect(s.body).toContain("<style>");
    expect(s.quoted.startsWith("<style>")).toBe(true);
  });
});

describe("splitQuotedText", () => {
  it("cuts at the attribution", () => {
    const s = splitQuotedText("Thanks!\n\nOn Mon, Oct 19, 2026 at 8:05 am Jade\nNeser <j@x.io> wrote:\n> hi")!;
    expect(s.body).toBe("Thanks!");
    expect(s.quoted).toContain("> hi");
  });
  it("cuts trailing > lines", () => {
    expect(splitQuotedText("Ok\n> a\n> b")!.body).toBe("Ok");
  });
  it("leaves plain text alone", () => {
    expect(splitQuotedText("Just text\nwith lines")).toBeNull();
  });
});

describe("isForward", () => {
  it("knows forwards by subject", () => {
    expect(isForward("Fwd: plan")).toBe(true);
    expect(isForward("Re: plan")).toBe(false);
  });
});
