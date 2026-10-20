package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
)

func TestConnectListAndCallWithMeta(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, false)
	_, seq := e.Events(e.WS.ID, 0)

	var s McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Shop orders", "url": f.url()}).Want(t, 201).JSON(t, &s)
	if s.Status != StatusConnected || s.Error != nil || s.Slug != "shop_orders" || s.Auth.Kind != AuthNone {
		t.Fatalf("created %+v", s)
	}
	if s.ServerInfo == nil || s.ServerInfo.Name != "test-shop" || s.ServerInfo.Version != "1.2.3" || s.LastSyncedAt == nil {
		t.Fatalf("server info %+v", s.ServerInfo)
	}
	lookup, refund := toolByName(s, "lookup_order"), toolByName(s, "refund")
	if !lookup.ReadOnly || lookup.Destructive || !lookup.Enabled || lookup.Policy != PolicyAuto || lookup.Title == nil || *lookup.Title != "Look up an order" {
		t.Fatalf("lookup defaults %+v", lookup)
	}
	if refund.ReadOnly || !refund.Destructive || refund.Enabled || refund.Policy != PolicyConfirm {
		t.Fatalf("refund defaults %+v", refund)
	}
	types, _ := e.Events(e.WS.ID, seq)
	if !slices.Contains(types, "ai_mcp_server.created") {
		t.Fatalf("events %v", types)
	}
	var audits int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.mcp_server_created'`, &audits, e.WS.ID)
	if audits != 1 {
		t.Fatalf("audit entries %d", audits)
	}

	ctx := context.Background()
	tools, err := e.R.Tools(ctx, e.WS.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools %+v", tools)
	}
	i := slices.IndexFunc(tools, func(t Tool) bool { return t.Name == "lookup_order" })
	if i < 0 || tools[i].ServerSlug != "shop_orders" || tools[i].ServerName != "Shop orders" || tools[i].Policy != PolicyAuto || !tools[i].ReadOnly {
		t.Fatalf("tools %+v", tools)
	}
	if name, ok := ToolName(tools[i].ServerSlug, tools[i].Name); !ok || name != "shop_orders__lookup_order" {
		t.Fatalf("tool name %q %v", name, ok)
	}

	contact, conv := ids.New(), ids.New()
	res, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", json.RawMessage(`{"number":"1001"}`),
		Visitor{Verified: true, UserID: "u_42", Email: "jane@example.com", Name: "Jane", ContactID: contact, ConversationID: conv})
	if err != nil || res.IsError || !strings.HasPrefix(res.Text, "order ok") {
		t.Fatalf("call %+v %v", res, err)
	}
	meta, _ := f.meta()
	v, _ := meta["opendesk/visitor"].(map[string]any)
	w, _ := meta["opendesk/workspace"].(map[string]any)
	if v["verified"] != true || v["user_id"] != "u_42" || v["email"] != "jane@example.com" || v["name"] != "Jane" ||
		v["contact_id"] != contact.String() || v["conversation_id"] != conv.String() {
		t.Fatalf("visitor meta %v", v)
	}
	if w["id"] != e.WS.ID.String() || w["name"] != "Acme "+e.WS.Slug {
		t.Fatalf("workspace meta %v", w)
	}
	if string(f.lastArgs) != `{"number":"1001"}` {
		t.Fatalf("args %s", f.lastArgs)
	}

	// An unverified session never carries identity, even when the caller passes it.
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{UserID: "u_42", Email: "jane@example.com", Name: "Jane"}); err != nil {
		t.Fatal(err)
	}
	meta, calls := f.meta()
	v, _ = meta["opendesk/visitor"].(map[string]any)
	if v["verified"] != false || v["user_id"] != nil || v["email"] != nil || v["name"] != "Jane" || calls != 2 {
		t.Fatalf("unverified meta %v", v)
	}

	// The pooled session is reused and safe for concurrent calls.
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", json.RawMessage(`{}`), Visitor{})
			errs <- err
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	// A disabled tool is refused; output is truncated; a tool error is a result.
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "refund", nil, Visitor{}); !errors.Is(err, ErrToolDenied) {
		t.Fatalf("disabled tool: %v", err)
	}
	res, err = e.R.Call(ctx, e.WS.ID, s.ID, "big", nil, Visitor{})
	if err != nil || utf8.RuneCountInString(res.Text) != MaxOutput {
		t.Fatalf("big: %d %v", utf8.RuneCountInString(res.Text), err)
	}

	// Tool settings: verified_only hides a tool from unverified sessions.
	var upd McpServer
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{
		"tools": map[string]any{"lookup_order": map[string]any{"verified_only": true}, "refund": map[string]any{"enabled": true}},
	}).Want(t, 200).JSON(t, &upd)
	if l := toolByName(upd, "lookup_order"); !l.VerifiedOnly || !l.Enabled {
		t.Fatalf("patched %+v", l)
	}
	tools, _ = e.R.Tools(ctx, e.WS.ID, false)
	for _, tl := range tools {
		if tl.Name == "lookup_order" {
			t.Fatal("verified_only tool offered to an unverified session")
		}
	}
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); !errors.Is(err, ErrToolDenied) {
		t.Fatalf("verified_only call: %v", err)
	}
	res, err = e.R.Call(ctx, e.WS.ID, s.ID, "refund", nil, Visitor{Verified: true})
	if err != nil || !res.IsError || res.Text != "refund failed" {
		t.Fatalf("refund %+v %v", res, err)
	}
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{
		"tools": map[string]any{"nope": map[string]any{"enabled": true}}}).Want(t, 422)

	// The test endpoint runs any tool and reports the time.
	var tr struct {
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
		MS      int64  `json:"ms"`
	}
	e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+s.ID.String()+"/tools/lookup_order/test", e.admin(),
		map[string]any{"arguments": map[string]any{"number": "7"}, "visitor": map[string]any{"verified": true, "email": "a@b.co"}}).
		Want(t, 200).JSON(t, &tr)
	if tr.IsError || !strings.Contains(tr.Content, `"email":"a@b.co"`) {
		t.Fatalf("test %+v", tr)
	}

	// Sync keeps tool settings by name.
	var synced McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+s.ID.String()+"/sync", e.admin(), nil).Want(t, 200).JSON(t, &synced)
	if l := toolByName(synced, "lookup_order"); !l.VerifiedOnly || synced.Status != StatusConnected {
		t.Fatalf("synced %+v", synced)
	}

	// Delete.
	_, seq = e.Events(e.WS.ID, 0)
	e.Do(http.MethodDelete, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), nil).Want(t, 204)
	types, _ = e.Events(e.WS.ID, seq)
	if !slices.Equal(types, []string{"ai_mcp_server.deleted"}) {
		t.Fatalf("delete events %v", types)
	}
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestLegacySSEFallback(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, true)
	var s McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Legacy", "url": f.url()}).Want(t, 201).JSON(t, &s)
	if s.Status != StatusConnected || len(s.Tools) != 3 {
		t.Fatalf("legacy %+v", s)
	}
	res, err := e.R.Call(context.Background(), e.WS.ID, s.ID, "lookup_order", json.RawMessage(`{"number":"1"}`), Visitor{Verified: true, UserID: "u1"})
	if err != nil || !strings.Contains(res.Text, `"user_id":"u1"`) {
		t.Fatalf("legacy call %+v %v", res, err)
	}
}

func TestBearerAndHeaderAuth(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, false)
	f.set(func(f *fakeMCP) { f.auth, f.token = "bearer", "tok-good-123" })

	var s McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Billing", "url": f.url(),
		"auth": map[string]any{"kind": "bearer", "token": "tok-bad"}}).Want(t, 201).JSON(t, &s)
	if s.Status != StatusError || s.Error == nil || *s.Error != msgCredentials || !s.Auth.HasSecret {
		t.Fatalf("wrong token %+v", s)
	}
	resp := e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{"auth": map[string]any{"kind": "bearer", "token": "tok-good-123"}}).Want(t, 200)
	resp.JSON(t, &s)
	if s.Status != StatusConnected || len(s.Tools) != 3 {
		t.Fatalf("good token %+v", s)
	}
	if bytes.Contains(resp.Body, []byte("tok-good-123")) {
		t.Fatal("token returned")
	}
	var raw []byte
	e.Scalar(`SELECT secrets_enc FROM ai_mcp_servers WHERE id = $1`, &raw, s.ID)
	if bytes.Contains(raw, []byte("tok-good-123")) {
		t.Fatal("token stored in plain text")
	}
	if _, err := e.R.Call(context.Background(), e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); err != nil {
		t.Fatal(err)
	}
	// Keeping the token while renaming.
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{"name": "Billing two", "auth": map[string]any{"kind": "bearer"}}).Want(t, 200).JSON(t, &s)
	if s.Status != StatusConnected || s.Name != "Billing two" || s.Slug != "billing" {
		t.Fatalf("rename %+v", s)
	}

	// A saved token never follows the server to a new address: it must be entered again.
	other := newFakeMCP(t, false)
	other.set(func(f *fakeMCP) { f.auth, f.token = "bearer", "tok-good-123" })
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{"url": other.url()}).Want(t, 422)
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{"url": other.url(), "auth": map[string]any{"kind": "bearer"}}).Want(t, 422)
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{"url": other.url(),
		"auth": map[string]any{"kind": "bearer", "token": "tok-good-123"}}).Want(t, 200)
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{"url": f.url(),
		"auth": map[string]any{"kind": "bearer", "token": "tok-good-123"}}).Want(t, 200)

	// A revoked token found by a call marks the server.
	f.set(func(f *fakeMCP) { f.token = "rotated" })
	e.R.forget(s.ID)
	if _, err := e.R.Call(context.Background(), e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked token: %v", err)
	}
	if got := e.server(s.ID.String()); got.Status != StatusError || *got.Error != msgCredentials {
		t.Fatalf("after refusal %+v", got)
	}

	// A server asking for another scheme is not an OAuth server.
	basic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="shop"`)
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	t.Cleanup(basic.Close)
	var bs McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Basic", "url": basic.URL + "/mcp"}).Want(t, 201).JSON(t, &bs)
	if bs.Status != StatusError || bs.Auth.Kind != AuthNone || *bs.Error != msgCredentials {
		t.Fatalf("basic %+v", bs)
	}

	// Header auth.
	g := newFakeMCP(t, false)
	g.set(func(f *fakeMCP) { f.auth, f.token = "header", "k-999" })
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Hdr", "url": g.url(),
		"auth": map[string]any{"kind": "header", "header_name": "Host", "header_value": "x"}}).Want(t, 422)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Hdr", "url": g.url(),
		"auth": map[string]any{"kind": "header", "header_name": "Content-Type", "header_value": "x"}}).Want(t, 422)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Hdr", "url": g.url(),
		"auth": map[string]any{"kind": "header", "header_name": "X-Api-Key", "header_value": "bad\nvalue"}}).Want(t, 422)
	var h McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Hdr", "url": g.url(),
		"auth": map[string]any{"kind": "header", "header_name": "X-Api-Key", "header_value": "k-999"}}).Want(t, 201).JSON(t, &h)
	if h.Status != StatusConnected || h.Auth.HeaderName != "X-Api-Key" || !h.Auth.HasSecret {
		t.Fatalf("header %+v", h)
	}
}

func TestOAuthFlow(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, false)
	f.set(func(f *fakeMCP) { f.auth = "oauth" })

	var s McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Orders", "url": f.url()}).Want(t, 201).JSON(t, &s)
	if s.Status != StatusNeedsAuth || s.Auth.Kind != AuthOAuth || s.Auth.OAuth == nil || s.Auth.OAuth.Connected {
		t.Fatalf("protected %+v", s)
	}
	tools, _ := e.R.Tools(context.Background(), e.WS.ID, true)
	if len(tools) != 0 {
		t.Fatal("tools of a server that is not connected")
	}

	// API keys cannot start a browser sign-in; agents cannot either.
	key := e.APIKey(e.WS, auth.ScopeSettingsWrite)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+s.ID.String()+"/oauth/start", authKey(key), nil).Want(t, 403)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+s.ID.String()+"/oauth/start", e.WS.Agent.As(e.WS), nil).Want(t, 403)

	start := func() (string, *http.Cookie) {
		t.Helper()
		resp := e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+s.ID.String()+"/oauth/start", e.admin(), nil).Want(t, 200)
		var out struct {
			URL string `json:"url"`
		}
		resp.JSON(t, &out)
		c := (&http.Response{Header: resp.Header}).Cookies()
		if len(c) != 1 || c[0].Name != oauthCookie || !c[0].HttpOnly || c[0].Path != oauthCookiePath {
			t.Fatalf("cookie %v", resp.Header)
		}
		return out.URL, c[0]
	}
	u1, cookie1 := start()
	state1, code1 := f.authorize(t, u1)

	// A browser without the binding cookie is refused, and the state is spent.
	if loc := e.callback(state1, code1, nil); loc.Query().Get("error") != "oauth_state" {
		t.Fatalf("no cookie: %v", loc)
	}
	if loc := e.callback(state1, code1, cookie1); loc.Query().Get("error") != "oauth_state" {
		t.Fatalf("spent state: %v", loc)
	}

	u2, cookie2 := start()
	state2, code2 := f.authorize(t, u2)
	if f.registered != 1 {
		t.Fatalf("registered %d clients, want the registration reused", f.registered)
	}
	loc := e.callback(state2, code2, cookie2)
	if loc.Query().Get("connected") != s.ID.String() || !strings.HasPrefix(loc.String(), "http://app.test/settings/ai/tools?") {
		t.Fatalf("callback %v", loc)
	}
	if loc := e.callback(state2, code2, cookie2); loc.Query().Get("error") != "oauth_state" {
		t.Fatalf("replayed state: %v", loc)
	}
	got := e.server(s.ID.String())
	if got.Status != StatusConnected || got.Auth.OAuth == nil || !got.Auth.OAuth.Connected || got.Auth.OAuth.ExpiresAt == nil ||
		!slices.Equal(got.Auth.OAuth.Scopes, []string{"orders.read"}) || len(got.Tools) != 3 {
		t.Fatalf("connected %+v", got)
	}
	var raw []byte
	e.Scalar(`SELECT secrets_enc FROM ai_mcp_servers WHERE id = $1`, &raw, s.ID)
	if bytes.Contains(raw, []byte("at-1")) || bytes.Contains(raw, []byte("rt-1")) || bytes.Contains(raw, []byte("secret-client")) {
		t.Fatal("tokens stored in plain text")
	}
	var connected int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.mcp_server_connected'`, &connected, e.WS.ID)
	if connected != 1 {
		t.Fatalf("connected audits %d", connected)
	}
	ctx := context.Background()
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); err != nil {
		t.Fatal(err)
	}

	// An expiring token is refreshed before use (with the resource parameter).
	e.Exec(`UPDATE ai_mcp_servers SET oauth = jsonb_set(oauth, '{expires_at}', to_jsonb($2::text)) WHERE id = $1`, s.ID,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano))
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if f.refreshes != 1 {
		t.Fatalf("refreshes %d", f.refreshes)
	}
	// A token the server stops accepting is refreshed once on the 401.
	f.set(func(f *fakeMCP) { f.access = map[string]bool{} })
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); err != nil {
		t.Fatalf("after 401: %v", err)
	}
	if f.refreshes != 2 {
		t.Fatalf("refreshes %d", f.refreshes)
	}

	// A refused refresh sets needs_auth.
	f.set(func(f *fakeMCP) { f.access, f.refuseRefresh = map[string]bool{}, true })
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); !errors.Is(err, ErrNeedsAuth) {
		t.Fatalf("refused refresh: %v", err)
	}
	got = e.server(s.ID.String())
	if got.Status != StatusNeedsAuth || got.Auth.OAuth.Connected || got.Auth.HasSecret {
		t.Fatalf("needs auth %+v", got)
	}

	// Sign in again, then delete: the tokens are revoked.
	f.set(func(f *fakeMCP) { f.refuseRefresh = false })
	u3, cookie3 := start()
	state3, code3 := f.authorize(t, u3)
	if loc := e.callback(state3, code3, cookie3); loc.Query().Get("connected") == "" {
		t.Fatalf("reconnect %v", loc)
	}
	e.Do(http.MethodDelete, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), nil).Want(t, 204)
	if !slices.Contains(f.revoked, "at-1") {
		t.Fatalf("revoked %v", f.revoked)
	}
}

func authKey(key string) hdtest.Auth { return hdtest.Auth{Bearer: key} }

func (e *env) callback(state, code string, cookie *http.Cookie) *url.URL {
	e.T.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.Srv.URL+"/v1/ai/mcp-servers/oauth/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil)
	if cookie != nil {
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusFound {
		e.T.Fatalf("callback status %d", res.StatusCode)
	}
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		e.T.Fatal(err)
	}
	return loc
}

func TestSSRFRefusal(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, false)

	// Production guard: loopback, private and http targets are refused.
	prod := newRegistry(e.D, false)
	o := prod.connectAndList(context.Background(), f.url(), "", "", AuthNone)
	if o.err == nil || o.err.message != msgBlocked {
		t.Fatalf("loopback: %+v", o.err)
	}
	if _, err := prod.discover(context.Background(), f.url(), nil); err == nil {
		t.Fatal("discovery against loopback")
	}
	for _, u := range []string{"https://10.0.0.1/mcp", "https://169.254.169.254/latest", "http://example.com/mcp", "https://user:pw@example.com/mcp"} {
		if _, err := prod.guard().ValidateURL(context.Background(), u); err == nil {
			t.Fatalf("%s accepted", u)
		}
	}
	// The API refuses such URLs.
	for _, u := range []string{"https://10.0.0.1/mcp", "http://192.168.1.2/mcp", "ftp://example.com"} {
		e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Bad", "url": u}).Want(t, 422)
	}

	// A redirect to a non-loopback http or private target is refused, even in development.
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.1.2.3/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redir.Close)
	o = e.R.connectAndList(context.Background(), redir.URL+"/mcp", "", "", AuthNone)
	if o.err == nil || o.err.status != StatusError {
		t.Fatalf("redirect: %+v", o.err)
	}

	// Credentials go only to the server's own origin.
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = leaked || r.Header.Get("Authorization") != ""
		http.Error(w, "no", 404)
	}))
	t.Cleanup(other.Close)
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(hop.Close)
	_ = e.R.connectAndList(context.Background(), hop.URL+"/mcp", "Authorization", "Bearer secret", AuthBearer)
	if leaked {
		t.Fatal("credential sent to another origin")
	}

	// Oversized answers are refused.
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"x":"` + strings.Repeat("a", maxResponse+10) + `"}}`))
	}))
	t.Cleanup(huge.Close)
	o = e.R.connectAndList(context.Background(), huge.URL+"/mcp", "", "", AuthNone)
	if o.err == nil || o.err.message != msgTooLarge {
		t.Fatalf("too large: %+v", o.err)
	}
}

func TestLimits(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, false)

	// 64 enabled tools in total.
	tools := make([]cachedTool, 0, 64)
	settings := map[string]toolSetting{}
	for i := range 64 {
		n := "t" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		tools = append(tools, cachedTool{Name: n, Description: "x", InputSchema: map[string]any{"type": "object"}, ReadOnly: true})
		settings[n] = toolSetting{Enabled: true, Policy: PolicyAuto}
	}
	e.Exec(`INSERT INTO ai_mcp_servers (id, workspace_id, name, slug, url, status, tools, tool_settings) VALUES ($1, $2, 'Big', 'big', $3, 'connected', $4, $5)`,
		ids.New(), e.WS.ID, f.url(), tools, settings)
	var s McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Shop", "url": f.url()}).Want(t, 201).JSON(t, &s)
	for _, tl := range s.Tools {
		if tl.Enabled {
			t.Fatalf("default-enabled %s beyond the limit", tl.Name)
		}
	}
	resp := e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{
		"tools": map[string]any{"lookup_order": map[string]any{"enabled": true}}}).Want(t, 422)
	if !strings.Contains(string(resp.Body), "limit_reached") {
		t.Fatalf("limit body %s", resp.Body)
	}
	// Changing a policy without enabling more is fine.
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+s.ID.String(), e.admin(), map[string]any{
		"tools": map[string]any{"lookup_order": map[string]any{"policy": "confirm"}}}).Want(t, 200)

	// 10 servers.
	for i := range 8 {
		e.Exec(`INSERT INTO ai_mcp_servers (id, workspace_id, name, slug, url) VALUES ($1, $2, 'S', $3, $4)`, ids.New(), e.WS.ID,
			"s"+string(rune('a'+i)), f.url())
	}
	resp = e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Eleventh", "url": f.url()}).Want(t, 422)
	if !strings.Contains(string(resp.Body), "limit_reached") {
		t.Fatalf("server limit body %s", resp.Body)
	}
}

func TestWorkspaceIsolationAndRoles(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, false)
	var s McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Shop", "url": f.url()}).Want(t, 201).JSON(t, &s)

	other := e.NewWorkspace()
	oa := other.Admin.As(other)
	var list struct {
		Data []McpServer `json:"data"`
	}
	e.Do(http.MethodGet, "/v1/ai/mcp-servers", oa, nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Fatalf("other workspace sees %d servers", len(list.Data))
	}
	id := s.ID.String()
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+id, oa, map[string]any{"name": "Mine"}).Want(t, 404)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+id+"/sync", oa, nil).Want(t, 404)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+id+"/oauth/start", oa, nil).Want(t, 404)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers/"+id+"/tools/lookup_order/test", oa, map[string]any{"arguments": map[string]any{}}).Want(t, 404)
	e.Do(http.MethodDelete, "/v1/ai/mcp-servers/"+id, oa, nil).Want(t, 404)
	ctx := context.Background()
	if _, err := e.R.Call(ctx, other.ID, s.ID, "lookup_order", nil, Visitor{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace call: %v", err)
	}
	if tools, _ := e.R.Tools(ctx, other.ID, true); len(tools) != 0 {
		t.Fatalf("cross-workspace tools %v", tools)
	}
	if _, calls := f.meta(); calls != 0 {
		t.Fatal("the server was called across workspaces")
	}

	// Admin only; API keys need the settings scopes.
	e.Do(http.MethodGet, "/v1/ai/mcp-servers", e.WS.Agent.As(e.WS), nil).Want(t, 403)
	e.Do(http.MethodGet, "/v1/ai/mcp-servers", e.WS.Viewer.As(e.WS), nil).Want(t, 403)
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.WS.Agent.As(e.WS), map[string]any{"name": "X", "url": f.url()}).Want(t, 403)
	read := e.APIKey(e.WS, auth.ScopeSettingsRead)
	e.Do(http.MethodGet, "/v1/ai/mcp-servers", authKey(read), nil).Want(t, 200)
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+id, authKey(read), map[string]any{"enabled": false}).Want(t, 403)
	write := e.APIKey(e.WS, auth.ScopeSettingsWrite)
	var upd McpServer
	e.Do(http.MethodPatch, "/v1/ai/mcp-servers/"+id, authKey(write), map[string]any{"enabled": false}).Want(t, 200).JSON(t, &upd)
	if upd.Enabled {
		t.Fatal("not disabled")
	}
	if _, err := e.R.Call(ctx, e.WS.ID, s.ID, "lookup_order", nil, Visitor{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled server: %v", err)
	}
	if tools, _ := e.R.Tools(ctx, e.WS.ID, true); len(tools) != 0 {
		t.Fatal("tools of a disabled server")
	}
}

func TestSyncWorkerSweep(t *testing.T) {
	e := newEnv(t)
	f := newFakeMCP(t, false)
	var s McpServer
	e.Do(http.MethodPost, "/v1/ai/mcp-servers", e.admin(), map[string]any{"name": "Shop", "url": f.url()}).Want(t, 201).JSON(t, &s)
	// The server drops a tool; the hourly sync notices.
	e.Exec(`UPDATE ai_mcp_servers SET tools = tools || '[{"name":"gone","description":"","input_schema":{"type":"object"},"read_only":true,"destructive":false}]'::jsonb WHERE id = $1`, s.ID)
	_, seq := e.Events(e.WS.ID, 0)
	got, err := e.R.syncServer(context.Background(), nil, e.WS.ID, s.ID, true)
	if err != nil || len(got.Tools) != 3 || got.Status != StatusConnected {
		t.Fatalf("sync %+v %v", got, err)
	}
	types, seq := e.Events(e.WS.ID, seq)
	if !slices.Equal(types, []string{"ai_mcp_server.updated"}) {
		t.Fatalf("sync events %v", types)
	}
	// Nothing changed: the hourly sync stays quiet.
	if _, err := e.R.syncServer(context.Background(), nil, e.WS.ID, s.ID, true); err != nil {
		t.Fatal(err)
	}
	if types, _ := e.Events(e.WS.ID, seq); len(types) != 0 {
		t.Fatalf("quiet sync published %v", types)
	}
	// An unreachable server: a transient failure is retried before it is recorded.
	f.srv.Close()
	if _, err := e.R.syncServer(context.Background(), nil, e.WS.ID, s.ID, false); err == nil {
		t.Fatal("transient failure not returned for retry")
	}
	got, err = e.R.syncServer(context.Background(), nil, e.WS.ID, s.ID, true)
	if err != nil || got.Status != StatusError || *got.Error != msgUnreachable || len(got.Tools) != 3 {
		t.Fatalf("unreachable %+v %v", got, err)
	}
}

func TestUnit(t *testing.T) {
	for in, want := range map[string]string{"Shop orders": "shop_orders", "  ACME--CRM!! ": "acme_crm", "Ünïcode": "n_code", "": "mcp",
		"a very long server name that keeps going": "a_very_long_server_name"} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if _, ok := ToolName("shop", "has.dot"); ok {
		t.Error("dotted tool name accepted")
	}
	if _, ok := ToolName("shop", strings.Repeat("x", 60)); ok {
		t.Error("long tool name accepted")
	}
	got := mergeSettings(map[string]toolSetting{"a": {Enabled: true, Policy: PolicyAuto}},
		[]cachedTool{{Name: "a", ReadOnly: true}, {Name: "b", ReadOnly: true}, {Name: "c", ReadOnly: true}, {Name: "d"}}, 2)
	if !got["a"].Enabled || !got["b"].Enabled || got["c"].Enabled || got["d"].Enabled || got["d"].Policy != PolicyConfirm {
		t.Errorf("mergeSettings %v", got)
	}
	su, _ := url.Parse("https://mcp.example.com/v1/mcp")
	for res, want := range map[string]bool{"https://mcp.example.com/v1/mcp": true, "https://mcp.example.com/v1/mcp/": true,
		"https://mcp.example.com/": true, "https://mcp.example.com/v2": false, "https://evil.example.com/v1/mcp": false} {
		if resourceMatches(res, su) != want {
			t.Errorf("resourceMatches(%q) != %v", res, want)
		}
	}
	for _, h := range []string{"Connection", "host", "Cookie", "Content-Length", "Transfer-Encoding", "Bad Header", "Mcp-Session-Id"} {
		if validHeaderName(h) == nil {
			t.Errorf("header %q accepted", h)
		}
	}
	if validHeaderName("X-Api-Key") != nil || validHeaderName("Authorization") != nil {
		t.Error("valid header refused")
	}
	b := &limitedBody{rc: io.NopCloser(strings.NewReader("abcdef")), left: 6, onExceed: func() {}}
	if out, err := io.ReadAll(b); err != nil || string(out) != "abcdef" {
		t.Errorf("exact limit: %q %v", out, err)
	}
	b = &limitedBody{rc: io.NopCloser(strings.NewReader("abcdefg")), left: 6, onExceed: func() {}}
	if _, err := io.ReadAll(b); !errors.Is(err, errTooLarge) {
		t.Errorf("over limit: %v", err)
	}
	m := callMeta(&server{WorkspaceID: uuid.Nil, WorkspaceName: "W"}, Visitor{Verified: false, UserID: "u", Email: "e"})
	if v := m["opendesk/visitor"].(map[string]any); v["user_id"] != nil || v["email"] != nil {
		t.Errorf("unverified meta %v", v)
	}
}
