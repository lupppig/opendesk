import { describe, expect, it } from "vitest";
import { cleanSignatureHtml, isDesignedEmail, sanitizeEmailHtml, sanitizeHtml, sanitizeProse } from "./sanitize";

describe("sanitize", () => {
  it("keeps only the app's own classes in the app's DOM", () => {
    const out = sanitizeHtml(`<p class="fixed inset-0">Hi <span class="mention" data-id="u1">@Maya</span></p><style>p{}</style>`);
    expect(out).not.toContain("fixed");
    expect(out).toContain(`class="mention"`);
    expect(out).not.toContain("<style");
  });

  it("keeps a designed email's styles and layout for its frame", () => {
    const { html: out } = sanitizeEmailHtml(
      `<style>.card{border-radius:8px}</style><table class="card" width="600" bgcolor="#ffffff" cellpadding="0"><tr><td valign="top" style="padding:24px" onclick="x()">Paid</td></tr></table><script>alert(1)</script>`,
    );
    expect(out).toContain("<style>.card{border-radius:8px}</style>");
    for (const want of [`class="card"`, `width="600"`, `bgcolor="#ffffff"`, `cellpadding="0"`, `valign="top"`, `style="padding:24px"`]) expect(out).toContain(want);
    for (const bad of ["onclick", "<script", "alert"]) expect(out).not.toContain(bad);
  });

  it("drops a style sheet that uses CSS escapes", () => {
    const { html } = sanitizeEmailHtml(`<style>.x{background:\\75 rl(https://t.example/a.png)}</style><p class="x">x</p>`);
    expect(html).not.toContain("t.example");
  });

  it("tells designed emails from plain ones", () => {
    expect(isDesignedEmail(`<table width="600"><tr><td>x</td></tr></table>`)).toBe(true);
    expect(isDesignedEmail(`<div style="background-color: #f6f9fc">x</div>`)).toBe(true);
    expect(isDesignedEmail(`<style>p.MsoNormal{margin:0}</style><div style="font-family: Aptos">Thanks</div>`)).toBe(false);
  });

  describe("remote images", () => {
    const ours = `${location.origin}/v1/attachments/0192f0c4-0000-7000-8000-000000000000?exp=1&sig=ab`;
    const email =
      `<style>.hero{background:url(https://t.example/a.png) #fff}.logo{background-image:url("${ours}")}@media (max-width:600px){.x{background:url('https://t.example/b.png')}}</style>` +
      `<table style="background-image:url(https://t.example/c.png);color:red"><tr><td>` +
      `<img src="https://t.example/open.gif" width="1" height="1"><img src="//t.example/d.gif"><img src="${ours}" alt="shot"><img src="data:image/png;base64,AAAA">` +
      `</td></tr></table>`;

    it("keeps a mail account's own signed attachments as inline images", () => {
      const mine = `${location.origin}/v1/collab/attachments/0192f0c4-0000-7000-8000-000000000000/0?exp=1&sig=ab`;
      const { html, held } = sanitizeEmailHtml(`<p><img src="${mine}" alt="logo"><img src="https://t.example/x.png"></p>`, { blockRemoteImages: true });
      expect(held).toBe(1);
      expect(html).toContain(mine.replace("&", "&amp;"));
    });

    it("holds back every image from another site until loaded", () => {
      const { html, held } = sanitizeEmailHtml(email, { blockRemoteImages: true });
      expect(html).not.toContain("t.example");
      expect(held).toBe(5);
      expect(html).toContain(ours.replace("&", "&amp;"));
      expect(html).toContain("data:image/png;base64,AAAA");
      expect(html).toContain("color:red");
      const loaded = sanitizeEmailHtml(email);
      expect(loaded.held).toBe(0);
      expect(loaded.html).toContain("https://t.example/open.gif");
    });

    it("fails closed on CSS it cannot read", () => {
      for (const style of [`background:url("https://t.example/a(1).png")`, `background:-webkit-image-set("https://t.example/a.png" 1x)`, `background:\\75 rl(https://t.example/a.png)`]) {
        const { html } = sanitizeEmailHtml(`<div style='${style}'>x</div>`, { blockRemoteImages: true });
        expect(html).not.toContain("t.example");
      }
      const sheet = sanitizeEmailHtml(`<style>@import "https://t.example/x.css"; p{color:red}</style><p>x</p>`, { blockRemoteImages: true });
      expect(sheet.html).not.toContain("t.example");
    });

    it("holds back images in plain emails too", () => {
      const { html, held } = sanitizeProse(`<p>Hi<img src="https://t.example/open.gif"></p>`, { blockRemoteImages: true });
      expect(held).toBe(1);
      expect(html).not.toContain("t.example");
    });
  });
});

describe("cleanSignatureHtml", () => {
  it("keeps a signature's layout and adds nothing to it", () => {
    const sig = '<table cellpadding="0" style="border-collapse: collapse"><tbody><tr><td class="gmail_signature"><img src="https://example.com/logo.png" width="96" height="38" alt="Logo"><a href="https://example.com">example.com</a></td></tr></tbody></table>';
    expect(cleanSignatureHtml(sig)).toBe(sig);
  });

  it("drops what runs", () => {
    expect(cleanSignatureHtml('<a href="javascript:alert(1)" onclick="x()">x</a><img src=x onerror="alert(1)"><style>a{}</style>')).toBe('<a>x</a><img>');
  });
});
