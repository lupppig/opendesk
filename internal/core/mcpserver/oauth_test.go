package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core/oauthgrant"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
)

const testRedirect = "https://client.example.com/callback"

// form posts application/x-www-form-urlencoded, optionally with HTTP Basic client auth.
func (e *env) form(path string, v url.Values, basic ...string) *hdtest.Resp {
	e.T.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.Srv.URL+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if len(basic) == 2 {
		req.SetBasicAuth(url.QueryEscape(basic[0]), url.QueryEscape(basic[1]))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer res.Body.Close()
	var raw json.RawMessage
	_ = json.NewDecoder(res.Body).Decode(&raw)
	return &hdtest.Resp{Status: res.StatusCode, Body: raw, Header: res.Header}
}

type registered struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Method       string `json:"token_endpoint_auth_method"`
}

func (e *env) registerClient(meta map[string]any) registered {
	e.T.Helper()
	var c registered
	e.Do("POST", "/oauth/register", hdtest.Auth{}, meta).Want(e.T, 201).JSON(e.T, &c)
	return c
}

func (e *env) publicClient() registered {
	return e.registerClient(map[string]any{"client_name": "Claude", "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "none"})
}

func pkce() (verifier, challenge string) {
	verifier = crypto.RandomToken(48)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize runs /oauth/authorize and returns the pending request id from the redirect
// to the consent page.
func (e *env) authorize(clientID, challenge, scope string) uuid.UUID {
	e.T.Helper()
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {testRedirect}, "state": {"st-123"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {"http://api.test/mcp"}}
	if scope != "" {
		q.Set("scope", scope)
	}
	res := e.Do("GET", "/oauth/authorize?"+q.Encode(), hdtest.Auth{}, nil).Want(e.T, http.StatusFound)
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(res.Header.Get("Location"), "http://app.test/oauth/consent?") {
		e.T.Fatalf("authorize redirect: %s", res.Header.Get("Location"))
	}
	id, err := uuid.Parse(loc.Query().Get("request"))
	if err != nil {
		e.T.Fatalf("request id: %s", loc)
	}
	return id
}

// approve approves as user for the workspace and returns the code (checking state and iss).
func (e *env) approve(u hdtest.User, req uuid.UUID) string {
	e.T.Helper()
	var d OAuthDecision
	e.Do("POST", "/v1/oauth/requests/"+req.String()+"/approve", u.As(e.WS), map[string]any{"workspace_id": e.WS.ID}).Want(e.T, 200).JSON(e.T, &d)
	loc, _ := url.Parse(d.RedirectTo)
	if !strings.HasPrefix(d.RedirectTo, testRedirect+"?") || loc.Query().Get("state") != "st-123" || loc.Query().Get("iss") != "http://api.test" {
		e.T.Fatalf("approve redirect: %s", d.RedirectTo)
	}
	return loc.Query().Get("code")
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (e *env) exchange(c registered, code, verifier string) (*hdtest.Resp, tokens) {
	e.T.Helper()
	v := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect},
		"code_verifier": {verifier}, "client_id": {c.ClientID}, "resource": {"http://api.test/mcp"}}
	res := e.form("/oauth/token", v)
	var t tokens
	_ = json.Unmarshal(res.Body, &t)
	return res, t
}

func (e *env) refresh(c registered, token string) (*hdtest.Resp, tokens) {
	e.T.Helper()
	res := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {c.ClientID}})
	var t tokens
	_ = json.Unmarshal(res.Body, &t)
	return res, t
}

// connectAs runs the whole flow for u and returns the client and its tokens.
func (e *env) connectAs(u hdtest.User, scope string) (registered, tokens) {
	e.T.Helper()
	c := e.publicClient()
	verifier, challenge := pkce()
	code := e.approve(u, e.authorize(c.ClientID, challenge, scope))
	res, t := e.exchange(c, code, verifier)
	res.Want(e.T, 200)
	return c, t
}

func TestOAuthMetadata(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		res := e.Do("GET", p, hdtest.Auth{Header: map[string]string{"Origin": "https://claude.ai"}}, nil).Want(t, 200)
		m := res.Map(t)
		if m["resource"] != "http://api.test/mcp" || m["authorization_servers"].([]any)[0] != "http://api.test" {
			t.Fatalf("%s: %s", p, res.Body)
		}
		if res.Header.Get("Access-Control-Allow-Origin") != "*" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s CORS: %v", p, res.Header)
		}
	}
	m := e.Do("GET", "/.well-known/oauth-authorization-server", hdtest.Auth{}, nil).Want(t, 200).Map(t)
	if m["issuer"] != "http://api.test" || m["token_endpoint"] != "http://api.test/oauth/token" ||
		m["registration_endpoint"] != "http://api.test/oauth/register" || m["authorization_endpoint"] != "http://api.test/oauth/authorize" ||
		m["revocation_endpoint"] != "http://api.test/oauth/revoke" {
		t.Fatalf("server metadata: %v", m)
	}
	if methods := m["code_challenge_methods_supported"].([]any); len(methods) != 1 || methods[0] != "S256" {
		t.Fatalf("PKCE methods: %v", methods)
	}
	// /mcp points unauthenticated clients at the resource metadata.
	res := e.post(nil)
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), `resource_metadata="http://api.test/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("challenge: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	// Token endpoint preflight from a browser client.
	req, _ := http.NewRequest(http.MethodOptions, e.Srv.URL+"/oauth/token", nil)
	req.Header.Set("Origin", "https://inspector.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pre.Body.Close()
	if pre.StatusCode != 204 || pre.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d %v", pre.StatusCode, pre.Header)
	}
}

func TestOAuthRegistration(t *testing.T) {
	e := newEnv(t)
	pub := e.publicClient()
	if pub.ClientSecret != "" || pub.Method != "none" {
		t.Fatalf("public client: %+v", pub)
	}
	// The RFC 7591 default is a confidential client with a secret, stored hashed.
	conf := e.registerClient(map[string]any{"redirect_uris": []string{"http://127.0.0.1:33418/cb", "http://localhost/cb", "http://[::1]:9000/cb"}})
	if conf.Method != "client_secret_basic" || !strings.HasPrefix(conf.ClientSecret, "od_mcps_") {
		t.Fatalf("confidential client: %+v", conf)
	}
	var stored []byte
	e.Scalar(`SELECT secret_hash FROM oauth_clients WHERE id = $1`, &stored, conf.ClientID)
	if string(stored) == conf.ClientSecret || len(stored) != 32 {
		t.Fatal("client secret not stored as a hash")
	}
	var name string
	e.Scalar(`SELECT name FROM oauth_clients WHERE id = $1`, &name, conf.ClientID)
	if name != "MCP client" {
		t.Fatalf("default name %q", name)
	}

	for _, uris := range [][]string{
		{},
		{"http://client.example.com/cb"},
		{"https://*.example.com/cb"},
		{"https://client.example.com/cb#frag"},
		{"javascript:alert(1)"},
		{"data:text/html,hi"},
		{"file:///etc/passwd"},
		{"https://user:pw@client.example.com/cb"},
		{"http://127.0.0.2.nip.io/cb"},
	} {
		res := e.Do("POST", "/oauth/register", hdtest.Auth{}, map[string]any{"redirect_uris": uris, "token_endpoint_auth_method": "none"}).Want(t, 400)
		if res.Map(t)["error"] != "invalid_redirect_uri" {
			t.Fatalf("%v: %s", uris, res.Body)
		}
	}
	// A native app's own scheme is accepted (RFC 8252 section 7.1).
	for _, uri := range []string{"cursor://anysphere.cursor-mcp/oauth/callback", "com.example.app:/oauth2redirect"} {
		e.Do("POST", "/oauth/register", hdtest.Auth{}, map[string]any{"redirect_uris": []string{uri}, "token_endpoint_auth_method": "none"}).Want(t, 201)
	}
	// Only JSON, so a cross-site form post can't register clients.
	plain, err := http.Post(e.Srv.URL+"/oauth/register", "text/plain", strings.NewReader(`{"redirect_uris":["https://client.example.com/cb"]}`))
	if err != nil {
		t.Fatal(err)
	}
	plain.Body.Close()
	if plain.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain registration: %d", plain.StatusCode)
	}
	for _, meta := range []map[string]any{
		{"redirect_uris": []string{testRedirect}, "grant_types": []string{"client_credentials"}},
		{"redirect_uris": []string{testRedirect}, "response_types": []string{"token"}},
		{"redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "private_key_jwt"},
		{"redirect_uris": []string{testRedirect}, "client_uri": "javascript:alert(1)"},
	} {
		if res := e.Do("POST", "/oauth/register", hdtest.Auth{}, meta).Want(t, 400); res.Map(t)["error"] != "invalid_client_metadata" {
			t.Fatalf("%v: %s", meta, res.Body)
		}
	}
	// A name that reads as a link is not shown to users.
	c := e.registerClient(map[string]any{"client_name": "Visit evil.example.com", "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "none"})
	e.Scalar(`SELECT name FROM oauth_clients WHERE id = $1`, &name, c.ClientID)
	if name != "MCP client" {
		t.Fatalf("link name kept: %q", name)
	}
}

func TestOAuthRegistrationRateLimited(t *testing.T) {
	e := newEnv(t)
	meta := map[string]any{"redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "none"}
	for i := 0; i < registerPerHour; i++ {
		e.Do("POST", "/oauth/register", hdtest.Auth{}, meta).Want(t, 201)
	}
	e.Do("POST", "/oauth/register", hdtest.Auth{}, meta).Want(t, 429)
}

func TestOAuthFullFlow(t *testing.T) {
	e := newEnv(t)
	tk := e.ticket("Refund for order 1042", "I was charged twice.")
	c := e.publicClient()
	verifier, challenge := pkce()
	req := e.authorize(c.ClientID, challenge, "")

	// The consent page reads the request; signing in is required, API keys can't.
	e.Do("GET", "/v1/oauth/requests/"+req.String(), hdtest.Auth{}, nil).Want(t, 401)
	e.Do("GET", "/v1/oauth/requests/"+req.String(), hdtest.Auth{Bearer: e.APIKey(e.WS, auth.AllScopes...)}, nil).Want(t, 401)
	var r OAuthRequest
	e.Do("GET", "/v1/oauth/requests/"+req.String(), e.WS.Agent.As(e.WS), nil).Want(t, 200).JSON(t, &r)
	if r.Client.Name != "Claude" || r.Client.RedirectHost != "client.example.com" || r.Client.Local || len(r.Workspaces) != 1 ||
		r.Workspaces[0].ID != e.WS.ID || r.Workspaces[0].Role != auth.RoleAgent || len(r.Scopes) != 9 || len(r.Workspaces[0].Scopes) != 9 {
		t.Fatalf("request: %+v", r)
	}
	// Only a workspace the user belongs to.
	other := e.NewWorkspace()
	e.Do("POST", "/v1/oauth/requests/"+req.String()+"/approve", e.WS.Agent.As(e.WS), map[string]any{"workspace_id": other.ID}).Want(t, 404)

	code := e.approve(e.WS.Agent, req)
	// Decided requests can't be decided again.
	e.Do("POST", "/v1/oauth/requests/"+req.String()+"/deny", e.WS.Agent.As(e.WS), nil).Want(t, 410)
	var stored []byte
	e.Scalar(`SELECT code_hash FROM oauth_authorizations WHERE id = $1`, &stored, req)
	if string(stored) == code || len(stored) != 32 {
		t.Fatal("code not stored as a hash")
	}

	res, tok := e.exchange(c, code, verifier)
	res.Want(t, 200)
	if tok.TokenType != "Bearer" || tok.ExpiresIn != 3600 || !strings.HasPrefix(tok.RefreshToken, "od_mcpr_") || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("tokens: %s", res.Body)
	}
	claims, err := e.D.Tokens.Verify(tok.AccessToken, auth.PurposeMCPAccess)
	if err != nil || claims.Subject != e.WS.Agent.ID.String() || claims.Workspace != e.WS.ID.String() || !slices.Contains(claims.Audience, "http://api.test/mcp") ||
		claims.ExpiresAt.Sub(time.Now()) > time.Hour+time.Minute || claims.Grant == "" {
		t.Fatalf("claims: %+v %v", claims, err)
	}
	// It is no session access token.
	if _, err := e.D.Tokens.Verify(tok.AccessToken, auth.PurposeAccess); err == nil {
		t.Fatal("mcp_access accepted as access")
	}
	e.Do("GET", "/v1/me", hdtest.Auth{Bearer: tok.AccessToken}, nil).Want(t, 401)

	// The token works on /mcp: the agent's tools, run as the agent.
	s := e.session(tok.AccessToken)
	names := toolNames(t, s)
	if !slices.Contains(names, "reply_to_ticket") || !slices.Contains(names, "get_report") {
		t.Fatalf("agent tools: %v", names)
	}
	var note struct {
		MessageID string `json:"message_id"`
	}
	call(t, s, "add_note", map[string]any{"ticket": tk.ID.String(), "body": "Looked into it."}, &note)
	var authorType string
	var authorUser uuid.UUID
	e.Scalar(`SELECT author_type FROM messages WHERE id = $1`, &authorType, note.MessageID)
	e.Scalar(`SELECT author_user_id FROM messages WHERE id = $1`, &authorUser, note.MessageID)
	if authorType == "api" || authorUser != e.WS.Agent.ID {
		t.Fatalf("note author %s %s", authorType, authorUser)
	}

	// The connection is listed for its owner and admins, audit-logged, and a viewer sees none.
	var list struct {
		Data []AIClientConnection `json:"data"`
	}
	e.Do("GET", "/v1/ai-clients/connections", e.WS.Agent.As(e.WS), nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 1 || !list.Data[0].Mine || list.Data[0].Client.Name != "Claude" || list.Data[0].LastUsedAt == nil {
		t.Fatalf("connections: %+v", list)
	}
	e.Do("GET", "/v1/ai-clients/connections", e.WS.Admin.As(e.WS), nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 1 || list.Data[0].Mine {
		t.Fatalf("admin connections: %+v", list)
	}
	e.Do("GET", "/v1/ai-clients/connections", e.WS.Viewer.As(e.WS), nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Fatalf("viewer connections: %+v", list)
	}
	var audits int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'oauth.grant_created' AND actor_user_id = $2`, &audits, e.WS.ID, e.WS.Agent.ID)
	if audits != 1 {
		t.Fatalf("grant_created audits: %d", audits)
	}
	var leaked int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND (data::text LIKE '%'||$2||'%' OR data::text LIKE '%'||$3||'%')`,
		&leaked, e.WS.ID, code, tok.RefreshToken)
	if leaked != 0 {
		t.Fatal("audit log carries a code or token")
	}

	// The server description lists every tool.
	var srv AIClientServer
	e.Do("GET", "/v1/ai-clients/server", e.WS.Viewer.As(e.WS), nil).Want(t, 200).JSON(t, &srv)
	if srv.URL != "http://api.test/mcp" || len(srv.Tools) != len(catalog()) || len(srv.Scopes) != len(oauthgrant.Scopes) {
		t.Fatalf("server: %+v", srv)
	}
}

func TestOAuthDeny(t *testing.T) {
	e := newEnv(t)
	c := e.publicClient()
	_, challenge := pkce()
	req := e.authorize(c.ClientID, challenge, "")
	var d OAuthDecision
	e.Do("POST", "/v1/oauth/requests/"+req.String()+"/deny", e.WS.Agent.As(e.WS), nil).Want(t, 200).JSON(t, &d)
	loc, _ := url.Parse(d.RedirectTo)
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "st-123" || loc.Query().Get("code") != "" {
		t.Fatalf("deny: %s", d.RedirectTo)
	}
}

func TestOAuthAuthorizeValidation(t *testing.T) {
	e := newEnv(t)
	c := e.publicClient()
	_, challenge := pkce()
	base := func() url.Values {
		return url.Values{"response_type": {"code"}, "client_id": {c.ClientID}, "redirect_uri": {testRedirect}, "state": {"s"},
			"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	}
	// Unknown clients and redirect URIs never redirect to the client.
	for _, mod := range []func(url.Values){
		func(v url.Values) { v.Set("client_id", uuid.NewString()) },
		func(v url.Values) { v.Set("client_id", "nope") },
		func(v url.Values) { v.Set("redirect_uri", "https://evil.example.com/cb") },
	} {
		v := base()
		mod(v)
		loc := e.Do("GET", "/oauth/authorize?"+v.Encode(), hdtest.Auth{}, nil).Want(t, 302).Header.Get("Location")
		if !strings.HasPrefix(loc, "http://app.test/oauth/consent?error=") {
			t.Fatalf("%v: %s", v, loc)
		}
	}
	// PKCE is S256 only and required; resource must be this server. Clients register
	// themselves, so these errors show on the consent page and never redirect.
	for want, mod := range map[string]func(url.Values){
		"invalid_request":           func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"invalid_request ":          func(v url.Values) { v.Del("code_challenge") },
		"invalid_request  ":         func(v url.Values) { v.Del("code_challenge_method") },
		"unsupported_response_type": func(v url.Values) { v.Set("response_type", "token") },
		"invalid_target":            func(v url.Values) { v.Set("resource", "https://other.example.com/mcp") },
	} {
		v := base()
		mod(v)
		loc, _ := url.Parse(e.Do("GET", "/oauth/authorize?"+v.Encode(), hdtest.Auth{}, nil).Want(t, 302).Header.Get("Location"))
		if !strings.HasPrefix(loc.String(), "http://app.test/oauth/consent?") || loc.Query().Get("error") != strings.TrimSpace(want) {
			t.Fatalf("%s: %s", want, loc)
		}
	}
	// A loopback redirect matches on any port.
	native := e.registerClient(map[string]any{"redirect_uris": []string{"http://127.0.0.1/callback"}, "token_endpoint_auth_method": "none"})
	v := base()
	v.Set("client_id", native.ClientID)
	v.Set("redirect_uri", "http://127.0.0.1:53682/callback")
	if loc := e.Do("GET", "/oauth/authorize?"+v.Encode(), hdtest.Auth{}, nil).Want(t, 302).Header.Get("Location"); !strings.HasPrefix(loc, "http://app.test/oauth/consent?request=") {
		t.Fatalf("loopback port: %s", loc)
	}
}

func TestOAuthWrongVerifierAndCodeReuse(t *testing.T) {
	e := newEnv(t)
	c := e.publicClient()
	verifier, challenge := pkce()
	code := e.approve(e.WS.Agent, e.authorize(c.ClientID, challenge, ""))
	other, _ := pkce()
	if res, tok := e.exchange(c, code, other); res.Status != 400 || tok.Error != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %s", res.Status, res.Body)
	}
	// The code is spent by the failed attempt.
	if res, tok := e.exchange(c, code, verifier); res.Status != 400 || tok.Error != "invalid_grant" {
		t.Fatalf("after wrong verifier: %d %s", res.Status, res.Body)
	}

	// A code used twice revokes what the first use issued.
	verifier, challenge = pkce()
	code = e.approve(e.WS.Agent, e.authorize(c.ClientID, challenge, ""))
	res, tok := e.exchange(c, code, verifier)
	res.Want(t, 200)
	if r := e.post(map[string]string{"Authorization": "Bearer " + tok.AccessToken}); r.StatusCode != 200 {
		t.Fatalf("token before reuse: %d", r.StatusCode)
	}
	if res, again := e.exchange(c, code, verifier); res.Status != 400 || again.Error != "invalid_grant" {
		t.Fatalf("reuse: %d %s", res.Status, res.Body)
	}
	if r := e.post(map[string]string{"Authorization": "Bearer " + tok.AccessToken}); r.StatusCode != 401 {
		t.Fatalf("token after code reuse: %d", r.StatusCode)
	}
	if res, _ := e.refresh(c, tok.RefreshToken); res.Status != 400 {
		t.Fatalf("refresh after code reuse: %d", res.Status)
	}

	// Another client can't exchange the code; neither can a confidential client without its secret.
	verifier, challenge = pkce()
	code = e.approve(e.WS.Agent, e.authorize(c.ClientID, challenge, ""))
	thief := e.publicClient()
	if res, tok := e.exchange(thief, code, verifier); res.Status != 400 || tok.Error != "invalid_grant" {
		t.Fatalf("other client: %d %s", res.Status, res.Body)
	}
	conf := e.registerClient(map[string]any{"redirect_uris": []string{testRedirect}})
	if res := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}, "client_id": {conf.ClientID}}); res.Status != 401 {
		t.Fatalf("missing secret: %d %s", res.Status, res.Body)
	}
	if res := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}}, conf.ClientID, "wrong"); res.Status != 401 {
		t.Fatalf("wrong secret: %d %s", res.Status, res.Body)
	}
	if res := e.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}}, conf.ClientID, conf.ClientSecret); res.Status != 400 {
		t.Fatalf("right secret, bad token: %d %s", res.Status, res.Body)
	}
}

func TestOAuthExpiredCode(t *testing.T) {
	e := newEnv(t)
	c := e.publicClient()
	verifier, challenge := pkce()
	req := e.authorize(c.ClientID, challenge, "")
	code := e.approve(e.WS.Agent, req)
	e.Exec(`UPDATE oauth_authorizations SET expires_at = now() - interval '1 second' WHERE id = $1`, req)
	if res, tok := e.exchange(c, code, verifier); res.Status != 400 || tok.Error != "invalid_grant" {
		t.Fatalf("expired code: %d %s", res.Status, res.Body)
	}
	// An expired request can't be approved either.
	_, challenge = pkce()
	req = e.authorize(c.ClientID, challenge, "")
	e.Exec(`UPDATE oauth_authorizations SET expires_at = now() - interval '1 second' WHERE id = $1`, req)
	e.Do("POST", "/v1/oauth/requests/"+req.String()+"/approve", e.WS.Agent.As(e.WS), map[string]any{"workspace_id": e.WS.ID}).Want(t, 410)
}

func TestOAuthRefreshRotation(t *testing.T) {
	e := newEnv(t)
	c, first := e.connectAs(e.WS.Agent, "")
	res, second := e.refresh(c, first.RefreshToken)
	res.Want(t, 200)
	if second.RefreshToken == first.RefreshToken || second.AccessToken == "" {
		t.Fatal("refresh token not rotated")
	}
	var stored int
	e.Scalar(`SELECT count(*) FROM oauth_refresh_tokens WHERE token_hash = $1`, &stored, []byte(second.RefreshToken))
	if stored != 0 {
		t.Fatal("refresh token stored in the clear")
	}
	if r := e.post(map[string]string{"Authorization": "Bearer " + second.AccessToken}); r.StatusCode != 200 {
		t.Fatalf("rotated access token: %d", r.StatusCode)
	}
	// Presenting the rotated token again revokes the grant: every token stops working.
	if res, tok := e.refresh(c, first.RefreshToken); res.Status != 400 || tok.Error != "invalid_grant" {
		t.Fatalf("reuse: %d %s", res.Status, res.Body)
	}
	if res, _ := e.refresh(c, second.RefreshToken); res.Status != 400 {
		t.Fatalf("newest refresh after reuse: %d", res.Status)
	}
	if r := e.post(map[string]string{"Authorization": "Bearer " + second.AccessToken}); r.StatusCode != 401 {
		t.Fatalf("access after reuse: %d", r.StatusCode)
	}
	var reason string
	e.Scalar(`SELECT data->>'reason' FROM audit_logs WHERE workspace_id = $1 AND action = 'oauth.grant_revoked'`, &reason, e.WS.ID)
	if reason != "refresh_reused" {
		t.Fatalf("revoked reason %q", reason)
	}
	// Refresh tokens belong to their client.
	c2, t2 := e.connectAs(e.WS.Agent, "")
	if res, _ := e.refresh(e.publicClient(), t2.RefreshToken); res.Status != 400 {
		t.Fatalf("other client's refresh: %d", res.Status)
	}
	if res, _ := e.refresh(c2, t2.RefreshToken); res.Status != 200 {
		t.Fatalf("own refresh: %d %s", res.Status, res.Body)
	}
}

func TestOAuthRevokedGrant(t *testing.T) {
	e := newEnv(t)
	bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

	// Disconnecting in settings.
	_, tok := e.connectAs(e.WS.Agent, "")
	var list struct {
		Data []AIClientConnection `json:"data"`
	}
	e.Do("GET", "/v1/ai-clients/connections", e.WS.Agent.As(e.WS), nil).Want(t, 200).JSON(t, &list)
	id := list.Data[0].ID.String()
	e.Do("DELETE", "/v1/ai-clients/connections/"+id, e.WS.Viewer.As(e.WS), nil).Want(t, 404)
	e.Do("DELETE", "/v1/ai-clients/connections/"+id, e.WS.Agent.As(e.WS), nil).Want(t, 204)
	if r := e.post(bearer(tok.AccessToken)); r.StatusCode != 401 {
		t.Fatalf("after disconnect: %d", r.StatusCode)
	}

	// The client revoking its refresh token (RFC 7009).
	c, tok := e.connectAs(e.WS.Agent, "")
	if res := e.form("/oauth/revoke", url.Values{"token": {tok.RefreshToken}, "client_id": {c.ClientID}}); res.Status != 200 {
		t.Fatalf("revoke: %d", res.Status)
	}
	if r := e.post(bearer(tok.AccessToken)); r.StatusCode != 401 {
		t.Fatalf("after revocation: %d", r.StatusCode)
	}
	if res := e.form("/oauth/revoke", url.Values{"token": {"unknown"}, "client_id": {c.ClientID}}); res.Status != 200 {
		t.Fatalf("revoke unknown: %d", res.Status)
	}

	// Removing the member.
	_, tok = e.connectAs(e.WS.Agent, "")
	e.Do("DELETE", "/v1/members/"+e.WS.Agent.ID.String(), e.WS.Owner.As(e.WS), nil).Want(t, 204)
	if r := e.post(bearer(tok.AccessToken)); r.StatusCode != 401 {
		t.Fatalf("after member removal: %d", r.StatusCode)
	}
	var revoked int
	e.Scalar(`SELECT count(*) FROM oauth_grants WHERE user_id = $1 AND revoked_at IS NULL`, &revoked, e.WS.Agent.ID)
	if revoked != 0 {
		t.Fatalf("active grants after removal: %d", revoked)
	}

	// A token for another resource, or past its expiry, is refused.
	_, tok = e.connectAs(e.WS.Admin, "")
	claims, _ := e.D.Tokens.Verify(tok.AccessToken, auth.PurposeMCPAccess)
	other := *claims
	other.Audience = jwt.ClaimStrings{"https://elsewhere.example.com/mcp"}
	forged, _, _ := e.D.Tokens.Sign(other, claims.Subject, time.Hour)
	if r := e.post(bearer(forged)); r.StatusCode != 401 {
		t.Fatalf("other audience: %d", r.StatusCode)
	}
	expired, _, _ := e.D.Tokens.Sign(*claims, claims.Subject, -time.Minute)
	if r := e.post(bearer(expired)); r.StatusCode != 401 {
		t.Fatalf("expired: %d", r.StatusCode)
	}
}

func TestOAuthScopesFollowRoleAndGrant(t *testing.T) {
	e := newEnv(t)
	tk := e.ticket("Login issue", "I can't sign in.")

	// A viewer gets the read tools only, whatever the client asked for.
	_, vt := e.connectAs(e.WS.Viewer, "")
	got := toolNames(t, e.session(vt.AccessToken))
	if !slices.Equal(got, []string{"get_contact", "get_report", "get_ticket", "get_wiki_page", "search_contacts", "search_knowledge", "search_tickets", "search_wiki"}) {
		t.Fatalf("viewer tools: %v", got)
	}
	if vt.Scope != "tickets:read contacts:read kb:read wiki:read settings:read" {
		t.Fatalf("viewer scope %q", vt.Scope)
	}

	// The client asked for tickets:read only: no writes even for an admin.
	_, at := e.connectAs(e.WS.Admin, "tickets:read openid")
	s := e.session(at.AccessToken)
	if got := toolNames(t, s); !slices.Equal(got, []string{"get_ticket", "search_tickets"}) {
		t.Fatalf("tickets:read tools: %v", got)
	}
	if res, err := s.CallTool(e.Ctx, &mcp.CallToolParams{Name: "add_note", Arguments: map[string]any{"ticket": tk.ID.String(), "body": "x"}}); err == nil && !res.IsError {
		t.Fatal("unoffered tool ran")
	}

	// A member demoted to viewer loses the write tools at once.
	_, gt := e.connectAs(e.WS.Agent, "")
	if got := toolNames(t, e.session(gt.AccessToken)); !slices.Contains(got, "add_note") {
		t.Fatalf("agent tools: %v", got)
	}
	e.Exec(`UPDATE members SET role = 'viewer' WHERE workspace_id = $1 AND user_id = $2`, e.WS.ID, e.WS.Agent.ID)
	if got := toolNames(t, e.session(gt.AccessToken)); slices.Contains(got, "add_note") || !slices.Contains(got, "get_ticket") {
		t.Fatalf("demoted tools: %v", got)
	}

	// A grant never reaches account or settings routes, even through the API in process.
	p := &auth.Principal{Kind: auth.KindUser, UserID: e.WS.Owner.ID, WorkspaceID: e.WS.ID, Role: auth.RoleOwner,
		GrantID: uuid.New(), Scopes: []string{auth.ScopeTicketsRead}}
	ctx := auth.WithPrincipal(context.Background(), p)
	if _, err := auth.RequireUser(ctx); err == nil {
		t.Fatal("RequireUser accepted a grant")
	}
	if _, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, ""); err == nil {
		t.Fatal("a user-only route accepted a grant")
	}
	if _, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsWrite); err == nil {
		t.Fatal("a grant passed without its scope")
	}
	if _, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead); err != nil {
		t.Fatalf("granted scope refused: %v", err)
	}
}

// collab:draft is an API key scope for now (contracts/collab.md "AI drafts"): an OAuth
// client that asks for it is not granted it and gets no Collab tools.
func TestOAuthNoCollabDraft(t *testing.T) {
	e := newEnv(t)
	_, ct := e.connectAs(e.WS.Agent, "collab:draft tickets:read")
	if got := toolNames(t, e.session(ct.AccessToken)); slices.Contains(got, "list_collab_conversations") || strings.Contains(ct.Scope, "collab") {
		t.Fatalf("collab:draft over OAuth: %q %v", ct.Scope, got)
	}
}

// redirect_uri at the token endpoint is required exactly when the authorization request
// named it (RFC 6749 section 4.1.3).
func TestOAuthRedirectURIOptional(t *testing.T) {
	e := newEnv(t)
	u := e.WS.Agent
	c := e.publicClient()
	start := func(withRedirect bool) (code, verifier string) {
		verifier, challenge := pkce()
		q := url.Values{"response_type": {"code"}, "client_id": {c.ClientID}, "state": {"st-123"},
			"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {"http://api.test/mcp"}}
		if withRedirect {
			q.Set("redirect_uri", testRedirect)
		}
		loc, _ := url.Parse(e.Do("GET", "/oauth/authorize?"+q.Encode(), hdtest.Auth{}, nil).Want(t, http.StatusFound).Header.Get("Location"))
		id, err := uuid.Parse(loc.Query().Get("request"))
		if err != nil {
			t.Fatalf("authorize: %s", loc)
		}
		return e.approve(u, id), verifier
	}
	exchange := func(code, verifier string, withRedirect bool) *hdtest.Resp {
		v := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {c.ClientID}}
		if withRedirect {
			v.Set("redirect_uri", testRedirect)
		}
		return e.form("/oauth/token", v)
	}
	if code, v := start(false); exchange(code, v, false).Status != http.StatusOK {
		t.Fatal("omitted at both ends should succeed")
	}
	if code, v := start(false); exchange(code, v, true).Status != http.StatusOK {
		t.Fatal("omitted at authorize, matching at token should succeed")
	}
	if code, v := start(true); exchange(code, v, false).Status != http.StatusBadRequest {
		t.Fatal("named at authorize, omitted at token should fail")
	}
}
