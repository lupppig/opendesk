package core

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// APIKeyPrefix starts every API key; the resolver recognizes keys by it.
const APIKeyPrefix = "od_live_"

type CoreAPIKey struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	Prefix     string          `json:"prefix" doc:"First characters of the key, for recognizing it"`
	Scopes     []string        `json:"scopes"`
	LastUsedAt *time.Time      `json:"last_used_at"`
	ExpiresAt  *time.Time      `json:"expires_at"`
	CreatedAt  time.Time       `json:"created_at"`
	CreatedBy  *domain.UserRef `json:"created_by"`
	// PrivateTeamIDs are the private teams whose tickets this key sees
	// (contracts/teams.md "Visibility").
	PrivateTeamIDs []uuid.UUID `json:"private_team_ids"`
	// PrivateTeams are the same teams, by name, with who granted each.
	PrivateTeams []PrivateTeamGrant `json:"private_teams"`
	// BotID is the bot the key speaks as: what it writes on tickets is shown and sent as that
	// bot (contracts/bots.md). Null: it writes as itself.
	BotID *uuid.UUID `json:"bot_id" doc:"The bot this key speaks as on tickets, or null"`
}

type CoreAPIKeyWithSecret struct {
	CoreAPIKey
	Secret string `json:"secret" doc:"The full key. Shown once."`
}

var apiKeySelect = `SELECT k.id, k.name, k.prefix, k.scopes, k.last_used_at, k.expires_at, k.created_at, k.bot_id,
	u.id, u.name, u.email, u.avatar_url, ` + grantedTeamsSQL("k.private_team_ids", "k.workspace_id") + `,
	` + teamGrantsSQL("k.private_team_ids", "k.workspace_id", grantAPIKey, "k.id") + `
	FROM api_keys k LEFT JOIN users u ON u.id = k.created_by`

func scanAPIKey(r pgx.Row) (CoreAPIKey, error) {
	var k CoreAPIKey
	var uid *uuid.UUID
	var name, email, avatar *string
	var grants []byte
	if err := r.Scan(&k.ID, &k.Name, &k.Prefix, &k.Scopes, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedAt, &k.BotID, &uid, &name, &email, &avatar,
		&k.PrivateTeamIDs, &grants); err != nil {
		return k, err
	}
	var err error
	if k.PrivateTeams, err = scanGrants(grants); err != nil {
		return k, err
	}
	if k.PrivateTeamIDs == nil {
		k.PrivateTeamIDs = []uuid.UUID{}
	}
	if uid != nil {
		k.CreatedBy = &domain.UserRef{ID: *uid, Name: *name, Email: *email, AvatarURL: domain.Avatar(*avatar)}
	}
	if k.Scopes == nil {
		k.Scopes = []string{}
	}
	return k, nil
}

func loadAPIKey(ctx context.Context, q db.Querier, wid, id uuid.UUID) (CoreAPIKey, error) {
	return scanAPIKey(q.QueryRow(ctx, apiKeySelect+` WHERE k.workspace_id = $1 AND k.id = $2 AND k.revoked_at IS NULL`, wid, id))
}

// newAPIKey returns a key, its stored hash and its displayed prefix.
func newAPIKey() (secret string, hash []byte, prefix string) {
	secret = APIKeyPrefix + crypto.RandomToken(32)
	return secret, crypto.HashToken(secret), secret[:len(APIKeyPrefix)+8]
}

// validScopes normalizes a scope list; every entry must be a known scope.
func validScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, httpx.Validation("Choose at least one scope.")
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !slices.Contains(auth.AllScopes, s) {
			return nil, httpx.Validation("Unknown scope: " + truncate(s, 40))
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out, nil
}

// keyBot checks the bot a key is to speak as: a bot of the workspace, for a key that may
// write on tickets. Nil clears it.
func keyBot(ctx context.Context, q db.Querier, wid uuid.UUID, raw *string, scopes []string) (*uuid.UUID, error) {
	bot, err := bots.ParseRef(ctx, q, wid, raw)
	if err != nil || bot == nil {
		return bot, err
	}
	if !slices.Contains(scopes, auth.ScopeTicketsWrite) {
		return nil, httpx.Validation("A key speaks as a bot only with the tickets:write scope.")
	}
	return bot, nil
}

// scopeChanges says what going from one sorted scope list to another adds and removes.
func scopeChanges(from, to []string) (added, removed []string) {
	added, removed = []string{}, []string{}
	for _, sc := range to {
		if !slices.Contains(from, sc) {
			added = append(added, sc)
		}
	}
	for _, sc := range from {
		if !slices.Contains(to, sc) {
			removed = append(removed, sc)
		}
	}
	return added, removed
}

func (s *svc) registerAPIKeys(api huma.API) {
	tags := []string{"API keys"}

	register(s, api, huma.Operation{OperationID: "list-api-keys", Method: http.MethodGet, Path: "/v1/api-keys",
		Summary: "List API keys", Tags: tags},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[CoreAPIKey] }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var cur struct {
				T  time.Time `json:"t"`
				ID uuid.UUID `json:"id"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			if cur.T.IsZero() {
				cur.T = time.Now().Add(time.Hour)
			}
			rows, err := s.d.DB.Pool.Query(ctx, apiKeySelect+` WHERE k.workspace_id = $1 AND k.revoked_at IS NULL
				AND (k.created_at, k.id) < ($2, $3) ORDER BY k.created_at DESC, k.id DESC LIMIT $4`, p.WorkspaceID, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreAPIKey, error) { return scanAPIKey(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[CoreAPIKey] }{httpx.NewPage(list, in.Limit, func(k CoreAPIKey) any {
				return map[string]any{"t": k.CreatedAt, "id": k.ID}
			})}, nil
		})

	register(s, api, huma.Operation{OperationID: "get-api-key", Method: http.MethodGet, Path: "/v1/api-keys/{id}",
		Summary: "Get an API key", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body CoreAPIKey }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "API key")
			if err != nil {
				return nil, err
			}
			k, err := loadAPIKey(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreAPIKey }{k}, nil
		})

	register(s, api, huma.Operation{OperationID: "create-api-key", Method: http.MethodPost, Path: "/v1/api-keys",
		Summary: "Create an API key", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				Name      string     `json:"name" maxLength:"200"`
				Scopes    []string   `json:"scopes" maxItems:"20"`
				ExpiresAt *time.Time `json:"expires_at,omitempty"`
				// PrivateTeamIDs: private teams whose tickets the key sees; only their members add them.
				PrivateTeamIDs []string `json:"private_team_ids,omitempty" maxItems:"200"`
				// BotID: the bot the key speaks as on tickets (needs tickets:write).
				BotID *string `json:"bot_id,omitempty" required:"false" nullable:"true" doc:"A bot of the workspace this key speaks as on tickets (needs tickets:write), or null"`
			}
		}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         CoreAPIKeyWithSecret
		}, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// A key is a long-lived credential: making one asks for the second factor
			// and a recent sign-in, like other owner-level operations.
			if err := s.requireMFA(ctx, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			name, err := displayname.Validate("Key name", in.Body.Name, displayname.Workspace, false)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			scopes, err := validScopes(in.Body.Scopes)
			if err != nil {
				return nil, err
			}
			if e := in.Body.ExpiresAt; e != nil && !e.After(time.Now().Add(time.Minute)) {
				return nil, httpx.Validation("expires_at must be in the future.")
			}
			if err := s.limit("apikey:create:"+p.WorkspaceID.String(), 30, time.Hour); err != nil {
				return nil, err
			}
			secret, hash, prefix := newAPIKey()
			var k CoreAPIKey
			err = s.tx(ctx, func(tx pgx.Tx) error {
				teams, err := checkPrivateTeams(ctx, tx, p, nil, in.Body.PrivateTeamIDs)
				if err != nil {
					return err
				}
				bot, err := keyBot(ctx, tx, p.WorkspaceID, in.Body.BotID, scopes)
				if err != nil {
					return err
				}
				id := ids.New()
				if _, err := tx.Exec(ctx, `INSERT INTO api_keys (id, workspace_id, name, prefix, key_hash, scopes, created_by, expires_at, private_team_ids, bot_id)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, id, p.WorkspaceID, name, prefix, hash, scopes, p.UserID, in.Body.ExpiresAt, teams, bot); err != nil {
					return err
				}
				if err := saveGrants(ctx, tx, p, grantAPIKey, id, teams); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "api_key.created", "api_key", &id,
					map[string]any{"name": name, "scopes": scopes, "prefix": prefix, "private_team_ids": uuidStrings(teams), "bot_id": bot}); err != nil {
					return err
				}
				k, err = loadAPIKey(ctx, tx, p.WorkspaceID, id)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.d.Analytics.Capture(p.UserID.String(), p.WorkspaceID.String(), "api_key_created",
				map[string]any{"scope_count": len(scopes), "expires": in.Body.ExpiresAt != nil, "bot": k.BotID != nil})
			return &struct {
				CacheControl string `header:"Cache-Control"`
				Body         CoreAPIKeyWithSecret
			}{"no-store", CoreAPIKeyWithSecret{CoreAPIKey: k, Secret: secret}}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-api-key", Method: http.MethodPatch, Path: "/v1/api-keys/{id}",
		Summary: "Update an API key", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Name *string `json:"name,omitempty" maxLength:"200"`
				// PrivateTeamIDs replaces the list. Only a team's members add it; admins may remove any.
				PrivateTeamIDs *[]string `json:"private_team_ids,omitempty" maxItems:"200"`
				// BotID: the bot the key speaks as on tickets (needs tickets:write); null: itself.
				BotID bots.Assignment `json:"bot_id,omitempty" doc:"A bot of the workspace this key speaks as on tickets (needs tickets:write), or null for none"`
				// Scopes replaces the key's scopes. Adding one asks for the same fresh sign-in as
				// creating a key; removing is immediate.
				Scopes *[]string `json:"scopes,omitempty" maxItems:"20" doc:"The key's scopes, replacing the current ones. Adding a scope needs a recent sign-in (and the second factor when on), as creating a key does"`
			}
		}) (*struct{ Body CoreAPIKey }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "API key")
			if err != nil {
				return nil, err
			}
			var name *string
			if in.Body.Name != nil {
				v, err := displayname.Validate("Key name", *in.Body.Name, displayname.Workspace, false)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				name = &v
			}
			var wanted []string
			if in.Body.Scopes != nil {
				if wanted, err = validScopes(*in.Body.Scopes); err != nil {
					return nil, err
				}
			}
			var k CoreAPIKey
			teamsChanged, scopesChanged := false, false
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var current, scopes []string
				var bot, creator *uuid.UUID
				err := tx.QueryRow(ctx, `SELECT private_team_ids::text[], scopes, bot_id, created_by FROM api_keys WHERE id = $1 AND workspace_id = $2 AND revoked_at IS NULL FOR UPDATE`,
					id, p.WorkspaceID).Scan(&current, &scopes, &bot, &creator)
				if db.IsNotFound(err) {
					return httpx.NotFound("API key")
				}
				if err != nil {
					return err
				}
				before := scopes
				var added, removed []string
				if wanted != nil {
					added, removed = scopeChanges(before, wanted)
					if len(added) > 0 {
						// More access for a live credential: the same checks as making a new key.
						if err := s.requireMFA(ctx, p); err != nil {
							return err
						}
						if err := requireRecentLogin(ctx, tx, p); err != nil {
							return err
						}
						// collab:draft reads its creator's mail: only they hand it to a key.
						if slices.Contains(added, auth.ScopeCollabDraft) && (creator == nil || *creator != p.UserID) {
							return httpx.E(http.StatusForbidden, "forbidden", "Only the member who created this key can give it collab:draft: the key drafts replies as them.")
						}
					}
					scopes, scopesChanged = wanted, len(added)+len(removed) > 0
				}
				var teams []uuid.UUID
				if in.Body.PrivateTeamIDs != nil {
					if teams, err = checkPrivateTeams(ctx, tx, p, current, *in.Body.PrivateTeamIDs); err != nil {
						return err
					}
					teamsChanged = !sameIDs(current, teams)
				}
				if in.Body.BotID.Set {
					if bot, err = keyBot(ctx, tx, p.WorkspaceID, in.Body.BotID.Value, scopes); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE api_keys SET name = COALESCE($3, name), private_team_ids = COALESCE($4, private_team_ids), bot_id = $5, scopes = $6
					WHERE id = $1 AND workspace_id = $2`, id, p.WorkspaceID, name, teams, bot, scopes); err != nil {
					return err
				}
				if in.Body.PrivateTeamIDs != nil {
					if err := saveGrants(ctx, tx, p, grantAPIKey, id, teams); err != nil {
						return err
					}
				}
				if k, err = loadAPIKey(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				if scopesChanged {
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "api_key.scopes_changed", "api_key", &id,
						map[string]any{"name": k.Name, "from": before, "to": k.Scopes, "added": added, "removed": removed}); err != nil {
						return err
					}
				}
				if in.Body.Name == nil && in.Body.PrivateTeamIDs == nil && !in.Body.BotID.Set {
					return nil
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "api_key.updated", "api_key", &id,
					map[string]any{"name": k.Name, "private_team_ids": uuidStrings(k.PrivateTeamIDs), "bot_id": k.BotID})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if teamsChanged || scopesChanged {
				// Sockets of this key carry its teams and scopes in their token; they reconnect with the new ones.
				s.disconnect(id, uuid.Nil)
			}
			return &struct{ Body CoreAPIKey }{k}, nil
		})

	register(s, api, huma.Operation{OperationID: "revoke-api-key", Method: http.MethodDelete, Path: "/v1/api-keys/{id}",
		Summary: "Revoke an API key", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "API key")
			if err != nil {
				return nil, err
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND workspace_id = $2 AND revoked_at IS NULL`, id, p.WorkspaceID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return httpx.NotFound("API key")
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "api_key.revoked", "api_key", &id, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// Sockets opened with this key carry it as their subject.
			s.disconnect(id, uuid.Nil)
			return nil, nil
		})
}
