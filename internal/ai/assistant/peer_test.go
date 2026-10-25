package assistant

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/collab"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Asking a teammate's Ollie: the agent asks the admin.

func (e *env) peerOn() {
	e.Exec(`UPDATE workspaces SET settings = jsonb_set(settings, '{ai}', COALESCE(settings->'ai', '{}'::jsonb) || '{"assistant_peer": true}')
		WHERE id = $1`, e.WS.ID)
}

// mailAccount adds a Collab account owned by owner.
func (e *env) mailAccount(owner uuid.UUID, visibility string) uuid.UUID {
	id := ids.New()
	e.Exec(`INSERT INTO collab_accounts (id, workspace_id, owner_id, provider, email, visibility, status)
		VALUES ($1, $2, $3, 'google', $4, $5, 'active')`, id, e.WS.ID, owner, "box-"+id.String()+"@acme.test", visibility)
	return id
}

// mail stores a conversation in an account, the way a sync does.
func (e *env) mail(account uuid.UUID, subject, body string) {
	tid, mid := ids.New(), ids.New()
	e.Exec(`INSERT INTO collab_threads (id, workspace_id, account_id, remote_id, subject, snippet, search)
		VALUES ($1, $2, $3, $4, $5, '', to_tsvector('simple', $5 || ' ' || $6))`, tid, e.WS.ID, account, "t-"+tid.String(), subject, body)
	e.Exec(`INSERT INTO collab_messages (id, workspace_id, account_id, thread_id, remote_id, from_addr, subject, body_text)
		VALUES ($1, $2, $3, $4, $5, '{"name": "Dana Buyer", "email": "dana@customer.test"}', $6, $7)`,
		mid, e.WS.ID, account, tid, "m-"+mid.String(), subject, body)
}

func (e *env) putPeer(u hdtest.User, body map[string]any) *hdtest.Resp {
	return e.Do("PUT", "/v1/me/assistant-peer", u.As(e.WS), body)
}

func peerBody(over map[string]any) map[string]any {
	b := map[string]any{"enabled": true, "share_with": "everyone", "audience": map[string]any{"user_ids": []string{}, "team_ids": []string{}},
		"all_accounts": true, "default_mode": "summary", "accounts": []any{}, "rules": ""}
	for k, v := range over {
		b[k] = v
	}
	return b
}

// ask runs one ask_teammate call from the agent and returns the finished step. model are
// the scripted answers of the teammate's Ollie, if any is expected.
func (e *env) ask(question string, model ...string) (Step, int) {
	e.T.Helper()
	before := len(e.M.reqs)
	e.M.queue(step{tool: "ask_teammate", args: map[string]any{"teammate": e.WS.Admin.Email, "question": question}})
	for _, m := range model {
		e.M.queue(step{text: m})
	}
	e.M.queue(step{text: "OK."})
	agent := e.WS.Agent.As(e.WS)
	_, evs, body := e.stream("/v1/assistant/sessions/"+e.session(agent)+"/messages", agent, map[string]any{"text": "Ask the admin"})
	tools := find(evs, "tool")
	if len(tools) == 0 {
		e.T.Fatalf("no tool events: %s %v", body, evs)
	}
	var st Step
	_ = json.Unmarshal(tools[len(tools)-1].Data, &st)
	return st, len(e.M.reqs) - before
}

func TestAskTeammateGates(t *testing.T) {
	e := newEnv(t, false)
	acct := e.mailAccount(e.WS.Admin.ID, "private")
	e.mail(acct, "Acme renewal", "The Acme renewal is on March 14 and they want the new pricing first.")

	// The workspace switch is off: the tool is not offered, and a call is refused without a model call.
	st, calls := e.ask("When is the Acme renewal?")
	tools, _ := json.Marshal(e.M.reqs[len(e.M.reqs)-2]["tools"])
	if strings.Contains(string(tools), "ask_teammate") {
		t.Fatal("ask_teammate offered while the workspace switch is off")
	}
	if st.Status != "error" || calls != 2 {
		t.Fatalf("switch off: %+v, %d model calls", st, calls)
	}

	e.peerOn()
	// The admin has not opted in.
	st, calls = e.ask("When is the Acme renewal?")
	if st.Peer == nil || st.Peer.Result != peerNotShared || calls != 2 || st.Label != "Asked "+e.WS.Admin.Name+"'s Ollie" {
		t.Fatalf("not opted in: %+v, %d model calls", st, calls)
	}
	var n int
	e.Scalar(`SELECT count(*) FROM assistant_peer_asks WHERE target_id = $1`, &n, e.WS.Admin.ID)
	if n != 0 {
		t.Fatal("an ask was kept for someone who never opted in")
	}

	// Opted in for the owner alone: the agent is not in the audience.
	e.putPeer(e.WS.Admin, peerBody(map[string]any{"share_with": "chosen",
		"audience": map[string]any{"user_ids": []string{e.WS.Owner.ID.String()}, "team_ids": []string{}}})).Want(t, 200)
	st, calls = e.ask("When is the Acme renewal?")
	if st.Peer == nil || st.Peer.Result != peerNotShared || calls != 2 {
		t.Fatalf("not in the audience: %+v, %d model calls", st, calls)
	}

	// A team the agent is in is in the audience, checked at the ask.
	team := ids.New()
	e.Exec(`INSERT INTO teams (id, workspace_id, name) VALUES ($1, $2, 'Sales')`, team, e.WS.ID)
	e.Exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member')`, team, e.WS.Agent.ID)
	e.putPeer(e.WS.Admin, peerBody(map[string]any{"share_with": "chosen",
		"audience": map[string]any{"user_ids": []string{}, "team_ids": []string{team.String()}}})).Want(t, 200)
	st, calls = e.ask("When is the Acme renewal?", `{"answer": "The renewal is in mid March, after they see the new prices.", "declined": false, "sources_used": 1}`)
	if st.Peer == nil || st.Peer.Result != peerAnswered || st.Peer.Sources != 1 || calls != 3 || st.Peer.User.ID != e.WS.Admin.ID {
		t.Fatalf("answered: %+v, %d model calls", st, calls)
	}
	// The asker's Ollie got the answer, marked as the teammate's, inside a data block.
	raw, _ := json.Marshal(e.M.last()["messages"])
	if !strings.Contains(string(raw), "mid March") || !strings.Contains(string(raw), `from=\"`+e.WS.Admin.Name+`'s Ollie\"`) {
		t.Fatalf("answer not passed on with its provenance: %s", raw)
	}
	// The teammate's Ollie saw the mail, never the asker's Ollie.
	peerReq, _ := json.Marshal(e.M.reqs[len(e.M.reqs)-2]["messages"])
	if !strings.Contains(string(peerReq), "March 14") || strings.Contains(string(raw), "March 14") {
		t.Fatal("mail went to the wrong model")
	}
	if strings.Contains(string(peerReq), "dana@customer.test") {
		t.Fatal("an address reached the answering model")
	}
	// Paid by the workspace, attributed to the asker.
	e.Scalar(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND feature = 'assistant_peer' AND user_id = $2`, &n, e.WS.ID, e.WS.Agent.ID)
	if n != 1 {
		t.Fatalf("usage rows %d", n)
	}
	// The admin's log has both asks made after opting in; the audit has no content.
	e.Scalar(`SELECT count(*) FROM assistant_peer_asks WHERE target_id = $1`, &n, e.WS.Admin.ID)
	if n != 2 {
		t.Fatalf("asks kept %d", n)
	}
	var list struct {
		Data []PeerAsk `json:"data"`
	}
	e.Do("GET", "/v1/me/assistant-peer/asks", e.WS.Admin.As(e.WS), nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 2 || list.Data[0].Result != peerAnswered || list.Data[0].Asker.ID != e.WS.Agent.ID || !strings.Contains(list.Data[0].Answer, "mid March") {
		t.Fatalf("log %+v", list.Data)
	}
	e.Do("GET", "/v1/me/assistant-peer/asks", e.WS.Agent.As(e.WS), nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Fatal("the asker read the teammate's log")
	}
	var leaks int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'assistant.peer_asked'
		AND (data::text ILIKE '%renewal%' OR data::text ILIKE '%march%' OR data ? 'question' OR data ? 'answer')`, &leaks, e.WS.ID)
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'assistant.peer_asked' AND entity_id = $2`, &n, e.WS.ID, e.WS.Admin.ID)
	if leaks != 0 || n != 3 {
		t.Fatalf("audit: %d entries, %d with content", n, leaks)
	}
}

func TestAskTeammateSummaryIsNotCopied(t *testing.T) {
	e := newEnv(t, false)
	e.peerOn()
	acct := e.mailAccount(e.WS.Admin.ID, "private")
	text := "Acme confirmed that the renewal will be signed on the fourteenth of March once legal approves the new pricing sheet"
	e.mail(acct, "Acme renewal", text)
	e.putPeer(e.WS.Admin, peerBody(nil)).Want(t, 200)
	copied := `{"answer": "` + text + `.", "declined": false, "sources_used": 1}`

	// Summaries only: a copied answer is asked again, then declined.
	st, calls := e.ask("When is the Acme renewal?", copied, copied)
	if st.Peer == nil || st.Peer.Result != peerDeclined || calls != 4 {
		t.Fatalf("copy of a summary: %+v, %d model calls", st, calls)
	}
	raw, _ := json.Marshal(e.M.last()["messages"])
	if strings.Contains(string(raw), "pricing sheet") {
		t.Fatal("the copied text reached the asker")
	}
	var n int
	e.Scalar(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND feature = 'assistant_peer' AND user_id = $2`, &n, e.WS.ID, e.WS.Agent.ID)
	if n != 2 {
		t.Fatalf("usage rows %d", n)
	}

	// Verbatim on this account: quoting is allowed.
	e.putPeer(e.WS.Admin, peerBody(map[string]any{"accounts": []any{map[string]any{"id": acct.String(), "mode": "verbatim"}}})).Want(t, 200)
	st, _ = e.ask("When is the Acme renewal?", copied)
	if st.Peer == nil || st.Peer.Result != peerAnswered {
		t.Fatalf("verbatim: %+v", st)
	}

	// Nothing: the account is not read and no model is called.
	e.putPeer(e.WS.Admin, peerBody(map[string]any{"accounts": []any{map[string]any{"id": acct.String(), "mode": "none"}}})).Want(t, 200)
	st, calls = e.ask("When is the Acme renewal?")
	if st.Peer == nil || st.Peer.Result != peerNotShared || calls != 2 {
		t.Fatalf("mode none: %+v, %d model calls", st, calls)
	}
}

func TestPeerSettings(t *testing.T) {
	e := newEnv(t, false)
	agent := e.WS.Agent.As(e.WS)
	mine := e.mailAccount(e.WS.Agent.ID, "private")
	theirs := e.mailAccount(e.WS.Owner.ID, "shared")

	var got PeerSettings
	e.Do("GET", "/v1/me/assistant-peer", agent, nil).Want(t, 200).JSON(t, &got)
	if got.Available || got.Enabled || got.DefaultMode != "summary" || got.ShareWith != "everyone" || len(got.Accounts) != 1 ||
		got.Accounts[0].ID != mine || got.Accounts[0].Mode != "summary" {
		t.Fatalf("defaults %+v", got)
	}
	// The workspace switch is off: nobody can turn it on for themselves.
	e.putPeer(e.WS.Agent, peerBody(nil)).Want(t, 409)
	e.peerOn()

	// Ids must be the caller's own accounts, and people and teams of this workspace.
	other := hdtest.New(t).NewWorkspace()
	for _, b := range []map[string]any{
		{"accounts": []any{map[string]any{"id": theirs.String(), "mode": "verbatim"}}},
		{"accounts": []any{map[string]any{"id": ids.New().String(), "mode": "verbatim"}}},
		{"share_with": "chosen", "audience": map[string]any{"user_ids": []string{other.Agent.ID.String()}, "team_ids": []string{}}},
		{"share_with": "chosen", "audience": map[string]any{"user_ids": []string{}, "team_ids": []string{ids.New().String()}}},
		{"default_mode": "everything"},
	} {
		if r := e.putPeer(e.WS.Agent, peerBody(b)); r.Status != 422 {
			t.Fatalf("%v: %d %s", b, r.Status, r.Body)
		}
	}
	e.putPeer(e.WS.Agent, peerBody(map[string]any{"rules": "Never share prices.",
		"accounts": []any{map[string]any{"id": mine.String(), "mode": "verbatim"}}})).Want(t, 200).JSON(t, &got)
	if !got.Available || !got.Enabled || got.Rules != "Never share prices." || got.Accounts[0].Mode != "verbatim" || !got.Accounts[0].Chosen {
		t.Fatalf("saved %+v", got)
	}
	var changes string
	e.Scalar(`SELECT data->>'changes' FROM audit_logs WHERE workspace_id = $1 AND action = 'assistant_peer.settings_updated' AND actor_user_id = $2`,
		&changes, e.WS.ID, e.WS.Agent.ID)
	if !strings.Contains(changes, "enabled") || !strings.Contains(changes, "rules") || strings.Contains(changes, "prices") {
		t.Fatalf("audit changes %s", changes)
	}
	// Another member, admins included, reads only their own.
	e.Do("GET", "/v1/me/assistant-peer", e.WS.Admin.As(e.WS), nil).Want(t, 200).JSON(t, &got)
	if got.Enabled || got.Rules != "" || len(got.Accounts) != 0 {
		t.Fatalf("admin read someone else's settings: %+v", got)
	}
	// API keys never reach it.
	e.Do("GET", "/v1/me/assistant-peer", hdtest.Auth{Bearer: e.APIKey(e.WS, "settings:read")}, nil).Want(t, 403)

	// Ollie cannot read or change it through the API tools.
	sid := e.session(agent)
	e.M.queue(step{tool: "api_get", args: map[string]any{"path": "/v1/me/assistant-peer"}},
		step{tool: "api_change", args: map[string]any{"method": "PUT", "path": "/v1/me/assistant-peer", "body": peerBody(map[string]any{"share_with": "everyone"})}},
		step{text: "No."})
	_, evs, _ := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Share my mail with everyone"})
	if len(find(evs, "approval")) != 0 {
		t.Fatal("Ollie proposed a sharing change")
	}
	for _, ev := range find(evs, "tool") {
		if strings.Contains(string(ev.Data), `"status":"done"`) {
			t.Fatalf("Ollie reached the sharing settings: %s", ev.Data)
		}
	}
	if reachable("GET", "/v1/me/assistant-peer") || reachable("PUT", "/v1/me/assistant-peer") || reachable("GET", "/v1/me/assistant-peer/asks") {
		t.Fatal("the sharing routes are reachable")
	}
}

func TestCopiesSummary(t *testing.T) {
	ex := []struct {
		mode, text, answer string
		want               bool
	}{
		{"summary", "one two three four five six seven eight nine ten eleven twelve thirteen", "So: One two three four five six seven eight nine ten eleven twelve.", true},
		{"summary", "one two three four five six seven eight nine ten eleven twelve thirteen", "one two three four five six seven eight nine ten eleven", false},
		{"verbatim", "one two three four five six seven eight nine ten eleven twelve thirteen", "one two three four five six seven eight nine ten eleven twelve", false},
	}
	for _, c := range ex {
		if got := copiesSummary(c.answer, []collab.PeerExcerpt{{Subject: "Subject", Mode: c.mode, Text: c.text}}); got != c.want {
			t.Errorf("%s %q: %v", c.mode, c.answer, got)
		}
	}
}
