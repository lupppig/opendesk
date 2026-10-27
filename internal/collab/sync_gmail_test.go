package collab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/jobs"
)

// The provider side against fakes: Gmail and Microsoft Graph over httptest, IMAP against
// go-imap's in-memory server.

// testMail builds a raw message.
type testMail struct {
	from, to, subject, msgID, inReplyTo, refs, body string
	date                                            time.Time
	image                                           bool // an inline image (cid:logo)
}

func (m testMail) raw() []byte {
	if m.date.IsZero() {
		m.date = time.Now().Add(-time.Hour)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s>\r\nMIME-Version: 1.0\r\n",
		m.from, m.to, m.subject, m.date.Format(time.RFC1123Z), m.msgID)
	if m.inReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: <%s>\r\n", m.inReplyTo)
	}
	if m.refs != "" {
		fmt.Fprintf(&b, "References: %s\r\n", m.refs)
	}
	if !m.image {
		fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", m.body)
		return []byte(b.String())
	}
	b.WriteString("Content-Type: multipart/related; boundary=\"BOUND\"\r\n\r\n")
	fmt.Fprintf(&b, "--BOUND\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>%s</p><img src=\"cid:logo\"><script>alert(1)</script>\r\n", m.body)
	b.WriteString("--BOUND\r\nContent-Type: image/png\r\nContent-ID: <logo>\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake-image-bytes")) + "\r\n--BOUND--\r\n")
	return []byte(b.String())
}

// fakeGmail is a small Gmail API.
type fakeGmail struct {
	t               *testing.T
	mu              sync.Mutex
	labels          []map[string]any
	msgs            map[string]*gmailFakeMsg
	order           []string // oldest first
	history         int
	changes         []map[string]any // history records
	historyPageSize int
	gone            bool // history answers 404
	status          int  // every call answers this when set
	busy            int  // the next calls answer 429 rateLimitExceeded
	modify          []map[string]any
	trashed         []string
	sent            []string // multipart bodies
	gets            int
	// failRawAfter makes raw reads past this many answer 503 (history_test.go); 0 never.
	failRawAfter int
	// settings is the account's sendAs and vacation (settings_test.go).
	settings fakeGmailSettings
	// drafts are the drafts API and label colours (drafts_test.go).
	drafts fakeGmailDrafts
	// beforeDraftUpload, when set, runs as a draft upload arrives, before the fake takes
	// its lock: a test pauses a save there (ai_drafts_test.go).
	beforeDraftUpload func()
	hookMu            sync.Mutex // guards beforeDraftUpload
}

// onDraftUpload sets (or, with nil, clears) beforeDraftUpload.
func (g *fakeGmail) onDraftUpload(f func()) {
	g.hookMu.Lock()
	defer g.hookMu.Unlock()
	g.beforeDraftUpload = f
}

type gmailFakeMsg struct {
	id, thread string
	labels     []string
	raw        []byte
	date       time.Time
}

func newFakeGmail(t *testing.T) *fakeGmail {
	return &fakeGmail{t: t, msgs: map[string]*gmailFakeMsg{}, history: 100, labels: []map[string]any{
		{"id": "INBOX", "name": "INBOX", "type": "system"}, {"id": "SENT", "name": "SENT", "type": "system"},
		{"id": "DRAFT", "name": "DRAFT", "type": "system"}, {"id": "TRASH", "name": "TRASH", "type": "system"},
		{"id": "SPAM", "name": "SPAM", "type": "system"}, {"id": "STARRED", "name": "STARRED", "type": "system"},
		{"id": "IMPORTANT", "name": "IMPORTANT", "type": "system"}, {"id": "UNREAD", "name": "UNREAD", "type": "system"},
		{"id": "CHAT", "name": "CHAT", "type": "system"}, {"id": "CATEGORY_PERSONAL", "name": "CATEGORY_PERSONAL", "type": "system"},
		{"id": "Label_1", "name": "Clients/Acme", "type": "user", "color": map[string]string{"backgroundColor": "#16a766"}},
	}}
}

func (g *fakeGmail) add(id, thread string, labels []string, m testMail) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m.date.IsZero() {
		m.date = time.Now().Add(-time.Duration(100-len(g.order)) * time.Minute)
	}
	g.msgs[id] = &gmailFakeMsg{id: id, thread: thread, labels: labels, raw: m.raw(), date: m.date}
	g.order = append(g.order, id)
}

// record adds a history record.
func (g *fakeGmail) record(kind string, id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.history++
	msg := map[string]any{"id": id}
	if m, ok := g.msgs[id]; ok {
		msg["threadId"], msg["labelIds"] = m.thread, m.labels
	}
	g.changes = append(g.changes, map[string]any{"id": strconv.Itoa(g.history), kind: []map[string]any{{"message": msg}}})
}

func (g *fakeGmail) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.hookMu.Lock()
	hook := g.beforeDraftUpload
	g.hookMu.Unlock()
	if hook != nil && strings.Contains(r.URL.Path, "/upload/gmail/v1/users/me/drafts") {
		hook()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.Header.Get("Authorization") == "" {
		w.WriteHeader(401)
		return
	}
	if g.busy > 0 {
		g.busy--
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"errors":[{"reason":"rateLimitExceeded"}]}}`))
		return
	}
	if g.status != 0 {
		w.WriteHeader(g.status)
		_, _ = w.Write([]byte(`{"error":{"code":` + strconv.Itoa(g.status) + `,"message":"secret detail from@example.com"}}`))
		return
	}
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	path := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me")
	q := r.URL.Query()
	switch {
	case strings.HasPrefix(path, "/settings/"):
		g.settings.serve(g.t, w, r, path)
	case strings.HasPrefix(path, "/drafts") || strings.HasPrefix(path, "/upload/gmail/v1/users/me/drafts") ||
		(r.Method == "PATCH" && strings.HasPrefix(path, "/labels/")):
		g.serveDrafts(w, r, path)
	case r.Method == "GET" && path == "/labels":
		reply(map[string]any{"labels": g.labels})
	case r.Method == "GET" && path == "/profile":
		reply(map[string]any{"historyId": strconv.Itoa(g.history), "messagesTotal": len(g.msgs)})
	case r.Method == "GET" && path == "/messages":
		// Newest first, with the search's after: and before: (unix seconds), spam and trash
		// only when asked for, in pages whose token is an offset.
		var after, before int64
		for _, term := range strings.Fields(q.Get("q")) {
			if v, ok := strings.CutPrefix(term, "after:"); ok {
				after, _ = strconv.ParseInt(v, 10, 64)
			}
			if v, ok := strings.CutPrefix(term, "before:"); ok {
				before, _ = strconv.ParseInt(v, 10, 64)
			}
		}
		list := []map[string]string{}
		for i := len(g.order) - 1; i >= 0; i-- {
			m := g.msgs[g.order[i]]
			if m == nil || (q.Get("labelIds") != "" && !slices.Contains(m.labels, q.Get("labelIds"))) {
				continue
			}
			if (after > 0 && m.date.Unix() <= after) || (before > 0 && m.date.Unix() >= before) {
				continue
			}
			if q.Get("includeSpamTrash") != "true" && (slices.Contains(m.labels, "SPAM") || slices.Contains(m.labels, "TRASH")) {
				continue
			}
			list = append(list, map[string]string{"id": m.id, "threadId": m.thread})
		}
		total := len(list)
		offset, _ := strconv.Atoi(q.Get("pageToken"))
		if q.Get("pageToken") == "refused" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid pageToken","errors":[{"reason":"invalidArgument"}]}}`))
			return
		}
		offset = min(offset, len(list))
		size, _ := strconv.Atoi(q.Get("maxResults"))
		if size <= 0 {
			size = 100
		}
		end := min(offset+size, len(list))
		next := ""
		if end < len(list) {
			next = strconv.Itoa(end)
		}
		reply(map[string]any{"messages": list[offset:end], "nextPageToken": next, "resultSizeEstimate": total})
	case r.Method == "GET" && strings.HasPrefix(path, "/messages/"):
		m := g.msgs[strings.TrimPrefix(path, "/messages/")]
		if q.Get("format") == "raw" {
			g.gets++
			if g.failRawAfter > 0 && g.gets > g.failRawAfter {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"error":{"code":503,"message":"Backend Error"}}`))
				return
			}
		}
		if m == nil {
			w.WriteHeader(404)
			return
		}
		reply(map[string]any{"id": m.id, "threadId": m.thread, "labelIds": m.labels, "historyId": strconv.Itoa(g.history),
			"internalDate": strconv.FormatInt(m.date.UnixMilli(), 10), "raw": base64.URLEncoding.EncodeToString(m.raw)})
	case r.Method == "GET" && path == "/history":
		if g.gone {
			w.WriteHeader(404)
			return
		}
		// A page token holds its place and the newest record of the read: a read sees
		// what was there when it began, the most Gmail promises.
		since, _ := strconv.Atoi(q.Get("startHistoryId"))
		offset, bound := 0, g.history
		if token := q.Get("pageToken"); token != "" {
			a, b, ok := strings.Cut(token, ":")
			offset, _ = strconv.Atoi(a)
			var err error
			if bound, err = strconv.Atoi(b); !ok || err != nil {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid pageToken","errors":[{"reason":"invalidArgument"}]}}`))
				return
			}
		}
		list := []map[string]any{}
		for _, c := range g.changes {
			if id, _ := strconv.Atoi(c["id"].(string)); id > since && id <= bound {
				list = append(list, c)
			}
		}
		pageSize := len(list)
		if g.historyPageSize > 0 {
			pageSize = g.historyPageSize
		}
		offset = min(offset, len(list))
		end := min(offset+pageSize, len(list))
		next := ""
		if end < len(list) {
			next = strconv.Itoa(end) + ":" + strconv.Itoa(bound)
		}
		reply(map[string]any{"history": list[offset:end], "historyId": strconv.Itoa(g.history), "nextPageToken": next})
	case r.Method == "POST" && path == "/messages/batchModify":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		g.modify = append(g.modify, in)
		w.WriteHeader(204)
	case r.Method == "POST" && strings.HasPrefix(path, "/threads/") && strings.HasSuffix(path, "/trash"):
		g.trashed = append(g.trashed, strings.TrimSuffix(strings.TrimPrefix(path, "/threads/"), "/trash"))
		reply(map[string]any{})
	case r.Method == "POST" && r.URL.Path == "/upload/gmail/v1/users/me/messages/send":
		b, _ := io.ReadAll(r.Body)
		g.sent = append(g.sent, string(b))
		reply(map[string]any{"id": "sent-1", "threadId": "t1", "labelIds": []string{"SENT"}})
	default:
		g.t.Errorf("unexpected Gmail call %s %s", r.Method, r.URL.String())
		w.WriteHeader(400)
	}
}

func (w *world) useGmail(g *fakeGmail) {
	srv := httptest.NewServer(g)
	w.t.Cleanup(srv.Close)
	w.s.gmailBase = srv.URL
}

// threadByRemote loads a synced conversation by provider thread id.
func (w *world) threadByRemote(a *accountRow, remote string) *threadRow {
	w.t.Helper()
	t, err := scanThread(w.d.DB.Pool.QueryRow(w.ctx, `SELECT `+threadColumns+` FROM collab_threads WHERE account_id = $1 AND remote_id = $2`, a.ID, remote))
	if err != nil {
		w.t.Fatalf("thread %s: %v", remote, err)
	}
	return t
}

func (w *world) messageByRemote(a *accountRow, remote string) *messageRow {
	w.t.Helper()
	m, err := scanMessage(w.d.DB.Pool.QueryRow(w.ctx, `SELECT `+messageColumns+` FROM collab_messages m WHERE m.account_id = $1 AND m.remote_id = $2`, a.ID, remote))
	if err != nil {
		w.t.Fatalf("message %s: %v", remote, err)
	}
	return m
}

func (w *world) folderByRemote(a *accountRow, remote string) *folderRow {
	w.t.Helper()
	f, err := scanFolder(w.d.DB.Pool.QueryRow(w.ctx, `SELECT `+folderColumns+` FROM collab_folders WHERE account_id = $1 AND remote_id = $2`, a.ID, remote))
	if err != nil {
		w.t.Fatalf("folder %s: %v", remote, err)
	}
	return f
}

func (w *world) reload(a *accountRow) *accountRow {
	w.t.Helper()
	b, err := loadAccount(w.ctx, w.d.DB.Pool, w.wid, a.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return b
}

// act applies an action in opendesk and then runs its collab.apply job.
func (w *world) act(a *accountRow, threads []uuid.UUID, action string, folder *folderRow) {
	w.t.Helper()
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return w.s.applyLocal(w.ctx, tx, a, threads, action, folder) })
	if err != nil {
		w.t.Fatalf("applyLocal %s: %v", action, err)
	}
	args := jobs.CollabApplyArgs{WorkspaceID: a.WorkspaceID, AccountID: a.ID, ThreadIDs: threads, Action: normalizeAction(action, folder)}
	if folder != nil && (action == ActionMove || action == ActionLabel || action == ActionUnlabel) && args.Action == action {
		args.FolderID = &folder.ID
	}
	if err := w.s.applyRemote(w.ctx, &river.Job[jobs.CollabApplyArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 8}, Args: args}); err != nil {
		w.t.Fatalf("applyRemote %s: %v", action, err)
	}
}

func historyOf(a *accountRow) string {
	var st struct {
		HistoryID string `json:"history_id"`
	}
	_ = json.Unmarshal(a.SyncState, &st)
	return st.HistoryID
}

func sliceHas(list []uuid.UUID, id uuid.UUID) bool { return slices.Contains(list, id) }

func TestGmailSync(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX", "UNREAD", "CATEGORY_PERSONAL"}, testMail{from: "Frank Sender <frank@example.com>",
		to: a.Email, subject: "Quote", msgID: "q1@example.com", body: "Can you send a quote?"})
	g.add("m2", "t1", []string{"SENT"}, testMail{from: a.Email, to: "Frank Sender <frank@example.com>, Gina <gina@example.com>",
		subject: "Re: Quote", msgID: "q2@acme.test", inReplyTo: "q1@example.com", body: "Here it is."})
	g.add("m3", "t2", []string{"Label_1"}, testMail{from: "News <news@example.com>", to: a.Email, subject: "Logo",
		msgID: "n1@example.com", body: "Our new logo", image: true})

	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	a = w.reload(a)
	if a.Status != "active" || a.LastSyncedAt == nil || historyOf(a) != "100" {
		t.Fatalf("account after first sync: %s %v %s", a.Status, a.LastSyncedAt, a.SyncState)
	}
	// Labels: system labels are the special folders, user labels are labels, the rest hidden.
	label := w.folderByRemote(a, "Label_1")
	if label.Kind != KindLabel || label.Path != "Clients/Acme" || label.Name != "Acme" || label.Color == nil || *label.Color != "#16a766" {
		t.Fatalf("label: %+v", label)
	}
	if n := w.count(`SELECT count(*) FROM collab_folders WHERE account_id = $1 AND remote_id IN ('UNREAD', 'CHAT', 'CATEGORY_PERSONAL', 'ARCHIVE', 'DRAFTS')`, a.ID); n != 0 {
		t.Fatalf("%d hidden or gone folders kept", n)
	}
	inbox, sent := w.folderByRemote(a, "INBOX"), w.folderByRemote(a, "SENT")
	if inbox.Role == nil || *inbox.Role != RoleInbox || w.folderByRemote(a, "DRAFT").Role == nil {
		t.Fatalf("inbox: %+v", inbox)
	}
	// Threads follow Gmail's.
	t1 := w.threadByRemote(a, "t1")
	if t1.MessageCount != 2 || !t1.Unread || t1.Subject != "Quote" || t1.Snippet != "Here it is." ||
		!sliceHas(t1.FolderIDs, inbox.ID) || !sliceHas(t1.FolderIDs, sent.ID) {
		t.Fatalf("t1: %+v", t1)
	}
	if len(t1.Participants) != 3 || t1.Participants[len(t1.Participants)-1].Email != a.Email || t1.Participants[0].Email == a.Email {
		t.Fatalf("participants: %+v", t1.Participants)
	}
	m1 := w.messageByRemote(a, "m1")
	if m1.MessageID != "q1@example.com" || m1.From.Name != "Frank Sender" || !m1.Unread || m1.RawKey != "" {
		t.Fatalf("m1: %+v", m1)
	}
	// Inline images point at their signed attachment URL; scripts are gone; the raw is kept.
	m3 := w.messageByRemote(a, "m3")
	if m3.RawKey == "" || len(m3.Attachments) != 1 || !m3.Attachments[0].Inline || m3.Attachments[0].ContentID != "logo" {
		t.Fatalf("m3: key %q atts %+v", m3.RawKey, m3.Attachments)
	}
	if !strings.Contains(m3.BodyHTML, "/v1/collab/attachments/"+m3.ID.String()+"/0?exp=") || strings.Contains(m3.BodyHTML, "cid:") ||
		strings.Contains(m3.BodyHTML, "<script") {
		t.Fatalf("m3 body: %s", m3.BodyHTML)
	}
	if w.threadByRemote(a, "t2").HasAttachments {
		t.Fatal("an inline image counts as an attachment")
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.changed' AND $2 = ANY(user_ids)`, w.wid, w.agent.UserID); n < 1 {
		t.Fatal("no collab.changed")
	}
	// Search covers the body.
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE account_id = $1 AND search @@ plainto_tsquery('simple', 'quote')`, a.ID); n != 1 {
		t.Fatalf("search: %d", n)
	}

	// Incremental: read elsewhere, a new reply, a deleted message.
	g.mu.Lock()
	g.msgs["m1"].labels = []string{"INBOX"}
	g.mu.Unlock()
	g.record("labelsRemoved", "m1")
	g.add("m4", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "Gina <gina@example.com>", to: a.Email, subject: "Re: Quote",
		msgID: "q3@example.com", body: "Thanks!", date: time.Now()})
	g.record("messagesAdded", "m4")
	g.record("messagesDeleted", "m3")
	g.mu.Lock()
	delete(g.msgs, "m3")
	g.mu.Unlock()
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	t1 = w.threadByRemote(a, "t1")
	if t1.MessageCount != 3 || !t1.Unread || t1.Snippet != "Thanks!" || w.messageByRemote(a, "m1").Unread {
		t.Fatalf("t1 after history: %+v", t1)
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE account_id = $1 AND remote_id = 't2'`, a.ID); n != 0 {
		t.Fatal("a conversation without messages is kept")
	}
	if n := w.count(`SELECT count(*) FROM collab_storage_trash WHERE key = $1`, m3.RawKey); n != 1 {
		t.Fatal("the deleted message's raw is not in the storage trash")
	}
	if historyOf(w.reload(a)) != strconv.Itoa(g.history) {
		t.Fatalf("history id not advanced: %s", w.reload(a).SyncState)
	}
	// A history id Gmail no longer has means a first sync again, without refetching mail.
	g.mu.Lock()
	g.gone, g.gets = true, 0
	g.mu.Unlock()
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a = w.reload(a); a.Status != "active" || g.gets != 0 {
		t.Fatalf("after expired history: %s, %d messages fetched again", a.Status, g.gets)
	}
	// The cleanup empties the storage trash.
	if err := w.s.cleanup(w.ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_storage_trash WHERE key = $1`, m3.RawKey); n != 0 {
		t.Fatal("storage trash not emptied")
	}
	if _, err := w.d.Storage.Get(context.Background(), m3.RawKey); err == nil {
		t.Fatal("the raw message is still in storage")
	}
}

func gmailStateOf(t *testing.T, a *accountRow) gmailSyncState {
	t.Helper()
	var st gmailSyncState
	if err := json.Unmarshal(a.SyncState, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// burst adds n new messages to the inbox, each with its messagesAdded record.
func (g *fakeGmail) burst(prefix string, n int, to string) {
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%04d", prefix, i)
		g.add(id, id, []string{"INBOX"}, testMail{from: "sender@example.com", to: to,
			subject: "Burst", msgID: id + "@example.com", body: "Message"})
		g.record("messagesAdded", id)
	}
}

func TestGmailSyncBurstKeepsNewMailAndLeavesTouchedMailOut(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	g.burst("burst", initialMax+1, a.Email)
	// Old mail never imported, read and moved in bulk elsewhere: history only touches it.
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("old-%d", i)
		g.add(id, id, []string{"INBOX"}, testMail{from: "old@example.com", to: a.Email, subject: "Old",
			msgID: id + "@example.com", body: "Old", date: time.Now().Add(-90 * 24 * time.Hour)})
		g.record("labelsRemoved", id)
		g.record("labelsAdded", id)
	}
	touched := `SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id LIKE 'old-%'`

	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != initialMax {
		t.Fatalf("first pass imported %d messages, want %d", got, initialMax)
	}
	if st := gmailStateOf(t, w.reload(a)); !slices.Equal(st.Backlog, []string{"burst-0000"}) || st.HistoryID != strconv.Itoa(g.history) {
		t.Fatalf("state after the first pass: %+v", st)
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != initialMax+1 || w.count(touched, a.ID) != 0 {
		t.Fatalf("second pass: %d messages, %d touched old ones", got, w.count(touched, a.ID))
	}
	if st := gmailStateOf(t, w.reload(a)); len(st.Backlog) != 0 {
		t.Fatalf("backlog after the second pass: %v", st.Backlog)
	}
}

func TestGmailSyncNewMailArrivesBeforeTheBacklog(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	g.burst("burst", 2*initialMax+1, a.Email)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if st := gmailStateOf(t, w.reload(a)); len(st.Backlog) != initialMax+1 {
		t.Fatalf("backlog after the burst: %d", len(st.Backlog))
	}
	// Mail that arrives while the backlog is longer than a pass comes with the next pass.
	g.add("fresh", "fresh", []string{"INBOX", "UNREAD"}, testMail{from: "new@example.com", to: a.Email, subject: "Fresh",
		msgID: "fresh@example.com", body: "Just now", date: time.Now()})
	g.record("messagesAdded", "fresh")
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id = 'fresh'`, a.ID); got != 1 {
		t.Fatal("new mail waited behind the backlog")
	}
	if st := gmailStateOf(t, w.reload(a)); len(st.Backlog) != 2 {
		t.Fatalf("backlog after the second pass: %d", len(st.Backlog))
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != 2*initialMax+2 {
		t.Fatalf("after the backlog: %d messages, want %d", got, 2*initialMax+2)
	}
}

func TestGmailSyncDeletedMailLeavesTheBacklog(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	g.burst("burst", initialMax+2, a.Email)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if st := gmailStateOf(t, w.reload(a)); !slices.Equal(st.Backlog, []string{"burst-0000", "burst-0001"}) {
		t.Fatalf("backlog after the burst: %v", st.Backlog)
	}
	// Deleted before its turn: history says so, and it is gone from the backlog.
	g.record("messagesDeleted", "burst-0000")
	g.mu.Lock()
	delete(g.msgs, "burst-0000")
	g.gets = 0
	g.mu.Unlock()
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if st := gmailStateOf(t, w.reload(a)); len(st.Backlog) != 0 {
		t.Fatalf("backlog after the delete: %v", st.Backlog)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id = 'burst-0000'`, a.ID); n != 0 || g.gets != 1 {
		t.Fatalf("deleted message: %d stored, %d messages fetched", n, g.gets)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != initialMax+1 {
		t.Fatalf("%d messages, want %d", got, initialMax+1)
	}
}

func TestGmailSyncBacklogIsBounded(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// A backlog already full: the oldest of it give way to this pass's new mail.
	full := make([]string, gmailBacklogMax)
	for i := range full {
		full[i] = fmt.Sprintf("waiting-%04d", i) // gone at Gmail by now: nothing to fetch
	}
	st := gmailStateOf(t, w.reload(a))
	st.Backlog = full
	if err := w.s.saveAccountState(w.ctx, a, st); err != nil {
		t.Fatal(err)
	}
	g.burst("burst", initialMax+3, a.Email)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	got := gmailStateOf(t, w.reload(a)).Backlog
	if len(got) != gmailBacklogMax || got[0] != "waiting-0003" || !slices.Equal(got[len(got)-3:], []string{"burst-0000", "burst-0001", "burst-0002"}) {
		t.Fatalf("backlog: %d, %v ... %v", len(got), got[:2], got[len(got)-3:])
	}
}

func TestGmailSyncResumesHistoryPagination(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}

	g.historyPageSize = 1
	g.burst("page", gmailHistoryPagesPerPass+1, a.Email)
	runStart := strconv.Itoa(g.history)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != gmailHistoryPagesPerPass {
		t.Fatalf("first pass imported %d messages, want %d", got, gmailHistoryPagesPerPass)
	}
	if st := gmailStateOf(t, w.reload(a)); st.HistoryID != "100" || st.PageToken == "" || st.RunHistoryID != runStart {
		t.Fatalf("state after the first pass: %+v", st)
	}

	// Mail that arrives while the read goes on is read with the next one.
	g.burst("late", 1, a.Email)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if st := gmailStateOf(t, w.reload(a)); st.HistoryID != runStart || st.PageToken != "" {
		t.Fatalf("state after the read: %+v", st)
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != gmailHistoryPagesPerPass+2 {
		t.Fatalf("imported %d messages, want %d", got, gmailHistoryPagesPerPass+2)
	}
}

func TestGmailSyncRefusedPageTokenReadsAgain(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	g.burst("mail", 2, a.Email)
	st := gmailStateOf(t, w.reload(a))
	st.PageToken, st.RunHistoryID = "no-longer-valid", "100"
	if err := w.s.saveAccountState(w.ctx, a, st); err != nil {
		t.Fatal(err)
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	a = w.reload(a)
	if st := gmailStateOf(t, a); a.Status != "active" || st.PageToken != "" || st.HistoryID != strconv.Itoa(g.history) {
		t.Fatalf("after a refused page: %s %+v", a.Status, st)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != 2 {
		t.Fatalf("imported %d messages, want 2", got)
	}
}

func TestGmailSyncExpiredHistoryDuringAPagedRead(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	g.historyPageSize = 1
	g.burst("page", gmailHistoryPagesPerPass+1, a.Email)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if st := gmailStateOf(t, w.reload(a)); st.PageToken == "" {
		t.Fatalf("no saved page: %+v", st)
	}
	// Gmail no longer has the history the read started from: a first sync again.
	g.mu.Lock()
	g.gone = true
	g.mu.Unlock()
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	a = w.reload(a)
	if st := gmailStateOf(t, a); a.Status != "active" || st.PageToken != "" || st.RunHistoryID != "" || st.HistoryID != strconv.Itoa(g.history) {
		t.Fatalf("after expired history: %s %+v", a.Status, st)
	}
	if got := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); got != gmailHistoryPagesPerPass+1 {
		t.Fatalf("imported %d messages, want %d", got, gmailHistoryPagesPerPass+1)
	}
}

func TestGmailActionsAndSend(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "frank@example.com", to: a.Email, subject: "Hi", msgID: "h1@example.com", body: "Hi"})
	g.add("m2", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "frank@example.com", to: a.Email, subject: "Re: Hi", msgID: "h2@example.com",
		inReplyTo: "h1@example.com", body: "Again", date: time.Now()})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	t1 := w.threadByRemote(a, "t1")
	inbox := w.folderByRemote(a, "INBOX")

	// Archive removes INBOX from every message, here and at Gmail.
	w.act(a, []uuid.UUID{t1.ID}, ActionArchive, nil)
	if tr := w.threadByRemote(a, "t1"); sliceHas(tr.FolderIDs, inbox.ID) {
		t.Fatalf("still in the inbox: %+v", tr)
	}
	last := g.modify[len(g.modify)-1]
	if fmt.Sprint(last["removeLabelIds"]) != "[INBOX]" || fmt.Sprint(last["ids"]) != "[m1 m2]" {
		t.Fatalf("archive at Gmail: %v", last)
	}
	// Star stars the newest message; read reads them all.
	w.act(a, []uuid.UUID{t1.ID}, ActionStar, nil)
	last = g.modify[len(g.modify)-1]
	if fmt.Sprint(last["addLabelIds"]) != "[STARRED]" || fmt.Sprint(last["ids"]) != "[m2]" || !w.threadByRemote(a, "t1").Starred {
		t.Fatalf("star: %v", last)
	}
	w.act(a, []uuid.UUID{t1.ID}, ActionRead, nil)
	if w.threadByRemote(a, "t1").Unread {
		t.Fatal("still unread")
	}
	// Labels; move to a label also leaves the inbox.
	label := w.folderByRemote(a, "Label_1")
	w.act(a, []uuid.UUID{t1.ID}, ActionInbox, nil)
	w.act(a, []uuid.UUID{t1.ID}, ActionMove, label)
	last = g.modify[len(g.modify)-1]
	if fmt.Sprint(last["addLabelIds"]) != "[Label_1]" || fmt.Sprint(last["removeLabelIds"]) != "[INBOX]" {
		t.Fatalf("move to label: %v", last)
	}
	if tr := w.threadByRemote(a, "t1"); !sliceHas(tr.FolderIDs, label.ID) || sliceHas(tr.FolderIDs, inbox.ID) {
		t.Fatalf("after move: %+v", tr)
	}
	w.act(a, []uuid.UUID{t1.ID}, ActionTrash, nil)
	if fmt.Sprint(g.trashed) != "[t1]" {
		t.Fatalf("trash: %v", g.trashed)
	}
	if !sliceHas(w.threadByRemote(a, "t1").FolderIDs, w.folderByRemote(a, "TRASH").ID) {
		t.Fatal("not in trash")
	}
	// Each action queues a sync.
	if n := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.sync_account' AND args->>'account_id' = $1`, a.ID.String()); n < 1 {
		t.Fatal("no sync after an action")
	}

	// Send in the thread, then store it; the next sync does not duplicate it.
	prov, err := w.s.provider(w.ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	raw := testMail{from: a.Email, to: "frank@example.com", subject: "Re: Hi", msgID: "out1@acme.test", inReplyTo: "h2@example.com", body: "Done"}.raw()
	ref, err := prov.Send(w.ctx, raw, SendInfo{From: a.Email, Recipients: []string{"frank@example.com"}, ThreadRemoteID: "t1"})
	if err != nil || ref.RemoteID != "sent-1" || len(g.sent) != 1 || !strings.Contains(g.sent[0], `"threadId":"t1"`) {
		t.Fatalf("send: %v %+v", err, ref)
	}
	id, err := w.s.storeSent(w.ctx, a, raw, ref, w.agent.UserID, &t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := w.messageByRemote(a, "sent-1")
	if m.ID != id || m.ThreadID != t1.ID || m.SentBy == nil || *m.SentBy != w.agent.UserID || m.Unread ||
		!sliceHas(m.FolderIDs, w.folderByRemote(a, "SENT").ID) {
		t.Fatalf("stored sent: %+v", m)
	}
	g.add("sent-1", "t1", []string{"SENT"}, testMail{from: a.Email, to: "frank@example.com", subject: "Re: Hi", msgID: "out1@acme.test", body: "Done"})
	g.record("messagesAdded", "sent-1")
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND message_id = 'out1@acme.test'`, a.ID); n != 1 {
		t.Fatalf("%d copies of the sent message", n)
	}
}

func TestRefusedGrantNeedsReconnect(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	tokens := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt" {
			t.Errorf("token request: %v", r.Form)
		}
		rw.WriteHeader(400)
		_, _ = rw.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
	}))
	defer tokens.Close()
	w.s.oauth[ProviderGoogle].tokenURL = tokens.URL
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	sealed, _ := w.s.sealCreds(credentials{RefreshToken: "rt", AccessToken: "old", Expiry: time.Now().Add(-time.Minute)})
	mustExec(t, w.d, `UPDATE collab_accounts SET credentials_enc = $2 WHERE id = $1`, a.ID, sealed)

	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	a = w.reload(a)
	if a.Status != "error" || !a.Reconnect || a.LastError != errReconnect.Error() || a.NextSyncAt == nil || a.SyncFailures != 1 {
		t.Fatalf("after a refused grant: %+v", a)
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.accounts'`, w.wid); n < 1 {
		t.Fatal("no collab.accounts on the status change")
	}
	// It is not synced again until reconnected.
	mustExec(t, w.d, `UPDATE collab_accounts SET next_sync_at = now() - interval '1 hour' WHERE id = $1`, a.ID)
	before := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.sync_account' AND args->>'account_id' = $1`, a.ID.String())
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if after := w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.sync_account' AND args->>'account_id' = $1`, a.ID.String()); after != before {
		t.Fatal("an account waiting for a reconnect was queued")
	}

	// A provider failure is stored safely: status and code, never the provider's message.
	b := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
	g.status = 400
	if err := w.s.syncAccount(w.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	b = w.reload(b)
	if b.Status != "error" || b.Reconnect || strings.Contains(b.LastError, "example.com") || !strings.Contains(b.LastError, "400") {
		t.Fatalf("provider failure: %+v", b)
	}
	// Backoff doubles to an hour.
	if syncBackoff(1) != time.Minute || syncBackoff(3) != 4*time.Minute || syncBackoff(20) != time.Hour {
		t.Fatal("backoff")
	}
	// 401 twice is a refused grant too.
	g.status = 401
	c := w.account(w.owner, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if c = w.reload(c); !c.Reconnect {
		t.Fatalf("401: %+v", c)
	}
}

// TestGmailBusy: a rate limit is waited out within the pass, and a provider that stays busy
// shows on the account only after a few passes in a row. Gmail's tab is kept with the mail.
func TestGmailBusy(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX", "CATEGORY_PROMOTIONS"}, testMail{from: "Shop <" + at("deals", "shop.example") + ">", to: a.Email,
		subject: "Sale", msgID: at("sale-1", "shop.example"), body: "Half off"})
	g.busy = len(w.s.busyWaits)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a = w.reload(a); a.Status != "active" || a.SyncFailures != 0 {
		t.Fatalf("after waiting out a rate limit: %s %d %q", a.Status, a.SyncFailures, a.LastError)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id = 'm1' AND $2 = ANY(signals)`, a.ID, signalGmailPromotions); n != 1 {
		t.Fatal("Gmail's tab was not kept")
	}

	g.status = http.StatusServiceUnavailable
	for i := 1; i < quietFailures; i++ {
		if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		if a = w.reload(a); a.Status != "active" || a.LastError != "" || a.SyncFailures != i || a.NextSyncAt == nil {
			t.Fatalf("busy pass %d: %s %d %q", i, a.Status, a.SyncFailures, a.LastError)
		}
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a = w.reload(a); a.Status != "error" || a.Reconnect || !strings.Contains(a.LastError, "busy") {
		t.Fatalf("still busy: %s %q", a.Status, a.LastError)
	}
	g.status = 0
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a = w.reload(a); a.Status != "active" || a.SyncFailures != 0 || a.LastError != "" {
		t.Fatalf("back: %s %d %q", a.Status, a.SyncFailures, a.LastError)
	}
}

func TestSyncDue(t *testing.T) {
	w := newWorld(t)
	idle := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	active := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
	fresh := w.account(w.owner, ProviderGoogle, VisibilityPrivate, Audience{})
	backoff := w.account(w.owner, ProviderMicrosoft, VisibilityPrivate, Audience{})
	mustExec(t, w.d, `UPDATE collab_accounts SET last_synced_at = now() - interval '5 minutes' WHERE id = ANY($1)`, []uuid.UUID{idle.ID, active.ID})
	mustExec(t, w.d, `UPDATE collab_accounts SET last_synced_at = now() - interval '20 seconds' WHERE id = $1`, fresh.ID)
	mustExec(t, w.d, `UPDATE collab_accounts SET next_sync_at = now() + interval '5 minutes' WHERE id = $1`, backoff.ID)
	mustExec(t, w.d, `UPDATE users SET last_seen_at = now() - interval '2 hours' WHERE id = $1`, w.agent.UserID)
	mustExec(t, w.d, `UPDATE users SET last_seen_at = now() WHERE id = $1`, w.agent2.UserID)
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	queued := func(a *accountRow) bool {
		return w.count(`SELECT count(*) FROM river_job WHERE kind = 'collab.sync_account' AND args->>'account_id' = $1`, a.ID.String()) > 0
	}
	if queued(idle) || !queued(active) || queued(fresh) || queued(backoff) {
		t.Fatalf("due: idle %v active %v fresh %v backoff %v", queued(idle), queued(active), queued(fresh), queued(backoff))
	}
	// A suspended workspace's accounts wait.
	mustExec(t, w.d, `UPDATE workspaces SET suspended_at = now() WHERE id = $1`, w.wid)
	mustExec(t, w.d, `UPDATE collab_accounts SET last_synced_at = NULL WHERE id = $1`, idle.ID)
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if queued(idle) {
		t.Fatal("a suspended workspace's account was queued")
	}
}

func TestCleanupLeavers(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	private := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	shared := w.account(w.agent, ProviderMicrosoft, VisibilityShared, Audience{Everyone: true})
	kept := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX"}, testMail{from: "news@example.com", to: private.Email, subject: "Logo", msgID: "l1@example.com", body: "x", image: true})
	if err := w.s.syncAccount(w.ctx, private.ID); err != nil {
		t.Fatal(err)
	}
	key := w.messageByRemote(private, "m1").RawKey
	if key == "" {
		t.Fatal("no raw kept")
	}
	mustExec(t, w.d, `DELETE FROM members WHERE workspace_id = $1 AND user_id = $2`, w.wid, w.agent.UserID)
	if err := w.s.cleanup(w.ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_accounts WHERE id = $1`, private.ID); n != 0 {
		t.Fatal("a leaver's private account is kept")
	}
	if n := w.count(`SELECT count(*) FROM collab_accounts WHERE id = $1 AND owner_id IS NULL`, shared.ID); n != 1 {
		t.Fatal("a leaver's shared account is not kept without an owner")
	}
	if n := w.count(`SELECT count(*) FROM collab_accounts WHERE id = $1 AND owner_id IS NOT NULL`, kept.ID); n != 1 {
		t.Fatal("a member's account changed")
	}
	if _, err := w.d.Storage.Get(w.ctx, key); err == nil {
		t.Fatal("the deleted account's raw message is still in storage")
	}
}

func TestAttachmentLinks(t *testing.T) {
	w := newWorld(t)
	id := uuid.New()
	u, err := url.Parse(w.s.attachmentURL(id, 2))
	if err != nil || !strings.HasPrefix(u.String(), "http://api.test/v1/collab/attachments/"+id.String()+"/2?") {
		t.Fatalf("url: %v %s", err, u)
	}
	q := u.Query()
	if !w.s.verifyAttachmentSig(id, 2, q.Get("exp"), q.Get("sig")) || w.s.verifyAttachmentSig(id, 3, q.Get("exp"), q.Get("sig")) ||
		w.s.verifyAttachmentSig(id, 2, "1", q.Get("sig")) {
		t.Fatal("signature check")
	}
	old := `<img src="http://api.test/v1/collab/attachments/` + id.String() + `/0?exp=1&amp;sig=abcd">`
	fresh := w.s.resignAttachmentLinks(id, old)
	if fresh == old || !strings.Contains(fresh, "http://api.test/v1/collab/attachments/"+id.String()+"/0?exp=") || !strings.Contains(fresh, "&amp;sig=") {
		t.Fatalf("resign: %s", fresh)
	}
	// A link to another message's file is left as it was stored.
	if other := w.s.resignAttachmentLinks(uuid.New(), old); other != old {
		t.Fatalf("resigned another message's link: %s", other)
	}
}

func TestParticipantsAndFolders(t *testing.T) {
	own := "me@acme.test"
	people := []Address{{Email: "a@x.test"}, {Email: own}, {Email: "A@x.test"}}
	for i := 0; i < 12; i++ {
		people = append(people, Address{Email: fmt.Sprintf("p%d@x.test", i)})
	}
	got := participantList(people, []string{own})
	if len(got) != 10 || got[9].Email != own || got[0].Email != "a@x.test" {
		t.Fatalf("participants: %+v", got)
	}
	list := []RemoteFolder{{Path: "Zeta"}, {Path: "Trash", Role: RoleTrash}, {Path: "Inbox", Role: RoleInbox}, {Path: "alpha"}}
	sortFolders(list)
	if list[0].Role != RoleInbox || list[1].Role != RoleTrash || list[2].Path != "alpha" {
		t.Fatalf("order: %+v", list)
	}
}
