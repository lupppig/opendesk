package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

type AdminUserRow struct {
	ID             uuid.UUID     `json:"id"`
	Email          string        `json:"email"`
	Name           string        `json:"name"`
	AvatarURL      domain.Avatar `json:"avatar_url"`
	EmailVerified  bool          `json:"email_verified"`
	TOTPEnabled    bool          `json:"totp_enabled"`
	HasPassword    bool          `json:"has_password"`
	WorkspaceCount int           `json:"workspace_count"`
	AdminRole      *Role         `json:"admin_role" enum:"viewer,support,super"`
	SuspendedAt    *time.Time    `json:"suspended_at"`
	LastSeenAt     *time.Time    `json:"last_seen_at"`
	CreatedAt      time.Time     `json:"created_at"`
}

type AdminIdentity struct {
	Provider  string    `json:"provider"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type AdminWorkspaceRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

type AdminUserMembership struct {
	Workspace AdminWorkspaceRef `json:"workspace"`
	Role      string            `json:"role" enum:"owner,admin,agent,viewer"`
	CreatedAt time.Time         `json:"created_at"`
}

type AdminSession struct {
	ID          uuid.UUID `json:"id"`
	Client      string    `json:"client" enum:"web,desktop,ios"`
	IP          string    `json:"ip"`
	UserAgent   string    `json:"user_agent"`
	MFAVerified bool      `json:"mfa_verified"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
}

type AdminUser struct {
	AdminUserRow
	SuspendedReason string                `json:"suspended_reason"`
	Identities      []AdminIdentity       `json:"identities"`
	Passkeys        int                   `json:"passkeys"`
	Memberships     []AdminUserMembership `json:"memberships"`
	Sessions        []AdminSession        `json:"sessions"`
}

const userRowCols = `u.id, u.email, u.name, u.avatar_url, u.email_verified_at IS NOT NULL, u.totp_enabled, u.password_hash IS NOT NULL,
	(SELECT count(*) FROM members m WHERE m.user_id = u.id), a.role, u.suspended_at, u.last_seen_at, u.created_at`

func scanUserRow(r pgx.Row, extra ...any) (AdminUserRow, error) {
	var x AdminUserRow
	err := r.Scan(append([]any{&x.ID, &x.Email, &x.Name, &x.AvatarURL, &x.EmailVerified, &x.TOTPEnabled, &x.HasPassword,
		&x.WorkspaceCount, &x.AdminRole, &x.SuspendedAt, &x.LastSeenAt, &x.CreatedAt}, extra...)...)
	return x, err
}

// likePattern is a substring ILIKE pattern with the wildcards in q escaped.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func (s *svc) loadUser(ctx context.Context, q db.Querier, uid uuid.UUID) (AdminUser, error) {
	var u AdminUser
	row, err := scanUserRow(q.QueryRow(ctx, `SELECT `+userRowCols+`, u.suspended_reason FROM users u
		LEFT JOIN instance_admins a ON a.user_id = u.id WHERE u.id = $1`, uid), &u.SuspendedReason)
	if db.IsNotFound(err) {
		return u, httpx.NotFound("User")
	}
	if err != nil {
		return u, err
	}
	u.AdminUserRow = row
	rows, err := q.Query(ctx, `SELECT provider, email, created_at FROM identities WHERE user_id = $1 ORDER BY created_at`, uid)
	if err != nil {
		return u, err
	}
	if u.Identities, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminIdentity, error) {
		var x AdminIdentity
		err := r.Scan(&x.Provider, &x.Email, &x.CreatedAt)
		return x, err
	}); err != nil {
		return u, err
	}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM passkeys WHERE user_id = $1`, uid).Scan(&u.Passkeys); err != nil {
		return u, err
	}
	rows, err = q.Query(ctx, `SELECT w.id, w.name, w.slug, m.role, m.created_at FROM members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 ORDER BY m.created_at LIMIT 200`, uid)
	if err != nil {
		return u, err
	}
	if u.Memberships, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminUserMembership, error) {
		var x AdminUserMembership
		err := r.Scan(&x.Workspace.ID, &x.Workspace.Name, &x.Workspace.Slug, &x.Role, &x.CreatedAt)
		return x, err
	}); err != nil {
		return u, err
	}
	rows, err = q.Query(ctx, `SELECT id, client, ip, user_agent, mfa_verified, created_at, last_used_at FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() ORDER BY last_used_at DESC LIMIT 50`, uid)
	if err != nil {
		return u, err
	}
	u.Sessions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminSession, error) {
		var x AdminSession
		err := r.Scan(&x.ID, &x.Client, &x.IP, &x.UserAgent, &x.MFAVerified, &x.CreatedAt, &x.LastUsedAt)
		return x, err
	})
	return u, err
}

type userPath struct {
	ID string `path:"id"`
}

type reasonBody struct {
	Reason string `json:"reason" minLength:"1" maxLength:"500"`
}

type codeBody struct {
	Code string `json:"code,omitempty" maxLength:"64" doc:"A current authenticator code (or a recovery code) of the acting admin"`
}

func (s *svc) registerUsers(api huma.API) {
	op(s, api, huma.Operation{OperationID: "admin-list-users", Method: http.MethodGet, Path: "/v1/admin/users", Summary: "List users"},
		func(ctx context.Context, in *struct {
			httpx.ListParams
			Q      string `query:"q" maxLength:"200"`
			Filter string `query:"filter" enum:"suspended,admins,unverified,"`
		}) (*struct{ Body httpx.Page[AdminUserRow] }, error) {
			if _, err := require(ctx, PermRead); err != nil {
				return nil, err
			}
			var cur struct {
				T  time.Time `json:"t"`
				ID uuid.UUID `json:"id"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			if cur.T.IsZero() {
				cur.T, cur.ID = time.Now().Add(time.Hour), uuid.Max
			}
			q := strings.TrimSpace(in.Q)
			var byID *uuid.UUID
			if id, err := uuid.Parse(q); err == nil {
				byID = &id
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+userRowCols+` FROM users u LEFT JOIN instance_admins a ON a.user_id = u.id
				WHERE (u.created_at, u.id) < ($1, $2)
				AND ($3 = '' OR ($4::uuid IS NOT NULL AND u.id = $4) OR u.email ILIKE $5 OR u.name ILIKE $5)
				AND ($6 = '' OR ($6 = 'suspended' AND u.suspended_at IS NOT NULL) OR ($6 = 'admins' AND a.user_id IS NOT NULL)
					OR ($6 = 'unverified' AND u.email_verified_at IS NULL))
				ORDER BY u.created_at DESC, u.id DESC LIMIT $7`, cur.T, cur.ID, q, byID, likePattern(q), in.Filter, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminUserRow, error) { return scanUserRow(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page := httpx.NewPage(list, in.Limit, func(x AdminUserRow) any { return map[string]any{"t": x.CreatedAt, "id": x.ID} })
			return &struct{ Body httpx.Page[AdminUserRow] }{page}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-get-user", Method: http.MethodGet, Path: "/v1/admin/users/{id}", Summary: "Get a user"},
		func(ctx context.Context, in *userPath) (*struct{ Body AdminUser }, error) {
			if _, err := require(ctx, PermRead); err != nil {
				return nil, err
			}
			uid, err := parseID(in.ID, "User")
			if err != nil {
				return nil, err
			}
			u, err := s.loadUser(ctx, s.d.DB.Pool, uid)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body AdminUser }{u}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-revoke-user-sessions", Method: http.MethodPost, Path: "/v1/admin/users/{id}/sessions/revoke",
		Summary: "Sign a user out of one session or all of them", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body *struct {
				SessionID string `json:"session_id,omitempty" maxLength:"36"`
			}
		}) (*struct{}, error) {
			a, err := require(ctx, PermUserSessions)
			if err != nil {
				return nil, err
			}
			uid, err := parseID(in.ID, "User")
			if err != nil {
				return nil, err
			}
			sid := uuid.Nil
			if in.Body != nil && in.Body.SessionID != "" {
				if sid, err = uuid.Parse(in.Body.SessionID); err != nil {
					return nil, httpx.NotFound("Session")
				}
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := userExists(ctx, tx, uid); err != nil {
					return err
				}
				// Ending another admin's sessions ends their panel sessions too: only an
				// admin who manages admins may.
				if uid != a.UserID && !a.Role.Can(PermAdminsManage) {
					var isAdmin bool
					if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_admins WHERE user_id = $1)`, uid).Scan(&isAdmin); err != nil {
						return err
					}
					if isAdmin {
						return httpx.E(403, "forbidden", "Only an admin who manages admins can sign another admin out.")
					}
				}
				data := map[string]any{"all": sid == uuid.Nil}
				if sid == uuid.Nil {
					if err := s.ops.EndSessions(ctx, tx, uid); err != nil {
						return err
					}
				} else {
					ended, err := s.ops.EndSession(ctx, tx, uid, sid)
					if err != nil {
						return err
					}
					if !ended {
						return httpx.NotFound("Session")
					}
					data["session_id"] = sid
				}
				if err := s.ops.AuditUser(ctx, tx, a.principal(), uid, "session.revoked", data); err != nil {
					return err
				}
				return s.audit(ctx, tx, a, "user.sessions_revoked", "user", uid.String(), data)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.ops.Disconnect(uid, sid)
			return nil, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-user-verification-email", Method: http.MethodPost, Path: "/v1/admin/users/{id}/verification-email",
		Summary: "Resend a user's email confirmation", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *userPath) (*struct{}, error) {
			a, err := require(ctx, PermUserSessions)
			if err != nil {
				return nil, err
			}
			uid, err := parseID(in.ID, "User")
			if err != nil {
				return nil, err
			}
			if err := s.limit("admin:verify:"+uid.String(), 5, time.Hour); err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var email string
				var verified bool
				err := tx.QueryRow(ctx, `SELECT email, email_verified_at IS NOT NULL FROM users WHERE id = $1`, uid).Scan(&email, &verified)
				if db.IsNotFound(err) {
					return httpx.NotFound("User")
				}
				if err != nil {
					return err
				}
				if verified {
					return httpx.E(409, "already_verified", "This email address is already confirmed.")
				}
				if err := s.ops.SendVerifyEmail(ctx, tx, uid, email); err != nil {
					return err
				}
				return s.audit(ctx, tx, a, "user.verification_sent", "user", uid.String(), nil)
			})
			return nil, httpx.Wrap(err)
		})

	op(s, api, huma.Operation{OperationID: "admin-suspend-user", Method: http.MethodPost, Path: "/v1/admin/users/{id}/suspend", Summary: "Suspend a user"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body reasonBody
		}) (*struct{ Body AdminUser }, error) {
			a, err := require(ctx, PermUserSuspend)
			if err != nil {
				return nil, err
			}
			uid, err := parseID(in.ID, "User")
			if err != nil {
				return nil, err
			}
			reason := strings.TrimSpace(in.Body.Reason)
			if reason == "" {
				return nil, httpx.Validation("Give a reason.")
			}
			var u AdminUser
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if uid == a.UserID {
					return httpx.E(409, "conflict", "You cannot suspend yourself.")
				}
				var isAdmin bool
				err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_admins WHERE user_id = $1)`, uid).Scan(&isAdmin)
				if err != nil {
					return err
				}
				if isAdmin {
					return httpx.E(409, "conflict", "Revoke this person's admin role before suspending them.")
				}
				tag, err := tx.Exec(ctx, `UPDATE users SET suspended_at = now(), suspended_reason = $2, updated_at = now()
					WHERE id = $1 AND suspended_at IS NULL`, uid, reason)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					if err := userExists(ctx, tx, uid); err != nil {
						return err
					}
					return httpx.E(409, "conflict", "This user is already suspended.")
				}
				if err := s.ops.EndSessions(ctx, tx, uid); err != nil {
					return err
				}
				if err := s.ops.AuditUser(ctx, tx, a.principal(), uid, "account.suspended", nil); err != nil {
					return err
				}
				if err := s.audit(ctx, tx, a, "user.suspended", "user", uid.String(), map[string]any{"reason": reason}); err != nil {
					return err
				}
				u, err = s.loadUser(ctx, tx, uid)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.ops.Disconnect(uid, uuid.Nil)
			return &struct{ Body AdminUser }{u}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-unsuspend-user", Method: http.MethodPost, Path: "/v1/admin/users/{id}/unsuspend", Summary: "Restore a suspended user"},
		func(ctx context.Context, in *userPath) (*struct{ Body AdminUser }, error) {
			a, err := require(ctx, PermUserSuspend)
			if err != nil {
				return nil, err
			}
			uid, err := parseID(in.ID, "User")
			if err != nil {
				return nil, err
			}
			var u AdminUser
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `UPDATE users SET suspended_at = NULL, suspended_reason = '', updated_at = now()
					WHERE id = $1 AND suspended_at IS NOT NULL`, uid)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					if err := userExists(ctx, tx, uid); err != nil {
						return err
					}
					return httpx.E(409, "conflict", "This user is not suspended.")
				}
				if err := s.ops.AuditUser(ctx, tx, a.principal(), uid, "account.unsuspended", nil); err != nil {
					return err
				}
				if err := s.audit(ctx, tx, a, "user.unsuspended", "user", uid.String(), nil); err != nil {
					return err
				}
				u, err = s.loadUser(ctx, tx, uid)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body AdminUser }{u}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-disable-user-mfa", Method: http.MethodPost, Path: "/v1/admin/users/{id}/mfa/disable",
		Summary: "Turn off a user's two-factor authentication"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body codeBody
		}) (*struct{ Body AdminUser }, error) {
			a, err := require(ctx, PermUserMFA)
			if err != nil {
				return nil, err
			}
			uid, err := parseID(in.ID, "User")
			if err != nil {
				return nil, err
			}
			var u AdminUser
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := s.stepUp(ctx, tx, a, in.Body.Code); err != nil {
					return err
				}
				if uid == a.UserID {
					return httpx.E(409, "conflict", "Turn off your own two-factor authentication in the app.")
				}
				var email string
				var enabled bool
				err := tx.QueryRow(ctx, `SELECT email, totp_enabled FROM users WHERE id = $1 FOR UPDATE`, uid).Scan(&email, &enabled)
				if db.IsNotFound(err) {
					return httpx.NotFound("User")
				}
				if err != nil {
					return err
				}
				if !enabled {
					return httpx.E(409, "mfa_not_enabled", "This user does not use two-factor authentication.")
				}
				if _, err := tx.Exec(ctx, `UPDATE users SET totp_enabled = false, totp_secret_enc = NULL, updated_at = now() WHERE id = $1`, uid); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM totp_recovery_codes WHERE user_id = $1`, uid); err != nil {
					return err
				}
				if err := s.ops.EndSessions(ctx, tx, uid); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, uid); err != nil {
					return err
				}
				if err := s.ops.SendTwoFactorDisabled(ctx, tx, email); err != nil {
					return err
				}
				if err := s.ops.AuditUser(ctx, tx, a.principal(), uid, "2fa.disabled", nil); err != nil {
					return err
				}
				if err := s.audit(ctx, tx, a, "user.mfa_disabled", "user", uid.String(), nil); err != nil {
					return err
				}
				u, err = s.loadUser(ctx, tx, uid)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.ops.Disconnect(uid, uuid.Nil)
			return &struct{ Body AdminUser }{u}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-delete-user", Method: http.MethodDelete, Path: "/v1/admin/users/{id}",
		Summary: "Delete a user and the workspaces they own alone", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Code         string `json:"code,omitempty" maxLength:"64" doc:"A current authenticator code (or a recovery code) of the acting admin"`
				ConfirmEmail string `json:"confirm_email" maxLength:"320"`
			}
		}) (*struct{}, error) {
			a, err := require(ctx, PermUserDelete)
			if err != nil {
				return nil, err
			}
			uid, err := parseID(in.ID, "User")
			if err != nil {
				return nil, err
			}
			removal, err := s.deleteUser(ctx, a, uid, in.Body.Code, in.Body.ConfirmEmail)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.d.Log.Info("account deleted by an instance admin", "user_id", uid, "admin_user_id", a.UserID, "ip", a.IP)
			s.ops.Finish(removal)
			return nil, nil
		})
}

func (s *svc) deleteUser(ctx context.Context, a *Admin, uid uuid.UUID, code, confirm string) (removal core.Removal, err error) {
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.stepUp(ctx, tx, a, code); err != nil {
			return err
		}
		if uid == a.UserID {
			return httpx.E(409, "conflict", "Delete your own account in the app.")
		}
		var email string
		var isAdmin bool
		err := tx.QueryRow(ctx, `SELECT u.email, EXISTS (SELECT 1 FROM instance_admins WHERE user_id = u.id) FROM users u WHERE u.id = $1 FOR UPDATE`,
			uid).Scan(&email, &isAdmin)
		if db.IsNotFound(err) {
			return httpx.NotFound("User")
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(confirm), email) {
			return httpx.E(422, "confirm_email_mismatch", "Type the user's email address exactly to confirm.")
		}
		if isAdmin {
			return httpx.E(409, "conflict", "Revoke this person's admin role before deleting their account.")
		}
		// The workspaces the user leaves record member.account_deleted, as when the user
		// deletes their own account; this admin's part is in the admin audit log.
		self := &auth.Principal{Kind: auth.KindUser, UserID: uid, IP: a.IP}
		if removal, err = s.ops.DeleteAccount(ctx, tx, self, uid); err != nil {
			if he, ok := err.(*httpx.Error); ok && he.Code == "sole_owner" {
				return httpx.E(409, "sole_owner", "This user owns a workspace with other members. Transfer it or delete the workspace first.")
			}
			return err
		}
		return s.audit(ctx, tx, a, "user.deleted", "user", uid.String(), nil)
	})
	return removal, err
}

func userExists(ctx context.Context, q db.Querier, uid uuid.UUID) error {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, uid).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.NotFound("User")
	}
	return nil
}
