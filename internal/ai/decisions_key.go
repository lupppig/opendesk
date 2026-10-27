package ai

// Where a workspace's decisions run (contracts/ai.md "Provider per workspace"): Jev on
// the workspace's own TypeSafe key, else Jev through OpenRouter on its own OpenRouter
// key, else the LLM adapter on its own provider key when the instance decides with the
// LLM, else the instance's engine (charged to the AI balance). Decisions on the
// workspace's credentials charge nothing and never fall back to the AI balance.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai/decide"
	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// KeyTypeSafe is the kind of a workspace's own decisions key (and of its key failure
// notification).
const KeyTypeSafe = "typesafe"

// Where decisions run, as GET /v1/ai/provider names it.
const (
	DecisionsInstance    = "instance"     // the instance's engine, charged to the AI balance
	DecisionsProviderKey = "provider_key" // the workspace's own provider key
	DecisionsTypeSafeKey = "typesafe_key" // the workspace's own TypeSafe key
	DecisionsNone        = "none"         // no engine
)

// decisionRoute is where one workspace's decisions go.
type decisionRoute struct {
	dc decide.Decider // nil: no engine
	// own: on the workspace's credentials, charged nothing.
	own    bool
	source string
	// rt is the provider key route whose state a failure updates (DecisionsProviderKey).
	rt *route
	// status is the TypeSafe key's state (DecisionsTypeSafeKey).
	status string
}

// decisionKeys caches the deciders built from workspaces' TypeSafe keys, until the key
// changes.
type decisionKeys struct {
	mu sync.Mutex
	m  map[uuid.UUID]*decisionEntry
}

type decisionEntry struct {
	updated time.Time
	dc      *decide.TypeSafe
}

// typesafeModel is the Jev model asked on a workspace's own TypeSafe key.
func (s *Service) typesafeModel() string {
	if s.d.Cfg.DecisionsProvider == "typesafe" {
		return s.d.Cfg.TypesafeModel
	}
	return ""
}

// openRouterJevModel is the Jev model asked through OpenRouter on a workspace's key.
func (s *Service) openRouterJevModel() string {
	if s.d.Cfg.DecisionsProvider == "openrouter" {
		return s.d.Cfg.TypesafeModel
	}
	return ""
}

// decisionsFor returns where the workspace's decisions go. rt is its LLM route when the
// caller has it (nil loads it). A key that cannot be read fails the call.
func (s *Service) decisionsFor(ctx context.Context, wid uuid.UUID, rt *route) (*decisionRoute, error) {
	var sealed []byte
	var status string
	var updated time.Time
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT key_enc, status, updated_at FROM ai_decision_keys WHERE workspace_id = $1`, wid).
		Scan(&sealed, &status, &updated)
	switch {
	case err == nil:
		s.decKeys.mu.Lock()
		defer s.decKeys.mu.Unlock()
		if s.decKeys.m == nil {
			s.decKeys.m = map[uuid.UUID]*decisionEntry{}
		}
		e := s.decKeys.m[wid]
		if e == nil || !e.updated.Equal(updated) {
			key, err := s.d.Box.Open(sealed)
			if err != nil {
				return nil, errors.New("ai: workspace decisions key cannot be opened")
			}
			e = &decisionEntry{updated: updated, dc: decide.NewTypeSafe(s.d.Cfg.TypesafeAPIURL, string(key), s.typesafeModel())}
			s.decKeys.m[wid] = e
		}
		return &decisionRoute{dc: e.dc, own: true, source: DecisionsTypeSafeKey, status: status}, nil
	case !db.IsNotFound(err):
		return nil, err
	}
	if rt == nil {
		if rt, err = s.routeFor(ctx, wid); err != nil {
			return nil, err
		}
	}
	if rt.own && rt.kind == KeyOpenRouter && rt.entry != nil {
		e := rt.entry
		e.jevOnce.Do(func() {
			e.jev = decide.NewOpenRouter(s.d.Cfg.OpenRouterBaseURL, e.key, s.openRouterJevModel(), s.d.Cfg.AppURL)
		})
		return &decisionRoute{dc: e.jev, own: true, source: DecisionsProviderKey, rt: rt}, nil
	}
	if _, ok := s.decider.(*llmDecider); ok && rt.own {
		return &decisionRoute{dc: &llmDecider{s: s, rt: rt}, own: true, source: DecisionsProviderKey, rt: rt}, nil
	}
	if s.decider == nil {
		return &decisionRoute{source: DecisionsNone}, nil
	}
	return &decisionRoute{dc: s.decider, source: DecisionsInstance}, nil
}

// jevFor reports whether the workspace's decisions run on a calibrated engine.
func (s *Service) jevFor(ctx context.Context, wid uuid.UUID) bool {
	dr, err := s.decisionsFor(ctx, wid, nil)
	return err == nil && dr.dc != nil && dr.dc.Calibrated()
}

// workspaceEngine is the hd.AIHooks.Engine hook: Jev on the workspace's own key, else
// not known here (the instance's engine).
func (s *Service) workspaceEngine(ctx context.Context, wid uuid.UUID) (hd.DecisionEngine, bool) {
	dr, err := s.decisionsFor(ctx, wid, nil)
	if err != nil || !dr.own || dr.dc == nil || !dr.dc.Calibrated() {
		return hd.DecisionEngine{}, false
	}
	return hd.DecisionEngine{Provider: "typesafe", Model: dr.dc.Model(), Connected: true}, true
}

// decisionKeyResult is keyResult for a decision on the workspace's credentials: a
// refused or unfunded key is recorded on the key it ran on.
func (s *Service) decisionKeyResult(ctx context.Context, wid uuid.UUID, dr *decisionRoute, err error) error {
	if !dr.own {
		return err
	}
	switch {
	case errors.Is(err, decide.ErrAuth):
		err = errors.Join(ErrUnavailable, ErrKeyRejected)
	case errors.Is(err, decide.ErrNoFunds):
		err = errors.Join(ErrUnavailable, ErrNoFunds)
	}
	if dr.rt != nil {
		return s.keyResult(ctx, wid, dr.rt, err)
	}
	return s.keyResultOn(ctx, wid, tableDecisionKeys, KeyTypeSafe, &dr.status, err)
}

// --- API ---------------------------------------------------------------------------

// DecisionsView is where the workspace's decisions run (GET /v1/ai/provider "decisions").
type DecisionsView struct {
	RunsOn string `json:"runs_on" enum:"instance,provider_key,typesafe_key,none" doc:"instance: the instance's engine (charged to the AI balance when billing is on); provider_key: the workspace's own provider key; typesafe_key: its own TypeSafe key; none: no decision engine"`
	Engine string `json:"engine" enum:"jev,llm,none" doc:"jev: calibrated decisions; llm: the AI model's structured answers"`
	// Key is the workspace's TypeSafe key, when it saved one.
	Key *DecisionsKey `json:"key"`
}

// DecisionsKey describes a saved TypeSafe key without revealing it.
type DecisionsKey struct {
	Last4     string          `json:"last4" doc:"The key's last 4 characters"`
	Status    string          `json:"status" enum:"ok,rejected,no_funds" doc:"What TypeSafe last said about the key"`
	StatusAt  *time.Time      `json:"status_at"`
	CreatedBy *domain.UserRef `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// DecisionsKeyInput is a TypeSafe key to test or save.
type DecisionsKeyInput struct {
	APIKey string `json:"api_key" minLength:"8" maxLength:"500" doc:"A TypeSafe API key; write only"`
}

func (s *Service) decisionsView(ctx context.Context, q db.Querier, wid uuid.UUID) (DecisionsView, error) {
	out := DecisionsView{RunsOn: DecisionsNone, Engine: "none"}
	var k DecisionsKey
	var uid *uuid.UUID
	var name, email, avatar *string
	err := q.QueryRow(ctx, `SELECT k.key_last4, k.status, k.status_at, k.created_at, k.updated_at, u.id, u.name, u.email, u.avatar_url
		FROM ai_decision_keys k LEFT JOIN users u ON u.id = k.created_by WHERE k.workspace_id = $1`, wid).
		Scan(&k.Last4, &k.Status, &k.StatusAt, &k.CreatedAt, &k.UpdatedAt, &uid, &name, &email, &avatar)
	switch {
	case err == nil:
		if uid != nil {
			k.CreatedBy = &domain.UserRef{ID: *uid, Name: *name, Email: *email, AvatarURL: domain.Avatar(*avatar)}
		}
		out.Key = &k
	case !db.IsNotFound(err):
		return out, err
	}
	dr, err := s.decisionsFor(ctx, wid, nil)
	if err != nil {
		return out, err
	}
	out.RunsOn = dr.source
	if dr.dc != nil {
		out.Engine = dr.dc.Name()
	}
	return out, nil
}

// checkDecisionsKey normalizes a candidate TypeSafe key.
func checkDecisionsKey(in *DecisionsKeyInput) error {
	in.APIKey = strings.TrimSpace(in.APIKey)
	if len(in.APIKey) < 8 || len(in.APIKey) > 500 || strings.IndexFunc(in.APIKey, func(r rune) bool { return r <= ' ' || r == 0x7f || r > unicode.MaxASCII }) >= 0 {
		return httpx.Validation("Enter the API key exactly as TypeSafe shows it.")
	}
	return nil
}

// testDecisionsKey asks one tiny question on the key. It never echoes TypeSafe's response.
func (s *Service) testDecisionsKey(ctx context.Context, key string) *ProviderTest {
	dc := decide.NewTypeSafe(s.d.Cfg.TypesafeAPIURL, key, s.typesafeModel())
	dc.Attempts = 1
	_, err := dc.Decide(ctx, decide.Request{State: map[string]any{"text": "Hello"},
		Questions: map[string]decide.Question{"ok": decide.Noul("Is `text` a greeting?", "", "")}})
	switch {
	case err == nil:
		return &ProviderTest{OK: true, Models: []string{}}
	case errors.Is(err, decide.ErrAuth):
		return testFailure("invalid_key", "TypeSafe rejected this key. Check that it's correct and still active.")
	case errors.Is(err, decide.ErrNoFunds):
		return testFailure("no_funds", "The TypeSafe account behind this key is out of credits.")
	case errors.Is(err, decide.ErrBusy):
		return testFailure("busy", "TypeSafe is rate limiting this key. Try again in a moment.")
	}
	return testFailure("unreachable", "Couldn't reach TypeSafe. Try again in a moment.")
}

func (s *Service) registerDecisionsKey(api huma.API) {
	tags := []string{"AI"}
	limited := func(p *auth.Principal) error {
		if !s.d.Limiter.Allow("ai:provider_test:"+p.WorkspaceID.String(), 20, time.Minute) {
			return httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many tests. Try again in a minute.")
		}
		return nil
	}

	huma.Register(api, huma.Operation{OperationID: "ai-test-decisions-key", Method: http.MethodPost, Path: "/v1/ai/provider/decisions/test",
		Summary: "Test a TypeSafe key", Description: "Checks a TypeSafe key with one tiny decision before it is saved. Nothing is stored.", Tags: tags},
		func(ctx context.Context, in *struct{ Body DecisionsKeyInput }) (*struct{ Body ProviderTest }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			if err := limited(p); err != nil {
				return nil, err
			}
			body := in.Body
			if err := checkDecisionsKey(&body); err != nil {
				return nil, err
			}
			return &struct{ Body ProviderTest }{*s.testDecisionsKey(ctx, body.APIKey)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-set-decisions-key", Method: http.MethodPut, Path: "/v1/ai/provider/decisions",
		Summary: "Run decisions on the workspace's own TypeSafe key", Description: "Tests the key, then stores it encrypted. Every Jev decision of the workspace " +
			"(triage, automation conditions, the widget relevance check, mail categories) then runs on it and charges nothing to the AI balance. Replaces a saved key.", Tags: tags},
		func(ctx context.Context, in *struct{ Body DecisionsKeyInput }) (*struct{ Body ProviderView }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			if err := limited(p); err != nil {
				return nil, err
			}
			body := in.Body
			if err := checkDecisionsKey(&body); err != nil {
				return nil, err
			}
			if t := s.testDecisionsKey(ctx, body.APIKey); !t.OK {
				return nil, errTestFailed(t)
			}
			sealed, err := s.d.Box.Seal([]byte(body.APIKey))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var replaced bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_decision_keys WHERE workspace_id = $1)`, p.WorkspaceID).Scan(&replaced); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO ai_decision_keys (workspace_id, key_enc, key_last4, created_by) VALUES ($1, $2, $3, $4)
					ON CONFLICT (workspace_id) DO UPDATE SET key_enc = EXCLUDED.key_enc, key_last4 = EXCLUDED.key_last4, created_by = EXCLUDED.created_by,
						status = 'ok', status_at = NULL, notified_at = NULL, created_at = now(), updated_at = now()`,
					p.WorkspaceID, sealed, last4(body.APIKey), p.ActorUserID()); err != nil {
					return err
				}
				action := "ai.decisions_key_set"
				if replaced {
					action = "ai.decisions_key_replaced"
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, action, "workspace", &p.WorkspaceID, map[string]any{"kind": KeyTypeSafe})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.providerView(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body ProviderView }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-remove-decisions-key", Method: http.MethodDelete, Path: "/v1/ai/provider/decisions", DefaultStatus: http.StatusNoContent,
		Summary: "Remove the workspace's own TypeSafe key", Description: "Deletes the key; decisions run on the workspace's own OpenRouter key when it has one, else on the instance's engine.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM ai_decision_keys WHERE workspace_id = $1`, p.WorkspaceID)
				if err != nil || tag.RowsAffected() == 0 {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "ai.decisions_key_removed", "workspace", &p.WorkspaceID, map[string]any{"kind": KeyTypeSafe})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.decKeys.mu.Lock()
			delete(s.decKeys.m, p.WorkspaceID)
			s.decKeys.mu.Unlock()
			return nil, nil
		})
}
