package assistant

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/domain"
)

// TestToolsWithoutSeverity checks a workspace that does not use severity gets the ticket
// tools without it, and a change the model still names leaves severity alone.
func TestToolsWithoutSeverity(t *testing.T) {
	e := newEnv(t, false)
	e.Exec(`UPDATE workspaces SET settings = settings || '{"severity_enabled": false}'::jsonb WHERE id = $1`, e.WS.ID)
	tk := e.ticket("Login broken", "I cannot sign in.")
	admin := e.WS.Admin.As(e.WS)
	sid := e.session(admin)
	e.M.queue(step{tool: "update_ticket", args: map[string]any{"ticket": tk.ID.String(), "priority": "urgent", "severity": "sev1"}})
	_, evs, _ := e.stream("/v1/assistant/sessions/"+sid+"/messages", admin, map[string]any{"text": "Make it urgent"})
	tools, _ := json.Marshal(e.M.reqs[0]["tools"])
	if strings.Contains(string(tools), "severity") || !strings.Contains(string(tools), "update_ticket") || !strings.Contains(string(tools), "set_priority") {
		t.Fatalf("tools: %s", tools)
	}
	ap := approvalOf(t, evs)
	if len(ap.Preview.Changes) != 1 || ap.Preview.Changes[0].Field != "Priority" {
		t.Fatalf("preview: %+v", ap.Preview)
	}
	e.M.queue(step{text: "Done."})
	if st, _, body := e.stream("/v1/assistant/actions/"+ap.ActionID.String()+"/decision", admin, map[string]any{"decision": "approve", "token": ap.Token}); st != 200 {
		t.Fatalf("approve: %d %s", st, body)
	}
	got, _ := domain.LoadTicket(e.Ctx, e.D.DB.Pool, e.WS.ID, tk.ID)
	if got.Priority != "urgent" || got.Severity != nil {
		t.Fatalf("ticket %v %v", got.Priority, got.Severity)
	}
}

func TestAutomationWithoutSeverity(t *testing.T) {
	e := newEnv(t, false)
	e.Exec(`UPDATE workspaces SET settings = settings || '{"severity_enabled": false}'::jsonb WHERE id = $1`, e.WS.ID)
	admin := e.WS.Admin.As(e.WS)
	sid := e.session(admin)
	e.M.queue(step{tool: "create_automation_draft", args: map[string]any{
		"name": "Raise impact", "trigger": "ticket_created", "conditions": []any{},
		"actions": []any{map[string]any{"type": "set_priority", "value": "urgent"}, map[string]any{"type": "set_severity", "value": "sev1"}},
	}})
	_, evs, body := e.stream("/v1/assistant/sessions/"+sid+"/messages", admin, map[string]any{"text": "Escalate outages"})
	ap := approvalOf(t, evs)
	raw, _ := json.Marshal(ap.Preview)
	if strings.Contains(string(raw), "severity") {
		t.Fatalf("severity-off approval still proposes severity: %s; body=%s", raw, body)
	}
	e.M.queue(step{text: "Created."})
	if st, _, body := e.stream("/v1/assistant/actions/"+ap.ActionID.String()+"/decision", admin, map[string]any{"decision": "approve", "token": ap.Token}); st != 200 {
		t.Fatalf("approval: %d %s", st, body)
	}
	var actions []byte
	e.Scalar(`SELECT actions FROM automations WHERE workspace_id=$1 AND name='Raise impact'`, &actions, e.WS.ID)
	if strings.Contains(string(actions), "severity") || !strings.Contains(string(actions), "set_priority") {
		t.Fatalf("stored actions: %s", actions)
	}
}

func TestAutomationSeverityRefused(t *testing.T) {
	for name, raw := range map[string]string{
		"condition":   `{"name":"Only critical","trigger":"ticket_created","conditions":[{"field":"severity","op":"equals","value":"sev1"}],"actions":[{"type":"set_priority","value":"urgent"}]}`,
		"only action": `{"name":"Critical","trigger":"ticket_created","conditions":[],"actions":[{"type":"set_severity","value":"sev1"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := proposeAutomation(nil, &runCtx{noSeverity: true}, json.RawMessage(raw)); err == nil {
				t.Fatal("severity-only proposal accepted")
			}
		})
	}
}
