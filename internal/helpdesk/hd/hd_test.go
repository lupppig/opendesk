package hd_test

import (
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
)

func TestSanitizeRich(t *testing.T) {
	cases := []struct{ in, want, notWant string }{
		{`<p onclick="x()">Hi <script>alert(1)</script><strong>there</strong></p>`, `<p>Hi <strong>there</strong></p>`, "script"},
		{`<a href="javascript:alert(1)">x</a>`, ``, "javascript"},
		{`<a href="https://example.com">x</a>`, `href="https://example.com"`, ""},
		{`<img src="https://example.com/a.png" onerror="x">`, `<img src="https://example.com/a.png">`, "onerror"},
		{`<img src="data:image/png;base64,AAAA">`, ``, "data:"},
		{`<img src="/v1/attachments/0192f0c4-0000-7000-8000-000000000000?exp=1&sig=ab">`, `src="/v1/attachments/0192f0c4-0000-7000-8000-000000000000?exp=1&amp;sig=ab"`, ""},
		{`<span data-mention="0192f0c4-0000-7000-8000-000000000000">@Anna</span>`, `data-mention="0192f0c4-0000-7000-8000-000000000000"`, ""},
		{`<span data-mention="evil">@x</span>`, ``, "evil"},
		{`<h4>t</h4><div style="color:red">d</div><iframe src="https://x"></iframe>`, `td`, "iframe"},
		{`<h2>Title</h2><ul><li>a</li></ul><pre><code>x</code></pre><blockquote>q</blockquote><u>u</u><s>s</s><em>e</em><br>`, `<h2>Title</h2><ul><li>a</li></ul><pre><code>x</code></pre><blockquote>q</blockquote><u>u</u><s>s</s><em>e</em><br>`, ""},
	}
	for _, c := range cases {
		got := hd.SanitizeRich(c.in)
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("SanitizeRich(%q) = %q, want contains %q", c.in, got, c.want)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("SanitizeRich(%q) = %q, must not contain %q", c.in, got, c.notWant)
		}
	}
}

func TestSanitizeEmailHTML(t *testing.T) {
	in := `<html><head><style><!--
body{margin:0} .x td{color:#333 !important;behavior:url(x.htc)} a{background:url(javascript:alert(1))}
@import url(https://evil.example/x.css);
@media only screen and (max-width:600px){.wrap{width:100% !important}}
@font-face{font-family:x;src:url(https://f.example/x.woff)}
--></style><script>alert(1)</script></head><body bgcolor="#f6f9fc">` +
		`<table width="600" cellpadding="0" border="0" align="center" bgcolor="#ffffff" style="border-radius:8px;position:fixed;width:expression(alert(1));background-image:url('https://img.example/a.png');font-family:'Helvetica Neue', Arial">` +
		`<tr><td valign="top" class="wrap" style="padding:16px 24px;background:url(data:image/svg+xml;base64,AAAA);color:rgb(10,20,30);-webkit-text-size-adjust:100%" onclick="x()">` +
		`<font color="red" size="2">Hi</font><img src="https://img.example/b.png" width="48" height="48" style="display:block;border:0"></td></tr></table></body></html>`
	got := hd.SanitizeEmailHTML(in)
	for _, want := range []string{
		`<style>body{margin:0}.x td{color:#333 !important}@media only screen and (max-width:600px){.wrap{width:100% !important}}</style>`,
		`width="600"`, `cellpadding="0"`, `align="center"`, `bgcolor="#ffffff"`, `valign="top"`, `class="wrap"`, `<font color="red" size="2">`,
		`border-radius: 8px`, `background-image: url(&#39;https://img.example/a.png&#39;)`, `color: rgb(10,20,30)`, `-webkit-text-size-adjust: 100%`, `display: block`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, bad := range []string{"script", "alert", "behavior", "javascript", "@import", "@font-face", "position", "expression", "svg", "onclick", "<body"} {
		if strings.Contains(got, bad) {
			t.Errorf("kept %q in %s", bad, got)
		}
	}
	// CSS escapes: checked decoded, refused when they do not decode.
	for _, style := range []string{`background:\75 rl(http://evil.example/x) \12345678`, `background:url(https://img.example/a.png) \12345678`,
		`width:\65 xpression(alert(1))`, `background:\75 rl(javascript:alert(1))`, `color:\3c/style\3e`,
		`background:\75 rl(https://tracker.example/open.gif)`, `background-image:u\72 l(https://tracker.example/open.gif)`} {
		if got := hd.SanitizeEmailHTML(`<p style="` + style + `">x</p>`); strings.Contains(got, "style") {
			t.Errorf("escaped style %q kept: %s", style, got)
		}
	}
	if got := hd.SanitizeEmailHTML(`<p style="color:red" title="Hi there">x &lt; y<br/>z</p>`); got != `<p style="color: red" title="Hi there">x &lt; y<br/>z</p>` {
		t.Errorf("plain markup changed: %s", got)
	}
	if got := hd.SanitizeEmailHTML(`<p style="color:red;background:\75 rl(https://t.example/x)" title="a\b">x\y</p>`); got != `<p style="color: red" title="a\b">x\y</p>` {
		t.Errorf("escaped style: %s", got)
	}
	if got := hd.SanitizeEmailHTML(`<style>p{color:red}</style><p>Hi</p>`); hd.SanitizeEmailHTML(got) != got {
		t.Errorf("not stable: %s", got)
	}
	if got := hd.SanitizeEmailHTML(`<style>.a{color:red};.b{color:blue}</style><p>Hi</p>`); got != `<style>.a{color:red}.b{color:blue}</style><p>Hi</p>` {
		t.Errorf("stray stylesheet semicolon: %s", got)
	}
}

func TestHTMLToText(t *testing.T) {
	got := hd.HTMLToText(`<p>Hello &amp; welcome</p><p>Line<br>two</p><style>p{}</style>`)
	if got != "Hello & welcome\nLine\ntwo" {
		t.Fatalf("got %q", got)
	}
	if hd.TextToHTML("a <b>\n\nc\nd") != "<p>a &lt;b&gt;</p><p>c<br>d</p>" {
		t.Fatal(hd.TextToHTML("a <b>\n\nc\nd"))
	}
}

func TestUploadChecks(t *testing.T) {
	if !hd.IsExecutable([]byte("MZ\x90\x00"), "report.pdf") || !hd.IsExecutable([]byte("hello"), "run.EXE") ||
		!hd.IsExecutable([]byte("#!/bin/sh\n"), "a.txt") || !hd.IsExecutable([]byte("\x7fELF"), "x") {
		t.Fatal("executable not refused")
	}
	if hd.IsExecutable([]byte("%PDF-1.7"), "a.pdf") || hd.IsExecutable([]byte("\xd0\xcf\x11\xe0"), "old.doc") {
		t.Fatal("document refused")
	}
	if ct := hd.DetectType([]byte("<html><script>x</script></html>"), "a.html"); strings.Contains(ct, "html") {
		t.Fatalf("active type kept: %s", ct)
	}
	if ct := hd.DetectType([]byte("\x89PNG\r\n\x1a\n"), "a.png"); ct != "image/png" {
		t.Fatalf("png: %s", ct)
	}
	// Browser voice recordings are audio, though their bytes sniff as video.
	for _, c := range []struct{ head, name, want string }{
		{"\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01webm\x18\x53\x80\x67\x86\x86A_OPUS\x63\xa2", "Voice message.webm", "audio/webm"},
		{"\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01webm\x86\x85V_VP8\x86\x86A_OPUS", "clip.webm", "video/webm"},
		{"OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x13OpusHead\x01", "Voice message.ogg", "audio/ogg"},
		{"\x00\x00\x00\x18ftypiso5\x00\x00\x02\x00iso5iso6mp41", "Voice message.m4a", "audio/mp4"},
		{"\x00\x00\x00\x1cftypiso5\x00\x00\x02\x00iso5iso6mp41", "clip.mp4", "video/mp4"},
		{"\x00\x00\x00\x18ftypiso5\x00\x00\x02\x00iso5iso6mp41\x00\x00\x00\x21hdlr\x00\x00\x00\x00\x00\x00\x00\x00vide", "renamed.m4a", "video/mp4"},
	} {
		if ct := hd.DetectType([]byte(c.head), c.name); ct != c.want {
			t.Errorf("%s: %s, want %s", c.name, ct, c.want)
		}
	}
	if hd.CleanFilename("../../etc/pa\x00ss\nwd") != "passwd" {
		t.Fatal(hd.CleanFilename("../../etc/pa\x00ss\nwd"))
	}
}

func TestSlugify(t *testing.T) {
	if s := hd.Slugify("  Héllo, Wörld!  Getting started "); s != "hello-world-getting-started" {
		t.Fatal(s)
	}
}

func TestHarness(t *testing.T) {
	e := hdtest.New(t)
	ws := e.NewWorkspace()
	s, err := hd.LoadSettings(e.Ctx, e.D.DB.Pool, ws.ID)
	if err != nil || s.AutoClose() != 7 {
		t.Fatal(err, s)
	}
	e.SetSettings(ws, map[string]any{"auto_close_days": 3, "csat_enabled": true})
	s, _ = hd.LoadSettings(e.Ctx, e.D.DB.Pool, ws.ID)
	if s.AutoClose() != 3 || !s.CSATEnabled {
		t.Fatal(s)
	}
}
