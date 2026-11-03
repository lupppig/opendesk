// Package bots is the AI agent's customer-facing identities (contracts/bots.md): the
// model, its validation, the look renderer and the /v1/bots API. A workspace has one
// default bot; widgets and mailboxes answer as their bot_id (NULL = the default).
package bots

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

const (
	// MaxBots is how many bots a workspace may have.
	MaxBots = 50
	// DefaultName is the default bot's name.
	DefaultName = "Ollie"

	nameMax         = 40
	roleMax         = 60
	customToneMax   = 200
	instructionsMax = 4000
	greetingMax     = 300
	handoffMax      = 300
	signOffMax      = 200
	topicsMax       = 20
)

// Tones, reply lengths and emoji levels a personality may take.
var (
	Tones        = []string{"friendly", "professional", "playful", "concise", "warm", "custom"}
	ReplyLengths = []string{"short", "medium", "detailed"}
	EmojiLevels  = []string{"none", "some", "lots"}
)

// Personality is how a bot talks (contracts/bots.md "BotPersonality").
type Personality struct {
	Tone           string   `json:"tone" enum:"friendly,professional,playful,concise,warm,custom"`
	CustomTone     string   `json:"custom_tone" doc:"Up to 200 characters; used when tone is custom"`
	Instructions   string   `json:"instructions" doc:"Up to 4,000 characters: what this bot should know and do"`
	ReplyLength    string   `json:"reply_length" enum:"short,medium,detailed"`
	Emoji          string   `json:"emoji" enum:"none,some,lots"`
	Greeting       string   `json:"greeting" doc:"Up to 300 characters: its opening message in a new chat; \"\" = the widget's greeting"`
	HandoffMessage string   `json:"handoff_message" doc:"Up to 300 characters: what it says when it passes the conversation to a person"`
	BlockedTopics  []string `json:"blocked_topics" doc:"At most 20 topics of 2..100 characters it declines"`
	SignOff        string   `json:"sign_off" doc:"Up to 200 characters: how its emails end; \"\" = its name and the company's"`
}

// DefaultPersonality is a new bot's personality.
func DefaultPersonality() Personality {
	return Personality{Tone: "friendly", ReplyLength: "short", Emoji: "none", BlockedTopics: []string{}}
}

// readPersonality reads a stored personality leniently: unknown values take the defaults.
func readPersonality(raw []byte) Personality {
	p := DefaultPersonality()
	_ = json.Unmarshal(raw, &p)
	d := DefaultPersonality()
	if !slices.Contains(Tones, p.Tone) {
		p.Tone = d.Tone
	}
	if !slices.Contains(ReplyLengths, p.ReplyLength) {
		p.ReplyLength = d.ReplyLength
	}
	if !slices.Contains(EmojiLevels, p.Emoji) {
		p.Emoji = d.Emoji
	}
	if p.BlockedTopics == nil {
		p.BlockedTopics = []string{}
	}
	return p
}

// UsageWidget and UsageMailbox are what answers as a bot.
type UsageWidget struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	BrandID uuid.UUID `json:"brand_id"`
}

type UsageMailbox struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
}

// UsageAPIKey is an API key that speaks as a bot (contracts/api.md "API keys").
type UsageAPIKey struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type Usage struct {
	Widgets   []UsageWidget  `json:"widgets"`
	Mailboxes []UsageMailbox `json:"mailboxes"`
	APIKeys   []UsageAPIKey  `json:"api_keys" doc:"Live API keys that speak as this bot"`
}

func emptyUsage() Usage {
	return Usage{Widgets: []UsageWidget{}, Mailboxes: []UsageMailbox{}, APIKeys: []UsageAPIKey{}}
}

// Bot is Bot from contracts/bots.md.
type Bot struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Role        string      `json:"role"`
	Language    string      `json:"language" doc:"BCP 47 tag the bot always writes in; \"\" = the customer's language"`
	Look        Look        `json:"look"`
	ImageURL    *string     `json:"image_url" doc:"The uploaded picture, or null"`
	AvatarURL   string      `json:"avatar_url" doc:"A PNG of the look (or the uploaded picture) for clients that cannot render looks"`
	Personality Personality `json:"personality"`
	IsDefault   bool        `json:"is_default"`
	Position    int         `json:"position"`
	Usage       Usage       `json:"usage"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`

	avatar domain.Avatar
}

// Ref is the bot as messages carry it (domain.BotRef).
func (b Bot) Ref() *domain.BotRef {
	if b.ID == uuid.Nil {
		return nil
	}
	raw, _ := json.Marshal(b.Look)
	return &domain.BotRef{ID: b.ID, Name: b.Name, AvatarURL: b.AvatarURL, Look: raw}
}

// IDPtr is the bot's id for messages.bot_id; nil for the built-in stand-in.
func (b Bot) IDPtr() *uuid.UUID {
	if b.ID == uuid.Nil {
		return nil
	}
	id := b.ID
	return &id
}

// Ollie is the stand-in for a workspace that has no stored default bot (it gets one on
// its first bots request): the default bot as it is created.
func Ollie() Bot {
	b := Bot{Name: DefaultName, Look: OllieLook, Personality: DefaultPersonality(), IsDefault: true, Usage: emptyUsage()}
	b.AvatarURL = domain.PublicURL("/v1/public/bots/ollie/avatar.png")
	return b
}

const botColumns = `b.id, b.name, b.role, b.language, b.look, b.avatar, b.personality, b.is_default, b.position, b.created_at, b.updated_at`

func scanBot(row pgx.Row) (Bot, error) {
	var b Bot
	var look, pers []byte
	var avatar string
	if err := row.Scan(&b.ID, &b.Name, &b.Role, &b.Language, &look, &avatar, &pers, &b.IsDefault, &b.Position, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return b, err
	}
	l, err := ParseLook(look)
	if err != nil {
		l = OllieLook // a stored look is always valid; never fail a read on one
	}
	b.Look = l
	b.avatar = domain.Avatar(avatar)
	if u := b.avatar.URL(); u != "" {
		b.ImageURL = &u
	}
	b.AvatarURL = domain.BotAvatarURL(b.ID, b.avatar, look)
	b.Personality = readPersonality(pers)
	b.Usage = emptyUsage()
	return b, nil
}

// List returns the workspace's bots in order, with what answers as each.
func List(ctx context.Context, q db.Querier, wid uuid.UUID) ([]Bot, error) {
	rows, err := q.Query(ctx, `SELECT `+botColumns+` FROM bots b WHERE b.workspace_id = $1 ORDER BY b.position, b.is_default DESC, b.created_at, b.id`, wid)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Bot, error) { return scanBot(r) })
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Bot{}
	}
	return list, fillUsage(ctx, q, wid, list)
}

// Get loads one bot with its usage.
func Get(ctx context.Context, q db.Querier, wid, id uuid.UUID) (Bot, error) {
	b, err := scanBot(q.QueryRow(ctx, `SELECT `+botColumns+` FROM bots b WHERE b.workspace_id = $1 AND b.id = $2`, wid, id))
	if db.IsNotFound(err) {
		return b, httpx.NotFound("Bot")
	}
	if err != nil {
		return b, err
	}
	one := []Bot{b}
	err = fillUsage(ctx, q, wid, one)
	return one[0], err
}

func fillUsage(ctx context.Context, q db.Querier, wid uuid.UUID, list []Bot) error {
	if len(list) == 0 {
		return nil
	}
	idx := map[uuid.UUID]int{}
	def := -1
	for i, b := range list {
		idx[b.ID] = i
		if b.IsDefault {
			def = i
		}
	}
	at := func(bot *uuid.UUID) int {
		if bot != nil {
			if i, ok := idx[*bot]; ok {
				return i
			}
			return -1
		}
		return def
	}
	rows, err := q.Query(ctx, `SELECT g.id, g.name, g.brand_id, g.bot_id FROM widgets g JOIN brands br ON br.id = g.brand_id
		WHERE g.workspace_id = $1 ORDER BY br.is_default DESC, br.position, br.name, g.position, g.created_at, g.id`, wid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var w UsageWidget
		var bot *uuid.UUID
		if err := rows.Scan(&w.ID, &w.Name, &w.BrandID, &bot); err != nil {
			rows.Close()
			return err
		}
		if i := at(bot); i >= 0 {
			list[i].Usage.Widgets = append(list[i].Usage.Widgets, w)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = q.Query(ctx, `SELECT id, name, email, bot_id FROM mailboxes WHERE workspace_id = $1 ORDER BY name, id`, wid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m UsageMailbox
		var bot *uuid.UUID
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &bot); err != nil {
			rows.Close()
			return err
		}
		if i := at(bot); i >= 0 {
			list[i].Usage.Mailboxes = append(list[i].Usage.Mailboxes, m)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// A key speaks as its own bot only: one without a bot speaks as itself, not the default.
	rows, err = q.Query(ctx, `SELECT id, name, bot_id FROM api_keys WHERE workspace_id = $1 AND bot_id IS NOT NULL AND `+liveKey+`
		ORDER BY name, id`, wid)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k UsageAPIKey
		var bot uuid.UUID
		if err := rows.Scan(&k.ID, &k.Name, &bot); err != nil {
			return err
		}
		if i, ok := idx[bot]; ok {
			list[i].Usage.APIKeys = append(list[i].Usage.APIKeys, k)
		}
	}
	return rows.Err()
}

// liveKey is the SQL condition of an API key that still works (no alias): not revoked and
// not expired.
const liveKey = `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`

// load reads a bot without its usage.
func load(ctx context.Context, q db.Querier, wid uuid.UUID, where string, args ...any) (Bot, error) {
	return scanBot(q.QueryRow(ctx, `SELECT `+botColumns+` FROM bots b WHERE b.workspace_id = $1 AND `+where, append([]any{wid}, args...)...))
}

// Default is the workspace's default bot, or the Ollie stand-in when it has none yet.
func Default(ctx context.Context, q db.Querier, wid uuid.UUID) (Bot, error) {
	b, err := load(ctx, q, wid, `b.is_default`)
	if db.IsNotFound(err) {
		return Ollie(), nil
	}
	return b, err
}

// Resolve is the bot with id when it is one of the workspace's, else the default bot.
func Resolve(ctx context.Context, q db.Querier, wid uuid.UUID, id *uuid.UUID) (Bot, error) {
	if id != nil {
		b, err := load(ctx, q, wid, `b.id = $2`, *id)
		if err == nil || !db.IsNotFound(err) {
			return b, err
		}
	}
	return Default(ctx, q, wid)
}

// ForWidget is the bot a widget answers as.
func ForWidget(ctx context.Context, q db.Querier, wid uuid.UUID, widgetID *uuid.UUID) (Bot, error) {
	var bot *uuid.UUID
	if widgetID != nil {
		if err := q.QueryRow(ctx, `SELECT bot_id FROM widgets WHERE workspace_id = $1 AND id = $2`, wid, *widgetID).Scan(&bot); err != nil && !db.IsNotFound(err) {
			return Bot{}, err
		}
	}
	return Resolve(ctx, q, wid, bot)
}

// ForMailbox is the bot a mailbox answers as.
func ForMailbox(ctx context.Context, q db.Querier, wid uuid.UUID, mailboxID *uuid.UUID) (Bot, error) {
	var bot *uuid.UUID
	if mailboxID != nil {
		if err := q.QueryRow(ctx, `SELECT bot_id FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *mailboxID).Scan(&bot); err != nil && !db.IsNotFound(err) {
			return Bot{}, err
		}
	}
	return Resolve(ctx, q, wid, bot)
}

// ForAPIKey is the bot an API key speaks as (contracts/api.md "API keys"), or nil when it
// speaks as itself.
func ForAPIKey(ctx context.Context, q db.Querier, wid, keyID uuid.UUID) (*Bot, error) {
	b, err := scanBot(q.QueryRow(ctx, `SELECT `+botColumns+` FROM api_keys k JOIN bots b ON b.id = k.bot_id AND b.workspace_id = k.workspace_id
		WHERE k.workspace_id = $1 AND k.id = $2`, wid, keyID))
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// Check confirms id is a bot of the workspace (a widget's or mailbox's bot_id).
func Check(ctx context.Context, q db.Querier, wid, id uuid.UUID) error {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bots WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.Validation("bot_id must be a bot of this workspace")
	}
	return nil
}

// ParseRef reads an optional bot_id: "" or null clears it, anything else must be a bot
// of the workspace.
func ParseRef(ctx context.Context, q db.Querier, wid uuid.UUID, raw *string) (*uuid.UUID, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	id, ok := ids.Parse(strings.TrimSpace(*raw))
	if !ok {
		return nil, httpx.Validation("bot_id must be a bot of this workspace")
	}
	if err := Check(ctx, q, wid, id); err != nil {
		return nil, err
	}
	return &id, nil
}

// EnsureDefault creates the workspace's default bot when it has none (a new workspace).
func EnsureDefault(ctx context.Context, tx pgx.Tx, wid uuid.UUID) error {
	look, err := json.Marshal(OllieLook)
	if err != nil {
		return err
	}
	pers, err := json.Marshal(DefaultPersonality())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO bots (id, workspace_id, name, look, personality, is_default, position)
		SELECT $2, $1, $3, $4, $5, true, 0 WHERE NOT EXISTS (SELECT 1 FROM bots WHERE workspace_id = $1 AND is_default)
		ON CONFLICT DO NOTHING`, wid, ids.New(), DefaultName, look, pers)
	return err
}

// ── Validation ──────────────────────────────────────────────────────────────────

var langTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}$`)

// plainText trims s, refuses markup and control characters (newlines allowed when
// multiline) and bounds its length.
func plainText(field, s string, maxLen int, multiline bool) (string, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if multiline {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t")
		}
		s = strings.Join(lines, "\n")
	} else {
		s = strings.Join(strings.Fields(s), " ")
	}
	if utf8.RuneCountInString(s) > maxLen {
		return "", httpx.Validation(fmt.Sprintf("%s must be %d characters or less", field, maxLen))
	}
	if strings.ContainsAny(s, "<>") {
		return "", httpx.Validation(field + " cannot contain < or >")
	}
	for _, r := range s {
		if (unicode.IsControl(r) && !(multiline && r == '\n')) || unicode.In(r, unicode.Cf) && r != '‍' {
			return "", httpx.Validation(field + " contains characters that are not allowed")
		}
	}
	return s, nil
}

// cleanName is a bot's name: a name people choose, 1..40 characters.
func cleanName(s string) (string, error) {
	n, err := displayname.Validate("name", s, displayname.Workspace, false)
	if err != nil {
		var inv *displayname.InvalidError
		if errors.As(err, &inv) {
			return "", httpx.E(http.StatusUnprocessableEntity, displayname.ErrorCode, inv.Message)
		}
		return "", err
	}
	if utf8.RuneCountInString(n) > nameMax {
		return "", httpx.Validation(fmt.Sprintf("name must be %d characters or less", nameMax))
	}
	return n, nil
}

func cleanLanguage(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s != "" && !langTag.MatchString(s) {
		return "", httpx.Validation("language must be a BCP 47 tag like en, de or pt-BR, or empty")
	}
	return s, nil
}

// PersonalityPatch is a partial personality; present keys replace the bot's.
type PersonalityPatch struct {
	Tone           *string   `json:"tone,omitempty" enum:"friendly,professional,playful,concise,warm,custom"`
	CustomTone     *string   `json:"custom_tone,omitempty" maxLength:"1000"`
	Instructions   *string   `json:"instructions,omitempty" maxLength:"8000"`
	ReplyLength    *string   `json:"reply_length,omitempty" enum:"short,medium,detailed"`
	Emoji          *string   `json:"emoji,omitempty" enum:"none,some,lots"`
	Greeting       *string   `json:"greeting,omitempty" maxLength:"1000"`
	HandoffMessage *string   `json:"handoff_message,omitempty" maxLength:"1000"`
	BlockedTopics  *[]string `json:"blocked_topics,omitempty" maxItems:"40"`
	SignOff        *string   `json:"sign_off,omitempty" maxLength:"1000"`
}

// Apply merges the patch into p, validating each key, and returns the keys that changed.
func (pp PersonalityPatch) Apply(p Personality) (Personality, []string, error) {
	var changed []string
	set := func(key string, cur *string, v *string, clean func(string) (string, error)) error {
		if v == nil {
			return nil
		}
		s, err := clean(*v)
		if err != nil {
			return err
		}
		if s != *cur {
			*cur = s
			changed = append(changed, "personality."+key)
		}
		return nil
	}
	enum := func(key string, list []string) func(string) (string, error) {
		return func(s string) (string, error) {
			if !slices.Contains(list, s) {
				return "", httpx.Validation(fmt.Sprintf("personality.%s must be one of %s", key, strings.Join(list, ", ")))
			}
			return s, nil
		}
	}
	text := func(key string, maxLen int, multiline bool) func(string) (string, error) {
		return func(s string) (string, error) { return plainText("personality."+key, s, maxLen, multiline) }
	}
	instructions := func(s string) (string, error) {
		s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
		if utf8.RuneCountInString(s) > instructionsMax {
			return "", httpx.Validation(fmt.Sprintf("personality.instructions must be %d characters or less", instructionsMax))
		}
		for _, r := range s {
			if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.In(r, unicode.Cf) && r != '‍' {
				return "", httpx.Validation("personality.instructions contains characters that are not allowed")
			}
		}
		return s, nil
	}
	for _, err := range []error{
		set("tone", &p.Tone, pp.Tone, enum("tone", Tones)),
		set("custom_tone", &p.CustomTone, pp.CustomTone, text("custom_tone", customToneMax, false)),
		set("instructions", &p.Instructions, pp.Instructions, instructions),
		set("reply_length", &p.ReplyLength, pp.ReplyLength, enum("reply_length", ReplyLengths)),
		set("emoji", &p.Emoji, pp.Emoji, enum("emoji", EmojiLevels)),
		set("greeting", &p.Greeting, pp.Greeting, text("greeting", greetingMax, true)),
		set("handoff_message", &p.HandoffMessage, pp.HandoffMessage, text("handoff_message", handoffMax, true)),
		set("sign_off", &p.SignOff, pp.SignOff, text("sign_off", signOffMax, true)),
	} {
		if err != nil {
			return p, nil, err
		}
	}
	if pp.BlockedTopics != nil {
		topics, err := cleanTopics(*pp.BlockedTopics)
		if err != nil {
			return p, nil, err
		}
		if !slices.Equal(topics, p.BlockedTopics) {
			p.BlockedTopics = topics
			changed = append(changed, "personality.blocked_topics")
		}
	}
	if p.Tone == "custom" && p.CustomTone == "" {
		return p, nil, httpx.Validation("personality.custom_tone is required when the tone is custom")
	}
	return p, changed, nil
}

func cleanTopics(in []string) ([]string, error) {
	out := []string{}
	for _, t := range in {
		s, err := plainText("personality.blocked_topics", t, 100, false)
		if err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(s) < 2 {
			return nil, httpx.Validation("each blocked topic must be 2 to 100 characters")
		}
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, s) }) {
			out = append(out, s)
		}
	}
	if len(out) > topicsMax {
		return nil, httpx.Validation(fmt.Sprintf("at most %d blocked topics", topicsMax))
	}
	return out, nil
}

// Draft is an unsaved edit of a bot the playground answers as (contracts/agent.md
// "Playground"): the same rules as PATCH /v1/bots/{id}, nothing stored.
type Draft struct {
	Name        *string           `json:"name,omitempty" maxLength:"200"`
	Role        *string           `json:"role,omitempty" maxLength:"300"`
	Language    *string           `json:"language,omitempty" maxLength:"35"`
	Personality *PersonalityPatch `json:"personality,omitempty"`
}

// WithDraft returns the bot with the draft applied and validated.
func (b Bot) WithDraft(d *Draft) (Bot, error) {
	if d == nil {
		return b, nil
	}
	var err error
	if d.Name != nil {
		if b.Name, err = cleanName(*d.Name); err != nil {
			return b, err
		}
	}
	if d.Role != nil {
		if b.Role, err = plainText("role", *d.Role, roleMax, false); err != nil {
			return b, err
		}
	}
	if d.Language != nil {
		if b.Language, err = cleanLanguage(*d.Language); err != nil {
			return b, err
		}
	}
	if d.Personality != nil {
		if b.Personality, _, err = d.Personality.Apply(b.Personality); err != nil {
			return b, err
		}
	}
	return b, nil
}
