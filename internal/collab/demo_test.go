package collab

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A demo account (sample data) keeps everything in opendesk: syncing, acting and sending
// never build a provider client, and nothing is queued for one.
func TestDemoAccountStaysLocal(t *testing.T) {
	w := newWorld(t)
	dialed := 0
	w.s.newProvider = func(context.Context, *accountRow) (Provider, error) {
		dialed++
		return nil, errors.New("a demo account dialed its provider")
	}
	if _, err := CreateDemoAccount(w.ctx, w.d, w.wid, DemoAccount{OwnerID: w.owner.UserID, Email: "sales@acme.com"}); err == nil {
		t.Fatal("a demo account on a deliverable domain was created")
	}
	team := w.team("Sales", w.owner, w.agent)
	id, err := CreateDemoAccount(w.ctx, w.d, w.wid, DemoAccount{OwnerID: w.owner.UserID, Email: "sales@acme.example.invalid", Name: "Sales",
		DisplayName: "Acme Sales", Shared: Audience{TeamIDs: []uuid.UUID{team}}, Folders: []string{"Clients"}})
	if err != nil {
		t.Fatal(err)
	}
	us := Address{Name: "Acme Sales", Email: "sales@acme.example.invalid"}
	them := Address{Name: "Elena Ruiz", Email: "elena@northwind.example.invalid"}
	start := time.Now().Add(-3 * time.Hour)
	stored, err := AddDemoThreads(w.ctx, w.d, w.wid, id, []DemoThread{
		{Messages: []DemoMessage{
			{From: them, To: []Address{us}, Subject: "Renewal", HTML: "<p>Can we talk about the renewal?</p><script>alert(1)</script>", Date: start, Folders: []string{RoleInbox}},
			{From: us, To: []Address{them}, Subject: "Re: Renewal", HTML: "<p>Sure, Thursday?</p>", Date: start.Add(time.Hour), SentBy: &w.agent.UserID, Folders: []string{RoleSent, RoleInbox}},
			{From: them, To: []Address{us}, Subject: "Re: Renewal", HTML: "<p>Thursday works.</p>", Date: start.Add(2 * time.Hour), Unread: true, Folders: []string{RoleInbox, "Clients"}},
		}, Assignees: []uuid.UUID{w.agent.UserID}, Comments: []DemoComment{{AuthorID: w.owner.UserID, BodyHTML: "<p>Yours, Adam.</p>"}}},
		{Messages: []DemoMessage{{From: us, To: []Address{them}, Subject: "Draft", HTML: "<p>Not sent yet</p>", Date: start, Draft: true, Folders: []string{RoleDrafts}}}},
	})
	if err != nil || stored != 2 {
		t.Fatalf("stored %d: %v", stored, err)
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE account_id = $1 AND message_count = 3 AND unread
		AND subject = 'Renewal' AND cardinality(folder_ids) = 3`, id); n != 1 {
		t.Fatal("the conversation was not threaded with its folders and state")
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND body_html LIKE '%script%'`, id); n != 0 {
		t.Fatal("a demo body was stored unsanitized")
	}
	if n := w.count(`SELECT count(*) FROM collab_assignees a JOIN collab_threads t ON t.id = a.thread_id WHERE t.account_id = $1`, id); n != 1 {
		t.Fatal("no assignee")
	}

	// The sync records it as up to date without a provider, and the scheduler skips it.
	if err := w.s.syncAccount(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.sync_account' AND args->>'account_id' = $1::text`, id); n != 0 {
		t.Fatalf("%d syncs queued for a demo account", n)
	}

	// A teammate on the team sees it, acts on it and sends from it: nothing leaves.
	srv := w.api(w.agent)
	var threads struct {
		Data []MailThread `json:"data"`
	}
	if st := doJSON(t, http.MethodGet, srv.URL+"/v1/collab/threads?box=all&account_id="+id.String(), nil, &threads); st != 200 || len(threads.Data) != 2 {
		t.Fatalf("list: %d %d", st, len(threads.Data))
	}
	var tid uuid.UUID
	for _, th := range threads.Data {
		if th.Subject == "Renewal" {
			tid = th.ID
		}
	}
	if st := doJSON(t, http.MethodPost, srv.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{tid}, "action": "archive"}, nil); st != 200 {
		t.Fatalf("archive: %d", st)
	}
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.apply' AND args->>'account_id' = $1::text`, id); n != 0 {
		t.Fatalf("%d provider actions queued for a demo account", n)
	}
	var last uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id FROM collab_messages WHERE thread_id = $1 ORDER BY sent_at DESC LIMIT 1`, tid).Scan(&last); err != nil {
		t.Fatal(err)
	}
	var sent MailMessage
	if st := doJSON(t, http.MethodPost, srv.URL+"/v1/collab/messages", map[string]any{"account_id": id, "mode": "reply", "message_id": last,
		"to": []string{them.Email}, "subject": "Re: Renewal", "body_html": "<p>See you Thursday.</p>"}, &sent); st != 200 {
		t.Fatalf("send: %d", st)
	}
	if sent.ThreadID != tid {
		t.Fatal("the reply left its conversation")
	}
	if n := w.count(`SELECT count(*) FROM collab_messages m JOIN collab_message_folders mf ON mf.message_id = m.id
		JOIN collab_folders f ON f.id = mf.folder_id WHERE m.id = $1 AND f.role = 'sent'`, sent.ID); n != 1 {
		t.Fatal("the sent reply is not in Sent")
	}
	if dialed != 0 {
		t.Fatalf("a provider was built %d times for a demo account", dialed)
	}
}
