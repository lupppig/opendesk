package collab

import (
	"slices"
	"strings"
	"testing"
)

func src(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n") + "\r\n\r\nbody\r\n") }

var gmailInbound = src(
	"Delivered-To: matt@warmbly.com",
	"Received: by 2002:a05:7300:1234 with SMTP id abc;",
	"        Mon, 19 Oct 2026 05:06:00 -0700 (PDT)",
	"Authentication-Results: mx.google.com;",
	"       dkim=pass header.i=@ramp.com header.s=google header.b=Ab12;",
	"       spf=pass (google.com: domain of jade@bounce.ramp.com designates 1.2.3.4 as permitted sender) smtp.mailfrom=jade@bounce.ramp.com;",
	"       dmarc=pass (p=REJECT sp=REJECT dis=NONE) header.from=ramp.com",
	"Return-Path: <jade@bounce.ramp.com>",
	"Received: from mail-sor-f41.google.com (mail-sor-f41.google.com. [1.2.3.4])",
	"        by mx.google.com with SMTPS id x;",
	"        Mon, 19 Oct 2026 05:06:00 -0700 (PDT)",
	"From: Jade Neser <jade.neser@ramp.com>",
	"To: Matthew <matt@warmbly.com>",
	"Bcc: hidden@ramp.com",
	"Subject: =?UTF-8?Q?Caf=C3=A9_follow_up?=",
)

func TestDetailsGmailPass(t *testing.T) {
	d := messageDetails(gmailInbound, "google", "jade.neser@ramp.com", false)
	if d.SPF != "pass" || d.DKIM != "pass" || d.DMARC != "pass" || d.Warning != "" {
		t.Fatalf("checks = %q %q %q warning %q", d.SPF, d.DKIM, d.DMARC, d.Warning)
	}
	if d.MailedBy != "bounce.ramp.com" || !slices.Equal(d.SignedBy, []string{"ramp.com"}) || d.Via != "" {
		t.Fatalf("mailed %q signed %v via %q", d.MailedBy, d.SignedBy, d.Via)
	}
	if d.Encryption != "tls" {
		t.Fatalf("encryption = %q", d.Encryption)
	}
	if header(d.Headers, "Subject") != "Café follow up" {
		t.Fatalf("subject = %q", header(d.Headers, "Subject"))
	}
	if header(d.Headers, "Bcc") != "" {
		t.Fatal("an inbound message's Bcc header is not shown")
	}
	if !strings.Contains(header(d.Headers, "Authentication-Results"), "header.from=ramp.com") {
		t.Fatal("folded header not joined")
	}
}

func TestDetailsSpoofedAndVia(t *testing.T) {
	raw := src(
		"Authentication-Results: mx.google.com; dkim=pass header.d=sendgrid.net; spf=softfail smtp.mailfrom=bounces@sendgrid.net; dmarc=fail (p=NONE) header.from=paypal.com",
		"Received: from o1.sendgrid.net by mx.google.com with ESMTP id y",
		"From: PayPal <service@paypal.com>",
	)
	d := messageDetails(raw, "google", "service@paypal.com", false)
	if d.Warning != "spoofed" || d.Via != "sendgrid.net" || d.Encryption != "none" {
		t.Fatalf("warning %q via %q encryption %q", d.Warning, d.Via, d.Encryption)
	}
}

func TestDetailsUnverifiedMicrosoft(t *testing.T) {
	raw := src(
		"Authentication-Results: spf=fail (sender IP is 5.6.7.8) smtp.mailfrom=example.org; dkim=none (message not signed) header.d=none;dmarc=none action=none header.from=example.org;compauth=fail reason=001",
		"Received: from evil.example (5.6.7.8) by BN8NAM.outlook.com (10.0.0.1) with Microsoft SMTP Server (version=TLS1_2, cipher=X) id 15",
		"From: Billing <billing@example.org>",
	)
	d := messageDetails(raw, "microsoft", "billing@example.org", false)
	if d.Warning != "unverified" || d.SPF != "fail" || d.Encryption != "tls" || d.MailedBy != "example.org" {
		t.Fatalf("%+v", d)
	}
}

func TestDetailsForgedResultsDoNotCount(t *testing.T) {
	// The sender wrote a passing result into its own mail: it sits below every Received line.
	raw := src(
		"Received: from evil.example by mx.example.net with ESMTPS id z",
		"Authentication-Results: mx.example.net; spf=pass smtp.mailfrom=bank.com; dkim=pass header.d=bank.com; dmarc=pass header.from=bank.com",
		"From: Bank <alerts@bank.com>",
	)
	d := messageDetails(raw, "imap", "alerts@bank.com", false)
	if d.SPF != "" || len(d.SignedBy) != 0 || d.Warning != "" {
		t.Fatalf("forged results read as checks: %+v", d)
	}
	// On Gmail only mx.google.com's own counts.
	g := messageDetails(src(
		"Authentication-Results: evil.example; dkim=pass header.d=bank.com",
		"Received: from evil.example by mx.google.com with ESMTPS id z",
		"From: Bank <alerts@bank.com>",
	), "google", "alerts@bank.com", false)
	if len(g.SignedBy) != 0 {
		t.Fatalf("another server's result read as Gmail's: %+v", g)
	}
}

func TestDetailsSentMail(t *testing.T) {
	raw := src("From: Me <me@x.io>", "To: a@y.io", "Bcc: secret@y.io", "Subject: Hi")
	d := messageDetails(raw, "google", "me@x.io", true)
	if header(d.Headers, "Bcc") != "secret@y.io" || d.Warning != "" || d.Encryption != "" {
		t.Fatalf("%+v", d)
	}
}
