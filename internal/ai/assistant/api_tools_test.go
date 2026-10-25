package assistant

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIToolsReachTheRestOfTheApp(t *testing.T) {
	e := newEnv(t, false)
	agent := e.WS.Agent.As(e.WS)
	sid := e.session(agent)

	// find_api finds the personal signature; api_get reads only the fields asked for; the
	// change waits for approval with the value it sets.
	e.M.queue(step{tool: "find_api", args: map[string]any{"query": "set my personal email signature"}},
		step{tool: "api_get", args: map[string]any{"path": "/v1/members", "fields": []string{"role"}}},
		step{tool: "api_change", args: map[string]any{"method": "PATCH", "path": "/v1/members/me/profile",
			"body": map[string]any{"signature_html": "<p>Jane Doe, Support</p>"}}})
	_, evs, _ := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Set my signature"})
	raw, _ := json.Marshal(e.M.last()["messages"])
	msgs := string(raw)
	if !strings.Contains(msgs, "PATCH /v1/members/{user_id}/profile") {
		t.Fatalf("find_api missed the profile: %s", msgs)
	}
	if !strings.Contains(msgs, `\"role\"`) || strings.Contains(msgs, `\"email\":`) {
		t.Fatalf("api_get fields not applied: %s", msgs)
	}
	ap := approvalOf(t, evs)
	if len(ap.Preview.Changes) != 1 || ap.Preview.Changes[0].Field != "signature_html" || !strings.Contains(ap.Preview.Changes[0].To, "Jane Doe") ||
		!strings.Contains(ap.Preview.Arguments, `"method": "PATCH"`) || !strings.Contains(ap.Preview.Arguments, `"signature_html": "<p>Jane Doe, Support</p>"`) {
		t.Fatalf("preview: %+v", ap.Preview)
	}
	var sig string
	e.Scalar(`SELECT signature_html FROM members WHERE workspace_id = $1 AND user_id = $2`, &sig, e.WS.ID, e.WS.Agent.ID)
	if sig != "" {
		t.Fatal("changed before approval")
	}
	e.M.queue(step{text: "Your signature is set."})
	if st, _, body := e.stream("/v1/assistant/actions/"+ap.ActionID.String()+"/decision", agent, map[string]any{"decision": "approve", "token": ap.Token}); st != 200 {
		t.Fatalf("approve: %d %s", st, body)
	}
	e.Scalar(`SELECT signature_html FROM members WHERE workspace_id = $1 AND user_id = $2`, &sig, e.WS.ID, e.WS.Agent.ID)
	if !strings.Contains(sig, "Jane Doe") {
		t.Fatalf("signature not applied: %q", sig)
	}

	// Credentials, sign-in and customer messages stay out of reach.
	for _, c := range []map[string]any{
		{"method": "POST", "path": "/v1/api-keys", "body": map[string]any{"name": "x"}},
		{"method": "PATCH", "path": "/v1/members/me/profile", "body": map[string]any{"title": "x", "password": "hunter2hunter2"}},
		{"method": "POST", "path": "/v1/auth/logout"},
		{"method": "POST", "path": "/v1/members/../auth/logout"},
	} {
		sid := e.session(agent)
		e.M.queue(step{tool: "api_change", args: c}, step{text: "I can't."})
		_, evs, _ := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Do it"})
		if len(find(evs, "approval")) != 0 {
			t.Fatalf("proposed %v", c)
		}
	}
	sid = e.session(agent)
	e.M.queue(step{tool: "api_get", args: map[string]any{"path": "/v1/auth/config"}}, step{text: "No."})
	_, evs, _ = e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Read it"})
	if tools := find(evs, "tool"); len(tools) != 2 || !strings.Contains(string(tools[1].Data), `"status":"error"`) {
		t.Fatalf("closed route read: %v", tools)
	}

	// Viewers change their own profile too; the API's role checks still apply on approval.
	viewer := e.WS.Viewer.As(e.WS)
	vsid := e.session(viewer)
	e.M.queue(step{tool: "api_change", args: map[string]any{"method": "PATCH", "path": "/v1/members/me/profile", "body": map[string]any{"title": "Reviewer"}}})
	_, evs, _ = e.stream("/v1/assistant/sessions/"+vsid+"/messages", viewer, map[string]any{"text": "Set my title"})
	ap = approvalOf(t, evs)
	e.M.queue(step{text: "Done."})
	e.stream("/v1/assistant/actions/"+ap.ActionID.String()+"/decision", viewer, map[string]any{"decision": "approve", "token": ap.Token})
	var title string
	e.Scalar(`SELECT title FROM members WHERE workspace_id = $1 AND user_id = $2`, &title, e.WS.ID, e.WS.Viewer.ID)
	if title != "Reviewer" {
		t.Fatalf("viewer title %q", title)
	}
}

func TestReachable(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"PATCH", "/v1/workspace", true}, {"DELETE", "/v1/workspace", false}, {"POST", "/v1/workspace/transfer", false},
		{"PATCH", "/v1/me", true}, {"DELETE", "/v1/me", false}, {"POST", "/v1/me/password", false},
		{"GET", "/v1/api-keys", true}, {"POST", "/v1/api-keys", false}, {"GET", "/v1/auth/config", false},
		{"PATCH", "/v1/widget/settings", true}, {"GET", "/v1/widget/conversations", false},
		{"POST", "/v1/tickets/{id}/messages", false}, {"POST", "/v1/tickets/{id}/merge", true},
	} {
		if got := reachable(c.method, c.path); got != c.want {
			t.Errorf("%s %s: %v", c.method, c.path, got)
		}
	}
}
