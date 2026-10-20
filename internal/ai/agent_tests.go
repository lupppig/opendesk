package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/plan"
)

// The agent quality loop (contracts/agent.md "Tests"): saved test conversations with
// expectations. Runs (agent_test_runs.go) answer them with the playground engine and
// grade the answers; improving an answer (agent_review.go) can save one.

const (
	maxAgentTests       = 200
	maxTestMessages     = 6
	maxTestExpectations = 10
	maxExpectationRunes = 1000
	testNameRunes       = 100
)

// Expectation kinds.
const (
	expectMentions    = "mentions"
	expectNotMentions = "not_mentions"
	expectHandoff     = "handoff"
	expectNoHandoff   = "no_handoff"
	expectUsesTool    = "uses_tool"
	expectCites       = "cites_knowledge"
)

var toolKeyRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// AgentExpectation is one thing a test expects of the agent's answer.
type AgentExpectation struct {
	Kind  string `json:"kind" enum:"mentions,not_mentions,handoff,no_handoff,uses_tool,cites_knowledge"`
	Value string `json:"value,omitempty" maxLength:"1000" doc:"mentions / not_mentions: the content; uses_tool: the tool as <server_slug>__<tool>; empty otherwise"`
}

// AgentTest is a saved test conversation (contracts/agent.md "Tests").
type AgentTest struct {
	ID           uuid.UUID           `json:"id"`
	Name         string              `json:"name"`
	Messages     []PlaygroundMessage `json:"messages"`
	Expectations []AgentExpectation  `json:"expectations"`
	Visitor      *PlaygroundVisitor  `json:"visitor"`
	LastResult   *AgentTestResult    `json:"last_result" doc:"The test's result in the latest run that finished it"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

// AgentTestInput is POST /v1/ai/agent/tests.
type AgentTestInput struct {
	Name         string              `json:"name,omitempty" maxLength:"200" doc:"Default: the first visitor message"`
	Messages     []PlaygroundMessage `json:"messages" minItems:"1" maxItems:"6" doc:"The last one is the visitor's"`
	Expectations []AgentExpectation  `json:"expectations" minItems:"1" maxItems:"10"`
	Visitor      *PlaygroundVisitor  `json:"visitor,omitempty"`
}

// AgentTestPatch is PATCH /v1/ai/agent/tests/{id}; absent members keep their value.
type AgentTestPatch struct {
	Name         *string              `json:"name,omitempty" maxLength:"200"`
	Messages     *[]PlaygroundMessage `json:"messages,omitempty"`
	Expectations *[]AgentExpectation  `json:"expectations,omitempty"`
	Visitor      *PlaygroundVisitor   `json:"visitor,omitempty" doc:"An empty object clears it"`
}

// AgentTestResult is a test's outcome in one run.
type AgentTestResult struct {
	RunID      uuid.UUID         `json:"run_id"`
	TestID     uuid.UUID         `json:"test_id"`
	Status     string            `json:"status" enum:"pending,running,passed,failed,error"`
	AnswerHTML string            `json:"answer_html"`
	Handoff    bool              `json:"handoff"`
	Tools      []string          `json:"tools" doc:"Display names of the tools the agent ran"`
	Sources    []CitedSource     `json:"sources" doc:"The passages the answer cited"`
	Action     *PlaygroundAction `json:"action" doc:"A tool call that needs confirmation; never run in tests"`
	Checks     []AgentTestCheck  `json:"checks"`
	Error      *string           `json:"error"`
	USD        float64           `json:"usd" doc:"What answering and grading the test cost, in US dollars"`
	// Draft reports that the run used unsaved settings.
	Draft bool `json:"draft"`
	// PreviousStatus is the test's final status in the run before, so a newly failing test stands out.
	PreviousStatus *string    `json:"previous_status" enum:"passed,failed,error"`
	FinishedAt     *time.Time `json:"finished_at"`
}

// AgentTestCheck is one expectation's verdict.
type AgentTestCheck struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`
}

type agentTestList struct {
	Data []AgentTest `json:"data"`
}

var (
	errTestNotFound = httpx.NotFound("Test")
	errTestLimit    = httpx.E(http.StatusUnprocessableEntity, "limit_reached", "A workspace can have at most 200 tests.")
)

func (s *Service) registerAgentQuality(api huma.API) {
	tags := []string{"AI"}
	read := func(ctx context.Context) (*auth.Principal, error) {
		p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
		return p, httpx.Wrap(err)
	}
	write := func(ctx context.Context) (*auth.Principal, error) {
		p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
		return p, httpx.Wrap(err)
	}

	huma.Register(api, huma.Operation{OperationID: "ai-agent-list-tests", Method: http.MethodGet, Path: "/v1/ai/agent/tests",
		Summary: "List agent tests", Description: "Saved test conversations of the widget agent, oldest first, each with its latest result.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body agentTestList }, error) {
			p, err := read(ctx)
			if err != nil {
				return nil, err
			}
			list, err := loadTests(ctx, s.d.DB.Pool, p.WorkspaceID, nil)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body agentTestList }{agentTestList{Data: list}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-agent-create-test", Method: http.MethodPost, Path: "/v1/ai/agent/tests",
		Summary: "Create an agent test", Description: "One to six messages, the last the visitor's, and what the agent's answer must do.",
		Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct{ Body AgentTestInput }) (*struct{ Body AgentTest }, error) {
			p, err := write(ctx)
			if err != nil {
				return nil, err
			}
			var t AgentTest
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var err error
				t, err = s.createTest(ctx, tx, p, in.Body)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body AgentTest }{t}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-agent-get-test", Method: http.MethodGet, Path: "/v1/ai/agent/tests/{id}",
		Summary: "Get an agent test", Tags: tags},
		func(ctx context.Context, in *struct {
			ID uuid.UUID `path:"id"`
		}) (*struct{ Body AgentTest }, error) {
			p, err := read(ctx)
			if err != nil {
				return nil, err
			}
			t, err := loadTest(ctx, s.d.DB.Pool, p.WorkspaceID, in.ID)
			if err != nil {
				return nil, testErr(err)
			}
			return &struct{ Body AgentTest }{t}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-agent-update-test", Method: http.MethodPatch, Path: "/v1/ai/agent/tests/{id}",
		Summary: "Update an agent test", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   uuid.UUID `path:"id"`
			Body AgentTestPatch
		}) (*struct{ Body AgentTest }, error) {
			p, err := write(ctx)
			if err != nil {
				return nil, err
			}
			t, err := s.updateTest(ctx, p, in.ID, in.Body)
			if err != nil {
				return nil, testErr(err)
			}
			return &struct{ Body AgentTest }{t}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-agent-delete-test", Method: http.MethodDelete, Path: "/v1/ai/agent/tests/{id}",
		Summary: "Delete an agent test", Description: "Its results go with it.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID uuid.UUID `path:"id"`
		}) (*struct{}, error) {
			p, err := write(ctx)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var name string
				if err := tx.QueryRow(ctx, `DELETE FROM ai_agent_tests WHERE workspace_id = $1 AND id = $2 RETURNING name`, p.WorkspaceID, in.ID).Scan(&name); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "ai.agent_test_deleted", "ai_agent_test", &in.ID, map[string]any{"name": name}); err != nil {
					return err
				}
				_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "ai_agent_test.deleted", Admins: true, Data: map[string]any{"test_id": in.ID}})
				return err
			})
			if err != nil {
				return nil, testErr(err)
			}
			return nil, nil
		})

	s.registerAgentRuns(api, read, write)
	s.registerAgentReview(api, read, write)
}

func testErr(err error) error {
	if db.IsNotFound(err) {
		return errTestNotFound
	}
	return httpx.Wrap(err)
}

// --- Validation -------------------------------------------------------------------------

// cleanTestName normalizes a test's name like other names people choose.
func cleanTestName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, s)
	s = displayname.Normalize(s)
	if utf8.RuneCountInString(s) > testNameRunes {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:testNameRunes-1])) + "…"
	}
	return s
}

// cleanMessages checks a test's conversation: 1..6 messages of 1..4,000 characters, the
// last one the visitor's.
func cleanMessages(in []PlaygroundMessage) ([]PlaygroundMessage, error) {
	if len(in) < 1 || len(in) > maxTestMessages {
		return nil, httpx.Validation("messages must be 1 to 6")
	}
	out := make([]PlaygroundMessage, 0, len(in))
	for _, m := range in {
		if m.Role != "visitor" && m.Role != "agent" {
			return nil, httpx.Validation("each message role must be visitor or agent")
		}
		text := strings.TrimSpace(strings.ToValidUTF8(m.Text, ""))
		if text == "" || utf8.RuneCountInString(text) > 4000 {
			return nil, httpx.Validation("each message text must be 1 to 4000 characters")
		}
		out = append(out, PlaygroundMessage{Role: m.Role, Text: text})
	}
	if out[len(out)-1].Role != "visitor" {
		return nil, httpx.Validation("the last message must be the visitor's")
	}
	return out, nil
}

// cleanExpectations checks a test's expectations: 1..10, no duplicates, not both
// handoff and no_handoff.
func cleanExpectations(in []AgentExpectation) ([]AgentExpectation, error) {
	if len(in) < 1 || len(in) > maxTestExpectations {
		return nil, httpx.Validation("expectations must be 1 to 10")
	}
	out := make([]AgentExpectation, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		v := strings.Join(strings.Fields(strings.ToValidUTF8(e.Value, "")), " ")
		switch e.Kind {
		case expectMentions, expectNotMentions:
			if v == "" || utf8.RuneCountInString(v) > maxExpectationRunes {
				return nil, httpx.Validation(e.Kind + " needs a value of 1 to 1000 characters")
			}
		case expectUsesTool:
			if !toolKeyRe.MatchString(v) {
				return nil, httpx.Validation("uses_tool needs the tool as <server_slug>__<tool>")
			}
		case expectHandoff, expectNoHandoff, expectCites:
			v = ""
		default:
			return nil, httpx.Validation("unknown expectation kind " + e.Kind)
		}
		key := e.Kind + "\x00" + strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, AgentExpectation{Kind: e.Kind, Value: v})
	}
	if seen[expectHandoff+"\x00"] && seen[expectNoHandoff+"\x00"] {
		return nil, httpx.Validation("a test can't expect both a handoff and no handoff")
	}
	return out, nil
}

// cleanVisitor trims the test's visitor; an empty one is none.
func cleanVisitor(v *PlaygroundVisitor) *PlaygroundVisitor {
	if v == nil {
		return nil
	}
	out := PlaygroundVisitor{Name: clip(displayname.Normalize(v.Name), 100), Email: clip(strings.TrimSpace(v.Email), 254), Verified: v.Verified}
	if v.Verified {
		out.UserID = clip(strings.TrimSpace(v.UserID), 200)
	}
	if out == (PlaygroundVisitor{}) {
		return nil
	}
	return &out
}

// defaultTestName is the first visitor message on one line.
func defaultTestName(msgs []PlaygroundMessage) string {
	for _, m := range msgs {
		if m.Role == "visitor" {
			return cleanTestName(m.Text)
		}
	}
	return "Test"
}

// --- Store ------------------------------------------------------------------------------

// createTest validates and saves a test in tx, with its audit entry and event.
func (s *Service) createTest(ctx context.Context, tx pgx.Tx, p *auth.Principal, in AgentTestInput) (AgentTest, error) {
	msgs, err := cleanMessages(in.Messages)
	if err != nil {
		return AgentTest{}, err
	}
	exp, err := cleanExpectations(in.Expectations)
	if err != nil {
		return AgentTest{}, err
	}
	name := cleanTestName(in.Name)
	if name == "" {
		name = defaultTestName(msgs)
	}
	wid := p.WorkspaceID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text || ':ai_agent_tests'))`, wid); err != nil {
		return AgentTest{}, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_agent_tests WHERE workspace_id = $1`, wid).Scan(&n); err != nil {
		return AgentTest{}, err
	}
	if n >= maxAgentTests {
		return AgentTest{}, errTestLimit
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `INSERT INTO ai_agent_tests (id, workspace_id, name, messages, expectations, visitor, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, wid, name, msgs, exp, cleanVisitor(in.Visitor), p.ActorUserID()); err != nil {
		return AgentTest{}, err
	}
	t, err := loadTest(ctx, tx, wid, id)
	if err != nil {
		return AgentTest{}, err
	}
	if err := audit.Log(ctx, tx, p, wid, "ai.agent_test_created", "ai_agent_test", &id, map[string]any{"name": name}); err != nil {
		return AgentTest{}, err
	}
	return t, publishTest(ctx, tx, wid, "ai_agent_test.created", t)
}

func (s *Service) updateTest(ctx context.Context, p *auth.Principal, id uuid.UUID, in AgentTestPatch) (AgentTest, error) {
	wid := p.WorkspaceID
	var out AgentTest
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		cur, err := loadTest(ctx, tx, wid, id)
		if err != nil {
			return err
		}
		changes := []string{}
		if in.Name != nil {
			name := cleanTestName(*in.Name)
			if name == "" {
				return httpx.Validation("name is required")
			}
			if name != cur.Name {
				cur.Name, changes = name, append(changes, "name")
			}
		}
		if in.Messages != nil {
			if cur.Messages, err = cleanMessages(*in.Messages); err != nil {
				return err
			}
			changes = append(changes, "messages")
		}
		if in.Expectations != nil {
			if cur.Expectations, err = cleanExpectations(*in.Expectations); err != nil {
				return err
			}
			changes = append(changes, "expectations")
		}
		if in.Visitor != nil {
			cur.Visitor, changes = cleanVisitor(in.Visitor), append(changes, "visitor")
		}
		if len(changes) == 0 {
			out = cur
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE ai_agent_tests SET name = $3, messages = $4, expectations = $5, visitor = $6, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wid, id, cur.Name, cur.Messages, cur.Expectations, cur.Visitor); err != nil {
			return err
		}
		if out, err = loadTest(ctx, tx, wid, id); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "ai.agent_test_updated", "ai_agent_test", &id, map[string]any{"name": out.Name, "changes": changes}); err != nil {
			return err
		}
		return publishTest(ctx, tx, wid, "ai_agent_test.updated", out)
	})
	return out, err
}

func publishTest(ctx context.Context, tx pgx.Tx, wid uuid.UUID, typ string, t AgentTest) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Admins: true, Data: map[string]any{"test": t}})
	return err
}

// resultCols reads a result x of run xr, with prev.status the test's status in the run
// before (the joins are resultJoins).
const resultCols = `x.run_id, x.test_id, x.status, x.answer_html, x.handoff, x.tools, x.sources, x.action, x.checks, x.error,
	x.micro_usd, x.finished_at, (xr.settings IS NOT NULL), prev.status`

const prevJoin = `LEFT JOIN LATERAL (SELECT y.status FROM ai_agent_test_results y JOIN ai_agent_test_runs yr ON yr.id = y.run_id
		WHERE y.workspace_id = x.workspace_id AND y.test_id = x.test_id AND y.status IN ('passed', 'failed', 'error')
		AND yr.created_at < xr.created_at ORDER BY yr.created_at DESC LIMIT 1) prev ON true`

// resultScan holds a result's columns, all nullable for outer joins.
type resultScan struct {
	runID, testID          *uuid.UUID
	status, answer         *string
	handoff, draft         *bool
	tools                  []string
	sources, action, check []byte
	errText, prev          *string
	usd                    *int64
	finished               *time.Time
}

func (r *resultScan) dest() []any {
	return []any{&r.runID, &r.testID, &r.status, &r.answer, &r.handoff, &r.tools, &r.sources, &r.action, &r.check, &r.errText,
		&r.usd, &r.finished, &r.draft, &r.prev}
}

func (r *resultScan) result() *AgentTestResult {
	if r.runID == nil || r.testID == nil {
		return nil
	}
	out := &AgentTestResult{RunID: *r.runID, TestID: *r.testID, Status: deref(r.status), AnswerHTML: deref(r.answer), Tools: r.tools,
		Sources: []CitedSource{}, Checks: []AgentTestCheck{}, Error: r.errText, PreviousStatus: r.prev, FinishedAt: r.finished}
	if r.handoff != nil {
		out.Handoff = *r.handoff
	}
	if r.draft != nil {
		out.Draft = *r.draft
	}
	if r.usd != nil {
		out.USD = plan.Micro(*r.usd).USD()
	}
	if out.Tools == nil {
		out.Tools = []string{}
	}
	if len(r.sources) > 0 {
		_ = json.Unmarshal(r.sources, &out.Sources)
	}
	if len(r.check) > 0 {
		_ = json.Unmarshal(r.check, &out.Checks)
	}
	if len(r.action) > 0 && string(r.action) != "null" {
		var a PlaygroundAction
		if json.Unmarshal(r.action, &a) == nil {
			out.Action = &a
		}
	}
	return out
}

// loadTests returns a workspace's tests (or the one id) with their latest final result.
func loadTests(ctx context.Context, q db.Querier, wid uuid.UUID, id *uuid.UUID) ([]AgentTest, error) {
	rows, err := q.Query(ctx, `SELECT t.id, t.name, t.messages, t.expectations, t.visitor, t.created_at, t.updated_at, `+resultCols+`
		FROM ai_agent_tests t
		LEFT JOIN LATERAL (SELECT y.run_id FROM ai_agent_test_results y JOIN ai_agent_test_runs yr ON yr.id = y.run_id
			WHERE y.workspace_id = t.workspace_id AND y.test_id = t.id AND y.status IN ('passed', 'failed', 'error')
			ORDER BY yr.created_at DESC LIMIT 1) lr ON true
		LEFT JOIN ai_agent_test_results x ON x.run_id = lr.run_id AND x.test_id = t.id
		LEFT JOIN ai_agent_test_runs xr ON xr.id = x.run_id
		`+prevJoin+`
		WHERE t.workspace_id = $1 AND ($2::uuid IS NULL OR t.id = $2)
		ORDER BY t.created_at, t.id`, wid, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentTest{}
	for rows.Next() {
		var t AgentTest
		var r resultScan
		if err := rows.Scan(append([]any{&t.ID, &t.Name, &t.Messages, &t.Expectations, &t.Visitor, &t.CreatedAt, &t.UpdatedAt}, r.dest()...)...); err != nil {
			return nil, err
		}
		if t.Messages == nil {
			t.Messages = []PlaygroundMessage{}
		}
		if t.Expectations == nil {
			t.Expectations = []AgentExpectation{}
		}
		t.LastResult = r.result()
		out = append(out, t)
	}
	return out, rows.Err()
}

func loadTest(ctx context.Context, q db.Querier, wid, id uuid.UUID) (AgentTest, error) {
	list, err := loadTests(ctx, q, wid, &id)
	if err != nil {
		return AgentTest{}, err
	}
	if len(list) == 0 {
		return AgentTest{}, pgx.ErrNoRows
	}
	return list[0], nil
}

// testToolName finds the display name of a tool key among the workspace's tools.
func testToolName(tools []AgentTool, key string) (string, bool) {
	i := slices.IndexFunc(tools, func(t AgentTool) bool { return toolKey(t) == key })
	if i < 0 {
		return "", false
	}
	return tools[i].displayName(), true
}
