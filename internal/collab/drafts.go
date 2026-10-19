package collab

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/useopendesk/opendesk/internal/email"
)

// Drafts at the provider (contracts/collab.md "Drafts"): Gmail's drafts API, Graph draft
// messages, and for IMAP the Drafts folder (APPEND with \Draft \Seen; saving again
// appends the new version and expunges the old). A draft keeps its Message-ID across
// saves. Drafts made in Gmail, Outlook or another client arrive with the normal sync as
// messages with draft set, in the Drafts folder.

// draftSave is one save of a draft: the MIME (Bcc included), the conversation of a reply,
// and for a draft saved again, where its previous version is.
type draftSave struct {
	Raw            []byte
	ThreadRemoteID string
	Existing       *MessageRef
	// MessageID is the draft's bare Message-ID (Gmail finds a draft made elsewhere by it).
	MessageID string
	// Fields let Graph update a draft in place when no files are involved.
	Fields *draftFields
}

// draftFields are a draft's editable fields for an in-place update.
type draftFields struct {
	Subject     string
	HTML        string
	To, Cc, Bcc []string
	// Files: the new version has files, or the old one had: Graph makes a new draft.
	Files bool
}

// draftRef is where the provider keeps a saved draft.
type draftRef struct {
	RemoteID        string
	ThreadRemoteID  string
	Location        json.RawMessage
	FolderRemoteIDs []string
}

// draftProvider is a provider that keeps drafts. Gmail, Graph and IMAP accounts all do.
type draftProvider interface {
	// SaveDraft creates the draft, or saves a new version of d.Existing.
	SaveDraft(ctx context.Context, d draftSave) (draftRef, error)
	// DeleteDraft discards a draft; one already gone is not an error.
	DeleteDraft(ctx context.Context, ref MessageRef, messageID string) error
	// SendDraft sends a draft as saved (raw is that version, for SMTP) and reports where
	// the sent copy is.
	SendDraft(ctx context.Context, ref MessageRef, messageID string, raw []byte, info SendInfo) (SentRef, error)
}

// draftLocation is what collab_messages.location keeps of a draft opendesk saved at Gmail.
type draftLocation struct {
	Draft string `json:"draft,omitempty"` // the Gmail draft id
}

func gmailDraftOf(loc json.RawMessage) string {
	var d draftLocation
	_ = json.Unmarshal(loc, &d)
	return d.Draft
}

// ── Gmail ────────────────────────────────────────────────────────────────

// gmailDraft is a draft as the drafts API answers.
type gmailDraft struct {
	ID      string       `json:"id"`
	Message gmailMessage `json:"message"`
}

// gmailUpload is a multipart/related body: JSON metadata, then the raw message.
func gmailUpload(meta any, raw []byte) ([]byte, string) {
	metaJSON, _ := json.Marshal(meta)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "application/json; charset=UTF-8")
	pw, _ := mw.CreatePart(h)
	_, _ = pw.Write(metaJSON)
	h = textproto.MIMEHeader{}
	h.Set("Content-Type", "message/rfc822")
	pw, _ = mw.CreatePart(h)
	_, _ = pw.Write(raw)
	_ = mw.Close()
	return body.Bytes(), "multipart/related; boundary=" + mw.Boundary()
}

// draftID finds the Gmail draft of a stored message: the id opendesk saved it with, else
// by its Message-ID (a draft made in Gmail), else by listing drafts.
func (g *gmailProvider) draftID(ctx context.Context, ref MessageRef, messageID string) (string, error) {
	if id := gmailDraftOf(ref.Location); id != "" {
		return id, nil
	}
	queries := []string{}
	if messageID != "" {
		queries = append(queries, "rfc822msgid:"+messageID)
	}
	queries = append(queries, "")
	for _, q := range queries {
		token := ""
		for pages := 0; pages < 5; pages++ {
			v := url.Values{"maxResults": {"500"}}
			if q != "" {
				v.Set("q", q)
			}
			if token != "" {
				v.Set("pageToken", token)
			}
			var page struct {
				Drafts        []gmailDraft `json:"drafts"`
				NextPageToken string       `json:"nextPageToken"`
			}
			if err := g.do(ctx, http.MethodGet, "/drafts?"+v.Encode(), nil, &page); err != nil {
				return "", err
			}
			for _, d := range page.Drafts {
				if d.Message.ID == ref.RemoteID {
					return d.ID, nil
				}
			}
			if page.NextPageToken == "" {
				break
			}
			token = page.NextPageToken
		}
	}
	return "", &providerError{msg: "Gmail no longer has this draft.", notFound: true, permanent: true}
}

// SaveDraft creates a draft (users.drafts.create) or replaces its message
// (users.drafts.update), in the conversation's thread. Gmail gives the new version a new
// message id; the draft id stays.
func (g *gmailProvider) SaveDraft(ctx context.Context, d draftSave) (draftRef, error) {
	id := ""
	if d.Existing != nil {
		var err error
		if id, err = g.draftID(ctx, *d.Existing, d.MessageID); err != nil && !isNotFound(err) {
			return draftRef{}, err
		}
	}
	msg := map[string]string{}
	if d.ThreadRemoteID != "" {
		msg["threadId"] = d.ThreadRemoteID
	}
	meta := map[string]any{"message": msg}
	method, target := http.MethodPost, g.s.gmailBase+"/upload/gmail/v1/users/me/drafts?uploadType=multipart"
	if id != "" {
		meta["id"] = id
		method, target = http.MethodPut, g.s.gmailBase+"/upload/gmail/v1/users/me/drafts/"+url.PathEscape(id)+"?uploadType=multipart"
	}
	body, ct := gmailUpload(meta, d.Raw)
	resp, err := g.s.call(ctx, g.a, "Gmail", restRequest{method: method, url: target, body: body, contentType: ct})
	if isNotFound(err) && id != "" {
		// Gone meanwhile (sent or discarded elsewhere): what is written becomes a new draft.
		return g.SaveDraft(ctx, draftSave{Raw: d.Raw, ThreadRemoteID: d.ThreadRemoteID, MessageID: d.MessageID})
	}
	if err != nil {
		return draftRef{}, err
	}
	var out gmailDraft
	if err := json.Unmarshal(resp, &out); err != nil || out.ID == "" || out.Message.ID == "" {
		return draftRef{}, &providerError{msg: "Gmail did not save the draft."}
	}
	loc, _ := json.Marshal(draftLocation{Draft: out.ID})
	labels := out.Message.LabelIDs
	if len(labels) == 0 {
		labels = []string{"DRAFT"}
	}
	return draftRef{RemoteID: out.Message.ID, ThreadRemoteID: out.Message.ThreadID, Location: loc, FolderRemoteIDs: labels}, nil
}

func (g *gmailProvider) DeleteDraft(ctx context.Context, ref MessageRef, messageID string) error {
	id, err := g.draftID(ctx, ref, messageID)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	err = g.do(ctx, http.MethodDelete, "/drafts/"+url.PathEscape(id), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// SendDraft sends through users.drafts.send; Gmail files the sent copy under SENT.
func (g *gmailProvider) SendDraft(ctx context.Context, ref MessageRef, messageID string, _ []byte, _ SendInfo) (SentRef, error) {
	id, err := g.draftID(ctx, ref, messageID)
	if err != nil {
		return SentRef{}, err
	}
	var sent gmailMessage
	if err := g.do(ctx, http.MethodPost, "/drafts/send", map[string]string{"id": id}, &sent); err != nil {
		return SentRef{}, err
	}
	labels := sent.LabelIDs
	if len(labels) == 0 {
		labels = []string{"SENT"}
	}
	return SentRef{RemoteID: sent.ID, ThreadRemoteID: sent.ThreadID, FolderRemoteIDs: labels}, nil
}

// ── Microsoft Graph ──────────────────────────────────────────────────────

// roleFolder is the remote id of the account's special folder with the role.
func (g *graphProvider) roleFolder(ctx context.Context, role string) string {
	roles, err := g.wellKnown(ctx)
	if err != nil {
		return ""
	}
	for id, r := range roles {
		if r == role {
			return id
		}
	}
	return ""
}

// createFromMIME makes a draft from a MIME message (base64, text/plain).
func (g *graphProvider) createFromMIME(ctx context.Context, raw []byte) (string, string, error) {
	encoded := base64.StdEncoding.EncodeToString(raw)
	if len(encoded) > graphMaxRequest {
		return "", "", &providerError{msg: "Microsoft accepts messages up to about 3 MB (attachments included).", permanent: true}
	}
	body, err := g.s.call(ctx, g.a, "Microsoft", restRequest{method: http.MethodPost, url: g.s.graphBase + "/me/messages",
		body: []byte(encoded), contentType: "text/plain", headers: graphHeaders})
	if err != nil {
		return "", "", err
	}
	var draft struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(body, &draft); err != nil || draft.ID == "" {
		return "", "", &providerError{msg: "Microsoft did not accept the message."}
	}
	return draft.ID, draft.ConversationID, nil
}

func graphRecipients(list []string) []map[string]map[string]string {
	out := make([]map[string]map[string]string, 0, len(list))
	for _, a := range list {
		out = append(out, map[string]map[string]string{"emailAddress": {"address": a}})
	}
	return out
}

// SaveDraft creates a draft from the MIME, or saves a new version: in place (PATCH of
// subject, body and recipients) when no files are involved, else as a new draft from the
// MIME with the old one deleted.
func (g *graphProvider) SaveDraft(ctx context.Context, d draftSave) (draftRef, error) {
	drafts := []string{}
	if id := g.roleFolder(ctx, RoleDrafts); id != "" {
		drafts = []string{id}
	}
	if d.Existing != nil && d.Fields != nil && !d.Fields.Files {
		f := d.Fields
		patch := map[string]any{"subject": f.Subject, "body": map[string]string{"contentType": "html", "content": f.HTML},
			"toRecipients": graphRecipients(f.To), "ccRecipients": graphRecipients(f.Cc), "bccRecipients": graphRecipients(f.Bcc)}
		var out struct {
			ID             string `json:"id"`
			ConversationID string `json:"conversationId"`
		}
		err := g.do(ctx, http.MethodPatch, "/me/messages/"+url.PathEscape(d.Existing.RemoteID), patch, &out)
		if err == nil {
			return draftRef{RemoteID: d.Existing.RemoteID, ThreadRemoteID: out.ConversationID, FolderRemoteIDs: drafts}, nil
		}
		if !isNotFound(err) {
			return draftRef{}, err
		}
		d.Existing = nil // gone meanwhile: a new draft
	}
	id, conv, err := g.createFromMIME(ctx, d.Raw)
	if err != nil {
		return draftRef{}, err
	}
	if d.Existing != nil {
		if err := g.do(ctx, http.MethodDelete, "/me/messages/"+url.PathEscape(d.Existing.RemoteID), nil, nil); err != nil && !isNotFound(err) {
			g.s.log.Info("collab old draft version not deleted", "account_id", g.a.ID, "reason", safeError(err))
		}
	}
	return draftRef{RemoteID: id, ThreadRemoteID: conv, FolderRemoteIDs: drafts}, nil
}

func (g *graphProvider) DeleteDraft(ctx context.Context, ref MessageRef, _ string) error {
	err := g.do(ctx, http.MethodDelete, "/me/messages/"+url.PathEscape(ref.RemoteID), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// SendDraft sends the draft; the sent copy keeps its (immutable) id in Sent Items.
func (g *graphProvider) SendDraft(ctx context.Context, ref MessageRef, _ string, _ []byte, _ SendInfo) (SentRef, error) {
	if err := g.do(ctx, http.MethodPost, "/me/messages/"+url.PathEscape(ref.RemoteID)+"/send", nil, nil); err != nil {
		return SentRef{}, err
	}
	out := SentRef{RemoteID: ref.RemoteID, ThreadRemoteID: ref.ThreadRemoteID}
	if id := g.roleFolder(ctx, RoleSent); id != "" {
		out.FolderRemoteIDs = []string{id}
	}
	return out, nil
}

// ── IMAP ─────────────────────────────────────────────────────────────────

// draftsFolder is the account's Drafts folder.
func (p *imapProvider) draftsFolder(ctx context.Context) (*folderRow, error) {
	f, err := scanFolder(p.s.d.DB.Pool.QueryRow(ctx, `SELECT `+folderColumns+` FROM collab_folders WHERE account_id = $1 AND role = 'drafts'
		ORDER BY path LIMIT 1`, p.a.ID))
	if err != nil {
		return nil, &imapError{msg: "This account has no Drafts folder. Create one at the provider first.", permanent: true}
	}
	return f, nil
}

// SaveDraft appends the draft to Drafts with \Draft \Seen, then expunges the old version.
func (p *imapProvider) SaveDraft(ctx context.Context, d draftSave) (draftRef, error) {
	drafts, err := p.draftsFolder(ctx)
	if err != nil {
		return draftRef{}, err
	}
	c, err := p.session(ctx)
	if err != nil {
		return draftRef{}, err
	}
	cmd := c.Append(drafts.RemoteID, int64(len(d.Raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft, imap.FlagSeen}, Time: time.Now()})
	_, werr := cmd.Write(d.Raw)
	cerr := cmd.Close()
	data, err := cmd.Wait()
	if err == nil {
		err = werr
	}
	if err == nil {
		err = cerr
	}
	if err != nil {
		return draftRef{}, opError("saving the draft", err)
	}
	ref := draftRef{RemoteID: imapRemoteID(d.MessageID, d.Raw), FolderRemoteIDs: []string{drafts.RemoteID}}
	var fresh imapLoc
	if data != nil && data.UID != 0 {
		fresh = imapLoc{Folder: drafts.RemoteID, Validity: data.UIDValidity, UID: uint32(data.UID)}
		ref.Location = encodeLocation([]imapLoc{fresh})
	}
	if d.Existing != nil {
		old := dropUID(parseLocation(d.Existing.Location), fresh)
		if err := p.expunge(ctx, old); err != nil {
			p.s.log.Info("collab old draft version not removed", "account_id", p.a.ID, "reason", safeError(err))
		}
	}
	return ref, nil
}

// expunge removes messages for good from their folders (\Deleted, then UID EXPUNGE, or
// EXPUNGE on a server without UIDPLUS). Collab only does this to drafts.
func (p *imapProvider) expunge(ctx context.Context, locs []imapLoc) error {
	if len(locs) == 0 {
		return nil
	}
	c, err := p.session(ctx)
	if err != nil {
		return err
	}
	byFolder := map[string][]imapLoc{}
	order := []string{}
	for _, l := range locs {
		if _, ok := byFolder[l.Folder]; !ok {
			order = append(order, l.Folder)
		}
		byFolder[l.Folder] = append(byFolder[l.Folder], l)
	}
	for _, f := range order {
		sel, err := c.Select(f, nil).Wait()
		if err != nil {
			if isNonExistent(err) {
				continue
			}
			return opError("opening a folder", err)
		}
		uids := []uint32{}
		for _, l := range byFolder[f] {
			if l.Validity == 0 || l.Validity == sel.UIDValidity {
				uids = append(uids, l.UID)
			}
		}
		if len(uids) == 0 {
			continue
		}
		set := uidSet(uids)
		if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
			return opError("discarding the draft", err)
		}
		if c.Caps().Has(imap.CapUIDPlus) {
			err = c.UIDExpunge(set).Close()
		} else {
			err = c.Expunge().Close()
		}
		if err != nil {
			return opError("discarding the draft", err)
		}
	}
	return nil
}

func (p *imapProvider) DeleteDraft(ctx context.Context, ref MessageRef, _ string) error {
	return p.expunge(ctx, parseLocation(ref.Location))
}

// SendDraft submits through SMTP, appends the \Seen copy to Sent and removes the draft.
func (p *imapProvider) SendDraft(ctx context.Context, ref MessageRef, _ string, raw []byte, info SendInfo) (SentRef, error) {
	sent, err := p.Send(ctx, raw, info)
	if err != nil {
		return SentRef{}, err
	}
	if err := p.expunge(ctx, parseLocation(ref.Location)); err != nil {
		p.s.log.Info("collab sent draft not removed from Drafts", "account_id", p.a.ID, "reason", safeError(err))
	}
	return sent, nil
}

// withoutBcc drops the Bcc header of a raw message (SMTP carries Bcc in the envelope only).
func withoutBcc(raw []byte) []byte {
	end := bytes.Index(raw, []byte("\r\n\r\n"))
	sep := 4
	if end < 0 {
		if end = bytes.Index(raw, []byte("\n\n")); end < 0 {
			return raw
		}
		sep = 2
	}
	head := raw[:end]
	var out bytes.Buffer
	skipping := false
	for _, line := range bytes.SplitAfter(head, []byte("\n")) {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if !skipping {
				out.Write(line)
			}
			continue
		}
		skipping = len(line) >= 4 && strings.EqualFold(string(line[:4]), "bcc:")
		if !skipping {
			out.Write(line)
		}
	}
	h := bytes.TrimRight(out.Bytes(), "\r\n")
	nl := "\r\n"
	if sep == 2 {
		nl = "\n"
	}
	return append(append(append([]byte{}, h...), []byte(nl+nl)...), raw[end+sep:]...)
}

// recipientsOf lists a raw message's To, Cc and Bcc addresses (sending a stored draft).
func recipientsOf(raw []byte) []string {
	out := []string{}
	pm, err := email.ParseMail(raw)
	if err != nil {
		return out
	}
	for _, key := range []string{"To", "Cc", "Bcc"} {
		for _, a := range pm.AddressList(key) {
			if a.Address != "" && !containsFold(out, a.Address) {
				out = append(out, strings.ToLower(a.Address))
			}
		}
	}
	return out
}
