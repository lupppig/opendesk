package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/billing"
	"github.com/useopendesk/opendesk/internal/core/oauthgrant"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/templates"
)

// profileText cleans one line of a member profile: spaces collapsed, at most max
// characters, no markup or control characters.
func profileText(field string, v *string, max int) (*string, error) {
	if v == nil {
		return nil, nil
	}
	s := strings.Join(strings.Fields(*v), " ")
	if utf8.RuneCountInString(s) > max {
		return nil, httpx.Validation(fmt.Sprintf("%s must be %d characters or less", field, max))
	}
	if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == '<' || r == '>' }) {
		return nil, httpx.Validation(field + " contains characters that are not allowed")
	}
	return &s, nil
}

// MemberUser is a member's user with the source of their picture.
type MemberUser struct {
	domain.UserRef
	AvatarSource *string `json:"avatar_source" enum:"upload,oauth"`
}

type CoreMember struct {
	User           MemberUser `json:"user"`
	Role           string     `json:"role" enum:"owner,admin,agent,viewer"`
	Availability   string     `json:"availability" enum:"online,away,offline"`
	Presence       string     `json:"presence" enum:"online,away,offline" doc:"availability, or away when auto-away applies"`
	MaxOpenTickets *int       `json:"max_open_tickets" doc:"Auto-assignment cap on open and pending tickets; null = no cap"`
	OpenTickets    int        `json:"open_tickets"`
	LastSeenAt     *time.Time `json:"last_seen_at"`
	CreatedAt      time.Time  `json:"created_at"`
	// Profile feeds the agent template variables (contracts/templates.md).
	Profile MemberProfile `json:"profile"`
}

// MemberProfile is what customers can see of a member in signatures, per workspace.
type MemberProfile struct {
	Title         string `json:"title"`
	Phone         string `json:"phone"`
	Pronouns      string `json:"pronouns"`
	SignatureHTML string `json:"signature_html"`
}

const memberSelect = `SELECT u.id, u.name, u.email, u.avatar_url, u.avatar_source, m.role, m.availability, ` + domain.PresenceSQL + `,
	m.max_open_tickets, ` + domain.OpenTicketsSQL + `, u.last_seen_at, m.created_at, m.title, m.phone, m.pronouns, m.signature_html
	FROM members m JOIN users u ON u.id = m.user_id JOIN workspaces w ON w.id = m.workspace_id`

func scanMember(r pgx.Row) (CoreMember, error) {
	var m CoreMember
	err := r.Scan(&m.User.ID, &m.User.Name, &m.User.Email, &m.User.AvatarURL, &m.User.AvatarSource, &m.Role, &m.Availability,
		&m.Presence, &m.MaxOpenTickets, &m.OpenTickets, &m.LastSeenAt, &m.CreatedAt,
		&m.Profile.Title, &m.Profile.Phone, &m.Profile.Pronouns, &m.Profile.SignatureHTML)
	return m, err
}

func loadMember(ctx context.Context, q db.Querier, wid, uid uuid.UUID) (CoreMember, error) {
	return scanMember(q.QueryRow(ctx, memberSelect+` WHERE m.workspace_id = $1 AND m.user_id = $2`, wid, uid))
}

// canManage reports whether actor may change or remove a member holding target.
// Owners manage everyone but themselves; admins manage agents and viewers.
func canManage(actor, target auth.Role) bool {
	switch {
	case target == auth.RoleOwner:
		return false
	case actor == auth.RoleOwner:
		return true
	case actor == auth.RoleAdmin:
		return target == auth.RoleAgent || target == auth.RoleViewer
	}
	return false
}

// canGrant reports whether actor may give someone role (owner moves only by transfer).
func canGrant(actor, role auth.Role) bool {
	switch role {
	case auth.RoleAdmin:
		return actor == auth.RoleOwner
	case auth.RoleAgent, auth.RoleViewer:
		return actor.AtLeast(auth.RoleAdmin)
	}
	return false
}

func (s *svc) registerMembers(api huma.API) {
	tags := []string{"Members"}

	register(s, api, huma.Operation{OperationID: "list-members", Method: http.MethodGet, Path: "/v1/members",
		Summary: "List workspace members", Tags: tags},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[CoreMember] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
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
			rows, err := s.d.DB.Pool.Query(ctx, memberSelect+` WHERE m.workspace_id = $1 AND (m.created_at, m.user_id) > ($2, $3)
				ORDER BY m.created_at, m.user_id LIMIT $4`, p.WorkspaceID, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreMember, error) { return scanMember(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[CoreMember] }{httpx.NewPage(list, in.Limit, func(m CoreMember) any {
				return map[string]any{"t": m.CreatedAt, "id": m.User.ID}
			})}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-member", Method: http.MethodPatch, Path: "/v1/members/{user_id}",
		Summary: "Change a member's role", Tags: tags},
		func(ctx context.Context, in *struct {
			UserID string `path:"user_id"`
			Body   struct {
				Role string `json:"role" enum:"admin,agent,viewer"`
			}
		}) (*struct{ Body CoreMember }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			uid, err := parseID(in.UserID, "Member")
			if err != nil {
				return nil, err
			}
			if uid == p.UserID {
				return nil, httpx.E(403, "forbidden", "You cannot change your own role.")
			}
			next := auth.Role(in.Body.Role)
			var m CoreMember
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var cur string
				if err := tx.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`, p.WorkspaceID, uid).Scan(&cur); err != nil {
					if db.IsNotFound(err) {
						return httpx.NotFound("Member")
					}
					return err
				}
				if !canManage(p.Role, auth.Role(cur)) || !canGrant(p.Role, next) {
					return httpx.Forbidden()
				}
				if cur == string(next) {
					m, err = loadMember(ctx, tx, p.WorkspaceID, uid)
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE members SET role = $3 WHERE workspace_id = $1 AND user_id = $2`, p.WorkspaceID, uid, next); err != nil {
					return err
				}
				if next == auth.RoleViewer {
					if err := observeTeams(ctx, tx, p, uid); err != nil {
						return err
					}
				}
				if m, err = loadMember(ctx, tx, p.WorkspaceID, uid); err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "member.updated",
					Topics: []string{events.UserTopic(uid)}, Data: map[string]any{"member": m}}); err != nil {
					return err
				}
				if err := billing.EnqueueSeatSync(ctx, s.d, tx, p.WorkspaceID); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "member.role_changed", "user", &uid, map[string]any{"from": cur, "to": next})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreMember }{m}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-member-profile", Method: http.MethodPatch, Path: "/v1/members/{user_id}/profile",
		Summary: "Change a member's profile", Tags: tags,
		Description: "user_id may be me. Title, phone, pronouns and personal signature feed the agent template variables; members edit their own; the owner anyone's, admins those of agents and viewers."},
		func(ctx context.Context, in *struct {
			UserID string `path:"user_id"`
			Body   struct {
				Title         *string `json:"title,omitempty" maxLength:"80"`
				Phone         *string `json:"phone,omitempty" maxLength:"40"`
				Pronouns      *string `json:"pronouns,omitempty" maxLength:"40"`
				SignatureHTML *string `json:"signature_html,omitempty" maxLength:"65536"`
			}
		}) (*struct{ Body CoreMember }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			uid := p.UserID
			if in.UserID != "me" {
				if uid, err = parseID(in.UserID, "Member"); err != nil {
					return nil, err
				}
			}
			if uid != p.UserID && !p.Role.AtLeast(auth.RoleAdmin) {
				return nil, httpx.Forbidden()
			}
			b := in.Body
			title, err := profileText("title", b.Title, 80)
			if err != nil {
				return nil, err
			}
			phone, err := profileText("phone", b.Phone, 40)
			if err != nil {
				return nil, err
			}
			pronouns, err := profileText("pronouns", b.Pronouns, 40)
			if err != nil {
				return nil, err
			}
			var signature *string
			if b.SignatureHTML != nil {
				sig := hd.SanitizeSignature(*b.SignatureHTML)
				if err := templates.CheckSaved("signature_html", sig, templates.KindPersonalSignature, nil); err != nil {
					return nil, err
				}
				signature = &sig
			}
			var m CoreMember
			err = s.tx(ctx, func(tx pgx.Tx) error {
				// Someone else's profile only as far as their role allows (canManage): an
				// admin edits agents and viewers, never the owner or another admin.
				if uid != p.UserID {
					var role string
					err := tx.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`, p.WorkspaceID, uid).Scan(&role)
					if db.IsNotFound(err) {
						return httpx.NotFound("Member")
					}
					if err != nil {
						return err
					}
					if !canManage(p.Role, auth.Role(role)) {
						return httpx.Forbidden()
					}
				}
				tag, err := tx.Exec(ctx, `UPDATE members SET title = COALESCE($3, title), phone = COALESCE($4, phone),
					pronouns = COALESCE($5, pronouns), signature_html = COALESCE($6, signature_html)
					WHERE workspace_id = $1 AND user_id = $2`, p.WorkspaceID, uid, title, phone, pronouns, signature)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return httpx.NotFound("Member")
				}
				if err := publishMember(ctx, tx, p.WorkspaceID, uid); err != nil {
					return err
				}
				m, err = loadMember(ctx, tx, p.WorkspaceID, uid)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreMember }{m}, nil
		})

	register(s, api, huma.Operation{OperationID: "remove-member", Method: http.MethodDelete, Path: "/v1/members/{user_id}",
		Summary: "Remove a member (or leave the workspace)", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			UserID string `path:"user_id"`
		}) (*struct{}, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			uid, err := parseID(in.UserID, "Member")
			if err != nil {
				return nil, err
			}
			var lapsedKeys []uuid.UUID
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var cur string
				if err := tx.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`, p.WorkspaceID, uid).Scan(&cur); err != nil {
					if db.IsNotFound(err) {
						return httpx.NotFound("Member")
					}
					return err
				}
				self := uid == p.UserID
				switch {
				case self && cur == string(auth.RoleOwner):
					return httpx.E(409, "owner_cannot_leave", "Transfer ownership before leaving the workspace.")
				case !self && !canManage(p.Role, auth.Role(cur)):
					return httpx.Forbidden()
				}
				var err error
				lapsedKeys, err = s.removeMember(ctx, tx, p, p.WorkspaceID, uid, cur, map[bool]string{true: "member.left", false: "member.removed"}[self])
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.disconnect(uid, uuid.Nil)
			s.disconnectAll(lapsedKeys)
			return nil, nil
		})
}

// removeMember deletes a membership and the user's team seats in that workspace.
// Their tickets in private teams are unassigned: those teams' tickets are assigned only to
// their members. The private teams they granted to API keys and webhooks leave those keys
// and webhooks; it returns the keys changed, whose sockets reconnect after the commit.
func (s *svc) removeMember(ctx context.Context, tx pgx.Tx, p *auth.Principal, wid, uid uuid.UUID, role, action string) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `DELETE FROM team_members tm USING teams t WHERE tm.team_id = t.id AND t.workspace_id = $1 AND tm.user_id = $2
		RETURNING CASE WHEN t.private THEN t.id END`, wid, uid)
	if err != nil {
		return nil, err
	}
	seats, err := pgx.CollectRows(rows, pgx.RowTo[*uuid.UUID])
	if err != nil {
		return nil, err
	}
	for _, team := range seats {
		if team != nil {
			if err := s.unassignOffTeam(ctx, tx, p, wid, *team); err != nil {
				return nil, err
			}
		}
	}
	keys, err := dropLapsedGrants(ctx, tx, p, wid)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM views WHERE workspace_id = $1 AND user_id = $2`, wid, uid); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM notifications WHERE workspace_id = $1 AND user_id = $2`, wid, uid); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM members WHERE workspace_id = $1 AND user_id = $2`, wid, uid); err != nil {
		return nil, err
	}
	// AI clients the member connected over OAuth lose access with the membership.
	if _, err := oauthgrant.Revoke(ctx, tx, p, uid, wid, uuid.Nil, oauthgrant.ReasonMemberRemoved); err != nil {
		return nil, err
	}
	if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "member.removed",
		Topics: []string{events.UserTopic(uid)}, Data: map[string]any{"user_id": uid}}); err != nil {
		return nil, err
	}
	if err := billing.EnqueueSeatSync(ctx, s.d, tx, wid); err != nil {
		return nil, err
	}
	return keys, audit.Log(ctx, tx, p, wid, action, "user", &uid, map[string]any{"role": role})
}

// observeTeams makes a viewer an observer of every team they lead or are a member of: a
// viewer reads only. The teams they see are unchanged, so their sockets stay connected.
func observeTeams(ctx context.Context, tx pgx.Tx, p *auth.Principal, uid uuid.UUID) error {
	rows, err := tx.Query(ctx, `UPDATE team_members tm SET role = 'observer' FROM teams t
		WHERE t.id = tm.team_id AND t.workspace_id = $1 AND tm.user_id = $2 AND tm.role <> 'observer'
		RETURNING tm.team_id`, p.WorkspaceID, uid)
	if err != nil {
		return err
	}
	teams, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range teams {
		t, err := loadTeam(ctx, tx, p.WorkspaceID, id)
		if err != nil {
			return err
		}
		if err := publishTeam(ctx, tx, p.WorkspaceID, "updated", t); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, p.WorkspaceID, "team.updated", "team", &id, map[string]any{
			"changes": []string{"members"}, "members": memberDiff{Changed: []uuid.UUID{uid}}}); err != nil {
			return err
		}
	}
	return nil
}
