package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// spaceBody is a Space for a request body.
func spaceBody(id, name string, sections ...map[string]any) map[string]any {
	if sections == nil {
		sections = []map[string]any{}
	}
	return map[string]any{"id": id, "name": name, "sections": sections}
}

func spaceSection(id string, items ...map[string]any) map[string]any {
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"id": id, "title": "", "items": items}
}

func spaceItem(wid, kind, id string) map[string]any {
	return map[string]any{"workspace_id": wid, "kind": kind, "id": id}
}

// spacesOf reads the spaces list of a response body.
func spacesOf(t *testing.T, r resp) []map[string]any {
	t.Helper()
	list, ok := r.Body["spaces"].([]any)
	if !ok {
		t.Fatalf("no spaces in %s", r.Raw)
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}

// sectionItems returns "kind:id" of each item of a Space's section i.
func sectionItems(sp map[string]any, i int) []string {
	secs := sp["sections"].([]any)
	items := secs[i].(map[string]any)["items"].([]any)
	out := []string{}
	for _, x := range items {
		it := x.(map[string]any)
		out = append(out, fmt.Sprint(it["kind"], ":", it["id"]))
	}
	return out
}

func (e *testEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.d.DB.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (e *testEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.d.DB.Pool.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

func TestSpacesNoneSaved(t *testing.T) {
	e := newEnv(t)
	c, _ := e.signup("spacesnone")
	alpha := c.newWorkspace("Alpha desk")
	c.workspace = "" // a user route: no OD-Workspace

	// Nothing saved: no Spaces, even with a starter Space in a workspace (it is offered to copy).
	r := c.do("GET", "/v1/me/spaces", nil)
	if r.Status != 200 || r.Body["saved"] != false || len(spacesOf(t, r)) != 0 {
		t.Fatalf("get: %d %s", r.Status, r.Raw)
	}
	c.workspace = alpha
	if r = c.do("PUT", "/v1/sidebar/starter", spaceBody(uuid.NewString(), "Team start")); r.Status != 200 {
		t.Fatalf("put starter: %d %s", r.Status, r.Raw)
	}
	c.workspace = ""
	if r = c.do("GET", "/v1/me/spaces", nil); r.Body["saved"] != false || len(spacesOf(t, r)) != 0 {
		t.Fatalf("with a starter: %s", r.Raw)
	}
}

func TestSpacesRoundTrip(t *testing.T) {
	e := newEnv(t)
	c, email := e.signup("spacesrt")
	wid := c.newWorkspace("Round trip")
	c.workspace = ""
	other, _ := e.signup("spacesother")
	foreign := other.newWorkspace("Not yours")

	team := uuid.NewString()
	body := map[string]any{"spaces": []any{
		map[string]any{"id": uuid.NewString(), "name": "  Morning   shift ", "icon": "sun", "color": "amber", "marks": false,
			"sections": []any{map[string]any{"id": "today", "title": "Today", "collapsed": true, "items": []any{
				spaceItem(wid, "ticket_folder", "mine"),
				map[string]any{"workspace_id": wid, "kind": "team", "id": team, "name": "Billing  crew", "badge": "awaiting"},
				spaceItem(wid, "box", "inbox"),
				spaceItem(wid, "page", "reports"),
				spaceItem("", "everywhere", "mine"),
			}}}},
		spaceBody(uuid.NewString(), "Second"),
	}}
	r := c.do("PUT", "/v1/me/spaces", body)
	if r.Status != 200 || r.Body["saved"] != true {
		t.Fatalf("put: %d %s", r.Status, r.Raw)
	}
	spaces := spacesOf(t, r)
	if spaces[0]["name"] != "Morning shift" || spaces[0]["marks"] != false {
		t.Fatalf("stored first: %s", r.Raw)
	}
	if spaces[1]["color"] != "grey" || spaces[1]["marks"] != true || spaces[1]["icon"] != "" {
		t.Fatalf("defaults of second: %s", r.Raw)
	}
	g := c.do("GET", "/v1/me/spaces", nil)
	if g.Status != 200 || g.Body["saved"] != true || string(mustJSON(g.Body["spaces"])) != string(mustJSON(r.Body["spaces"])) {
		t.Fatalf("get after put: %s\nput: %s", g.Raw, r.Raw)
	}
	item := spacesOf(t, g)[0]["sections"].([]any)[0].(map[string]any)["items"].([]any)[1].(map[string]any)
	if item["name"] != "Billing crew" || item["badge"] != "awaiting" || item["id"] != team {
		t.Fatalf("named item: %v", item)
	}
	if _, ok := spacesOf(t, g)[0]["sections"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["name"]; ok {
		t.Fatalf("an unnamed item carries no name: %s", g.Raw)
	}

	// An everywhere row belongs to no workspace, and stays whatever workspaces the user leaves.
	if got := sectionItems(spacesOf(t, g)[0], 0); !slices.Contains(got, "everywhere:mine") {
		t.Fatalf("everywhere row: %v", got)
	}

	// A workspace the user is not a member of.
	r = c.do("PUT", "/v1/me/spaces", map[string]any{"spaces": []any{
		spaceBody(uuid.NewString(), "Sneaky", spaceSection("s", spaceItem(foreign, "section", "inbox")))}})
	if r.Status != 422 {
		t.Fatalf("foreign workspace: %d %s", r.Status, r.Raw)
	}

	// Rows of a workspace left are dropped on read; their section stays.
	fid := uuid.MustParse(foreign)
	uid := e.userID(email)
	e.exec(`INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'agent')`, fid, uid)
	r = c.do("PUT", "/v1/me/spaces", map[string]any{"spaces": []any{
		spaceBody(uuid.NewString(), "Both",
			spaceSection("a", spaceItem(wid, "section", "inbox"), spaceItem(foreign, "section", "views")),
			spaceSection("b", spaceItem(foreign, "view", uuid.NewString()), spaceItem("", "everywhere", "mail")))}})
	if r.Status != 200 {
		t.Fatalf("put with joined workspace: %d %s", r.Status, r.Raw)
	}
	e.exec(`DELETE FROM members WHERE workspace_id = $1 AND user_id = $2`, fid, uid)
	g = c.do("GET", "/v1/me/spaces", nil)
	sp := spacesOf(t, g)[0]
	if got := sectionItems(sp, 0); !slices.Equal(got, []string{"section:inbox"}) {
		t.Fatalf("left workspace items: %v", got)
	}
	if got := sectionItems(sp, 1); !slices.Equal(got, []string{"everywhere:mail"}) {
		t.Fatalf("left workspace's row dropped, everywhere row kept: %v", got)
	}

	// An empty list is a saved choice.
	r = c.do("PUT", "/v1/me/spaces", map[string]any{"spaces": []any{}})
	if r.Status != 200 || r.Body["saved"] != true || len(spacesOf(t, r)) != 0 {
		t.Fatalf("empty list: %d %s", r.Status, r.Raw)
	}
}

func TestSpacesValidation(t *testing.T) {
	e := newEnv(t)
	c, email := e.signup("spacesval")
	wid := c.newWorkspace("Validation")
	c.workspace = ""

	one := func(sp map[string]any) map[string]any { return map[string]any{"spaces": []any{sp}} }
	withItem := func(it map[string]any) map[string]any {
		return one(spaceBody(uuid.NewString(), "Space", spaceSection("s", it)))
	}
	items := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = spaceItem(wid, "section", "inbox")
		}
		return out
	}
	many := func(n int) map[string]any {
		list := []any{}
		for range n {
			list = append(list, spaceBody(uuid.NewString(), "Space"))
		}
		return map[string]any{"spaces": list}
	}
	dup := uuid.NewString()
	sections := func(n, each int) map[string]any {
		secs := []map[string]any{}
		for i := range n {
			secs = append(secs, spaceSection(fmt.Sprint("s", i), items(each)...))
		}
		return one(spaceBody(uuid.NewString(), "Space", secs...))
	}
	// Two Spaces of 300 named rows: well under the body limit, over 64 KB stored.
	big := func() map[string]any {
		list := []any{}
		for range 2 {
			secs := []map[string]any{}
			for i := range 5 {
				its := []map[string]any{}
				for range 60 {
					its = append(its, map[string]any{"workspace_id": wid, "kind": "team", "id": uuid.NewString(), "name": strings.Repeat("n", 64)})
				}
				secs = append(secs, spaceSection(fmt.Sprint("s", i), its...))
			}
			list = append(list, spaceBody(uuid.NewString(), "Big", secs...))
		}
		return map[string]any{"spaces": list}
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{"21 spaces", many(21)},
		{"space id not a uuid", one(spaceBody("morning", "Space"))},
		{"space ids twice", map[string]any{"spaces": []any{spaceBody(dup, "One"), spaceBody(dup, "Two")}}},
		{"no name", one(spaceBody(uuid.NewString(), "  "))},
		{"name 41 characters", one(spaceBody(uuid.NewString(), strings.Repeat("a", 41)))},
		{"name with a link", one(spaceBody(uuid.NewString(), "Visit example.com"))},
		{"unknown icon", one(map[string]any{"id": uuid.NewString(), "name": "Space", "icon": "skull", "sections": []any{}})},
		{"unknown colour", one(map[string]any{"id": uuid.NewString(), "name": "Space", "color": "black", "sections": []any{}})},
		{"21 sections", sections(21, 0)},
		{"61 rows in a section", sections(1, 61)},
		{"301 rows in a space", sections(6, 51)},
		{"section ids twice", one(spaceBody(uuid.NewString(), "Space", spaceSection("a"), spaceSection("a")))},
		{"bad section id", one(spaceBody(uuid.NewString(), "Space", spaceSection("a b")))},
		{"section id 41 characters", one(spaceBody(uuid.NewString(), "Space", spaceSection(strings.Repeat("a", 41))))},
		{"title 41 characters", one(spaceBody(uuid.NewString(), "Space", map[string]any{"id": "a", "title": strings.Repeat("t", 41), "items": []any{}}))},
		{"title with a link", one(spaceBody(uuid.NewString(), "Space", map[string]any{"id": "a", "title": "see https://x.io", "items": []any{}}))},
		{"no workspace id", withItem(spaceItem("", "section", "inbox"))},
		{"workspace id not a uuid", withItem(spaceItem("acme", "section", "inbox"))},
		{"unknown kind", withItem(spaceItem(wid, "ticket", "1"))},
		{"unknown section", withItem(spaceItem(wid, "section", "settings"))},
		{"unknown ticket folder", withItem(spaceItem(wid, "ticket_folder", "archived"))},
		{"unknown box", withItem(spaceItem(wid, "box", "starred"))},
		{"unknown page", withItem(spaceItem(wid, "page", "billing"))},
		{"team id not a uuid", withItem(spaceItem(wid, "team", "support"))},
		{"view id not a uuid", withItem(spaceItem(wid, "view", "42"))},
		{"account id not a uuid", withItem(spaceItem(wid, "account", "x"))},
		{"folder id not a uuid", withItem(spaceItem(wid, "folder", "inbox"))},
		{"row name with a link", withItem(map[string]any{"workspace_id": wid, "kind": "section", "id": "inbox", "name": "mail me@x.com"})},
		{"row name 65 characters", withItem(map[string]any{"workspace_id": wid, "kind": "section", "id": "inbox", "name": strings.Repeat("r", 65)})},
		{"unknown everywhere list", withItem(spaceItem("", "everywhere", "views"))},
		{"everywhere row of a workspace", withItem(spaceItem(wid, "everywhere", "mine"))},
		{"unknown badge", withItem(map[string]any{"workspace_id": wid, "kind": "section", "id": "inbox", "badge": "all"})},
		{"over 64 KB", big()},
	}
	for _, tc := range cases {
		r := c.do("PUT", "/v1/me/spaces", tc.body)
		if r.Status != 422 {
			t.Errorf("%s: %d %s", tc.name, r.Status, r.Raw)
		}
	}
	if n := e.count(`SELECT count(*) FROM user_spaces s JOIN users u ON u.id = s.user_id WHERE u.email = $1`, email); n != 0 {
		t.Fatalf("a refused list was saved")
	}

	// At the limits.
	if r := c.do("PUT", "/v1/me/spaces", many(20)); r.Status != 200 {
		t.Fatalf("20 spaces: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PUT", "/v1/me/spaces", sections(5, 60)); r.Status != 200 {
		t.Fatalf("300 rows: %d %s", r.Status, r.Raw)
	}
	if r := c.do("PUT", "/v1/me/spaces", one(spaceBody(uuid.NewString(), strings.Repeat("a", 40)))); r.Status != 200 {
		t.Fatalf("40 character name: %d %s", r.Status, r.Raw)
	}
}

func TestStarterSpace(t *testing.T) {
	e := newEnv(t)
	owner, _ := e.signup("starterowner")
	wid := owner.newWorkspace("Starter co")
	w := uuid.MustParse(wid)
	agent, _ := e.memberOf(w, "starteragent", "agent")
	viewer, _ := e.memberOf(w, "starterviewer", "viewer")
	other, _ := e.signup("starterother")
	foreign := other.newWorkspace("Elsewhere")

	r := agent.do("GET", "/v1/sidebar/starter", nil)
	if r.Status != 200 || r.Body["space"] != nil {
		t.Fatalf("no starter: %d %s", r.Status, r.Raw)
	}
	id := uuid.NewString()
	body := spaceBody(id, "Welcome", spaceSection("main",
		spaceItem(foreign, "section", "inbox"), // overwritten with the workspace
		spaceItem("", "page", "knowledge")))
	if r := agent.do("PUT", "/v1/sidebar/starter", body); r.Status != 403 {
		t.Fatalf("agent put: %d %s", r.Status, r.Raw)
	}
	r = owner.do("PUT", "/v1/sidebar/starter", body)
	if r.Status != 200 {
		t.Fatalf("owner put: %d %s", r.Status, r.Raw)
	}
	sp := r.Body["space"].(map[string]any)
	for _, x := range sp["sections"].([]any)[0].(map[string]any)["items"].([]any) {
		if x.(map[string]any)["workspace_id"] != wid {
			t.Fatalf("item workspace not forced: %s", r.Raw)
		}
	}
	if sp["id"] != id || sp["color"] != "grey" {
		t.Fatalf("starter: %s", r.Raw)
	}
	g := viewer.do("GET", "/v1/sidebar/starter", nil)
	if g.Status != 200 || string(mustJSON(g.Body["space"])) != string(mustJSON(r.Body["space"])) {
		t.Fatalf("viewer get: %d %s", g.Status, g.Raw)
	}
	if r := owner.do("PUT", "/v1/sidebar/starter", spaceBody(uuid.NewString(), "Everywhere", spaceSection("main", spaceItem("", "everywhere", "mine")))); r.Status != 422 {
		t.Fatalf("starter with an everywhere row: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PUT", "/v1/sidebar/starter", spaceBody(uuid.NewString(), "")); r.Status != 422 {
		t.Fatalf("starter without a name: %d %s", r.Status, r.Raw)
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'sidebar.starter_updated'`, w); n != 1 {
		t.Fatalf("starter_updated audit rows: %d", n)
	}
	if r := agent.do("DELETE", "/v1/sidebar/starter", nil); r.Status != 403 {
		t.Fatalf("agent delete: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("DELETE", "/v1/sidebar/starter", nil); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("DELETE", "/v1/sidebar/starter", nil); r.Status != 204 {
		t.Fatalf("delete again: %d %s", r.Status, r.Raw)
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'sidebar.starter_removed'`, w); n != 1 {
		t.Fatalf("starter_removed audit rows: %d", n)
	}
	if r := agent.do("GET", "/v1/sidebar/starter", nil); r.Status != 200 || r.Body["space"] != nil {
		t.Fatalf("after delete: %d %s", r.Status, r.Raw)
	}
	// The starter belongs to the workspace in OD-Workspace; an outsider cannot read it.
	other.workspace = wid
	if r := other.do("GET", "/v1/sidebar/starter", nil); r.Status < 400 {
		t.Fatalf("outsider get: %d %s", r.Status, r.Raw)
	}
}
