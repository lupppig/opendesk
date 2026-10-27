package ai

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// ProviderView is GET /v1/ai/provider: which provider the workspace's AI runs on.
type ProviderView struct {
	Mode             string       `json:"mode" enum:"instance,own_key" doc:"instance: the instance's provider (charged to the AI balance when billing is on); own_key: the workspace's key"`
	InstanceProvider string       `json:"instance_provider" doc:"The instance's provider (openrouter, anthropic, openai)"`
	Billing          bool         `json:"billing" doc:"Calls on the instance's provider are charged to the AI balance, in US dollars"`
	Key              *ProviderKey `json:"key"`
	// Decisions is where the workspace's decisions (Jev) run, and its TypeSafe key.
	Decisions DecisionsView `json:"decisions"`
}

// ProviderKey describes a saved key without revealing it.
type ProviderKey struct {
	Kind        string          `json:"kind" enum:"openrouter,openai,anthropic"`
	BaseURLHost string          `json:"base_url_host" doc:"The host of an OpenAI-compatible endpoint, else empty"`
	Last4       string          `json:"last4" doc:"The key's last 4 characters"`
	Model       string          `json:"model" doc:"Default model on this provider"`
	FastModel   string          `json:"fast_model"`
	Status      string          `json:"status" enum:"ok,rejected,no_funds" doc:"What the provider last said about the key"`
	StatusAt    *time.Time      `json:"status_at"`
	CreatedBy   *domain.UserRef `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

var errTestFailed = func(t *ProviderTest) error {
	return httpx.E(http.StatusUnprocessableEntity, "provider_test_failed", t.Message)
}

// providerView reads the workspace's provider and decisions; q must see what is
// committed (decisions are routed through the pool).
func (s *Service) providerView(ctx context.Context, q db.Querier, wid uuid.UUID) (ProviderView, error) {
	out := ProviderView{Mode: "instance", InstanceProvider: "none", Billing: s.d.Plan != nil && s.d.Plan.Billing}
	if s.provider != nil {
		out.InstanceProvider = s.provider.Name()
	}
	dv, err := s.decisionsView(ctx, q, wid)
	if err != nil {
		return out, err
	}
	out.Decisions = dv
	var k ProviderKey
	var baseURL string
	var uid *uuid.UUID
	var name, email, avatar *string
	err = q.QueryRow(ctx, `SELECT k.kind, k.base_url, k.key_last4, k.model, k.fast_model, k.status, k.status_at, k.created_at, k.updated_at,
			u.id, u.name, u.email, u.avatar_url
		FROM ai_provider_keys k LEFT JOIN users u ON u.id = k.created_by WHERE k.workspace_id = $1`, wid).
		Scan(&k.Kind, &baseURL, &k.Last4, &k.Model, &k.FastModel, &k.Status, &k.StatusAt, &k.CreatedAt, &k.UpdatedAt, &uid, &name, &email, &avatar)
	if db.IsNotFound(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if u, err := url.Parse(baseURL); err == nil && baseURL != "" {
		k.BaseURLHost = u.Host
	}
	if def, ok := ownDefaults[k.Kind]; ok {
		k.Model, k.FastModel = firstNonEmpty(k.Model, def[0]), firstNonEmpty(k.FastModel, def[1])
	}
	k.FastModel = firstNonEmpty(k.FastModel, k.Model)
	if uid != nil {
		k.CreatedBy = &domain.UserRef{ID: *uid, Name: *name, Email: *email, AvatarURL: domain.Avatar(*avatar)}
	}
	out.Mode, out.Key = "own_key", &k
	return out, nil
}

// last4 is what is shown of a key.
func last4(key string) string {
	if len(key) <= 4 {
		return key
	}
	return key[len(key)-4:]
}

func (s *Service) registerProvider(api huma.API) {
	tags := []string{"AI"}
	s.registerDecisionsKey(api)

	huma.Register(api, huma.Operation{OperationID: "ai-get-provider", Method: http.MethodGet, Path: "/v1/ai/provider",
		Summary: "AI provider", Description: "Whether the workspace's AI runs on the instance's provider or its own key. The key itself is never returned.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body ProviderView }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			out, err := s.providerView(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body ProviderView }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-test-provider", Method: http.MethodPost, Path: "/v1/ai/provider/test",
		Summary: "Test an AI provider key", Description: "Checks a key with a tiny call before it is saved. Nothing is stored.", Tags: tags},
		func(ctx context.Context, in *struct{ Body ProviderKeyInput }) (*struct{ Body ProviderTest }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			if !s.d.Limiter.Allow("ai:provider_test:"+p.WorkspaceID.String(), 20, time.Minute) {
				return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many tests. Try again in a minute.")
			}
			body := in.Body
			if err := s.checkKeyInput(ctx, &body); err != nil {
				return nil, err
			}
			return &struct{ Body ProviderTest }{*s.testKey(ctx, body)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-set-provider", Method: http.MethodPut, Path: "/v1/ai/provider",
		Summary: "Use the workspace's own AI provider key", Description: "Tests the key, then stores it encrypted. Every LLM call of the workspace then runs on it " +
			"and charges nothing to the AI balance. Replaces a saved key.", Tags: tags},
		func(ctx context.Context, in *struct{ Body ProviderKeyInput }) (*struct{ Body ProviderView }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.Enabled() {
				return nil, errDisabled
			}
			if !s.d.Limiter.Allow("ai:provider_test:"+p.WorkspaceID.String(), 20, time.Minute) {
				return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many tests. Try again in a minute.")
			}
			body := in.Body
			if err := s.checkKeyInput(ctx, &body); err != nil {
				return nil, err
			}
			if t := s.testKey(ctx, body); !t.OK {
				return nil, errTestFailed(t)
			}
			sealed, err := s.d.Box.Seal([]byte(body.APIKey))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var replaced bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_provider_keys WHERE workspace_id = $1)`, p.WorkspaceID).Scan(&replaced); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO ai_provider_keys (workspace_id, kind, base_url, key_enc, key_last4, model, fast_model, created_by)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
					ON CONFLICT (workspace_id) DO UPDATE SET kind = EXCLUDED.kind, base_url = EXCLUDED.base_url, key_enc = EXCLUDED.key_enc,
						key_last4 = EXCLUDED.key_last4, model = EXCLUDED.model, fast_model = EXCLUDED.fast_model, created_by = EXCLUDED.created_by,
						status = 'ok', status_at = NULL, notified_at = NULL, created_at = now(), updated_at = now()`,
					p.WorkspaceID, body.Kind, body.BaseURL, sealed, last4(body.APIKey), body.Model, body.FastModel, p.ActorUserID()); err != nil {
					return err
				}
				action := "ai.provider_key_set"
				if replaced {
					action = "ai.provider_key_replaced"
				}
				data := map[string]any{"kind": body.Kind}
				if body.BaseURL != "" {
					if u, err := url.Parse(body.BaseURL); err == nil {
						data["base_url_host"] = u.Host
					}
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, action, "workspace", &p.WorkspaceID, data)
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

	huma.Register(api, huma.Operation{OperationID: "ai-remove-provider", Method: http.MethodDelete, Path: "/v1/ai/provider", DefaultStatus: http.StatusNoContent,
		Summary: "Remove the workspace's own AI provider key", Description: "Deletes the key; AI returns to the instance's provider (the AI balance).", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var kind string
				err := tx.QueryRow(ctx, `DELETE FROM ai_provider_keys WHERE workspace_id = $1 RETURNING kind`, p.WorkspaceID).Scan(&kind)
				if db.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "ai.provider_key_removed", "workspace", &p.WorkspaceID, map[string]any{"kind": kind})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.own.mu.Lock()
			delete(s.own.m, p.WorkspaceID)
			s.own.mu.Unlock()
			return nil, nil
		})
}
