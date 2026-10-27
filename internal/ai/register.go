// Package ai is the ai area: providers (Anthropic, OpenAI-compatible), embeddings
// (Voyage, OpenAI-compatible, or Postgres full-text search), and the AI features of
// contracts/ai.md. Register wires its HTTP routes, RegisterWorkers its River workers,
// Periodic its scheduled jobs and Background its long-running loops.
package ai

import (
	"context"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/collab"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

var (
	mu       sync.Mutex
	services = map[*deps.Deps]*Service{}
)

// For returns the process's AI service for d, building it once. It also installs the
// helpdesk hooks (auto-triage, the ai_triage automation action).
func For(d *deps.Deps) *Service {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := services[d]; ok {
		return s
	}
	s := New(context.Background(), d)
	services[d] = s
	s.installHooks()
	collab.SetMailAI(d, s)
	return s
}

func (s *Service) installHooks() {
	if !s.canTriage() && s.decider == nil {
		hd.SetAIHooks(nil)
		return
	}
	h := &hd.AIHooks{}
	if s.canTriage() {
		h.AutoTriage, h.Triage = s.autoTriage, s.triageAction
	}
	// A workspace may bring its own decision engine (decisions_key.go), so conditions
	// are asked whenever the instance has AI.
	if s.decider != nil || s.Enabled() {
		h.Decide = s.decideConditions
	}
	if s.Enabled() {
		h.Engine = s.workspaceEngine
	}
	hd.SetAIHooks(h)
}

// Register adds this area's routes. Streaming variants are served by an operation
// middleware on the same routes.
func Register(d *deps.Deps, api huma.API, _ chi.Router) { For(d).register(api) }

// RegisterWorkers adds this area's River workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	s := For(d)
	river.AddWorker(w, &EmbedWorker{S: s})
	river.AddWorker(w, &SummarizeWorker{S: s})
	river.AddWorker(w, &WidgetAnswerWorker{S: s})
	river.AddWorker(w, &WidgetActionWorker{S: s})
	river.AddWorker(w, &EmailAnswerWorker{S: s})
	river.AddWorker(w, &MaintainWorker{S: s})
	river.AddWorker(w, &AgentTestRunWorker{S: s})
}

// Periodic returns this area's scheduled jobs.
func Periodic(d *deps.Deps) []*river.PeriodicJob { return []*river.PeriodicJob{maintainPeriodic()} }

// Background runs until ctx is done, in the worker role only.
func Background(ctx context.Context, d *deps.Deps) {}
