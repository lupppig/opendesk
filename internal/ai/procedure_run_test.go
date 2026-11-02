package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/integrations"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// fakeLinear stands in for a connected Linear: list_issues reads, create_issue runs
// without asking, delete_issue waits for a person's approval.
type fakeLinear struct {
	iid   uuid.UUID
	real  *integrations.Service
	mu    sync.Mutex
	calls []string
	modes []integrations.CallMode
}

func (f *fakeLinear) Tools(context.Context, uuid.UUID) ([]integrations.Tool, error) {
	obj := map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}}
	t := func(remote string, read, auto bool) integrations.Tool {
		return integrations.Tool{Name: "linear__" + remote, Remote: remote, IntegrationID: f.iid, Integration: "Linear", Provider: "linear",
			Title: remote, Description: "[Linear] " + remote, Schema: obj, ReadOnly: read, AutoRun: auto, Hosts: []string{"linear.app"}}
	}
	return []integrations.Tool{t("list_issues", true, false), t("create_issue", false, true), t("delete_issue", false, false)}, nil
}

func (f *fakeLinear) Call(_ context.Context, _, _ uuid.UUID, remote string, args json.RawMessage, mode integrations.CallMode) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls, f.modes = append(f.calls, remote), append(f.modes, mode)
	if remote == "create_issue" {
		return "Created ACM-7: https://linear.app/acme/issue/ACM-7/login-loop", false, nil
	}
	return "No issues match.", false, nil
}

func (f *fakeLinear) LinkTicketAsAI(ctx context.Context, tx pgx.Tx, wid, tid, iid uuid.UUID, rawURL, title string) error {
	_, err := f.real.LinkTicketAsAI(ctx, tx, wid, tid, iid, rawURL, title)
	return err
}

func (e *env) linear() *fakeLinear {
	e.T.Helper()
	f := &fakeLinear{iid: ids.New(), real: integrations.For(e.D)}
	e.Exec(`INSERT INTO integrations (id, workspace_id, provider, slug, name, url, auth_type, status) VALUES ($1, $2, 'linear', 'linear', 'Linear', 'https://mcp.linear.app/mcp', 'none', 'connected')`,
		f.iid, e.WS.ID)
	e.S.connected = f
	e.T.Cleanup(func() { e.S.connected = nil })
	return f
}

// procedureRequests are the model requests of procedure runs so far.
func (e *env) procedureRequests() []map[string]any {
	e.F.mu.Lock()
	defer e.F.mu.Unlock()
	var out []map[string]any
	for _, r := range e.F.requests {
		if feature(systemText(r)) == "procedure" {
			out = append(out, r)
		}
	}
	return out
}

func (e *env) procedureRan(tid uuid.UUID) []map[string]any {
	e.T.Helper()
	rows, err := e.D.DB.Pool.Query(e.Ctx, `SELECT data FROM activities WHERE ticket_id = $1 AND type = 'ai_procedure_ran' AND actor_type = 'ai' ORDER BY created_at`, tid)
	if err != nil {
		e.T.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[map[string]any])
	if err != nil {
		e.T.Fatal(err)
	}
	return out
}

func (e *env) runProcedureJobs() []jobs.AIProcedureRunArgs {
	e.T.Helper()
	var out []jobs.AIProcedureRunArgs
	for _, raw := range e.Jobs(jobs.AIProcedureRunArgs{}.Kind(), e.WS.ID) {
		var a jobs.AIProcedureRunArgs
		_ = json.Unmarshal(raw, &a)
		w := &ProcedureRunWorker{S: e.S}
		if err := w.Work(e.Ctx, &river.Job[jobs.AIProcedureRunArgs]{JobRow: &rivertype.JobRow{CreatedAt: time.Now()}, Args: a}); err != nil {
			e.T.Fatalf("procedure run: %v", err)
		}
		out = append(out, a)
	}
	return out
}

func TestRunProcedureAction(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	p := e.createProcedure(map[string]any{"name": "Linear issues", "when": "Work outside the ticket", "instructions": "Create an issue.", "channels": []string{}})
	action := []any{map[string]any{"type": "run_procedure", "value": p.ID.String()}}

	// Never on ticket_updated; only the workspace's procedures.
	e.Do("POST", "/v1/automations", e.admin(), map[string]any{"name": "Loop", "trigger": "ticket_updated", "actions": action}).Want(t, 422)
	other := e.NewWorkspace()
	var foreign uuid.UUID
	e.Scalar(`INSERT INTO ai_procedures (workspace_id, name, when_text, instructions) VALUES ($1, 'x', 'when', 'x') RETURNING id`, &foreign, other.ID)
	e.Do("POST", "/v1/automations", e.admin(), map[string]any{"name": "Foreign", "trigger": "ticket_created",
		"actions": []any{map[string]any{"type": "run_procedure", "value": foreign.String()}}}).Want(t, 422)
	for _, trig := range []string{"ticket_created", "customer_replied", "agent_replied", "call_ended"} {
		e.Do("POST", "/v1/automations", e.admin(), map[string]any{"name": "Run " + trig, "trigger": trig, "actions": action, "enabled": trig == "ticket_created"}).Want(t, 201)
	}

	// The trigger only queues the job, once per automation, ticket and message, however
	// often the trigger runs.
	tk := e.ticket("Login loops", "I can't sign in, the page keeps reloading")
	triggers := e.Jobs(jobs.TicketTriggerArgs{}.Kind(), e.WS.ID)
	if len(triggers) != 1 {
		t.Fatalf("triggers %d", len(triggers))
	}
	var ta jobs.TicketTriggerArgs
	_ = json.Unmarshal(triggers[0], &ta)
	calls := e.F.calls()
	for i := 0; i < 2; i++ {
		if err := tickets.RunTrigger(e.Ctx, e.D, ta); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.procedureRequests()) != 0 || e.F.calls() != calls {
		t.Fatal("the trigger called the model")
	}
	queued := e.Jobs(jobs.AIProcedureRunArgs{}.Kind(), e.WS.ID)
	if len(queued) != 1 {
		t.Fatalf("procedure jobs %d", len(queued))
	}
	var a jobs.AIProcedureRunArgs
	_ = json.Unmarshal(queued[0], &a)
	if a.TicketID != tk.ID || a.ProcedureID != p.ID || a.MessageID == nil {
		t.Fatalf("args %+v", a)
	}

	// It runs once, whatever the procedure's channels; a replay does nothing.
	for i := 0; i < 2; i++ {
		if err := e.S.RunProcedure(e.Ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.procedureRequests()); n != 1 {
		t.Fatalf("model calls %d", n)
	}
	ran := e.procedureRan(tk.ID)
	if len(ran) != 1 || ran[0]["outcome"] != "nothing_to_do" || ran[0]["name"] != "Linear issues" || ran[0]["automation_id"] == nil {
		t.Fatalf("activity %v", ran)
	}
	if n := e.count(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND feature = 'procedure'`, e.WS.ID); n != 1 {
		t.Fatalf("usage %d", n)
	}
	// The ticket, its thread and the procedure are data blocks.
	req := e.procedureRequests()[0]
	if user := userText(req); !strings.Contains(user, "<ticket>") || !strings.Contains(user, "page keeps reloading") ||
		!strings.Contains(user, "<procedure_instructions") || !strings.Contains(user, "Create an issue.") {
		t.Fatalf("user turn %s", user)
	}
}

func TestProcedureRunTools(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	f := e.linear()
	e.Exec(`INSERT INTO tags (workspace_id, name) VALUES ($1, 'bug')`, e.WS.ID)
	p := e.createProcedure(map[string]any{"name": "Linear issues", "when": "Work outside the ticket", "instructions": "Create an issue in Platform, then note it."})
	tk := e.ticket("Login loops", "I can't sign in")
	e.Jobs(jobs.TicketTriggerArgs{}.Kind(), e.WS.ID)
	a := jobs.AIProcedureRunArgs{WorkspaceID: e.WS.ID, TicketID: tk.ID, AutomationID: ids.New(), ProcedureID: p.ID}

	e.F.queue("procedure",
		`@tool linear__list_issues {"title":"login"}`,
		`@tool linear__create_issue {"title":"Login loop"}`+"\n"+`@tool linear__delete_issue {"title":"x"}`+"\n"+`@tool send_reply {"body":"hi"}`,
		`@tool add_note {"body":"Created [ACM-7](https://linear.app/acme/issue/ACM-7/login-loop) <script>x</script>"}`+"\n"+
			`@tool update_ticket {"priority":"high","severity":"sev2","add_labels":["bug","unknown"]}`,
		"Created the issue and noted it.")
	if err := e.S.RunProcedure(e.Ctx, a); err != nil {
		t.Fatal(err)
	}
	reqs := e.procedureRequests()
	names := toolNames(reqs[0])
	for _, want := range []string{"linear__list_issues", "linear__create_issue", "add_note", "update_ticket", "search_knowledge", "search_wiki", "get_wiki_page"} {
		if !containsStr(names, want) {
			t.Fatalf("tools %v lack %s", names, want)
		}
	}
	for _, never := range []string{"linear__delete_issue", "send_reply", "handoff_to_human"} {
		if containsStr(names, never) {
			t.Fatalf("tools %v offer %s", names, never)
		}
	}
	f.mu.Lock()
	if strings.Join(f.calls, ",") != "list_issues,create_issue" || f.modes[0] != integrations.CallRead || f.modes[1] != integrations.CallAuto {
		t.Fatalf("calls %v %v", f.calls, f.modes)
	}
	f.mu.Unlock()

	// The created issue is linked to the ticket as Ollie's; the note is internal and sanitized.
	var actor string
	e.Scalar(`SELECT actor_type FROM ticket_links WHERE ticket_id = $1 AND url = 'https://linear.app/acme/issue/ACM-7/login-loop'`, &actor, tk.ID)
	if actor != "ai" {
		t.Fatalf("link actor %q", actor)
	}
	var note string
	e.Scalar(`SELECT body_html FROM messages WHERE ticket_id = $1 AND kind = 'note' AND ai`, &note, tk.ID)
	if !strings.Contains(note, "linear.app/acme/issue/ACM-7") || strings.Contains(note, "<script>") {
		t.Fatalf("note %s", note)
	}
	if n := e.count(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND kind = 'reply' AND direction = 'outbound'`, tk.ID); n != 0 {
		t.Fatalf("replies %d", n)
	}
	got := e.loadTicket(tk.ID)
	if got.Priority != "high" || got.Severity == nil || *got.Severity != "sev2" || len(got.Tags) != 1 || got.Tags[0].Name != "bug" {
		t.Fatalf("ticket %+v %v", got.Priority, got.Tags)
	}
	ran := e.procedureRan(tk.ID)
	if len(ran) != 1 || ran[0]["outcome"] != "done" || ran[0]["writes"] != float64(3) {
		t.Fatalf("activity %v", ran)
	}
	// Priority is never lowered.
	e.F.queue("procedure", `@tool update_ticket {"priority":"low"}`, "Done.")
	if err := e.S.RunProcedure(e.Ctx, jobs.AIProcedureRunArgs{WorkspaceID: e.WS.ID, TicketID: tk.ID, AutomationID: ids.New(), ProcedureID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if e.loadTicket(tk.ID).Priority != "high" {
		t.Fatal("priority lowered")
	}
}

func TestProcedureRunLimits(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	p := e.createProcedure(map[string]any{"name": "Notes", "when": "Always", "instructions": "Write notes."})
	tk := e.ticket("Hello", "Hi")
	e.Jobs(jobs.TicketTriggerArgs{}.Kind(), e.WS.ID)
	replies := make([]string, 12)
	for i := range replies {
		replies[i] = `@tool add_note {"body":"note"}`
	}
	e.F.queue("procedure", replies...)
	if err := e.S.RunProcedure(e.Ctx, jobs.AIProcedureRunArgs{WorkspaceID: e.WS.ID, TicketID: tk.ID, AutomationID: ids.New(), ProcedureID: p.ID}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.procedureRequests()); n != procedureMaxTurns {
		t.Fatalf("turns %d", n)
	}
	if n := e.count(`SELECT count(*) FROM messages WHERE ticket_id = $1 AND kind = 'note'`, tk.ID); n != procedureMaxWrites {
		t.Fatalf("notes %d", n)
	}
	if ran := e.procedureRan(tk.ID); len(ran) != 1 || ran[0]["writes"] != float64(procedureMaxWrites) {
		t.Fatalf("activity %v", ran)
	}
}

func TestProcedureRunSkips(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	p := e.createProcedure(map[string]any{"name": "Notes", "when": "Always", "instructions": "Write notes."})
	tk := e.ticket("Hello", "Hi")
	e.Jobs(jobs.TicketTriggerArgs{}.Kind(), e.WS.ID)
	args := func() jobs.AIProcedureRunArgs {
		return jobs.AIProcedureRunArgs{WorkspaceID: e.WS.ID, TicketID: tk.ID, AutomationID: ids.New(), ProcedureID: p.ID}
	}
	run := func(a jobs.AIProcedureRunArgs, created time.Time) error {
		w := &ProcedureRunWorker{S: e.S}
		return w.Work(e.Ctx, &river.Job[jobs.AIProcedureRunArgs]{JobRow: &rivertype.JobRow{CreatedAt: created}, Args: a})
	}

	// A procedure turned off or deleted is skipped.
	e.Do("PATCH", "/v1/ai/procedures/"+p.ID.String(), e.admin(), map[string]any{"enabled": false}).Want(t, 200)
	if err := run(args(), time.Now()); err != nil {
		t.Fatal(err)
	}
	e.Do("PATCH", "/v1/ai/procedures/"+p.ID.String(), e.admin(), map[string]any{"enabled": true}).Want(t, 200)
	// AI turned off.
	e.settings(map[string]any{"enabled": false})
	if err := run(args(), time.Now()); err != nil {
		t.Fatal(err)
	}
	e.settings(map[string]any{"enabled": true})
	// The provider fails: skipped quietly.
	e.F.fail(503, 503, 503, 503, 503, 503)
	if err := run(args(), time.Now()); err != nil {
		t.Fatalf("provider failure: %v", err)
	}
	if ran := e.procedureRan(tk.ID); len(ran) != 0 {
		t.Fatalf("activity %v", ran)
	}
	// Nothing was written, so the claim is given back and a retry may run it.
	if claims := e.count(`SELECT count(*) FROM ai_procedure_runs WHERE ticket_id = $1`, tk.ID); claims != 0 {
		t.Fatalf("a run that wrote nothing kept %d claims", claims)
	}
	// 200 runs an hour.
	for i := 0; i < procedureHourly; i++ {
		e.Exec(`INSERT INTO ai_procedure_runs (workspace_id, ticket_id, automation_id, procedure_id, trigger_key) VALUES ($1, $2, $3, $4, '')`,
			e.WS.ID, tk.ID, ids.New(), p.ID)
	}
	before := len(e.procedureRequests())
	if err := run(args(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(e.procedureRequests()) != before {
		t.Fatal("ran past the hourly limit")
	}
	e.Exec(`DELETE FROM ai_procedure_runs WHERE workspace_id = $1`, e.WS.ID)

	// From call_ended, a recorded call without its transcript waits, up to 15 minutes.
	call := ids.New()
	e.Exec(`INSERT INTO phone_calls (id, workspace_id, provider, external_call_id, ticket_id, direction, stage, has_recording, summary)
		VALUES ($1, $2, 'aircall', 'c1', $3, 'inbound', 'ended', true, 'Customer cannot sign in.')`, call, e.WS.ID, tk.ID)
	a := args()
	a.CallID = &call
	err := run(a, time.Now())
	var snooze *rivertype.JobSnoozeError
	if err == nil || !errors.As(err, &snooze) {
		t.Fatalf("want a snooze, got %v", err)
	}
	if err := run(a, time.Now().Add(-16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if user := userText(e.procedureRequests()[len(e.procedureRequests())-1]); !strings.Contains(user, "<calls>") || !strings.Contains(user, "Customer cannot sign in.") {
		t.Fatalf("calls %s", user)
	}
}
