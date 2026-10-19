package integrations

// OAuth for MCP servers (MCP authorization, 2025-11-25): find the authorization server
// through the server's protected resource metadata (RFC 9728) and the authorization
// server's metadata (RFC 8414), identify opendesk to it (a pre-registered client, a
// client ID metadata document, or dynamic registration, RFC 7591), and sign in with
// the authorization code flow and PKCE S256, naming the MCP server as the resource
// (RFC 8707). Every metadata and token request uses the guarded client.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// oauthClient is how one connection signs in; stored sealed (oauth_enc).
type oauthClient struct {
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret,omitempty"`
	AuthMethod    string `json:"auth_method,omitempty"` // none | client_secret_post | client_secret_basic
	Issuer        string `json:"issuer"`
	AuthorizeURL  string `json:"authorize_url"`
	TokenURL      string `json:"token_url"`
	Resource      string `json:"resource"`
	Scope         string `json:"scope,omitempty"`
	RedirectURI   string `json:"redirect_uri"`
	RegisteredVia string `json:"registered_via"` // preregistered | metadata_document | dynamic
}

// tokens are stored sealed (credentials_enc).
type tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	// Token is a pasted access token (auth "token").
	Token string `json:"token,omitempty"`
}

func (t tokens) bearer() string {
	if t.Token != "" {
		return t.Token
	}
	return t.AccessToken
}

// fresh reports whether the access token is good for at least two more minutes.
func (t tokens) fresh(now time.Time) bool {
	if t.Token != "" {
		return true
	}
	return t.AccessToken != "" && (t.ExpiresAt.IsZero() || now.Add(2*time.Minute).Before(t.ExpiresAt))
}

type resourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type serverMetadata struct {
	Issuer                       string   `json:"issuer"`
	AuthorizationEndpoint        string   `json:"authorization_endpoint"`
	TokenEndpoint                string   `json:"token_endpoint"`
	RegistrationEndpoint         string   `json:"registration_endpoint"`
	CodeChallengeMethods         []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethods     []string `json:"token_endpoint_auth_methods_supported"`
	ClientIDMetadataDocSupported bool     `json:"client_id_metadata_document_supported"`
}

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

// parseChallenge reads resource_metadata and scope from a Bearer WWW-Authenticate.
func parseChallenge(h string) (metadataURL, scope string) {
	for _, m := range challengeParam.FindAllStringSubmatch(h, -1) {
		switch strings.ToLower(m[1]) {
		case "resource_metadata":
			metadataURL = m[2]
		case "scope":
			scope = m[2]
		}
	}
	return metadataURL, scope
}

// discover finds the authorization server for an MCP server URL.
func (s *Service) discover(ctx context.Context, server string) (*serverMetadata, *resourceMetadata, string, error) {
	sess := newSession(s.http, server, "")
	challenge := ""
	if err := sess.initialize(ctx); err != nil {
		var ua *errUnauthorized
		if errors.As(err, &ua) {
			challenge = ua.challenge
		} else if !errors.Is(err, errTransport) {
			return nil, nil, "", err
		}
	} else {
		return nil, nil, "", errNoAuthNeeded
	}
	metaURL, scope := parseChallenge(challenge)
	u, err := url.Parse(server)
	if err != nil {
		return nil, nil, "", err
	}
	origin := u.Scheme + "://" + u.Host
	var candidates []string
	if metaURL != "" {
		candidates = append(candidates, metaURL)
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" {
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+p)
	}
	candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")
	var prm *resourceMetadata
	for _, c := range candidates {
		var m resourceMetadata
		if err := s.getJSON(ctx, c, &m); err == nil && len(m.AuthorizationServers) > 0 {
			prm = &m
			break
		}
	}
	issuer := origin // servers from before protected resource metadata host their own
	if prm != nil {
		issuer = strings.TrimRight(prm.AuthorizationServers[0], "/")
		if scope == "" {
			scope = strings.Join(prm.ScopesSupported, " ")
		}
	}
	asm, err := s.serverMetadata(ctx, issuer)
	if err != nil {
		if prm != nil {
			return nil, nil, "", err
		}
		// Oldest servers: default endpoints on the server's origin.
		asm = &serverMetadata{Issuer: origin, AuthorizationEndpoint: origin + "/authorize", TokenEndpoint: origin + "/token",
			RegistrationEndpoint: origin + "/register", CodeChallengeMethods: []string{"S256"}}
	}
	if !slices.Contains(asm.CodeChallengeMethods, "S256") {
		return nil, nil, "", errors.New("the server's sign-in does not support PKCE, so it cannot be connected safely")
	}
	for _, e := range []string{asm.AuthorizationEndpoint, asm.TokenEndpoint} {
		if err := s.checkEndpoint(ctx, e); err != nil {
			return nil, nil, "", err
		}
	}
	return asm, prm, scope, nil
}

var errNoAuthNeeded = errors.New("this server does not ask for a sign-in")

// serverMetadata tries the RFC 8414 and OpenID locations for an issuer, path-aware.
func (s *Service) serverMetadata(ctx context.Context, issuer string) (*serverMetadata, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return nil, errors.New("the server names an authorization server we could not read")
	}
	origin := u.Scheme + "://" + u.Host
	p := strings.TrimRight(u.Path, "/")
	var tries []string
	if p != "" {
		tries = []string{origin + "/.well-known/oauth-authorization-server" + p, origin + "/.well-known/openid-configuration" + p,
			origin + p + "/.well-known/openid-configuration"}
	} else {
		tries = []string{origin + "/.well-known/oauth-authorization-server", origin + "/.well-known/openid-configuration"}
	}
	for _, t := range tries {
		var m serverMetadata
		if err := s.getJSON(ctx, t, &m); err == nil && m.AuthorizationEndpoint != "" && m.TokenEndpoint != "" {
			return &m, nil
		}
	}
	return nil, errors.New("the server's authorization server publishes no metadata")
}

// checkEndpoint applies the outbound URL rules to an endpoint a server named.
func (s *Service) checkEndpoint(ctx context.Context, raw string) error {
	if _, err := s.guard.ValidateURL(ctx, raw); err != nil {
		return errors.New("the server names a sign-in address that is not allowed: " + err.Error())
	}
	return nil
}

func (s *Service) getJSON(ctx context.Context, target string, v any) error {
	if _, err := s.guard.ValidateURL(ctx, target); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	resp, err := s.http.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// redirectURI is where every authorization server sends the browser back.
func (s *Service) redirectURI() string {
	return strings.TrimRight(s.d.Cfg.APIURL, "/") + "/v1/integrations/oauth/callback"
}

// clientMetadataURL is opendesk's client ID metadata document, served by this API.
func (s *Service) clientMetadataURL() string {
	return strings.TrimRight(s.d.Cfg.APIURL, "/") + "/v1/integrations/oauth/client"
}

// clientMetadata is the document at clientMetadataURL.
func (s *Service) clientMetadata() map[string]any {
	return map[string]any{
		"client_id":                  s.clientMetadataURL(),
		"client_name":                "opendesk",
		"client_uri":                 strings.TrimRight(s.d.Cfg.AppURL, "/"),
		"redirect_uris":              []string{s.redirectURI()},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
}

// identify picks how opendesk introduces itself to the authorization server: an
// operator's pre-registered client, the metadata document (only on https), or a
// dynamic registration.
func (s *Service) identify(ctx context.Context, providerKey string, asm *serverMetadata) (*oauthClient, error) {
	c := &oauthClient{Issuer: asm.Issuer, AuthorizeURL: asm.AuthorizationEndpoint, TokenURL: asm.TokenEndpoint, RedirectURI: s.redirectURI()}
	if id, secret := preRegistered(providerKey); id != "" {
		c.ClientID, c.ClientSecret, c.RegisteredVia = id, secret, "preregistered"
		c.AuthMethod = tokenAuthMethod(asm, secret != "")
		if p, ok := provider(providerKey); ok && p.SecretInForm && secret != "" && len(asm.TokenEndpointAuthMethods) == 0 {
			c.AuthMethod = "client_secret_post"
		}
		return c, nil
	}
	if asm.ClientIDMetadataDocSupported && strings.HasPrefix(s.clientMetadataURL(), "https://") {
		c.ClientID, c.AuthMethod, c.RegisteredVia = s.clientMetadataURL(), "none", "metadata_document"
		return c, nil
	}
	if asm.RegistrationEndpoint == "" {
		return nil, errors.New("this server only accepts registered apps. Ask the operator to set MCP_" + strings.ToUpper(providerKey) + "_CLIENT_ID")
	}
	if err := s.checkEndpoint(ctx, asm.RegistrationEndpoint); err != nil {
		return nil, err
	}
	body := map[string]any{
		"client_name": "opendesk", "client_uri": strings.TrimRight(s.d.Cfg.AppURL, "/"),
		"redirect_uris": []string{s.redirectURI()}, "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "token_endpoint_auth_method": "none",
	}
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, asm.RegistrationEndpoint, strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()
	var reg struct {
		ClientID                string `json:"client_id"`
		ClientSecret            string `json:"client_secret"`
		TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
	}
	if resp.StatusCode >= 300 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reg) != nil || reg.ClientID == "" {
		return nil, fmt.Errorf("the server refused to register opendesk (%d)", resp.StatusCode)
	}
	c.ClientID, c.ClientSecret, c.RegisteredVia = reg.ClientID, reg.ClientSecret, "dynamic"
	c.AuthMethod = reg.TokenEndpointAuthMethod
	if c.AuthMethod == "" {
		c.AuthMethod = tokenAuthMethod(asm, reg.ClientSecret != "")
	}
	return c, nil
}

func tokenAuthMethod(asm *serverMetadata, hasSecret bool) string {
	if !hasSecret {
		return "none"
	}
	if slices.Contains(asm.TokenEndpointAuthMethods, "client_secret_post") && !slices.Contains(asm.TokenEndpointAuthMethods, "client_secret_basic") {
		return "client_secret_post"
	}
	return "client_secret_basic"
}

// authorizeURL is the browser address that starts the sign-in.
func (c *oauthClient) authorizeURL(state, verifier string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURI},
		"state":                 {state},
		"code_challenge":        {oauth2.S256ChallengeFromVerifier(verifier)},
		"code_challenge_method": {"S256"},
		"resource":              {c.Resource},
	}
	if c.Scope != "" {
		q.Set("scope", c.Scope)
	}
	sep := "?"
	if strings.Contains(c.AuthorizeURL, "?") {
		sep = "&"
	}
	return c.AuthorizeURL + sep + q.Encode()
}

type tokenReply struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

// errReauthorize means the grant is gone; an admin must sign in again.
var errReauthorize = errors.New("the sign-in expired or was revoked. Reconnect the integration")

func (s *Service) tokenRequest(ctx context.Context, c *oauthClient, form url.Values) (*tokens, error) {
	form.Set("resource", c.Resource)
	if c.AuthMethod != "client_secret_basic" {
		form.Set("client_id", c.ClientID)
	}
	if c.AuthMethod == "client_secret_post" {
		form.Set("client_secret", c.ClientSecret)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.AuthMethod == "client_secret_basic" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(c.ClientID)+":"+url.QueryEscape(c.ClientSecret))))
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()
	var tr tokenReply
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr)
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("the sign-in service is not responding. Try again in a moment")
	case tr.Error == "invalid_grant" || tr.Error == "bad_refresh_token":
		return nil, errReauthorize
	case resp.StatusCode >= 400 || tr.Error != "":
		return nil, fmt.Errorf("the sign-in service refused the request (%s)", safeCode(tr.Error))
	case tr.AccessToken == "":
		return nil, errors.New("the sign-in service returned no access token")
	}
	t := &tokens{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken}
	if tr.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return t, nil
}

func (s *Service) exchange(ctx context.Context, c *oauthClient, code, verifier string) (*tokens, error) {
	return s.tokenRequest(ctx, c, url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {c.RedirectURI}, "code_verifier": {verifier}})
}

func (s *Service) refresh(ctx context.Context, c *oauthClient, refreshToken string) (*tokens, error) {
	t, err := s.tokenRequest(ctx, c, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if err != nil {
		return nil, err
	}
	if t.RefreshToken == "" {
		t.RefreshToken = refreshToken // not rotated
	}
	return t, nil
}

// safeCode keeps an OAuth error code readable and bounded; descriptions can echo input.
func safeCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
