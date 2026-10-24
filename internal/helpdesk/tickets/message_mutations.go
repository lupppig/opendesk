package tickets

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
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
	direction  string
	deleted    bool
	purged     bool
	// sentAsReply: a note sent as a reply, kept as a marker; nothing is left to change.
	sentAsReply bool
	threadID    *uuid.UUID
	bodyHTML    string
	bodyText    string
	// delivery is the reply's email delivery_status (a chat reply's follow-up email too).
	delivery  string
	createdAt time.Time
}

// EditNote applies the same note mutation as the HTTP API for another trusted
// surface. The caller must already have resolved and checked visibility of the
// ticket; authorship, sanitizing, notifications, activity and events are checked here.
func EditNote(ctx context.Context, tx pgx.Tx, d *deps.Deps, p *auth.Principal, tid, mid uuid.UUID, bodyHTML string) (domain.Message, error) {
	cleanHTML := hd.SanitizeRich(bodyHTML)
	bodyText := hd.HTMLToText(cleanHTML)
	target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, mid)
	if err != nil {
		return domain.Message{}, err
	}
	if target.sentAsReply {
		return domain.Message{}, httpx.Validation("this note was sent as a reply; it can't be edited")
	}
	if target.deleted {
		return domain.Message{}, httpx.NotFound("Message")
	}
	if target.kind != "note" || target.threadID != nil {
		return domain.Message{}, httpx.Validation("only internal notes can be edited")
	}
	if target.authorType != "user" || target.authorID == nil || *target.authorID != p.UserID || p.Kind != auth.KindUser {
		return domain.Message{}, httpx.Forbidden()
	}
	if strings.TrimSpace(bodyText) == "" && !strings.Contains(cleanHTML, "<img") {
		var hasAttachments bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachments WHERE workspace_id = $1 AND message_id = $2)`, p.WorkspaceID, mid).Scan(&hasAttachments); err != nil {
			return domain.Message{}, err
		}
		if !hasAttachments {
			return domain.Message{}, httpx.Validation("a note needs text or an attachment")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE messages SET body_html = $1, body_text = $2, edited_at = now()
		WHERE workspace_id = $3 AND ticket_id = $4 AND id = $5`, cleanHTML, bodyText, p.WorkspaceID, tid, mid); err != nil {
		return domain.Message{}, err
	}
	actor := actorOf(p)
	if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID, "note_edited", map[string]any{"message_id": mid}); err != nil {
		return domain.Message{}, err
	}
	edited, err := domain.LoadMessage(ctx, tx, p.WorkspaceID, mid)
	if err != nil {
		return domain.Message{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET data = jsonb_set(data, '{preview}', to_jsonb($4::text))
		WHERE workspace_id = $1 AND ticket_id = $2 AND type = $3 AND data->>'message_id' = $5`,
		p.WorkspaceID, tid, hd.NotifyMentioned, hd.Excerpt(bodyText, 140), mid.String()); err != nil {
		return domain.Message{}, err
	}
	if err := notifyNewMentions(ctx, tx, p.WorkspaceID, tid, edited, actor.UserID, target.bodyHTML, target.bodyText); err != nil {
		return domain.Message{}, err
	}
	return edited, domain.PublishMessageUpdated(ctx, tx, p.WorkspaceID, mid)
}

// DeleteMessage applies the normal ticket deletion policy for another trusted
// surface. A provider cannot use this to recall a delivered customer reply
// unless the same member could delete it in opendesk.
func DeleteMessage(ctx context.Context, tx pgx.Tx, d *deps.Deps, p *auth.Principal, tid, mid uuid.UUID) error {
	target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, mid)
	if err != nil {
		return err
	}
	if target.sentAsReply {
		return httpx.Validation("this note was sent as a reply; delete the reply instead")
	}
	if target.deleted {
		return nil
	}
	if target.threadID != nil {
		return httpx.Validation("a side conversation's messages can't be deleted")
	}
	rule, err := messageDeleteRule(ctx, tx, p.WorkspaceID, p.UserID)
	if err != nil {
		return err
	}
	if !mayDelete(p, rule, target) {
		return httpx.Forbidden()
	}
	if _, err := tx.Exec(ctx, `UPDATE messages SET deleted_at = now(), deleted_by = $4
		WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`, p.WorkspaceID, tid, mid, p.UserID); err != nil {
		return err
	}
	if target.delivery == "pending" {
		if _, err := tx.Exec(ctx, `UPDATE messages SET delivery_status = 'failed', delivery_error = $4
			WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3 AND delivery_status = 'pending'`, p.WorkspaceID, tid, mid, domain.DeletedBeforeSent); err != nil {
			return err
		}
	}
	if err := hideMessage(ctx, tx, d, p.WorkspaceID, tid, mid); err != nil {
		return err
	}
	typ := "message_deleted"
	if target.kind == "note" {
		typ = "note_deleted"
	}
	actor := actorOf(p)
	if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID, typ, map[string]any{"message_id": mid}); err != nil {
		return err
	}
	return publishDeletion(ctx, tx, p.WorkspaceID, tid, mid, target.kind == "reply")
}

func registerMessageMutations(d *deps.Deps, api huma.API, tags []string) {
	registerRetract(d, api, tags)
	registerPurge(d, api, tags)
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
			var edited domain.Message
			if err := d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				edited, err = EditNote(ctx, tx, d, p, tid, mid, in.Body.BodyHTML)
				return err
			}); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Message }{edited}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-ticket-message", Method: http.MethodDelete,
		Path: "/v1/tickets/{id}/messages/{messageId}", Summary: "Delete a message", Tags: tags,
		Description: "Leaves a marker in the conversation; admins can read it and restore it. Who may delete what follows settings.message_delete."},
		func(ctx context.Context, in *deleteMessageInput) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			mid, err := hd.ParseID(in.MessageID, "Message")
			if err != nil {
				return nil, err
			}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				return DeleteMessage(ctx, tx, d, p, tid, mid)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "restore-ticket-message", Method: http.MethodPost,
		Path: "/v1/tickets/{id}/messages/{messageId}/restore", Summary: "Restore a deleted message", Tags: tags},
		func(ctx context.Context, in *deleteMessageInput) (*struct{ Body domain.Message }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if p.Kind != auth.KindUser {
				return nil, httpx.Forbidden()
			}
			mid, err := hd.ParseID(in.MessageID, "Message")
			if err != nil {
				return nil, err
			}
			var msg domain.Message
			if err := d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, mid)
				if err != nil {
					return err
				}
				if target.sentAsReply {
					return httpx.Validation("this note was sent as a reply; it can't be restored")
				}
				if !target.deleted {
					return httpx.Validation("this message is not deleted")
				}
				if target.purged {
					return httpx.Validation("this message's content was removed for good")
				}
				if _, err := tx.Exec(ctx, `UPDATE messages SET deleted_at = NULL, deleted_by = NULL
					WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`, p.WorkspaceID, tid, mid); err != nil {
					return err
				}
				if err := hd.EnqueueAIEmbed(ctx, tx, d.Jobs, d.Cfg, p.WorkspaceID, "ticket", tid); err != nil {
					return err
				}
				actor := actorOf(p)
				if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID,
					"message_restored", map[string]any{"message_id": mid}); err != nil {
					return err
				}
				if target.kind == "reply" {
					if err := recountTicket(ctx, tx, p.WorkspaceID, tid); err != nil {
						return err
					}
					if err := domain.PublishTicketUpdated(ctx, tx, p.WorkspaceID, tid, []string{"last_message_preview"}); err != nil {
						return err
					}
				}
				if err := domain.PublishMessageUpdated(ctx, tx, p.WorkspaceID, mid); err != nil {
					return err
				}
				msg, err = domain.LoadMessage(ctx, tx, p.WorkspaceID, mid)
				return err
			}); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Message }{msg}, nil
		})
}

// registerPurge lets an admin remove a deleted message's content for good: its text, its
// files and the original email it came in, keeping the marker. For what should never have
// been written down, such as a card number or a password, or a request to erase data.
func registerPurge(d *deps.Deps, api huma.API, tags []string) {
	huma.Register(api, huma.Operation{OperationID: "purge-ticket-message", Method: http.MethodPost,
		Path: "/v1/tickets/{id}/messages/{messageId}/purge", Summary: "Remove a deleted message's content for good", Tags: tags},
		func(ctx context.Context, in *deleteMessageInput) (*struct{ Body domain.Message }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if p.Kind != auth.KindUser {
				return nil, httpx.Forbidden()
			}
			mid, err := hd.ParseID(in.MessageID, "Message")
			if err != nil {
				return nil, err
			}
			var msg domain.Message
			var keys []string
			if err := d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, mid)
				if err != nil {
					return err
				}
				if !target.deleted {
					return httpx.Validation("delete the message before removing it for good")
				}
				if !target.purged {
					if keys, err = purgeMessage(ctx, tx, d, p.WorkspaceID, tid, mid); err != nil {
						return err
					}
					actor := actorOf(p)
					if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID,
						"message_purged", map[string]any{"message_id": mid}); err != nil {
						return err
					}
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "message.purged", "message", &mid, map[string]any{"ticket_id": tid}); err != nil {
						return err
					}
					if err := publishDeletion(ctx, tx, p.WorkspaceID, tid, mid, false); err != nil {
						return err
					}
				}
				msg, err = domain.LoadMessage(ctx, tx, p.WorkspaceID, mid)
				return err
			}); err != nil {
				return nil, httpx.Wrap(err)
			}
			for _, key := range keys {
				if err := d.Storage.Delete(context.WithoutCancel(ctx), key); err != nil {
					d.Log.Warn("purge message file", "key", key, "error", err)
				}
			}
			return &struct{ Body domain.Message }{msg}, nil
		})
}

// purgeMessage empties a message and removes its files and its original email, returning
// the stored objects no other record still uses, to delete once the transaction commits.
// What was made from it goes too: notifications, Ollie's suggested reply to it or sent as
// it, an action card's summary and arguments, the ticket's AI summary and its search index.
func purgeMessage(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, tid, mid uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT storage_key FROM attachments WHERE workspace_id = $1 AND message_id = $2`, wid, mid)
	if err != nil {
		return nil, err
	}
	files, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM attachments WHERE workspace_id = $1 AND message_id = $2`, wid, mid); err != nil {
		return nil, err
	}
	// The original email it came in, unless another stored email is the same file.
	var raw string
	var inbound *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT ie.id, ie.raw_storage_key FROM messages m JOIN inbound_emails ie ON ie.workspace_id = m.workspace_id AND ie.id = m.inbound_email_id
		WHERE m.workspace_id = $1 AND m.id = $2`, wid, mid).Scan(&inbound, &raw)
	if err != nil && !db.IsNotFound(err) {
		return nil, err
	}
	if inbound != nil {
		if _, err := tx.Exec(ctx, `UPDATE inbound_emails SET raw_storage_key = '' WHERE workspace_id = $1 AND id = $2`, wid, *inbound); err != nil {
			return nil, err
		}
		var shared bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inbound_emails WHERE raw_storage_key = $1)`, raw).Scan(&shared); err != nil {
			return nil, err
		}
		if shared {
			raw = ""
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE messages SET body_html = '', body_text = '', full_html = '', subject = '', ai_action = NULL, purged_at = now()
		WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`, wid, tid, mid); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM notifications WHERE workspace_id = $1 AND ticket_id = $2 AND data->>'message_id' = $3`,
		wid, tid, mid.String()); err != nil {
		return nil, err
	}
	// Ollie's reply to it, or the one it was sent as, may quote it.
	if _, err := tx.Exec(ctx, `UPDATE ai_suggested_replies SET body_html = '',
		status = CASE WHEN status = 'pending' THEN 'superseded' ELSE status END
		WHERE workspace_id = $1 AND ticket_id = $2 AND (message_id = $3 OR reply_id = $3)`, wid, tid, mid); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_widget_actions SET summary = '', args = ''::bytea,
		status = CASE WHEN status IN ('pending', 'running') THEN 'expired' ELSE status END
		WHERE workspace_id = $1 AND ticket_id = $2 AND message_id = $3`, wid, tid, mid); err != nil {
		return nil, err
	}
	// The ticket's AI summary was written from the conversation it was part of.
	tag, err := tx.Exec(ctx, `UPDATE tickets SET custom = custom - 'ai_summary', updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND custom ? 'ai_summary'`, wid, tid)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() > 0 {
		if err := domain.PublishTicketUpdated(ctx, tx, wid, tid, []string{"custom"}); err != nil {
			return nil, err
		}
	}
	if err := hd.EnqueueAIEmbed(ctx, tx, d.Jobs, d.Cfg, wid, "ticket", tid); err != nil {
		return nil, err
	}
	var keys []string
	for _, key := range files {
		var shared bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachments WHERE workspace_id = $1 AND storage_key = $2)`, wid, key).Scan(&shared); err != nil {
			return nil, err
		}
		if !shared {
			keys = append(keys, key)
		}
	}
	if raw != "" {
		keys = append(keys, raw)
	}
	return keys, nil
}

// hideMessage takes what a message said out of what was made from it when it is deleted or
// a note is sent as a reply: its mention notifications go (before their emails quote it),
// other notifications lose their preview, an action card waiting on the visitor expires,
// and the ticket's search index is rebuilt without it.
func hideMessage(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, tid, mid uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM notifications WHERE workspace_id = $1 AND ticket_id = $2 AND type = $3 AND data->>'message_id' = $4`,
		wid, tid, hd.NotifyMentioned, mid.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET data = data - 'preview'
		WHERE workspace_id = $1 AND ticket_id = $2 AND data->>'message_id' = $3 AND data ? 'preview'`, wid, tid, mid.String()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_widget_actions SET status = 'expired', decided_at = COALESCE(decided_at, now())
		WHERE workspace_id = $1 AND ticket_id = $2 AND message_id = $3 AND status = 'pending'`, wid, tid, mid); err != nil {
		return err
	}
	return hd.EnqueueAIEmbed(ctx, tx, d.Jobs, d.Cfg, wid, "ticket", tid)
}

// messageDeleteRule is which messages an agent may delete: their own rule if an admin set
// one, else the workspace's (settings.message_delete).
func messageDeleteRule(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID) (string, error) {
	var rule string
	err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT m.message_delete FROM members m WHERE m.workspace_id = w.id AND m.user_id = $2),
		w.settings->>'message_delete', 'own') FROM workspaces w WHERE w.id = $1`, wid, uid).Scan(&rule)
	return rule, err
}

// mayDelete: an admin any message; an agent, as their rule allows: "own" (the default)
// their own internal notes, "all" any agent's notes and replies, "off" none. Never a
// customer's, a bot's or an automation's message.
func mayDelete(p *auth.Principal, rule string, t messageMutationTarget) bool {
	if p.Kind != auth.KindUser {
		return false
	}
	if p.Role.AtLeast(auth.RoleAdmin) {
		return true
	}
	if t.authorType != "user" {
		return false
	}
	switch rule {
	case "all":
		return true
	case "own":
		return t.kind == "note" && t.authorID != nil && *t.authorID == p.UserID
	}
	return false
}

// publishDeletion tells the team a message is now a marker (without what it said) and,
// for a reply, takes it out of the visitor's widget and the ticket's count and preview.
func publishDeletion(ctx context.Context, tx pgx.Tx, wid, tid, mid uuid.UUID, reply bool) error {
	if err := domain.PublishMessageUpdated(ctx, tx, wid, mid); err != nil {
		return err
	}
	if !reply {
		return nil
	}
	if err := withdrawFromVisitor(ctx, tx, wid, tid, mid); err != nil {
		return err
	}
	if err := recountTicket(ctx, tx, wid, tid); err != nil {
		return err
	}
	return domain.PublishTicketUpdated(ctx, tx, wid, tid, []string{"last_message_preview"})
}

// withdrawFromVisitor removes a reply from the visitor's widget on a chat ticket
// (message.withdrawn). The team keeps it, as a marker or a note, so the event is the
// visitor's alone in meaning: the agent app leaves its copy as it is.
func withdrawFromVisitor(ctx context.Context, tx pgx.Tx, wid, tid, mid uuid.UUID) error {
	var channel string
	var contactID *uuid.UUID
	var brandID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT channel, contact_id, brand_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, tid).
		Scan(&channel, &contactID, &brandID); err != nil {
		return err
	}
	if channel != domain.ChannelChat || contactID == nil {
		return nil
	}
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "message.withdrawn", TicketID: &tid,
		Visibility: events.VisibilityPublic, Topics: []string{events.ContactTopic(wid, *contactID, brandID)},
		Data: map[string]any{"ticket_id": tid, "message_id": mid}})
	return err
}

// recountTicket sets a ticket's reply count and preview from the replies it still shows.
func recountTicket(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE tickets SET updated_at = now(),
		message_count = (SELECT count(*) FROM messages m WHERE m.workspace_id = $1 AND m.ticket_id = $2 AND m.kind = 'reply' AND m.deleted_at IS NULL),
		last_message_preview = COALESCE((SELECT left(regexp_replace(btrim(m.body_text), '\s+', ' ', 'g'), 200) FROM messages m
			WHERE m.workspace_id = $1 AND m.ticket_id = $2 AND m.kind = 'reply' AND m.deleted_at IS NULL
			ORDER BY m.created_at DESC, m.id DESC LIMIT 1), '')
		WHERE workspace_id = $1 AND id = $2`, wid, tid)
	return err
}

// registerRetract takes a chat reply back from the customer: it becomes an internal note,
// leaves the visitor's widget and is never sent by email as a chat follow-up. An email reply
// is delivered when it is sent, so it cannot be taken back.
func registerRetract(d *deps.Deps, api huma.API, tags []string) {
	huma.Register(api, huma.Operation{OperationID: "retract-ticket-reply", Method: http.MethodPost,
		Path: "/v1/tickets/{id}/messages/{messageId}/retract", Summary: "Take a chat reply back from the customer", Tags: tags},
		func(ctx context.Context, in *deleteMessageInput) (*struct{ Body domain.Message }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			mid, err := hd.ParseID(in.MessageID, "Message")
			if err != nil {
				return nil, err
			}
			var msg domain.Message
			if err := d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, mid)
				if err != nil {
					return err
				}
				var channel string
				if err := tx.QueryRow(ctx, `SELECT channel FROM tickets WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, tid).Scan(&channel); err != nil {
					return err
				}
				if target.deleted {
					return httpx.NotFound("Message")
				}
				if target.kind != "reply" || target.direction != "outbound" || target.authorType != "user" {
					return httpx.Validation("only an agent's reply can be taken back")
				}
				if channel != domain.ChannelChat {
					return httpx.Validation("only a chat reply can be taken back; email is delivered when it is sent")
				}
				// A chat reply the visitor missed goes out by email (chat follow-up): once that
				// is queued or sent, it is theirs.
				if target.delivery != "none" {
					return httpx.Validation("this reply was also sent by email; it can't be taken back")
				}
				isAuthor := p.Kind == auth.KindUser && target.authorID != nil && *target.authorID == p.UserID
				isAdmin := p.Kind == auth.KindUser && p.Role.AtLeast(auth.RoleAdmin)
				if !isAuthor && !isAdmin {
					return httpx.Forbidden()
				}
				if _, err := tx.Exec(ctx, `UPDATE messages SET kind = 'note' WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`,
					p.WorkspaceID, tid, mid); err != nil {
					return err
				}
				// The conversation reads as if the reply was never there, and it is not the
				// ticket's first response.
				if err := recountTicket(ctx, tx, p.WorkspaceID, tid); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE tickets t SET first_responded_at = (SELECT min(m.created_at) FROM messages m
					WHERE m.workspace_id = t.workspace_id AND m.ticket_id = t.id AND m.kind = 'reply' AND m.direction = 'outbound'
					AND m.author_type = 'user' AND m.deleted_at IS NULL)
					WHERE t.workspace_id = $1 AND t.id = $2 AND t.first_responded_at = $3`, p.WorkspaceID, tid, target.createdAt); err != nil {
					return err
				}
				actor := actorOf(p)
				if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID,
					"reply_retracted", map[string]any{"message_id": mid}); err != nil {
					return err
				}
				if err := withdrawFromVisitor(ctx, tx, p.WorkspaceID, tid, mid); err != nil {
					return err
				}
				if err := domain.PublishMessageUpdated(ctx, tx, p.WorkspaceID, mid); err != nil {
					return err
				}
				if err := domain.PublishTicketUpdated(ctx, tx, p.WorkspaceID, tid, []string{"last_message_preview"}); err != nil {
					return err
				}
				msg, err = domain.LoadMessage(ctx, tx, p.WorkspaceID, mid)
				return err
			}); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Message }{msg}, nil
		})
}

func lockMessageMutationTarget(ctx context.Context, tx pgx.Tx, wid, ticketID, messageID uuid.UUID) (messageMutationTarget, error) {
	var target messageMutationTarget
	err := tx.QueryRow(ctx, `SELECT author_type, author_user_id, kind, direction, thread_id, body_html, body_text, deleted_at IS NOT NULL, purged_at IS NOT NULL,
		sent_as_reply_id IS NOT NULL, delivery_status, created_at FROM messages
		WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3 FOR UPDATE`, wid, ticketID, messageID).
		Scan(&target.authorType, &target.authorID, &target.kind, &target.direction, &target.threadID, &target.bodyHTML, &target.bodyText,
			&target.deleted, &target.purged, &target.sentAsReply, &target.delivery, &target.createdAt)
	if db.IsNotFound(err) {
		return target, httpx.NotFound("Message")
	}
	return target, err
}
