package collab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeGoogleOAuth answers the token and userinfo endpoints for one Google account (its
// profile name with the profile scope, none when profileName is ""), and Gmail's send-as list.
func fakeGoogleOAuth(t *testing.T, w *world, address, profileName string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") == "" || r.Form.Get("client_secret") != "gsecret" ||
				r.Form.Get("redirect_uri") != "http://api.test/v1/collab/accounts/oauth/google/callback" {
				rw.WriteHeader(400)
				_, _ = rw.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = rw.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","expires_in":3600}`))
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer at-1" {
				rw.WriteHeader(401)
				return
			}
			info := map[string]any{"email": address, "email_verified": true}
			if profileName != "" {
				info["name"] = profileName
			}
			_ = json.NewEncoder(rw).Encode(info)
		case "/gmail/v1/users/me/settings/sendAs":
			if r.Header.Get("Authorization") != "Bearer at-1" {
				rw.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(rw).Encode(map[string]any{"sendAs": []map[string]any{
				{"sendAsEmail": "alias@other.test", "displayName": "Not this one"},
				{"sendAsEmail": address, "displayName": "Adam from Gmail", "isPrimary": true}}})
		default:
			rw.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := w.s.oauth[ProviderGoogle]
	c.tokenURL, c.userinfoURL, c.revokeURL = srv.URL+"/token", srv.URL+"/userinfo", ""
	w.s.gmailBase = srv.URL
}

// connectGoogle runs the OAuth flow as p and returns the redirect target.
func connectGoogle(t *testing.T, w *world, srvURL string, body map[string]any, code string) *url.URL {
	t.Helper()
	var start struct {
		URL string `json:"url"`
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", srvURL+"/v1/collab/accounts/oauth/google/start", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	_ = json.NewDecoder(resp.Body).Decode(&start)
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == oauthCookie {
			cookie = c
		}
	}
	u, err := url.Parse(start.URL)
	if err != nil || cookie == nil || !cookie.HttpOnly || cookie.Path != oauthCookiePath {
		t.Fatalf("start url %v cookie %+v", err, cookie)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" ||
		!strings.Contains(q.Get("scope"), "https://www.googleapis.com/auth/gmail.modify") {
		t.Fatalf("auth url: %s", u)
	}
	cb, _ := http.NewRequest("GET", srvURL+"/v1/collab/accounts/oauth/google/callback?"+url.Values{"state": {q.Get("state")}, "code": {code}}.Encode(), nil)
	cb.AddCookie(cookie)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cbResp, err := client.Do(cb)
	if err != nil {
		t.Fatal(err)
	}
	cbResp.Body.Close()
	loc, err := url.Parse(cbResp.Header.Get("Location"))
	if err != nil || cbResp.StatusCode != http.StatusFound || !strings.HasPrefix(loc.String(), "http://app.test/settings/mail-accounts?") {
		t.Fatalf("callback: %d %s", cbResp.StatusCode, cbResp.Header.Get("Location"))
	}
	return loc
}

func TestOAuthConnect(t *testing.T) {
	w := newWorld(t)
	address := "adam-" + uuid.NewString()[:6] + "@gmail.test"
	fakeGoogleOAuth(t, w, address, "Adam Agent")
	team := w.team("Support", w.agent2)
	srv := w.api(w.agent)

	loc := connectGoogle(t, w, srv.URL, map[string]any{"visibility": "shared", "shared_with": map[string]any{"everyone": false,
		"team_ids": []uuid.UUID{team}, "user_ids": []uuid.UUID{}}}, "good-code")
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect: %s", loc)
	}
	a := w.reload(&accountRow{ID: id})
	creds, _ := w.s.openCreds(a)
	if a.OwnerID == nil || *a.OwnerID != w.agent.UserID || a.Email != address || a.Name != address || a.DisplayName != "Adam Agent" ||
		a.Visibility != VisibilityShared || a.Status != "syncing" || creds.RefreshToken != "rt-1" {
		t.Fatalf("account: %+v", a)
	}
	aud, _ := loadAudience(w.ctx, w.d.DB.Pool, w.wid, &id, nil)
	if len(aud.TeamIDs) != 1 || aud.TeamIDs[0] != team {
		t.Fatalf("audience: %+v", aud)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'collab.account_connected' AND entity_id = $2
		AND NOT (data::text LIKE '%' || $3 || '%')`, w.wid, id, address); n != 1 {
		t.Fatal("no audit entry, or it names the address")
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.sync_account' AND args->>'account_id' = $1`, id.String()); n != 1 {
		t.Fatal("first sync not queued")
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.accounts' AND $2 = ANY(user_ids)`, w.wid, w.agent2.UserID); n != 1 {
		t.Fatal("the audience was not told")
	}

	// The same address again by its owner is a reconnect of the same account.
	mustExec(t, w.d, `UPDATE collab_accounts SET status = 'error', reconnect = true WHERE id = $1`, id)
	loc = connectGoogle(t, w, srv.URL, map[string]any{}, "good-code")
	if loc.Query().Get("connected") != id.String() {
		t.Fatalf("reconnect: %s", loc)
	}
	if a = w.reload(a); a.Reconnect || a.Status != "syncing" {
		t.Fatalf("after reconnect: %+v", a)
	}
	// By someone else: a conflict.
	loc = connectGoogle(t, w, w.api(w.agent2).URL, map[string]any{}, "good-code")
	if loc.Query().Get("error") != "account_conflict" {
		t.Fatalf("conflict: %s", loc)
	}
	// A refused code is a provider error; viewers cannot start.
	loc = connectGoogle(t, w, srv.URL, map[string]any{}, "bad-code")
	if loc.Query().Get("error") != "provider_error" {
		t.Fatalf("bad code: %s", loc)
	}
	if st := doJSON(t, "POST", w.api(w.viewer).URL+"/v1/collab/accounts/oauth/google/start", map[string]any{}, nil); st != 403 {
		t.Fatalf("viewer start: %d", st)
	}
	// A reconnect of someone else's private account is not found.
	if st := doJSON(t, "POST", w.api(w.agent2).URL+"/v1/collab/accounts/oauth/google/start", map[string]any{"account_id": id}, nil); st != 404 && st != 403 {
		t.Fatalf("foreign reconnect: %d", st)
	}
	// A forged state, or a missing cookie, is refused.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/v1/collab/accounts/oauth/google/callback?state=x&code=good-code")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Location"), "error=oauth_state") {
		t.Fatalf("forged state: %s", resp.Header.Get("Location"))
	}
	// Not configured.
	w.s.oauth[ProviderMicrosoft].clientID = ""
	var e struct {
		Code string `json:"code"`
	}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/oauth/microsoft/start", map[string]any{}, &e); st != 422 || e.Code != "provider_not_configured" {
		t.Fatalf("microsoft off: %d %s", st, e.Code)
	}
	if got := w.s.providersOffered(); !got.Google || got.Microsoft || !got.IMAP {
		t.Fatalf("offered: %+v", got)
	}
}

func TestIMAPConnectRoute(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.agent)
	body := map[string]any{"email": "me@example.com", "imap_host": "127.0.0.1", "imap_port": 1, "imap_tls": "tls", "password": "pw",
		"smtp_host": "127.0.0.1", "smtp_port": 1, "smtp_tls": "tls", "visibility": "private"}
	var e struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/imap", body, &e); st != 422 || e.Code != "connection_failed" || e.Detail == "" {
		t.Fatalf("closed port: %d %+v", st, e)
	}
	w.d.Cfg.CollabIMAP = false
	defer func() { w.d.Cfg.CollabIMAP = true }()
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/imap", body, &e); st != 422 || e.Code != "imap_unavailable" {
		t.Fatalf("imap off: %d %+v", st, e)
	}
}

// TestOAuthConnectNameFromGmail: a Google account without a profile name takes its From
// name from its Gmail send-as address.
func TestOAuthConnectNameFromGmail(t *testing.T) {
	w := newWorld(t)
	address := "adam-" + uuid.NewString()[:6] + "@gmail.test"
	fakeGoogleOAuth(t, w, address, "")
	srv := w.api(w.agent)
	loc := connectGoogle(t, w, srv.URL, map[string]any{"visibility": "private"}, "good-code")
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect: %s", loc)
	}
	if a := w.reload(&accountRow{ID: id}); a.DisplayName != "Adam from Gmail" {
		t.Fatalf("display name %q", a.DisplayName)
	}
}
