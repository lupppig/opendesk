package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/plan"
)

// AIConfig is GET /v1/ai/config.
type AIConfig struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider" enum:"openrouter,anthropic,openai,none"`
	Models   struct {
		Default string `json:"default"`
		Fast    string `json:"fast"`
	} `json:"models"`
	Embeddings bool               `json:"embeddings"`
	Features   hd.AISettings      `json:"features"`
	Costs      map[string]float64 `json:"costs" doc:"What a typical call of each feature costs on the workspace's chosen models, in US dollars"`
	// Metered reports that AI usage is charged on each call's real cost (contracts/billing.md).
	Metered bool `json:"metered"`
	// AssistantName is the name AI acts under: the assistant, and the actor of every
	// change AI makes.
	AssistantName string `json:"assistant_name"`
	// Decisions is the decision engine (Jev, the LLM fallback, or none).
	Decisions hd.DecisionEngine `json:"decisions"`
	// OwnKey reports that the workspace's LLM calls run on its own provider key and
	// charge nothing to the AI balance (GET /v1/ai/provider).
	OwnKey bool `json:"own_key"`
}

// ModelList is GET /v1/ai/models.
type ModelList struct {
	Data    []ModelInfo `json:"data"`
	Default string      `json:"default" doc:"The instance default model (AI_MODEL)"`
	Fast    string      `json:"fast" doc:"The instance fast model (AI_FAST_MODEL)"`
}

func (s *Service) register(api huma.API) {
	tags := []string{"AI"}
	s.registerAgent(api)
	s.registerSetup(api)
	s.registerSuggested(api)
	s.registerProvider(api)

	huma.Register(api, huma.Operation{OperationID: "ai-config", Method: http.MethodGet, Path: "/v1/ai/config",
		Summary: "AI configuration", Description: "Whether AI is available, the models, and the workspace's AI settings.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body AIConfig }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			st, err := hd.LoadSettings(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := AIConfig{Enabled: s.Enabled(), Provider: "none", Features: st.AI(), Costs: map[string]float64{}, AssistantName: AssistantName,
				Decisions: hd.Engine(s.d.Cfg), Metered: true}
			for k, v := range plan.Typical {
				out.Costs[k] = v.USD()
			}
			if s.Enabled() {
				rt, err := s.routeFor(ctx, p.WorkspaceID)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				out.Provider, out.OwnKey = rt.p.Name(), rt.own
				out.Models.Default, out.Models.Fast = s.tierModelOn(ctx, rt, out.Features, tierDefault), s.tierModelOn(ctx, rt, out.Features, tierFast)
				out.Embeddings = s.semantic()
				s.featureCosts(ctx, out.Features, out.Costs, rt.own)
			}
			return &struct{ Body AIConfig }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-models", Method: http.MethodGet, Path: "/v1/ai/models",
		Summary: "AI models", Description: "The models a workspace may choose, with prices in US dollars per million tokens as the workspace pays them, when known. " +
			"On the workspace's own key, its provider's models (no prices: the workspace pays its provider).", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body ModelList }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			rt, err := s.routeFor(ctx, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if rt.own {
				return &struct{ Body ModelList }{ModelList{Data: s.ownModelList(ctx, rt), Default: rt.models.Default, Fast: rt.models.Fast}}, nil
			}
			return &struct{ Body ModelList }{ModelList{Data: s.Catalog(ctx), Default: s.models.Default, Fast: s.models.Fast}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-update-settings", Method: http.MethodPatch, Path: "/v1/ai/settings",
		Summary: "Update AI settings", Description: "Admins turn AI features on and off and set the reply tone, language and instructions.", Tags: tags},
		func(ctx context.Context, in *struct{ Body hd.AISettingsPatch }) (*struct{ Body hd.AISettings }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			patch, err := hd.ValidateAIPatch(in.Body, s.modelCheck(ctx, p.WorkspaceID))
			if err != nil {
				return nil, err
			}
			var out hd.AISettings
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if len(patch) > 0 {
					if _, err := tx.Exec(ctx, `UPDATE workspaces SET settings = jsonb_set(settings, '{ai}', COALESCE(settings->'ai', '{}'::jsonb) || $2::jsonb),
						updated_at = now() WHERE id = $1`, p.WorkspaceID, patch); err != nil {
						return err
					}
					keys := make([]string, 0, len(patch))
					for k := range patch {
						keys = append(keys, k)
					}
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "ai.settings_updated", "workspace", &p.WorkspaceID,
						map[string]any{"changes": keys}); err != nil {
						return err
					}
				}
				st, err := hd.LoadSettings(ctx, tx, p.WorkspaceID)
				out = st.AI()
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body hd.AISettings }{out}, nil
		})

	streamDoc := " Send `Accept: text/event-stream` to stream `delta` events ({text}) and a final `done` event with this response; the call is charged once, at done."

	huma.Register(api, huma.Operation{OperationID: "ai-draft-reply", Method: http.MethodPost, Path: "/v1/tickets/{id}/ai/draft",
		Summary: "Draft a reply", Description: "Drafts a reply from the thread, the contact, saved replies and the best knowledge base matches. Not sent." + streamDoc,
		Tags: tags, Middlewares: huma.Middlewares{s.sse(s.streamDraft)}},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body DraftInput
		}) (*struct{ Body DraftOutput }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.Draft(ctx, p, in.ID, in.Body, nil)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body DraftOutput }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-summarize-ticket", Method: http.MethodPost, Path: "/v1/tickets/{id}/ai/summary",
		Summary: "Summarize a ticket", Description: "Stores the summary in custom.ai_summary and publishes ticket.updated.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body *struct{}
		}) (*struct{ Body SummaryOutput }, error) {
			// The summary is stored on the ticket: a write, as an agent.
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			tid, err := resolveVisibleTicket(ctx, s.d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.Summarize(ctx, p.WorkspaceID, p.ActorUserID(), tid)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body SummaryOutput }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-ask-ticket", Method: http.MethodPost, Path: "/v1/tickets/{id}/ai/ask",
		Summary: "Ask about a ticket", Description: "Copilot: answers an agent's question about the ticket." + streamDoc,
		Tags: tags, Middlewares: huma.Middlewares{s.sse(s.streamAsk)}},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body AskInput
		}) (*struct{ Body AskOutput }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.Ask(ctx, p, in.ID, in.Body, nil)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body AskOutput }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-triage-ticket", Method: http.MethodPost, Path: "/v1/tickets/{id}/ai/triage",
		Summary: "Triage a ticket", Description: "Decides labels (from the workspace's labels), priority, severity, team, sentiment, language and category, " +
			"on Jev when configured. With apply, sets them, creating suggested labels, attributed to ai.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Apply bool `json:"apply,omitempty"`
			}
		}) (*struct{ Body Triage }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.TriageTicket(ctx, p, in.ID, in.Body.Apply)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body Triage }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-dismiss-suggestion", Method: http.MethodDelete,
		Path: "/v1/tickets/{id}/ai/suggestions/{field}", DefaultStatus: http.StatusNoContent,
		Summary: "Dismiss a triage suggestion", Description: "Removes Ollie's pending suggestion for one field. Accepting is setting the field.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID    string `path:"id"`
			Field string `path:"field" enum:"priority,severity,labels,team,spam"`
		}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.DismissSuggestion(ctx, p, in.ID, in.Field); err != nil {
				return nil, apiErr(err)
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-rewrite", Method: http.MethodPost, Path: "/v1/ai/rewrite",
		Summary: "Rewrite text", Description: "Changes the tone or length of a draft, or fixes its grammar." + streamDoc,
		Tags: tags, Middlewares: huma.Middlewares{s.sse(s.streamRewrite)}},
		func(ctx context.Context, in *struct{ Body RewriteInput }) (*struct{ Body RewriteOutput }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.Rewrite(ctx, p, in.Body, nil)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body RewriteOutput }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-translate", Method: http.MethodPost, Path: "/v1/ai/translate",
		Summary: "Translate text", Description: "Translates html or text into a BCP 47 target language.", Tags: tags},
		func(ctx context.Context, in *struct{ Body TranslateInput }) (*struct{ Body TranslateOutput }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.Translate(ctx, p, in.Body)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body TranslateOutput }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-draft-kb-article", Method: http.MethodPost, Path: "/v1/kb/articles/ai/draft",
		Summary: "Draft a knowledge base article", Description: "Drafts an article from resolved tickets and/or a topic. Not saved.", Tags: tags},
		func(ctx context.Context, in *struct{ Body KBDraftInput }) (*struct{ Body KBDraftOutput }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeKBWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.KBDraft(ctx, p, in.Body)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body KBDraftOutput }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-search", Method: http.MethodGet, Path: "/v1/ai/search",
		Summary: "Semantic search", Description: "Tickets and articles by meaning (embeddings fused with full-text search; full-text only without embeddings), " +
			"and contacts. Same shape as /v1/search.", Tags: tags},
		func(ctx context.Context, in *struct {
			Q string `query:"q" required:"true" minLength:"1" maxLength:"500"`
		}) (*struct{ Body AISearchResults }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, "")
			if err != nil && !errors.Is(err, auth.ErrForbidden) {
				return nil, httpx.Wrap(err)
			}
			if p == nil {
				// API keys need at least one read scope.
				p = auth.FromContext(ctx)
				if p == nil || !(p.HasScope(auth.ScopeTicketsRead) || p.HasScope(auth.ScopeContactsRead) || p.HasScope(auth.ScopeKBRead)) {
					return nil, httpx.Forbidden()
				}
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			q := strings.TrimSpace(in.Q)
			if q == "" {
				return nil, httpx.Validation("q is required")
			}
			if _, err := s.workspaceAI(ctx, p.WorkspaceID); err != nil {
				return nil, err
			}
			out, err := s.Search(ctx, p, q)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body AISearchResults }{*out}, nil
		})
}

// --- Server-sent events --------------------------------------------------------------

// sse is an operation middleware: requests that accept text/event-stream are served by
// h on the raw response, everything else by the JSON operation. Auth is the same
// principal; errors before the first event are the same problem responses.
func (s *Service) sse(h func(w http.ResponseWriter, r *http.Request, id string)) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !strings.Contains(ctx.Header("Accept"), "text/event-stream") {
			next(ctx)
			return
		}
		r, w := humachi.Unwrap(ctx)
		h(w, r, ctx.Param("id"))
	}
}

// eventStream writes SSE lazily: the 200 and headers go out with the first event, so
// an error before any output is still a normal problem response.
type eventStream struct {
	w       http.ResponseWriter
	r       *http.Request
	rc      *http.ResponseController
	mu      sync.Mutex
	started bool
}

func newEventStream(w http.ResponseWriter, r *http.Request) *eventStream {
	return &eventStream{w: w, r: r, rc: http.NewResponseController(w)}
}

func (e *eventStream) send(event string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		h := e.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Accel-Buffering", "no")
		_ = e.rc.SetWriteDeadline(time.Now().Add(streamTimeout + 30*time.Second))
		e.w.WriteHeader(http.StatusOK)
		e.started = true
	}
	raw, _ := json.Marshal(data)
	_, _ = io.WriteString(e.w, "event: "+event+"\ndata: "+string(raw)+"\n\n")
	_ = e.rc.Flush()
}

// fail reports err: a problem response before the stream started, an error event after.
func (e *eventStream) fail(err error) {
	he := problemOf(apiErr(err))
	he.RequestID = httpx.RequestIDFrom(e.r.Context())
	e.mu.Lock()
	started := e.started
	e.mu.Unlock()
	if started {
		e.send("error", he)
		return
	}
	e.w.Header().Set("Content-Type", "application/problem+json")
	e.w.WriteHeader(he.Status)
	_ = json.NewEncoder(e.w).Encode(he)
}

// problemOf turns any error from apiErr into a problem body with a safe message.
func problemOf(err error) *httpx.Error {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he
	}
	var se huma.StatusError
	if errors.As(err, &se) && se.GetStatus() < 500 {
		return httpx.E(se.GetStatus(), "", err.Error())
	}
	return httpx.E(http.StatusInternalServerError, "internal", "Something went wrong on our side. Quote the request id if you contact support.")
}

func decodeBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil {
		return httpx.BadRequest("could not read the request body")
	}
	if len(raw) > 1<<20 {
		return httpx.E(http.StatusRequestEntityTooLarge, "too_large", "request body too large")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return httpx.E(http.StatusUnprocessableEntity, "validation_failed", "request body must be valid JSON")
	}
	return nil
}

func (s *Service) streamFeature(w http.ResponseWriter, r *http.Request, role auth.Role, scope string, body any,
	run func(ctx context.Context, p *auth.Principal, onDelta func(string)) (any, error)) {
	es := newEventStream(w, r)
	ctx := r.Context()
	p, err := auth.RequireWorkspace(ctx, role, scope)
	if err != nil {
		es.fail(httpx.Wrap(err))
		return
	}
	if err := decodeBody(r, body); err != nil {
		es.fail(err)
		return
	}
	out, err := run(ctx, p, func(t string) { es.send("delta", map[string]string{"text": t}) })
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		es.fail(err)
		return
	}
	es.send("done", out)
}

func (s *Service) streamDraft(w http.ResponseWriter, r *http.Request, id string) {
	var in DraftInput
	s.streamFeature(w, r, auth.RoleAgent, auth.ScopeTicketsWrite, &in, func(ctx context.Context, p *auth.Principal, d func(string)) (any, error) {
		return s.Draft(ctx, p, id, in, d)
	})
}

func (s *Service) streamAsk(w http.ResponseWriter, r *http.Request, id string) {
	var in AskInput
	s.streamFeature(w, r, auth.RoleViewer, auth.ScopeTicketsRead, &in, func(ctx context.Context, p *auth.Principal, d func(string)) (any, error) {
		return s.Ask(ctx, p, id, in, d)
	})
}

func (s *Service) streamRewrite(w http.ResponseWriter, r *http.Request, _ string) {
	var in RewriteInput
	s.streamFeature(w, r, auth.RoleAgent, auth.ScopeTicketsWrite, &in, func(ctx context.Context, p *auth.Principal, d func(string)) (any, error) {
		return s.Rewrite(ctx, p, in, d)
	})
}
