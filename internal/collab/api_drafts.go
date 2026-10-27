package collab

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/useopendesk/opendesk/internal/audit"
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

// What an AI credential may not do with drafts ("AI drafts").
var (
	errAIDraftMode   = httpx.E(403, "ai_draft_reply_only", "An AI credential drafts replies to a conversation only (mode reply or reply_all).")
	errAIDraftFiles  = httpx.E(403, "ai_draft_no_files", "An AI credential cannot attach files to a draft.")
	errAIDraftNotOwn = httpx.E(403, "ai_draft_not_own",
		"This draft was not written by this API key, or a person has saved it since. Save a new draft instead.")
)

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
	FromEmail       string   `json:"from_email,omitempty" maxLength:"254" doc:"One of the account's usable addresses; left out keeps the draft's"`
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
		FromEmail          string      `json:"from_email,omitempty" maxLength:"254" doc:"One of the account's usable addresses"`
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
			p, err := s.requireDrafter(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.d.Limiter.Allow("collab-draft:"+p.UserID.String(), draftsPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			b := in.Body
			if viaAIClient(p) {
				// An AI client answers a conversation, with words only ("AI drafts").
				if b.Mode != ModeReply && b.Mode != ModeReplyAll {
					return nil, errAIDraftMode
				}
				if len(b.AttachmentIDs) > 0 || b.ForwardAttachments {
					return nil, errAIDraftFiles
				}
			}
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
				Subject: b.Subject, BodyHTML: b.BodyHTML, AttachmentIDs: b.AttachmentIDs, ForwardAttachments: b.ForwardAttachments, FromEmail: b.FromEmail})
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
			p, err := s.requireDrafter(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !s.d.Limiter.Allow("collab-draft:"+p.UserID.String(), draftsPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			m, a, t, unlock, err := s.lockDraft(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			defer unlock()
			if viaAIClient(p) {
				// A key saves again only a draft it wrote and nobody has saved since: checked
				// under the draft's lock, so a person's save cannot land in between.
				if m.AIAPIKeyID == nil || *m.AIAPIKeyID != p.APIKeyID {
					return nil, errAIDraftNotOwn
				}
				if len(in.Body.AttachmentIDs) > 0 {
					return nil, errAIDraftFiles
				}
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
			m, a, t, unlock, err := s.lockDraft(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			defer unlock()
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
			m, a, t, unlock, err := s.lockDraft(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			defer unlock()
			var raw []byte
			var recipients []string
			from := a.Email
			content := in.Body
			if content == nil {
				// Sent as saved, unless its From name has variables: then it is composed again
				// from what was saved, so the name is the sender's (personal.go).
				idents, err := accountIdentities(ctx, s.d.DB.Pool, a)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if snd, ok := usableSender(a, idents, m.From.Email); ok && nameTemplated(snd.Name) {
					content = savedContent(m)
				}
			}
			if content != nil {
				id, out, err := s.saveDraft(ctx, p, target{a: a}, m, t.RemoteID, &t.ID, draftCompose(m, content, true))
				if err != nil {
					return nil, err
				}
				if m, err = scanMessage(s.d.DB.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM collab_messages m
					WHERE m.workspace_id = $1 AND m.id = $2`, p.WorkspaceID, id)); err != nil {
					return nil, httpx.Wrap(err)
				}
				raw, recipients, from = out.Raw, out.Recipients, out.From
			} else {
				// As saved: from its own From when that is one of the account's usable addresses.
				idents, err := accountIdentities(ctx, s.d.DB.Pool, a)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if snd, ok := usableSender(a, idents, m.From.Email); ok {
					from = snd.Email
				}
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
			ref, err := dp.SendDraft(sctx, m.ref(t.RemoteID), bare(m.MessageID), raw, SendInfo{From: from, Recipients: recipients,
				ThreadRemoteID: t.RemoteID})
			if err != nil {
				s.log.Warn("collab draft send refused", "account_id", a.ID, "err", err)
				return nil, errSend(safeError(err))
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
// savedContent is a draft's content as saved, for composing it again.
func savedContent(m *messageRow) *draftContent {
	emails := func(list []Address) []string {
		out := make([]string, 0, len(list))
		for _, x := range list {
			out = append(out, x.Email)
		}
		return out
	}
	return &draftContent{To: emails(m.To), Cc: emails(m.Cc), Bcc: emails(m.Bcc), Subject: m.Subject, BodyHTML: m.BodyHTML,
		FromEmail: m.From.Email}
}

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
		FromEmail: c.FromEmail, MessageID: m.MessageID, Draft: m, Keep: keep, NeedRecipients: send}
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

// draftLockWait leaves time to return a busy response within the HTTP request deadline.
const draftLockWait = 20 * time.Second

var errDraftBusy = httpx.E(409, "draft_busy", "This draft is being changed by someone else. Try again in a moment.")

// lockDraft is loadDraft for a change: saves, sends and discards of one draft run one at
// a time, on every server, from the check of who owns it to its stored result. The draft
// is loaded under the lock, so a request checks the version it changes at the provider:
// an AI client whose draft a person saved meanwhile is refused before the provider is
// called ("AI drafts"). Call unlock when done, whatever happened.
func (s *service) lockDraft(ctx context.Context, p *auth.Principal, rawID string) (*messageRow, *accountRow, *threadRow, func(), error) {
	id, err := parseID(rawID, "Draft")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, draftLockWait)
	defer cancel()
	tx, err := s.d.DB.Locks.Begin(lockCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, nil, nil, nil, errDraftBusy
		}
		return nil, nil, nil, nil, httpx.Wrap(err)
	}
	// The lock is the transaction's: rolling back (or a lost connection) releases it.
	unlock := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }
	if _, err := tx.Exec(lockCtx, `SELECT set_config('lock_timeout', $1, true)`,
		strconv.FormatInt(draftLockWait.Milliseconds(), 10)+"ms"); err != nil {
		unlock()
		return nil, nil, nil, nil, httpx.Wrap(err)
	}
	if _, err := tx.Exec(lockCtx, `SELECT pg_advisory_xact_lock(hashtextextended('collab_draft:' || $1::text, 0))`, id); err != nil {
		unlock()
		var pg *pgconn.PgError
		if (errors.As(err, &pg) && pg.Code == "55P03") || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
			return nil, nil, nil, nil, errDraftBusy
		}
		return nil, nil, nil, nil, httpx.Wrap(err)
	}
	m, a, t, err := s.loadDraft(ctx, p, rawID)
	if err != nil {
		unlock()
		return nil, nil, nil, nil, err
	}
	return m, a, t, unlock, nil
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
	ai, err := s.aiDrafterOf(ctx, p)
	if err != nil {
		return uuid.Nil, nil, httpx.Wrap(err)
	}
	ref, err := dp.SaveDraft(pctx, d)
	if err != nil {
		s.log.Info("collab draft not saved", "account_id", t.a.ID, "reason", safeError(err))
		return uuid.Nil, nil, errProvider(err)
	}
	id, err := s.storeDraft(ctx, t.a, out.Raw, ref, p.UserID, threadID, draft, ai)
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

// aiDrafterOf is the AI credential a draft the caller saves is attributed to: their API
// key, by its name now; nil for a person.
func (s *service) aiDrafterOf(ctx context.Context, p *auth.Principal) (*aiDrafter, error) {
	if !viaAIClient(p) {
		return nil, nil
	}
	ai := &aiDrafter{APIKeyID: p.APIKeyID, P: p}
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT name FROM api_keys WHERE id = $1 AND workspace_id = $2`,
		p.APIKeyID, p.WorkspaceID).Scan(&ai.Client); err != nil {
		return nil, err
	}
	return ai, nil
}

// storeDraft stores a draft the provider saved: in Drafts, written by the caller, in its
// conversation (threadID; for a draft saved again, the one it was in). A draft saved
// again replaces its previous version under the same id, in one transaction with the
// collab.changed it publishes. ai is the AI client that saved it (audit-logged), nil
// for a person, whose save takes the draft over.
func (s *service) storeDraft(ctx context.Context, a *accountRow, raw []byte, ref draftRef, by uuid.UUID, threadID *uuid.UUID,
	replace *messageRow, ai *aiDrafter) (uuid.UUID, error) {
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
		State: state{Draft: true, FolderIDs: folders, Location: ref.Location}, SentBy: &by, ThreadID: threadID, AI: ai}
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
		if id, err = p.insert(ctx, tx, in); err != nil {
			return err
		}
		if ai != nil && id != uuid.Nil {
			// The content is the client's; the entry names the conversation, never the words.
			// The actor is ai, through the key, on behalf of the member who created it.
			if err := audit.Log(ctx, tx, ai.P, a.WorkspaceID, "collab.ai_draft_saved", "collab_message", &id,
				map[string]any{"actor": "ai", "client": ai.Client, "api_key_id": ai.APIKeyID, "account_id": a.ID,
					"again": replace != nil}); err != nil {
				return err
			}
		}
		if replace == nil || replace.RawKey == "" {
			return nil
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
