package tickets

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
)

type editMessageInput struct {
	ID        string `path:"id"`
	MessageID string `path:"messageId"`
	Body      struct {
		BodyHTML string `json:"body_html" maxLength:"500000"`
	}
}

type deleteMessageInput struct {
	ID        string `path:"id"`
	MessageID string `path:"messageId"`
}

type messageMutationTarget struct {
	authorID   *uuid.UUID
	authorType string
	kind       string
	threadID   *uuid.UUID
	bodyHTML   string
	bodyText   string
}

func registerMessageMutations(d *deps.Deps, api huma.API, tags []string) {
	huma.Register(api, huma.Operation{OperationID: "edit-ticket-note", Method: http.MethodPatch,
		Path: "/v1/tickets/{id}/messages/{messageId}", Summary: "Edit an internal note", Tags: tags},
		func(ctx context.Context, in *editMessageInput) (*struct{ Body domain.Message }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			mid, err := hd.ParseID(in.MessageID, "Message")
			if err != nil {
				return nil, err
			}
			cleanHTML := hd.SanitizeRich(in.Body.BodyHTML)
			bodyText := hd.HTMLToText(cleanHTML)
			var edited domain.Message
			if err := d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, mid)
				if err != nil {
					return err
				}
				if target.kind != "note" || target.threadID != nil {
					return httpx.Validation("only internal notes can be edited")
				}
				if target.authorType != "user" || target.authorID == nil || *target.authorID != p.UserID || p.Kind != auth.KindUser {
					return httpx.Forbidden()
				}
				// Empty as on create: no text, no inline image and no attachment.
				if strings.TrimSpace(bodyText) == "" && !strings.Contains(cleanHTML, "<img") {
					var hasAttachments bool
					if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachments WHERE workspace_id = $1 AND message_id = $2)`, p.WorkspaceID, mid).Scan(&hasAttachments); err != nil {
						return err
					}
					if !hasAttachments {
						return httpx.Validation("a note needs text or an attachment")
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE messages SET body_html = $1, body_text = $2, edited_at = now()
				WHERE workspace_id = $3 AND ticket_id = $4 AND id = $5`, cleanHTML, bodyText, p.WorkspaceID, tid, mid); err != nil {
					return err
				}
				actor := actorOf(p)
				if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID,
					"note_edited", map[string]any{"message_id": mid}); err != nil {
					return err
				}
				edited, err = domain.LoadMessage(ctx, tx, p.WorkspaceID, mid)
				if err != nil {
					return err
				}
				// Mentions already sent show the note as it reads now.
				if _, err := tx.Exec(ctx, `UPDATE notifications SET data = jsonb_set(data, '{preview}', to_jsonb($4::text))
				WHERE workspace_id = $1 AND ticket_id = $2 AND type = $3 AND data->>'message_id' = $5`,
					p.WorkspaceID, tid, hd.NotifyMentioned, hd.Excerpt(bodyText, 140), mid.String()); err != nil {
					return err
				}
				// Someone the edit mentions for the first time hears about it; the others already did.
				if err := notifyNewMentions(ctx, tx, p.WorkspaceID, tid, edited, actor.UserID, target.bodyHTML, target.bodyText); err != nil {
					return err
				}
				return domain.PublishMessageUpdated(ctx, tx, p.WorkspaceID, mid)
			}); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Message }{edited}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-ticket-note", Method: http.MethodDelete,
		Path: "/v1/tickets/{id}/messages/{messageId}", Summary: "Delete an internal note", Tags: tags},
		func(ctx context.Context, in *deleteMessageInput) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			mid, err := hd.ParseID(in.MessageID, "Message")
			if err != nil {
				return nil, err
			}
			var storageKeys []string
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, mid)
				if err != nil {
					return err
				}
				if target.kind != "note" || target.threadID != nil {
					return httpx.Validation("only internal notes can be deleted")
				}
				isAuthor := p.Kind == auth.KindUser && target.authorType == "user" && target.authorID != nil && *target.authorID == p.UserID
				isAdmin := p.Kind == auth.KindUser && p.Role.AtLeast(auth.RoleAdmin)
				if !isAuthor && !isAdmin {
					return httpx.Forbidden()
				}
				rows, err := tx.Query(ctx, `SELECT DISTINCT storage_key FROM attachments WHERE workspace_id = $1 AND message_id = $2`,
					p.WorkspaceID, mid)
				if err != nil {
					return err
				}
				keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
					var key string
					err := r.Scan(&key)
					return key, err
				})
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM messages WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`, p.WorkspaceID, tid, mid); err != nil {
					return err
				}
				// The note's mentions go with it, before their emails quote it.
				if _, err := tx.Exec(ctx, `DELETE FROM notifications WHERE workspace_id = $1 AND ticket_id = $2 AND type = $3 AND data->>'message_id' = $4`,
					p.WorkspaceID, tid, hd.NotifyMentioned, mid.String()); err != nil {
					return err
				}
				for _, key := range keys {
					var shared bool
					if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachments WHERE workspace_id = $1 AND storage_key = $2)`,
						p.WorkspaceID, key).Scan(&shared); err != nil {
						return err
					}
					if !shared {
						storageKeys = append(storageKeys, key)
					}
				}
				actor := actorOf(p)
				if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID,
					"note_deleted", map[string]any{"message_id": mid}); err != nil {
					return err
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "message.deleted", TicketID: &tid,
					Data: map[string]any{"ticket_id": tid, "message_id": mid}})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			for _, key := range storageKeys {
				if err := d.Storage.Delete(context.WithoutCancel(ctx), key); err != nil {
					d.Log.Warn("delete note attachment", "key", key, "error", err)
				}
			}
			return &struct{}{}, nil
		})
}

func lockMessageMutationTarget(ctx context.Context, tx pgx.Tx, wid, ticketID, messageID uuid.UUID) (messageMutationTarget, error) {
	var target messageMutationTarget
	err := tx.QueryRow(ctx, `SELECT author_type, author_user_id, kind, thread_id, body_html, body_text FROM messages
		WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3 FOR UPDATE`, wid, ticketID, messageID).
		Scan(&target.authorType, &target.authorID, &target.kind, &target.threadID, &target.bodyHTML, &target.bodyText)
	if db.IsNotFound(err) {
		return target, httpx.NotFound("Message")
	}
	return target, err
}
