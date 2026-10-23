package email

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
	"github.com/useopendesk/opendesk/internal/email/mailhost"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/plan"
)

// oauthCookie binds a mailbox OAuth flow to the browser that started it, so a
// provider URL handed to someone else cannot connect their mailbox to this workspace.
const (
	oauthCookie     = "od_mailbox_oauth"
	oauthCookiePath = "/v1/mailboxes/oauth"
	oauthFlow       = "mailbox"
)

type oauthStartInput struct {
	Provider string `path:"provider" enum:"google,microsoft"`
	Body     struct {
		MailboxID string `json:"mailbox_id,omitempty" doc:"Reconnect this mailbox instead of adding one"`
		Name      string `json:"name,omitempty" maxLength:"64"`
		Email     string `json:"email,omitempty" maxLength:"254" doc:"Login hint for the provider"`
		Receive   string `json:"receive,omitempty" enum:"forwarding,sync,api" doc:"How a new mailbox receives: forwarding (default; the connection only sends), sync (read the inbox over IMAP, while this instance has IMAP on) or api (read the inbox through the Gmail API or Microsoft Graph, while the workspace has Collab). A reconnect keeps the mailbox's own, or moves it to api."`
		SyncStart string `json:"sync_start,omitempty" enum:"recent,new" doc:"Where the first sync reads from: the last 7 days (recent, default) or only new mail. A reconnect without it keeps the mailbox's own."`
	}
}

type oauthStartOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		URL string `json:"url"`
	}
}

func (s *service) registerOAuthRoutes(api huma.API, r chi.Router) {
	huma.Register(api, huma.Operation{OperationID: "start-mailbox-oauth", Method: http.MethodPost,
		Path: "/v1/mailboxes/oauth/{provider}/start", Summary: "Start connecting a Google or Microsoft mailbox",
		Description: "Returns the provider URL to open in the same browser. The call sets a short-lived cookie, so send it with credentials.",
		Tags:        []string{"Mailboxes"}},
		func(ctx context.Context, in *oauthStartInput) (*oauthStartOutput, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, "")
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if p.Kind != auth.KindUser {
				return nil, httpx.Forbidden()
			}
			b := in.Body
			a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if b.Receive != "api" && b.MailboxID == "" && !s.providers[in.Provider].configured() {
				return nil, httpx.E(422, "provider_not_configured", providerLabel(in.Provider)+" sign-in is not configured on this instance.")
			}
			if b.Receive == "sync" && !s.mailboxIMAP {
				return nil, imapOff()
			}
			grant := GrantSend
			switch b.Receive {
			case "sync":
				grant = GrantSync
			case "api":
				grant = GrantAPI
			}
			data := map[string]string{"flow": oauthFlow, "provider": in.Provider, "wid": p.WorkspaceID.String()}
			if b.MailboxID != "" {
				id, ok := ids.Parse(b.MailboxID)
				if !ok {
					return nil, httpx.NotFound("Mailbox")
				}
				m, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, id)
				if err != nil {
					return nil, wrapMailboxErr(err)
				}
				// A reconnect keeps how the mailbox receives, or moves it to reading through
				// the provider's API when asked. One that reads through the API while the
				// workspace no longer may, or over IMAP while IMAP is off, moves to forwarding.
				switch {
				case b.Receive == "api" || (m.readsAPI() && m.InboundKind == in.Provider):
					grant = GrantAPI
					if !s.apiReceives(in.Provider, a) {
						if b.Receive == "api" {
							return nil, apiSyncUnavailable(in.Provider, a)
						}
						grant = GrantSend
					}
				case receivesIMAP(m.InboundKind) && !m.readsAPI() && s.mailboxIMAP:
					grant = GrantSync
				default:
					grant = GrantSend
				}
				// Reconnecting a mailbox that already uses this sign-in is always allowed;
				// moving one onto it is a new connection.
				same := m.oauthProvider() == in.Provider && (grant != GrantAPI || m.Config.OAuthClient == ClientCollab)
				if !same && !s.connectAllowed(in.Provider, grant) {
					if grant == GrantAPI {
						return nil, apiSyncUnavailable(in.Provider, a)
					}
					return nil, oauthDisabled(in.Provider)
				}
				data["mailbox_id"] = id.String()
				if b.Email == "" {
					b.Email = m.Email
				}
			} else {
				if grant == GrantAPI && !s.apiSync(in.Provider, a) {
					return nil, apiSyncUnavailable(in.Provider, a)
				}
				if !s.connectAllowed(in.Provider, grant) {
					return nil, oauthDisabled(in.Provider)
				}
				if err := s.d.Plan.CheckMailbox(ctx, s.d.DB.Pool, p.WorkspaceID); err != nil {
					return nil, httpx.Wrap(err)
				}
				name := b.Name
				if strings.TrimSpace(name) == "" {
					name = providerLabel(in.Provider) + " mailbox"
				}
				if name, err = displayname.Validate("Name", name, displayname.Workspace, false); err != nil {
					return nil, httpx.Wrap(err)
				}
				data["name"] = name
			}
			prov := s.client(in.Provider, grant)
			if !prov.configured() {
				if grant == GrantAPI {
					return nil, apiSyncUnavailable(in.Provider, a)
				}
				return nil, httpx.E(422, "provider_not_configured", providerLabel(in.Provider)+" sign-in is not configured on this instance.")
			}
			data["grant"] = grant
			if b.SyncStart != "" {
				data["sync_start"] = b.SyncStart
			}
			verifier := oauth2.GenerateVerifier()
			if data["verifier"], err = s.sealVerifier(verifier); err != nil {
				return nil, httpx.Wrap(err)
			}
			nonce := crypto.RandomToken(32)
			data["nonce"] = hex.EncodeToString(crypto.HashToken(nonce))
			state, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeOAuthState, Data: data}, p.UserID.String(), auth.OAuthStateTTL)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			hint := strings.TrimSpace(b.Email)
			if !validAddress(strings.ToLower(hint)) {
				hint = ""
			}
			out := &oauthStartOutput{SetCookie: s.stateCookie(nonce, int(auth.OAuthStateTTL.Seconds()))}
			out.Body.URL = s.authCodeURL(prov, grant, state, verifier, hint)
			return out, nil
		})

	r.Get("/v1/mailboxes/oauth/{provider}/callback", s.oauthCallback)
}

// systemSendOff answers a request to send through the platform mailer while this
// instance has it off.
func systemSendOff() error {
	return httpx.E(http.StatusUnprocessableEntity, "system_send_off",
		"Sending through opendesk is off on this instance. Connect Google or Microsoft, or add SMTP.")
}

// connectAllowed reports whether a new connection with provider's client for grant is
// allowed: the Collab client for an api grant (apiConnect), else the mailbox sign-in.
func (s *service) connectAllowed(provider, grant string) bool {
	if grant == GrantAPI {
		return s.apiConnect(provider)
	}
	return s.oauthConnect(provider)
}

// oauthDisabled answers a new connection with a sign-in this instance does not offer
// for mailboxes (GOOGLE_MAILBOX_OAUTH, MICROSOFT_MAILBOX_OAUTH).
func oauthDisabled(provider string) error {
	return httpx.E(http.StatusForbidden, "mailbox_oauth_disabled", "Connecting a new mailbox with "+providerLabel(provider)+
		" sign-in is not available on this instance. Connect it with an app password over IMAP and SMTP, or forward its mail into opendesk.")
}

func (s *service) stateCookie(value string, maxAge int) http.Cookie {
	return http.Cookie{Name: oauthCookie, Value: value, Path: oauthCookiePath, Domain: s.d.Cfg.CookieDomain,
		MaxAge: maxAge, HttpOnly: true, Secure: s.d.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
}

func (s *service) settingsURL(q url.Values) string {
	return strings.TrimRight(s.d.Cfg.AppURL, "/") + "/settings/mailboxes?" + q.Encode()
}

// oauthCallback finishes the provider flow and redirects back to the app.
func (s *service) oauthCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	fail := func(code string) {
		http.Redirect(w, r, s.settingsURL(url.Values{"error": {code}}), http.StatusFound)
	}
	clear := s.stateCookie("", -1)
	http.SetCookie(w, &clear)

	if s.providers[provider] == nil {
		fail("provider_not_configured")
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
	if e := q.Get("error"); e != "" {
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
	var role string
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2`, wid, uid).Scan(&role); err != nil ||
		!auth.Role(role).AtLeast(auth.RoleAdmin) {
		fail("forbidden")
		return
	}
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid); err != nil || suspended {
		fail("forbidden")
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
	// A state signed before grants were recorded asked for the sync scopes.
	grant := claims.Data["grant"]
	if grant != GrantSend && grant != GrantAPI {
		grant = GrantSync
	}
	prov := s.client(provider, grant)
	if !prov.configured() {
		fail("provider_not_configured")
		return
	}
	creds, email, username, err := s.completeOAuth(ctx, prov, grant, code, verifier)
	if err != nil {
		s.log.Warn("mailbox oauth exchange failed", "provider", provider, "workspace_id", wid, "err", safeError(err))
		fail("provider_error")
		return
	}
	p := &auth.Principal{Kind: auth.KindUser, UserID: uid, WorkspaceID: wid, Role: auth.Role(role),
		IP: httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(r), UserAgent: truncate(r.UserAgent(), 400)}
	var mailboxID *uuid.UUID
	if v := claims.Data["mailbox_id"]; v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			fail("oauth_state")
			return
		}
		mailboxID = &id
	}
	// An api connection names the mailbox's sender as the account does, when it has no name yet.
	var names providerNames
	if grant == GrantAPI {
		names = s.fromNames(ctx, provider, creds, email)
	}
	id, err := s.saveOAuthMailbox(ctx, p, provider, grant, mailboxID, claims.Data["name"], claims.Data["sync_start"], email, username, creds, names)
	if err != nil {
		switch {
		case db.IsUniqueViolation(err, "mailboxes_workspace_id_email_key"):
			fail("mailbox_conflict")
		case db.IsNotFound(err):
			fail("mailbox_not_found")
		case errors.Is(err, plan.ErrLimit):
			fail("plan_limit")
		default:
			s.log.Error("save oauth mailbox", "workspace_id", wid, "err", err)
			fail("internal")
		}
		return
	}
	// Gmail's send-as addresses, when the connection may read them: which aliases send as
	// themselves, and which addresses the mailbox page offers to add. Best effort.
	if provider == KindGoogle && (grant == GrantSync || grant == GrantAPI) {
		if err := s.checkSendAs(ctx, nil, wid, id); err != nil {
			s.log.Info("gmail send-as list not read", "workspace_id", wid, "mailbox_id", id, "reason", safeError(err))
		}
	}
	http.Redirect(w, r, s.settingsURL(url.Values{"connected": {id.String()}}), http.StatusFound)
}

// completeOAuth redeems the code and reads the account identity. For a Microsoft sync
// grant the code yields the IMAP (outlook.office.com) token; the refresh token is then
// exchanged for a Graph token, which also proves the send consent was granted. A
// Microsoft send or api grant redeems the code for the Graph token alone.
func (s *service) completeOAuth(ctx context.Context, p *oauthProvider, grant, code, verifier string) (credentials, string, string, error) {
	tr, err := s.exchangeCode(ctx, p, grant, code, verifier)
	if err != nil {
		return credentials{}, "", "", err
	}
	if tr.RefreshToken == "" {
		return credentials{}, "", "", errors.New("the provider did not grant offline access")
	}
	now := timeNow()
	c := credentials{RefreshToken: tr.RefreshToken, AccessToken: tr.AccessToken, Expiry: expiryOf(tr, now)}
	identityToken := tr.AccessToken
	if p.name == KindMicrosoft && (grant == GrantSend || grant == GrantAPI) {
		c.AccessToken, c.Expiry = "", time.Time{}
		c.GraphAccessToken, c.GraphExpiry = tr.AccessToken, expiryOf(tr, now)
	} else if p.name == KindMicrosoft {
		g, err := s.refresh(ctx, p, c.RefreshToken, p.graphScope)
		if err != nil {
			return credentials{}, "", "", err
		}
		if g.RefreshToken != "" {
			c.RefreshToken = g.RefreshToken
		}
		c.GraphAccessToken, c.GraphExpiry = g.AccessToken, expiryOf(g, now)
		identityToken = g.AccessToken
	}
	email, username, err := s.oauthIdentity(ctx, p, identityToken)
	if err != nil {
		return credentials{}, "", "", err
	}
	return c, email, username, nil
}

// saveOAuthMailbox creates the mailbox, or updates the one being reconnected (or the
// one with the same address), and returns its id. A sync grant receives over IMAP
// while this instance has it on; an api grant reads the inbox through the provider's
// API while the workspace may; otherwise the mailbox receives by forwarding and sends
// through the provider. A mailbox without a display name takes the one names gives its
// address (an api connection's send-as name); a reconnect that keeps reading through the
// API keeps the mailbox's tidy-up options.
func (s *service) saveOAuthMailbox(ctx context.Context, p *auth.Principal, provider, grant string, mailboxID *uuid.UUID,
	name, syncStart, email, username string, creds credentials, names providerNames) (uuid.UUID, error) {
	prov := s.providers[provider]
	inbound := InboundForwarding
	cfg := MailboxConfig{OAuthEmail: email, OAuthGrant: grant, MailHost: string(mailhost.ForMailbox("", provider, "", email)), SyncStart: syncStart}
	switch grant {
	case GrantSync:
		if s.mailboxIMAP {
			inbound = provider
			cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPTLS, cfg.IMAPUsername = prov.imapHost, 993, TLSImplicit, username
		}
	case GrantAPI:
		cfg.OAuthClient = ClientCollab
		a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
		if err != nil {
			return uuid.Nil, err
		}
		if s.apiReceives(provider, a) {
			inbound = provider
		}
	}
	sealed, err := s.sealCreds(creds)
	if err != nil {
		return uuid.Nil, err
	}
	wid := p.WorkspaceID
	var id uuid.UUID
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var existing *mailboxRow
		var err error
		if mailboxID != nil {
			existing, err = scanMailbox(tx.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, wid, *mailboxID))
			if err != nil {
				return err
			}
		} else {
			existing, err = scanMailbox(tx.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE workspace_id = $1 AND email = $2 FOR UPDATE`, wid, email))
			if err != nil && !db.IsNotFound(err) {
				return err
			}
		}
		typ := "mailbox.updated"
		if existing != nil {
			id = existing.ID
			// A reconnect keeps the mailbox's own address unless it was the old account's
			// (a row from before the account was recorded is one this sign-in synced).
			// Connecting a provider to send for an address keeps that address.
			addr := existing.Email
			legacy := existing.Config.OAuthEmail == "" && existing.InboundKind == provider
			if legacy || (existing.Config.OAuthEmail != "" && strings.EqualFold(existing.Config.OAuthEmail, existing.Email)) {
				addr = email
			}
			if cfg.SyncStart == "" {
				cfg.SyncStart = existing.Config.SyncStart
			}
			resetCursor := !strings.EqualFold(existing.Config.IMAPUsername, cfg.IMAPUsername) || existing.InboundKind != inbound
			// The API cursor is the account's: another account, or a mailbox that did not
			// read through the API, starts over.
			resetAPI := !existing.readsAPI() || !strings.EqualFold(existing.Config.OAuthEmail, email) || existing.InboundKind != inbound
			if grant == GrantAPI && inbound == provider && existing.readsAPI() {
				cfg.ProviderMarkRead = existing.Config.ProviderMarkRead
				cfg.ProviderArchiveOnSolve = existing.Config.ProviderArchiveOnSolve
				cfg.ProviderLabel = existing.Config.ProviderLabel
			}
			// What the provider said about sending as each alias was about the account it said
			// it for: another account or provider starts from unknown.
			aliasSettings := existing.AliasSettings
			if existing.oauthProvider() != provider || !strings.EqualFold(existing.Config.OAuthEmail, email) {
				aliasSettings = forgetProviderSendAs(existing.AliasSettings)
			}
			_, err = tx.Exec(ctx, `UPDATE mailboxes SET email = $3, inbound_kind = $4, outbound_kind = $5, config = $6,
				credentials_enc = $7, status = 'active', last_error = '', sync_failures = 0, next_sync_at = NULL,
				imap_uid_validity = CASE WHEN $8 THEN 0 ELSE imap_uid_validity END,
				imap_last_uid = CASE WHEN $8 THEN 0 ELSE imap_last_uid END,
				api_sync_state = CASE WHEN $9 THEN '{}'::jsonb ELSE api_sync_state END,
				display_name = CASE WHEN btrim(display_name) = '' THEN $10 ELSE display_name END, alias_settings = $11, updated_at = now()
				WHERE workspace_id = $1 AND id = $2`, wid, id, addr, inbound, provider, cfg, sealed, resetCursor, resetAPI, names.forAddress(addr), aliasSettings)
			if err != nil {
				return err
			}
		} else {
			typ = "mailbox.created"
			if err := s.d.Plan.CheckMailbox(ctx, tx, wid); err != nil {
				return err
			}
			var slug string
			if err := tx.QueryRow(ctx, `SELECT slug FROM workspaces WHERE id = $1`, wid).Scan(&slug); err != nil {
				return err
			}
			m := &mailboxRow{WorkspaceID: wid}
			m.ID = ids.New()
			m.Name, m.Email, m.InboundKind, m.OutboundKind, m.Config, m.Status = name, email, inbound, provider, cfg, "active"
			m.DisplayName = names.forAddress(email)
			if m.Name == "" {
				m.Name = providerLabel(provider) + " mailbox"
			}
			// A connected mailbox starts in the default brand.
			if err := checkAddressing(ctx, tx, m); err != nil {
				return err
			}
			if err := s.insertMailbox(ctx, tx, m, slug, sealed); err != nil {
				return err
			}
			id = m.ID
		}
		if err := s.publishMailbox(ctx, tx, wid, id, typ); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "mailbox.connected", "mailbox", &id, map[string]any{
			"provider": provider, "email": email, "reconnect": existing != nil, "grant": grant, "inbound_kind": inbound}); err != nil {
			return err
		}
		m, err := loadMailbox(ctx, tx, wid, id)
		if err != nil {
			return err
		}
		return s.enqueueSync(ctx, tx, m)
	})
	return id, err
}
