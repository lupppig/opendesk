package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/useopendesk/opendesk/internal/plan"
)

// ownFake is a provider reached with a workspace's own key: OpenRouter's key and model
// routes, an OpenAI-compatible /models, and chat through fakeLLM. It records the
// Authorization header of every chat request.
type ownFake struct {
	*fakeLLM
	srv *httptest.Server

	mu        sync.Mutex
	auths     []string
	paths     []string
	keyStatus int      // GET .../key answers this status when set
	models    []string // GET .../models; nil answers 404
	// decisions answers OpenRouter's Decisions API (Jev on the key) when set.
	decisions *fakeTypeSafe
}

func newOwnFake(t *testing.T, models ...string) *ownFake {
	o := &ownFake{fakeLLM: newFake(t), models: models}
	o.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		keyStatus, list, decisions := o.keyStatus, o.models, o.decisions
		o.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/alpha/decisions") && decisions != nil:
			decisions.serve(w, r)
			return
		case strings.HasSuffix(r.URL.Path, "/key"):
			if keyStatus != 0 {
				w.WriteHeader(keyStatus)
				_, _ = fmt.Fprint(w, `{"error":{"message":"provider-secret-detail"}}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"data":{"label":"k","limit_remaining":null}}`)
			return
		case strings.HasSuffix(r.URL.Path, "/models") && r.Method == http.MethodGet:
			if list == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if keyStatus != 0 {
				w.WriteHeader(keyStatus)
				return
			}
			data := []any{}
			for _, id := range list {
				data = append(data, map[string]any{"id": id, "name": "Vendor: " + id, "context_length": 200000,
					"pricing": map[string]any{"prompt": "0.000003", "completion": "0.000015"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		o.mu.Lock()
		o.auths = append(o.auths, r.Header.Get("Authorization"))
		o.paths = append(o.paths, r.URL.Path)
		o.mu.Unlock()
		o.serve(w, r)
	}))
	t.Cleanup(o.srv.Close)
	return o
}

func (o *ownFake) setKeyStatus(st int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.keyStatus = st
}

func (o *ownFake) lastAuth() (string, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.auths) == 0 {
		return "", ""
	}
	return o.auths[len(o.auths)-1], o.paths[len(o.paths)-1]
}

const orKey = "sk-or-v1-0123456789abcdefWXYZ"

// useOpenRouterKey saves an OpenRouter key whose API root is own.
func (e *env) useOpenRouterKey(own *ownFake) map[string]any {
	e.T.Helper()
	e.D.Cfg.OpenRouterBaseURL = own.srv.URL + "/api"
	return e.Do("PUT", "/v1/ai/provider", e.admin(), map[string]any{"kind": "openrouter", "api_key": orKey}).Want(e.T, 200).Map(e.T)
}

func TestOwnKeyOpenRouter(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	own := newOwnFake(t, "anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5", "openai/gpt-5.6-sol", "mistral/large")

	// Only admins see and change the provider.
	e.Do("GET", "/v1/ai/provider", e.agent(), nil).Want(t, 403)
	e.Do("PUT", "/v1/ai/provider", e.agent(), map[string]any{"kind": "openrouter", "api_key": orKey}).Want(t, 403)
	var view ProviderView
	e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200).JSON(t, &view)
	if view.Mode != "instance" || view.Key != nil || view.InstanceProvider != "anthropic" || !view.Billing {
		t.Fatalf("%+v", view)
	}

	saved := e.useOpenRouterKey(own)
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), orKey) || strings.Contains(string(raw), "0123456789abcdef") {
		t.Fatalf("key returned: %s", raw)
	}
	key, _ := saved["key"].(map[string]any)
	if saved["mode"] != "own_key" || key["kind"] != "openrouter" || key["last4"] != "WXYZ" || key["status"] != "ok" ||
		key["model"] != "anthropic/claude-sonnet-5.5" || key["created_by"] == nil {
		t.Fatalf("%s", raw)
	}
	get := e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200)
	if strings.Contains(string(get.Body), orKey) {
		t.Fatal("key returned by GET")
	}
	// Stored sealed, audited without the key.
	var sealed []byte
	e.Scalar(`SELECT key_enc FROM ai_provider_keys WHERE workspace_id = $1`, &sealed, e.WS.ID)
	if strings.Contains(string(sealed), orKey) {
		t.Fatal("key stored in clear")
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.provider_key_set' AND data::text NOT LIKE '%0123456789%'`, e.WS.ID); n != 1 {
		t.Fatalf("audit %d", n)
	}

	// No AI balance left: calls on the workspace key still run and charge nothing.
	if err := plan.Ensure(e.Ctx, e.D.DB.Pool, e.WS.ID); err != nil {
		t.Fatal(err)
	}
	e.Exec(`UPDATE ai_balances SET included_used_micro_usd = 10000000, purchased_micro_usd = 0 WHERE workspace_id = $1`, e.WS.ID)
	tk := e.ticket("Charged twice", "I was charged twice for order 1234")
	e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{}).Want(t, 200)
	if e.F.calls() != 0 {
		t.Fatalf("instance provider called %d times", e.F.calls())
	}
	auth, path := own.lastAuth()
	if auth != "Bearer "+orKey || path != "/api/v1/chat/completions" {
		t.Fatalf("auth %q path %q", auth, path)
	}
	if m := own.last()["model"]; m != "anthropic/claude-sonnet-5.5" {
		t.Fatalf("model %v", m)
	}
	var usd int64
	var source string
	e.Scalar(`SELECT micro_usd FROM ai_usage WHERE workspace_id = $1 AND feature = 'summarize'`, &usd, e.WS.ID)
	e.Scalar(`SELECT source FROM ai_usage WHERE workspace_id = $1 AND feature = 'summarize'`, &source, e.WS.ID)
	if usd != 0 || source != "own_key" {
		t.Fatalf("usage %d %s", usd, source)
	}
	var used int64
	e.Scalar(`SELECT included_used_micro_usd FROM ai_balances WHERE workspace_id = $1`, &used, e.WS.ID)
	if used != 10000000 {
		t.Fatalf("balance changed: %d", used)
	}

	// Streaming draft and the assistant-style tool turn go to the key too.
	st, _, evs, body := e.stream(fmt.Sprintf("/v1/tickets/%s/ai/draft", tk.ID), e.agent(), map[string]any{})
	if st != 200 || len(evs) == 0 || evs[len(evs)-1].Name != "done" {
		t.Fatalf("draft %d %s %v", st, body, evs)
	}
	if _, err := e.S.AssistantTurn(e.Ctx, e.WS.ID, &e.WS.Agent.ID, ChatRequest{Turns: []ChatTurn{{Role: "user", Text: "hi"}}, MaxTokens: 100}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.S.Reserve(e.Ctx, e.WS.ID, "assistant"); err != nil {
		t.Fatalf("reserve on own key: %v", err)
	}
	if e.F.calls() != 0 || e.count(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND (source <> 'own_key' OR micro_usd <> 0)`, e.WS.ID) != 0 {
		t.Fatal("charged on own key")
	}

	// Models are the provider's; choices must be valid for it.
	var models ModelList
	e.Do("GET", "/v1/ai/models", e.viewer(), nil).Want(t, 200).JSON(t, &models)
	if models.Default != "anthropic/claude-sonnet-5.5" || len(models.Data) != 4 || models.Data[0].USDPerMTok != nil {
		t.Fatalf("%+v", models)
	}
	e.Do("PATCH", "/v1/ai/settings", e.admin(), map[string]any{"model": "claude-sonnet-5-5"}).Want(t, 422)
	e.Do("PATCH", "/v1/ai/settings", e.admin(), map[string]any{"model": "mistral/large"}).Want(t, 200)
	e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{}).Want(t, 200)
	if m := own.last()["model"]; m != "mistral/large" {
		t.Fatalf("model %v", m)
	}
	cfg := e.Do("GET", "/v1/ai/config", e.viewer(), nil).Want(t, 200).Map(t)
	if cfg["own_key"] != true || cfg["provider"] != "openrouter" {
		t.Fatalf("%v", cfg)
	}

	// Remove: back to the AI balance, audited.
	e.Do("DELETE", "/v1/ai/provider", e.agent(), nil).Want(t, 403)
	e.Do("DELETE", "/v1/ai/provider", e.admin(), nil).Want(t, 204)
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.provider_key_removed'`, e.WS.ID); n != 1 {
		t.Fatalf("audit %d", n)
	}
	r := e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{})
	if r.Status != 402 {
		t.Fatalf("after removal with no balance: %d %s", r.Status, r.Body)
	}
	e.Exec(`UPDATE ai_balances SET included_used_micro_usd = 0 WHERE workspace_id = $1`, e.WS.ID)
	e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{}).Want(t, 200)
	if e.F.calls() != 1 {
		t.Fatalf("instance calls %d", e.F.calls())
	}
	if n := e.count(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND source = 'instance' AND micro_usd > 0`, e.WS.ID); n != 1 {
		t.Fatalf("charged usage %d", n)
	}
}

func TestOwnKeyCompatibleEndpoint(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	own := newOwnFake(t, "local-model", "local-small")
	base := own.srv.URL + "/v1"

	// A model is required; one the endpoint doesn't list fails the test.
	e.Do("PUT", "/v1/ai/provider", e.admin(), map[string]any{"kind": "openai", "api_key": "sk-local-abcd1234", "base_url": base}).Want(t, 422)
	var res ProviderTest
	e.Do("POST", "/v1/ai/provider/test", e.admin(), map[string]any{"kind": "openai", "api_key": "sk-local-abcd1234", "base_url": base, "model": "gpt-9"}).
		Want(t, 200).JSON(t, &res)
	if res.OK || res.Reason != "model_unavailable" || len(res.Models) != 2 {
		t.Fatalf("%+v", res)
	}
	e.Do("POST", "/v1/ai/provider/test", e.admin(), map[string]any{"kind": "openai", "api_key": "sk-local-abcd1234", "base_url": base, "model": "local-model"}).
		Want(t, 200).JSON(t, &res)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if n := e.count(`SELECT count(*) FROM ai_provider_keys WHERE workspace_id = $1`, e.WS.ID); n != 0 {
		t.Fatal("test stored a key")
	}
	saved := e.Do("PUT", "/v1/ai/provider", e.admin(), map[string]any{"kind": "openai", "api_key": "sk-local-abcd1234", "base_url": base,
		"model": "local-model", "fast_model": "local-small"}).Want(t, 200).Map(t)
	key, _ := saved["key"].(map[string]any)
	if key["base_url_host"] != strings.TrimPrefix(own.srv.URL, "http://") || key["last4"] != "1234" {
		t.Fatalf("%v", saved)
	}

	tk := e.ticket("Login", "I can't log in")
	e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/draft", tk.ID), e.agent(), map[string]any{}).Want(t, 200)
	if auth, path := own.lastAuth(); auth != "Bearer sk-local-abcd1234" || path != "/v1/chat/completions" {
		t.Fatalf("auth %q path %q", auth, path)
	}
	if m := own.last()["model"]; m != "local-model" {
		t.Fatalf("model %v", m)
	}
	e.Do("POST", "/v1/ai/rewrite", e.agent(), map[string]any{"html": "<p>hello</p>", "mode": "friendlier"}).Want(t, 200)
	if m := own.last()["model"]; m != "local-small" {
		t.Fatalf("fast model %v", m)
	}
	if e.F.calls() != 0 {
		t.Fatal("instance provider called")
	}
	// LLM triage runs on the key too.
	e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/triage", tk.ID), e.agent(), map[string]any{}).Want(t, 200)
	if e.F.calls() != 0 || own.last()["model"] != "local-small" {
		t.Fatal("triage not on the key")
	}
	if n := e.count(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND source = 'own_key' AND micro_usd = 0`, e.WS.ID); n != 3 {
		t.Fatalf("usage %d", n)
	}
}

func TestOwnKeySSRF(t *testing.T) {
	e := newEnv(t, opts{})
	try := func(base string) {
		t.Helper()
		for _, path := range []string{"/v1/ai/provider/test"} {
			r := e.Do("POST", path, e.admin(), map[string]any{"kind": "openai", "api_key": "sk-test-abcd1234", "base_url": base, "model": "m"})
			if r.Status != 422 {
				t.Fatalf("%s %s: %d %s", path, base, r.Status, r.Body)
			}
		}
		r := e.Do("PUT", "/v1/ai/provider", e.admin(), map[string]any{"kind": "openai", "api_key": "sk-test-abcd1234", "base_url": base, "model": "m"})
		if r.Status != 422 {
			t.Fatalf("PUT %s: %d %s", base, r.Status, r.Body)
		}
	}
	// Private and link-local targets are refused even on a development instance.
	try("https://10.0.0.5/v1")
	try("https://169.254.169.254/latest")
	try("https://[fd00::1]/v1")
	try("ftp://example.com/v1")
	try("https://user:pass@example.com/v1")
	// Production also refuses http and loopback.
	e.S.guardDev = false
	try("http://127.0.0.1:9/v1")
	try("https://localhost/v1")
	try("http://example.com/v1")
	if n := e.count(`SELECT count(*) FROM ai_provider_keys WHERE workspace_id = $1`, e.WS.ID); n != 0 {
		t.Fatal("refused key stored")
	}

	// A host that passes the check but resolves privately at connect time is refused
	// by the guarded dialer: a saved loopback endpoint cannot be reached in production.
	e.S.guardDev = true
	own := newOwnFake(t, "m")
	e.Do("PUT", "/v1/ai/provider", e.admin(), map[string]any{"kind": "openai", "api_key": "sk-test-abcd1234", "base_url": own.srv.URL + "/v1", "model": "m"}).Want(t, 200)
	e.S.guardDev = false
	e.S.own.mu.Lock()
	e.S.own.m = nil
	e.S.own.mu.Unlock()
	tk := e.ticket("Hello", "Hello")
	r := e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/draft", tk.ID), e.agent(), map[string]any{})
	if r.Status != 502 || own.calls() != 0 || e.F.calls() != 0 {
		t.Fatalf("%d %s own=%d instance=%d", r.Status, r.Body, own.calls(), e.F.calls())
	}
}

func TestOwnKeyRejected(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	own := newOwnFake(t, "anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5")

	// The test explains a bad key without echoing the provider.
	own.setKeyStatus(401)
	e.D.Cfg.OpenRouterBaseURL = own.srv.URL + "/api"
	r := e.Do("POST", "/v1/ai/provider/test", e.admin(), map[string]any{"kind": "openrouter", "api_key": orKey}).Want(t, 200)
	var res ProviderTest
	r.JSON(t, &res)
	if res.OK || res.Reason != "invalid_key" || strings.Contains(string(r.Body), "provider-secret-detail") {
		t.Fatalf("%s", r.Body)
	}
	e.Do("PUT", "/v1/ai/provider", e.admin(), map[string]any{"kind": "openrouter", "api_key": orKey}).Want(t, 422)
	own.setKeyStatus(402)
	e.Do("POST", "/v1/ai/provider/test", e.admin(), map[string]any{"kind": "openrouter", "api_key": orKey}).Want(t, 200).JSON(t, &res)
	if res.Reason != "no_funds" {
		t.Fatalf("%+v", res)
	}
	own.setKeyStatus(0)
	e.useOpenRouterKey(own)

	// The key is revoked at the provider: the feature fails like an outage, with no
	// fallback to the AI balance, and owners and admins are told once.
	tk := e.ticket("Charged twice", "Help")
	own.fail(401)
	r = e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{})
	if r.Status != 502 || !strings.Contains(string(r.Body), "ai_key_rejected") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if e.F.calls() != 0 {
		t.Fatal("fell back to the instance provider")
	}
	notified := func() int {
		return e.count(`SELECT count(*) FROM notifications WHERE workspace_id = $1 AND type = $2`, e.WS.ID, NotifyAIKeyFailed)
	}
	if n := notified(); n != 2 {
		t.Fatalf("notifications %d, want owner and admin", n)
	}
	var view ProviderView
	e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200).JSON(t, &view)
	if view.Key == nil || view.Key.Status != "rejected" || view.Key.StatusAt == nil {
		t.Fatalf("%+v", view.Key)
	}
	// Out of funds a minute later: the state changes, the notification is not repeated.
	own.fail(402)
	r = e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{})
	if r.Status != 502 || !strings.Contains(string(r.Body), "ai_key_no_funds") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if n := notified(); n != 2 {
		t.Fatalf("notifications %d after a second failure", n)
	}
	// An hour later a failure notifies again.
	e.Exec(`UPDATE ai_provider_keys SET notified_at = now() - interval '61 minutes' WHERE workspace_id = $1`, e.WS.ID)
	own.fail(403)
	e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{}).Want(t, 502)
	if n := notified(); n != 4 {
		t.Fatalf("notifications %d after an hour", n)
	}
	if n := e.count(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1`, e.WS.ID); n != 0 {
		t.Fatal("failed calls recorded")
	}
	// A working call clears the state.
	e.Do("POST", fmt.Sprintf("/v1/tickets/%s/ai/summary", tk.ID), e.agent(), map[string]any{}).Want(t, 200)
	e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200).JSON(t, &view)
	if view.Key.Status != "ok" {
		t.Fatalf("%+v", view.Key)
	}
	// Replacing the key resets the state and is audited as a replacement.
	e.useOpenRouterKey(own)
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.provider_key_replaced'`, e.WS.ID); n != 1 {
		t.Fatalf("audit %d", n)
	}
}

func TestOwnKeyWidgetPlayground(t *testing.T) {
	e := newEnv(t, opts{billing: true})
	own := newOwnFake(t, "anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5")
	e.useOpenRouterKey(own)
	if err := plan.Ensure(e.Ctx, e.D.DB.Pool, e.WS.ID); err != nil {
		t.Fatal(err)
	}
	e.Exec(`UPDATE ai_balances SET included_used_micro_usd = 10000000 WHERE workspace_id = $1`, e.WS.ID)
	body := map[string]any{"messages": []any{map[string]any{"role": "visitor", "text": "How do I reset my password?"}}}
	var out PlaygroundOutput
	e.Do("POST", "/v1/ai/agent/playground", e.admin(), body).Want(t, 200).JSON(t, &out)
	if out.USD != 0 || e.F.calls() != 0 || own.calls() == 0 {
		t.Fatalf("%+v instance=%d own=%d", out, e.F.calls(), own.calls())
	}
	if auth, _ := own.lastAuth(); auth != "Bearer "+orKey {
		t.Fatalf("auth %q", auth)
	}
}
