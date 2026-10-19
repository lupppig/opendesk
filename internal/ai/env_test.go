package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/ai/decide"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
)

func TestMain(m *testing.M) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		os.Setenv("TEST_DATABASE_URL", "postgres://opendesk:opendesk@localhost:55432/opendesk_ai?sslmode=disable")
	}
	os.Exit(m.Run())
}

type env struct {
	*hdtest.Env
	S    *Service
	F    *fakeLLM
	TS   *fakeTypeSafe
	WS   hdtest.WS
	Base string
}

type opts struct {
	billing  bool
	disabled bool
	openai   bool
	noEmbed  bool
	// jev decides on a fake TypeSafe server; llmDecide uses the LLM adapter.
	jev       bool
	llmDecide bool
}

// newEnv starts the helpdesk and AI routes against a fake model server.
func newEnv(t *testing.T, o opts) *env {
	t.Helper()
	base := hdtest.Deps(t)
	d := *base
	cfg := *base.Cfg
	cfg.AIProvider, cfg.AnthropicAPIKey = "anthropic", "test-key"
	cfg.AIModel, cfg.AIFastModel = "claude-sonnet-5-5", "claude-haiku-4-5-20251001"
	cfg.EmbeddingsDriver = "openai"
	d.Cfg = &cfg
	d.Plan = plan.New(o.billing, 1.5)
	f := newFake(t)
	s := New(context.Background(), &d)
	var p Provider = NewAnthropic("test-key", f.srv.URL)
	if o.openai {
		p = NewOpenAI(f.srv.URL+"/v1", "k")
	}
	var em Embedder = NewOpenAIEmbedder(f.srv.URL+"/v1", "k", "fake-embed")
	if o.noEmbed {
		em = nil
	}
	s.WithProvider(p, em, cfg.AIModel, cfg.AIFastModel)
	if o.disabled {
		s.provider = nil
	}
	var ts *fakeTypeSafe
	switch {
	case o.jev:
		ts = newFakeTypeSafe(t)
		cfg.DecisionsProvider, cfg.TypesafeAPIKey, cfg.TypesafeModel = "typesafe", "ts-test-key", "jev-latest"
		s.WithDecider(decide.NewTypeSafe(ts.srv.URL, "ts-test-key", "jev-latest"))
	case o.llmDecide:
		cfg.DecisionsProvider = "llm"
		s.WithDecider(&llmDecider{s: s})
	default:
		cfg.DecisionsProvider = "llm"
		s.WithDecider(nil)
	}
	mu.Lock()
	services[&d] = s
	mu.Unlock()
	s.installHooks()
	t.Cleanup(func() { s.installHooks() })

	httpx.Install(d.Log)
	r := chi.NewRouter()
	r.Use(httpx.RequestID, httpx.SecurityHeaders(false))
	resolver := &auth.Resolver{Pool: d.DB.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}
	r.Use(resolver.Middleware)
	api := humachi.New(r, huma.DefaultConfig("opendesk test", "test"))
	helpdesk.Register(&d, api, r)
	Register(&d, api, r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	e := &env{Env: &hdtest.Env{T: t, D: &d, Srv: srv, Ctx: context.Background()}, S: s, F: f, TS: ts}
	e.WS = e.NewWorkspace()
	return e
}

func (e *env) agent() hdtest.Auth  { return e.WS.Agent.As(e.WS) }
func (e *env) viewer() hdtest.Auth { return e.WS.Viewer.As(e.WS) }
func (e *env) admin() hdtest.Auth  { return e.WS.Admin.As(e.WS) }

func (e *env) settings(m map[string]any) {
	e.T.Helper()
	raw, _ := json.Marshal(m)
	e.Exec(`UPDATE workspaces SET settings = jsonb_set(settings, '{ai}', COALESCE(settings->'ai', '{}'::jsonb) || $2::jsonb) WHERE id = $1`, e.WS.ID, raw)
}

// ticket creates an email ticket with a customer's first message.
func (e *env) ticket(subject, text string) domain.Ticket {
	e.T.Helper()
	var tk domain.Ticket
	err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		cid, _, err := domain.UpsertContactByEmail(e.Ctx, tx, e.WS.ID, "jane-"+ids.New().String()[:8]+"@example.com", "Jane Doe")
		if err != nil {
			return err
		}
		tk, err = domain.CreateTicket(e.Ctx, tx, domain.NewTicket{WorkspaceID: e.WS.ID, Subject: subject, Channel: domain.ChannelEmail, ContactID: &cid})
		if err != nil {
			return err
		}
		_, err = domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: e.WS.ID, TicketID: tk.ID, Kind: "reply",
			Direction: "inbound", AuthorType: "contact", AuthorContactID: &cid, BodyHTML: "<p>" + text + "</p>", BodyText: text,
			Trigger: jobs.TriggerTicketCreated})
		return err
	})
	if err != nil {
		e.T.Fatal(err)
	}
	return tk
}

func (e *env) article(title, body, status string) uuid.UUID {
	e.T.Helper()
	id := ids.New()
	e.Exec(`INSERT INTO kb_articles (id, workspace_id, title, slug, excerpt, body_html, status, published_at)
		VALUES ($1, $2, $3, $4, '', $5, $6, now())`, id, e.WS.ID, title, "a-"+id.String()[24:], body, status)
	return id
}

func (e *env) count(sql string, args ...any) int {
	e.T.Helper()
	var n int
	e.Scalar(sql, &n, args...)
	return n
}

func (e *env) loadTicket(id uuid.UUID) domain.Ticket {
	e.T.Helper()
	t, err := domain.LoadTicket(e.Ctx, e.D.DB.Pool, e.WS.ID, id)
	if err != nil {
		e.T.Fatal(err)
	}
	return t
}

// runTriggers runs the queued ticket triggers for the workspace.
func (e *env) runTriggers() {
	e.T.Helper()
	for i := 0; i < 10; i++ {
		batch := e.Jobs(jobs.TicketTriggerArgs{}.Kind(), e.WS.ID)
		if len(batch) == 0 {
			return
		}
		for _, raw := range batch {
			var a jobs.TicketTriggerArgs
			_ = json.Unmarshal(raw, &a)
			if err := tickets.RunTrigger(e.Ctx, e.D, a); err != nil {
				e.T.Fatalf("trigger: %v", err)
			}
		}
	}
}

type sseEvent struct {
	Name string
	Data json.RawMessage
}

// stream posts with Accept: text/event-stream and returns the status, content type and events.
func (e *env) stream(path string, a hdtest.Auth, body any) (int, string, []sseEvent, []byte) {
	e.T.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, e.Srv.URL+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.Bearer)
	req.Header.Set(auth.WorkspaceHeader, a.Workspace.String())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer res.Body.Close()
	ct := res.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		var b strings.Builder
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			b.WriteString(sc.Text())
		}
		return res.StatusCode, ct, nil, []byte(b.String())
	}
	var evs []sseEvent
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var cur sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.Name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
		case line == "":
			if cur.Name != "" {
				evs = append(evs, cur)
			}
			cur = sseEvent{}
		}
	}
	return res.StatusCode, ct, evs, nil
}

// userText returns all user-turn text of a recorded Anthropic request.
func userText(req map[string]any) string {
	var b strings.Builder
	msgs, _ := req["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		switch c := mm["content"].(type) {
		case string:
			b.WriteString(c)
		case []any:
			for _, blk := range c {
				bm, _ := blk.(map[string]any)
				if s, ok := bm["text"].(string); ok {
					b.WriteString(s + "\n")
				}
			}
		}
	}
	return b.String()
}

var _ = deps.Deps{}

func jobFor(a jobs.AIWidgetAnswerArgs) *river.Job[jobs.AIWidgetAnswerArgs] {
	return &river.Job[jobs.AIWidgetAnswerArgs]{JobRow: &rivertype.JobRow{}, Args: a}
}

func jobForMaintain() *river.Job[MaintainArgs] {
	return &river.Job[MaintainArgs]{JobRow: &rivertype.JobRow{}, Args: MaintainArgs{}}
}
