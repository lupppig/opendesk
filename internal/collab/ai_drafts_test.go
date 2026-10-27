package collab

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/ids"
)

// AI drafts (contracts/collab.md "AI drafts"): an API key with collab:draft acts as the
// member who created it: it reads what they see (internal comments included) and saves
// reply drafts labelled "AI draft by <key name>"; it never sends, changes read state,
// touches a person's draft or reaches mail the member does not see, and it stops working
// when the member leaves, is suspended or loses the role. Keys without the scope, OAuth
// grants and the assistant stay out.

// key creates an API key named name by p with scopes and returns its principal, as the
// resolver makes it.
func (w *world) key(p *auth.Principal, name string, scopes ...string) *auth.Principal {
	w.t.Helper()
	id := ids.New()
	mustExec(w.t, w.d, `INSERT INTO api_keys (id, workspace_id, name, prefix, key_hash, scopes, created_by)
		VALUES ($1, $2, $3, 'od_live_test', $4, $5, $6)`, id, w.wid, name, []byte(id.String()), scopes, p.UserID)
	return &auth.Principal{Kind: auth.KindAPIKey, APIKeyID: id, WorkspaceID: w.wid, Scopes: scopes, IP: "127.0.0.1"}
}

func TestAIClientDrafts(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "Frank <frank@example.com>", to: a.Email, subject: "Quote?", msgID: "h1@example.com", body: "Price for 500?"})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	m1 := w.messageByRemote(a, "m1")
	bot := w.key(w.agent, "Local drafter", auth.ScopeCollabDraft)
	ai := w.api(bot)
	human := w.api(w.agent)
	var e apiError
	// The team's internal comment, which the key reads with the conversation.
	if st := doJSON(t, "POST", human.URL+"/v1/collab/threads/"+m1.ThreadID.String()+"/comments",
		map[string]any{"body_html": "<p>Quote at volume price</p>"}, nil); st != 200 {
		t.Fatalf("comment: %d", st)
	}

	// It lists and reads what the member sees, and reading leaves the conversation unread.
	var list struct {
		Data []MailThread `json:"data"`
	}
	if st := doJSON(t, "GET", ai.URL+"/v1/collab/threads?box=unread", nil, &list); st != 200 || len(list.Data) != 1 {
		t.Fatalf("list: %d %+v", st, list)
	}
	var detail MailThreadDetail
	if st := doJSON(t, "GET", ai.URL+"/v1/collab/threads/"+m1.ThreadID.String(), nil, &detail); st != 200 || len(detail.Messages) != 1 || len(detail.Comments) != 1 {
		t.Fatalf("read: %d %+v", st, detail)
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE id = $1 AND unread`, m1.ThreadID); n != 1 {
		t.Fatal("reading marked the conversation read")
	}

	// It saves a reply draft, labelled as its own and audit-logged.
	var d MailMessage
	st := doJSON(t, "POST", ai.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m1.ID,
		"to": []string{"frank@example.com"}, "subject": "", "body_html": "<p>About $2 each.</p>"}, &d)
	if st != 200 || !d.Draft || d.DraftID == nil || d.DraftedByAI == nil || *d.DraftedByAI != "Local drafter" || d.ThreadID != m1.ThreadID {
		t.Fatalf("create: %d %+v", st, d)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'collab.ai_draft_saved' AND entity_id = $2
		AND actor_user_id = $3`, w.wid, d.ID, w.agent.UserID); n != 1 {
		t.Fatalf("%d audit entries", n)
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE id = $1 AND unread`, m1.ThreadID); n != 1 {
		t.Fatal("drafting marked the conversation read")
	}
	// Saving it again keeps the label; the Gmail sync's view of the new version keeps it too.
	var d2 MailMessage
	if st := doJSON(t, "PUT", ai.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Quote?", "body_html": "<p>About $1.90 each.</p>"}, &d2); st != 200 || d2.DraftedByAI == nil {
		t.Fatalf("save again: %d %+v", st, d2)
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE id = $1 AND draft AND ai_api_key_id = $2`, d.ID, bot.APIKeyID); n != 1 {
		t.Fatal("the sync dropped the draft's key")
	}

	// It never sends, discards, marks read, assigns or comments.
	refusals := []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/collab/drafts/" + d.ID.String() + "/send", map[string]any{"to": []string{"frank@example.com"}, "subject": "Re: Quote?",
			"body_html": "<p>Sent by the client</p>"}},
		{"DELETE", "/v1/collab/drafts/" + d.ID.String(), nil},
		{"POST", "/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{m1.ThreadID}, "action": "read"}},
		{"PUT", "/v1/collab/threads/" + m1.ThreadID.String() + "/assignees", map[string]any{"user_ids": []uuid.UUID{w.agent.UserID}}},
		{"POST", "/v1/collab/messages", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m1.ID, "to": []string{"frank@example.com"},
			"subject": "", "body_html": "<p>x</p>"}},
		{"POST", "/v1/collab/threads/" + m1.ThreadID.String() + "/comments", map[string]any{"body_html": "<p>x</p>"}},
		{"GET", "/v1/collab/counts", nil},
	}
	for _, r := range refusals {
		if st := doJSON(t, r.method, ai.URL+r.path, r.body, &e); st != 403 {
			t.Errorf("%s %s: %d", r.method, r.path, st)
		}
	}
	if len(g.drafts.sent) != 0 || len(g.drafts.deleted) != 0 {
		t.Fatalf("the client reached Gmail: sent %v deleted %v", g.drafts.sent, g.drafts.deleted)
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE id = $1 AND unread`, m1.ThreadID); n != 1 {
		t.Fatal("the conversation was marked read")
	}

	// Replies only, without files.
	if st := doJSON(t, "POST", ai.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"x@example.com"},
		"subject": "Hi", "body_html": "<p>x</p>"}, &e); st != 403 {
		t.Fatalf("new mail: %d", st)
	}
	if st := doJSON(t, "POST", ai.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "forward", "message_id": m1.ID,
		"to": []string{"x@example.com"}, "subject": "", "body_html": "<p>x</p>", "forward_attachments": true}, &e); st != 403 {
		t.Fatalf("forward: %d", st)
	}

	// A person's draft is theirs: the key cannot save it again, and once a person saves
	// the key's draft it is theirs too (the label goes).
	var hd MailMessage
	if st := doJSON(t, "POST", human.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m1.ID,
		"to": []string{"frank@example.com"}, "subject": "", "body_html": "<p>Mine</p>"}, &hd); st != 200 || hd.DraftedByAI != nil {
		t.Fatalf("person's draft: %d %+v", st, hd)
	}
	if st := doJSON(t, "PUT", ai.URL+"/v1/collab/drafts/"+hd.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Quote?", "body_html": "<p>Overwritten</p>"}, &e); st != 403 {
		t.Fatalf("client saves a person's draft: %d", st)
	}
	var taken MailMessage
	if st := doJSON(t, "PUT", human.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Quote?", "body_html": "<p>$1.90 each, checked.</p>"}, &taken); st != 200 || taken.DraftedByAI != nil {
		t.Fatalf("person takes the draft over: %d %+v", st, taken)
	}
	if st := doJSON(t, "PUT", ai.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Quote?", "body_html": "<p>Again</p>"}, &e); st != 403 {
		t.Fatalf("client saves a draft a person took over: %d", st)
	}
	// Another key of the same member does not own the first one's drafts.
	var d3 MailMessage
	doJSON(t, "POST", ai.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m1.ID,
		"to": []string{"frank@example.com"}, "subject": "", "body_html": "<p>Three</p>"}, &d3)
	other := w.api(w.key(w.agent, "Other key", auth.ScopeCollabDraft))
	if st := doJSON(t, "PUT", other.URL+"/v1/collab/drafts/"+d3.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Quote?", "body_html": "<p>x</p>"}, &e); st != 403 {
		t.Fatalf("another key: %d", st)
	}
	// The person sends the draft: it goes out as theirs.
	var sent MailMessage
	if st := doJSON(t, "POST", human.URL+"/v1/collab/drafts/"+d.ID.String()+"/send", nil, &sent); st != 200 ||
		sent.SentBy == nil || sent.SentBy.ID != w.agent.UserID || sent.DraftedByAI != nil {
		t.Fatalf("person sends: %d %+v", st, sent)
	}
}

// A person and an AI key saving one draft at the same moment: the changes run one at a
// time, and the key's save is checked against the version it would replace, so it never
// overwrites a person's save that reached the provider first (PR #180 review).
func TestAIClientDraftTakeoverRace(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "frank@example.com", to: a.Email, subject: "Quote?", msgID: "h1@example.com", body: "Price?"})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	m1 := w.messageByRemote(a, "m1")
	bot := w.key(w.agent, "Local drafter", auth.ScopeCollabDraft)
	ai, human := w.api(bot), w.api(w.agent)
	newAIDraft := func() MailMessage {
		var d MailMessage
		if st := doJSON(t, "POST", ai.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m1.ID,
			"to": []string{"frank@example.com"}, "subject": "", "body_html": "<p>AI first</p>"}, &d); st != 200 || d.DraftedByAI == nil {
			t.Fatalf("AI draft: %d %+v", st, d)
		}
		return d
	}
	save := func(srv string, id uuid.UUID, body string) (int, MailMessage) {
		var out MailMessage
		st := doJSON(t, "PUT", srv+"/v1/collab/drafts/"+id.String(), map[string]any{"to": []string{"frank@example.com"},
			"subject": "Re: Quote?", "body_html": body}, &out)
		return st, out
	}
	// pauseNextUpload holds the next draft upload at the provider until release is called.
	pauseNextUpload := func() (entered chan struct{}, release func()) {
		entered, gate := make(chan struct{}), make(chan struct{})
		var once sync.Once
		g.onDraftUpload(func() {
			first := false
			once.Do(func() { first = true })
			if first {
				close(entered)
				<-gate
			}
		})
		return entered, func() { close(gate) }
	}
	// waitForLockWaiter returns once a request waits on a draft's lock.
	waitForLockWaiter := func() {
		deadline := time.Now().Add(10 * time.Second)
		for w.count(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("no request waited on the draft's lock")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	type result struct {
		st  int
		out MailMessage
	}

	// The person's save is at the provider when the key saves: the key waits, then is
	// refused, and the person's version stays theirs.
	d := newAIDraft()
	entered, release := pauseNextUpload()
	humanDone := make(chan result, 1)
	go func() {
		st, out := save(human.URL, d.ID, "<p>Checked by a person</p>")
		humanDone <- result{st, out}
	}()
	<-entered
	aiDone := make(chan result, 1)
	go func() {
		st, out := save(ai.URL, d.ID, "<p>Stale AI save</p>")
		aiDone <- result{st, out}
	}()
	waitForLockWaiter()
	g.mu.Lock()
	uploads := len(g.drafts.saves)
	g.mu.Unlock()
	release()
	if h := <-humanDone; h.st != 200 || h.out.DraftedByAI != nil {
		t.Fatalf("person's save: %d %+v", h.st, h.out)
	}
	if r := <-aiDone; r.st != 403 {
		t.Fatalf("stale AI save after a person's: %d", r.st)
	}
	g.mu.Lock()
	extra := len(g.drafts.saves) - uploads
	g.mu.Unlock()
	if extra != 1 {
		t.Fatalf("%d uploads after the pause; the refused AI save reached the provider", extra)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE id = $1 AND draft AND ai_api_key_id IS NULL
		AND body_html LIKE '%Checked by a person%'`, d.ID); n != 1 {
		t.Fatal("the person's version was overwritten")
	}

	// The key's save is at the provider when the person saves: the person waits and then
	// takes the draft over, with their words.
	g.onDraftUpload(nil)
	d2 := newAIDraft()
	entered, release = pauseNextUpload()
	go func() {
		st, out := save(ai.URL, d2.ID, "<p>AI second</p>")
		aiDone <- result{st, out}
	}()
	<-entered
	go func() {
		st, out := save(human.URL, d2.ID, "<p>Person last</p>")
		humanDone <- result{st, out}
	}()
	waitForLockWaiter()
	release()
	if r := <-aiDone; r.st != 200 {
		t.Fatalf("AI save before the person's: %d", r.st)
	}
	if h := <-humanDone; h.st != 200 || h.out.DraftedByAI != nil {
		t.Fatalf("person's later save: %d %+v", h.st, h.out)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE id = $1 AND draft AND ai_api_key_id IS NULL
		AND body_html LIKE '%Person last%'`, d2.ID); n != 1 {
		t.Fatal("the person's later save did not take the draft over")
	}
}

func TestAIClientDraftsAccess(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "frank@example.com", to: a.Email, subject: "Hi", msgID: "h1@example.com", body: "Hi"})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	m1 := w.messageByRemote(a, "m1")
	draft := map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m1.ID, "to": []string{"frank@example.com"}, "subject": "", "body_html": "<p>x</p>"}
	var e apiError

	// A key without collab:draft, an OAuth grant even with it, and the assistant are refused.
	noScope := w.key(w.agent, "Tickets only", auth.ScopeTicketsRead, auth.ScopeTicketsWrite)
	grant := *w.agent
	grant.GrantID, grant.Scopes = ids.New(), []string{auth.ScopeCollabDraft}
	assistant := *w.agent
	assistant.ViaAssistant = true
	for name, p := range map[string]*auth.Principal{"key without scope": noScope, "oauth grant": &grant, "assistant": &assistant} {
		srv := w.api(p)
		if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads", nil, &e); st != 403 {
			t.Errorf("%s lists: %d", name, st)
		}
		if st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts", draft, &e); st != 403 {
			t.Errorf("%s drafts: %d", name, st)
		}
	}
	// A key sees only what its creator sees, and a viewer's key reads but cannot draft.
	outsider := w.api(w.key(w.agent2, "Bea's key", auth.ScopeCollabDraft))
	if st := doJSON(t, "GET", outsider.URL+"/v1/collab/threads/"+m1.ThreadID.String(), nil, &e); st != 404 {
		t.Fatalf("outsider reads: %d", st)
	}
	if st := doJSON(t, "POST", outsider.URL+"/v1/collab/drafts", draft, &e); st != 404 {
		t.Fatalf("outsider drafts: %d", st)
	}
	viewer := w.api(w.key(w.viewer, "Vic's key", auth.ScopeCollabDraft))
	if st := doJSON(t, "GET", viewer.URL+"/v1/collab/threads", nil, &e); st != 200 {
		t.Fatalf("viewer's key lists: %d", st)
	}
	if st := doJSON(t, "POST", viewer.URL+"/v1/collab/drafts", draft, &e); st != 403 {
		t.Fatalf("viewer's key drafts: %d", st)
	}

	// The key follows its creator now: demoted, it cannot draft; suspended or removed, it
	// cannot even read.
	mine := w.api(w.key(w.agent, "Ada's key", auth.ScopeCollabDraft))
	if st := doJSON(t, "GET", mine.URL+"/v1/collab/threads/"+m1.ThreadID.String(), nil, &e); st != 200 {
		t.Fatalf("creator's key reads: %d", st)
	}
	mustExec(t, w.d, `UPDATE members SET role = 'viewer' WHERE workspace_id = $1 AND user_id = $2`, w.wid, w.agent.UserID)
	if st := doJSON(t, "POST", mine.URL+"/v1/collab/drafts", draft, &e); st != 403 {
		t.Fatalf("demoted creator's key drafts: %d", st)
	}
	mustExec(t, w.d, `UPDATE members SET role = 'agent' WHERE workspace_id = $1 AND user_id = $2`, w.wid, w.agent.UserID)
	mustExec(t, w.d, `UPDATE users SET suspended_at = now() WHERE id = $1`, w.agent.UserID)
	if st := doJSON(t, "GET", mine.URL+"/v1/collab/threads", nil, &e); st != 403 {
		t.Fatalf("suspended creator's key lists: %d", st)
	}
	mustExec(t, w.d, `UPDATE users SET suspended_at = NULL WHERE id = $1`, w.agent.UserID)
	mustExec(t, w.d, `DELETE FROM members WHERE workspace_id = $1 AND user_id = $2`, w.wid, w.agent.UserID)
	if st := doJSON(t, "GET", mine.URL+"/v1/collab/threads", nil, &e); st != 403 {
		t.Fatalf("removed creator's key lists: %d", st)
	}
	if len(g.drafts.saves) != 0 {
		t.Fatalf("refused callers saved drafts: %v", g.drafts.saves)
	}
}
