package core

import (
	"context"
	"testing"
	"time"
)

type stretch struct {
	Started, Ended time.Time
	Open           bool
}

// TestPresenceStretches checks that heartbeats while online make one stretch, a silence
// longer than PresenceGap starts a new one, a change of availability ends the open one,
// and viewers and old stretches are not kept.
func TestPresenceStretches(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, _ := e.signup("presence-boss")
	wid := owner.newWorkspace("Presence Co")
	agent, _ := e.signup("presence-agent")
	viewer, _ := e.signup("presence-viewer")
	ids := map[*client]string{}
	for c, role := range map[*client]string{agent: "agent", viewer: "viewer"} {
		me := c.do("GET", "/v1/me", nil)
		id := me.Body["user"].(map[string]any)["id"].(string)
		ids[c] = id
		if _, err := e.d.DB.Pool.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, wid, id, role); err != nil {
			t.Fatal(err)
		}
		c.workspace = wid
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.d.DB.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	stretches := func(uid string) []stretch {
		t.Helper()
		rows, err := e.d.DB.Pool.Query(ctx, `SELECT started_at, ended_at, open FROM agent_presence
			WHERE workspace_id = $1 AND user_id = $2 ORDER BY started_at`, wid, uid)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []stretch
		for rows.Next() {
			var s stretch
			if err := rows.Scan(&s.Started, &s.Ended, &s.Open); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	// Moves the agent's stretches back in time, as if the last heartbeat came d ago.
	ago := func(d time.Duration) {
		exec(`UPDATE agent_presence SET started_at = started_at - make_interval(secs => $3), ended_at = ended_at - make_interval(secs => $3)
			WHERE workspace_id = $1 AND user_id = $2`, wid, ids[agent], d.Seconds())
	}
	beat := func(c *client) {
		t.Helper()
		if r := c.do("POST", "/v1/me/heartbeat", nil); r.Status != 204 {
			t.Fatalf("heartbeat: %d %s", r.Status, r.Raw)
		}
	}
	near := func(at time.Time) bool { return time.Since(at).Abs() < time.Minute }

	// The first heartbeat starts a stretch; one 3 minutes later extends it.
	beat(agent)
	if s := stretches(ids[agent]); len(s) != 1 || !s[0].Open || !s[0].Ended.Equal(s[0].Started) {
		t.Fatalf("first heartbeat %+v", s)
	}
	ago(3 * time.Minute)
	beat(agent)
	s := stretches(ids[agent])
	if len(s) != 1 || !s[0].Open || !near(s[0].Ended) || s[0].Ended.Sub(s[0].Started) < 3*time.Minute {
		t.Fatalf("merged heartbeats %+v", s)
	}
	// A silence longer than the gap closes it at its last heartbeat and starts another.
	ago(PresenceGap + time.Minute)
	lastBeat := stretches(ids[agent])[0].Ended
	beat(agent)
	s = stretches(ids[agent])
	if len(s) != 2 || s[0].Open || !s[0].Ended.Equal(lastBeat) || !s[1].Open || !near(s[1].Started) {
		t.Fatalf("after a gap %+v", s)
	}

	// Going away yourself ends the stretch now; heartbeats while away record nothing.
	ago(2 * time.Minute)
	if r := agent.do("PATCH", "/v1/members/me/availability", map[string]any{"availability": "away"}); r.Status != 200 {
		t.Fatalf("away: %d %s", r.Status, r.Raw)
	}
	s = stretches(ids[agent])
	if len(s) != 2 || s[1].Open || !near(s[1].Ended) || s[1].Ended.Sub(s[1].Started) < 2*time.Minute {
		t.Fatalf("away %+v", s)
	}
	beat(agent)
	if n := len(stretches(ids[agent])); n != 2 {
		t.Fatalf("heartbeat while away recorded %d stretches", n)
	}
	// Coming back online yourself starts a stretch at once.
	if r := agent.do("PATCH", "/v1/members/me/availability", map[string]any{"availability": "online"}); r.Status != 200 {
		t.Fatalf("online: %d %s", r.Status, r.Raw)
	}
	s = stretches(ids[agent])
	if len(s) != 3 || !s[2].Open || !near(s[2].Started) {
		t.Fatalf("back online %+v", s)
	}
	// Someone else setting the agent offline ends it at the agent's last heartbeat.
	ago(4 * time.Minute)
	lastBeat = stretches(ids[agent])[2].Ended
	if r := owner.do("PATCH", "/v1/members/"+ids[agent]+"/availability", map[string]any{"availability": "offline"}); r.Status != 200 {
		t.Fatalf("owner sets offline: %d %s", r.Status, r.Raw)
	}
	s = stretches(ids[agent])
	if len(s) != 3 || s[2].Open || !s[2].Ended.Equal(lastBeat) {
		t.Fatalf("set offline by the owner %+v", s)
	}
	// Someone else setting the agent online starts nothing until the agent's app beats.
	if r := owner.do("PATCH", "/v1/members/"+ids[agent]+"/availability", map[string]any{"availability": "online"}); r.Status != 200 {
		t.Fatalf("owner sets online: %d %s", r.Status, r.Raw)
	}
	if n := len(stretches(ids[agent])); n != 3 {
		t.Fatalf("set online by the owner recorded %d stretches", n)
	}

	// Viewers' time is not recorded.
	beat(viewer)
	if n := len(stretches(ids[viewer])); n != 0 {
		t.Fatalf("viewer stretches %d", n)
	}

	// Stretches are kept 400 days.
	exec(`INSERT INTO agent_presence (workspace_id, user_id, started_at, ended_at, open)
		VALUES ($1, $2, now() - interval '402 days', now() - interval '401 days', false)`, wid, ids[agent])
	if err := (&cleanupWorker{s: e.s}).Work(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(stretches(ids[agent])); n != 3 {
		t.Fatalf("after cleanup %d stretches", n)
	}
	// Removing the member removes their stretches.
	if r := owner.do("DELETE", "/v1/members/"+ids[agent], nil); r.Status != 204 {
		t.Fatalf("remove member: %d %s", r.Status, r.Raw)
	}
	if n := len(stretches(ids[agent])); n != 0 {
		t.Fatalf("removed member keeps %d stretches", n)
	}
}
