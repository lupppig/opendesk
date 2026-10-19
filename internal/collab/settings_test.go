package collab

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// An account's own settings against the Gmail and Graph fakes: signature and From name,
// automatic replies, granted scopes.

// fakeGmailSettings is Gmail's sendAs and vacation; served under fakeGmail's lock.
type fakeGmailSettings struct {
	signature, displayName string
	unknownAlias           bool // GET sendAs/{address} is 404: found in the list instead
	vacation               gmailVacation
	refuse                 int // writes answer this status
	patches                []map[string]any
	vacationPuts           []map[string]any
	reads                  int // sendAs reads
}

func (f *fakeGmailSettings) serve(t *testing.T, w http.ResponseWriter, r *http.Request, path string) {
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	entry := func(addr string) map[string]any {
		return map[string]any{"sendAsEmail": addr, "displayName": f.displayName, "signature": f.signature, "isPrimary": true}
	}
	refuse := func() bool {
		if f.refuse == 0 {
			return false
		}
		w.WriteHeader(f.refuse)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Signature has secret content","status":"INVALID_ARGUMENT"}}`))
		return true
	}
	switch {
	case r.Method == "GET" && path == "/settings/sendAs":
		f.reads++
		reply(map[string]any{"sendAs": []map[string]any{
			{"sendAsEmail": "alias@example.com", "isPrimary": false, "signature": "alias"}, entry("primary@example.com")}})
	case r.Method == "GET" && strings.HasPrefix(path, "/settings/sendAs/"):
		f.reads++
		if f.unknownAlias {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND"}}`))
			return
		}
		reply(entry(strings.TrimPrefix(path, "/settings/sendAs/")))
	case r.Method == "PATCH" && strings.HasPrefix(path, "/settings/sendAs/"):
		if refuse() {
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.patches = append(f.patches, in)
		// Gmail keeps its own form of the signature.
		if v, ok := in["signature"].(string); ok {
			f.signature = `<div dir="ltr">` + v + `</div>`
		}
		if v, ok := in["displayName"].(string); ok {
			f.displayName = v
		}
		reply(entry(strings.TrimPrefix(path, "/settings/sendAs/")))
	case r.Method == "GET" && path == "/settings/vacation":
		reply(f.vacation)
	case r.Method == "PUT" && path == "/settings/vacation":
		if refuse() {
			return
		}
		b, _ := io.ReadAll(r.Body)
		var in map[string]any
		_ = json.Unmarshal(b, &in)
		f.vacationPuts = append(f.vacationPuts, in)
		var v gmailVacation
		_ = json.Unmarshal(b, &v)
		f.vacation = v
		reply(f.vacation)
	default:
		t.Errorf("unexpected Gmail settings call %s %s", r.Method, path)
		w.WriteHeader(400)
	}
}

// fakeGraphSettings is the profile name and automatic replies; served under fakeGraph's lock.
type fakeGraphSettings struct {
	displayName  string
	replies      graphAutoReplies
	refuse       int
	patches      []map[string]any
	replyReads   int
	profileReads int
}

func (f *fakeGraphSettings) serve(t *testing.T, w http.ResponseWriter, r *http.Request, path string) {
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.Method == "GET" && path == "/me":
		f.profileReads++
		reply(map[string]any{"displayName": f.displayName})
	case r.Method == "GET" && path == "/me/mailboxSettings/automaticRepliesSetting":
		f.replyReads++
		reply(f.replies)
	case r.Method == "PATCH" && path == "/me/mailboxSettings":
		if f.refuse != 0 {
			w.WriteHeader(f.refuse)
			_, _ = w.Write([]byte(`{"error":{"code":"ErrorInvalidRequest","message":"secret content"}}`))
			return
		}
		var in struct {
			Set graphAutoReplies `json:"automaticRepliesSetting"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &in)
		var raw map[string]any
		_ = json.Unmarshal(b, &raw)
		f.patches = append(f.patches, raw)
		// Graph answers its own form: seven fractional digits, and HTML around the message.
		for _, d := range []*graphDateTime{in.Set.ScheduledStartDateTime, in.Set.ScheduledEndDateTime} {
			if d != nil {
				d.DateTime += ".0000000"
			}
		}
		in.Set.InternalReplyMessage = "<html><body>" + in.Set.InternalReplyMessage + "</body></html>"
		in.Set.ExternalReplyMessage = "<html><body>" + in.Set.ExternalReplyMessage + "</body></html>"
		f.replies = in.Set
		reply(map[string]any{"automaticRepliesSetting": f.replies})
	default:
		t.Errorf("unexpected Graph settings call %s %s", r.Method, path)
		w.WriteHeader(400)
	}
}

func (w *world) setScopes(a *accountRow, scopes ...string) *accountRow {
	w.t.Helper()
	b, _ := json.Marshal(scopes)
	mustExec(w.t, w.d, `UPDATE collab_accounts SET config = jsonb_set(config, '{scopes}', $2::jsonb) WHERE id = $1`, a.ID, b)
	return w.reload(a)
}

// settingsDue makes the next sync read the settings again.
func (w *world) settingsDue(a *accountRow) {
	w.t.Helper()
	mustExec(w.t, w.d, `UPDATE collab_accounts SET config = jsonb_set(config, '{settings,checked_at}',
		to_jsonb(now() - interval '1 hour')) WHERE id = $1`, a.ID)
}

func (w *world) sync(a *accountRow) *accountRow {
	w.t.Helper()
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		w.t.Fatal(err)
	}
	b := w.reload(a)
	if b.Status != "active" {
		w.t.Fatalf("sync: %s %s", b.Status, b.LastError)
	}
	return b
}

// apiAccount reads the account as the API shows it to p.
func (w *world) apiAccount(srv *httptest.Server, a *accountRow) MailAccount {
	w.t.Helper()
	var list []MailAccount
	if st := doJSON(w.t, "GET", srv.URL+"/v1/collab/accounts", nil, &list); st != 200 {
		w.t.Fatalf("accounts: %d", st)
	}
	for _, m := range list {
		if m.ID == a.ID {
			return m
		}
	}
	w.t.Fatalf("account %s not listed", a.ID)
	return MailAccount{}
}

func TestGmailSignatureAndNameTwoWay(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.setScopes(w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{}), "openid", "email", googleModifyScope, googleSettingsScope)
	g.settings.signature = `<div>Adam<br><b>Acme</b></div><script>alert(1)</script>`
	g.settings.displayName = "Adam A."
	srv := w.api(w.agent)

	// The first sync takes Gmail's signature (sanitized) and From name.
	a = w.sync(a)
	if a.SignatureHTML != `<div>Adam<br><b>Acme</b></div>` || a.DisplayName != "Adam A." {
		t.Fatalf("after the first sync: %q %q", a.SignatureHTML, a.DisplayName)
	}
	m := w.apiAccount(srv, a)
	if m.SignatureSync != SyncTwoWay || m.DisplayNameSync != SyncTwoWay || m.OutOfOfficeSync != SyncTwoWay || m.SignatureSyncedAt == nil {
		t.Fatalf("modes: %+v", m)
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.accounts' AND $2 = ANY(user_ids)`, w.wid, w.agent.UserID); n < 1 {
		t.Fatal("no collab.accounts")
	}

	// A change in Gmail waits for the next read (at most every 15 minutes), then wins.
	g.mu.Lock()
	g.settings.signature = "<p>Changed in Gmail</p>"
	g.mu.Unlock()
	if a = w.sync(a); a.SignatureHTML == "<p>Changed in Gmail</p>" {
		t.Fatal("settings read again before they were due")
	}
	w.settingsDue(a)
	if a = w.sync(a); a.SignatureHTML != "<p>Changed in Gmail</p>" {
		t.Fatalf("Gmail's change: %q", a.SignatureHTML)
	}

	// An edit in opendesk is made at Gmail first, sanitized; Gmail's own form of it is not pulled back.
	var out MailAccount
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{
		"signature_html": `<p>New <i>sig</i></p><img src=x onerror=alert(1)>`, "display_name": "Adam Agent"}, &out); st != 200 {
		t.Fatalf("patch: %d", st)
	}
	g.mu.Lock()
	patches := g.settings.patches
	g.mu.Unlock()
	if len(patches) != 1 || strings.Contains(patches[0]["signature"].(string), "onerror") || patches[0]["displayName"] != "Adam Agent" {
		t.Fatalf("Gmail patches: %v", patches)
	}
	if out.DisplayName != "Adam Agent" || strings.Contains(out.SignatureHTML, "onerror") || !strings.Contains(out.SignatureHTML, "<i>sig</i>") {
		t.Fatalf("patched: %+v", out)
	}
	saved := out.SignatureHTML
	w.settingsDue(a)
	if a = w.sync(a); a.SignatureHTML != saved || a.DisplayName != "Adam Agent" {
		t.Fatalf("our own edit came back from Gmail: %q %q", a.SignatureHTML, a.DisplayName)
	}

	// Gmail refuses: 502, and nothing of the change is saved.
	g.mu.Lock()
	g.settings.refuse = 400
	g.mu.Unlock()
	var e apiError
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{
		"signature_html": "<p>Refused</p>", "color": "red"}, &e); st != 502 || e.Code != "provider_error" ||
		strings.Contains(e.Detail, "secret") || strings.Contains(e.Detail, "Refused") {
		t.Fatalf("refused: %d %+v", st, e)
	}
	if b := w.reload(a); b.SignatureHTML != saved || b.Color == "red" {
		t.Fatalf("saved after a refusal: %q %s", b.SignatureHTML, b.Color)
	}
	g.mu.Lock()
	g.settings.refuse = 0
	g.mu.Unlock()

	// Get from Gmail replaces it now.
	g.mu.Lock()
	g.settings.signature = "<p>From Gmail now</p>"
	g.mu.Unlock()
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/signature/pull", nil, &out); st != 200 || out.SignatureHTML != "<p>From Gmail now</p>" {
		t.Fatalf("pull: %d %q", st, out.SignatureHTML)
	}
	// Someone who does not manage it cannot.
	if st := doJSON(t, "POST", w.api(w.agent2).URL+"/v1/collab/accounts/"+a.ID.String()+"/signature/pull", nil, nil); st != 404 {
		t.Fatalf("foreign pull: %d", st)
	}
	// An address Gmail only knows from its list is found there.
	g.mu.Lock()
	g.settings.unknownAlias = true
	g.mu.Unlock()
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/signature/pull", nil, &out); st != 200 || out.SignatureHTML != "<p>From Gmail now</p>" {
		t.Fatalf("pull from the list: %d %q", st, out.SignatureHTML)
	}
}

func TestGmailSignatureReadOnly(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	g.settings.signature = "<p>Gmail sig</p>"
	srv := w.api(w.agent)
	// Granted without gmail.settings.basic, and connected before scopes were recorded.
	for _, a := range []*accountRow{
		w.setScopes(w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{}), "openid", "email", googleModifyScope),
		w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{}),
	} {
		a = w.sync(a)
		if a.SignatureHTML != "<p>Gmail sig</p>" {
			t.Fatalf("read only still reads: %q", a.SignatureHTML)
		}
		if m := w.apiAccount(srv, a); m.SignatureSync != SyncReadOnly || m.DisplayNameSync != SyncReadOnly || m.OutOfOfficeSync != SyncReadOnly {
			t.Fatalf("modes: %+v", m)
		}
		var out MailAccount
		if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"signature_html": "<p>Local</p>"}, &out); st != 200 ||
			out.SignatureHTML != "<p>Local</p>" {
			t.Fatalf("local save: %d %q", st, out.SignatureHTML)
		}
		var e apiError
		if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", map[string]any{"enabled": true, "start": nil,
			"end": nil, "subject": "Away", "message_html": "<p>Away</p>", "contacts_only": false, "internal_only": false}, &e); st != 422 ||
			e.Code != "out_of_office_read_only" {
			t.Fatalf("read-only automatic reply: %d %+v", st, e)
		}
		// The local signature stays until Gmail's changes.
		w.settingsDue(a)
		if a = w.sync(a); a.SignatureHTML != "<p>Local</p>" {
			t.Fatalf("an unchanged Gmail signature replaced the local one: %q", a.SignatureHTML)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.settings.patches) != 0 || len(g.settings.vacationPuts) != 0 {
		t.Fatalf("Gmail was written: %v %v", g.settings.patches, g.settings.vacationPuts)
	}
}

func TestGrantedScopesRecorded(t *testing.T) {
	w := newWorld(t)
	address := "adam-" + uuid.NewString()[:6] + "@gmail.test"
	granted := "openid https://www.googleapis.com/auth/userinfo.email " + googleModifyScope
	var refreshForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshForm = r.Form
				_ = json.NewEncoder(rw).Encode(map[string]any{"access_token": "at-2", "expires_in": 3600,
					"scope": googleModifyScope + " " + googleSettingsScope})
				return
			}
			_ = json.NewEncoder(rw).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600, "scope": granted})
		case "/userinfo":
			_ = json.NewEncoder(rw).Encode(map[string]any{"email": address, "email_verified": true, "name": "Adam Agent"})
		}
	}))
	t.Cleanup(srv.Close)
	c := w.s.oauth[ProviderGoogle]
	c.tokenURL, c.userinfoURL, c.revokeURL = srv.URL+"/token", srv.URL+"/userinfo", ""
	// New connects ask for the settings scope.
	if u := w.s.authCodeURL(c, "st", "verifier-verifier-verifier-verifier-verifier", ""); !strings.Contains(u, url.QueryEscape(googleSettingsScope)) {
		t.Fatalf("auth url: %s", u)
	}
	if !strings.Contains(strings.Join(w.s.oauth[ProviderMicrosoft].scopes, " "), msMailboxSettingsScope) {
		t.Fatal("Microsoft does not ask for MailboxSettings.ReadWrite")
	}

	loc := connectGoogle(t, w, w.api(w.agent).URL, map[string]any{}, "good-code")
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect: %s", loc)
	}
	a := w.reload(&accountRow{ID: id})
	if strings.Join(a.Config.Scopes, " ") != granted || signatureSync(a) != SyncReadOnly {
		t.Fatalf("scopes: %v %s", a.Config.Scopes, signatureSync(a))
	}
	// A refresh records what the grant carries now.
	if err := w.s.expireToken(w.ctx, a); err != nil {
		t.Fatal(err)
	}
	if tok, err := w.s.accessToken(w.ctx, a); err != nil || tok != "at-2" || refreshForm.Get("scope") != "" {
		t.Fatalf("refresh: %v %s %v", err, tok, refreshForm)
	}
	if a = w.reload(a); signatureSync(a) != SyncTwoWay {
		t.Fatalf("after the refresh: %v", a.Config.Scopes)
	}

	// Microsoft refreshes ask only for the Graph scopes the account was granted.
	ms := w.s.oauth[ProviderMicrosoft]
	if got := ms.refreshScopeFor(nil); got != ms.refreshScope || strings.Contains(got, "MailboxSettings") {
		t.Fatalf("legacy refresh scope: %s", got)
	}
	if got := ms.refreshScopeFor([]string{"Mail.ReadWrite", "Mail.Send", "User.Read", "openid"}); strings.Contains(got, "MailboxSettings") || !strings.Contains(got, msSendScope) {
		t.Fatalf("narrowed refresh scope: %s", got)
	}
	if got := ms.refreshScopeFor([]string{msReadWriteScope, msSendScope, msUserScope, msMailboxSettingsScope}); !strings.Contains(got, msMailboxSettingsScope) {
		t.Fatalf("full refresh scope: %s", got)
	}
}

func TestMicrosoftDisplayName(t *testing.T) {
	w := newWorld(t)
	g := newFakeGraph(t)
	w.s.graphBase = g.srv.URL
	a := w.account(w.agent, ProviderMicrosoft, VisibilityPrivate, Audience{})
	g.settings.displayName = "Sales Team"
	srv := w.api(w.agent)

	// Read from the profile while nobody chose another name; no automatic replies without MailboxSettings.
	a = w.sync(a)
	if a.DisplayName != "Sales Team" {
		t.Fatalf("profile name: %q", a.DisplayName)
	}
	m := w.apiAccount(srv, a)
	if m.DisplayNameSync != SyncReadOnly || m.SignatureSync != SyncNone || m.OutOfOfficeSync != SyncNone || m.OutOfOffice != nil {
		t.Fatalf("modes: %+v", m)
	}
	g.mu.Lock()
	if g.settings.replyReads != 0 {
		t.Fatal("automatic replies read without MailboxSettings")
	}
	g.mu.Unlock()
	var e apiError
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", map[string]any{"enabled": true, "start": nil,
		"end": nil, "subject": "", "message_html": "<p>Away</p>", "contacts_only": false, "internal_only": false}, &e); st != 422 || e.Code != "out_of_office_unsupported" {
		t.Fatalf("no MailboxSettings: %d %+v", st, e)
	}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/signature/pull", nil, &e); st != 422 || e.Code != "signature_sync_unsupported" {
		t.Fatalf("pull on Microsoft: %d %+v", st, e)
	}
	// A signature is saved in opendesk only.
	var out MailAccount
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"signature_html": "<p>Sales</p>"}, &out); st != 200 || out.SignatureHTML != "<p>Sales</p>" {
		t.Fatalf("signature: %d %+v", st, out)
	}

	// Someone chooses the name: the profile no longer replaces it.
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"display_name": "Margin Sales"}, &out); st != 200 ||
		out.DisplayName != "Margin Sales" || out.DisplayNameSync != SyncNone {
		t.Fatalf("named: %d %+v", st, out)
	}
	g.mu.Lock()
	g.settings.displayName = "Somebody Else"
	reads := g.settings.profileReads
	g.mu.Unlock()
	w.settingsDue(a)
	if a = w.sync(a); a.DisplayName != "Margin Sales" || a.SignatureHTML != "<p>Sales</p>" {
		t.Fatalf("after a person chose the name: %q %q", a.DisplayName, a.SignatureHTML)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.settings.profileReads != reads {
		t.Fatal("the profile is still read")
	}
}

func TestOutOfOfficeGmail(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.setScopes(w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{}), googleModifyScope, googleSettingsScope)
	start := time.Date(2026, 12, 20, 9, 0, 0, 0, time.UTC)
	end := start.Add(14 * 24 * time.Hour)
	g.settings.vacation = gmailVacation{EnableAutoReply: true, ResponseSubject: "Away", ResponseBodyHTML: `<p>On holiday</p><script>x()</script>`,
		RestrictToDomain: true, StartTime: millisOf(&start), EndTime: millisOf(&end)}
	srv := w.api(w.agent)

	a = w.sync(a)
	m := w.apiAccount(srv, a)
	o := m.OutOfOffice
	if o == nil || !o.Enabled || o.Subject != "Away" || o.MessageHTML != "<p>On holiday</p>" || !o.InternalOnly || o.ContactsOnly ||
		o.Start == nil || !o.Start.Equal(start) || o.End == nil || !o.End.Equal(end) {
		t.Fatalf("read from Gmail: %+v", o)
	}

	// Set in opendesk: written to Gmail first, sanitized.
	newEnd := end.Add(24 * time.Hour)
	var out MailAccount
	body := map[string]any{"enabled": true, "start": nil, "end": newEnd, "subject": "Out until January",
		"message_html": `<p>Back soon</p><img src=x onerror=alert(1)>`, "contacts_only": true, "internal_only": false}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", body, &out); st != 200 {
		t.Fatalf("put: %d", st)
	}
	g.mu.Lock()
	put := g.settings.vacationPuts[len(g.settings.vacationPuts)-1]
	g.mu.Unlock()
	if put["enableAutoReply"] != true || put["restrictToContacts"] != true || put["restrictToDomain"] != false || put["startTime"] != nil ||
		put["responseSubject"] != "Out until January" || strings.Contains(put["responseBodyHtml"].(string), "onerror") {
		t.Fatalf("vacation sent: %v", put)
	}
	if ms, _ := put["endTime"].(float64); int64(ms) != newEnd.UnixMilli() {
		t.Fatalf("end sent: %v", put["endTime"])
	}
	if o = out.OutOfOffice; o == nil || o.Start != nil || !o.End.Equal(newEnd) || !o.ContactsOnly || o.Subject != "Out until January" || strings.Contains(o.MessageHTML, "onerror") {
		t.Fatalf("stored: %+v", o)
	}
	// Not pulled back; Gmail's next change wins.
	w.settingsDue(a)
	w.sync(a)
	if o = w.apiAccount(srv, a).OutOfOffice; o.Subject != "Out until January" {
		t.Fatalf("after the next read: %+v", o)
	}
	g.mu.Lock()
	g.settings.vacation.EnableAutoReply = false
	g.mu.Unlock()
	w.settingsDue(a)
	w.sync(a)
	if o = w.apiAccount(srv, a).OutOfOffice; o.Enabled {
		t.Fatal("Gmail turning it off did not arrive")
	}

	// Refused: 502, nothing saved. Backwards times: 422.
	g.mu.Lock()
	g.settings.refuse = 400
	g.mu.Unlock()
	var e apiError
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", body, &e); st != 502 || e.Code != "provider_error" || strings.Contains(e.Detail, "secret") {
		t.Fatalf("refused: %d %+v", st, e)
	}
	if o = w.apiAccount(srv, a).OutOfOffice; o.Enabled {
		t.Fatal("saved after a refusal")
	}
	body["start"] = newEnd.Add(time.Hour)
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", body, &e); st != 422 {
		t.Fatalf("backwards: %d %+v", st, e)
	}
	// IMAP has none.
	imapAcc := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	if m := w.apiAccount(srv, imapAcc); m.OutOfOffice != nil || m.OutOfOfficeSync != SyncNone || m.SignatureSync != SyncNone || m.DisplayNameSync != SyncNone {
		t.Fatalf("imap: %+v", m)
	}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+imapAcc.ID.String()+"/out-of-office", body, &e); st != 422 || e.Code != "out_of_office_unsupported" {
		t.Fatalf("imap put: %d %+v", st, e)
	}
}

func TestOutOfOfficeGraph(t *testing.T) {
	w := newWorld(t)
	g := newFakeGraph(t)
	w.s.graphBase = g.srv.URL
	a := w.setScopes(w.account(w.agent, ProviderMicrosoft, VisibilityPrivate, Audience{}),
		"Mail.ReadWrite", "Mail.Send", "User.Read", "MailboxSettings.ReadWrite")
	g.settings.displayName = "Box"
	g.settings.replies = graphAutoReplies{Status: "scheduled", ExternalAudience: "contactsOnly",
		ScheduledStartDateTime: &graphDateTime{DateTime: "2026-12-20T09:00:00.0000000", TimeZone: "Pacific Standard Time"},
		ScheduledEndDateTime:   &graphDateTime{DateTime: "2027-01-04T09:00:00.0000000", TimeZone: "Pacific Standard Time"},
		InternalReplyMessage:   "<html><body><p>Away</p></body></html>", ExternalReplyMessage: "<p>Away</p>"}
	srv := w.api(w.agent)

	a = w.sync(a)
	m := w.apiAccount(srv, a)
	o := m.OutOfOffice
	if m.OutOfOfficeSync != SyncTwoWay || o == nil || !o.Enabled || !o.ContactsOnly || o.InternalOnly || o.Subject != "" ||
		!strings.Contains(o.MessageHTML, "<p>Away</p>") || strings.Contains(o.MessageHTML, "<html") {
		t.Fatalf("read from Outlook: %s %+v", m.OutOfOfficeSync, o)
	}
	if want := time.Date(2026, 12, 20, 17, 0, 0, 0, time.UTC); o.Start == nil || !o.Start.Equal(want) {
		t.Fatalf("start in UTC: %v", o.Start)
	}

	// Set in opendesk: scheduled, inside the organization only, one message for both.
	start, end := time.Date(2027, 2, 1, 8, 0, 0, 0, time.UTC), time.Date(2027, 2, 8, 8, 0, 0, 0, time.UTC)
	var out MailAccount
	body := map[string]any{"enabled": true, "start": start, "end": end, "subject": "ignored", "message_html": "<p>Skiing</p><script>x</script>",
		"contacts_only": false, "internal_only": true}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", body, &out); st != 200 {
		t.Fatalf("put: %d", st)
	}
	g.mu.Lock()
	sent, _ := g.settings.patches[len(g.settings.patches)-1]["automaticRepliesSetting"].(map[string]any)
	g.mu.Unlock()
	startSent, _ := sent["scheduledStartDateTime"].(map[string]any)
	if sent["status"] != "scheduled" || sent["externalAudience"] != "none" || sent["internalReplyMessage"] != sent["externalReplyMessage"] ||
		strings.Contains(sent["internalReplyMessage"].(string), "script") || startSent["timeZone"] != "UTC" || startSent["dateTime"] != "2027-02-01T08:00:00" {
		t.Fatalf("automatic replies sent: %v", sent)
	}
	if o = out.OutOfOffice; o == nil || !o.InternalOnly || o.ContactsOnly || !o.Start.Equal(start) || !o.End.Equal(end) || o.Subject != "" {
		t.Fatalf("stored: %+v", o)
	}
	// Not pulled back; Outlook's change wins.
	w.settingsDue(a)
	w.sync(a)
	if got := w.apiAccount(srv, a).OutOfOffice; !got.Start.Equal(start) {
		t.Fatalf("after the next read: %+v", got)
	}
	g.mu.Lock()
	g.settings.replies.Status = "alwaysEnabled"
	g.mu.Unlock()
	w.settingsDue(a)
	w.sync(a)
	if got := w.apiAccount(srv, a).OutOfOffice; !got.Enabled || got.Start != nil || got.End != nil {
		t.Fatalf("Outlook's change: %+v", got)
	}

	// Outlook needs both times; a refusal saves nothing.
	var e apiError
	body["end"] = nil
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", body, &e); st != 422 {
		t.Fatalf("start only: %d %+v", st, e)
	}
	body["start"], body["enabled"] = nil, false
	g.mu.Lock()
	g.settings.refuse = 400
	g.mu.Unlock()
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", body, &e); st != 502 || strings.Contains(e.Detail, "secret") {
		t.Fatalf("refused: %d %+v", st, e)
	}
	if got := w.apiAccount(srv, a).OutOfOffice; !got.Enabled {
		t.Fatal("saved after a refusal")
	}
	g.mu.Lock()
	g.settings.refuse = 0
	g.mu.Unlock()
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/out-of-office", body, &out); st != 200 || out.OutOfOffice.Enabled {
		t.Fatalf("turned off: %d %+v", st, out.OutOfOffice)
	}
}
