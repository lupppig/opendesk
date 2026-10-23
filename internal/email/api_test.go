package email

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/ids"
)

func TestMailboxCRUD(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.admin)

	var fwd Mailbox
	code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "Support", "email": "Help@Acme.test",
		"display_name": "Acme Help", "inbound_kind": "forwarding", "outbound_kind": "system",
		"signature_html": `<p onclick="x()">Thanks <script>alert(1)</script></p>`}, &fwd)
	if code != http.StatusCreated {
		t.Fatalf("create forwarding: %d", code)
	}
	if fwd.Email != "help@acme.test" || !strings.HasPrefix(fwd.ForwardingAddress, "acme-") || !strings.HasSuffix(fwd.ForwardingAddress, "@in.test") {
		t.Fatalf("mailbox %+v", fwd)
	}
	if strings.Contains(fwd.SignatureHTML, "script") || strings.Contains(fwd.SignatureHTML, "onclick") {
		t.Fatalf("signature not sanitized: %q", fwd.SignatureHTML)
	}
	if w.eventCount("mailbox.created") != 1 || w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.connected'`, w.wid) != 1 {
		t.Fatal("event or audit missing")
	}
	// Same address again conflicts.
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "Dup", "email": "help@acme.test",
		"inbound_kind": "forwarding", "outbound_kind": "system"}, nil); code != http.StatusConflict {
		t.Fatalf("duplicate: %d", code)
	}
	// IMAP needs a password; passwords are never returned.
	imapBody := map[string]any{"name": "Sales", "email": "sales@acme.test", "inbound_kind": "imap", "outbound_kind": "smtp",
		"config": map[string]any{"imap_host": "imap.acme.test", "smtp_host": "smtp.acme.test", "smtp_port": 587}}
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", imapBody, nil); code != 422 {
		t.Fatalf("imap without password: %d", code)
	}
	imapBody["password"] = "imap-secret-123"
	resp := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", imapBody)
	if resp.code != http.StatusCreated || strings.Contains(resp.body, "imap-secret-123") || strings.Contains(resp.body, "password") {
		t.Fatalf("create imap: %d %s", resp.code, resp.body)
	}
	var imapBox Mailbox
	_ = json.Unmarshal([]byte(resp.body), &imapBox)
	if imapBox.Config.IMAPPort != 993 || imapBox.Config.IMAPTLS != "tls" || imapBox.Config.SMTPTLS != "starttls" || imapBox.Config.IMAPUsername != "sales@acme.test" {
		t.Fatalf("defaults %+v", imapBox.Config)
	}
	row, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, imapBox.ID)
	c, _ := w.s.openCreds(row)
	if c.Password != "imap-secret-123" || c.smtpPassword() != "imap-secret-123" {
		t.Fatal("password not sealed")
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", imapBox.ID.String()) != 1 {
		t.Fatal("sync not queued")
	}
	// Google mailboxes are connected through OAuth only.
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "G", "email": "g@acme.test",
		"inbound_kind": "google", "outbound_kind": "google"}, nil); code != 422 {
		t.Fatalf("google via CRUD: %d", code)
	}

	// PATCH.
	var patched Mailbox
	code = doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+imapBox.ID.String(), map[string]any{"display_name": "Sales team",
		"password": "new-secret-456", "config": map[string]any{"imap_host": "imap2.acme.test"}}, &patched)
	if code != 200 || patched.DisplayName != "Sales team" || patched.Config.IMAPHost != "imap2.acme.test" {
		t.Fatalf("patch %d %+v", code, patched)
	}
	var changes string
	_ = w.d.DB.Pool.QueryRow(w.ctx, `SELECT data->>'changes' FROM audit_logs WHERE entity_id = $1 AND action = 'mailbox.updated'`, imapBox.ID).Scan(&changes)
	if !strings.Contains(changes, "credentials") || !strings.Contains(changes, "config") || strings.Contains(changes, "new-secret") {
		t.Fatalf("audit changes %q", changes)
	}

	// Roles and scopes.
	agent := *w.admin
	agent.Role = auth.RoleAgent
	asAgent := w.api(&agent)
	var got Mailbox
	if code := doJSON(t, http.MethodGet, asAgent.URL+"/v1/mailboxes/"+imapBox.ID.String(), nil, &got); code != 200 || got.Config.IMAPHost != "" {
		t.Fatalf("agent read: %d %+v", code, got.Config)
	}
	if code := doJSON(t, http.MethodPatch, asAgent.URL+"/v1/mailboxes/"+imapBox.ID.String(), map[string]any{"name": "x"}, nil); code != 403 {
		t.Fatalf("agent write: %d", code)
	}
	key := &auth.Principal{Kind: auth.KindAPIKey, APIKeyID: ids.New(), WorkspaceID: w.wid, Scopes: []string{auth.ScopeTicketsRead}}
	if code := doJSON(t, http.MethodGet, w.api(key).URL+"/v1/mailboxes", nil, nil); code != 403 {
		t.Fatalf("api key without settings scope: %d", code)
	}
	key.Scopes = []string{auth.ScopeSettingsRead}
	var page struct {
		Data []Mailbox `json:"data"`
	}
	if code := doJSON(t, http.MethodGet, w.api(key).URL+"/v1/mailboxes", nil, &page); code != 200 || len(page.Data) != 2 {
		t.Fatalf("list: %d %d", code, len(page.Data))
	}
	// Another workspace cannot see it.
	other := newWorld(t)
	if code := doJSON(t, http.MethodGet, other.api(other.admin).URL+"/v1/mailboxes/"+imapBox.ID.String(), nil, nil); code != 404 {
		t.Fatalf("cross-workspace read: %d", code)
	}

	// Test endpoint on the forwarding mailbox.
	var tr struct {
		Inbound, Outbound mailboxTestResult
	}
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/"+fwd.ID.String()+"/test", nil, &tr); code != 200 || !tr.Inbound.OK || !tr.Outbound.OK {
		t.Fatalf("test: %d %+v", code, tr)
	}

	// Delete keeps tickets.
	mustExec(t, w.d, `INSERT INTO tickets (workspace_id, number, subject, channel, mailbox_id, brand_id) VALUES ($1, 99, 's', 'email', $2, $3)`, w.wid, fwd.ID, w.brand)
	if code := doJSON(t, http.MethodDelete, srv.URL+"/v1/mailboxes/"+fwd.ID.String(), nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if w.count(`SELECT count(*) FROM tickets WHERE workspace_id = $1 AND number = 99 AND mailbox_id IS NULL`, w.wid) != 1 {
		t.Fatal("ticket should stay with no mailbox")
	}
	if w.eventCount("mailbox.deleted") != 1 || w.count(`SELECT count(*) FROM audit_logs WHERE entity_id = $1 AND action = 'mailbox.deleted'`, fwd.ID) != 1 {
		t.Fatal("delete event or audit missing")
	}
}

func TestMailboxHostGuard(t *testing.T) {
	w := newWorld(t)
	w.s.guard = &netGuard{allowPrivate: false}
	srv := w.api(w.admin)
	for _, host := range []string{"10.0.0.5", "127.0.0.1", "localhost", "169.254.169.254", "metadata.google.internal", "[::1]", "imap.acme.test:993"} {
		body := map[string]any{"name": "X", "email": "x-" + ids.New().String()[:6] + "@acme.test", "inbound_kind": "imap", "outbound_kind": "system",
			"password": "pw", "config": map[string]any{"imap_host": host}}
		if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", body, nil); code != 422 {
			t.Errorf("host %q accepted (%d)", host, code)
		}
	}
	body := map[string]any{"name": "X", "email": "plain@acme.test", "inbound_kind": "imap", "outbound_kind": "system", "password": "pw",
		"config": map[string]any{"imap_host": "imap.acme.test", "imap_tls": "none"}}
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", body, nil); code != 422 {
		t.Errorf("cleartext IMAP accepted (%d)", code)
	}
	// The dial-time guard refuses a private address even if it got stored.
	if err := w.s.guard.control("tcp", "10.1.2.3:993", nil); err == nil {
		t.Error("dial guard let a private address through")
	}
	if err := w.s.guard.control("tcp", "8.8.8.8:993", nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
}

type rawResp struct {
	code int
	body string
}

func rawJSON(t *testing.T, method, u string, body any) rawResp {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, u, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return rawResp{resp.StatusCode, string(out)}
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func startOAuth(t *testing.T, base, provider string, body map[string]any) (*url.URL, *http.Cookie) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+"/v1/mailboxes/oauth/"+provider+"/start", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("start: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	u, err := url.Parse(out.URL)
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == oauthCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != oauthCookiePath {
		t.Fatalf("binding cookie %+v", cookie)
	}
	return u, cookie
}

func callback(t *testing.T, base, provider, state, code string, cookie *http.Cookie) *url.URL {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/mailboxes/oauth/"+provider+"/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status %d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	return loc
}

func TestGoogleOAuthConnect(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	var challenge string
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/userinfo" {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-2-") {
				rw.WriteHeader(401)
				return true
			}
			_, _ = rw.Write([]byte(`{"email":"Help@Acme.test","email_verified":true}`))
			return true
		}
		return false
	}
	p := w.s.providers[KindGoogle]
	p.tokenURL, p.userinfoURL = f.srv.URL+"/token", f.srv.URL+"/userinfo"
	w.s.gmailBase = f.srv.URL // the send-as list, read after connecting (best effort)
	srv := w.api(w.admin)

	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"name": "Gmail support", "receive": "sync"})
	q := u.Query()
	challenge = q.Get("code_challenge")
	if u.Host != "accounts.google.com" || q.Get("code_challenge_method") != "S256" || challenge == "" || q.Get("access_type") != "offline" ||
		q.Get("prompt") != "consent" || !strings.Contains(q.Get("scope"), "https://mail.google.com/") ||
		q.Get("redirect_uri") != "http://api.test/v1/mailboxes/oauth/google/callback" {
		t.Fatalf("auth url %s", u)
	}
	state := q.Get("state")

	// Without the binding cookie the callback refuses.
	if loc := callback(t, srv.URL, "google", state, "code-1", nil); loc.Query().Get("error") != "oauth_state" {
		t.Fatalf("no cookie: %s", loc)
	}
	// A state for another provider is refused.
	if loc := callback(t, srv.URL, "microsoft", state, "code-1", cookie); loc.Query().Get("error") != "oauth_state" {
		t.Fatalf("wrong provider: %s", loc)
	}
	loc := callback(t, srv.URL, "google", state, "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil || loc.Host != "app.test" || loc.Path != "/settings/mailboxes" {
		t.Fatalf("redirect %s", loc)
	}
	// PKCE: the verifier sent to the token endpoint matches the challenge.
	var exchange map[string]string
	for _, r := range f.tokenReq {
		if r["grant_type"] == "authorization_code" {
			exchange = r
		}
	}
	sum := sha256.Sum256([]byte(exchange["code_verifier"]))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || exchange["code"] != "code-1" {
		t.Fatalf("pkce mismatch %v", exchange)
	}
	m, err := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Email != "help@acme.test" || m.InboundKind != "google" || m.OutboundKind != "google" || m.Config.IMAPHost != "imap.gmail.com" || m.Name != "Gmail support" {
		t.Fatalf("mailbox %+v", m.Mailbox)
	}
	c, _ := w.s.openCreds(m)
	if c.RefreshToken != "refresh-1" || c.AccessToken != "access-2-1" {
		t.Fatalf("tokens %+v", c.RefreshToken)
	}
	if w.count(`SELECT count(*) FROM audit_logs WHERE entity_id = $1 AND action = 'mailbox.connected' AND actor_user_id = $2`, id, w.uid) != 1 {
		t.Fatal("audit missing")
	}
	if w.eventCount("mailbox.created") != 1 || w.jobCount("email.sync_mailbox", "mailbox_id", id.String()) != 1 {
		t.Fatal("event or sync missing")
	}
	// Reconnecting the same account updates the mailbox rather than adding one.
	u, cookie = startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": id.String()})
	loc = callback(t, srv.URL, "google", u.Query().Get("state"), "code-2", cookie)
	if loc.Query().Get("connected") != id.String() || w.count(`SELECT count(*) FROM mailboxes WHERE workspace_id = $1`, w.wid) != 1 {
		t.Fatalf("reconnect %s", loc)
	}
	// A non-admin cannot start.
	agent := *w.admin
	agent.Role = auth.RoleAgent
	resp, _ := http.Post(w.api(&agent).URL+"/v1/mailboxes/oauth/google/start", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != 403 {
		t.Fatalf("agent start: %d", resp.StatusCode)
	}
}

func TestMicrosoftOAuthConnect(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/me" {
			// Only the Graph token (second token response) may read the profile.
			if r.Header.Get("Authorization") != "Bearer access-2-2" {
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
	u, cookie := startOAuth(t, srv.URL, "microsoft", map[string]any{"name": "Ops", "receive": "sync"})
	scope := u.Query().Get("scope")
	for _, s := range []string{"offline_access", msIMAPScope, msSendScope, msUserScope} {
		if !strings.Contains(scope, s) {
			t.Fatalf("authorize scope %q lacks %s", scope, s)
		}
	}
	loc := callback(t, srv.URL, "microsoft", u.Query().Get("state"), "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect %s", loc)
	}
	if len(f.tokenReq) != 2 || f.tokenReq[0]["scope"] != "openid email offline_access "+msIMAPScope ||
		f.tokenReq[1]["grant_type"] != "refresh_token" || f.tokenReq[1]["refresh_token"] != "refresh-1" ||
		f.tokenReq[1]["scope"] != "offline_access "+msSendScope+" "+msUserScope {
		t.Fatalf("token requests %v", f.tokenReq)
	}
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	c, _ := w.s.openCreds(m)
	if m.Email != "ops@acme.test" || m.Config.IMAPUsername != "ops@acme.onmicrosoft.com" || m.Config.IMAPHost != "outlook.office365.com" {
		t.Fatalf("mailbox %+v", m.Config)
	}
	if c.AccessToken != "access-2-1" || c.GraphAccessToken != "access-2-2" || c.RefreshToken != "refresh-2" {
		t.Fatalf("tokens imap=%q graph=%q refresh=%q", c.AccessToken, c.GraphAccessToken, c.RefreshToken)
	}
}
