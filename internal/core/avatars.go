package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/images"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// installImageHooks publishes member.updated (users) and workspace.updated
// (workspace images) when a picture changes, including from the refresh job.
func installImageHooks() {
	images.SetHooks(images.Hooks{
		User: func(ctx context.Context, tx pgx.Tx, uid uuid.UUID) error {
			wids, err := userWorkspaces(ctx, tx, uid)
			if err != nil {
				return err
			}
			for _, wid := range wids {
				if err := publishMember(ctx, tx, wid, uid); err != nil {
					return err
				}
			}
			return nil
		},
		Workspace: func(ctx context.Context, tx pgx.Tx, wid uuid.UUID) error {
			w, err := loadWorkspace(ctx, tx, wid)
			if err != nil {
				return err
			}
			return publishWorkspace(ctx, tx, w)
		},
	})
}

func publishMember(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID) error {
	m, err := loadMember(ctx, tx, wid, uid)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "member.updated",
		Topics: []string{events.UserTopic(uid)}, Data: map[string]any{"member": m}})
	return err
}

// validPictureURL accepts an https URL without credentials, up to 2000 characters.
func validPictureURL(s string) bool {
	if len(s) > 2000 {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// providerPicture queues the sign-in provider's picture for a user who has not uploaded
// one, when it changed since the last copy.
func (s *svc) providerPicture(ctx context.Context, tx pgx.Tx, uid uuid.UUID, raw string) error {
	if raw == "" || !validPictureURL(raw) {
		return nil
	}
	var changed bool
	if err := tx.QueryRow(ctx, `UPDATE users SET avatar_remote_url = $2 WHERE id = $1
		AND NOT (avatar_source = 'upload' AND avatar_url <> '') AND (avatar_remote_url <> $2 OR avatar_url = '') RETURNING true`,
		uid, raw).Scan(&changed); err != nil {
		if db.IsNotFound(err) {
			return nil
		}
		return err
	}
	return images.Enqueue(ctx, tx, s.d.Jobs, jobs.AvatarFetchArgs{Owner: images.OwnerUser, ID: uid, URL: raw, Source: "oauth"})
}

// auditPicture records a picture change in every workspace of the user.
func auditPicture(p *auth.Principal, uid uuid.UUID, action string) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		_, err := auditUser(ctx, tx, p, uid, action, nil)
		return err
	}
}

func (s *svc) registerAvatars(api huma.API) {
	register(s, api, images.UploadOperation(api, huma.Operation{OperationID: "upload-my-avatar", Method: http.MethodPut,
		Path: "/v1/me/avatar", Summary: "Upload your profile picture", Tags: []string{"Account"},
		Description: "PNG, JPEG, GIF or WebP, at most 5 MB. Stored as a 256 and a 64 px PNG."}),
		func(ctx context.Context, in *images.UploadInput) (*struct{ Body CoreUser }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("avatar:"+p.UserID.String(), 30, time.Hour); err != nil {
				return nil, err
			}
			img, err := images.ReadUpload(in, images.Cover)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := images.Set(ctx, s.d, images.Owner{Type: images.OwnerUser, ID: p.UserID}, img, "upload",
				images.SetOptions{After: auditPicture(p, p.UserID, "user.avatar_updated")}); err != nil {
				return nil, httpx.Wrap(err)
			}
			u, err := loadUser(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreUser }{u}, nil
		})

	register(s, api, huma.Operation{OperationID: "delete-my-avatar", Method: http.MethodDelete, Path: "/v1/me/avatar",
		Summary: "Remove your profile picture", Tags: []string{"Account"}},
		func(ctx context.Context, _ *struct{}) (*struct{ Body CoreUser }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = images.Clear(ctx, s.d, images.Owner{Type: images.OwnerUser, ID: p.UserID}, false, images.SetOptions{
				After: func(ctx context.Context, tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `UPDATE users SET avatar_remote_url = '' WHERE id = $1`, p.UserID); err != nil {
						return err
					}
					return auditPicture(p, p.UserID, "user.avatar_removed")(ctx, tx)
				}})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			u, err := loadUser(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreUser }{u}, nil
		})

	register(s, api, huma.Operation{OperationID: "heartbeat", Method: http.MethodPost, Path: "/v1/me/heartbeat",
		Summary: "Mark yourself seen", Description: "Keeps auto-away from marking you away while the app is open, and holds notification emails for the workspace in OD-Workspace.",
		Tags: []string{"Account"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE users SET last_seen_at = now() WHERE id = $1
				AND (last_seen_at IS NULL OR last_seen_at < now() - interval '30 seconds')`, p.UserID); err != nil {
				return nil, httpx.Wrap(err)
			}
			// Notification emails wait while the member is in the app here.
			if p.WorkspaceID != uuid.Nil {
				if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE members SET active_at = now() WHERE workspace_id = $1 AND user_id = $2
					AND (active_at IS NULL OR active_at < now() - interval '30 seconds')`, p.WorkspaceID, p.UserID); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "update-member-availability", Method: http.MethodPatch,
		Path: "/v1/members/{user_id}/availability", Summary: "Set a member's availability and assignment cap", Tags: []string{"Members"},
		Description: "user_id may be me. Members set their own availability; the owner anyone's, admins those of agents and viewers, and max_open_tickets."},
		func(ctx context.Context, in *struct {
			UserID string `path:"user_id"`
			Body   struct {
				Availability   *string     `json:"availability,omitempty" enum:"online,away,offline"`
				MaxOpenTickets OptionalInt `json:"max_open_tickets,omitempty" doc:"1..1000, null clears"`
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
			self := uid == p.UserID
			admin := p.Role.AtLeast(auth.RoleAdmin)
			if (!self && !admin) || (in.Body.MaxOpenTickets.Set && !admin) {
				return nil, httpx.Forbidden()
			}
			if !self {
				// Someone else's only as far as their role allows (canManage): an admin sets
				// agents' and viewers', never the owner's or another admin's.
				var role string
				err := s.d.DB.Pool.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2`, p.WorkspaceID, uid).Scan(&role)
				if db.IsNotFound(err) {
					return nil, httpx.NotFound("Member")
				}
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if !canManage(p.Role, auth.Role(role)) {
					return nil, httpx.Forbidden()
				}
			}
			if a := in.Body.Availability; a != nil && !slices.Contains([]string{"online", "away", "offline"}, *a) {
				return nil, httpx.Validation("availability must be online, away or offline.")
			}
			mc := in.Body.MaxOpenTickets
			if mc.Set && mc.Value != nil && (*mc.Value < 1 || *mc.Value > 1000) {
				return nil, httpx.Validation("max_open_tickets must be between 1 and 1000, or null.")
			}
			var m CoreMember
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var role string
				if err := tx.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`,
					p.WorkspaceID, uid).Scan(&role); err != nil {
					if db.IsNotFound(err) {
						return httpx.NotFound("Member")
					}
					return err
				}
				if mc.Set && role == string(auth.RoleViewer) {
					return httpx.Validation("Viewers are never assigned tickets, so they have no cap.")
				}
				if _, err := tx.Exec(ctx, `UPDATE members SET availability = COALESCE($3, availability),
					max_open_tickets = CASE WHEN $4 THEN $5 ELSE max_open_tickets END WHERE workspace_id = $1 AND user_id = $2`,
					p.WorkspaceID, uid, in.Body.Availability, mc.Set, mc.Value); err != nil {
					return err
				}
				if self && in.Body.Availability != nil {
					if _, err := tx.Exec(ctx, `UPDATE users SET last_seen_at = now() WHERE id = $1`, uid); err != nil {
						return err
					}
				}
				if mc.Set {
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "member.assignment_cap_changed", "user", &uid,
						map[string]any{"max_open_tickets": mc.Value}); err != nil {
						return err
					}
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

	slotDoc := "logo (the workspace avatar_url), help_center_logo, help_center_favicon, widget_avatar or widget_launcher (a bot's picture: PUT /v1/bots/{id}/image)"
	register(s, api, images.UploadOperation(api, huma.Operation{OperationID: "upload-workspace-image", Method: http.MethodPut,
		Path: "/v1/workspace/images/{slot}", Summary: "Upload a workspace logo or brand image", Tags: []string{"Workspace"},
		Description: "slot: " + slotDoc + ". PNG, JPEG, GIF or WebP, at most 5 MB."}),
		func(ctx context.Context, in *struct {
			Slot string `path:"slot" enum:"logo,help_center_logo,help_center_favicon,widget_avatar,widget_launcher"`
			images.UploadInput
		}) (*struct{ Body CoreWorkspace }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("wsimage:"+p.WorkspaceID.String(), 60, time.Hour); err != nil {
				return nil, err
			}
			img, err := images.ReadUpload(&in.UploadInput, images.Contain)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			o := images.Owner{Type: images.OwnerWorkspace, ID: p.WorkspaceID, WorkspaceID: &p.WorkspaceID, Slot: in.Slot}
			if err := images.Set(ctx, s.d, o, img, "upload", images.SetOptions{After: func(ctx context.Context, tx pgx.Tx) error {
				return audit.Log(ctx, tx, p, p.WorkspaceID, "workspace.image_updated", "workspace", &p.WorkspaceID, map[string]any{"slot": in.Slot})
			}}); err != nil {
				return nil, httpx.Wrap(err)
			}
			w, err := loadWorkspace(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreWorkspace }{w}, nil
		})

	register(s, api, huma.Operation{OperationID: "delete-workspace-image", Method: http.MethodDelete,
		Path: "/v1/workspace/images/{slot}", Summary: "Remove a workspace logo or brand image", Tags: []string{"Workspace"}},
		func(ctx context.Context, in *struct {
			Slot string `path:"slot" enum:"logo,help_center_logo,help_center_favicon,widget_avatar,widget_launcher"`
		}) (*struct{ Body CoreWorkspace }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			o := images.Owner{Type: images.OwnerWorkspace, ID: p.WorkspaceID, WorkspaceID: &p.WorkspaceID, Slot: in.Slot}
			if err := images.Clear(ctx, s.d, o, false, images.SetOptions{After: func(ctx context.Context, tx pgx.Tx) error {
				return audit.Log(ctx, tx, p, p.WorkspaceID, "workspace.image_removed", "workspace", &p.WorkspaceID, map[string]any{"slot": in.Slot})
			}}); err != nil {
				return nil, httpx.Wrap(err)
			}
			w, err := loadWorkspace(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreWorkspace }{w}, nil
		})
}

// OptionalInt tells an absent member (Set false) from null (Value nil).
type OptionalInt struct {
	Set   bool
	Value *int
}

func (o *OptionalInt) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// Schema describes the field as a nullable integer.
func (OptionalInt) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeInteger, Nullable: true}
}
