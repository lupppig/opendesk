package ai

import (
	"errors"
	"strings"
	"testing"

	"github.com/useopendesk/opendesk/internal/ai/decide"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
)

// tsOwnKey is a workspace's own TypeSafe key in tests.
const tsOwnKey = "ts-own-key-0123456789ABCD"

func greetingDecision() decide.Request {
	return decide.Request{State: map[string]any{"text": "Hello"},
		Questions: map[string]decide.Question{"q0": decide.Noul("Is `text` a greeting?", "", "")}}
}

func TestOwnOpenRouterKeyRunsJev(t *testing.T) {
	// The instance has no decision engine: Jev runs on the workspace's OpenRouter key.
	e := newEnv(t, opts{billing: true})
	own := newOwnFake(t, "anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5")
	jev := newFakeTypeSafe(t)
	own.decisions = jev
	e.useOpenRouterKey(own)
	jevWorkspace(e)
	e.settings(map[string]any{"auto_triage": true})

	var view ProviderView
	e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200).JSON(t, &view)
	if view.Decisions.RunsOn != DecisionsProviderKey || view.Decisions.Engine != "jev" || view.Decisions.Key != nil {
		t.Fatalf("%+v", view.Decisions)
	}
	var cfg AIConfig
	e.Do("GET", "/v1/ai/config", e.agent(), nil).Want(t, 200).JSON(t, &cfg)
	if !cfg.DecisionsOwnKey || cfg.Costs["triage"] != 0 || cfg.Costs["answer_check"] != 0 || cfg.Decisions.Provider != "typesafe" || !cfg.Decisions.Connected {
		t.Fatalf("%+v", cfg)
	}
	tr := e.Do("GET", "/v1/triage", e.admin(), nil).Want(t, 200).Map(t)
	if eng, _ := tr["engine"].(map[string]any); eng["provider"] != "typesafe" {
		t.Fatalf("triage engine %v", tr["engine"])
	}

	tk := e.ticket("Charged twice", "I was charged twice for order 1234.")
	e.runTriggers()
	if jev.calls() != 1 || own.calls() != 0 || e.F.calls() != 0 {
		t.Fatalf("jev %d, own llm %d, instance llm %d", jev.calls(), own.calls(), e.F.calls())
	}
	if req := jev.last(); req.Auth != "Bearer "+orKey || req.Model != "~typesafe/jev-latest" {
		t.Fatalf("auth/model %q %q", req.Auth, req.Model)
	}
	if got := e.loadTicket(tk.ID); got.Priority != "high" || e.decisions(got).Engine != "jev" {
		t.Fatalf("not triaged on Jev: %+v", got)
	}
	if n := e.count(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND feature = 'triage' AND source = 'own_key' AND micro_usd = 0`, e.WS.ID); n != 1 {
		t.Fatalf("usage %d", n)
	}

	// OpenRouter refuses the key: recorded on the key, owners and admins told, the error names it.
	jev.fail(401)
	_, err := e.S.newCall(e.WS.ID, nil, nil, "decide").decide(e.Ctx, greetingDecision())
	var ke *ownKeyError
	if !errors.As(err, &ke) || ke.status != keyRejected || !skippable(err) {
		t.Fatalf("err %v", err)
	}
	var status string
	e.Scalar(`SELECT status FROM ai_provider_keys WHERE workspace_id = $1`, &status, e.WS.ID)
	if status != keyRejected {
		t.Fatalf("status %q", status)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE workspace_id = $1 AND type = $2 AND data->>'provider' = 'openrouter'`, e.WS.ID, NotifyAIKeyFailed); n != 2 {
		t.Fatalf("notifications %d", n)
	}
	// The next decision that works clears it.
	if _, err := e.S.newCall(e.WS.ID, nil, nil, "decide").decide(e.Ctx, greetingDecision()); err != nil {
		t.Fatal(err)
	}
	e.Scalar(`SELECT status FROM ai_provider_keys WHERE workspace_id = $1`, &status, e.WS.ID)
	if status != keyOK {
		t.Fatalf("status %q", status)
	}
}

func TestDecisionsTypeSafeKey(t *testing.T) {
	// The instance decides on its own Jev; the workspace brings a TypeSafe key.
	e := newEnv(t, opts{billing: true, jev: true})
	ownTS := newFakeTypeSafe(t)
	e.D.Cfg.TypesafeAPIURL = ownTS.srv.URL
	jevWorkspace(e)
	e.settings(map[string]any{"auto_triage": true})

	put := func(a hdtest.Auth) *struct{ Status int } {
		r := e.Do("PUT", "/v1/ai/provider/decisions", a, map[string]any{"api_key": tsOwnKey})
		return &struct{ Status int }{r.Status}
	}
	if put(e.agent()).Status != 403 {
		t.Fatal("agent saved a decisions key")
	}
	e.Do("POST", "/v1/ai/provider/decisions/test", e.admin(), map[string]any{"api_key": "short"}).Want(t, 422)

	// The test explains a bad key without echoing TypeSafe.
	ownTS.fail(401)
	var res ProviderTest
	e.Do("POST", "/v1/ai/provider/decisions/test", e.admin(), map[string]any{"api_key": tsOwnKey}).Want(t, 200).JSON(t, &res)
	if res.OK || res.Reason != "invalid_key" {
		t.Fatalf("%+v", res)
	}
	ownTS.fail(402)
	e.Do("POST", "/v1/ai/provider/decisions/test", e.admin(), map[string]any{"api_key": tsOwnKey}).Want(t, 200).JSON(t, &res)
	if res.OK || res.Reason != "no_funds" {
		t.Fatalf("%+v", res)
	}
	ownTS.fail(401)
	if put(e.admin()).Status != 422 {
		t.Fatal("saved a refused key")
	}

	// Saved alongside an OpenRouter key: the TypeSafe key wins for decisions.
	own := newOwnFake(t, "anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5")
	orJev := newFakeTypeSafe(t)
	own.decisions = orJev
	e.useOpenRouterKey(own)
	r := e.Do("PUT", "/v1/ai/provider/decisions", e.admin(), map[string]any{"api_key": tsOwnKey}).Want(t, 200)
	if strings.Contains(string(r.Body), tsOwnKey) {
		t.Fatal("key returned")
	}
	var view ProviderView
	r.JSON(t, &view)
	if view.Decisions.RunsOn != DecisionsTypeSafeKey || view.Decisions.Key == nil || view.Decisions.Key.Last4 != "ABCD" ||
		view.Decisions.Key.Status != "ok" || view.Decisions.Key.CreatedBy == nil || view.Mode != "own_key" {
		t.Fatalf("%+v", view)
	}
	var sealed []byte
	e.Scalar(`SELECT key_enc FROM ai_decision_keys WHERE workspace_id = $1`, &sealed, e.WS.ID)
	if strings.Contains(string(sealed), tsOwnKey) {
		t.Fatal("key stored in clear")
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.decisions_key_set' AND data::text NOT LIKE '%0123456789%'`, e.WS.ID); n != 1 {
		t.Fatalf("audit %d", n)
	}

	before, instance := ownTS.calls(), e.TS.calls()
	tk := e.ticket("Charged twice", "I was charged twice for order 1234.")
	e.runTriggers()
	if ownTS.calls() != before+1 || orJev.calls() != 0 || e.TS.calls() != instance || own.calls() != 0 {
		t.Fatalf("own ts %d, openrouter jev %d, instance jev %d, own llm %d", ownTS.calls()-before, orJev.calls(), e.TS.calls()-instance, own.calls())
	}
	if req := ownTS.last(); req.Auth != "Bearer "+tsOwnKey || req.Model != "jev-latest" {
		t.Fatalf("auth/model %q %q", req.Auth, req.Model)
	}
	if got := e.loadTicket(tk.ID); got.Priority != "high" {
		t.Fatalf("not triaged: %+v", got)
	}
	if n := e.count(`SELECT count(*) FROM ai_usage WHERE workspace_id = $1 AND feature = 'triage' AND source = 'own_key' AND micro_usd = 0`, e.WS.ID); n != 1 {
		t.Fatalf("usage %d", n)
	}

	// TypeSafe refuses the key later: recorded on it, owners and admins told once.
	ownTS.fail(401)
	_, err := e.S.newCall(e.WS.ID, nil, nil, "decide").decide(e.Ctx, greetingDecision())
	if !errors.As(err, new(*ownKeyError)) {
		t.Fatalf("err %v", err)
	}
	e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200).JSON(t, &view)
	if view.Decisions.Key.Status != keyRejected || view.Decisions.Key.StatusAt == nil {
		t.Fatalf("%+v", view.Decisions.Key)
	}
	var orStatus string
	e.Scalar(`SELECT status FROM ai_provider_keys WHERE workspace_id = $1`, &orStatus, e.WS.ID)
	if orStatus != keyOK {
		t.Fatalf("provider key marked %q", orStatus)
	}
	if n := e.count(`SELECT count(*) FROM notifications WHERE workspace_id = $1 AND type = $2 AND data->>'provider' = 'typesafe'`, e.WS.ID, NotifyAIKeyFailed); n != 2 {
		t.Fatalf("notifications %d", n)
	}

	// Removing it returns decisions to the OpenRouter key, then (without it) to the instance.
	e.Do("DELETE", "/v1/ai/provider/decisions", e.agent(), nil).Want(t, 403)
	e.Do("DELETE", "/v1/ai/provider/decisions", e.admin(), nil).Want(t, 204)
	e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200).JSON(t, &view)
	if view.Decisions.RunsOn != DecisionsProviderKey || view.Decisions.Key != nil {
		t.Fatalf("%+v", view.Decisions)
	}
	if n := e.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.decisions_key_removed'`, e.WS.ID); n != 1 {
		t.Fatalf("audit %d", n)
	}
	e.Do("DELETE", "/v1/ai/provider", e.admin(), nil).Want(t, 204)
	e.Do("GET", "/v1/ai/provider", e.admin(), nil).Want(t, 200).JSON(t, &view)
	if view.Decisions.RunsOn != DecisionsInstance || view.Decisions.Engine != "jev" {
		t.Fatalf("%+v", view.Decisions)
	}
	// On the instance's Jev, a decision is charged again.
	before = e.TS.calls()
	c := e.S.newCall(e.WS.ID, nil, nil, "decide")
	if _, err := c.decide(e.Ctx, greetingDecision()); err != nil || c.onKey || e.TS.calls() != before+1 {
		t.Fatalf("err %v onKey %v calls %d", err, c.onKey, e.TS.calls()-before)
	}
}
