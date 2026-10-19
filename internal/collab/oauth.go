package collab

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

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/email"
)

// OAuth for Google and Microsoft accounts (contracts/collab.md "Providers and scopes"):
// PKCE S256, a signed oauth_state token and a browser-bound cookie (connect.go), refresh
// tokens sealed with d.Box and refreshed under the account's row lock.

const (
	googleModifyScope = "https://www.googleapis.com/auth/gmail.modify"
	msReadWriteScope  = "https://graph.microsoft.com/Mail.ReadWrite"
	msSendScope       = "https://graph.microsoft.com/Mail.Send"
	msUserScope       = "https://graph.microsoft.com/User.Read"
	tokenSkew         = 2 * time.Minute

	// The account's own settings (settings.go): Gmail's signature, From name and automatic
	// reply, and Outlook's mailbox settings (automatic replies, categories). Without them
	// the account still syncs mail; its settings are read only, or not synced.
	googleSettingsScope    = "https://www.googleapis.com/auth/gmail.settings.basic"
	msMailboxSettingsScope = "https://graph.microsoft.com/MailboxSettings.ReadWrite"
)

// oauthClient is a provider's OAuth client for Collab.
type oauthClient struct {
	name         string
	clientID     string
	clientSecret string
	authURL      string
	tokenURL     string
	// userinfoURL is Google's OIDC userinfo; Microsoft reads Graph /me instead.
	userinfoURL string
	// revokeURL revokes a token when the account is disconnected (Google only).
	revokeURL string
	scopes    []string
	// refreshScope is sent with a refresh (Microsoft: the Graph scopes) of an account whose
	// granted scopes were never recorded; refreshScopeFor narrows it to what was granted.
	refreshScope string
	authParams   []oauth2.AuthCodeOption
}

func (c *oauthClient) configured() bool { return c != nil && c.clientID != "" && c.clientSecret != "" }

func oauthClients(cfg *config.Config) map[string]*oauthClient {
	tenant := cfg.MicrosoftTenant
	if tenant == "" {
		tenant = "common"
	}
	ms := "https://login.microsoftonline.com/" + url.PathEscape(tenant) + "/oauth2/v2.0"
	return map[string]*oauthClient{
		ProviderGoogle: {
			name: ProviderGoogle, clientID: cfg.CollabGoogleClientID, clientSecret: cfg.CollabGoogleClientSecret,
			authURL: "https://accounts.google.com/o/oauth2/v2/auth", tokenURL: "https://oauth2.googleapis.com/token",
			userinfoURL: "https://openidconnect.googleapis.com/v1/userinfo", revokeURL: "https://oauth2.googleapis.com/revoke",
			scopes: []string{"openid", "email", "profile", googleModifyScope, googleSettingsScope},
			authParams: []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"),
				oauth2.SetAuthURLParam("include_granted_scopes", "false")},
		},
		ProviderMicrosoft: {
			name: ProviderMicrosoft, clientID: cfg.CollabMicrosoftClientID, clientSecret: cfg.CollabMicrosoftClientSecret,
			authURL: ms + "/authorize", tokenURL: ms + "/token",
			scopes:       []string{"openid", "email", "offline_access", msReadWriteScope, msSendScope, msUserScope, msMailboxSettingsScope},
			refreshScope: "offline_access " + msReadWriteScope + " " + msSendScope + " " + msUserScope,
			authParams:   []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("prompt", "select_account")},
		},
	}
}

// providersOffered reports which providers can be connected on this instance.
func (s *service) providersOffered() CollabProviders {
	return CollabProviders{
		Google:    s.oauth[ProviderGoogle].configured(),
		Microsoft: s.oauth[ProviderMicrosoft].configured(),
		IMAP:      s.d.Cfg.CollabIMAP,
	}
}

func providerLabel(provider string) string {
	switch provider {
	case ProviderGoogle:
		return "Google"
	case ProviderMicrosoft:
		return "Microsoft"
	}
	return "IMAP"
}

func (s *service) redirectURI(provider string) string {
	return strings.TrimRight(s.d.Cfg.APIURL, "/") + "/v1/collab/accounts/oauth/" + provider + "/callback"
}

// authCodeURL builds the provider URL with state and a PKCE S256 challenge.
func (s *service) authCodeURL(c *oauthClient, state, verifier, loginHint string) string {
	conf := &oauth2.Config{ClientID: c.clientID, Endpoint: oauth2.Endpoint{AuthURL: c.authURL, TokenURL: c.tokenURL},
		RedirectURL: s.redirectURI(c.name), Scopes: c.scopes}
	opts := append([]oauth2.AuthCodeOption{}, c.authParams...)
	opts = append(opts, oauth2.S256ChallengeOption(verifier))
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return conf.AuthCodeURL(state, opts...)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	// Scope is the space-separated scopes the token carries (what the person granted).
	Scope string `json:"scope"`
	Error string `json:"error"`
}

func (s *service) tokenRequest(ctx context.Context, c *oauthClient, form url.Values) (*tokenResponse, error) {
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
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
		return nil, &providerError{msg: fmt.Sprintf("The provider refused the token request (%s).", safeCode(tr.Error))}
	case tr.AccessToken == "":
		return nil, &providerError{msg: "The provider returned no access token."}
	}
	return &tr, nil
}

// safeCode keeps an OAuth or API error code readable and bounded.
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

// exchangeCode redeems an authorization code with the PKCE verifier.
func (s *service) exchangeCode(ctx context.Context, c *oauthClient, code, verifier string) (*tokenResponse, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {s.redirectURI(c.name)},
		"code_verifier": {verifier}}
	if c.name == ProviderMicrosoft {
		form.Set("scope", strings.Join(c.scopes, " "))
	}
	return s.tokenRequest(ctx, c, form)
}

func (s *service) refreshToken(ctx context.Context, c *oauthClient, refresh string, granted []string) (*tokenResponse, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}
	if scope := c.refreshScopeFor(granted); scope != "" {
		form.Set("scope", scope)
	}
	return s.tokenRequest(ctx, c, form)
}

// refreshScopeFor is the scope a refresh asks for (Microsoft): the Graph scopes the account
// was granted, since asking for one it was not granted refuses the refresh; refreshScope for
// an account whose scopes were never recorded.
func (c *oauthClient) refreshScopeFor(granted []string) string {
	if c.refreshScope == "" || granted == nil {
		return c.refreshScope
	}
	out := []string{"offline_access"}
	for _, sc := range c.scopes {
		if strings.HasPrefix(sc, "https://") && hasScope(granted, sc) {
			out = append(out, sc)
		}
	}
	if len(out) == 1 {
		return c.refreshScope
	}
	return strings.Join(out, " ")
}

// grantedScopes reads a token response's scope list; nil when it names none.
func grantedScopes(tr *tokenResponse) []string {
	list := strings.Fields(tr.Scope)
	if len(list) == 0 {
		return nil
	}
	return list
}

// hasScope reports whether granted holds want. Microsoft may answer a Graph scope without
// its resource ("Mail.Send" for "https://graph.microsoft.com/Mail.Send").
func hasScope(granted []string, want string) bool {
	short := strings.TrimPrefix(want, "https://graph.microsoft.com/")
	for _, g := range granted {
		if strings.EqualFold(g, want) || short != want && strings.EqualFold(g, short) {
			return true
		}
	}
	return false
}

// identity is who the provider says the connected account is.
type identity struct {
	Email string
	Name  string
	// Scopes are what the person granted (the requested ones when the provider does not say).
	Scopes []string
}

// completeOAuth redeems the code and reads the account's address and name.
func (s *service) completeOAuth(ctx context.Context, c *oauthClient, code, verifier string) (credentials, identity, error) {
	tr, err := s.exchangeCode(ctx, c, code, verifier)
	if err != nil {
		return credentials{}, identity{}, err
	}
	if tr.RefreshToken == "" {
		return credentials{}, identity{}, &providerError{msg: "The provider did not grant offline access."}
	}
	creds := credentials{RefreshToken: tr.RefreshToken, AccessToken: tr.AccessToken, Expiry: expiryOf(tr, time.Now())}
	id, err := s.oauthIdentity(ctx, c, tr.AccessToken)
	if err != nil {
		return credentials{}, identity{}, err
	}
	if id.Scopes = grantedScopes(tr); id.Scopes == nil {
		id.Scopes = append([]string{}, c.scopes...)
	}
	return creds, id, nil
}

// oauthIdentity reads the connected account's address and name.
func (s *service) oauthIdentity(ctx context.Context, c *oauthClient, accessToken string) (identity, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	target := c.userinfoURL
	if c.name == ProviderMicrosoft {
		target = s.graphBase + "/me?$select=mail,userPrincipalName,displayName"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return identity{}, errProviderUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return identity{}, &providerError{msg: fmt.Sprintf("The provider refused the profile request (%d).", resp.StatusCode)}
	}
	var body struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
		Mail          string `json:"mail"`
		UPN           string `json:"userPrincipalName"`
		DisplayName   string `json:"displayName"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return identity{}, &providerError{msg: "The provider returned an unreadable profile."}
	}
	var out identity
	if c.name == ProviderMicrosoft {
		out.Email, out.Name = body.Mail, body.DisplayName
		if out.Email == "" {
			out.Email = body.UPN
		}
	} else {
		if body.EmailVerified != nil && !*body.EmailVerified {
			return identity{}, &providerError{msg: "The Google account's address is not verified."}
		}
		out.Email, out.Name = body.Email, body.Name
	}
	out.Email = strings.ToLower(strings.TrimSpace(out.Email))
	if !email.ValidAddress(out.Email) {
		return identity{}, &providerError{msg: "The provider did not return an email address."}
	}
	// Google's profile name (the profile scope); without one, the address's send-as name in
	// Gmail, which gmail.modify reads.
	if c.name == ProviderGoogle && strings.TrimSpace(out.Name) == "" {
		out.Name = s.gmailSendAsName(ctx, accessToken, out.Email)
	}
	// A name from the identity provider is untrusted.
	out.Name = displayname.Clean(out.Name, displayname.Workspace)
	return out, nil
}

// gmailSendAsName is the Gmail send-as name of addr (else of the primary send-as address),
// or "" when Gmail does not say. A failure only leaves the name to the first sync.
func (s *service) gmailSendAsName(ctx context.Context, accessToken, addr string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gmailBase+"/gmail/v1/users/me/settings/sendAs", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var list struct {
		SendAs []gmailSendAs `json:"sendAs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list); err != nil {
		return ""
	}
	name := ""
	for _, sa := range list.SendAs {
		if strings.EqualFold(sa.SendAsEmail, addr) {
			return sa.DisplayName
		}
		if sa.IsPrimary {
			name = sa.DisplayName
		}
	}
	return name
}

// accessToken returns a valid access token for an OAuth account, refreshing it (and
// storing a rotated refresh token) under the row lock so concurrent jobs refresh once.
func (s *service) accessToken(ctx context.Context, a *accountRow) (string, error) {
	c := s.oauth[a.Provider]
	if !c.configured() {
		return "", &providerError{msg: providerLabel(a.Provider) + " sign-in is not configured on this instance."}
	}
	creds, err := s.openCreds(a)
	if err != nil {
		return "", err
	}
	if creds.AccessToken != "" && time.Now().Add(tokenSkew).Before(creds.Expiry) {
		return creds.AccessToken, nil
	}
	var out string
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		locked, err := loadAccountForUpdate(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		creds, err := s.openCreds(locked)
		if err != nil {
			return err
		}
		now := time.Now()
		if creds.AccessToken != "" && now.Add(tokenSkew).Before(creds.Expiry) {
			out, a.CredentialsEnc = creds.AccessToken, locked.CredentialsEnc
			return nil
		}
		if creds.RefreshToken == "" {
			return errReconnect
		}
		tr, err := s.refreshToken(ctx, c, creds.RefreshToken, locked.Config.Scopes)
		if err != nil {
			return err
		}
		// A refresh says what the grant still carries: a scope the person removed since
		// (at the provider's security page) stops the settings it allowed.
		if scopes := grantedScopes(tr); scopes != nil && !slices.Equal(scopes, locked.Config.Scopes) {
			b, err := json.Marshal(scopes)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE collab_accounts SET config = jsonb_set(config, '{scopes}', $2::jsonb) WHERE id = $1`, a.ID, b); err != nil {
				return err
			}
			a.Config.Scopes = scopes
		}
		if tr.RefreshToken != "" {
			creds.RefreshToken = tr.RefreshToken
		}
		creds.AccessToken, creds.Expiry = tr.AccessToken, expiryOf(tr, now)
		sealed, err := s.sealCreds(creds)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE collab_accounts SET credentials_enc = $2, updated_at = now() WHERE id = $1`, a.ID, sealed); err != nil {
			return err
		}
		a.CredentialsEnc = sealed
		out = tr.AccessToken
		return nil
	})
	return out, err
}

// expireToken forgets a cached access token the provider just refused.
func (s *service) expireToken(ctx context.Context, a *accountRow) error {
	creds, err := s.openCreds(a)
	if err != nil {
		return err
	}
	creds.Expiry = time.Time{}
	sealed, err := s.sealCreds(creds)
	if err != nil {
		return err
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE collab_accounts SET credentials_enc = $2 WHERE id = $1`, a.ID, sealed); err != nil {
		return err
	}
	a.CredentialsEnc = sealed
	return nil
}

// revokeGoogle revokes a disconnected Google account's grant, best effort and briefly.
func (s *service) revokeGoogle(ctx context.Context, a *accountRow) {
	c := s.oauth[ProviderGoogle]
	if a.Provider != ProviderGoogle || c == nil || c.revokeURL == "" {
		return
	}
	creds, err := s.openCreds(a)
	if err != nil || creds.RefreshToken == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.revokeURL, strings.NewReader(url.Values{"token": {creds.RefreshToken}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		s.log.Info("collab google revoke failed", "account_id", a.ID)
		return
	}
	_ = resp.Body.Close()
}

// sealVerifier carries the PKCE verifier inside the signed state, encrypted so the state
// alone (which passes through the browser) never reveals it.
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

// providerError is a provider failure whose message is safe to store and show.
// permanent failures of an action are not retried.
type providerError struct {
	msg       string
	status    int
	permanent bool
	notFound  bool
}

func (e *providerError) Error() string { return e.msg }

// safeError renders an error for last_error and API details: our own messages as they
// are, anything else as a generic message.
func safeError(err error) string {
	if err == nil {
		return ""
	}
	var pe *providerError
	var ie *imapError
	switch {
	case errors.As(err, &pe):
		return cleanLine(pe.msg, 300)
	case errors.As(err, &ie):
		return cleanLine(ie.msg, 300)
	case errors.Is(err, errReconnect), errors.Is(err, errProviderUnavailable):
		return err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "Timed out talking to the mail provider."
	}
	return "Something went wrong talking to the mail provider."
}

// isReconnect reports a refused grant or login.
func isReconnect(err error) bool {
	var ie *imapError
	return errors.Is(err, errReconnect) || errors.As(err, &ie) && ie.auth
}

var addressLike = regexp.MustCompile(`[^\s<>"',;:()\[\]]+@[^\s<>"',;:()\[\]]+`)

// serverText is a mail server's own words, fit to store and show: one bounded line with
// any address in it replaced.
func serverText(s string) string {
	return cleanLine(addressLike.ReplaceAllString(s, "[address]"), 160)
}

// cleanLine keeps one line of printable text, at most n runes.
func cleanLine(s string, n int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
