package assistant

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/integrations"
)

// setAuto stores settings.ai.assistant_auto for the test workspace.
func (e *env) setAuto(tools ...string) {
	e.T.Helper()
	raw, _ := json.Marshal(map[string]any{"assistant_auto": tools})
	if _, err := e.D.DB.Pool.Exec(e.Ctx, `UPDATE workspaces SET settings = jsonb_set(settings, '{ai}', COALESCE(settings->'ai', '{}'::jsonb) || $2::jsonb)
		WHERE id = $1`, e.WS.ID, raw); err != nil {
		e.T.Fatal(err)
	}
}

func lastStep(t *testing.T, evs []event, tool string) Step {
	t.Helper()
	var st Step
	for _, ev := range find(evs, "tool") {
		var s Step
		_ = json.Unmarshal(ev.Data, &s)
		if s.Tool == tool {
			st = s
		}
	}
	return st
}

func TestAutoRunBuiltInChange(t *testing.T) {
	e := newEnv(t, false)
	e.setAuto("update_ticket")
	tk := e.ticket("Login broken", "I cannot sign in.")
	agent := e.WS.Agent.As(e.WS)
	sid := e.session(agent)
	e.M.queue(step{tool: "update_ticket", args: map[string]any{"ticket": tk.ID.String(), "priority": "urgent"}}, step{text: "Made it urgent."})
	_, evs, body := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Make this urgent"})
	if len(find(evs, "approval")) != 0 {
		t.Fatalf("asked for approval: %s %v", body, evs)
	}
	st := lastStep(t, evs, "update_ticket")
	if st.Status != "done" || !st.Auto || st.ActionID == nil || st.Preview == nil || len(st.Preview.Changes) != 1 {
		t.Fatalf("step = %+v", st)
	}
	if got, _ := domain.LoadTicket(e.Ctx, e.D.DB.Pool, e.WS.ID, tk.ID); got.Priority != "urgent" {
		t.Fatalf("not applied: %s", got.Priority)
	}
	// Recorded like an approved change, marked as run without asking, as ai for the user.
	var status string
	var auto bool
	e.Scalar(`SELECT status FROM assistant_actions WHERE id = $1`, &status, st.ActionID)
	e.Scalar(`SELECT auto FROM assistant_actions WHERE id = $1`, &auto, st.ActionID)
	if status != "done" || !auto {
		t.Fatalf("action = %s auto %v", status, auto)
	}
	var n int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'assistant.action' AND actor_user_id = $2
		AND data->>'actor' = 'ai' AND data->>'auto' = 'true' AND data->>'result' = 'done'`, &n, e.WS.ID, e.WS.Agent.ID)
	if n != 1 {
		t.Fatalf("audit %d", n)
	}
	e.Scalar(`SELECT count(*) FROM activities WHERE ticket_id = $1 AND actor_type = 'ai' AND actor_user_id = $2`, &n, tk.ID, e.WS.Agent.ID)
	if n < 1 {
		t.Fatalf("ai activities %d", n)
	}
	// The model reads the result as for an approved change, and the transcript keeps the mark.
	raw, _ := json.Marshal(e.M.last()["messages"])
	if !strings.Contains(string(raw), "Updated:") {
		t.Fatalf("result not returned: %s", raw)
	}
	var tr Transcript
	e.Do("GET", "/v1/assistant/sessions/"+sid, agent, nil).Want(t, 200).JSON(t, &tr)
	found := false
	for _, it := range tr.Items {
		for _, s := range it.Steps {
			found = found || (s.Tool == "update_ticket" && s.Auto && s.Status == "done" && s.Preview != nil)
		}
	}
	if !found || tr.Approval != nil {
		t.Fatalf("transcript = %+v", tr)
	}
	// The role check is the user's own: a viewer gets no write tool to run.
	viewer := e.WS.Viewer.As(e.WS)
	vsid := e.session(viewer)
	e.M.queue(step{tool: "update_ticket", args: map[string]any{"ticket": tk.ID.String(), "priority": "low"}}, step{text: "No."})
	e.stream("/v1/assistant/sessions/"+vsid+"/messages", viewer, map[string]any{"text": "Make it low"})
	if got, _ := domain.LoadTicket(e.Ctx, e.D.DB.Pool, e.WS.ID, tk.ID); got.Priority != "urgent" {
		t.Fatalf("a viewer changed it: %s", got.Priority)
	}
}

func TestAutoRunAlwaysAsksForSomeChanges(t *testing.T) {
	e := newEnv(t, false)
	// Stored directly, past validation: the run still refuses what may never run on its own.
	e.setAuto("update_ticket", "draft_reply", "api_change")
	tk := e.ticket("Refund", "Please refund me.")
	agent := e.WS.Agent.As(e.WS)
	for _, c := range []step{
		{tool: "draft_reply", args: map[string]any{"ticket": tk.ID.String(), "body": "We refunded you."}},
		{tool: "api_change", args: map[string]any{"method": "PATCH", "path": "/v1/members/me/profile", "body": map[string]any{"title": "Lead"}}},
	} {
		sid := e.session(agent)
		e.M.queue(c)
		_, evs, body := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Do it"})
		ap := approvalOf(t, evs)
		if st := lastStep(t, evs, c.tool); st.Auto || st.Status != "awaiting_approval" {
			t.Fatalf("%s step = %+v %s", c.tool, st, body)
		}
		_ = ap
	}
	var n int
	e.Scalar(`SELECT count(*) FROM assistant_actions WHERE workspace_id = $1 AND auto`, &n, e.WS.ID)
	if n != 0 {
		t.Fatalf("auto actions = %d", n)
	}
	e.Scalar(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND direction = 'outbound'`, &n, tk.ID)
	if n != 0 {
		t.Fatal("a reply went out without approval")
	}
	// The setting itself refuses anything but the built-in changes that may run on their own.
	for _, bad := range [][]string{{"draft_reply"}, {"api_change"}, {"linear__create_issue"}} {
		if _, err := hd.ValidateAIPatch(hd.AISettingsPatch{AssistantAuto: &bad}, nil); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	ok := []string{"add_note", "add_note", "create_view"}
	if out, err := hd.ValidateAIPatch(hd.AISettingsPatch{AssistantAuto: &ok}, nil); err != nil || len(out["assistant_auto"].([]string)) != 2 {
		t.Fatalf("valid list = %v %v", out, err)
	}
	// And a stored list reads back without them.
	st, err := hd.LoadSettings(e.Ctx, e.D.DB.Pool, e.WS.ID)
	if err != nil || strings.Join(st.AI().AssistantAuto, ",") != "update_ticket" {
		t.Fatalf("read back = %v %v", st.AI().AssistantAuto, err)
	}
}

func TestAlwaysAsks(t *testing.T) {
	for _, c := range []struct {
		t    *tool
		pv   Preview
		asks bool
	}{
		{&tool{Name: "add_note", Write: true, Apply: applyMessage("note")}, Preview{}, false},
		{&tool{Name: "add_note", Write: true, Apply: applyMessage("note")}, Preview{External: true}, true},
		{&tool{Name: "draft_reply", Write: true, Apply: applyMessage("reply")}, Preview{}, true},
		{&tool{Name: "api_change", Write: true, Apply: applyAPI}, Preview{}, true},
		{&tool{Name: "linear__create", Write: true, Apply: applyAPI, ext: &integrations.Tool{}}, Preview{}, false},
		{&tool{Name: "linear__send", Write: true, Apply: applyAPI, ext: &integrations.Tool{}}, Preview{External: true}, true},
		{&tool{Name: "search_tickets"}, Preview{}, true},
	} {
		if got := alwaysAsks(c.t, c.pv); got != c.asks {
			t.Errorf("alwaysAsks(%s, external %v) = %v", c.t.Name, c.pv.External, got)
		}
	}
}

func TestAutoRunCapPerMessage(t *testing.T) {
	e := newEnv(t, false)
	e.setAuto("add_note")
	tk := e.ticket("Busy", "Lots to do.")
	agent := e.WS.Agent.As(e.WS)
	sid := e.session(agent)
	e.M.queue(step{tool: "add_note", args: map[string]any{"ticket": tk.ID.String(), "body": "Checked."}, repeat: maxAutoPerMessage + 2})
	_, evs, body := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Note it everywhere"})
	ap := approvalOf(t, evs)
	if ap.Tool != "add_note" {
		t.Fatalf("approval = %+v %s", ap, body)
	}
	var autoN, notes, waits int
	e.Scalar(`SELECT count(*) FROM assistant_actions WHERE session_id = $1 AND auto AND status = 'done'`, &autoN, sid)
	e.Scalar(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND kind = 'note'`, &notes, tk.ID)
	waited := map[string]bool{}
	for _, ev := range find(evs, "tool") {
		var s Step
		_ = json.Unmarshal(ev.Data, &s)
		if s.Summary == "Waits for the change before it" {
			waited[s.ID] = true
		}
	}
	waits = len(waited)
	// 20 ran, the 21st waits for approval, and the one after it waits behind that one.
	if autoN != maxAutoPerMessage || notes != maxAutoPerMessage || waits != 1 {
		t.Fatalf("auto %d notes %d waits %d", autoN, notes, waits)
	}
	// Approving the proposal does not reset the count: the next write in the same message asks.
	e.M.queue(step{tool: "add_note", args: map[string]any{"ticket": tk.ID.String(), "body": "One more."}})
	_, evs, _ = e.stream("/v1/assistant/actions/"+ap.ActionID.String()+"/decision", agent, map[string]any{"decision": "approve", "token": ap.Token})
	if len(find(evs, "approval")) != 1 {
		t.Fatalf("a write past the cap ran on its own: %v", evs)
	}
	// A new message starts a new count.
	e.M.queue(step{tool: "add_note", args: map[string]any{"ticket": tk.ID.String(), "body": "Next day."}}, step{text: "Noted."})
	_, evs, _ = e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "One more note"})
	if len(find(evs, "approval")) != 0 || !lastStep(t, evs, "add_note").Auto {
		t.Fatalf("new message did not run on its own: %v", evs)
	}
}

func TestAutoRunStopsWhenTurnedOff(t *testing.T) {
	e := newEnv(t, false)
	e.setAuto("update_ticket")
	tk := e.ticket("Slow", "It is slow.")
	agent := e.WS.Agent.As(e.WS)
	sid := e.session(agent)
	// The run starts with the setting on; an admin turns it off while the model answers.
	e.M.queue(step{tool: "update_ticket", args: map[string]any{"ticket": tk.ID.String(), "priority": "high"}, before: func() { e.setAuto() }})
	_, evs, _ := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Raise it"})
	approvalOf(t, evs)
	if got, _ := domain.LoadTicket(e.Ctx, e.D.DB.Pool, e.WS.ID, tk.ID); got.Priority == "high" {
		t.Fatal("ran after the setting was turned off")
	}
	var n int
	e.Scalar(`SELECT count(*) FROM assistant_actions WHERE session_id = $1 AND auto`, &n, sid)
	if n != 0 {
		t.Fatalf("auto actions %d", n)
	}
}

func TestConnectedAutoTools(t *testing.T) {
	e := newEnv(t, false)
	fl := &fakeLinear{}
	srv := httptest.NewServer(fl)
	defer srv.Close()
	tools := `[{"name":"list_issues","description":"Search issues","input_schema":{"type":"object"},"read_only":true},
		{"name":"create_issue","title":"Create issue","input_schema":{"type":"object","properties":{"title":{"type":"string"}}},"read_only":false}]`
	iid := ids.New()
	if _, err := e.D.DB.Pool.Exec(e.Ctx, `INSERT INTO integrations (id, workspace_id, provider, slug, name, url, auth_type, status, tools, auto_tools)
		VALUES ($1,$2,'linear','linear','Linear',$3,'none','connected',$4,'{create_issue}')`, iid, e.WS.ID, srv.URL+"/mcp", tools); err != nil {
		t.Fatal(err)
	}
	agent := e.WS.Agent.As(e.WS)
	sid := e.session(agent)
	e.M.queue(step{tool: "linear__create_issue", args: map[string]any{"title": "Login loop"}}, step{text: "Created."})
	_, evs, body := e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Open an issue"})
	st := lastStep(t, evs, "linear__create_issue")
	if len(find(evs, "approval")) != 0 || !st.Auto || st.Status != "done" || st.Preview == nil || !strings.Contains(st.Preview.Arguments, "Login loop") {
		t.Fatalf("step = %+v %s", st, body)
	}
	fl.mu.Lock()
	if len(fl.calls) != 1 || fl.calls[0] != "create_issue" {
		t.Fatalf("calls = %v", fl.calls)
	}
	fl.mu.Unlock()
	// Ask first wins over runs without asking.
	if _, err := e.D.DB.Pool.Exec(e.Ctx, `UPDATE integrations SET ask_tools = '{create_issue}' WHERE id = $1`, iid); err != nil {
		t.Fatal(err)
	}
	e.M.queue(step{tool: "linear__create_issue", args: map[string]any{"title": "Second"}})
	_, evs, _ = e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "Another one"})
	approvalOf(t, evs)
	// Taken off auto_tools while the model answers: it asks instead.
	if _, err := e.D.DB.Pool.Exec(e.Ctx, `UPDATE integrations SET ask_tools = '{}' WHERE id = $1`, iid); err != nil {
		t.Fatal(err)
	}
	e.M.queue(step{tool: "linear__create_issue", args: map[string]any{"title": "Third"}, before: func() {
		_, _ = e.D.DB.Pool.Exec(e.Ctx, `UPDATE integrations SET auto_tools = '{}' WHERE id = $1`, iid)
	}})
	_, evs, _ = e.stream("/v1/assistant/sessions/"+sid+"/messages", agent, map[string]any{"text": "And a third"})
	approvalOf(t, evs)
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if len(fl.calls) != 1 {
		t.Fatalf("calls after = %v", fl.calls)
	}
}
