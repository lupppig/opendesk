package ai

// A workspace's own AI provider key (contracts/ai.md "Provider per workspace"). With a
// key, every LLM call of the workspace goes to its provider and charges nothing to the
// AI balance. Jev decisions run on an OpenRouter key too, or on the workspace's own
// TypeSafe key (decisions_key.go); embeddings stay on the instance.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai/decide"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/netsafe"
)

// Provider kinds a workspace may bring its own key for.
const (
	KeyOpenRouter = "openrouter"
	KeyOpenAI     = "openai" // any OpenAI-compatible endpoint
	KeyAnthropic  = "anthropic"
)

// What the provider last said about a workspace key.
const (
	keyOK       = "ok"
	keyRejected = "rejected"
	keyNoFunds  = "no_funds"
)

// NotifyAIKeyFailed tells owners and admins that the workspace's own AI key was rejected
// or ran out of funds, at most once an hour.
const NotifyAIKeyFailed = "ai_key_failed"

// keyNotifyEvery is the minimum time between two key failure notifications.
const keyNotifyEvery = time.Hour

// ownDefaults are a provider's usual default and fast models when the key names none.
// An OpenAI-compatible endpoint has none: its key row must name a model.
var ownDefaults = map[string][2]string{
	KeyOpenRouter: {"anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5"},
	KeyAnthropic:  {"claude-sonnet-5-5", "claude-haiku-4-5-20251001"},
}

// route is where a workspace's LLM calls go: the instance's provider (charged to the AI
// balance) or its own key (recorded at $0).
type route struct {
	p      Provider
	own    bool
	kind   string
	status string
	models struct{ Default, Fast string }
	entry  *ownEntry
}

// ownEntry caches a workspace's provider built from its key, until the key changes.
type ownEntry struct {
	updated time.Time
	p       Provider
	kind    string
	baseURL string
	key     string
	models  struct{ Default, Fast string }

	// jev is Jev through OpenRouter's Decisions API on an OpenRouter key, built once.
	jevOnce sync.Once
	jev     *decide.TypeSafe

	catMu      sync.Mutex
	catFetched time.Time
	cat        map[string]ModelInfo
	catIDs     []string
}

// ownKeys is the per-workspace provider cache.
type ownKeys struct {
	mu sync.Mutex
	m  map[uuid.UUID]*ownEntry
}

// ownKeyError is a call that failed because the workspace's own key was rejected or ran
// out of funds. It unwraps to the provider error (an outage for every feature).
type ownKeyError struct {
	status string
	err    error
}

func (e *ownKeyError) Error() string { return "workspace ai key " + e.status }
func (e *ownKeyError) Unwrap() error { return e.err }

func (s *Service) instanceRoute() *route {
	r := &route{p: s.provider}
	r.models.Default, r.models.Fast = s.models.Default, s.models.Fast
	return r
}

// routeFor returns where the workspace's LLM calls go. A key that cannot be read fails
// the call: a workspace on its own key never falls back to the AI balance silently.
func (s *Service) routeFor(ctx context.Context, wid uuid.UUID) (*route, error) {
	var kind, baseURL, model, fast, status string
	var sealed []byte
	var updated time.Time
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT kind, base_url, key_enc, model, fast_model, status, updated_at
		FROM ai_provider_keys WHERE workspace_id = $1`, wid).Scan(&kind, &baseURL, &sealed, &model, &fast, &status, &updated)
	if db.IsNotFound(err) {
		return s.instanceRoute(), nil
	}
	if err != nil {
		return nil, err
	}
	s.own.mu.Lock()
	defer s.own.mu.Unlock()
	if s.own.m == nil {
		s.own.m = map[uuid.UUID]*ownEntry{}
	}
	e := s.own.m[wid]
	if e == nil || !e.updated.Equal(updated) {
		key, err := s.d.Box.Open(sealed)
		if err != nil {
			return nil, errors.New("ai: workspace key cannot be opened")
		}
		e = &ownEntry{updated: updated, kind: kind, baseURL: baseURL, key: string(key)}
		e.p = s.buildOwn(kind, baseURL, string(key))
		def := ownDefaults[kind]
		e.models.Default, e.models.Fast = firstNonEmpty(model, def[0]), firstNonEmpty(fast, def[1], model, def[0])
		s.own.m[wid] = e
	}
	r := &route{p: e.p, own: true, kind: kind, status: status, entry: e}
	r.models = e.models
	return r, nil
}

// usesOwnKey reports whether the workspace's LLM calls run on its own key.
func (s *Service) usesOwnKey(ctx context.Context, wid uuid.UUID) bool {
	var ok bool
	_ = s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_provider_keys WHERE workspace_id = $1)`, wid).Scan(&ok)
	return ok
}

// buildOwn returns the provider for a workspace key. OpenRouter and Anthropic use the
// instance's configured API roots; an OpenAI-compatible endpoint is a user-chosen URL,
// reached only through the SSRF guard.
func (s *Service) buildOwn(kind, baseURL, key string) Provider {
	switch kind {
	case KeyOpenRouter:
		return NewOpenRouter(s.d.Cfg.OpenRouterBaseURL, key, s.d.Cfg.AppURL)
	case KeyAnthropic:
		return NewAnthropic(key, s.anthropicBase)
	}
	return NewOpenAI(baseURL, key).withClient(s.guardedClient())
}

// guard is the SSRF guard for user-chosen endpoints; development instances also allow
// loopback http.
func (s *Service) guard() netsafe.Guard { return netsafe.Guard{Dev: s.guardDev} }

// guardedClient is an HTTP client whose dialer refuses non-public addresses at connect
// time, with no proxy and no redirects.
func (s *Service) guardedClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           s.guard().DialContext(d),
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   16,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// keyResult records what a call on the workspace's own key said about the key: a
// success clears a failed state; a rejected or unfunded key is stored, owners and admins
// are told (once an hour), and the error is marked so the API names the key. Other
// errors pass through.
func (s *Service) keyResult(ctx context.Context, wid uuid.UUID, rt *route, err error) error {
	if rt == nil || !rt.own {
		return err
	}
	return s.keyResultOn(ctx, wid, tableProviderKeys, rt.kind, &rt.status, err)
}

// Tables of a workspace's own keys, one row per workspace, each with status, status_at
// and notified_at.
const (
	tableProviderKeys = "ai_provider_keys"
	tableDecisionKeys = "ai_decision_keys"
)

// keyResultOn is keyResult for the key in table (one of the key tables above); status
// is the caller's copy of the key's state, updated in place.
func (s *Service) keyResultOn(ctx context.Context, wid uuid.UUID, table, kind string, status *string, err error) error {
	ctx = context.WithoutCancel(ctx)
	if err == nil {
		if *status != keyOK {
			if _, e := s.d.DB.Pool.Exec(ctx, `UPDATE `+table+` SET status = 'ok', status_at = now() WHERE workspace_id = $1 AND status <> 'ok'`, wid); e != nil {
				s.d.Log.Warn("ai key status", "workspace_id", wid, "err", e)
			}
			*status = keyOK
		}
		return nil
	}
	next := ""
	switch {
	case errors.Is(err, ErrKeyRejected):
		next = keyRejected
	case errors.Is(err, ErrNoFunds):
		next = keyNoFunds
	default:
		return err
	}
	if e := s.markKey(ctx, wid, table, kind, next); e != nil {
		s.d.Log.Warn("ai key status", "workspace_id", wid, "err", e)
	}
	*status = next
	return &ownKeyError{status: next, err: err}
}

// markKey stores a failed key state and notifies owners and admins unless they were
// told within keyNotifyEvery. The conditional update makes concurrent failures notify once.
func (s *Service) markKey(ctx context.Context, wid uuid.UUID, table, kind, status string) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var notify bool
		err := tx.QueryRow(ctx, `UPDATE `+table+` SET status = $2, status_at = now(),
				notified_at = CASE WHEN notified_at IS NULL OR notified_at < now() - $3 * interval '1 second' THEN now() ELSE notified_at END
			WHERE workspace_id = $1
			RETURNING notified_at = now()`, wid, status, int(keyNotifyEvery.Seconds())).Scan(&notify)
		if db.IsNotFound(err) || (err == nil && !notify) {
			return nil
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM members WHERE workspace_id = $1 AND role IN ('owner', 'admin')`, wid)
		if err != nil {
			return err
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, uid := range users {
			if err := hd.Notify(ctx, tx, wid, uid, NotifyAIKeyFailed, nil, map[string]any{"provider": kind, "reason": status}); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- models on a workspace key ------------------------------------------------------

// ownCatalog returns the models of the workspace's provider, refreshed hourly (a failed
// fetch retries after a minute). ids are the defaults first, then the curated models the
// provider lists, then the rest by id.
func (s *Service) ownCatalog(ctx context.Context, rt *route) (map[string]ModelInfo, []string) {
	e := rt.entry
	e.catMu.Lock()
	defer e.catMu.Unlock()
	if e.cat != nil && time.Since(e.catFetched) < catalogTTL {
		return e.cat, e.catIDs
	}
	info, err := s.listModels(ctx, e.kind, e.baseURL, e.key)
	if err != nil {
		s.d.Log.Warn("ai: workspace model list unavailable", "kind", e.kind, "reason", errReason(err))
		e.catFetched = time.Now().Add(-catalogTTL + time.Minute)
		return e.cat, e.catIDs
	}
	ids := make([]string, 0, len(info))
	add := func(id string) {
		if _, ok := info[id]; ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	add(e.models.Default)
	add(e.models.Fast)
	if e.kind == KeyOpenRouter {
		for _, id := range curatedModels {
			add(id)
		}
	}
	rest := make([]string, 0, len(info))
	for id := range info {
		if !slices.Contains(ids, id) {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	e.cat, e.catIDs, e.catFetched = info, append(ids, rest...), time.Now()
	return e.cat, e.catIDs
}

// ownModelOK reports whether id is a model of the workspace's provider: one it lists,
// or the key's defaults.
func (s *Service) ownModelOK(ctx context.Context, rt *route, id string) bool {
	if id == "" {
		return false
	}
	if id == rt.models.Default || id == rt.models.Fast {
		return true
	}
	info, _ := s.ownCatalog(ctx, rt)
	_, ok := info[id]
	return ok
}

// ownModelList is GET /v1/ai/models on a workspace key: the provider's models, without
// prices (the workspace pays its provider; calls on the key charge nothing).
func (s *Service) ownModelList(ctx context.Context, rt *route) []ModelInfo {
	info, ids := s.ownCatalog(ctx, rt)
	out := make([]ModelInfo, 0, len(ids)+2)
	for _, id := range []string{rt.models.Default, rt.models.Fast} {
		if _, ok := info[id]; !ok && id != "" && !slices.ContainsFunc(out, func(m ModelInfo) bool { return m.ID == id }) {
			out = append(out, ModelInfo{ID: id, Name: id})
		}
	}
	for _, id := range ids {
		m := info[id]
		m.USDPerMTok, m.TypicalReplyUSD = nil, nil
		out = append(out, m)
	}
	return out
}

// modelCheck returns the validator for a workspace's model choices: the instance's list,
// or its own provider's models.
func (s *Service) modelCheck(ctx context.Context, wid uuid.UUID) func(string) bool {
	rt, err := s.routeFor(ctx, wid)
	if err != nil {
		return func(string) bool { return false }
	}
	if !rt.own {
		return s.allowedModel
	}
	return func(id string) bool { return s.ownModelOK(ctx, rt, id) }
}

// tierModelOn picks the model of a tier on a route.
func (s *Service) tierModelOn(ctx context.Context, rt *route, set hd.AISettings, tier string) string {
	if !rt.own {
		return s.tierModel(set, tier)
	}
	pick := func(choice, fallback string) string {
		if choice != "" && s.ownModelOK(ctx, rt, choice) {
			return choice
		}
		return fallback
	}
	def := pick(set.Model, rt.models.Default)
	switch tier {
	case tierFast:
		return pick(set.FastModel, rt.models.Fast)
	case tierWidget:
		return pick(set.WidgetModel, def)
	}
	return def
}

const maxListedModels = 1000

// listModels reads a provider's model list with a key. Failures are the provider errors
// (ErrKeyRejected, ErrNoFunds joined with ErrUnavailable), errNoModelList when the
// endpoint has no model list, else ErrUnavailable. Response bodies are never surfaced.
func (s *Service) listModels(ctx context.Context, kind, baseURL, key string) (map[string]ModelInfo, error) {
	switch kind {
	case KeyOpenRouter:
		return fetchOpenRouterModels(ctx, s.d.Cfg.OpenRouterBaseURL, key)
	case KeyAnthropic:
		base := strings.TrimRight(firstNonEmpty(s.anthropicBase, "https://api.anthropic.com"), "/")
		return fetchModelIDs(ctx, newHTTPClient(), base+"/v1/models?limit=1000", map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"})
	}
	return fetchModelIDs(ctx, s.guardedClient(), strings.TrimRight(baseURL, "/")+"/models", map[string]string{"Authorization": "Bearer " + key})
}

// errNoModelList means an OpenAI-compatible endpoint does not list its models.
var errNoModelList = errors.New("endpoint has no model list")

// fetchModelIDs reads an OpenAI- or Anthropic-style model list ({data: [{id, display_name?}]}).
func fetchModelIDs(ctx context.Context, c *http.Client, u string, headers map[string]string) (map[string]ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusMethodNotAllowed:
		return nil, errNoModelList
	case res.StatusCode != http.StatusOK:
		return nil, statusError(res.StatusCode)
	}
	var body struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Name        string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body); err != nil {
		return nil, errNoModelList
	}
	out := make(map[string]ModelInfo, len(body.Data))
	for _, m := range body.Data {
		if !validModelID(m.ID) || len(out) >= maxListedModels {
			continue
		}
		out[m.ID] = ModelInfo{ID: m.ID, Name: firstNonEmpty(cleanName(m.DisplayName), cleanName(m.Name), m.ID)}
	}
	return out, nil
}

func cleanName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return -1
		}
		return r
	}, s))
	if len([]rune(s)) > 120 {
		return ""
	}
	return s
}

// validModelID accepts plain model ids: 1..200 printable characters without spaces or
// markup.
func validModelID(id string) bool {
	if id == "" || len(id) > 200 {
		return false
	}
	for _, r := range id {
		if r <= ' ' || r == 0x7f || r == '<' || r == '>' || r == '"' || unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// --- test and save ------------------------------------------------------------------

// ProviderKeyInput is the candidate key of POST /v1/ai/provider/test and PUT /v1/ai/provider.
type ProviderKeyInput struct {
	Kind      string `json:"kind" enum:"openrouter,openai,anthropic"`
	APIKey    string `json:"api_key" maxLength:"500" doc:"Write only; never returned"`
	BaseURL   string `json:"base_url,omitempty" maxLength:"2000" doc:"OpenAI-compatible endpoints only, e.g. https://api.openai.com/v1"`
	Model     string `json:"model,omitempty" maxLength:"200" doc:"Default model on this provider; required for OpenAI-compatible endpoints"`
	FastModel string `json:"fast_model,omitempty" maxLength:"200" doc:"Fast model (rewrite, translate, LLM triage); defaults to model"`
}

// ProviderTest is the result of POST /v1/ai/provider/test.
type ProviderTest struct {
	OK      bool     `json:"ok"`
	Reason  string   `json:"reason,omitempty" enum:"invalid_key,no_funds,unreachable,busy,model_unavailable"`
	Message string   `json:"message,omitempty"`
	Models  []string `json:"models" doc:"Model ids the provider lists (at most 1,000)"`
}

func providerLabel(kind string) string {
	switch kind {
	case KeyOpenRouter:
		return "OpenRouter"
	case KeyAnthropic:
		return "Anthropic"
	}
	return "The endpoint"
}

// checkKeyInput normalizes and validates a candidate key. The base URL must be https
// and resolve only to public addresses, except on a development instance.
func (s *Service) checkKeyInput(ctx context.Context, in *ProviderKeyInput) error {
	in.APIKey = strings.TrimSpace(in.APIKey)
	in.Model, in.FastModel = strings.TrimSpace(in.Model), strings.TrimSpace(in.FastModel)
	switch in.Kind {
	case KeyOpenRouter, KeyAnthropic:
		in.BaseURL = ""
	case KeyOpenAI:
		u, err := s.guard().ValidateURL(ctx, in.BaseURL)
		if err != nil {
			return httpx.Validation(err.Error())
		}
		pu, _ := url.Parse(u)
		pu.RawQuery = ""
		in.BaseURL = strings.TrimRight(pu.String(), "/")
		if in.Model == "" {
			return httpx.Validation("Enter the model to use on this endpoint.")
		}
	default:
		return httpx.Validation("kind must be openrouter, openai or anthropic.")
	}
	if len(in.APIKey) < 8 || len(in.APIKey) > 500 || strings.IndexFunc(in.APIKey, func(r rune) bool { return r <= ' ' || r == 0x7f || r > unicode.MaxASCII }) >= 0 {
		return httpx.Validation("Enter the API key exactly as the provider shows it.")
	}
	for _, m := range []string{in.Model, in.FastModel} {
		if m != "" && !validModelID(m) {
			return httpx.Validation("Model ids are up to 200 characters, without spaces.")
		}
	}
	return nil
}

func testFailure(reason, msg string) *ProviderTest {
	return &ProviderTest{Reason: reason, Message: msg, Models: []string{}}
}

// testKey checks a validated candidate key with a tiny call: the model list (and, on
// OpenRouter, the key's remaining limit), or a one-line completion on an endpoint that
// lists no models. It never echoes provider responses.
func (s *Service) testKey(ctx context.Context, in ProviderKeyInput) *ProviderTest {
	label := providerLabel(in.Kind)
	fail := func(err error) *ProviderTest {
		switch {
		case errors.Is(err, ErrKeyRejected):
			return testFailure("invalid_key", label+" rejected this key. Check that it's correct and still active.")
		case errors.Is(err, ErrNoFunds):
			if in.Kind == KeyOpenAI {
				return testFailure("no_funds", "The account behind this key is out of funds or quota.")
			}
			return testFailure("no_funds", "The "+label+" account behind this key is out of credits.")
		case errors.Is(err, ErrBusy):
			return testFailure("busy", label+" is rate limiting this key. Try again in a moment.")
		}
		if in.Kind == KeyOpenAI {
			return testFailure("unreachable", "Couldn't reach the endpoint. Check the base URL and try again.")
		}
		return testFailure("unreachable", "Couldn't reach "+label+". Try again in a moment.")
	}
	if in.Kind == KeyOpenRouter {
		if err := s.openRouterKeyCheck(ctx, in.APIKey); err != nil {
			return fail(err)
		}
	}
	info, err := s.listModels(ctx, in.Kind, in.BaseURL, in.APIKey)
	if errors.Is(err, errNoModelList) && in.Kind == KeyOpenAI {
		// No model list: a one-line completion proves the key and the model.
		p := s.buildOwn(in.Kind, in.BaseURL, in.APIKey)
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := p.Complete(cctx, Request{Model: in.Model, MaxTokens: 16, Messages: []Message{UserText("Reply with OK.")}}); err != nil {
			return fail(err)
		}
		return &ProviderTest{OK: true, Models: []string{}}
	}
	if err != nil {
		return fail(err)
	}
	ids := make([]string, 0, len(info))
	for id := range info {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, m := range []string{in.Model, in.FastModel} {
		if m != "" && len(info) > 0 {
			if _, ok := info[m]; !ok {
				t := testFailure("model_unavailable", label+" doesn't list the model "+m+".")
				t.Models = ids
				return t
			}
		}
	}
	return &ProviderTest{OK: true, Models: ids}
}

// openRouterKeyCheck reads GET {base}/v1/key: 401 is a rejected key, a spending limit
// with nothing left is out of funds.
func (s *Service) openRouterKeyCheck(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.d.Cfg.OpenRouterBaseURL, "/")+"/v1/key", nil)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := newHTTPClient().Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return statusError(res.StatusCode)
	}
	var body struct {
		Data struct {
			LimitRemaining *float64 `json:"limit_remaining"`
		} `json:"data"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if json.NewDecoder(bytes.NewReader(raw)).Decode(&body) == nil && body.Data.LimitRemaining != nil && *body.Data.LimitRemaining <= 0 {
		return errors.Join(ErrUnavailable, ErrNoFunds)
	}
	return nil
}
