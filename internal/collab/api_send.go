package collab

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Sending (contracts/collab.md "POST /v1/collab/messages").
const (
	ModeNew      = "new"
	ModeReply    = "reply"
	ModeReplyAll = "reply_all"
	ModeForward  = "forward"
)

// sendPerMinute bounds the messages one person sends per minute.
const sendPerMinute = 60

// maxSendHTML bounds a sent body.
const maxSendHTML = 2 << 20

type sendInput struct {
	Body struct {
		AccountID          uuid.UUID   `json:"account_id"`
		Mode               string      `json:"mode" enum:"new,reply,reply_all,forward"`
		MessageID          *uuid.UUID  `json:"message_id,omitempty" doc:"The message replied to or forwarded; required unless new"`
		To                 []string    `json:"to" maxItems:"100"`
		Cc                 []string    `json:"cc,omitempty" maxItems:"100"`
		Bcc                []string    `json:"bcc,omitempty" maxItems:"100"`
		Subject            string      `json:"subject" maxLength:"998"`
		BodyHTML           string      `json:"body_html" maxLength:"2097152"`
		AttachmentIDs      []uuid.UUID `json:"attachment_ids,omitempty" maxItems:"50" doc:"POST /v1/attachments uploads of the caller"`
		ForwardAttachments bool        `json:"forward_attachments,omitempty" doc:"Forward: attach the original's files"`
	}
}

func errSend(detail string) error { return httpx.E(502, "send_failed", detail) }

func (s *service) registerSendRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "send-collab-message", Method: http.MethodPost, Path: "/v1/collab/messages",
		Summary: "Send mail from an account"},
		func(ctx context.Context, in *sendInput) (*struct{ Body MailMessage }, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.d.Limiter.Allow("collab-send:"+p.UserID.String(), sendPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			out, err := s.send(ctx, p, in)
			if err != nil {
				return nil, err
			}
			return &struct{ Body MailMessage }{out}, nil
		})
}

func (s *service) send(ctx context.Context, p *auth.Principal, in *sendInput) (MailMessage, error) {
	b := in.Body
	t, err := s.composeTarget(ctx, p, b.AccountID, b.Mode, b.MessageID)
	if err != nil {
		return MailMessage{}, err
	}
	out, err := s.compose(ctx, p, t, composeIn{Mode: b.Mode, To: b.To, Cc: b.Cc, Bcc: b.Bcc, Subject: b.Subject, BodyHTML: b.BodyHTML,
		AttachmentIDs: b.AttachmentIDs, ForwardAttachments: b.ForwardAttachments, NeedRecipients: true})
	if err != nil {
		return MailMessage{}, err
	}
	a := t.a

	// Sent through the provider within the request: nothing is stored when it refuses.
	prov, err := s.provider(ctx, a)
	if err != nil {
		return MailMessage{}, errSend(err.Error())
	}
	defer prov.Close()
	info := SendInfo{From: a.Email, Recipients: out.Recipients}
	var threadID *uuid.UUID
	if t.thread != nil && b.Mode != ModeForward {
		info.ThreadRemoteID = t.thread.RemoteID
		threadID = &t.thread.ID
	}
	sctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	ref, err := prov.Send(sctx, out.Raw, info)
	if err != nil {
		s.log.Warn("collab send refused", "account_id", a.ID, "err", err)
		return MailMessage{}, errSend(err.Error())
	}
	mid, err := s.storeSent(ctx, a, out.Raw, ref, p.UserID, threadID)
	if err != nil {
		return MailMessage{}, httpx.Wrap(err)
	}
	return s.messageByID(ctx, p, a, mid)
}

// messageByID loads one stored message of the account as the API shows it.
func (s *service) messageByID(ctx context.Context, p *auth.Principal, a *accountRow, id uuid.UUID) (MailMessage, error) {
	pool := s.d.DB.Pool
	m, err := scanMessage(pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m WHERE m.workspace_id = $1 AND m.id = $2`,
		p.WorkspaceID, id))
	if err != nil {
		return MailMessage{}, httpx.Wrap(err)
	}
	list, err := s.messagesOut(ctx, pool, a, []*messageRow{m})
	if err != nil {
		return MailMessage{}, httpx.Wrap(err)
	}
	return list[0], nil
}

// uploadedFiles reads the caller's uploads. Inline images the HTML shows by their upload
// URL are sent as cid: parts so they keep showing in the recipient's mail client.
func (s *service) uploadedFiles(ctx context.Context, wid uuid.UUID, attIDs []uuid.UUID, body string) ([]email.OutAttachment, string, error) {
	if len(attIDs) == 0 {
		return nil, body, nil
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT id, filename, content_type, storage_key, size_bytes, inline FROM attachments
		WHERE workspace_id = $1 AND id = ANY($2) ORDER BY created_at, id`, wid, attIDs)
	if err != nil {
		return nil, "", httpx.Wrap(err)
	}
	type upload struct {
		id       uuid.UUID
		filename string
		ct, key  string
		size     int64
		inline   bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (upload, error) {
		var x upload
		return x, r.Scan(&x.id, &x.filename, &x.ct, &x.key, &x.size, &x.inline)
	})
	if err != nil {
		return nil, "", httpx.Wrap(err)
	}
	var total int64
	for _, u := range list {
		total += u.size
	}
	if total > maxSendBytes {
		return nil, "", httpx.E(422, "attachments_too_large", "Attach at most 25 MB of files.")
	}
	referenced := map[uuid.UUID]string{}
	body = rewriteImages(body, func(src string) string {
		for _, u := range list {
			if strings.Contains(src, "/v1/attachments/"+u.id.String()) {
				referenced[u.id] = u.id.String() + "@opendesk"
				return referenced[u.id]
			}
		}
		return ""
	})
	out := make([]email.OutAttachment, 0, len(list))
	for _, u := range list {
		rc, err := s.d.Storage.Get(ctx, u.key)
		if err != nil {
			return nil, "", httpx.Wrap(err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxSendBytes+1))
		rc.Close()
		if err != nil {
			return nil, "", httpx.Wrap(err)
		}
		out = append(out, email.OutAttachment{Filename: u.filename, ContentType: u.ct, Data: data, ContentID: referenced[u.id]})
	}
	return out, body, nil
}

// collabImageLink is a signed link to a stored message's file (attachmentURL).
var collabImageLink = regexp.MustCompile(`/v1/collab/attachments/([0-9a-f-]{36})/(\d{1,4})\?`)

// rewriteImages points each <img> whose src cidFor names at "cid:<content id>", for
// files sent with the message.
func rewriteImages(body string, cidFor func(src string) string) string {
	doc := email.ParseHTML(body)
	if doc == nil {
		return body
	}
	changed := false
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.DataAtom == atom.Img {
			for i, at := range n.Attr {
				if strings.EqualFold(at.Key, "src") {
					if cid := cidFor(at.Val); cid != "" {
						n.Attr[i].Val, changed = "cid:"+cid, true
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if !changed {
		return body
	}
	return email.RenderHTML(doc)
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// clipRunes shortens s to n runes.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
