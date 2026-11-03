package collab

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
)

// The AI queue (contracts/collab.md "AI queue"): keys claim the conversation that has
// waited longest in Needs action on a shared account with handling on, one holder at a
// time; a claim expires on its own; a draft ends it and keeps the conversation in Needs
// action for a person; a skip holds until new mail arrives. People, viewers' keys and keys
// without the scope get nothing.

type claimResp struct {
	Claim        *AIClaim          `json:"claim"`
	Conversation *MailThreadDetail `json:"conversation"`
}

func TestAIQueue(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	mustExec(t, w.d, `UPDATE collab_accounts SET handling_enabled = true WHERE id = $1`, a.ID)
	now := time.Now()
	g.add("m1", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "frank@example.com", to: a.Email, subject: "Oldest", msgID: "q1@example.com",
		body: "First", date: now.Add(-3 * time.Hour)})
	g.add("m2", "t2", []string{"INBOX", "UNREAD"}, testMail{from: "gina@example.com", to: a.Email, subject: "Middle", msgID: "q2@example.com",
		body: "Second", date: now.Add(-2 * time.Hour)})
	g.add("m3", "t3", []string{"INBOX", "UNREAD"}, testMail{from: "hal@example.com", to: a.Email, subject: "Newest", msgID: "q3@example.com",
		body: "Third", date: now.Add(-time.Hour)})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	t1, t2, t3 := w.messageByRemote(a, "m1").ThreadID, w.messageByRemote(a, "m2").ThreadID, w.messageByRemote(a, "m3").ThreadID
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE account_id = $1 AND needs_action`, a.ID); n != 3 {
		t.Fatalf("%d conversations need action", n)
	}
	botKey := w.key(w.agent, "Local drafter", auth.ScopeCollabDraft)
	bot, bot2 := w.api(botKey), w.api(w.key(w.agent2, "Second drafter", auth.ScopeCollabDraft))
	claim := func(srv string, want uuid.UUID) {
		t.Helper()
		var r claimResp
		st := doJSON(t, "POST", srv+"/v1/collab/ai-queue/claim", map[string]any{}, &r)
		if st != 200 {
			t.Fatalf("claim: %d", st)
		}
		got := uuid.Nil
		if r.Claim != nil {
			got = r.Claim.ConversationID
			if r.Conversation == nil || r.Conversation.Thread.ID != got || len(r.Conversation.Messages) == 0 {
				t.Fatalf("claim without its conversation: %+v", r)
			}
			if d := time.Until(r.Claim.ExpiresAt); d < 4*time.Minute || d > 6*time.Minute {
				t.Fatalf("claim expires in %s", d)
			}
		}
		if got != want {
			t.Fatalf("claimed %s, want %s", got, want)
		}
	}
	release := func(srv string, id uuid.UUID, body any) int {
		t.Helper()
		return doJSON(t, "POST", srv+"/v1/collab/ai-queue/claims/"+id.String()+"/release", body, nil)
	}

	// Oldest first, one holder each.
	claim(bot.URL, t1)
	claim(bot2.URL, t2)
	claim(bot.URL, t3)
	claim(bot2.URL, uuid.Nil)

	// Only the holder releases its claim.
	if st := release(bot2.URL, t1, nil); st != 404 {
		t.Fatalf("another key's claim: %d", st)
	}
	// A skip holds for that key until new mail; a plain release offers it again.
	if st := release(bot2.URL, t2, map[string]any{"skip": true, "reason": "not confident"}); st != 204 {
		t.Fatalf("skip: %d", st)
	}
	if n := w.count(`SELECT count(*) FROM collab_ai_skips WHERE thread_id = $1 AND reason = 'not confident'`, t2); n != 1 {
		t.Fatal("skip not kept")
	}
	claim(bot2.URL, uuid.Nil)
	if st := release(bot.URL, t3, map[string]any{}); st != 204 {
		t.Fatalf("release: %d", st)
	}
	claim(bot2.URL, t3)
	claim(bot.URL, t2) // skipped by the other key only

	// A draft is the report back: the claim ends, the conversation still needs action for a
	// person, and it is not offered again while the draft waits.
	m2 := w.messageByRemote(a, "m2")
	var d MailMessage
	if st := doJSON(t, "POST", bot.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m2.ID,
		"to": []string{"gina@example.com"}, "subject": "", "body_html": "<p>Yes.</p>"}, &d); st != 200 || d.DraftedByAI == nil {
		t.Fatalf("draft: %d %+v", st, d)
	}
	if n := w.count(`SELECT count(*) FROM collab_ai_claims WHERE thread_id = $1`, t2); n != 0 {
		t.Fatal("the draft did not end the claim")
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE id = $1 AND needs_action`, t2); n != 1 {
		t.Fatal("an AI draft cleared Needs action")
	}

	// An expired claim is taken over.
	mustExec(t, w.d, `UPDATE collab_ai_claims SET expires_at = now() - interval '1 second' WHERE thread_id = $1`, t3)
	claim(bot.URL, t3)
	if st := release(bot2.URL, t3, nil); st != 404 {
		t.Fatalf("expired claim released: %d", st)
	}

	// New mail lifts a skip.
	if st := release(bot.URL, t1, map[string]any{"skip": true}); st != 204 {
		t.Fatalf("skip t1: %d", st)
	}
	if st := release(bot.URL, t3, nil); st != 204 {
		t.Fatalf("release t3: %d", st)
	}
	claim(bot.URL, t3)
	claim(bot.URL, uuid.Nil)
	g.add("m4", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "frank@example.com", to: a.Email, subject: "Re: Oldest", msgID: "q4@example.com",
		inReplyTo: "q1@example.com", refs: "<q1@example.com>", body: "Any news?", date: now.Add(-time.Minute)})
	g.record("messagesAdded", "m4")
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if w.messageByRemote(a, "m4").ThreadID != t1 {
		t.Fatal("the new mail is not in t1")
	}
	claim(bot.URL, t1)

	// Claims of a revoked key do not hold their conversations (t3 has waited longest).
	mustExec(t, w.d, `UPDATE api_keys SET revoked_at = now() WHERE id = $1`, botKey.APIKeyID)
	claim(bot2.URL, t3)
	claim(bot2.URL, t1)

	// Handling off: nothing is offered.
	for _, id := range []uuid.UUID{t1, t3} {
		if st := release(bot2.URL, id, nil); st != 204 {
			t.Fatalf("release: %d", st)
		}
	}
	mustExec(t, w.d, `UPDATE collab_accounts SET handling_enabled = false WHERE id = $1`, a.ID)
	claim(bot2.URL, uuid.Nil)
	mustExec(t, w.d, `UPDATE collab_accounts SET handling_enabled = true WHERE id = $1`, a.ID)

	// A claim's token is new each time.
	var r1, r2 claimResp
	doJSON(t, "POST", bot2.URL+"/v1/collab/ai-queue/claim", map[string]any{}, &r1)
	if r1.Claim == nil || r1.Claim.Token == uuid.Nil {
		t.Fatalf("claim without a token: %+v", r1.Claim)
	}
	release(bot2.URL, r1.Claim.ConversationID, nil)
	doJSON(t, "POST", bot2.URL+"/v1/collab/ai-queue/claim", map[string]any{}, &r2)
	if r2.Claim == nil || r2.Claim.Token == r1.Claim.Token {
		t.Fatal("a new claim kept the old token")
	}
	release(bot2.URL, r2.Claim.ConversationID, nil)

	// People, a viewer's key and a key without the scope do not use the queue.
	for name, p := range map[string]*auth.Principal{
		"person":     w.agent,
		"viewer key": w.key(w.viewer, "Vic's key", auth.ScopeCollabDraft),
		"no scope":   w.key(w.agent, "Tickets key", auth.ScopeTicketsRead),
	} {
		if st := doJSON(t, "POST", w.api(p).URL+"/v1/collab/ai-queue/claim", map[string]any{}, nil); st != 403 {
			t.Fatalf("%s: %d", name, st)
		}
	}
}

// queueWorld is a shared account with handling on and n conversations in Needs action,
// oldest first.
func queueWorld(t *testing.T, n int) (*world, *fakeGmail, *accountRow, []uuid.UUID) {
	t.Helper()
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	mustExec(t, w.d, `UPDATE collab_accounts SET handling_enabled = true WHERE id = $1`, a.ID)
	now := time.Now()
	for i := range n {
		id := strconv.Itoa(i + 1)
		g.add("m"+id, "t"+id, []string{"INBOX", "UNREAD"}, testMail{from: "frank@example.com", to: a.Email, subject: "Q" + id,
			msgID: "q" + id + "@example.com", body: "Question " + id, date: now.Add(-time.Duration(n-i) * time.Hour)})
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	threads := make([]uuid.UUID, n)
	for i := range n {
		threads[i] = w.messageByRemote(a, "m"+strconv.Itoa(i+1)).ThreadID
	}
	return w, g, a, threads
}

// A claim may name categories (GH#262): it is offered only conversations in them, read as
// the inbox tabs read them, and none still waiting to be sorted. A person's move decides.
func TestAIQueueCategories(t *testing.T) {
	w, _, a, threads := queueWorld(t, 4)
	bot := w.api(w.key(w.agent, "Question drafter", auth.ScopeCollabDraft))
	human := w.api(w.agent)
	q, upd, gone, pending := threads[0], threads[1], threads[2], threads[3]
	claim := func(cats []string, skip bool) (uuid.UUID, int) {
		t.Helper()
		var r claimResp
		st := doJSON(t, "POST", bot.URL+"/v1/collab/ai-queue/claim", map[string]any{"categories": cats, "ttl_seconds": 60}, &r)
		if st != 200 || r.Claim == nil {
			return uuid.Nil, st
		}
		if st := doJSON(t, "POST", bot.URL+"/v1/collab/ai-queue/claims/"+r.Claim.ConversationID.String()+"/release",
			map[string]any{"skip": skip}, nil); st != 204 {
			t.Fatalf("release: %d", st)
		}
		return r.Claim.ConversationID, st
	}

	// Not split: every conversation is primary.
	if id, _ := claim([]string{"primary"}, false); id != q {
		t.Fatalf("an account not split reads primary: %s", id)
	}
	if id, st := claim([]string{"updates"}, false); id != uuid.Nil || st != 200 {
		t.Fatalf("updates on an account not split: %d %s", st, id)
	}

	// Split, with the built-in categories: one in Updates, one in a category the account
	// no longer has (read as primary), one waiting to be sorted.
	if st := doJSON(t, "PUT", w.api(w.owner).URL+"/v1/collab/categories/workspace", map[string]any{"enabled": true}, nil); st != 200 {
		t.Fatalf("categories on: %d", st)
	}
	w.s.mailAI = &fakeMailAI{prob: 0.9}
	if err := w.s.categorize(w.ctx, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	mustExec(t, w.d, `UPDATE collab_threads SET category = 'primary', category_source = 'ai', category_pending = false WHERE id = $1`, q)
	mustExec(t, w.d, `UPDATE collab_threads SET category = 'updates', category_source = 'rule', category_pending = false WHERE id = $1`, upd)
	mustExec(t, w.d, `UPDATE collab_threads SET category = 'retired', category_source = 'ai', category_pending = false WHERE id = $1`, gone)
	mustExec(t, w.d, `UPDATE collab_threads SET category = NULL, category_source = NULL, category_pending = true WHERE id = $1`, pending)

	if id, _ := claim([]string{"updates", "updates"}, false); id != upd {
		t.Fatalf("updates: %s", id)
	}
	if id, _ := claim([]string{"primary"}, false); id != q {
		t.Fatalf("primary: %s", id)
	}
	if id, _ := claim(nil, false); id != q {
		t.Fatalf("without categories the oldest waiting comes first: %s", id)
	}

	// The person's move decides: moved to Updates, it is no longer offered as primary.
	if st := doJSON(t, "POST", human.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{q},
		"action": "categorize", "category": "updates"}, nil); st != 200 {
		t.Fatalf("categorize: %d", st)
	}
	if id, _ := claim([]string{"updates"}, false); id != q {
		t.Fatalf("moved in: %s", id)
	}
	if id, _ := claim([]string{"primary"}, true); id != gone {
		t.Fatalf("a key the account no longer has reads primary: %s", id)
	}
	// Skipped that one: what is left in primary is waiting to be sorted, held back until it is.
	if id, st := claim([]string{"primary"}, false); id != uuid.Nil || st != 200 {
		t.Fatalf("not yet sorted: %d %s", st, id)
	}
	if id, _ := claim(nil, false); id != q {
		t.Fatalf("without categories: %s", id)
	}
	mustExec(t, w.d, `UPDATE collab_threads SET category = 'primary', category_source = 'ai', category_pending = false WHERE id = $1`, pending)
	if id, _ := claim([]string{"primary"}, false); id != pending {
		t.Fatalf("once sorted: %s", id)
	}

	var e apiError
	for _, bad := range [][]string{{"Not a key"}, {"a", "b", "c", "d", "e", "f", "g", "h", "i"}} {
		if st := doJSON(t, "POST", bot.URL+"/v1/collab/ai-queue/claim", map[string]any{"categories": bad}, &e); st != 422 {
			t.Fatalf("categories %v: %d", bad, st)
		}
	}
}

// A queue worker's draft carries its claim's token and is checked against the live claim
// before the provider is called: a worker whose claim expired and passed on is refused,
// without ending the new holder's claim; a draft without a token (manual drafting) still
// works and ends only its own key's claim; a person's draft ends any claim, and the
// holder's later draft is refused (PR #194 review).
func TestAIQueueFencing(t *testing.T) {
	w, g, a, threads := queueWorld(t, 2)
	t1, t2 := threads[0], threads[1]
	m1, m2 := w.messageByRemote(a, "m1"), w.messageByRemote(a, "m2")
	keyA, keyB := w.key(w.agent, "Drafter A", auth.ScopeCollabDraft), w.key(w.agent, "Drafter B", auth.ScopeCollabDraft)
	botA, botB, human := w.api(keyA), w.api(keyB), w.api(w.agent)
	claimed := func(srv string, want uuid.UUID) uuid.UUID {
		t.Helper()
		var r claimResp
		if st := doJSON(t, "POST", srv+"/v1/collab/ai-queue/claim", map[string]any{"account_id": a.ID}, &r); st != 200 ||
			r.Claim == nil || r.Claim.ConversationID != want {
			t.Fatalf("claim: %d %+v, want %s", st, r.Claim, want)
		}
		return r.Claim.Token
	}
	draft := func(srv string, m *messageRow, token *uuid.UUID) int {
		t.Helper()
		body := map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m.ID, "to": []string{"frank@example.com"},
			"subject": "", "body_html": "<p>Answer</p>"}
		if token != nil {
			body["claim_token"] = *token
		}
		var e apiError
		return doJSON(t, "POST", srv+"/v1/collab/drafts", body, &e)
	}
	uploads := func() int {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.drafts.saves)
	}
	holds := func(key *auth.Principal, thread uuid.UUID) bool {
		return w.count(`SELECT count(*) FROM collab_ai_claims WHERE thread_id = $1 AND api_key_id = $2`, thread, key.APIKeyID) == 1
	}

	// A's claim expires and passes to B: A's late draft is refused before Gmail, and B
	// keeps the claim.
	tokA := claimed(botA.URL, t1)
	mustExec(t, w.d, `UPDATE collab_ai_claims SET expires_at = now() - interval '1 second' WHERE thread_id = $1`, t1)
	tokB := claimed(botB.URL, t1)
	before := uploads()
	if st := draft(botA.URL, m1, &tokA); st != 409 {
		t.Fatalf("late worker's draft: %d", st)
	}
	if uploads() != before || !holds(keyB, t1) {
		t.Fatal("a refused draft reached Gmail or ended the holder's claim")
	}
	// A key's draft without a token is manual drafting: it is saved, and ends only that
	// key's own claim, never B's.
	if st := draft(botA.URL, m1, nil); st != 200 {
		t.Fatalf("manual draft: %d", st)
	}
	if !holds(keyB, t1) {
		t.Fatal("another key's draft ended the holder's claim")
	}
	// B reports back with its token, which ends its claim; the token is spent.
	if st := draft(botB.URL, m1, &tokB); st != 200 || holds(keyB, t1) {
		t.Fatalf("holder's draft: %d, claim kept %v", st, holds(keyB, t1))
	}
	if st := draft(botB.URL, m1, &tokB); st != 409 {
		t.Fatalf("spent token: %d", st)
	}
	// Tokens are for keys, and name the claim of the conversation replied to.
	tokA2 := claimed(botA.URL, t2)
	if st := draft(human.URL, m2, &tokA2); st != 422 {
		t.Fatalf("a person with a token: %d", st)
	}
	if st := draft(botA.URL, m1, &tokA2); st != 409 {
		t.Fatalf("token of another conversation: %d", st)
	}
	// A person's draft takes the conversation on: the claim ends and the worker is refused.
	if st := draft(human.URL, m2, nil); st != 200 || holds(keyA, t2) {
		t.Fatalf("person's draft: %d, claim kept %v", st, holds(keyA, t2))
	}
	if st := draft(botA.URL, m2, &tokA2); st != 409 {
		t.Fatalf("worker after a person's draft: %d", st)
	}
}

// Many keys claiming at the same moment: each conversation goes to one key, and the rest
// are claimed after; nothing is claimed twice.
func TestAIQueueConcurrentClaims(t *testing.T) {
	const conversations, workers = 6, 12
	w, _, _, threads := queueWorld(t, conversations)
	servers := make([]string, workers)
	for i := range servers {
		servers[i] = w.api(w.key(w.agent, "Worker "+strconv.Itoa(i), auth.ScopeCollabDraft)).URL
	}
	type result struct {
		st int
		id uuid.UUID
	}
	results := make(chan result, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var r claimResp
			st := doJSON(t, "POST", srv+"/v1/collab/ai-queue/claim", map[string]any{}, &r)
			id := uuid.Nil
			if r.Claim != nil {
				id = r.Claim.ConversationID
			}
			results <- result{st, id}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	got := map[uuid.UUID]int{}
	for r := range results {
		if r.st != 200 {
			t.Fatalf("concurrent claim: %d", r.st)
		}
		if r.id != uuid.Nil {
			got[r.id]++
		}
	}
	for id, n := range got {
		if n != 1 {
			t.Fatalf("conversation %s claimed %d times", id, n)
		}
	}
	if len(got) == 0 {
		t.Fatal("no concurrent claim succeeded")
	}
	if n := w.count(`SELECT count(*) FROM collab_ai_claims WHERE workspace_id = $1`, w.wid); n != len(got) {
		t.Fatalf("%d claims stored, %d returned", n, len(got))
	}
	// What the race left is claimed afterwards, each conversation once.
	for _, srv := range servers {
		var r claimResp
		doJSON(t, "POST", srv+"/v1/collab/ai-queue/claim", map[string]any{}, &r)
		if r.Claim != nil {
			if got[r.Claim.ConversationID]++; got[r.Claim.ConversationID] != 1 {
				t.Fatalf("conversation %s claimed twice", r.Claim.ConversationID)
			}
		}
	}
	for _, id := range threads {
		if got[id] != 1 {
			t.Fatalf("conversation %s claimed %d times in all", id, got[id])
		}
	}
}
