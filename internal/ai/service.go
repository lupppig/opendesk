package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/ai/decide"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/plan"
)

// Service runs the AI features for every area.
type Service struct {
	d        *deps.Deps
	provider Provider // nil when AI is off
	embedder Embedder // nil without embeddings
	models   struct{ Default, Fast string }
	// vectors reports whether ai_embeddings exists (pgvector available).
	vectors bool
	// decider answers typed decisions (Jev, or the LLM adapter); nil without one.
	decider decide.Decider
	// knowledge retrieves from knowledge sources (internal/ai/knowledge); nil until installed.
	knowledgeMu sync.RWMutex
	knowledge   KnowledgeRetriever
	// toolbox lists and runs workspace tools (internal/ai/mcp); nil until installed.
	toolbox AgentToolbox
	// catalog caches the provider's model names and prices.
	catalog catalog
	// own caches the providers built from workspaces' own keys (byok.go).
	own ownKeys
	// decKeys caches the deciders built from workspaces' own TypeSafe keys (decisions_key.go).
	decKeys decisionKeys
	// anthropicBase is the Anthropic API root (ANTHROPIC_BASE_URL, "" = the public API).
	anthropicBase string
	// guardDev relaxes the SSRF guard for user-chosen endpoints on a development instance.
	guardDev bool
}

// New builds the service from configuration. It never fails: a missing provider turns
// AI off, a missing vector table turns semantic search into FTS.
func New(ctx context.Context, d *deps.Deps) *Service {
	cfg := d.Cfg
	s := &Service{d: d, anthropicBase: os.Getenv("ANTHROPIC_BASE_URL"), guardDev: !cfg.IsProduction()}
	s.models.Default, s.models.Fast = cfg.AIModel, cfg.AIFastModel
	if s.models.Fast == "" {
		s.models.Fast = s.models.Default
	}
	switch cfg.AIProvider {
	case "openrouter":
		if cfg.OpenRouterAPIKey != "" {
			s.provider = NewOpenRouter(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey, cfg.AppURL)
		}
	case "anthropic":
		if cfg.AnthropicAPIKey != "" {
			s.provider = NewAnthropic(cfg.AnthropicAPIKey, s.anthropicBase)
		}
	case "openai":
		if s.models.Default != "" {
			s.provider = NewOpenAI(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey)
		} else {
			d.Log.Warn("ai: AI_MODEL is required for an OpenAI-compatible provider; AI is off")
		}
	}
	if s.provider != nil {
		switch cfg.EmbeddingsDriver {
		case "voyage":
			if cfg.VoyageAPIKey != "" {
				s.embedder = NewVoyage(cfg.VoyageAPIKey, cfg.EmbeddingsModel)
			}
		case "openai":
			s.embedder = NewOpenAIEmbedder(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, cfg.EmbeddingsModel)
		case "openrouter":
			if cfg.OpenRouterAPIKey != "" {
				model := cfg.EmbeddingsModel
				if model == "" {
					model = "openai/text-embedding-3-small"
				}
				s.embedder = NewOpenAIEmbedder(cfg.OpenRouterBaseURL+"/v1", cfg.OpenRouterAPIKey, model)
			}
		}
	}
	s.vectors = vectorTable(ctx, d.DB.Pool)
	switch cfg.DecisionsProvider {
	case "typesafe":
		if cfg.TypesafeAPIKey != "" {
			s.decider = decide.NewTypeSafe(cfg.TypesafeAPIURL, cfg.TypesafeAPIKey, cfg.TypesafeModel)
		}
	case "openrouter":
		if cfg.OpenRouterAPIKey != "" {
			s.decider = decide.NewOpenRouter(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey, cfg.TypesafeModel, cfg.AppURL)
		}
	case "llm":
		if s.provider != nil {
			s.decider = &llmDecider{s: s}
		}
	}
	if s.decider != nil {
		d.Log.Info("decisions ready", "engine", s.decider.Name(), "model", s.decider.Model())
	}
	if s.provider != nil {
		d.Log.Info("ai ready", "provider", s.provider.Name(), "model", s.models.Default, "fast_model", s.models.Fast,
			"embeddings", s.embedder != nil, "vector_store", s.vectors)
	}
	return s
}

// WithDecider replaces the decision engine (tests, alternative backends); nil turns it off.
func (s *Service) WithDecider(dc decide.Decider) *Service {
	s.decider = dc
	return s
}

// WithProvider replaces the provider and embedder (tests, alternative backends).
func (s *Service) WithProvider(p Provider, e Embedder, defaultModel, fastModel string) *Service {
	s.provider, s.embedder = p, e
	s.models.Default, s.models.Fast = defaultModel, fastModel
	return s
}

func vectorTable(ctx context.Context, q db.Querier) bool {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT to_regclass('public.ai_embeddings') IS NOT NULL`).Scan(&ok); err != nil {
		return false
	}
	return ok
}

// Enabled reports whether an AI provider is configured.
func (s *Service) Enabled() bool { return s != nil && s.provider != nil }

// canTriage reports whether triage can run: an AI provider or a calibrated decision
// engine (Jev) is configured.
func (s *Service) canTriage() bool { return s != nil && (s.provider != nil || s.jev()) }

// jev reports whether decisions run on a calibrated engine.
func (s *Service) jev() bool { return s.decider != nil && s.decider.Calibrated() }

// semantic reports whether embeddings can be used for retrieval.
func (s *Service) semantic() bool { return s.embedder != nil && s.vectors }

var errDisabled = httpx.E(http.StatusNotFound, "ai_disabled", "AI is not enabled on this instance.")
var errOff = httpx.E(http.StatusForbidden, "ai_off", "AI features are turned off for this workspace.")

// workspaceAI loads the workspace's AI settings and refuses when AI is off.
func (s *Service) workspaceAI(ctx context.Context, wid uuid.UUID) (hd.AISettings, error) {
	if !s.Enabled() {
		return hd.AISettings{}, errDisabled
	}
	return s.workspaceOn(ctx, wid)
}

// workspaceOn loads the workspace's AI settings and refuses when the workspace turned
// AI off, whatever engine is configured.
func (s *Service) workspaceOn(ctx context.Context, wid uuid.UUID) (hd.AISettings, error) {
	st, err := hd.LoadSettings(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return hd.AISettings{}, err
	}
	// A suspended workspace runs no AI (contracts/admin.md): to every feature it is off.
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid); err != nil || suspended {
		if err != nil {
			return hd.AISettings{}, err
		}
		return hd.AISettings{}, errOff
	}
	a := st.AI()
	if !a.Enabled {
		return a, errOff
	}
	return a, nil
}

// call is one model call billed to a feature. The AI balance is checked against the
// call's estimate before the call, and the real cost is charged by charge after the
// feature succeeded, so a failed call costs nothing. A call on the workspace's own key
// is recorded and charges nothing.
type call struct {
	s        *Service
	wid      uuid.UUID
	userID   *uuid.UUID
	ticketID *uuid.UUID
	feature  string
	resp     Response
	// model, when set, is used instead of the workspace's model for the feature's tier.
	model string
	// charged is what the last charge cost.
	charged plan.Micro
	// rt is where the call goes (the instance's provider or the workspace's key).
	rt *route
	// onKey marks a call that ran on the workspace's own key: nothing is charged.
	onKey bool
	// calibrated marks a decision answered by a calibrated engine (Jev): its
	// probabilities can be gated on.
	calibrated bool
}

func (s *Service) newCall(wid uuid.UUID, userID, ticketID *uuid.UUID, feature string) *call {
	return &call{s: s, wid: wid, userID: userID, ticketID: ticketID, feature: feature}
}

// route loads where the workspace's LLM calls go, once per call.
func (c *call) route(ctx context.Context) (*route, error) {
	if c.rt != nil {
		return c.rt, nil
	}
	if suspended, err := domain.WorkspaceSuspended(ctx, c.s.d.DB.Pool, c.wid); err != nil || suspended {
		if err != nil {
			return nil, err
		}
		return nil, errOff
	}
	rt, err := c.s.routeFor(ctx, c.wid)
	if err != nil {
		return nil, err
	}
	c.rt = rt
	return rt, nil
}

// reserve checks the AI balance before an LLM call on model with about in prompt tokens
// and up to maxOut output tokens. A workspace on its own key needs none.
func (c *call) reserve(ctx context.Context, model string, in, maxOut int) error {
	rt, err := c.route(ctx)
	if err != nil {
		return err
	}
	if rt.own {
		return nil
	}
	return c.reserveBalance(ctx, c.s.estimate(ctx, c.feature, model, in, maxOut))
}

// reserveBalance checks the balance covers an estimate (instance calls, Jev decisions).
func (c *call) reserveBalance(ctx context.Context, estimate plan.Micro) error {
	if c.s.d.Plan == nil {
		return nil // no plan service: unlimited
	}
	return c.s.d.Plan.Reserve(ctx, c.s.d.DB.Pool, c.wid, estimate)
}

// resolveModel picks the model of a call: an explicit override, else the workspace's
// choice for the feature's tier when the request asks for an instance default. On the
// workspace's own key, only that provider's models are used.
func (c *call) resolveModel(ctx context.Context, requested string) string {
	rt, err := c.route(ctx)
	if err != nil {
		rt = c.s.instanceRoute()
	}
	if c.model != "" && (!rt.own || c.s.ownModelOK(ctx, rt, c.model)) {
		return c.model
	}
	if !rt.own && requested != "" && requested != c.s.models.Default && requested != c.s.models.Fast {
		return requested
	}
	st, err := hd.LoadSettings(ctx, c.s.d.DB.Pool, c.wid)
	if err != nil {
		if requested != "" && !rt.own {
			return requested
		}
		if featureTier(c.feature) == tierFast {
			return rt.models.Fast
		}
		return rt.models.Default
	}
	return c.s.tierModelOn(ctx, rt, st.AI(), featureTier(c.feature))
}

// run reserves and calls the model; onDelta non-nil streams.
func (c *call) run(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	req.Model = c.resolveModel(ctx, req.Model)
	if err := c.reserve(ctx, req.Model, requestTokens(req), req.MaxTokens); err != nil {
		return Response{}, err
	}
	rt := c.rt
	var err error
	start := time.Now()
	if onDelta != nil {
		c.resp, err = rt.p.Stream(ctx, req, onDelta)
	} else {
		c.resp, err = rt.p.Complete(ctx, req)
	}
	c.onKey = rt.own
	err = c.s.keyResult(ctx, c.wid, rt, err)
	if c.resp.Model == "" {
		c.resp.Model = req.Model
	}
	// Counts and timings only: prompts and completions are never logged.
	c.s.d.Log.Debug("ai call", "feature", c.feature, "model", c.resp.Model, "input_tokens", c.resp.InputTokens,
		"output_tokens", c.resp.OutputTokens, "ms", time.Since(start).Milliseconds(), "ok", err == nil)
	return c.resp, err
}

// decide asks the workspace's decision engine (decisionsFor). Usage is recorded by charge
// with the engine's name as the model ("jev", counts only). On the instance's engine the
// feature's typical cost is reserved first; on the workspace's own TypeSafe or OpenRouter
// key, or the LLM adapter on its own provider key, nothing is charged and a refused key
// is recorded like any call on it.
func (c *call) decide(ctx context.Context, req decide.Request) (decide.Result, error) {
	rt, err := c.route(ctx)
	if err != nil {
		return decide.Result{}, err
	}
	dr, err := c.s.decisionsFor(ctx, c.wid, rt)
	if err != nil {
		return decide.Result{}, err
	}
	if dr.dc == nil {
		return decide.Result{}, ErrUnavailable
	}
	dc := dr.dc
	c.onKey, c.calibrated = dr.own, dc.Calibrated()
	if !c.onKey {
		if err := c.reserveBalance(ctx, plan.Typical[c.feature]); err != nil {
			return decide.Result{}, err
		}
	}
	start := time.Now()
	res, err := dc.Decide(ctx, req)
	err = c.s.decisionKeyResult(ctx, c.wid, dr, err)
	c.resp = Response{Model: dc.Name(), InputTokens: res.InputTokens, OutputTokens: res.OutputTokens, CostUSD: res.CostUSD}
	if dc.Name() != "jev" {
		c.resp.Model = res.Model
	}
	c.s.d.Log.Debug("ai decision", "feature", c.feature, "engine", dc.Name(), "questions", len(req.Questions),
		"input_tokens", res.InputTokens, "ms", time.Since(start).Milliseconds(), "ok", err == nil)
	return res, decisionErr(err)
}

// decisionErr maps decision engine errors to the AI errors features handle.
func decisionErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.As(err, new(*ownKeyError)):
		return err // names the workspace's key; it unwraps to ErrUnavailable
	case errors.Is(err, decide.ErrBusy), errors.Is(err, ErrBusy):
		return ErrBusy
	case errors.Is(err, decide.ErrBadOutput), errors.Is(err, ErrBadOutput):
		return ErrBadOutput
	case errors.Is(err, ErrRefused):
		return ErrRefused
	case isNoBalance(err), errors.Is(err, context.Canceled):
		return err
	}
	return ErrUnavailable
}

// charge records usage and charges the call's cost after it succeeded.
func (c *call) charge(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	var err error
	if c.s.d.Plan != nil {
		c.charged, err = c.s.d.Plan.Spend(ctx, c.s.d.DB.Pool, c.wid, plan.Usage{UserID: c.userID, TicketID: c.ticketID, Feature: c.feature,
			Model: c.resp.Model, InputTokens: c.resp.InputTokens, OutputTokens: c.resp.OutputTokens, CostUSD: c.s.costOf(ctx, c.resp), OwnKey: c.onKey})
	}
	if err != nil {
		c.s.d.Log.Warn("ai spend failed", "feature", c.feature, "err", err)
	}
	distinct := "workspace:" + c.wid.String()
	if c.userID != nil {
		distinct = c.userID.String()
	}
	c.s.d.Analytics.Capture(distinct, c.wid.String(), "ai_feature_used", map[string]any{"feature": c.feature, "own_key": c.onKey})
}

// apiErr maps AI errors to problem responses.
func apiErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.As(err, new(*ownKeyError)):
		var ke *ownKeyError
		errors.As(err, &ke)
		if ke.status == keyNoFunds {
			return httpx.E(http.StatusBadGateway, "ai_key_no_funds", "The AI provider account behind this workspace's key is out of funds. An admin can fix it in AI settings.")
		}
		return httpx.E(http.StatusBadGateway, "ai_key_rejected", "The AI provider rejected this workspace's key. An admin can fix it in AI settings.")
	case errors.Is(err, ErrBusy):
		return httpx.E(http.StatusServiceUnavailable, "ai_busy", "The AI provider is busy. Try again in a moment.")
	case errors.Is(err, ErrRefused):
		return httpx.E(http.StatusUnprocessableEntity, "ai_refused", "The AI model declined this request.")
	case errors.Is(err, ErrBadOutput):
		return httpx.E(http.StatusBadGateway, "ai_bad_output", "The AI model returned an unusable answer. Try again.")
	case errors.Is(err, ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return httpx.E(http.StatusBadGateway, "ai_unavailable", "The AI provider is unavailable. Try again.")
	}
	return httpx.Wrap(err)
}

// resolveTicket accepts a uuid, "#123" or "123".
func resolveTicket(ctx context.Context, q db.Querier, wid uuid.UUID, raw string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if id, err := uuid.Parse(raw); err == nil {
		return id, nil
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(raw, "#"), 10, 64)
	if err != nil || n <= 0 {
		return uuid.Nil, httpx.NotFound("Ticket")
	}
	var id uuid.UUID
	err = q.QueryRow(ctx, `SELECT id FROM tickets WHERE workspace_id = $1 AND number = $2 AND deleted_at IS NULL`, wid, n).Scan(&id)
	if db.IsNotFound(err) {
		return uuid.Nil, httpx.NotFound("Ticket")
	}
	return id, err
}

// resolveVisibleTicket is resolveTicket for a principal: a ticket p may not see (a private
// team's, contracts/teams.md "Visibility") is not found.
func resolveVisibleTicket(ctx context.Context, q db.Querier, p *auth.Principal, raw string) (uuid.UUID, error) {
	id, err := resolveTicket(ctx, q, p.WorkspaceID, raw)
	if err != nil {
		return id, err
	}
	var team, assignee *uuid.UUID
	err = q.QueryRow(ctx, `SELECT team_id, assignee_id FROM tickets WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		p.WorkspaceID, id).Scan(&team, &assignee)
	if db.IsNotFound(err) {
		return uuid.Nil, httpx.NotFound("Ticket")
	}
	if err != nil {
		return uuid.Nil, err
	}
	ok, err := domain.CanSee(ctx, q, p, team, assignee)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, httpx.NotFound("Ticket")
	}
	return id, nil
}

// ticketCtx is what features read about a ticket.
type ticketCtx struct {
	T         domain.Ticket
	Msgs      []domain.Message
	Contact   *domain.Contact
	Workspace struct{ Name, Slug string }
	Signature bool // the mailbox appends a signature on send
	Summary   string
}

func (s *Service) loadTicketCtx(ctx context.Context, q db.Querier, wid, tid uuid.UUID, publicOnly bool) (*ticketCtx, error) {
	t, err := domain.LoadTicket(ctx, q, wid, tid)
	if db.IsNotFound(err) {
		return nil, httpx.NotFound("Ticket")
	}
	if err != nil {
		return nil, err
	}
	tc := &ticketCtx{T: t}
	if tc.Msgs, err = domain.ListMessages(ctx, q, wid, tid, publicOnly); err != nil {
		return nil, err
	}
	if t.Contact != nil {
		c, err := domain.LoadContact(ctx, q, wid, t.Contact.ID)
		if err != nil && !db.IsNotFound(err) {
			return nil, err
		}
		if err == nil {
			tc.Contact = &c
		}
	}
	if err := q.QueryRow(ctx, `SELECT name, slug FROM workspaces WHERE id = $1`, wid).Scan(&tc.Workspace.Name, &tc.Workspace.Slug); err != nil {
		return nil, err
	}
	if t.MailboxID != nil {
		_ = q.QueryRow(ctx, `SELECT signature_html <> '' FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *t.MailboxID).Scan(&tc.Signature)
	}
	tc.Summary = storedSummary(t.Custom).Text
	return tc, nil
}

func (tc *ticketCtx) language() string {
	l, _ := tc.T.Custom["language"].(string)
	return l
}

// StoredSummary is custom.ai_summary.
type StoredSummary struct {
	Text         string    `json:"text"`
	KeyPoints    []string  `json:"key_points"`
	Sentiment    string    `json:"sentiment"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
}

func storedSummary(custom map[string]any) StoredSummary {
	var s StoredSummary
	if v, ok := custom["ai_summary"]; ok {
		raw, _ := json.Marshal(v)
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// articleURL is the public help center link of an article on a brand's help center:
// on its active custom domain (hosts, by brand slug) when it has one.
func (s *Service) articleURL(hosts map[string]string, brandSlug, articleSlug string) string {
	base := s.d.Cfg.HelpCenterURL(url.PathEscape(brandSlug))
	if h := hosts[brandSlug]; h != "" {
		base = "https://" + h
	}
	return base + "/a/" + url.PathEscape(articleSlug)
}

// helpHosts maps the workspace's brand slugs to their active help center domains.
func (s *Service) helpHosts(ctx context.Context, wid uuid.UUID) (map[string]string, error) {
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT b.slug, hd.hostname FROM brands b
		JOIN help_domains hd ON hd.brand_id = b.id AND hd.status = 'active' WHERE b.workspace_id = $1`, wid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var slug, host string
		if err := rows.Scan(&slug, &host); err != nil {
			return nil, err
		}
		out[slug] = host
	}
	return out, rows.Err()
}
