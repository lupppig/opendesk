package collab

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Routes of inbox categories (contracts/collab.md "Categories", "Endpoints").

// The AI area's answers when it cannot suggest (contracts/ai.md "Errors").
var (
	errAIDisabled  = httpx.E(http.StatusNotFound, "ai_disabled", "AI is not enabled on this instance.")
	errAIBadOutput = httpx.E(http.StatusBadGateway, "ai_bad_output", "The AI model returned an unusable answer. Try again.")
)

var errCategoriesLocked = httpx.E(http.StatusUnprocessableEntity, "categories_locked",
	"Your workspace admins set the categories for everyone, so they cannot be changed here.")

type categorySetInput struct {
	Enabled    bool           `json:"enabled"`
	Categories []MailCategory `json:"categories,omitempty" maxItems:"8" doc:"2..8 when enabled, primary first; left out: the default set"`
}

type settingsBody struct {
	Body MailCategorySettings
}

// defaultCategories are the built-in set a new default starts with.
func defaultCategories() []MailCategory { return slices.Clone(presetCategories[:4]) }

// setCategories checks the categories of an enabled set; none given is the default set.
func setCategories(in categorySetInput) ([]MailCategory, error) {
	if !in.Enabled {
		return []MailCategory{}, nil
	}
	if len(in.Categories) == 0 {
		return defaultCategories(), nil
	}
	return cleanCategories(in.Categories)
}

func modeOf(enabled bool) string {
	if enabled {
		return modeOn
	}
	return modeOff
}

func categoryKeysOf(list []MailCategory) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Key)
	}
	return out
}

// categorySettings answers GET /v1/collab/categories for the caller.
func (s *service) categorySettings(ctx context.Context, q db.Querier, p *auth.Principal) (MailCategorySettings, error) {
	out := MailCategorySettings{Teams: []TeamCategorySet{}, Presets: presetCategories, Sorting: "rules"}
	if s.sortsWithAI(ctx, p.WorkspaceID) {
		out.Sorting = "ai"
	}
	if s.mailAI != nil && s.mailAI.Enabled() {
		if st, err := hd.LoadSettings(ctx, q, p.WorkspaceID); err == nil && st.AI().Enabled {
			out.Suggest = true
		}
	}
	cs, err := loadCategorySets(ctx, q, p.WorkspaceID, nil, []uuid.UUID{p.UserID})
	if err != nil {
		return out, err
	}
	var by []uuid.UUID
	if cs.workspace != nil && cs.workspace.UpdatedBy != nil {
		by = append(by, *cs.workspace.UpdatedBy)
	}
	for _, t := range cs.teams {
		if t.UpdatedBy != nil {
			by = appendNew(by, *t.UpdatedBy)
		}
	}
	refs, err := userRefs(ctx, q, by)
	if err != nil {
		return out, err
	}
	setOf := func(r *categorySetRow) MailCategorySet {
		return MailCategorySet{Enabled: r.Mode == modeOn, Categories: r.Categories, UpdatedAt: r.UpdatedAt, UpdatedBy: refOf(refs, r.UpdatedBy)}
	}
	if cs.workspace != nil {
		out.Workspace = &WorkspaceCategorySet{MailCategorySet: setOf(cs.workspace), Locked: cs.workspace.Locked}
	}
	admin := p.Role.AtLeast(auth.RoleAdmin)
	for _, t := range cs.teams {
		if !admin && !slices.Contains(cs.ownerTeams[p.UserID], *t.TeamID) {
			continue
		}
		out.Teams = append(out.Teams, TeamCategorySet{MailCategorySet: setOf(t), TeamID: *t.TeamID, TeamName: cs.teamNames[*t.TeamID], Position: t.Position})
	}
	return out, nil
}

func (s *service) settingsResponse(ctx context.Context, p *auth.Principal) (*settingsBody, error) {
	out, err := s.categorySettings(ctx, s.d.DB.Pool, p)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	return &settingsBody{out}, nil
}

// teamOf checks a team belongs to the workspace.
func teamOf(ctx context.Context, q db.Querier, wid uuid.UUID, raw string) (uuid.UUID, error) {
	id, err := parseID(raw, "Team")
	if err != nil {
		return uuid.Nil, err
	}
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&ok); err != nil {
		return uuid.Nil, httpx.Wrap(err)
	}
	if !ok {
		return uuid.Nil, httpx.NotFound("Team")
	}
	return id, nil
}

func (s *service) registerCategoryRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "get-collab-categories", Method: http.MethodGet, Path: "/v1/collab/categories",
		Summary: "Get the workspace's and teams' inbox categories"},
		func(ctx context.Context, _ *struct{}) (*settingsBody, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.settingsResponse(ctx, p)
		})

	collabOp(api, huma.Operation{OperationID: "set-collab-workspace-categories", Method: http.MethodPut, Path: "/v1/collab/categories/workspace",
		Summary: "Set the workspace's default inbox categories"},
		func(ctx context.Context, in *struct {
			Body struct {
				Enabled    bool           `json:"enabled"`
				Categories []MailCategory `json:"categories,omitempty" maxItems:"8" doc:"2..8 when enabled, primary first; left out: the default set"`
				Locked     *bool          `json:"locked,omitempty" doc:"Members who are not admins cannot choose their own"`
			}
		}) (*settingsBody, error) {
			p, err := s.require(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			cats, err := setCategories(categorySetInput{Enabled: in.Body.Enabled, Categories: in.Body.Categories})
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO collab_category_sets (id, workspace_id, scope, mode, categories, locked, updated_by)
					VALUES ($1, $2, 'workspace', $3, $4, COALESCE($5, false), $6)
					ON CONFLICT (workspace_id) WHERE scope = 'workspace' DO UPDATE SET mode = EXCLUDED.mode, categories = EXCLUDED.categories,
						locked = COALESCE($5, collab_category_sets.locked), updated_by = EXCLUDED.updated_by, updated_at = now()`,
					ids.New(), p.WorkspaceID, modeOf(in.Body.Enabled), cats, in.Body.Locked, p.UserID)
				if err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.categories_updated", "workspace", &p.WorkspaceID, map[string]any{
					"scope": scopeWorkspace, "enabled": in.Body.Enabled, "keys": categoryKeysOf(cats), "locked": in.Body.Locked}); err != nil {
					return err
				}
				return s.queueCategorizeWorkspace(ctx, tx, p.WorkspaceID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.settingsResponse(ctx, p)
		})

	collabOp(api, huma.Operation{OperationID: "delete-collab-workspace-categories", Method: http.MethodDelete, Path: "/v1/collab/categories/workspace",
		Summary: "Remove the workspace's default inbox categories"},
		func(ctx context.Context, _ *struct{}) (*settingsBody, error) {
			p, err := s.require(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM collab_category_sets WHERE workspace_id = $1 AND scope = 'workspace'`, p.WorkspaceID)
				if err != nil || tag.RowsAffected() == 0 {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.categories_updated", "workspace", &p.WorkspaceID, map[string]any{
					"scope": scopeWorkspace, "removed": true}); err != nil {
					return err
				}
				return s.queueCategorizeWorkspace(ctx, tx, p.WorkspaceID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.settingsResponse(ctx, p)
		})

	collabOp(api, huma.Operation{OperationID: "set-collab-team-categories", Method: http.MethodPut, Path: "/v1/collab/categories/teams/{team_id}",
		Summary: "Set a team's inbox categories"},
		func(ctx context.Context, in *struct {
			TeamID string `path:"team_id"`
			Body   categorySetInput
		}) (*settingsBody, error) {
			p, err := s.require(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			team, err := teamOf(ctx, s.d.DB.Pool, p.WorkspaceID, in.TeamID)
			if err != nil {
				return nil, err
			}
			cats, err := setCategories(in.Body)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO collab_category_sets (id, workspace_id, scope, team_id, mode, categories, position, updated_by)
					VALUES ($1, $2, 'team', $3, $4, $5, (SELECT COALESCE(max(position) + 1, 0) FROM collab_category_sets WHERE workspace_id = $2 AND scope = 'team'), $6)
					ON CONFLICT (team_id) WHERE scope = 'team' DO UPDATE SET mode = EXCLUDED.mode, categories = EXCLUDED.categories,
						updated_by = EXCLUDED.updated_by, updated_at = now()`,
					ids.New(), p.WorkspaceID, team, modeOf(in.Body.Enabled), cats, p.UserID)
				if err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.categories_updated", "team", &team, map[string]any{
					"scope": scopeTeam, "team_id": team, "enabled": in.Body.Enabled, "keys": categoryKeysOf(cats)}); err != nil {
					return err
				}
				return s.queueCategorizeWorkspace(ctx, tx, p.WorkspaceID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.settingsResponse(ctx, p)
		})

	collabOp(api, huma.Operation{OperationID: "delete-collab-team-categories", Method: http.MethodDelete, Path: "/v1/collab/categories/teams/{team_id}",
		Summary: "Let a team follow the workspace's inbox categories"},
		func(ctx context.Context, in *struct {
			TeamID string `path:"team_id"`
		}) (*settingsBody, error) {
			p, err := s.require(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			team, err := teamOf(ctx, s.d.DB.Pool, p.WorkspaceID, in.TeamID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM collab_category_sets WHERE workspace_id = $1 AND scope = 'team' AND team_id = $2`, p.WorkspaceID, team)
				if err != nil || tag.RowsAffected() == 0 {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.categories_updated", "team", &team, map[string]any{
					"scope": scopeTeam, "team_id": team, "removed": true}); err != nil {
					return err
				}
				return s.queueCategorizeWorkspace(ctx, tx, p.WorkspaceID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.settingsResponse(ctx, p)
		})

	collabOp(api, huma.Operation{OperationID: "order-collab-team-categories", Method: http.MethodPut, Path: "/v1/collab/categories/team-order",
		Summary: "Order the teams' inbox categories"},
		func(ctx context.Context, in *struct {
			Body struct {
				TeamIDs []uuid.UUID `json:"team_ids" maxItems:"500"`
			}
		}) (*settingsBody, error) {
			p, err := s.require(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, `SELECT team_id FROM collab_category_sets WHERE workspace_id = $1 AND scope = 'team' FOR UPDATE`, p.WorkspaceID)
				if err != nil {
					return err
				}
				have, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
				if err != nil {
					return err
				}
				order := appendNew(nil, in.Body.TeamIDs...)
				if len(order) != len(in.Body.TeamIDs) || len(order) != len(have) || slices.ContainsFunc(order, func(id uuid.UUID) bool { return !slices.Contains(have, id) }) {
					return httpx.Validation("List every team with its own categories, once.")
				}
				for i, id := range order {
					if _, err := tx.Exec(ctx, `UPDATE collab_category_sets SET position = $3 WHERE workspace_id = $1 AND scope = 'team' AND team_id = $2`,
						p.WorkspaceID, id, i); err != nil {
						return err
					}
				}
				return s.queueCategorizeWorkspace(ctx, tx, p.WorkspaceID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.settingsResponse(ctx, p)
		})

	collabOp(api, huma.Operation{OperationID: "suggest-collab-categories", Method: http.MethodPost, Path: "/v1/collab/categories/suggest",
		Summary: "Suggest inbox categories with Ollie"},
		func(ctx context.Context, in *struct {
			Body struct {
				AccountID *uuid.UUID `json:"account_id,omitempty" doc:"Learn from this account's inbox (one the caller sees)"`
				About     string     `json:"about,omitempty" maxLength:"500" doc:"What the inbox is for, in the caller's words"`
			}
		}) (*struct {
			Body struct {
				Categories []MailCategory `json:"categories"`
			}
		}, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var sample []string
			if in.Body.AccountID != nil {
				a, err := visibleAccount(ctx, s.d.DB.Pool, p, *in.Body.AccountID)
				if err != nil {
					return nil, notFound(err, "Account")
				}
				if sample, err = inboxSample(ctx, s.d.DB.Pool, a); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			if s.mailAI == nil || !s.mailAI.Enabled() {
				return nil, errAIDisabled
			}
			got, err := s.mailAI.SuggestMailCategories(ctx, p.WorkspaceID, p.UserID, sample, cleanLine(in.Body.About, 500))
			if err != nil {
				return nil, s.mailAI.APIError(err)
			}
			cats := suggestedCategories(got)
			if len(cats) < minCategories {
				return nil, errAIBadOutput
			}
			resp := &struct {
				Body struct {
					Categories []MailCategory `json:"categories"`
				}
			}{}
			resp.Body.Categories = cats
			return resp, nil
		})

	collabOp(api, huma.Operation{OperationID: "set-collab-account-categories", Method: http.MethodPut, Path: "/v1/collab/accounts/{id}/categories",
		Summary: "Choose an account's inbox categories"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Mode       string         `json:"mode" enum:"follow,custom,off"`
				Categories []MailCategory `json:"categories,omitempty" maxItems:"8" doc:"custom: 2..8, primary first; left out keeps its own, else the default set"`
				AI         *bool          `json:"ai,omitempty" doc:"Ollie may sort its new mail"`
			}
		}) (*struct{ Body MailAccount }, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Account")
			}
			cs, err := loadAccountCategorySets(ctx, s.d.DB.Pool, a)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			cur := cs.resolve(a)
			mode := map[string]string{modeFollow: modeFollow, modeCustom: modeOn, modeOff: modeOff}[in.Body.Mode]
			if mode == "" {
				return nil, httpx.Validation("Choose follow, custom or off.")
			}
			cats := cur.Own
			if len(in.Body.Categories) > 0 {
				if cats, err = cleanCategories(in.Body.Categories); err != nil {
					return nil, err
				}
			} else if mode == modeOn && len(cats) < minCategories {
				cats = defaultCategories()
			}
			changed := in.Body.Mode != cur.Mode || len(in.Body.Categories) > 0 && !slices.Equal(cats, cur.Own)
			if changed && cs.locked() && !p.Role.AtLeast(auth.RoleAdmin) {
				return nil, errCategoriesLocked
			}
			aiOn := cur.AI
			if in.Body.AI != nil {
				aiOn = *in.Body.AI
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO collab_category_sets (id, workspace_id, scope, account_id, mode, categories, ai, by_admin, updated_by)
					VALUES ($1, $2, 'account', $3, $4, $5, $6, $7, $8)
					ON CONFLICT (account_id) WHERE scope = 'account' DO UPDATE SET mode = EXCLUDED.mode, categories = EXCLUDED.categories,
						ai = EXCLUDED.ai, by_admin = CASE WHEN $9 THEN EXCLUDED.by_admin ELSE collab_category_sets.by_admin END,
						updated_by = EXCLUDED.updated_by, updated_at = now()`,
					ids.New(), p.WorkspaceID, a.ID, mode, cats, aiOn, p.Role.AtLeast(auth.RoleAdmin), p.UserID, changed)
				if err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID, map[string]any{
					"fields": []string{"categories"}, "mode": in.Body.Mode, "keys": categoryKeysOf(cats), "ai": aiOn}); err != nil {
					return err
				}
				if err := s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil); err != nil {
					return err
				}
				return s.queueCategorize(ctx, tx, a.ID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.accountOut(ctx, s.d.DB.Pool, p, a)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body MailAccount }{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "list-collab-category-rules", Method: http.MethodGet, Path: "/v1/collab/accounts/{id}/category-rules",
		Summary: "List an account's sender rules"},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct {
			Body struct {
				Rules []categoryRule `json:"rules"`
			}
		}, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Account")
			}
			list, err := categoryRules(ctx, s.d.DB.Pool, a.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			resp := &struct {
				Body struct {
					Rules []categoryRule `json:"rules"`
				}
			}{}
			resp.Body.Rules = list
			return resp, nil
		})

	collabOp(api, huma.Operation{OperationID: "add-collab-category-rule", Method: http.MethodPost, Path: "/v1/collab/accounts/{id}/category-rules",
		Summary: "Always put a sender's mail in a category"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Sender   string `json:"sender" maxLength:"254" doc:"An address, or @ and a domain"`
				Category string `json:"category" maxLength:"40"`
			}
		}) (*struct{ Body categoryRule }, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Account")
			}
			sender := cleanSender(in.Body.Sender)
			if sender == "" {
				return nil, httpx.Validation("Enter an address, or @ and a domain.")
			}
			if !slices.Contains(a.CategoryKeys, in.Body.Category) {
				return nil, httpx.Validation("Choose one of the account's categories.")
			}
			var rule categoryRule
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM collab_category_rules WHERE account_id = $1 AND sender <> $2`, a.ID, sender).Scan(&n); err != nil {
					return err
				}
				if n >= maxCategoryRules {
					return httpx.Validation("An account keeps at most 500 sender rules.")
				}
				err := tx.QueryRow(ctx, `INSERT INTO collab_category_rules (id, workspace_id, account_id, sender, category, created_by)
					VALUES ($1, $2, $3, $4, $5, $6)
					ON CONFLICT (account_id, sender) DO UPDATE SET category = EXCLUDED.category, created_by = EXCLUDED.created_by, created_at = now()
					RETURNING id, sender, category, created_at`, ids.New(), p.WorkspaceID, a.ID, sender, in.Body.Category, p.UserID).
					Scan(&rule.ID, &rule.Sender, &rule.Category, &rule.CreatedAt)
				if err != nil {
					return err
				}
				// The sender's mail in the inbox moves now.
				q := `UPDATE collab_threads t SET category = $2, category_source = 'rule', category_pending = false
					WHERE t.account_id = $1 AND t.folder_ids && ARRAY(SELECT id FROM collab_folders WHERE account_id = $1 AND role = 'inbox')
					AND EXISTS (SELECT 1 FROM collab_messages m WHERE m.thread_id = t.id AND m.sent_by IS NULL AND lower(m.from_addr->>'email') LIKE $3)`
				args := []any{a.ID, in.Body.Category, senderLike(sender)}
				if !strings.HasPrefix(sender, "@") {
					q += ` AND t.addresses @> ARRAY[$4::text]` // served by the addresses index
					args = append(args, sender)
				}
				rows, err := tx.Query(ctx, q+` RETURNING t.id`, args...)
				if err != nil {
					return err
				}
				moved, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
				if err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.category_rule", "collab_account", &a.ID, map[string]any{
					"account_id": a.ID, "action": "added", "category": in.Body.Category}); err != nil {
					return err
				}
				return s.publishChanged(ctx, tx, p.WorkspaceID, &a.ID, moved, true)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body categoryRule }{rule}, nil
		})

	collabOp(api, huma.Operation{OperationID: "delete-collab-category-rule", Method: http.MethodDelete, Path: "/v1/collab/accounts/{id}/category-rules/{rule_id}",
		Summary: "Remove a sender rule"},
		func(ctx context.Context, in *struct {
			ID     string `path:"id"`
			RuleID string `path:"rule_id"`
		}) (*struct{}, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			ruleID, err := parseID(in.RuleID, "Rule")
			if err != nil {
				return nil, err
			}
			a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Account")
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var category string
				err := tx.QueryRow(ctx, `DELETE FROM collab_category_rules WHERE account_id = $1 AND id = $2 RETURNING category`, a.ID, ruleID).Scan(&category)
				if err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "collab.category_rule", "collab_account", &a.ID, map[string]any{
					"account_id": a.ID, "action": "removed", "category": category})
			})
			if err != nil {
				return nil, notFound(err, "Rule")
			}
			return &struct{}{}, nil
		})
}

// categorizeThreads moves conversations to a category of their account, the person's
// choice (the action categorize), and tells who sees them.
func (s *service) categorizeThreads(ctx context.Context, p *auth.Principal, threads []*threadRow, key string) error {
	var accountIDs []uuid.UUID
	for _, t := range threads {
		accountIDs = appendNew(accountIDs, t.AccountID)
	}
	keys, err := accountCategoryKeys(ctx, s.d.DB.Pool, threads)
	if err != nil {
		return httpx.Wrap(err)
	}
	for _, id := range accountIDs {
		if !slices.Contains(keys[id], key) {
			return httpx.Validation("Choose one of the inbox's categories.")
		}
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tids := make([]uuid.UUID, 0, len(threads))
		for _, t := range threads {
			tids = append(tids, t.ID)
		}
		if _, err := tx.Exec(ctx, `UPDATE collab_threads SET category = $3, category_source = 'person', category_pending = false
			WHERE workspace_id = $1 AND id = ANY($2)`, p.WorkspaceID, tids, key); err != nil {
			return err
		}
		return s.publishChanged(ctx, tx, p.WorkspaceID, nil, tids, true)
	})
	return httpx.Wrap(err)
}

// cleanSender reads a sender rule's sender: an address, or @ and a domain, lower case.
func cleanSender(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if d, ok := strings.CutPrefix(s, "@"); ok {
		if !strings.Contains(d, ".") || strings.ContainsAny(d, "@ \t<>") {
			return ""
		}
		return s
	}
	local, d, ok := strings.Cut(s, "@")
	if !ok || local == "" || !strings.Contains(d, ".") || strings.ContainsAny(s, " \t<>") || strings.Count(s, "@") != 1 {
		return ""
	}
	return s
}

// senderLike matches a sender rule's From addresses: the address, or the domain and its
// subdomains.
func senderLike(sender string) string {
	if strings.HasPrefix(sender, "@") {
		return "%" + escapeLike(sender[1:])
	}
	return escapeLike(sender)
}

// inboxSample is the sender and subject of the account's latest inbox conversations, for
// suggestions: what its reader sees in the list, nothing more.
func inboxSample(ctx context.Context, q db.Querier, a *accountRow) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT COALESCE(NULLIF(m.from_addr->>'name', ''), split_part(m.from_addr->>'email', '@', 2)), m.subject
		FROM collab_threads t
		JOIN LATERAL (SELECT m.from_addr, m.subject FROM collab_messages m WHERE m.thread_id = t.id AND m.sent_by IS NULL AND NOT m.draft
			ORDER BY m.sent_at DESC LIMIT 1) m ON true
		WHERE t.account_id = $1 AND t.folder_ids && ARRAY(SELECT id FROM collab_folders WHERE account_id = $1 AND role = 'inbox')
		ORDER BY t.last_message_at DESC LIMIT 60`, a.ID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var from, subject string
		if err := r.Scan(&from, &subject); err != nil {
			return "", err
		}
		return cleanLine(from, 80) + " | " + cleanLine(subject, 160), nil
	})
}

// suggestedCategories cleans Ollie's proposal into a set that passes cleanCategories:
// primary first, names checked, up to 8.
func suggestedCategories(in []SuggestedCategory) []MailCategory {
	out := []MailCategory{presetCategories[0]}
	seen := map[string]bool{categoryPrimary: true}
	for _, c := range in {
		name := displayname.Clean(c.Name, displayname.Workspace)
		if len([]rune(name)) > maxCategoryName {
			name = ""
		}
		key := slugKey(c.Key)
		if key == "" {
			key = slugKey(name)
		}
		if strings.EqualFold(key, categoryPrimary) {
			if name != "" {
				out[0].Name = name
			}
			if d := cleanLine(c.Description, maxCategoryDesc); d != "" {
				out[0].Description = d
			}
			continue
		}
		if name == "" || key == "" || seen[key] || seen[strings.ToLower(name)] || len(out) >= maxCategories {
			continue
		}
		seen[key], seen[strings.ToLower(name)] = true, true
		color := c.Color
		if !slices.Contains(Colors, color) {
			color = "gray"
		}
		out = append(out, MailCategory{Key: key, Name: name, Description: cleanLine(c.Description, maxCategoryDesc), Color: color})
	}
	return out
}
