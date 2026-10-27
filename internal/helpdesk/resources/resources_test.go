package resources_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/resources"
	"github.com/useopendesk/opendesk/internal/ids"
)

type page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

type tag struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Color       string   `json:"color"`
	Description string   `json:"description"`
	GroupID     *string  `json:"group_id"`
	Archived    bool     `json:"archived"`
	AppliesTo   []string `json:"applies_to"`
	Usage       struct {
		Tickets int `json:"tickets"`
	} `json:"usage"`
}

func TestTags(t *testing.T) {
	e := hdtest.New(t, resources.Register)
	ws := e.NewWorkspace()
	other := e.NewWorkspace()
	a := ws.Admin.As(ws)

	var bug tag
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "  bug  "}).Want(t, 201).JSON(t, &bug)
	if bug.Name != "bug" || bug.Color == "" || len(bug.AppliesTo) != 3 {
		t.Fatal(bug)
	}
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "BUG"}).Want(t, 409)
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "<b>x</b>"}).Want(t, 422)
	e.Do("POST", "/v1/tags", a, map[string]any{"name": ""}).Want(t, 422)
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "x", "color": "chartreuse"}).Want(t, 422)
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "x", "description": "<script>"}).Want(t, 422)
	e.Do("POST", "/v1/tags", ws.Viewer.As(ws), map[string]any{"name": "nope"}).Want(t, 403)
	e.Do("POST", "/v1/tags", ws.Agent.As(ws), map[string]any{"name": "nope"}).Want(t, 403)
	var vip tag
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "VIP", "color": "amber", "description": "Look after", "applies_to": []string{"contacts", "tickets"}}).
		Want(t, 201).JSON(t, &vip)
	if vip.Color != "amber" || vip.Description != "Look after" || !slices.Equal(vip.AppliesTo, []string{"tickets", "contacts"}) {
		t.Fatal(vip)
	}

	var p page[tag]
	e.Do("GET", "/v1/tags", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 2 || p.Data[0].Name != "bug" || p.NextCursor != nil {
		t.Fatalf("list %+v", p)
	}
	e.Do("PATCH", "/v1/tags/"+vip.ID, a, map[string]any{"name": "Bug"}).Want(t, 409)
	e.Do("PATCH", "/v1/tags/"+vip.ID, a, map[string]any{"name": "vip", "archived": true}).Want(t, 200)
	e.Do("GET", "/v1/tags?archived=false", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 1 || p.Data[0].Name != "bug" {
		t.Fatalf("unarchived %+v", p)
	}
	e.Do("PATCH", "/v1/tags/"+vip.ID, other.Owner.As(other), map[string]any{"name": "x"}).Want(t, 404)
	e.Do("DELETE", "/v1/tags/"+vip.ID, other.Owner.As(other), nil).Want(t, 404)

	// Usage counts, then delete cascades to tickets.
	tid := ids.New()
	e.Exec(`INSERT INTO tickets (id, workspace_id, number, channel, brand_id) VALUES ($1, $2, 1, 'api', $3)`, tid, ws.ID, ws.BrandID)
	e.Exec(`INSERT INTO ticket_tags (ticket_id, tag_id) VALUES ($1, $2)`, tid, bug.ID)
	var got tag
	e.Do("GET", "/v1/tags/"+bug.ID, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &got)
	if got.Usage.Tickets != 1 {
		t.Fatalf("usage %+v", got)
	}
	e.Do("DELETE", "/v1/tags/"+bug.ID, a, nil).Want(t, 204)
	var n int
	e.Scalar(`SELECT count(*) FROM ticket_tags WHERE ticket_id = $1`, &n, tid)
	if n != 0 {
		t.Fatal("ticket_tags kept")
	}

	key := e.APIKey(ws, "tickets:read")
	e.Do("GET", "/v1/tags", hdtest.Auth{Bearer: key}, nil).Want(t, 200)
	e.Do("GET", "/v1/tags", hdtest.Auth{Bearer: e.APIKey(ws, "settings:read")}, nil).Want(t, 200)
	e.Do("POST", "/v1/tags", hdtest.Auth{Bearer: key}, map[string]any{"name": "k"}).Want(t, 403)
	e.Do("POST", "/v1/tags", hdtest.Auth{Bearer: e.APIKey(ws, "tickets:write")}, map[string]any{"name": "k"}).Want(t, 403)
	e.Do("POST", "/v1/tags", hdtest.Auth{Bearer: e.APIKey(ws, "settings:write")}, map[string]any{"name": "k"}).Want(t, 201)

	types, _ := e.Events(ws.ID, 0)
	for _, w := range []string{"tag.created", "tag.updated", "tag.deleted"} {
		if !slices.Contains(types, w) {
			t.Errorf("missing %s", w)
		}
	}
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action LIKE 'tag.%'`, &n, ws.ID)
	if n < 4 {
		t.Fatalf("audit entries %d", n)
	}
}

type tagGroup struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Color     *string `json:"color"`
	Exclusive bool    `json:"exclusive"`
	Position  int     `json:"position"`
	TagCount  int     `json:"tag_count"`
}

func TestTagGroupsReorderAndMerge(t *testing.T) {
	e := hdtest.New(t, resources.Register)
	ws := e.NewWorkspace()
	a := ws.Admin.As(ws)

	var g tagGroup
	e.Do("POST", "/v1/tag-groups", a, map[string]any{"name": "Type", "exclusive": true, "color": "blue"}).Want(t, 201).JSON(t, &g)
	if !g.Exclusive || g.Color == nil || *g.Color != "blue" {
		t.Fatal(g)
	}
	e.Do("POST", "/v1/tag-groups", a, map[string]any{"name": "type"}).Want(t, 409)
	e.Do("POST", "/v1/tag-groups", ws.Agent.As(ws), map[string]any{"name": "Nope"}).Want(t, 403)
	var g2 tagGroup
	e.Do("POST", "/v1/tag-groups", a, map[string]any{"name": "Area"}).Want(t, 201).JSON(t, &g2)
	if g2.Position != 1 {
		t.Fatal(g2)
	}
	var gp page[tagGroup]
	e.Do("POST", "/v1/tag-groups/reorder", a, map[string]any{"ids": []string{g2.ID, g.ID}}).Want(t, 200).JSON(t, &gp)
	if gp.Data[0].ID != g2.ID {
		t.Fatalf("reorder %+v", gp)
	}
	e.Do("POST", "/v1/tag-groups/reorder", a, map[string]any{"ids": []string{ids.New().String()}}).Want(t, 422)

	var bug, defect, question tag
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "Bug", "group_id": g.ID}).Want(t, 201).JSON(t, &bug)
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "Defect"}).Want(t, 201).JSON(t, &defect)
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "Question", "group_id": g.ID}).Want(t, 201).JSON(t, &question)
	e.Do("POST", "/v1/tags", a, map[string]any{"name": "X", "group_id": ids.New().String()}).Want(t, 422)

	var tp page[tag]
	e.Do("POST", "/v1/tags/reorder", a, map[string]any{"ids": []string{question.ID, bug.ID}}).Want(t, 200).JSON(t, &tp)
	e.Do("GET", "/v1/tags?group_id="+g.ID, ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &tp)
	if len(tp.Data) != 2 || tp.Data[0].Name != "Question" {
		t.Fatalf("group list %+v", tp)
	}

	// An automation and a view name "Defect"; merging into Bug rewrites them.
	aid := ids.New()
	e.Exec(`INSERT INTO automations (id, workspace_id, name, trigger, conditions, actions, enabled, position)
		VALUES ($1, $2, 'a', 'ticket_created', '[{"field":"tag","op":"equals","value":"defect"}]', '[{"type":"add_tag","value":"Defect"}]', true, 0)`, aid, ws.ID)
	vid := ids.New()
	e.Exec(`INSERT INTO views (id, workspace_id, name, filters, position) VALUES ($1, $2, 'v', '{"tag":"Defect"}', 0)`, vid, ws.ID)
	// A ticket with Defect and Question: after merging into Bug (exclusive Type), it keeps only Bug.
	tid := ids.New()
	e.Exec(`INSERT INTO tickets (id, workspace_id, number, channel, brand_id) VALUES ($1, $2, 1, 'api', $3)`, tid, ws.ID, ws.BrandID)
	e.Exec(`INSERT INTO ticket_tags (ticket_id, tag_id) VALUES ($1, $2), ($1, $3)`, tid, defect.ID, question.ID)

	e.Do("POST", "/v1/tags/"+defect.ID+"/merge", a, map[string]any{"into": defect.ID}).Want(t, 422)
	var into tag
	e.Do("POST", "/v1/tags/"+defect.ID+"/merge", a, map[string]any{"into": bug.ID}).Want(t, 200).JSON(t, &into)
	if into.ID != bug.ID || into.Usage.Tickets != 1 {
		t.Fatalf("merged %+v", into)
	}
	var names []string
	rows, _ := e.D.DB.Pool.Query(e.Ctx, `SELECT g.name FROM ticket_tags tt JOIN tags g ON g.id = tt.tag_id WHERE tt.ticket_id = $1`, tid)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		names = append(names, s)
	}
	rows.Close()
	if !slices.Equal(names, []string{"Bug"}) {
		t.Fatalf("ticket labels %v", names)
	}
	var conds, acts, filters string
	e.Scalar(`SELECT conditions::text FROM automations WHERE id = $1`, &conds, aid)
	e.Scalar(`SELECT actions::text FROM automations WHERE id = $1`, &acts, aid)
	e.Scalar(`SELECT filters::text FROM views WHERE id = $1`, &filters, vid)
	if !strings.Contains(conds, `"Bug"`) || !strings.Contains(acts, `"Bug"`) || !strings.Contains(filters, `"Bug"`) {
		t.Fatalf("refs not rewritten: %s %s %s", conds, acts, filters)
	}

	// Deleting a group ungroups its labels.
	e.Do("DELETE", "/v1/tag-groups/"+g.ID, a, nil).Want(t, 204)
	var got tag
	e.Do("GET", "/v1/tags/"+bug.ID, a, nil).Want(t, 200).JSON(t, &got)
	if got.GroupID != nil {
		t.Fatal("still grouped")
	}
	types, _ := e.Events(ws.ID, 0)
	for _, w := range []string{"tag_group.created", "tag_group.updated", "tag_group.deleted", "tag.merged"} {
		if !slices.Contains(types, w) {
			t.Errorf("missing %s", w)
		}
	}
}

type customField struct {
	ID              string `json:"id"`
	Entity          string `json:"entity"`
	Key             string `json:"key"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	RequiredOnSolve bool   `json:"required_on_solve"`
	Archived        bool   `json:"archived"`
	Position        int    `json:"position"`
	Options         []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Color string `json:"color"`
	} `json:"options"`
}

func TestCustomFields(t *testing.T) {
	e := hdtest.New(t, resources.Register)
	ws := e.NewWorkspace()
	other := e.NewWorkspace()
	a := ws.Admin.As(ws)

	var plan customField
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "name": "Plan", "type": "select",
		"options": []map[string]any{{"label": "Free"}, {"label": "Pro plan", "color": "violet"}}, "required_on_solve": true}).
		Want(t, 201).JSON(t, &plan)
	if plan.Key != "plan" || len(plan.Options) != 2 || plan.Options[1].ID != "pro-plan" || plan.Options[0].Color != "gray" {
		t.Fatalf("%+v", plan)
	}
	var plan2 customField
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "name": "Plan", "type": "text"}).Want(t, 201).JSON(t, &plan2)
	if plan2.Key != "plan_2" || plan2.Position != 1 {
		t.Fatalf("%+v", plan2)
	}
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "key": "plan", "name": "P", "type": "text"}).Want(t, 409)
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "key": "Bad Key", "name": "P", "type": "text"}).Want(t, 422)
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "name": "<b>", "type": "text"}).Want(t, 422)
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "name": "T", "type": "text", "options": []map[string]any{{"label": "x"}}}).Want(t, 422)
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "contact", "name": "Tier", "type": "text", "required_on_solve": true}).Want(t, 422)
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "name": "T", "type": "select",
		"options": []map[string]any{{"id": "a", "label": "A"}, {"id": "a", "label": "B"}}}).Want(t, 422)
	e.Do("POST", "/v1/custom-fields", ws.Agent.As(ws), map[string]any{"entity": "ticket", "name": "X", "type": "text"}).Want(t, 403)

	// Removing an option clears it from stored values.
	tid := ids.New()
	e.Exec(`INSERT INTO tickets (id, workspace_id, number, channel, fields, brand_id) VALUES ($1, $2, 1, 'api', '{"plan": "free"}', $3)`, tid, ws.ID, ws.BrandID)
	e.Do("PATCH", "/v1/custom-fields/"+plan.ID, a, map[string]any{"options": []map[string]any{{"id": "pro-plan", "label": "Pro"}}}).Want(t, 200)
	var fields string
	e.Scalar(`SELECT fields::text FROM tickets WHERE id = $1`, &fields, tid)
	if fields != "{}" {
		t.Fatalf("fields %s", fields)
	}
	e.Do("PATCH", "/v1/custom-fields/"+plan.ID, a, map[string]any{"type": "text"}).Want(t, 422)
	e.Do("PATCH", "/v1/custom-fields/"+plan.ID, other.Owner.As(other), map[string]any{"name": "x"}).Want(t, 404)

	var p page[customField]
	e.Do("POST", "/v1/custom-fields/reorder", a, map[string]any{"entity": "ticket", "ids": []string{plan2.ID, plan.ID}}).Want(t, 200).JSON(t, &p)
	if p.Data[0].ID != plan2.ID {
		t.Fatalf("%+v", p)
	}
	e.Do("GET", "/v1/custom-fields?entity=ticket", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 2 {
		t.Fatalf("%+v", p)
	}
	e.Do("GET", "/v1/custom-fields", other.Owner.As(other), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 0 {
		t.Fatal("leaked across workspaces")
	}
	e.Do("GET", "/v1/custom-fields", hdtest.Auth{Bearer: e.APIKey(ws, "contacts:read")}, nil).Want(t, 200)
	e.Do("POST", "/v1/custom-fields", hdtest.Auth{Bearer: e.APIKey(ws, "tickets:write")}, map[string]any{"entity": "ticket", "name": "K", "type": "text"}).Want(t, 403)

	// The 100 per entity limit.
	for i := 0; i < 98; i++ {
		e.Exec(`INSERT INTO custom_fields (workspace_id, entity, key, name, type) VALUES ($1, 'ticket', $2, 'F', 'text')`, ws.ID, hdtest.Fmt("f%d", i))
	}
	e.Do("POST", "/v1/custom-fields", a, map[string]any{"entity": "ticket", "name": "Over", "type": "text"}).Want(t, 422)

	e.Exec(`UPDATE tickets SET fields = '{"plan_2": "x"}' WHERE id = $1`, tid)
	e.Do("DELETE", "/v1/custom-fields/"+plan2.ID, a, nil).Want(t, 204)
	e.Scalar(`SELECT fields::text FROM tickets WHERE id = $1`, &fields, tid)
	if fields != "{}" {
		t.Fatalf("fields after delete %s", fields)
	}
	types, _ := e.Events(ws.ID, 0)
	for _, w := range []string{"custom_field.created", "custom_field.updated", "custom_field.deleted"} {
		if !slices.Contains(types, w) {
			t.Errorf("missing %s", w)
		}
	}
}

type savedReply struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Shortcut string `json:"shortcut"`
	BodyHTML string `json:"body_html"`
}

func TestSavedReplies(t *testing.T) {
	e := hdtest.New(t, resources.Register)
	ws := e.NewWorkspace()
	other := e.NewWorkspace()
	a := ws.Agent.As(ws)

	var r savedReply
	e.Do("POST", "/v1/saved-replies", a, map[string]any{"title": "Refund", "shortcut": "/Refund",
		"body_html": `<p onclick="x">Hi <script>bad()</script>{{contact.first_name}}</p>`}).Want(t, 201).JSON(t, &r)
	if r.Shortcut != "refund" || strings.Contains(r.BodyHTML, "script") || strings.Contains(r.BodyHTML, "onclick") {
		t.Fatalf("created %+v", r)
	}
	e.Do("POST", "/v1/saved-replies", a, map[string]any{"title": "Other", "shortcut": "refund", "body_html": "<p>x</p>"}).Want(t, 409)
	e.Do("POST", "/v1/saved-replies", a, map[string]any{"title": "Bad", "shortcut": "has space", "body_html": "<p>x</p>"}).Want(t, 422)
	e.Do("POST", "/v1/saved-replies", a, map[string]any{"title": "Empty", "body_html": "<script>x</script>"}).Want(t, 422)
	// Empty shortcuts do not clash.
	e.Do("POST", "/v1/saved-replies", a, map[string]any{"title": "A", "body_html": "<p>a</p>"}).Want(t, 201)
	var b savedReply
	e.Do("POST", "/v1/saved-replies", a, map[string]any{"title": "B", "body_html": "<p>b</p>"}).Want(t, 201).JSON(t, &b)
	e.Do("PATCH", "/v1/saved-replies/"+b.ID, a, map[string]any{"shortcut": "refund"}).Want(t, 409)
	e.Do("PATCH", "/v1/saved-replies/"+b.ID, a, map[string]any{"shortcut": "bee", "title": "Bee"}).Want(t, 200).JSON(t, &b)
	if b.Shortcut != "bee" || b.Title != "Bee" || b.BodyHTML != "<p>b</p>" {
		t.Fatalf("patched %+v", b)
	}
	// The same shortcut is fine in another workspace.
	e.Do("POST", "/v1/saved-replies", other.Agent.As(other), map[string]any{"title": "R", "shortcut": "refund", "body_html": "<p>x</p>"}).Want(t, 201)
	e.Do("GET", "/v1/saved-replies/"+r.ID, other.Agent.As(other), nil).Want(t, 404)

	var p page[savedReply]
	e.Do("GET", "/v1/saved-replies?limit=2", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 2 || p.NextCursor == nil {
		t.Fatalf("page %+v", p)
	}
	var p2 page[savedReply]
	e.Do("GET", "/v1/saved-replies?limit=2&cursor="+*p.NextCursor, a, nil).Want(t, 200).JSON(t, &p2)
	if len(p2.Data) != 1 || p2.NextCursor != nil {
		t.Fatalf("page2 %+v", p2)
	}
	e.Do("GET", "/v1/saved-replies?q=ref", a, nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 1 {
		t.Fatalf("q %+v", p.Data)
	}
	e.Do("DELETE", "/v1/saved-replies/"+r.ID, ws.Viewer.As(ws), nil).Want(t, 403)
	e.Do("DELETE", "/v1/saved-replies/"+r.ID, a, nil).Want(t, 204)
	e.Do("GET", "/v1/saved-replies/"+r.ID, a, nil).Want(t, 404)
}

type view struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Shared   bool           `json:"shared"`
	Filters  map[string]any `json:"filters"`
	Position int            `json:"position"`
}

func TestViews(t *testing.T) {
	e := hdtest.New(t, resources.Register)
	ws := e.NewWorkspace()
	other := e.NewWorkspace()

	var shared view
	e.Do("POST", "/v1/views", ws.Admin.As(ws), map[string]any{"name": "Urgent", "shared": true,
		"filters": map[string]any{"status": []string{"open", "pending", "open"}, "priority": []string{"urgent"}, "assignee": "none"}}).
		Want(t, 201).JSON(t, &shared)
	if !shared.Shared || len(shared.Filters["status"].([]any)) != 2 || shared.Filters["assignee"] != "none" {
		t.Fatalf("shared %+v", shared)
	}
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"name": "Team", "shared": true}).Want(t, 403)

	var mine view
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"name": "Mine", "filters": map[string]any{"assignee": "me"}}).Want(t, 201).JSON(t, &mine)
	if mine.Shared || mine.Position != 1 {
		t.Fatalf("mine %+v", mine)
	}
	// Validation.
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"name": "Bad", "filters": map[string]any{"status": []string{"nope"}}}).Want(t, 422)
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"name": "Bad", "filters": map[string]any{"assignee": other.Agent.ID.String()}}).Want(t, 422)
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"name": "Bad", "filters": map[string]any{"team_id": ids.New().String()}}).Want(t, 422)
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"name": "Bad", "filters": map[string]any{"channel": "fax"}}).Want(t, 422)
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"filters": map[string]any{}}).Want(t, 422)
	e.Do("POST", "/v1/views", ws.Viewer.As(ws), map[string]any{"name": "V"}).Want(t, 403)
	e.Do("POST", "/v1/views", ws.Agent.As(ws), map[string]any{"name": "Assigned", "filters": map[string]any{"assignee": ws.Admin.ID.String()}}).Want(t, 201)

	// The agent sees shared + own; the admin sees only shared.
	var p page[view]
	e.Do("GET", "/v1/views", ws.Agent.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 3 || p.Data[0].ID != shared.ID {
		t.Fatalf("agent list %+v", p.Data)
	}
	e.Do("GET", "/v1/views", ws.Admin.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 1 {
		t.Fatalf("admin list %+v", p.Data)
	}
	e.Do("GET", "/v1/views/"+mine.ID, ws.Admin.As(ws), nil).Want(t, 404)
	e.Do("PATCH", "/v1/views/"+mine.ID, ws.Admin.As(ws), map[string]any{"name": "x"}).Want(t, 404)
	e.Do("DELETE", "/v1/views/"+mine.ID, ws.Owner.As(ws), nil).Want(t, 404)
	e.Do("GET", "/v1/views/"+shared.ID, other.Owner.As(other), nil).Want(t, 404)

	var mine2 view
	// Agents cannot edit shared views or share their own.
	e.Do("PATCH", "/v1/views/"+shared.ID, ws.Agent.As(ws), map[string]any{"name": "x"}).Want(t, 403)
	e.Do("DELETE", "/v1/views/"+shared.ID, ws.Agent.As(ws), nil).Want(t, 403)
	e.Do("PATCH", "/v1/views/"+mine.ID, ws.Agent.As(ws), map[string]any{"shared": true}).Want(t, 403)
	e.Do("PATCH", "/v1/views/"+mine.ID, ws.Agent.As(ws), map[string]any{"name": "My open", "filters": map[string]any{"status": []string{"open"}}}).Want(t, 200).JSON(t, &mine2)
	if mine2.Name != "My open" || mine2.Filters["assignee"] != nil {
		t.Fatalf("patched %+v", mine2)
	}

	// An admin can share their personal view.
	var adm view
	e.Do("POST", "/v1/views", ws.Admin.As(ws), map[string]any{"name": "Admin own"}).Want(t, 201).JSON(t, &adm)
	e.Do("PATCH", "/v1/views/"+adm.ID, ws.Admin.As(ws), map[string]any{"shared": true}).Want(t, 200).JSON(t, &adm)
	if !adm.Shared {
		t.Fatal("not shared")
	}
	e.Do("GET", "/v1/views/"+adm.ID, ws.Agent.As(ws), nil).Want(t, 200)

	// API keys see and manage shared views only.
	readKey := e.APIKey(ws, "tickets:read")
	e.Do("GET", "/v1/views", hdtest.Auth{Bearer: readKey}, nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 2 {
		t.Fatalf("key list %+v", p.Data)
	}
	e.Do("POST", "/v1/views", hdtest.Auth{Bearer: readKey}, map[string]any{"name": "K", "shared": true}).Want(t, 403)
	setKey := e.APIKey(ws, "settings:write")
	e.Do("POST", "/v1/views", hdtest.Auth{Bearer: setKey}, map[string]any{"name": "K"}).Want(t, 403)
	e.Do("POST", "/v1/views", hdtest.Auth{Bearer: setKey}, map[string]any{"name": "K", "shared": true}).Want(t, 201)
	e.Do("DELETE", "/v1/views/"+shared.ID, hdtest.Auth{Bearer: setKey}, nil).Want(t, 204)

	e.Do("DELETE", "/v1/views/"+mine.ID, ws.Agent.As(ws), nil).Want(t, 204)
	var topics []string
	e.Scalar(`SELECT topics FROM events WHERE workspace_id = $1 AND type = 'view.deleted' AND data->>'view_id' = $2`, &topics, ws.ID, mine.ID)
	if !slices.Contains(topics, "user:"+ws.Agent.ID.String()) {
		t.Fatalf("topics %v", topics)
	}
}

type sla struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Priority             *string `json:"priority"`
	FirstResponseMinutes int     `json:"first_response_minutes"`
	ResolutionMinutes    int     `json:"resolution_minutes"`
	Enabled              bool    `json:"enabled"`
	Position             int     `json:"position"`
}

func TestSLAPolicies(t *testing.T) {
	e := hdtest.New(t, resources.Register)
	ws := e.NewWorkspace()
	other := e.NewWorkspace()
	ad := ws.Admin.As(ws)

	var s sla
	e.Do("POST", "/v1/sla-policies", ad, map[string]any{"name": "Urgent", "priority": "urgent", "first_response_minutes": 30, "resolution_minutes": 240}).Want(t, 201).JSON(t, &s)
	if s.Priority == nil || *s.Priority != "urgent" || !s.Enabled || s.Position != 0 {
		t.Fatalf("created %+v", s)
	}
	var def sla
	e.Do("POST", "/v1/sla-policies", ad, map[string]any{"name": "Default", "first_response_minutes": 60, "resolution_minutes": 1440}).Want(t, 201).JSON(t, &def)
	if def.Priority != nil || def.Position != 1 {
		t.Fatalf("default %+v", def)
	}
	e.Do("POST", "/v1/sla-policies", ad, map[string]any{"name": "Bad", "priority": "meh", "first_response_minutes": 1, "resolution_minutes": 1}).Want(t, 422)
	e.Do("POST", "/v1/sla-policies", ad, map[string]any{"name": "Bad", "first_response_minutes": 0, "resolution_minutes": 1}).Want(t, 422)
	e.Do("POST", "/v1/sla-policies", ad, map[string]any{"name": "Bad"}).Want(t, 422)
	e.Do("POST", "/v1/sla-policies", ws.Agent.As(ws), map[string]any{"name": "A", "first_response_minutes": 1, "resolution_minutes": 1}).Want(t, 403)

	e.Do("PATCH", "/v1/sla-policies/"+s.ID, ad, map[string]any{"priority": nil, "enabled": false}).Want(t, 200).JSON(t, &s)
	if s.Priority != nil || s.Enabled || s.FirstResponseMinutes != 30 {
		t.Fatalf("patched %+v", s)
	}
	e.Do("PATCH", "/v1/sla-policies/"+s.ID, other.Owner.As(other), map[string]any{"enabled": true}).Want(t, 404)
	var p page[sla]
	e.Do("GET", "/v1/sla-policies", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 2 {
		t.Fatal(p)
	}
	e.Do("GET", "/v1/sla-policies", hdtest.Auth{Bearer: e.APIKey(ws, "tickets:read")}, nil).Want(t, 403)
	e.Do("DELETE", "/v1/sla-policies/"+s.ID, other.Owner.As(other), nil).Want(t, 404)
	e.Do("DELETE", "/v1/sla-policies/"+s.ID, ad, nil).Want(t, 204)

	var actions []string
	rows, _ := e.D.DB.Pool.Query(e.Ctx, `SELECT action FROM audit_logs WHERE workspace_id = $1 AND entity_type = 'sla_policy' ORDER BY created_at`, ws.ID)
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	rows.Close()
	if strings.Join(actions, ",") != "sla_policy.created,sla_policy.created,sla_policy.updated,sla_policy.deleted" {
		t.Fatalf("audit %v", actions)
	}
	types, _ := e.Events(ws.ID, 0)
	if !slices.Contains(types, "sla_policy.deleted") {
		t.Fatal(types)
	}
}

type automation struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Trigger    string           `json:"trigger"`
	Conditions []map[string]any `json:"conditions"`
	Actions    []map[string]any `json:"actions"`
	Enabled    bool             `json:"enabled"`
	Position   int              `json:"position"`
	RunCount   int              `json:"run_count"`
}

func TestAutomations(t *testing.T) {
	e := hdtest.New(t, resources.Register)
	ws := e.NewWorkspace()
	other := e.NewWorkspace()
	ad := ws.Admin.As(ws)
	team := ids.New()
	e.Exec(`INSERT INTO teams (id, workspace_id, name) VALUES ($1, $2, 'Billing')`, team, ws.ID)

	var a automation
	e.Do("POST", "/v1/automations", ad, map[string]any{"name": "Refunds", "trigger": "ticket_created",
		"conditions": []map[string]any{{"field": "subject", "op": "contains", "value": "refund"}, {"field": "priority", "op": "equals", "value": "high"}},
		"actions": []map[string]any{{"type": "assign_team", "value": team.String()}, {"type": "add_tag", "value": "billing"},
			{"type": "send_reply", "value": `<p>Thanks<script>x</script></p>`}, {"type": "assign_user", "value": ws.Agent.ID.String()}, {"type": "mark_spam", "value": ""}}}).
		Want(t, 201).JSON(t, &a)
	if !a.Enabled || len(a.Conditions) != 2 || a.Actions[2]["value"] != "<p>Thanks</p>" || a.Actions[4]["value"] != "true" || a.RunCount != 0 {
		t.Fatalf("created %+v", a)
	}
	bad := []map[string]any{
		{"name": "x", "trigger": "nope", "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{}},
		{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{{"type": "explode", "value": ""}}},
		{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{{"type": "set_status", "value": "gone"}}},
		{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{{"type": "assign_user", "value": other.Agent.ID.String()}}},
		{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{{"type": "assign_user", "value": ws.Viewer.ID.String()}}},
		{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{{"type": "assign_team", "value": ids.New().String()}}},
		{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{{"type": "add_note", "value": "<script>x</script>"}}},
		{"name": "x", "trigger": "ticket_created", "conditions": []map[string]any{{"field": "colour", "op": "equals", "value": "x"}}, "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"name": "x", "trigger": "ticket_created", "conditions": []map[string]any{{"field": "status", "op": "equals", "value": "lost"}}, "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"name": "x", "trigger": "ticket_created", "conditions": []map[string]any{{"field": "mailbox_id", "op": "equals", "value": ids.New().String()}}, "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"name": "x", "trigger": "ticket_created", "conditions": []map[string]any{{"field": "assignee_id", "op": "equals", "value": other.Agent.ID.String()}}, "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"trigger": "ticket_created", "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"name": "x", "trigger": "ticket_created", "conditions": []map[string]any{{"field": "subject", "op": "any", "value": " \n \n "}}, "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"name": "x", "trigger": "ticket_created", "conditions": []map[string]any{{"field": "priority", "op": "none", "value": "high"}}, "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
		{"name": "x", "trigger": "ticket_created", "conditions": []map[string]any{{"field": "body", "op": "any", "value": strings.Repeat("x", 5001)}}, "actions": []map[string]any{{"type": "add_tag", "value": "a"}}},
	}
	for i, b := range bad {
		if r := e.Do("POST", "/v1/automations", ad, b); r.Status != 422 {
			t.Errorf("bad[%d]: status %d %s", i, r.Status, r.Body)
		}
	}
	e.Do("POST", "/v1/automations", ws.Agent.As(ws), map[string]any{"name": "x", "trigger": "ticket_created", "actions": []map[string]any{{"type": "add_tag", "value": "a"}}}).Want(t, 403)

	e.Do("PATCH", "/v1/automations/"+a.ID, ad, map[string]any{"enabled": false, "conditions": []map[string]any{}}).Want(t, 200).JSON(t, &a)
	if a.Enabled || len(a.Conditions) != 0 || len(a.Actions) != 5 {
		t.Fatalf("patched %+v", a)
	}
	e.Do("PATCH", "/v1/automations/"+a.ID, ad, map[string]any{"actions": []map[string]any{}}).Want(t, 422)
	e.Do("GET", "/v1/automations/"+a.ID, other.Owner.As(other), nil).Want(t, 404)
	var p page[automation]
	e.Do("GET", "/v1/automations?trigger=ticket_created", ws.Viewer.As(ws), nil).Want(t, 200).JSON(t, &p)
	if len(p.Data) != 1 {
		t.Fatal(p)
	}
	key := e.APIKey(ws, "settings:read")
	e.Do("GET", "/v1/automations", hdtest.Auth{Bearer: key}, nil).Want(t, 200)
	e.Do("DELETE", "/v1/automations/"+a.ID, hdtest.Auth{Bearer: key}, nil).Want(t, 403)
	e.Do("DELETE", "/v1/automations/"+a.ID, ad, nil).Want(t, 204)

	var n int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND entity_type = 'automation' AND actor_user_id = $2`, &n, ws.ID, ws.Admin.ID)
	if n != 3 {
		t.Fatalf("audit rows %d", n)
	}
	types, _ := e.Events(ws.ID, 0)
	for _, w := range []string{"automation.created", "automation.updated", "automation.deleted"} {
		if !slices.Contains(types, w) {
			t.Errorf("missing %s", w)
		}
	}

	// A phrase list is kept one trimmed phrase per line.
	var phrased automation
	e.Do("POST", "/v1/automations", ad, map[string]any{"name": "Carrier mail", "trigger": "ticket_created",
		"conditions": []map[string]any{{"field": "subject", "op": "any", "value": " Your parcel has shipped \r\n\n newsletter \n"}},
		"actions":    []map[string]any{{"type": "set_status", "value": "closed"}}}).Want(t, 201).JSON(t, &phrased)
	if v := phrased.Conditions[0]["value"]; v != "Your parcel has shipped\nnewsletter" {
		t.Fatalf("phrase list stored as %q", v)
	}

	// merge_into_open_ticket: a window of 1..30 days (7 by default), same_subject stored,
	// and only on ticket_created.
	var merge automation
	e.Do("POST", "/v1/automations", ad, map[string]any{"name": "Follow-ups", "trigger": "ticket_created",
		"actions": []map[string]any{{"type": "merge_into_open_ticket", "value": ""}, {"type": "add_tag", "value": "new"}}}).Want(t, 201).JSON(t, &merge)
	if a := merge.Actions[0]; a["value"] != "7" || a["same_subject"] != true {
		t.Fatalf("merge action stored as %v", a)
	}
	if _, ok := merge.Actions[1]["same_subject"]; ok {
		t.Fatalf("same_subject on another action %v", merge.Actions[1])
	}
	e.Do("PATCH", "/v1/automations/"+merge.ID, ad, map[string]any{"actions": []map[string]any{{"type": "merge_into_open_ticket", "value": "14", "same_subject": false}}}).
		Want(t, 200).JSON(t, &merge)
	if a := merge.Actions[0]; a["value"] != "14" || a["same_subject"] != false {
		t.Fatalf("merge action patched to %v", a)
	}
	e.Do("PATCH", "/v1/automations/"+merge.ID, ad, map[string]any{"trigger": "customer_replied"}).Want(t, 422)
	e.Do("PATCH", "/v1/automations/"+merge.ID, ad, map[string]any{"actions": []map[string]any{{"type": "merge_into_open_ticket", "value": "31"}}}).Want(t, 422)
	e.Do("POST", "/v1/automations", ad, map[string]any{"name": "x", "trigger": "ticket_updated",
		"actions": []map[string]any{{"type": "merge_into_open_ticket", "value": "7"}}}).Want(t, 422)
}
