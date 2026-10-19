package collab

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Drafts (contracts/collab.md "Drafts"): saved at the provider first, then stored like
// synced mail (draft set, in the Drafts folder, a reply in its conversation). The same
// visibility, act rules and checks as sending; saving again keeps the draft's id.

// draftsPerMinute bounds the draft saves one person makes per minute (the composer saves
// a couple of seconds after typing stops).
const draftsPerMinute = 120

var errDraftsUnsupported = httpx.E(422, "drafts_unsupported", "This account cannot keep drafts.")

// draftContent is what a save of an existing draft carries.
type draftContent struct {
	To            []string    `json:"to" maxItems:"100"`
	Cc            []string    `json:"cc,omitempty" maxItems:"100"`
	Bcc           []string    `json:"bcc,omitempty" maxItems:"100"`
	Subject       string      `json:"subject" maxLength:"998"`
	BodyHTML      string      `json:"body_html" maxLength:"2097152"`
	AttachmentIDs []uuid.UUID `json:"attachment_ids,omitempty" maxItems:"50" doc:"POST /v1/attachments uploads of the caller"`
	// KeepAttachments are the draft's own files to keep (MailAttachment ids); left out keeps
	// every file that is not an inline image. Inline images stay while the HTML shows them.
	KeepAttachments []string `json:"keep_attachments,omitempty" maxItems:"100" doc:"The draft's own files to keep (MailAttachment ids); left out keeps them all"`
}

type draftCreateInput struct {
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

type draftIDInput struct {
	ID string `path:"id"`
}

type messageBody struct {
	Body MailMessage
}

func (s *service) registerDraftRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "create-collab-draft", Method: http.MethodPost, Path: "/v1/collab/drafts",
		Summary: "Save a new draft"},
		func(ctx context.Context, in *draftCreateInput) (*messageBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.d.Limiter.Allow("collab-draft:"+p.UserID.String(), draftsPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			b := in.Body
			t, err := s.composeTarget(ctx, p, b.AccountID, b.Mode, b.MessageID)
			if err != nil {
				return nil, err
			}
			var threadID *uuid.UUID
			threadRemote := ""
			if t.thread != nil && b.Mode != ModeForward {
				threadID, threadRemote = &t.thread.ID, t.thread.RemoteID
			}
			id, _, err := s.saveDraft(ctx, p, t, nil, threadRemote, threadID, composeIn{Mode: b.Mode, To: b.To, Cc: b.Cc, Bcc: b.Bcc,
				Subject: b.Subject, BodyHTML: b.BodyHTML, AttachmentIDs: b.AttachmentIDs, ForwardAttachments: b.ForwardAttachments})
			if err != nil {
				return nil, err
			}
			out, err := s.messageByID(ctx, p, t.a, id)
			if err != nil {
				return nil, err
			}
			return &messageBody{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "update-collab-draft", Method: http.MethodPut, Path: "/v1/collab/drafts/{id}",
		Summary: "Save a draft again"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body draftContent
		}) (*messageBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.d.Limiter.Allow("collab-draft:"+p.UserID.String(), draftsPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			m, a, t, err := s.loadDraft(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			id, _, err := s.saveDraft(ctx, p, target{a: a}, m, t.RemoteID, &t.ID, draftCompose(m, &in.Body, false))
			if err != nil {
				return nil, err
			}
			out, err := s.messageByID(ctx, p, a, id)
			if err != nil {
				return nil, err
			}
			return &messageBody{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "delete-collab-draft", Method: http.MethodDelete, Path: "/v1/collab/drafts/{id}",
		Summary: "Discard a draft", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *draftIDInput) (*struct{}, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			m, a, t, err := s.loadDraft(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			prov, dp, err := s.drafter(ctx, a)
			if err != nil {
				return nil, err
			}
			defer prov.Close()
			pctx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			if err := dp.DeleteDraft(pctx, m.ref(t.RemoteID), bare(m.MessageID)); err != nil {
				return nil, errProvider(err)
			}
			pass, err := s.newPass(ctx, s.d.DB.Pool, a)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := pass.batch(ctx, func(tx pgx.Tx) error { return pass.remove(ctx, tx, storedOf(m)) }); err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	collabOp(api, huma.Operation{OperationID: "send-collab-draft", Method: http.MethodPost, Path: "/v1/collab/drafts/{id}/send",
		Summary: "Send a draft"},
		func(ctx context.Context, in *struct {
			ID   string        `path:"id"`
			Body *draftContent `doc:"What the composer shows: saved first. Left out: the draft is sent as saved"`
		}) (*messageBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.d.Limiter.Allow("collab-send:"+p.UserID.String(), sendPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			m, a, t, err := s.loadDraft(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			var raw []byte
			var recipients []string
			if in.Body != nil {
				id, out, err := s.saveDraft(ctx, p, target{a: a}, m, t.RemoteID, &t.ID, draftCompose(m, in.Body, true))
				if err != nil {
					return nil, err
				}
				if m, err = scanMessage(s.d.DB.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m
					WHERE m.workspace_id = $1 AND m.id = $2`, p.WorkspaceID, id)); err != nil {
					return nil, httpx.Wrap(err)
				}
				raw, recipients = out.Raw, out.Recipients
			} else {
				if raw, err = s.loadRaw(ctx, a, m, t.RemoteID); err != nil {
					return nil, errSend(safeError(err))
				}
				if recipients = recipientsOf(raw); len(recipients) == 0 {
					return nil, httpx.Validation("Add a recipient.")
				}
				if len(recipients) > maxRecipients {
					return nil, httpx.Validation("Send to at most 100 people.")
				}
			}
			prov, dp, err := s.drafter(ctx, a)
			if err != nil {
				return nil, err
			}
			defer prov.Close()
			sctx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			ref, err := dp.SendDraft(sctx, m.ref(t.RemoteID), bare(m.MessageID), raw, SendInfo{From: a.Email, Recipients: recipients,
				ThreadRemoteID: t.RemoteID})
			if err != nil {
				s.log.Warn("collab draft send refused", "account_id", a.ID, "err", err)
				return nil, errSend(err.Error())
			}
			id, err := s.storeSentDraft(ctx, a, raw, ref, p.UserID, m)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.messageByID(ctx, p, a, id)
			if err != nil {
				return nil, err
			}
			return &messageBody{out}, nil
		})
}

// draftCompose is the compose input of a draft saved again: its Message-ID, its replies'
// headers and the files it keeps.
func draftCompose(m *messageRow, c *draftContent, send bool) composeIn {
	keep := []int{}
	if c.KeepAttachments == nil {
		for _, at := range m.Attachments {
			if !at.Inline {
				keep = append(keep, at.Index)
			}
		}
	} else {
		for _, id := range c.KeepAttachments {
			if i, err := strconv.Atoi(id); err == nil {
				for _, at := range m.Attachments {
					if at.Index == i {
						keep = append(keep, i)
					}
				}
			}
		}
	}
	return composeIn{To: c.To, Cc: c.Cc, Bcc: c.Bcc, Subject: c.Subject, BodyHTML: c.BodyHTML, AttachmentIDs: c.AttachmentIDs,
		MessageID: m.MessageID, Draft: m, Keep: keep, NeedRecipients: send}
}

// loadDraft loads a draft the caller sees, with its account and conversation; anything
// else (a message that is not a draft included) is not found.
func (s *service) loadDraft(ctx context.Context, p *auth.Principal, rawID string) (*messageRow, *accountRow, *threadRow, error) {
	id, err := parseID(rawID, "Draft")
	if err != nil {
		return nil, nil, nil, err
	}
	pool := s.d.DB.Pool
	m, err := scanMessage(pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m
		WHERE m.workspace_id = $1 AND m.id = $2 AND m.draft`, p.WorkspaceID, id))
	if err != nil {
		return nil, nil, nil, notFound(err, "Draft")
	}
	t, err := visibleThread(ctx, pool, p, m.ThreadID)
	if err != nil {
		return nil, nil, nil, notFound(err, "Draft")
	}
	a, err := loadAccount(ctx, pool, p.WorkspaceID, m.AccountID)
	if err != nil {
		return nil, nil, nil, notFound(err, "Draft")
	}
	return m, a, t, nil
}

// drafter opens the account's provider as one that keeps drafts. Close the Provider.
func (s *service) drafter(ctx context.Context, a *accountRow) (Provider, draftProvider, error) {
	prov, err := s.provider(ctx, a)
	if err != nil {
		return nil, nil, errProvider(err)
	}
	dp, ok := prov.(draftProvider)
	if !ok {
		prov.Close()
		return nil, nil, errDraftsUnsupported
	}
	return prov, dp, nil
}

// saveDraft composes a draft, saves it at the provider (a new version of draft when set)
// and stores it. It returns the stored id (a draft saved again keeps its own).
func (s *service) saveDraft(ctx context.Context, p *auth.Principal, t target, draft *messageRow, threadRemote string, threadID *uuid.UUID,
	in composeIn) (uuid.UUID, *composed, error) {
	out, err := s.compose(ctx, p, t, in)
	if err != nil {
		return uuid.Nil, nil, err
	}
	prov, dp, err := s.drafter(ctx, t.a)
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer prov.Close()
	d := draftSave{Raw: out.Raw, ThreadRemoteID: threadRemote, MessageID: out.MessageID,
		Fields: &draftFields{Subject: out.Subject, HTML: out.HTML, To: out.To, Cc: out.Cc, Bcc: out.Bcc,
			Files: out.HasFiles || (draft != nil && len(draft.Attachments) > 0)}}
	if draft != nil {
		ref := draft.ref(threadRemote)
		d.Existing = &ref
	}
	pctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	ref, err := dp.SaveDraft(pctx, d)
	if err != nil {
		s.log.Info("collab draft not saved", "account_id", t.a.ID, "reason", safeError(err))
		return uuid.Nil, nil, errProvider(err)
	}
	id, err := s.storeDraft(ctx, t.a, out.Raw, ref, p.UserID, threadID, draft)
	if err != nil {
		return uuid.Nil, nil, httpx.Wrap(err)
	}
	return id, out, nil
}

// storedOf is the part of a stored message the sync's writes need.
func storedOf(m *messageRow) *storedMsg {
	return &storedMsg{ID: m.ID, ThreadID: m.ThreadID, RemoteID: m.RemoteID, MessageID: m.MessageID, Unread: m.Unread, Starred: m.Starred,
		Draft: m.Draft, RawKey: m.RawKey, Location: m.Location, FolderIDs: m.FolderIDs}
}

// storeDraft stores a draft the provider saved: in Drafts, written by the caller, in its
// conversation (threadID; for a draft saved again, the one it was in). A draft saved
// again replaces its previous version under the same id, in one transaction with the
// collab.changed it publishes.
func (s *service) storeDraft(ctx context.Context, a *accountRow, raw []byte, ref draftRef, by uuid.UUID, threadID *uuid.UUID,
	replace *messageRow) (uuid.UUID, error) {
	p, err := s.newPass(ctx, s.d.DB.Pool, a)
	if err != nil {
		return uuid.Nil, err
	}
	folders := p.folderIDs(ref.FolderRemoteIDs)
	if len(folders) == 0 {
		if f := p.roleFolder(RoleDrafts); f != nil {
			folders = []uuid.UUID{f.ID}
		}
	}
	in := &newMessage{RemoteID: ref.RemoteID, ThreadRemoteID: ref.ThreadRemoteID, Raw: raw, Date: time.Now(),
		State: state{Draft: true, FolderIDs: folders, Location: ref.Location}, SentBy: &by, ThreadID: threadID}
	if replace != nil {
		id := replace.ID
		in.ID = &id
		if threadID == nil {
			in.ThreadID = &replace.ThreadID
		}
	}
	var id uuid.UUID
	err = p.batch(ctx, func(tx pgx.Tx) error {
		if replace != nil {
			if err := p.remove(ctx, tx, storedOf(replace)); err != nil {
				return err
			}
			// The new version stored by a sync meanwhile: this row takes its place.
			if ref.RemoteID != replace.RemoteID {
				other, err := p.lookup(ctx, tx, ref.RemoteID)
				if err != nil {
					return err
				}
				if other != nil {
					if err := p.remove(ctx, tx, other); err != nil {
						return err
					}
				}
			}
		}
		var err error
		if id, err = p.insert(ctx, tx, in); err != nil || replace == nil || replace.RawKey == "" {
			return err
		}
		// The new version's raw source has the same key (it is named by the id) and was
		// just written over the old one: it stays.
		_, err = tx.Exec(ctx, `DELETE FROM collab_storage_trash WHERE key = $1
			AND EXISTS (SELECT 1 FROM collab_messages WHERE id = $2 AND raw_key = $1)`, replace.RawKey, id)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	if id == uuid.Nil {
		return uuid.Nil, httpx.E(422, "validation_error", "The draft could not be read back to store it.")
	}
	return id, nil
}

// storeSentDraft stores a sent draft as the sent message, in the draft's conversation and
// in Sent, and removes the draft, in one transaction.
func (s *service) storeSentDraft(ctx context.Context, a *accountRow, raw []byte, ref SentRef, by uuid.UUID, draft *messageRow) (uuid.UUID, error) {
	p, err := s.newPass(ctx, s.d.DB.Pool, a)
	if err != nil {
		return uuid.Nil, err
	}
	remote := ref.RemoteID
	if remote == "" {
		msgID := ""
		if pm, err := email.ParseMail(raw); err == nil {
			msgID = pm.MessageID
		}
		remote = imapRemoteID(msgID, raw)
		if a.Provider != ProviderIMAP {
			remote = "sent:" + remote
		}
	}
	folders := p.folderIDs(ref.FolderRemoteIDs)
	if len(folders) == 0 {
		if f := p.roleFolder(RoleSent); f != nil {
			folders = []uuid.UUID{f.ID}
		}
	}
	thread := draft.ThreadID
	in := &newMessage{RemoteID: remote, ThreadRemoteID: ref.ThreadRemoteID, Raw: raw, Date: time.Now(),
		State: state{FolderIDs: folders, Location: ref.Location}, SentBy: &by, ThreadID: &thread}
	var id uuid.UUID
	err = p.batch(ctx, func(tx pgx.Tx) error {
		if err := p.remove(ctx, tx, storedOf(draft)); err != nil {
			return err
		}
		var err error
		id, err = p.insert(ctx, tx, in)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	if id == uuid.Nil {
		return uuid.Nil, httpx.E(422, "validation_error", "The message could not be read back to store it.")
	}
	return id, nil
}
