package tickets

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/collab"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/templates"
)

// MessageInput is a reply or note written through any trusted agent surface: the
// REST API, a support-channel adapter, or a future native client. PostMessage owns
// the shared validation and delivery behavior; callers must first authorize the
// principal for the ticket with domain.LoadVisibleTicket.
type MessageInput struct {
	Kind          string            `json:"kind" enum:"reply,note"`
	BodyHTML      string            `json:"body_html" maxLength:"500000"`
	Cc            []string          `json:"cc,omitempty" maxItems:"20"`
	Bcc           []string          `json:"bcc,omitempty" maxItems:"20"`
	AttachmentIDs []string          `json:"attachment_ids,omitempty" maxItems:"20"`
	Status        *string           `json:"status,omitempty" enum:"open,pending,on_hold,solved,closed" doc:"Applied after the message is added"`
	MailboxID     string            `json:"mailbox_id,omitempty" doc:"Replies: send from this address of the ticket's brand (the caller must be allowed to send from it); it becomes the ticket's reply address"`
	FromNoteID    string            `json:"from_note_id,omitempty" doc:"Replies: an internal note of yours, without files, this reply is written from. The note stays as a marker pointing at the reply; only admins read what it said"`
	FromAccount   *FromAccountInput `json:"from_account,omitempty" doc:"Email replies: send from one of your own Collab addresses instead of the ticket's reply address, which stays as it is"`
}

// FromAccountInput is the Collab address a reply goes out from (contracts/collab.md
// "Answering tickets").
type FromAccountInput struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email,omitempty" maxLength:"254" doc:"One of the account's addresses that can send; left out: the one you last answered this ticket from in that account, else its default"`
	ReplyTo   string `json:"reply_to,omitempty" maxLength:"254" doc:"address (the address's own reply-to), mailbox (the ticket's reply address) or an email address; left out: the workspace's collab_ticket_reply_to"`
}

const activitySelect = `SELECT a.id, a.ticket_id, a.actor_type, a.type, a.data, a.created_at, u.id, u.name, u.email, u.avatar_url
FROM activities a LEFT JOIN users u ON u.id = a.actor_user_id`

func scanActivity(row pgx.Row) (domain.Activity, error) {
	var a domain.Activity
	var uID *uuid.UUID
	var uName, uEmail, uAvatar *string
	if err := row.Scan(&a.ID, &a.TicketID, &a.Actor.Type, &a.Type, &a.Data, &a.CreatedAt, &uID, &uName, &uEmail, &uAvatar); err != nil {
		return a, err
	}
	if uID != nil {
		a.Actor.User = &domain.UserRef{ID: *uID, Name: str(uName), Email: str(uEmail), AvatarURL: domain.Avatar(str(uAvatar))}
	}
	if a.Data == nil {
		a.Data = map[string]any{}
	}
	return a, nil
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func registerMessages(d *deps.Deps, api huma.API) {
	tags := []string{"Tickets"}
	registerMessageMutations(d, api, tags)

	huma.Register(api, huma.Operation{OperationID: "list-ticket-messages", Method: http.MethodGet, Path: "/v1/tickets/{id}/messages",
		Summary: "List a ticket's messages", Description: "Oldest first, notes included.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct {
			Body struct {
				Data []domain.Message `json:"data"`
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
			// Deleted messages stay as markers; only admins read what they said.
			msgs, err := domain.ListTimeline(ctx, d.DB.Pool, p.WorkspaceID, id, p.Role.AtLeast(auth.RoleAdmin))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []domain.Message `json:"data"`
				}
			}{}
			out.Body.Data = msgs
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-ticket-message", Method: http.MethodPost, Path: "/v1/tickets/{id}/messages",
		Summary: "Reply or add a note", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body MessageInput
		}) (*struct{ Body domain.Message }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var msg domain.Message
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				id, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				msg, err = PostMessage(ctx, tx, d, p, id, in.Body)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body domain.Message }{msg}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-ticket-activities", Method: http.MethodGet, Path: "/v1/tickets/{id}/activities",
		Summary: "List a ticket's activities", Description: "Oldest first.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct {
			Body struct {
				Data []domain.Activity `json:"data"`
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
			rows, err := d.DB.Pool.Query(ctx, activitySelect+` WHERE a.workspace_id = $1 AND a.ticket_id = $2 ORDER BY a.created_at, a.id`,
				p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Activity, error) { return scanActivity(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []domain.Activity `json:"data"`
				}
			}{}
			out.Body.Data = list
			if out.Body.Data == nil {
				out.Body.Data = []domain.Activity{}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-message-original", Method: http.MethodGet, Path: "/v1/messages/{id}/original",
		Summary: "Get a message with its quoted history", Tags: tags,
		Description: "full_html is the stored (sanitized) original including quoted history; the body when there is none."},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct {
			Body struct {
				FullHTML string `json:"full_html"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Message")
			if err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					FullHTML string `json:"full_html"`
				}
			}{}
			var tid uuid.UUID
			err = d.DB.Pool.QueryRow(ctx, `SELECT CASE WHEN m.full_html <> '' THEN m.full_html ELSE m.body_html END, t.id
				FROM messages m JOIN tickets t ON t.id = m.ticket_id AND t.deleted_at IS NULL
				WHERE m.workspace_id = $1 AND m.id = $2 AND (m.deleted_at IS NULL OR $3)`, p.WorkspaceID, id, p.Role.AtLeast(auth.RoleAdmin)).Scan(&out.Body.FullHTML, &tid)
			if err == nil {
				if _, err = resolveVisible(ctx, d.DB.Pool, p, tid.String()); err != nil {
					return nil, httpx.NotFound("Message")
				}
			}
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Message")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return out, nil
		})
}

// PostMessage adds an agent or API reply or note and applies an optional status.
func PostMessage(ctx context.Context, tx pgx.Tx, d *deps.Deps, p *auth.Principal, tid uuid.UUID, b MessageInput) (domain.Message, error) {
	wid := p.WorkspaceID
	actor := actorOf(p)
	var channel, subject string
	var mailboxEmail, mailboxName, contactEmail *string
	var mailboxID, team *uuid.UUID
	var brand uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT t.channel, t.subject, t.mailbox_id, t.brand_id, t.team_id, mb.email, mb.display_name, c.email FROM tickets t
		LEFT JOIN mailboxes mb ON mb.id = t.mailbox_id LEFT JOIN contacts c ON c.id = t.contact_id
		WHERE t.workspace_id = $1 AND t.id = $2`, wid, tid).Scan(&channel, &subject, &mailboxID, &brand, &team, &mailboxEmail, &mailboxName, &contactEmail); err != nil {
		return domain.Message{}, err
	}
	// A reply from the member's own Collab address leaves the reply address as it is.
	var personal *collab.TicketFrom
	if b.FromAccount != nil {
		switch {
		case b.Kind != "reply":
			return domain.Message{}, httpx.Validation("from_account applies to replies")
		case strings.TrimSpace(b.MailboxID) != "":
			return domain.Message{}, httpx.Validation("send from mailbox_id or from_account, not both")
		case channel != domain.ChannelEmail:
			return domain.Message{}, httpx.Validation("from_account applies to email tickets")
		}
		accountID, err := hd.ParseRef(b.FromAccount.AccountID, "from_account.account_id")
		if err != nil {
			return domain.Message{}, err
		}
		from, err := collab.TicketSender(ctx, d, tx, p, tid, accountID, b.FromAccount.Email, b.FromAccount.ReplyTo)
		if err != nil {
			return domain.Message{}, err
		}
		personal = &from
	}
	// The reply address: the one asked for (it becomes the ticket's), else the ticket's,
	// else, for an email reply on a ticket without one, the brand's address of its team.
	var reply *uuid.UUID
	switch {
	case personal != nil:
	case strings.TrimSpace(b.MailboxID) != "":
		if b.Kind != "reply" {
			return domain.Message{}, httpx.Validation("mailbox_id applies to replies")
		}
		id, err := hd.ParseRef(b.MailboxID, "mailbox_id")
		if err != nil {
			return domain.Message{}, err
		}
		reply = &id
	case b.Kind == "reply" && channel == domain.ChannelEmail && mailboxID == nil:
		m, err := replyMailbox(ctx, tx, wid, brand, team)
		if err != nil {
			return domain.Message{}, err
		}
		if m == nil {
			return domain.Message{}, httpx.Validation("this ticket's brand has no address to reply from")
		}
		reply = m
	}
	// The address a reply actually goes out from must be one the caller may send from: a
	// private team's address is for that team's members (contracts/teams.md "Reply address"),
	// whether the reply names it or inherits it from the ticket.
	if b.Kind == "reply" && personal == nil {
		from := mailboxID
		if reply != nil {
			from = reply
		}
		if from != nil && (channel == domain.ChannelEmail || reply != nil) {
			ok, err := domain.CanSendFrom(ctx, tx, p, *from)
			if err != nil {
				return domain.Message{}, err
			}
			if !ok {
				return domain.Message{}, httpx.E(403, "reply_address_forbidden",
					"You can't send from this ticket's reply address. Pick another address to reply from.")
			}
		}
	}
	if reply != nil && !sameID(reply, mailboxID) {
		patch := Patch{Mailbox: reply}
		if strings.TrimSpace(b.MailboxID) != "" {
			if err := patch.checkCaller(ctx, tx, p, tid); err != nil {
				return domain.Message{}, err
			}
		}
		if _, err := applyPatch(ctx, tx, d, wid, tid, actor, patch, applyOpts{}); err != nil {
			return domain.Message{}, err
		}
		mailboxID = reply
		if err := tx.QueryRow(ctx, `SELECT email, display_name FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, *reply).
			Scan(&mailboxEmail, &mailboxName); err != nil {
			return domain.Message{}, err
		}
	}
	// An API key may speak as a bot: the bot is then the agent its reply is filled and sent as.
	bot, err := keyBot(ctx, tx, p)
	if err != nil {
		return domain.Message{}, err
	}
	bodyHTML := hd.SanitizeRich(b.BodyHTML)
	if b.Kind == "reply" {
		ref := templates.Ref{WorkspaceID: wid, TicketID: &tid, AuthorUserID: actor.UserID, MailboxID: mailboxID}
		if bot != nil {
			ref.BotName = bot.Name
		}
		var err error
		if bodyHTML, err = hd.FillReply(ctx, tx, d.Cfg, ref, b.BodyHTML); err != nil {
			return domain.Message{}, err
		}
	}
	bodyText := hd.HTMLToText(bodyHTML)
	attIDs, err := hd.ParseAttachmentIDs(b.AttachmentIDs)
	if err != nil {
		return domain.Message{}, err
	}
	if bodyText == "" && len(attIDs) == 0 && !strings.Contains(bodyHTML, "<img") {
		return domain.Message{}, httpx.Validation("the message is empty")
	}
	if err := hd.CheckAttachments(ctx, tx, wid, attIDs, hd.Uploader{User: p.ActorUserID(), APIKey: p.ActorAPIKeyID()}); err != nil {
		return domain.Message{}, err
	}
	fromNote, err := noteForReply(ctx, tx, p, tid, b)
	if err != nil {
		return domain.Message{}, err
	}
	authorType := actor.Type
	if authorType == "ai" {
		authorType = "user" // the assistant writes on the user's behalf, after their approval
	}
	nm := domain.NewMessage{WorkspaceID: wid, TicketID: tid, Kind: b.Kind, Direction: "outbound", AuthorType: authorType,
		AuthorUserID: actor.UserID, BodyHTML: bodyHTML, BodyText: bodyText, Subject: replySubject(subject), AttachmentIDs: attIDs}
	if b.Kind == "reply" && channel == domain.ChannelEmail {
		if contactEmail == nil || *contactEmail == "" {
			return domain.Message{}, httpx.Validation("this ticket's contact has no email address to reply to")
		}
		cc, err := cleanEmails(b.Cc)
		if err != nil {
			return domain.Message{}, err
		}
		bcc, err := cleanEmails(b.Bcc)
		if err != nil {
			return domain.Message{}, err
		}
		nm.To, nm.Cc, nm.Bcc = []string{*contactEmail}, cc, bcc
		nm.FromEmail, nm.FromName = str(mailboxEmail), str(mailboxName)
		nm.MailboxID = mailboxID
		if personal != nil {
			nm.FromEmail, nm.FromName, nm.MailboxID = personal.Email, personal.Name, nil
			nm.CollabAccountID, nm.CollabReplyTo = &personal.AccountID, personal.ReplyTo
		}
		nm.DeliveryStatus = "pending"
		nm.BodyHTML, nm.BodyText = withSignOff(nm.BodyHTML, nm.BodyText, bot)
	} else if len(b.Cc)+len(b.Bcc) > 0 {
		return domain.Message{}, httpx.Validation("cc and bcc apply only to email replies")
	}
	asKeyBot(&nm, p, bot)
	if actor.UserID != nil && nm.FromName == "" {
		if u, err := domain.LoadUserRef(ctx, tx, *actor.UserID); err == nil {
			nm.FromName = u.Name
		}
	}
	msg, err := domain.AddMessage(ctx, tx, d.Jobs, nm)
	if err != nil {
		return domain.Message{}, err
	}
	if fromNote != nil {
		// The reply carries the note's words now: the note stays where it was as a marker
		// pointing at the reply. What it said is kept, hidden like a deleted message's (only
		// admins read it), so words edited out before sending are not lost; its mentions go.
		if _, err := tx.Exec(ctx, `UPDATE messages SET sent_as_reply_id = $4, deleted_at = now(), deleted_by = $5
			WHERE workspace_id = $1 AND ticket_id = $2 AND id = $3`, wid, tid, *fromNote, msg.ID, p.UserID); err != nil {
			return domain.Message{}, err
		}
		if err := hideMessage(ctx, tx, d, wid, tid, *fromNote); err != nil {
			return domain.Message{}, err
		}
		if _, err := domain.LogActivity(ctx, tx, wid, tid, actor.Type, actor.UserID, "note_sent_as_reply",
			map[string]any{"message_id": *fromNote, "reply_id": msg.ID}); err != nil {
			return domain.Message{}, err
		}
		if err := domain.PublishMessageUpdated(ctx, tx, wid, *fromNote); err != nil {
			return domain.Message{}, err
		}
	}
	if b.Kind == "note" {
		if err := notifyMentions(ctx, tx, wid, tid, msg, actor.UserID); err != nil {
			return domain.Message{}, err
		}
	}
	if b.Status != nil {
		if _, err := applyPatch(ctx, tx, d, wid, tid, actor, Patch{Status: b.Status}, applyOpts{Enqueue: true, RequireFields: true}); err != nil {
			return domain.Message{}, err
		}
	}
	return msg, nil
}

// noteForReply checks the note a reply is written from (from_note_id): a plain internal
// note on this ticket, by the caller, without files. Nil when there is none.
func noteForReply(ctx context.Context, tx pgx.Tx, p *auth.Principal, tid uuid.UUID, b MessageInput) (*uuid.UUID, error) {
	if strings.TrimSpace(b.FromNoteID) == "" {
		return nil, nil
	}
	if b.Kind != "reply" {
		return nil, httpx.Validation("from_note_id applies to replies")
	}
	id, err := hd.ParseRef(b.FromNoteID, "from_note_id")
	if err != nil {
		return nil, err
	}
	target, err := lockMessageMutationTarget(ctx, tx, p.WorkspaceID, tid, id)
	if err != nil {
		return nil, err
	}
	if target.kind != "note" || target.threadID != nil || target.deleted || target.sentAsReply {
		return nil, httpx.Validation("from_note_id must be an internal note on this ticket")
	}
	if p.Kind != auth.KindUser || target.authorType != "user" || target.authorID == nil || *target.authorID != p.UserID {
		return nil, httpx.Forbidden()
	}
	var files bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attachments WHERE workspace_id = $1 AND message_id = $2)`, p.WorkspaceID, id).Scan(&files); err != nil {
		return nil, err
	}
	if files {
		return nil, httpx.Validation("a note with files can't become a reply")
	}
	return &id, nil
}

func replySubject(s string) string {
	if s == "" || strings.HasPrefix(strings.ToLower(s), "re:") {
		return s
	}
	return "Re: " + s
}

var mentionAttr = regexp.MustCompile(`data-(?:mention|id)="([0-9a-fA-F-]{36})"`)

type member struct {
	ID    uuid.UUID
	Name  string
	Email string
}

// mentionedUsers finds members mentioned in a note: a data-id (or data-mention) user id in the HTML,
// or "@" followed by a member's email or full name in the text.
func mentionedUsers(html, text string, members []member, author *uuid.UUID) []uuid.UUID {
	byID := map[uuid.UUID]bool{}
	for _, m := range members {
		byID[m.ID] = true
	}
	var out []uuid.UUID
	add := func(id uuid.UUID) {
		if (author != nil && *author == id) || !byID[id] {
			return
		}
		for _, x := range out {
			if x == id {
				return
			}
		}
		out = append(out, id)
	}
	for _, m := range mentionAttr.FindAllStringSubmatch(html, -1) {
		if id, err := uuid.Parse(m[1]); err == nil {
			add(id)
		}
	}
	lower := strings.ToLower(text)
	for _, m := range members {
		for _, handle := range []string{m.Email, m.Name} {
			handle = strings.ToLower(strings.TrimSpace(handle))
			if handle == "" {
				continue
			}
			if containsMention(lower, "@"+handle) {
				add(m.ID)
				break
			}
		}
	}
	return out
}

// containsMention reports needle in s followed by a non-word character or the end.
func containsMention(s, needle string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], needle)
		if j < 0 {
			return false
		}
		end := i + j + len(needle)
		start := i + j
		before, _ := utf8.DecodeLastRuneInString(s[:start])
		after, _ := utf8.DecodeRuneInString(s[end:])
		okBefore := start == 0 || !(unicode.IsLetter(before) || unicode.IsDigit(before))
		okAfter := end == len(s) || !(unicode.IsLetter(after) || unicode.IsDigit(after) || after == '@')
		if okBefore && okAfter {
			return true
		}
		i = start + 1
	}
}

func notifyMentions(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID, msg domain.Message, author *uuid.UUID) error {
	return notifyNewMentions(ctx, tx, wid, tid, msg, author, "", "")
}

// notifyNewMentions notifies the members msg mentions that its earlier body (prevHTML, prevText) did not.
func notifyNewMentions(ctx context.Context, tx pgx.Tx, wid, tid uuid.UUID, msg domain.Message, author *uuid.UUID, prevHTML, prevText string) error {
	if !strings.Contains(msg.BodyText, "@") && !strings.Contains(msg.BodyHTML, "data-mention") && !strings.Contains(msg.BodyHTML, "data-id") {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT u.id, u.name, u.email::text FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1`, wid)
	if err != nil {
		return err
	}
	members, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (member, error) {
		var m member
		err := r.Scan(&m.ID, &m.Name, &m.Email)
		return m, err
	})
	if err != nil {
		return err
	}
	had := map[uuid.UUID]bool{}
	for _, uid := range mentionedUsers(prevHTML, prevText, members, author) {
		had[uid] = true
	}
	for _, uid := range mentionedUsers(msg.BodyHTML, msg.BodyText, members, author) {
		if had[uid] {
			continue
		}
		data := map[string]any{"message_id": msg.ID.String(), "by": idOrNil(author), "preview": hd.Excerpt(msg.BodyText, 140)}
		if err := hd.Notify(ctx, tx, wid, uid, hd.NotifyMentioned, &tid, data); err != nil {
			return err
		}
	}
	return nil
}
