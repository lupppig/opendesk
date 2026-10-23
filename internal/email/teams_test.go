package email

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jhillyerd/enmime/v2"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/ids"
)

func (w *world) team(name string, private bool, signature string) uuid.UUID {
	w.t.Helper()
	id := ids.New()
	mustExec(w.t, w.d, `INSERT INTO teams (id, workspace_id, name, private, signature_html) VALUES ($1, $2, $3, $4, $5)`,
		id, w.wid, name, private, signature)
	return id
}

func (w *world) rule(name string, team uuid.UUID, position int, conds string) uuid.UUID {
	w.t.Helper()
	id := ids.New()
	mustExec(w.t, w.d, `INSERT INTO routing_rules (id, workspace_id, name, conditions, team_id, position) VALUES ($1, $2, $3, $4::jsonb, $5, $6)`,
		id, w.wid, name, conds, team, position)
	return id
}

func rawMail(from, to, subject, extra string) string {
	return "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMessage-ID: <t" + crypto.RandomString(10) + "@c.test>\r\n" +
		extra + "\r\nHello there\r\n"
}

func TestInboundRoutingAndAlias(t *testing.T) {
	w := newWorld(t)
	support := w.team("Support", false, "")
	billing := w.team("Billing", false, "")
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.TeamID = &support
		m.Aliases = []string{"sales@acme.test"}
	})
	rule := w.rule("Invoices", billing, 0, `[{"field":"subject","op":"contains","value":"INVOICE"}]`)

	// Written to the alias, about an invoice: the rule wins over the address's team.
	id := w.receive(m, rawMail(customer(), "Sales <sales@acme.test>", "My invoice is wrong", ""))
	msg := w.messageFor(id)
	tk := w.ticket(msg.TicketID)
	if tk.TeamID == nil || *tk.TeamID != billing || tk.Routed == nil || tk.Routed.Via != "rule" || tk.Routed.RuleID == nil || *tk.Routed.RuleID != rule {
		t.Fatalf("routed %+v team %v", tk.Routed, tk.TeamID)
	}
	if tk.ReceivedAt != "sales@acme.test" || tk.OriginMailboxID == nil || *tk.OriginMailboxID != m.ID || tk.BrandID != w.brand {
		t.Fatalf("arrival %q %v %v", tk.ReceivedAt, tk.OriginMailboxID, tk.BrandID)
	}
	var runs int
	var mailbox *uuid.UUID
	w.d.DB.Pool.QueryRow(w.ctx, `SELECT run_count FROM routing_rules WHERE id = $1`, rule).Scan(&runs)
	w.d.DB.Pool.QueryRow(w.ctx, `SELECT mailbox_id FROM messages WHERE id = $1`, msg.ID).Scan(&mailbox)
	if runs != 1 || mailbox == nil || *mailbox != m.ID {
		t.Fatalf("run_count %d message mailbox %v", runs, mailbox)
	}

	// Anything else gets the address's team; the mailbox address is where it arrived.
	tk2 := w.ticket(w.messageFor(w.receive(m, rawMail(customer(), "help <"+m.Email+">", "Hello", ""))).TicketID)
	if tk2.TeamID == nil || *tk2.TeamID != support || tk2.Routed == nil || tk2.Routed.Via != "address" || tk2.ReceivedAt != strings.ToLower(m.Email) {
		t.Fatalf("address routing %+v %v %q", tk2.Routed, tk2.TeamID, tk2.ReceivedAt)
	}

	// A reply on an existing ticket never re-routes.
	contact := *tk2.Contact.Email
	reply := w.agentReply(tk2.ID, "Hi", "")
	ours := messageIDFor("m", reply, tk2.Number, m)
	mustExec(t, w.d, `UPDATE messages SET email_message_id = $1, delivery_status = 'sent' WHERE id = $2`, ours, reply)
	again := w.messageFor(w.receive(m, rawMail(contact, "sales@acme.test", "Re: invoice", "In-Reply-To: "+ours+"\r\n")))
	if again.TicketID != tk2.ID {
		t.Fatal("reply did not thread")
	}
	if tk := w.ticket(tk2.ID); *tk.TeamID != support || tk.ReceivedAt != strings.ToLower(m.Email) {
		t.Fatalf("reply re-routed: %v %q", tk.TeamID, tk.ReceivedAt)
	}
	w.d.DB.Pool.QueryRow(w.ctx, `SELECT run_count FROM routing_rules WHERE id = $1`, rule).Scan(&runs)
	if runs != 1 {
		t.Fatalf("run_count %d", runs)
	}
}

func TestAliasTeams(t *testing.T) {
	w := newWorld(t)
	support := w.team("Support", false, "")
	projects := w.team("Projects", false, "")
	m := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.TeamID = &support
		m.Aliases = []string{"projects@acme.test", "hello@acme.test"}
	})
	mustExec(t, w.d, `UPDATE mailboxes SET alias_teams = jsonb_build_object('projects@acme.test', $2::text) WHERE id = $1`, m.ID, projects.String())

	// Written to an alias with a team: that team, routed by its address. The message records the alias.
	msg := w.messageFor(w.receive(m, rawMail(customer(), "Projects <Projects@acme.test>", "Kickoff", "")))
	tk := w.ticket(msg.TicketID)
	if tk.TeamID == nil || *tk.TeamID != projects || tk.Routed == nil || tk.Routed.Via != "address" || tk.ReceivedAt != "projects@acme.test" {
		t.Fatalf("alias team: %v %+v %q", tk.TeamID, tk.Routed, tk.ReceivedAt)
	}
	var at string
	w.d.DB.Pool.QueryRow(w.ctx, `SELECT received_at FROM messages WHERE id = $1`, msg.ID).Scan(&at)
	if at != "projects@acme.test" {
		t.Fatalf("message received_at %q", at)
	}
	// An alias without a team, and the address itself: the mailbox's team.
	for _, to := range []string{"hello@acme.test", m.Email} {
		tk := w.ticket(w.messageFor(w.receive(m, rawMail(customer(), to, "Hi", ""))).TicketID)
		if tk.TeamID == nil || *tk.TeamID != support {
			t.Fatalf("%s: team %v", to, tk.TeamID)
		}
	}
	// A private team's address delivers every alias to that team.
	vault := w.team("Vault", true, "")
	mustExec(t, w.d, `UPDATE mailboxes SET team_id = $2 WHERE id = $1`, m.ID, vault)
	if tk := w.ticket(w.messageFor(w.receive(m, rawMail(customer(), "projects@acme.test", "Again", ""))).TicketID); tk.TeamID == nil || *tk.TeamID != vault {
		t.Fatalf("private mailbox: team %v", tk.TeamID)
	}
}

func TestAliasTeamsAPI(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.admin)
	support := w.team("Support", false, "")
	projects := w.team("Projects", false, "")
	foreign := newWorld(t).team("Theirs", false, "")
	var a Mailbox
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", map[string]any{"name": "Help", "email": "help-" + crypto.RandomString(5) + "@acme.test",
		"inbound_kind": "forwarding", "outbound_kind": "system", "team_id": support, "aliases": []string{"projects@acme.test", "hello@acme.test"},
		"alias_teams": map[string]any{"Projects@Acme.test": projects}}, &a); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if len(a.AliasTeams) != 1 || a.AliasTeams["projects@acme.test"] != projects {
		t.Fatalf("alias_teams %v", a.AliasTeams)
	}
	url := srv.URL + "/v1/mailboxes/" + a.ID.String()
	for _, bad := range []map[string]any{
		{"alias_teams": map[string]any{"other@acme.test": projects}}, // not an alias
		{"alias_teams": map[string]any{"hello@acme.test": foreign}},  // another workspace's team
	} {
		if code := doJSON(t, http.MethodPatch, url, bad, nil); code != 422 {
			t.Errorf("patch %v: %d, want 422", bad, code)
		}
	}
	// A private team's address takes no alias teams.
	vault := w.team("Vault", true, "")
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"team_id": vault.String()}, nil); code != 422 {
		t.Fatalf("private team with alias teams: %d", code)
	}
	// Removing an alias drops its team.
	a = Mailbox{}
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"aliases": []string{"hello@acme.test"}}, &a); code != 200 || len(a.AliasTeams) != 0 {
		t.Fatalf("drop alias: %d %v", code, a.AliasTeams)
	}
	if code := doJSON(t, http.MethodPatch, url, map[string]any{"alias_teams": map[string]any{"hello@acme.test": projects}}, &a); code != 200 ||
		a.AliasTeams["hello@acme.test"] != projects {
		t.Fatalf("set: %d %v", code, a.AliasTeams)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.updated' AND data ? 'alias_teams'`, w.wid); n != 2 {
		t.Fatalf("audit alias_teams %d", n)
	}
}

func TestMailBetweenWorkspaceMailboxes(t *testing.T) {
	w := newWorld(t)
	a := w.mailbox(InboundForwarding, OutboundSystem, nil)
	b := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.Aliases = []string{"billing-alias@acme.test"}
		m.AutoReplyEnabled, m.AutoReplyHTML = true, "<p>Got it</p>"
	})

	// A teammate forwards mail from one address to another: a ticket opens there.
	id := w.receive(b, rawMail("Support <"+a.Email+">", b.Email, "Fwd: customer question", ""))
	if st, reason := w.inbound(id); st != "processed" {
		t.Fatalf("forward from another address: %s %s", st, reason)
	}
	tk := w.ticket(w.messageFor(id).TicketID)
	if tk.MailboxID == nil || *tk.MailboxID != b.ID {
		t.Fatalf("ticket mailbox %v", tk.MailboxID)
	}
	// Nothing automatic answers it, so the two addresses cannot loop.
	if n := w.jobCount("email.send_ticket_email", "", ""); n != 0 {
		t.Fatalf("auto-reply to a teammate: %d", n)
	}

	cases := []struct {
		name, raw, reason string
	}{
		{"own address", rawMail(b.Email, b.Email, "x", ""), "from_own_mailbox"},
		{"own alias", rawMail("billing-alias@acme.test", b.Email, "x", ""), "from_own_mailbox"},
		{"forwarding address", rawMail(a.ForwardingToken+"+t1@in.test", b.Email, "x", ""), "from_own_mailbox"},
		// Mail we sent from another address, still carrying our Message-ID.
		{"our message id", "From: " + a.Email + "\r\nTo: " + b.Email + "\r\nSubject: x\r\nMessage-ID: <m_" + ids.New().String() + ".t4@acme.test>\r\n\r\nx\r\n", "from_own_mailbox"},
		// Mail we sent coming back with a new Message-ID: X-Loop names our address.
		{"x-loop", rawMail(customer(), b.Email, "x", "X-Loop: "+a.Email+"\r\n"), "loop"},
		{"auto-submitted", rawMail(a.Email, b.Email, "x", "Auto-Submitted: auto-replied\r\n"), "auto_submitted"},
	}
	for _, c := range cases {
		id := w.receive(b, c.raw)
		if st, reason := w.inbound(id); st != "ignored" || reason != c.reason {
			t.Errorf("%s: %s %s, want ignored %s", c.name, st, reason, c.reason)
		}
	}
}

func TestAutoReplyPerSenderAndAddress(t *testing.T) {
	w := newWorld(t)
	mustExec(t, w.d, `UPDATE brands SET name = 'Acme & Co' WHERE id = $1`, w.brand)
	billing := w.team("Billing", false, "")
	mk := func(team *uuid.UUID) *mailboxRow {
		return w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) {
			m.TeamID = team
			m.AutoReplyEnabled, m.AutoReplySubject = true, "{{team.name}} got it"
			m.AutoReplyHTML = "<p>{{brand.name}} / {{team.name}} / {{mailbox.email}} / {{agent.name}}</p>"
		})
	}
	one, two := mk(&billing), mk(nil)
	from := customer()
	w.receive(one, rawMail(from, one.Email, "First", ""))
	w.receive(one, rawMail(from, one.Email, "Second", ""))
	w.receive(two, rawMail(from, two.Email, "Third", ""))
	var got [][2]string
	for _, env := range w.sendQueued(t) {
		got = append(got, [2]string{env.GetHeader("Subject"), env.HTML})
	}
	if len(got) != 2 {
		t.Fatalf("want one auto-reply per sender per address, got %d", len(got))
	}
	if !strings.HasPrefix(got[0][0], "Billing got it [#") || !strings.Contains(got[0][1], "Acme &amp; Co / Billing / "+strings.ToLower(one.Email)+" / </p>") {
		t.Errorf("first auto-reply %q", got[0])
	}
	// Without a team, {{team.name}} reads the brand.
	if !strings.HasPrefix(got[1][0], "Acme &amp; Co got it") && !strings.HasPrefix(got[1][0], "Acme & Co got it") {
		t.Errorf("second auto-reply %q", got[1])
	}
}

func TestOutboundAddressNameAndSignature(t *testing.T) {
	w := newWorld(t)
	billing := w.team("Billing", false, `<p>{{agent.name}} | {{team.name}} | {{contact.name}}</p>`)
	m := w.mailbox(InboundIMAP, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.TeamID = &billing
		m.Aliases = []string{"invoices@acme.test"}
		m.FromNameFormat = "{agent_first} from {team}"
		m.SignatureHTML = "<p>mailbox signature</p>"
	})
	var sent [][]byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, raw []byte) error { sent = append(sent, raw); return nil }
	from := customer()
	id := w.receive(m, rawMail("Carla <"+from+">", "invoices@acme.test", "Refund", ""))
	tk := w.ticket(w.messageFor(id).TicketID)
	if tk.ReceivedAt != "invoices@acme.test" {
		t.Fatalf("received_at %q", tk.ReceivedAt)
	}
	mid := w.agentReply(tk.ID, "On it", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(sent[0]))
	if err != nil {
		t.Fatal(err)
	}
	if g := env.GetHeader("From"); g != `"Anna from Billing" <notifications@opendesk.test>` {
		t.Errorf("From %q", g)
	}
	// The platform sender answers to the alias the customer wrote to.
	if g := env.GetHeader("Reply-To"); !strings.Contains(g, "<invoices@acme.test>") {
		t.Errorf("Reply-To %q", g)
	}
	// X-Loop names the mailbox by an opaque token, never its address.
	if g := env.GetHeader("X-Loop"); g != w.s.loopToken(m.ID) || strings.Contains(g, "@") {
		t.Errorf("X-Loop %q", g)
	}
	if !strings.Contains(env.HTML, "Anna Agent | Billing | Carla") || strings.Contains(env.HTML, "mailbox signature") {
		t.Errorf("signature %s", env.HTML)
	}
	var fromEmail, fromName string
	var mailbox *uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT from_email, from_name, mailbox_id FROM messages WHERE id = $1`, mid).
		Scan(&fromEmail, &fromName, &mailbox); err != nil {
		t.Fatal(err)
	}
	if fromEmail != "notifications@opendesk.test" || fromName != "Anna from Billing" || mailbox == nil || *mailbox != m.ID {
		t.Errorf("recorded %q %q %v", fromEmail, fromName, mailbox)
	}

	// Without an author (auto-replies, CSAT) the agent and its words drop out.
	if err := w.s.sendTicketEmail(w.ctx, jobsSendTicketEmail(w.wid, tk.ID, "", "<p>Thanks</p>")); err != nil {
		t.Fatal(err)
	}
	env, err = enmime.ReadEnvelope(bytes.NewReader(sent[1]))
	if err != nil {
		t.Fatal(err)
	}
	if g := env.GetHeader("From"); g != `"Billing" <notifications@opendesk.test>` {
		t.Errorf("auto From %q", g)
	}
	if !strings.Contains(env.HTML, " | Billing | Carla") {
		t.Errorf("auto signature %s", env.HTML)
	}
}

func TestOutboundSMTPSendsAsAlias(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundIMAP, OutboundSystem, func(m *mailboxRow, _ *credentials) {
		m.Aliases = []string{"team@acme.test"}
	})
	m.OutboundKind = OutboundSMTP
	from, replyTo, env := w.s.addressing(m, "Anna at Support", "team@acme.test")
	if from.Address != "team@acme.test" || replyTo != nil || env != m.Email {
		t.Fatalf("smtp alias: %v %v %q", from, replyTo, env)
	}
	m.OutboundKind = KindGoogle
	from, replyTo, _ = w.s.addressing(m, "Anna at Support", "team@acme.test")
	if from.Address != m.Email || replyTo == nil || replyTo.Address != "team@acme.test" {
		t.Fatalf("google alias: %v %v", from, replyTo)
	}
}

func TestOutboundTicketWithoutAddressTakesBrandAddress(t *testing.T) {
	w := newWorld(t)
	sales := w.team("Sales", false, "")
	other := w.mailbox(InboundForwarding, OutboundSystem, nil)
	teamBox := w.mailbox(InboundForwarding, OutboundSystem, func(m *mailboxRow, _ *credentials) { m.TeamID = &sales })
	_ = other
	var raw []byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, b []byte) error { raw = b; return nil }
	contact := ids.New()
	mustExec(t, w.d, `INSERT INTO contacts (id, workspace_id, email, name) VALUES ($1, $2, $3, 'Carla')`, contact, w.wid, customer())
	tid := ids.New()
	mustExec(t, w.d, `INSERT INTO tickets (id, workspace_id, number, subject, channel, contact_id, team_id, brand_id)
		VALUES ($1, $2, 7, 'API ticket', 'email', $3, $4, $5)`, tid, w.wid, contact, sales, w.brand)
	mid := w.agentReply(tid, "Hello", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		t.Fatal("not sent")
	}
	tk := w.ticket(tid)
	if tk.MailboxID == nil || *tk.MailboxID != teamBox.ID {
		t.Fatalf("ticket took %v, want the team's address %v", tk.MailboxID, teamBox.ID)
	}
}

func TestMailboxAddressesAPI(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.admin)
	brand2 := ids.New()
	mustExec(t, w.d, `INSERT INTO brands (id, workspace_id, name, slug) VALUES ($1, $2, 'Second', $3)`, brand2, w.wid, "second-"+crypto.RandomString(6))
	foreign := ids.New()
	other := newWorld(t)
	mustExec(t, w.d, `INSERT INTO brands (id, workspace_id, name, slug) VALUES ($1, $2, 'Theirs', $3)`, foreign, other.wid, "theirs-"+crypto.RandomString(6))

	base := func(extra map[string]any) map[string]any {
		b := map[string]any{"name": "Help", "email": "help-" + crypto.RandomString(5) + "@acme.test", "inbound_kind": "forwarding", "outbound_kind": "system"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	var a Mailbox
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", base(map[string]any{"aliases": []string{"Sales@Acme.test"},
		"from_name_format": "{agent} from {brand}"}), &a); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if a.BrandID != w.brand || len(a.Aliases) != 1 || a.Aliases[0] != "sales@acme.test" || a.FromNameFormat != "{agent} from {brand}" || a.Private {
		t.Fatalf("mailbox %+v", a)
	}
	var b Mailbox
	if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", base(map[string]any{"brand_id": brand2}), &b); code != http.StatusCreated || b.BrandID != brand2 {
		t.Fatalf("create in brand: %d %v", code, b.BrandID)
	}
	if b.FromNameFormat != defaultFromNameFormat {
		t.Fatalf("default format %q", b.FromNameFormat)
	}
	bad := []map[string]any{
		{"brand_id": foreign},
		{"aliases": []string{"x@in.test"}},
		{"aliases": []string{"sales@acme.test"}},        // another mailbox's alias
		{"aliases": []string{strings.ToLower(a.Email)}}, // another mailbox's address
		{"aliases": []string{"dup@acme.test", "DUP@acme.test"}},
		{"aliases": []string{"not an address"}},
		{"from_name_format": "{agent} <x>"},
		{"from_name_format": "{agent} from {company}"},
		{"from_name_format": strings.Repeat("a", 81)},
	}
	for _, extra := range bad {
		if code := doJSON(t, http.MethodPost, srv.URL+"/v1/mailboxes", base(extra), nil); code != 422 {
			t.Errorf("create %v: %d, want 422", extra, code)
		}
	}
	// Moving an address to another brand is allowed; a foreign brand is not.
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+a.ID.String(), map[string]any{"brand_id": brand2.String()}, &a); code != 200 || a.BrandID != brand2 {
		t.Fatalf("move brand: %d %v", code, a.BrandID)
	}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+a.ID.String(), map[string]any{"brand_id": foreign.String()}, nil); code != 422 {
		t.Fatalf("foreign brand: %d", code)
	}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+b.ID.String(), map[string]any{"email": "sales@acme.test"}, nil); code != 422 {
		t.Fatalf("address taken as an alias: %d", code)
	}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+a.ID.String(), map[string]any{"aliases": []string{}, "from_name_format": ""}, &a); code != 200 ||
		len(a.Aliases) != 0 || a.FromNameFormat != defaultFromNameFormat {
		t.Fatalf("clear: %d %+v", code, a)
	}
	// private follows the team.
	team := w.team("Private team", true, "")
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+a.ID.String(), map[string]any{"team_id": team.String()}, &a); code != 200 || !a.Private {
		t.Fatalf("private: %d %v", code, a.Private)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.updated' AND data->'changes' ? 'brand_id'`, w.wid); n != 1 {
		t.Fatalf("audit brand move %d", n)
	}
}

func TestFromName(t *testing.T) {
	n := senderNames{Agent: "Anna Agent", Team: "Billing", Brand: "Acme", Mailbox: "Acme Support"}
	none := n
	none.Agent = ""
	noTeam := none
	noTeam.Team = ""
	cases := []struct {
		format string
		names  senderNames
		want   string
	}{
		{"{agent} at {mailbox}", n, "Anna Agent at Acme Support"},
		{"{agent} at {mailbox}", none, "Acme Support"},
		{"{agent_first} from {team}", n, "Anna from Billing"},
		{"{agent_first} from {team}", none, "Billing"},
		{"{agent_first} from {team}", noTeam, "Acme"},
		{"{agent}", none, "Acme Support"},
		{"{agent} from Acme", none, "Acme Support"},
		{"{brand} - {agent}", none, "Acme"},
		{"{brand} support", n, "Acme support"},
		{"", n, "Anna Agent at Acme Support"},
	}
	for _, c := range cases {
		if got := fromName(c.format, c.names); got != c.want {
			t.Errorf("fromName(%q, agent %q) = %q, want %q", c.format, c.names.Agent, got, c.want)
		}
	}
	for _, f := range []string{"{agent} at {mailbox}", "Team {team}", "{brand}", "Support desk"} {
		if _, err := cleanFromNameFormat(f); err != nil {
			t.Errorf("%q refused: %v", f, err)
		}
	}
	for _, f := range []string{"{agent} {x}", "{agent", "a <b>", "a\x01"} {
		if _, err := cleanFromNameFormat(f); err == nil {
			t.Errorf("%q accepted", f)
		}
	}
}

func TestReceivedAddress(t *testing.T) {
	m := &mailboxRow{}
	m.Email, m.Aliases = "Support@Acme.test", []string{"sales@acme.test", "billing@acme.test"}
	pm := &parsedMail{To: []string{"someone@else.test", "billing@acme.test"}, Cc: []string{"sales@acme.test"}}
	if got := receivedAddress(m, pm, "tok@in.test"); got != "billing@acme.test" {
		t.Fatalf("got %q", got)
	}
	if got := receivedAddress(m, &parsedMail{To: []string{"x@y.test"}}, "SALES@acme.test"); got != "sales@acme.test" {
		t.Fatalf("rcpt %q", got)
	}
	if got := receivedAddress(m, &parsedMail{To: []string{"x@y.test"}}, ""); got != "support@acme.test" {
		t.Fatalf("fallback %q", got)
	}
}
