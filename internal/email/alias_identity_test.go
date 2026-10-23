package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jhillyerd/enmime/v2"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/ids"
)

// gmailSendAsList is a Gmail settings/sendAs answer: the account, a verified alias and one
// still waiting for its owner's confirmation.
const gmailSendAsList = `{"sendAs":[
	{"sendAsEmail":"%s","displayName":"Acme Desk","isPrimary":true},
	{"sendAsEmail":"billing@acme.test","displayName":"Acme Billing","verificationStatus":"accepted"},
	{"sendAsEmail":"press@acme.test","displayName":"Press","verificationStatus":"pending"},
	{"sendAsEmail":"jobs@acme.test","displayName":"Acme Jobs","verificationStatus":"accepted"}]}`

func (w *world) brandNamed(name string) uuid.UUID {
	id := ids.New()
	mustExec(w.t, w.d, `INSERT INTO brands (id, workspace_id, name, slug) VALUES ($1, $2, $3, $4)`, id, w.wid, name,
		strings.ToLower(name)+"-"+crypto.RandomString(6))
	return id
}

// googleMailbox is a Google mailbox with grant whose token needs no refresh.
func (w *world) googleMailbox(grant string, aliases ...string) *mailboxRow {
	inbound := KindGoogle
	if grant == GrantSend {
		inbound = InboundForwarding
	}
	return w.mailbox(inbound, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Email = "desk-" + crypto.RandomString(5) + "@acme.test"
		m.Aliases = aliases
		m.Config = MailboxConfig{OAuthEmail: m.Email, OAuthGrant: grant}
		if grant == GrantAPI {
			m.Config.OAuthClient = ClientCollab
		}
		c.RefreshToken, c.AccessToken, c.Expiry = "refresh-0", "access-0", time.Now().Add(time.Hour)
	})
}

// TestAliasSettingsAPI: each alias takes its own name, signature and brand; keys must be
// aliases, brands the workspace's; removing an alias drops its settings; mailbox.updated is
// audit-logged without the signature itself.
func TestAliasSettingsAPI(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.admin)
	other := w.brandNamed("Side")
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.Aliases = []string{"side@acme.test", "team@acme.test"}
	})
	url := srv.URL + "/v1/mailboxes/" + m.ID.String()
	var got Mailbox
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"alias_settings": map[string]any{
		"Side@Acme.test": map[string]any{"display_name": "Side Project", "signature_html": `<p>{{ mailbox.name }} team</p><script>x</script>`,
			"brand_id": other.String()},
	}}, &got); code != 200 {
		t.Fatalf("patch %d", code)
	}
	st := got.AliasSettings["side@acme.test"]
	if st.DisplayName != "Side Project" || st.BrandID == nil || *st.BrandID != other || strings.Contains(st.SignatureHTML, "script") ||
		!strings.Contains(st.SignatureHTML, "{{ mailbox.name }} team") || st.SendAs != SendAsUnknown {
		t.Fatalf("settings %+v", st)
	}
	if team := got.AliasSettings["team@acme.test"]; team.SendAs != SendAsUnknown || team.DisplayName != "" {
		t.Fatalf("every alias has an entry: %+v", got.AliasSettings)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.updated'
		AND data -> 'alias_settings' -> 'side@acme.test' ->> 'signature' = 'true' AND NOT data::text LIKE '%team</p>%'`, w.wid); n != 1 {
		t.Fatalf("audit %d", n)
	}
	// The mailbox's own brand is stored as none: the alias follows the mailbox. A signature
	// left out is kept; "" clears it.
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"alias_settings": map[string]any{
		"side@acme.test": map[string]any{"display_name": "Side Project", "brand_id": w.brand.String()}}}, &got); code != 200 ||
		got.AliasSettings["side@acme.test"].BrandID != nil || !strings.Contains(got.AliasSettings["side@acme.test"].SignatureHTML, "team</p>") {
		t.Fatalf("own brand %d %+v", code, got.AliasSettings)
	}
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"alias_settings": map[string]any{
		"side@acme.test": map[string]any{"display_name": "Side Project", "signature_html": ""}}}, &got); code != 200 ||
		got.AliasSettings["side@acme.test"].SignatureHTML != "" {
		t.Fatalf("cleared signature %d %+v", code, got.AliasSettings)
	}
	for name, body := range map[string]map[string]any{
		"not an alias":      {"other@acme.test": map[string]any{"display_name": "X"}},
		"another workspace": {"side@acme.test": map[string]any{"brand_id": ids.New().String()}},
		"bad template":      {"side@acme.test": map[string]any{"signature_html": "<p>{{ nope.nope }}</p>"}},
	} {
		if code := doJSON(t, http.MethodPatch, url, map[string]any{"alias_settings": body}, nil); code != 422 {
			t.Errorf("%s: %d", name, code)
		}
	}
	// Where no provider list decides, an admin's word is recorded as theirs.
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"alias_settings": map[string]any{
		"team@acme.test": map[string]any{"send_as": "verified"}}}, &got); code != 200 ||
		got.AliasSettings["team@acme.test"].SendAs != SendAsVerified || got.AliasSettings["team@acme.test"].SendAsBy != SendAsByAdmin {
		t.Fatalf("claim %d %+v", code, got.AliasSettings)
	}
	// Left out, send_as is kept; removing the alias drops everything about it.
	got = Mailbox{}
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"aliases": []string{"side@acme.test"}}, &got); code != 200 {
		t.Fatalf("remove %d", code)
	}
	if _, ok := got.AliasSettings["team@acme.test"]; ok || len(got.AliasSettings) != 1 {
		t.Fatalf("removed alias kept %+v", got.AliasSettings)
	}
	if n := w.count(`SELECT count(*) FROM mailboxes WHERE id = $1 AND alias_settings ? 'team@acme.test'`, m.ID); n != 0 {
		t.Fatal("stored settings of a removed alias")
	}
}

// TestAliasIdentityOnReplies: mail to an alias is answered with its name and signature
// (before the team's), in its brand, and new tickets written to it take its brand.
func TestAliasIdentityOnReplies(t *testing.T) {
	w := newWorld(t)
	side := w.brandNamed("Side")
	billing := w.team("Billing", false, `<p>team signature</p>`)
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.TeamID = &billing
		m.Aliases = []string{"hello@side.test"}
		m.FromNameFormat = "{agent_first} at {mailbox}"
		m.SignatureHTML = "<p>address signature</p>"
		m.AliasSettings = map[string]AliasSettings{"hello@side.test": {DisplayName: "Side Project",
			SignatureHTML: `<p>{{ mailbox.name }} by {{ agent.first_name }}</p>`, BrandID: &side}}
	})
	var sent [][]byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, raw []byte) error { sent = append(sent, raw); return nil }

	id := w.receive(m, rawMail("Carla <"+customer()+">", "hello@side.test", "Side question", ""))
	tk := w.ticket(w.messageFor(id).TicketID)
	if tk.ReceivedAt != "hello@side.test" || tk.BrandID != side {
		t.Fatalf("ticket at %q brand %v (want %v)", tk.ReceivedAt, tk.BrandID, side)
	}
	if ok, err := domain.MailboxInBrand(w.ctx, w.d.DB.Pool, w.wid, side, m.ID); err != nil || !ok {
		t.Fatalf("the alias's brand counts the address as its own: %v %v", ok, err)
	}
	mid := w.agentReply(tk.ID, "Sure", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(sent[0]))
	if err != nil {
		t.Fatal(err)
	}
	if g := env.GetHeader("From"); g != `"Anna at Side Project" <notifications@opendesk.test>` {
		t.Errorf("From %q", g)
	}
	if g := env.GetHeader("Reply-To"); !strings.Contains(g, `"Side Project" <hello@side.test>`) {
		t.Errorf("Reply-To %q", g)
	}
	if !strings.Contains(env.HTML, "Side Project by Anna") || strings.Contains(env.HTML, "team signature") || strings.Contains(env.HTML, "address signature") {
		t.Errorf("alias signature first: %s", env.HTML)
	}

	// Written to the mailbox's own address: the team's signature, the mailbox's name and brand.
	id = w.receive(m, rawMail("Dan <"+customer()+">", m.Email, "Main question", ""))
	tk = w.ticket(w.messageFor(id).TicketID)
	if tk.BrandID != w.brand {
		t.Fatalf("brand %v", tk.BrandID)
	}
	mid = w.agentReply(tk.ID, "Sure", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	env, err = enmime.ReadEnvelope(bytes.NewReader(sent[len(sent)-1]))
	if err != nil {
		t.Fatal(err)
	}
	if g := env.GetHeader("From"); g != `"Anna at Acme Support" <notifications@opendesk.test>` {
		t.Errorf("From %q", g)
	}
	if !strings.Contains(env.HTML, "team signature") || strings.Contains(env.HTML, "Side Project") {
		t.Errorf("team signature: %s", env.HTML)
	}
}

// TestGmailSendAsPerGrant: a sync or api grant reads Gmail's send-as list and every alias
// sends as itself exactly when Gmail lists it verified; a send grant cannot read it (the
// check answers 422) and takes an admin's word instead.
func TestGmailSendAsPerGrant(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	var mu sync.Mutex
	var uploads [][]byte
	reads := 0
	var account string
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/gmail/v1/users/me/settings/sendAs":
			reads++
			_, _ = rw.Write([]byte(strings.Replace(gmailSendAsList, "%s", account, 1)))
		case r.Method == http.MethodPost && r.URL.Path == "/upload/gmail/v1/users/me/messages/send":
			uploads = append(uploads, readAll(r))
			_, _ = rw.Write([]byte(`{"id":"s-1","threadId":"th-1"}`))
		case r.URL.Path == "/gmail/v1/users/me/messages/s-1":
			_, _ = rw.Write([]byte(`{"payload":{"headers":[]}}`))
		default:
			return false
		}
		return true
	}
	w.s.providers[KindGoogle].tokenURL = f.srv.URL + "/token"
	w.collabClient(f, KindGoogle)
	w.s.gmailBase = f.srv.URL
	srv := w.api(w.admin)

	for _, grant := range []string{GrantSync, GrantAPI} {
		m := w.googleMailbox(grant, "billing@acme.test", "press@acme.test")
		account = m.Email
		var got Mailbox
		if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/"+m.ID.String()+"/send-as/check", nil, &got); code != 200 {
			t.Fatalf("%s check %d", grant, code)
		}
		if got.AliasSettings["billing@acme.test"].SendAs != SendAsVerified || got.AliasSettings["press@acme.test"].SendAs != SendAsUnverified ||
			got.AliasSettings["billing@acme.test"].SendAsBy != SendAsByProvider {
			t.Fatalf("%s send_as %+v", grant, got.AliasSettings)
		}
		ps := got.ProviderSendAs
		if ps == nil || !ps.Readable || ps.CheckedAt == nil || len(ps.Addresses) != 3 {
			t.Fatalf("%s list %+v", grant, ps)
		}
		// Gmail decides: an admin's word is refused.
		if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+m.ID.String(), map[string]any{"alias_settings": map[string]any{
			"press@acme.test": map[string]any{"send_as": "verified"}}}, nil); code != 422 {
			t.Fatalf("%s claim %d", grant, code)
		}
		// An alias added from the list takes Gmail's word at once.
		if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+m.ID.String(), map[string]any{
			"aliases": []string{"billing@acme.test", "press@acme.test", "jobs@acme.test"}}, &got); code != 200 ||
			got.AliasSettings["jobs@acme.test"].SendAs != SendAsVerified {
			t.Fatalf("%s added %d %+v", grant, code, got.AliasSettings)
		}
		// The next grant's mailbox takes the same aliases.
		mustExec(t, w.d, `DELETE FROM mailboxes WHERE id = $1`, m.ID)
	}
	if reads != 2 {
		t.Fatalf("reads %d", reads)
	}

	// A send grant: Google does not share the list; the admin says so instead.
	m := w.googleMailbox(GrantSend, "billing@acme.test")
	var got Mailbox
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes/"+m.ID.String()+"/send-as/check", nil, nil); code != 422 {
		t.Fatalf("send grant check %d", code)
	}
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/"+m.ID.String(), nil, &got); code != 200 ||
		got.ProviderSendAs == nil || got.ProviderSendAs.Readable || got.AliasSettings["billing@acme.test"].SendAs != SendAsUnknown {
		t.Fatalf("send grant %+v %+v", got.ProviderSendAs, got.AliasSettings)
	}
	if reads != 2 {
		t.Fatal("a send grant asked Gmail for the list")
	}
	from := customer()
	id := w.receive(m, rawMail("Carla <"+from+">", "billing@acme.test", "Invoice", ""))
	tk := w.ticket(w.messageFor(id).TicketID)
	// Unknown: From the mailbox, the alias as Reply-To.
	mid := w.agentReply(tk.ID, "One", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(uploads[0], []byte("From: \"Anna Agent at Acme Support\" <"+m.Email+">")) ||
		!bytes.Contains(uploads[0], []byte("Reply-To: \"Acme Support\" <billing@acme.test>")) {
		t.Fatalf("unknown alias mime %q", uploads[0])
	}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+m.ID.String(), map[string]any{"alias_settings": map[string]any{
		"billing@acme.test": map[string]any{"send_as": "verified"}}}, &got); code != 200 ||
		got.AliasSettings["billing@acme.test"].SendAsBy != SendAsByAdmin {
		t.Fatalf("admin claim %d %+v", code, got.AliasSettings)
	}
	// Verified: From the alias, through the Gmail API.
	mid = w.agentReply(tk.ID, "Two", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	last := uploads[len(uploads)-1]
	if !bytes.Contains(last, []byte("From: \"Anna Agent at Acme Support\" <billing@acme.test>")) || bytes.Contains(last, []byte("\r\nReply-To:")) {
		t.Fatalf("verified alias mime %q", last)
	}
	if msg := w.message(mid); msg.FromEmail != "billing@acme.test" {
		t.Fatalf("recorded From %q", msg.FromEmail)
	}
}

// TestGmailSendAsAtConnect: connecting with a grant that reads the list stores it, so the
// mailbox page can offer Gmail's other addresses.
func TestGmailSendAsAtConnect(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/userinfo":
			_, _ = rw.Write([]byte(`{"email":"desk@acme.test","email_verified":true}`))
		case "/gmail/v1/users/me/settings/sendAs":
			_, _ = rw.Write([]byte(strings.Replace(gmailSendAsList, "%s", "desk@acme.test", 1)))
		case "/gmail/v1/users/me/profile":
			_, _ = rw.Write([]byte(`{"historyId":"1"}`))
		default:
			return false
		}
		return true
	}
	w.collabClient(f, KindGoogle)
	w.s.gmailBase = f.srv.URL
	srv := w.api(w.admin)
	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"name": "Desk", "receive": "api"})
	loc := callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect %s", loc)
	}
	var got Mailbox
	if code := doJSON(t, http.MethodGet, srv.URL+"/v1/mailboxes/"+id.String(), nil, &got); code != 200 {
		t.Fatalf("get %d", code)
	}
	ps := got.ProviderSendAs
	if ps == nil || !ps.Readable || len(ps.Addresses) != 3 || ps.Addresses[0].Email != "billing@acme.test" || !ps.Addresses[0].Verified ||
		ps.Addresses[0].DisplayName != "Acme Billing" || ps.Addresses[1].Verified {
		t.Fatalf("list %+v", ps)
	}
	// Never added on its own.
	if len(got.Aliases) != 0 {
		t.Fatalf("aliases %v", got.Aliases)
	}
}

// TestGraphSendAsRefusedFallsBack: Microsoft refusing the alias as From sends the reply
// once more From the mailbox with the alias as Reply-To and marks the alias; the next reply
// goes that way at once.
func TestGraphSendAsRefusedFallsBack(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	var mu sync.Mutex
	var attempts [][]byte
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost || r.URL.Path != "/me/sendMail" {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		raw, _ := base64.StdEncoding.DecodeString(string(readAll(r)))
		attempts = append(attempts, raw)
		// Sending as the alias carries no Reply-To; the fallback answers to the alias.
		if !bytes.Contains(raw, []byte("\r\nReply-To: ")) {
			rw.WriteHeader(http.StatusForbidden)
			_, _ = rw.Write([]byte(`{"error":{"code":"ErrorSendAsDenied","message":"The user account which was used to submit this request does not have the right to send mail on behalf of the specified sending account."}}`))
			return true
		}
		rw.WriteHeader(http.StatusAccepted)
		return true
	}
	w.s.graphBase = f.srv.URL
	m := w.mailbox(InboundForwarding, KindMicrosoft, func(m *mailboxRow, c *credentials) {
		m.Email = "ops-" + crypto.RandomString(5) + "@acme.test"
		m.Aliases = []string{"sales@acme.test"}
		m.Config = MailboxConfig{OAuthEmail: m.Email, OAuthGrant: GrantSend}
		c.RefreshToken, c.GraphAccessToken, c.GraphExpiry = "refresh-0", "graph-tok", time.Now().Add(time.Hour)
	})
	if !m.sendsAs("sales@acme.test") {
		t.Fatal("Microsoft tries the alias until it refuses it")
	}
	id := w.receive(m, rawMail("Carla <"+customer()+">", "sales@acme.test", "Quote", ""))
	tk := w.ticket(w.messageFor(id).TicketID)
	mid := w.agentReply(tk.ID, "Here it is", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts %d", len(attempts))
	}
	if !bytes.Contains(attempts[1], []byte("<"+m.Email+">")) || !bytes.Contains(attempts[1], []byte("Reply-To: \"Acme Support\" <sales@acme.test>")) {
		t.Fatalf("fallback mime %q", attempts[1])
	}
	msg := w.message(mid)
	if msg.Delivery != "sent" || msg.FromEmail != m.Email {
		t.Fatalf("message %+v", msg)
	}
	reloaded, err := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st := reloaded.AliasSettings["sales@acme.test"]; st.SendAs != SendAsUnverified || st.SendAsBy != SendAsByProvider {
		t.Fatalf("alias not marked %+v", st)
	}
	// A retry or the next reply reads the mark: one attempt, From the mailbox.
	mid = w.agentReply(tk.ID, "And another", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 || !bytes.Contains(attempts[2], []byte("\r\nReply-To: ")) {
		t.Fatalf("after the mark: %d", len(attempts))
	}
}
