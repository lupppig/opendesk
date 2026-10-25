package email

import (
	"context"
	"errors"
	"html"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/mailer"
	"github.com/useopendesk/opendesk/internal/templates"
)

type sendMessageWorker struct {
	river.WorkerDefaults[jobs.SendMessageArgs]
	s *service
}

func (w *sendMessageWorker) Work(ctx context.Context, job *river.Job[jobs.SendMessageArgs]) error {
	if err := w.s.notSuspended(ctx, job.Args.WorkspaceID); err != nil {
		return err
	}
	return w.s.sendMessage(ctx, job.Args.WorkspaceID, job.Args.MessageID, job.Attempt >= job.MaxAttempts)
}

// notSuspended cancels a send for a suspended workspace: nothing goes out while it is,
// and nothing queued meanwhile goes out after it is restored (contracts/admin.md).
func (s *service) notSuspended(ctx context.Context, wid uuid.UUID) error {
	suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return err
	}
	if suspended {
		s.log.Info("send canceled: workspace suspended", "workspace_id", wid)
		return river.JobCancel(errors.New("workspace suspended"))
	}
	return nil
}

type sendTicketEmailWorker struct {
	river.WorkerDefaults[jobs.SendTicketEmailArgs]
	s *service
}

func (w *sendTicketEmailWorker) Work(ctx context.Context, job *river.Job[jobs.SendTicketEmailArgs]) error {
	if err := w.s.notSuspended(ctx, job.Args.WorkspaceID); err != nil {
		return err
	}
	err := w.s.sendTicketEmail(ctx, job.Args)
	var se *sendError
	if errors.As(err, &se) && se.permanent {
		w.s.log.Warn("ticket email not sent", "workspace_id", job.Args.WorkspaceID, "ticket_id", job.Args.TicketID, "reason", se.msg)
		return river.JobCancel(err)
	}
	return err
}

// ollieSender marks a reply written by the AI agent and the bot name it is sent under.
// system marks a reply an automation sent.
type ollieSender struct {
	ai     bool
	name   string
	system bool
}

// ticketInfo is what outbound mail needs from a ticket.
type ticketInfo struct {
	id             uuid.UUID
	number         int64
	subject        string
	mailboxID      *uuid.UUID
	contactEmail   *string
	contactName    string
	contactBlocked bool
	brandID        uuid.UUID
	teamID         *uuid.UUID
	receivedAt     string
	names          senderNames // team and brand; the caller fills agent and mailbox
}

func loadTicketInfo(ctx context.Context, q db.Querier, wid, id uuid.UUID) (*ticketInfo, error) {
	var t ticketInfo
	var blocked *bool
	var contactName *string
	err := q.QueryRow(ctx, `SELECT t.id, t.number, t.subject, t.mailbox_id, c.email, c.blocked, c.name, t.brand_id, t.team_id,
		t.received_at, COALESCE(tm.name, ''), COALESCE(b.name, '')
		FROM tickets t
		LEFT JOIN contacts c ON c.id = t.contact_id AND c.workspace_id = t.workspace_id
		LEFT JOIN teams tm ON tm.id = t.team_id AND tm.workspace_id = t.workspace_id
		LEFT JOIN brands b ON b.id = t.brand_id AND b.workspace_id = t.workspace_id
		WHERE t.workspace_id = $1 AND t.id = $2 AND t.deleted_at IS NULL`, wid, id).
		Scan(&t.id, &t.number, &t.subject, &t.mailboxID, &t.contactEmail, &blocked, &contactName, &t.brandID, &t.teamID,
			&t.receivedAt, &t.names.Team, &t.names.Brand)
	t.contactBlocked = blocked != nil && *blocked
	if contactName != nil {
		t.contactName = displayname.Displayable(*contactName)
	}
	t.names.Team, t.names.Brand = displayname.Displayable(t.names.Team), displayname.Displayable(t.names.Brand)
	return &t, err
}

// sendingMailbox is the address a message goes out from: the message's own, else the
// ticket's reply address, else the brand's address for the ticket's team, else the
// brand's default address, else the workspace's first active mailbox. The last two are
// never another private team's address (contracts/teams.md "Reply address").
func (s *service) sendingMailbox(ctx context.Context, wid uuid.UUID, t *ticketInfo, preferred *uuid.UUID) (*mailboxRow, error) {
	for _, id := range []*uuid.UUID{preferred, t.mailboxID} {
		if id == nil {
			continue
		}
		m, err := loadMailbox(ctx, s.d.DB.Pool, wid, *id)
		if err == nil || !db.IsNotFound(err) {
			return m, err
		}
	}
	if t.brandID != uuid.Nil {
		var id *uuid.UUID
		var err error
		if t.teamID != nil {
			if id, err = domain.TeamReplyMailbox(ctx, s.d.DB.Pool, wid, t.brandID, *t.teamID); err != nil {
				return nil, err
			}
		}
		if id == nil {
			if id, err = domain.BrandMailboxFor(ctx, s.d.DB.Pool, wid, t.brandID, t.teamID); err != nil {
				return nil, err
			}
		}
		if id != nil {
			m, err := loadMailbox(ctx, s.d.DB.Pool, wid, *id)
			if err == nil || !db.IsNotFound(err) {
				return m, err
			}
		}
	}
	m, err := scanMailbox(s.d.DB.Pool.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE workspace_id = $1
		AND (team_id IS NULL OR team_id IS NOT DISTINCT FROM $2
			OR NOT EXISTS (SELECT 1 FROM teams pt WHERE pt.workspace_id = $1 AND pt.id = mailboxes.team_id AND pt.private))
		ORDER BY (status = 'active') DESC, created_at LIMIT 1`, wid, t.teamID))
	if db.IsNotFound(err) {
		return nil, permanent("No mailbox is set up to send email from.")
	}
	return m, err
}

// threadHeaders returns In-Reply-To and References for a reply on the ticket: the
// latest inbound message, or else the latest message we sent.
func threadHeaders(ctx context.Context, q db.Querier, wid, ticketID uuid.UUID, exclude uuid.UUID) (string, []string, error) {
	var id string
	var refs []string
	err := q.QueryRow(ctx, `SELECT email_message_id, "references" FROM messages
		WHERE workspace_id = $1 AND ticket_id = $2 AND id <> $3 AND kind = 'reply' AND email_message_id IS NOT NULL
		ORDER BY (direction = 'inbound') DESC, created_at DESC LIMIT 1`, wid, ticketID, exclude).Scan(&id, &refs)
	if db.IsNotFound(err) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	refs = append(refs, id)
	if len(refs) > 20 {
		refs = append(refs[:1], refs[len(refs)-19:]...)
	}
	return id, refs, nil
}

// addressing picks From, Reply-To and the envelope sender for a mailbox. Customers always
// answer to the mailbox's own address (or the alias they wrote to), never the forwarding
// address: their reply reaches opendesk the way their first message did, and threads by
// In-Reply-To and References. alias is the mailbox alias the customer wrote to, or "":
// mail goes out From the alias where the provider sends as it (sendsAs), else From the
// mailbox (the platform sender: its own address) with the alias as Reply-To.
func (s *service) addressing(m *mailboxRow, name, alias string) (from mail.Address, replyTo *mail.Address, envFrom string) {
	display := m.nameFor(alias)
	if name == "" {
		name = display
	}
	addr := m.Email
	if alias != "" {
		addr = alias
	}
	switch m.OutboundKind {
	case OutboundSystem:
		sys := s.systemFrom()
		return mail.Address{Name: name, Address: sys.Address}, &mail.Address{Name: display, Address: addr}, sys.Address
	}
	if alias != "" && m.sendsAs(alias) {
		return mail.Address{Name: name, Address: addr}, nil, m.Email
	}
	if alias != "" {
		replyTo = &mail.Address{Name: display, Address: alias}
	}
	return mail.Address{Name: name, Address: m.Email}, replyTo, m.Email
}

// deliverAs delivers out, which goes out as alias (or the mailbox when ""), with addressing's
// From and Reply-To. When Microsoft refuses the alias as From, the alias is marked as one it
// does not send as and the message goes out once more From the mailbox with the alias as
// Reply-To, so the reply is not lost. A retry of the job reads the mark and sends that way
// straight away.
func (s *service) deliverAs(ctx context.Context, m *mailboxRow, out *outMessage, envFrom, alias string) (string, error) {
	stamped, err := s.deliver(ctx, m, out, envFrom)
	var se *sendError
	if alias == "" || m.OutboundKind != KindMicrosoft || !strings.EqualFold(out.From.Address, alias) ||
		!errors.As(err, &se) || !se.sendAsDenied {
		return stamped, err
	}
	if err := s.refuseSendAs(ctx, m, alias); err != nil {
		return "", err
	}
	s.log.Info("alias not sent as: provider refused it, sent from the mailbox address", "workspace_id", m.WorkspaceID, "mailbox_id", m.ID)
	from, replyTo, envFrom := s.addressing(m, out.From.Name, alias)
	out.From, out.ReplyTo = from, replyTo
	return s.deliver(ctx, m, out, envFrom)
}

// refuseSendAs records that the provider refused alias as From, and publishes the change.
func (s *service) refuseSendAs(ctx context.Context, m *mailboxRow, alias string) error {
	alias = strings.ToLower(alias)
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE mailboxes SET alias_settings = jsonb_set(alias_settings, ARRAY[$3::text],
			COALESCE(alias_settings -> $3::text, '{}'::jsonb) || jsonb_build_object('send_as', $4::text, 'send_as_by', $5::text)),
			updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND $3::text = ANY(aliases)
			AND coalesce(alias_settings -> $3::text ->> 'send_as', '') <> $4::text`,
			m.WorkspaceID, m.ID, alias, SendAsUnverified, SendAsByProvider)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return s.publishMailbox(ctx, tx, m.WorkspaceID, m.ID, "mailbox.updated")
	})
	if err != nil {
		return err
	}
	st := m.AliasSettings[alias]
	st.SendAs, st.SendAsBy = SendAsUnverified, SendAsByProvider
	if m.AliasSettings == nil {
		m.AliasSettings = map[string]AliasSettings{}
	}
	m.AliasSettings[alias] = st
	return nil
}

// sentAs is what a delivered message actually went out as.
type sentAs struct {
	mailboxID uuid.UUID
	fromEmail string
	fromName  string
	// ticketMailbox is set when the ticket had no reply address and takes this one.
	ticketMailbox bool
	// to is who it went to.
	to []string
	// provider is where it lives at Gmail, when the mailbox sends through its API.
	provider *providerRef
	// html is the email as it went out: the layout, the reply, the signature and the
	// quoted conversation, kept so the team can open it as the customer got it.
	html string
}

func messageIDFor(prefix string, id uuid.UUID, number int64, m *mailboxRow) string {
	domainPart := emailDomain(m.Email)
	if domainPart == "" {
		domainPart = "opendesk.local"
	}
	return "<" + prefix + "_" + id.String() + ".t" + itoa(number) + "@" + domainPart + ">"
}

// deliver encodes and sends out through the mailbox's outbound kind. It returns the
// Message-ID the recipient sees.
func (s *service) deliver(ctx context.Context, m *mailboxRow, out *outMessage, envFrom string) (string, error) {
	rcpts := dedupe(append(append(append([]string{}, out.To...), out.Cc...), out.Bcc...))
	if len(rcpts) == 0 {
		return "", permanent("The message has no recipient.")
	}
	// Reserved .invalid addresses (test workspaces' sample people) can never receive mail:
	// they are left out, and a message to nobody else counts as sent without being handed
	// to any provider.
	out.To, out.Cc, out.Bcc = deliverable(out.To), deliverable(out.Cc), deliverable(out.Bcc)
	if rcpts = deliverable(rcpts); len(rcpts) == 0 {
		s.log.Info("reply to reserved test addresses only, not sent", "mailbox_id", m.ID)
		return out.MessageID, nil
	}
	switch m.OutboundKind {
	case OutboundSMTP:
		raw, err := buildMIME(out, false)
		if err != nil {
			return "", err
		}
		creds, err := s.openCreds(m)
		if err != nil {
			return "", permanent(err.Error())
		}
		ep := smtpEndpoint{host: m.Config.SMTPHost, port: m.Config.SMTPPort, mode: m.Config.SMTPTLS, user: m.Config.SMTPUsername,
			pass: creds.smtpPassword(), guarded: true, helo: s.heloName(), allowNoTLS: s.guard.allowPrivate, authRequired: true}
		return out.MessageID, s.sendSMTP(ctx, ep, &envelope{from: envFrom, rcpts: rcpts, raw: raw})
	case KindGoogle:
		raw, err := buildMIME(out, true)
		if err != nil {
			return "", err
		}
		stamped, sent, err := s.sendGmail(ctx, m, raw, out.GmailThread)
		out.sent = sent
		if stamped == "" {
			stamped = out.MessageID
		}
		return stamped, err
	case KindMicrosoft:
		raw, err := buildMIME(out, true)
		if err != nil {
			return "", err
		}
		return out.MessageID, s.sendGraph(ctx, m, raw)
	case OutboundSystem:
		if !s.systemSend {
			return "", permanent(domain.ErrCannotSend.Detail)
		}
		raw, err := buildMIME(out, false)
		if err != nil {
			return "", err
		}
		return out.MessageID, s.sendSystem(ctx, &envelope{from: envFrom, rcpts: rcpts, raw: raw})
	}
	return "", permanent("The mailbox has no way to send email.")
}

// deliverable drops reserved .invalid addresses, which no mail server accepts.
func deliverable(list []string) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		if !mailer.Undeliverable(a) {
			out = append(out, a)
		}
	}
	return out
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range list {
		a = strings.ToLower(strings.TrimSpace(a))
		if validAddress(a) && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// sendMessage delivers one outbound reply and records the result on the message.
func (s *service) sendMessage(ctx context.Context, wid, mid uuid.UUID, lastAttempt bool) error {
	var ticketID uuid.UUID
	var kind, direction, bodyHTML, bodyText, subject, status, authorType string
	var to, cc, bcc []string
	var authorID, mailboxID, threadID *uuid.UUID
	var bot ollieSender
	var deleted bool
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT ticket_id, kind, direction, body_html, body_text, subject, to_emails, cc_emails, bcc_emails,
		delivery_status, author_user_id, ai, from_name, mailbox_id, author_type, thread_id, deleted_at IS NOT NULL FROM messages WHERE workspace_id = $1 AND id = $2`, wid, mid).
		Scan(&ticketID, &kind, &direction, &bodyHTML, &bodyText, &subject, &to, &cc, &bcc, &status, &authorID, &bot.ai, &bot.name, &mailboxID, &authorType, &threadID, &deleted)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	thread := threadID != nil && kind == "note"
	if status != "pending" || direction != "outbound" || kind != "reply" && !thread {
		return nil
	}
	// A reply deleted before it went out is not sent (deleting marks it so; this covers a
	// row deleted otherwise).
	if deleted {
		return s.recordDelivery(ctx, wid, mid, ticketID, "failed", domain.DeletedBeforeSent, "", nil)
	}
	bot.system = authorType == "system"
	var stamped string
	var sent *sentAs
	if thread {
		stamped, sent, err = s.composeAndSendThread(ctx, wid, mid, ticketID, *threadID, bodyHTML, bodyText, to, cc, authorID)
	} else {
		stamped, sent, err = s.composeAndSend(ctx, wid, mid, ticketID, bodyHTML, bodyText, subject, to, cc, bcc, authorID, bot, mailboxID)
	}
	if err == nil {
		return s.recordDelivery(ctx, wid, mid, ticketID, "sent", "", stamped, sent)
	}
	if errors.Is(err, errNotSendable) {
		return nil
	}
	var se *sendError
	isSend := errors.As(err, &se)
	if isSend && se.permanent || lastAttempt {
		msg := "Sending failed."
		if isSend {
			msg = se.msg
		}
		s.log.Warn("outbound message failed", "workspace_id", wid, "message_id", mid, "reason", msg)
		if rerr := s.recordDelivery(ctx, wid, mid, ticketID, "failed", msg, "", nil); rerr != nil {
			return rerr
		}
		if lastAttempt {
			return nil
		}
		return river.JobCancel(err)
	}
	return err
}

func (s *service) composeAndSend(ctx context.Context, wid, mid, ticketID uuid.UUID, bodyHTML, bodyText, subject string,
	to, cc, bcc []string, authorID *uuid.UUID, bot ollieSender, mailboxID *uuid.UUID) (string, *sentAs, error) {
	t, err := loadTicketInfo(ctx, s.d.DB.Pool, wid, ticketID)
	if db.IsNotFound(err) {
		return "", nil, permanent("The ticket no longer exists.")
	}
	if err != nil {
		return "", nil, err
	}
	m, err := s.sendingMailbox(ctx, wid, t, mailboxID)
	if err != nil {
		return "", nil, err
	}
	if m.Status == "disabled" {
		return "", nil, permanent("The mailbox is disabled.")
	}
	if len(dedupe(to)) == 0 {
		if t.contactEmail == nil || *t.contactEmail == "" {
			return "", nil, permanent("The customer has no email address.")
		}
		to = []string{*t.contactEmail}
	}
	if to, err = replyDestination(ctx, s.d.DB.Pool, wid, ticketID, t.contactEmail, to); err != nil {
		return "", nil, err
	}
	alias := m.alias(t.receivedAt)
	names := t.names
	names.Mailbox = m.nameFor(alias)
	if authorID != nil {
		if u, err := domain.LoadUserRef(ctx, s.d.DB.Pool, *authorID); err == nil {
			names.Agent = agentName(u.Name)
		}
	} else if bot.ai {
		names.Agent = agentName(bot.name)
	}
	from, replyTo, envFrom := s.addressing(m, fromName(m.FromNameFormat, names), alias)
	inReplyTo, refs, err := threadHeaders(ctx, s.d.DB.Pool, wid, ticketID, mid)
	if err != nil {
		return "", nil, err
	}
	// From a mailbox that reads and sends through Gmail or Microsoft, the reply joins the
	// customer's conversation there.
	thread, threadSubject, err := s.providerThread(ctx, wid, ticketID, mid, m)
	if err != nil {
		return "", nil, err
	}
	msgID := messageIDFor("m", mid, t.number, m)
	// Record our id first so a fast reply threads even before the send returns.
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE messages SET email_message_id = $3 WHERE workspace_id = $1 AND id = $2 AND email_message_id IS NULL`,
		wid, mid, msgID); err != nil && !db.IsUniqueViolation(err, "") {
		return "", nil, err
	}
	atts, html, err := s.outboundAttachments(ctx, wid, mid, bodyHTML)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(bodyText) == "" {
		bodyText = htmlToText(bodyHTML)
	}
	if strings.TrimSpace(subject) == "" || !subjectTagRe.MatchString(subject) {
		if same := providerReplySubject(threadSubject); thread != nil && same != "" {
			subject = same
		} else {
			subject = replySubject(firstNonEmpty(subject, t.subject), t.number, inReplyTo != "")
		}
	}
	ref := templates.Ref{WorkspaceID: wid, TicketID: &ticketID, AuthorUserID: authorID, MailboxID: &m.ID, MailboxEmail: firstNonEmpty(alias, m.Email)}
	if authorID == nil && bot.ai {
		ref.BotName = bot.name
	}
	mail, err := s.renderEmail(ctx, ref, templates.Parts{Content: html, ContentText: bodyText}, &mid)
	if err != nil {
		return "", nil, err
	}
	out := &outMessage{From: from, ReplyTo: replyTo, To: to, Cc: cc, Bcc: bcc, Subject: subject, MessageID: msgID,
		InReplyTo: inReplyTo, References: refs, HTML: mail.HTML,
		Text: mail.Text, Attachments: atts, Loop: s.loopToken(m.ID),
		// The agent's and automations' replies are automatic: autoresponders must not
		// answer them (RFC 3834).
		AutoGenerated: authorID == nil && (bot.ai || bot.system)}
	if thread != nil && thread.Provider == KindGoogle {
		out.GmailThread = thread.Thread
	}
	// Deleted while it was being composed: it is not sent. Checked as late as possible; a
	// delete after this point finds it being handed over, and it is recorded as sent.
	if ok, err := stillSendable(ctx, s.d.DB.Pool, wid, mid); err != nil {
		return "", nil, err
	} else if !ok {
		return "", nil, errNotSendable
	}
	stamped, err := s.deliverAs(ctx, m, out, envFrom, alias)
	if err != nil {
		return stamped, nil, err
	}
	return stamped, &sentAs{mailboxID: m.ID, fromEmail: strings.ToLower(out.From.Address), fromName: out.From.Name, ticketMailbox: t.mailboxID == nil,
		to: dedupe(to), provider: out.sent, html: mail.HTML}, nil
}

// composeAndSendThread emails a message of a third-party thread (contracts/api.md
// "Threads") to the thread's recipients, from the thread's address. It threads with the
// thread's own messages only. Only a first message started with include_history quotes the
// conversation with the customer.
func (s *service) composeAndSendThread(ctx context.Context, wid, mid, ticketID, threadID uuid.UUID, bodyHTML, bodyText string,
	to, cc []string, authorID *uuid.UUID) (string, *sentAs, error) {
	t, err := loadTicketInfo(ctx, s.d.DB.Pool, wid, ticketID)
	if db.IsNotFound(err) {
		return "", nil, permanent("The ticket no longer exists.")
	}
	if err != nil {
		return "", nil, err
	}
	var subject string
	var mailboxID *uuid.UUID
	var withHistory bool
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT th.subject, th.mailbox_id, COALESCE(th.include_history AND
			(SELECT e.id FROM messages e WHERE e.workspace_id = th.workspace_id AND e.thread_id = th.id ORDER BY e.created_at, e.id LIMIT 1) = $3, false)
		FROM ticket_threads th WHERE th.workspace_id = $1 AND th.id = $2`, wid, threadID, mid).
		Scan(&subject, &mailboxID, &withHistory)
	if db.IsNotFound(err) {
		return "", nil, permanent("The thread no longer exists.")
	}
	if err != nil {
		return "", nil, err
	}
	m, err := s.sendingMailbox(ctx, wid, t, mailboxID)
	if err != nil {
		return "", nil, err
	}
	if m.Status == "disabled" {
		return "", nil, permanent("The mailbox is disabled.")
	}
	if len(dedupe(to)) == 0 {
		return "", nil, permanent("The thread has no recipient.")
	}
	names := t.names
	names.Mailbox = m.displayName()
	if authorID != nil {
		if u, err := domain.LoadUserRef(ctx, s.d.DB.Pool, *authorID); err == nil {
			names.Agent = agentName(u.Name)
		}
	}
	from, replyTo, envFrom := s.addressing(m, fromName(m.FromNameFormat, names), "")
	inReplyTo, refs, err := threadMessageHeaders(ctx, s.d.DB.Pool, wid, threadID, mid)
	if err != nil {
		return "", nil, err
	}
	msgID := messageIDFor("m", mid, t.number, m)
	// Record our id first so a fast reply threads even before the send returns.
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE messages SET email_message_id = $3 WHERE workspace_id = $1 AND id = $2 AND email_message_id IS NULL`,
		wid, mid, msgID); err != nil && !db.IsUniqueViolation(err, "") {
		return "", nil, err
	}
	atts, html, err := s.outboundAttachments(ctx, wid, mid, bodyHTML)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(bodyText) == "" {
		bodyText = htmlToText(bodyHTML)
	}
	if inReplyTo != "" && !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	// Filled without the ticket: its brand, team, address and the sender, never the customer's
	// or the ticket's details, which a signature, footer or layout could otherwise carry.
	ref := templates.Ref{WorkspaceID: wid, AuthorUserID: authorID, MailboxID: &m.ID, MailboxEmail: m.Email, BrandID: &t.brandID, TeamID: t.teamID}
	tc, err := templates.Load(ctx, s.d.DB.Pool, s.d.Cfg, ref)
	if err != nil {
		return "", nil, err
	}
	design, err := templates.LoadDesign(ctx, s.d.DB.Pool, wid, tc.BrandID)
	if err != nil {
		return "", nil, err
	}
	// The conversation with the customer goes to the third party only when asked for.
	parts := templates.Parts{Content: html, ContentText: bodyText, Signature: tc.Signature(), Forward: withHistory}
	if withHistory {
		if parts.History, err = loadHistory(ctx, s.d.DB.Pool, wid, ticketID, &mid, 10); err != nil {
			return "", nil, err
		}
	}
	mail := tc.RenderEmail(design, parts)
	out := &outMessage{From: from, ReplyTo: replyTo, To: to, Cc: cc, Subject: subject, MessageID: msgID,
		InReplyTo: inReplyTo, References: refs, HTML: mail.HTML, Text: mail.Text, Attachments: atts, Loop: s.loopToken(m.ID)}
	stamped, err := s.deliver(ctx, m, out, envFrom)
	if err != nil {
		return stamped, nil, err
	}
	return stamped, &sentAs{mailboxID: m.ID, fromEmail: strings.ToLower(from.Address), fromName: from.Name, to: dedupe(to), html: mail.HTML}, nil
}

// threadMessageHeaders returns In-Reply-To and References for a message of a third-party
// thread: the thread's latest reply from the other side, or else its latest message we sent.
func threadMessageHeaders(ctx context.Context, q db.Querier, wid, threadID uuid.UUID, exclude uuid.UUID) (string, []string, error) {
	var id string
	var refs []string
	err := q.QueryRow(ctx, `SELECT email_message_id, "references" FROM messages
		WHERE workspace_id = $1 AND thread_id = $2 AND id <> $3 AND email_message_id IS NOT NULL
		ORDER BY (direction = 'inbound') DESC, created_at DESC LIMIT 1`, wid, threadID, exclude).Scan(&id, &refs)
	if db.IsNotFound(err) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	refs = append(refs, id)
	if len(refs) > 20 {
		refs = append(refs[:1], refs[len(refs)-19:]...)
	}
	return id, refs, nil
}

// replyDestination sends a reply addressed to the customer to the Reply-To of their latest
// message instead, when it is at the same domain as the From they wrote from (a shared or
// system address answering for a person of the same organization). Any other Reply-To
// stays on their message for agents to see, and replies go to the From address.
func replyDestination(ctx context.Context, q db.Querier, wid, ticketID uuid.UUID, contactEmail *string, to []string) ([]string, error) {
	if contactEmail == nil || *contactEmail == "" {
		return to, nil
	}
	var from, replyTo string
	err := q.QueryRow(ctx, `SELECT from_email, reply_to FROM messages
		WHERE workspace_id = $1 AND ticket_id = $2 AND direction = 'inbound' AND kind = 'reply' AND NOT auth_failed
		ORDER BY created_at DESC LIMIT 1`, wid, ticketID).Scan(&from, &replyTo)
	if db.IsNotFound(err) {
		return to, nil
	}
	if err != nil {
		return nil, err
	}
	if replyTo == "" || !strings.EqualFold(from, *contactEmail) || !validAddress(replyTo) ||
		!strings.EqualFold(emailDomain(replyTo), emailDomain(from)) {
		return to, nil
	}
	out := make([]string, 0, len(to))
	for _, a := range to {
		if strings.EqualFold(strings.TrimSpace(a), *contactEmail) {
			a = replyTo
		}
		out = append(out, a)
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// outboundAttachments loads the message's files. Inline images the HTML references by
// their attachment URL are sent as cid: parts so they keep working in the mail client.
func (s *service) outboundAttachments(ctx context.Context, wid, mid uuid.UUID, bodyHTML string) ([]outAttachment, string, error) {
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT id, filename, content_type, storage_key, size_bytes, inline FROM attachments
		WHERE workspace_id = $1 AND message_id = $2 ORDER BY created_at`, wid, mid)
	if err != nil {
		return nil, "", err
	}
	type row struct {
		id       uuid.UUID
		filename string
		ct, key  string
		size     int64
		inline   bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.id, &x.filename, &x.ct, &x.key, &x.size, &x.inline)
	})
	if err != nil {
		return nil, "", err
	}
	if len(list) == 0 {
		return nil, bodyHTML, nil
	}
	var total int64
	for _, a := range list {
		total += a.size
	}
	if total > maxOutboundBytes {
		return nil, "", permanent("Attachments are limited to 25 MB per message.")
	}
	doc := parseFragment(bodyHTML)
	referenced := map[uuid.UUID]string{}
	if doc != nil {
		var walk func(*xhtml.Node)
		walk = func(n *xhtml.Node) {
			if n.Type == xhtml.ElementNode && n.DataAtom == atom.Img {
				for i, a := range n.Attr {
					if !strings.EqualFold(a.Key, "src") {
						continue
					}
					for _, att := range list {
						if att.inline && strings.Contains(a.Val, "/v1/attachments/"+att.id.String()) {
							cid := att.id.String() + "@opendesk"
							n.Attr[i].Val = "cid:" + cid
							referenced[att.id] = cid
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if len(referenced) > 0 {
			bodyHTML = render(doc)
		}
	}
	out := make([]outAttachment, 0, len(list))
	for _, a := range list {
		data, err := s.readObject(ctx, a.key, maxAttachmentBytes)
		if err != nil {
			return nil, "", err
		}
		out = append(out, outAttachment{Filename: a.filename, ContentType: a.ct, Data: data, ContentID: referenced[a.id]})
	}
	return out, bodyHTML, nil
}

// sentCID is the content id an inline image of ours goes out as (outboundAttachments).
var sentCID = regexp.MustCompile(`(?i)cid:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})@opendesk`)

// keptCopy is an email as it went out, for the team to open: its inline images point at
// their stored attachments again, and it is sanitized like every stored email.
func keptCopy(sent string) string {
	if strings.TrimSpace(sent) == "" {
		return ""
	}
	doc := parseFragment(sent)
	if doc == nil {
		return ""
	}
	urls := map[string]string{}
	for _, m := range sentCID.FindAllStringSubmatch(sent, -1) {
		if id, ok := ids.Parse(m[1]); ok {
			urls[strings.ToLower(m[1])+"@opendesk"] = domain.AttachmentURL(id)
		}
	}
	rewriteCID(doc, urls)
	return sanitizeHTML(render(doc))
}

// errNotSendable: the message was deleted (or otherwise settled) before it was handed over.
var errNotSendable = errors.New("message no longer sendable")

// stillSendable reports whether a reply is still waiting to go out and not deleted.
func stillSendable(ctx context.Context, q db.Querier, wid, mid uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT delivery_status = 'pending' AND deleted_at IS NULL FROM messages WHERE workspace_id = $1 AND id = $2`, wid, mid).Scan(&ok)
	if db.IsNotFound(err) {
		return false, nil
	}
	return ok, err
}

// recordDelivery stores the outcome and publishes message.updated.
func (s *service) recordDelivery(ctx context.Context, wid, mid, ticketID uuid.UUID, status, detail, stamped string, sent *sentAs) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if status == "sent" {
			if _, err := tx.Exec(ctx, `SAVEPOINT stamp`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE messages SET email_message_id = $3 WHERE workspace_id = $1 AND id = $2 AND $3 <> ''`, wid, mid, stamped)
			if err != nil {
				if !db.IsUniqueViolation(err, "") {
					return err
				}
				if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT stamp`); err != nil {
					return err
				}
			}
		}
		// A reply deleted while it was being handed over went out all the same: say so.
		tag, err := tx.Exec(ctx, `UPDATE messages SET delivery_status = $3, delivery_error = $4,
			sent_at = CASE WHEN $3 = 'sent' THEN now() ELSE sent_at END
			WHERE workspace_id = $1 AND id = $2 AND (delivery_status = 'pending'
				OR $3 = 'sent' AND delivery_status = 'failed' AND delivery_error = $5)`, wid, mid, status, truncate(detail, 500), domain.DeletedBeforeSent)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if sent != nil {
			// The message records the address and name it actually went out as (a reply from a
			// member's own address has no mailbox).
			var mailbox *uuid.UUID
			if sent.mailboxID != uuid.Nil {
				mailbox = &sent.mailboxID
			}
			if _, err := tx.Exec(ctx, `UPDATE messages SET mailbox_id = COALESCE($3, mailbox_id), from_email = $4, from_name = $5,
				to_emails = CASE WHEN cardinality($6::text[]) > 0 THEN $6 ELSE to_emails END WHERE workspace_id = $1 AND id = $2`,
				wid, mid, mailbox, sent.fromEmail, truncate(sent.fromName, 200), sent.to); err != nil {
				return err
			}
			if sent.provider != nil {
				if _, err := tx.Exec(ctx, `UPDATE messages SET provider_ref = $3 WHERE workspace_id = $1 AND id = $2`, wid, mid, sent.provider); err != nil {
					return err
				}
			}
			// The email as it went out; one too large to keep whole is not kept.
			if kept := keptCopy(sent.html); kept != "" && len(kept) <= maxBodyHTML {
				if _, err := tx.Exec(ctx, `UPDATE messages SET full_html = $3 WHERE workspace_id = $1 AND id = $2`, wid, mid, kept); err != nil {
					return err
				}
			}
			if sent.ticketMailbox {
				// A ticket without a reply address takes the one it first sent from.
				tag, err := tx.Exec(ctx, `UPDATE tickets SET mailbox_id = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2 AND mailbox_id IS NULL`,
					wid, ticketID, sent.mailboxID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() > 0 {
					if err := domain.PublishTicketUpdated(ctx, tx, wid, ticketID, []string{"mailbox_id"}); err != nil {
						return err
					}
				}
			}
		}
		msg, err := domain.LoadMessage(ctx, tx, wid, mid)
		if err != nil {
			return err
		}
		_, err = events.Publish(ctx, tx, domain.MessageUpdatedEvent(wid, msg))
		return err
	})
}

// sendTicketEmail sends an auto-reply or CSAT email from the ticket's reply address. It
// has no author: the From name reads the address, team or brand, and the signature is the
// team's or the address's.
func (s *service) sendTicketEmail(ctx context.Context, a jobs.SendTicketEmailArgs) error {
	t, err := loadTicketInfo(ctx, s.d.DB.Pool, a.WorkspaceID, a.TicketID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.contactEmail == nil || !validAddress(strings.ToLower(*t.contactEmail)) || t.contactBlocked {
		return nil
	}
	m, err := s.sendingMailbox(ctx, a.WorkspaceID, t, nil)
	if err != nil {
		return err
	}
	if m.Status == "disabled" || !s.canSend(m) {
		return nil
	}
	alias := m.alias(t.receivedAt)
	names := t.names
	names.Mailbox = m.nameFor(alias)
	from, replyTo, envFrom := s.addressing(m, fromName(m.FromNameFormat, names), alias)
	inReplyTo, refs, err := threadHeaders(ctx, s.d.DB.Pool, a.WorkspaceID, t.id, uuid.Nil)
	if err != nil {
		return err
	}
	thread, _, err := s.providerThread(ctx, a.WorkspaceID, t.id, uuid.Nil, m)
	if err != nil {
		return err
	}
	ref := templates.Ref{WorkspaceID: a.WorkspaceID, TicketID: &t.id, MailboxID: &m.ID, MailboxEmail: firstNonEmpty(alias, m.Email)}
	tc, err := templates.Load(ctx, s.d.DB.Pool, s.d.Cfg, ref)
	if err != nil {
		return err
	}
	design, err := templates.LoadDesign(ctx, s.d.DB.Pool, a.WorkspaceID, tc.BrandID)
	if err != nil {
		return err
	}
	body, text, subject := a.HTML, a.Text, a.Subject
	if a.Template {
		// Filled now, so the email reads the ticket as it is when it goes out.
		text = ""
		if a.SurveyURL != "" {
			accent := design.Layout.Accent
			if accent == "" {
				accent = tc.BrandColor
			}
			buttons, links := templates.SurveyButtons(a.SurveyURL, accent)
			tc.Values.HTML["survey"] = "<p>" + strings.ReplaceAll(html.EscapeString(strings.TrimSpace(links)), "\n", "<br>") + "</p>"
			text = templates.TextOf(tc.Fill(body))
			tc.Values.HTML["survey"] = buttons
		}
		body = tc.Fill(body)
		subject = tc.FillText(subject)
		if strings.TrimSpace(subject) != "" {
			if _, ok := subjectTicketNumber(subject); !ok {
				subject += " [#" + itoa(t.number) + "]"
			}
		}
	}
	if strings.TrimSpace(subject) == "" {
		subject = replySubject(t.subject, t.number, inReplyTo != "")
	}
	if strings.TrimSpace(text) == "" {
		text = htmlToText(body)
	}
	mail := tc.RenderEmail(design, templates.Parts{Content: body, ContentText: text, Signature: tc.Signature()})
	out := &outMessage{From: from, ReplyTo: replyTo, To: []string{strings.ToLower(*t.contactEmail)}, Subject: subject,
		MessageID: s.autoMessageID(a.WorkspaceID, t.number, m), InReplyTo: inReplyTo, References: refs,
		HTML: mail.HTML, Text: mail.Text, AutoGenerated: a.AutoGenerated, Date: time.Now(), Loop: s.loopToken(m.ID)}
	if thread != nil && thread.Provider == KindGoogle {
		out.GmailThread = thread.Thread
	}
	_, err = s.deliverAs(ctx, m, out, envFrom, alias)
	return err
}
