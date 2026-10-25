package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
)

func TestMain(m *testing.M) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		os.Setenv("TEST_DATABASE_URL", "postgres://opendesk:opendesk@localhost:55432/opendesk_mcp?sslmode=disable")
	}
	hdtest.Main(m)
}

// requireDB skips a test when the test database is not reachable.
func requireDB(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Skipf("test database unavailable (TEST_DATABASE_URL): %v", err)
	}
	_ = c.Close(ctx)
}

type env struct {
	*hdtest.Env
	R  *Registry
	WS hdtest.WS
}

func newEnv(t *testing.T) *env {
	t.Helper()
	requireDB(t)
	e := hdtest.New(t, Register)
	r := For(e.D)
	ev := &env{Env: e, R: r}
	ev.WS = e.NewWorkspace()
	return ev
}

func (e *env) admin() hdtest.Auth { return e.WS.Admin.As(e.WS) }

// fakeMCP is an in-process MCP server with a fake OAuth authorization server on the
// same origin.
type fakeMCP struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	lastMeta map[string]any
	lastArgs json.RawMessage
	calls    int

	// auth: "" open, "bearer" (token), "header" (X-Api-Key = token), "oauth".
	auth  string
	token string

	// OAuth state.
	clients       map[string]string // client_id -> secret
	codes         map[string]codeGrant
	access        map[string]bool
	refresh       map[string]bool
	refuseRefresh bool // refuse refresh grants
	refreshes     int
	revoked       []string
	registered    int
	prmHits       int
}

type codeGrant struct {
	challenge, redirect, resource, client string
}

func newFakeMCP(t *testing.T, legacy bool) *fakeMCP {
	t.Helper()
	f := &fakeMCP{t: t, clients: map[string]string{}, codes: map[string]codeGrant{}, access: map[string]bool{}, refresh: map[string]bool{}}
	server := sdk.NewServer(&sdk.Implementation{Name: "test-shop", Version: "1.2.3"}, nil)
	server.AddTool(&sdk.Tool{Name: "lookup_order", Title: "Look up an order", Description: "Finds an order by number.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"number": map[string]any{"type": "string"}}},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			f.mu.Lock()
			f.lastMeta = map[string]any(req.Params.Meta)
			f.lastArgs = req.Params.Arguments
			f.calls++
			f.mu.Unlock()
			meta, _ := json.Marshal(req.Params.Meta)
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "order ok " + string(meta)}}}, nil
		})
	server.AddTool(&sdk.Tool{Name: "refund", Description: "Refunds an order.", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "refund failed"}}, IsError: true}, nil
		})
	server.AddTool(&sdk.Tool{Name: "big", Description: "Answers a lot.", InputSchema: map[string]any{"type": "object"},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("é", 9000)}}}, nil
		})
	var mcpHandler http.Handler
	if legacy {
		mcpHandler = sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return server }, nil)
	} else {
		mcpHandler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", f.guard(mcpHandler))
	mux.Handle("/mcp/", f.guard(mcpHandler))
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.prmHits++
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"resource": f.srv.URL + "/mcp", "authorization_servers": []string{f.srv.URL},
			"scopes_supported": []string{"orders.read"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "registration_endpoint": f.srv.URL + "/register",
			"revocation_endpoint": f.srv.URL + "/revoke", "code_challenge_methods_supported": []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_basic"}, "response_types_supported": []string{"code"}})
	})
	mux.HandleFunc("/register", f.register)
	mux.HandleFunc("/token", f.tokenEndpoint)
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
		w.WriteHeader(200)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		// Pooled sessions keep event streams open.
		f.srv.CloseClientConnections()
		f.srv.Close()
	})
	return f
}

func (f *fakeMCP) url() string { return f.srv.URL + "/mcp" }

func (f *fakeMCP) set(fn func(f *fakeMCP)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeMCP) meta() (map[string]any, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastMeta, f.calls
}

func (f *fakeMCP) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		mode, token := f.auth, f.token
		ok := true
		switch mode {
		case "bearer":
			ok = r.Header.Get("Authorization") == "Bearer "+token
		case "header":
			ok = r.Header.Get("X-Api-Key") == token
		case "oauth":
			ok = strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		}
		f.mu.Unlock()
		if !ok {
			if mode == "oauth" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource/mcp", scope="orders.read"`)
			}
			http.Error(w, "unauthorized: secret server details", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (f *fakeMCP) register(w http.ResponseWriter, r *http.Request) {
	var meta map[string]any
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		http.Error(w, "bad", 400)
		return
	}
	f.mu.Lock()
	f.registered++
	id := "client-" + string(rune('a'+f.registered))
	f.clients[id] = "secret-" + id
	f.mu.Unlock()
	writeJSON(w, 201, map[string]any{"client_id": id, "client_secret": "secret-" + id, "token_endpoint_auth_method": "client_secret_basic",
		"redirect_uris": meta["redirect_uris"]})
}

// authorize plays the browser and the authorization server's consent page: it checks
// the authorization URL and returns a code for it.
func (f *fakeMCP) authorize(t *testing.T, authURL string) (state, code string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, f.srv.URL+"/authorize?") {
		t.Fatalf("authorization URL %q", authURL)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("resource") != f.url() || q.Get("redirect_uri") != "http://api.test/v1/ai/mcp-servers/oauth/callback" ||
		q.Get("state") == "" || q.Get("scope") != "orders.read" {
		t.Fatalf("authorization query %v", q)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.clients[q.Get("client_id")]; !ok {
		t.Fatalf("unknown client %q", q.Get("client_id"))
	}
	code = "code-" + q.Get("state")[:8]
	f.codes[code] = codeGrant{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), resource: q.Get("resource"), client: q.Get("client_id")}
	return q.Get("state"), code
}

func (f *fakeMCP) tokenEndpoint(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, secret, ok := r.BasicAuth()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ok || f.clients[id] != secret {
		writeJSON(w, 401, map[string]any{"error": "invalid_client"})
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		g, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || g.client != id || g.redirect != r.PostForm.Get("redirect_uri") || g.resource != r.PostForm.Get("resource") ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
			return
		}
		f.access["at-1"], f.refresh["rt-1"] = true, true
		writeJSON(w, 200, map[string]any{"access_token": "at-1", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "rt-1",
			"scope": "orders.read"})
	case "refresh_token":
		f.refreshes++
		rt := r.PostForm.Get("refresh_token")
		if f.refuseRefresh || !f.refresh[rt] || r.PostForm.Get("resource") != f.url() {
			writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
			return
		}
		delete(f.refresh, rt)
		n := f.refreshes + 1
		at, nrt := "at-"+strconv.Itoa(n), "rt-"+strconv.Itoa(n)
		f.access[at], f.refresh[nrt] = true, true
		writeJSON(w, 200, map[string]any{"access_token": at, "token_type": "bearer", "expires_in": 3600, "refresh_token": nrt})
	default:
		writeJSON(w, 400, map[string]any{"error": "unsupported_grant_type"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// serverByID reads a server back from the API list.
func (e *env) server(id string) McpServer {
	e.T.Helper()
	var out struct {
		Data []McpServer `json:"data"`
	}
	e.Do(http.MethodGet, "/v1/ai/mcp-servers", e.admin(), nil).Want(e.T, 200).JSON(e.T, &out)
	for _, s := range out.Data {
		if s.ID.String() == id {
			return s
		}
	}
	e.T.Fatalf("server %s not listed", id)
	return McpServer{}
}

func toolByName(s McpServer, name string) McpTool {
	for _, t := range s.Tools {
		if t.Name == name {
			return t
		}
	}
	return McpTool{}
}
