package tickets_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/ids"
)

// agentsFixture seeds March 2026 (UTC) with exact timestamps:
//
//	t1  Anna, created assigned to her 03-02 10:00, first reply 10:30, solved 12:00
//	t2  created 03-03 09:00 unassigned, assigned to Anna 09:10, first reply 10:00, solved 11:10, second brand
//	t3  created 02-20 for Adam, moved to Anna 03-05 08:00, solved 08:30
//	t4  created 03-10 10:00 for Adam, moved to Bea 03-11 10:00, first reply 10:20 on 03-10, team, open
//	spam and deleted tickets of Anna's, solved in March, count nowhere.
type agentsFixture struct {
	ws          hdtest.WS
	bea, carl   hdtest.User
	team, brand uuid.UUID
}

func seedAgents(t *testing.T, e *hdtest.Env) agentsFixture {
	t.Helper()
	ws := e.NewWorkspace()
	e.SetSettings(ws, map[string]any{"timezone": "UTC"})
	f := agentsFixture{ws: ws, bea: e.AddUser(ws, auth.RoleAgent, "Bea Agent"), carl: e.AddUser(ws, auth.RoleAgent, "Carl Agent")}
	f.team = ids.New()
	e.Exec(`INSERT INTO teams (id, workspace_id, name) VALUES ($1, $2, 'Billing')`, f.team, ws.ID)
	e.Exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'member'), ($1, $3, 'observer')`, f.team, f.bea.ID, f.carl.ID)
	f.brand = ids.New()
	e.Exec(`INSERT INTO brands (id, workspace_id, name, slug) VALUES ($1, $2, 'Second', $3)`, f.brand, ws.ID, ws.Slug+"-second")

	n := 0
	ticket := func(assignee *uuid.UUID, created string, firstReply, solved any, extra string) uuid.UUID {
		n++
		id := ids.New()
		status := "open"
		if solved != nil {
			status = "solved"
		}
		e.Exec(`INSERT INTO tickets (id, workspace_id, number, brand_id, channel, assignee_id, status, created_at, first_responded_at, solved_at)
			VALUES ($1, $2, $3, $4, 'email', $5, $6, $7, $8, $9)`, id, ws.ID, n, ws.BrandID, assignee, status, created, firstReply, solved)
		if extra != "" {
			e.Exec(`UPDATE tickets SET `+extra+` WHERE id = $1`, id)
		}
		return id
	}
	assigned := func(tid uuid.UUID, from, to *uuid.UUID, at string) {
		data := map[string]any{"from": nil, "to": nil}
		if from != nil {
			data["from"] = from.String()
		}
		if to != nil {
			data["to"] = to.String()
		}
		e.Exec(`INSERT INTO activities (workspace_id, ticket_id, actor_type, type, data, created_at) VALUES ($1, $2, 'system', 'assigned', $3, $4)`,
			ws.ID, tid, data, at)
	}
	message := func(tid uuid.UUID, author uuid.UUID, kind, direction, at, extra string) {
		id := ids.New()
		e.Exec(`INSERT INTO messages (id, workspace_id, ticket_id, kind, direction, author_type, author_user_id, created_at)
			VALUES ($1, $2, $3, $4, $5, 'user', $6, $7)`, id, ws.ID, tid, kind, direction, author, at)
		if extra != "" {
			e.Exec(`UPDATE messages SET `+extra+` WHERE id = $1`, id)
		}
	}
	anna, adam := ws.Agent.ID, ws.Admin.ID

	t1 := ticket(&anna, "2026-03-02T10:00:00Z", "2026-03-02T10:30:00Z", "2026-03-02T12:00:00Z", "")
	message(t1, anna, "reply", "outbound", "2026-03-02T10:30:00Z", "")
	message(t1, anna, "note", "outbound", "2026-03-02T11:00:00Z", "")
	message(t1, anna, "reply", "outbound", "2026-03-02T11:30:00Z", "")
	// A message in a third-party thread is neither a reply nor a note.
	thread := ids.New()
	e.Exec(`INSERT INTO ticket_threads (id, workspace_id, ticket_id, subject) VALUES ($1, $2, $3, 'Supplier')`, thread, ws.ID, t1)
	message(t1, anna, "note", "outbound", "2026-03-02T11:40:00Z", "thread_id = '"+thread.String()+"'")
	// The customer's message is not an agent's.
	e.Exec(`INSERT INTO messages (workspace_id, ticket_id, kind, direction, author_type, created_at) VALUES ($1, $2, 'reply', 'inbound', 'contact', '2026-03-02T10:00:00Z')`, ws.ID, t1)

	t2 := ticket(&anna, "2026-03-03T09:00:00Z", "2026-03-03T10:00:00Z", "2026-03-03T11:10:00Z", "brand_id = '"+f.brand.String()+"'")
	assigned(t2, nil, &anna, "2026-03-03T09:10:00Z")
	message(t2, anna, "reply", "outbound", "2026-03-03T10:00:00Z", "")
	message(t2, anna, "note", "outbound", "2026-03-03T10:05:00Z", "deleted_at = now()")

	t3 := ticket(&anna, "2026-02-20T09:00:00Z", "2026-02-20T09:30:00Z", "2026-03-05T08:30:00Z", "")
	assigned(t3, nil, &adam, "2026-02-20T09:05:00Z")
	assigned(t3, &adam, &anna, "2026-03-05T08:00:00Z")
	message(t3, anna, "reply", "outbound", "2026-02-21T09:00:00Z", "")
	message(t3, anna, "reply", "outbound", "2026-03-05T08:20:00Z", "")

	bea := f.bea.ID
	t4 := ticket(&bea, "2026-03-10T10:00:00Z", "2026-03-10T10:20:00Z", nil, "team_id = '"+f.team.String()+"'")
	assigned(t4, &adam, &bea, "2026-03-11T10:00:00Z")
	message(t4, bea, "note", "outbound", "2026-03-11T10:05:00Z", "")
	message(t4, bea, "reply", "outbound", "2026-03-11T10:10:00Z", "")

	spam := ticket(&anna, "2026-03-04T09:00:00Z", "2026-03-04T09:10:00Z", "2026-03-04T10:00:00Z", "spam = true")
	message(spam, anna, "reply", "outbound", "2026-03-04T09:10:00Z", "")
	gone := ticket(&anna, "2026-03-04T09:00:00Z", "2026-03-04T09:10:00Z", "2026-03-04T10:00:00Z", "deleted_at = now()")
	message(gone, anna, "reply", "outbound", "2026-03-04T09:10:00Z", "")

	// Online: Anna 2 h on 03-02 and 1 h of a stretch across the start of March; Adam 45 min.
	e.Exec(`INSERT INTO agent_presence (workspace_id, user_id, started_at, ended_at, open) VALUES
		($1, $2, '2026-03-02T09:00:00Z', '2026-03-02T11:00:00Z', false),
		($1, $2, '2026-02-28T23:00:00Z', '2026-03-01T01:00:00Z', false),
		($1, $2, '2026-01-10T09:00:00Z', '2026-01-10T17:00:00Z', false),
		($1, $3, '2026-03-10T08:00:00Z', '2026-03-10T08:45:00Z', false)`, ws.ID, anna, adam)
	return f
}

const march = "from=2026-03-01&to=2026-03-31"

func agentsReport(t *testing.T, e *hdtest.Env, a hdtest.Auth, query string) tickets.ReportAgents {
	t.Helper()
	var r tickets.ReportAgents
	e.Do("GET", "/v1/reports/agents?"+query, a, nil).Want(t, 200).JSON(t, &r)
	return r
}

func rowOf(t *testing.T, r tickets.ReportAgents, id uuid.UUID) *tickets.ReportAgentRow {
	t.Helper()
	for i := range r.Data {
		if r.Data[i].User.ID == id {
			return &r.Data[i]
		}
	}
	return nil
}

func eq(p *float64, want float64) bool { return p != nil && *p == want }

func TestAgentsReport(t *testing.T) {
	e, _ := newEnv(t)
	f := seedAgents(t, e)
	ws := f.ws
	// Another workspace Anna also works in counts nowhere here.
	other := e.NewWorkspace()
	e.Exec(`INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'agent')`, other.ID, ws.Agent.ID)
	elsewhere := ids.New()
	e.Exec(`INSERT INTO tickets (id, workspace_id, number, brand_id, channel, assignee_id, status, created_at, first_responded_at, solved_at)
		VALUES ($1, $2, 1, $3, 'email', $4, 'solved', '2026-03-02T10:00:00Z', '2026-03-02T10:01:00Z', '2026-03-02T10:02:00Z')`, elsewhere, other.ID, other.BrandID, ws.Agent.ID)
	e.Exec(`INSERT INTO messages (workspace_id, ticket_id, kind, direction, author_type, author_user_id, created_at)
		VALUES ($1, $2, 'reply', 'outbound', 'user', $3, '2026-03-02T10:01:00Z')`, other.ID, elsewhere, ws.Agent.ID)
	e.Exec(`INSERT INTO agent_presence (workspace_id, user_id, started_at, ended_at, open) VALUES ($1, $2, '2026-03-02T08:00:00Z', '2026-03-02T13:00:00Z', false)`,
		other.ID, ws.Agent.ID)

	r := agentsReport(t, e, ws.Admin.As(ws), march)
	if r.OnlineTime != "all" {
		t.Fatalf("admin online_time %q", r.OnlineTime)
	}
	// Owners, admins and agents, busiest first; the viewer is not one of them.
	if len(r.Data) != 5 || r.Data[0].User.ID != ws.Agent.ID || rowOf(t, r, ws.Viewer.ID) != nil {
		t.Fatalf("rows %+v", r.Data)
	}
	anna := rowOf(t, r, ws.Agent.ID)
	if anna.Assigned != 3 || anna.Solved != 3 || anna.Replies != 4 || anna.Notes != 1 {
		t.Fatalf("anna counts %+v", anna)
	}
	// 5 replies on her 3 solved tickets; first responses 30 and 60; handle 120, 120 and 30.
	if !eq(anna.RepliesPerSolved, 1.7) || !eq(anna.FirstResponseP50Minutes, 45) || !eq(anna.HandleP50Minutes, 120) || !eq(anna.OnlineMinutes, 180) {
		t.Fatalf("anna figures rps %v frt %v handle %v online %v", anna.RepliesPerSolved, anna.FirstResponseP50Minutes, anna.HandleP50Minutes, anna.OnlineMinutes)
	}
	adam := rowOf(t, r, ws.Admin.ID)
	if adam.Assigned != 1 || adam.Solved != 0 || adam.Replies != 0 || adam.RepliesPerSolved != nil || adam.FirstResponseP50Minutes != nil ||
		adam.HandleP50Minutes != nil || !eq(adam.OnlineMinutes, 45) {
		t.Fatalf("adam %+v", adam)
	}
	bea := rowOf(t, r, f.bea.ID)
	if bea.Assigned != 1 || bea.Solved != 0 || bea.Replies != 1 || bea.Notes != 1 || !eq(bea.FirstResponseP50Minutes, 20) || !eq(bea.OnlineMinutes, 0) {
		t.Fatalf("bea %+v", bea)
	}
	owner := rowOf(t, r, ws.Owner.ID)
	if owner == nil || owner.Assigned != 0 || owner.Replies != 0 || !eq(owner.OnlineMinutes, 0) || rowOf(t, r, f.carl.ID) == nil {
		t.Fatalf("idle members %+v", r.Data)
	}

	// A team: its leads and members, and whoever worked its tickets; observers are not on it.
	r = agentsReport(t, e, ws.Admin.As(ws), march+"&team_id="+f.team.String())
	if len(r.Data) != 2 || rowOf(t, r, f.bea.ID) == nil || rowOf(t, r, ws.Admin.ID) == nil {
		t.Fatalf("team rows %+v", r.Data)
	}
	if b := rowOf(t, r, f.bea.ID); b.Assigned != 1 || b.Replies != 1 || b.Notes != 1 || !eq(b.FirstResponseP50Minutes, 20) {
		t.Fatalf("team bea %+v", b)
	}
	// Online time is the member's whole time, whatever the filter.
	if a := rowOf(t, r, ws.Admin.ID); a.Assigned != 1 || !eq(a.OnlineMinutes, 45) {
		t.Fatalf("team adam %+v", a)
	}
	// No team: only those who worked tickets without one.
	r = agentsReport(t, e, ws.Admin.As(ws), march+"&team_id=none")
	if len(r.Data) != 1 || r.Data[0].User.ID != ws.Agent.ID || r.Data[0].Solved != 3 {
		t.Fatalf("no team rows %+v", r.Data)
	}
	// A brand narrows the tickets, not the roster.
	r = agentsReport(t, e, ws.Admin.As(ws), march+"&brand_id="+f.brand.String())
	if len(r.Data) != 5 {
		t.Fatalf("brand rows %d", len(r.Data))
	}
	if a := rowOf(t, r, ws.Agent.ID); a.Assigned != 1 || a.Solved != 1 || a.Replies != 1 || a.Notes != 0 || !eq(a.RepliesPerSolved, 1) ||
		!eq(a.FirstResponseP50Minutes, 60) || !eq(a.HandleP50Minutes, 120) {
		t.Fatalf("brand anna %+v", a)
	}
	// The range bounds every figure: February has t3's first assignment, Anna's reply of 02-21 and
	// the first hour of her stretch across the end of the month, not her January one.
	r = agentsReport(t, e, ws.Admin.As(ws), "from=2026-02-01&to=2026-02-28")
	if a := rowOf(t, r, ws.Admin.ID); a.Assigned != 1 || !eq(a.OnlineMinutes, 0) {
		t.Fatalf("february adam %+v", a)
	}
	if a := rowOf(t, r, ws.Agent.ID); a.Solved != 0 || a.Replies != 1 || !eq(a.OnlineMinutes, 60) {
		t.Fatalf("february anna %+v", a)
	}

	e.Do("GET", "/v1/reports/agents?from=2026-03-31&to=2026-03-01", ws.Admin.As(ws), nil).Want(t, 422)
	e.Do("GET", "/v1/reports/agents?team_id=nope", ws.Admin.As(ws), nil).Want(t, 422)
}

// Online time is personal: admins see everyone's, other members their own.
func TestAgentsReportOnlineTime(t *testing.T) {
	e, _ := newEnv(t)
	f := seedAgents(t, e)
	ws := f.ws
	r := agentsReport(t, e, ws.Owner.As(ws), march)
	if r.OnlineTime != "all" || !eq(rowOf(t, r, ws.Agent.ID).OnlineMinutes, 180) {
		t.Fatalf("owner %+v", r)
	}
	r = agentsReport(t, e, ws.Agent.As(ws), march)
	if r.OnlineTime != "self" || len(r.Data) != 1 || !eq(rowOf(t, r, ws.Agent.ID).OnlineMinutes, 180) {
		t.Fatalf("agent %+v", r)
	}
	// A viewer has no row of their own, so sees none.
	r = agentsReport(t, e, ws.Viewer.As(ws), march)
	if r.OnlineTime != "self" || len(r.Data) != 0 {
		t.Fatalf("viewer %+v", r)
	}
	r = agentsReport(t, e, hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeTicketsRead)}, march)
	if r.OnlineTime != "none" || rowOf(t, r, ws.Agent.ID).OnlineMinutes != nil || rowOf(t, r, ws.Agent.ID).Solved != 3 {
		t.Fatalf("key %+v", r)
	}
	r = agentsReport(t, e, hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeTicketsRead, auth.ScopeSettingsRead)}, march)
	if r.OnlineTime != "all" || !eq(rowOf(t, r, ws.Agent.ID).OnlineMinutes, 180) {
		t.Fatalf("settings key %+v", r)
	}
	e.Do("GET", "/v1/reports/agents?"+march, hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeSettingsRead)}, nil).Want(t, 403)
}

// A member's work is personal too: other members get their own row alone, with the figures an
// admin sees on it; admins get everyone's.
func TestAgentsReportOwnRow(t *testing.T) {
	e, _ := newEnv(t)
	f := seedAgents(t, e)
	ws := f.ws
	all := agentsReport(t, e, ws.Admin.As(ws), march)
	if len(all.Data) != 5 || rowOf(t, all, f.bea.ID) == nil || rowOf(t, all, ws.Agent.ID) == nil {
		t.Fatalf("admin rows %+v", all.Data)
	}
	r := agentsReport(t, e, f.bea.As(ws), march)
	if r.OnlineTime != "self" || len(r.Data) != 1 || r.Data[0].User.ID != f.bea.ID {
		t.Fatalf("bea rows %+v", r.Data)
	}
	if b, want := r.Data[0], rowOf(t, all, f.bea.ID); b.Assigned != want.Assigned || b.Replies != want.Replies || b.Notes != want.Notes ||
		!eq(b.FirstResponseP50Minutes, 20) || !eq(b.OnlineMinutes, 0) {
		t.Fatalf("bea row %+v, admin sees %+v", b, want)
	}
	// Filters still apply: off the team, a member has no row there.
	if r := agentsReport(t, e, f.bea.As(ws), march+"&team_id="+f.team.String()); len(r.Data) != 1 || r.Data[0].User.ID != f.bea.ID {
		t.Fatalf("bea team rows %+v", r.Data)
	}
	if r := agentsReport(t, e, ws.Agent.As(ws), march+"&team_id="+f.team.String()); len(r.Data) != 0 {
		t.Fatalf("anna team rows %+v", r.Data)
	}
	// API keys without settings:read keep every row, without online time.
	r = agentsReport(t, e, hdtest.Auth{Bearer: e.APIKey(ws, auth.ScopeTicketsRead)}, march)
	if r.OnlineTime != "none" || len(r.Data) != 5 {
		t.Fatalf("key rows %+v", r.Data)
	}
}
