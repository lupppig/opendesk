package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/useopendesk/opendesk/internal/config"
)

// Token resources. Google issues one token for IMAP and the Gmail API; Microsoft
// issues one per resource, so IMAP and Graph each get their own access token from the
// same refresh token.
const (
	resIMAP  = "imap"
	resGraph = "graph"
)

const tokenSkew = 2 * time.Minute

type oauthProvider struct {
	name         string
	clientID     string
	clientSecret string
	authURL      string
	tokenURL     string
	userinfoURL  string // Google OIDC userinfo; Microsoft reads Graph /me instead
	// authScopes are consented at /authorize for a sync grant (Microsoft accepts several
	// resources here); sendScopes for a send grant.
	authScopes []string
	sendScopes []string
	// codeScope is sent with the code redemption of a sync grant, sendCodeScope with
	// that of a send grant; Microsoft's token endpoint takes scopes for exactly one resource.
	codeScope     string
	sendCodeScope string
	// graphScope is requested by exchanging the refresh token (Microsoft only).
	graphScope string
	authParams []oauth2.AuthCodeOption
	imapHost   string
}

func (p *oauthProvider) configured() bool {
	return p != nil && p.clientID != "" && p.clientSecret != ""
}

// scopes are the scopes consented at /authorize for a grant.
func (p *oauthProvider) scopes(grant string) []string {
	if grant == GrantSend {
		return p.sendScopes
	}
	return p.authScopes
}

// codeScopeFor is the scope sent with the code redemption for a grant.
func (p *oauthProvider) codeScopeFor(grant string) string {
	if grant == GrantSend {
		return p.sendCodeScope
	}
	return p.codeScope
}

// scopeFor is the scope sent when refreshing a token for a resource ("" = unchanged).
func (p *oauthProvider) scopeFor(resource string) string {
	if p.name != KindMicrosoft {
		return ""
	}
	if resource == resGraph {
		return p.graphScope
	}
	return p.codeScope
}

const (
	googleIMAPScope   = "https://mail.google.com/"
	googleSendScope   = "https://www.googleapis.com/auth/gmail.send"
	googleModifyScope = "https://www.googleapis.com/auth/gmail.modify"
	msIMAPScope       = "https://outlook.office.com/IMAP.AccessAsUser.All"
	msSendScope       = "https://graph.microsoft.com/Mail.Send"
	msUserScope       = "https://graph.microsoft.com/User.Read"
	msReadWriteScope  = "https://graph.microsoft.com/Mail.ReadWrite"
)

func defaultProviders(cfg *config.Config) map[string]*oauthProvider {
	tenant := cfg.MicrosoftTenant
	if tenant == "" {
		tenant = "common"
	}
	ms := "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0"
	return map[string]*oauthProvider{
		KindGoogle: {
			name: KindGoogle, clientID: cfg.GoogleMailboxClientID, clientSecret: cfg.GoogleMailboxClientSecret,
			authURL: "https://accounts.google.com/o/oauth2/v2/auth", tokenURL: "https://oauth2.googleapis.com/token",
			userinfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
			authScopes:  []string{"openid", "email", googleIMAPScope},
			sendScopes:  []string{"openid", "email", googleSendScope},
			authParams: []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"),
				oauth2.SetAuthURLParam("include_granted_scopes", "false")},
			imapHost: "imap.gmail.com",
		},
		KindMicrosoft: {
			name: KindMicrosoft, clientID: cfg.MicrosoftMailboxClientID, clientSecret: cfg.MicrosoftMailboxClientSecret,
			authURL: ms + "/authorize", tokenURL: ms + "/token",
			authScopes:    []string{"openid", "email", "offline_access", msIMAPScope, msSendScope, msUserScope},
			sendScopes:    []string{"openid", "email", "offline_access", msSendScope, msUserScope},
			codeScope:     "openid email offline_access " + msIMAPScope,
			sendCodeScope: "openid email offline_access " + msSendScope + " " + msUserScope,
			graphScope:    "offline_access " + msSendScope + " " + msUserScope,
			authParams:    []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("prompt", "select_account")},
			imapHost:      "outlook.office365.com",
		},
	}
}

// apiClients are the Collab OAuth clients (contracts/collab.md "Providers and scopes"),
// which an api grant connects with: the Gmail API with gmail.modify, Microsoft Graph with
// Mail.ReadWrite and Mail.Send. Each holds one token resource (Google's one token, or
// Microsoft's Graph token); neither has IMAP consent.
func apiClients(cfg *config.Config) map[string]*oauthProvider {
	tenant := cfg.MicrosoftTenant
	if tenant == "" {
		tenant = "common"
	}
	ms := "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0"
	googleScopes := []string{"openid", "email", googleModifyScope}
	msScopes := []string{"openid", "email", "offline_access", msReadWriteScope, msSendScope, msUserScope}
	msGraph := msReadWriteScope + " " + msSendScope + " " + msUserScope
	return map[string]*oauthProvider{
		KindGoogle: {
			name: KindGoogle, clientID: cfg.CollabGoogleClientID, clientSecret: cfg.CollabGoogleClientSecret,
			authURL: "https://accounts.google.com/o/oauth2/v2/auth", tokenURL: "https://oauth2.googleapis.com/token",
			userinfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
			authScopes:  googleScopes, sendScopes: googleScopes,
			authParams: []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"),
				oauth2.SetAuthURLParam("include_granted_scopes", "false")},
		},
		KindMicrosoft: {
			name: KindMicrosoft, clientID: cfg.CollabMicrosoftClientID, clientSecret: cfg.CollabMicrosoftClientSecret,
			authURL: ms + "/authorize", tokenURL: ms + "/token",
			authScopes: msScopes, sendScopes: msScopes,
			codeScope: "openid email offline_access " + msGraph, sendCodeScope: "openid email offline_access " + msGraph,
			graphScope: "offline_access " + msGraph,
			authParams: []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("prompt", "select_account")},
		},
	}
}

// client is the OAuth client a grant connects with: the Collab client for an api grant,
// else the mailbox client.
func (s *service) client(provider, grant string) *oauthProvider {
	if grant == GrantAPI {
		return s.apiClients[provider]
	}
	return s.providers[provider]
}

// clientOf is the OAuth client the mailbox's tokens belong to.
func (s *service) clientOf(m *mailboxRow) *oauthProvider {
	if m.Config.OAuthClient == ClientCollab {
		return s.apiClients[m.oauthProvider()]
	}
	return s.providers[m.oauthProvider()]
}

func (s *service) redirectURI(provider string) string {
	return strings.TrimRight(s.d.Cfg.APIURL, "/") + "/v1/mailboxes/oauth/" + provider + "/callback"
}

// authCodeURL builds the provider URL for a grant with state and a PKCE S256 challenge.
func (s *service) authCodeURL(p *oauthProvider, grant, state, verifier, loginHint string) string {
	c := &oauth2.Config{ClientID: p.clientID, Endpoint: oauth2.Endpoint{AuthURL: p.authURL, TokenURL: p.tokenURL},
		RedirectURL: s.redirectURI(p.name), Scopes: p.scopes(grant)}
	opts := append([]oauth2.AuthCodeOption{}, p.authParams...)
	opts = append(opts, oauth2.S256ChallengeOption(verifier))
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return c.AuthCodeURL(state, opts...)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// errReconnect means the provider refused the stored grant; the owner must reconnect.
var errReconnect = errors.New("the mailbox connection was revoked or expired. Reconnect it in settings")

// errProviderUnavailable is a transient provider failure.
var errProviderUnavailable = errors.New("the email provider is not responding. We will try again")

func (s *service) tokenRequest(ctx context.Context, p *oauthProvider, form url.Values) (*tokenResponse, error) {
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, errProviderUnavailable
	}
	defer resp.Body.Close()
	var tr tokenResponse
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(body, &tr)
	switch {
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return nil, errProviderUnavailable
	case resp.StatusCode >= 400 || tr.Error != "":
		// Error codes only: descriptions can echo request details.
		if tr.Error == "invalid_grant" || tr.Error == "interaction_required" || tr.Error == "consent_required" {
			return nil, errReconnect
		}
		return nil, fmt.Errorf("the provider refused the token request (%s)", safeCode(tr.Error))
	case tr.AccessToken == "":
		return nil, errors.New("the provider returned no access token")
	}
	return &tr, nil
}

// safeCode keeps an OAuth error code readable and bounded.
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

func expiryOf(tr *tokenResponse, now time.Time) time.Time {
	if tr.ExpiresIn <= 0 {
		return now.Add(time.Hour)
	}
	return now.Add(time.Duration(tr.ExpiresIn) * time.Second)
}

// exchangeCode redeems an authorization code for a grant with the PKCE verifier.
func (s *service) exchangeCode(ctx context.Context, p *oauthProvider, grant, code, verifier string) (*tokenResponse, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {s.redirectURI(p.name)},
		"code_verifier": {verifier}}
	if scope := p.codeScopeFor(grant); scope != "" {
		form.Set("scope", scope)
	}
	return s.tokenRequest(ctx, p, form)
}

func (s *service) refresh(ctx context.Context, p *oauthProvider, refreshToken, scope string) (*tokenResponse, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	if scope != "" {
		form.Set("scope", scope)
	}
	return s.tokenRequest(ctx, p, form)
}

// accessToken returns a valid access token for the mailbox and resource, refreshing
// (and storing a rotated refresh token) under a row lock so concurrent jobs refresh once.
func (s *service) accessToken(ctx context.Context, m *mailboxRow, resource string) (string, error) {
	provider := m.oauthProvider()
	p := s.clientOf(m)
	if !p.configured() {
		return "", fmt.Errorf("%s sign-in is not configured on this instance", providerLabel(provider))
	}
	if p.name == KindGoogle {
		resource = resIMAP
	} else if resource == resIMAP && m.noIMAPGrant() {
		// A send or api grant holds no IMAP consent to refresh.
		if m.Config.OAuthGrant == GrantAPI {
			return "", errors.New("the provider connection reads the inbox through Microsoft Graph, not IMAP")
		}
		return "", errors.New("the provider connection can only send. Reconnect the mailbox to read its inbox")
	}
	c, err := s.openCreds(m)
	if err != nil {
		return "", err
	}
	if tok, ok := c.valid(resource, time.Now()); ok {
		return tok, nil
	}
	var out string
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		locked, err := loadMailboxForUpdate(ctx, tx, m.ID)
		if err != nil {
			return err
		}
		c, err := s.openCreds(locked)
		if err != nil {
			return err
		}
		now := time.Now()
		if tok, ok := c.valid(resource, now); ok {
			out = tok
			return nil
		}
		if c.RefreshToken == "" {
			return errReconnect
		}
		tr, err := s.refresh(ctx, p, c.RefreshToken, p.scopeFor(resource))
		if err != nil {
			return err
		}
		if tr.RefreshToken != "" {
			c.RefreshToken = tr.RefreshToken
		}
		if resource == resGraph {
			c.GraphAccessToken, c.GraphExpiry = tr.AccessToken, expiryOf(tr, now)
		} else {
			c.AccessToken, c.Expiry = tr.AccessToken, expiryOf(tr, now)
		}
		sealed, err := s.sealCreds(c)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE mailboxes SET credentials_enc = $2, updated_at = now() WHERE id = $1`, m.ID, sealed); err != nil {
			return err
		}
		m.CredentialsEnc = sealed
		out = tr.AccessToken
		return nil
	})
	return out, err
}

func (c credentials) valid(resource string, now time.Time) (string, bool) {
	if resource == resGraph {
		return c.GraphAccessToken, c.GraphAccessToken != "" && now.Add(tokenSkew).Before(c.GraphExpiry)
	}
	return c.AccessToken, c.AccessToken != "" && now.Add(tokenSkew).Before(c.Expiry)
}

// oauthIdentity returns the connected account's address and IMAP login name.
func (s *service) oauthIdentity(ctx context.Context, p *oauthProvider, accessToken string) (email, username string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	target := p.userinfoURL
	if p.name == KindMicrosoft {
		target = s.graphBase + "/me?$select=mail,userPrincipalName"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", "", errProviderUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("the provider refused the profile request (%d)", resp.StatusCode)
	}
	var body struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Mail          string `json:"mail"`
		UPN           string `json:"userPrincipalName"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", "", errors.New("the provider returned an unreadable profile")
	}
	if p.name == KindMicrosoft {
		email = body.Mail
		if email == "" {
			email = body.UPN
		}
		username = body.UPN
		if username == "" {
			username = email
		}
	} else {
		if body.EmailVerified != nil && !*body.EmailVerified {
			return "", "", errors.New("the Google account's address is not verified")
		}
		email, username = body.Email, body.Email
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !validAddress(email) {
		return "", "", errors.New("the provider did not return an email address")
	}
	return email, strings.TrimSpace(username), nil
}

func providerLabel(kind string) string {
	switch kind {
	case KindGoogle:
		return "Google"
	case KindMicrosoft:
		return "Microsoft"
	}
	return kind
}

// sealedVerifier carries the PKCE verifier inside the signed state, encrypted so the
// state alone (which passes through the browser) never reveals it.
func (s *service) sealVerifier(v string) (string, error) {
	b, err := s.d.Box.Seal([]byte(v))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *service) openVerifier(sealed string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	v, err := s.d.Box.Open(b)
	return string(v), err
}
