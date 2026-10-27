package wiki_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/wiki"
)

type ref struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon"`
}

type page struct {
	ID        string  `json:"id"`
	ParentID  *string `json:"parent_id"`
	Title     string  `json:"title"`
	Icon      string  `json:"icon"`
	BodyHTML  string  `json:"body_html"`
	Position  int     `json:"position"`
	Version   int     `json:"version"`
	CreatedBy *struct {
		ID string `json:"id"`
	} `json:"created_by"`
	UpdatedBy *struct {
		ID string `json:"id"`
	} `json:"updated_by"`
	Breadcrumbs []ref `json:"breadcrumbs"`
	Children    []ref `json:"children"`
}

type node struct {
	ID          string  `json:"id"`
	ParentID    *string `json:"parent_id"`
	Title       string  `json:"title"`
	Position    int     `json:"position"`
	CreatedByID *string `json:"created_by_id"`
}

func create(t *testing.T, e *hdtest.Env, a hdtest.Auth, body map[string]any) page {
	t.Helper()
	var out page
	e.Do("POST", "/v1/wiki/pages", a, body).Want(t, 201).JSON(t, &out)
	return out
}

func tree(t *testing.T, e *hdtest.Env, a hdtest.Auth) []node {
	t.Helper()
	var out struct{ Data []node }
	e.Do("GET", "/v1/wiki/pages", a, nil).Want(t, 200).JSON(t, &out)
	return out.Data
}

// children lists the titles under parent ("" for the top level), in order, and flags a
// gap in the positions.
func children(nodes []node, parent string) []string {
	var out []node
	for _, n := range nodes {
		if (n.ParentID == nil && parent == "") || (n.ParentID != nil && *n.ParentID == parent) {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b node) int { return a.Position - b.Position })
	titles := []string{}
	for i, n := range out {
		if n.Position != i {
			return append(titles, fmt.Sprintf("bad position %d at %d", n.Position, i))
		}
		titles = append(titles, n.Title)
	}
	return titles
}

func TestPagesAndAccess(t *testing.T) {
	e := hdtest.New(t, wiki.Register)
	ws := e.NewWorkspace()
	agent, viewer := ws.Agent.As(ws), ws.Viewer.As(ws)
	_, seq := e.Events(ws.ID, 0)

	root := create(t, e, agent, map[string]any{"title": "  Support   handbook ", "icon": "📘",
		"body_html": `<p onclick="x()">Read <strong>this</strong> first.</p><script>alert(1)</script>`})
	if root.Title != "Support handbook" || root.Icon != "📘" || root.Version != 1 || root.ParentID != nil {
		t.Fatalf("created %+v", root)
	}
	if strings.Contains(root.BodyHTML, "script") || strings.Contains(root.BodyHTML, "onclick") || !strings.Contains(root.BodyHTML, "<strong>this</strong>") {
		t.Fatalf("body not sanitized: %s", root.BodyHTML)
	}
	if root.CreatedBy == nil || root.CreatedBy.ID != ws.Agent.ID.String() {
		t.Fatal("creator not set")
	}
	child := create(t, e, agent, map[string]any{"title": "Refunds", "parent_id": root.ID})
	grand := create(t, e, agent, map[string]any{"title": "Over 100 euros", "parent_id": child.ID})
	if len(grand.Breadcrumbs) != 2 || grand.Breadcrumbs[0].ID != root.ID || grand.Breadcrumbs[1].ID != child.ID {
		t.Fatalf("breadcrumbs %+v", grand.Breadcrumbs)
	}
	var got page
	e.Do("GET", "/v1/wiki/pages/"+root.ID, viewer, nil).Want(t, 200).JSON(t, &got)
	if len(got.Children) != 1 || got.Children[0].ID != child.ID {
		t.Fatalf("children %+v", got.Children)
	}
	// An untitled page is fine; a word is not an icon; bodies have a size cap.
	create(t, e, agent, map[string]any{})
	e.Do("POST", "/v1/wiki/pages", agent, map[string]any{"title": "x", "icon": "abc"}).Want(t, 422)
	e.Do("POST", "/v1/wiki/pages", agent, map[string]any{"title": "x", "icon": "\u202e📘"}).Want(t, 422)
	e.Do("POST", "/v1/wiki/pages", agent, map[string]any{"title": strings.Repeat("a", 201)}).Want(t, 422)
	e.Do("POST", "/v1/wiki/pages", agent, map[string]any{"title": "x", "body_html": strings.Repeat("a", 513<<10)}).Want(t, 422)

	// Viewers read only; scopes bound API keys.
	if n := tree(t, e, viewer); len(n) != 4 {
		t.Fatalf("tree %d", len(n))
	}
	e.Do("POST", "/v1/wiki/pages", viewer, map[string]any{"title": "x"}).Want(t, 403)
	e.Do("PATCH", "/v1/wiki/pages/"+root.ID, viewer, map[string]any{"title": "x"}).Want(t, 403)
	e.Do("POST", "/v1/wiki/pages/"+root.ID+"/move", viewer, map[string]any{"parent_id": nil, "position": 0}).Want(t, 403)
	e.Do("DELETE", "/v1/wiki/pages/"+root.ID, viewer, nil).Want(t, 403)
	read := hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeWikiRead)}
	e.Do("GET", "/v1/wiki/pages/"+root.ID, read, nil).Want(t, 200)
	e.Do("GET", "/v1/wiki/search?q=refund", read, nil).Want(t, 200)
	e.Do("POST", "/v1/wiki/pages", read, map[string]any{"title": "x"}).Want(t, 403)
	kbOnly := hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeKBRead, auth.ScopeKBWrite)}
	e.Do("GET", "/v1/wiki/pages", kbOnly, nil).Want(t, 403)
	e.Do("GET", "/v1/wiki/pages/"+root.ID, kbOnly, nil).Want(t, 403)

	// Another workspace sees nothing, changes nothing and can't nest under it.
	other := e.NewWorkspace()
	oa := other.Owner.As(other)
	e.Do("GET", "/v1/wiki/pages/"+root.ID, oa, nil).Want(t, 404)
	e.Do("PATCH", "/v1/wiki/pages/"+root.ID, oa, map[string]any{"title": "x"}).Want(t, 404)
	e.Do("POST", "/v1/wiki/pages/"+root.ID+"/move", oa, map[string]any{"parent_id": nil, "position": 0}).Want(t, 404)
	e.Do("DELETE", "/v1/wiki/pages/"+root.ID, oa, nil).Want(t, 404)
	e.Do("GET", "/v1/wiki/pages/"+root.ID+"/revisions", oa, nil).Want(t, 404)
	e.Do("POST", "/v1/wiki/pages", oa, map[string]any{"title": "x", "parent_id": root.ID}).Want(t, 422)
	mine := create(t, e, oa, map[string]any{"title": "Mine"})
	e.Do("POST", "/v1/wiki/pages/"+mine.ID+"/move", oa, map[string]any{"parent_id": root.ID, "position": 0}).Want(t, 422)
	if n := tree(t, e, oa); len(n) != 1 {
		t.Fatalf("other tree %+v", n)
	}
	var hits struct{ Data []ref }
	e.Do("GET", "/v1/wiki/search?q=refunds", oa, nil).Want(t, 200).JSON(t, &hits)
	if len(hits.Data) != 0 {
		t.Fatalf("search leaked %+v", hits.Data)
	}

	types, _ := e.Events(ws.ID, seq)
	if !slices.Contains(types, "wiki_page.created") {
		t.Fatalf("events %v", types)
	}
	var audits int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'wiki_page.created'`, &audits, ws.ID)
	if audits != 4 {
		t.Fatalf("audit entries %d", audits)
	}
}

func TestMoveOrderAndCycles(t *testing.T) {
	e := hdtest.New(t, wiki.Register)
	ws := e.NewWorkspace()
	agent := ws.Agent.As(ws)

	a := create(t, e, agent, map[string]any{"title": "A"})
	b := create(t, e, agent, map[string]any{"title": "B"})
	c := create(t, e, agent, map[string]any{"title": "C", "position": 0})
	if got := children(tree(t, e, agent), ""); !slices.Equal(got, []string{"C", "A", "B"}) {
		t.Fatalf("top %v", got)
	}
	// Reorder, then nest.
	e.Do("POST", "/v1/wiki/pages/"+c.ID+"/move", agent, map[string]any{"parent_id": nil, "position": 2}).Want(t, 200)
	if got := children(tree(t, e, agent), ""); !slices.Equal(got, []string{"A", "B", "C"}) {
		t.Fatalf("reordered %v", got)
	}
	var moved page
	e.Do("POST", "/v1/wiki/pages/"+b.ID+"/move", agent, map[string]any{"parent_id": a.ID, "position": 99}).Want(t, 200).JSON(t, &moved)
	if moved.ParentID == nil || *moved.ParentID != a.ID || len(moved.Breadcrumbs) != 1 {
		t.Fatalf("moved %+v", moved)
	}
	nodes := tree(t, e, agent)
	if got := children(nodes, ""); !slices.Equal(got, []string{"A", "C"}) {
		t.Fatalf("top after nest %v", got)
	}
	if got := children(nodes, a.ID); !slices.Equal(got, []string{"B"}) {
		t.Fatalf("under A %v", got)
	}
	// Not into itself or below itself.
	e.Do("POST", "/v1/wiki/pages/"+a.ID+"/move", agent, map[string]any{"parent_id": a.ID, "position": 0}).Want(t, 422)
	r := e.Do("POST", "/v1/wiki/pages/"+a.ID+"/move", agent, map[string]any{"parent_id": b.ID, "position": 0})
	r.Want(t, 422)
	if !strings.Contains(string(r.Body), "page_cycle") {
		t.Fatalf("cycle answer %s", r.Body)
	}

	// Ten levels at most, counting the subpages that move along.
	level := []string{"", c.ID}
	for i := 2; i <= 10; i++ {
		level = append(level, create(t, e, agent, map[string]any{"title": fmt.Sprint("L", i), "parent_id": level[i-1]}).ID)
	}
	e.Do("POST", "/v1/wiki/pages", agent, map[string]any{"title": "L11", "parent_id": level[10]}).Want(t, 422)
	// A has B under it: under level 9 B would be the eleventh.
	e.Do("POST", "/v1/wiki/pages/"+a.ID+"/move", agent, map[string]any{"parent_id": level[9], "position": 0}).Want(t, 422)
	e.Do("POST", "/v1/wiki/pages/"+b.ID+"/move", agent, map[string]any{"parent_id": level[9], "position": 0}).Want(t, 200)
}

func TestStaleUpdate(t *testing.T) {
	e := hdtest.New(t, wiki.Register)
	ws := e.NewWorkspace()
	agent, admin := ws.Agent.As(ws), ws.Admin.As(ws)

	p := create(t, e, agent, map[string]any{"title": "Escalation", "body_html": "<p>Call the lead.</p>"})
	var mine, theirs page
	e.Do("PATCH", "/v1/wiki/pages/"+p.ID, admin, map[string]any{"body_html": "<p>Page the on-call lead.</p>", "version": p.Version}).Want(t, 200).JSON(t, &theirs)
	if theirs.Version != 2 || theirs.UpdatedBy == nil || theirs.UpdatedBy.ID != ws.Admin.ID.String() {
		t.Fatalf("admin edit %+v", theirs)
	}
	// The agent still edits version 1: refused, nothing changes.
	r := e.Do("PATCH", "/v1/wiki/pages/"+p.ID, agent, map[string]any{"body_html": "<p>Mine</p>", "version": p.Version})
	r.Want(t, 409)
	if !strings.Contains(string(r.Body), "page_changed") {
		t.Fatalf("stale answer %s", r.Body)
	}
	e.Do("GET", "/v1/wiki/pages/"+p.ID, agent, nil).Want(t, 200).JSON(t, &mine)
	if mine.BodyHTML != "<p>Page the on-call lead.</p>" || mine.Version != 2 {
		t.Fatalf("after refusal %+v", mine)
	}
	// Overwriting is an edit of the version now there.
	e.Do("PATCH", "/v1/wiki/pages/"+p.ID, agent, map[string]any{"body_html": "<p>Mine</p>", "version": mine.Version}).Want(t, 200).JSON(t, &mine)
	if mine.Version != 3 {
		t.Fatalf("overwrite %+v", mine)
	}
	// An edit that changes nothing keeps the version.
	e.Do("PATCH", "/v1/wiki/pages/"+p.ID, agent, map[string]any{"body_html": "<p>Mine</p>", "version": 3}).Want(t, 200).JSON(t, &mine)
	if mine.Version != 3 {
		t.Fatalf("no-op edit moved the version: %+v", mine)
	}
}

func TestDelete(t *testing.T) {
	e := hdtest.New(t, wiki.Register)
	ws := e.NewWorkspace()
	agent, admin := ws.Agent.As(ws), ws.Admin.As(ws)
	otherUser := e.AddUser(ws, auth.RoleAgent, "Other Agent")
	other := otherUser.As(ws)

	top := create(t, e, agent, map[string]any{"title": "Top"})
	sibling := create(t, e, agent, map[string]any{"title": "Sibling"})
	sub := create(t, e, other, map[string]any{"title": "Sub", "parent_id": top.ID})
	subsub := create(t, e, agent, map[string]any{"title": "Subsub", "parent_id": sub.ID})
	_, seq := e.Events(ws.ID, 0)

	// Agents delete a page they created with everything under it; admins any.
	e.Do("DELETE", "/v1/wiki/pages/"+top.ID, other, nil).Want(t, 403)
	e.Do("DELETE", "/v1/wiki/pages/"+sub.ID, agent, nil).Want(t, 403)
	if r := e.Do("DELETE", "/v1/wiki/pages/"+top.ID, agent, nil).Want(t, 403); !strings.Contains(string(r.Body), "only an admin") {
		t.Fatalf("refusal %s", r.Body)
	}
	e.Do("DELETE", "/v1/wiki/pages/"+top.ID, admin, nil).Want(t, 204)
	e.Do("GET", "/v1/wiki/pages/"+sub.ID, agent, nil).Want(t, 404)
	var left, revs int
	e.Scalar(`SELECT count(*) FROM wiki_pages WHERE workspace_id = $1`, &left, ws.ID)
	e.Scalar(`SELECT count(*) FROM wiki_page_revisions WHERE workspace_id = $1`, &revs, ws.ID)
	if left != 1 || revs != 1 {
		t.Fatalf("left %d pages, %d revisions", left, revs)
	}
	if got := children(tree(t, e, agent), ""); !slices.Equal(got, []string{"Sibling"}) {
		t.Fatalf("after delete %v", got)
	}
	types, _ := e.Events(ws.ID, seq)
	if !slices.Equal(types, []string{"wiki_page.deleted"}) {
		t.Fatalf("events %v", types)
	}
	var data string
	e.Scalar(`SELECT data::text FROM events WHERE workspace_id = $1 AND type = 'wiki_page.deleted'`, &data, ws.ID)
	for _, id := range []string{top.ID, sub.ID, subsub.ID} {
		if !strings.Contains(data, id) {
			t.Fatalf("deleted event lacks %s: %s", id, data)
		}
	}
	var subpages string
	e.Scalar(`SELECT data->>'subpages' FROM audit_logs WHERE workspace_id = $1 AND action = 'wiki_page.deleted'`, &subpages, ws.ID)
	if subpages != "2" {
		t.Fatalf("audit subpages %q", subpages)
	}
	e.Do("DELETE", "/v1/wiki/pages/"+sibling.ID, agent, nil).Want(t, 204)
	e.Do("DELETE", "/v1/wiki/pages/"+sibling.ID, admin, nil).Want(t, 404)

	// Moving someone else's pages under one's own page doesn't make them deletable.
	policies := create(t, e, other, map[string]any{"title": "Policies"})
	create(t, e, other, map[string]any{"title": "Refunds", "parent_id": policies.ID})
	mine := create(t, e, agent, map[string]any{"title": "Empty"})
	e.Do("POST", "/v1/wiki/pages/"+policies.ID+"/move", agent, map[string]any{"parent_id": mine.ID, "position": 0}).Want(t, 200)
	e.Do("DELETE", "/v1/wiki/pages/"+mine.ID, agent, nil).Want(t, 403)
	nodes := tree(t, e, agent)
	if got := children(nodes, mine.ID); !slices.Equal(got, []string{"Policies"}) {
		t.Fatalf("after refused delete %v", got)
	}
	// The tree names each page's creator, so the app knows which deletes to offer.
	for _, n := range nodes {
		want := otherUser.ID.String()
		if n.ID == mine.ID {
			want = ws.Agent.ID.String()
		}
		if n.CreatedByID == nil || *n.CreatedByID != want {
			t.Fatalf("created_by_id of %s: %v", n.Title, n.CreatedByID)
		}
	}
	// An API key with wiki:write deletes any page.
	key := hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeWikiRead, auth.ScopeWikiWrite)}
	e.Do("DELETE", "/v1/wiki/pages/"+mine.ID, key, nil).Want(t, 204)
	if n := tree(t, e, agent); len(n) != 0 {
		t.Fatalf("left %+v", n)
	}
}

type rev struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	BodyHTML *string `json:"body_html"`
	Author   *struct {
		ID string `json:"id"`
	} `json:"author"`
}

func TestRevisions(t *testing.T) {
	e := hdtest.New(t, wiki.Register)
	ws := e.NewWorkspace()
	agent, admin := ws.Agent.As(ws), ws.Admin.As(ws)
	list := func(id string) []rev {
		t.Helper()
		var out struct{ Data []rev }
		e.Do("GET", "/v1/wiki/pages/"+id+"/revisions", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &out)
		return out.Data
	}
	body := func(page, rid string) string {
		t.Helper()
		var out rev
		e.Do("GET", "/v1/wiki/pages/"+page+"/revisions/"+rid, agent, nil).Want(t, 200).JSON(t, &out)
		if out.BodyHTML == nil {
			t.Fatal("revision without a body")
		}
		return *out.BodyHTML
	}

	p := create(t, e, agent, map[string]any{"title": "Refund policy", "body_html": "<p>v1</p>"})
	e.Do("PATCH", "/v1/wiki/pages/"+p.ID, agent, map[string]any{"body_html": "<p>v2</p>"}).Want(t, 200)
	e.Do("PATCH", "/v1/wiki/pages/"+p.ID, agent, map[string]any{"body_html": "<p>v3</p>"}).Want(t, 200)
	// One author typing for a while is one revision.
	if rs := list(p.ID); len(rs) != 1 || rs[0].BodyHTML != nil || rs[0].Author == nil || rs[0].Author.ID != ws.Agent.ID.String() {
		t.Fatalf("coalesced %+v", rs)
	}
	// Someone else's edit is a revision of its own.
	e.Do("PATCH", "/v1/wiki/pages/"+p.ID, admin, map[string]any{"body_html": "<p>v4</p>"}).Want(t, 200)
	// And so is the same author's after a pause.
	e.Exec(`UPDATE wiki_page_revisions SET updated_at = now() - interval '11 minutes' WHERE page_id = $1`, p.ID)
	e.Do("PATCH", "/v1/wiki/pages/"+p.ID, admin, map[string]any{"body_html": "<p>v5</p>"}).Want(t, 200)
	rs := list(p.ID)
	if len(rs) != 3 {
		t.Fatalf("revisions %d", len(rs))
	}
	if got := body(p.ID, rs[2].ID); got != "<p>v3</p>" {
		t.Fatalf("oldest revision %q", got)
	}

	// Restoring is a new version and a new revision; what it replaced stays in history.
	e.Do("POST", "/v1/wiki/pages/"+p.ID+"/revisions/"+rs[2].ID+"/restore", ws.Viewer.As(ws), nil).Want(t, 403)
	var restored page
	e.Do("POST", "/v1/wiki/pages/"+p.ID+"/revisions/"+rs[2].ID+"/restore", agent, nil).Want(t, 200).JSON(t, &restored)
	if restored.BodyHTML != "<p>v3</p>" || restored.Version != 6 {
		t.Fatalf("restored %+v", restored)
	}
	if rs = list(p.ID); len(rs) != 4 || body(p.ID, rs[1].ID) != "<p>v5</p>" {
		t.Fatalf("after restore %+v", rs)
	}
	other := e.NewWorkspace()
	e.Do("GET", "/v1/wiki/pages/"+p.ID+"/revisions/"+rs[0].ID, other.Owner.As(other), nil).Want(t, 404)
	e.Do("POST", "/v1/wiki/pages/"+p.ID+"/revisions/"+rs[0].ID+"/restore", other.Owner.As(other), nil).Want(t, 404)

	// At most 100 are kept, the newest.
	key := hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeWikiRead, auth.ScopeWikiWrite)}
	for i := range 100 {
		e.Do("PATCH", "/v1/wiki/pages/"+p.ID, key, map[string]any{"body_html": fmt.Sprintf("<p>key %d</p>", i)}).Want(t, 200)
	}
	if rs = list(p.ID); len(rs) != 100 || body(p.ID, rs[99].ID) != "<p>key 0</p>" {
		t.Fatalf("kept %d", len(rs))
	}
}

func TestSearch(t *testing.T) {
	e := hdtest.New(t, wiki.Register)
	ws := e.NewWorkspace()
	agent := ws.Agent.As(ws)

	policies := create(t, e, agent, map[string]any{"title": "Policies"})
	refunds := create(t, e, agent, map[string]any{"title": "Refund escalation", "parent_id": policies.ID,
		"body_html": "<p>Refunds over 100 euros need a lead &amp; a note on the ticket.</p>"})
	mention := create(t, e, agent, map[string]any{"title": "Onboarding", "body_html": "<p>Learn the refund flow in week two.</p>"})
	create(t, e, agent, map[string]any{"title": "Holidays", "body_html": "<p>Nothing here.</p>"})

	type hit struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
		Path    []ref  `json:"path"`
	}
	var res struct{ Data []hit }
	e.Do("GET", "/v1/wiki/search?q=refu", agent, nil).Want(t, 200).JSON(t, &res)
	if len(res.Data) != 2 || res.Data[0].ID != refunds.ID || res.Data[1].ID != mention.ID {
		t.Fatalf("ranking %+v", res.Data)
	}
	if !strings.Contains(res.Data[0].Snippet, "lead & a note") || strings.Contains(res.Data[0].Snippet, "<") {
		t.Fatalf("snippet %q", res.Data[0].Snippet)
	}
	if len(res.Data[0].Path) != 1 || res.Data[0].Path[0].ID != policies.ID {
		t.Fatalf("path %+v", res.Data[0].Path)
	}
	e.Do("GET", "/v1/wiki/search?q=%27%3A*%20%26%20%21", agent, nil).Want(t, 200).JSON(t, &res)
	if len(res.Data) != 0 {
		t.Fatalf("punctuation matched %+v", res.Data)
	}
	found, err := wiki.Search(e.Ctx, e.D.DB.Pool, ws.ID, "holiday", 5)
	if err != nil || len(found) != 1 {
		t.Fatalf("Search %v %+v", err, found)
	}
}
