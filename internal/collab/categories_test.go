package collab

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Outlook categories as labels and label colours (contracts/collab.md "Labels and colours").

// fakeGraphCats is fakeGraph's master categories, message categories and deleted messages.
type fakeGraphCats struct {
	list      []map[string]string // id, displayName, color
	onMsg     map[string][]string // message id → categories
	forbidden bool                // the account lacks MailboxSettings
	created   []string            // "name/preset"
	patched   []string            // "id/preset"
	deleted   []string            // message ids deleted
	next      int
}

func (g *fakeGraph) serveCats(w http.ResponseWriter, r *http.Request, parts []string) {
	c := &g.cats
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	if parts[1] == "messages" {
		id := parts[2]
		if r.Method == "DELETE" {
			c.deleted = append(c.deleted, id)
			delete(g.msgs, id)
			w.WriteHeader(204)
			return
		}
		if r.URL.Query().Get("$select") != "categories" {
			g.t.Errorf("message read: %s", r.URL.RawQuery)
		}
		cats := c.onMsg[id]
		if cats == nil {
			cats = []string{}
		}
		reply(map[string]any{"id": id, "categories": cats})
		return
	}
	if c.forbidden {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"ErrorAccessDenied","message":"Access is denied."}}`))
		return
	}
	var in map[string]string
	if r.Method == "POST" || r.Method == "PATCH" {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	switch {
	case r.Method == "GET" && len(parts) == 3:
		reply(map[string]any{"value": c.list})
	case r.Method == "POST" && len(parts) == 3:
		c.next++
		cat := map[string]string{"id": "cat-new" + strconv.Itoa(c.next), "displayName": in["displayName"], "color": in["color"]}
		c.list = append(c.list, cat)
		c.created = append(c.created, in["displayName"]+"/"+in["color"])
		reply(cat)
	case r.Method == "PATCH" && len(parts) == 4:
		for _, cat := range c.list {
			if cat["id"] == parts[3] {
				cat["color"] = in["color"]
				c.patched = append(c.patched, parts[3]+"/"+in["color"])
				reply(cat)
				return
			}
		}
		w.WriteHeader(404)
	case r.Method == "DELETE" && len(parts) == 4:
		c.list = slices.DeleteFunc(c.list, func(cat map[string]string) bool { return cat["id"] == parts[3] })
		w.WriteHeader(204)
	default:
		g.t.Errorf("unexpected Graph categories call %s %s", r.Method, r.URL.String())
		w.WriteHeader(400)
	}
}

// addWithCategories adds a message whose delta item carries categories.
func (g *fakeGraph) addWithCategories(id, conv, folder string, m testMail, cats ...string) {
	g.add(id, conv, folder, true, m)
	g.mu.Lock()
	defer g.mu.Unlock()
	list := g.pending[folder]
	list[len(list)-1]["categories"] = cats
	if g.cats.onMsg == nil {
		g.cats.onMsg = map[string][]string{}
	}
	g.cats.onMsg[id] = cats
}

func TestOutlookCategories(t *testing.T) {
	w := newWorld(t)
	g := newFakeGraph(t)
	w.s.graphBase = g.srv.URL
	g.cats.list = []map[string]string{{"id": "cat1", "displayName": "Red team", "color": "preset0"}}
	a := w.account(w.agent, ProviderMicrosoft, VisibilityPrivate, Audience{})
	g.addWithCategories("g1", "c1", "f-inbox", testMail{from: "frank@example.com", to: a.Email, subject: "Order", msgID: "o1@example.com", body: "x"},
		"Red team", "Loose")
	g.add("g2", "c2", "f-inbox", true, testMail{from: "gina@example.com", to: a.Email, subject: "Plan", msgID: "p1@example.com", body: "y"})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a = w.reload(a); a.Status != "active" {
		t.Fatalf("status %s %s", a.Status, a.LastError)
	}
	red, loose := w.folderByRemote(a, "category:Red team"), w.folderByRemote(a, "category:Loose")
	if red.Kind != KindLabel || red.Name != "Red team" || red.Color == nil || *red.Color != "#e74856" || loose.Kind != KindLabel || loose.Color != nil {
		t.Fatalf("labels: %+v %+v", red, loose)
	}
	inbox, archive := w.folderByRemote(a, "f-inbox"), w.folderByRemote(a, "f-archive")
	g1 := w.messageByRemote(a, "g1")
	if len(g1.FolderIDs) != 3 || !sliceHas(g1.FolderIDs, inbox.ID) || !sliceHas(g1.FolderIDs, red.ID) || !sliceHas(g1.FolderIDs, loose.ID) {
		t.Fatalf("g1 folders: %v", g1.FolderIDs)
	}
	c1, c2 := w.threadByRemote(a, "c1"), w.threadByRemote(a, "c2")

	// Label and unlabel PATCH the message's categories.
	w.act(a, []uuid.UUID{c2.ID}, ActionLabel, red)
	if last := g.patches[len(g.patches)-1]; last != `g2:{"categories":["Red team"]}` {
		t.Fatalf("label: %s", last)
	}
	if !sliceHas(w.threadByRemote(a, "c2").FolderIDs, red.ID) {
		t.Fatal("not labelled here")
	}
	w.act(a, []uuid.UUID{c1.ID}, ActionUnlabel, loose)
	if last := g.patches[len(g.patches)-1]; last != `g1:{"categories":["Red team"]}` {
		t.Fatalf("unlabel: %s", last)
	}
	// Moving keeps the labels; a category is not a folder to move to.
	w.act(a, []uuid.UUID{c1.ID}, ActionArchive, nil)
	if m := w.messageByRemote(a, "g1"); !sliceHas(m.FolderIDs, archive.ID) || sliceHas(m.FolderIDs, inbox.ID) || !sliceHas(m.FolderIDs, red.ID) {
		t.Fatalf("after archive: %v", m.FolderIDs)
	}
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return w.s.applyLocal(w.ctx, tx, a, []uuid.UUID{c1.ID}, ActionMove, red) }); err != errMoveToCategory {
		t.Fatalf("move to a category: %v", err)
	}
	// A message only in a category is not an orphan.
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		p, err := w.s.newPass(w.ctx, tx, a)
		if err != nil {
			return err
		}
		return p.removeOrphans(w.ctx, tx)
	})
	if err != nil || w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID) != 2 {
		t.Fatalf("orphans: %v", err)
	}

	srv := w.api(w.agent)
	// Creating a label makes a master category with its colour.
	var f MailFolder
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/folders", map[string]any{"name": "Billing", "kind": "label", "color": "green"}, &f); st != 200 ||
		f.Kind != KindLabel || f.Color == nil || *f.Color != "#13a10e" || len(g.cats.created) != 1 || g.cats.created[0] != "Billing/preset4" {
		t.Fatalf("create: %d %+v %v", st, f, g.cats.created)
	}
	// Colour: the preset at Microsoft, then stored; a category messages carry joins the list.
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+red.ID.String(), map[string]any{"color": "blue"}, &f); st != 200 || f.Color == nil || *f.Color != "#0078d4" ||
		g.cats.patched[0] != "cat1/preset7" {
		t.Fatalf("colour: %d %+v %v", st, f, g.cats.patched)
	}
	var e apiError
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+red.ID.String(), map[string]any{"name": "Blue team"}, &e); st != 422 || e.Code != "rename_unsupported" {
		t.Fatalf("rename a category: %d %+v", st, e)
	}
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+inbox.ID.String(), map[string]any{"color": "blue"}, &e); st != 422 {
		t.Fatalf("colour a special folder: %d", st)
	}
	// The next refresh keeps what Microsoft has.
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if red = w.folderByRemote(a, "category:Red team"); *red.Color != "#0078d4" {
		t.Fatalf("after refresh: %+v", red)
	}
	// Without MailboxSettings: categories seen on messages stay, and the account is fine.
	g.cats.forbidden = true
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a = w.reload(a); a.Status != "active" || a.Reconnect {
		t.Fatalf("without the master list: %+v", a)
	}
	if n := w.count(`SELECT count(*) FROM collab_folders WHERE account_id = $1 AND kind = 'label'`, a.ID); n < 2 {
		t.Fatalf("%d labels kept", n)
	}
}

func TestGmailLabelColor(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	label := w.folderByRemote(a, "Label_1")
	srv := w.api(w.agent)
	var f MailFolder
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+label.ID.String(), map[string]any{"color": "violet"}, &f); st != 200 || f.Color == nil ||
		*f.Color != "#a479e2" || f.Name != "Acme" {
		t.Fatalf("colour: %d %+v", st, f)
	}
	if strings.Join(g.drafts.colors, ",") != "Label_1:#a479e2/#ffffff" {
		t.Fatalf("at Gmail: %v", g.drafts.colors)
	}
	// It comes back with the next folder refresh.
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if l := w.folderByRemote(a, "Label_1"); *l.Color != "#a479e2" {
		t.Fatalf("after refresh: %+v", l)
	}
	var e apiError
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+label.ID.String(), map[string]any{"color": "magenta"}, &e); st != 422 {
		t.Fatalf("a colour outside the palette: %d", st)
	}
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+label.ID.String(), map[string]any{}, &e); st != 422 {
		t.Fatalf("nothing to change: %d", st)
	}
	imapAcc := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	mustExec(t, w.d, `INSERT INTO collab_folders (id, workspace_id, account_id, remote_id, name, path, kind) VALUES ($1, $2, $3, 'Clients', 'Clients', 'Clients', 'folder')`,
		uuid.New(), w.wid, imapAcc.ID)
	clients := w.folderByRemote(imapAcc, "Clients")
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+clients.ID.String(), map[string]any{"color": "red"}, &e); st != 422 {
		t.Fatalf("an IMAP folder's colour: %d", st)
	}
}
