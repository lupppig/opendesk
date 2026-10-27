package collab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Inbox categories (contracts/collab.md "Categories"): the workspace's default, a team's,
// or an account's own, and which of them an account follows. Not Outlook categories,
// which are labels (categories.go).

const (
	categoryPrimary    = "primary"
	minCategories      = 2
	maxCategories      = 8
	maxCategoryName    = 40
	maxCategoryDesc    = 300
	maxCategoryRules   = 500
	categoryResortDays = 30
	categoryResortMax  = 300
)

// Scopes and modes of collab_category_sets.
const (
	scopeWorkspace = "workspace"
	scopeTeam      = "team"
	scopeAccount   = "account"

	modeOn     = "on"
	modeOff    = "off"
	modeFollow = "follow"
	modeCustom = "custom" // an account's "on", as the API names it
)

// Where an account's categories come from (MailAccountCategories.source).
const (
	sourceAccount   = "account"
	sourceTeam      = "team"
	sourceWorkspace = "workspace"
	sourceNone      = "none"
)

// MailCategory is one tab of an inbox.
type MailCategory struct {
	Key         string `json:"key" required:"false" maxLength:"40" doc:"1..40 of a-z, 0-9, - and _; made from the name when left out; primary is the first, always"`
	Name        string `json:"name" maxLength:"40"`
	Description string `json:"description" required:"false" maxLength:"300" doc:"What belongs in it, for Ollie"`
	Color       string `json:"color" required:"false" doc:"A tone key; default gray"`
}

// presetCategories are the built-in categories; the first four are the default set.
var presetCategories = []MailCategory{
	{Key: categoryPrimary, Name: "Primary", Color: "gray",
		Description: "Mail from people that needs you: replies, questions, requests and conversations."},
	{Key: "updates", Name: "Updates", Color: "blue",
		Description: "Automatic notifications: receipts, orders, shipping, invoices, statements, account and security alerts, calendar and app notifications."},
	{Key: "newsletters", Name: "Newsletters", Color: "violet",
		Description: "Newsletters, digests and articles from lists you subscribed to."},
	{Key: "promotions", Name: "Promotions", Color: "amber",
		Description: "Marketing, offers, sales, discounts, event invitations from brands and cold sales outreach."},
	{Key: "social", Name: "Social", Color: "teal",
		Description: "Notifications from social networks, communities and forums."},
}

// MailCategorySet is the workspace's or a team's categories.
type MailCategorySet struct {
	Enabled    bool            `json:"enabled"`
	Categories []MailCategory  `json:"categories"`
	UpdatedAt  time.Time       `json:"updated_at"`
	UpdatedBy  *domain.UserRef `json:"updated_by"`
}

// WorkspaceCategorySet is the workspace's default.
type WorkspaceCategorySet struct {
	MailCategorySet
	Locked bool `json:"locked" doc:"Members who are not admins cannot choose their own"`
}

// TeamCategorySet is a team's own categories.
type TeamCategorySet struct {
	MailCategorySet
	TeamID   uuid.UUID `json:"team_id"`
	TeamName string    `json:"team_name"`
	Position int       `json:"position"`
}

// MailCategorySettings answers GET /v1/collab/categories.
type MailCategorySettings struct {
	Workspace *WorkspaceCategorySet `json:"workspace"`
	Teams     []TeamCategorySet     `json:"teams"`
	Presets   []MailCategory        `json:"presets"`
	Sorting   string                `json:"sorting" enum:"ai,rules"`
	Suggest   bool                  `json:"suggest"`
}

// CategoryTeam names the team an account's categories come from.
type CategoryTeam struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// MailAccountCategories is how an account's inbox is split (MailAccount.categories).
type MailAccountCategories struct {
	Enabled    bool           `json:"enabled"`
	Categories []MailCategory `json:"categories"`
	Source     string         `json:"source" enum:"account,team,workspace,none"`
	Team       *CategoryTeam  `json:"team"`
	Mode       string         `json:"mode" enum:"follow,custom,off"`
	Own        []MailCategory `json:"own"`
	AI         bool           `json:"ai"`
	Locked     bool           `json:"locked"`
}

// categorySetRow is a stored collab_category_sets row.
type categorySetRow struct {
	ID         uuid.UUID
	Scope      string
	TeamID     *uuid.UUID
	AccountID  *uuid.UUID
	Mode       string
	Categories []MailCategory
	Locked     bool
	AI         bool
	ByAdmin    bool
	Position   int
	UpdatedBy  *uuid.UUID
	UpdatedAt  time.Time
}

const categorySetColumns = `id, scope, team_id, account_id, mode, categories, locked, ai, by_admin, position, updated_by, updated_at`

func scanCategorySet(row pgx.Row) (*categorySetRow, error) {
	var r categorySetRow
	if err := row.Scan(&r.ID, &r.Scope, &r.TeamID, &r.AccountID, &r.Mode, &r.Categories, &r.Locked, &r.AI, &r.ByAdmin,
		&r.Position, &r.UpdatedBy, &r.UpdatedAt); err != nil {
		return nil, err
	}
	if r.Categories == nil {
		r.Categories = []MailCategory{}
	}
	return &r, nil
}

// categorySets are a workspace's sets, with what resolving accounts needs.
type categorySets struct {
	workspace *categorySetRow
	teams     []*categorySetRow // by position
	accounts  map[uuid.UUID]*categorySetRow
	// ownerTeams are owners' teams that have a set; teamNames those teams' names.
	ownerTeams map[uuid.UUID][]uuid.UUID
	teamNames  map[uuid.UUID]string
}

// loadCategorySets loads the workspace's sets, the account sets of accountIDs and the
// team memberships of owners.
func loadCategorySets(ctx context.Context, q db.Querier, wid uuid.UUID, accountIDs, owners []uuid.UUID) (*categorySets, error) {
	cs := &categorySets{accounts: map[uuid.UUID]*categorySetRow{}, ownerTeams: map[uuid.UUID][]uuid.UUID{}, teamNames: map[uuid.UUID]string{}}
	rows, err := q.Query(ctx, `SELECT `+categorySetColumns+` FROM collab_category_sets
		WHERE workspace_id = $1 AND (scope <> 'account' OR account_id = ANY($2)) ORDER BY position, updated_at`, wid, nonNil(accountIDs))
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*categorySetRow, error) { return scanCategorySet(r) })
	if err != nil {
		return nil, err
	}
	var teamIDs []uuid.UUID
	for _, r := range list {
		switch r.Scope {
		case scopeWorkspace:
			cs.workspace = r
		case scopeTeam:
			cs.teams = append(cs.teams, r)
			teamIDs = append(teamIDs, *r.TeamID)
		case scopeAccount:
			cs.accounts[*r.AccountID] = r
		}
	}
	if len(teamIDs) == 0 {
		return cs, nil
	}
	rows, err = q.Query(ctx, `SELECT id, name FROM teams WHERE workspace_id = $1 AND id = ANY($2)`, wid, teamIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		cs.teamNames[id] = name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(owners) == 0 {
		return cs, nil
	}
	rows, err = q.Query(ctx, `SELECT user_id, team_id FROM team_members WHERE team_id = ANY($1) AND user_id = ANY($2)`, teamIDs, owners)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u, t uuid.UUID
		if err := rows.Scan(&u, &t); err != nil {
			return nil, err
		}
		cs.ownerTeams[u] = append(cs.ownerTeams[u], t)
	}
	return cs, rows.Err()
}

// loadAccountCategorySets loads what resolving one account needs.
func loadAccountCategorySets(ctx context.Context, q db.Querier, a *accountRow) (*categorySets, error) {
	var owners []uuid.UUID
	if a.OwnerID != nil {
		owners = []uuid.UUID{*a.OwnerID}
	}
	return loadCategorySets(ctx, q, a.WorkspaceID, []uuid.UUID{a.ID}, owners)
}

func (cs *categorySets) locked() bool { return cs.workspace != nil && cs.workspace.Locked }

// resolve is the categories the account follows ("Who decides").
func (cs *categorySets) resolve(a *accountRow) MailAccountCategories {
	out := MailAccountCategories{Categories: []MailCategory{}, Source: sourceNone, Mode: modeFollow, Own: []MailCategory{}, AI: true}
	own := cs.accounts[a.ID]
	if own != nil {
		out.AI = own.AI
		out.Own = own.Categories
		switch own.Mode {
		case modeOn:
			out.Mode = modeCustom
		case modeOff:
			out.Mode = modeOff
		}
	}
	set := func(r *categorySetRow, source string) {
		out.Source = source
		out.Enabled = r.Mode == modeOn && len(r.Categories) >= minCategories
		if out.Enabled {
			out.Categories = r.Categories
		}
	}
	if own != nil && own.Mode != modeFollow && (!cs.locked() || own.ByAdmin) {
		set(own, sourceAccount)
		return out
	}
	if a.OwnerID != nil {
		mine := cs.ownerTeams[*a.OwnerID]
		for _, t := range cs.teams {
			if slices.Contains(mine, *t.TeamID) {
				set(t, sourceTeam)
				out.Team = &CategoryTeam{ID: *t.TeamID, Name: cs.teamNames[*t.TeamID]}
				return out
			}
		}
	}
	if cs.workspace != nil {
		set(cs.workspace, sourceWorkspace)
	}
	return out
}

// forCaller adds whether the caller may change the account's choice.
func (c MailAccountCategories) forCaller(p *auth.Principal, cs *categorySets) MailAccountCategories {
	c.Locked = cs.locked() && !p.Role.AtLeast(auth.RoleAdmin)
	return c
}

// categoryKeys are the keys an inbox is sorted with ([] when not split).
func categoryKeys(c MailAccountCategories) []string {
	out := []string{}
	if !c.Enabled {
		return out
	}
	for _, x := range c.Categories {
		out = append(out, x.Key)
	}
	return out
}

// categorySig hashes what sorting depends on: the categories and whether Ollie may sort.
// A change sorts the account's recent mail again.
func categorySig(c MailAccountCategories, aiOn bool) string {
	if !c.Enabled {
		return ""
	}
	b, _ := json.Marshal(struct {
		C  []MailCategory `json:"c"`
		AI bool           `json:"ai"`
	}{c.Categories, c.AI && aiOn})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:12])
}

// effectiveCategory is a conversation's key for its account's keys: nil when the inbox
// is not split, primary when its own key is not one of them.
func effectiveCategory(keys []string, c *string) *string {
	if len(keys) == 0 {
		return nil
	}
	if c != nil && slices.Contains(keys, *c) {
		return c
	}
	p := categoryPrimary
	return &p
}

var categoryKeyRe = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

// slugKey makes a key from a name.
func slugKey(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if b.Len() >= 36 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// cleanCategories checks a set's categories: 2..8, primary first, keys and names once.
func cleanCategories(in []MailCategory) ([]MailCategory, error) {
	if len(in) < minCategories || len(in) > maxCategories {
		return nil, httpx.Validation("Use 2 to 8 categories.")
	}
	out := make([]MailCategory, 0, len(in))
	keys, names := map[string]bool{}, map[string]bool{}
	for i, c := range in {
		name, r := displayname.Check(c.Name, displayname.Workspace)
		if r != displayname.OK || utf8.RuneCountInString(name) > maxCategoryName {
			return nil, httpx.Validation("Give each category a name of up to 40 characters, without links.")
		}
		key := strings.ToLower(strings.TrimSpace(c.Key))
		if key == "" {
			key = slugKey(name)
			if i == 0 {
				key = categoryPrimary
			}
			if key == "" || key == categoryPrimary && i > 0 {
				key = "category"
			}
			for base, n := key, 2; keys[key]; n++ {
				key = base + "-" + strconv.Itoa(n)
			}
		}
		if !categoryKeyRe.MatchString(key) {
			return nil, httpx.Validation("A category key is 1 to 40 of a-z, 0-9, - and _.")
		}
		if (i == 0) != (key == categoryPrimary) {
			return nil, httpx.Validation("Primary comes first, once.")
		}
		if keys[key] || names[strings.ToLower(name)] {
			return nil, httpx.Validation("Each category needs its own name.")
		}
		keys[key], names[strings.ToLower(name)] = true, true
		color := c.Color
		if color == "" {
			color = "gray"
		}
		if !slices.Contains(Colors, color) {
			return nil, httpx.Validation("Choose one of the category colours.")
		}
		out = append(out, MailCategory{Key: key, Name: name, Description: cleanLine(c.Description, maxCategoryDesc), Color: color})
	}
	return out, nil
}
