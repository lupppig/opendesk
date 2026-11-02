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
