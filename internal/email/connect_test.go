package email

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/email/mailcause"
)

type checkBody struct {
	Code     string            `json:"code"`
	Detail   string            `json:"detail"`
	Inbound  mailboxTestResult `json:"inbound"`
	Outbound mailboxTestResult `json:"outbound"`
	Cause    *mailcause.Cause  `json:"cause"`
	SMTPPort int               `json:"smtp_port"`
	Config   MailboxConfig     `json:"config"`
	ID       string            `json:"id"`
}

// TestCreateVerify: a create with verify signs in first; a refusal names a cause,
// never echoes the password, and stores nothing.
func TestCreateVerify(t *testing.T) {
	w := newWorld(t)
	_, imapPort := imapServer(t, w)
	srv := w.api(w.admin)
	body := func(pass string) map[string]any {
		return map[string]any{"name": "Support", "email": "agent@acme.test", "inbound_kind": "imap", "outbound_kind": "system", "verify": true,
			"password": pass, "config": map[string]any{
				"imap_host": "127.0.0.1", "imap_port": imapPort, "imap_tls": "tls", "imap_username": "agent@acme.test"}}
	}
	var out checkBody
	code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", body("Wrong-Passw0rd"), &out)
	if code != 422 || out.Code != "mailbox_connection_failed" || out.Inbound.OK || !out.Outbound.OK || out.Cause == nil ||
		out.Cause.Key != mailcause.AuthRefused || !strings.Contains(out.Detail, "Nothing was saved") {
		t.Fatalf("refused: %d %+v", code, out)
	}
	if strings.Contains(out.Inbound.Error+out.Inbound.Detail+out.Detail, "Wrong-Passw0rd") {
		t.Fatal("password echoed")
	}
	if w.count(`SELECT count(*) FROM mailboxes WHERE workspace_id = $1`, w.wid) != 0 {
		t.Fatal("a refused check stored the mailbox")
	}
	// Surrounding whitespace is trimmed from a password.
	out = checkBody{}
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", body("  imap-pass-1 "), &out); code != 201 {
		t.Fatalf("create: %d %+v", code, out)
	}
	if out.Config.MailHost != "other" {
		t.Fatalf("mail_host %q", out.Config.MailHost)
	}

	// Forwarding in, SMTP out: only the SMTP server is checked.
	_, smtpPort := startSink(t, w)
	fwd := func(pass string) map[string]any {
		return map[string]any{"name": "Sales", "email": "sales@acme.test", "inbound_kind": "forwarding", "outbound_kind": "smtp", "verify": true,
			"password": pass, "config": map[string]any{"smtp_host": "127.0.0.1", "smtp_port": smtpPort, "smtp_tls": "starttls",
				"smtp_username": "support@acme.test", "mail_host": "microsoft365"}}
	}
	out = checkBody{}
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", fwd("nope-nope"), &out); code != 422 || out.Outbound.OK || out.Cause == nil {
		t.Fatalf("smtp refused: %d %+v", code, out)
	}
	out = checkBody{}
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", fwd("s3cret-pass"), &out); code != 201 || out.Config.MailHost != "microsoft365" {
		t.Fatalf("smtp create: %d %+v", code, out)
	}
}

// TestSMTPFallbackPort: when the implicit-TLS port is silent, STARTTLS on the
// submission port of the same server is tried and kept.
func TestSMTPFallbackPort(t *testing.T) {
	w := newWorld(t)
	_, smtpPort := startSink(t, w)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	prev := smtpFallback
	smtpFallback.from, smtpFallback.to = closed, smtpPort
	t.Cleanup(func() { smtpFallback = prev })

	m := w.mailbox(InboundForwarding, OutboundSMTP, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{SMTPHost: "127.0.0.1", SMTPPort: closed, SMTPTLS: TLSImplicit, SMTPUsername: "support@acme.test"}
		c.Password = "s3cret-pass"
	})
	var out checkBody
	if code := doJSON(t, http.MethodPost, w.api(w.admin).URL+"/v1/mailboxes/"+m.ID.String()+"/test", nil, &out); code != 200 ||
		!out.Outbound.OK || out.SMTPPort != smtpPort {
		t.Fatalf("fallback: %d %+v", code, out)
	}
	got, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if got.Config.SMTPPort != smtpPort || got.Config.SMTPTLS != TLSStart {
		t.Fatalf("port not kept: %+v", got.Config)
	}
}

func TestCleanPassword(t *testing.T) {
	cases := []struct {
		google bool
		in     string
		want   string
	}{
		{true, "abcd efgh ijkl mnop", "abcdefghijklmnop"},
		{true, " abcd efgh ijkl mnop\n", "abcdefghijklmnop"},
		{true, " my pass word ", "my pass word"},
		{false, "abcd efgh ijkl mnop", "abcd efgh ijkl mnop"},
		{false, "  hunter 2\t", "hunter 2"},
	}
	for _, c := range cases {
		if got := cleanPassword(c.google, c.in); got != c.want {
			t.Errorf("cleanPassword(%v, %q) = %q, want %q", c.google, c.in, got, c.want)
		}
	}
	m := &mailboxRow{}
	m.Email, m.InboundKind, m.OutboundKind = "help@acme.io", InboundIMAP, OutboundSMTP
	m.Config = MailboxConfig{IMAPHost: "imap.gmail.com", SMTPHost: "smtp.gmail.com"}
	creds := credentials{Password: "abcd efgh ijkl mnop"}
	prepareMailbox(m, &creds, true)
	if m.Config.MailHost != "google_workspace" || creds.Password != "abcdefghijklmnop" {
		t.Fatalf("prepare: %q %q", m.Config.MailHost, creds.Password)
	}
}

// TestMailboxOAuthSwitch: with a provider's mailbox sign-in off, new connections are
// refused and reconnecting a mailbox that already uses it is not.
func TestMailboxOAuthSwitch(t *testing.T) {
	w := newWorld(t)
	d := *w.d
	cfg := *w.d.Cfg
	cfg.GoogleMailboxOAuth = false
	d.Cfg = &cfg
	w.s = newService(&d)
	srv := w.api(w.admin)

	var opts connectOptions
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/connect-options", nil, &opts); code != 200 ||
		!opts.Google.Configured || opts.Google.OAuth || !opts.Microsoft.OAuth || opts.Forwarding.Domain != "in.test" ||
		opts.SystemSender != "notifications@opendesk.test" {
		t.Fatalf("connect options: %d %+v", code, opts)
	}
	r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/oauth/google/start", map[string]any{"name": "Gmail"})
	if r.code != 403 || !strings.Contains(r.body, "mailbox_oauth_disabled") || !strings.Contains(r.body, "app password") {
		t.Fatalf("new google connect: %d %s", r.code, r.body)
	}
	g := w.mailbox(KindGoogle, KindGoogle, nil)
	if r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/oauth/google/start", map[string]any{"mailbox_id": g.ID.String()}); r.code != 200 {
		t.Fatalf("reconnect: %d %s", r.code, r.body)
	}
	imapBox := w.mailbox(InboundIMAP, OutboundSystem, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: TLSImplicit, IMAPUsername: "x"}
		c.Password = "x"
	})
	if r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/oauth/google/start", map[string]any{"mailbox_id": imapBox.ID.String()}); r.code != 403 {
		t.Fatalf("moving a mailbox onto a disabled sign-in: %d %s", r.code, r.body)
	}
	if r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/oauth/microsoft/start", map[string]any{"name": "Outlook"}); r.code != 200 {
		t.Fatalf("microsoft stays on: %d %s", r.code, r.body)
	}
}

func TestDetectRoute(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.admin)
	var d detection
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/detect", map[string]any{"email": "Help@Gmail.com"}, &d); code != 200 ||
		d.Host != "gmail" || d.Label != "Gmail" || d.Source != "known" || d.PasswordAuth != "app_password" ||
		d.AppPasswordURL != "https://myaccount.google.com/apppasswords" || d.OAuth == nil || *d.OAuth != "google" ||
		d.Settings == nil || d.Settings.IMAP.Host != "imap.gmail.com" {
		t.Fatalf("detect: %d %+v", code, d)
	}
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/detect", map[string]any{"email": "nope"}, nil); code != 422 {
		t.Fatalf("invalid address: %d", code)
	}
	limited := false
	for i := 0; i < detectPerMinute+2; i++ {
		if doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/detect", map[string]any{"email": "a@yahoo.com"}, nil) == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("detect is not rate limited")
	}
	agent := *w.admin
	agent.Role = "agent"
	if code := doJSON(t, http.MethodPost, w.api(&agent).URL+"/v1/mailboxes/detect", map[string]any{"email": "a@gmail.com"}, nil); code != 403 {
		t.Fatalf("agent: %d", code)
	}
}

// TestGuardHTTPClient: autoconfig fetches refuse private targets and unsafe redirects.
func TestGuardHTTPClient(t *testing.T) {
	c := (&netGuard{}).httpClient()
	for _, u := range []string{"https://127.0.0.1/", "https://10.1.2.3/", "https://[::1]/", "https://169.254.169.254/"} {
		resp, err := c.Get(u)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil || !errors.Is(err, errBlockedAddress) {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, dest := range []string{"http://example.com/", "https://example.com:8443/", "https://u:p@example.com/"} {
		u, _ := url.Parse(dest)
		if c.CheckRedirect(&http.Request{URL: u}, []*http.Request{{}}) == nil {
			t.Errorf("redirect to %s accepted", dest)
		}
	}
	u, _ := url.Parse("https://example.com/x")
	if c.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, 3)) == nil {
		t.Error("redirect cap")
	}
}

// TestUpdateSending: a forwarding mailbox moves its replies to its own SMTP server
// and back. With verify the server is signed in to first and a refusal stores
// nothing; the SMTP password is write-only, kept when left out and dropped when
// replies go back through opendesk; the host guard and the audit log apply.
func TestUpdateSending(t *testing.T) {
	w := newWorld(t)
	_, smtpPort := startSink(t, w)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	srv := w.api(w.admin)
	url := srv.URL + "/v1/mailboxes/" + m.ID.String()
	smtp := func(pass string) map[string]any {
		b := map[string]any{"outbound_kind": "smtp", "verify": true, "config": map[string]any{
			"smtp_host": "127.0.0.1", "smtp_port": smtpPort, "smtp_tls": "starttls", "smtp_username": "support@acme.test"}}
		if pass != "" {
			b["smtp_password"] = pass
		}
		return b
	}
	stored := func() (*mailboxRow, credentials) {
		got, err := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		c, err := w.s.openCreds(got)
		if err != nil {
			t.Fatal(err)
		}
		return got, c
	}

	// No password: nothing to sign in with.
	var out checkBody
	if code := doJSON(t, http.MethodPatch, url, smtp(""), &out); code != 422 {
		t.Fatalf("no password: %d %+v", code, out)
	}
	// A refused sign-in names a cause and stores nothing.
	out = checkBody{}
	code := doJSON(t, http.MethodPatch, url, smtp("nope-nope"), &out)
	if code != 422 || out.Code != "mailbox_connection_failed" || out.Outbound.OK || !out.Inbound.OK || out.Cause == nil ||
		!strings.Contains(out.Detail, "Nothing was saved") || strings.Contains(out.Outbound.Detail+out.Detail, "nope-nope") {
		t.Fatalf("refused: %d %+v", code, out)
	}
	if got, _ := stored(); got.OutboundKind != OutboundSystem || got.Config.SMTPHost != "" {
		t.Fatalf("a refused check stored the change: %s %+v", got.OutboundKind, got.Config)
	}
	// Signed in: stored, audited with the server but never the password.
	out = checkBody{}
	if code := doJSON(t, http.MethodPatch, url, smtp("s3cret-pass"), &out); code != 200 {
		t.Fatalf("smtp: %d %+v", code, out)
	}
	got, creds := stored()
	if got.OutboundKind != OutboundSMTP || got.Config.SMTPHost != "127.0.0.1" || got.Config.SMTPPort != smtpPort || creds.smtpPassword() != "s3cret-pass" {
		t.Fatalf("not stored: %s %+v", got.OutboundKind, got.Config)
	}
	var audit string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT data::text FROM audit_logs WHERE entity_id = $1 AND action = 'mailbox.updated'
		ORDER BY created_at DESC LIMIT 1`, m.ID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit, `"outbound_kind": "smtp"`) || !strings.Contains(audit, `"verified": true`) || !strings.Contains(audit, "127.0.0.1") ||
		strings.Contains(audit, "s3cret-pass") {
		t.Fatalf("audit: %s", audit)
	}
	if w.eventCount("mailbox.updated") == 0 {
		t.Fatal("no mailbox.updated event")
	}
	// Leaving the password out keeps the stored one, and the check uses it.
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"verify": true, "config": map[string]any{"smtp_username": "support@acme.test", "smtp_port": smtpPort}}, nil); code != 200 {
		t.Fatalf("keep password: %d", code)
	}
	// A change that does not touch the servers checks nothing.
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"verify": true, "name": "Sales"}, nil); code != 200 {
		t.Fatalf("rename: %d", code)
	}

	// The host guard applies to an edit as it does to a create.
	guard := w.s.guard
	w.s.guard = &netGuard{allowPrivate: false}
	for _, host := range []string{"10.0.0.5", "localhost", "169.254.169.254"} {
		b := map[string]any{"config": map[string]any{"smtp_host": host}}
		if code := doJSON(t, http.MethodPatch, url, b, nil); code != 422 {
			t.Errorf("host %q accepted (%d)", host, code)
		}
	}
	w.s.guard = guard

	// Back through opendesk: the SMTP settings and password are dropped.
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"outbound_kind": "system", "verify": true}, nil); code != 200 {
		t.Fatalf("system: %d", code)
	}
	got, creds = stored()
	if got.OutboundKind != OutboundSystem || got.Config.SMTPHost != "" || creds.Password != "" || creds.SMTPPassword != "" {
		t.Fatalf("system kept SMTP: %+v", got.Config)
	}
	// So moving to SMTP again needs the password.
	b := smtp("")
	b["verify"] = false
	if code := doJSON(t, http.MethodPatch, url, b, nil); code != 422 {
		t.Fatalf("smtp without a stored password: %d", code)
	}
}

// TestUpdateVerifyIMAP: a new IMAP password is signed in with before it is stored.
func TestUpdateVerifyIMAP(t *testing.T) {
	w := newWorld(t)
	_, imapPort := imapServer(t, w)
	m := w.mailbox(InboundIMAP, OutboundSystem, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: imapPort, IMAPTLS: TLSImplicit, IMAPUsername: "agent@acme.test"}
		c.Password = "old-pass"
	})
	url := w.api(w.admin).URL + "/v1/mailboxes/" + m.ID.String()
	var out checkBody
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"password": "Wrong-Passw0rd", "verify": true}, &out); code != 422 ||
		out.Inbound.OK || !out.Outbound.OK || out.Cause == nil || out.Cause.Key != mailcause.AuthRefused {
		t.Fatalf("refused: %d %+v", code, out)
	}
	got, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if c, _ := w.s.openCreds(got); c.Password != "old-pass" {
		t.Fatal("a refused check stored the password")
	}
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"password": " imap-pass-1 ", "verify": true}, &out); code != 200 {
		t.Fatalf("accepted: %d %+v", code, out)
	}
	got, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if c, _ := w.s.openCreds(got); c.Password != "imap-pass-1" {
		t.Fatal("password not stored")
	}
}
