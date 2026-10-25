package core

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/ids"
)

func TestApplyWorkspacePrefs(t *testing.T) {
	cur := defaultWorkspacePrefs()
	s := func(v string) *string { return &v }
	wid := ids.New()

	next, err := applyWorkspacePrefs(cur, workspacePrefsInput{Mode: s("separate"), Layout: s("mixed"),
		Order: &[]uuid.UUID{wid, wid}, Collapsed: &[]string{"everywhere", wid.String()},
		Items: &WorkspacePrefItems{Hidden: []string{wid.String() + "/view:abc-1", "everywhere/mine"}}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Mode != ModeSeparate || !next.ModeSet || next.Layout != LayoutMixed || len(next.Order) != 1 || len(next.Collapsed) != 2 ||
		len(next.Items.Hidden) != 2 || next.Items.Order == nil || !next.Everywhere {
		t.Fatalf("applied: %+v", next)
	}
	// Left out keeps; "" goes back to the default.
	back, err := applyWorkspacePrefs(next, workspacePrefsInput{Mode: s("")})
	if err != nil || back.Mode != ModeTogether || back.ModeSet || back.Layout != LayoutMixed {
		t.Fatalf("reset mode: %+v %v", back, err)
	}

	many := make([]uuid.UUID, 101)
	for i := range many {
		many[i] = ids.New()
	}
	for name, in := range map[string]workspacePrefsInput{
		"mode":          {Mode: s("both")},
		"layout":        {Layout: s("tree")},
		"order":         {Order: &many},
		"hidden":        {Hidden: &many},
		"collapsed":     {Collapsed: &[]string{"nope"}},
		"item id":       {Items: &WorkspacePrefItems{Hidden: []string{"view:1"}}},
		"item injected": {Items: &WorkspacePrefItems{Order: []string{wid.String() + "/view:<script>"}}},
		"item other":    {Items: &WorkspacePrefItems{Order: []string{"someone/view:1"}}},
	} {
		if _, err := applyWorkspacePrefs(cur, in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestWorkspacePrefsRoutes(t *testing.T) {
	e := newEnv(t)
	u, _ := e.signup("prefs")
	// A person's own settings: no workspace needed.
	r := u.do("GET", "/v1/me/workspace-prefs", nil)
	if r.Status != 200 || r.str("mode") != "together" || r.str("layout") != "grouped" || r.Body["mode_set"] != false || r.Body["everywhere"] != true {
		t.Fatalf("defaults: %d %s", r.Status, r.Raw)
	}
	wid := ids.New().String()
	r = u.do("PUT", "/v1/me/workspace-prefs", map[string]any{"mode": "separate", "order": []string{wid}, "items": map[string]any{"hidden": []string{wid + "/team:x1"}}})
	if r.Status != 200 || r.str("mode") != "separate" || r.Body["mode_set"] != true {
		t.Fatalf("put: %d %s", r.Status, r.Raw)
	}
	r = u.do("PUT", "/v1/me/workspace-prefs", map[string]any{"layout": "mixed", "everywhere": false})
	if r.Status != 200 {
		t.Fatalf("put 2: %d %s", r.Status, r.Raw)
	}
	r = u.do("GET", "/v1/me/workspace-prefs", nil)
	if r.str("mode") != "separate" || r.str("layout") != "mixed" || r.Body["everywhere"] != false {
		t.Fatalf("kept: %s", r.Raw)
	}
	if order, _ := r.Body["order"].([]any); len(order) != 1 || order[0] != wid {
		t.Fatalf("order: %s", r.Raw)
	}
	if r := u.do("PUT", "/v1/me/workspace-prefs", map[string]any{"mode": "both"}); r.Status != 422 {
		t.Fatalf("bad mode: %d %s", r.Status, r.Raw)
	}
	// Someone else's settings stay theirs.
	o, _ := e.signup("other")
	if r := o.do("GET", "/v1/me/workspace-prefs", nil); r.str("mode") != "together" || r.Body["mode_set"] != false {
		t.Fatalf("leaked: %s", r.Raw)
	}
	if r := e.client().do("GET", "/v1/me/workspace-prefs", nil); r.Status != 401 {
		t.Fatalf("anonymous: %d", r.Status)
	}
}

// togetherWorld: Uma owns W1, is a viewer in W2 (member of private team Legal, not of
// private team Board), an agent in suspended W3; Otto alone is in W4.
type togetherWorld struct {
	u                  *client
	w1, w2, w3, w4     string
	legal, board, open string
}

func newTogetherWorld(t *testing.T, e *testEnv) togetherWorld {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.d.DB.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	u, _ := e.signup("uma")
	o, _ := e.signup("otto")
	w := togetherWorld{u: u, w1: u.newWorkspace("Uma Co"), w2: o.newWorkspace("Otto Co"), w3: o.newWorkspace("Paused Co"), w4: o.newWorkspace("Otto Only")}
	u.workspace = w.w1
	var uid uuid.UUID
	if err := e.d.DB.Pool.QueryRow(ctx, `SELECT user_id FROM members WHERE workspace_id = $1`, w.w1).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $3, 'viewer'), ($2, $3, 'agent')`, w.w2, w.w3, uid)
	exec(`UPDATE workspaces SET suspended_at = now() WHERE id = $1`, w.w3)
	w.legal, w.board, w.open = ids.New().String(), ids.New().String(), ids.New().String()
	exec(`INSERT INTO teams (id, workspace_id, name, private) VALUES ($1, $4, 'Legal', true), ($2, $4, 'Board', true), ($3, $4, 'Support', false)`,
		w.legal, w.board, w.open, w.w2)
	exec(`INSERT INTO team_members (team_id, user_id) VALUES ($1, $2)`, w.legal, uid)
	exec(`INSERT INTO notifications (workspace_id, user_id, type) VALUES ($1, $2, 'mentioned'), ($1, $2, 'assigned'), ($3, $2, 'mentioned')`, w.w1, uid, w.w2)
	exec(`INSERT INTO notifications (workspace_id, user_id, type, read_at) VALUES ($1, $2, 'mentioned', now())`, w.w1, uid)
	// Notifications from a workspace Uma is not in never count (none should exist, but the join guards it).
	exec(`INSERT INTO notifications (workspace_id, user_id, type) VALUES ($1, $2, 'mentioned')`, w.w4, uid)
	return w
}

func TestOverview(t *testing.T) {
	e := newEnv(t)
	// The areas' parts are what their routes answer for the principal they get: here, who it is.
	type seen struct {
		Workspace string   `json:"workspace"`
		Role      string   `json:"role"`
		Hidden    []string `json:"hidden"`
	}
	e.d.Overview.TicketCounts = func(ctx context.Context, p *auth.Principal) (any, error) {
		hidden, err := domain.HiddenTeams(ctx, e.d.DB.Pool, p)
		if err != nil {
			return nil, err
		}
		out := seen{Workspace: p.WorkspaceID.String(), Role: string(p.Role), Hidden: []string{}}
		for _, h := range hidden {
			out.Hidden = append(out.Hidden, h.String())
		}
		return out, nil
	}
	var mailFor []uuid.UUID
	e.d.Overview.MailCounts = func(_ context.Context, p *auth.Principal) (any, error) {
		mailFor = append(mailFor, p.WorkspaceID)
		return nil, nil // a workspace without Collab
	}
	w := newTogetherWorld(t, e)

	// No OD-Workspace needed; one sent changes nothing.
	w.u.workspace = ""
	r := w.u.do("GET", "/v1/me/overview", nil)
	if r.Status != 200 {
		t.Fatalf("overview: %d %s", r.Status, r.Raw)
	}
	var body struct {
		Workspaces []struct {
			ID                  string          `json:"id"`
			Name                string          `json:"name"`
			Role                string          `json:"role"`
			Suspended           bool            `json:"suspended"`
			UnreadNotifications int             `json:"unread_notifications"`
			TicketCounts        *seen           `json:"ticket_counts"`
			MailCounts          json.RawMessage `json:"mail_counts"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(r.Raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Workspaces) != 3 {
		t.Fatalf("want W1, W2, W3 only: %s", r.Raw)
	}
	byID := map[string]int{}
	for i, x := range body.Workspaces {
		byID[x.ID] = i
	}
	if _, ok := byID[w.w4]; ok {
		t.Fatal("someone else's workspace listed")
	}
	one, two, three := body.Workspaces[byID[w.w1]], body.Workspaces[byID[w.w2]], body.Workspaces[byID[w.w3]]
	if one.Role != "owner" || one.UnreadNotifications != 2 || one.TicketCounts == nil || one.TicketCounts.Workspace != w.w1 || one.TicketCounts.Role != "owner" {
		t.Fatalf("W1: %+v", one)
	}
	// W2 with Uma's role there and W2's private teams: Board hidden, Legal (hers) not.
	if two.Role != "viewer" || two.UnreadNotifications != 1 || two.TicketCounts == nil || two.TicketCounts.Workspace != w.w2 || two.TicketCounts.Role != "viewer" {
		t.Fatalf("W2: %+v", two)
	}
	if !slices.Equal(two.TicketCounts.Hidden, []string{w.board}) {
		t.Fatalf("W2 hidden teams: %v (board %s, legal %s)", two.TicketCounts.Hidden, w.board, w.legal)
	}
	if len(one.TicketCounts.Hidden) != 0 {
		t.Fatalf("W1 hides W2's teams: %v", one.TicketCounts.Hidden)
	}
	if string(two.MailCounts) != "null" {
		t.Fatalf("W2 without Collab: %s", two.MailCounts)
	}
	// Suspended: listed, nothing loaded from it.
	if !three.Suspended || three.TicketCounts != nil || three.UnreadNotifications != 0 || string(three.MailCounts) != "null" {
		t.Fatalf("W3: %+v", three)
	}
	if len(mailFor) != 2 || slices.Contains(mailFor, uuid.MustParse(w.w3)) {
		t.Fatalf("mail parts ran for %v", mailFor)
	}
	if r := e.client().do("GET", "/v1/me/overview", nil); r.Status != 401 {
		t.Fatalf("anonymous: %d", r.Status)
	}
}

func TestRealtimeTokenAcrossWorkspaces(t *testing.T) {
	e := newEnv(t)
	w := newTogetherWorld(t, e)
	claims := func(path string) *auth.Claims {
		t.Helper()
		r := w.u.do("POST", path, nil)
		if r.Status != 200 {
			t.Fatalf("%s: %d %s", path, r.Status, r.Raw)
		}
		c, err := e.d.Tokens.Verify(r.str("token"), auth.PurposeWS)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// The default token is the focus workspace's alone, as before.
	if c := claims("/v1/realtime/token"); c.Workspace != w.w1 || c.Workspaces != nil {
		t.Fatalf("single: %+v", c)
	}
	c := claims("/v1/realtime/token?all_workspaces=true")
	if c.Workspace != w.w1 || c.Role != "owner" || len(c.Teams) != 0 {
		t.Fatalf("focus: %+v", c)
	}
	// W2 with Uma's role and only the private team she is on; never the suspended W3 or
	// Otto's W4; the focus is the token's own wid, not repeated.
	if len(c.Workspaces) != 1 {
		t.Fatalf("workspaces: %+v", c.Workspaces)
	}
	two, ok := c.Workspaces[w.w2]
	if !ok || two.Role != "viewer" || !slices.Equal(two.Teams, []string{w.legal}) {
		t.Fatalf("W2 entry: %+v", c.Workspaces)
	}
	// Focus on W2: W1 is the other one, with no private teams.
	w.u.workspace = w.w2
	c = claims("/v1/realtime/token?all_workspaces=true")
	if c.Workspace != w.w2 || !slices.Equal(c.Teams, []string{w.legal}) || len(c.Workspaces) != 1 || c.Workspaces[w.w1].Role != "owner" {
		t.Fatalf("focus W2: %+v", c)
	}
}
