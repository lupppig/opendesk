package core

import (
	"context"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/notifyprefs"
)

// Notification preferences (contracts/api.md "Notification preferences and emails").

const (
	testEmailsPerHour   = 5
	unsubscribesPerHour = 30
)

// CoreNotificationTypePrefs is one row of the preferences table.
type CoreNotificationTypePrefs struct {
	Type     string               `json:"type"`
	Group    string               `json:"group" enum:"tickets,team,mail,ai_billing,workspace"`
	Email    bool                 `json:"email"`
	Push     bool                 `json:"push"`
	Desktop  bool                 `json:"desktop"`
	Defaults notifyprefs.Channels `json:"defaults"`
	Urgent   bool                 `json:"urgent" doc:"May be emailed sooner than the cooldown"`
}

// CoreNotificationPacing is the contract shape NotificationPacing: how this instance paces
// notification emails (config.NotifyEmail).
type CoreNotificationPacing struct {
	DelayMinutes      int   `json:"delay_minutes"`
	CooldownMinutes   []int `json:"cooldown_minutes"`
	QuietResetMinutes int   `json:"quiet_reset_minutes"`
	UrgentGapMinutes  int   `json:"urgent_gap_minutes"`
	UrgentPerDay      int   `json:"urgent_per_day"`
	PerHour           int   `json:"per_hour"`
	PerDay            int   `json:"per_day"`
	ActiveMinutes     int   `json:"active_minutes"`
}

func pacingOut(p notifyprefs.Pacing) CoreNotificationPacing {
	m := func(d time.Duration) int { return int(d / time.Minute) }
	out := CoreNotificationPacing{DelayMinutes: m(p.Delay), QuietResetMinutes: m(p.QuietReset), UrgentGapMinutes: m(p.UrgentGap),
		UrgentPerDay: p.UrgentPerDay, PerHour: p.PerHour, PerDay: p.PerDay, ActiveMinutes: m(p.ActiveWindow), CooldownMinutes: []int{}}
	for _, c := range p.Cooldowns {
		out.CooldownMinutes = append(out.CooldownMinutes, m(c))
	}
	return out
}

// CoreNotificationPreferences is the contract shape NotificationPreferences.
type CoreNotificationPreferences struct {
	EmailCadence  string                 `json:"email_cadence" enum:"instant,hourly,daily,off"`
	DigestHour    int                    `json:"digest_hour"`
	Timezone      string                 `json:"timezone"`
	PausedUntil   *time.Time             `json:"paused_until"`
	Email         string                 `json:"email"`
	EmailVerified bool                   `json:"email_verified"`
	DelayMinutes  int                    `json:"delay_minutes"`
	Pacing        CoreNotificationPacing `json:"pacing"`
	// UnsubscribedAt: an email's unsubscribe link turned off email from this workspace;
	// POST /v1/me/notification-preferences/resubscribe restores the choices from before.
	UnsubscribedAt *time.Time                  `json:"unsubscribed_at"`
	Types          []CoreNotificationTypePrefs `json:"types"`
	Teams          []CoreNotificationTeam      `json:"teams"`
}

// CoreNotificationTeam is a team the caller leads or works in, and whether they hear about
// its tickets nobody has (new_ticket, and customer_replied on an unassigned ticket).
type CoreNotificationTeam struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Notify bool      `json:"notify"`
}

type coreNotificationTeamPatch struct {
	ID     uuid.UUID `json:"id"`
	Notify bool      `json:"notify"`
}

type coreNotificationTypePatch struct {
	Type    string `json:"type" maxLength:"64"`
	Email   *bool  `json:"email,omitempty"`
	Push    *bool  `json:"push,omitempty"`
	Desktop *bool  `json:"desktop,omitempty"`
}

type coreNotificationPrefsPatch struct {
	EmailCadence *string                     `json:"email_cadence,omitempty" enum:"instant,hourly,daily,off"`
	DigestHour   *int                        `json:"digest_hour,omitempty" minimum:"0" maximum:"23"`
	Timezone     *string                     `json:"timezone,omitempty" maxLength:"64"`
	PausedUntil  *string                     `json:"paused_until,omitempty" maxLength:"40" doc:"RFC 3339 time, or \"\" to resume"`
	Types        []coreNotificationTypePatch `json:"types,omitempty" maxItems:"32"`
	Teams        []coreNotificationTeamPatch `json:"teams,omitempty" maxItems:"200"`
}

// typesFor are the catalogue types a member with role receives.
func typesFor(role auth.Role) []notifyprefs.Type {
	var out []notifyprefs.Type
	for _, t := range notifyprefs.Types {
		if t.Admins && !role.AtLeast(auth.RoleAdmin) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func loadNotificationPrefs(ctx context.Context, q db.Querier, p *auth.Principal) (CoreNotificationPreferences, error) {
	var out CoreNotificationPreferences
	set, err := notifyprefs.LoadSettings(ctx, q, p.UserID)
	if err != nil {
		return out, err
	}
	if err := q.QueryRow(ctx, `SELECT email::text, email_verified_at IS NOT NULL FROM users WHERE id = $1`, p.UserID).
		Scan(&out.Email, &out.EmailVerified); err != nil {
		return out, err
	}
	all, err := notifyprefs.All(ctx, q, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	out.EmailCadence, out.DigestHour, out.Timezone = set.Cadence, set.DigestHour, set.Timezone
	if set.Paused(time.Now()) {
		t := set.PausedUntil.UTC()
		out.PausedUntil = &t
	}
	if err := q.QueryRow(ctx, `SELECT unsubscribed_at FROM notification_unsubscribes WHERE workspace_id = $1 AND user_id = $2`,
		p.WorkspaceID, p.UserID).Scan(&out.UnsubscribedAt); err != nil && !db.IsNotFound(err) {
		return out, err
	}
	pace := notifyprefs.Current()
	out.Pacing = pacingOut(pace)
	out.DelayMinutes = out.Pacing.DelayMinutes
	out.Types = []CoreNotificationTypePrefs{}
	for _, t := range typesFor(p.Role) {
		c := all[t.Type]
		out.Types = append(out.Types, CoreNotificationTypePrefs{Type: t.Type, Group: t.Group, Email: c.Email, Push: c.Push,
			Desktop: c.Desktop, Defaults: t.Defaults, Urgent: notifyprefs.Urgent(t.Type)})
	}
	// Observers never hear about a team's new tickets, so only leads and members choose.
	rows, err := q.Query(ctx, `SELECT t.id, t.name, tm.notify FROM team_members tm JOIN teams t ON t.id = tm.team_id
		WHERE t.workspace_id = $1 AND tm.user_id = $2 AND tm.role <> 'observer' ORDER BY t.name, t.id`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	out.Teams, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreNotificationTeam, error) {
		var t CoreNotificationTeam
		return t, r.Scan(&t.ID, &t.Name, &t.Notify)
	})
	return out, err
}

// rescheduleEmails moves the user's waiting notification emails to the schedule in set.
func rescheduleEmails(ctx context.Context, tx pgx.Tx, uid uuid.UUID, set notifyprefs.Settings) error {
	rows, err := tx.Query(ctx, `SELECT id, created_at FROM notifications
		WHERE user_id = $1 AND email_due_at IS NOT NULL AND emailed_at IS NULL AND read_at IS NULL FOR UPDATE`, uid)
	if err != nil {
		return err
	}
	type waiting struct {
		id      uuid.UUID
		created time.Time
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (waiting, error) {
		var w waiting
		return w, r.Scan(&w.id, &w.created)
	})
	if err != nil {
		return err
	}
	for _, w := range list {
		if _, err := tx.Exec(ctx, `UPDATE notifications SET email_due_at = $2 WHERE id = $1`, w.id, set.EmailDue(w.created)); err != nil {
			return err
		}
	}
	return nil
}

func (s *svc) registerNotificationPrefs(api huma.API, r chi.Router) {
	tags := []string{"Notifications"}

	register(s, api, huma.Operation{OperationID: "get-notification-preferences", Method: http.MethodGet, Path: "/v1/me/notification-preferences",
		Summary: "Your notification preferences in this workspace", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body CoreNotificationPreferences }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := loadNotificationPrefs(ctx, s.d.DB.Pool, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreNotificationPreferences }{out}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-notification-preferences", Method: http.MethodPatch, Path: "/v1/me/notification-preferences",
		Summary: "Change your notification preferences", Tags: tags},
		func(ctx context.Context, in *struct{ Body coreNotificationPrefsPatch }) (*struct{ Body CoreNotificationPreferences }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b := in.Body
			if b.Timezone != nil && !notifyprefs.ValidTimezone(*b.Timezone) {
				return nil, httpx.Validation("timezone must be an IANA time zone such as Europe/Budapest.")
			}
			var pause *time.Time
			resume := false
			if b.PausedUntil != nil {
				if *b.PausedUntil == "" {
					resume = true
				} else {
					t, err := time.Parse(time.RFC3339, *b.PausedUntil)
					if err != nil {
						return nil, httpx.Validation("paused_until must be an RFC 3339 time, or empty to resume.")
					}
					if t.After(time.Now().Add(notifyprefs.MaxPause)) {
						return nil, httpx.Validation("Notifications can be paused for at most 30 days.")
					}
					if t.After(time.Now()) {
						t = t.UTC()
						pause = &t
					} else {
						resume = true
					}
				}
			}
			allowed := typesFor(p.Role)
			for _, t := range b.Types {
				if !slices.ContainsFunc(allowed, func(x notifyprefs.Type) bool { return x.Type == t.Type }) {
					return nil, httpx.Validation("Unknown notification type " + t.Type + ".")
				}
			}
			var out CoreNotificationPreferences
			err = s.tx(ctx, func(tx pgx.Tx) error {
				before, err := notifyprefs.LoadSettings(ctx, tx, p.UserID)
				if err != nil {
					return err
				}
				after := before
				if b.EmailCadence != nil {
					after.Cadence = *b.EmailCadence
				}
				if b.DigestHour != nil {
					after.DigestHour = *b.DigestHour
				}
				if b.Timezone != nil {
					after.Timezone = *b.Timezone
				}
				if pause != nil {
					after.PausedUntil = pause
				}
				if resume {
					after.PausedUntil = nil
				}
				if _, err := tx.Exec(ctx, `INSERT INTO notification_settings (user_id, email_cadence, digest_hour, timezone, paused_until)
					VALUES ($1, $2, $3, $4, $5)
					ON CONFLICT (user_id) DO UPDATE SET email_cadence = EXCLUDED.email_cadence, digest_hour = EXCLUDED.digest_hour,
						timezone = EXCLUDED.timezone, paused_until = EXCLUDED.paused_until, updated_at = now()`,
					p.UserID, after.Cadence, after.DigestHour, after.Timezone, after.PausedUntil); err != nil {
					return err
				}
				if after.Cadence != before.Cadence || after.DigestHour != before.DigestHour || after.Timezone != before.Timezone {
					if err := rescheduleEmails(ctx, tx, p.UserID, after); err != nil {
						return err
					}
				}
				for _, t := range b.Types {
					if t.Email == nil && t.Push == nil && t.Desktop == nil {
						continue
					}
					// Turning an email back on by hand ends the unsubscribe.
					if t.Email != nil && *t.Email {
						if _, err := tx.Exec(ctx, `DELETE FROM notification_unsubscribes WHERE workspace_id = $1 AND user_id = $2`,
							p.WorkspaceID, p.UserID); err != nil {
							return err
						}
					}
					if err := notifyprefs.Set(ctx, tx, p.WorkspaceID, p.UserID, t.Type, t.Email, t.Push, t.Desktop); err != nil {
						return err
					}
				}
				// Only the caller's own memberships of this workspace's teams; another id changes nothing.
				for _, t := range b.Teams {
					if _, err := tx.Exec(ctx, `UPDATE team_members tm SET notify = $4 FROM teams t
						WHERE t.id = tm.team_id AND t.workspace_id = $1 AND tm.team_id = $2 AND tm.user_id = $3`,
						p.WorkspaceID, t.ID, p.UserID, t.Notify); err != nil {
						return err
					}
				}
				out, err = loadNotificationPrefs(ctx, tx, p)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreNotificationPreferences }{out}, nil
		})

	register(s, api, huma.Operation{OperationID: "resubscribe-notifications", Method: http.MethodPost, Path: "/v1/me/notification-preferences/resubscribe",
		Summary: "Turn notification emails back on after unsubscribing", Tags: tags,
		Description: "Restores each type's email choice from before the unsubscribe link was used in this workspace. Without an unsubscribe, nothing changes."},
		func(ctx context.Context, _ *struct{}) (*struct{ Body CoreNotificationPreferences }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var out CoreNotificationPreferences
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if err := resubscribe(ctx, tx, p.WorkspaceID, p.UserID); err != nil {
					return err
				}
				out, err = loadNotificationPrefs(ctx, tx, p)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreNotificationPreferences }{out}, nil
		})

	register(s, api, huma.Operation{OperationID: "test-notification-email", Method: http.MethodPost, Path: "/v1/me/notification-preferences/test",
		Summary: "Send yourself a sample notification email", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("notifytest:"+p.UserID.String(), testEmailsPerHour, time.Hour); err != nil {
				return nil, err
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				return s.sendTestNotificationEmail(ctx, tx, p.WorkspaceID, p.UserID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "unsubscribe-notifications", Method: http.MethodPost, Path: "/v1/notifications/unsubscribe",
		Summary: "Turn off notification emails from a workspace with an email's link", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Token string `json:"token" maxLength:"2048"`
			}
		}) (*struct{ Body coreUnsubscribed }, error) {
			ri := requestInfo(ctx)
			if err := s.limit("unsubscribe:"+ri.IP, unsubscribesPerHour, time.Hour); err != nil {
				return nil, err
			}
			out, err := s.unsubscribe(ctx, in.Body.Token)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body coreUnsubscribed }{out}, nil
		})

	// RFC 8058 one-click: mail providers POST List-Unsubscribe=One-Click as a form to the
	// List-Unsubscribe URL, so this one is plain HTTP rather than JSON.
	r.Post("/v1/notifications/unsubscribe/one-click", func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(req.Body, 4096))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		ip := httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(req)
		if !s.d.Limiter.Allow("unsubscribe:"+ip, unsubscribesPerHour, time.Hour) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "Too many requests.\n")
			return
		}
		if _, err := s.unsubscribe(req.Context(), req.URL.Query().Get("token")); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "This unsubscribe link is not valid.\n")
			return
		}
		_, _ = io.WriteString(w, "Unsubscribed from notification emails.\n")
	})
}

type coreUnsubscribed struct {
	Workspace struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"workspace"`
	Email string `json:"email"`
}

func errInvalidUnsubscribe() *httpx.Error {
	return httpx.E(400, "invalid_token", "This unsubscribe link is not valid any more. Change notification emails in your notification settings.")
}

// unsubscribe turns off every notification email from the token's workspace for its user.
func (s *svc) unsubscribe(ctx context.Context, token string) (coreUnsubscribed, error) {
	var out coreUnsubscribed
	if token == "" || len(token) > 2048 {
		return out, errInvalidUnsubscribe()
	}
	c, err := s.d.Tokens.Verify(token, auth.PurposeNotificationUnsubscribe)
	if err != nil {
		return out, errInvalidUnsubscribe()
	}
	uid, err1 := uuid.Parse(c.Subject)
	wid, err2 := uuid.Parse(c.Workspace)
	if err1 != nil || err2 != nil {
		return out, errInvalidUnsubscribe()
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT w.id, w.name, u.email::text FROM members m JOIN workspaces w ON w.id = m.workspace_id
			JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1 AND m.user_id = $2`, wid, uid).
			Scan(&out.Workspace.ID, &out.Workspace.Name, &out.Email)
		if db.IsNotFound(err) {
			return errInvalidUnsubscribe()
		}
		if err != nil {
			return err
		}
		// The choices from before are kept once: a second unsubscribe must not overwrite
		// them with "all off".
		previous := map[string]*bool{}
		rows, err := tx.Query(ctx, `SELECT type, email FROM notification_preferences WHERE workspace_id = $1 AND user_id = $2`, wid, uid)
		if err != nil {
			return err
		}
		for rows.Next() {
			var typ string
			var email *bool
			if err := rows.Scan(&typ, &email); err != nil {
				rows.Close()
				return err
			}
			previous[typ] = email
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO notification_unsubscribes (workspace_id, user_id, previous) VALUES ($1, $2, $3)
			ON CONFLICT (workspace_id, user_id) DO NOTHING`, wid, uid, previous); err != nil {
			return err
		}
		off := false
		for _, t := range notifyprefs.Types {
			if err := notifyprefs.Set(ctx, tx, wid, uid, t.Type, &off, nil, nil); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// resubscribe restores each type's email choice from before the unsubscribe: a type
// without a choice then follows its default again.
func resubscribe(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID) error {
	var previous map[string]*bool
	err := tx.QueryRow(ctx, `DELETE FROM notification_unsubscribes WHERE workspace_id = $1 AND user_id = $2 RETURNING previous`,
		wid, uid).Scan(&previous)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, t := range notifyprefs.Types {
		if _, err := tx.Exec(ctx, `UPDATE notification_preferences SET email = $4, updated_at = now()
			WHERE workspace_id = $1 AND user_id = $2 AND type = $3`, wid, uid, t.Type, previous[t.Type]); err != nil {
			return err
		}
	}
	return nil
}
