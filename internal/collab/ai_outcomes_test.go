package collab

import (
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
)

// AI draft outcomes (contracts/collab.md "AI draft outcomes", GH#261): the server records
// what became of each AI draft (sent as written, sent edited, discarded, replaced by a
// person's own reply, abandoned when the conversation was done) and keeps the AI's last
// text even after a person takes the draft over. A person may add feedback. Only the key
// that wrote a draft reads it back, through a cursor feed.

type outcomesPage struct {
	Data       []AIDraftOutcome `json:"data"`
	NextCursor *string          `json:"next_cursor"`
}

func TestAIDraftOutcomes(t *testing.T) {
	w, g, a, threads := queueWorld(t, 5)
	_ = g
	botKey := w.key(w.agent, "Local drafter", auth.ScopeCollabDraft)
	bot := w.api(botKey)
	other := w.api(w.key(w.agent, "Other drafter", auth.ScopeCollabDraft))
	human := w.api(w.agent)
	var e apiError

	// draft has the key save a reply to conversation i, labelled ref.
	draft := func(i int, html, ref string) MailMessage {
		t.Helper()
		m := w.messageByRemote(a, "m"+strconv.Itoa(i+1))
		var d MailMessage
		if st := doJSON(t, "POST", bot.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m.ID,
			"to": []string{"frank@example.com"}, "subject": "", "body_html": html, "client_ref": ref}, &d); st != 200 || d.AIDraft == nil {
			t.Fatalf("draft %d: %d %+v", i, st, d)
		}
		if d.AIDraft.Outcome != nil || d.AIDraft.Client != "Local drafter" {
			t.Fatalf("new AI draft: %+v", d.AIDraft)
		}
		return d
	}
	feed := func(srv, cursor string) outcomesPage {
		t.Helper()
		var pg outcomesPage
		url := srv + "/v1/collab/ai-drafts/outcomes"
		if cursor != "" {
			url += "?cursor=" + cursor
		}
		if st := doJSON(t, "GET", url, nil, &pg); st != 200 {
			t.Fatalf("feed: %d", st)
		}
		return pg
	}

	// Nothing has happened yet: the feed is empty.
	d0 := draft(0, "<p>About $2 each.</p>", "prompt:replies@v14")
	if pg := feed(bot.URL, ""); len(pg.Data) != 0 {
		t.Fatalf("feed before any outcome: %+v", pg.Data)
	}

	// 1. Sent as the AI wrote it.
	var sent0 MailMessage
	if st := doJSON(t, "POST", human.URL+"/v1/collab/drafts/"+d0.ID.String()+"/send", nil, &sent0); st != 200 {
		t.Fatalf("send d0: %d", st)
	}
	if sent0.AIDraft == nil || sent0.AIDraft.Outcome == nil || *sent0.AIDraft.Outcome != OutcomeSentUnchanged {
		t.Fatalf("sent message's AI draft: %+v", sent0.AIDraft)
	}

	// 2. A person edits it (taking it over), then sends: edited, and the AI's own last
	// text is what the feed returns.
	d1 := draft(1, "<p>First try.</p>", "")
	if st := doJSON(t, "PUT", bot.URL+"/v1/collab/drafts/"+d1.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Q2", "body_html": "<p>Second try.</p>", "client_ref": "prompt:replies@v15"}, nil); st != 200 {
		t.Fatalf("AI saves again: %d", st)
	}
	var taken MailMessage
	if st := doJSON(t, "PUT", human.URL+"/v1/collab/drafts/"+d1.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Q2", "body_html": "<p>Second try, checked: $1.90.</p>"}, &taken); st != 200 || taken.DraftedByAI != nil ||
		taken.AIDraft == nil {
		t.Fatalf("take over: %d %+v", st, taken)
	}
	if st := doJSON(t, "POST", human.URL+"/v1/collab/drafts/"+d1.ID.String()+"/send", nil, nil); st != 200 {
		t.Fatalf("send d1: %d", st)
	}

	// 3. Discarded.
	d2 := draft(2, "<p>Wrong answer.</p>", "")
	if st := doJSON(t, "DELETE", human.URL+"/v1/collab/drafts/"+d2.ID.String(), nil, nil); st != 204 {
		t.Fatalf("discard: %d", st)
	}

	// 4. A person writes their own reply instead.
	d3 := draft(3, "<p>Maybe.</p>", "")
	m4 := w.messageByRemote(a, "m4")
	var own MailMessage
	if st := doJSON(t, "POST", human.URL+"/v1/collab/messages", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m4.ID,
		"to": []string{"frank@example.com"}, "subject": "", "body_html": "<p>Yes, definitely.</p>"}, &own); st != 200 {
		t.Fatalf("own reply: %d", st)
	}
	if own.AIDraft != nil {
		t.Fatalf("a person's own reply carries an AI draft: %+v", own.AIDraft)
	}

	// 5. Done without sending it.
	d4 := draft(4, "<p>Not needed.</p>", "")
	if st := doJSON(t, "POST", human.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{threads[4]},
		"action": "done"}, nil); st != 200 {
		t.Fatalf("done: %d", st)
	}

	pg := feed(bot.URL, "")
	want := map[uuid.UUID]string{d0.ID: OutcomeSentUnchanged, d1.ID: OutcomeSentEdited, d2.ID: OutcomeDiscarded,
		d3.ID: OutcomeReplaced, d4.ID: OutcomeAbandoned}
	if len(pg.Data) != len(want) {
		t.Fatalf("feed: %d items %+v", len(pg.Data), pg.Data)
	}
	for _, o := range pg.Data {
		if o.Outcome == nil || *o.Outcome != want[o.DraftID] {
			t.Errorf("draft %s: outcome %v, want %s", o.DraftID, o.Outcome, want[o.DraftID])
		}
		switch o.DraftID {
		case d0.ID:
			if o.ClientRef != "prompt:replies@v14" || o.SentMessageID == nil || *o.SentMessageID != sent0.ID ||
				draftWords(o.AIBodyHTML) != "About $2 each." || o.TakenOverAt != nil {
				t.Errorf("sent unchanged: %+v", o)
			}
		case d1.ID:
			if o.ClientRef != "prompt:replies@v15" || draftWords(o.AIBodyHTML) != "Second try." || o.TakenOverAt == nil {
				t.Errorf("sent edited: %+v", o)
			}
		case d3.ID:
			if o.SentMessageID == nil || *o.SentMessageID != own.ID {
				t.Errorf("replaced: %+v", o)
			}
		}
	}
	cursor := pg.Data[len(pg.Data)-1].Cursor
	if more := feed(bot.URL, cursor); len(more.Data) != 0 {
		t.Fatalf("past the last cursor: %+v", more.Data)
	}

	// Paging.
	var first outcomesPage
	if st := doJSON(t, "GET", bot.URL+"/v1/collab/ai-drafts/outcomes?limit=2", nil, &first); st != 200 || len(first.Data) != 2 ||
		first.NextCursor == nil {
		t.Fatalf("page: %d %+v", st, first)
	}
	if rest := feed(bot.URL, *first.NextCursor); len(rest.Data) != 3 {
		t.Fatalf("rest: %+v", rest.Data)
	}

	// Feedback from a person brings the draft back past the cursor, without its words in
	// the audit log. The key cannot give feedback, and the feed is the key's alone.
	fb := map[string]any{"reason": "wrong_facts", "note": "Price is per case, not per bottle.", "better_answer_html": "<p>$2 per case.</p>"}
	if st := doJSON(t, "PUT", bot.URL+"/v1/collab/ai-drafts/"+sent0.AIDraft.ID.String()+"/feedback", fb, &e); st != 403 {
		t.Fatalf("key gives feedback: %d", st)
	}
	if st := doJSON(t, "PUT", human.URL+"/v1/collab/ai-drafts/"+sent0.AIDraft.ID.String()+"/feedback",
		map[string]any{"reason": "made_up"}, &e); st != 422 {
		t.Fatalf("unknown reason: %d", st)
	}
	if st := doJSON(t, "PUT", human.URL+"/v1/collab/ai-drafts/"+sent0.AIDraft.ID.String()+"/feedback", fb, nil); st != 204 {
		t.Fatalf("feedback: %d", st)
	}
	again := feed(bot.URL, cursor)
	if len(again.Data) != 1 || again.Data[0].DraftID != d0.ID || again.Data[0].Feedback == nil ||
		again.Data[0].Feedback.Reason != "wrong_facts" || again.Data[0].Feedback.Note != "Price is per case, not per bottle." ||
		draftWords(again.Data[0].Feedback.BetterAnswerHTML) != "$2 per case." {
		t.Fatalf("feedback in the feed: %+v", again.Data)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE action = 'collab.ai_draft_feedback' AND entity_id = $1
		AND actor_user_id = $2 AND data::text NOT LIKE '%per case%'`, sent0.AIDraft.ID, w.agent.UserID); n != 1 {
		t.Fatalf("%d feedback audit entries without the words", n)
	}
	if pg := feed(other.URL, ""); len(pg.Data) != 0 {
		t.Fatalf("another key reads this key's outcomes: %+v", pg.Data)
	}
	if st := doJSON(t, "GET", human.URL+"/v1/collab/ai-drafts/outcomes", nil, &e); st != 403 {
		t.Fatalf("a person reads the feed: %d", st)
	}

	// A draft already settled keeps its final outcome; a draft the conversation's member
	// no longer sees drops out of the feed.
	mustExec(t, w.d, `UPDATE collab_accounts SET visibility = 'private', owner_id = $2 WHERE id = $1`, a.ID, w.agent2.UserID)
	if pg := feed(bot.URL, ""); len(pg.Data) != 0 {
		t.Fatalf("outcomes of conversations the member no longer sees: %+v", pg.Data)
	}
}

func TestDraftWords(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"<p>Hello  there</p>", "<div>Hello there</div>"},
		{"<p>Hi</p>", `<p>Hi</p><div class="od-signature"><p>All the best,<br>Justin</p></div>`},
		{"<p>Hi</p>", `<p>Hi</p><blockquote>On Monday Frank wrote: price?</blockquote>`},
	} {
		if draftWords(c.a) != draftWords(c.b) {
			t.Errorf("%q vs %q: %q != %q", c.a, c.b, draftWords(c.a), draftWords(c.b))
		}
	}
	if draftWords("<p>$2 each</p>") == draftWords("<p>$1.90 each</p>") {
		t.Fatal("an edit reads as unchanged")
	}
}
