package collab

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeGraph is a small Microsoft Graph mail API with per-folder delta.
type fakeGraph struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	folders []map[string]any
	msgs    map[string]*graphFakeMsg
	// pending are the delta changes of each folder since its last delta link.
	pending map[string][]map[string]any
	moves   []string // "id->folder"
	patches []string // "id:json"
	drafts  []string // MIME of created drafts
	sentIDs []string
	nextID  int
	// settings is the profile name and automatic replies (settings_test.go).
	settings fakeGraphSettings
	// cats are the master categories, message categories and deleted drafts (categories_test.go).
	cats fakeGraphCats
}

type graphFakeMsg struct {
	id, conv, folder string
	read             bool
	raw              []byte
	date             time.Time
}

var graphWell = map[string]string{"inbox": "f-inbox", "sentitems": "f-sent", "drafts": "f-drafts", "deleteditems": "f-trash",
	"junkemail": "f-junk", "archive": "f-archive"}

func newFakeGraph(t *testing.T) *fakeGraph {
	g := &fakeGraph{t: t, msgs: map[string]*graphFakeMsg{}, pending: map[string][]map[string]any{}, folders: []map[string]any{
		{"id": "f-inbox", "displayName": "Inbox", "childFolderCount": 1}, {"id": "f-sent", "displayName": "Sent Items"},
		{"id": "f-drafts", "displayName": "Drafts"}, {"id": "f-trash", "displayName": "Deleted Items"},
		{"id": "f-junk", "displayName": "Junk Email"}, {"id": "f-archive", "displayName": "Archive"},
	}}
	g.srv = httptest.NewServer(g)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGraph) item(m *graphFakeMsg) map[string]any {
	return map[string]any{"id": m.id, "conversationId": m.conv, "isRead": m.read, "isDraft": false, "parentFolderId": m.folder,
		"receivedDateTime": m.date.UTC().Format(time.RFC3339), "flag": map[string]string{"flagStatus": "notFlagged"}}
}

func (g *fakeGraph) add(id, conv, folder string, read bool, m testMail) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m.date.IsZero() {
		m.date = time.Now().Add(-time.Duration(100-len(g.msgs)) * time.Minute)
	}
	msg := &graphFakeMsg{id: id, conv: conv, folder: folder, read: read, raw: m.raw(), date: m.date}
	g.msgs[id] = msg
	g.pending[folder] = append(g.pending[folder], g.item(msg))
}

// move moves a message at the provider (as a phone would).
func (g *fakeGraph) move(id, folder string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.msgs[id]
	g.pending[m.folder] = append(g.pending[m.folder], map[string]any{"id": id, "@removed": map[string]string{"reason": "changed"}})
	m.folder = folder
	g.pending[folder] = append(g.pending[folder], g.item(m))
}

func (g *fakeGraph) remove(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.msgs[id]
	g.pending[m.folder] = append(g.pending[m.folder], map[string]any{"id": id, "@removed": map[string]string{"reason": "deleted"}})
	delete(g.msgs, id)
}

func (g *fakeGraph) markRead(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.msgs[id]
	m.read = true
	g.pending[m.folder] = append(g.pending[m.folder], g.item(m))
}

func (g *fakeGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !strings.Contains(r.Header.Get("Prefer"), `IdType="ImmutableId"`) {
		g.t.Errorf("no immutable ids on %s %s", r.Method, r.URL.Path)
	}
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	p := r.URL.Path
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/me" || strings.HasPrefix(p, "/me/mailboxSettings"):
		g.settings.serve(g.t, w, r, p)
	case strings.HasPrefix(p, "/me/outlook/") || (len(parts) == 3 && parts[1] == "messages" && (r.Method == "GET" || r.Method == "DELETE")):
		g.serveCats(w, r, parts)
	case r.Method == "GET" && p == "/me/mailFolders":
		reply(map[string]any{"value": g.folders})
	case r.Method == "GET" && p == "/me/mailFolders/f-inbox/childFolders":
		reply(map[string]any{"value": []map[string]any{{"id": "f-proj", "displayName": "Projects", "parentFolderId": "f-inbox"}}})
	case r.Method == "GET" && len(parts) == 3 && parts[1] == "mailFolders" && graphWell[parts[2]] != "":
		reply(map[string]any{"id": graphWell[parts[2]]})
	case r.Method == "GET" && (strings.HasSuffix(p, "/messages/delta") || strings.HasPrefix(p, "/delta/") || strings.HasPrefix(p, "/next/")):
		folder := parts[len(parts)-1]
		if strings.HasSuffix(p, "/messages/delta") {
			folder = parts[2]
			if !strings.Contains(r.URL.Query().Get("$filter"), "receivedDateTime ge ") {
				g.t.Errorf("first delta without the window: %s", r.URL.RawQuery)
			}
		}
		list := g.pending[folder]
		if len(list) > 2 {
			// Two per page.
			g.pending[folder] = list[2:]
			reply(map[string]any{"value": list[:2], "@odata.nextLink": g.srv.URL + "/next/" + folder})
			return
		}
		g.pending[folder] = nil
		reply(map[string]any{"value": list, "@odata.deltaLink": g.srv.URL + "/delta/" + folder})
	case r.Method == "GET" && len(parts) == 4 && parts[1] == "messages" && parts[3] == "$value":
		m := g.msgs[parts[2]]
		if m == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"ErrorItemNotFound","message":"x"}}`))
			return
		}
		_, _ = w.Write(m.raw)
	case r.Method == "PATCH" && len(parts) == 3 && parts[1] == "messages":
		b, _ := io.ReadAll(r.Body)
		g.patches = append(g.patches, parts[2]+":"+string(b))
		reply(map[string]any{"id": parts[2]})
	case r.Method == "POST" && len(parts) == 4 && parts[1] == "messages" && parts[3] == "move":
		var in struct {
			DestinationID string `json:"destinationId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		g.moves = append(g.moves, parts[2]+"->"+in.DestinationID)
		if m := g.msgs[parts[2]]; m != nil {
			m.folder = in.DestinationID
		}
		reply(map[string]any{"id": parts[2]})
	case r.Method == "POST" && p == "/me/messages":
		b, _ := io.ReadAll(r.Body)
		raw, err := base64.StdEncoding.DecodeString(string(b))
		if err != nil || r.Header.Get("Content-Type") != "text/plain" {
			g.t.Errorf("draft body: %v %s", err, r.Header.Get("Content-Type"))
		}
		g.drafts = append(g.drafts, string(raw))
		g.nextID++
		reply(map[string]any{"id": "d" + strconv.Itoa(g.nextID), "conversationId": "c1"})
	case r.Method == "POST" && len(parts) == 4 && parts[1] == "messages" && parts[3] == "send":
		g.sentIDs = append(g.sentIDs, parts[2])
		w.WriteHeader(202)
	default:
		g.t.Errorf("unexpected Graph call %s %s", r.Method, r.URL.String())
		w.WriteHeader(400)
	}
}

func TestGraphSync(t *testing.T) {
	w := newWorld(t)
	g := newFakeGraph(t)
	w.s.graphBase = g.srv.URL
	a := w.account(w.agent, ProviderMicrosoft, VisibilityShared, Audience{Everyone: true})
	g.add("g1", "c1", "f-inbox", false, testMail{from: "Frank <frank@example.com>", to: a.Email, subject: "Order", msgID: "o1@example.com", body: "Where is it?"})
	g.add("g2", "c1", "f-sent", true, testMail{from: a.Email, to: "frank@example.com", subject: "Re: Order", msgID: "o2@acme.test",
		inReplyTo: "o1@example.com", body: "On its way"})
	g.add("g3", "c2", "f-proj", false, testMail{from: "pm@example.com", to: a.Email, subject: "Plan", msgID: "p1@example.com", body: "The plan"})

	for i := 0; i < 3; i++ {
		g.add("n"+strconv.Itoa(i), "c0", "f-inbox", true, testMail{from: "news@example.com", to: a.Email, subject: "News",
			msgID: "n" + strconv.Itoa(i) + "@example.com", body: "News"})
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	a = w.reload(a)
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id LIKE 'n%'`, a.ID); n != 3 {
		t.Fatalf("paged delta: %d of 3", n)
	}
	if a.Status != "active" {
		t.Fatalf("status %s %s", a.Status, a.LastError)
	}
	proj := w.folderByRemote(a, "f-proj")
	if proj.Path != "Inbox/Projects" || proj.Name != "Projects" || proj.Role != nil {
		t.Fatalf("child folder: %+v", proj)
	}
	inbox, archive, trash := w.folderByRemote(a, "f-inbox"), w.folderByRemote(a, "f-archive"), w.folderByRemote(a, "f-trash")
	sent := w.folderByRemote(a, "f-sent")
	if *inbox.Role != RoleInbox || *archive.Role != RoleArchive || *trash.Role != RoleTrash || *w.folderByRemote(a, "f-junk").Role != RoleSpam {
		t.Fatal("well-known roles")
	}
	c1 := w.threadByRemote(a, "c1")
	if c1.MessageCount != 2 || !c1.Unread || !sliceHas(c1.FolderIDs, inbox.ID) || !sliceHas(c1.FolderIDs, sent.ID) {
		t.Fatalf("c1: %+v", c1)
	}
	// Every member sees a shared account's changes.
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.changed' AND $2 = ANY(user_ids)`, w.wid, w.viewer.UserID); n < 1 {
		t.Fatal("the audience got no collab.changed")
	}

	// Changes at Microsoft: read, moved to Archive (same id), deleted.
	g.markRead("g1")
	g.move("g1", "f-archive")
	g.remove("g3")
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	m1 := w.messageByRemote(a, "g1")
	if m1.Unread || len(m1.FolderIDs) != 1 || m1.FolderIDs[0] != archive.ID {
		t.Fatalf("g1 after delta: %+v", m1)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id = 'g3'`, a.ID); n != 0 {
		t.Fatal("a deleted message is kept")
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE account_id = $1 AND remote_id = 'c2'`, a.ID); n != 0 {
		t.Fatal("an empty conversation is kept")
	}

	// Actions: inbox moves the archived message back, the sent one stays in Sent.
	w.act(a, []uuid.UUID{c1.ID}, ActionInbox, nil)
	if strings.Join(g.moves, ",") != "g1->f-inbox" {
		t.Fatalf("moves: %v", g.moves)
	}
	if m := w.messageByRemote(a, "g2"); len(m.FolderIDs) != 1 || m.FolderIDs[0] != sent.ID {
		t.Fatalf("the sent copy moved: %+v", m)
	}
	w.act(a, []uuid.UUID{c1.ID}, ActionUnread, nil)
	if len(g.patches) != 1 || !strings.HasPrefix(g.patches[0], "g2:") || !strings.Contains(g.patches[0], `"isRead":false`) {
		t.Fatalf("unread marks the newest: %v", g.patches)
	}
	w.act(a, []uuid.UUID{c1.ID}, ActionStar, nil)
	if !strings.Contains(g.patches[len(g.patches)-1], `"flagStatus":"flagged"`) {
		t.Fatalf("star: %v", g.patches)
	}
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return w.s.applyLocal(w.ctx, tx, a, []uuid.UUID{c1.ID}, ActionLabel, proj) })
	if err != errLabels {
		t.Fatalf("label on Microsoft: %v", err)
	}

	// Send: a draft from the MIME, then sent; the copy keeps the draft's id in Sent Items.
	prov, err := w.s.provider(w.ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	raw := testMail{from: a.Email, to: "frank@example.com", subject: "Re: Order", msgID: "o3@acme.test", inReplyTo: "o2@acme.test", body: "Shipped"}.raw()
	ref, err := prov.Send(w.ctx, raw, SendInfo{From: a.Email, Recipients: []string{"frank@example.com"}, ThreadRemoteID: "c1"})
	if err != nil || ref.RemoteID != "d1" || len(g.sentIDs) != 1 || g.sentIDs[0] != "d1" || len(ref.FolderRemoteIDs) != 1 || ref.FolderRemoteIDs[0] != "f-sent" {
		t.Fatalf("send: %v %+v %v", err, ref, g.sentIDs)
	}
	if !strings.Contains(g.drafts[0], "Message-ID: <o3@acme.test>") {
		t.Fatal("the draft is not the MIME message")
	}
	if _, err := w.s.storeSent(w.ctx, a, raw, ref, w.agent.UserID, &c1.ID); err != nil {
		t.Fatal(err)
	}
	if tr := w.threadByRemote(a, "c1"); tr.MessageCount != 3 || tr.Snippet != "Shipped" {
		t.Fatalf("after send: %+v", tr)
	}
}
