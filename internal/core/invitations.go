package core

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/billing"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

type CoreInvitation struct {
	ID        uuid.UUID       `json:"id"`
	Email     string          `json:"email"`
	Role      string          `json:"role" enum:"admin,agent,viewer"`
	ExpiresAt time.Time       `json:"expires_at"`
	CreatedAt time.Time       `json:"created_at"`
	InvitedBy *domain.UserRef `json:"invited_by"`
}

const invitationSelect = `SELECT i.id, i.email, i.role, i.expires_at, i.created_at, u.id, u.name, u.email, u.avatar_url
	FROM invitations i LEFT JOIN users u ON u.id = i.invited_by`

func scanInvitation(r pgx.Row) (CoreInvitation, error) {
	var i CoreInvitation
	var uid *uuid.UUID
	var name, email, avatar *string
	if err := r.Scan(&i.ID, &i.Email, &i.Role, &i.ExpiresAt, &i.CreatedAt, &uid, &name, &email, &avatar); err != nil {
		return i, err
	}
	if uid != nil {
		i.InvitedBy = &domain.UserRef{ID: *uid, Name: *name, Email: *email, AvatarURL: domain.Avatar(*avatar)}
	}
	return i, nil
}

func (s *svc) registerInvitations(api huma.API) {
	tags := []string{"Members"}

	register(s, api, huma.Operation{OperationID: "list-invitations", Method: http.MethodGet, Path: "/v1/invitations",
		Summary: "List pending invitations", Tags: tags},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[CoreInvitation] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
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
			rows, err := s.d.DB.Pool.Query(ctx, invitationSelect+` WHERE i.workspace_id = $1 AND i.accepted_at IS NULL
				AND (i.created_at, i.id) < ($2, $3) ORDER BY i.created_at DESC, i.id DESC LIMIT $4`, p.WorkspaceID, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreInvitation, error) { return scanInvitation(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[CoreInvitation] }{httpx.NewPage(list, in.Limit, func(i CoreInvitation) any {
				return map[string]any{"t": i.CreatedAt, "id": i.ID}
			})}, nil
		})

	register(s, api, huma.Operation{OperationID: "create-invitation", Method: http.MethodPost, Path: "/v1/invitations",
		Summary: "Invite someone by email", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				Email string `json:"email" maxLength:"320"`
				Role  string `json:"role" enum:"admin,agent,viewer"`
			}
		}) (*struct{ Body CoreInvitation }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			role := auth.Role(in.Body.Role)
			actorRole := p.Role
			if p.Kind == auth.KindAPIKey {
				actorRole = auth.RoleAdmin
			}
			if !canGrant(actorRole, role) {
				return nil, httpx.E(403, "forbidden", "Only the owner can invite admins.")
			}
			email := normEmail(in.Body.Email)
			if email == "" {
				return nil, httpx.E(422, "invalid_email", "Enter a valid email address.")
			}
			if err := s.limit("invite:"+p.WorkspaceID.String(), 50, time.Hour); err != nil {
				return nil, err
			}
			var inv CoreInvitation
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var member bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members m JOIN users u ON u.id = m.user_id
					WHERE m.workspace_id = $1 AND u.email = $2)`, p.WorkspaceID, email).Scan(&member); err != nil {
					return err
				}
				if member {
					return httpx.E(409, "already_member", "This person is already a member.")
				}
				if _, err := tx.Exec(ctx, `DELETE FROM invitations WHERE workspace_id = $1 AND email = $2 AND accepted_at IS NULL`, p.WorkspaceID, email); err != nil {
					return err
				}
				if err := s.d.Plan.CheckMember(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				token := crypto.RandomToken(emailLinkSize)
				sealed, err := s.d.Box.Seal([]byte(token))
				if err != nil {
					return err
				}
				id := ids.New()
				if _, err := tx.Exec(ctx, `INSERT INTO invitations (id, workspace_id, email, role, token_hash, token_enc, invited_by, expires_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, p.WorkspaceID, email, role, crypto.HashToken(token), sealed, p.ActorUserID(),
					time.Now().Add(inviteTTL)); err != nil {
					return err
				}
				var wsName, inviter string
				if err := tx.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1`, p.WorkspaceID).Scan(&wsName); err != nil {
					return err
				}
				if p.Kind == auth.KindUser {
					if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&inviter); err != nil {
						return err
					}
				}
				if err := s.sendInviteEmail(ctx, tx, email, token, wsName, inviter, string(role)); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "invitation.created", "invitation", &id,
					map[string]any{"role": role}); err != nil {
					return err
				}
				inv, err = scanInvitation(tx.QueryRow(ctx, invitationSelect+` WHERE i.id = $1`, id))
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			distinct := p.WorkspaceID.String()
			if p.Kind == auth.KindUser {
				distinct = p.UserID.String()
			}
			s.d.Analytics.Capture(distinct, p.WorkspaceID.String(), "member_invited", map[string]any{"role": role})
			return &struct{ Body CoreInvitation }{inv}, nil
		})

	register(s, api, huma.Operation{OperationID: "revoke-invitation", Method: http.MethodDelete, Path: "/v1/invitations/{id}",
		Summary: "Revoke an invitation", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Invitation")
			if err != nil {
				return nil, err
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM invitations WHERE id = $1 AND workspace_id = $2 AND accepted_at IS NULL`, id, p.WorkspaceID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return httpx.NotFound("Invitation")
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "invitation.revoked", "invitation", &id, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "copy-invitation-link", Method: http.MethodPost, Path: "/v1/invitations/{id}/link",
		Summary: "Get an invitation's link, to send it another way", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct {
			Body struct {
				URL       string    `json:"url"`
				ExpiresAt time.Time `json:"expires_at"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Invitation")
			if err != nil {
				return nil, err
			}
			if err := s.limit("invite:link:"+p.WorkspaceID.String(), 60, time.Hour); err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					URL       string    `json:"url"`
					ExpiresAt time.Time `json:"expires_at"`
				}
			}{}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				inv, err := s.openInvitation(ctx, tx, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE invitations SET link_copied_at = now() WHERE id = $1`, id); err != nil {
					return err
				}
				out.Body.URL, out.Body.ExpiresAt = s.appLink("/invite", "token", inv.token), inv.expiresAt
				return audit.Log(ctx, tx, p, p.WorkspaceID, "invitation.link_copied", "invitation", &id, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "resend-invitation", Method: http.MethodPost, Path: "/v1/invitations/{id}/resend",
		Summary: "Send an invitation email again", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body CoreInvitation }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Invitation")
			if err != nil {
				return nil, err
			}
			if err := s.limit("invite:"+p.WorkspaceID.String(), 50, time.Hour); err != nil {
				return nil, err
			}
			if err := s.limit("invite:resend:"+id.String(), 3, time.Hour); err != nil {
				return nil, err
			}
			var out CoreInvitation
			err = s.tx(ctx, func(tx pgx.Tx) error {
				inv, err := s.openInvitation(ctx, tx, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				var wsName, inviter string
				if err := tx.QueryRow(ctx, `SELECT name FROM workspaces WHERE id = $1`, p.WorkspaceID).Scan(&wsName); err != nil {
					return err
				}
				if p.Kind == auth.KindUser {
					if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&inviter); err != nil {
						return err
					}
				}
				if err := s.sendInviteEmail(ctx, tx, inv.email, inv.token, wsName, inviter, inv.role); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "invitation.resent", "invitation", &id, nil); err != nil {
					return err
				}
				out, err = scanInvitation(tx.QueryRow(ctx, invitationSelect+` WHERE i.id = $1`, id))
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreInvitation }{out}, nil
		})

	// The token rides in the body, never the URL, where proxies and browsers keep it.
	register(s, api, huma.Operation{OperationID: "lookup-invitation", Method: http.MethodPost, Path: "/v1/invitations/lookup",
		Summary: "Describe an invitation from its token", Tags: tags, Security: []map[string][]string{}},
		func(ctx context.Context, in *struct {
			Body struct {
				Token string `json:"token" maxLength:"200"`
			}
		}) (*struct {
			Body struct {
				WorkspaceName string `json:"workspace_name"`
				Email         string `json:"email"`
				Role          string `json:"role"`
			}
		}, error) {
			if err := s.limit("invite:lookup:"+requestInfo(ctx).IP, 30, 15*time.Minute); err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					WorkspaceName string `json:"workspace_name"`
					Email         string `json:"email"`
					Role          string `json:"role"`
				}
			}{}
			err := s.d.DB.Pool.QueryRow(ctx, `SELECT w.name, i.email, i.role FROM invitations i JOIN workspaces w ON w.id = i.workspace_id
				WHERE i.token_hash = $1 AND i.accepted_at IS NULL AND i.expires_at > now()`, crypto.HashToken(in.Body.Token)).
				Scan(&out.Body.WorkspaceName, &out.Body.Email, &out.Body.Role)
			if db.IsNotFound(err) {
				return nil, httpx.E(404, "invalid_invitation", "This invitation is invalid or has expired.")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return out, nil
		})

	register(s, api, huma.Operation{OperationID: "accept-invitation", Method: http.MethodPost, Path: "/v1/invitations/accept",
		Summary: "Accept an invitation", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				Token string `json:"token" maxLength:"200"`
			}
		}) (*struct{ Body CoreMembership }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("invite:accept:"+p.UserID.String(), 20, 15*time.Minute); err != nil {
				return nil, err
			}
			var m CoreMembership
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var id, wid uuid.UUID
				var email, role string
				var copied bool
				err := tx.QueryRow(ctx, `SELECT id, workspace_id, email, role, link_copied_at IS NOT NULL FROM invitations
					WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > now() FOR UPDATE`, crypto.HashToken(in.Body.Token)).
					Scan(&id, &wid, &email, &role, &copied)
				if db.IsNotFound(err) {
					return httpx.E(404, "invalid_invitation", "This invitation is invalid or has expired.")
				}
				if err != nil {
					return err
				}
				var userEmail string
				if err := tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&userEmail); err != nil {
					return err
				}
				if userEmail != email {
					return httpx.E(403, "invitation_email_mismatch", "This invitation was sent to a different email address. Sign in with that address to accept it.")
				}
				if _, err := tx.Exec(ctx, `UPDATE invitations SET accepted_at = now() WHERE id = $1`, id); err != nil {
					return err
				}
				// Checked once this invitation no longer counts as pending.
				if err := s.d.Plan.CheckMember(ctx, tx, wid); err != nil {
					return err
				}
				tag, err := tx.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, wid, p.UserID, role)
				if err != nil {
					return err
				}
				// A token that only ever went by the invitation email proves the address; a
				// link an admin copied and sent another way does not (as at sign-up).
				if !copied {
					if _, err := tx.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1`, p.UserID); err != nil {
						return err
					}
				}
				if tag.RowsAffected() == 1 {
					mem, err := loadMember(ctx, tx, wid, p.UserID)
					if err != nil {
						return err
					}
					if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "member.added",
						Topics: []string{events.UserTopic(p.UserID)}, Data: map[string]any{"member": mem}}); err != nil {
						return err
					}
					if err := audit.Log(ctx, tx, p, wid, "member.joined", "user", &p.UserID, map[string]any{"role": role, "invitation_id": id}); err != nil {
						return err
					}
					if err := billing.EnqueueSeatSync(ctx, s.d, tx, wid); err != nil {
						return err
					}
				}
				w, err := loadWorkspace(ctx, tx, wid)
				if err != nil {
					return err
				}
				var cur string
				if err := tx.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2`, wid, p.UserID).Scan(&cur); err != nil {
					return err
				}
				m = CoreMembership{Workspace: w, Role: cur}
				return nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreMembership }{m}, nil
		})
}

// openedInvitation is a pending invitation with its token readable again.
type openedInvitation struct {
	email, role, token string
	expiresAt          time.Time
}

// openInvitation locks a pending invitation of the workspace, gives it seven more days and
// returns its token. An invitation from before tokens were kept sealed gets a new token, so
// the link it was first sent with stops working.
func (s *svc) openInvitation(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID) (openedInvitation, error) {
	var inv openedInvitation
	var sealed []byte
	err := tx.QueryRow(ctx, `SELECT email, role, token_enc FROM invitations
		WHERE id = $1 AND workspace_id = $2 AND accepted_at IS NULL FOR UPDATE`, id, wid).Scan(&inv.email, &inv.role, &sealed)
	if db.IsNotFound(err) {
		return inv, httpx.NotFound("Invitation")
	}
	if err != nil {
		return inv, err
	}
	if sealed != nil {
		if plain, err := s.d.Box.Open(sealed); err == nil {
			inv.token = string(plain)
		}
	}
	if inv.token == "" {
		inv.token = crypto.RandomToken(emailLinkSize)
		if sealed, err = s.d.Box.Seal([]byte(inv.token)); err != nil {
			return inv, err
		}
	}
	err = tx.QueryRow(ctx, `UPDATE invitations SET token_hash = $2, token_enc = $3, expires_at = $4 WHERE id = $1 RETURNING expires_at`,
		id, crypto.HashToken(inv.token), sealed, time.Now().Add(inviteTTL)).Scan(&inv.expiresAt)
	return inv, err
}
