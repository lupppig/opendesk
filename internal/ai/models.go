package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/plan"
)

// Model tiers: which workspace setting picks the model of a feature
// (contracts/ai.md "Workspace settings").
const (
	tierDefault = "default"
	tierFast    = "fast"
	tierWidget  = "widget"
)

// featureTier maps an AI feature to its model tier.
func featureTier(feature string) string {
	switch feature {
	case "rewrite", "translate", "triage", "decide", "assistant_peer":
		return tierFast
	case "answer", "answer_email", "playground":
		return tierWidget
	}
	return tierDefault
}

// curatedModels is the default list a workspace chooses from on OpenRouter when
// AI_MODELS is unset (contracts/ai.md "Models").
var curatedModels = []string{
	"anthropic/claude-sonnet-5.5",
	"anthropic/claude-haiku-5.5",
	"anthropic/claude-opus-5.5",
	"openai/gpt-5.6-sol",
	"openai/gpt-5.6-luna",
	"google/gemini-3.8-flash",
	"deepseek/deepseek-v4.1-flash",
	"z-ai/glm-5.3-flash",
}

// ModelInfo describes one model a workspace may choose.
type ModelInfo struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Vendor        string     `json:"vendor"`
	ContextLength *int       `json:"context_length"`
	USDPerMTok    *MTokPrice `json:"usd_per_mtok" doc:"US dollars per million tokens as the workspace pays them (markup included); null when unknown"`
	// TypicalReplyUSD is what a typical draft reply costs on this model; null when the
	// price is unknown.
	TypicalReplyUSD *float64 `json:"typical_reply_usd"`
	// USD per token, for the cost of calls whose provider reports none.
	inUSD, outUSD float64
	priced        bool
}

// MTokPrice is US dollars per million tokens, markup included.
type MTokPrice struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// catalog caches the provider's model metadata (OpenRouter's /v1/models).
type catalog struct {
	mu      sync.Mutex
	fetched time.Time
	info    map[string]ModelInfo
}

const catalogTTL = time.Hour

// modelIDs is the instance's list of models a workspace may choose, defaults first.
func (s *Service) modelIDs() []string {
	ids := []string{}
	add := func(id string) {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	add(s.models.Default)
	add(s.models.Fast)
	switch {
	case len(s.d.Cfg.AIModels) > 0:
		for _, id := range s.d.Cfg.AIModels {
			add(id)
		}
	case s.provider != nil && s.provider.Name() == "openrouter":
		for _, id := range curatedModels {
			add(id)
		}
	}
	return ids
}

// Catalog returns the models a workspace may choose, with names and prices when the
// provider publishes them. On OpenRouter, an id it does not list is dropped, except the
// instance defaults.
func (s *Service) Catalog(ctx context.Context) []ModelInfo {
	ids := s.modelIDs()
	info := s.catalogInfo(ctx)
	out := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		m, ok := info[id]
		if !ok {
			if info != nil && id != s.models.Default && id != s.models.Fast {
				continue
			}
			m = ModelInfo{ID: id, Name: id}
		}
		if m.priced {
			perM := func(usd float64) float64 { return s.pay(usd * 1e6).USD() }
			m.USDPerMTok = &MTokPrice{Input: perM(m.inUSD), Output: perM(m.outUSD)}
			t := typicalTokens["draft_reply"]
			reply := s.pay(float64(t.in)*m.inUSD + float64(t.out)*m.outUSD).USD()
			m.TypicalReplyUSD = &reply
		}
		out = append(out, m)
	}
	return out
}

// allowedModel reports whether id is one of the instance's models.
func (s *Service) allowedModel(id string) bool { return slices.Contains(s.modelIDs(), id) }

// priceOf returns a model's USD price per input and output token when known.
func (s *Service) priceOf(ctx context.Context, model string) (in, out float64, ok bool) {
	m, found := s.catalogInfo(ctx)[model]
	if !found || !m.priced {
		return 0, 0, false
	}
	return m.inUSD, m.outUSD, true
}

// catalogInfo returns the cached model metadata, refreshing it hourly. It is nil when
// the provider publishes none (or could not be reached and nothing is cached).
func (s *Service) catalogInfo(ctx context.Context) map[string]ModelInfo {
	if s.provider == nil || s.provider.Name() != "openrouter" {
		return nil
	}
	c := &s.catalog
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.info != nil && time.Since(c.fetched) < catalogTTL {
		return c.info
	}
	info, err := fetchOpenRouterModels(ctx, s.d.Cfg.OpenRouterBaseURL, s.d.Cfg.OpenRouterAPIKey)
	if err != nil {
		s.d.Log.Warn("ai: model list unavailable", "err", err)
		if c.info != nil {
			c.fetched = time.Now().Add(-catalogTTL + time.Minute) // retry in a minute
		}
		return c.info
	}
	c.info, c.fetched = info, time.Now()
	return c.info
}

// fetchOpenRouterModels reads GET {base}/v1/models.
func fetchOpenRouterModels(ctx context.Context, base, key string) (map[string]ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := newHTTPClient().Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, statusError(res.StatusCode)
	}
	var body struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&body); err != nil {
		return nil, ErrBadOutput
	}
	out := make(map[string]ModelInfo, len(body.Data))
	for _, m := range body.Data {
		vendor, name := "", m.Name
		if v, n, ok := strings.Cut(m.Name, ": "); ok {
			vendor, name = v, n
		}
		mi := ModelInfo{ID: m.ID, Name: name, Vendor: vendor}
		if m.ContextLength > 0 {
			cl := m.ContextLength
			mi.ContextLength = &cl
		}
		in, errIn := strconv.ParseFloat(m.Pricing.Prompt, 64)
		outP, errOut := strconv.ParseFloat(m.Pricing.Completion, 64)
		if errIn == nil && errOut == nil && in >= 0 && outP >= 0 {
			mi.inUSD, mi.outUSD, mi.priced = in, outP, true
		}
		out[m.ID] = mi
	}
	return out, nil
}

// tierModel picks a workspace's model for a tier: its choice when the instance still
// offers it, else the instance default.
func (s *Service) tierModel(set hd.AISettings, tier string) string {
	pick := func(choice, fallback string) string {
		if choice != "" && s.allowedModel(choice) {
			return choice
		}
		return fallback
	}
	def := pick(set.Model, s.models.Default)
	switch tier {
	case tierFast:
		return pick(set.FastModel, s.models.Fast)
	case tierWidget:
		return pick(set.WidgetModel, def)
	}
	return def
}

// costOf is a call's cost in USD: what the provider reported, else its tokens at the
// model's published price, else 0 (the feature's typical cost is charged).
func (s *Service) costOf(ctx context.Context, r Response) float64 {
	if r.CostUSD > 0 {
		return r.CostUSD
	}
	if r.InputTokens == 0 && r.OutputTokens == 0 {
		return 0
	}
	in, out, ok := s.priceOf(ctx, r.Model)
	if !ok {
		return 0
	}
	return float64(r.InputTokens)*in + float64(r.OutputTokens)*out
}

// markup is what the workspace pays per dollar of provider cost: the instance markup
// when billing is on, else 1 (a self-hosted instance pays its provider directly).
func (s *Service) markup() float64 {
	if s.d.Plan != nil && s.d.Plan.Billing {
		return max(s.d.Plan.Markup, 1)
	}
	return 1
}

// pay is what a provider cost in USD costs the workspace.
func (s *Service) pay(usd float64) plan.Micro { return plan.FromCost(usd, s.markup()) }

// tokenCount is a call's size in tokens, for typical costs and estimates.
type tokenCount struct{ in, out int }

// typicalTokens is the size of a typical call of each LLM feature with a typical
// thread: what GET /v1/ai/config's costs and a model's typical reply are priced on.
var typicalTokens = map[string]tokenCount{
	"summarize":    {3000, 300},
	"rewrite":      {800, 400},
	"translate":    {800, 500},
	"triage":       {1500, 150},
	"decide":       {1500, 100},
	"copilot":      {4000, 400},
	"draft_reply":  {4000, 500},
	"answer":       {5000, 400},
	"answer_email": {5000, 500},
	"playground":   {5000, 400},
	"kb_draft":     {8000, 1500},
	"assistant":    {6000, 400},
	// One answer of a teammate's Ollie: mail excerpts in, a short answer out.
	"assistant_peer": {4000, 400},
}

// estimateMaxOutput caps the output tokens an estimate counts: features allow long
// outputs for the rare long answer, and a call that ends up costing more than the
// balance takes it to zero rather than being refused.
const estimateMaxOutput = 4000

// estimate is the balance a call needs to start (contracts/billing.md "Starting a
// call"): its prompt's tokens plus its max output tokens (at most estimateMaxOutput) at
// the model's price, as the workspace pays it. When the model's price is unknown it is
// the feature's typical cost. plan.Reserve applies the plan.MinBalance floor.
func (s *Service) estimate(ctx context.Context, feature, model string, in, maxOut int) plan.Micro {
	pin, pout, ok := s.priceOf(ctx, model)
	if !ok {
		return plan.Typical[feature]
	}
	return s.pay(float64(in)*pin + float64(min(max(maxOut, 0), estimateMaxOutput))*pout)
}

// typicalCost is what a typical call of feature costs on model, as the workspace pays
// it: the model's price on typicalTokens when known, else the feature's typical cost.
func (s *Service) typicalCost(ctx context.Context, feature, model string) plan.Micro {
	t, sized := typicalTokens[feature]
	pin, pout, ok := s.priceOf(ctx, model)
	if !sized || !ok {
		return plan.Typical[feature]
	}
	return s.pay(float64(t.in)*pin + float64(t.out)*pout)
}

// promptTokens estimates the input tokens of prompt text: about four characters each.
func promptTokens(texts ...string) int {
	n := 0
	for _, t := range texts {
		n += len(t)
	}
	return (n + 3) / 4
}

// requestTokens estimates the input tokens of a request.
func requestTokens(req Request) int {
	n := 0
	for _, b := range req.System {
		n += promptTokens(b.Text)
	}
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			n += promptTokens(b.Text)
		}
	}
	return n
}

// chatTokens estimates the input tokens of a tool-using request, tool schemas included.
func chatTokens(req ChatRequest) int {
	n := 0
	for _, b := range req.System {
		n += promptTokens(b.Text)
	}
	for _, t := range req.Turns {
		n += promptTokens(t.Text)
		for _, c := range t.Calls {
			n += promptTokens(c.Name, string(c.Input))
		}
		for _, r := range t.Results {
			n += promptTokens(r.Content)
		}
	}
	for _, t := range req.Tools {
		schema, _ := json.Marshal(t.Schema)
		n += promptTokens(t.Name, t.Description, string(schema))
	}
	return n
}

// featureCosts sets costs[feature] to what a typical call costs on the workspace's
// model for its tier (GET /v1/ai/config). Features answered by Jev keep their typical
// cost, which does not depend on the model. On the workspace's own key, LLM features
// cost $0: the workspace pays its provider.
func (s *Service) featureCosts(ctx context.Context, set hd.AISettings, costs map[string]float64, ownKey bool) {
	jev := hd.Engine(s.d.Cfg).Provider == "typesafe"
	for feature := range typicalTokens {
		switch {
		case jev && (feature == "triage" || feature == "decide"):
		case ownKey:
			costs[feature] = 0
		default:
			costs[feature] = s.typicalCost(ctx, feature, s.tierModel(set, featureTier(feature))).USD()
		}
	}
}
