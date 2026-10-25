package collab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// Answering tickets from a member's own address (contracts/collab.md "Answering tickets"):
// the reply is a ticket message sent through the account, and mail the account receives
// in answer to it is taken into the ticket.

// Reply-To choices of a ticket reply sent from an account.
const (
	ReplyToAddress = "address" // the address's own reply-to, else none
	ReplyToMailbox = "mailbox" // the ticket's reply address
)

// ErrTicketRepliesOff answers a reply from an account while the workspace has it off.
var ErrTicketRepliesOff = httpx.E(http.StatusConflict, "collab_ticket_replies_off",
	"Replying from your own mail accounts is turned off in this workspace.")

// TicketFrom is the address a ticket reply goes out from.
type TicketFrom struct {
	AccountID uuid.UUID
	Email     string
	// Name is the From name filled for the member, as the reply shows until it is sent.
	Name    string
	ReplyTo string
}

// ticketRules decide replies from members' own addresses on one ticket: the workspace's
// collab_ticket_replies (default on) and collab_ticket_reply_to (default "address"), each
// overridden by the ticket's reply address (its personal_replies), which may also limit
// which addresses ("Answering tickets").
type ticketRules struct {
	on       bool
	replyTo  string
	accounts string
	// mailbox names the ticket's reply address in refusals ("" without one); domains are
	// its own and its aliases' domains.
	mailbox string
	domains []string
}

func loadTicketRules(ctx context.Context, q db.Querier, wid, ticketID uuid.UUID) (ticketRules, error) {
	var on *bool
	var replyTo, address, name *string
	var aliases []string
	var own []byte
	err := q.QueryRow(ctx, `SELECT (w.settings->>'collab_ticket_replies')::boolean, w.settings->>'collab_ticket_reply_to',
		m.email, COALESCE(NULLIF(m.display_name, ''), m.name), m.aliases, m.personal_replies
		FROM workspaces w LEFT JOIN tickets t ON t.workspace_id = w.id AND t.id = $2
		LEFT JOIN mailboxes m ON m.workspace_id = w.id AND m.id = t.mailbox_id
		WHERE w.id = $1`, wid, ticketID).Scan(&on, &replyTo, &address, &name, &aliases, &own)
	if err != nil {
		return ticketRules{}, err
	}
	r := ticketRules{on: on == nil || *on, replyTo: ReplyToAddress, accounts: email.PersonalAny}
	if replyTo != nil && *replyTo == ReplyToMailbox {
		r.replyTo = ReplyToMailbox
	}
	if address == nil {
		return r, nil
	}
	var pr email.PersonalReplies
	if len(own) > 0 {
		if err := json.Unmarshal(own, &pr); err != nil {
			return ticketRules{}, err
		}
	}
	pr = pr.Normalized()
	if pr.Enabled != nil {
		r.on = *pr.Enabled
	}
	if pr.ReplyTo != nil {
		r.replyTo = *pr.ReplyTo
	}
	r.accounts = pr.Accounts
	if name != nil {
		r.mailbox = *name
	}
	for _, a := range append([]string{*address}, aliases...) {
		if d := domainOf(a); d != "" && !slices.Contains(r.domains, d) {
			r.domains = append(r.domains, d)
		}
	}
	return r, nil
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return strings.ToLower(addr[i+1:])
	}
	return ""
}

// off is the refusal while the rules have it off.
func (r ticketRules) off() *httpx.Error {
	if r.mailbox == "" {
		return ErrTicketRepliesOff
	}
	return httpx.E(http.StatusConflict, "collab_ticket_replies_off", "Replying from your own mail accounts is turned off for "+r.mailbox+".")
}

// refuses says why the account's address addr may not answer the ticket, "" when it may.
func (r ticketRules) refuses(a *accountRow, addr string) string {
	switch r.accounts {
	case email.PersonalShared:
		if a.Visibility != VisibilityShared {
			return r.mailbox + " takes replies from shared accounts only."
		}
	case email.PersonalDomain:
		if !slices.Contains(r.domains, domainOf(addr)) {
			return r.mailbox + " takes replies only from addresses at " + strings.Join(r.domains, ", ") + "."
		}
	}
	return ""
}

// TicketSender checks that p may send a reply on ticket ticketID from the account's address
// addr ("" = the one p last answered the ticket from in that account, else its default) with
// replyTo ("" = the workspace's choice), and returns who it goes out as.
func TicketSender(ctx context.Context, d *deps.Deps, q db.Querier, p *auth.Principal, ticketID, accountID uuid.UUID, addr, replyTo string) (TicketFrom, error) {
	if p.Kind != auth.KindUser || p.Assistant || p.ViaAssistant || p.GrantID != uuid.Nil {
		return TicketFrom{}, auth.ErrForbidden
	}
	if !p.Role.AtLeast(auth.RoleAgent) {
		return TicketFrom{}, auth.ErrForbidden
	}
	if err := d.Plan.RequireCollab(ctx, q, p.WorkspaceID); err != nil {
		return TicketFrom{}, err
	}
	rules, err := loadTicketRules(ctx, q, p.WorkspaceID, ticketID)
	if err != nil {
		return TicketFrom{}, err
	}
	if !rules.on {
		return TicketFrom{}, rules.off()
	}
	a, err := visibleAccount(ctx, q, p, accountID)
	if err != nil {
		return TicketFrom{}, notFound(err, "Account")
	}
	if a.Reconnect {
		return TicketFrom{}, httpx.Validation("Connect " + a.Email + " again to send from it.")
	}
	idents, err := accountIdentities(ctx, q, a)
	if err != nil {
		return TicketFrom{}, err
	}
	var from sender
	if strings.TrimSpace(addr) != "" {
		var ok bool
		if from, ok = usableSender(a, idents, addr); !ok {
			return TicketFrom{}, httpx.Validation("Choose one of the account's addresses to send from.")
		}
	} else {
		var last string
		err := q.QueryRow(ctx, `SELECT from_email FROM messages WHERE workspace_id = $1 AND ticket_id = $2 AND author_user_id = $3
			AND collab_account_id = $4 AND kind = 'reply' ORDER BY created_at DESC LIMIT 1`, p.WorkspaceID, ticketID, p.UserID, a.ID).Scan(&last)
		if err != nil && !db.IsNotFound(err) {
			return TicketFrom{}, err
		}
		var ok bool
		if from, ok = usableSender(a, idents, last); !ok || rules.refuses(a, from.Email) != "" {
			from = allowedSender(a, idents, rules)
		}
	}
	if why := rules.refuses(a, from.Email); why != "" {
		return TicketFrom{}, httpx.Validation(why)
	}
	switch v := strings.TrimSpace(replyTo); {
	case v == "":
		replyTo = rules.replyTo
	case v == ReplyToAddress, v == ReplyToMailbox:
		replyTo = v
	default:
		if replyTo = cleanAddress(v); replyTo == "" {
			return TicketFrom{}, httpx.Validation("Check the Reply-To address.")
		}
	}
	s := &service{d: d}
	name, err := s.fillerFor(q, p.WorkspaceID, p.UserID).fromName(ctx, a, from.Name)
	if err != nil {
		return TicketFrom{}, err
	}
	return TicketFrom{AccountID: a.ID, Email: from.Email, Name: name, ReplyTo: replyTo}, nil
}

// allowedSender is the account's address a reply starts from: its default when the rules
// allow it, else the first of its usable addresses they allow, else the default (refused).
func allowedSender(a *accountRow, idents []*identityRow, rules ticketRules) sender {
	def := defaultSender(a, idents)
	if rules.refuses(a, def.Email) == "" {
		return def
	}
	for _, e := range ownAddresses(a, idents) {
		if s, ok := usableSender(a, idents, e); ok && rules.refuses(a, s.Email) == "" {
			return s
		}
	}
	return def
}

// sendTicketReplyWorker sends a ticket reply from its author's account (collab.send_ticket_reply).
type sendTicketReplyWorker struct {
	river.WorkerDefaults[jobs.CollabSendTicketReplyArgs]
	s *service
}

func (w *sendTicketReplyWorker) Timeout(*river.Job[jobs.CollabSendTicketReplyArgs]) time.Duration {
	return sessionTimeout + time.Minute
}

func (w *sendTicketReplyWorker) Work(ctx context.Context, job *river.Job[jobs.CollabSendTicketReplyArgs]) error {
	return w.s.sendTicketReply(ctx, job.Args.WorkspaceID, job.Args.MessageID, job.Attempt >= job.MaxAttempts)
}

// ticketReplyFailure is a reason a ticket reply cannot go out that trying again does not change.
type ticketReplyFailure struct{ msg string }

func (e *ticketReplyFailure) Error() string { return e.msg }

func (s *service) sendTicketReply(ctx context.Context, wid, mid uuid.UUID, lastAttempt bool) error {
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid); err != nil || suspended {
		if err != nil {
			return err
		}
		return river.JobCancel(errors.New("workspace suspended"))
	}
	r, err := s.tickets.PersonalReply(ctx, wid, mid)
	if errors.Is(err, email.ErrReplySettled) {
		return nil
	}
	var sf *email.SendFailure
	if errors.As(err, &sf) {
		return s.failTicketReply(ctx, wid, mid, sf.Msg)
	}
	if err != nil {
		return err
	}
	err = s.deliverTicketReply(ctx, wid, mid, r)
	if err == nil || errors.Is(err, email.ErrReplySettled) {
		return nil
	}
	var tf *ticketReplyFailure
	var pe *providerError
	var ie *imapError
	switch {
	case errors.As(err, &tf):
		return s.failTicketReply(ctx, wid, mid, tf.msg)
	case isReconnect(err), errors.As(err, &pe) && pe.permanent, errors.As(err, &ie) && ie.permanent, lastAttempt:
		s.log.Warn("ticket reply from account not sent", "workspace_id", wid, "message_id", mid, "err", err)
		if ferr := s.failTicketReply(ctx, wid, mid, safeError(err)); ferr != nil {
			return ferr
		}
		if lastAttempt {
			return nil
		}
		return river.JobCancel(err)
	}
	return err
}

func (s *service) failTicketReply(ctx context.Context, wid, mid uuid.UUID, reason string) error {
	return s.tickets.RecordFailed(ctx, wid, mid, uuid.Nil, reason)
}

// deliverTicketReply checks the author may still send from the account, composes the reply as
// the address's own mail and sends it through the provider.
func (s *service) deliverTicketReply(ctx context.Context, wid, mid uuid.UUID, r *email.PersonalReply) error {
	pool := s.d.DB.Pool
	if r.AuthorID == nil {
		return &ticketReplyFailure{"Only a member can send from their mail account."}
	}
	var role auth.Role
	if err := pool.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2`, wid, *r.AuthorID).Scan(&role); err != nil {
		if db.IsNotFound(err) {
			return &ticketReplyFailure{"The person who wrote this reply is no longer a member."}
		}
		return err
	}
	if err := s.d.Plan.RequireCollab(ctx, pool, wid); err != nil {
		return &ticketReplyFailure{"Collab is not on this workspace's plan."}
	}
	rules, err := loadTicketRules(ctx, pool, wid, r.TicketID)
	if err != nil {
		return err
	}
	if !rules.on {
		return &ticketReplyFailure{rules.off().Detail}
	}
	p := &auth.Principal{Kind: auth.KindUser, UserID: *r.AuthorID, WorkspaceID: wid, Role: role}
	a, err := visibleAccount(ctx, pool, p, r.AccountID)
	if db.IsNotFound(err) || err == nil && !canAct(p) {
		return &ticketReplyFailure{"The mail account this reply was to go from is no longer yours to send from."}
	}
	if err != nil {
		return err
	}
	if a.Reconnect {
		return &ticketReplyFailure{"Connect " + a.Email + " again to send from it."}
	}
	idents, err := accountIdentities(ctx, pool, a)
	if err != nil {
		return err
	}
	from, ok := usableSender(a, idents, r.FromEmail)
	if !ok {
		return &ticketReplyFailure{r.FromEmail + " can no longer send from this account."}
	}
	if why := rules.refuses(a, from.Email); why != "" {
		return &ticketReplyFailure{why}
	}
	f := s.fillerFor(pool, wid, *r.AuthorID)
	if from.Name, err = f.fromName(ctx, a, from.Name); err != nil {
		return err
	}
	sig, err := f.signature(ctx, identitySignature(a, idents, from.Email), from.Email, from.Name)
	if err != nil {
		return err
	}
	html, text := r.HTML, r.Text
	if strings.TrimSpace(sig) != "" {
		html += `<p></p><div class="od-signature">` + sig + `</div>`
		text = strings.TrimRight(text, "\n") + "\n\n" + email.HTMLToText(sig)
	}
	msg := &email.OutMessage{From: from.address(), To: r.To, Cc: r.Cc, Bcc: r.Bcc, Subject: r.Subject, MessageID: r.MessageID,
		InReplyTo: r.InReplyTo, References: r.References, HTML: html, Text: text, Attachments: r.Attachments, Date: time.Now()}
	if replyTo := ticketReplyTo(r, from); replyTo != "" && !strings.EqualFold(replyTo, from.Email) {
		msg.ReplyTo = &mail.Address{Address: replyTo}
	}
	raw, err := email.BuildMIME(msg, true)
	if err != nil {
		return err
	}
	thread, err := ticketThread(ctx, pool, a, r)
	if err != nil {
		return err
	}
	info := SendInfo{From: from.Email, Recipients: append(append(append([]string{}, r.To...), r.Cc...), r.Bcc...)}
	var threadID *uuid.UUID
	if thread != nil {
		info.ThreadRemoteID, threadID = thread.RemoteID, &thread.ID
	}
	// Deleted while it was being composed: it is not sent.
	if ok, err := s.tickets.StillSendable(ctx, wid, mid); err != nil {
		return err
	} else if !ok {
		return email.ErrReplySettled
	}
	prov, err := s.provider(ctx, a)
	if err != nil {
		return err
	}
	defer prov.Close()
	sctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	ref, err := prov.Send(sctx, raw, info)
	if err != nil {
		return err
	}
	// It went out: what is left is bookkeeping, which must not send it again.
	ctx = context.WithoutCancel(ctx)
	if err := s.tickets.RecordSent(ctx, wid, mid, r.TicketID, r.MessageID, from.Email, from.Name, r.To, html); err != nil {
		return err
	}
	stored, err := s.storeSent(ctx, a, raw, ref, *r.AuthorID, threadID)
	if err != nil {
		s.log.Warn("ticket reply sent from account but not stored", "account_id", a.ID, "err", err)
		return nil
	}
	return s.linkTicket(ctx, a, stored, r.TicketID)
}

// ticketReplyTo is the Reply-To a ticket reply goes out with: the address's own, the ticket's
// reply address, or the one asked for ("" none).
func ticketReplyTo(r *email.PersonalReply, from sender) string {
	switch r.ReplyTo {
	case ReplyToAddress, "":
		return from.ReplyTo
	case ReplyToMailbox:
		return r.MailboxAddress
	}
	return r.ReplyTo
}

// identitySignature is the stored signature of the account's address addr.
func identitySignature(a *accountRow, idents []*identityRow, addr string) string {
	if strings.EqualFold(addr, a.Email) {
		return a.SignatureHTML
	}
	for _, x := range idents {
		if strings.EqualFold(x.Email, addr) {
			return x.SignatureHTML
		}
	}
	return ""
}

// ticketThread is the account's conversation a ticket reply joins: one linked to the ticket,
// else one holding any of the ticket's emails; nil for none.
func ticketThread(ctx context.Context, q db.Querier, a *accountRow, r *email.PersonalReply) (*threadRow, error) {
	ids := r.TicketMessageIDs
	if ids == nil {
		ids = []string{}
	}
	t, err := scanThread(q.QueryRow(ctx, `SELECT `+threadColumns+` FROM collab_threads t WHERE t.account_id = $1 AND (t.ticket_id = $2
		OR EXISTS (SELECT 1 FROM collab_messages m WHERE m.thread_id = t.id AND m.message_id = ANY($3)))
		ORDER BY t.ticket_id IS NOT DISTINCT FROM $2 DESC, t.last_message_at DESC LIMIT 1`, a.ID, r.TicketID, ids))
	if db.IsNotFound(err) {
		return nil, nil
	}
	return t, err
}

// linkTicket records that the conversation of stored message id answered ticket, and tells
// those who see the conversation.
func (s *service) linkTicket(ctx context.Context, a *accountRow, id, ticket uuid.UUID) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var thread uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE collab_threads t SET ticket_id = $3 FROM collab_messages m
			WHERE m.id = $2 AND m.account_id = $1 AND t.id = m.thread_id AND t.ticket_id IS DISTINCT FROM $3 RETURNING t.id`,
			a.ID, id, ticket).Scan(&thread)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		accountID := a.ID
		return s.publishChanged(ctx, tx, a.WorkspaceID, &accountID, []uuid.UUID{thread}, false)
	})
}

// answeredTicket checks whether a newly stored message of the account names one of the
// ticket replies sent from this account (named: its In-Reply-To and References, without
// brackets). If so it links the conversation to that ticket and, unless the account sent the
// message itself, queues taking it into the ticket, in tx.
func (p *pass) answeredTicket(ctx context.Context, tx pgx.Tx, id, threadID uuid.UUID, from string, named []string) error {
	ids := make([]string, 0, len(named))
	for _, n := range named {
		if n != "" {
			ids = append(ids, "<"+n+">")
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var ticket uuid.UUID
	err := tx.QueryRow(ctx, `SELECT m.ticket_id FROM messages m JOIN tickets t ON t.id = m.ticket_id AND t.deleted_at IS NULL
		WHERE m.workspace_id = $1 AND m.collab_account_id = $2 AND m.email_message_id = ANY($3) AND m.thread_id IS NULL
		ORDER BY m.created_at DESC LIMIT 1`, p.a.WorkspaceID, p.a.ID, ids).Scan(&ticket)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE collab_threads SET ticket_id = $2 WHERE id = $1 AND ticket_id IS DISTINCT FROM $2`, threadID, ticket); err != nil {
		return err
	}
	idents, err := accountIdentities(ctx, tx, p.a)
	if err != nil {
		return err
	}
	for _, own := range ownAddresses(p.a, idents) {
		if strings.EqualFold(own, from) {
			return nil // the account's own mail (sent from it elsewhere): not an answer
		}
	}
	_, err = p.s.d.Jobs.InsertTx(ctx, tx, jobs.CollabTicketAnswerArgs{WorkspaceID: p.a.WorkspaceID, MessageID: id, TicketID: ticket},
		&river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 5})
	return err
}

// ticketAnswerWorker takes an answer to a ticket reply into the ticket (collab.ticket_answer).
type ticketAnswerWorker struct {
	river.WorkerDefaults[jobs.CollabTicketAnswerArgs]
	s *service
}

func (w *ticketAnswerWorker) Timeout(*river.Job[jobs.CollabTicketAnswerArgs]) time.Duration {
	return sessionTimeout + time.Minute
}

func (w *ticketAnswerWorker) Work(ctx context.Context, job *river.Job[jobs.CollabTicketAnswerArgs]) error {
	return w.s.takeInAnswer(ctx, job.Args)
}

func (s *service) takeInAnswer(ctx context.Context, args jobs.CollabTicketAnswerArgs) error {
	pool := s.d.DB.Pool
	m, err := scanMessage(pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m WHERE m.workspace_id = $1 AND m.id = $2`,
		args.WorkspaceID, args.MessageID))
	if db.IsNotFound(err) {
		return nil // removed meanwhile
	}
	if err != nil {
		return err
	}
	a, err := loadAccount(ctx, pool, args.WorkspaceID, m.AccountID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var threadRemote string
	if err := pool.QueryRow(ctx, `SELECT remote_id FROM collab_threads WHERE id = $1`, m.ThreadID).Scan(&threadRemote); err != nil && !db.IsNotFound(err) {
		return err
	}
	raw, err := s.loadRaw(ctx, a, m, threadRemote)
	if err != nil {
		if isReconnect(err) {
			return river.JobCancel(err)
		}
		return err
	}
	return s.tickets.TakeIn(ctx, args.WorkspaceID, args.TicketID, raw)
}
