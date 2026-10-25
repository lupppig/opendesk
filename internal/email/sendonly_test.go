package email

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
)

// TestGoogleSendOnlyConnect: by default Google connects with gmail.send only, the
// mailbox receives by forwarding, and nothing reads the mailbox back.
func TestGoogleSendOnlyConnect(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	var reads atomic.Int32
	var uploaded []byte
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.URL.Path == "/userinfo":
			_, _ = rw.Write([]byte(`{"email":"desk@acme.test","email_verified":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/upload/gmail/v1/users/me/messages/send":
			uploaded = readAll(r)
			_, _ = rw.Write([]byte(`{"id":"g-9","threadId":"t-9"}`))
		case strings.HasPrefix(r.URL.Path, "/gmail/v1/users/me/"):
			// gmail.send cannot read messages or the profile.
			reads.Add(1)
			rw.WriteHeader(http.StatusForbidden)
		default:
			return false
		}
		return true
	}
	p := w.s.providers[KindGoogle]
	p.tokenURL, p.userinfoURL = f.srv.URL+"/token", f.srv.URL+"/userinfo"
	w.s.gmailBase = f.srv.URL
	srv := w.api(w.admin)

	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"name": "Desk"})
	if got := u.Query().Get("scope"); got != "openid email "+googleSendScope {
		t.Fatalf("send-only scope %q", got)
	}
	loc := callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect %s", loc)
	}
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	if m.InboundKind != InboundForwarding || m.OutboundKind != KindGoogle || m.Config.OAuthGrant != GrantSend ||
		m.Config.IMAPHost != "" || m.Config.OAuthEmail != "desk@acme.test" || m.ForwardingToken == "" {
		t.Fatalf("mailbox %+v %+v", m.Mailbox, m.Config)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", id.String()) != 0 {
		t.Fatal("a send-only mailbox must not sync")
	}
	var got Mailbox
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/"+id.String(), nil, &got); code != 200 ||
		got.Receiving != ReceivingOK || got.Config.OAuthGrant != GrantSend || !strings.HasSuffix(got.ForwardingAddress, "@in.test") {
		t.Fatalf("api %d %+v", code, got)
	}

	// A reply goes out through Gmail; the sent message is not read back.
	tk, _ := firstTicket(t, w, m, customer())
	mid := w.agentReply(tk.ID, "Sent only", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(uploaded, []byte("Sent only")) || bytes.Contains(uploaded, []byte("@in.test")) {
		t.Fatalf("upload %q", uploaded)
	}
	if msg := w.message(mid); msg.Delivery != "sent" || !strings.HasPrefix(*msg.MsgID, "<m_"+mid.String()) {
		t.Fatalf("delivery %s %v", msg.Delivery, *msg.MsgID)
	}
	// The connection check refreshes the token instead of reading the profile.
	before := len(f.tokenReq)
	var tr mailboxCheck
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/"+id.String()+"/test", nil, &tr); code != 200 || !tr.Inbound.OK || !tr.Outbound.OK {
		t.Fatalf("test: %d %+v", code, tr)
	}
	if len(f.tokenReq) != before+1 || reads.Load() != 0 {
		t.Fatalf("token requests %d, reads %d", len(f.tokenReq)-before, reads.Load())
	}

	// A sync connection on IMAP-on still reads the stamped Message-ID (see
	// TestSendMessageGmailAPI); reconnecting this one stays send-only.
	u, cookie = startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": id.String()})
	if !strings.Contains(u.Query().Get("scope"), googleSendScope) || strings.Contains(u.Query().Get("scope"), googleIMAPScope) {
		t.Fatalf("reconnect scope %q", u.Query().Get("scope"))
	}
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-2", cookie)
	m, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	if m.InboundKind != InboundForwarding || m.Config.OAuthGrant != GrantSend {
		t.Fatalf("reconnected %+v", m.Mailbox)
	}
}

// TestMicrosoftSendOnlyConnect: a send-only Microsoft connection redeems the code for
// the Graph token alone and never asks for the IMAP resource.
func TestMicrosoftSendOnlyConnect(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/me" {
			if r.Header.Get("Authorization") != "Bearer access-2-1" {
				rw.WriteHeader(401)
				return true
			}
			_, _ = rw.Write([]byte(`{"mail":"ops@acme.test","userPrincipalName":"ops@acme.onmicrosoft.com"}`))
			return true
		}
		return false
	}
	w.s.providers[KindMicrosoft].tokenURL = f.srv.URL + "/token"
	w.s.graphBase = f.srv.URL
	srv := w.api(w.admin)

	u, cookie := startOAuth(t, srv.URL, "microsoft", map[string]any{"name": "Ops", "receive": "forwarding"})
	scope := u.Query().Get("scope")
	if strings.Contains(scope, msIMAPScope) || !strings.Contains(scope, msSendScope) || !strings.Contains(scope, msUserScope) {
		t.Fatalf("authorize scope %q", scope)
	}
	loc := callback(t, srv.URL, "microsoft", u.Query().Get("state"), "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect %s", loc)
	}
	if len(f.tokenReq) != 1 || f.tokenReq[0]["grant_type"] != "authorization_code" ||
		f.tokenReq[0]["scope"] != "openid email offline_access "+msSendScope+" "+msUserScope {
		t.Fatalf("token requests %v", f.tokenReq)
	}
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	c, _ := w.s.openCreds(m)
	if m.InboundKind != InboundForwarding || m.OutboundKind != KindMicrosoft || m.Config.OAuthGrant != GrantSend || m.Config.IMAPHost != "" {
		t.Fatalf("mailbox %+v", m.Mailbox)
	}
	if c.AccessToken != "" || c.GraphAccessToken != "access-2-1" || c.RefreshToken != "refresh-1" {
		t.Fatalf("tokens imap=%q graph=%q refresh=%q", c.AccessToken, c.GraphAccessToken, c.RefreshToken)
	}
	// Refreshing asks for the Graph resource only; there is no IMAP token to ask for.
	if _, err := w.s.accessToken(w.ctx, m, resIMAP); err == nil {
		t.Fatal("a send grant handed out an IMAP token")
	}
	if err := w.s.expireToken(w.ctx, m, resGraph); err != nil {
		t.Fatal(err)
	}
	if tok, err := w.s.accessToken(w.ctx, m, resGraph); err != nil || tok != "access-2-2" {
		t.Fatalf("graph refresh: %q %v", tok, err)
	}
	if f.tokenReq[1]["grant_type"] != "refresh_token" || f.tokenReq[1]["scope"] != "offline_access "+msSendScope+" "+msUserScope {
		t.Fatalf("refresh %v", f.tokenReq[1])
	}
}

// TestIMAPOff: with MAILBOX_IMAP off nothing new receives over IMAP, existing IMAP
// mailboxes are kept but never dialled, and Google connects send-only.
func TestIMAPOff(t *testing.T) {
	w := newWorld(t)
	w.s.mailboxIMAP = false
	srv := w.api(w.admin)

	// A listener that counts every connection stands in for the IMAP server.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var dials atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			_ = c.Close()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port

	var opts connectOptions
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/connect-options", nil, &opts); code != 200 || opts.MailboxIMAP {
		t.Fatalf("connect options: %d %+v", code, opts)
	}
	for _, kind := range []string{"imap", "google", "microsoft"} {
		r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "X", "email": kind + "@acme.test",
			"inbound_kind": kind, "outbound_kind": "system", "password": "pw", "config": map[string]any{"imap_host": "127.0.0.1", "imap_port": port}})
		if r.code != 422 || !strings.Contains(r.body, `"imap_off"`) || !strings.Contains(r.body, "Forward the address to opendesk instead.") {
			t.Fatalf("create %s: %d %s", kind, r.code, r.body)
		}
	}
	if r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/oauth/google/start", map[string]any{"receive": "sync"}); r.code != 422 || !strings.Contains(r.body, `"imap_off"`) {
		t.Fatalf("start sync: %d %s", r.code, r.body)
	}
	u, _ := startOAuth(t, srv.URL, "google", map[string]any{})
	if u.Query().Get("scope") != "openid email "+googleSendScope {
		t.Fatalf("start scope %q", u.Query().Get("scope"))
	}

	// An existing IMAP mailbox is reported, not polled and not dialled.
	box := w.mailbox(InboundIMAP, OutboundSMTP, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: port, IMAPTLS: TLSImplicit, IMAPUsername: "x",
			SMTPHost: "smtp.acme.test", SMTPPort: 587, SMTPTLS: TLSStart, SMTPUsername: "x"}
		c.Password = "shared-pw"
	})
	var got Mailbox
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/"+box.ID.String(), nil, &got); code != 200 || got.Receiving != ReceivingIMAPOff {
		t.Fatalf("receiving %d %q", code, got.Receiving)
	}
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", box.ID.String()) != 0 {
		t.Fatal("the scheduler queued an IMAP mailbox")
	}
	if err := w.s.syncMailbox(w.ctx, box.ID); err != nil {
		t.Fatal(err)
	}
	var check mailboxCheck
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/"+box.ID.String()+"/test", nil, &check); code != 200 ||
		check.Inbound.OK || !strings.Contains(check.Inbound.Error, "IMAP is off") {
		t.Fatalf("test: %d %+v", code, check)
	}
	if dials.Load() != 0 {
		t.Fatalf("dialled the IMAP server %d times", dials.Load())
	}
	if after, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, box.ID); after.Status != "active" || after.LastError != "" {
		t.Fatalf("an unpolled mailbox is not in error: %s %q", after.Status, after.LastError)
	}

	// It can still be edited, cannot move back to IMAP once forwarding, and switches to
	// forwarding keeping how it sends.
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+box.ID.String(), map[string]any{"name": "Renamed"}, nil); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	got = Mailbox{}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+box.ID.String(), map[string]any{"inbound_kind": "forwarding"}, &got); code != 200 ||
		got.InboundKind != InboundForwarding || got.OutboundKind != OutboundSMTP || got.Receiving != ReceivingOK || got.Config.IMAPHost != "" {
		t.Fatalf("to forwarding: %d %+v", code, got)
	}
	row, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, box.ID)
	c, _ := w.s.openCreds(row)
	if c.Password != "" || c.SMTPPassword != "shared-pw" {
		t.Fatalf("passwords after the switch: imap %q smtp %q", c.Password, c.SMTPPassword)
	}
	var conn string
	_ = w.d.DB.Pool.QueryRow(w.ctx, `SELECT data->'connection'->>'inbound_kind' FROM audit_logs WHERE entity_id = $1 AND action = 'mailbox.updated'
		AND data->'changes' ? 'inbound_kind'`, box.ID).Scan(&conn)
	if conn != InboundForwarding {
		t.Fatalf("audit connection %q", conn)
	}
	r := rawJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+box.ID.String(), map[string]any{"inbound_kind": "imap", "password": "pw",
		"config": map[string]any{"imap_host": "127.0.0.1", "imap_port": port}})
	if r.code != 422 || !strings.Contains(r.body, `"imap_off"`) {
		t.Fatalf("back to imap: %d %s", r.code, r.body)
	}
	if dials.Load() != 0 {
		t.Fatalf("dialled the IMAP server %d times", dials.Load())
	}
}

// TestIMAPOffReconnect: reconnecting a Google sync mailbox while IMAP is off converts
// it to forwarding with a send grant.
func TestIMAPOffReconnect(t *testing.T) {
	w := newWorld(t)
	w.s.mailboxIMAP = false
	f := newFakeProvider(t)
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/userinfo" {
			_, _ = rw.Write([]byte(`{"email":"sync@acme.test","email_verified":true}`))
			return true
		}
		return false
	}
	p := w.s.providers[KindGoogle]
	p.tokenURL, p.userinfoURL = f.srv.URL+"/token", f.srv.URL+"/userinfo"
	srv := w.api(w.admin)
	g := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Email = "sync@acme.test"
		m.Config = MailboxConfig{IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: TLSImplicit, OAuthEmail: "sync@acme.test"}
		c.RefreshToken = "refresh-0"
	})
	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": g.ID.String()})
	if u.Query().Get("scope") != "openid email "+googleSendScope {
		t.Fatalf("reconnect scope %q", u.Query().Get("scope"))
	}
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, g.ID)
	if m.InboundKind != InboundForwarding || m.OutboundKind != KindGoogle || m.Config.OAuthGrant != GrantSend || m.Config.IMAPHost != "" {
		t.Fatalf("reconnected %+v %+v", m.Mailbox, m.Config)
	}
}

// TestPatchToForwarding: with IMAP on, a Google sync mailbox moves to forwarding and
// keeps sending through Google; once it stops sending through Google the grant goes.
func TestPatchToForwarding(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.admin)
	g := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: TLSImplicit, IMAPUsername: "g@acme.test", OAuthEmail: "g@acme.test"}
		c.RefreshToken, c.AccessToken, c.Expiry = "refresh-0", "cached", time.Now().Add(time.Hour)
	})
	mustExec(t, w.d, `UPDATE mailboxes SET status = 'error', last_error = 'IMAP refused' WHERE id = $1`, g.ID)
	var got Mailbox
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+g.ID.String(), map[string]any{"inbound_kind": "forwarding"}, &got); code != 200 ||
		got.InboundKind != InboundForwarding || got.OutboundKind != KindGoogle || got.Config.OAuthGrant != GrantSync ||
		got.Config.IMAPHost != "" || got.Status != "active" || got.LastError != "" {
		t.Fatalf("to forwarding: %d %+v", code, got)
	}
	row, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, g.ID)
	if c, _ := w.s.openCreds(row); c.RefreshToken != "refresh-0" {
		t.Fatal("the grant it still sends with was dropped")
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", g.ID.String()) != 0 {
		t.Fatal("a forwarding mailbox was queued to sync")
	}
	got = Mailbox{}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+g.ID.String(), map[string]any{"outbound_kind": "system"}, &got); code != 200 ||
		got.Config.OAuthGrant != "" || got.Config.OAuthEmail != "" {
		t.Fatalf("to system: %d %+v", code, got.Config)
	}
	row, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, g.ID)
	if c, _ := w.s.openCreds(row); c.RefreshToken != "" || c.AccessToken != "" {
		t.Fatal("an unused grant was kept")
	}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+g.ID.String(), map[string]any{"outbound_kind": "google"}, nil); code != 422 {
		t.Fatalf("google without a grant: %d", code)
	}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+g.ID.String(), map[string]any{"inbound_kind": "google"}, nil); code != 422 {
		t.Fatalf("onto google by PATCH: %d", code)
	}
	// A forwarding mailbox may still move to IMAP while it is on.
	got = Mailbox{}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+g.ID.String(), map[string]any{"inbound_kind": "imap", "password": "pw",
		"config": map[string]any{"imap_host": "imap.acme.test"}}, &got); code != 200 || got.InboundKind != InboundIMAP || got.Config.IMAPPort != 993 {
		t.Fatalf("to imap: %d %+v", code, got)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", g.ID.String()) != 1 {
		t.Fatal("moving to IMAP did not queue a sync")
	}
}

func readAll(r *http.Request) []byte {
	var b bytes.Buffer
	_, _ = b.ReadFrom(r.Body)
	return b.Bytes()
}

// TestConnectProviderToForwardingMailbox: connecting Google to send for a forwarding
// mailbox keeps its address, even when the account signed in is another one.
func TestConnectProviderToForwardingMailbox(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/userinfo" {
			_, _ = rw.Write([]byte(`{"email":"jane@acme.test","email_verified":true}`))
			return true
		}
		return false
	}
	p := w.s.providers[KindGoogle]
	p.tokenURL, p.userinfoURL = f.srv.URL+"/token", f.srv.URL+"/userinfo"
	srv := w.api(w.admin)
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) { m.Email = "support@acme.test" })

	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": m.ID.String()})
	if got := u.Query().Get("scope"); got != "openid email "+googleSendScope {
		t.Fatalf("scope %q", got)
	}
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	got, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if got.Email != "support@acme.test" || got.InboundKind != InboundForwarding || got.OutboundKind != KindGoogle ||
		got.Config.OAuthEmail != "jane@acme.test" || got.Config.OAuthGrant != GrantSend {
		t.Fatalf("mailbox %+v %+v", got.Mailbox, got.Config)
	}
}

// TestSendOnlyMailboxStartsReading: a forwarding mailbox that sends through Google starts
// reading its inbox over IMAP when its page asks (receive sync on a reconnect). It stays the
// same mailbox, with its address and its tickets; a plain reconnect stays send-only
// (TestGoogleSendOnlyConnect).
func TestSendOnlyMailboxStartsReading(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/userinfo" {
			_, _ = rw.Write([]byte(`{"email":"support@acme.test","email_verified":true}`))
			return true
		}
		return false
	}
	p := w.s.providers[KindGoogle]
	p.tokenURL, p.userinfoURL = f.srv.URL+"/token", f.srv.URL+"/userinfo"
	w.s.gmailBase = f.srv.URL
	srv := w.api(w.admin)
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) { m.Email = "support@acme.test" })
	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": m.ID.String()})
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	tk, _ := firstTicket(t, w, m, customer())

	u, cookie = startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": m.ID.String(), "receive": "sync", "sync_start": "new"})
	if !strings.Contains(u.Query().Get("scope"), googleIMAPScope) {
		t.Fatalf("scope %q", u.Query().Get("scope"))
	}
	loc := callback(t, srv.URL, "google", u.Query().Get("state"), "code-2", cookie)
	if loc.Query().Get("error") != "" {
		t.Fatalf("redirect %s", loc)
	}
	got, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if got.Email != "support@acme.test" || got.InboundKind != KindGoogle || got.OutboundKind != KindGoogle ||
		got.Config.OAuthGrant != GrantSync || got.Config.IMAPHost != "imap.gmail.com" || got.Config.SyncStart != "new" {
		t.Fatalf("mailbox %+v %+v", got.Mailbox, got.Config)
	}
	if n := w.count(`SELECT count(*) FROM mailboxes WHERE workspace_id = $1`, w.wid); n != 1 {
		t.Fatalf("%d mailboxes, want the same one", n)
	}
	if n := w.count(`SELECT count(*) FROM tickets WHERE id = $1 AND mailbox_id = $2`, tk.ID, m.ID); n != 1 {
		t.Fatal("the ticket left the mailbox")
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", m.ID.String()) != 1 {
		t.Fatal("no sync queued")
	}
}

// TestSystemSendOff: with MAILBOX_SYSTEM_SEND off no mailbox starts sending through the
// platform mailer, and one that already does sends nothing until it connects a way to send.
func TestSystemSendOff(t *testing.T) {
	w := newWorld(t)
	w.s.systemSend = false
	domain.ConfigureSending(false)
	t.Cleanup(func() { domain.ConfigureSending(true) })
	srv := w.api(w.admin)
	sends := 0
	w.s.rawSend = func(context.Context, string, []string, []byte) error { sends++; return nil }

	var opts connectOptions
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/connect-options", nil, &opts); code != 200 || opts.SystemSend {
		t.Fatalf("connect options: %d %+v", code, opts)
	}
	r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "X", "email": "new@acme.test",
		"inbound_kind": "forwarding", "outbound_kind": "system"})
	if r.code != 422 || !strings.Contains(r.body, `"system_send_off"`) {
		t.Fatalf("create: %d %s", r.code, r.body)
	}

	// An existing one is reported and can still be edited.
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	var got Mailbox
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+m.ID.String(), map[string]any{"name": "Renamed"}, &got); code != 200 ||
		got.Sending != SendingSystemOff {
		t.Fatalf("rename: %d %q", code, got.Sending)
	}

	// Its replies are refused before anything is stored, and nothing automatic goes out.
	in := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", customer(), "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8)))
	tk := w.ticket(w.messageFor(in).TicketID)
	before := w.count(`SELECT count(*) FROM messages WHERE ticket_id = $1`, tk.ID)
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		_, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk.ID, Kind: "reply",
			Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, BodyHTML: "<p>Hi</p>", BodyText: "Hi", DeliveryStatus: "pending"})
		return err
	})
	if !errors.Is(err, domain.ErrCannotSend) {
		t.Fatalf("reply: %v", err)
	}
	if n := w.count(`SELECT count(*) FROM messages WHERE ticket_id = $1`, tk.ID); n != before {
		t.Fatalf("messages %d, want %d", n, before)
	}
	if err := w.s.sendTicketEmail(w.ctx, jobsSendTicketEmail(w.wid, tk.ID, "Got it", "<p>Got it</p>")); err != nil || sends != 0 {
		t.Fatalf("auto-reply: %v, %d sent", err, sends)
	}

	// A reply queued before the switch fails with the reason, unsent.
	domain.ConfigureSending(true)
	mid := w.agentReply(tk.ID, "Queued", "")
	domain.ConfigureSending(false)
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err == nil {
		t.Fatal("sent through the platform mailer")
	}
	if msg := w.message(mid); msg.Delivery != "failed" || !strings.Contains(msg.DelivError, "Connect Google or Microsoft, or add SMTP") || sends != 0 {
		t.Fatalf("queued reply: %s %q, %d sent", msg.Delivery, msg.DelivError, sends)
	}

	// Moving to SMTP sends again; moving back is refused.
	got = Mailbox{}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+m.ID.String(), map[string]any{"outbound_kind": "smtp", "smtp_password": "pw",
		"config": map[string]any{"smtp_host": "smtp.acme.test", "smtp_port": 587}}, &got); code != 200 || got.Sending != SendingOK {
		t.Fatalf("to smtp: %d %+v", code, got)
	}
	r = rawJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+m.ID.String(), map[string]any{"outbound_kind": "system"})
	if r.code != 422 || !strings.Contains(r.body, `"system_send_off"`) {
		t.Fatalf("back to system: %d %s", r.code, r.body)
	}
}
