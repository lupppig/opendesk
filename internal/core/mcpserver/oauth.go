package mcpserver

// OAuth 2.1 for the MCP server, following the MCP authorization spec: Protected
// Resource Metadata (RFC 9728), Authorization Server Metadata (RFC 8414), Dynamic
// Client Registration (RFC 7591), the authorization code flow with PKCE S256 and
// resource indicators (RFC 8707), refresh token rotation and revocation (RFC 7009).
// The consent step is a page of the web app (oauth_consent.go serves its API).
//
// Codes, client secrets and refresh tokens are stored as SHA-256 hashes; none of them,
// nor PKCE verifiers, are ever logged.

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core/oauthgrant"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// OAuth endpoints, on the API host next to /mcp.
const (
	pathResourceMeta = "/.well-known/oauth-protected-resource"
	pathServerMeta   = "/.well-known/oauth-authorization-server"
	pathRegister     = "/oauth/register"
	pathAuthorize    = "/oauth/authorize"
	pathToken        = "/oauth/token"
	pathRevoke       = "/oauth/revoke"
	// ConsentPath is the web app page that asks the signed-in user to approve.
	ConsentPath = "/oauth/consent"
)

const (
	requestTTL = 10 * time.Minute
	codeTTL    = 60 * time.Second
	refreshTTL = 30 * 24 * time.Hour

	refreshPrefix = "od_mcpr_"
	secretPrefix  = "od_mcps_"

	maxRedirectURIs = 10
	maxStateLen     = 1024
	maxFormBytes    = 64 << 10
)

// Rate limits: registrations per address per hour (and overall), authorize and token
// requests per address per minute.
const (
	registerPerHour = 20
	// Per /24 (IPv4) or /48 (IPv6), so one network can't flood registrations from many
	// addresses, and no shared bucket lets anyone lock everyone else out.
	registerPerNetworkPerHour = 60
	authorizePerMinute        = 60
	tokenPerMinute            = 60
)

// oauth serves the authorization server and resource metadata.
type oauth struct{ d *deps.Deps }

func (o *oauth) issuer() string { return o.d.Cfg.APIURL }

// Resource is the MCP server's resource identifier (RFC 8707), the access tokens' aud.
func Resource(d *deps.Deps) string { return d.Cfg.APIURL + Path }

// ResourceMetadataURL is where the MCP server's Protected Resource Metadata lives.
func ResourceMetadataURL(d *deps.Deps) string { return d.Cfg.APIURL + pathResourceMeta + Path }

func (o *oauth) mount(r chi.Router) {
	r.Get(pathResourceMeta, o.resourceMetadata)
	r.Get(pathResourceMeta+Path, o.resourceMetadata)
	r.Get(pathServerMeta, o.serverMetadata)
	r.Post(pathRegister, o.register)
	r.Get(pathAuthorize, o.authorize)
	r.Post(pathToken, o.token)
	r.Post(pathRevoke, o.revoke)
}

// --- metadata ----------------------------------------------------------------------

func (o *oauth) resourceMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 Resource(o.d),
		"authorization_servers":    []string{o.issuer()},
		"scopes_supported":         oauthgrant.Scopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "opendesk",
		"resource_documentation":   "https://docs.opendesk.sh/api/mcp/",
	})
}

func (o *oauth) serverMetadata(w http.ResponseWriter, r *http.Request) {
	iss := o.issuer()
	methods := []string{"none", "client_secret_post", "client_secret_basic"}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + pathAuthorize,
		"token_endpoint":                                 iss + pathToken,
		"registration_endpoint":                          iss + pathRegister,
		"revocation_endpoint":                            iss + pathRevoke,
		"scopes_supported":                               oauthgrant.Scopes,
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":          methods,
		"revocation_endpoint_auth_methods_supported":     methods,
		"code_challenge_methods_supported":               []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          "https://docs.opendesk.sh/api/mcp/",
	})
}

// --- registration (RFC 7591) -------------------------------------------------------

type clientMetadata struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

func (o *oauth) register(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		oauthError(w, http.StatusUnsupportedMediaType, "invalid_client_metadata", "Send the client metadata as application/json.")
		return
	}
	ip := o.clientIP(r)
	if !o.d.Limiter.Allow("mcp:oauth:register:"+ip, registerPerHour, time.Hour) ||
		!o.d.Limiter.Allow("mcp:oauth:register:net:"+network(ip), registerPerNetworkPerHour, time.Hour) {
		w.Header().Set("Retry-After", "3600")
		oauthError(w, http.StatusTooManyRequests, "slow_down", "Too many client registrations. Try again later.")
		return
	}
	var in clientMetadata
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFormBytes))
	if err := dec.Decode(&in); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "The body must be a JSON object of client metadata.")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > maxRedirectURIs {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "Register 1 to 10 redirect_uris.")
		return
	}
	uris := make([]string, 0, len(in.RedirectURIs))
	for _, u := range in.RedirectURIs {
		if err := validRedirect(u); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
		if !slices.Contains(uris, u) {
			uris = append(uris, u)
		}
	}
	method := in.TokenEndpointAuthMethod
	if method == "" {
		method = "client_secret_basic" // the RFC 7591 default
	}
	if !slices.Contains([]string{"none", "client_secret_post", "client_secret_basic"}, method) {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method must be none, client_secret_post or client_secret_basic.")
		return
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types may be authorization_code and refresh_token.")
			return
		}
	}
	if len(in.GrantTypes) > 0 && !slices.Contains(in.GrantTypes, "authorization_code") {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types must include authorization_code.")
		return
	}
	for _, t := range in.ResponseTypes {
		if t != "code" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "response_types may only be code.")
			return
		}
	}
	clientURI := ""
	if in.ClientURI != "" {
		u, err := url.Parse(in.ClientURI)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || len(in.ClientURI) > 500 {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_uri must be an https URL.")
			return
		}
		clientURI = u.String()
	}
	name := displayname.CleanOr(in.ClientName, displayname.Workspace, "MCP client")

	var secret string
	var secretHash []byte
	if method != "none" {
		secret = secretPrefix + crypto.RandomToken(32)
		secretHash = crypto.HashToken(secret)
	}
	id := ids.New()
	now := time.Now()
	if _, err := o.d.DB.Pool.Exec(r.Context(), `INSERT INTO oauth_clients (id, name, uri, redirect_uris, auth_method, secret_hash, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, name, clientURI, uris, method, secretHash, now); err != nil {
		o.d.Log.Error("mcp oauth: register client", "err", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "Something went wrong on our side. Try again in a moment.")
		return
	}
	out := map[string]any{
		"client_id":                  id.String(),
		"client_id_issued_at":        now.Unix(),
		"client_name":                name,
		"redirect_uris":              uris,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
	}
	if clientURI != "" {
		out["client_uri"] = clientURI
	}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	writeJSON(w, http.StatusCreated, out)
}

// validRedirect accepts https URLs, http on a loopback address and a native app's own
// URI scheme. No fragments, credentials or wildcards.
func validRedirect(raw string) error {
	bad := errors.New("redirect_uris must be https URLs, http on 127.0.0.1, localhost or [::1], or a native app's own scheme, without fragments or wildcards.")
	if raw == "" || len(raw) > 2000 || strings.ContainsAny(raw, "*# \t\r\n") {
		return bad
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || (u.Hostname() == "" && !privateScheme(u.Scheme)) {
		return bad
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback(u.Hostname()) {
			return nil
		}
		return bad
	}
	if privateScheme(u.Scheme) {
		return nil
	}
	return bad
}

// privateScheme accepts a native app's own URI scheme (RFC 8252 section 7.1), never one
// a browser would run or load as a page.
func privateScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "", "http", "https", "javascript", "data", "file", "vbscript", "blob", "about", "ftp", "ws", "wss", "mailto", "tel", "sms", "intent", "chrome", "view-source":
		return false
	}
	for i, c := range scheme {
		if !(c >= 'a' && c <= 'z' || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
			return false
		}
	}
	return true
}

// network is the /24 (IPv4) or /48 (IPv6) an address belongs to.
func network(ip string) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if v4 := p.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return p.Mask(net.CIDRMask(48, 128)).String()
}

func loopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// matchRedirect finds the registered redirect URI a request names. Loopback
// redirects match on any port (RFC 8252 section 7.3); everything else is exact.
func matchRedirect(registered []string, given string) (string, bool) {
	if given == "" {
		if len(registered) == 1 {
			return registered[0], true
		}
		return "", false
	}
	if slices.Contains(registered, given) {
		return given, true
	}
	g, err := url.Parse(given)
	if err != nil || g.Scheme != "http" || !loopback(g.Hostname()) || validRedirect(given) != nil {
		return "", false
	}
	for _, reg := range registered {
		u, err := url.Parse(reg)
		if err == nil && u.Scheme == "http" && u.Hostname() == g.Hostname() && u.EscapedPath() == g.EscapedPath() && u.RawQuery == g.RawQuery {
			return given, true
		}
	}
	return "", false
}

// --- authorization endpoint --------------------------------------------------------

type oauthClient struct {
	ID           uuid.UUID
	Name         string
	URI          string
	RedirectURIs []string
	Method       string
	SecretHash   []byte
}

func loadClient(ctx context.Context, q db.Querier, id uuid.UUID) (*oauthClient, error) {
	c := &oauthClient{ID: id}
	err := q.QueryRow(ctx, `SELECT name, uri, redirect_uris, auth_method, secret_hash FROM oauth_clients WHERE id = $1`, id).
		Scan(&c.Name, &c.URI, &c.RedirectURIs, &c.Method, &c.SecretHash)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (o *oauth) authorize(w http.ResponseWriter, r *http.Request) {
	if !o.d.Limiter.Allow("mcp:oauth:authorize:"+o.clientIP(r), authorizePerMinute, time.Minute) {
		tooMany(w, r, authorizePerMinute)
		return
	}
	q := r.URL.Query()
	// Until the client and its redirect URI are known, errors are shown to the user and
	// never redirected (RFC 6749 section 4.1.2.1).
	cid, err := uuid.Parse(q.Get("client_id"))
	if err != nil {
		o.consentError(w, r, "invalid_client")
		return
	}
	client, err := loadClient(r.Context(), o.d.DB.Pool, cid)
	if err != nil {
		if !db.IsNotFound(err) {
			o.d.Log.Error("mcp oauth: load client", "err", err)
		}
		o.consentError(w, r, "invalid_client")
		return
	}
	redirect, ok := matchRedirect(client.RedirectURIs, q.Get("redirect_uri"))
	if !ok || len(q["redirect_uri"]) > 1 {
		o.consentError(w, r, "invalid_redirect_uri")
		return
	}
	state := q.Get("state")
	if len(state) > maxStateLen {
		o.consentError(w, r, "invalid_request")
		return
	}
	// Clients register themselves, so an error never redirects on its own: the consent
	// page explains it and the browser stays on opendesk.
	fail := func(code, _ string) { o.consentError(w, r, code) }
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "response_type must be code.")
		return
	}
	challenge, method := q.Get("code_challenge"), q.Get("code_challenge_method")
	if challenge == "" {
		fail("invalid_request", "PKCE is required: send code_challenge with code_challenge_method S256.")
		return
	}
	if method != "S256" {
		fail("invalid_request", "code_challenge_method must be S256.")
		return
	}
	if !validChallenge(challenge) {
		fail("invalid_request", "code_challenge must be the base64url SHA-256 of the code verifier.")
		return
	}
	resource := Resource(o.d)
	if rs := q["resource"]; len(rs) > 1 || (len(rs) == 1 && strings.TrimRight(rs[0], "/") != resource) {
		fail("invalid_target", "resource must be "+resource+".")
		return
	}
	scopes := oauthgrant.Parse(strings.Fields(q.Get("scope")))
	if len(scopes) == 0 {
		scopes = slices.Clone(oauthgrant.Scopes)
	}
	id := ids.New()
	if _, err := o.d.DB.Pool.Exec(r.Context(), `INSERT INTO oauth_authorizations (id, client_id, redirect_uri, redirect_given, scopes, resource, state, code_challenge, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, client.ID, redirect, q.Get("redirect_uri") != "", scopes, resource, state, challenge, time.Now().Add(requestTTL)); err != nil {
		o.d.Log.Error("mcp oauth: store authorization request", "err", err)
		fail("server_error", "Something went wrong on our side. Try again in a moment.")
		return
	}
	http.Redirect(w, r, o.d.Cfg.AppURL+ConsentPath+"?request="+id.String(), http.StatusFound)
}

// consentError sends the user to the consent page with an error it explains.
func (o *oauth) consentError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, o.d.Cfg.AppURL+ConsentPath+"?error="+url.QueryEscape(code), http.StatusFound)
}

// withParams adds query parameters to a registered redirect URI; empty values are left out.
func withParams(redirect string, params map[string]string) string {
	u, err := url.Parse(redirect)
	if err != nil {
		return redirect
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// validChallenge accepts a base64url S256 challenge: 43 characters without padding.
func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

// verifierMatches checks a PKCE code verifier (RFC 7636) against an S256 challenge.
func verifierMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, c := range verifier {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// --- token endpoint ----------------------------------------------------------------

// tokenError is an RFC 6749 section 5.2 error answer.
type tokenError struct {
	status int
	code   string
	desc   string
}

func errGrant(desc string) *tokenError {
	return &tokenError{http.StatusBadRequest, "invalid_grant", desc}
}

var errClient = &tokenError{http.StatusUnauthorized, "invalid_client", "Client authentication failed."}

func (o *oauth) token(w http.ResponseWriter, r *http.Request) {
	if !o.d.Limiter.Allow("mcp:oauth:token:"+o.clientIP(r), tokenPerMinute, time.Minute) {
		tooManyOAuth(w)
		return
	}
	form, terr := readForm(w, r)
	if terr == nil {
		var client *oauthClient
		if client, terr = o.authenticateClient(r, form); terr == nil {
			switch form.Get("grant_type") {
			case "authorization_code":
				terr = o.exchangeCode(w, r, client, form)
			case "refresh_token":
				terr = o.exchangeRefresh(w, r, client, form)
			case "":
				terr = &tokenError{http.StatusBadRequest, "invalid_request", "grant_type is required."}
			default:
				terr = &tokenError{http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token."}
			}
		}
	}
	if terr != nil {
		if terr == errClient && strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			w.Header().Set("WWW-Authenticate", `Basic realm="opendesk"`)
		}
		oauthError(w, terr.status, terr.code, terr.desc)
	}
}

func readForm(w http.ResponseWriter, r *http.Request) (url.Values, *tokenError) {
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/x-www-form-urlencoded") {
		return nil, &tokenError{http.StatusBadRequest, "invalid_request", "Send the parameters as application/x-www-form-urlencoded."}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return nil, &tokenError{http.StatusBadRequest, "invalid_request", "The request body could not be read."}
	}
	for k, v := range r.PostForm {
		if len(v) > 1 {
			return nil, &tokenError{http.StatusBadRequest, "invalid_request", k + " must appear once."}
		}
	}
	return r.PostForm, nil
}

// authenticateClient identifies the client by HTTP Basic or the form, and checks its
// secret when it registered as a confidential client.
func (o *oauth) authenticateClient(r *http.Request, form url.Values) (*oauthClient, *tokenError) {
	rawID, secret := form.Get("client_id"), form.Get("client_secret")
	if u, p, ok := r.BasicAuth(); ok {
		var err1, err2 error
		if rawID, err1 = url.QueryUnescape(u); err1 == nil {
			secret, err2 = url.QueryUnescape(p)
		}
		if err1 != nil || err2 != nil || (form.Get("client_id") != "" && form.Get("client_id") != rawID) {
			return nil, errClient
		}
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil, errClient
	}
	c, err := loadClient(r.Context(), o.d.DB.Pool, id)
	if err != nil {
		if !db.IsNotFound(err) {
			o.d.Log.Error("mcp oauth: load client", "err", err)
			return nil, &tokenError{http.StatusInternalServerError, "server_error", "Something went wrong on our side."}
		}
		return nil, errClient
	}
	if c.Method != "none" {
		if secret == "" || subtle.ConstantTimeCompare(crypto.HashToken(secret), c.SecretHash) != 1 {
			return nil, errClient
		}
	}
	if o.d.Limiter.Allow("mcp:oauth:client-touch:"+c.ID.String(), 1, time.Minute) {
		_, _ = o.d.DB.Pool.Exec(context.WithoutCancel(r.Context()), `UPDATE oauth_clients SET last_used_at = now() WHERE id = $1`, c.ID)
	}
	return c, nil
}

// grantRow is what token issuance needs to know about a grant.
type grantRow struct {
	ID, WorkspaceID, UserID uuid.UUID
	Scopes                  []string
	Resource                string
}

func (o *oauth) exchangeCode(w http.ResponseWriter, r *http.Request, client *oauthClient, form url.Values) *tokenError {
	code := form.Get("code")
	if code == "" {
		return &tokenError{http.StatusBadRequest, "invalid_request", "code is required."}
	}
	ctx := r.Context()
	var out *tokenResponse
	var terr *tokenError
	err := o.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var aid, aclient uuid.UUID
		var grantID *uuid.UUID
		var redirect, resource, challenge, status string
		var redirectGiven bool
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT id, client_id, grant_id, redirect_uri, redirect_given, resource, code_challenge, status, expires_at
			FROM oauth_authorizations WHERE code_hash = $1 FOR UPDATE`, crypto.HashToken(code)).
			Scan(&aid, &aclient, &grantID, &redirect, &redirectGiven, &resource, &challenge, &status, &expires)
		if db.IsNotFound(err) {
			terr = errGrant("The code is not valid.")
			return nil
		}
		if err != nil {
			return err
		}
		if status == "used" {
			// A code presented twice: whoever holds it is not to be trusted, so the grant
			// it created ends (RFC 6749 section 4.1.2).
			if grantID != nil {
				if _, err := oauthgrant.Revoke(ctx, tx, nil, uuid.Nil, uuid.Nil, *grantID, oauthgrant.ReasonCodeReused); err != nil {
					return err
				}
				o.d.Log.Warn("mcp oauth: authorization code reused; grant revoked", "grant_id", *grantID)
			}
			terr = errGrant("The code was already used.")
			return nil
		}
		if status != "approved" || grantID == nil {
			terr = errGrant("The code is not valid.")
			return nil
		}
		// Single use from here on, whatever the outcome.
		if _, err := tx.Exec(ctx, `UPDATE oauth_authorizations SET status = 'used', used_at = now() WHERE id = $1`, aid); err != nil {
			return err
		}
		switch {
		case aclient != client.ID:
			terr = errGrant("The code was issued to another client.")
		case time.Now().After(expires):
			terr = errGrant("The code has expired.")
		// redirect_uri is required here exactly when the authorization request sent it
		// (RFC 6749 section 4.1.3); when sent anyway it must still match.
		case (redirectGiven || form.Get("redirect_uri") != "") && form.Get("redirect_uri") != redirect:
			terr = errGrant("redirect_uri does not match the authorization request.")
		case form.Get("resource") != "" && strings.TrimRight(form.Get("resource"), "/") != resource:
			terr = &tokenError{http.StatusBadRequest, "invalid_target", "resource does not match the authorization request."}
		case !verifierMatches(form.Get("code_verifier"), challenge):
			terr = errGrant("The code verifier does not match.")
		}
		if terr != nil {
			return nil
		}
		g, role, err := activeGrant(ctx, tx, *grantID)
		if db.IsNotFound(err) {
			terr = errGrant("The authorization was revoked.")
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE oauth_grants SET last_used_at = now() WHERE id = $1`, g.ID); err != nil {
			return err
		}
		// Connecting the same client again replaces its previous connection.
		rows, err := tx.Query(ctx, `SELECT id FROM oauth_grants WHERE user_id = $1 AND workspace_id = $2 AND client_id = $3
			AND id <> $4 AND revoked_at IS NULL AND last_used_at IS NOT NULL`, g.UserID, g.WorkspaceID, client.ID, g.ID)
		if err != nil {
			return err
		}
		old, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range old {
			if _, err := oauthgrant.Revoke(ctx, tx, nil, uuid.Nil, uuid.Nil, id, oauthgrant.ReasonReplaced); err != nil {
				return err
			}
		}
		out, err = o.issue(ctx, tx, g, oauthgrant.Effective(g.Scopes, role))
		return err
	})
	if err != nil {
		o.d.Log.Error("mcp oauth: exchange code", "err", err)
		return &tokenError{http.StatusInternalServerError, "server_error", "Something went wrong on our side."}
	}
	if terr != nil {
		return terr
	}
	writeToken(w, out)
	return nil
}

func (o *oauth) exchangeRefresh(w http.ResponseWriter, r *http.Request, client *oauthClient, form url.Values) *tokenError {
	token := form.Get("refresh_token")
	if token == "" {
		return &tokenError{http.StatusBadRequest, "invalid_request", "refresh_token is required."}
	}
	ctx := r.Context()
	var out *tokenResponse
	var terr *tokenError
	err := o.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var tid, gid, gclient uuid.UUID
		var used *time.Time
		var expires time.Time
		var revoked *time.Time
		err := tx.QueryRow(ctx, `SELECT t.id, t.grant_id, t.used_at, t.expires_at, g.client_id, g.revoked_at
			FROM oauth_refresh_tokens t JOIN oauth_grants g ON g.id = t.grant_id
			WHERE t.token_hash = $1 FOR UPDATE OF t, g`, crypto.HashToken(token)).Scan(&tid, &gid, &used, &expires, &gclient, &revoked)
		if db.IsNotFound(err) {
			terr = errGrant("The refresh token is not valid.")
			return nil
		}
		if err != nil {
			return err
		}
		if gclient != client.ID {
			terr = errGrant("The refresh token was issued to another client.")
			return nil
		}
		if used != nil {
			// A rotated token came back: it was copied. End the whole grant.
			if revoked == nil {
				if _, err := oauthgrant.Revoke(ctx, tx, nil, uuid.Nil, uuid.Nil, gid, oauthgrant.ReasonRefreshReused); err != nil {
					return err
				}
				o.d.Log.Warn("mcp oauth: refresh token reused; grant revoked", "grant_id", gid)
			}
			terr = errGrant("The refresh token was already used.")
			return nil
		}
		if revoked != nil || time.Now().After(expires) {
			terr = errGrant("The refresh token has expired or was revoked.")
			return nil
		}
		g, role, err := activeGrant(ctx, tx, gid)
		if db.IsNotFound(err) {
			terr = errGrant("The authorization was revoked.")
			return nil
		}
		if err != nil {
			return err
		}
		scopes := oauthgrant.Effective(g.Scopes, role)
		if s := form.Get("scope"); s != "" {
			asked := strings.Fields(s)
			for _, a := range asked {
				if !slices.Contains(g.Scopes, a) {
					terr = &tokenError{http.StatusBadRequest, "invalid_scope", "scope may only narrow the granted scopes."}
					return nil
				}
			}
			scopes = oauthgrant.Effective(asked, role)
		}
		if _, err := tx.Exec(ctx, `UPDATE oauth_refresh_tokens SET used_at = now() WHERE id = $1`, tid); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE oauth_grants SET last_used_at = now() WHERE id = $1`, gid); err != nil {
			return err
		}
		out, err = o.issue(ctx, tx, g, scopes)
		return err
	})
	if err != nil {
		o.d.Log.Error("mcp oauth: refresh", "err", err)
		return &tokenError{http.StatusInternalServerError, "server_error", "Something went wrong on our side."}
	}
	if terr != nil {
		return terr
	}
	writeToken(w, out)
	return nil
}

// activeGrant loads a grant that is not revoked, with the user's current role in its
// workspace. pgx.ErrNoRows when the grant ended or the membership did, or while the
// workspace or the user is suspended (contracts/admin.md).
func activeGrant(ctx context.Context, q db.Querier, gid uuid.UUID) (grantRow, auth.Role, error) {
	g := grantRow{ID: gid}
	var role string
	err := q.QueryRow(ctx, `SELECT g.workspace_id, g.user_id, g.scopes, g.resource, m.role FROM oauth_grants g
		JOIN members m ON m.workspace_id = g.workspace_id AND m.user_id = g.user_id
		JOIN workspaces w ON w.id = g.workspace_id AND w.suspended_at IS NULL
		JOIN users u ON u.id = g.user_id AND u.suspended_at IS NULL
		WHERE g.id = $1 AND g.revoked_at IS NULL`, gid).Scan(&g.WorkspaceID, &g.UserID, &g.Scopes, &g.Resource, &role)
	return g, auth.Role(role), err
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// issue signs an access token for the grant and stores a new refresh token.
func (o *oauth) issue(ctx context.Context, tx pgx.Tx, g grantRow, scopes []string) (*tokenResponse, error) {
	scope := strings.Join(scopes, " ")
	access, _, err := o.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeMCPAccess, Workspace: g.WorkspaceID.String(),
		Grant: g.ID.String(), Scope: scope, RegisteredClaims: jwt.RegisteredClaims{Issuer: o.issuer(), Audience: jwt.ClaimStrings{g.Resource}}},
		g.UserID.String(), auth.MCPAccessTTL)
	if err != nil {
		return nil, err
	}
	refresh := refreshPrefix + crypto.RandomToken(32)
	if _, err := tx.Exec(ctx, `INSERT INTO oauth_refresh_tokens (id, grant_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		ids.New(), g.ID, crypto.HashToken(refresh), time.Now().Add(refreshTTL)); err != nil {
		return nil, err
	}
	return &tokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(auth.MCPAccessTTL / time.Second), RefreshToken: refresh, Scope: scope}, nil
}

func writeToken(w http.ResponseWriter, t *tokenResponse) {
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, t)
}

// --- revocation (RFC 7009) ---------------------------------------------------------

func (o *oauth) revoke(w http.ResponseWriter, r *http.Request) {
	if !o.d.Limiter.Allow("mcp:oauth:token:"+o.clientIP(r), tokenPerMinute, time.Minute) {
		tooManyOAuth(w)
		return
	}
	form, terr := readForm(w, r)
	var client *oauthClient
	if terr == nil {
		client, terr = o.authenticateClient(r, form)
	}
	if terr == nil && form.Get("token") == "" {
		terr = &tokenError{http.StatusBadRequest, "invalid_request", "token is required."}
	}
	if terr != nil {
		oauthError(w, terr.status, terr.code, terr.desc)
		return
	}
	ctx := r.Context()
	token := form.Get("token")
	var gid uuid.UUID
	if strings.HasPrefix(token, refreshPrefix) {
		err := o.d.DB.Pool.QueryRow(ctx, `SELECT g.id FROM oauth_refresh_tokens t JOIN oauth_grants g ON g.id = t.grant_id
			WHERE t.token_hash = $1 AND g.client_id = $2`, crypto.HashToken(token), client.ID).Scan(&gid)
		if err != nil && !db.IsNotFound(err) {
			o.d.Log.Error("mcp oauth: revoke lookup", "err", err)
		}
	} else if c, err := o.d.Tokens.Verify(token, auth.PurposeMCPAccess); err == nil {
		if id, err := uuid.Parse(c.Grant); err == nil {
			var owner uuid.UUID
			if o.d.DB.Pool.QueryRow(ctx, `SELECT client_id FROM oauth_grants WHERE id = $1`, id).Scan(&owner) == nil && owner == client.ID {
				gid = id
			}
		}
	}
	if gid != uuid.Nil {
		err := o.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			_, err := oauthgrant.Revoke(ctx, tx, nil, uuid.Nil, uuid.Nil, gid, oauthgrant.ReasonClient)
			return err
		})
		if err != nil {
			o.d.Log.Error("mcp oauth: revoke", "err", err)
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "Try again in a moment.")
			return
		}
	}
	// Unknown and already revoked tokens answer 200 too (RFC 7009 section 2.2).
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// --- helpers -----------------------------------------------------------------------

func (o *oauth) clientIP(r *http.Request) string {
	return httpx.ClientIP(o.d.Cfg.TrustProxy, o.d.Cfg.ProxyHops)(r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func tooManyOAuth(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	oauthError(w, http.StatusTooManyRequests, "slow_down", "Too many requests. Try again in a minute.")
}
