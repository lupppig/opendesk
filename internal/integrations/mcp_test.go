package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/deps"
)

// fakeMCP is a minimal MCP server: two tools, one read-only, answering as JSON or as
// an SSE stream, optionally behind a bearer token.
type fakeMCP struct {
	sse    bool
	bearer string
	icons  []any // serverInfo.icons
	mu     sync.Mutex
	calls  []string
}

func (f *fakeMCP) answer(m rpcMessage) any {
	switch m.Method {
	case "initialize":
		info := map[string]any{"name": "fake", "version": "1"}
		if f.icons != nil {
			info["icons"] = f.icons
		}
		return map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": info}
	case "tools/list":
		var p struct {
			Cursor string `json:"cursor"`
		}
		raw, _ := json.Marshal(m.Params)
		_ = json.Unmarshal(raw, &p)
		if p.Cursor == "" {
			return map[string]any{"tools": []any{map[string]any{"name": "search_issues", "description": "Search issues",
				"inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}}}, "nextCursor": "2"}
		}
		return map[string]any{"tools": []any{map[string]any{"name": "create_issue", "title": "Create issue",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}}}}}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		raw, _ := json.Marshal(m.Params)
		_ = json.Unmarshal(raw, &p)
		return map[string]any{"content": []any{map[string]any{"type": "text",
			"text": fmt.Sprintf("Created %v at https://linear.app/acme/issue/ACM-1", p.Arguments["title"])}}}
	}
	return nil
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.bearer != "" && r.Header.Get("Authorization") != "Bearer "+f.bearer {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+"http://"+r.Host+`/.well-known/oauth-protected-resource/mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var m rpcMessage
	_ = json.NewDecoder(r.Body).Decode(&m)
	f.mu.Lock()
	f.calls = append(f.calls, m.Method)
	f.mu.Unlock()
	if len(m.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if m.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", "sess-1")
	} else if r.Header.Get("Mcp-Session-Id") != "sess-1" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}
	res, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": f.answer(m)})
	if f.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, ": keepalive\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", res)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(res)
}

func exercise(t *testing.T, endpoint, bearer string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := newSession(http.DefaultClient, endpoint, bearer)
	if err := s.open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}
	tools, err := s.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "search_issues" || tools[0].Annotations.ReadOnlyHint == nil || !*tools[0].Annotations.ReadOnlyHint || tools[1].Name != "create_issue" {
		t.Fatalf("tools = %+v", tools)
	}
	res, err := s.CallTool(ctx, "create_issue", json.RawMessage(`{"title":"Login loop"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := res.Text(); !strings.Contains(got, "Created Login loop") {
		t.Fatalf("text = %q", got)
	}
}

func TestStreamableJSON(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{})
	defer srv.Close()
	exercise(t, srv.URL+"/mcp", "")
}

func TestStreamableSSE(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{sse: true})
	defer srv.Close()
	exercise(t, srv.URL+"/mcp", "")
}

func TestUnauthorizedCarriesChallenge(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{bearer: "secret"})
	defer srv.Close()
	err := newSession(http.DefaultClient, srv.URL+"/mcp", "").open(context.Background())
	ua, ok := err.(*errUnauthorized)
	if !ok || !strings.Contains(ua.challenge, "resource_metadata=") {
		t.Fatalf("err = %v", err)
	}
	exercise(t, srv.URL+"/mcp", "secret")
}

// legacyMCP speaks the 2024-11-05 HTTP+SSE transport: GET opens the stream, which
// names the POST endpoint; answers arrive on the stream.
type legacyMCP struct {
	fakeMCP
	mu      sync.Mutex
	streams map[string]chan []byte
}

func (l *legacyMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/sse":
		ch := make(chan []byte, 8)
		id := fmt.Sprintf("%d", time.Now().UnixNano())
		l.mu.Lock()
		l.streams[id] = ch
		l.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: endpoint\ndata: /messages?session=%s\n\n", id)
		w.(http.Flusher).Flush()
		for {
			select {
			case msg := <-ch:
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	case r.Method == http.MethodPost && r.URL.Path == "/messages":
		l.mu.Lock()
		ch := l.streams[r.URL.Query().Get("session")]
		l.mu.Unlock()
		var m rpcMessage
		_ = json.NewDecoder(r.Body).Decode(&m)
		w.WriteHeader(http.StatusAccepted)
		if ch != nil && len(m.ID) > 0 {
			res, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": l.answer(m)})
			ch <- res
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestLegacySSE(t *testing.T) {
	srv := httptest.NewServer(&legacyMCP{streams: map[string]chan []byte{}})
	defer srv.Close()
	exercise(t, srv.URL+"/sse", "")
}

func TestReadSSEResponseSkipsOtherMessages(t *testing.T) {
	stream := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"sampling/createMessage\"}\n\n" +
		"data: {\"jsonrpc\":\"2.0\",\"id\":7,\n" + "data: \"result\":{\"ok\":true}}\n\n"
	m, err := readSSEResponse(strings.NewReader(stream), json.RawMessage("7"))
	if err != nil || string(m.Result) != `{"ok":true}` {
		t.Fatalf("m = %+v, err = %v", m, err)
	}
}

func TestToolName(t *testing.T) {
	if got := toolName("linear", "create_issue"); got != "linear__create_issue" {
		t.Fatalf("got %q", got)
	}
	long := toolName("a-very-long-workspace-integration-slug", strings.Repeat("create_something_", 6))
	if len(long) > 64 || !IsTool(long) {
		t.Fatalf("long = %q (%d)", long, len(long))
	}
	if toolName("x", "do it/now") == toolName("x", "do_it_now2") {
		t.Fatal("names collide")
	}
	if IsTool("search_tickets") {
		t.Fatal("built-in tool read as connected")
	}
}

func TestSameOrigin(t *testing.T) {
	base := "https://mcp.example.com/sse"
	if _, err := sameOrigin(base, "/messages?s=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := sameOrigin(base, "https://evil.example.net/messages"); err == nil {
		t.Fatal("other host accepted")
	}
}

// --- OAuth ------------------------------------------------------------------------

func testService(t *testing.T) *Service {
	t.Helper()
	d := &deps.Deps{Cfg: &config.Config{APIURL: "http://api.test", AppURL: "http://app.test"}}
	return newService(d, http.DefaultClient, true)
}

func TestOAuthDiscoveryRegistrationAndTokens(t *testing.T) {
	var origin string
	var registered, exchanged, refreshed bool
	mcp := &fakeMCP{bearer: "at-2"}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp)
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin + "/auth"},
			"scopes_supported": []string{"read", "write"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": origin + "/auth", "authorization_endpoint": origin + "/auth/authorize",
			"token_endpoint": origin + "/auth/token", "registration_endpoint": origin + "/auth/register",
			"code_challenge_methods_supported": []string{"S256"}})
	})
	mux.HandleFunc("/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if uris, _ := body["redirect_uris"].([]any); len(uris) != 1 || uris[0] != "http://api.test/v1/integrations/oauth/callback" {
			http.Error(w, "bad redirect", 400)
			return
		}
		registered = true
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "dyn-1"})
	})
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "dyn-1" || r.Form.Get("resource") != origin+"/mcp" {
			http.Error(w, `{"error":"invalid_client"}`, 401)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "code-1" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != "challenge-ok" &&
				r.Form.Get("code_verifier") == "" {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			exchanged = true
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "rt-1" {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			refreshed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2", "expires_in": 3600})
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	origin = srv.URL
	s := testService(t)
	ctx := context.Background()

	asm, prm, scope, err := s.discover(ctx, origin+"/mcp")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if asm.TokenEndpoint != origin+"/auth/token" || scope != "read write" {
		t.Fatalf("asm = %+v scope = %q", asm, scope)
	}
	c, err := s.identify(ctx, "custom", asm)
	if err != nil || !registered || c.ClientID != "dyn-1" || c.AuthMethod != "none" {
		t.Fatalf("identify: %+v %v", c, err)
	}
	c.Resource, c.Scope = canonicalResource(origin+"/mcp", prm), scope
	au, _ := url.Parse(c.authorizeURL("st", "verifier-verifier-verifier-verifier-verifier"))
	q := au.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("resource") != origin+"/mcp" || q.Get("state") != "st" || q.Get("scope") != "read write" {
		t.Fatalf("authorize url = %s", au)
	}
	tk, err := s.exchange(ctx, c, "code-1", "verifier-verifier-verifier-verifier-verifier")
	if err != nil || !exchanged || tk.AccessToken != "at-1" || tk.RefreshToken != "rt-1" || tk.ExpiresAt.IsZero() {
		t.Fatalf("exchange: %+v %v", tk, err)
	}
	tk, err = s.refresh(ctx, c, "rt-1")
	if err != nil || !refreshed || tk.AccessToken != "at-2" || tk.RefreshToken != "rt-1" {
		t.Fatalf("refresh: %+v %v", tk, err)
	}
	if _, err := s.refresh(ctx, c, "revoked"); err != errReauthorize {
		t.Fatalf("revoked refresh = %v", err)
	}
	exercise(t, origin+"/mcp", tk.AccessToken)
}

func TestDiscoverOpenServer(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{})
	defer srv.Close()
	if _, _, _, err := testService(t).discover(context.Background(), srv.URL+"/mcp"); err != errNoAuthNeeded {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverRefusesNonPKCE(t *testing.T) {
	var origin string
	mux := http.NewServeMux()
	mux.Handle("/mcp", &fakeMCP{bearer: "x"})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{origin}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": origin, "authorization_endpoint": origin + "/a", "token_endpoint": origin + "/t"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	origin = srv.URL
	if _, _, _, err := testService(t).discover(context.Background(), origin+"/mcp"); err == nil || !strings.Contains(err.Error(), "PKCE") {
		t.Fatalf("err = %v", err)
	}
}

func TestProductionRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(&fakeMCP{})
	defer srv.Close()
	d := &deps.Deps{Cfg: &config.Config{APIURL: "https://api.test", AppURL: "https://app.test"}}
	s := newService(d, guardedClient(false), false)
	if _, err := s.guard.ValidateURL(context.Background(), srv.URL+"/mcp"); err == nil {
		t.Fatal("loopback accepted")
	}
	if err := newSession(s.http, srv.URL+"/mcp", "").open(context.Background()); err == nil {
		t.Fatal("guarded client reached loopback")
	}
}

func TestStoredToolsSanitizes(t *testing.T) {
	in := []RemoteTool{
		{Name: "a", Description: strings.Repeat("x", 5000), InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "a"},
		{Name: ""},
		{Name: "big", InputSchema: json.RawMessage(`{"d":"` + strings.Repeat("y", 40<<10) + `"}`)},
	}
	out := storedTools(in)
	// Duplicates, nameless tools and oversized schemas are dropped, long text is clipped.
	if len(out) != 1 || out[0].Name != "a" || len([]rune(out[0].Description)) > 1100 {
		t.Fatalf("out = %d tools", len(out))
	}
}

func TestAskFirstMakesAReadAWrite(t *testing.T) {
	x := &row{Tools: []StoredTool{{Name: "search", ReadOnly: true}, {Name: "fetch", ReadOnly: true}, {Name: "create"}},
		AskTools: []string{"search"}}
	for name, want := range map[string]bool{"search": false, "fetch": true, "create": false} {
		i := slices.IndexFunc(x.Tools, func(t StoredTool) bool { return t.Name == name })
		if got := x.readOnly(x.Tools[i]); got != want {
			t.Errorf("readOnly(%s) = %v, want %v", name, got, want)
		}
	}
	for _, tl := range x.api().Tools {
		switch tl.Name {
		case "search":
			if tl.ReadOnly || !tl.ReadOnlyHint || !tl.AskFirst {
				t.Errorf("search = %+v", tl)
			}
		case "fetch":
			if !tl.ReadOnly || !tl.ReadOnlyHint || tl.AskFirst {
				t.Errorf("fetch = %+v", tl)
			}
		case "create":
			if tl.ReadOnly || tl.ReadOnlyHint || tl.AskFirst {
				t.Errorf("create = %+v", tl)
			}
		}
	}
	if got := x.knownTools([]string{"fetch", "nope", "fetch", "create"}); !slices.Equal(got, []string{"fetch", "create"}) {
		t.Fatalf("knownTools = %v", got)
	}
}

func TestAutoToolsRunOnlyWritesAndAskFirstWins(t *testing.T) {
	x := &row{Tools: []StoredTool{{Name: "fetch", ReadOnly: true}, {Name: "search", ReadOnly: true}, {Name: "create"}, {Name: "close"}, {Name: "post"}},
		AskTools: []string{"search", "close"}, AutoTools: []string{"fetch", "search", "create", "close"}}
	// fetch: a lookup already; search: asks first; create: auto; close: on both lists, ask first wins; post: not listed.
	for name, want := range map[string]bool{"fetch": false, "search": false, "create": true, "close": false, "post": false} {
		i := slices.IndexFunc(x.Tools, func(t StoredTool) bool { return t.Name == name })
		if got := x.autoRun(x.Tools[i]); got != want {
			t.Errorf("autoRun(%s) = %v, want %v", name, got, want)
		}
	}
	for _, tl := range x.api().Tools {
		if tl.AutoRun != (tl.Name == "create") {
			t.Errorf("api %s auto_run = %v", tl.Name, tl.AutoRun)
		}
	}
}

func TestLinkProvider(t *testing.T) {
	cases := map[string]string{
		"https://linear.app/acme/issue/ACM-1":        "linear",
		"https://github.com/acme/app/issues/3":       "github",
		"https://acme.atlassian.net/browse/OPS-1":    "atlassian",
		"https://example.com/thing":                  "link",
		"https://notlinear.app.evil.example/issue/1": "link",
	}
	for in, want := range cases {
		if got := providerFor(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestRegisteredAppSignIn(t *testing.T) {
	gh, _ := provider("github")
	if gh.Auth != "token" || gh.TokenURL == "" {
		t.Fatalf("github without an app = %+v", gh)
	}
	t.Setenv("MCP_GITHUB_CLIENT_ID", "Iv1.app")
	t.Setenv("MCP_GITHUB_CLIENT_SECRET", "secret")
	gh, _ = provider("github")
	if gh.Auth != "oauth" || gh.TokenHelp != "" || gh.TokenURL != "" {
		t.Fatalf("github with an app = %+v", gh)
	}
	for _, p := range catalog() {
		if p.Key == "github" && p.Auth != "oauth" {
			t.Fatalf("catalog github = %+v", p)
		}
	}
	if st, _ := provider("stripe"); st.Auth != "oauth" {
		t.Fatalf("stripe = %+v", st)
	}

	s := testService(t)
	asm := &serverMetadata{Issuer: "https://github.com/login/oauth", AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint: "https://github.com/login/oauth/access_token", CodeChallengeMethods: []string{"S256"}}
	c, err := s.identify(context.Background(), "github", asm)
	if err != nil || c.ClientID != "Iv1.app" || c.RegisteredVia != "preregistered" || c.AuthMethod != "client_secret_post" {
		t.Fatalf("identify github: %+v %v", c, err)
	}
	t.Setenv("MCP_SLACK_CLIENT_ID", "slack-app")
	t.Setenv("MCP_SLACK_CLIENT_SECRET", "secret")
	if c, err := s.identify(context.Background(), "slack", asm); err != nil || c.AuthMethod != "client_secret_basic" {
		t.Fatalf("identify slack: %+v %v", c, err)
	}
}

// TestOAuthIssuerMustMatch: an authorization server's metadata counts only when it names the
// issuer it was fetched for.
func TestOAuthIssuerMustMatch(t *testing.T) {
	var origin, named string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin + "/auth"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": named, "authorization_endpoint": origin + "/auth/authorize",
			"token_endpoint": origin + "/auth/token", "code_challenge_methods_supported": []string{"S256"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	origin = srv.URL
	s := testService(t)
	for _, c := range []struct {
		named string
		ok    bool
	}{{origin + "/auth", true}, {origin + "/auth/", true}, {"https://other.example/auth", false}, {"", false}} {
		named = c.named
		if _, _, _, err := s.discover(context.Background(), origin+"/mcp"); (err == nil) != c.ok {
			t.Fatalf("issuer %q: err %v", c.named, err)
		}
	}
}
