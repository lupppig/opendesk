package collab

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
)

// Wait for the competing HTTP write to finish or to wait on the held transaction.
// Observing Postgres's wait graph avoids relying on a sleep to establish the race.
func outcomeWriteWaiting(t *testing.T, w *world, tx pgx.Tx, done <-chan int) (bool, int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case status := <-done:
			return false, status
		case <-deadline.C:
			t.Fatal("outcome write neither finished nor waited on the held transaction")
		case <-poll.C:
			var waiting bool
			if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT EXISTS (
    SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, tx.Conn().PgConn().PID()).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return true, 0
			}
		}
	}
}

func outcomeWriteFinished(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case status := <-done:
		return status
	case <-time.After(5 * time.Second):
		t.Fatal("outcome write did not finish after transaction ended")
	}
	return 0
}

func outcomeDraft(t *testing.T, w *world, srv string, a *accountRow, remote string) MailMessage {
	t.Helper()
	m := w.messageByRemote(a, remote)
	var draft MailMessage
	if status := doJSON(t, "POST", srv+"/v1/collab/drafts", map[string]any{
		"account_id": a.ID, "mode": "reply", "subject": "", "message_id": m.ID,
		"to": []string{"frank@example.com"}, "body_html": "<p>Answer</p>",
	}, &draft); status != 200 || draft.AIDraft == nil {
		t.Fatalf("draft: %d %+v", status, draft)
	}
	return draft
}

func outcomeFeed(t *testing.T, srv, cursor string) outcomesPage {
	t.Helper()
	var page outcomesPage
	if status := doJSON(t, "GET", srv+"/v1/collab/ai-drafts/outcomes?cursor="+cursor, nil, &page); status != 200 {
		t.Fatalf("feed: %d", status)
	}
	return page
}

func TestAIOutcomeCursorCommitOrder(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback=%t", rollback), func(t *testing.T) {
			w, _, a, threads := queueWorld(t, 2)
			bot := w.api(w.key(w.agent, "Cursor drafter", auth.ScopeCollabDraft))
			human := w.api(w.agent)
			first := outcomeDraft(t, w, bot.URL, a, "m1")
			second := outcomeDraft(t, w, bot.URL, a, "m2")
			tx, err := w.d.DB.Pool.Begin(w.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(w.ctx)
			if _, err := loadAccountForUpdate(w.ctx, tx, a.ID); err != nil {
				t.Fatal(err)
			}
			if err := settleDone(w.ctx, tx, []uuid.UUID{threads[0]}, w.agent.UserID); err != nil {
				t.Fatal(err)
			}
			done := make(chan int, 1)
			go func() {
				done <- doJSON(t, "PUT", human.URL+"/v1/collab/ai-drafts/"+second.AIDraft.ID.String()+"/feedback", map[string]any{"reason": "wrong_tone"}, nil)
			}()
			waiting, status := outcomeWriteWaiting(t, w, tx, done)
			before := outcomeFeed(t, bot.URL, "")
			cursor := ""
			if len(before.Data) > 0 {
				cursor = before.Data[len(before.Data)-1].Cursor
			}
			if rollback {
				err = tx.Rollback(w.ctx)
			} else {
				err = tx.Commit(w.ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				status = outcomeWriteFinished(t, done)
			}
			if status != 204 {
				t.Fatalf("feedback: %d", status)
			}
			after := outcomeFeed(t, bot.URL, cursor)
			found := false
			for _, outcome := range after.Data {
				if outcome.DraftID == first.ID {
					found = true
				}
			}
			if !rollback && !found {
				t.Fatalf("late Done outcome permanently omitted after cursor %q: before=%+v after=%+v", cursor, before.Data, after.Data)
			}
			all := outcomeFeed(t, bot.URL, "")
			want := 2
			if rollback {
				want = 1
			}
			if len(all.Data) != want {
				t.Fatalf("committed outcomes: %+v, want %d", all.Data, want)
			}
			if !waiting {
				t.Fatal("competing feedback passed an uncommitted outcome allocation")
			}
			t.Logf("feedback waited for transaction; rollback=%t; first page=%d; incremental=%d; total=%d", rollback, len(before.Data), len(after.Data), len(all.Data))
		})
	}
}

// Exercise every settlement path behind a different account's uncommitted allocation.
// Another member's private feed remains private while
// waiting and after commit. Reads must not wait behind a write.
func TestAIOutcomeCursorWritersAndPrivacy(t *testing.T) {
	for _, action := range []string{"done", "discard", "send", "replace"} {
		t.Run(action, func(t *testing.T) {
			w, _, a, _ := queueWorld(t, 1)
			private := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
			if err := w.s.syncAccount(w.ctx, private.ID); err != nil {
				t.Fatal(err)
			}
			bot := w.api(w.key(w.agent, "Shared drafter", auth.ScopeCollabDraft))
			other := w.api(w.key(w.agent2, "Private drafter", auth.ScopeCollabDraft))
			human := w.api(w.agent)
			draft := outcomeDraft(t, w, bot.URL, a, "m1")
			secret := outcomeDraft(t, w, other.URL, private, "m1")
			tx, err := w.d.DB.Pool.Begin(w.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(w.ctx)
			if _, err := loadAccountForUpdate(w.ctx, tx, private.ID); err != nil {
				t.Fatal(err)
			}
			if err := settleDone(w.ctx, tx, []uuid.UUID{secret.ThreadID}, w.agent2.UserID); err != nil {
				t.Fatal(err)
			}
			done := make(chan int, 1)
			go func() {
				switch action {
				case "done":
					done <- doJSON(t, "POST", human.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{draft.ThreadID}, "action": "done"}, nil)
				case "discard":
					done <- doJSON(t, "DELETE", human.URL+"/v1/collab/drafts/"+draft.ID.String(), nil, nil)
				case "send":
					done <- doJSON(t, "POST", human.URL+"/v1/collab/drafts/"+draft.ID.String()+"/send", nil, nil)
				case "replace":
					m := w.messageByRemote(a, "m1")
					done <- doJSON(t, "POST", human.URL+"/v1/collab/messages", map[string]any{"account_id": a.ID, "mode": "reply", "subject": "", "message_id": m.ID, "to": []string{"frank@example.com"}, "body_html": "<p>Human answer</p>"}, nil)
				}
			}()
			waiting, status := outcomeWriteWaiting(t, w, tx, done)
			// HTTP reads during the held write also prove that readers need no writer lock.
			before := outcomeFeed(t, bot.URL, "")
			if page := outcomeFeed(t, other.URL, ""); len(page.Data) != 0 {
				t.Fatalf("uncommitted private outcome visible: %+v", page.Data)
			}
			if err := tx.Commit(w.ctx); err != nil {
				t.Fatal(err)
			}
			if waiting {
				status = outcomeWriteFinished(t, done)
			}
			wantStatus := 200
			if action == "discard" {
				wantStatus = 204
			}
			if status != wantStatus {
				t.Fatalf("%s: %d", action, status)
			}
			if !waiting || len(before.Data) != 0 {
				t.Fatalf("%s passed uncommitted allocation: waiting=%t feed=%+v", action, waiting, before.Data)
			}
			sharedPage, privatePage := outcomeFeed(t, bot.URL, ""), outcomeFeed(t, other.URL, "")
			if len(sharedPage.Data) != 1 || sharedPage.Data[0].DraftID != draft.ID {
				t.Fatalf("shared feed: %+v", sharedPage.Data)
			}
			if len(privatePage.Data) != 1 || privatePage.Data[0].DraftID != secret.ID {
				t.Fatalf("private feed: %+v", privatePage.Data)
			}
			oldSeq, err := strconv.ParseInt(privatePage.Data[0].Cursor, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			newSeq, err := strconv.ParseInt(sharedPage.Data[0].Cursor, 10, 64)
			if err != nil || newSeq <= oldSeq {
				t.Fatalf("allocation order: private=%d shared=%d err=%v", oldSeq, newSeq, err)
			}
			// An owner/admin still cannot read another member's private conversation.
			admin := w.api(w.owner)
			if status := doJSON(t, "PUT", admin.URL+"/v1/collab/ai-drafts/"+secret.AIDraft.ID.String()+"/feedback", map[string]any{"reason": "wrong_tone"}, nil); status != 404 {
				t.Fatalf("owner gave private feedback: %d", status)
			}
		})
	}
}

func TestAIOutcomeCursorDoesNotBlockClaimedDraft(t *testing.T) {
	w, g, a, threads := queueWorld(t, 1)
	private := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, private.ID); err != nil {
		t.Fatal(err)
	}
	privateBot := w.api(w.key(w.agent2, "Private drafter", auth.ScopeCollabDraft))
	secret := outcomeDraft(t, w, privateBot.URL, private, "m1")
	bot := w.api(w.key(w.agent, "Queue drafter", auth.ScopeCollabDraft))
	tx, err := w.d.DB.Pool.Begin(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(w.ctx)
	if _, err := loadAccountForUpdate(w.ctx, tx, private.ID); err != nil {
		t.Fatal(err)
	}
	if err := settleDone(w.ctx, tx, []uuid.UUID{secret.ThreadID}, w.agent2.UserID); err != nil {
		t.Fatal(err)
	}
	var claim claimResp
	if status := doJSON(t, "POST", bot.URL+"/v1/collab/ai-queue/claim", map[string]any{"account_id": a.ID}, &claim); status != 200 || claim.Claim == nil || claim.Claim.ConversationID != threads[0] {
		t.Fatalf("claim during outcome write: %d %+v", status, claim)
	}
	m := w.messageByRemote(a, "m1")
	body := map[string]any{"account_id": a.ID, "mode": "reply", "subject": "", "message_id": m.ID, "to": []string{"frank@example.com"}, "body_html": "<p>Answer</p>", "claim_token": claim.Claim.Token}
	done := make(chan int, 1)
	go func() { done <- doJSON(t, "POST", bot.URL+"/v1/collab/drafts", body, nil) }()
	if status := outcomeWriteFinished(t, done); status != 200 {
		t.Fatalf("claimed draft while cursor held: %d", status)
	}
	if n := w.count(`SELECT count(*) FROM collab_ai_claims WHERE thread_id = $1`, threads[0]); n != 0 {
		t.Fatal("claimed draft did not end claim")
	}
	g.mu.Lock()
	uploads := len(g.drafts.saves)
	g.mu.Unlock()
	if status := doJSON(t, "POST", bot.URL+"/v1/collab/drafts", body, nil); status != 409 {
		t.Fatalf("spent token: %d", status)
	}
	g.mu.Lock()
	after := len(g.drafts.saves)
	g.mu.Unlock()
	if after != uploads {
		t.Fatal("spent token reached provider")
	}
	if page := outcomeFeed(t, bot.URL, ""); len(page.Data) != 0 {
		t.Fatalf("initial draft entered outcome feed: %+v", page.Data)
	}
	if err := tx.Rollback(w.ctx); err != nil {
		t.Fatal(err)
	}
}
