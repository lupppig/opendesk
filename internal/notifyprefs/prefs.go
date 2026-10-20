// Package notifyprefs holds who wants which notification where (contracts/api.md
// "Notification preferences and emails"): the catalogue of notification types with their
// default channels, a member's choices per workspace, and the account's email schedule.
//
// Every notification lands in the app. The email, push (phone) and desktop channels are
// chosen per type and per workspace; the email cadence and the pause are account-wide.
package notifyprefs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
)

// MaxPause bounds paused_until.
const MaxPause = 30 * 24 * time.Hour

// Email cadences.
const (
	CadenceInstant = "instant"
	CadenceHourly  = "hourly"
	CadenceDaily   = "daily"
	CadenceOff     = "off"
)

// Groups of notification types.
const (
	GroupTickets   = "tickets"
	GroupTeam      = "team"
	GroupAIBilling = "ai_billing"
	GroupWorkspace = "workspace"
	// GroupMail is Collab's conversations (contracts/collab.md "Notifications").
	GroupMail = "mail"
)

// Channels is where a notification goes besides the app.
type Channels struct {
	Email   bool `json:"email"`
	Push    bool `json:"push"`
	Desktop bool `json:"desktop"`
}

// Type is one notification type of the catalogue.
type Type struct {
	Type     string
	Group    string
	Defaults Channels
	// Admins: only owners and admins receive it.
	Admins bool
}

var (
	everywhere = Channels{Email: true, Push: true, Desktop: true}
	notPhone   = Channels{Email: true, Push: false, Desktop: true}
	quiet      = Channels{Email: false, Push: false, Desktop: true}
)

// Types is the catalogue, in the order the settings page lists it.
var Types = []Type{
	{Type: "assigned", Group: GroupTickets, Defaults: everywhere},
	{Type: "mentioned", Group: GroupTickets, Defaults: everywhere},
	{Type: "customer_replied", Group: GroupTickets, Defaults: everywhere},
	// Someone outside the team answered a third-party thread on a ticket assigned to you.
	{Type: "thread_replied", Group: GroupTickets, Defaults: everywhere},
	{Type: "sla_breached", Group: GroupTickets, Defaults: everywhere},
	{Type: "ai_handoff", Group: GroupTickets, Defaults: everywhere},
	{Type: "team_joined", Group: GroupTeam, Defaults: quiet},
	// Collab conversations assigned to the member or mentioning them (contracts/collab.md).
	{Type: "collab_assigned", Group: GroupMail, Defaults: everywhere},
	{Type: "collab_mention", Group: GroupMail, Defaults: everywhere},
	{Type: "ai_cap_reached", Group: GroupAIBilling, Defaults: notPhone, Admins: true},
	{Type: "ai_key_failed", Group: GroupAIBilling, Defaults: notPhone, Admins: true},
	{Type: "ai_balance_low", Group: GroupAIBilling, Defaults: notPhone, Admins: true},
	{Type: "ai_balance_out", Group: GroupAIBilling, Defaults: notPhone, Admins: true},
	{Type: "auto_recharge_failed", Group: GroupAIBilling, Defaults: notPhone, Admins: true},
	// To the admin who started a history import (contracts/imports.md).
	{Type: "import_finished", Group: GroupWorkspace, Defaults: everywhere, Admins: true},
}

// Lookup finds a type of the catalogue.
func Lookup(typ string) (Type, bool) {
	for _, t := range Types {
		if t.Type == typ {
			return t, true
		}
	}
	return Type{}, false
}

// Defaults are a type's channels when the member chose nothing. A type outside the
// catalogue is pushed and shown on the desktop but not emailed.
func Defaults(typ string) Channels {
	if t, ok := Lookup(typ); ok {
		return t.Defaults
	}
	return Channels{Push: true, Desktop: true}
}

// Settings is the account's email schedule and pause.
type Settings struct {
	Cadence     string
	DigestHour  int
	Timezone    string
	PausedUntil *time.Time
}

// DefaultSettings applies until the user saves anything.
func DefaultSettings() Settings {
	return Settings{Cadence: CadenceInstant, DigestHour: 9, Timezone: "UTC"}
}

// Paused reports whether notifications are paused at now.
func (s Settings) Paused(now time.Time) bool {
	return s.PausedUntil != nil && s.PausedUntil.After(now)
}

// Location is the settings' timezone, UTC when it no longer loads.
func (s Settings) Location() *time.Location {
	if loc, err := time.LoadLocation(s.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

// EmailDue is when a notification made at created is emailed under this schedule, or nil
// when it is not emailed.
func (s Settings) EmailDue(created time.Time) *time.Time {
	var due time.Time
	switch s.Cadence {
	case CadenceInstant:
		due = created.Add(Current().Delay)
	case CadenceHourly:
		due = created.Truncate(time.Hour).Add(time.Hour)
	case CadenceDaily:
		local := created.In(s.Location())
		due = time.Date(local.Year(), local.Month(), local.Day(), s.DigestHour, 0, 0, 0, local.Location())
		if !due.After(created) {
			due = time.Date(local.Year(), local.Month(), local.Day()+1, s.DigestHour, 0, 0, 0, local.Location())
		}
	default:
		return nil
	}
	due = due.UTC()
	return &due
}

// ValidTimezone reports whether tz is an IANA zone this server knows.
func ValidTimezone(tz string) bool {
	if tz == "" || len(tz) > 64 || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// LoadSettings reads the user's settings, or the defaults.
func LoadSettings(ctx context.Context, q db.Querier, uid uuid.UUID) (Settings, error) {
	s := DefaultSettings()
	var hour int16
	err := q.QueryRow(ctx, `SELECT email_cadence, digest_hour, timezone, paused_until FROM notification_settings WHERE user_id = $1`, uid).
		Scan(&s.Cadence, &hour, &s.Timezone, &s.PausedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return s, fmt.Errorf("notification settings: %w", err)
	}
	s.DigestHour = int(hour)
	return s, nil
}

// override is a member's stored choice for one type; nil follows the default.
type override struct {
	Email, Push, Desktop *bool
}

func (o override) apply(c Channels) Channels {
	if o.Email != nil {
		c.Email = *o.Email
	}
	if o.Push != nil {
		c.Push = *o.Push
	}
	if o.Desktop != nil {
		c.Desktop = *o.Desktop
	}
	return c
}

// Channel is the member's effective channels for one type in one workspace.
func Channel(ctx context.Context, q db.Querier, wid, uid uuid.UUID, typ string) (Channels, error) {
	var o override
	err := q.QueryRow(ctx, `SELECT email, push, desktop FROM notification_preferences
		WHERE workspace_id = $1 AND user_id = $2 AND type = $3`, wid, uid, typ).Scan(&o.Email, &o.Push, &o.Desktop)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(typ), nil
	}
	if err != nil {
		return Channels{}, fmt.Errorf("notification preference: %w", err)
	}
	return o.apply(Defaults(typ)), nil
}

// All is the member's effective channels for every type of the catalogue, keyed by type.
func All(ctx context.Context, q db.Querier, wid, uid uuid.UUID) (map[string]Channels, error) {
	rows, err := q.Query(ctx, `SELECT type, email, push, desktop FROM notification_preferences
		WHERE workspace_id = $1 AND user_id = $2`, wid, uid)
	if err != nil {
		return nil, fmt.Errorf("notification preferences: %w", err)
	}
	stored := map[string]override{}
	for rows.Next() {
		var typ string
		var o override
		if err := rows.Scan(&typ, &o.Email, &o.Push, &o.Desktop); err != nil {
			rows.Close()
			return nil, err
		}
		stored[typ] = o
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]Channels, len(Types))
	for _, t := range Types {
		out[t.Type] = stored[t.Type].apply(t.Defaults)
	}
	return out, nil
}

// Set stores a member's choice for one type; nil channels are left as they are.
func Set(ctx context.Context, q db.Querier, wid, uid uuid.UUID, typ string, email, push, desktop *bool) error {
	_, err := q.Exec(ctx, `INSERT INTO notification_preferences (workspace_id, user_id, type, email, push, desktop)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, user_id, type) DO UPDATE SET
			email = COALESCE(EXCLUDED.email, notification_preferences.email),
			push = COALESCE(EXCLUDED.push, notification_preferences.push),
			desktop = COALESCE(EXCLUDED.desktop, notification_preferences.desktop),
			updated_at = now()`, wid, uid, typ, email, push, desktop)
	return err
}

// EmailDueFor is when a new notification of typ for the member is emailed, or nil.
func EmailDueFor(ctx context.Context, q db.Querier, wid, uid uuid.UUID, typ string, created time.Time) (*time.Time, error) {
	ch, err := Channel(ctx, q, wid, uid, typ)
	if err != nil || !ch.Email {
		return nil, err
	}
	s, err := LoadSettings(ctx, q, uid)
	if err != nil || s.Paused(created) {
		return nil, err
	}
	return s.EmailDue(created), nil
}
