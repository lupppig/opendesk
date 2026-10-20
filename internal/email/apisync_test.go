package email

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/plan"
)

// withDeps gives the world's service its own copy of the deps and config to change.
func (w *world) withDeps(change func(d *deps.Deps)) {
	d := *w.s.d
	cfg := *d.Cfg
	d.Cfg = &cfg
	change(&d)
	w.s.d = &d
}

// withoutCollab puts the world's workspace on an instance with billing, where it has no Collab.
func (w *world) withoutCollab() {
	w.withDeps(func(d *deps.Deps) { d.Plan = &plan.Service{Billing: true} })
}

// collabClient points the provider's Collab OAuth client at the fake provider.
func (w *world) collabClient(f *fakeProvider, provider string) *oauthProvider {
	c := w.s.apiClients[provider]
	c.clientID, c.clientSecret = "collab-"+provider, "collab-secret"
	c.tokenURL = f.srv.URL + "/token"
	if provider == KindGoogle {
		c.userinfoURL = f.srv.URL + "/userinfo"
	}
	return c
}

func (w *world) inboundCount(m *mailboxRow) int {
	return w.count(`SELECT count(*) FROM inbound_emails WHERE mailbox_id = $1`, m.ID)
}

// processAll processes the mailbox's received mail and returns its ticket count.
func (w *world) processAll(m *mailboxRow) int {
	w.t.Helper()
	rows, err := w.d.DB.Pool.Query(w.ctx, `SELECT id FROM inbound_emails WHERE mailbox_id = $1 AND status = 'pending'`, m.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var list []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		list = append(list, id)
	}
	rows.Close()
	for _, id := range list {
		if err := w.s.processInbound(w.ctx, w.wid, id); err != nil {
			w.t.Fatal(err)
		}
	}
	return w.count(`SELECT count(*) FROM tickets WHERE workspace_id = $1 AND mailbox_id = $2`, w.wid, m.ID)
}

func (w *world) apiState(m *mailboxRow) apiSyncState {
	w.t.Helper()
	var raw []byte
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT api_sync_state FROM mailboxes WHERE id = $1`, m.ID).Scan(&raw); err != nil {
		w.t.Fatal(err)
	}
	var st apiSyncState
	_ = json.Unmarshal(raw, &st)
	return st
}

// customerMail is a raw message from a new customer to the mailbox.
func customerMail(t *testing.T, to, id string) []byte {
	raw := fixture(t, "new_plain.eml", "{{FROM}}", customer(), "{{TO}}", to, "{{ID}}", id)
	return []byte(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\n", "\r\n"))
}

// fakeGmail serves the Gmail API for one mailbox.
type fakeGmail struct {
	mu        sync.Mutex
	profile   string              // the profile's historyId
	inbox     []string            // users.messages.list, newest first
	labels    map[string][]string // message labels
	threads   map[string]string   // message threadId
	raw       map[string][]byte
	history   func(start string) (int, string)
	fetches   map[string]int
	queries   []string
	authSeen  []string
	inboxRead int
}

func (g *fakeGmail) serve(rw http.ResponseWriter, r *http.Request) bool {
	const base = "/gmail/v1/users/me"
	if !strings.HasPrefix(r.URL.Path, base) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.authSeen = append(g.authSeen, r.Header.Get("Authorization"))
	path := strings.TrimPrefix(r.URL.Path, base)
	q := r.URL.Query()
	switch {
	case path == "/profile":
		_, _ = rw.Write([]byte(`{"emailAddress":"desk@acme.test","historyId":"` + g.profile + `"}`))
	case path == "/labels/INBOX":
		g.inboxRead++
		_, _ = rw.Write([]byte(`{"id":"INBOX"}`))
	case path == "/messages":
		g.queries = append(g.queries, r.URL.RawQuery)
		var list []map[string]string
		for _, id := range g.inbox {
			list = append(list, map[string]string{"id": id})
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"messages": list})
	case path == "/history":
		g.queries = append(g.queries, r.URL.RawQuery)
		if q.Get("labelId") != "INBOX" || q.Get("historyTypes") != "messageAdded" {
			rw.WriteHeader(400)
			return true
		}
		code, body := g.history(q.Get("startHistoryId"))
		rw.WriteHeader(code)
		_, _ = rw.Write([]byte(body))
	case strings.HasPrefix(path, "/messages/"):
		id := strings.TrimPrefix(path, "/messages/")
		raw, ok := g.raw[id]
		if !ok || q.Get("format") != "raw" {
			rw.WriteHeader(404)
			_, _ = rw.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND"}}`))
			return true
		}
		if g.fetches == nil {
			g.fetches = map[string]int{}
		}
		g.fetches[id]++
		labels := g.labels[id]
		if labels == nil {
			labels = []string{"INBOX", "UNREAD"}
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"id": id, "threadId": g.threads[id], "labelIds": labels, "raw": base64.URLEncoding.EncodeToString(raw)})
	default:
		return false
	}
	return true
}

// TestGmailAPISync: a Google mailbox connected to read the inbox directly uses the Collab
// client and gmail.modify, imports the inbox of the last week into tickets, then follows
// Gmail's history, skipping what the mailbox sent, deduping what it has seen and starting
// over when Gmail no longer keeps the history.
func TestGmailAPISync(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	g := &fakeGmail{profile: "100", labels: map[string][]string{}, raw: map[string][]byte{}}
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/userinfo" {
			_, _ = rw.Write([]byte(`{"email":"desk@acme.test","email_verified":true}`))
			return true
		}
		return g.serve(rw, r)
	}
	w.collabClient(f, KindGoogle)
	w.s.gmailBase = f.srv.URL
	srv := w.api(w.admin)

	var opts connectOptions
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/connect-options", nil, &opts); code != 200 ||
		!opts.Google.APISync || opts.Microsoft.APISync {
		t.Fatalf("connect options: %d %+v", code, opts)
	}
	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"name": "Desk", "receive": "api"})
	if got := u.Query().Get("scope"); got != "openid email "+googleModifyScope || u.Query().Get("client_id") != "collab-google" ||
		u.Query().Get("access_type") != "offline" || u.Query().Get("prompt") != "consent" {
		t.Fatalf("authorize %s", u.RawQuery)
	}
	loc := callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect %s", loc)
	}
	if f.tokenReq[0]["client_id"] != "collab-google" || f.tokenReq[0]["client_secret"] != "collab-secret" {
		t.Fatalf("code redeemed with %v", f.tokenReq[0])
	}
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	if m.InboundKind != KindGoogle || m.OutboundKind != KindGoogle || m.Config.OAuthGrant != GrantAPI ||
		m.Config.OAuthClient != ClientCollab || m.Config.IMAPHost != "" || m.Email != "desk@acme.test" {
		t.Fatalf("mailbox %+v %+v", m.Mailbox, m.Config)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", id.String()) != 1 {
		t.Fatal("connecting did not queue a sync")
	}
	var got Mailbox
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/"+id.String(), nil, &got); code != 200 ||
		got.Receiving != ReceivingOK || got.Config.OAuthGrant != GrantAPI {
		t.Fatalf("api %d %+v", code, got)
	}

	// First pass: the inbox of the import window, read even though MAILBOX_IMAP is off.
	w.s.mailboxIMAP = false
	g.inbox = []string{"g2", "g1"}
	g.raw["g1"], g.raw["g2"] = customerMail(t, m.Email, "g1-"+crypto.RandomString(6)), customerMail(t, m.Email, "g2-"+crypto.RandomString(6))
	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := w.inboundCount(m); n != 2 {
		t.Fatalf("first pass: %d messages", n)
	}
	if !strings.Contains(g.queries[0], "labelIds=INBOX") || !strings.Contains(g.queries[0], "q=after%3A") {
		t.Fatalf("list query %q", g.queries[0])
	}
	if st := w.apiState(m); st.HistoryID != "100" || st.Account != "desk@acme.test" {
		t.Fatalf("state %+v", st)
	}
	if n := w.processAll(m); n != 2 {
		t.Fatalf("tickets %d", n)
	}
	after, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	if after.Status != "active" || after.LastError != "" || after.LastSyncedAt == nil {
		t.Fatalf("after first pass %s %q", after.Status, after.LastError)
	}

	// Then the history: a new message, one the mailbox sent and a draft are told apart,
	// and one seen before is not stored twice.
	g.raw["g3"], g.raw["g4"] = customerMail(t, m.Email, "g3-"+crypto.RandomString(6)), customerMail(t, m.Email, "g4-"+crypto.RandomString(6))
	g.labels["g4"] = []string{"SENT"}
	g.history = func(start string) (int, string) {
		if start != "100" && start != "105" {
			return 404, `{"error":{"code":404,"status":"NOT_FOUND"}}`
		}
		return 200, `{"history":[
			{"id":"101","messagesAdded":[{"message":{"id":"g3","labelIds":["INBOX","UNREAD"]}}]},
			{"id":"102","messagesAdded":[{"message":{"id":"g4","labelIds":["SENT"]}}]},
			{"id":"103","messagesAdded":[{"message":{"id":"g1","labelIds":["INBOX"]}}]},
			{"id":"104","messagesAdded":[{"message":{"id":"d1","labelIds":["DRAFT"]}}]}],
			"historyId":"105"}`
	}
	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := w.inboundCount(m); n != 3 {
		t.Fatalf("incremental: %d messages", n)
	}
	if g.fetches["g4"] != 0 || g.fetches["d1"] != 0 {
		t.Fatalf("fetched what the mailbox sent or a draft: %v", g.fetches)
	}
	if st := w.apiState(m); st.HistoryID != "105" {
		t.Fatalf("history cursor %q", st.HistoryID)
	}
	if n := w.processAll(m); n != 3 {
		t.Fatalf("tickets after the history %d", n)
	}
	// The same changes again store nothing new.
	if err := w.s.syncMailbox(w.ctx, id); err != nil || w.inboundCount(m) != 3 {
		t.Fatalf("repeated pass: %v %d", err, w.inboundCount(m))
	}

	// History Gmail no longer keeps: the inbox is read again and deduped.
	mustExec(t, w.d, `UPDATE mailboxes SET api_sync_state = jsonb_set(api_sync_state, '{history_id}', '"7"') WHERE id = $1`, id)
	g.profile = "200"
	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if st := w.apiState(m); st.HistoryID != "200" || w.inboundCount(m) != 3 {
		t.Fatalf("restart: state %+v, %d messages", st, w.inboundCount(m))
	}

	// The connection check reads the inbox label through the API; nothing dials IMAP.
	var check mailboxCheck
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/"+id.String()+"/test", nil, &check); code != 200 || !check.Inbound.OK || !check.Outbound.OK {
		t.Fatalf("test: %d %+v", code, check)
	}
	if g.inboxRead != 1 {
		t.Fatalf("inbox label read %d times", g.inboxRead)
	}
	if _, err := w.s.dialIMAP(w.ctx, after); err == nil {
		t.Fatal("dialled IMAP for a mailbox that reads through the API")
	}
	// The scheduler queues it with MAILBOX_IMAP off.
	mustExec(t, w.d, `DELETE FROM river_job WHERE kind = 'email.sync_mailbox' AND args->>'mailbox_id' = $1`, id.String())
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", id.String()) != 1 {
		t.Fatal("the scheduler did not queue an api mailbox")
	}
	// A suspended workspace's mailbox is neither queued nor read.
	mustExec(t, w.d, `DELETE FROM river_job WHERE kind = 'email.sync_mailbox' AND args->>'mailbox_id' = $1`, id.String())
	mustExec(t, w.d, `UPDATE workspaces SET suspended_at = now() WHERE id = $1`, w.wid)
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", id.String()) != 0 {
		t.Fatal("queued a suspended workspace's mailbox")
	}
	reads := len(g.authSeen)
	if err := w.s.syncMailbox(w.ctx, id); err != nil || len(g.authSeen) != reads {
		t.Fatalf("read a suspended workspace's inbox: %v", err)
	}
}

// TestGraphAPISync: a Microsoft mailbox connected to read the inbox directly redeems the
// code for a Graph token with the Collab scopes, follows the inbox's delta into tickets,
// and starts over when Graph refuses the delta link.
func TestGraphAPISync(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	var mu sync.Mutex
	raws := map[string][]byte{}
	pages := map[string]string{}
	var deltaQueries []string
	var to string
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/me":
			_, _ = rw.Write([]byte(`{"mail":"ops@acme.test","userPrincipalName":"ops@acme.onmicrosoft.com"}`))
		case r.URL.Path == "/me/mailFolders/inbox/messages/delta":
			deltaQueries = append(deltaQueries, r.URL.RawQuery)
			key := r.URL.Query().Get("$deltatoken") + r.URL.Query().Get("$skiptoken")
			body, ok := pages[key]
			if !ok {
				rw.WriteHeader(410)
				_, _ = rw.Write([]byte(`{"error":{"code":"SyncStateNotFound","message":"The sync state was not found."}}`))
				return true
			}
			_, _ = rw.Write([]byte(strings.ReplaceAll(body, "BASE", f.srv.URL)))
		case strings.HasPrefix(r.URL.Path, "/me/messages/") && strings.HasSuffix(r.URL.Path, "/$value"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/me/messages/"), "/$value")
			raw, ok := raws[id]
			if !ok {
				rw.WriteHeader(404)
				_, _ = rw.Write([]byte(`{"error":{"code":"ErrorItemNotFound"}}`))
				return true
			}
			_, _ = rw.Write(raw)
		default:
			return false
		}
		return true
	}
	w.collabClient(f, KindMicrosoft)
	w.s.graphBase = f.srv.URL
	w.s.mailboxIMAP = false
	srv := w.api(w.admin)

	u, cookie := startOAuth(t, srv.URL, "microsoft", map[string]any{"name": "Ops", "receive": "api"})
	scope := u.Query().Get("scope")
	if !strings.Contains(scope, msReadWriteScope) || !strings.Contains(scope, msSendScope) || strings.Contains(scope, msIMAPScope) ||
		u.Query().Get("client_id") != "collab-microsoft" {
		t.Fatalf("authorize %s", u.RawQuery)
	}
	loc := callback(t, srv.URL, "microsoft", u.Query().Get("state"), "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect %s", loc)
	}
	wantScope := "openid email offline_access " + msReadWriteScope + " " + msSendScope + " " + msUserScope
	if len(f.tokenReq) != 1 || f.tokenReq[0]["scope"] != wantScope || f.tokenReq[0]["client_id"] != "collab-microsoft" {
		t.Fatalf("token requests %v", f.tokenReq)
	}
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	c, _ := w.s.openCreds(m)
	if m.InboundKind != KindMicrosoft || m.Config.OAuthGrant != GrantAPI || m.Config.OAuthClient != ClientCollab || c.GraphAccessToken == "" || c.AccessToken != "" {
		t.Fatalf("mailbox %+v %+v", m.Mailbox, m.Config)
	}
	to = m.Email

	mu.Lock()
	raws["a"], raws["c"], raws["d"] = customerMail(t, to, "a-"+crypto.RandomString(6)), customerMail(t, to, "c-"+crypto.RandomString(6)), customerMail(t, to, "d-"+crypto.RandomString(6))
	pages[""] = `{"value":[{"id":"a","receivedDateTime":"2026-10-17T09:00:00Z"},{"id":"b","@removed":{"reason":"deleted"}},{"id":"gone"}],
		"@odata.nextLink":"BASE/me/mailFolders/inbox/messages/delta?$skiptoken=p2"}`
	pages["p2"] = `{"value":[{"id":"c","receivedDateTime":"2026-10-17T10:00:00Z"},{"id":"x","isDraft":true}],
		"@odata.deltaLink":"BASE/me/mailFolders/inbox/messages/delta?$deltatoken=d1"}`
	pages["d1"] = `{"value":[{"id":"d"},{"id":"a"}],"@odata.deltaLink":"BASE/me/mailFolders/inbox/messages/delta?$deltatoken=d2"}`
	mu.Unlock()

	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := w.inboundCount(m); n != 2 {
		t.Fatalf("first round: %d messages", n)
	}
	if !strings.Contains(deltaQueries[0], "receivedDateTime+ge+") && !strings.Contains(deltaQueries[0], "receivedDateTime%20ge%20") {
		t.Fatalf("first round filter %q", deltaQueries[0])
	}
	if st := w.apiState(m); st.Delta != f.srv.URL+"/me/mailFolders/inbox/messages/delta?$deltatoken=d1" || st.Next != "" {
		t.Fatalf("state %+v", st)
	}
	if n := w.processAll(m); n != 2 {
		t.Fatalf("tickets %d", n)
	}

	// The next round brings the new message; one seen before is not stored twice.
	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := w.inboundCount(m); n != 3 || w.processAll(m) != 3 {
		t.Fatalf("delta: %d messages", n)
	}
	// An expired delta link starts over from the import window, deduped.
	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if st := w.apiState(m); w.inboundCount(m) != 3 || !strings.HasSuffix(st.Delta, "deltatoken=d1") {
		t.Fatalf("restart: %d messages, state %+v", w.inboundCount(m), st)
	}
	if after, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id); after.Status != "active" || after.LastError != "" {
		t.Fatalf("after restart %s %q", after.Status, after.LastError)
	}
	// A delta link off Graph is never followed with the token.
	mustExec(t, w.d, `UPDATE mailboxes SET api_sync_state = jsonb_set(api_sync_state, '{delta}', '"https://evil.test/steal"') WHERE id = $1`, id)
	mu.Lock()
	before := len(deltaQueries)
	mu.Unlock()
	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if st := w.apiState(m); !strings.HasPrefix(st.Delta, f.srv.URL) || len(deltaQueries) != before+2 {
		t.Fatalf("foreign link: state %+v", st)
	}
}

// TestAPISyncReconnectError: a refused grant leaves the mailbox in error asking for a
// reconnect, with a safe message.
func TestAPISyncReconnectError(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	w.collabClient(f, KindGoogle)
	w.s.gmailBase = f.srv.URL
	m := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{OAuthEmail: "desk@acme.test", OAuthGrant: GrantAPI, OAuthClient: ClientCollab}
		c.RefreshToken = "revoked"
	})
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if after.Status != "error" || !strings.Contains(after.LastError, "Reconnect") || strings.Contains(after.LastError, "invalid_grant") || after.SyncFailures != 1 {
		t.Fatalf("after a refused grant: %s %q %d", after.Status, after.LastError, after.SyncFailures)
	}
	if len(f.tokenReq) != 1 || f.tokenReq[0]["client_id"] != "collab-google" {
		t.Fatalf("refreshed with %v", f.tokenReq)
	}
	// A Gmail refusal of the scope asks for a reconnect too, without the provider's words.
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		rw.WriteHeader(403)
		_, _ = rw.Write([]byte(`{"error":{"code":403,"message":"Request had insufficient authentication scopes for desk@acme.test","status":"PERMISSION_DENIED"}}`))
		return true
	}
	sealed, _ := w.s.sealCreds(credentials{RefreshToken: "refresh-ok", AccessToken: "tok", Expiry: time.Now().Add(time.Hour)})
	mustExec(t, w.d, `UPDATE mailboxes SET credentials_enc = $2 WHERE id = $1`, m.ID, sealed)
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	after, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if !strings.Contains(after.LastError, "Reconnect") || strings.Contains(after.LastError, "desk@acme.test") {
		t.Fatalf("scope refusal: %q", after.LastError)
	}
}

// TestAPISyncNeedsCollab: without Collab a workspace cannot connect a mailbox to read
// directly, and one that already does is kept and says why it stopped.
func TestAPISyncNeedsCollab(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	w.collabClient(f, KindGoogle)
	w.collabClient(f, KindMicrosoft)
	var reads int
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/userinfo" {
			_, _ = rw.Write([]byte(`{"email":"desk@acme.test","email_verified":true}`))
			return true
		}
		reads++
		return false
	}
	p := w.s.providers[KindGoogle]
	p.tokenURL, p.userinfoURL = f.srv.URL+"/token", f.srv.URL+"/userinfo"
	w.s.gmailBase = f.srv.URL
	w.s.mailboxIMAP = false
	w.withoutCollab()
	srv := w.api(w.admin)

	var opts connectOptions
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/connect-options", nil, &opts); code != 200 ||
		opts.Google.APISync || opts.Microsoft.APISync || opts.MailboxIMAP || opts.InstanceIMAP || !opts.Google.OAuth {
		t.Fatalf("connect options: %d %+v", code, opts)
	}
	for _, prov := range []string{"google", "microsoft"} {
		r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/oauth/"+prov+"/start", map[string]any{"name": "Desk", "receive": "api"})
		if r.code != 422 || !strings.Contains(r.body, `"api_sync_unavailable"`) || !strings.Contains(r.body, "needs Collab") {
			t.Fatalf("start %s: %d %s", prov, r.code, r.body)
		}
	}

	// A mailbox that read directly is kept, unread, with the reason.
	m := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Email = "desk@acme.test"
		m.Config = MailboxConfig{OAuthEmail: "desk@acme.test", OAuthGrant: GrantAPI, OAuthClient: ClientCollab}
		c.RefreshToken, c.AccessToken, c.Expiry = "refresh-0", "tok", time.Now().Add(time.Hour)
	})
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if after.Status != "error" || after.LastError != "Reading this inbox directly needs Collab. Forward its mail or connect it again." || reads != 0 {
		t.Fatalf("without Collab: %s %q, %d reads", after.Status, after.LastError, reads)
	}
	// Reconnecting it connects send-only and turns it into a forwarding mailbox.
	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": m.ID.String()})
	if got := u.Query().Get("scope"); got != "openid email "+googleSendScope || u.Query().Get("client_id") != "gid" {
		t.Fatalf("reconnect %s", u.RawQuery)
	}
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	after, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if after.InboundKind != InboundForwarding || after.Config.OAuthGrant != GrantSend || after.Config.OAuthClient != "" || after.Status != "active" {
		t.Fatalf("reconnected %+v %+v", after.Mailbox, after.Config)
	}
}

// TestAPIReconnectKeepsMode: an api mailbox reconnects with the Collab client and scopes;
// a send-only one can move to reading directly.
func TestAPIReconnectKeepsMode(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/userinfo" {
			_, _ = rw.Write([]byte(`{"email":"desk@acme.test","email_verified":true}`))
			return true
		}
		return false
	}
	w.collabClient(f, KindGoogle)
	p := w.s.providers[KindGoogle]
	p.tokenURL, p.userinfoURL = f.srv.URL+"/token", f.srv.URL+"/userinfo"
	srv := w.api(w.admin)
	m := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Email = "desk@acme.test"
		m.Config = MailboxConfig{OAuthEmail: "desk@acme.test", OAuthGrant: GrantAPI, OAuthClient: ClientCollab}
		c.RefreshToken = "refresh-0"
	})
	mustExec(t, w.d, `UPDATE mailboxes SET api_sync_state = '{"account":"desk@acme.test","history_id":"55"}' WHERE id = $1`, m.ID)
	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": m.ID.String()})
	if got := u.Query().Get("scope"); got != "openid email "+googleModifyScope || u.Query().Get("client_id") != "collab-google" {
		t.Fatalf("reconnect %s", u.RawQuery)
	}
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	after, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if after.InboundKind != KindGoogle || after.Config.OAuthGrant != GrantAPI || w.apiState(m).HistoryID != "55" {
		t.Fatalf("reconnected %+v %+v %+v", after.Mailbox, after.Config, w.apiState(m))
	}

	// A send-only mailbox moves to reading directly when asked.
	fw := w.mailbox(InboundForwarding, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Email = "help@acme.test"
		m.Config = MailboxConfig{OAuthEmail: "jane@acme.test", OAuthGrant: GrantSend}
		c.RefreshToken = "refresh-1"
	})
	u, cookie = startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": fw.ID.String(), "receive": "api"})
	if got := u.Query().Get("scope"); got != "openid email "+googleModifyScope {
		t.Fatalf("upgrade scope %q", got)
	}
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-2", cookie)
	after, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, fw.ID)
	if after.InboundKind != KindGoogle || after.Config.OAuthGrant != GrantAPI || after.Config.OAuthClient != ClientCollab || after.Email != "help@acme.test" {
		t.Fatalf("upgraded %+v %+v", after.Mailbox, after.Config)
	}
}

// TestAPISendThroughCollabClient: an api mailbox sends through the Gmail API and Graph
// with tokens refreshed by the Collab client.
func TestAPISendThroughCollabClient(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	var uploaded, graphSent []byte
	var mu sync.Mutex
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/gmail/v1/users/me/messages/send":
			uploaded = readAll(r)
			_, _ = rw.Write([]byte(`{"id":"s-1","threadId":"t-1"}`))
		case r.URL.Path == "/gmail/v1/users/me/messages/s-1":
			_, _ = rw.Write([]byte(`{"payload":{"headers":[{"name":"Message-ID","value":"<stamped-1@mail.gmail.com>"}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/me/sendMail":
			graphSent = readAll(r)
			rw.WriteHeader(202)
		default:
			return false
		}
		return true
	}
	w.collabClient(f, KindGoogle)
	w.collabClient(f, KindMicrosoft)
	// The mailbox client must not be used: it points nowhere.
	w.s.providers[KindGoogle].tokenURL = "http://127.0.0.1:1/token"
	w.s.providers[KindMicrosoft].tokenURL = "http://127.0.0.1:1/token"
	w.s.gmailBase, w.s.graphBase = f.srv.URL, f.srv.URL

	g := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{OAuthEmail: "g@acme.test", OAuthGrant: GrantAPI, OAuthClient: ClientCollab}
		c.RefreshToken = "refresh-g"
	})
	tk, _ := firstTicket(t, w, g, customer())
	mid := w.agentReply(tk.ID, "Through Gmail", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(uploaded, []byte("Through Gmail")) {
		t.Fatalf("upload %q", uploaded)
	}
	if msg := w.message(mid); msg.Delivery != "sent" || *msg.MsgID != "<stamped-1@mail.gmail.com>" {
		t.Fatalf("delivery %s %v", msg.Delivery, *msg.MsgID)
	}
	if len(f.tokenReq) != 1 || f.tokenReq[0]["client_id"] != "collab-google" || f.tokenReq[0]["refresh_token"] != "refresh-g" {
		t.Fatalf("google refresh %v", f.tokenReq)
	}

	ms := w.mailbox(KindMicrosoft, KindMicrosoft, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{OAuthEmail: "o@acme.test", OAuthGrant: GrantAPI, OAuthClient: ClientCollab}
		c.RefreshToken = "refresh-m"
	})
	tk2, _ := firstTicket(t, w, ms, customer())
	mid2 := w.agentReply(tk2.ID, "Through Graph", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid2, false); err != nil {
		t.Fatal(err)
	}
	dec, _ := base64.StdEncoding.DecodeString(string(graphSent))
	if !bytes.Contains(dec, []byte("Through Graph")) {
		t.Fatalf("graph %q", graphSent)
	}
	want := "offline_access " + msReadWriteScope + " " + msSendScope + " " + msUserScope
	if len(f.tokenReq) != 2 || f.tokenReq[1]["client_id"] != "collab-microsoft" || f.tokenReq[1]["scope"] != want {
		t.Fatalf("microsoft refresh %v", f.tokenReq)
	}
	// No IMAP token is handed out for it.
	if _, err := w.s.accessToken(w.ctx, ms, resIMAP); err == nil {
		t.Fatal("an api grant handed out an IMAP token")
	}
}

// TestIMAPPerWorkspace: with MAILBOX_IMAP off, a workspace with Collab may receive over
// generic IMAP (and is polled); Google and Microsoft still may not sync over IMAP; a
// workspace without Collab may not.
func TestIMAPPerWorkspace(t *testing.T) {
	w := newWorld(t)
	w.s.mailboxIMAP = false
	w.withDeps(func(d *deps.Deps) { d.Cfg.CollabIMAP = true })
	u, port := imapServer(t, w)
	appendMail(t, u, testMail(customer(), "hello over imap"), time.Now())
	srv := w.api(w.admin)

	var opts connectOptions
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/connect-options", nil, &opts); code != 200 || !opts.MailboxIMAP || opts.InstanceIMAP {
		t.Fatalf("connect options: %d %+v", code, opts)
	}
	var got Mailbox
	code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "Agent", "email": "agent@acme.test",
		"inbound_kind": "imap", "outbound_kind": "system", "password": "imap-pass-1", "verify": true,
		"config": map[string]any{"imap_host": "127.0.0.1", "imap_port": port, "imap_tls": "tls", "imap_username": "agent@acme.test"}}, &got)
	if code != 201 || got.Receiving != ReceivingOK {
		t.Fatalf("create: %d %+v", code, got)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", got.ID.String()) != 1 {
		t.Fatal("creating did not queue a sync")
	}
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, got.ID)
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.inboundCount(m); n != 1 || w.processAll(m) != 1 {
		t.Fatalf("imap pass: %d messages", n)
	}
	mustExec(t, w.d, `DELETE FROM river_job WHERE kind = 'email.sync_mailbox' AND args->>'mailbox_id' = $1`, m.ID.String())
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", m.ID.String()) != 1 {
		t.Fatal("the scheduler did not queue the IMAP mailbox")
	}
	// Google over IMAP stays the instance's.
	if r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/oauth/google/start", map[string]any{"receive": "sync"}); r.code != 422 || !strings.Contains(r.body, `"imap_off"`) {
		t.Fatalf("start sync: %d %s", r.code, r.body)
	}

	// Without Collab: refused, reported and not polled.
	w.withoutCollab()
	srv = w.api(w.admin)
	opts = connectOptions{}
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/connect-options", nil, &opts); code != 200 || opts.MailboxIMAP {
		t.Fatalf("connect options without Collab: %d %+v", code, opts)
	}
	r := rawJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "X", "email": "x@acme.test",
		"inbound_kind": "imap", "outbound_kind": "system", "password": "pw", "config": map[string]any{"imap_host": "127.0.0.1", "imap_port": port}})
	if r.code != 422 || !strings.Contains(r.body, `"imap_off"`) {
		t.Fatalf("create without Collab: %d %s", r.code, r.body)
	}
	got = Mailbox{}
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/"+m.ID.String(), nil, &got); code != 200 || got.Receiving != ReceivingIMAPOff {
		t.Fatalf("receiving without Collab: %d %q", code, got.Receiving)
	}
	mustExec(t, w.d, `DELETE FROM river_job WHERE kind = 'email.sync_mailbox' AND args->>'mailbox_id' = $1`, m.ID.String())
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.jobCount("email.sync_mailbox", "mailbox_id", m.ID.String()) != 0 {
		t.Fatal("queued an IMAP mailbox of a workspace without Collab")
	}
	appendMail(t, u, testMail(customer(), "not read"), time.Now())
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil || w.inboundCount(m) != 1 {
		t.Fatalf("polled without Collab: %v %d", err, w.inboundCount(m))
	}
}

// TestAPISyncStartNew: a mailbox connected with sync_start new reads only mail that
// arrives from then on, through Gmail and through Graph.
func TestAPISyncStartNew(t *testing.T) {
	t.Run("gmail", func(t *testing.T) {
		w := newWorld(t)
		f := newFakeProvider(t)
		g := &fakeGmail{profile: "200", labels: map[string][]string{}, raw: map[string][]byte{}}
		f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/userinfo" {
				_, _ = rw.Write([]byte(`{"email":"desk@acme.test","email_verified":true}`))
				return true
			}
			return g.serve(rw, r)
		}
		w.collabClient(f, KindGoogle)
		w.s.gmailBase = f.srv.URL
		srv := w.api(w.admin)
		u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"name": "Desk", "receive": "api", "sync_start": "new"})
		id, err := uuid.Parse(callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie).Query().Get("connected"))
		if err != nil {
			t.Fatal(err)
		}
		m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
		if m.Config.SyncStart != SyncStartNew {
			t.Fatalf("config %+v", m.Config)
		}
		g.inbox = []string{"g1"}
		g.raw["g1"] = customerMail(t, m.Email, "g1-"+crypto.RandomString(6))
		if err := w.s.syncMailbox(w.ctx, id); err != nil {
			t.Fatal(err)
		}
		if n := w.inboundCount(m); n != 0 || len(g.queries) != 0 {
			t.Fatalf("first pass: %d messages, queries %q", n, g.queries)
		}
		if st := w.apiState(m); st.HistoryID != "200" {
			t.Fatalf("state %+v", st)
		}
	})
	t.Run("graph", func(t *testing.T) {
		w := newWorld(t)
		f := newFakeProvider(t)
		var mu sync.Mutex
		var deltaQueries []string
		f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
			mu.Lock()
			defer mu.Unlock()
			switch r.URL.Path {
			case "/me":
				_, _ = rw.Write([]byte(`{"mail":"ops@acme.test","userPrincipalName":"ops@acme.onmicrosoft.com"}`))
			case "/me/mailFolders/inbox/messages/delta":
				deltaQueries = append(deltaQueries, r.URL.Query().Get("$filter"))
				_, _ = rw.Write([]byte(`{"value":[],"@odata.deltaLink":"` + f.srv.URL + `/me/mailFolders/inbox/messages/delta?$deltatoken=d1"}`))
			default:
				return false
			}
			return true
		}
		w.collabClient(f, KindMicrosoft)
		w.s.graphBase = f.srv.URL
		srv := w.api(w.admin)
		u, cookie := startOAuth(t, srv.URL, "microsoft", map[string]any{"name": "Ops", "receive": "api", "sync_start": "new"})
		id, err := uuid.Parse(callback(t, srv.URL, "microsoft", u.Query().Get("state"), "code-1", cookie).Query().Get("connected"))
		if err != nil {
			t.Fatal(err)
		}
		before := time.Now().Add(-time.Minute)
		if err := w.s.syncMailbox(w.ctx, id); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(deltaQueries) != 1 {
			t.Fatalf("delta queries %q", deltaQueries)
		}
		since, err := time.Parse(time.RFC3339, strings.TrimPrefix(deltaQueries[0], "receivedDateTime ge "))
		if err != nil || since.Before(before) {
			t.Fatalf("first round filter %q", deltaQueries[0])
		}
	})
}
