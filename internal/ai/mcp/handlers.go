package mcp

import (
	"context"
	"crypto/subtle"
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
	"golang.org/x/net/http/httpguts"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

type authBody struct {
	Kind        string `json:"kind" enum:"none,bearer,header,oauth"`
	Token       string `json:"token,omitempty" maxLength:"4096" doc:"Bearer token (kind bearer). Omit to keep the stored one."`
	HeaderName  string `json:"header_name,omitempty" maxLength:"100" doc:"Header name (kind header)"`
	HeaderValue string `json:"header_value,omitempty" maxLength:"4096" doc:"Header value (kind header). Omit to keep the stored one."`
}

type toolPatch struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	Policy       *string `json:"policy,omitempty" enum:"auto,confirm"`
	VerifiedOnly *bool   `json:"verified_only,omitempty"`
}

type serverOut struct{ Body McpServer }

type idPath struct {
	ID string `path:"id"`
}

func (r *Registry) validateURL(ctx context.Context, raw string) (string, error) {
	u, err := r.guard().ValidateURL(ctx, raw)
	if err != nil {
		return "", httpx.E(422, "invalid_url", err.Error())
	}
	return u, nil
}

func validName(name string) (string, error) {
	n, err := displayname.Validate("Name", name, displayname.Workspace, false)
	if err != nil {
		return "", err
	}
	if len([]rune(n)) > 60 {
		return "", httpx.Validation("Name must be 60 characters or less.")
	}
	return n, nil
}

// refusedHeaders cannot carry a credential: hop-by-hop headers, Host, Cookie, the
// Content-* family and the headers the MCP transport sets itself.
var refusedHeaders = []string{"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te",
	"trailer", "trailers", "transfer-encoding", "upgrade", "host", "cookie", "accept", "user-agent", "mcp-session-id",
	"mcp-protocol-version", "last-event-id"}

func validHeaderName(name string) error {
	l := strings.ToLower(name)
	if !httpguts.ValidHeaderFieldName(name) || slices.Contains(refusedHeaders, l) || strings.HasPrefix(l, "content-") {
		return httpx.Validation("Use a valid header name. Hop-by-hop, Host, Cookie and Content headers are not allowed.")
	}
	return nil
}

func validSecretValue(label, v string) error {
	if v != strings.TrimSpace(v) || !httpguts.ValidHeaderFieldValue(v) {
		return httpx.Validation(label + " contains characters that are not allowed in a header.")
	}
	return nil
}

// applyAuth applies an auth change to s (in memory) and reports whether the
// credentials changed.
func (r *Registry) applyAuth(s *server, in *authBody) (bool, error) {
	sec, err := s.open(r.d.Box)
	if err != nil {
		return false, err
	}
	prev := s.AuthKind
	changed := in.Kind != prev
	switch in.Kind {
	case AuthNone:
		sec = secrets{}
		s.HeaderName, s.OAuth = nil, oauthMeta{}
	case AuthBearer:
		if in.Token == "" && (prev != AuthBearer || sec.Token == "") {
			return false, httpx.Validation("Enter the bearer token.")
		}
		if in.Token != "" {
			if err := validSecretValue("The token", in.Token); err != nil {
				return false, err
			}
			changed = changed || in.Token != sec.Token
			sec = secrets{Token: in.Token}
		} else {
			sec = secrets{Token: sec.Token}
		}
		s.HeaderName, s.OAuth = nil, oauthMeta{}
	case AuthHeader:
		name := strings.TrimSpace(in.HeaderName)
		if name == "" && s.HeaderName != nil && prev == AuthHeader {
			name = *s.HeaderName
		}
		if name == "" {
			return false, httpx.Validation("Enter the header name.")
		}
		if err := validHeaderName(name); err != nil {
			return false, err
		}
		if in.HeaderValue == "" && (prev != AuthHeader || sec.HeaderValue == "") {
			return false, httpx.Validation("Enter the header value.")
		}
		value := sec.HeaderValue
		if in.HeaderValue != "" {
			if err := validSecretValue("The header value", in.HeaderValue); err != nil {
				return false, err
			}
			value = in.HeaderValue
		}
		changed = changed || value != sec.HeaderValue || s.HeaderName == nil || !strings.EqualFold(*s.HeaderName, name)
		sec = secrets{HeaderValue: value}
		s.HeaderName, s.OAuth = &name, oauthMeta{}
	case AuthOAuth:
		if prev != AuthOAuth {
			sec = secrets{}
			s.HeaderName, s.OAuth = nil, oauthMeta{}
		}
	default:
		return false, httpx.Validation("Unknown auth kind.")
	}
	s.AuthKind = in.Kind
	if !changed {
		return false, nil
	}
	if sec == (secrets{}) {
		s.SecretsEnc = nil
		return true, nil
	}
	sealed, err := r.d.Box.SealJSON(sec)
	if err != nil {
		return false, err
	}
	s.SecretsEnc = sealed
	return true, nil
}

// dropTokens forgets OAuth tokens (a new URL is a new resource); the client
// registration stays.
func (r *Registry) dropTokens(s *server) error {
	if s.AuthKind != AuthOAuth {
		return nil
	}
	sec, err := s.open(r.d.Box)
	if err != nil {
		return err
	}
	sec.AccessToken, sec.RefreshToken = "", ""
	s.OAuth.Connected, s.OAuth.ExpiresAt, s.OAuth.Scopes, s.OAuth.Resource = false, nil, nil, ""
	if sec == (secrets{}) {
		s.SecretsEnc = nil
		return nil
	}
	s.SecretsEnc, err = r.d.Box.SealJSON(sec)
	return err
}

// credentialOf is the header a saved or draft server sends, without refreshing.
func (r *Registry) credentialOf(s *server) (string, string) {
	sec, err := s.open(r.d.Box)
	if err != nil {
		return "", ""
	}
	switch s.AuthKind {
	case AuthBearer:
		if sec.Token != "" {
			return "Authorization", "Bearer " + sec.Token
		}
	case AuthHeader:
		if s.HeaderName != nil && sec.HeaderValue != "" {
			return *s.HeaderName, sec.HeaderValue
		}
	case AuthOAuth:
		if sec.AccessToken != "" {
			return "Authorization", "Bearer " + sec.AccessToken
		}
	}
	return "", ""
}

func limitErr(detail string) error { return httpx.E(422, "limit_reached", detail) }

func (r *Registry) limit(key string, n int, per time.Duration) error {
	if !r.d.Limiter.Allow(key, n, per) {
		return httpx.RateLimited()
	}
	return nil
}

func parseServerID(raw string) (uuid.UUID, error) {
	id, ok := ids.Parse(raw)
	if !ok {
		return uuid.Nil, httpx.NotFound("MCP server")
	}
	return id, nil
}

func wrapNotFound(err error) error {
	if isNotFound(err) {
		return httpx.NotFound("MCP server")
	}
	return httpx.Wrap(err)
}

func publishServer(ctx context.Context, tx pgx.Tx, typ string, s *server, view McpServer) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: s.WorkspaceID, Type: typ, Data: map[string]any{"mcp_server": view}})
	return err
}

func registerRoutes(r *Registry, api huma.API, router chi.Router) {
	tags := []string{"AI"}

	huma.Register(api, huma.Operation{OperationID: "list-mcp-servers", Method: http.MethodGet, Path: "/v1/ai/mcp-servers",
		Summary: "List MCP servers", Description: "The workspace's connected MCP servers and their tools.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []McpServer `json:"data"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := listServers(ctx, r.d.DB.Pool, p.WorkspaceID, false)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []McpServer `json:"data"`
				}
			}{}
			out.Body.Data = make([]McpServer, 0, len(list))
			for _, s := range list {
				out.Body.Data = append(out.Body.Data, s.view(r.d.Box))
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-mcp-server", Method: http.MethodPost, Path: "/v1/ai/mcp-servers",
		Summary: "Connect an MCP server", Description: "Connects and lists the server's tools. An OAuth-protected server answers status needs_auth.",
		Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				Name string    `json:"name" minLength:"1" maxLength:"60"`
				URL  string    `json:"url" maxLength:"2000"`
				Auth *authBody `json:"auth,omitempty"`
			}
		}) (*serverOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			wid := p.WorkspaceID
			if err := r.limit("mcp:write:"+wid.String(), 60, time.Hour); err != nil {
				return nil, err
			}
			name, err := validName(in.Body.Name)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			u, err := r.validateURL(ctx, in.Body.URL)
			if err != nil {
				return nil, err
			}
			var n int
			if err := r.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM ai_mcp_servers WHERE workspace_id = $1`, wid).Scan(&n); err != nil {
				return nil, httpx.Wrap(err)
			}
			if n >= MaxServers {
				return nil, limitErr("A workspace can connect at most 10 MCP servers.")
			}
			ab := in.Body.Auth
			if ab == nil {
				ab = &authBody{Kind: AuthNone}
			}
			s := &server{ID: ids.New(), WorkspaceID: wid, Name: name, URL: u, Enabled: true, AuthKind: AuthNone, Version: 1,
				Settings: map[string]toolSetting{}, Tools: []cachedTool{}, Status: StatusConnecting}
			if _, err := r.applyAuth(s, ab); err != nil {
				return nil, httpx.Wrap(err)
			}
			header, value := r.credentialOf(s)
			o := r.connectAndList(ctx, u, header, value, s.AuthKind)
			if o.err != nil && o.err.status == StatusNeedsAuth && s.AuthKind == AuthNone {
				s.AuthKind = AuthOAuth
			}
			var view McpServer
			err = r.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockWorkspace(ctx, tx, wid); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_mcp_servers WHERE workspace_id = $1`, wid).Scan(&n); err != nil {
					return err
				}
				if n >= MaxServers {
					return limitErr("A workspace can connect at most 10 MCP servers.")
				}
				if s.Slug, err = uniqueSlug(ctx, tx, wid, name); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `INSERT INTO ai_mcp_servers (id, workspace_id, name, slug, url, auth_kind, header_name, secrets_enc,
					oauth, status, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
					RETURNING created_at, (SELECT name FROM workspaces WHERE id = $2)`, s.ID, wid, name, s.Slug, u, s.AuthKind, s.HeaderName,
					s.SecretsEnc, s.OAuth, s.Status, p.ActorUserID()).Scan(&s.CreatedAt, &s.WorkspaceName); err != nil {
					return err
				}
				if _, err := r.mergeOutcome(ctx, tx, s, o); err != nil {
					return err
				}
				if err := saveServer(ctx, tx, s, true); err != nil {
					return err
				}
				view = s.view(r.d.Box)
				if err := publishServer(ctx, tx, "ai_mcp_server.created", s, view); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, wid, "ai.mcp_server_created", "ai_mcp_server", &s.ID,
					map[string]any{"name": name, "host": hostOf(u), "auth_kind": s.AuthKind, "status": s.Status})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &serverOut{view}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-mcp-server", Method: http.MethodPatch, Path: "/v1/ai/mcp-servers/{id}",
		Summary: "Update an MCP server", Description: "Rename, move, turn on or off, change credentials, or choose which tools the agent may use and how.",
		Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Name    *string              `json:"name,omitempty" minLength:"1" maxLength:"60"`
				URL     *string              `json:"url,omitempty" maxLength:"2000"`
				Enabled *bool                `json:"enabled,omitempty"`
				Auth    *authBody            `json:"auth,omitempty"`
				Tools   map[string]toolPatch `json:"tools,omitempty" doc:"Settings by tool name"`
			}
		}) (*serverOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			wid := p.WorkspaceID
			id, err := parseServerID(in.ID)
			if err != nil {
				return nil, err
			}
			if err := r.limit("mcp:write:"+wid.String(), 600, time.Hour); err != nil {
				return nil, err
			}
			if len(in.Body.Tools) > maxToolsListed {
				return nil, httpx.Validation("Too many tools in one change.")
			}
			cur, err := loadServer(ctx, r.d.DB.Pool, wid, id)
			if err != nil {
				return nil, wrapNotFound(err)
			}
			old := *cur
			draft := *cur
			var changes []string
			b := in.Body
			if b.Name != nil {
				n, err := validName(*b.Name)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if n != draft.Name {
					draft.Name = n
					changes = append(changes, "name")
				}
			}
			urlChanged := false
			if b.URL != nil {
				u, err := r.validateURL(ctx, *b.URL)
				if err != nil {
					return nil, err
				}
				if u != draft.URL {
					draft.URL, urlChanged = u, true
					changes = append(changes, "url")
					if err := r.dropTokens(&draft); err != nil {
						return nil, httpx.Wrap(err)
					}
				}
			}
			authChanged := false
			if b.Auth != nil {
				if authChanged, err = r.applyAuth(&draft, b.Auth); err != nil {
					return nil, httpx.Wrap(err)
				}
				if authChanged {
					changes = append(changes, "auth")
				}
			}
			if b.Enabled != nil && *b.Enabled != draft.Enabled {
				draft.Enabled = *b.Enabled
				changes = append(changes, "enabled")
			}
			var o *outcome
			if urlChanged || authChanged {
				header, value := r.credentialOf(&draft)
				res := r.connectAndList(ctx, draft.URL, header, value, draft.AuthKind)
				if res.err != nil && res.err.status == StatusNeedsAuth && draft.AuthKind == AuthNone {
					draft.AuthKind = AuthOAuth
				}
				o = &res
			}

			var view McpServer
			err = r.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockWorkspace(ctx, tx, wid); err != nil {
					return err
				}
				row, err := lockServer(ctx, tx, wid, id)
				if err != nil {
					return err
				}
				if (urlChanged || authChanged) && row.Version != cur.Version {
					return httpx.Conflict("The server changed while saving. Reload and try again.")
				}
				s := row
				if b.Name != nil {
					s.Name = draft.Name
				}
				if b.Enabled != nil {
					s.Enabled = draft.Enabled
				}
				if urlChanged || authChanged {
					s.URL, s.AuthKind, s.HeaderName, s.SecretsEnc, s.OAuth = draft.URL, draft.AuthKind, draft.HeaderName, draft.SecretsEnc, draft.OAuth
					s.Version++
					if urlChanged && o.err != nil {
						// Another address: the old tool list no longer applies (settings stay by name).
						s.Tools, s.ServerInfo = []cachedTool{}, nil
					}
					if _, err := r.mergeOutcome(ctx, tx, s, *o); err != nil {
						return err
					}
				}
				before := s.enabledTools()
				for name, tp := range b.Tools {
					t, ok := s.tool(name)
					if !ok {
						return httpx.E(422, "unknown_tool", "This server has no tool named "+truncateRunes(name, 64)+".")
					}
					st := s.setting(t)
					if tp.Enabled != nil {
						st.Enabled = *tp.Enabled
					}
					if tp.Policy != nil {
						if *tp.Policy != PolicyAuto && *tp.Policy != PolicyConfirm {
							return httpx.Validation("Policy is auto or confirm.")
						}
						st.Policy = *tp.Policy
					}
					if tp.VerifiedOnly != nil {
						st.VerifiedOnly = *tp.VerifiedOnly
					}
					s.Settings[name] = st
				}
				if len(b.Tools) > 0 {
					changes = append(changes, "tools")
				}
				if after := s.enabledTools(); after > before {
					used, err := workspaceEnabledTools(ctx, tx, wid, s.ID)
					if err != nil {
						return err
					}
					if used+after > MaxEnabledTools {
						return limitErr("A workspace can enable at most 64 MCP tools.")
					}
				}
				if err := saveServer(ctx, tx, s, true); err != nil {
					return err
				}
				view = s.view(r.d.Box)
				if err := publishServer(ctx, tx, "ai_mcp_server.updated", s, view); err != nil {
					return err
				}
				if changes == nil {
					changes = []string{}
				}
				if err := audit.Log(ctx, tx, p, wid, "ai.mcp_server_updated", "ai_mcp_server", &s.ID,
					map[string]any{"name": s.Name, "changes": changes, "host": hostOf(s.URL), "auth_kind": s.AuthKind}); err != nil {
					return err
				}
				if old.AuthKind == AuthOAuth && old.OAuth.Connected && (urlChanged || authChanged) {
					return audit.Log(ctx, tx, p, wid, "ai.mcp_server_disconnected", "ai_mcp_server", &s.ID,
						map[string]any{"name": s.Name, "reason": "settings_changed"})
				}
				return nil
			})
			if err != nil {
				return nil, wrapNotFound(err)
			}
			if urlChanged || authChanged || (b.Enabled != nil && !*b.Enabled) {
				r.forget(id)
			}
			if old.AuthKind == AuthOAuth && (urlChanged || (authChanged && draft.AuthKind != AuthOAuth)) {
				r.revoke(context.WithoutCancel(ctx), &old)
			}
			return &serverOut{view}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-mcp-server", Method: http.MethodDelete, Path: "/v1/ai/mcp-servers/{id}",
		Summary: "Remove an MCP server", Description: "Removes the server and revokes its OAuth tokens when the server supports revocation.",
		Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseServerID(in.ID)
			if err != nil {
				return nil, err
			}
			s, err := loadServer(ctx, r.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapNotFound(err)
			}
			r.revoke(ctx, s)
			err = r.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM ai_mcp_servers WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return pgx.ErrNoRows
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "ai_mcp_server.deleted",
					Data: map[string]any{"mcp_server_id": id}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "ai.mcp_server_deleted", "ai_mcp_server", &id,
					map[string]any{"name": s.Name, "host": hostOf(s.URL)})
			})
			if err != nil {
				return nil, wrapNotFound(err)
			}
			r.forget(id)
			return &struct{}{}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "sync-mcp-server", Method: http.MethodPost, Path: "/v1/ai/mcp-servers/{id}/sync",
		Summary: "Sync an MCP server", Description: "Reconnects and lists the tools again. Tool settings are kept by name.", Tags: tags},
		func(ctx context.Context, in *idPath) (*serverOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseServerID(in.ID)
			if err != nil {
				return nil, err
			}
			if err := r.limit("mcp:sync:"+p.WorkspaceID.String(), 60, time.Hour); err != nil {
				return nil, err
			}
			s, err := r.syncServer(ctx, p, p.WorkspaceID, id, true)
			if err != nil {
				return nil, wrapNotFound(err)
			}
			return &serverOut{s.view(r.d.Box)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "start-mcp-server-oauth", Method: http.MethodPost, Path: "/v1/ai/mcp-servers/{id}/oauth/start",
		Summary: "Start signing in to an MCP server", Description: "Returns the authorization URL to open in the browser. The callback returns to the app.",
		Tags: tags},
		func(ctx context.Context, in *idPath) (*struct {
			SetCookie http.Cookie `header:"Set-Cookie"`
			Body      struct {
				URL string `json:"url"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if p.Kind != auth.KindUser {
				return nil, httpx.E(403, "forbidden", "Signing in to an MCP server needs a person in a browser.")
			}
			id, err := parseServerID(in.ID)
			if err != nil {
				return nil, err
			}
			if err := r.limit("mcp:oauth:"+p.UserID.String(), 20, time.Minute); err != nil {
				return nil, err
			}
			s, err := loadServer(ctx, r.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapNotFound(err)
			}
			if s.AuthKind != AuthOAuth {
				return nil, httpx.E(409, "not_oauth", "This server does not use OAuth sign-in.")
			}
			authURL, nonce, err := r.startAuthorization(ctx, p.UserID, s)
			if err != nil {
				var oe *oauthError
				if errors.As(err, &oe) {
					return nil, httpx.E(422, oe.code, oe.detail)
				}
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				SetCookie http.Cookie `header:"Set-Cookie"`
				Body      struct {
					URL string `json:"url"`
				}
			}{SetCookie: r.stateCookie(nonce, int(oauthStateTTL.Seconds()))}
			out.Body.URL = authURL
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "test-mcp-tool", Method: http.MethodPost, Path: "/v1/ai/mcp-servers/{id}/tools/{name}/test",
		Summary: "Test an MCP tool", Description: "Runs the tool once with the given arguments, whatever its policy.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Name string `path:"name" maxLength:"128"`
			Body struct {
				Arguments map[string]any `json:"arguments"`
				Visitor   *struct {
					Name     string `json:"name,omitempty" maxLength:"100"`
					Email    string `json:"email,omitempty" maxLength:"254"`
					Verified bool   `json:"verified,omitempty"`
					UserID   string `json:"user_id,omitempty" maxLength:"200"`
				} `json:"visitor,omitempty"`
			}
		}) (*struct {
			Body struct {
				Content string `json:"content"`
				IsError bool   `json:"is_error"`
				MS      int64  `json:"ms"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseServerID(in.ID)
			if err != nil {
				return nil, err
			}
			actor := p.WorkspaceID.String()
			if p.Kind == auth.KindUser {
				actor = p.UserID.String()
			}
			if err := r.limit("mcp:test:"+actor, 30, time.Minute); err != nil {
				return nil, err
			}
			s, err := loadServer(ctx, r.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapNotFound(err)
			}
			if _, ok := s.tool(in.Name); !ok {
				return nil, httpx.NotFound("Tool")
			}
			var v Visitor
			if vi := in.Body.Visitor; vi != nil {
				v = Visitor{Verified: vi.Verified, UserID: vi.UserID, Email: vi.Email, Name: displayname.Clean(vi.Name, displayname.Person)}
			}
			args, _ := json.Marshal(in.Body.Arguments)
			start := time.Now()
			res, err := r.call(ctx, s, in.Name, args, v)
			out := &struct {
				Body struct {
					Content string `json:"content"`
					IsError bool   `json:"is_error"`
					MS      int64  `json:"ms"`
				}
			}{}
			out.Body.MS = time.Since(start).Milliseconds()
			if err != nil {
				if ctx.Err() != nil {
					return nil, httpx.Wrap(ctx.Err())
				}
				out.Body.Content, out.Body.IsError = safeCallError(err), true
				return out, nil
			}
			out.Body.Content, out.Body.IsError = res.Text, res.IsError
			return out, nil
		})

	router.Get("/v1/ai/mcp-servers/oauth/callback", r.oauthCallback)
}

// safeCallError is the message for an error Call returned.
func safeCallError(err error) string {
	switch {
	case errors.Is(err, ErrNeedsAuth):
		return ErrNeedsAuth.Error()
	case errors.Is(err, ErrToolDenied):
		return ErrToolDenied.Error()
	case errors.Is(err, ErrNotFound):
		return "MCP server not found."
	}
	var ce *connectError
	if errors.As(err, &ce) {
		return ce.message
	}
	msg := err.Error()
	for _, m := range []string{msgUnreachable, msgBlocked, msgCredentials, msgSignIn, msgSignInAgain, msgProtocol, msgTooLarge, msgTimeout} {
		if strings.Contains(msg, m) {
			return m
		}
	}
	return ErrUnavailable.Error()
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (r *Registry) stateCookie(value string, maxAge int) http.Cookie {
	return http.Cookie{Name: oauthCookie, Value: value, Path: oauthCookiePath, Domain: r.d.Cfg.CookieDomain, MaxAge: maxAge,
		HttpOnly: true, Secure: r.d.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode}
}

func (r *Registry) toolsURL(q url.Values) string {
	return strings.TrimRight(r.d.Cfg.AppURL, "/") + "/settings/ai/tools?" + q.Encode()
}

// oauthCallback finishes a sign-in and redirects back to the app's tools settings.
func (r *Registry) oauthCallback(w http.ResponseWriter, req *http.Request) {
	fail := func(code string) {
		http.Redirect(w, req, r.toolsURL(url.Values{"error": {code}}), http.StatusFound)
	}
	clear := r.stateCookie("", -1)
	http.SetCookie(w, &clear)
	ctx := req.Context()
	q := req.URL.Query()
	pf, err := r.redeemState(ctx, q.Get("state"))
	if err != nil {
		fail("oauth_state")
		return
	}
	cookie, err := req.Cookie(oauthCookie)
	if err != nil || cookie.Value == "" || subtle.ConstantTimeCompare(crypto.HashToken(cookie.Value), pf.NonceHash) != 1 {
		fail("oauth_state")
		return
	}
	if q.Get("error") != "" {
		fail("access_denied")
		return
	}
	if iss := q.Get("iss"); iss != "" && strings.TrimRight(iss, "/") != strings.TrimRight(pf.Flow.Issuer, "/") {
		fail("oauth_state")
		return
	}
	var role string
	if err := r.d.DB.Pool.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2`, pf.WorkspaceID, pf.UserID).
		Scan(&role); err != nil || !auth.Role(role).AtLeast(auth.RoleAdmin) {
		fail("forbidden")
		return
	}
	s, err := loadServer(ctx, r.d.DB.Pool, pf.WorkspaceID, pf.ServerID)
	if err != nil {
		fail("not_found")
		return
	}
	if s.AuthKind != AuthOAuth || s.URL != pf.Flow.URL {
		fail("oauth_state")
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > 4096 {
		fail("provider_error")
		return
	}
	tok, err := r.exchangeCode(ctx, pf.Flow, code)
	if err != nil {
		r.d.Log.Warn("mcp: oauth code exchange failed", "workspace_id", pf.WorkspaceID, "server_id", pf.ServerID)
		fail("provider_error")
		return
	}
	p := &auth.Principal{Kind: auth.KindUser, UserID: pf.UserID, WorkspaceID: pf.WorkspaceID, Role: auth.Role(role),
		IP: httpx.ClientIP(r.d.Cfg.TrustProxy, r.d.Cfg.ProxyHops)(req), UserAgent: truncateRunes(req.UserAgent(), 400)}
	if err := r.saveTokens(ctx, p, s, pf.Flow, tok); err != nil {
		if isNotFound(err) {
			fail("not_found")
			return
		}
		r.d.Log.Error("mcp: save oauth tokens", "workspace_id", pf.WorkspaceID, "server_id", pf.ServerID, "err", err)
		fail("internal")
		return
	}
	http.Redirect(w, req, r.toolsURL(url.Values{"connected": {s.ID.String()}}), http.StatusFound)
}

// saveTokens stores the tokens of a finished sign-in, connects with them and lists
// the tools, in one change the admins see.
func (r *Registry) saveTokens(ctx context.Context, p *auth.Principal, s *server, f flowState, tok *tokenResponse) error {
	sec, err := s.open(r.d.Box)
	if err != nil {
		return err
	}
	sec.Token, sec.HeaderValue = "", ""
	sec.ClientSecret, sec.AccessToken, sec.RefreshToken = f.ClientSecret, tok.AccessToken, tok.RefreshToken
	meta := oauthMeta{Issuer: f.Issuer, TokenEndpoint: f.TokenEndpoint, RevocationEndpoint: f.RevocationEndpoint,
		RegistrationIssuer: s.OAuth.RegistrationIssuer, ClientID: f.ClientID, AuthMethod: f.AuthMethod, RedirectURI: f.RedirectURI,
		Resource: f.Resource, Scopes: f.Scopes, ExpiresAt: tok.expiresAt(r.now()), Connected: true}
	if meta.RegistrationIssuer == "" || meta.ClientID != s.OAuth.ClientID {
		meta.RegistrationIssuer = f.Issuer
	}
	if tok.Scope != "" {
		meta.Scopes = strings.Fields(tok.Scope)
	}
	o := r.connectAndList(ctx, s.URL, "Authorization", "Bearer "+tok.AccessToken, AuthOAuth)
	sealed, err := r.d.Box.SealJSON(sec)
	if err != nil {
		return err
	}
	err = r.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		row, err := lockServer(ctx, tx, s.WorkspaceID, s.ID)
		if err != nil {
			return err
		}
		if row.URL != f.URL || row.AuthKind != AuthOAuth {
			return httpx.Conflict("changed")
		}
		row.SecretsEnc, row.OAuth, row.HeaderName = sealed, meta, nil
		row.Version++
		if _, err := r.mergeOutcome(ctx, tx, row, o); err != nil {
			return err
		}
		if err := saveServer(ctx, tx, row, true); err != nil {
			return err
		}
		if err := publishServer(ctx, tx, "ai_mcp_server.updated", row, row.view(r.d.Box)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, p, s.WorkspaceID, "ai.mcp_server_connected", "ai_mcp_server", &s.ID,
			map[string]any{"name": row.Name, "host": hostOf(row.URL), "scopes": meta.Scopes, "status": row.Status})
	})
	if err == nil {
		r.forget(s.ID)
	}
	return err
}
