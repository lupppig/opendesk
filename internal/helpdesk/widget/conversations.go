package widget

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/routing"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
)

const maxBodyRunes = 10000

// Conversation is a chat ticket as the visitor sees it.
type WidgetConversation struct {
	ID                 uuid.UUID `json:"id"`
	Subject            string    `json:"subject"`
	Status             string    `json:"status" enum:"open,pending,on_hold,solved,closed"`
	LastMessagePreview string    `json:"last_message_preview"`
	LastMessageAt      time.Time `json:"last_message_at"`
	AIHandoff          bool      `json:"ai_handoff" doc:"The conversation was handed from the AI agent to the team"`
}

type widgetMessageBody struct {
	Body          string   `json:"body,omitempty" required:"false" maxLength:"40000" doc:"Plain text, up to 10000 characters; may be empty when files are attached"`
	AttachmentIDs []string `json:"attachment_ids,omitempty" maxItems:"20"`
	PageURL       string   `json:"page_url,omitempty" maxLength:"2000" doc:"The host page (https), given to the AI agent"`
}

// cleanPageURL keeps an https page URL of at most 500 characters, without its fragment;
// anything else is dropped (it is context for the AI agent, never required).
func cleanPageURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	u.Fragment = ""
	if s := u.String(); utf8.RuneCountInString(s) <= 500 {
		return s
	}
	return ""
}

type widgetCreateInput struct {
	Origin string `header:"Origin"`
	Body   struct {
		Body          string   `json:"body,omitempty" required:"false" maxLength:"40000" doc:"Plain text, up to 10000 characters; may be empty when files are attached"`
		AttachmentIDs []string `json:"attachment_ids,omitempty" maxItems:"20"`
		PageURL       string   `json:"page_url,omitempty" maxLength:"2000" doc:"The host page (https): stored on the ticket, used by routing rules and given to the AI agent"`
		TopicID       string   `json:"topic_id,omitempty" maxLength:"40" doc:"The topic the visitor picked (WidgetConfig.topics)"`
	}
}

// cleanBody trims a visitor's text; it may be empty only when files come with it.
func cleanBody(s string, files int) (string, error) {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if s == "" && files == 0 {
		return "", httpx.Validation("body is required")
	}
	if utf8.RuneCountInString(s) > maxBodyRunes {
		return "", httpx.Validation("messages can be at most 10000 characters")
	}
	return s, nil
}

const conversationCols = `SELECT id, subject, status, last_message_preview, last_message_at,
	COALESCE(custom->>'ai_handoff' = 'true', false) FROM tickets`

// sameBrand limits a visitor's conversations to the brand of the widget their session
// started in, bound as $n.
func sameBrand(n int) string {
	return fmt.Sprintf(` AND ($%[1]d::uuid IS NULL OR brand_id = (SELECT g.brand_id FROM widgets g WHERE g.id = $%[1]d))`, n)
}

// sessionWidget is the visitor's widget id for sameBrand, nil for a session without one.
func sessionWidget(p *auth.Principal) *uuid.UUID {
	if p.WidgetID == uuid.Nil {
		return nil
	}
	id := p.WidgetID
	return &id
}

// loadConversation returns the visitor's own chat ticket, or not found.
func loadConversation(ctx context.Context, q db.Querier, p *auth.Principal, rawID string) (WidgetConversation, error) {
	var c WidgetConversation
	id, ok := parseUUID(rawID)
	if !ok {
		return c, httpx.NotFound("Conversation")
	}
	err := q.QueryRow(ctx, conversationCols+` WHERE workspace_id = $1 AND id = $2 AND contact_id = $3 AND channel = 'chat'
		AND deleted_at IS NULL`+sameBrand(4), p.WorkspaceID, id, p.ContactID, sessionWidget(p)).
		Scan(&c.ID, &c.Subject, &c.Status, &c.LastMessagePreview, &c.LastMessageAt, &c.AIHandoff)
	if db.IsNotFound(err) {
		return c, httpx.NotFound("Conversation")
	}
	return c, err
}

// seen records that the visitor is on the page now: an agent's reply posted before this
// is not emailed to them (domain.ChatFollowUpDue).
func (h *handler) seen(ctx context.Context, p *auth.Principal) error {
	_, err := h.d.DB.Pool.Exec(ctx, `UPDATE widget_sessions SET last_seen_at = now() WHERE id = $1 AND workspace_id = $2`, p.VisitorID, p.WorkspaceID)
	return err
}

func (h *handler) registerConversations(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "widget-list-conversations", Method: http.MethodGet, Path: "/v1/widget/conversations",
		Summary: "List the visitor's conversations", Tags: []string{"Widget"}},
		func(ctx context.Context, in *struct {
			Origin string `header:"Origin"`
		}) (*struct {
			Body struct {
				Data []WidgetConversation `json:"data"`
			}
		}, error) {
			p, err := h.visitor(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			rows, err := h.d.DB.Pool.Query(ctx, conversationCols+` WHERE workspace_id = $1 AND contact_id = $2 AND channel = 'chat'
				AND deleted_at IS NULL`+sameBrand(3)+` ORDER BY last_message_at DESC, id DESC LIMIT 100`, p.WorkspaceID, p.ContactID, sessionWidget(p))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WidgetConversation, error) {
				var c WidgetConversation
				err := r.Scan(&c.ID, &c.Subject, &c.Status, &c.LastMessagePreview, &c.LastMessageAt, &c.AIHandoff)
				return c, err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []WidgetConversation `json:"data"`
				}
			}{}
			out.Body.Data = list
			if out.Body.Data == nil {
				out.Body.Data = []WidgetConversation{}
			}
			return out, nil
		})

	type created struct {
		Conversation WidgetConversation `json:"conversation"`
		Message      domain.Message     `json:"message"`
	}
	huma.Register(api, huma.Operation{OperationID: "widget-create-conversation", Method: http.MethodPost, Path: "/v1/widget/conversations",
		Summary: "Start a conversation", Tags: []string{"Widget"}, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *widgetCreateInput) (*struct{ Body created }, error) {
			p, g, err := h.visitorWidget(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.allowWrite(p, "write", visitorWrites); err != nil {
				return nil, err
			}
			attIDs, err := hd.ParseAttachmentIDs(in.Body.AttachmentIDs)
			if err != nil {
				return nil, err
			}
			body, err := cleanBody(in.Body.Body, len(attIDs))
			if err != nil {
				return nil, err
			}
			if len(attIDs) > 0 && !takesUploads(g.Settings) {
				return nil, errAttachmentsOff
			}
			if err := hd.CheckAttachmentFiles(ctx, h.d.Storage, h.d.DB.Pool, p.WorkspaceID, attIDs, visitorAccept(g.Settings)); err != nil {
				return nil, httpx.Wrap(err)
			}
			pageURL := cleanPageURL(in.Body.PageURL)
			out := &struct{ Body created }{}
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := hd.CheckAttachments(ctx, tx, p.WorkspaceID, attIDs, hd.Uploader{Contact: &p.ContactID}); err != nil {
					return err
				}
				g, err := loadVisitorWidget(ctx, tx, p)
				if err != nil {
					return err
				}
				topic := strings.TrimSpace(in.Body.TopicID)
				if topic != "" && !slices.ContainsFunc(g.Settings.Topics, func(t hd.WidgetTopic) bool { return t.ID == topic }) {
					return httpx.Validation("topic_id is not a topic of this widget")
				}
				contact, err := domain.LoadContact(ctx, tx, p.WorkspaceID, p.ContactID)
				if err != nil {
					return err
				}
				fromEmail := ""
				if contact.Email != nil {
					fromEmail = *contact.Email
				}
				subject := hd.Excerpt(body, 80)
				if subject == "" {
					// Only files: the first one names the conversation.
					if err := tx.QueryRow(ctx, `SELECT filename FROM attachments WHERE workspace_id = $1 AND id = $2`,
						p.WorkspaceID, attIDs[0]).Scan(&subject); err != nil {
						return err
					}
					subject = hd.Excerpt(subject, 80)
				}
				widgetID := g.ID
				res, err := routing.Resolve(ctx, tx, routing.Input{WorkspaceID: p.WorkspaceID, Channel: domain.ChannelChat,
					BrandID: g.BrandID, WidgetID: &widgetID, Topic: topic, FromEmail: fromEmail, Subject: subject, Body: body,
					PageURL: pageURL, ContactID: &p.ContactID})
				if err != nil {
					return err
				}
				brandID := g.BrandID
				t, err := domain.CreateTicket(ctx, tx, domain.NewTicket{WorkspaceID: p.WorkspaceID, Subject: subject,
					Channel: domain.ChannelChat, ContactID: &p.ContactID, TeamID: res.TeamID, Routed: res.Routed,
					BrandID: &brandID, WidgetID: &widgetID, PageURL: pageURL})
				if err != nil {
					return err
				}
				if rule := res.RuleID(); rule != nil {
					if err := routing.Count(ctx, tx, *rule); err != nil {
						return err
					}
				}
				m, err := domain.AddMessage(ctx, tx, h.d.Jobs, h.visitorMessage(p, t.ID, body, attIDs, jobs.TriggerTicketCreated))
				if err != nil {
					return err
				}
				if err := hd.EnqueueAIWidgetAnswer(ctx, tx, h.d.Jobs, h.d.Cfg, p.WorkspaceID, t.ID, m.ID, pageURL); err != nil {
					return err
				}
				out.Body.Message = publicMessage(m)
				out.Body.Conversation, err = loadConversation(ctx, tx, p, t.ID.String())
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return out, nil
		})

	type messagesOut struct {
		Data []domain.Message `json:"data"`
	}
	huma.Register(api, huma.Operation{OperationID: "widget-list-messages", Method: http.MethodGet, Path: "/v1/widget/conversations/{id}/messages",
		Summary: "List a conversation's messages", Description: "Public replies only; internal notes are never returned.", Tags: []string{"Widget"}},
		func(ctx context.Context, in *struct {
			ID     string `path:"id"`
			Origin string `header:"Origin"`
		}) (*struct{ Body messagesOut }, error) {
			p, err := h.visitor(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			c, err := loadConversation(ctx, h.d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			msgs, err := domain.ListMessages(ctx, h.d.DB.Pool, p.WorkspaceID, c.ID, true)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.seen(ctx, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			for i := range msgs {
				msgs[i] = publicMessage(msgs[i])
			}
			return &struct{ Body messagesOut }{messagesOut{msgs}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "widget-seen", Method: http.MethodPost, Path: "/v1/widget/conversations/{id}/seen",
		Summary: "Mark a conversation seen", Tags: []string{"Widget"}, DefaultStatus: http.StatusNoContent,
		Description: "The widget calls this while a conversation is on screen and a reply arrives: a visitor who is there does not get the reply by email (email_follow_up)."},
		func(ctx context.Context, in *struct {
			ID     string `path:"id"`
			Origin string `header:"Origin"`
		}) (*hd.Empty, error) {
			p, err := h.visitor(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.allowWrite(p, "seen", visitorWrites); err != nil {
				return nil, err
			}
			if _, err := loadConversation(ctx, h.d.DB.Pool, p, in.ID); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.seen(ctx, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "widget-typing", Method: http.MethodPost, Path: "/v1/widget/conversations/{id}/typing",
		Summary: "Show the team the visitor is writing", Tags: []string{"Widget"}, DefaultStatus: http.StatusNoContent,
		Description: "The widget calls this at most every few seconds while the visitor types. Agents on the ticket who may see it " +
			"get \"customer_typing\" on the realtime gateway."},
		func(ctx context.Context, in *struct {
			ID     string `path:"id"`
			Origin string `header:"Origin"`
		}) (*hd.Empty, error) {
			p, err := h.visitor(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.allowWrite(p, "typing", visitorWrites); err != nil {
				return nil, err
			}
			c, err := loadConversation(ctx, h.d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if h.d.NATS == nil {
				return nil, nil
			}
			// The ticket's private team keeps it from agents outside the team.
			ev := events.LiveEvent{WorkspaceID: p.WorkspaceID, TicketID: c.ID, ContactID: p.ContactID, Type: "customer.typing", Data: map[string]any{}}
			if err := h.d.DB.Pool.QueryRow(ctx, `SELECT t.brand_id, tm.id FROM tickets t
				LEFT JOIN teams tm ON tm.id = t.team_id AND tm.private
				WHERE t.workspace_id = $1 AND t.id = $2`, p.WorkspaceID, c.ID).Scan(&ev.BrandID, &ev.TeamID); err != nil {
				return nil, httpx.Wrap(err)
			}
			events.PublishLive(h.d.NATS.Conn, ev)
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "widget-post-message", Method: http.MethodPost, Path: "/v1/widget/conversations/{id}/messages",
		Summary: "Send a message in a conversation", Tags: []string{"Widget"}, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			ID     string `path:"id"`
			Origin string `header:"Origin"`
			Body   widgetMessageBody
		}) (*struct{ Body domain.Message }, error) {
			p, g, err := h.visitorWidget(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.allowWrite(p, "write", visitorWrites); err != nil {
				return nil, err
			}
			attIDs, err := hd.ParseAttachmentIDs(in.Body.AttachmentIDs)
			if err != nil {
				return nil, err
			}
			body, err := cleanBody(in.Body.Body, len(attIDs))
			if err != nil {
				return nil, err
			}
			if len(attIDs) > 0 && !takesUploads(g.Settings) {
				return nil, errAttachmentsOff
			}
			if err := hd.CheckAttachmentFiles(ctx, h.d.Storage, h.d.DB.Pool, p.WorkspaceID, attIDs, visitorAccept(g.Settings)); err != nil {
				return nil, httpx.Wrap(err)
			}
			var msg domain.Message
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				c, err := loadConversation(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				if c.Status == domain.StatusClosed {
					return httpx.E(http.StatusConflict, "conversation_closed", "This conversation is closed. Start a new one.")
				}
				if err := hd.CheckAttachments(ctx, tx, p.WorkspaceID, attIDs, hd.Uploader{Contact: &p.ContactID}); err != nil {
					return err
				}
				if err := hd.ExpireAIActions(ctx, tx, p.WorkspaceID, c.ID); err != nil {
					return err
				}
				m, err := domain.AddMessage(ctx, tx, h.d.Jobs, h.visitorMessage(p, c.ID, body, attIDs, ""))
				if err != nil {
					return err
				}
				msg = publicMessage(m)
				return hd.EnqueueAIWidgetAnswer(ctx, tx, h.d.Jobs, h.d.Cfg, p.WorkspaceID, c.ID, m.ID, cleanPageURL(in.Body.PageURL))
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Message }{msg}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "widget-handoff", Method: http.MethodPost, Path: "/v1/widget/conversations/{id}/handoff",
		Summary: "Ask for a person", Description: "Hands the conversation from the AI answer bot to the team. Idempotent.",
		Tags: []string{"Widget"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID     string `path:"id"`
			Origin string `header:"Origin"`
		}) (*hd.Empty, error) {
			p, err := h.visitor(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.allowWrite(p, "write", visitorWrites); err != nil {
				return nil, err
			}
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				c, err := loadConversation(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				_, err = hd.MarkAIHandoff(ctx, tx, p.WorkspaceID, c.ID, "contact", hd.HandoffBot{})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

func (h *handler) registerAgentRoutes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "widget-action-decision", Method: http.MethodPost,
		Path: "/v1/widget/conversations/{id}/actions/{action_id}", Summary: "Confirm or decline an action",
		Description: "Answers an action the AI agent asked the visitor to confirm. Runs at most once.",
		Tags:        []string{"Widget"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID       string `path:"id"`
			ActionID string `path:"action_id"`
			Origin   string `header:"Origin"`
			Body     struct {
				Decision string `json:"decision" enum:"confirm,decline"`
			}
		}) (*hd.Empty, error) {
			p, err := h.visitor(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.allowWrite(p, "write", visitorWrites); err != nil {
				return nil, err
			}
			actionID, err := uuid.Parse(in.ActionID)
			if err != nil {
				return nil, httpx.NotFound("Action")
			}
			confirm := in.Body.Decision == "confirm"
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				c, err := loadConversation(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				status := "declined"
				if confirm {
					status = "running"
				}
				ok, err := hd.SetAIActionStatus(ctx, tx, p.WorkspaceID, c.ID, actionID, status, "pending")
				if err != nil {
					return err
				}
				if !ok {
					var exists bool
					if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_widget_actions WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3)`,
						p.WorkspaceID, c.ID, actionID).Scan(&exists); err != nil {
						return err
					}
					if !exists {
						return httpx.NotFound("Action")
					}
					return httpx.E(http.StatusConflict, "action_not_pending", "This action was already answered or has expired.")
				}
				_, err = h.d.Jobs.InsertTx(ctx, tx, jobs.AIWidgetActionArgs{WorkspaceID: p.WorkspaceID, TicketID: c.ID,
					ActionID: actionID, Confirmed: confirm}, &river.InsertOpts{MaxAttempts: 1})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "widget-rate-message", Method: http.MethodPost,
		Path: "/v1/widget/conversations/{id}/messages/{message_id}/rating", Summary: "Rate an AI agent reply",
		Tags: []string{"Widget"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID        string `path:"id"`
			MessageID string `path:"message_id"`
			Origin    string `header:"Origin"`
			Body      struct {
				Rating *string `json:"rating" enum:"up,down" required:"false" nullable:"true"`
			}
		}) (*hd.Empty, error) {
			p, err := h.visitor(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := h.allowWrite(p, "write", visitorWrites); err != nil {
				return nil, err
			}
			mid, err := uuid.Parse(in.MessageID)
			if err != nil {
				return nil, httpx.NotFound("Message")
			}
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				c, err := loadConversation(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				tag, err := tx.Exec(ctx, `UPDATE messages SET ai_rating = $4 WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3
					AND ai AND kind = 'reply' AND ai_rating IS DISTINCT FROM $4`, p.WorkspaceID, c.ID, mid, in.Body.Rating)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					var exists bool
					if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3 AND ai AND kind = 'reply')`,
						p.WorkspaceID, c.ID, mid).Scan(&exists); err != nil {
						return err
					}
					if !exists {
						return httpx.NotFound("Message")
					}
					return nil // unchanged
				}
				return domain.PublishMessageUpdated(ctx, tx, p.WorkspaceID, mid)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

func (h *handler) visitorMessage(p *auth.Principal, ticketID uuid.UUID, body string, attIDs []uuid.UUID, trigger string) domain.NewMessage {
	return domain.NewMessage{WorkspaceID: p.WorkspaceID, TicketID: ticketID, Kind: "reply", Direction: "inbound",
		AuthorType: "contact", AuthorContactID: &p.ContactID, BodyHTML: hd.TextToHTML(body), BodyText: body,
		AttachmentIDs: attIDs, Trigger: trigger}
}
