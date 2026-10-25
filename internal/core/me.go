package core

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core/oauthgrant"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/images"
	"github.com/useopendesk/opendesk/internal/jobs"
)

type coreMeResponse struct {
	User        CoreUser         `json:"user"`
	Memberships []CoreMembership `json:"memberships"`
}

func (s *svc) registerMe(api huma.API) {
	tags := []string{"Account"}

	register(s, api, huma.Operation{OperationID: "get-me", Method: http.MethodGet, Path: "/v1/me",
		Summary: "Get your account and workspaces", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body coreMeResponse }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			u, err := loadUser(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			ms, err := loadMemberships(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body coreMeResponse }{coreMeResponse{User: u, Memberships: ms}}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-me", Method: http.MethodPatch, Path: "/v1/me",
		Summary: "Update your profile", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				Name       *string `json:"name,omitempty" maxLength:"200"`
				AvatarURL  *string `json:"avatar_url,omitempty" maxLength:"2000" doc:"An https picture URL to copy (fetched and processed server-side), or empty to remove the picture"`
				TimeFormat *string `json:"time_format,omitempty" enum:"auto,12h,24h" doc:"The clock times are shown in, in the app and in notification emails"`
			}
		}) (*struct{ Body CoreUser }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var name *string
			if in.Body.Name != nil {
				n, err := displayname.Validate("Name", *in.Body.Name, displayname.Person, false)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				name = &n
			}
			if in.Body.AvatarURL != nil && *in.Body.AvatarURL != "" && !validPictureURL(*in.Body.AvatarURL) {
				return nil, httpx.Validation("avatar_url must be an https URL.")
			}
			var oldPrefix string
			var u CoreUser
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE users SET name = COALESCE($2, name), time_format = COALESCE($3, time_format), updated_at = now() WHERE id = $1`,
					p.UserID, name, in.Body.TimeFormat); err != nil {
					return err
				}
				if a := in.Body.AvatarURL; a != nil {
					if *a == "" {
						o := images.Owner{Type: images.OwnerUser, ID: p.UserID}
						if oldPrefix, err = images.ClearTx(ctx, tx, o, false, images.SetOptions{}); err != nil {
							return err
						}
						if _, err := tx.Exec(ctx, `UPDATE users SET avatar_remote_url = '' WHERE id = $1`, p.UserID); err != nil {
							return err
						}
					} else {
						if _, err := tx.Exec(ctx, `UPDATE users SET avatar_remote_url = $2 WHERE id = $1`, p.UserID, *a); err != nil {
							return err
						}
						if err := images.Enqueue(ctx, tx, s.d.Jobs, jobs.AvatarFetchArgs{Owner: images.OwnerUser, ID: p.UserID,
							URL: *a, Source: "upload"}); err != nil {
							return err
						}
					}
				}
				if name != nil && (in.Body.AvatarURL == nil || *in.Body.AvatarURL != "") {
					if err := images.Changed(ctx, tx, images.Owner{Type: images.OwnerUser, ID: p.UserID}); err != nil {
						return err
					}
				}
				u, err = loadUser(ctx, tx, p.UserID)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if oldPrefix != "" {
				images.Discard(s.d.Storage, oldPrefix)
			}
			return &struct{ Body CoreUser }{u}, nil
		})

	register(s, api, huma.Operation{OperationID: "change-password", Method: http.MethodPost, Path: "/v1/me/password",
		Summary: "Change your password", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				CurrentPassword string `json:"current_password,omitempty" maxLength:"1024" doc:"Required when the account has a password"`
				NewPassword     string `json:"new_password" maxLength:"1024"`
			}
		}) (*struct{}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limitShared(ctx, "password:change:"+p.UserID.String(), 10, time.Hour); err != nil {
				return nil, err
			}
			var hash *string
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT `+auth.PasswordHashSQL+` FROM users WHERE id = $1`, p.UserID).Scan(&hash); err != nil {
				return nil, httpx.Wrap(err)
			}
			if hash == nil {
				// Setting a first password on a provider-only account.
				if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
					return nil, httpx.Wrap(err)
				}
			} else if !auth.VerifyPassword(in.Body.CurrentPassword, *hash) {
				s.credentialFailed(ctx, p, "current_password")
				return nil, httpx.E(401, "invalid_credentials", "Your current password is incorrect.")
			}
			if err := auth.CheckPassword(in.Body.NewPassword); err != nil {
				return nil, httpx.Wrap(err)
			}
			if hash != nil && auth.VerifyPassword(in.Body.NewPassword, *hash) {
				return nil, httpx.E(422, "weak_password", "Choose a password different from your current one.")
			}
			newHash, err := auth.HashPassword(in.Body.NewPassword)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, password_expires_at = NULL, updated_at = now() WHERE id = $1`, p.UserID, newHash); err != nil {
					return err
				}
				if err := revokeOtherSessions(ctx, tx, p.UserID, p.SessionID); err != nil {
					return err
				}
				if _, err := oauthgrant.Revoke(ctx, tx, p, p.UserID, uuid.Nil, uuid.Nil, oauthgrant.ReasonSignedOut); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE one_time_tokens SET used_at = now() WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL`,
					p.UserID, ottReset); err != nil {
					return err
				}
				if _, err := auditUser(ctx, tx, p, p.UserID, "password.changed", nil); err != nil {
					return err
				}
				return s.notifySecurity(ctx, tx, p.UserID, emails.PasswordChanged, "")
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.disconnect(p.UserID, uuid.Nil)
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "delete-me", Method: http.MethodDelete, Path: "/v1/me",
		Summary: "Delete your account", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				Password string `json:"password,omitempty" maxLength:"1024"`
				Code     string `json:"code,omitempty" maxLength:"64"`
			} `required:"false"`
		}) (*struct {
			SetCookie []string `header:"Set-Cookie"`
		}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limitShared(ctx, "me:delete:"+p.UserID.String(), 10, time.Hour); err != nil {
				return nil, err
			}
			var hash *string
			var totp bool
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT `+auth.PasswordHashSQL+`, totp_enabled FROM users WHERE id = $1`, p.UserID).Scan(&hash, &totp); err != nil {
				return nil, httpx.Wrap(err)
			}
			if hash != nil {
				if !auth.VerifyPassword(in.Body.Password, *hash) {
					s.credentialFailed(ctx, p, "current_password")
					return nil, httpx.E(401, "invalid_credentials", "Your password is incorrect.")
				}
			} else if err := requireRecentLogin(ctx, s.d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			// This account's cookies in this browser go with it; other accounts stay signed in.
			ri := requestInfo(ctx)
			var clear []string
			if !ri.Native {
				held, err := s.heldSessions(ctx, s.d.DB.Pool, ri)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				for _, h := range held {
					if h.UserID == p.UserID || (h.Valid && h.Account == p.UserID) {
						clear = append(clear, s.clearCookie(h.Name))
					}
				}
			}
			var keys []string
			var disconnect []uuid.UUID
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if totp {
					if in.Body.Code == "" {
						return httpx.E(403, "mfa_required", "Enter a code from your authenticator app.")
					}
					if _, err := s.checkSecondFactor(ctx, tx, p.UserID, in.Body.Code); err != nil {
						return err
					}
				}
				var err error
				disconnect, keys, err = s.deleteAccount(ctx, tx, p, p.UserID)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.d.Log.Info("account deleted", "user_id", p.UserID, "ip", p.IP)
			s.purgeFiles(keys)
			for _, uid := range disconnect {
				s.disconnect(uid, uuid.Nil)
			}
			return &struct {
				SetCookie []string `header:"Set-Cookie"`
			}{clear}, nil
		})

	s.registerSessions(api)
}

// deleteAccount removes a user: the workspaces they own alone go with them, they leave
// the others. It refuses (409 sole_owner) while they own a workspace with other members.
// It returns the users to disconnect and the stored files to purge.
func (s *svc) deleteAccount(ctx context.Context, tx pgx.Tx, p *auth.Principal, uid uuid.UUID) ([]uuid.UUID, []string, error) {
	var keys []string
	var disconnect []uuid.UUID
	rows, err := tx.Query(ctx, `SELECT m.workspace_id, m.role,
		(SELECT count(*) FROM members o WHERE o.workspace_id = m.workspace_id AND o.user_id <> m.user_id)
		FROM members m WHERE m.user_id = $1 FOR UPDATE OF m`, uid)
	if err != nil {
		return nil, nil, err
	}
	type mem struct {
		wid    uuid.UUID
		role   string
		others int
	}
	mems, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (mem, error) {
		var m mem
		err := r.Scan(&m.wid, &m.role, &m.others)
		return m, err
	})
	if err != nil {
		return nil, nil, err
	}
	for _, m := range mems {
		if m.role == string(auth.RoleOwner) && m.others > 0 {
			return nil, nil, httpx.E(409, "sole_owner", "Transfer ownership of your workspaces that have other members before deleting your account.")
		}
	}
	for _, m := range mems {
		if m.role == string(auth.RoleOwner) {
			_, k, err := s.deleteWorkspace(ctx, tx, m.wid)
			if err != nil {
				return nil, nil, err
			}
			keys = append(keys, k...)
			continue
		}
		lapsed, err := s.removeMember(ctx, tx, p, m.wid, uid, m.role, "member.account_deleted")
		if err != nil {
			return nil, nil, err
		}
		disconnect = append(disconnect, lapsed...)
	}
	disconnect = append(disconnect, uid)
	_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid)
	return disconnect, keys, err
}
