package core

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/billing"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/brands"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
)

// CoreWorkspaceSettings are the agent-editable workspace options (contracts/api.md).
// Other keys in workspaces.settings (for example "widget") belong to other areas and
// are preserved on update.
type CoreWorkspaceSettings struct {
	BrandColor        *string `json:"brand_color,omitempty" doc:"Hex colour, #RRGGBB"`
	Timezone          *string `json:"timezone,omitempty" doc:"IANA time zone"`
	HelpCenterEnabled *bool   `json:"help_center_enabled,omitempty"`
	HelpCenterTitle   *string `json:"help_center_title,omitempty" maxLength:"100"`
	CSATEnabled       *bool   `json:"csat_enabled,omitempty"`
	AutoAssign        *string `json:"auto_assign,omitempty" enum:"none,round_robin,load_balanced" doc:"Default auto-assignment mode (see /v1/assignment)"`
	Gravatar          *bool   `json:"gravatar,omitempty" doc:"Look up contact pictures on Gravatar (sends a hash of each contact's email to Gravatar). Default false"`
	DomainLogos       *bool   `json:"domain_logos,omitempty" doc:"Fetch company logos from the company's website. Default true"`
	Locale            *string `json:"locale,omitempty" maxLength:"35" doc:"Default language (BCP 47) of customer-facing pages: help center, CSAT, widget"`
	LocaleFixed       *bool   `json:"locale_fixed,omitempty" doc:"Customer-facing pages always use locale (a brand's own, else the workspace's), whatever the visitor's browser. Default false"`
	LabelCreation     *string `json:"label_creation,omitempty" enum:"anyone,admins" doc:"Who may create labels by applying a new name"`
	MessageDelete     *string `json:"message_delete,omitempty" enum:"off,own,all" doc:"Which messages agents may delete: none, their own internal notes, or any agent's notes and replies. Admins may delete any message. Default own"`
	CollabReplies     *bool   `json:"collab_ticket_replies,omitempty" doc:"With Collab, members may answer email tickets from their own mail accounts. Default true"`
	CollabReplyTo     *string `json:"collab_ticket_reply_to,omitempty" enum:"address,mailbox" doc:"Where answers to a reply sent from a member's own address go when it names no Reply-To: the address's own reply-to (else the address), or the ticket's reply address. Default address"`
	UndoSendSeconds   *int    `json:"undo_send_seconds,omitempty" enum:"0,5,10,20,30" doc:"Seconds the app holds an agent's reply on an email (or any other non-chat) ticket, with a way to take it back or make it a note, before sending it. Default 5; 0 sends at once"`
	UndoSendChatSecs  *int    `json:"undo_send_chat_seconds,omitempty" enum:"0,5,10,20,30" doc:"The same for replies on chat tickets. Default 0: a chat reply goes at once"`
	// StatusLabels and PriorityLabels rename and recolour the fixed values for display only.
	StatusLabels   map[string]DisplayOverride `json:"status_labels,omitempty"`
	PriorityLabels map[string]DisplayOverride `json:"priority_labels,omitempty"`
}

// DisplayOverride is a workspace's display name and colour for a status or priority.
type DisplayOverride struct {
	Label string `json:"label,omitempty" maxLength:"40"`
	Color string `json:"color,omitempty" enum:"gray,red,orange,amber,yellow,lime,green,teal,cyan,blue,indigo,violet,purple,pink,rose"`
}

func validOverrides(field string, in map[string]DisplayOverride, allowed []string) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range in {
		if !slices.Contains(allowed, k) {
			return nil, httpx.Validation(field + " keys must be one of " + strings.Join(allowed, ", ") + ".")
		}
		label := displayname.Normalize(v.Label)
		if utf8.RuneCountInString(label) > 40 || strings.ContainsAny(label, "<>`") || strings.IndexFunc(label, unicode.IsControl) >= 0 {
			return nil, httpx.Validation(field + " labels must be 40 characters or less, without markup.")
		}
		if v.Color != "" && !slices.Contains(domain.LabelColors, v.Color) {
			return nil, httpx.Validation(field + " colours must be one of " + strings.Join(domain.LabelColors, ", ") + ".")
		}
		if label == "" && v.Color == "" {
			continue
		}
		o := map[string]any{}
		if label != "" {
			o["label"] = label
		}
		if v.Color != "" {
			o["color"] = v.Color
		}
		out[k] = o
	}
	return out, nil
}

type CoreWorkspace struct {
	ID           uuid.UUID             `json:"id"`
	Name         string                `json:"name"`
	Slug         string                `json:"slug"`
	WidgetKey    string                `json:"widget_key"`
	Settings     CoreWorkspaceSettings `json:"settings"`
	AvatarURL    domain.Avatar         `json:"avatar_url" doc:"Workspace logo"`
	AvatarSource *string               `json:"avatar_source" enum:"upload"`
	Images       WorkspaceImages       `json:"images"`
	CreatedAt    time.Time             `json:"created_at"`
}

// WorkspaceImages are the brand pictures besides the logo.
type WorkspaceImages struct {
	HelpCenterLogo    domain.Avatar `json:"help_center_logo"`
	HelpCenterFavicon domain.Avatar `json:"help_center_favicon"`
	WidgetAvatar      domain.Avatar `json:"widget_avatar"`
	WidgetLauncher    domain.Avatar `json:"widget_launcher"`
}

type CoreMembership struct {
	Workspace CoreWorkspace `json:"workspace"`
	Role      string        `json:"role" enum:"owner,admin,agent,viewer"`
	// Suspended: an instance admin suspended the workspace; its routes answer
	// 403 workspace_suspended until it is restored (contracts/admin.md).
	Suspended bool `json:"suspended"`
}

// workspaceCols reads the widget key and the help center and widget pictures from the
// default brand and its first widget, which own them (contracts/teams.md).
const workspaceCols = `w.id, w.name, w.slug,
	COALESCE((SELECT g.key FROM widgets g WHERE g.id = (` + primaryWidget + `)), w.widget_key),
	w.settings, w.avatar_url, w.brand_images, w.created_at,
	COALESCE((SELECT b.images FROM brands b WHERE b.workspace_id = w.id AND b.is_default), '{}'),
	COALESCE((SELECT g.images FROM widgets g WHERE g.id = (` + primaryWidget + `)), '{}')`

// primaryWidget is brands.PrimaryWidgetSQL for the workspace aliased w.
const primaryWidget = `SELECT g.id FROM widgets g JOIN brands b ON b.id = g.brand_id AND b.is_default
	WHERE g.workspace_id = w.id ORDER BY g.position, g.created_at, g.id LIMIT 1`

func scanWorkspace(row pgx.Row, extra ...any) (CoreWorkspace, error) {
	var w CoreWorkspace
	var raw []byte
	var ws, brand, widget map[string]string
	err := row.Scan(append([]any{&w.ID, &w.Name, &w.Slug, &w.WidgetKey, &raw, &w.AvatarURL, &ws, &w.CreatedAt, &brand, &widget}, extra...)...)
	if err != nil {
		return w, err
	}
	_ = json.Unmarshal(raw, &w.Settings)
	if w.AvatarURL != "" {
		w.AvatarSource = ptr("upload")
	}
	w.Images = WorkspaceImages{HelpCenterLogo: domain.Avatar(brand["help_center_logo"]), HelpCenterFavicon: domain.Avatar(brand["help_center_favicon"]),
		WidgetAvatar: domain.Avatar(widget["avatar"]), WidgetLauncher: domain.Avatar(widget["launcher"])}
	return w, nil
}

func loadWorkspace(ctx context.Context, q db.Querier, wid uuid.UUID) (CoreWorkspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceCols+` FROM workspaces w WHERE w.id = $1`, wid))
}

func loadMemberships(ctx context.Context, q db.Querier, uid uuid.UUID) ([]CoreMembership, error) {
	rows, err := q.Query(ctx, `SELECT `+workspaceCols+`, m.role, w.suspended_at IS NOT NULL FROM members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 ORDER BY m.created_at LIMIT 200`, uid)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreMembership, error) {
		var m CoreMembership
		w, err := scanWorkspace(r, &m.Role, &m.Suspended)
		m.Workspace = w
		return m, err
	})
	if out == nil {
		out = []CoreMembership{}
	}
	return out, err
}

var (
	hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	slugDashRe = regexp.MustCompile(`-+`)
)

var localeRe = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8}){0,3}$`)

// validateSettings checks a settings patch and returns the JSON object to merge.
func validateSettings(s *CoreWorkspaceSettings) (map[string]any, error) {
	out := map[string]any{}
	if s.BrandColor != nil {
		if *s.BrandColor != "" && !hexColorRe.MatchString(*s.BrandColor) {
			return nil, httpx.Validation("brand_color must be a hex colour like #0A0A0A.")
		}
		out["brand_color"] = strings.ToUpper(*s.BrandColor)
	}
	if s.Timezone != nil {
		if _, err := time.LoadLocation(*s.Timezone); err != nil || *s.Timezone == "" || *s.Timezone == "Local" {
			return nil, httpx.Validation("timezone must be an IANA time zone like Europe/Berlin.")
		}
		out["timezone"] = *s.Timezone
	}
	if s.HelpCenterEnabled != nil {
		out["help_center_enabled"] = *s.HelpCenterEnabled
	}
	if s.HelpCenterTitle != nil {
		t := displayname.Normalize(*s.HelpCenterTitle)
		if utf8.RuneCountInString(t) > 100 || strings.ContainsAny(t, "<>`") || strings.IndexFunc(t, unicode.IsControl) >= 0 {
			return nil, httpx.Validation("help_center_title must be 100 characters or less, without markup.")
		}
		out["help_center_title"] = t
	}
	if s.CSATEnabled != nil {
		out["csat_enabled"] = *s.CSATEnabled
	}
	if s.Locale != nil {
		if *s.Locale != "" && !localeRe.MatchString(*s.Locale) {
			return nil, httpx.Validation("locale must be a language tag like en or pt-BR.")
		}
		out["locale"] = *s.Locale
	}
	if s.LocaleFixed != nil {
		out["locale_fixed"] = *s.LocaleFixed
	}
	if s.LabelCreation != nil {
		if *s.LabelCreation != "anyone" && *s.LabelCreation != "admins" {
			return nil, httpx.Validation("label_creation must be anyone or admins.")
		}
		out["label_creation"] = *s.LabelCreation
	}
	if s.StatusLabels != nil {
		m, err := validOverrides("status_labels", s.StatusLabels, domain.Statuses)
		if err != nil {
			return nil, err
		}
		out["status_labels"] = m
	}
	if s.PriorityLabels != nil {
		m, err := validOverrides("priority_labels", s.PriorityLabels, domain.Priorities)
		if err != nil {
			return nil, err
		}
		out["priority_labels"] = m
	}
	if s.AutoAssign != nil {
		if !slices.Contains([]string{"none", "round_robin", "load_balanced"}, *s.AutoAssign) {
			return nil, httpx.Validation("auto_assign must be none, round_robin or load_balanced.")
		}
		out["auto_assign"] = *s.AutoAssign
	}
	if s.MessageDelete != nil {
		if !slices.Contains([]string{"off", "own", "all"}, *s.MessageDelete) {
			return nil, httpx.Validation("message_delete must be off, own or all.")
		}
		out["message_delete"] = *s.MessageDelete
	}
	for key, v := range map[string]*int{"undo_send_seconds": s.UndoSendSeconds, "undo_send_chat_seconds": s.UndoSendChatSecs} {
		if v == nil {
			continue
		}
		if !slices.Contains([]int{0, 5, 10, 20, 30}, *v) {
			return nil, httpx.Validation(key + " must be 0, 5, 10, 20 or 30.")
		}
		out[key] = *v
	}
	if s.Gravatar != nil {
		out["gravatar"] = *s.Gravatar
	}
	if s.DomainLogos != nil {
		out["domain_logos"] = *s.DomainLogos
	}
	if s.CollabReplies != nil {
		out["collab_ticket_replies"] = *s.CollabReplies
	}
	if s.CollabReplyTo != nil {
		if !slices.Contains([]string{"address", "mailbox"}, *s.CollabReplyTo) {
			return nil, httpx.Validation("collab_ticket_reply_to must be address or mailbox.")
		}
		out["collab_ticket_reply_to"] = *s.CollabReplyTo
	}
	return out, nil
}

// slugify makes a URL slug from a workspace name.
func slugify(name string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(strings.ToLower(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.Is(unicode.Mn, r):
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(slugDashRe.ReplaceAllString(b.String(), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if len(s) < 2 {
		s = "workspace"
	}
	return s
}

// createWorkspace creates a workspace owned by owner and seeds its defaults, including its
// default brand and widget. p is who created it, for the audit log.
func (s *svc) createWorkspace(ctx context.Context, tx pgx.Tx, p *auth.Principal, owner uuid.UUID, name string) (CoreWorkspace, error) {
	wid := ids.New()
	base := slugify(name)
	slug := base
	for i := 0; ; i++ {
		// The default brand takes the workspace slug as its help center address, so the
		// slug must be free among brands too.
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workspaces WHERE slug = $1)
			OR EXISTS (SELECT 1 FROM brands WHERE slug = $1)`, slug).Scan(&taken); err != nil {
			return CoreWorkspace{}, err
		}
		if !taken {
			break
		}
		// The address is picked for the workspace, never asked of it: a longer suffix after a
		// few tries, so a name is never refused over it. Admins change it in brand settings.
		suffix := crypto.RandomString(4)
		if i >= 4 {
			suffix = crypto.RandomString(10)
		}
		slug = strings.Trim(base[:min(len(base), 39-len(suffix))], "-") + "-" + suffix
	}
	settings := map[string]any{"timezone": "UTC", "help_center_enabled": true, "help_center_title": name + " help center",
		"csat_enabled": false, "auto_assign": "none"}
	if utf8.RuneCountInString(settings["help_center_title"].(string)) > 100 {
		settings["help_center_title"] = "Help center"
	}
	widgetKey := brands.NewWidgetKey()
	if _, err := tx.Exec(ctx, `INSERT INTO workspaces (id, name, slug, widget_key, settings) VALUES ($1, $2, $3, $4, $5)`,
		wid, name, slug, widgetKey, settings); err != nil {
		return CoreWorkspace{}, err
	}
	if _, err := brands.CreateDefaults(ctx, tx, s.d.Box, wid, name, slug, settings["help_center_title"].(string), widgetKey); err != nil {
		return CoreWorkspace{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, wid, owner); err != nil {
		return CoreWorkspace{}, err
	}
	if err := plan.Ensure(ctx, tx, wid); err != nil {
		return CoreWorkspace{}, err
	}
	views := []struct {
		name    string
		filters map[string]any
	}{
		{"Unassigned", map[string]any{"status": []string{"open", "pending"}, "assignee": "none"}},
		{"My open tickets", map[string]any{"status": []string{"open", "pending"}, "assignee": "me"}},
		{"All open", map[string]any{"status": []string{"open"}}},
	}
	for i, v := range views {
		if _, err := tx.Exec(ctx, `INSERT INTO views (id, workspace_id, user_id, name, filters, position) VALUES ($1, $2, NULL, $3, $4, $5)`,
			ids.New(), wid, v.name, v.filters, i); err != nil {
			return CoreWorkspace{}, err
		}
	}
	typeGroup := ids.New()
	if _, err := tx.Exec(ctx, `INSERT INTO tag_groups (id, workspace_id, name, exclusive, position) VALUES ($1, $2, 'Type', true, 0)`,
		typeGroup, wid); err != nil {
		return CoreWorkspace{}, err
	}
	for i, t := range []struct {
		name, color, desc string
		grouped           bool
	}{
		{"Question", "blue", "How do I, can I, where is", true},
		{"Problem", "orange", "Something is not working as expected", true},
		{"Bug", "red", "A defect in the product", true},
		{"Feature request", "violet", "Asks for something new", true},
		{"Billing", "green", "Invoices, payments and plans", true},
		{"VIP", "amber", "A customer to look after closely", false},
		{"Feedback", "teal", "Praise, complaints and suggestions", false},
	} {
		var group *uuid.UUID
		if t.grouped {
			group = &typeGroup
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tags (id, workspace_id, name, color, description, group_id, position) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			ids.New(), wid, t.name, t.color, t.desc, group, i); err != nil {
			return CoreWorkspace{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sla_policies (id, workspace_id, name, priority, first_response_minutes, resolution_minutes, enabled, position)
		VALUES ($1, $2, 'Default', NULL, 240, 2880, true, 0)`, ids.New(), wid); err != nil {
		return CoreWorkspace{}, err
	}
	if err := audit.Log(ctx, tx, p, wid, "workspace.created", "workspace", &wid, map[string]any{"name": name}); err != nil {
		return CoreWorkspace{}, err
	}
	return loadWorkspace(ctx, tx, wid)
}

// fileKeys lists the stored objects of a workspace, removed after the rows are.
func fileKeys(ctx context.Context, q db.Querier, wid uuid.UUID) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT storage_key FROM attachments WHERE workspace_id = $1
		UNION ALL SELECT raw_storage_key FROM inbound_emails WHERE workspace_id = $1 AND raw_storage_key <> ''
		UNION ALL SELECT storage_key || '/256.png' FROM images WHERE workspace_id = $1
		UNION ALL SELECT storage_key || '/64.png' FROM images WHERE workspace_id = $1
		UNION ALL SELECT storage_key FROM ai_knowledge_sources WHERE workspace_id = $1 AND storage_key IS NOT NULL
		UNION ALL SELECT storage_key FROM imports WHERE workspace_id = $1 AND storage_key IS NOT NULL
		UNION ALL SELECT storage_key FROM import_uploads WHERE workspace_id = $1`, wid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// purgeFiles deletes stored objects in the background after their rows are gone.
func (s *svc) purgeFiles(keys []string) {
	if len(keys) == 0 || s.d.Storage == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		failed := 0
		for _, k := range keys {
			if k == "" {
				continue
			}
			if err := s.d.Storage.Delete(ctx, k); err != nil {
				failed++
			}
		}
		if failed > 0 {
			s.d.Log.Warn("purge files", "failed", failed, "total", len(keys))
		}
	}()
}

// deleteWorkspace removes a workspace and everything it owns. It returns the members
// to disconnect and the stored files to purge.
func (s *svc) deleteWorkspace(ctx context.Context, tx pgx.Tx, wid uuid.UUID) ([]uuid.UUID, []string, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM members WHERE workspace_id = $1`, wid)
	if err != nil {
		return nil, nil, err
	}
	members, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, nil, err
	}
	keys, err := fileKeys(ctx, tx, wid)
	if err != nil {
		return nil, nil, err
	}
	if err := billing.EnqueueCancel(ctx, s.d, tx, wid); err != nil {
		return nil, nil, err
	}
	// Help center domains release their certificates (contracts/teams.md "Help center addresses").
	rows, err = tx.Query(ctx, `SELECT provider_id FROM help_domains WHERE workspace_id = $1 AND provider_id IS NOT NULL`, wid)
	if err != nil {
		return nil, nil, err
	}
	providers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, nil, err
	}
	for _, id := range providers {
		if _, err := s.d.Jobs.InsertTx(ctx, tx, jobs.HelpDomainReleaseArgs{ProviderID: id}, nil); err != nil {
			return nil, nil, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, wid); err != nil {
		return nil, nil, err
	}
	return members, keys, nil
}

func (s *svc) registerWorkspaces(api huma.API) {
	tags := []string{"Workspace"}

	register(s, api, huma.Operation{OperationID: "create-workspace", Method: http.MethodPost, Path: "/v1/workspaces",
		Summary: "Create a workspace", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				Name string `json:"name" maxLength:"200"`
			}
		}) (*struct{ Body CoreWorkspace }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("workspace:create:"+p.UserID.String(), 10, time.Hour); err != nil {
				return nil, err
			}
			name, err := displayname.Validate("Workspace name", in.Body.Name, displayname.Workspace, false)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var w CoreWorkspace
			err = s.tx(ctx, func(tx pgx.Tx) error {
				w, err = s.createWorkspace(ctx, tx, p, p.UserID, name)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.d.Analytics.Capture(p.UserID.String(), w.ID.String(), "workspace_created", nil)
			return &struct{ Body CoreWorkspace }{w}, nil
		})

	register(s, api, huma.Operation{OperationID: "get-workspace", Method: http.MethodGet, Path: "/v1/workspace",
		Summary: "Get the current workspace", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body CoreWorkspace }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			w, err := loadWorkspace(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreWorkspace }{w}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-workspace", Method: http.MethodPatch, Path: "/v1/workspace",
		Summary: "Update the current workspace", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				Name     *string                `json:"name,omitempty" maxLength:"200"`
				Settings *CoreWorkspaceSettings `json:"settings,omitempty"`
			}
		}) (*struct{ Body CoreWorkspace }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var name *string
			if in.Body.Name != nil {
				n, err := displayname.Validate("Workspace name", *in.Body.Name, displayname.Workspace, false)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				name = &n
			}
			patch := map[string]any{}
			if in.Body.Settings != nil {
				if patch, err = validateSettings(in.Body.Settings); err != nil {
					return nil, err
				}
			}
			var w CoreWorkspace
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE workspaces SET name = COALESCE($2, name), settings = settings || $3::jsonb, updated_at = now()
					WHERE id = $1`, p.WorkspaceID, name, patch); err != nil {
					return err
				}
				changed := make([]string, 0, len(patch)+1)
				if name != nil {
					changed = append(changed, "name")
				}
				for k := range patch {
					changed = append(changed, "settings."+k)
				}
				if len(changed) > 0 {
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "workspace.updated", "workspace", &p.WorkspaceID,
						map[string]any{"changes": changed}); err != nil {
						return err
					}
				}
				if err := requeuePictures(ctx, tx, p.WorkspaceID, patch); err != nil {
					return err
				}
				if err := brands.SyncDefault(ctx, tx, s.d.Cfg, p.WorkspaceID, patch); err != nil {
					return err
				}
				if w, err = loadWorkspace(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				if len(changed) == 0 {
					return nil
				}
				return publishWorkspace(ctx, tx, w)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreWorkspace }{w}, nil
		})

	register(s, api, huma.Operation{OperationID: "delete-workspace", Method: http.MethodDelete, Path: "/v1/workspace",
		Summary: "Delete the current workspace and all its data", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				ConfirmName string `json:"confirm_name" maxLength:"200"`
			}
		}) (*struct{}, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleOwner)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.requireMFA(ctx, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			var members []uuid.UUID
			var keys []string
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var name string
				if err := tx.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1 FOR UPDATE`, p.WorkspaceID).Scan(&name); err != nil {
					return err
				}
				if strings.TrimSpace(in.Body.ConfirmName) != name {
					return httpx.E(422, "confirm_name_mismatch", "Type the workspace name exactly to confirm.")
				}
				members, keys, err = s.deleteWorkspace(ctx, tx, p.WorkspaceID)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.d.Log.Info("workspace deleted", "workspace_id", p.WorkspaceID, "actor_user_id", p.UserID, "ip", p.IP)
			s.purgeFiles(keys)
			for _, uid := range members {
				s.disconnect(uid, uuid.Nil)
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "transfer-workspace", Method: http.MethodPost, Path: "/v1/workspace/transfer",
		Summary: "Transfer workspace ownership", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				UserID uuid.UUID `json:"user_id"`
			}
		}) (*struct{ Body CoreMember }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleOwner)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.requireMFA(ctx, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			if in.Body.UserID == p.UserID {
				return nil, httpx.Validation("You already own this workspace.")
			}
			var m CoreMember
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var role string
				if err := tx.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`,
					p.WorkspaceID, in.Body.UserID).Scan(&role); err != nil {
					if db.IsNotFound(err) {
						return httpx.NotFound("Member")
					}
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE members SET role = 'admin' WHERE workspace_id = $1 AND user_id = $2`, p.WorkspaceID, p.UserID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE members SET role = 'owner' WHERE workspace_id = $1 AND user_id = $2`, p.WorkspaceID, in.Body.UserID); err != nil {
					return err
				}
				for _, uid := range []uuid.UUID{p.UserID, in.Body.UserID} {
					mm, err := loadMember(ctx, tx, p.WorkspaceID, uid)
					if err != nil {
						return err
					}
					if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "member.updated",
						Topics: []string{events.UserTopic(uid)}, Data: map[string]any{"member": mm}}); err != nil {
						return err
					}
					if uid == in.Body.UserID {
						m = mm
					}
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "workspace.transferred", "user", &in.Body.UserID,
					map[string]any{"from_user_id": p.UserID, "previous_role": role})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreMember }{m}, nil
		})

	register(s, api, huma.Operation{OperationID: "rotate-widget-secret", Method: http.MethodPost, Path: "/v1/workspace/widget-secret/rotate",
		Summary: "Rotate the chat widget identity secret", Description: "The default brand's first widget; see /v1/widgets/{id}/secret/rotate.",
		Tags: tags},
		func(ctx context.Context, _ *struct{}) (*widgetSecretOut, error) {
			return s.rotateWidgetSecret(ctx, nil)
		})

	register(s, api, huma.Operation{OperationID: "rotate-widget-secret-by-id", Method: http.MethodPost, Path: "/v1/widgets/{id}/secret/rotate",
		Summary: "Rotate a chat widget's identity secret", Description: "Answers the new secret once; the previous one stops verifying user_hash.",
		Tags: []string{"Widget"}},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*widgetSecretOut, error) {
			id, err := parseID(in.ID, "Widget")
			if err != nil {
				return nil, err
			}
			return s.rotateWidgetSecret(ctx, &id)
		})
}

type widgetSecretOut struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		Secret string `json:"secret"`
	}
}

// rotateWidgetSecret replaces a widget's identity secret (the default brand's first widget
// when id is nil). An owner-level operation: a second factor when the user has one, and a
// recent sign-in.
func (s *svc) rotateWidgetSecret(ctx context.Context, id *uuid.UUID) (*widgetSecretOut, error) {
	p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if err := s.requireMFA(ctx, p); err != nil {
		return nil, httpx.Wrap(err)
	}
	if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
		return nil, httpx.Wrap(err)
	}
	secret, sealed, err := brands.NewWidgetSecret(s.d.Box)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var widgetID uuid.UUID
		if id == nil {
			widgetID, err = brands.PrimaryWidgetID(ctx, tx, p.WorkspaceID)
		} else {
			err = tx.QueryRow(ctx, `SELECT id FROM widgets WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, *id).Scan(&widgetID)
		}
		if db.IsNotFound(err) {
			return httpx.NotFound("Widget")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE widgets SET secret_enc = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			p.WorkspaceID, widgetID, sealed); err != nil {
			return err
		}
		if id == nil {
			return audit.Log(ctx, tx, p, p.WorkspaceID, "workspace.widget_secret_rotated", "widget", &widgetID, nil)
		}
		return audit.Log(ctx, tx, p, p.WorkspaceID, "widget.secret_rotated", "widget", &widgetID, nil)
	})
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	out := &widgetSecretOut{CacheControl: "no-store"}
	out.Body.Secret = secret
	return out, nil
}

// publishWorkspace sends workspace.updated.
func publishWorkspace(ctx context.Context, tx pgx.Tx, w CoreWorkspace) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: w.ID, Type: "workspace.updated", Data: map[string]any{"workspace": w}})
	return err
}

// requeuePictures schedules picture lookups when the automatic sources were switched:
// on looks up every record without a picture, off lets the refresh job remove the
// pictures that source provided.
func requeuePictures(ctx context.Context, tx pgx.Tx, wid uuid.UUID, patch map[string]any) error {
	if v, ok := patch["gravatar"].(bool); ok {
		q := `UPDATE contacts SET avatar_retry_at = now(), avatar_failures = 0 WHERE workspace_id = $1 AND avatar_source = 'gravatar'`
		if v {
			q = `UPDATE contacts SET avatar_retry_at = now(), avatar_failures = 0 WHERE workspace_id = $1 AND avatar_url = '' AND email IS NOT NULL`
		}
		if _, err := tx.Exec(ctx, q, wid); err != nil {
			return err
		}
	}
	if v, ok := patch["domain_logos"].(bool); ok {
		q := `UPDATE companies SET avatar_retry_at = now(), avatar_failures = 0 WHERE workspace_id = $1 AND avatar_source = 'domain'`
		if v {
			q = `UPDATE companies SET avatar_retry_at = now(), avatar_failures = 0 WHERE workspace_id = $1 AND avatar_url = '' AND domain <> ''`
		}
		if _, err := tx.Exec(ctx, q, wid); err != nil {
			return err
		}
	}
	return nil
}
