package core

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// privateTeam creates a private team as the owner with members (user id → role).
func (f *teamFixture) privateTeam(name string, members map[uuid.UUID]string) uuid.UUID {
	f.e.t.Helper()
	list := []map[string]any{}
	for id, role := range members {
		list = append(list, map[string]any{"user_id": id, "role": role})
	}
	r := f.owner.do("POST", "/v1/teams", map[string]any{"name": name, "private": true, "members": list})
	if r.Status != 201 {
		f.e.t.Fatalf("create %s: %d %s", name, r.Status, r.Raw)
	}
	return uuid.MustParse(r.str("id"))
}

func (f *teamFixture) assignee(tk uuid.UUID) *uuid.UUID {
	f.e.t.Helper()
	var a *uuid.UUID
	f.scalar(`SELECT assignee_id FROM tickets WHERE id = $1`, &a, tk)
	return a
}

// keyClient is a caller with an API key of the fixture's workspace.
func (f *teamFixture) keyClient(scopes ...string) (*client, uuid.UUID) {
	f.e.t.Helper()
	r := f.owner.do("POST", "/v1/api-keys", map[string]any{"name": "Key", "scopes": scopes})
	if r.Status != 201 {
		f.e.t.Fatalf("key: %d %s", r.Status, r.Raw)
	}
	c := f.e.client()
	c.token = r.str("secret")
	return c, uuid.MustParse(r.str("id"))
}

// A private team's tickets are assigned only to its members: leaving the team, being
// removed from it or from the workspace, or the team becoming private unassigns them.
func TestPrivateTeamAssigneeLeaves(t *testing.T) {
	f := newTeamFixture(t)
	team := f.privateTeam("Security", map[uuid.UUID]string{f.ownerID: "lead", f.agentID: "member", f.otherID: "member"})
	open := f.ticket(&team, &f.agentID)
	solved := f.ticket(&team, &f.agentID)
	f.exec(`UPDATE tickets SET status = 'solved' WHERE id = $1`, solved)

	if r := f.agent.do("POST", "/v1/teams/"+team.String()+"/leave", nil); r.Status != 204 {
		t.Fatalf("leave: %d %s", r.Status, r.Raw)
	}
	if f.assignee(open) != nil {
		t.Fatal("leaver still assigned an open ticket of the private team")
	}
	if a := f.assignee(solved); a == nil || *a != f.agentID {
		t.Fatal("a solved ticket was unassigned")
	}
	var n int
	f.scalar(`SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'assigned' AND data->>'from' = $2 AND data->'to' = 'null'`,
		&n, open, f.agentID.String())
	if n != 1 {
		t.Fatal("no assigned activity")
	}
	f.scalar(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'ticket.updated' AND data->'ticket'->>'id' = $2
		AND team_id = $3`, &n, f.wid, open.String(), team)
	if n != 1 {
		t.Fatal("no ticket.updated on the team's topic")
	}

	// Removed by the lead through PATCH members.
	tk := f.ticket(&team, &f.otherID)
	if r := f.owner.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"members": []map[string]any{{"user_id": f.ownerID, "role": "lead"}}}); r.Status != 200 {
		t.Fatalf("patch members: %d %s", r.Status, r.Raw)
	}
	if f.assignee(tk) != nil {
		t.Fatal("removed member still assigned")
	}

	// Removed from the workspace.
	if r := f.owner.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"members": []map[string]any{
		{"user_id": f.ownerID, "role": "lead"}, {"user_id": f.otherID, "role": "member"}}}); r.Status != 200 {
		t.Fatalf("re-add: %d %s", r.Status, r.Raw)
	}
	tk = f.ticket(&team, &f.otherID)
	if r := f.owner.do("DELETE", "/v1/members/"+f.otherID.String(), nil); r.Status != 204 {
		t.Fatalf("remove member: %d %s", r.Status, r.Raw)
	}
	if f.assignee(tk) != nil {
		t.Fatal("member removed from the workspace still assigned")
	}

	// A public team made private unassigns those not on it.
	pub := f.owner.do("POST", "/v1/teams", map[string]any{"name": "Billing"})
	pid := uuid.MustParse(pub.str("id"))
	tk = f.ticket(&pid, &f.agentID)
	if r := f.owner.do("PATCH", "/v1/teams/"+pid.String(), map[string]any{"private": true}); r.Status != 200 {
		t.Fatalf("privatize: %d %s", r.Status, r.Raw)
	}
	if f.assignee(tk) != nil {
		t.Fatal("assignee off the team kept a ticket of a team made private")
	}
}

// Deleting a private team never leaves its tickets without a team (visible to everyone).
func TestDeletePrivateTeamNeedsMoveTo(t *testing.T) {
	f := newTeamFixture(t)
	team := f.privateTeam("Legal", map[uuid.UUID]string{f.ownerID: "lead"})
	dst := f.privateTeam("Legal 2", map[uuid.UUID]string{f.ownerID: "lead"})
	open := f.ticket(&team, nil)
	closed := f.ticket(&team, nil)
	f.exec(`UPDATE tickets SET status = 'closed' WHERE id = $1`, closed)

	r := f.owner.do("DELETE", "/v1/teams/"+team.String(), nil)
	if r.Status != 422 || r.str("code") != "move_required" || !strings.Contains(r.str("detail"), "2 tickets") {
		t.Fatalf("delete without move_to: %d %s", r.Status, r.Raw)
	}
	if r := f.owner.do("DELETE", "/v1/teams/"+team.String()+"?move_to="+dst.String(), nil); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	for _, tk := range []uuid.UUID{open, closed} {
		var got *uuid.UUID
		f.scalar(`SELECT team_id FROM tickets WHERE id = $1`, &got, tk)
		if got == nil || *got != dst {
			t.Fatalf("ticket %s team %v", tk, got)
		}
	}
	var moved float64
	f.scalar(`SELECT (data->>'tickets_moved')::float FROM audit_logs WHERE workspace_id = $1 AND action = 'team.deleted'`, &moved, f.wid)
	if moved != 2 {
		t.Fatalf("audited %v moved", moved)
	}
	// A private team without tickets goes without move_to.
	empty := f.privateTeam("Empty", map[uuid.UUID]string{f.ownerID: "lead"})
	if r := f.owner.do("DELETE", "/v1/teams/"+empty.String(), nil); r.Status != 204 {
		t.Fatalf("delete empty: %d %s", r.Status, r.Raw)
	}
}

// API keys change a private team's privacy and members only when granted it; grants go
// when the team becomes public or is deleted.
func TestPrivateTeamKeysAndGrantHygiene(t *testing.T) {
	f := newTeamFixture(t)
	team := f.privateTeam("Security", map[uuid.UUID]string{f.ownerID: "lead"})
	key, keyID := f.keyClient("settings:write", "settings:read")

	if r := key.do("POST", "/v1/teams", map[string]any{"name": "Shadow", "private": true}); r.Status != 403 {
		t.Fatalf("key created a private team: %d %s", r.Status, r.Raw)
	}
	if r := key.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"members": []map[string]any{{"user_id": f.agentID, "role": "member"}}}); r.Status != 403 {
		t.Fatalf("key changed a private team's members: %d %s", r.Status, r.Raw)
	}
	if r := key.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"private": false}); r.Status != 403 {
		t.Fatalf("key made a private team public: %d %s", r.Status, r.Raw)
	}
	if r := key.do("DELETE", "/v1/teams/"+team.String(), nil); r.Status != 403 {
		t.Fatalf("key deleted a private team: %d %s", r.Status, r.Raw)
	}
	pub := f.owner.do("POST", "/v1/teams", map[string]any{"name": "Billing"})
	if r := key.do("PATCH", "/v1/teams/"+pub.str("id"), map[string]any{"private": true}); r.Status != 403 {
		t.Fatalf("key made a team private: %d %s", r.Status, r.Raw)
	}
	if r := key.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"description": "Incidents"}); r.Status != 200 {
		t.Fatalf("key edit: %d %s", r.Status, r.Raw)
	}

	// Granted, the key may.
	if r := f.owner.do("PATCH", "/v1/api-keys/"+keyID.String(), map[string]any{"private_team_ids": []string{team.String()}}); r.Status != 200 {
		t.Fatalf("grant: %d %s", r.Status, r.Raw)
	}
	recv := httptest.NewServer(echoChallenges(nil))
	defer recv.Close()
	hook := f.owner.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL, "private_team_ids": []string{team.String()}})
	if hook.Status != 201 {
		t.Fatalf("webhook: %d %s", hook.Status, hook.Raw)
	}
	if r := key.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"members": []map[string]any{{"user_id": f.ownerID, "role": "lead"},
		{"user_id": f.agentID, "role": "member"}}}); r.Status != 200 {
		t.Fatalf("granted key members: %d %s", r.Status, r.Raw)
	}

	// Public, then private again: the grants are gone.
	if r := f.owner.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"private": false}); r.Status != 200 {
		t.Fatalf("public: %d %s", r.Status, r.Raw)
	}
	if r := f.owner.do("PATCH", "/v1/teams/"+team.String(), map[string]any{"private": true}); r.Status != 200 {
		t.Fatalf("private: %d %s", r.Status, r.Raw)
	}
	var n int
	f.scalar(`SELECT cardinality(private_team_ids) FROM api_keys WHERE id = $1`, &n, keyID)
	if n != 0 {
		t.Fatal("key grant survived the team going public")
	}
	f.scalar(`SELECT cardinality(private_team_ids) FROM webhooks WHERE id = $1`, &n, uuid.MustParse(hook.str("id")))
	if n != 0 {
		t.Fatal("webhook grant survived the team going public")
	}

	// Deleting a team drops it from grants too.
	other := f.privateTeam("Legal", map[uuid.UUID]string{f.ownerID: "lead"})
	if r := f.owner.do("PATCH", "/v1/api-keys/"+keyID.String(), map[string]any{"private_team_ids": []string{other.String()}}); r.Status != 200 {
		t.Fatalf("grant: %d %s", r.Status, r.Raw)
	}
	if r := f.owner.do("DELETE", "/v1/teams/"+other.String(), nil); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	f.scalar(`SELECT cardinality(private_team_ids) FROM api_keys WHERE id = $1`, &n, keyID)
	if n != 0 {
		t.Fatal("key grant survived the team's deletion")
	}
}

// A webhook granted private teams sends their tickets to its URL: only someone on every one
// of them may point it elsewhere, widen its events or turn it back on.
func TestGrantedWebhookChanges(t *testing.T) {
	f := newTeamFixture(t)
	admin, _ := f.e.memberOf(f.wid, "hookadmin", "admin")
	team := f.privateTeam("Security", map[uuid.UUID]string{f.ownerID: "lead"})
	recv := httptest.NewServer(echoChallenges(nil))
	defer recv.Close()
	r := f.owner.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL, "events": []string{"ticket.created"},
		"private_team_ids": []string{team.String()}})
	if r.Status != 201 {
		t.Fatalf("webhook: %d %s", r.Status, r.Raw)
	}
	path := "/v1/webhooks/" + r.str("id")
	if r := admin.do("PATCH", path, map[string]any{"url": recv.URL + "/elsewhere"}); r.Status != 403 {
		t.Fatalf("admin off the team moved the URL: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PATCH", path, map[string]any{"events": []string{"ticket.created", "message.created"}}); r.Status != 403 {
		t.Fatalf("admin off the team widened events: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PATCH", path, map[string]any{"events": []string{"*"}}); r.Status != 403 {
		t.Fatalf("admin off the team subscribed to everything: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PATCH", path, map[string]any{"enabled": false, "description": "Paused"}); r.Status != 200 {
		t.Fatalf("disable: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PATCH", path, map[string]any{"enabled": true}); r.Status != 403 {
		t.Fatalf("admin off the team re-enabled: %d %s", r.Status, r.Raw)
	}
	// Dropping the grant first makes the rest anyone's call.
	if r := admin.do("PATCH", path, map[string]any{"private_team_ids": []string{}, "url": recv.URL + "/elsewhere"}); r.Status != 200 {
		t.Fatalf("drop and move: %d %s", r.Status, r.Raw)
	}
	r = f.owner.do("POST", "/v1/webhooks", map[string]any{"url": recv.URL, "private_team_ids": []string{team.String()}})
	if r := f.owner.do("PATCH", "/v1/webhooks/"+r.str("id"), map[string]any{"url": recv.URL + "/new", "enabled": true}); r.Status != 200 {
		t.Fatalf("member change: %d %s", r.Status, r.Raw)
	}
}

// A notification about a ticket the user can no longer see names no ticket and keeps none
// of its content.
func TestNotificationOfHiddenTicket(t *testing.T) {
	f := newTeamFixture(t)
	team := f.privateTeam("Security", map[uuid.UUID]string{f.ownerID: "lead", f.agentID: "member"})
	tk := f.ticket(&team, nil)
	f.exec(`INSERT INTO notifications (id, workspace_id, user_id, type, ticket_id, data) VALUES ($1, $2, $3, 'mentioned', $4, $5)`,
		uuid.New(), f.wid, f.agentID, tk, map[string]any{"preview": "the breach is in", "by": f.ownerID.String(), "message_id": uuid.NewString()})
	list := func() map[string]any {
		r := f.agent.do("GET", "/v1/notifications", nil)
		data, _ := r.Body["data"].([]any)
		if r.Status != 200 || len(data) != 1 {
			t.Fatalf("notifications: %d %s", r.Status, r.Raw)
		}
		return data[0].(map[string]any)
	}
	if n := list(); n["ticket"] == nil {
		t.Fatal("a member lost the ticket")
	}
	if r := f.agent.do("POST", "/v1/teams/"+team.String()+"/leave", nil); r.Status != 204 {
		t.Fatalf("leave: %d", r.Status)
	}
	n := list()
	data, _ := n["data"].(map[string]any)
	if n["ticket"] != nil || data["preview"] != nil || data["message_id"] != nil || data["by"] != f.ownerID.String() {
		t.Fatalf("hidden ticket notification %v", n)
	}
}
