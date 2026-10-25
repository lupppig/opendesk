package email

import (
	"net/mail"
	"net/netip"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/domain"
)

func TestPublicAddr(t *testing.T) {
	for _, s := range []string{"10.0.0.1", "172.16.4.4", "192.168.1.1", "127.0.0.1", "169.254.169.254", "100.64.0.1", "0.0.0.0",
		"::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1"} {
		if publicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s treated as public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "142.250.1.108", "2a00:1450:4001::6d"} {
		if !publicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s treated as private", s)
		}
	}
	prod := &netGuard{}
	for _, h := range []string{"localhost", "db.internal", "printer.local", "10.0.0.2", "imap.example.com:993", "user@host", ""} {
		if prod.validHost(h) == nil {
			t.Errorf("validHost(%q) accepted", h)
		}
	}
	if err := prod.validHost("imap.example.com"); err != nil {
		t.Error(err)
	}
	if err := (&netGuard{allowPrivate: true}).validHost("localhost"); err != nil {
		t.Error("development allows local hosts")
	}
	if cfg := prod.tlsConfig("imap.example.com"); cfg.MinVersion < 0x0303 || cfg.InsecureSkipVerify || cfg.ServerName != "imap.example.com" {
		t.Error("TLS config must verify and require 1.2+")
	}
}

func TestXOAUTH2(t *testing.T) {
	mech, ir, err := (&xoauth2{user: "a@b.test", token: "tok"}).Start()
	if err != nil || mech != "XOAUTH2" || string(ir) != "user=a@b.test\x01auth=Bearer tok\x01\x01" {
		t.Fatalf("%s %q %v", mech, ir, err)
	}
}

func TestSplitRecipient(t *testing.T) {
	tok, tag, dom, ok := splitRecipient(" <Acme-AB12+T42@In.Test> ")
	if !ok || tok != "acme-ab12" || tag != "t42" || dom != "in.test" {
		t.Fatalf("%q %q %q %v", tok, tag, dom, ok)
	}
	for _, bad := range []string{"", "nobody", "@in.test", "x@", "+t1@in.test"} {
		if _, _, _, ok := splitRecipient(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if n, ok := plusTicketNumber("t42"); !ok || n != 42 {
		t.Errorf("plusTicketNumber %d %v", n, ok)
	}
}

func TestSubjects(t *testing.T) {
	cases := map[string]string{
		"Re: RE: Fwd: Printer [#12]": "Printer",
		"AW: Drucker":                "Drucker",
		"  spaced   out ":            "spaced out",
	}
	for in, want := range cases {
		if got := baseSubject(in); got != want {
			t.Errorf("baseSubject(%q) = %q", in, got)
		}
	}
	if got := replySubject("Re: Printer [#12]", 12, true); got != "Re: Printer [#12]" {
		t.Errorf("replySubject %q", got)
	}
	if got := replySubject("", 3, false); got != "Your request [#3]" {
		t.Errorf("replySubject %q", got)
	}
	if n, ok := subjectTicketNumber("Re: help [#77]"); !ok || n != 77 {
		t.Errorf("subjectTicketNumber %d %v", n, ok)
	}
}

func TestStripQuotesText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Thanks, that worked.\n\nOn Sat, Oct 10, 2026 at 9:00 AM Acme <help@acme.test> wrote:\n> old", "Thanks, that worked."},
		{"Merci\n\nLe sam. 10 oct. 2026 à 09:00, Acme <help@acme.test> a écrit :\n> vieux", "Merci"},
		{"Danke\n\nAm 10.10.2026 um 09:00 schrieb Acme <help@acme.test>:\n> alt", "Danke"},
		{"Yes\n\n-----Original Message-----\nFrom: Acme\nSent: today", "Yes"},
		{"Ok\n________________________________\nFrom: Acme <help@acme.test>\nSent: Saturday", "Ok"},
		{"Got it\n\n-- \nCarla\nCEO", "Got it"},
		{"Sure thing\n\nSent from my iPhone", "Sure thing"},
		{"Fine\n> quoted\n> more", "Fine"},
		{"Line one\n> inline quote\nmy answer", "Line one\n> inline quote\nmy answer"},
	}
	for _, c := range cases {
		got, cut := stripQuotesText(c.in)
		if got != c.want {
			t.Errorf("stripQuotesText(%q) = %q", c.in, got)
		}
		if cut != (got != strings.TrimSpace(c.in)) {
			t.Errorf("cut flag wrong for %q", c.in)
		}
	}
	// Nothing left means nothing is stripped.
	if got, cut := stripQuotesText("> all quoted"); cut || got != "> all quoted" {
		t.Errorf("all-quoted: %q %v", got, cut)
	}
}

func TestRenderBodyKeepsDesign(t *testing.T) {
	pm := &parsedMail{Subject: "Your receipt", HTML: `<html><head><style>.card{border-radius:8px}</style></head>` +
		`<body bgcolor="#f6f9fc" style="font-family:Arial"><table class="card" width="600" align="center"><tr><td style="padding:24px">Receipt #1221</td></tr></table></body></html>`}
	body := renderBody(pm, nil)
	for _, want := range []string{`<style>.card{border-radius:8px}</style>`, `<div style="background-color: #f6f9fc; font-family: Arial">`, `width="600"`, `padding: 24px`} {
		if !strings.Contains(body.html, want) {
			t.Errorf("missing %q in %s", want, body.html)
		}
	}
	if body.text != "Receipt #1221" {
		t.Errorf("text %q", body.text)
	}
}

func TestBuildMIMEHeaderInjection(t *testing.T) {
	m := &outMessage{
		From:      mail.Address{Name: "Eve\r\nBcc: victim@x.test", Address: "help@acme.test"},
		To:        []string{"carla@customer.test"},
		Subject:   "Hi\r\nBcc: victim@x.test",
		MessageID: "<m_1.t1@acme.test>",
		HTML:      "<p>Hello</p>",
		Text:      "Hello",
		Bcc:       []string{"secret@acme.test"},
	}
	raw, err := buildMIME(m, false)
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("injected or leaked Bcc header: %q", line)
		}
	}
	if !strings.Contains(head, "Message-ID: <m_1.t1@acme.test>") || !strings.Contains(head, "multipart/alternative") {
		t.Fatalf("headers:\n%s", head)
	}
	raw, _ = buildMIME(m, true)
	if !strings.Contains(string(raw), "Bcc: <secret@acme.test>") {
		t.Fatal("Bcc missing when requested (Gmail API needs it in the MIME)")
	}
}

func TestKeptCopy(t *testing.T) {
	domain.Configure("http://api.test", testSecret) // as the test world does: attachment links are absolute
	id := "0b6f3c2e-8f4a-4d3b-9c1e-2a7d5e6f8a90"
	got := keptCopy(`<!doctype html><html><head><style>p{color:#111}</style></head><body><p onclick="x()">Hi<img src="cid:` + id + `@opendesk"></p><script>alert(1)</script><img src="cid:other@example.com"></body></html>`)
	for _, want := range []string{"<style>", "http://api.test/v1/attachments/" + id, "<p>Hi"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, bad := range []string{"<script", "onclick", "cid:" + id} {
		if strings.Contains(got, bad) {
			t.Errorf("kept %q in %s", bad, got)
		}
	}
	if keptCopy("  ") != "" {
		t.Error("nothing sent keeps nothing")
	}
}
