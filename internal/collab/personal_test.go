package collab

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/email"
)

// From names and signatures that name the sender (personal.go): filled for the member who
// writes and sends, refused when they use what a Collab mail has not, and never written
// to Gmail.

func TestTemplatedFromNameAndSignature(t *testing.T) {
	w := newWorld(t)
	fake := &fakeProvider{}
	w.useFake(fake)
	a := w.account(w.agent, ProviderIMAP, VisibilityShared, Audience{Everyone: true})
	mustExec(t, w.d, `UPDATE collab_accounts SET name = 'Margin Support' WHERE id = $1`, a.ID)
	mustExec(t, w.d, `UPDATE members SET title = 'Support lead', phone = '+15550100' WHERE workspace_id = $1 AND user_id = $2`, w.wid, w.agent2.UserID)
	owner, other := w.api(w.agent), w.api(w.agent2)
	base := "/v1/collab/accounts/" + a.ID.String()

	var out MailAccount
	sig := `<p>{{ agent.name }}, {{ agent.title }}</p><p><a href="tel:{{ agent.phone }}">Call</a> {{ mailbox.email }}</p>`
	if st := doJSON(t, "PATCH", owner.URL+base, map[string]any{"display_name": "  {agent}  at {mailbox} ", "signature_html": sig}, &out); st != 200 {
		t.Fatalf("patch: %d", st)
	}
	if out.DisplayName != "{agent} at {mailbox}" || !strings.Contains(out.SignatureHTML, "{{ agent.title }}") || !strings.Contains(out.SignatureHTML, "tel:{{ agent.phone }}") {
		t.Fatalf("stored as written: %q %q", out.DisplayName, out.SignatureHTML)
	}
	if me := out.Identities[0]; me.FromName != "Adam Agent at Margin Support" || me.FilledSignatureHTML == nil || !strings.Contains(*me.FilledSignatureHTML, "Adam Agent,") ||
		me.DisplayNameLocal || me.SignatureLocal {
		t.Fatalf("for the owner: %+v", me)
	}

	// Someone else the account is shared with sees it filled for themselves.
	var list []MailAccount
	if st := doJSON(t, "GET", other.URL+"/v1/collab/accounts", nil, &list); st != 200 || len(list) != 1 {
		t.Fatalf("list: %d %+v", st, list)
	}
	bea := list[0].Identities[0]
	if bea.FromName != "Bea Agent at Margin Support" || bea.FilledSignatureHTML == nil {
		t.Fatalf("for Bea: %+v", bea)
	}
	for _, want := range []string{"Bea Agent, Support lead", `href="tel:+15550100"`, strings.ToLower(a.Email)} {
		if !strings.Contains(*bea.FilledSignatureHTML, want) {
			t.Fatalf("Bea's signature lacks %q: %s", want, *bea.FilledSignatureHTML)
		}
	}

	// An alias without a From name of its own takes the account's, filled the same way.
	if st := doJSON(t, "POST", owner.URL+base+"/identities", map[string]any{"email": "sales@margin.test", "display_name": "{agent_first} from {workspace}",
		"signature_html": "<p>{{ mailbox.name }} &lt;{{ mailbox.email }}&gt;</p>"}, &out); st != 200 {
		t.Fatalf("add: %d", st)
	}
	if st := doJSON(t, "POST", owner.URL+base+"/identities", map[string]any{"email": "plain@margin.test"}, &out); st != 200 {
		t.Fatalf("add plain: %d", st)
	}
	sales, plain := identityOf(t, out, "sales@margin.test"), identityOf(t, out, "plain@margin.test")
	if sales.FromName != "Adam from Acme" || sales.FilledSignatureHTML == nil || !strings.Contains(*sales.FilledSignatureHTML, "Adam from Acme &lt;sales@margin.test&gt;") {
		t.Fatalf("sales: %+v", sales)
	}
	if plain.FromName != "Adam Agent at Margin Support" || plain.FilledSignatureHTML != nil {
		t.Fatalf("plain: %+v", plain)
	}

	// The From name is the sender's when the mail goes out.
	send := func(srvURL string, from string) *email.Parsed {
		t.Helper()
		fake.mu.Lock()
		before := len(fake.sent)
		fake.mu.Unlock()
		body := map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"cust@example.com"}, "subject": "Hi", "body_html": "<p>Hi</p>", "from_email": from}
		if st := doJSON(t, "POST", srvURL+"/v1/collab/messages", body, nil); st != 200 {
			t.Fatalf("send: %d", st)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.sent) == before {
			t.Fatal("nothing sent")
		}
		pm, err := email.ParseMail(fake.sent[len(fake.sent)-1])
		if err != nil {
			t.Fatal(err)
		}
		return pm
	}
	if pm := send(other.URL, a.Email); pm.FromName != "Bea Agent at Margin Support" {
		t.Fatalf("Bea sends as %q", pm.FromName)
	}
	if pm := send(owner.URL, "sales@margin.test"); pm.FromName != "Adam from Acme" || pm.FromAddr != "sales@margin.test" {
		t.Fatalf("Adam sends as %q <%s>", pm.FromName, pm.FromAddr)
	}

	// The same checks as the helpdesk's From names, and only the variables a Collab mail has.
	var e apiError
	for _, bad := range []map[string]any{
		{"display_name": "{agent} for {team}"}, {"display_name": "{agent} <x>"}, {"display_name": "{agent} " + strings.Repeat("a", 80)},
		{"signature_html": "<p>{{ ticket.subject }}</p>"}, {"signature_html": "<p>{{ contact.first_name }}</p>"}, {"signature_html": "<p>{{ agent.nmae }}</p>"},
	} {
		if st := doJSON(t, "PATCH", owner.URL+base, bad, &e); st != 422 {
			t.Fatalf("%v accepted: %d", bad, st)
		}
	}
	if st := doJSON(t, "PATCH", owner.URL+base+"/identities/"+sales.ID.String(), map[string]any{"display_name": "{brand}"}, &e); st != 422 {
		t.Fatalf("alias {brand}: %d", st)
	}

	// The preview fills what is typed, for the caller, without saving.
	var pv IdentityPreview
	if st := doJSON(t, "POST", owner.URL+base+"/identities/preview", map[string]any{"email": "sales@margin.test", "display_name": "{agent} at Margin",
		"signature_html": "<p>{{ mailbox.name }}</p><p>{{ ticket.subject }}</p>"}, &pv); st != 200 {
		t.Fatalf("preview: %d", st)
	}
	if pv.From != "Adam Agent at Margin <sales@margin.test>" || !strings.Contains(pv.SignatureHTML, "Adam Agent at Margin") || len(pv.Problems) != 1 || pv.FromNameProblem != "" {
		t.Fatalf("preview: %+v", pv)
	}
	if st := doJSON(t, "POST", owner.URL+base+"/identities/preview", map[string]any{"display_name": "{agent} {team}"}, &pv); st != 200 || pv.FromNameProblem == "" {
		t.Fatalf("preview problem: %d %+v", st, pv)
	}
}

func TestGmailNeverGetsTemplatedValues(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.setScopes(w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{}), googleModifyScope, googleSettingsScope)
	g.settings.signature, g.settings.displayName = "<p>Adam</p>", "Adam A."
	g.settings.aliases = []gmailSendAs{{SendAsEmail: "sales@acme.test", DisplayName: "Acme Sales", Signature: "<p>Sales</p>", VerificationStatus: "accepted"}}
	srv := w.api(w.agent)
	a = w.sync(a)
	base := srv.URL + "/v1/collab/accounts/" + a.ID.String()
	patches := func() []map[string]any {
		g.mu.Lock()
		defer g.mu.Unlock()
		return append([]map[string]any{}, g.settings.patches...)
	}

	// Saved in opendesk only: Gmail is not asked, and keeps its own.
	var out MailAccount
	if st := doJSON(t, "PATCH", base, map[string]any{"display_name": "{agent} at Acme", "signature_html": "<p>{{ agent.name }}</p>"}, &out); st != 200 {
		t.Fatalf("patch: %d", st)
	}
	if n := len(patches()); n != 0 {
		t.Fatalf("Gmail was sent a templated value: %v", patches())
	}
	if me := out.Identities[0]; !me.DisplayNameLocal || !me.SignatureLocal || me.FromName != "Adam Agent at Acme" || me.DisplayNameSync != SyncTwoWay {
		t.Fatalf("primary: %+v", me)
	}
	// A plain field changed with them still goes to Gmail, without the templated one.
	if st := doJSON(t, "PATCH", base, map[string]any{"display_name": "{agent} at Acme", "signature_html": "<p>Plain</p>"}, &out); st != 200 {
		t.Fatalf("patch plain signature: %d", st)
	}
	if p := patches(); len(p) != 1 || p[0]["signature"] != "<p>Plain</p>" || p[0]["displayName"] != nil {
		t.Fatalf("pushed: %v", p)
	}
	if st := doJSON(t, "PATCH", base, map[string]any{"signature_html": "<p>{{ agent.first_name }}</p>"}, &out); st != 200 {
		t.Fatalf("patch signature again: %d", st)
	}

	// Gmail's later changes do not replace them.
	g.mu.Lock()
	g.settings.displayName, g.settings.signature = "Changed in Gmail", "<p>Gmail sig</p>"
	g.settings.aliases[0].DisplayName, g.settings.aliases[0].Signature = "Gmail Sales", "<p>Gmail sales</p>"
	g.mu.Unlock()
	w.settingsDue(a)
	a = w.sync(a)
	m := w.apiAccount(srv, a)
	if m.DisplayName != "{agent} at Acme" || m.SignatureHTML != "<p>{{ agent.first_name }}</p>" {
		t.Fatalf("Gmail replaced a templated value: %q %q", m.DisplayName, m.SignatureHTML)
	}

	// An address Gmail lists other than the primary: the same.
	sales := identityOf(t, m, "sales@acme.test")
	path := base + "/identities/" + sales.ID.String()
	if st := doJSON(t, "PATCH", path, map[string]any{"display_name": "{agent_first} for Sales", "signature_html": "<p>{{ agent.name }}</p>"}, &out); st != 200 {
		t.Fatalf("patch alias: %d", st)
	}
	g.mu.Lock()
	g.settings.aliases[0].DisplayName, g.settings.aliases[0].Signature = "Gmail Sales 2", "<p>Gmail sales 2</p>"
	tried := g.settings.aliasPatches
	g.mu.Unlock()
	w.settingsDue(a)
	a = w.sync(a)
	sales = identityOf(t, w.apiAccount(srv, a), "sales@acme.test")
	if sales.DisplayName != "{agent_first} for Sales" || sales.SignatureHTML != "<p>{{ agent.name }}</p>" || sales.FromName != "Adam for Sales" ||
		!sales.DisplayNameLocal || tried != 0 {
		t.Fatalf("alias: %+v (%d patches)", sales, tried)
	}

	// "Get from Gmail" brings Gmail's back.
	if st := doJSON(t, "POST", base+"/display-name/pull", nil, &out); st != 200 || out.DisplayName != "Changed in Gmail" || out.Identities[0].DisplayNameLocal {
		t.Fatalf("pull name: %d %q", st, out.DisplayName)
	}
	if st := doJSON(t, "POST", base+"/signature/pull", nil, &out); st != 200 || out.SignatureHTML != "<p>Gmail sig</p>" {
		t.Fatalf("pull signature: %d %q", st, out.SignatureHTML)
	}
	if st := doJSON(t, "POST", path+"/display-name/pull", nil, &out); st != 200 || identityOf(t, out, "sales@acme.test").DisplayName != "Gmail Sales 2" {
		t.Fatalf("pull alias name: %d %+v", st, identityOf(t, out, "sales@acme.test"))
	}

	// Nothing with a variable ever reached Gmail.
	b, _ := json.Marshal(patches())
	if strings.Contains(string(b), "{") && strings.Contains(string(b), "agent") {
		t.Fatalf("Gmail got a variable: %s", b)
	}
}

func TestSharedDraftGoesOutAsItsSender(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	mustExec(t, w.d, `UPDATE collab_accounts SET display_name = '{agent} at Acme Support' WHERE id = $1`, a.ID)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	var d MailMessage
	if st := doJSON(t, "POST", w.api(w.agent).URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"frank@example.com"},
		"subject": "Plans", "body_html": "<p>Written by Adam</p>"}, &d); st != 200 {
		t.Fatalf("draft: %d", st)
	}
	g.mu.Lock()
	first := g.drafts.raws[len(g.drafts.raws)-1]
	g.mu.Unlock()
	if !strings.Contains(first, "Adam Agent at Acme Support") {
		t.Fatalf("draft From: %s", first)
	}
	// Bea sends it as saved: it goes out with her name.
	if st := doJSON(t, "POST", w.api(w.agent2).URL+"/v1/collab/drafts/"+d.ID.String()+"/send", nil, nil); st != 200 {
		t.Fatalf("send: %d", st)
	}
	g.mu.Lock()
	last := g.drafts.raws[len(g.drafts.raws)-1]
	sent := len(g.drafts.sent)
	g.mu.Unlock()
	pm, err := email.ParseMail([]byte(last))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || pm.FromName != "Bea Agent at Acme Support" || !strings.Contains(pm.HTML, "Written by Adam") {
		t.Fatalf("sent as %q (%d sent): %s", pm.FromName, sent, last)
	}
}
