package email

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/routing"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/storage"
)

const (
	maxBodyHTML    = 1 << 20
	autoReplyEvery = 4 * time.Hour
)

type processInboundWorker struct {
	river.WorkerDefaults[jobs.ProcessInboundArgs]
	s *service
}

func (w *processInboundWorker) Work(ctx context.Context, job *river.Job[jobs.ProcessInboundArgs]) error {
	err := w.s.processInbound(ctx, job.Args.WorkspaceID, job.Args.InboundEmailID)
	if err != nil && job.Attempt >= job.MaxAttempts {
		_ = w.s.markInbound(context.WithoutCancel(ctx), job.Args.WorkspaceID, job.Args.InboundEmailID, "failed", "processing_failed")
	}
	return err
}

func (s *service) markInbound(ctx context.Context, wid, id uuid.UUID, status, reason string) error {
	_, err := s.d.DB.Pool.Exec(ctx, `UPDATE inbound_emails SET status = $3, reason = $4, processed_at = now()
		WHERE workspace_id = $1 AND id = $2`, wid, id, status, reason)
	return err
}

// preparedAttachment is a stored attachment awaiting its row.
type preparedAttachment struct {
	id          uuid.UUID
	filename    string
	contentType string
	size        int64
	key         string
	cid         string
	inline      bool
}

// threadMatch is the ticket an inbound message belongs to.
type threadMatch struct {
	ticketID uuid.UUID
	number   int64
	status   string
	// thread is set when the message answers one of a third-party thread's messages.
	thread *uuid.UUID
}

// processInbound turns a stored raw email into a ticket message.
func (s *service) processInbound(ctx context.Context, wid, inboundID uuid.UUID) error {
	var mailboxID uuid.UUID
	var key, status, rcpt, msgIDHdr, hash string
	var envFrom *string
	var ref *providerRef
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT mailbox_id, raw_storage_key, status, rcpt_to, message_id_hdr, content_hash, envelope_from, provider_ref
		FROM inbound_emails WHERE workspace_id = $1 AND id = $2`, wid, inboundID).
		Scan(&mailboxID, &key, &status, &rcpt, &msgIDHdr, &hash, &envFrom, &ref)
	if db.IsNotFound(err) || err == nil && status != "pending" {
		return nil
	}
	if err != nil {
		return err
	}
	m, err := loadMailbox(ctx, s.d.DB.Pool, wid, mailboxID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// A suspended workspace keeps its mail but acts on none of it (contracts/admin.md).
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid); err != nil || suspended {
		if err != nil {
			return err
		}
		return s.markInbound(ctx, wid, inboundID, "ignored", "workspace_suspended")
	}
	raw, err := s.readObject(ctx, key, maxRawBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return s.markInbound(ctx, wid, inboundID, "failed", "raw_missing")
	}
	if err != nil {
		return err
	}
	pm, err := parseMail(raw)
	if err != nil {
		return s.markInbound(ctx, wid, inboundID, "failed", "unparseable")
	}
	own, err := s.ownAddresses(ctx, wid)
	if err != nil {
		return err
	}
	self := m.addresses()
	null := nullSender(envFrom, pm)
	reason := noiseReason(pm, self, m.Email, null)
	// A trusted contact form marks its mail as generated or bulk; it is still a visitor
	// writing in. An autoresponder through the form stays noise.
	if (reason == "auto_submitted" || reason == "precedence") && formSenderTrusted(m.TrustedFormSenders, pm.FromAddr) && !autoresponse(pm, null) {
		reason = ""
	}
	if reason != "" {
		if reason == "bounce" && null {
			if err := s.recordBounce(ctx, wid, m.ID, pm, raw); err != nil {
				return err
			}
		}
		return s.markInbound(ctx, wid, inboundID, "ignored", reason)
	}
	loop, err := s.ourLoop(ctx, wid, pm, own)
	if err != nil {
		return err
	}
	if loop {
		return s.markInbound(ctx, wid, inboundID, "ignored", "loop")
	}
	sender, senderName := pm.sender()
	if !validAddress(sender) {
		return s.markInbound(ctx, wid, inboundID, "ignored", "no_sender")
	}
	// Mail from this mailbox's own addresses, or replying through a forwarding address, is
	// our own. Mail from another of the workspace's addresses (forwarded or sent to this
	// one by a teammate) opens a ticket here, unless it is mail we generated.
	if slicesContainsFold(self, sender) || strings.EqualFold(emailDomain(sender), s.inboundDomain()) {
		return s.markInbound(ctx, wid, inboundID, "ignored", "from_own_mailbox")
	}
	internal := slicesContainsFold(own, sender)
	if internal && ourMsgIDRe.MatchString(pm.MessageID) {
		return s.markInbound(ctx, wid, inboundID, "ignored", "from_own_mailbox")
	}
	// A DMARC failure reported by a trusted receiving server: the From address is not
	// the sender's own. A new ticket is marked spam; a message on an existing one is kept
	// and flagged, and changes nothing about the ticket.
	verdict := dmarcVerdict(raw, s.trustedAuthserv(m))
	authFailed := verdict == authFail
	// A website contact form the mailbox trusts, whose mail passed sender authentication,
	// writes for the visitor its Reply-To names: the visitor is the customer.
	formSender := ""
	if !internal && verdict == authPass && formSenderTrusted(m.TrustedFormSenders, sender) {
		if visitor := pm.replyTo(); s.formVisitorOK(visitor, own) {
			formSender = sender
			sender, senderName = visitor, pm.ReplyName
		}
	}
	receivedAt := receivedAddress(m, pm, rcpt)
	var blocked bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contacts WHERE workspace_id = $1 AND email = $2 AND blocked)`,
		wid, sender).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return s.markInbound(ctx, wid, inboundID, "ignored", "blocked_contact")
	}
	dup, taken, err := s.seenBefore(ctx, wid, inboundID, pm.MessageID, msgIDHdr, hash)
	if err != nil {
		return err
	}
	if dup {
		return s.markInbound(ctx, wid, inboundID, "ignored", "duplicate")
	}

	atts, err := s.storeAttachments(ctx, wid, pm)
	if err != nil {
		return err
	}
	cidURLs := map[string]string{}
	for _, a := range atts {
		if a.cid != "" {
			cidURLs[a.cid] = domain.AttachmentURL(a.id)
		}
	}
	body := renderBody(pm, cidURLs)
	for i := range atts {
		atts[i].inline = atts[i].cid != "" && body.usedCIDs[atts[i].cid]
	}
	// Nothing automatic answers automatic or list mail, a teammate's mail (so two
	// addresses never reply to each other) or mail that failed authentication. A trusted
	// form's mail is machine-sent by design and written for a person, so only an
	// autoresponder or a loop marker stops an answer to its visitor.
	automatic := automaticMail(pm, m.Email, null)
	if formSender != "" {
		automatic = autoresponse(pm, null)
	}
	noAuto := internal || authFailed || automatic || noReplySender(sender)

	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		match, err := s.findThread(ctx, tx, wid, m, pm, rcpt, sender)
		if err != nil {
			return err
		}
		if match != nil && match.thread != nil {
			// An answer to a third-party thread stays in the thread: an internal note on the
			// ticket, never a message from the customer, and nothing automatic answers it.
			attIDs, err := insertAttachments(ctx, tx, wid, atts)
			if err != nil {
				return err
			}
			msg, err := domain.AddMessage(ctx, tx, s.d.Jobs, domain.NewMessage{
				WorkspaceID: wid, TicketID: match.ticketID, ThreadID: match.thread, Kind: "note", Direction: "inbound", AuthorType: "contact",
				BodyHTML: body.html, BodyText: body.text, FullHTML: body.full, Subject: pm.Subject, FromEmail: sender,
				FromName: displayname.Clean(senderName, displayname.Person), To: pm.To, Cc: pm.Cc, EmailMessageID: msgIDOf(pm, taken),
				InReplyTo: pm.InReplyTo, References: pm.References, InboundEmailID: &inboundID, AttachmentIDs: attIDs, MailboxID: &m.ID,
				ReplyTo: pm.replyTo(), AuthFailed: authFailed, NoAutoReply: true, ReceivedAt: receivedAt,
			})
			if err != nil {
				return err
			}
			if ref != nil {
				// Tidied at the provider like any mail the ticket takes in, and archived with it.
				if _, err := tx.Exec(ctx, `UPDATE messages SET provider_ref = $3 WHERE workspace_id = $1 AND id = $2`, wid, msg.ID, ref); err != nil {
					return err
				}
				if err := s.queueImportedTidy(ctx, tx, m, ref, match.ticketID, msg.ID); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE inbound_emails SET status = 'processed', reason = '', processed_at = now()
				WHERE workspace_id = $1 AND id = $2`, wid, inboundID)
			return err
		}
		if match != nil && internal {
			// A teammate's mail threads only into a conversation they are the customer of.
			if match, err = s.contactMatch(ctx, tx, wid, match, sender); err != nil {
				return err
			}
		}
		contactID, _, err := domain.UpsertContactByEmail(ctx, tx, wid, sender, senderName)
		if err != nil {
			return err
		}
		attIDs, err := insertAttachments(ctx, tx, wid, atts)
		if err != nil {
			return err
		}
		var ticket domain.Ticket
		created := false
		if match != nil && match.status != domain.StatusClosed {
			ticket.ID, ticket.Number = match.ticketID, match.number
		} else {
			custom := map[string]any{}
			subject := subjectTagRe.ReplaceAllString(pm.Subject, "")
			if match != nil {
				custom["previous_ticket_id"] = match.ticketID.String()
				subject = baseSubject(pm.Subject)
			}
			if subject = strings.Join(strings.Fields(subject), " "); subject == "" {
				subject = "(no subject)"
			}
			// An alias with a brand of its own gives its new tickets that brand.
			brand, err := domain.ResolveBrand(ctx, tx, wid, m.aliasSettings(receivedAt).BrandID, &m.ID, nil)
			if err != nil {
				return err
			}
			route, err := routing.Resolve(ctx, tx, routing.Input{WorkspaceID: wid, Channel: domain.ChannelEmail, BrandID: brand,
				MailboxID: &m.ID, ReceivedAt: receivedAt, FromEmail: sender, Subject: subject, Body: body.text, ContactID: &contactID})
			if err != nil {
				return err
			}
			// Replies go out from the address the ticket arrived at.
			ticket, err = domain.CreateTicket(ctx, tx, domain.NewTicket{WorkspaceID: wid, Subject: subject, Channel: domain.ChannelEmail,
				MailboxID: &m.ID, ContactID: &contactID, TeamID: route.TeamID, Routed: route.Routed, Custom: custom,
				BrandID: &brand, OriginMailboxID: &m.ID, ReceivedAt: receivedAt})
			if err != nil {
				return err
			}
			if rule := route.RuleID(); rule != nil {
				if err := routing.Count(ctx, tx, *rule); err != nil {
					return err
				}
			}
			created = true
			if authFailed {
				if _, err := tx.Exec(ctx, `UPDATE tickets SET spam = true, updated_at = now() WHERE workspace_id = $1 AND id = $2`, wid, ticket.ID); err != nil {
					return err
				}
				if err := domain.PublishTicketUpdated(ctx, tx, wid, ticket.ID, []string{"spam"}); err != nil {
					return err
				}
			}
		}
		msgID := msgIDOf(pm, taken)
		trigger := ""
		switch {
		case created:
			trigger = jobs.TriggerTicketCreated
		case authFailed:
			trigger = "none" // no customer_replied automations for a sender that failed authentication
		}
		replyTo := pm.replyTo()
		if formSender != "" {
			replyTo = "" // the visitor it named is the sender
		}
		// A reply that failed sender authentication does not reopen the ticket
		// (status_changed with reason "sender_unverified" records it).
		msg, err := domain.AddMessage(ctx, tx, s.d.Jobs, domain.NewMessage{
			WorkspaceID: wid, TicketID: ticket.ID, Kind: "reply", Direction: "inbound", AuthorType: "contact",
			AuthorContactID: &contactID, BodyHTML: body.html, BodyText: body.text, FullHTML: body.full,
			Subject: pm.Subject, FromEmail: sender, FromName: displayname.Clean(senderName, displayname.Person),
			To: pm.To, Cc: pm.Cc, EmailMessageID: msgID, InReplyTo: pm.InReplyTo, References: pm.References,
			InboundEmailID: &inboundID, AttachmentIDs: attIDs, Trigger: trigger, MailboxID: &m.ID,
			ReplyTo: replyTo, AuthFailed: authFailed, NoAutoReply: noAuto, FormSender: formSender, NoReopen: authFailed && !created,
			ReceivedAt: receivedAt,
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE inbound_emails SET status = 'processed', reason = '', processed_at = now()
			WHERE workspace_id = $1 AND id = $2`, wid, inboundID); err != nil {
			return err
		}
		if ref != nil {
			// Where the email lives at Gmail or Microsoft: replies join its conversation.
			if _, err := tx.Exec(ctx, `UPDATE messages SET provider_ref = $3 WHERE workspace_id = $1 AND id = $2`, wid, msg.ID, ref); err != nil {
				return err
			}
			if err := s.queueImportedTidy(ctx, tx, m, ref, ticket.ID, msg.ID); err != nil {
				return err
			}
		}
		if !authFailed && !internal {
			if err := s.queueAIAnswer(ctx, tx, m, ticket.ID, msg.ID, pm, sender); err != nil {
				return err
			}
		}
		if created && !noAuto {
			return s.queueAutoReply(ctx, tx, m, ticket, sender, senderName)
		}
		return nil
	})
	if err != nil {
		s.deleteAttachmentObjects(atts)
		if db.IsUniqueViolation(err, "messages_ws_msgid_uq") {
			return s.markInbound(ctx, wid, inboundID, "ignored", "duplicate")
		}
		return err
	}
	return nil
}

// seenBefore reports whether an inbound message was already taken in: the same message
// (Message-ID and content) at any of the workspace's addresses, mail we sent, or a
// message with its Message-ID that no inbound email recorded (imported history: the first
// sync after an import reads it again). taken is set when a different message already
// holds its Message-ID.
func (s *service) seenBefore(ctx context.Context, wid, inboundID uuid.UUID, msgID, msgIDHdr, hash string) (dup, taken bool, err error) {
	var direction string
	var existingHash *string
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT ms.direction, ie.content_hash FROM messages ms
		LEFT JOIN inbound_emails ie ON ie.id = ms.inbound_email_id AND ie.workspace_id = ms.workspace_id
		WHERE ms.workspace_id = $1 AND ms.email_message_id = $2`, wid, msgID).Scan(&direction, &existingHash)
	switch {
	case err == nil:
		// Mail we sent, an imported message, or the same message recorded before its content
		// was fingerprinted.
		if direction == "outbound" || existingHash == nil || *existingHash == "" || *existingHash == hash {
			return true, true, nil
		}
		taken = true
	case !db.IsNotFound(err):
		return false, false, err
	}
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inbound_emails WHERE workspace_id = $1 AND id <> $2
		AND message_id_hdr = $3 AND content_hash = $4 AND status = 'processed')`, wid, inboundID, msgIDHdr, hash).Scan(&dup)
	return dup, taken, err
}

func (s *service) readObject(ctx context.Context, key string, limit int64) ([]byte, error) {
	rc, err := s.d.Storage.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("stored object exceeds the size limit")
	}
	return b, nil
}

// ownAddresses lists the workspace's mailbox addresses and aliases, for loop detection.
func (s *service) ownAddresses(ctx context.Context, wid uuid.UUID) ([]string, error) {
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT lower(email::text) FROM mailboxes WHERE workspace_id = $1
		UNION SELECT lower(config->>'oauth_email') FROM mailboxes WHERE workspace_id = $1 AND coalesce(config->>'oauth_email', '') <> ''
		UNION SELECT lower(a) FROM mailboxes, unnest(aliases) a WHERE workspace_id = $1`, wid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func slicesContainsFold(list []string, s string) bool {
	if s == "" {
		return false
	}
	for _, a := range list {
		if strings.EqualFold(a, s) {
			return true
		}
	}
	return false
}

// ourLoop reports mail we sent coming back: every message we send carries its mailbox's
// loop token in X-Loop, which a person's reply or forward does not carry but redirects and
// automatic forwards keep. Mail naming one of the workspace's addresses there (as mail
// sent before the token) counts too.
func (s *service) ourLoop(ctx context.Context, wid uuid.UUID, pm *parsedMail, own []string) (bool, error) {
	vals := pm.headerValues(loopHeader)
	if len(vals) == 0 {
		return false, nil
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT id FROM mailboxes WHERE workspace_id = $1`, wid)
	if err != nil {
		return false, err
	}
	boxes, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return false, err
	}
	tokens := make([]string, 0, len(boxes))
	for _, id := range boxes {
		tokens = append(tokens, s.loopToken(id))
	}
	for _, v := range vals {
		for _, a := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '<' || r == '>' }) {
			if slicesContainsFold(own, a) || slicesContainsFold(tokens, a) {
				return true, nil
			}
		}
	}
	return false, nil
}

// receivedAddress is the exact address the mail was written to: the first of the
// envelope recipient, Delivered-To, X-Original-To, To and Cc that is the mailbox's address
// or one of its aliases, else the mailbox's address.
func receivedAddress(m *mailboxRow, pm *parsedMail, rcpt string) string {
	cands := []string{rcpt}
	for _, h := range []string{"Delivered-To", "X-Original-To"} {
		for _, v := range pm.headerValues(h) {
			if a := looseAddrRe.FindString(v); a != "" {
				cands = append(cands, a)
			}
		}
	}
	cands = append(cands, pm.To...)
	cands = append(cands, pm.Cc...)
	for _, a := range cands {
		a = strings.ToLower(strings.Trim(strings.TrimSpace(a), "<>"))
		if a == "" {
			continue
		}
		if strings.EqualFold(a, m.Email) || m.alias(a) != "" {
			return a
		}
	}
	return strings.ToLower(m.Email)
}

// insertAttachments stores the rows of an inbound message's prepared attachments.
func insertAttachments(ctx context.Context, tx pgx.Tx, wid uuid.UUID, atts []preparedAttachment) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(atts))
	for _, a := range atts {
		if _, err := tx.Exec(ctx, `INSERT INTO attachments (id, workspace_id, filename, content_type, size_bytes, storage_key, content_id, inline)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, a.id, wid, a.filename, a.contentType, a.size, a.key, a.cid, a.inline); err != nil {
			return nil, err
		}
		ids = append(ids, a.id)
	}
	return ids, nil
}

// msgIDOf is the Message-ID an inbound message is stored with: a Message-ID already held
// by a different message stays with that one.
func msgIDOf(pm *parsedMail, taken bool) *string {
	if taken {
		return nil
	}
	return &pm.MessageID
}

// contactMatch keeps a thread match only when sender is the ticket's contact.
func (s *service) contactMatch(ctx context.Context, tx pgx.Tx, wid uuid.UUID, t *threadMatch, sender string) (*threadMatch, error) {
	var contactEmail *string
	err := tx.QueryRow(ctx, `SELECT c.email FROM tickets t LEFT JOIN contacts c ON c.id = t.contact_id AND c.workspace_id = t.workspace_id
		WHERE t.workspace_id = $1 AND t.id = $2`, wid, t.ticketID).Scan(&contactEmail)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if contactEmail == nil || !strings.EqualFold(*contactEmail, sender) {
		return nil, nil
	}
	return t, nil
}

func (s *service) storeAttachments(ctx context.Context, wid uuid.UUID, pm *parsedMail) ([]preparedAttachment, error) {
	var out []preparedAttachment
	for _, p := range pm.Parts {
		if len(out) >= maxAttachments {
			break
		}
		if len(p.Content) > maxAttachmentBytes {
			continue
		}
		ct := strings.ToLower(p.ContentType)
		if mt, _, err := mime.ParseMediaType(ct); err == nil {
			ct = mt
		} else {
			ct = "application/octet-stream"
		}
		a := preparedAttachment{id: ids.New(), size: int64(len(p.Content)), cid: normalizeCID(p.ContentID)}
		a.filename = cleanFilename(p.FileName, ct)
		// The stored type comes from the bytes, as for an upload: active types are kept as plain text.
		a.contentType = hd.DetectType(p.Content[:min(len(p.Content), 512)], a.filename)
		a.key = "workspaces/" + wid.String() + "/attachments/" + a.id.String()
		if err := s.d.Storage.Put(ctx, a.key, bytes.NewReader(p.Content), a.size, a.contentType); err != nil {
			s.deleteAttachmentObjects(out)
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *service) deleteAttachmentObjects(atts []preparedAttachment) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, a := range atts {
		_ = s.d.Storage.Delete(ctx, a.key)
	}
}

type renderedBody struct {
	html, full, text string
	usedCIDs         map[string]bool
}

// renderBody sanitizes the message, rewrites inline images and separates the new text
// from quoted history. full is set only when something was stripped.
func renderBody(pm *parsedMail, cidURLs map[string]string) renderedBody {
	src := pm.HTML
	textOnly := strings.TrimSpace(src) == ""
	if textOnly {
		src = textToHTML(pm.Text)
	}
	out := renderedBody{usedCIDs: map[string]bool{}}
	doc := parseFragment(src)
	if doc == nil {
		out.text = strings.TrimSpace(pm.Text)
		out.html = sanitizeHTML(textToHTML(out.text))
		return out
	}
	out.usedCIDs = rewriteCID(doc, cidURLs)
	full := sanitizeHTML(render(doc))
	visible, removed := full, false
	if !isForward(pm.Subject) {
		if textOnly {
			if t, ok := stripQuotesText(pm.Text); ok {
				visible, removed = sanitizeHTML(textToHTML(t)), true
			}
		} else if stripQuotesHTML(doc) {
			if cand := sanitizeHTML(render(doc)); htmlToText(cand) != "" || strings.Contains(cand, "<img") {
				visible, removed = cand, true
			}
		}
	}
	text := htmlToText(visible)
	if textOnly && !removed {
		text = strings.TrimSpace(pm.Text)
	}
	if !isForward(pm.Subject) {
		if t, ok := stripQuotesText(text); ok {
			text = t
			if !removed {
				// The text shows quoting the HTML had no marker for; keep both in step.
				visible, removed = sanitizeHTML(textToHTML(t)), true
			}
		}
	}
	if len(visible) > maxBodyHTML {
		visible = sanitizeHTML(textToHTML(truncate(text, maxBodyHTML/4)))
	}
	if len(full) > maxBodyHTML*4 {
		full = sanitizeHTML(textToHTML(truncate(htmlToText(full), maxBodyHTML)))
	}
	out.html, out.text = visible, truncate(text, 200000)
	if removed {
		out.full = full
	}
	return out
}

// findThread matches an inbound message to a ticket, within the receiving workspace:
// a Message-ID we stored in its References, then our own Message-ID format when it
// proves itself (the message it names exists on that ticket, or the auto-reply id carries
// a valid MAC), then the "+t<n>" address tag, then a "[#n]" subject tag. A ticket number
// that is not proven is guessable, so it only matches when the sender is the ticket's
// contact.
func (s *service) findThread(ctx context.Context, tx pgx.Tx, wid uuid.UUID, m *mailboxRow, pm *parsedMail, rcpt, sender string) (*threadMatch, error) {
	refs := append([]string{}, pm.References...)
	if pm.InReplyTo != "" {
		refs = append(refs, pm.InReplyTo)
	}
	var candidates []int64
	if len(refs) > 0 {
		var t threadMatch
		err := tx.QueryRow(ctx, `SELECT t.id, t.number, t.status, msg.thread_id FROM messages msg
			JOIN tickets t ON t.id = msg.ticket_id AND t.workspace_id = msg.workspace_id AND t.deleted_at IS NULL
			WHERE msg.workspace_id = $1 AND msg.email_message_id = ANY($2)
			ORDER BY msg.created_at DESC LIMIT 1`, wid, refs).Scan(&t.ticketID, &t.number, &t.status, &t.thread)
		if err == nil {
			return &t, nil
		}
		if !db.IsNotFound(err) {
			return nil, err
		}
		// Our own Message-ID format carries the ticket number (auto-replies are not
		// stored as messages, and some providers re-stamp the id of what we sent).
		for i := len(refs) - 1; i >= 0; i-- {
			kind, id, n, ok := ourMessageID(refs[i])
			if !ok {
				continue
			}
			t, err := s.provenThread(ctx, tx, wid, kind, id, n)
			if err != nil || t != nil {
				return t, err
			}
			candidates = append(candidates, n)
		}
	}
	addrs := append([]string{rcpt}, pm.To...)
	addrs = append(addrs, pm.Cc...)
	for _, h := range []string{"Delivered-To", "X-Original-To", "X-Forwarded-To"} {
		addrs = append(addrs, pm.headerValues(h)...)
	}
	for _, a := range addrs {
		token, tag, domain, ok := splitRecipient(a)
		if !ok || domain != s.inboundDomain() || token != m.ForwardingToken {
			continue
		}
		if n, ok := plusTicketNumber(tag); ok {
			candidates = append(candidates, n)
		}
	}
	if n, ok := subjectTicketNumber(pm.Subject); ok {
		candidates = append(candidates, n)
	}
	for _, n := range candidates {
		if t, err := s.ticketByNumber(ctx, tx, wid, n, sender); err != nil || t != nil {
			return t, err
		}
	}
	return nil, nil
}

// provenThread returns the ticket our own Message-ID names when the id proves itself: an
// "m_" id names a message of this workspace on ticket n, an "a_" id carries the MAC
// autoMessageID made for this workspace and ticket n.
func (s *service) provenThread(ctx context.Context, tx pgx.Tx, wid uuid.UUID, kind byte, id string, n int64) (*threadMatch, error) {
	switch kind {
	case 'm':
		mid, err := uuid.Parse(id)
		if err != nil {
			return nil, nil
		}
		var t threadMatch
		err = tx.QueryRow(ctx, `SELECT t.id, t.number, t.status, msg.thread_id FROM messages msg
			JOIN tickets t ON t.id = msg.ticket_id AND t.workspace_id = msg.workspace_id AND t.deleted_at IS NULL
			WHERE msg.workspace_id = $1 AND msg.id = $2 AND t.number = $3`, wid, mid, n).Scan(&t.ticketID, &t.number, &t.status, &t.thread)
		if db.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &t, nil
	case 'a':
		if s.verifyAutoID(wid, id, n) {
			return s.ticketByNumber(ctx, tx, wid, n, "")
		}
	}
	return nil, nil
}

// ticketByNumber loads a ticket by number; with sender set it must be the contact's.
func (s *service) ticketByNumber(ctx context.Context, tx pgx.Tx, wid uuid.UUID, n int64, sender string) (*threadMatch, error) {
	var t threadMatch
	var contactEmail *string
	err := tx.QueryRow(ctx, `SELECT t.id, t.number, t.status, c.email FROM tickets t
		LEFT JOIN contacts c ON c.id = t.contact_id AND c.workspace_id = t.workspace_id
		WHERE t.workspace_id = $1 AND t.number = $2 AND t.deleted_at IS NULL`, wid, n).Scan(&t.ticketID, &t.number, &t.status, &contactEmail)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sender != "" && (contactEmail == nil || !strings.EqualFold(*contactEmail, sender)) {
		return nil, nil
	}
	return &t, nil
}

// recordBounce marks the outbound message a delivery report refers to. Only a report
// with the null envelope sender reaches here, and it counts only for a message this
// mailbox sent.
func (s *service) recordBounce(ctx context.Context, wid, mailboxID uuid.UUID, pm *parsedMail, raw []byte) error {
	refs := bounceRefs(pm, raw)
	if len(refs) == 0 {
		return nil
	}
	var msgIDs []uuid.UUID
	for _, r := range refs {
		if kind, id, _, ok := ourMessageID(r); ok && kind == 'm' {
			if id, err := uuid.Parse(id); err == nil {
				msgIDs = append(msgIDs, id)
			}
		}
	}
	detail := bounceDetail(raw)
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE messages SET delivery_status = 'bounced', delivery_error = $4
			WHERE workspace_id = $1 AND direction = 'outbound' AND mailbox_id = $5 AND delivery_status IN ('sent', 'pending')
			AND (email_message_id = ANY($2) OR id = ANY($3))
			RETURNING id, ticket_id`, wid, refs, msgIDs, detail, mailboxID)
		if err != nil {
			return err
		}
		type hit struct{ id, ticket uuid.UUID }
		hits, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hit, error) {
			var h hit
			return h, r.Scan(&h.id, &h.ticket)
		})
		if err != nil {
			return err
		}
		for _, h := range hits {
			msg, err := domain.LoadMessage(ctx, tx, wid, h.id)
			if err != nil {
				return err
			}
			if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "message.updated", TicketID: &h.ticket,
				Data: map[string]any{"ticket_id": h.ticket, "message": msg}}); err != nil {
				return err
			}
		}
		return nil
	})
}

// queueAutoReply sends the mailbox auto-reply for a new ticket, at most once per sender
// and address per window, so writing to two addresses gets both.
func (s *service) queueAutoReply(ctx context.Context, tx pgx.Tx, m *mailboxRow, t domain.Ticket, sender, senderName string) error {
	if !m.AutoReplyEnabled || strings.TrimSpace(m.AutoReplyHTML) == "" {
		return nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `INSERT INTO email_auto_replies (workspace_id, mailbox_id, email) VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, mailbox_id, email) DO UPDATE SET sent_at = now()
		WHERE email_auto_replies.sent_at < now() - $4::interval RETURNING true`,
		m.WorkspaceID, m.ID, sender, fmt.Sprintf("%d seconds", int(autoReplyEvery.Seconds()))).Scan(&ok)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// The subject and body are filled when the email is sent (contracts/templates.md).
	_, err = s.d.Jobs.InsertTx(ctx, tx, jobs.SendTicketEmailArgs{WorkspaceID: m.WorkspaceID, TicketID: t.ID, Subject: m.AutoReplySubject,
		HTML: m.AutoReplyHTML, AutoGenerated: true, Template: true}, &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 5})
	return err
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
