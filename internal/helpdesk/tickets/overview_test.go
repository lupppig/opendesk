package tickets_test

import (
	"encoding/json"
	"testing"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/ids"
)

// The overview's ticket counts for a workspace are exactly what GET /v1/tickets/counts
// answers the same person there: their role and that workspace's private teams.
func TestOverviewTicketCounts(t *testing.T) {
	e, home := newEnv(t)
	other := e.NewWorkspace()
	// Anna (an agent at home) is a viewer in the other workspace, not on its private team.
	e.Exec(`INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'viewer')`, other.ID, home.Agent.ID)
	private := ids.New()
	e.Exec(`INSERT INTO teams (id, workspace_id, name, private) VALUES ($1, $2, 'Board', true)`, private, other.ID)
	e.Exec(`INSERT INTO team_members (team_id, user_id) VALUES ($1, $2)`, private, other.Agent.ID)
	createTicket(t, e, other, map[string]any{"subject": "Open to all"})
	createTicket(t, e, other, map[string]any{"subject": "Board only", "team_id": private})
	createTicket(t, e, home, map[string]any{"subject": "At home"})

	part := e.D.Overview.TicketCounts
	if part == nil {
		t.Fatal("tickets did not register its overview part")
	}
	p := &auth.Principal{Kind: auth.KindUser, UserID: home.Agent.ID, WorkspaceID: other.ID, Role: auth.RoleViewer}
	got, err := part(e.Ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	var want map[string]any
	e.Do("GET", "/v1/tickets/counts", home.Agent.As(other), nil).Want(t, 200).JSON(t, &want)
	wantJSON, _ := json.Marshal(want)
	var a, b map[string]any
	_ = json.Unmarshal(gotJSON, &a)
	_ = json.Unmarshal(wantJSON, &b)
	delete(b, "$schema")
	if string(mustJSON(a)) != string(mustJSON(b)) {
		t.Fatalf("overview %s\nroute    %s", gotJSON, wantJSON)
	}
	// The Board is not one of her rows there, and its ticket is in none of her totals.
	if teams, _ := a["teams"].(map[string]any); teams[private.String()] != nil {
		t.Fatalf("private team listed: %v", teams)
	}
	if a["unassigned"] != float64(1) || a["open"] != float64(1) {
		t.Fatalf("totals count the private team's ticket: unassigned %v open %v", a["unassigned"], a["open"])
	}
	// Someone on the Board counts it.
	var lead map[string]any
	e.Do("GET", "/v1/tickets/counts", other.Agent.As(other), nil).Want(t, 200).JSON(t, &lead)
	if lead["unassigned"] != float64(2) {
		t.Fatalf("a member of the team does not count its ticket: %v", lead["unassigned"])
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
