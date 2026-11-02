package ai

import (
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// severityOff turns the workspace's severity off (settings severity_enabled false).
func (e *env) severityOff() {
	e.T.Helper()
	e.Exec(`UPDATE workspaces SET settings = settings || '{"severity_enabled": false}'::jsonb WHERE id = $1`, e.WS.ID)
}

// toolSchema is the input schema of the named tool in a model request.
func toolSchema(req map[string]any, name string) (desc string, props map[string]any) {
	tools, _ := req["tools"].([]any)
	for _, t := range tools {
		m, _ := t.(map[string]any)
		if m["name"] == name {
			s, _ := m["input_schema"].(map[string]any)
			props, _ = s["properties"].(map[string]any)
			desc, _ = m["description"].(string)
			return desc, props
		}
	}
	return "", nil
}

func TestJevTriageWithoutSeverity(t *testing.T) {
	e := newEnv(t, opts{billing: true, jev: true})
	jevWorkspace(e)
	e.severityOff()
	e.settings(map[string]any{"auto_triage": true})
	e.TS.set("severity", scoreAns(3, 4, 0.99, 0.99)) // sev1, were it asked
	tk := e.ticket("Charged twice", "I was charged twice for order 1234.")
	e.runTriggers()

	if e.TS.calls() != 1 {
		t.Fatalf("typesafe calls %d", e.TS.calls())
	}
	req := e.TS.last()
	if _, ok := req.Questions["severity"]; ok {
		t.Fatal("severity asked of a workspace without it")
	}
	if _, ok := req.Questions["priority"]; !ok {
		t.Fatalf("questions %v", req.Questions)
	}
	got := e.loadTicket(tk.ID)
	if got.Severity != nil {
		t.Fatalf("severity set: %v", *got.Severity)
	}
	if _, ok := e.suggestions(got)["severity"]; ok {
		t.Fatal("severity suggested")
	}
	if d := e.decisions(got); d.Fields["severity"] != nil || d.Fields["priority"] == nil {
		t.Fatalf("decisions %+v", d.Fields)
	}
	if n := e.count(`SELECT count(*) FROM activities WHERE ticket_id = $1 AND type = 'severity_changed'`, tk.ID); n != 0 {
		t.Fatalf("severity activities %d", n)
	}
}

func TestLLMTriageWithoutSeverity(t *testing.T) {
	e := newEnv(t, opts{})
	e.severityOff()
	e.F.set("triage", `{"tags":[],"suggested_tags":[],"priority":"urgent","severity":"sev1","team":"none","sentiment":"negative","language":"en","category":"problem","summary":"Down."}`)
	tk := e.ticket("Down", "Everything is down")
	var out Triage
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/ai/triage", e.agent(), map[string]any{"apply": true}).Want(t, 200).JSON(t, &out)
	got := e.loadTicket(tk.ID)
	if out.Severity != nil || got.Severity != nil || got.Priority != "urgent" {
		t.Fatalf("triage %+v ticket %+v", out, got)
	}
	if out.Decisions.Fields["severity"] != nil {
		t.Fatalf("decisions %+v", out.Decisions.Fields)
	}
}

func TestLLMTriageSchemaWithoutSeverity(t *testing.T) {
	for _, on := range []bool{true, false} {
		s := llmTriageSchema([]string{"Billing"}, on)
		_, has := s["properties"].(map[string]any)["severity"]
		required := strings.Join(s["required"].([]string), ",")
		if has != on || strings.Contains(required, "severity") != on || !strings.Contains(required, "priority") {
			t.Fatalf("severity %v: schema %v", on, s)
		}
	}
	if q := triageQuestions(triageOptions{noSeverity: true}); q["severity"].Type != "" || q["priority"].Type == "" {
		t.Fatalf("questions %v", q)
	}
	got := validateTriage(llmTriage{Priority: "high", Severity: "sev2"}, triageOptions{noSeverity: true})
	if got.Severity != nil || got.Decisions.Fields["severity"] != nil {
		t.Fatalf("%+v", got)
	}
}

func TestProcedureRunWithoutSeverity(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	e.severityOff()
	p := e.createProcedure(map[string]any{"name": "Escalate", "when": "Outages", "instructions": "Raise the ticket for outages."})
	tk := e.ticket("Down", "Everything is down")
	e.Exec(`UPDATE tickets SET severity = 'sev3' WHERE id = $1`, tk.ID)
	e.Jobs(jobs.TicketTriggerArgs{}.Kind(), e.WS.ID)
	e.F.queue("procedure", `@tool update_ticket {"priority":"urgent","severity":"sev1"}`, "Raised it.")
	if err := e.S.RunProcedure(e.Ctx, jobs.AIProcedureRunArgs{WorkspaceID: e.WS.ID, TicketID: tk.ID, AutomationID: ids.New(), ProcedureID: p.ID}); err != nil {
		t.Fatal(err)
	}
	reqs := e.procedureRequests()
	if len(reqs) == 0 {
		t.Fatal("no procedure request")
	}
	desc, props := toolSchema(reqs[0], "update_ticket")
	if props == nil || props["priority"] == nil || props["severity"] != nil || strings.Contains(desc, "severity") {
		t.Fatalf("update_ticket %q %v", desc, props)
	}
	if strings.Contains(asJSON(reqs[0]["messages"]), "severity: sev3") {
		t.Fatal("the ticket's severity was shown to the run")
	}
	got := e.loadTicket(tk.ID)
	if got.Priority != "urgent" || got.Severity == nil || *got.Severity != "sev3" {
		t.Fatalf("ticket %v %v", got.Priority, got.Severity)
	}
}
