package assistant

import (
	"context"
	"net/http"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/collab"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// A member's own agent-to-agent sharing (contracts/ai.md "Asking a teammate"): only ever the
// caller's, for the current workspace. No role reads or changes anyone else's, and Ollie
// never reaches these routes (they are out of the spec and closed to its API tools).

// PeerSettings is GET and PUT /v1/me/assistant-peer.
type PeerSettings struct {
	// Available: the workspace allows it (settings.ai.assistant_peer, AI and Ollie on) and has Collab.
	Available   bool             `json:"available"`
	Enabled     bool             `json:"enabled"`
	ShareWith   string           `json:"share_with" enum:"everyone,chosen"`
	Audience    PeerAudience     `json:"audience"`
	AllAccounts bool             `json:"all_accounts"`
	DefaultMode string           `json:"default_mode" enum:"verbatim,summary,none"`
	Accounts    []PeerAccountOut `json:"accounts"`
	Rules       string           `json:"rules"`
	UpdatedAt   *time.Time       `json:"updated_at"`
}

// PeerAudience is who may ask when share_with is chosen.
type PeerAudience struct {
	Users []domain.UserRef `json:"users"`
	Teams []PeerTeam       `json:"teams"`
}

// PeerTeam is a team of the audience.
type PeerTeam struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// PeerAccountOut is one of the caller's own Collab accounts with what it shares.
type PeerAccountOut struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	Name     string    `json:"name"`
	Provider string    `json:"provider"`
	// Mode is what the account shares now; Chosen: it has its own mode, not the default.
	Mode   string `json:"mode" enum:"verbatim,summary,none"`
	Chosen bool   `json:"chosen"`
}

// PeerSettingsInput is the body of PUT /v1/me/assistant-peer: the whole settings.
type PeerSettingsInput struct {
	Enabled   bool   `json:"enabled"`
	ShareWith string `json:"share_with" enum:"everyone,chosen"`
	Audience  struct {
		UserIDs []uuid.UUID `json:"user_ids" maxItems:"200"`
		TeamIDs []uuid.UUID `json:"team_ids" maxItems:"100"`
	} `json:"audience"`
	AllAccounts bool   `json:"all_accounts"`
	DefaultMode string `json:"default_mode" enum:"verbatim,summary,none"`
	Accounts    []struct {
		ID   uuid.UUID `json:"id"`
		Mode string    `json:"mode" enum:"verbatim,summary,none"`
	} `json:"accounts" maxItems:"100"`
	Rules string `json:"rules" maxLength:"2000"`
}

// PeerAsk is one ask made to the caller's Ollie.
type PeerAsk struct {
	ID        uuid.UUID      `json:"id"`
	Asker     domain.UserRef `json:"asker"`
	Question  string         `json:"question"`
	Answer    string         `json:"answer"`
	Result    string         `json:"result" enum:"answered,declined,not_shared,failed"`
	Sources   int            `json:"sources"`
	CreatedAt time.Time      `json:"created_at"`
}

var peerModes = []string{collab.PeerVerbatim, collab.PeerSummary, collab.PeerNone}

// requireSelf is the gate of the caller's own sharing: a signed-in member, never an API
// key, an AI client or Ollie, in a workspace with Collab.
func (s *Service) requireSelf(ctx context.Context) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, "")
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if p.Kind != auth.KindUser || p.Assistant || p.ViaAssistant || p.GrantID != uuid.Nil {
		return nil, httpx.Forbidden()
	}
	if s.d.Plan != nil {
		if err := s.d.Plan.RequireCollab(ctx, s.d.DB.Pool, p.WorkspaceID); err != nil {
			return nil, httpx.Wrap(err)
		}
	}
	return p, nil
}

// peerOp registers one of these routes, left out of the public spec like Collab's.
func peerOp[I, O any](api huma.API, o huma.Operation, h func(context.Context, *I) (*O, error)) {
	o.Hidden = true
	o.Tags = []string{"Assistant"}
	huma.Register(api, o, h)
}

func (s *Service) registerPeer(api huma.API) {
	peerOp(api, huma.Operation{OperationID: "get-my-assistant-peer", Method: http.MethodGet, Path: "/v1/me/assistant-peer",
		Summary: "What my Ollie shares with teammates' Ollie"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body PeerSettings }, error) {
			p, err := s.requireSelf(ctx)
			if err != nil {
				return nil, err
			}
			out, err := s.loadPeerSettings(ctx, s.d.DB.Pool, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body PeerSettings }{out}, nil
		})

	peerOp(api, huma.Operation{OperationID: "put-my-assistant-peer", Method: http.MethodPut, Path: "/v1/me/assistant-peer",
		Summary: "Choose what my Ollie shares with teammates' Ollie"},
		func(ctx context.Context, in *struct{ Body PeerSettingsInput }) (*struct{ Body PeerSettings }, error) {
			p, err := s.requireSelf(ctx)
			if err != nil {
				return nil, err
			}
			b := in.Body
			if b.ShareWith != "everyone" && b.ShareWith != "chosen" {
				return nil, httpx.Validation("share_with must be everyone or chosen")
			}
			if !slices.Contains(peerModes, b.DefaultMode) {
				return nil, httpx.Validation("default_mode must be verbatim, summary or none")
			}
			rules := cleanText(b.Rules)
			if utf8.RuneCountInString(rules) > 2000 {
				return nil, httpx.Validation("rules must be 2000 characters or less")
			}
			if b.Enabled && !s.peerAvailable(ctx, p.WorkspaceID) {
				return nil, errPeerOff
			}
			users := uniq(slices.DeleteFunc(slices.Clone(b.Audience.UserIDs), func(id uuid.UUID) bool { return id == p.UserID }))
			teams := uniq(b.Audience.TeamIDs)
			if len(users) > 0 {
				var n int
				if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1 AND user_id = ANY($2)`,
					p.WorkspaceID, users).Scan(&n); err != nil {
					return nil, httpx.Wrap(err)
				}
				if n != len(users) {
					return nil, httpx.Validation("audience.user_ids must be people in this workspace")
				}
			}
			if len(teams) > 0 {
				var n int
				if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM teams WHERE workspace_id = $1 AND id = ANY($2)`,
					p.WorkspaceID, teams).Scan(&n); err != nil {
					return nil, httpx.Wrap(err)
				}
				if n != len(teams) {
					return nil, httpx.Validation("audience.team_ids must be teams in this workspace")
				}
			}
			own, err := collab.OwnAccounts(ctx, s.d.DB.Pool, p.WorkspaceID, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			sources := map[uuid.UUID]string{}
			for _, a := range b.Accounts {
				if !slices.Contains(peerModes, a.Mode) {
					return nil, httpx.Validation("each account mode must be verbatim, summary or none")
				}
				if !slices.ContainsFunc(own, func(o collab.OwnAccount) bool { return o.ID == a.ID }) {
					return nil, httpx.Validation("accounts must be your own mail accounts in this workspace")
				}
				sources[a.ID] = a.Mode
			}
			var out PeerSettings
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				before, err := s.loadPeerSettings(ctx, tx, p)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO assistant_peer_settings (workspace_id, user_id, enabled, share_with, all_accounts, default_mode, rules)
					VALUES ($1, $2, $3, $4, $5, $6, $7)
					ON CONFLICT (workspace_id, user_id) DO UPDATE SET enabled = $3, share_with = $4, all_accounts = $5, default_mode = $6,
						rules = $7, updated_at = now()`,
					p.WorkspaceID, p.UserID, b.Enabled, b.ShareWith, b.AllAccounts, b.DefaultMode, rules); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM assistant_peer_audience WHERE workspace_id = $1 AND owner_id = $2`, p.WorkspaceID, p.UserID); err != nil {
					return err
				}
				for _, u := range users {
					if _, err := tx.Exec(ctx, `INSERT INTO assistant_peer_audience (workspace_id, owner_id, user_id) VALUES ($1, $2, $3)`,
						p.WorkspaceID, p.UserID, u); err != nil {
						return err
					}
				}
				for _, t := range teams {
					if _, err := tx.Exec(ctx, `INSERT INTO assistant_peer_audience (workspace_id, owner_id, team_id) VALUES ($1, $2, $3)`,
						p.WorkspaceID, p.UserID, t); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `DELETE FROM assistant_peer_sources WHERE workspace_id = $1 AND owner_id = $2`, p.WorkspaceID, p.UserID); err != nil {
					return err
				}
				for id, mode := range sources {
					// Only the caller's own accounts, checked again in the statement.
					if _, err := tx.Exec(ctx, `INSERT INTO assistant_peer_sources (workspace_id, owner_id, account_id, mode)
						SELECT $1, $2, a.id, $4 FROM collab_accounts a WHERE a.id = $3 AND a.workspace_id = $1 AND a.owner_id = $2`,
						p.WorkspaceID, p.UserID, id, mode); err != nil {
						return err
					}
				}
				if out, err = s.loadPeerSettings(ctx, tx, p); err != nil {
					return err
				}
				if changed := peerChanges(before, out); len(changed) > 0 {
					return audit.Log(ctx, tx, p, p.WorkspaceID, "assistant_peer.settings_updated", "user", &p.UserID,
						map[string]any{"changes": changed})
				}
				return nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body PeerSettings }{out}, nil
		})

	peerOp(api, huma.Operation{OperationID: "list-my-assistant-peer-asks", Method: http.MethodGet, Path: "/v1/me/assistant-peer/asks",
		Summary: "What teammates' Ollie asked mine", Description: "Newest first; kept 90 days."},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[PeerAsk] }, error) {
			p, err := s.requireSelf(ctx)
			if err != nil {
				return nil, err
			}
			limit := in.Limit
			if limit <= 0 || limit > 100 {
				limit = 50
			}
			var cur struct {
				T time.Time `json:"t"`
				I uuid.UUID `json:"i"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			args := []any{p.WorkspaceID, p.UserID, limit + 1}
			where := ""
			if in.Cursor != "" {
				where = ` AND (k.created_at, k.id) < ($4, $5)`
				args = append(args, cur.T, cur.I)
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT k.id, k.question, k.answer, k.result, k.sources, k.created_at,
					u.id, u.name, u.email::text, u.avatar_url
				FROM assistant_peer_asks k JOIN users u ON u.id = k.asker_id
				WHERE k.workspace_id = $1 AND k.target_id = $2`+where+`
				ORDER BY k.created_at DESC, k.id DESC LIMIT $3`, args...)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PeerAsk, error) {
				var a PeerAsk
				err := r.Scan(&a.ID, &a.Question, &a.Answer, &a.Result, &a.Sources, &a.CreatedAt,
					&a.Asker.ID, &a.Asker.Name, &a.Asker.Email, &a.Asker.AvatarURL)
				return a, err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page := httpx.NewPage(list, limit, func(a PeerAsk) any {
				return struct {
					T time.Time `json:"t"`
					I uuid.UUID `json:"i"`
				}{a.CreatedAt, a.ID}
			})
			return &struct{ Body httpx.Page[PeerAsk] }{page}, nil
		})
}

// loadPeerSettings is the caller's settings with defaults, their own accounts and audience.
func (s *Service) loadPeerSettings(ctx context.Context, q db.Querier, p *auth.Principal) (PeerSettings, error) {
	out := PeerSettings{Available: s.peerAvailable(ctx, p.WorkspaceID), ShareWith: "everyone", AllAccounts: true,
		DefaultMode: collab.PeerSummary, Audience: PeerAudience{Users: []domain.UserRef{}, Teams: []PeerTeam{}}, Accounts: []PeerAccountOut{}}
	var at time.Time
	err := q.QueryRow(ctx, `SELECT enabled, share_with, all_accounts, default_mode, rules, updated_at FROM assistant_peer_settings
		WHERE workspace_id = $1 AND user_id = $2`, p.WorkspaceID, p.UserID).
		Scan(&out.Enabled, &out.ShareWith, &out.AllAccounts, &out.DefaultMode, &out.Rules, &at)
	switch {
	case err == nil:
		out.UpdatedAt = &at
	case !db.IsNotFound(err):
		return out, err
	}
	rows, err := q.Query(ctx, `SELECT u.id, u.name, u.email::text, u.avatar_url FROM assistant_peer_audience au
		JOIN users u ON u.id = au.user_id JOIN members m ON m.workspace_id = au.workspace_id AND m.user_id = au.user_id
		WHERE au.workspace_id = $1 AND au.owner_id = $2 ORDER BY u.name, u.id`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	users, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.UserRef, error) {
		var u domain.UserRef
		err := r.Scan(&u.ID, &u.Name, &u.Email, &u.AvatarURL)
		return u, err
	})
	if err != nil {
		return out, err
	}
	if users != nil {
		out.Audience.Users = users
	}
	rows, err = q.Query(ctx, `SELECT t.id, t.name FROM assistant_peer_audience au JOIN teams t ON t.id = au.team_id
		WHERE au.workspace_id = $1 AND au.owner_id = $2 AND t.workspace_id = $1 ORDER BY t.name, t.id`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	teams, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PeerTeam, error) {
		var t PeerTeam
		err := r.Scan(&t.ID, &t.Name)
		return t, err
	})
	if err != nil {
		return out, err
	}
	if teams != nil {
		out.Audience.Teams = teams
	}
	own, err := collab.OwnAccounts(ctx, q, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	chosen := map[uuid.UUID]string{}
	rows, err = q.Query(ctx, `SELECT account_id, mode FROM assistant_peer_sources WHERE workspace_id = $1 AND owner_id = $2`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id uuid.UUID
		var mode string
		if err := rows.Scan(&id, &mode); err != nil {
			rows.Close()
			return out, err
		}
		chosen[id] = mode
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, a := range own {
		mode, ok := chosen[a.ID]
		if !ok {
			mode = collab.PeerNone
			if out.AllAccounts {
				mode = out.DefaultMode
			}
		}
		out.Accounts = append(out.Accounts, PeerAccountOut{ID: a.ID, Email: a.Email, Name: a.Name, Provider: a.Provider, Mode: mode, Chosen: ok})
	}
	return out, nil
}

// peerChanges names the settings that differ, for the audit log (never their values).
func peerChanges(a, b PeerSettings) []string {
	var out []string
	add := func(changed bool, key string) {
		if changed {
			out = append(out, key)
		}
	}
	add(a.Enabled != b.Enabled, "enabled")
	add(a.ShareWith != b.ShareWith, "share_with")
	add(!slices.EqualFunc(a.Audience.Users, b.Audience.Users, func(x, y domain.UserRef) bool { return x.ID == y.ID }) ||
		!slices.EqualFunc(a.Audience.Teams, b.Audience.Teams, func(x, y PeerTeam) bool { return x.ID == y.ID }), "audience")
	add(a.AllAccounts != b.AllAccounts, "all_accounts")
	add(a.DefaultMode != b.DefaultMode, "default_mode")
	add(!slices.EqualFunc(a.Accounts, b.Accounts, func(x, y PeerAccountOut) bool { return x.ID == y.ID && x.Mode == y.Mode && x.Chosen == y.Chosen }), "accounts")
	add(a.Rules != b.Rules, "rules")
	return out
}

func uniq(list []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(list))
	for _, id := range list {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
