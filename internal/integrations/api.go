package integrations

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/oauth2"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

const (
	oauthCookie     = "od_integration_oauth"
	oauthCookiePath = "/v1/integrations/oauth"
	oauthFlow       = "integration"
	// maxPerWorkspace bounds a workspace's connections.
	maxPerWorkspace = 25
)

// Integration is a workspace's connection to one MCP server. Credentials never leave
// the server; custom URLs are shown by host only, since a path can carry a secret.
type Integration struct {
	ID        uuid.UUID     `json:"id"`
	Provider  string        `json:"provider"`
	Name      string        `json:"name"`
	Host      string        `json:"host"`
	URL       string        `json:"url,omitempty" doc:"Catalog integrations only"`
	AuthType  string        `json:"auth_type" enum:"oauth,token,none"`
	Status    string        `json:"status" enum:"pending,connected,error"`
	Enabled   bool          `json:"enabled"`
	LastError string        `json:"last_error"`
	Icon      domain.Avatar `json:"icon_url" doc:"Custom servers: the server's own icon, else its site's. Null when none; the app draws catalog products"`
	Tools     []ToolInfo    `json:"tools"`
	Starters  []Starter     `json:"starters"`
	SyncedAt  *time.Time    `json:"synced_at"`
	CreatedAt time.Time     `json:"created_at"`
}

// ToolInfo is one of a connection's tools as settings shows it.
type ToolInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"read_only" doc:"Runs without approval: the server marks it read-only and it is not on ask_tools"`
	// ReadOnlyHint is what the server itself says, whatever the workspace decided.
	ReadOnlyHint bool `json:"read_only_hint" doc:"The server marks the tool read-only (its readOnlyHint)"`
	AskFirst     bool `json:"ask_first" doc:"An admin set it to wait for approval (ask_tools)"`
	AutoRun      bool `json:"auto_run" doc:"A write that runs without the user's approval: on auto_tools and not on ask_tools"`
	Enabled      bool `json:"enabled"`
}

func (x *row) api() Integration {
	out := Integration{ID: x.ID, Provider: x.Provider, Name: x.Name, Host: hostOf(x.URL), AuthType: x.AuthType, Status: x.Status,
		Enabled: x.Enabled, LastError: x.LastError, Icon: x.Icon, Tools: []ToolInfo{}, Starters: []Starter{}, SyncedAt: x.SyncedAt, CreatedAt: x.CreatedAt}
	if p, ok := provider(x.Provider); ok {
		out.URL = x.URL
		out.Starters = p.Starters
	}
	for _, t := range x.Tools {
		out.Tools = append(out.Tools, ToolInfo{Name: t.Name, Title: firstNonEmpty(t.Title, humanize(t.Name)), Description: t.Description,
			ReadOnly: x.readOnly(t), ReadOnlyHint: t.ReadOnly, AskFirst: slices.Contains(x.AskTools, t.Name), AutoRun: x.autoRun(t),
			Enabled: !slices.Contains(x.DisabledTools, t.Name)})
	}
	return out
}

type connectOutput struct {
	SetCookie    []http.Cookie `header:"Set-Cookie"`
	CacheControl string        `header:"Cache-Control"`
	Body         struct {
		Integration  Integration `json:"integration"`
		AuthorizeURL string      `json:"authorize_url,omitempty" doc:"Open in the same browser to finish signing in"`
	}
}

func requireAdminUser(ctx context.Context, scope string) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, scope)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if p.Kind != auth.KindUser {
		return nil, httpx.Forbidden() // signing in to another service needs a person in a browser
	}
	return p, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, ok := ids.Parse(raw)
	if !ok {
		return uuid.Nil, httpx.NotFound("Integration")
	}
	return id, nil
}

// wrapLoad answers a failed load of an integration: 404, or a logged 500.
func wrapLoad(err error) error {
	if db.IsNotFound(err) {
		return httpx.NotFound("Integration")
	}
	return httpx.Wrap(err)
}

// internalErr reports an error of opendesk's own storage, whose text is never shown.
func internalErr(err error) bool {
	var pgErr *pgconn.PgError
	var connectErr *pgconn.ConnectError
	return errors.As(err, &pgErr) || errors.As(err, &connectErr)
}

// remoteFailed answers a failed first connection to the product with what went wrong.
func remoteFailed(prefix string, err error) error {
	if internalErr(err) {
		return httpx.Wrap(err)
	}
	return httpx.E(422, "integration_failed", prefix+clip(err.Error(), 300))
}

// wrapRemote answers a failed call to the connected product. Only what the product said
// or how reaching it failed is shown; the database's own errors stay a logged 500.
func wrapRemote(err error) error {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he
	}
	if db.IsNotFound(err) {
		return httpx.NotFound("Integration")
	}
	if errors.Is(err, context.Canceled) || internalErr(err) {
		return httpx.Wrap(err)
	}
	return httpx.E(http.StatusBadGateway, "integration_failed", "Couldn't reach the server: "+clip(err.Error(), 300))
}

func (s *Service) publish(ctx context.Context, tx pgx.Tx, typ string, wid uuid.UUID, data map[string]any) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Data: data})
	return err
}

// publishTicket publishes an event about a ticket, scoped to its team when that is private.
func (s *Service) publishTicket(ctx context.Context, tx pgx.Tx, typ string, wid, ticketID uuid.UUID, data map[string]any) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Data: data, TicketID: &ticketID})
	return err
}

func (s *Service) registerRoutes(api huma.API, r chi.Router) {
	tags := []string{"Integrations"}

	huma.Register(api, huma.Operation{OperationID: "list-integration-catalog", Method: http.MethodGet, Path: "/v1/integrations/catalog",
		Summary: "List the integrations that connect in one step", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []Provider `json:"data"`
			}
		}, error) {
			if _, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead); err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []Provider `json:"data"`
				}
			}{}
			out.Body.Data = catalog()
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-integrations", Method: http.MethodGet, Path: "/v1/integrations",
		Summary: "List the workspace's integrations", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []Integration `json:"data"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			rows, err := listRows(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []Integration `json:"data"`
				}
			}{}
			out.Body.Data = []Integration{}
			for _, x := range rows {
				out.Body.Data = append(out.Body.Data, x.api())
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-integration", Method: http.MethodPost, Path: "/v1/integrations",
		Summary: "Connect an integration", Tags: tags, DefaultStatus: http.StatusCreated,
		Description: "Catalog providers need only `provider` (and `token` for token sign-in). OAuth answers an `authorize_url` to open in the same browser; the call sets a short-lived cookie, so send it with credentials."},
		func(ctx context.Context, in *struct {
			Body struct {
				Provider string `json:"provider" maxLength:"40"`
				Name     string `json:"name,omitempty" maxLength:"64"`
				URL      string `json:"url,omitempty" maxLength:"2000" doc:"Custom servers only"`
				Auth     string `json:"auth,omitempty" enum:"oauth,token,none" doc:"Custom servers only"`
				Token    string `json:"token,omitempty" maxLength:"4096"`
			}
		}) (*connectOutput, error) {
			p, err := requireAdminUser(ctx, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, err
			}
			if !s.d.Limiter.Allow("integration:create:"+p.WorkspaceID.String(), 30, time.Hour) {
				return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many connection attempts. Try again later.")
			}
			b := in.Body
			key := strings.TrimSpace(b.Provider)
			x := &row{WorkspaceID: p.WorkspaceID, Provider: key, Enabled: true, Status: "pending"}
			if prov, ok := provider(key); ok {
				x.Name, x.URL, x.AuthType = prov.Name, prov.URL, prov.Auth
			} else if key == CustomKey {
				if x.URL, err = s.guard.ValidateURL(ctx, b.URL); err != nil {
					return nil, httpx.E(422, "invalid_url", err.Error())
				}
				x.AuthType = b.Auth
				if x.AuthType == "" {
					x.AuthType = "oauth"
				}
				x.Name = hostOf(x.URL)
			} else {
				return nil, httpx.Validation("Unknown provider: " + clip(key, 40))
			}
			if strings.TrimSpace(b.Name) != "" {
				if x.Name, err = displayname.Validate("Name", b.Name, displayname.Workspace, false); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			token := strings.TrimSpace(b.Token)
			if x.AuthType == "token" {
				if token == "" || strings.ContainsAny(token, " \r\n\t") {
					return nil, httpx.Validation("Paste the access token.")
				}
				if x.CredentialsEnc, err = s.d.Box.SealJSON(tokens{Token: token}); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			var n int
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM integrations WHERE workspace_id = $1`, p.WorkspaceID).Scan(&n); err != nil {
				return nil, httpx.Wrap(err)
			}
			if n >= maxPerWorkspace {
				return nil, httpx.Conflict("A workspace can have at most 25 integrations.")
			}

			out := &connectOutput{CacheControl: "no-store"}
			var client *oauthClient
			if x.AuthType == "oauth" {
				asm, prm, scope, err := s.discover(ctx, x.URL)
				if errors.Is(err, errNoAuthNeeded) {
					x.AuthType = "none"
				} else if err != nil {
					return nil, remoteFailed("Couldn't start the sign-in: ", err)
				} else if client, err = s.identify(ctx, x.Provider, asm); err != nil {
					return nil, remoteFailed("", err)
				} else {
					client.Resource, client.Scope = canonicalResource(x.URL, prm), scope
					if x.OAuthEnc, err = s.d.Box.SealJSON(client); err != nil {
						return nil, httpx.Wrap(err)
					}
				}
			}
			if x.AuthType != "oauth" {
				// Token and open servers are checked now: a wrong token is better caught here.
				x.ID = ids.New()
				if err := s.sync(ctx, x); err != nil {
					s.forget(x.ID)
					return nil, remoteFailed("Couldn't connect: ", err)
				}
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if x.Slug, err = slugFor(ctx, tx, p.WorkspaceID, firstNonEmpty(map[bool]string{true: x.Provider}[x.Provider != CustomKey], x.Name)); err != nil {
					return err
				}
				if x.ID == uuid.Nil {
					x.ID = ids.New()
				}
				raw, _ := json.Marshal(x.Tools)
				if _, err := tx.Exec(ctx, `INSERT INTO integrations (id, workspace_id, provider, slug, name, url, auth_type, status, credentials_enc,
					oauth_enc, tools, synced_at, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
					x.ID, p.WorkspaceID, x.Provider, x.Slug, x.Name, x.URL, x.AuthType, x.Status, x.CredentialsEnc, x.OAuthEnc, raw, x.SyncedAt,
					p.ActorUserID()); err != nil {
					return err
				}
				saved, err := loadRow(ctx, tx, p.WorkspaceID, x.ID)
				if err != nil {
					return err
				}
				*x = *saved
				if err := s.publish(ctx, tx, "integration.created", p.WorkspaceID, map[string]any{"integration": x.api()}); err != nil {
					return err
				}
				if err := s.enqueueIcon(ctx, tx, x); err != nil {
					return err
				}
				if x.Status == "connected" {
					return audit.Log(ctx, tx, p, p.WorkspaceID, "integration.connected", "integration", &x.ID,
						map[string]any{"provider": x.Provider, "host": hostOf(x.URL), "auth": x.AuthType})
				}
				return nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out.Body.Integration = x.api()
			if client != nil {
				var ck http.Cookie
				if out.Body.AuthorizeURL, ck, err = s.beginSignIn(p, x, client); err != nil {
					return nil, httpx.Wrap(err)
				}
				out.SetCookie = []http.Cookie{ck}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "authorize-integration", Method: http.MethodPost, Path: "/v1/integrations/{id}/authorize",
		Summary: "Sign in to an integration again", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*connectOutput, error) {
			p, err := requireAdminUser(ctx, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			x, err := loadRow(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapLoad(err)
			}
			if x.AuthType != "oauth" {
				return nil, httpx.Validation("This integration signs in with a token. Update the token instead.")
			}
			asm, prm, scope, err := s.discover(ctx, x.URL)
			if err != nil {
				return nil, remoteFailed("Couldn't start the sign-in: ", err)
			}
			client, err := s.identify(ctx, x.Provider, asm)
			if err != nil {
				return nil, remoteFailed("", err)
			}
			client.Resource, client.Scope = canonicalResource(x.URL, prm), scope
			sealed, err := s.d.Box.SealJSON(client)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE integrations SET oauth_enc = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, x.ID, sealed); err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &connectOutput{CacheControl: "no-store"}
			out.Body.Integration = x.api()
			var ck http.Cookie
			if out.Body.AuthorizeURL, ck, err = s.beginSignIn(p, x, client); err != nil {
				return nil, httpx.Wrap(err)
			}
			out.SetCookie = []http.Cookie{ck}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "sync-integration", Method: http.MethodPost, Path: "/v1/integrations/{id}/sync",
		Summary: "Fetch an integration's tools again", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body Integration }, error) {
			p, err := requireAdminUser(ctx, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			x, err := loadRow(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapLoad(err)
			}
			if err := s.sync(ctx, x); err != nil {
				return nil, wrapRemote(err)
			}
			if err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := s.publish(ctx, tx, "integration.updated", p.WorkspaceID, map[string]any{"integration": x.api()}); err != nil {
					return err
				}
				return s.enqueueIcon(ctx, tx, x)
			}); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Integration }{x.api()}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-integration", Method: http.MethodPatch, Path: "/v1/integrations/{id}",
		Summary: "Update an integration", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Name          *string  `json:"name,omitempty" maxLength:"64"`
				Enabled       *bool    `json:"enabled,omitempty"`
				DisabledTools []string `json:"disabled_tools,omitempty" maxItems:"200" doc:"Tools Ollie may not use; replaces the list"`
				AskTools      []string `json:"ask_tools,omitempty" maxItems:"200" doc:"Tools that wait for approval even when their server marks them read-only; replaces the list"`
				AutoTools     []string `json:"auto_tools,omitempty" maxItems:"200" doc:"Writes that run without the user's approval; ask_tools wins over it. Replaces the list"`
				Token         *string  `json:"token,omitempty" maxLength:"4096" doc:"Token sign-in only"`
			}
		}) (*struct{ Body Integration }, error) {
			p, err := requireAdminUser(ctx, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			x, err := loadRow(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapLoad(err)
			}
			b := in.Body
			changed := []string{}
			if b.Name != nil {
				if x.Name, err = displayname.Validate("Name", *b.Name, displayname.Workspace, false); err != nil {
					return nil, httpx.Wrap(err)
				}
				changed = append(changed, "name")
			}
			if b.Enabled != nil {
				x.Enabled = *b.Enabled
				changed = append(changed, "enabled")
			}
			if b.DisabledTools != nil {
				x.DisabledTools = x.knownTools(b.DisabledTools)
				changed = append(changed, "tools")
			}
			if b.AskTools != nil {
				x.AskTools = x.knownTools(b.AskTools)
				changed = append(changed, "ask_tools")
			}
			if b.AutoTools != nil {
				x.AutoTools = x.knownTools(b.AutoTools)
				changed = append(changed, "auto_tools")
			}
			if b.Token != nil {
				if x.AuthType != "token" {
					return nil, httpx.Validation("This integration does not sign in with a token.")
				}
				tok := strings.TrimSpace(*b.Token)
				if tok == "" || strings.ContainsAny(tok, " \r\n\t") {
					return nil, httpx.Validation("Paste the access token.")
				}
				sealed, err := s.d.Box.SealJSON(tokens{Token: tok})
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				// The new token is tried on a copy first; a wrong one leaves the connection as it was.
				probe := *x
				probe.ID, probe.CredentialsEnc = ids.New(), sealed
				err = s.sync(ctx, &probe)
				s.forget(probe.ID)
				if err != nil {
					return nil, remoteFailed("Couldn't connect with this token: ", err)
				}
				s.forget(x.ID)
				x.CredentialsEnc, x.Tools, x.Status, x.LastError, x.SyncedAt = sealed, probe.Tools, probe.Status, probe.LastError, probe.SyncedAt
				changed = append(changed, "token")
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				raw, _ := json.Marshal(x.Tools)
				if _, err := tx.Exec(ctx, `UPDATE integrations SET name = $3, enabled = $4, disabled_tools = $5, credentials_enc = $6,
					tools = $7, status = $8, last_error = $9, synced_at = $10, ask_tools = $11, auto_tools = $12, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, x.ID, x.Name, x.Enabled, x.DisabledTools, x.CredentialsEnc, raw, x.Status, x.LastError, x.SyncedAt, x.AskTools, x.AutoTools); err != nil {
					return err
				}
				if err := s.publish(ctx, tx, "integration.updated", p.WorkspaceID, map[string]any{"integration": x.api()}); err != nil {
					return err
				}
				if b.Token != nil {
					if err := s.enqueueIcon(ctx, tx, x); err != nil {
						return err
					}
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "integration.updated", "integration", &x.ID,
					map[string]any{"provider": x.Provider, "changes": changed})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Integration }{x.api()}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-integration", Method: http.MethodDelete, Path: "/v1/integrations/{id}",
		Summary: "Disconnect an integration", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := requireAdminUser(ctx, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				x, err := loadRow(ctx, tx, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				// The row goes, credentials with it; links on tickets stay as plain links.
				if _, err := tx.Exec(ctx, `DELETE FROM integrations WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id); err != nil {
					return err
				}
				if err := s.publish(ctx, tx, "integration.deleted", p.WorkspaceID, map[string]any{"integration_id": id}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "integration.disconnected", "integration", &id,
					map[string]any{"provider": x.Provider, "host": hostOf(x.URL)})
			})
			if err != nil {
				return nil, wrapRemote(err)
			}
			s.forget(id)
			return &struct{}{}, nil
		})

	r.Get("/v1/integrations/oauth/callback", s.oauthCallback)
	r.Get("/v1/integrations/oauth/client", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(s.clientMetadata())
	})
}

// canonicalResource is the MCP server's URI for the resource parameter (RFC 8707):
// what its metadata names when that is the same server, else its URL without fragment.
func canonicalResource(server string, prm *resourceMetadata) string {
	u, err := url.Parse(server)
	if err != nil {
		return server
	}
	u.Fragment = ""
	if prm != nil && prm.Resource != "" {
		if r, err := url.Parse(prm.Resource); err == nil && strings.EqualFold(r.Host, u.Host) && r.Scheme == u.Scheme {
			return prm.Resource
		}
	}
	return u.String()
}

func (s *Service) stateCookie(value string, maxAge int) http.Cookie {
	return http.Cookie{Name: oauthCookie, Value: value, Path: oauthCookiePath, Domain: s.d.Cfg.CookieDomain,
		MaxAge: maxAge, HttpOnly: true, Secure: s.d.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
}

// beginSignIn signs the state (with the PKCE verifier sealed inside) and binds the flow
// to this browser with a nonce cookie, so a sign-in link handed to someone else cannot
// connect their account to this workspace.
func (s *Service) beginSignIn(p *auth.Principal, x *row, c *oauthClient) (string, http.Cookie, error) {
	verifier := oauth2.GenerateVerifier()
	sealed, err := s.d.Box.Seal([]byte(verifier))
	if err != nil {
		return "", http.Cookie{}, err
	}
	nonce := crypto.RandomToken(32)
	data := map[string]string{"flow": oauthFlow, "wid": p.WorkspaceID.String(), "iid": x.ID.String(),
		"verifier": hex.EncodeToString(sealed), "nonce": hex.EncodeToString(crypto.HashToken(nonce))}
	state, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeOAuthState, SessionID: p.SessionID.String(), Data: data},
		p.UserID.String(), auth.OAuthStateTTL)
	if err != nil {
		return "", http.Cookie{}, err
	}
	return c.authorizeURL(state, verifier), s.stateCookie(nonce, int(auth.OAuthStateTTL.Seconds())), nil
}

func (s *Service) settingsURL(q url.Values) string {
	return strings.TrimRight(s.d.Cfg.AppURL, "/") + "/settings/integrations?" + q.Encode()
}

// oauthCallback finishes a sign-in and sends the browser back to settings.
func (s *Service) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back := url.Values{}
	fail := func(code string) {
		back.Set("error", code)
		http.Redirect(w, r, s.settingsURL(back), http.StatusFound)
	}
	clear := s.stateCookie("", -1)
	http.SetCookie(w, &clear)

	claims, err := s.d.Tokens.Verify(q.Get("state"), auth.PurposeOAuthState)
	if err != nil || claims.Data["flow"] != oauthFlow {
		fail("oauth_state")
		return
	}
	cookie, err := r.Cookie(oauthCookie)
	if err != nil || cookie.Value == "" ||
		subtle.ConstantTimeCompare([]byte(hex.EncodeToString(crypto.HashToken(cookie.Value))), []byte(claims.Data["nonce"])) != 1 {
		fail("oauth_state")
		return
	}
	uid, err1 := uuid.Parse(claims.Subject)
	wid, err2 := uuid.Parse(claims.Data["wid"])
	iid, err3 := uuid.Parse(claims.Data["iid"])
	if err1 != nil || err2 != nil || err3 != nil {
		fail("oauth_state")
		return
	}
	back.Set("id", iid.String())
	if q.Get("error") != "" {
		fail("access_denied")
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
	x, err := loadRow(ctx, s.d.DB.Pool, wid, iid)
	if err != nil {
		fail("not_found")
		return
	}
	sealed, err := hex.DecodeString(claims.Data["verifier"])
	if err != nil {
		fail("oauth_state")
		return
	}
	verifier, err := s.d.Box.Open(sealed)
	if err != nil {
		fail("oauth_state")
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > 4096 {
		fail("provider_error")
		return
	}
	c, err := s.openClient(x)
	if err != nil {
		fail("oauth_state")
		return
	}
	// A response that names its issuer must name this connection's (RFC 9207), and one from
	// a server that says it names itself must: a code is only ever sent back to its issuer.
	if iss := q.Get("iss"); (iss != "" || c.IssParam) && !sameIssuer(iss, c.Issuer) {
		fail("oauth_state")
		return
	}
	t, err := s.exchange(ctx, c, code, string(verifier))
	if err != nil {
		s.d.Log.Warn("integration sign-in failed", "provider", x.Provider, "workspace_id", wid, "err", err.Error())
		fail("provider_error")
		return
	}
	if x.CredentialsEnc, err = s.d.Box.SealJSON(t); err != nil {
		fail("provider_error")
		return
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE integrations SET credentials_enc = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`, wid, x.ID, x.CredentialsEnc); err != nil {
		fail("provider_error")
		return
	}
	s.forget(x.ID)
	syncErr := s.sync(ctx, x)
	p := &auth.Principal{Kind: auth.KindUser, UserID: uid, WorkspaceID: wid, Role: auth.Role(role),
		IP: httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(r), UserAgent: clip(r.UserAgent(), 400)}
	_ = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.publish(ctx, tx, "integration.updated", wid, map[string]any{"integration": x.api()}); err != nil {
			return err
		}
		if syncErr == nil {
			if err := s.enqueueIcon(ctx, tx, x); err != nil {
				return err
			}
		}
		return audit.Log(ctx, tx, p, wid, "integration.connected", "integration", &x.ID,
			map[string]any{"provider": x.Provider, "host": hostOf(x.URL), "auth": "oauth", "client": c.RegisteredVia})
	})
	if syncErr != nil {
		fail("sync_failed")
		return
	}
	back.Set("connected", x.ID.String())
	http.Redirect(w, r, s.settingsURL(back), http.StatusFound)
}
