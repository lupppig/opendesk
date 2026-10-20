package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// oauthBindCookie ties an OAuth callback to the browser that started it.
const oauthBindCookie = "od_oauth"

// stateFlowLogin marks oauth_state tokens minted for sign-in (mailbox connections use
// the same purpose with a different flow).
const stateFlowLogin = "login"

const (
	githubIssuer = "https://github.com"
	// msaTenant is the tenant id of personal Microsoft accounts.
	msaTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"
)

var msIssuerRe = regexp.MustCompile(`^https://login\.microsoftonline\.com/([0-9a-f-]{36})/v2\.0$`)

// Native sign-in (contracts/native.md): the flow runs in the system browser and its
// result goes back to the app through this exact URL, never another scheme or target.
const nativeCallbackURL = "opendesk://auth/callback"

var (
	// pkceChallengeRe is BASE64URL(SHA256(verifier)) without padding.
	pkceChallengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	// The id a browser gives a sign-in it runs in a popup window, so the waiting tab
	// can tell its own result from another tab's.
	popupIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	// pkceVerifierRe is RFC 7636's code_verifier.
	pkceVerifierRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

// pkceMatches checks a code_verifier against the S256 challenge in constant time.
func pkceMatches(verifier, challenge string) bool {
	if !pkceVerifierRe.MatchString(verifier) || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return constantTimeEq(base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
}

type oauthProvider struct {
	id, name string
	kind     string // oidc | github
	issuer   string // discovery URL for oidc kinds
	tenant   string // microsoft only
	cfg      oauth2.Config

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
	endpoint oauth2.Endpoint
}

type oauthProviders struct {
	list []*oauthProvider
	byID map[string]*oauthProvider
	http *http.Client
}

func newOAuthProviders(cfg *config.Config) *oauthProviders {
	ps := &oauthProviders{byID: map[string]*oauthProvider{}, http: &http.Client{Timeout: 10 * time.Second}}
	add := func(p *oauthProvider) {
		p.cfg.RedirectURL = cfg.APIURL + "/v1/auth/oauth/" + p.id + "/callback"
		ps.list = append(ps.list, p)
		ps.byID[p.id] = p
	}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		add(&oauthProvider{id: "google", name: "Google", kind: "oidc", issuer: "https://accounts.google.com",
			cfg: oauth2.Config{ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret,
				Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}})
	}
	if cfg.MicrosoftClientID != "" && cfg.MicrosoftClientSecret != "" {
		tenant := cfg.MicrosoftTenant
		if tenant == "" {
			tenant = "common"
		}
		add(&oauthProvider{id: "microsoft", name: "Microsoft", kind: "oidc", tenant: tenant,
			issuer: "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/v2.0",
			cfg: oauth2.Config{ClientID: cfg.MicrosoftClientID, ClientSecret: cfg.MicrosoftClientSecret,
				Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}})
	}
	if cfg.GitHubClientID != "" && cfg.GitHubClientSecret != "" {
		add(&oauthProvider{id: "github", name: "GitHub", kind: "github",
			cfg: oauth2.Config{ClientID: cfg.GitHubClientID, ClientSecret: cfg.GitHubClientSecret,
				Endpoint: github.Endpoint, Scopes: []string{"read:user", "user:email"}}})
	}
	if cfg.OIDCIssuer != "" && cfg.OIDCClientID != "" {
		name := displayname.CleanOr(cfg.OIDCName, displayname.Workspace, "SSO")
		add(&oauthProvider{id: "oidc", name: name, kind: "oidc", issuer: strings.TrimRight(cfg.OIDCIssuer, "/"),
			cfg: oauth2.Config{ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
				Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}})
	}
	return ps
}

// ensure discovers an OIDC provider once. Microsoft's multi-tenant metadata names a
// templated issuer, so its id tokens are checked against the tenant claim instead.
func (ps *oauthProviders) ensure(ctx context.Context, p *oauthProvider) (oauth2.Config, error) {
	if p.kind != "oidc" {
		return p.cfg, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verifier == nil {
		dctx := oidc.ClientContext(ctx, ps.http)
		var prov *oidc.Provider
		var err error
		if p.id == "microsoft" {
			var iss string
			if iss, err = ps.discoveredIssuer(ctx, p.issuer); err == nil {
				prov, err = oidc.NewProvider(oidc.InsecureIssuerURLContext(dctx, iss), p.issuer)
			}
		} else {
			prov, err = oidc.NewProvider(dctx, p.issuer)
		}
		if err != nil {
			return oauth2.Config{}, fmt.Errorf("discover %s: %w", p.id, err)
		}
		p.endpoint = prov.Endpoint()
		p.verifier = prov.Verifier(&oidc.Config{ClientID: p.cfg.ClientID, SkipIssuerCheck: p.id == "microsoft"})
	}
	c := p.cfg
	c.Endpoint = p.endpoint
	return c, nil
}

func (ps *oauthProviders) discoveredIssuer(ctx context.Context, issuer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	resp, err := ps.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery status %d", resp.StatusCode)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return "", err
	}
	return doc.Issuer, nil
}

// fedIdentity is a verified identity from a provider.
type fedIdentity struct {
	Provider      string
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	AvatarURL     string
}

type idClaims struct {
	Issuer        string `json:"iss"`
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	Nonce         string `json:"nonce"`
	TenantID      string `json:"tid"`
	EmailDomainOV any    `json:"xms_edov"`
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	}
	return false
}

// exchange trades the authorization code for a verified identity.
func (ps *oauthProviders) exchange(ctx context.Context, p *oauthProvider, code, verifier, nonce string) (*fedIdentity, error) {
	cfg, err := ps.ensure(ctx, p)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, ps.http)
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("code exchange: %w", err)
	}
	if p.kind == "github" {
		return ps.githubIdentity(ctx, tok)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("no id_token in token response")
	}
	idt, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	var c idClaims
	if err := idt.Claims(&c); err != nil {
		return nil, err
	}
	if nonce == "" || !constantTimeEq(c.Nonce, nonce) {
		return nil, errors.New("id_token nonce mismatch")
	}
	if c.Subject == "" {
		return nil, errors.New("id_token without subject")
	}
	id := &fedIdentity{Provider: p.id, Issuer: idt.Issuer, Subject: c.Subject, Email: normEmail(c.Email),
		EmailVerified: truthy(c.EmailVerified), Name: c.Name, AvatarURL: c.Picture}
	if p.id == "microsoft" {
		m := msIssuerRe.FindStringSubmatch(idt.Issuer)
		if m == nil || m[1] != c.TenantID {
			return nil, errors.New("microsoft issuer does not match tenant")
		}
		switch p.tenant {
		case "common":
		case "consumers":
			if c.TenantID != msaTenant {
				return nil, errors.New("tenant not allowed")
			}
		case "organizations":
			if c.TenantID == msaTenant {
				return nil, errors.New("tenant not allowed")
			}
		default:
			if _, err := uuid.Parse(p.tenant); err == nil && !strings.EqualFold(p.tenant, c.TenantID) {
				return nil, errors.New("tenant not allowed")
			}
		}
		// Personal accounts own their address; work accounts prove the domain with xms_edov.
		id.EmailVerified = c.TenantID == msaTenant || truthy(c.EmailDomainOV)
		id.AvatarURL = ""
	}
	return id, nil
}

func (ps *oauthProviders) githubIdentity(ctx context.Context, tok *oauth2.Token) (*fedIdentity, error) {
	get := func(path string, v any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := ps.http.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("github %s: status %d", path, resp.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
	}
	var u struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := get("/user", &u); err != nil {
		return nil, err
	}
	if u.ID == 0 {
		return nil, errors.New("github user without id")
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := get("/user/emails", &emails); err != nil {
		return nil, err
	}
	id := &fedIdentity{Provider: "github", Issuer: githubIssuer, Subject: strconv.FormatInt(u.ID, 10), Name: u.Name, AvatarURL: u.AvatarURL}
	if id.Name == "" {
		id.Name = u.Login
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			id.Email, id.EmailVerified = normEmail(e.Email), true
		}
	}
	return id, nil
}

// safeRedirectPath keeps redirect_to inside the app: a same-origin path, or an absolute
// URL on APP_URL reduced to its path.
func safeRedirectPath(appURL, raw string) string {
	if raw == "" || len(raw) > 1000 {
		return ""
	}
	if strings.HasPrefix(raw, appURL+"/") {
		raw = strings.TrimPrefix(raw, appURL)
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n\t") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return ""
	}
	return u.RequestURI()
}

func bindHash(v string) string {
	sum := sha256.Sum256([]byte("oauth-bind:" + v))
	return hex.EncodeToString(sum[:])
}

func (s *svc) bindCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: oauthBindCookie, Value: value, Path: "/v1/auth/oauth", Domain: s.d.Cfg.CookieDomain,
		HttpOnly: true, Secure: s.d.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(httpx.E(status, code, detail))
}

func (s *svc) oauthStart(w http.ResponseWriter, r *http.Request) {
	p := s.oauth.byID[chi.URLParam(r, "provider")]
	if p == nil {
		writeProblem(w, 404, "not_found", "Unknown sign-in provider.")
		return
	}
	ip := httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(r)
	if !s.d.Limiter.Allow("oauth:start:"+ip, 30, 15*time.Minute) {
		writeProblem(w, 429, "rate_limited", "Too many requests. Wait a moment and try again.")
		return
	}
	q := r.URL.Query()
	native := q.Get("client") == "native"
	challenge := q.Get("code_challenge")
	if native && !pkceChallengeRe.MatchString(challenge) {
		s.oauthFail(w, r, true, "", "invalid_request")
		return
	}
	popup := ""
	if !native && popupIDRe.MatchString(q.Get("popup")) {
		popup = q.Get("popup")
	}
	cfg, err := s.oauth.ensure(r.Context(), p)
	if err != nil {
		s.d.Log.Error("oauth provider unavailable", "provider", p.id, "err", err)
		s.oauthFail(w, r, native, popup, "provider_unavailable")
		return
	}
	verifier := oauth2.GenerateVerifier()
	sealed, err := s.d.Box.Seal([]byte(verifier))
	if err != nil {
		s.oauthFail(w, r, native, popup, "server_error")
		return
	}
	nonce := crypto.RandomToken(16)
	bind := crypto.RandomToken(16)
	data := map[string]string{
		"flow": stateFlowLogin, "provider": p.id, "v": base64.RawURLEncoding.EncodeToString(sealed),
		"nonce": nonce, "bind": bindHash(bind), "rt": safeRedirectPath(s.d.Cfg.AppURL, q.Get("redirect_to")),
	}
	if native {
		data["client"], data["cc"] = "native", challenge
	}
	if popup != "" {
		data["popup"] = popup
	}
	state, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeOAuthState, Data: data}, "", auth.OAuthStateTTL)
	if err != nil {
		s.oauthFail(w, r, native, popup, "server_error")
		return
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if p.kind == "oidc" {
		opts = append(opts, oauth2.SetAuthURLParam("nonce", nonce))
	}
	if p.id == "google" || p.id == "microsoft" {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "select_account"))
	}
	http.SetCookie(w, s.bindCookie(bind, int(auth.OAuthStateTTL.Seconds())))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, cfg.AuthCodeURL(state, opts...), http.StatusFound)
}

// oauthDone sends the browser back with a one-time code or an error code: to the app's
// /auth/callback, to its /auth/popup for a sign-in running in a popup window (which hands
// the result to the tab that opened it), or to the native app's callback URL for a native
// flow.
func (s *svc) oauthDone(w http.ResponseWriter, r *http.Request, native bool, popup, key, value string) {
	http.SetCookie(w, s.bindCookie("", -1))
	w.Header().Set("Cache-Control", "no-store")
	target := s.appLink("/auth/callback", key, value)
	switch {
	case native:
		target = nativeCallbackURL + "?" + url.Values{key: {value}}.Encode()
	case popup != "":
		target = s.d.Cfg.AppURL + "/auth/popup?" + url.Values{key: {value}, "popup": {popup}}.Encode()
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *svc) oauthFail(w http.ResponseWriter, r *http.Request, native bool, popup, code string) {
	s.oauthDone(w, r, native, popup, "error", code)
}

func (s *svc) oauthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := s.oauth.byID[chi.URLParam(r, "provider")]
	if p == nil {
		writeProblem(w, 404, "not_found", "Unknown sign-in provider.")
		return
	}
	q := r.URL.Query()
	c, stateErr := s.d.Tokens.Verify(q.Get("state"), auth.PurposeOAuthState)
	stateOK := stateErr == nil && c.Data["flow"] == stateFlowLogin && c.Data["provider"] == p.id
	// Only a verified state can send the browser to the native app or a popup's opener.
	native := stateOK && c.Data["client"] == "native"
	popup := ""
	if stateOK && !native {
		popup = c.Data["popup"]
	}
	if q.Get("error") != "" {
		s.oauthFail(w, r, native, popup, "access_denied")
		return
	}
	if !stateOK {
		s.oauthFail(w, r, false, "", "invalid_state")
		return
	}
	bc, err := r.Cookie(oauthBindCookie)
	if err != nil || !constantTimeEq(bindHash(bc.Value), c.Data["bind"]) {
		s.oauthFail(w, r, native, popup, "invalid_state")
		return
	}
	sealed, err := base64.RawURLEncoding.DecodeString(c.Data["v"])
	if err != nil {
		s.oauthFail(w, r, native, popup, "invalid_state")
		return
	}
	verifier, err := s.d.Box.Open(sealed)
	if err != nil {
		s.oauthFail(w, r, native, popup, "invalid_state")
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > 4096 {
		s.oauthFail(w, r, native, popup, "invalid_request")
		return
	}
	id, err := s.oauth.exchange(ctx, p, code, string(verifier), c.Data["nonce"])
	if err != nil {
		s.d.Log.Warn("oauth sign-in failed", "provider", p.id, "err", err)
		s.oauthFail(w, r, native, popup, "provider_error")
		return
	}
	ri := s.readReqInfo(r)
	codeChallenge := ""
	if native {
		codeChallenge = c.Data["cc"]
	}
	loginCode, errCode := s.resolveIdentity(ctx, id, c.Data["rt"], codeChallenge, ri)
	if errCode != "" {
		s.oauthFail(w, r, native, popup, errCode)
		return
	}
	s.oauthDone(w, r, native, popup, "code", loginCode)
}

// resolveIdentity binds the identity to a user and returns a one-time login code, or an
// error code for the app. Identities bind by (issuer, subject); an existing password
// account is linked only after its password is presented (POST /v1/auth/oauth/link).
// A code for a native flow carries its PKCE challenge, which exchange then requires.
func (s *svc) resolveIdentity(ctx context.Context, id *fedIdentity, redirectTo, codeChallenge string, ri reqInfo) (string, string) {
	var code, errCode string
	var newUser uuid.UUID
	err := s.tx(ctx, func(tx pgx.Tx) error {
		data := map[string]string{"kind": "login", "method": id.Provider, "rt": redirectTo}
		if codeChallenge != "" {
			data["cc"] = codeChallenge
		}
		var uid uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE identities SET email = $3 WHERE issuer = $1 AND subject = $2 RETURNING user_id`,
			id.Issuer, id.Subject, id.Email).Scan(&uid)
		if err != nil && !db.IsNotFound(err) {
			return err
		}
		if err == nil {
			if err := s.providerPicture(ctx, tx, uid, id.AvatarURL); err != nil {
				return err
			}
			code, err = createOneTime(ctx, tx, ottOAuthLogin, &uid, "", data, oauthCodeTTL)
			return err
		}
		if id.Email == "" || !id.EmailVerified {
			errCode = "email_unverified"
			return nil
		}
		var hasPassword bool
		err = tx.QueryRow(ctx, `SELECT id, `+auth.PasswordHashSQL+` IS NOT NULL FROM users WHERE email = $1`, id.Email).Scan(&uid, &hasPassword)
		switch {
		case err == nil && hasPassword:
			data["kind"] = "link"
			data["provider"], data["issuer"], data["subject"] = id.Provider, id.Issuer, id.Subject
			code, err = createOneTime(ctx, tx, ottOAuthLogin, &uid, id.Email, data, oauthCodeTTL)
			return err
		case err == nil:
			// A passwordless account whose address the provider verified.
			if err := insertIdentity(ctx, tx, uid, id); err != nil {
				return err
			}
			if _, err := auditUser(ctx, tx, userPrincipal(uid, ri), uid, "identity.linked", map[string]any{"provider": id.Provider}); err != nil {
				return err
			}
			if err := s.notifySecurity(ctx, tx, uid, emails.ProviderLinked, providerDetail(id.Provider)); err != nil {
				return err
			}
		case db.IsNotFound(err):
			if !s.d.Cfg.SignupEnabled {
				var invited bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM invitations WHERE email = $1 AND accepted_at IS NULL AND expires_at > now())`,
					id.Email).Scan(&invited); err != nil {
					return err
				}
				if !invited {
					errCode = "signup_disabled"
					return nil
				}
			}
			uid = ids.New()
			name := displayname.CleanOr(id.Name, displayname.Person, displayname.FromEmail(id.Email))
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name, email_verified_at) VALUES ($1, $2, $3, now())`,
				uid, id.Email, name); err != nil {
				return err
			}
			if err := insertIdentity(ctx, tx, uid, id); err != nil {
				return err
			}
			newUser = uid
		default:
			return err
		}
		if err := s.providerPicture(ctx, tx, uid, id.AvatarURL); err != nil {
			return err
		}
		code, err = createOneTime(ctx, tx, ottOAuthLogin, &uid, "", data, oauthCodeTTL)
		return err
	})
	if err != nil {
		s.d.Log.Error("oauth resolve identity", "provider", id.Provider, "err", err)
		return "", "server_error"
	}
	if newUser != uuid.Nil {
		s.d.Analytics.Capture(newUser.String(), "", "user_signed_up", map[string]any{"method": id.Provider})
	}
	return code, errCode
}

func insertIdentity(ctx context.Context, tx pgx.Tx, uid uuid.UUID, id *fedIdentity) error {
	_, err := tx.Exec(ctx, `INSERT INTO identities (id, user_id, provider, issuer, subject, email) VALUES ($1, $2, $3, $4, $5, $6)`,
		ids.New(), uid, id.Provider, id.Issuer, id.Subject, id.Email)
	if db.IsUniqueViolation(err, "") {
		return httpx.Conflict("This sign-in is already linked to another account.")
	}
	return err
}

func (s *svc) registerOAuth(api huma.API, r chi.Router) {
	r.Get("/v1/auth/oauth/{provider}/start", s.oauthStart)
	r.Get("/v1/auth/oauth/{provider}/callback", s.oauthCallback)

	register(s, api, huma.Operation{OperationID: "auth-oauth-exchange", Method: http.MethodPost, Path: "/v1/auth/oauth/exchange",
		Summary: "Trade an OAuth callback code for a session", Tags: []string{"Auth"}, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Code         string `json:"code" maxLength:"200"`
				CodeVerifier string `json:"code_verifier,omitempty" maxLength:"128" doc:"The PKCE verifier; required for a code issued to a native sign-in"`
			}
		}) (*authOut, error) {
			ri := requestInfo(ctx)
			if err := s.limit("oauth:exchange:"+ri.IP, loginPerIP, loginWindow); err != nil {
				return nil, err
			}
			o, err := consumeOneTime(ctx, s.d.DB.Pool, ottOAuthLogin, in.Body.Code)
			if db.IsNotFound(err) || (err == nil && o.UserID == nil) {
				return nil, errInvalidToken()
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// The code is spent either way: a wrong verifier cannot be retried.
			if cc := o.Data["cc"]; cc != "" && !pkceMatches(in.Body.CodeVerifier, cc) {
				return nil, httpx.E(400, "invalid_code", "This sign-in code is invalid or has expired.")
			}
			if o.Data["kind"] == "link" {
				tok, err := createOneTime(ctx, s.d.DB.Pool, ottOAuthLink, o.UserID, o.Email, o.Data, oauthLinkTTL)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				return &authOut{CacheControl: "no-store", Body: CoreAuthResult{LinkRequired: true, LinkToken: tok, Email: o.Email,
					RedirectTo: o.Data["rt"]}}, nil
			}
			var totp bool
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT totp_enabled FROM users WHERE id = $1`, *o.UserID).Scan(&totp); err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.completeFirstFactor(ctx, *o.UserID, totp, o.Data["method"], o.Data["rt"], false, ri)
		})

	register(s, api, huma.Operation{OperationID: "auth-oauth-link", Method: http.MethodPost, Path: "/v1/auth/oauth/link",
		Summary: "Link a sign-in provider to an existing password account", Tags: []string{"Auth"}, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				LinkToken string `json:"link_token" maxLength:"200"`
				Password  string `json:"password" maxLength:"1024"`
			}
		}) (*authOut, error) {
			ri := requestInfo(ctx)
			if err := s.limitShared(ctx, "login:ip:"+ri.IP, loginPerIP, loginWindow); err != nil {
				return nil, err
			}
			var uid uuid.UUID
			var email string
			var raw []byte
			err := s.d.DB.Pool.QueryRow(ctx, `SELECT user_id, email, data FROM one_time_tokens
				WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now() AND user_id IS NOT NULL`,
				crypto.HashToken(in.Body.LinkToken), ottOAuthLink).Scan(&uid, &email, &raw)
			if db.IsNotFound(err) {
				return nil, errInvalidToken()
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limitShared(ctx, "login:email:"+email, loginPerAccount, loginWindow); err != nil {
				return nil, err
			}
			var hash *string
			var totp bool
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT `+auth.PasswordHashSQL+`, totp_enabled FROM users WHERE id = $1`, uid).Scan(&hash, &totp); err != nil {
				return nil, httpx.Wrap(err)
			}
			if hash == nil {
				auth.DummyVerify(in.Body.Password)
				return nil, errInvalidCredentials()
			}
			if !auth.VerifyPassword(in.Body.Password, *hash) {
				s.loginFailed(ctx, uid, ri, "oauth_link")
				return nil, errInvalidCredentials()
			}
			var data map[string]string
			err = s.tx(ctx, func(tx pgx.Tx) error {
				o, err := consumeOneTime(ctx, tx, ottOAuthLink, in.Body.LinkToken)
				if db.IsNotFound(err) {
					return errInvalidToken()
				}
				if err != nil {
					return err
				}
				data = o.Data
				id := &fedIdentity{Provider: o.Data["provider"], Issuer: o.Data["issuer"], Subject: o.Data["subject"], Email: o.Email}
				if err := insertIdentity(ctx, tx, uid, id); err != nil {
					return err
				}
				if _, err := auditUser(ctx, tx, userPrincipal(uid, ri), uid, "identity.linked", map[string]any{"provider": id.Provider}); err != nil {
					return err
				}
				return s.notifySecurity(ctx, tx, uid, emails.ProviderLinked, providerDetail(id.Provider))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.completeFirstFactor(ctx, uid, totp, data["method"], data["rt"], false, ri)
		})
}
