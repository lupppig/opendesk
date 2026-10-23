package collab

import (
	"context"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Composing a message from the composer's fields (contracts/collab.md "POST
// /v1/collab/messages", "Drafts"): recipients checked, the HTML cleaned with the policy of
// stored email, files gathered (the caller's uploads, a forward's originals, and images
// the HTML shows from stored messages, sent as cid: parts), then the MIME built. Sending
// and saving a draft share it.

// composeIn is what the composer sends.
type composeIn struct {
	Mode               string
	To, Cc, Bcc        []string
	Subject            string
	BodyHTML           string
	AttachmentIDs      []uuid.UUID
	ForwardAttachments bool
	// FromEmail is the account's address it goes out from ("" = composeSender's choice).
	FromEmail string
	// MessageID keeps a draft's Message-ID across saves ("" = a new one).
	MessageID string
	// Draft is the draft being saved again: its files listed in Keep stay on it.
	Draft *messageRow
	Keep  []int
	// NeedRecipients refuses a message without recipients (sending; a draft may have none).
	NeedRecipients bool
}

// composed is a message ready for the provider.
type composed struct {
	Raw        []byte // Bcc included: Gmail and Graph read recipients from it
	Recipients []string
	To, Cc     []string
	Bcc        []string
	Subject    string
	HTML       string
	MessageID  string // bare
	HasFiles   bool
	From       string // the address it goes out from
}

// target is the account a message goes out from and, for a reply or forward, the message
// it answers and its conversation.
type target struct {
	a      *accountRow
	orig   *messageRow
	thread *threadRow
}

// composeTarget checks who may write: new mail needs sight of the account; a reply or
// forward, sight of the conversation (a conversation shared on its own is enough).
func (s *service) composeTarget(ctx context.Context, p *auth.Principal, accountID uuid.UUID, mode string, messageID *uuid.UUID) (target, error) {
	pool := s.d.DB.Pool
	a, err := loadAccount(ctx, pool, p.WorkspaceID, accountID)
	if err != nil {
		return target{}, notFound(err, "Account")
	}
	out := target{a: a}
	if mode == ModeNew {
		if _, err := visibleAccount(ctx, pool, p, a.ID); err != nil {
			return target{}, httpx.NotFound("Account")
		}
		return out, nil
	}
	if messageID == nil {
		return target{}, httpx.Validation("Choose the message to reply to or forward.")
	}
	out.orig, err = scanMessage(pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m
		WHERE m.workspace_id = $1 AND m.id = $2 AND m.account_id = $3`, p.WorkspaceID, *messageID, a.ID))
	if err != nil {
		return target{}, notFound(err, "Message")
	}
	if out.thread, err = visibleThread(ctx, pool, p, out.orig.ThreadID); err != nil {
		return target{}, notFound(err, "Message")
	}
	return out, nil
}

// composeSender is the account's address a message goes out from (identities.go): the
// one chosen, which must be one of its usable addresses; else a draft saved again keeps
// its own From, a reply or forward goes from the address the original was written to,
// and anything else from the account's default.
func (s *service) composeSender(ctx context.Context, t target, in composeIn) (sender, error) {
	idents, err := accountIdentities(ctx, s.d.DB.Pool, t.a)
	if err != nil {
		return sender{}, httpx.Wrap(err)
	}
	if strings.TrimSpace(in.FromEmail) != "" {
		if from, ok := usableSender(t.a, idents, in.FromEmail); ok {
			return from, nil
		}
		return sender{}, httpx.Validation("Choose one of the account's addresses to send from.")
	}
	if in.Draft != nil {
		if from, ok := usableSender(t.a, idents, in.Draft.From.Email); ok {
			return from, nil
		}
	}
	if t.orig != nil {
		return replySender(t.a, idents, t.orig), nil
	}
	return defaultSender(t.a, idents), nil
}

// cleanRecipients checks and dedupes addresses ("Ada <ada@x.test>" or bare).
func cleanRecipients(list []string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, v := range list {
		addr := strings.ToLower(strings.TrimSpace(v))
		if parsed, err := mail.ParseAddress(v); err == nil {
			addr = strings.ToLower(parsed.Address)
		}
		if !email.ValidAddress(addr) {
			return nil, httpx.Validation("Check the address " + clipRunes(strings.TrimSpace(v), 80) + ".")
		}
		if !containsFold(out, addr) {
			out = append(out, addr)
		}
	}
	return out, nil
}

// cleanSubject drops control characters.
func cleanSubject(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s))
}

// imageSource is a stored message whose files go out with a message: those the HTML
// shows (as cid: parts), every one (a forward that keeps them), or the listed ones (a
// draft's own files).
type imageSource struct {
	m     *messageRow
	shown map[int]bool
	all   bool
	keep  map[int]bool
}

func (s *service) compose(ctx context.Context, p *auth.Principal, t target, in composeIn) (*composed, error) {
	a, orig := t.a, t.orig
	from, err := s.composeSender(ctx, t, in)
	if err != nil {
		return nil, err
	}
	// A From name with variables names the member sending it (personal.go), whoever wrote
	// the draft.
	if from.Name, err = s.fillerFor(s.d.DB.Pool, p.WorkspaceID, p.UserID).fromName(ctx, a, from.Name); err != nil {
		return nil, httpx.Wrap(err)
	}
	to, err := cleanRecipients(in.To)
	if err != nil {
		return nil, err
	}
	cc, err := cleanRecipients(in.Cc)
	if err != nil {
		return nil, err
	}
	bcc, err := cleanRecipients(in.Bcc)
	if err != nil {
		return nil, err
	}
	total := len(to) + len(cc) + len(bcc)
	if total == 0 && in.NeedRecipients {
		return nil, httpx.Validation("Add a recipient.")
	}
	if total > maxRecipients {
		return nil, httpx.Validation("Send to at most 100 people.")
	}
	subject := cleanSubject(in.Subject)
	if subject == "" && orig != nil {
		base := email.BaseSubject(orig.Subject)
		if in.Mode == ModeForward {
			subject = "Fwd: " + base
		} else {
			subject = "Re: " + base
		}
	}
	if len(in.BodyHTML) > maxSendHTML {
		return nil, httpx.Validation("The message is too long.")
	}
	// The composer's HTML carries the signature and the quoted message: it is cleaned with
	// the policy of stored email, which keeps their layout and drops anything active.
	// Images of uploads may be linked relative to the API; they are made absolute first so
	// they survive cleaning and are then sent as cid: parts.
	api := strings.TrimRight(s.d.Cfg.APIURL, "/")
	body := email.SanitizeHTML(strings.ReplaceAll(in.BodyHTML, `src="/v1/attachments/`, `src="`+api+`/v1/attachments/`))

	// Files of stored messages: a draft's own first (their order, and so the links to
	// them, stay put across saves), then the caller's uploads, then the original's and any
	// other stored message's the HTML shows.
	sources := []*imageSource{}
	byID := map[uuid.UUID]*imageSource{}
	add := func(m *messageRow) *imageSource {
		if src, ok := byID[m.ID]; ok {
			return src
		}
		src := &imageSource{m: m, shown: map[int]bool{}, keep: map[int]bool{}}
		byID[m.ID] = src
		sources = append(sources, src)
		return src
	}
	if in.Draft != nil {
		src := add(in.Draft)
		for _, i := range in.Keep {
			src.keep[i] = true
		}
	}
	if orig != nil && in.Mode == ModeForward && in.ForwardAttachments && len(orig.Attachments) > 0 {
		add(orig).all = true
	}
	for _, m := range collabImageLink.FindAllStringSubmatch(body, -1) {
		id, ok := ids.Parse(m[1])
		i, err := strconv.Atoi(m[2])
		if !ok || err != nil {
			continue
		}
		src := byID[id]
		if src == nil {
			var row *messageRow
			switch {
			case orig != nil && orig.ID == id:
				row = orig
			default:
				// Another stored message of the account the caller sees (a quoted older message).
				r, err := scanMessage(s.d.DB.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m
					WHERE m.workspace_id = $1 AND m.id = $2 AND m.account_id = $3`, p.WorkspaceID, id, a.ID))
				if err != nil {
					continue
				}
				if _, err := visibleThread(ctx, s.d.DB.Pool, p, r.ThreadID); err != nil {
					continue
				}
				row = r
			}
			src = add(row)
		}
		src.shown[i] = true
	}

	uploader := p.UserID
	if err := hd.CheckAttachments(ctx, s.d.DB.Pool, p.WorkspaceID, in.AttachmentIDs, hd.Uploader{User: &uploader}); err != nil {
		return nil, httpx.Wrap(err)
	}
	uploads, body, err := s.uploadedFiles(ctx, p.WorkspaceID, in.AttachmentIDs, body)
	if err != nil {
		return nil, err
	}
	var own, others []email.OutAttachment
	cidOf := map[uuid.UUID]map[int]string{}
	for _, src := range sources {
		if len(src.m.Attachments) == 0 || (!src.all && len(src.shown) == 0 && len(src.keep) == 0) {
			continue
		}
		raw, err := s.loadRaw(ctx, a, src.m, "")
		if err != nil {
			return nil, errSend("The files of a message could not be read. Try again.")
		}
		pm, err := email.ParseMail(raw)
		if err != nil {
			return nil, errSend("The files of a message could not be read.")
		}
		cids := map[int]string{}
		for i, part := range pm.Parts {
			if !src.all && !src.shown[i] && !src.keep[i] {
				continue
			}
			f := email.OutAttachment{Filename: email.CleanFilename(part.FileName, part.ContentType), ContentType: part.ContentType, Data: part.Content}
			if src.shown[i] {
				// A draft's own images keep their Content-ID; others get a stable one.
				f.ContentID = email.NormalizeCID(part.ContentID)
				if f.ContentID == "" || src.m != in.Draft {
					f.ContentID = src.m.ID.String() + "." + strconv.Itoa(i) + "@opendesk"
				}
				cids[i] = f.ContentID
			}
			if src.m == in.Draft {
				own = append(own, f)
			} else {
				others = append(others, f)
			}
		}
		cidOf[src.m.ID] = cids
	}
	if len(cidOf) > 0 {
		body = rewriteImages(body, func(link string) string {
			m := collabImageLink.FindStringSubmatch(link)
			if m == nil {
				return ""
			}
			id, ok := ids.Parse(m[1])
			i, err := strconv.Atoi(m[2])
			if !ok || err != nil {
				return ""
			}
			return cidOf[id][i]
		})
	}
	files := slices.Concat(own, uploads, others)
	var size int
	for _, f := range files {
		size += len(f.Data)
	}
	if size > maxSendBytes {
		return nil, httpx.E(422, "attachments_too_large", "Attach at most 25 MB of files.")
	}

	msgID := bare(in.MessageID)
	if msgID == "" {
		domain := from.Email[strings.LastIndex(from.Email, "@")+1:]
		msgID = ids.New().String() + "@" + domain
	}
	msg := &email.OutMessage{From: from.address(), To: to, Cc: cc, Bcc: bcc,
		Subject: subject, MessageID: "<" + msgID + ">", HTML: body, Text: email.HTMLToText(body), Attachments: files, Date: time.Now()}
	if from.ReplyTo != "" && !strings.EqualFold(from.ReplyTo, from.Email) {
		msg.ReplyTo = &mail.Address{Address: from.ReplyTo}
	}
	if orig != nil && in.Mode != ModeForward {
		parent := email.NormalizeMsgID(orig.MessageID)
		if parent == "" && orig.MessageID != "" {
			parent = "<" + strings.Trim(orig.MessageID, "<>") + ">"
		}
		msg.InReplyTo = parent
		refs := email.MsgIDList(strings.Join(orig.Refs, " "))
		if parent != "" {
			refs = append(refs, parent)
		}
		if len(refs) > 50 {
			refs = refs[len(refs)-50:]
		}
		msg.References = refs
	}
	if d := in.Draft; d != nil {
		// A draft saved again answers what it answered (it may name a message opendesk never imported).
		msg.InReplyTo, msg.References = "", nil
		if id := bare(d.InReplyTo); id != "" {
			msg.InReplyTo = "<" + id + ">"
		}
		for _, r := range d.Refs {
			if id := bare(r); id != "" {
				msg.References = append(msg.References, "<"+id+">")
			}
		}
	}
	raw, err := email.BuildMIME(msg, true)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	return &composed{Raw: raw, Recipients: slices.Concat(to, cc, bcc), To: to, Cc: cc, Bcc: bcc, Subject: subject, HTML: body,
		MessageID: msgID, HasFiles: len(files) > 0, From: from.Email}, nil
}
