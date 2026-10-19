package collab

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/plan"
)

// Connecting accounts (contracts/collab.md "Endpoints"): OAuth for Google and Microsoft
// (the flow is bound to the browser that started it by a cookie whose hash is in the
// signed state), and IMAP accounts tried before they are saved.

const (
	oauthCookie     = "od_collab_oauth"
	oauthCookiePath = "/v1/collab/accounts/oauth"
	oauthFlow       = "collab"
	// maxConnectAudience bounds the audience carried through the provider in the state;
	// a larger one is set in the account's settings after connecting.
	maxConnectAudience = 40
)

type oauthStartInput struct {
	Provider string `path:"provider" enum:"google,microsoft"`
	Body     struct {
		Visibility string    `json:"visibility,omitempty" enum:"private,shared" doc:"Private (default) or shared"`
		SharedWith *Audience `json:"shared_with,omitempty"`
		Name       string    `json:"name,omitempty" maxLength:"64" doc:"How the sidebar names the account; the address by default"`
		Email      string    `json:"email,omitempty" maxLength:"254" doc:"Login hint for the provider"`
		AccountID  string    `json:"account_id,omitempty" doc:"Reconnect this account instead of adding one"`
	}
}

type oauthStartOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		URL string `json:"url"`
	}
}

type imapConnectInput struct {
	Body struct {
		Email        string    `json:"email" maxLength:"254"`
		Name         string    `json:"name,omitempty" maxLength:"64"`
		DisplayName  string    `json:"display_name,omitempty" maxLength:"64"`
		IMAPHost     string    `json:"imap_host" maxLength:"253"`
		IMAPPort     int       `json:"imap_port" minimum:"1" maximum:"65535"`
		IMAPTLS      string    `json:"imap_tls" enum:"tls,starttls"`
		IMAPUsername string    `json:"imap_username,omitempty" maxLength:"254"`
		Password     string    `json:"password" minLength:"1" maxLength:"1024"`
		SMTPHost     string    `json:"smtp_host" maxLength:"253"`
		SMTPPort     int       `json:"smtp_port" minimum:"1" maximum:"65535"`
		SMTPTLS      string    `json:"smtp_tls" enum:"tls,starttls"`
		SMTPUsername string    `json:"smtp_username,omitempty" maxLength:"254"`
		SMTPPassword string    `json:"smtp_password,omitempty" maxLength:"1024"`
		Visibility   string    `json:"visibility,omitempty" enum:"private,shared"`
		SharedWith   *Audience `json:"shared_with,omitempty"`
	}
}

type accountOutput struct {
	Body MailAccount
}

// registerConnectRoutes adds the OAuth start and callback and the IMAP connect routes.
func (s *service) registerConnectRoutes(api huma.API, r chi.Router) {
	huma.Register(api, huma.Operation{OperationID: "start-collab-oauth", Method: http.MethodPost,
		Path: "/v1/collab/accounts/oauth/{provider}/start", Summary: "Start connecting a Google or Microsoft mail account",
		Description: "Returns the provider URL to open in the same browser. The call sets a short-lived cookie, so send it with credentials.",
		Tags:        []string{"Collab"}, Hidden: true},
		func(ctx context.Context, in *oauthStartInput) (*oauthStartOutput, error) {
			out, err := s.startOAuth(ctx, in)
			return out, httpx.Wrap(err)
		})

	r.Get("/v1/collab/accounts/oauth/{provider}/callback", s.oauthCallback)

	huma.Register(api, huma.Operation{OperationID: "connect-collab-imap", Method: http.MethodPost,
		Path: "/v1/collab/accounts/imap", Summary: "Connect an IMAP mail account",
		Description: "Both servers are tried with the credentials before the account is saved.",
		Tags:        []string{"Collab"}, Hidden: true},
		func(ctx context.Context, in *imapConnectInput) (*accountOutput, error) {
			out, err := s.connectIMAP(ctx, in)
			return out, httpx.Wrap(err)
		})
}

// connectAudience checks the visibility and audience of a new account.
func connectAudience(ctx context.Context, q db.Querier, wid uuid.UUID, visibility string, shared *Audience) (string, Audience, error) {
	if visibility == "" {
		visibility = VisibilityPrivate
	}
	if visibility == VisibilityPrivate || shared == nil {
		return visibility, Audience{TeamIDs: []uuid.UUID{}, UserIDs: []uuid.UUID{}}, nil
	}
	a, err := checkAudience(ctx, q, wid, *shared)
	return visibility, a, err
}

func (s *service) startOAuth(ctx context.Context, in *oauthStartInput) (*oauthStartOutput, error) {
	p, err := s.require(ctx, auth.RoleAgent)
	if err != nil {
		return nil, err
	}
	c := s.oauth[in.Provider]
	if !c.configured() {
		return nil, httpx.E(422, "provider_not_configured", providerLabel(in.Provider)+" sign-in is not configured on this instance.")
	}
	b := in.Body
	data := map[string]string{"flow": oauthFlow, "provider": in.Provider, "wid": p.WorkspaceID.String()}
	if b.AccountID != "" {
		id, err := parseConnectID(b.AccountID)
		if err != nil {
			return nil, err
		}
		a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
		if err != nil {
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Account")
			}
			return nil, err
		}
		if a.Provider != in.Provider {
			return nil, httpx.E(422, "validation_error", "Reconnect the account with "+providerLabel(a.Provider)+".")
		}
		data["account_id"] = id.String()
		if b.Email == "" {
			b.Email = a.Email
		}
	} else {
		visibility, audience, err := connectAudience(ctx, s.d.DB.Pool, p.WorkspaceID, b.Visibility, b.SharedWith)
		if err != nil {
			return nil, err
		}
		if len(audience.TeamIDs)+len(audience.UserIDs) > maxConnectAudience {
			return nil, httpx.E(422, "validation_error", "Share with fewer teams and people while connecting, then add the rest in the account's settings.")
		}
		name, err := accountName(b.Name)
		if err != nil {
			return nil, err
		}
		data["visibility"], data["shared"], data["name"] = visibility, encodeAudience(audience), name
	}
	verifier := oauth2.GenerateVerifier()
	if data["verifier"], err = s.sealVerifier(verifier); err != nil {
		return nil, err
	}
	nonce := crypto.RandomToken(32)
	data["nonce"] = hex.EncodeToString(crypto.HashToken(nonce))
	state, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeOAuthState, Data: data}, p.UserID.String(), auth.OAuthStateTTL)
	if err != nil {
		return nil, err
	}
	hint := strings.ToLower(strings.TrimSpace(b.Email))
	if !email.ValidAddress(hint) {
		hint = ""
	}
	out := &oauthStartOutput{SetCookie: s.stateCookie(nonce, int(auth.OAuthStateTTL.Seconds()))}
	out.Body.URL = s.authCodeURL(c, state, verifier, hint)
	return out, nil
}

func parseConnectID(s string) (uuid.UUID, error) {
	id, ok := ids.Parse(s)
	if !ok {
		return uuid.Nil, httpx.NotFound("Account")
	}
	return id, nil
}

// accountName checks a name typed for the sidebar ("" keeps the address).
func accountName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	return displayname.Validate("Name", name, displayname.Workspace, true)
}

// encodeAudience packs an audience into the state: "e" for everyone, then t:<team> and
// u:<user> entries, comma separated.
func encodeAudience(a Audience) string {
	parts := []string{}
	if a.Everyone {
		parts = append(parts, "e")
	}
	for _, id := range a.TeamIDs {
		parts = append(parts, "t:"+id.String())
	}
	for _, id := range a.UserIDs {
		parts = append(parts, "u:"+id.String())
	}
	return strings.Join(parts, ",")
}

func decodeAudience(s string) Audience {
	a := Audience{TeamIDs: []uuid.UUID{}, UserIDs: []uuid.UUID{}}
	for _, part := range strings.Split(s, ",") {
		switch {
		case part == "e":
			a.Everyone = true
		case strings.HasPrefix(part, "t:"):
			if id, err := uuid.Parse(part[2:]); err == nil {
				a.TeamIDs = append(a.TeamIDs, id)
			}
		case strings.HasPrefix(part, "u:"):
			if id, err := uuid.Parse(part[2:]); err == nil {
				a.UserIDs = append(a.UserIDs, id)
			}
		}
	}
	return a
}

func (s *service) stateCookie(value string, maxAge int) http.Cookie {
	return http.Cookie{Name: oauthCookie, Value: value, Path: oauthCookiePath, Domain: s.d.Cfg.CookieDomain,
		MaxAge: maxAge, HttpOnly: true, Secure: s.d.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
}

func (s *service) settingsURL(q url.Values) string {
	return strings.TrimRight(s.d.Cfg.AppURL, "/") + "/settings/mail-accounts?" + q.Encode()
}

// oauthCallback finishes the provider flow and redirects back to the app.
func (s *service) oauthCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	fail := func(code string) {
		http.Redirect(w, r, s.settingsURL(url.Values{"error": {code}}), http.StatusFound)
	}
	clear := s.stateCookie("", -1)
	http.SetCookie(w, &clear)

	c := s.oauth[provider]
	if !c.configured() {
		fail("provider_error")
		return
	}
	q := r.URL.Query()
	claims, err := s.d.Tokens.Verify(q.Get("state"), auth.PurposeOAuthState)
	if err != nil || claims.Data["flow"] != oauthFlow || claims.Data["provider"] != provider {
		fail("oauth_state")
		return
	}
	cookie, err := r.Cookie(oauthCookie)
	if err != nil || cookie.Value == "" ||
		subtle.ConstantTimeCompare([]byte(hex.EncodeToString(crypto.HashToken(cookie.Value))), []byte(claims.Data["nonce"])) != 1 {
		fail("oauth_state")
		return
	}
	if q.Get("error") != "" {
		fail("access_denied")
		return
	}
	uid, err1 := uuid.Parse(claims.Subject)
	wid, err2 := uuid.Parse(claims.Data["wid"])
	if err1 != nil || err2 != nil {
		fail("oauth_state")
		return
	}
	ctx := r.Context()
	// Membership, role and Collab are checked again: they may have changed meanwhile.
	var role string
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2`, wid, uid).Scan(&role); err != nil ||
		!auth.Role(role).AtLeast(auth.RoleAgent) {
		fail("forbidden")
		return
	}
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid); err != nil || suspended {
		fail("forbidden")
		return
	}
	if err := s.d.Plan.RequireCollab(ctx, s.d.DB.Pool, wid); err != nil {
		if errors.Is(err, plan.ErrCollab) {
			fail("collab_required")
		} else {
			fail("internal")
		}
		return
	}
	verifier, err := s.openVerifier(claims.Data["verifier"])
	if err != nil {
		fail("oauth_state")
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > 4096 {
		fail("provider_error")
		return
	}
	creds, ident, err := s.completeOAuth(ctx, c, code, verifier)
	if err != nil {
		s.log.Warn("collab oauth exchange failed", "provider", provider, "workspace_id", wid, "reason", safeError(err))
		fail("provider_error")
		return
	}
	p := &auth.Principal{Kind: auth.KindUser, UserID: uid, WorkspaceID: wid, Role: auth.Role(role),
		IP: httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(r)}
	req := connection{provider: provider, email: ident.Email, name: claims.Data["name"], displayName: ident.Name,
		visibility: claims.Data["visibility"], audience: decodeAudience(claims.Data["shared"]), creds: creds,
		config: accountConfig{Scopes: ident.Scopes}}
	if v := claims.Data["account_id"]; v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			fail("oauth_state")
			return
		}
		req.accountID = &id
	}
	id, err := s.saveConnection(ctx, p, req)
	if err != nil {
		var he *httpx.Error
		switch {
		case errors.As(err, &he) && he.Code == "account_conflict":
			fail("account_conflict")
		case errors.Is(err, auth.ErrForbidden), db.IsNotFound(err):
			fail("forbidden")
		default:
			s.log.Error("save collab account", "workspace_id", wid, "err", err)
			fail("internal")
		}
		return
	}
	http.Redirect(w, r, s.settingsURL(url.Values{"connected": {id.String()}}), http.StatusFound)
}

// connection is an account being connected or reconnected.
type connection struct {
	provider    string
	email       string // lower case
	name        string // "" = the address
	displayName string // "" = the address
	visibility  string
	audience    Audience
	config      accountConfig
	creds       credentials
	accountID   *uuid.UUID // reconnect this account
}

// saveConnection creates the account, or updates the one reconnected (an address the
// caller already connected counts as a reconnect), audits, publishes and queues the
// first sync. An address someone else connected in the workspace is errConflict.
func (s *service) saveConnection(ctx context.Context, p *auth.Principal, req connection) (uuid.UUID, error) {
	sealed, err := s.sealCreds(req.creds)
	if err != nil {
		return uuid.Nil, err
	}
	wid := p.WorkspaceID
	var id uuid.UUID
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var existing *accountRow
		if req.accountID != nil {
			a, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountColumns+` FROM collab_accounts WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
				wid, *req.accountID))
			if err != nil {
				return err
			}
			if !canManageAccount(p, a) {
				return auth.ErrForbidden
			}
			// A reconnect is the same mailbox: another address would mix two accounts' mail.
			if a.Provider != req.provider || !strings.EqualFold(a.Email, req.email) {
				return errConflict
			}
			existing = a
		} else {
			a, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountColumns+` FROM collab_accounts WHERE workspace_id = $1 AND lower(email) = $2 FOR UPDATE`,
				wid, req.email))
			if err != nil && !db.IsNotFound(err) {
				return err
			}
			if a != nil {
				if a.OwnerID == nil || *a.OwnerID != p.UserID || a.Provider != req.provider {
					return errConflict
				}
				existing = a
			}
		}
		if existing != nil {
			id = existing.ID
			// A reconnect keeps how the settings synced; OAuth records what was granted now.
			cfg := existing.Config
			if req.provider == ProviderIMAP {
				cfg = req.config
				cfg.Settings = existing.Config.Settings
			} else if req.config.Scopes != nil {
				cfg.Scopes = req.config.Scopes
			}
			if cfg.Settings != nil {
				st := *cfg.Settings
				st.CheckedAt = nil // the first sync after a reconnect reads the settings again
				cfg.Settings = &st
			}
			if _, err := tx.Exec(ctx, `UPDATE collab_accounts SET credentials_enc = $3, config = $4, status = 'syncing', reconnect = false,
				last_error = '', sync_failures = 0, next_sync_at = NULL, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				wid, id, sealed, cfg); err != nil {
				return err
			}
		} else {
			id = ids.New()
			name, display := req.name, req.displayName
			if name == "" {
				name = req.email
			}
			if display == "" {
				display = req.email
			}
			visibility := req.visibility
			if visibility != VisibilityShared {
				visibility = VisibilityPrivate
			}
			_, err := tx.Exec(ctx, `INSERT INTO collab_accounts (id, workspace_id, owner_id, provider, email, name, display_name, visibility,
				config, credentials_enc, status) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'syncing')`,
				id, wid, p.UserID, req.provider, req.email, name, display, visibility, req.config, sealed)
			if db.IsUniqueViolation(err, "collab_accounts_workspace_id_email_key") {
				return errConflict
			}
			if err != nil {
				return err
			}
			if visibility == VisibilityShared {
				// The audience is checked again: teams and members may have changed meanwhile.
				audience, err := checkAudience(ctx, tx, wid, req.audience)
				if err != nil {
					return err
				}
				if err := saveAudience(ctx, tx, wid, &id, nil, audience, p.UserID); err != nil {
					return err
				}
			}
		}
		visibility := req.visibility
		if existing != nil {
			visibility = existing.Visibility
		}
		if err := audit.Log(ctx, tx, p, wid, "collab.account_connected", "collab_account", &id, map[string]any{
			"provider": req.provider, "visibility": visibility, "reconnect": existing != nil}); err != nil {
			return err
		}
		if err := s.publishAccounts(ctx, tx, wid, id, nil); err != nil {
			return err
		}
		return s.queueSync(ctx, tx, id)
	})
	return id, err
}

func (s *service) connectIMAP(ctx context.Context, in *imapConnectInput) (*accountOutput, error) {
	p, err := s.require(ctx, auth.RoleAgent)
	if err != nil {
		return nil, err
	}
	if !s.d.Cfg.CollabIMAP {
		return nil, errIMAPOff
	}
	b := in.Body
	address := strings.ToLower(strings.TrimSpace(b.Email))
	if !email.ValidAddress(address) {
		return nil, httpx.E(422, "validation_error", "Enter a valid email address.")
	}
	for _, h := range []string{b.IMAPHost, b.SMTPHost} {
		if err := s.guard.ValidHost(h); err != nil {
			if errors.Is(err, email.ErrBlockedAddress) {
				return nil, httpx.E(422, "connection_failed", "The server address is not allowed (it must be a public address).")
			}
			return nil, httpx.E(422, "validation_error", "Enter server host names like imap.example.com.")
		}
	}
	visibility, audience, err := connectAudience(ctx, s.d.DB.Pool, p.WorkspaceID, b.Visibility, b.SharedWith)
	if err != nil {
		return nil, err
	}
	name, err := accountName(b.Name)
	if err != nil {
		return nil, err
	}
	display := ""
	if strings.TrimSpace(b.DisplayName) != "" {
		if display, err = displayname.Validate("Display name", b.DisplayName, displayname.Workspace, true); err != nil {
			return nil, err
		}
	}
	if !s.d.Limiter.Allow("collab-imap-connect:"+p.UserID.String(), 10, 10*time.Minute) {
		return nil, httpx.E(429, "rate_limited", "Too many connection attempts. Try again in a few minutes.")
	}
	cfg := accountConfig{IMAPHost: strings.TrimSpace(b.IMAPHost), IMAPPort: b.IMAPPort, IMAPTLS: b.IMAPTLS,
		IMAPUsername: strings.TrimSpace(b.IMAPUsername), SMTPHost: strings.TrimSpace(b.SMTPHost), SMTPPort: b.SMTPPort,
		SMTPTLS: b.SMTPTLS, SMTPUsername: strings.TrimSpace(b.SMTPUsername)}
	if cfg.IMAPUsername == "" {
		cfg.IMAPUsername = address
	}
	creds := credentials{Password: b.Password, SMTPPassword: b.SMTPPassword}
	if err := s.testIMAP(ctx, cfg, address, creds); err != nil {
		return nil, httpx.E(422, "connection_failed", safeError(err))
	}
	id, err := s.saveConnection(ctx, p, connection{provider: ProviderIMAP, email: address, name: name, displayName: display,
		visibility: visibility, audience: audience, config: cfg, creds: creds})
	if err != nil {
		return nil, err
	}
	a, err := loadAccount(ctx, s.d.DB.Pool, p.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	out, err := s.accountOut(ctx, s.d.DB.Pool, p, a)
	if err != nil {
		return nil, err
	}
	return &accountOutput{Body: out}, nil
}
