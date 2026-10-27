package collab

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Addresses (identities.go): Gmail's send-as list synced two-way, hand-added aliases,
// sending from one, and the address a reply goes out from.

func identityOf(t *testing.T, m MailAccount, addr string) MailIdentity {
	t.Helper()
	for _, x := range m.Identities {
		if x.Email == addr {
			return x
		}
	}
	t.Fatalf("no address %s in %+v", addr, m.Identities)
	return MailIdentity{}
}

func TestGmailIdentities(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.setScopes(w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{}), googleModifyScope, googleSettingsScope)
	g.settings.signature, g.settings.displayName = "<p>Adam</p>", "Adam A."
	g.settings.aliases = []gmailSendAs{
		{SendAsEmail: "Sales@Acme.test", DisplayName: "Acme Sales", Signature: `<p>Sales team</p><script>x()</script>`,
			ReplyToAddress: "help@acme.test", VerificationStatus: "accepted", IsDefault: true},
		{SendAsEmail: "new@acme.test", DisplayName: "", Signature: "", VerificationStatus: "pending"},
	}
	srv := w.api(w.agent)

	// One list read brings the account's own settings and its other addresses.
	a = w.sync(a)
	m := w.apiAccount(srv, a)
	if len(m.Identities) != 3 || !m.Identities[0].Primary || m.Identities[0].Email != strings.ToLower(a.Email) || m.Identities[0].SignatureHTML != "<p>Adam</p>" ||
		m.Identities[0].SignatureSync != SyncTwoWay {
		t.Fatalf("identities: %+v", m.Identities)
	}
	sales := identityOf(t, m, "sales@acme.test")
	if sales.SignatureHTML != "<p>Sales team</p>" || sales.DisplayName != "Acme Sales" || sales.ReplyTo != "help@acme.test" || !sales.CanSend ||
		!sales.Default || sales.Source != SourceProvider || sales.SignatureSync != SyncPull || sales.DisplayNameSync != SyncPull || sales.SignatureSyncedAt == nil {
		t.Fatalf("sales: %+v", sales)
	}
	if pending := identityOf(t, m, "new@acme.test"); pending.CanSend || pending.Verification != VerificationPending || pending.Default {
		t.Fatalf("pending: %+v", pending)
	}
	if m.DefaultFrom != "sales@acme.test" || m.Identities[0].Default {
		t.Fatalf("default: %s", m.DefaultFrom)
	}

	// Gmail's change wins at the next read.
	g.mu.Lock()
	g.settings.aliases[0].Signature = "<p>Changed in Gmail</p>"
	g.mu.Unlock()
	w.settingsDue(a)
	a = w.sync(a)
	if got := identityOf(t, w.apiAccount(srv, a), "sales@acme.test"); got.SignatureHTML != "<p>Changed in Gmail</p>" {
		t.Fatalf("Gmail's change: %q", got.SignatureHTML)
	}

	// An edit in opendesk is kept in opendesk, sanitized: Gmail refuses apps' changes of
	// any address but the primary, so it is never asked, and nothing is refused.
	var out MailAccount
	path := srv.URL + "/v1/collab/accounts/" + a.ID.String() + "/identities/" + sales.ID.String()
	if st := doJSON(t, "PATCH", path, map[string]any{"signature_html": `<p>Hi <b>there</b></p><img src=x onerror=alert(1)>`,
		"display_name": "Acme Sales Team"}, &out); st != 200 {
		t.Fatalf("patch: %d", st)
	}
	saved := identityOf(t, out, "sales@acme.test")
	if saved.DisplayName != "Acme Sales Team" || !strings.Contains(saved.SignatureHTML, "<b>there</b>") || strings.Contains(saved.SignatureHTML, "onerror") ||
		saved.SignatureSync != SyncPull {
		t.Fatalf("saved: %+v", saved)
	}
	g.mu.Lock()
	tried := g.settings.aliasPatches
	g.mu.Unlock()
	if tried != 0 {
		t.Fatalf("Gmail asked to change an address that is not the primary: %d", tried)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'collab.account_updated'`, w.wid); n < 1 {
		t.Fatal("no audit")
	}

	// It stays through the next read while Gmail's value is unchanged, and is what goes out.
	w.settingsDue(a)
	a = w.sync(a)
	if got := identityOf(t, w.apiAccount(srv, a), "sales@acme.test"); got.SignatureHTML != saved.SignatureHTML || got.DisplayName != "Acme Sales Team" {
		t.Fatalf("the edit here was undone: %+v", got)
	}

	// "Get from Gmail" replaces it with Gmail's now.
	if st := doJSON(t, "POST", path+"/signature/pull", nil, &out); st != 200 {
		t.Fatalf("pull: %d", st)
	}
	if got := identityOf(t, out, "sales@acme.test"); got.SignatureHTML != "<p>Changed in Gmail</p>" || got.DisplayName != "Acme Sales Team" {
		t.Fatalf("pulled: %+v", got)
	}

	// A later change in Gmail replaces an edit made here.
	if st := doJSON(t, "PATCH", path, map[string]any{"signature_html": "<p>Ours</p>"}, &out); st != 200 {
		t.Fatalf("patch again: %d", st)
	}
	g.mu.Lock()
	g.settings.aliases[0].Signature = "<p>Gmail again</p>"
	g.mu.Unlock()
	w.settingsDue(a)
	a = w.sync(a)
	if got := identityOf(t, w.apiAccount(srv, a), "sales@acme.test"); got.SignatureHTML != "<p>Gmail again</p>" {
		t.Fatalf("Gmail's later change: %q", got.SignatureHTML)
	}

	// Gmail keeps its addresses: they cannot be added, removed or given a reply-to here.
	var e apiError
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/identities", map[string]any{"email": "x@acme.test"}, &e); st != 422 ||
		e.Code != "identities_from_provider" {
		t.Fatalf("add on Gmail: %d %+v", st, e)
	}
	if st := doJSON(t, "DELETE", path, nil, &e); st != 422 {
		t.Fatalf("remove on Gmail: %d", st)
	}
	if st := doJSON(t, "PATCH", path, map[string]any{"reply_to": "other@acme.test"}, &e); st != 422 {
		t.Fatalf("reply-to on Gmail: %d", st)
	}

	// Drafts carry it too, with the From name set here, and keep it when saved again without one.
	var d MailMessage
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"c@example.com"},
		"subject": "Draft", "body_html": "<p>x</p>", "from_email": "sales@acme.test"}, &d); st != 200 || d.From.Email != "sales@acme.test" || d.From.Name != "Acme Sales Team" {
		t.Fatalf("draft: %d %+v", st, d.From)
	}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"c@example.com"}, "subject": "Draft",
		"body_html": "<p>y</p>"}, &d); st != 200 || d.From.Email != "sales@acme.test" {
		t.Fatalf("draft saved again: %d %+v", st, d.From)
	}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"c@example.com"}, "subject": "Draft",
		"body_html": "<p>y</p>", "from_email": "new@acme.test"}, &e); st != 422 {
		t.Fatalf("draft from a pending address: %d", st)
	}

	// The default for new mail is chosen here and kept here; a pending address cannot be it.
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"default_from": "new@acme.test"}, &e); st != 422 {
		t.Fatalf("pending default: %d", st)
	}
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"default_from": a.Email}, &out); st != 200 ||
		out.DefaultFrom != strings.ToLower(a.Email) {
		t.Fatalf("default: %d %s", st, out.DefaultFrom)
	}
	g.mu.Lock()
	tried, patches := g.settings.aliasPatches, len(g.settings.patches)
	g.mu.Unlock()
	if tried != 0 || patches != 0 {
		t.Fatalf("Gmail written for the default: %d %d", tried, patches)
	}
	w.settingsDue(a)
	a = w.sync(a)
	if m = w.apiAccount(srv, a); m.DefaultFrom != strings.ToLower(a.Email) {
		t.Fatalf("default after the next read: %s", m.DefaultFrom)
	}
	// Gmail's own change of its default comes in.
	g.mu.Lock()
	g.settings.aliases[0].IsDefault, g.settings.primaryIsDefault = false, true
	g.mu.Unlock()
	w.settingsDue(a)
	a = w.sync(a)
	g.mu.Lock()
	g.settings.aliases[0].IsDefault, g.settings.primaryIsDefault = true, false
	g.mu.Unlock()
	w.settingsDue(a)
	a = w.sync(a)
	if m = w.apiAccount(srv, a); m.DefaultFrom != "sales@acme.test" {
		t.Fatalf("Gmail's default change: %s", m.DefaultFrom)
	}

	// An address Gmail no longer lists goes.
	g.mu.Lock()
	g.settings.aliases = g.settings.aliases[:1]
	g.mu.Unlock()
	w.settingsDue(a)
	a = w.sync(a)
	if m = w.apiAccount(srv, a); len(m.Identities) != 2 {
		t.Fatalf("after Gmail removed one: %+v", m.Identities)
	}

	// Without gmail.settings.basic the account's own is read only; the others work as before.
	a = w.setScopes(a, googleModifyScope)
	if st := doJSON(t, "PATCH", path, map[string]any{"signature_html": "<p>Local</p>"}, &out); st != 200 {
		t.Fatalf("read-only patch: %d", st)
	}
	if got := identityOf(t, out, "sales@acme.test"); got.SignatureHTML != "<p>Local</p>" || got.SignatureSync != SyncPull || out.Identities[0].SignatureSync != SyncReadOnly {
		t.Fatalf("read only: %+v", out.Identities)
	}

	// A hand-added address has nothing to pull.
	var mout MailAccount
	ms := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+ms.ID.String()+"/identities", map[string]any{"email": "x@acme.test"}, &mout); st != 200 {
		t.Fatalf("add: %d", st)
	}
	x := identityOf(t, mout, "x@acme.test")
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+ms.ID.String()+"/identities/"+x.ID.String()+"/signature/pull", nil, &e); st != 422 ||
		e.Code != "signature_sync_unsupported" {
		t.Fatalf("pull a hand-added address: %d %+v", st, e)
	}
}

func TestManualIdentitiesAndSendingFrom(t *testing.T) {
	w := newWorld(t)
	fake := &fakeProvider{}
	w.useFake(fake)
	a := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	srv := w.api(w.agent)
	base := srv.URL + "/v1/collab/accounts/" + a.ID.String()

	var out MailAccount
	if st := doJSON(t, "POST", base+"/identities", map[string]any{"email": "Billing <Billing@Acme.test>", "display_name": "Acme Billing",
		"signature_html": "<p>Billing</p><script>x()</script>", "reply_to": "accounts@acme.test"}, &out); st != 200 {
		t.Fatalf("add: %d", st)
	}
	billing := identityOf(t, out, "billing@acme.test")
	if billing.Source != SourceManual || billing.SignatureHTML != "<p>Billing</p>" || billing.SignatureSync != SyncNone || !billing.CanSend {
		t.Fatalf("added: %+v", billing)
	}
	var e apiError
	for _, body := range []map[string]any{{"email": "billing@acme.test"}, {"email": a.Email}} {
		if st := doJSON(t, "POST", base+"/identities", body, &e); st != 409 || e.Code != "identity_conflict" {
			t.Fatalf("conflict %v: %d %+v", body, st, e)
		}
	}
	if st := doJSON(t, "POST", base+"/identities", map[string]any{"email": "not an address"}, &e); st != 422 {
		t.Fatalf("invalid: %d", st)
	}
	// Someone who does not manage the account cannot.
	if st := doJSON(t, "POST", w.api(w.agent2).URL+"/v1/collab/accounts/"+a.ID.String()+"/identities", map[string]any{"email": "z@acme.test"}, nil); st != 404 {
		t.Fatalf("foreign add: %d", st)
	}

	// Sending from it: its From name, its reply-to, and the envelope from it.
	send := func(body map[string]any) (int, *email.Parsed) {
		t.Helper()
		fake.mu.Lock()
		before := len(fake.sent)
		fake.mu.Unlock()
		body["account_id"] = a.ID
		if body["mode"] == nil {
			body["mode"] = "new"
		}
		body["to"], body["subject"], body["body_html"] = []string{"cust@example.com"}, "Hi", "<p>Hi</p>"
		var res apiError
		st := doJSON(t, "POST", srv.URL+"/v1/collab/messages", body, &res)
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if st != 200 || len(fake.sent) == before {
			return st, nil
		}
		pm, err := email.ParseMail(fake.sent[len(fake.sent)-1])
		if err != nil {
			t.Fatal(err)
		}
		return st, pm
	}
	st, pm := send(map[string]any{"from_email": "BILLING@acme.test"})
	if st != 200 || pm.FromAddr != "billing@acme.test" || pm.FromName != "Acme Billing" || !strings.Contains(pm.Header("Reply-To"), "accounts@acme.test") {
		t.Fatalf("sent from the alias: %d %+v", st, pm)
	}
	if !strings.HasSuffix(strings.Trim(pm.MessageID, "<>"), "@acme.test") {
		t.Fatalf("message id: %s", pm.MessageID)
	}
	// Not one of the account's addresses: 422, nothing sent.
	if st, _ := send(map[string]any{"from_email": "ceo@elsewhere.test"}); st != 422 {
		t.Fatalf("foreign from: %d", st)
	}
	// New mail without one goes from the default.
	if st, pm = send(map[string]any{}); st != 200 || !strings.EqualFold(pm.FromAddr, a.Email) {
		t.Fatalf("default from: %d %s", st, pm.FromAddr)
	}
	if st := doJSON(t, "PATCH", base, map[string]any{"default_from": "billing@acme.test"}, &out); st != 200 || out.DefaultFrom != "billing@acme.test" {
		t.Fatalf("set default: %d %s", st, out.DefaultFrom)
	}
	if st, pm = send(map[string]any{}); st != 200 || pm.FromAddr != "billing@acme.test" {
		t.Fatalf("new default from: %d %s", st, pm.FromAddr)
	}

	// Removing the default address: new mail goes from the account's own address again.
	if st := doJSON(t, "DELETE", base+"/identities/"+billing.ID.String(), nil, &out); st != 200 || len(out.Identities) != 1 ||
		out.DefaultFrom != strings.ToLower(a.Email) {
		t.Fatalf("remove: %d %+v", st, out)
	}
}

func TestReplyGoesFromTheAddressWrittenTo(t *testing.T) {
	w := newWorld(t)
	fake := &fakeProvider{}
	w.useFake(fake)
	a := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	inbox := w.folder(a, RoleInbox)
	srv := w.api(w.agent)
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/identities", map[string]any{"email": "support@acme.test",
		"display_name": "Acme Support"}, nil); st != 200 {
		t.Fatalf("add: %d", st)
	}
	tid, mid := w.seedThread(a, "Help", inbox)
	mustExec(t, w.d, `UPDATE collab_messages SET to_addrs = $2, cc_addrs = $3 WHERE id = $1`, mid,
		[]Address{{Name: "Support", Email: "Support@acme.test"}}, []Address{{Email: a.Email}, {Email: "other@example.com"}})
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return recomputeThreads(w.ctx, tx, a, []uuid.UUID{tid}) })
	if err != nil {
		t.Fatal(err)
	}

	// The list shows the alias and filters by it; the message says which address it was written to.
	var page struct {
		Data []MailThread `json:"data"`
	}
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads?to=SUPPORT@acme.test", nil, &page); st != 200 || len(page.Data) != 1 ||
		page.Data[0].ToAddress == nil || *page.Data[0].ToAddress != "support@acme.test" {
		t.Fatalf("filtered: %d %+v", st, page.Data)
	}
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads?to=nobody@acme.test", nil, &page); st != 200 || len(page.Data) != 0 {
		t.Fatalf("other address: %d %d", st, len(page.Data))
	}
	var detail MailThreadDetail
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads/"+tid.String(), nil, &detail); st != 200 || detail.Messages[0].OwnAddress == nil ||
		*detail.Messages[0].OwnAddress != "support@acme.test" || len(detail.Account.Identities) != 2 {
		t.Fatalf("detail: %d %+v", st, detail.Account.Identities)
	}
	// Participants keep one of the account's addresses last, the alias included.
	if p := detail.Thread.Participants; len(p) == 0 || !strings.EqualFold(p[len(p)-1].Email, "support@acme.test") {
		t.Fatalf("participants: %+v", p)
	}

	// A reply without a From goes from the alias it answers.
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/messages", map[string]any{"account_id": a.ID, "mode": "reply_all", "message_id": mid,
		"to": []string{"from@example.com"}, "subject": "", "body_html": "<p>On it</p>"}, nil); st != 200 {
		t.Fatalf("reply: %d", st)
	}
	fake.mu.Lock()
	pm, err := email.ParseMail(fake.sent[len(fake.sent)-1])
	fake.mu.Unlock()
	if err != nil || pm.FromAddr != "support@acme.test" || pm.FromName != "Acme Support" {
		t.Fatalf("reply from: %v %+v", err, pm)
	}
}

func TestReplySenderChoice(t *testing.T) {
	a := &accountRow{Email: "me@acme.test", DisplayName: "Me"}
	idents := []*identityRow{
		{Email: "sales@acme.test", DisplayName: "", Verification: VerificationAccepted},
		{Email: "pending@acme.test", Verification: VerificationPending},
	}
	cases := []struct {
		m    messageRow
		want string
	}{
		{messageRow{From: Address{Email: "x@example.com"}, To: []Address{{Email: "SALES@acme.test"}}}, "sales@acme.test"},
		{messageRow{From: Address{Email: "x@example.com"}, To: []Address{{Email: "list@example.com"}}, DeliveredTo: []string{"sales@acme.test"}}, "sales@acme.test"},
		{messageRow{From: Address{Email: "sales@acme.test"}, To: []Address{{Email: "x@example.com"}}}, "sales@acme.test"},
		{messageRow{From: Address{Email: "x@example.com"}, To: []Address{{Email: "pending@acme.test"}}}, "me@acme.test"},
		{messageRow{From: Address{Email: "x@example.com"}, To: []Address{{Email: "me@acme.test"}}, Cc: []Address{{Email: "sales@acme.test"}}}, "me@acme.test"},
		{messageRow{From: Address{Email: "x@example.com"}}, "me@acme.test"},
	}
	for i, c := range cases {
		if got := replySender(a, idents, &c.m); got.Email != c.want {
			t.Errorf("case %d: %s, want %s", i, got.Email, c.want)
		}
	}
	// An address's empty From name is the account's.
	if s, ok := usableSender(a, idents, "sales@acme.test"); !ok || s.Name != "Me" {
		t.Fatalf("name: %+v", s)
	}
	// The default: a chosen one that can send, else the account's own.
	a.Config.Settings = &settingsState{DefaultFrom: "pending@acme.test"}
	if got := defaultSender(a, idents); got.Email != "me@acme.test" {
		t.Fatalf("pending default: %s", got.Email)
	}
	a.Config.Settings.DefaultFrom = "sales@acme.test"
	if got := replySender(a, idents, &messageRow{From: Address{Email: "x@example.com"}}); got.Email != "sales@acme.test" {
		t.Fatalf("default reply: %s", got.Email)
	}
	// Delivered-To and X-Original-To are read where a message was delivered.
	pm, err := email.ParseMail([]byte("Delivered-To: Me@acme.test\r\nX-Original-To: <sales@acme.test>\r\nFrom: x@example.com\r\nTo: list@example.com\r\nSubject: s\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := deliveredTo(pm); len(got) != 2 || got[0] != "me@acme.test" || got[1] != "sales@acme.test" {
		t.Fatalf("delivered to: %v", got)
	}
	// Exchange refusing an alias says why, in plain words.
	err = httpFailure("Microsoft", 403, []byte(`{"error":{"code":"ErrorSendAsDenied","message":"secret"}}`))
	if msg := safeError(err); !strings.Contains(msg, "allow sending from aliases") || strings.Contains(msg, "secret") {
		t.Fatalf("send as denied: %s", msg)
	}
}

func TestMicrosoftAddressSuggestions(t *testing.T) {
	w := newWorld(t)
	g := newFakeGraph(t)
	w.s.graphBase = g.srv.URL
	a := w.account(w.agent, ProviderMicrosoft, VisibilityPrivate, Audience{})
	srv := w.api(w.agent)
	var out struct {
		Addresses []string `json:"addresses"`
	}
	// Not shared (personal accounts, a refusal): no suggestions, no error.
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/identities/suggestions", nil, &out); st != 200 || len(out.Addresses) != 0 {
		t.Fatalf("refused: %d %v", st, out.Addresses)
	}
	g.mu.Lock()
	g.settings.proxies = []string{"SMTP:" + a.Email, "smtp:Sales@acme.test", "smtp:billing@acme.test", "SIP:me@acme.test", "x500:/o=foo"}
	g.mu.Unlock()
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/identities", map[string]any{"email": "billing@acme.test"}, nil); st != 200 {
		t.Fatalf("add: %d", st)
	}
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/identities/suggestions", nil, &out); st != 200 ||
		len(out.Addresses) != 1 || out.Addresses[0] != "sales@acme.test" {
		t.Fatalf("suggestions: %d %v", st, out.Addresses)
	}
}

func TestCountsByAddress(t *testing.T) {
	w := newWorld(t)
	w.useFake(&fakeProvider{})
	a := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	plain := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	inbox, plainInbox := w.folder(a, RoleInbox), w.folder(plain, RoleInbox)
	srv := w.api(w.agent)
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/identities", map[string]any{"email": "support@acme.test"}, nil); st != 200 {
		t.Fatalf("add: %d", st)
	}
	toSupport := func(subject string) uuid.UUID {
		tid, mid := w.seedThread(a, subject, inbox)
		mustExec(t, w.d, `UPDATE collab_messages SET to_addrs = $2 WHERE id = $1`, mid, []Address{{Email: "Support@acme.test"}})
		if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return recomputeThreads(w.ctx, tx, a, []uuid.UUID{tid}) }); err != nil {
			t.Fatal(err)
		}
		return tid
	}
	toSupport("Help")
	read := toSupport("Already read")
	mustExec(t, w.d, `UPDATE collab_threads SET unread = false WHERE id = $1`, read)
	own, _ := w.seedThread(a, "To me", inbox)
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return recomputeThreads(w.ctx, tx, a, []uuid.UUID{own}) }); err != nil {
		t.Fatal(err)
	}
	w.seedThread(plain, "Elsewhere", plainInbox)

	var counts CollabCounts
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/counts", nil, &counts); st != 200 {
		t.Fatalf("counts: %d", st)
	}
	got := counts.Addresses[a.ID]
	if got["support@acme.test"] != 1 || got[strings.ToLower(a.Email)] != 1 || len(got) != 2 {
		t.Fatalf("addresses: %+v", counts.Addresses)
	}
	// An account with only its own address has no numbers by address.
	if _, ok := counts.Addresses[plain.ID]; ok || counts.Accounts[plain.ID] != 1 {
		t.Fatalf("account without aliases: %+v", counts)
	}
}

// An address added in Gmail after the account was connected: the mail already written to it
// is found by it as soon as opendesk knows it, which "Check Gmail" and "Sync now" make at once.
func TestGmailAddressAddedLater(t *testing.T) {
	sales, support := "sales@acme.test", "support@acme.test"
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.setScopes(w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{}), googleModifyScope, googleSettingsScope)
	g.settings.aliases = []gmailSendAs{}
	g.add("m1", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "Frank <frank" + "@customer.test>", to: sales, subject: "To the alias",
		msgID: "m1@customer.test", body: "Hello sales"})
	a = w.sync(a)
	srv := w.api(w.agent)
	if m := w.apiAccount(srv, a); len(m.Identities) != 1 {
		t.Fatalf("before Gmail has it: %+v", m.Identities)
	}

	g.mu.Lock()
	g.settings.aliases = []gmailSendAs{{SendAsEmail: sales, DisplayName: "Sales", VerificationStatus: "accepted"}}
	g.mu.Unlock()
	// The next scheduled sync, inside the settings interval, does not read it yet.
	a = w.sync(a)
	if m := w.apiAccount(srv, a); len(m.Identities) != 1 {
		t.Fatalf("read before it was due: %+v", m.Identities)
	}

	// "Check Gmail" reads it now, and the mail written to it is found by it.
	var out MailAccount
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/identities/refresh", nil, &out); st != 200 {
		t.Fatalf("refresh: %d", st)
	}
	if x := identityOf(t, out, sales); !x.CanSend || x.Source != SourceProvider {
		t.Fatalf("new address: %+v", x)
	}
	var page httpx.Page[MailThread]
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads?account_id="+a.ID.String()+"&to="+sales, nil, &page); st != 200 ||
		len(page.Data) != 1 || page.Data[0].ToAddress == nil || *page.Data[0].ToAddress != sales {
		t.Fatalf("its mail: %d %+v", st, page.Data)
	}

	// "Sync now" reads the addresses at that sync, whenever they were last read.
	g.mu.Lock()
	g.settings.aliases = append(g.settings.aliases, gmailSendAs{SendAsEmail: support, VerificationStatus: "accepted"})
	g.mu.Unlock()
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/sync", nil, nil); st != 202 {
		t.Fatalf("sync now: %d", st)
	}
	a = w.sync(a)
	identityOf(t, w.apiAccount(srv, a), support)

	// Hand-added addresses have nothing to read.
	var e apiError
	imap := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+imap.ID.String()+"/identities/refresh", nil, &e); st != 422 || e.Code != "identities_by_hand" {
		t.Fatalf("refresh IMAP: %d %+v", st, e)
	}
}
