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
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/templates"
)

// Third-party threads (contracts/api.md "Threads"): an email conversation on a ticket with
// someone outside the team. Its messages are internal notes, sent to the thread's
// recipients from the thread's address; their answers come back into the thread.

type threadCreateBody struct {
	To            []string `json:"to" minItems:"1" maxItems:"20"`
	Cc            []string `json:"cc,omitempty" maxItems:"20"`
	Subject       string   `json:"subject" maxLength:"500"`
	BodyHTML      string   `json:"body_html" maxLength:"500000"`
	AttachmentIDs []string `json:"attachment_ids,omitempty" maxItems:"20"`
	MailboxID     string   `json:"mailbox_id,omitempty" doc:"The address to send from: one of the ticket's brand the caller may send from. Default the ticket's reply address"`
	// What of the customer's conversation goes along; nothing unless asked for.
	IncludeHistory       bool     `json:"include_history,omitempty" doc:"Quote the ticket's conversation with the customer (its last 10 public replies, never notes) under the first message"`
	ForwardAttachmentIDs []string `json:"forward_attachment_ids,omitempty" maxItems:"20" doc:"Attachments of the ticket's public replies to send along with the first message"`
}

type threadMessageBody struct {
	BodyHTML      string   `json:"body_html" maxLength:"500000"`
	AttachmentIDs []string `json:"attachment_ids,omitempty" maxItems:"20"`
}

type threadPatchBody struct {
	Status string `json:"status" enum:"open,closed"`
}

type threadCreated struct {
	Thread  domain.Thread  `json:"thread"`
	Message domain.Message `json:"message"`
}

func registerThreads(d *deps.Deps, api huma.API) {
	tags := []string{"Tickets"}

	huma.Register(api, huma.Operation{OperationID: "list-ticket-threads", Method: http.MethodGet, Path: "/v1/tickets/{id}/threads",
		Summary: "List a ticket's third-party threads", Description: "Oldest first. Their messages are in the ticket's messages, with thread_id set.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct {
			Body struct {
				Data []domain.Thread `json:"data"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := resolveVisible(ctx, d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := domain.ListThreads(ctx, d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []domain.Thread `json:"data"`
				}
			}{}
			out.Body.Data = list
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-ticket-thread", Method: http.MethodPost, Path: "/v1/tickets/{id}/threads",
		Summary: "Start a third-party thread", Description: "Emails the first message to the recipients from the ticket's reply address (or mailbox_id).",
		Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body threadCreateBody
		}) (*struct{ Body threadCreated }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var out threadCreated
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				out, err = startThread(ctx, tx, d, p, tid, in.Body)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body threadCreated }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-ticket-thread-message", Method: http.MethodPost, Path: "/v1/tickets/{id}/threads/{threadId}/messages",
		Summary: "Write in a third-party thread", Description: "Emails the message to the thread's recipients. A closed thread opens again.",
		Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			ID       string `path:"id"`
			ThreadID string `path:"threadId"`
			Body     threadMessageBody
		}) (*struct{ Body domain.Message }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var msg domain.Message
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, th, err := resolveThread(ctx, tx, p, in.ID, in.ThreadID)
				if err != nil {
					return err
				}
				msg, err = addThreadMessage(ctx, tx, d, p, tid, th, in.Body)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Message }{msg}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-ticket-thread", Method: http.MethodPatch, Path: "/v1/tickets/{id}/threads/{threadId}",
		Summary: "Close or reopen a third-party thread", Tags: tags},
		func(ctx context.Context, in *struct {
			ID       string `path:"id"`
			ThreadID string `path:"threadId"`
			Body     threadPatchBody
		}) (*struct{ Body domain.Thread }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var th domain.Thread
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tid, cur, err := resolveThread(ctx, tx, p, in.ID, in.ThreadID)
				if err != nil {
					return err
				}
				if cur.Status == in.Body.Status {
					th = cur
					return nil
				}
				if _, err := tx.Exec(ctx, `UPDATE ticket_threads SET status = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, cur.ID, in.Body.Status); err != nil {
					return err
				}
				typ := "thread_closed"
				if in.Body.Status == domain.ThreadOpen {
					typ = "thread_reopened"
				}
				actor := actorOf(p)
				if _, err := domain.LogActivity(ctx, tx, p.WorkspaceID, tid, actor.Type, actor.UserID, typ,
					map[string]any{"thread_id": cur.ID.String(), "subject": cur.Subject}); err != nil {
					return err
				}
				th, err = domain.PublishThread(ctx, tx, p.WorkspaceID, tid, cur.ID, "thread.updated")
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Thread }{th}, nil
		})
}

// resolveThread finds a thread of a ticket the caller can see.
func resolveThread(ctx context.Context, tx pgx.Tx, p *auth.Principal, ticketRef, threadRef string) (uuid.UUID, domain.Thread, error) {
	tid, err := resolveVisible(ctx, tx, p, ticketRef)
	if err != nil {
		return tid, domain.Thread{}, err
	}
	id, err := uuid.Parse(threadRef)
	if err != nil {
		return tid, domain.Thread{}, httpx.E(http.StatusNotFound, "not_found", "No such thread.")
	}
	th, err := domain.LoadThread(ctx, tx, p.WorkspaceID, tid, id)
	if db.IsNotFound(err) {
		return tid, th, httpx.E(http.StatusNotFound, "not_found", "No such thread.")
	}
	return tid, th, err
}

func startThread(ctx context.Context, tx pgx.Tx, d *deps.Deps, p *auth.Principal, tid uuid.UUID, b threadCreateBody) (threadCreated, error) {
	wid := p.WorkspaceID
	subject := strings.Join(strings.Fields(b.Subject), " ")
	if subject == "" {
		return threadCreated{}, httpx.Validation("a thread needs a subject")
	}
	to, err := cleanEmails(b.To)
	if err != nil {
		return threadCreated{}, err
	}
	if len(to) == 0 {
		return threadCreated{}, httpx.Validation("a thread needs at least one recipient")
	}
	cc, err := cleanEmails(b.Cc)
	if err != nil {
		return threadCreated{}, err
	}
	var brand uuid.UUID
	var ticketMailbox, team *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT brand_id, mailbox_id, team_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, tid).
		Scan(&brand, &ticketMailbox, &team); err != nil {
		return threadCreated{}, err
	}
	// The address the thread is sent from: the one asked for, else the ticket's reply
	// address, else its brand's address for the ticket's team.
	from := ticketMailbox
	if strings.TrimSpace(b.MailboxID) != "" {
		id, err := hd.ParseRef(b.MailboxID, "mailbox_id")
		if err != nil {
			return threadCreated{}, err
		}
		ok, err := mailboxInBrand(ctx, tx, wid, brand, &id)
		if err != nil {
			return threadCreated{}, err
		}
		if !ok {
			return threadCreated{}, httpx.Validation("mailbox_id is not an address of this ticket's brand")
		}
		from = &id
	} else if from == nil {
		if from, err = replyMailbox(ctx, tx, wid, brand, team); err != nil {
			return threadCreated{}, err
		}
	}
	if from == nil {
		return threadCreated{}, httpx.Validation("this ticket's brand has no address to send from")
	}
	ok, err := domain.CanSendFrom(ctx, tx, p, *from)
	if err != nil {
		return threadCreated{}, err
	}
	if !ok {
		return threadCreated{}, httpx.E(http.StatusForbidden, "reply_address_forbidden",
			"You can't send from this address. Pick another address to send from.")
	}
	actor := actorOf(p)
	id := ids.New()
	forwarded, err := forwardAttachments(ctx, tx, p, tid, b.ForwardAttachmentIDs)
	if err != nil {
		return threadCreated{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ticket_threads (id, workspace_id, ticket_id, subject, to_emails, cc_emails, mailbox_id, created_by, include_history)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, wid, tid, subject, to, cc, *from, actor.UserID, b.IncludeHistory); err != nil {
		return threadCreated{}, err
	}
	th, err := domain.PublishThread(ctx, tx, wid, tid, id, "thread.created")
	if err != nil {
		return threadCreated{}, err
	}
	if _, err := domain.LogActivity(ctx, tx, wid, tid, actor.Type, actor.UserID, "thread_started",
		map[string]any{"thread_id": id.String(), "subject": th.Subject, "to": th.To}); err != nil {
		return threadCreated{}, err
	}
	msg, err := addThreadMessage(ctx, tx, d, p, tid, th, threadMessageBody{BodyHTML: b.BodyHTML, AttachmentIDs: b.AttachmentIDs}, forwarded...)
	if err != nil {
		return threadCreated{}, err
	}
	if th, err = domain.LoadThread(ctx, tx, wid, tid, id); err != nil {
		return threadCreated{}, err
	}
	return threadCreated{Thread: th, Message: msg}, nil
}

// forwardAttachments copies attachments of the ticket's public replies as the caller's
// uploads, to send along in a thread. Files of inbound mail that failed its sender checks
// stay out, as the timeline keeps them. A copy shares the stored file: only replies are
// copied, and their attachments are removed only with the ticket, so the file outlives neither.
func forwardAttachments(ctx context.Context, tx pgx.Tx, p *auth.Principal, tid uuid.UUID, raw []string) ([]uuid.UUID, error) {
	src, err := hd.ParseAttachmentIDs(raw)
	if err != nil || len(src) == 0 {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT a.filename, a.content_type, a.size_bytes, a.storage_key FROM attachments a
		JOIN messages m ON m.id = a.message_id AND m.workspace_id = a.workspace_id
		WHERE a.workspace_id = $1 AND m.ticket_id = $2 AND m.kind = 'reply' AND NOT (m.direction = 'inbound' AND m.auth_failed)
			AND a.id = ANY($3)`, p.WorkspaceID, tid, src)
	if err != nil {
		return nil, err
	}
	type file struct {
		name, ct, key string
		size          int64
	}
	files, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (file, error) {
		var f file
		return f, r.Scan(&f.name, &f.ct, &f.size, &f.key)
	})
	if err != nil {
		return nil, err
	}
	if len(files) != len(src) {
		return nil, httpx.Validation("forward_attachment_ids must be attachments of this ticket's conversation with the customer")
	}
	out := make([]uuid.UUID, 0, len(files))
	for _, f := range files {
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO attachments (id, workspace_id, filename, content_type, size_bytes, storage_key, uploaded_by, uploaded_by_api_key_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, p.WorkspaceID, f.name, f.ct, f.size, f.key, p.ActorUserID(), p.ActorAPIKeyID()); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// addThreadMessage writes the team's message in a thread and queues its email, with the
// attachments forwarded from the customer's conversation, if any.
func addThreadMessage(ctx context.Context, tx pgx.Tx, d *deps.Deps, p *auth.Principal, tid uuid.UUID, th domain.Thread, b threadMessageBody,
	forwarded ...uuid.UUID) (domain.Message, error) {
	wid := p.WorkspaceID
	if th.MailboxID == nil {
		return domain.Message{}, httpx.Validation("this thread's address was removed; start a new thread")
	}
	actor := actorOf(p)
	// A saved reply's tags are filled for the third party: nothing of the customer's.
	ref, err := threadTemplateRef(ctx, tx, wid, tid, actor.UserID, th.MailboxID)
	if err != nil {
		return domain.Message{}, err
	}
	bodyHTML, err := hd.FillReply(ctx, tx, d.Cfg, ref, b.BodyHTML)
	if err != nil {
		return domain.Message{}, err
	}
	bodyText := hd.HTMLToText(bodyHTML)
	attIDs, err := hd.ParseAttachmentIDs(b.AttachmentIDs)
	if err != nil {
		return domain.Message{}, err
	}
	attIDs = append(attIDs, forwarded...)
	if len(attIDs) > 20 {
		return domain.Message{}, httpx.Validation("at most 20 attachments per message")
	}
	if bodyText == "" && len(attIDs) == 0 && !strings.Contains(bodyHTML, "<img") {
		return domain.Message{}, httpx.Validation("the message is empty")
	}
	if err := hd.CheckAttachments(ctx, tx, wid, attIDs, hd.Uploader{User: p.ActorUserID(), APIKey: p.ActorAPIKeyID()}); err != nil {
		return domain.Message{}, err
	}
	// Refused here rather than when sending: the email would fail for good.
	var total int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes), 0) FROM attachments WHERE workspace_id = $1 AND id = ANY($2)`, wid, attIDs).
		Scan(&total); err != nil {
		return domain.Message{}, err
	}
	if total > hd.MaxAttachmentBytes {
		return domain.Message{}, httpx.Validation("attachments are limited to 25 MB per message")
	}
	ok, err := domain.CanSendFrom(ctx, tx, p, *th.MailboxID)
	if err != nil {
		return domain.Message{}, err
	}
	if !ok {
		return domain.Message{}, httpx.E(http.StatusForbidden, "reply_address_forbidden", "You can't send from this thread's address.")
	}
	var fromEmail, fromName string
	if err := tx.QueryRow(ctx, `SELECT email, display_name FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *th.MailboxID).
		Scan(&fromEmail, &fromName); err != nil {
		return domain.Message{}, err
	}
	authorType := actor.Type
	if authorType == "ai" {
		authorType = "user"
	}
	if actor.UserID != nil {
		if u, err := domain.LoadUserRef(ctx, tx, *actor.UserID); err == nil {
			fromName = u.Name
		}
	}
	return domain.AddMessage(ctx, tx, d.Jobs, domain.NewMessage{WorkspaceID: wid, TicketID: tid, ThreadID: &th.ID, Kind: "note",
		Direction: "outbound", AuthorType: authorType, AuthorUserID: actor.UserID, BodyHTML: bodyHTML, BodyText: bodyText,
		Subject: th.Subject, FromEmail: fromEmail, FromName: fromName, To: th.To, Cc: th.Cc, MailboxID: th.MailboxID,
		AttachmentIDs: attIDs, DeliveryStatus: "pending", APIKeyID: p.ActorAPIKeyID()})
}

// threadTemplateRef is what a thread's text is filled with (contracts/templates.md): the
// ticket's brand and team, the address it goes out from and the sender, never the customer's
// or the ticket's details. mailbox is the thread's address; nil takes the ticket's.
func threadTemplateRef(ctx context.Context, q db.Querier, wid, tid uuid.UUID, author, mailbox *uuid.UUID) (templates.Ref, error) {
	var brand uuid.UUID
	var team, ticketMailbox *uuid.UUID
	if err := q.QueryRow(ctx, `SELECT brand_id, team_id, mailbox_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, tid).
		Scan(&brand, &team, &ticketMailbox); err != nil {
		return templates.Ref{}, err
	}
	if mailbox == nil {
		mailbox = ticketMailbox
	}
	return templates.Ref{WorkspaceID: wid, AuthorUserID: author, MailboxID: mailbox, BrandID: &brand, TeamID: team}, nil
}
