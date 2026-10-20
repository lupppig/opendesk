package widget

import (
	"context"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/brands"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/images"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// widgetRow is a widget with what its public routes show of its brand and workspace.
type widgetRow struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	BrandID     uuid.UUID
	TeamID      *uuid.UUID
	SecretEnc   []byte
	Settings    hd.WidgetSettings
	Images      map[string]string
	// The workspace.
	WorkspaceName string
	WSSettings    hd.Settings
	Logo          domain.Avatar
	WSImages      map[string]string
	// The brand.
	BrandName       string
	BrandSlug       string
	BrandColor      string
	BrandLocale     string
	BrandHelpCenter bool
	BrandImages     map[string]string
	BrandHelpHost   string // the brand's active help center domain, or ""
}

var widgetRowSelect = `SELECT g.id, g.workspace_id, g.brand_id, g.team_id, g.secret_enc, g.settings, g.images,
	w.name, w.settings, w.avatar_url, w.brand_images,
	b.name, b.slug, b.color, b.locale, b.help_center_enabled, b.images, COALESCE(` + brands.ActiveHostSQL("b.id") + `, '')
FROM widgets g JOIN workspaces w ON w.id = g.workspace_id JOIN brands b ON b.id = g.brand_id`

func scanWidgetRow(row pgx.Row) (widgetRow, error) {
	var g widgetRow
	var raw, wsRaw []byte
	err := row.Scan(&g.ID, &g.WorkspaceID, &g.BrandID, &g.TeamID, &g.SecretEnc, &raw, &g.Images,
		&g.WorkspaceName, &wsRaw, &g.Logo, &g.WSImages,
		&g.BrandName, &g.BrandSlug, &g.BrandColor, &g.BrandLocale, &g.BrandHelpCenter, &g.BrandImages, &g.BrandHelpHost)
	if err != nil {
		return g, err
	}
	g.Settings = hd.ParseWidgetSettings(raw)
	g.WSSettings = hd.ParseSettings(wsRaw)
	return g, nil
}

// loadByKey finds the widget of a key, refusing unknown keys, disabled widgets and the
// widgets of a suspended workspace alike.
func loadByKey(ctx context.Context, q db.Querier, key string) (widgetRow, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 200 {
		return widgetRow{}, httpx.NotFound("Widget")
	}
	g, err := scanWidgetRow(q.QueryRow(ctx, widgetRowSelect+` WHERE g.key = $1 AND w.suspended_at IS NULL`, key))
	if db.IsNotFound(err) {
		return g, httpx.NotFound("Widget")
	}
	if err != nil {
		return g, err
	}
	if !g.Settings.Enabled {
		return g, httpx.NotFound("Widget")
	}
	return g, nil
}

// loadVisitorWidget returns the widget a visitor session started in; a session from
// before widgets existed belongs to the default brand's first widget.
func loadVisitorWidget(ctx context.Context, q db.Querier, p *auth.Principal) (widgetRow, error) {
	id := p.WidgetID
	if id == uuid.Nil {
		var err error
		if id, err = brands.PrimaryWidgetID(ctx, q, p.WorkspaceID); err != nil {
			if db.IsNotFound(err) {
				return widgetRow{}, errWidgetDisabled
			}
			return widgetRow{}, err
		}
	}
	g, err := scanWidgetRow(q.QueryRow(ctx, widgetRowSelect+` WHERE g.workspace_id = $1 AND g.id = $2`, p.WorkspaceID, id))
	if db.IsNotFound(err) {
		return g, errWidgetDisabled
	}
	return g, err
}

var errWidgetDisabled = httpx.E(http.StatusForbidden, "widget_disabled", "The chat widget is turned off.")

var errAttachmentsOff = httpx.E(http.StatusForbidden, "attachments_off", "This chat does not take files.")

var errVoiceTooLong = httpx.E(http.StatusRequestEntityTooLarge, "voice_too_long", "This voice message is longer than this chat takes.")

// takesUploads reports whether visitors may send the widget anything: files or voice
// messages.
func takesUploads(w hd.WidgetSettings) bool { return w.Attachments || w.VoiceMessages }

// visitorAccept decides whether a visitor's file is taken through a widget: as a file of
// its attachment types (any but a program while it has none), or, with voice messages on,
// as a recording of sound alone no larger than voice_max_seconds allows. Nil when the
// widget takes any file, so nothing is checked again.
func visitorAccept(w hd.WidgetSettings) hd.Accept {
	if w.Attachments && len(w.AttachmentTypes) == 0 {
		return nil
	}
	return func(head []byte, filename string, size int64) error {
		if w.Attachments && hd.FileTypeAllowed(head, filename, w.AttachmentTypes) {
			return nil
		}
		if w.VoiceMessages && hd.IsVoice(head, filename) {
			if size > hd.VoiceMaxBytes(w.VoiceMaxSeconds) {
				return errVoiceTooLong
			}
			return nil
		}
		return hd.ErrFileTypeNotAllowed
	}
}

type configTopic struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type widgetConfig struct {
	Body struct {
		WorkspaceName   string        `json:"workspace_name" doc:"The brand's name"`
		BrandColor      string        `json:"brand_color"`
		Greeting        string        `json:"greeting"`
		HelpCenterURL   string        `json:"help_center_url" doc:"The brand's help center, when it is on"`
		RequireEmail    bool          `json:"require_email"`
		Locale          string        `json:"locale" doc:"The brand's language, else the workspace's (BCP 47); empty when not set"`
		AvatarURL       domain.Avatar `json:"avatar_url" doc:"The widget avatar, else the brand logo, else the workspace logo"`
		LauncherIconURL domain.Avatar `json:"launcher_icon_url"`
		Theme           string        `json:"theme" enum:"auto,light,dark"`
		Position        string        `json:"position" enum:"right,left"`
		OffsetX         int           `json:"offset_x"`
		OffsetY         int           `json:"offset_y"`
		Launcher        string        `json:"launcher" enum:"icon,label"`
		LauncherLabel   string        `json:"launcher_label"`
		Radius          string        `json:"radius" enum:"square,soft,round"`
		Title           string        `json:"title" doc:"The header title; the brand name when not set"`
		Subtitle        string        `json:"subtitle"`
		Suggestions     []string      `json:"suggestions"`
		Sound           bool          `json:"sound"`
		Attachments     bool          `json:"attachments" doc:"Visitors may send files"`
		AttachmentExts  []string      `json:"attachment_extensions" nullable:"true" doc:"Extensions of the file types visitors may send; null when any file but a program or a script; [] while attachments is off"`
		VoiceMessages   bool          `json:"voice_messages" doc:"Visitors may record voice messages"`
		VoiceMaxSeconds int           `json:"voice_max_seconds" doc:"The longest voice message, in seconds"`
		Team            []teamFace    `json:"team" doc:"Up to 3 members of the widget's team (else of the workspace), available first; first names only"`
		AIAnswers       bool          `json:"ai_answers" doc:"The AI agent handles new conversations"`
		Bot             struct {
			ID        *uuid.UUID `json:"id" doc:"null until the workspace has its default bot"`
			Name      string     `json:"name"`
			Role      string     `json:"role"`
			AvatarURL string     `json:"avatar_url" doc:"A PNG of the look, or the uploaded picture"`
			Look      bots.Look  `json:"look" doc:"Drawn with the shared renderer (contracts/bots.md)"`
			Greeting  string     `json:"greeting" doc:"The bot's opening message; empty = the widget's greeting"`
		} `json:"bot" doc:"The bot the AI agent answers as here (contracts/bots.md)"`
		Topics         []configTopic `json:"topics" doc:"Asked before the first message; send the pick as topic_id"`
		Open           bool          `json:"open" doc:"Within the business hours of the widget's team, else the workspace's; true without hours"`
		OfflineMessage string        `json:"offline_message" doc:"Shown while closed; empty = the translated default"`
		Brand          struct {
			Name  string `json:"name"`
			Color string `json:"color" doc:"#rrggbb, the brand's colour, else the workspace brand_color"`
		} `json:"brand"`
	}
}

type teamFace struct {
	Name      string        `json:"name"`
	AvatarURL domain.Avatar `json:"avatar_url"`
}

// teamFaces returns up to 3 members who can reply, available first: first names and
// pictures only. With a team, its leads and members come first; without any, everyone. A
// private team's members are never shown: its widget shows no faces.
func teamFaces(ctx context.Context, q db.Querier, wid uuid.UUID, team *uuid.UUID) ([]teamFace, error) {
	if private, err := hd.TeamPrivate(ctx, q, wid, team); err != nil || private {
		return []teamFace{}, err
	}
	rows, err := q.Query(ctx, `SELECT u.name, u.avatar_url FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND m.role <> 'viewer'
		AND ($2::uuid IS NULL OR EXISTS (SELECT 1 FROM team_members tm WHERE tm.team_id = $2 AND tm.user_id = m.user_id AND tm.role <> 'observer'))
		ORDER BY CASE m.availability WHEN 'online' THEN 0 WHEN 'away' THEN 1 ELSE 2 END, u.avatar_url IS NULL, m.created_at
		LIMIT 3`, wid, team)
	if err != nil {
		return nil, err
	}
	out := []teamFace{}
	_, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (struct{}, error) {
		var name string
		var avatar *string
		if err := r.Scan(&name, &avatar); err != nil {
			return struct{}{}, err
		}
		first, _, _ := strings.Cut(strings.TrimSpace(name), " ")
		face := teamFace{Name: first}
		if avatar != nil {
			face.AvatarURL = domain.Avatar(*avatar)
		}
		out = append(out, face)
		return struct{}{}, nil
	})
	if err == nil && len(out) == 0 && team != nil {
		return teamFaces(ctx, q, wid, nil)
	}
	return out, err
}

// isOpen reports whether the widget's team (else the workspace) is within business hours.
// Without business hours it is always open.
func isOpen(ctx context.Context, q db.Querier, g widgetRow, now time.Time) (bool, error) {
	bh, loc, err := hd.HoursFor(ctx, q, g.WorkspaceID, g.TeamID)
	if err != nil {
		return false, err
	}
	if !bh.Enabled {
		return true, nil
	}
	return hd.OpenAt(bh, loc, now), nil
}

type widgetSessionInput struct {
	Origin    string `header:"Origin"`
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Key          string `json:"key" maxLength:"200"`
		VisitorToken string `json:"visitor_token,omitempty" maxLength:"200"`
		Email        string `json:"email,omitempty" maxLength:"254"`
		Name         string `json:"name,omitempty" maxLength:"200"`
		UserID       string `json:"user_id,omitempty" maxLength:"200"`
		UserHash     string `json:"user_hash,omitempty" maxLength:"128"`
		AvatarURL    string `json:"avatar_url,omitempty" maxLength:"2000" doc:"https picture of the signed-in user; used only with a valid user_hash"`
	}
}

type widgetSession struct {
	Body struct {
		VisitorToken string            `json:"visitor_token"`
		Contact      domain.ContactRef `json:"contact"`
		WSToken      string            `json:"ws_token"`
		GatewayURL   string            `json:"gateway_url"`
	}
}

func (h *handler) registerPublic(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "widget-config", Method: http.MethodGet, Path: "/v1/widget/config",
		Summary: "Get the chat widget configuration", Tags: []string{"Widget"}, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Key    string `query:"key" required:"true"`
			Origin string `header:"Origin"`
		}) (*widgetConfig, error) {
			g, err := loadByKey(ctx, h.d.DB.Pool, in.Key)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			ws := g.Settings
			if err := checkOrigin(ws, in.Origin); err != nil {
				return nil, err
			}
			out := &widgetConfig{}
			b := &out.Body
			b.WorkspaceName = g.BrandName
			b.Brand.Name = g.BrandName
			b.Brand.Color = g.WSSettings.BrandColor
			if hexColor.MatchString(g.BrandColor) {
				b.Brand.Color = g.BrandColor
			}
			b.BrandColor = b.Brand.Color
			if hexColor.MatchString(ws.Color) {
				b.BrandColor = ws.Color
			}
			b.Greeting = ws.Greeting
			b.RequireEmail = ws.RequireEmail
			b.Locale = g.BrandLocale
			if b.Locale == "" {
				b.Locale = g.WSSettings.Locale
			}
			b.AvatarURL = domain.Avatar(g.Images["avatar"])
			if b.AvatarURL == "" {
				b.AvatarURL = domain.Avatar(g.BrandImages["logo"])
			}
			if b.AvatarURL == "" {
				b.AvatarURL = g.Logo
			}
			b.LauncherIconURL = domain.Avatar(g.Images["launcher"])
			b.Theme, b.Position, b.OffsetX, b.OffsetY = ws.Theme, ws.Position, ws.OffsetX, ws.OffsetY
			b.Launcher, b.LauncherLabel, b.Radius = ws.Launcher, ws.LauncherLabel, ws.Radius
			b.Title, b.Subtitle, b.Suggestions, b.Sound = ws.Title, ws.Subtitle, ws.Suggestions, ws.Sound
			b.Attachments = ws.Attachments
			b.AttachmentExts = []string{}
			if ws.Attachments && len(ws.AttachmentTypes) == 0 {
				b.AttachmentExts = nil
			} else if ws.Attachments {
				b.AttachmentExts = hd.FileTypeExtensions(ws.AttachmentTypes)
			}
			b.VoiceMessages, b.VoiceMaxSeconds = ws.VoiceMessages, ws.VoiceMaxSeconds
			if b.Title == "" {
				b.Title = g.BrandName
			}
			b.Team = []teamFace{}
			if ws.ShowTeam {
				if b.Team, err = teamFaces(ctx, h.d.DB.Pool, g.WorkspaceID, g.TeamID); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			ai := g.WSSettings.AI()
			b.AIAnswers = h.d.Cfg.AIEnabled() && ai.Enabled && ai.WidgetAnswers
			bot, err := bots.ForWidget(ctx, h.d.DB.Pool, g.WorkspaceID, &g.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b.Bot.ID, b.Bot.Name, b.Bot.Role, b.Bot.AvatarURL = bot.IDPtr(), bot.Name, bot.Role, bot.AvatarURL
			b.Bot.Look, b.Bot.Greeting = bot.Look, bot.Personality.Greeting
			if g.BrandHelpCenter {
				b.HelpCenterURL = brands.HelpCenterURL(h.d.Cfg, g.BrandSlug, g.BrandHelpHost)
			}
			b.Topics = make([]configTopic, 0, len(ws.Topics))
			for _, t := range ws.Topics {
				b.Topics = append(b.Topics, configTopic{ID: t.ID, Label: t.Label})
			}
			if b.Open, err = isOpen(ctx, h.d.DB.Pool, g, time.Now()); err != nil {
				return nil, httpx.Wrap(err)
			}
			b.OfflineMessage = ws.OfflineMessage
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "widget-session", Method: http.MethodPost, Path: "/v1/widget/session",
		Summary: "Start or resume a chat widget session", Tags: []string{"Widget"}, Security: []map[string][]string{},
		Description: "Returns a visitor token for the widget API and a short-lived realtime token. " +
			"user_hash = hex HMAC-SHA256(widget_secret, user_id) marks the visitor verified.",
		Middlewares: huma.Middlewares{h.withClientIP}},
		h.session)

	huma.Register(api, huma.Operation{OperationID: "widget-end-session", Method: http.MethodDelete, Path: "/v1/widget/session",
		Summary: "End the visitor's chat widget session", Tags: []string{"Widget"}, DefaultStatus: http.StatusNoContent,
		Description: "The visitor token stops working at once; the widget's shutdown calls it when the host page signs its user out."},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := auth.RequireVisitor(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if _, err := h.d.DB.Pool.Exec(ctx, `DELETE FROM widget_sessions WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, p.VisitorID); err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

// errInvalidHash answers a user_hash that does not verify.
var errInvalidHash = httpx.E(http.StatusUnauthorized, "invalid_user_hash", "The user_hash does not match user_id.")

// verifyUser checks user_hash with the identity secret of the widget the session is for.
func (h *handler) verifyUser(w widgetRow, userID, userHash string) error {
	if userID == "" || userHash == "" {
		return httpx.Validation("user_id and user_hash must be sent together")
	}
	if len(w.SecretEnc) == 0 {
		return errInvalidHash
	}
	secret, err := h.d.Box.Open(w.SecretEnc)
	if err != nil {
		return err
	}
	if !crypto.EqualHex(crypto.HMACHex(secret, userID), strings.ToLower(strings.TrimSpace(userHash))) {
		return errInvalidHash
	}
	return nil
}

func normalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return "", httpx.Validation("email is not a valid address")
	}
	return s, nil
}

func (h *handler) session(ctx context.Context, in *widgetSessionInput) (*widgetSession, error) {
	g, err := loadByKey(ctx, h.d.DB.Pool, in.Body.Key)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	wid := g.WorkspaceID
	if !h.d.Limiter.Allow("widget:session:"+wid.String()+":"+clientIP(ctx), sessionLimit, time.Minute) {
		return nil, httpx.RateLimited()
	}
	wset := g.Settings
	if err := checkOrigin(wset, in.Origin); err != nil {
		return nil, err
	}
	b := in.Body
	userID := strings.TrimSpace(b.UserID)
	verified := false
	if userID != "" || b.UserHash != "" {
		if err := h.verifyUser(g, userID, b.UserHash); err != nil {
			return nil, httpx.Wrap(err)
		}
		verified = true
	}
	email, err := normalizeEmail(b.Email)
	if err != nil {
		return nil, err
	}
	name := displayname.Clean(b.Name, displayname.Person)

	out := &widgetSession{}
	err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var contactID uuid.UUID
		token := ""
		if t := strings.TrimSpace(b.VisitorToken); t != "" {
			var sessContact uuid.UUID
			var sessWidget *uuid.UUID
			var sessVerified, blocked bool
			var external *string
			err := tx.QueryRow(ctx, `SELECT s.contact_id, s.widget_id, s.verified, c.blocked, c.external_id FROM widget_sessions s
				JOIN contacts c ON c.id = s.contact_id WHERE s.workspace_id = $1 AND s.token_hash = $2 AND `+auth.VisitorSessionLive,
				wid, crypto.HashToken(t)).Scan(&sessContact, &sessWidget, &sessVerified, &blocked, &external)
			switch {
			case db.IsNotFound(err):
			case err != nil:
				return err
			case blocked:
				return httpx.E(http.StatusForbidden, "blocked", "This conversation is not available.")
			case verified && (external == nil || *external != userID):
				// A different signed-in user on this browser gets a session of their own.
			case sessVerified && !verified:
				// A signed-in visitor's session is not someone's who is not signed in: on a
				// shared computer, the next person starts anonymous and sees nothing of it.
			case sessWidget != nil && *sessWidget != g.ID:
				// The same visitor on another widget of the workspace: a session of this
				// widget. An identity is verified with each widget's own secret, so a
				// signed-in contact carries over only when this request verified it too
				// (the case above); otherwise the visitor starts anonymous here.
				if external == nil || verified {
					contactID = sessContact
				}
			default:
				contactID, token = sessContact, t
				if verified && !sessVerified {
					if _, err := tx.Exec(ctx, `UPDATE widget_sessions SET verified = true WHERE workspace_id = $1 AND token_hash = $2`,
						wid, crypto.HashToken(t)); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE widget_sessions SET last_seen_at = now() WHERE workspace_id = $1 AND token_hash = $2`,
					wid, crypto.HashToken(t)); err != nil {
					return err
				}
			}
		}

		if contactID == uuid.Nil {
			var err error
			if verified {
				contactID, err = h.verifiedContact(ctx, tx, wid, userID, email, name)
			} else {
				contactID, err = h.newVisitorContact(ctx, tx, wid, email, name)
			}
			if err != nil {
				return err
			}
		} else if email != "" || name != "" {
			if err := h.fillContact(ctx, tx, wid, contactID, email, name); err != nil {
				return err
			}
		}

		if verified {
			if err := widgetPicture(ctx, h.d, tx, wid, contactID, strings.TrimSpace(b.AvatarURL)); err != nil {
				return err
			}
		}
		c, err := domain.LoadContact(ctx, tx, wid, contactID)
		if err != nil {
			return err
		}
		if c.Blocked {
			return httpx.E(http.StatusForbidden, "blocked", "This conversation is not available.")
		}
		if wset.RequireEmail && c.Email == nil && email == "" {
			return httpx.E(http.StatusUnprocessableEntity, "email_required", "Enter your email address to start a chat.")
		}
		if token == "" {
			token = "od_vis_" + crypto.RandomToken(32)
			if _, err := tx.Exec(ctx, `INSERT INTO widget_sessions (id, workspace_id, contact_id, token_hash, verified, user_agent, widget_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`, ids.New(), wid, contactID, crypto.HashToken(token), verified, hd.Truncate(in.UserAgent, 400), g.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE widgets SET seen_at = now() WHERE id = $1 AND seen_at IS NULL`, g.ID); err != nil {
				return err
			}
		}
		wsToken, _, err := h.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeWidgetWS, Workspace: wid.String(), Brand: g.BrandID.String()},
			contactID.String(), auth.WSTTL)
		if err != nil {
			return err
		}
		out.Body.VisitorToken = token
		out.Body.Contact = domain.ContactRef{ID: c.ID, Email: c.Email, Name: c.Name, AvatarURL: c.AvatarURL}
		out.Body.WSToken = wsToken
		out.Body.GatewayURL = h.d.Cfg.GatewayURL + "/widget" // the widget socket, open to any origin
		return nil
	})
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	return out, nil
}

func emailTaken(ctx context.Context, tx pgx.Tx, wid uuid.UUID, email string, except uuid.UUID) (bool, error) {
	var taken bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contacts WHERE workspace_id = $1 AND email = $2 AND id <> $3)`,
		wid, email, except).Scan(&taken)
	return taken, err
}

// insertContact creates a contact. The email is stored only when no other contact has it:
// an address a visitor types is unproven, so it never links the visitor to an existing
// contact's conversations. A claimed address that is taken is kept in custom.widget_email
// for agents to see.
func (h *handler) insertContact(ctx context.Context, tx pgx.Tx, wid uuid.UUID, externalID *string, email, name string) (uuid.UUID, error) {
	if name == "" {
		name = displayname.FromEmail(email)
	}
	if name == "" {
		name = "Visitor"
	}
	custom := map[string]any{"source": "widget"}
	var emailCol *string
	if email != "" {
		taken, err := emailTaken(ctx, tx, wid, email, uuid.Nil)
		if err != nil {
			return uuid.Nil, err
		}
		if taken {
			custom["widget_email"] = email
		} else {
			emailCol = &email
		}
	}
	id := ids.New()
	tag, err := tx.Exec(ctx, `INSERT INTO contacts (id, workspace_id, email, name, external_id, custom) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT DO NOTHING`, id, wid, emailCol, name, externalID, custom)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() == 0 {
		// Lost a race on the email or external id; store without the address.
		if externalID != nil {
			var existing uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND external_id = $2`, wid, *externalID).Scan(&existing); err == nil {
				return existing, nil
			}
		}
		custom["widget_email"] = email
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, workspace_id, name, external_id, custom) VALUES ($1,$2,$3,$4,$5)`,
			id, wid, name, externalID, custom); err != nil {
			return uuid.Nil, err
		}
	}
	if err := publishContact(ctx, tx, wid, id, "contact.created"); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (h *handler) newVisitorContact(ctx context.Context, tx pgx.Tx, wid uuid.UUID, email, name string) (uuid.UUID, error) {
	return h.insertContact(ctx, tx, wid, nil, email, name)
}

// verifiedContact finds or creates the contact for a user id the customer's backend signed.
func (h *handler) verifiedContact(ctx context.Context, tx pgx.Tx, wid uuid.UUID, userID, email, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND external_id = $2`, wid, userID).Scan(&id)
	if db.IsNotFound(err) {
		return h.insertContact(ctx, tx, wid, &userID, email, name)
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, h.fillContact(ctx, tx, wid, id, email, name)
}

// fillContact sets a missing name or email on the session's own contact. The email is
// set only when the contact has none and no other contact uses it.
func (h *handler) fillContact(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID, email, name string) error {
	changed := false
	if email != "" {
		taken, err := emailTaken(ctx, tx, wid, email, id)
		if err != nil {
			return err
		}
		if !taken {
			tag, err := tx.Exec(ctx, `UPDATE contacts SET email = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2 AND email IS NULL`, wid, id, email)
			if err != nil {
				return err
			}
			changed = changed || tag.RowsAffected() > 0
		}
	}
	if name != "" {
		tag, err := tx.Exec(ctx, `UPDATE contacts SET name = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2
			AND (name = '' OR name = 'Visitor') AND name <> $3`, wid, id, name)
		if err != nil {
			return err
		}
		changed = changed || tag.RowsAffected() > 0
	}
	if changed {
		return publishContact(ctx, tx, wid, id, "contact.updated")
	}
	return nil
}

func publishContact(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID, typ string) error {
	c, err := domain.LoadContact(ctx, tx, wid, id)
	if err != nil {
		return err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Data: map[string]any{"contact": c}})
	return err
}

// widgetPicture records a verified visitor's picture URL and queues its copy, unless an
// agent uploaded one. Only https URLs without credentials are kept.
func widgetPicture(ctx context.Context, d *deps.Deps, tx pgx.Tx, wid, cid uuid.UUID, raw string) error {
	if raw == "" || len(raw) > 2000 {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil
	}
	var changed bool
	err = tx.QueryRow(ctx, `UPDATE contacts SET avatar_remote_url = $3, avatar_source = 'widget'
		WHERE workspace_id = $1 AND id = $2 AND avatar_source IS DISTINCT FROM 'upload' AND avatar_remote_url IS DISTINCT FROM $3
		RETURNING true`, wid, cid, raw).Scan(&changed)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return images.Enqueue(ctx, tx, d.Jobs, jobs.AvatarFetchArgs{Owner: images.OwnerContact, ID: cid, WorkspaceID: wid})
}
