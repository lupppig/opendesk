package collab

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Demo accounts are sample data (internal/sample): mail accounts whose mail exists only in
// opendesk. Their provider is local (demoProvider): it never dials a server and never
// sends, so mail sent from one is stored in Sent and goes nowhere; actions, folders and
// drafts change only what opendesk keeps. The sync engine skips them. Their mail is
// written through the same store as synced mail (pass.insert): parsed, sanitized and
// threaded once.

// demoBatch is how many conversations one transaction stores.
const demoBatch = 25

// DemoAccount is a demo mail account to create.
type DemoAccount struct {
	OwnerID       uuid.UUID
	Email         string
	Name          string
	DisplayName   string
	Color         string
	SignatureHTML string
	// Shared shares the account; empty keeps it private to its owner.
	Shared Audience
	// Folders are user folders besides the special ones (Inbox, Sent, Drafts, Archive, Spam
	// and Trash), by path.
	Folders []string
}

// DemoMessage is one message of a demo conversation.
type DemoMessage struct {
	From    Address
	To, Cc  []Address
	Subject string
	HTML    string
	Date    time.Time
	Unread  bool
	Starred bool
	Draft   bool
	SentBy  *uuid.UUID
	// Folders are where it is: folder roles (inbox, sent, archive, ...) or user folder paths.
	Folders []string
}

// DemoComment is a teammate's comment on a demo conversation.
type DemoComment struct {
	AuthorID uuid.UUID
	BodyHTML string
	Mentions []uuid.UUID
	At       time.Time
}

// DemoThread is a demo conversation: its messages in order, and how the team handles it.
type DemoThread struct {
	Messages   []DemoMessage
	SharedWith Audience
	Assignees  []uuid.UUID
	Comments   []DemoComment
}

// CreateDemoAccount creates a demo account (provider "imap", config demo) with its folders,
// ready to use, and announces it to who sees it.
func CreateDemoAccount(ctx context.Context, d *deps.Deps, wid uuid.UUID, in DemoAccount) (uuid.UUID, error) {
	s := newService(d)
	addr, err := mail.ParseAddress(in.Email)
	if err != nil || !strings.HasSuffix(strings.ToLower(addr.Address), ".invalid") {
		return uuid.Nil, fmt.Errorf("a demo account needs an address on a .invalid domain")
	}
	name := displayname.CleanOr(in.Name, displayname.Workspace, addr.Address)
	display := displayname.CleanOr(in.DisplayName, displayname.Person, addr.Address)
	color := in.Color
	if !containsFold(Colors, color) {
		color = "blue"
	}
	visibility := VisibilityPrivate
	if !in.Shared.Empty() {
		visibility = VisibilityShared
	}
	id := ids.New()
	err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO collab_accounts (id, workspace_id, owner_id, provider, email, name, display_name, color,
			visibility, signature_html, config, status, last_synced_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'active', now())`,
			id, wid, in.OwnerID, ProviderIMAP, strings.ToLower(addr.Address), name, display, color, visibility,
			hd.SanitizeSignature(in.SignatureHTML), accountConfig{Demo: true})
		if err != nil {
			return err
		}
		a, err := loadAccount(ctx, tx, wid, id)
		if err != nil {
			return err
		}
		folders := []RemoteFolder{
			{RemoteID: "INBOX", Name: "Inbox", Path: "INBOX", Role: RoleInbox},
			{RemoteID: "Sent", Name: "Sent", Path: "Sent", Role: RoleSent},
			{RemoteID: "Drafts", Name: "Drafts", Path: "Drafts", Role: RoleDrafts},
			{RemoteID: "Archive", Name: "Archive", Path: "Archive", Role: RoleArchive},
			{RemoteID: "Junk", Name: "Spam", Path: "Junk", Role: RoleSpam},
			{RemoteID: "Trash", Name: "Trash", Path: "Trash", Role: RoleTrash},
		}
		for _, path := range in.Folders {
			path = strings.Trim(strings.TrimSpace(path), "/")
			if path == "" {
				continue
			}
			folders = append(folders, RemoteFolder{RemoteID: path, Name: path[strings.LastIndex(path, "/")+1:], Path: path})
		}
		for _, f := range folders {
			f.Kind = KindFolder
			if _, err := s.upsertFolder(ctx, tx, a, f); err != nil {
				return err
			}
		}
		if visibility == VisibilityShared {
			audience, err := checkAudience(ctx, tx, wid, in.Shared)
			if err != nil {
				return err
			}
			if err := saveAudience(ctx, tx, wid, &id, nil, audience, in.OwnerID); err != nil {
				return err
			}
		}
		return s.publishAccounts(ctx, tx, wid, id, nil)
	})
	return id, err
}

// AddDemoThreads stores conversations in a demo account, demoBatch per transaction, each
// batch announced with one collab.changed. It returns how many conversations it stored.
func AddDemoThreads(ctx context.Context, d *deps.Deps, wid, accountID uuid.UUID, threads []DemoThread) (int, error) {
	s := newService(d)
	a, err := loadAccount(ctx, d.DB.Pool, wid, accountID)
	if err != nil {
		return 0, err
	}
	if !a.Config.Demo {
		return 0, fmt.Errorf("account %s is not a demo account", accountID)
	}
	p, err := s.newPass(ctx, d.DB.Pool, a)
	if err != nil {
		return 0, err
	}
	stored := 0
	for start := 0; start < len(threads); start += demoBatch {
		chunk := threads[start:min(start+demoBatch, len(threads))]
		err := p.batch(ctx, func(tx pgx.Tx) error {
			for _, t := range chunk {
				ok, err := s.insertDemoThread(ctx, tx, p, t)
				if err != nil {
					return err
				}
				if ok {
					stored++
				}
			}
			return nil
		})
		if err != nil {
			return stored, err
		}
	}
	return stored, nil
}

// insertDemoThread stores one conversation: its messages through the store, then its
// shares, assignees and comments.
func (s *service) insertDemoThread(ctx context.Context, tx pgx.Tx, p *pass, t DemoThread) (bool, error) {
	a := p.a
	key := "demo:" + ids.New().String()
	domain := a.Email[strings.LastIndex(a.Email, "@")+1:]
	var threadID uuid.UUID
	var refs []string
	for _, m := range t.Messages {
		msgID := "<" + ids.New().String() + "@" + domain + ">"
		out := &email.OutMessage{From: mail.Address{Name: m.From.Name, Address: m.From.Email}, To: addressStrings(m.To),
			Cc: addressStrings(m.Cc), Subject: m.Subject, MessageID: msgID, HTML: m.HTML, Text: email.HTMLToText(m.HTML), Date: m.Date}
		if len(refs) > 0 {
			out.InReplyTo, out.References = refs[len(refs)-1], append([]string{}, refs...)
		}
		raw, err := email.BuildMIME(out, true)
		if err != nil {
			return false, err
		}
		folders := []uuid.UUID{}
		for _, name := range m.Folders {
			f := p.roleFolder(name)
			if f == nil {
				f = p.folders[name]
			}
			if f != nil {
				folders = addID(folders, f.ID)
			}
		}
		in := &newMessage{ThreadRemoteID: key, Raw: raw, Date: m.Date, SentBy: m.SentBy,
			State: state{Unread: m.Unread, Starred: m.Starred, Draft: m.Draft, FolderIDs: folders}}
		if threadID != uuid.Nil {
			in.ThreadID = &threadID
		}
		id, err := p.insert(ctx, tx, in)
		if err != nil {
			return false, err
		}
		if id == uuid.Nil {
			continue
		}
		if threadID == uuid.Nil {
			if err := tx.QueryRow(ctx, `SELECT thread_id FROM collab_messages WHERE id = $1`, id).Scan(&threadID); err != nil {
				return false, err
			}
		}
		refs = append(refs, msgID)
	}
	if threadID == uuid.Nil {
		return false, nil
	}
	by := uuid.Nil
	if a.OwnerID != nil {
		by = *a.OwnerID
	}
	if !t.SharedWith.Empty() {
		audience, err := checkAudience(ctx, tx, a.WorkspaceID, t.SharedWith)
		if err != nil {
			return false, err
		}
		if err := saveAudience(ctx, tx, a.WorkspaceID, nil, &threadID, audience, by); err != nil {
			return false, err
		}
	}
	for _, uid := range t.Assignees {
		if _, err := tx.Exec(ctx, `INSERT INTO collab_assignees (thread_id, user_id, assigned_by)
			SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM members WHERE workspace_id = $4 AND user_id = $2) ON CONFLICT DO NOTHING`,
			threadID, uid, nilIfZero(by), a.WorkspaceID); err != nil {
			return false, err
		}
	}
	for _, c := range t.Comments {
		body, err := cleanComment(c.BodyHTML)
		if err != nil {
			continue
		}
		mentions := c.Mentions
		if mentions == nil {
			mentions = []uuid.UUID{}
		}
		at := c.At
		if at.IsZero() || at.After(time.Now()) {
			at = time.Now()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO collab_comments (id, workspace_id, thread_id, author_id, body_html, mentions, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`, ids.New(), a.WorkspaceID, threadID, c.AuthorID, body, mentions, at); err != nil {
			return false, err
		}
	}
	return true, nil
}

func addressStrings(list []Address) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, (&mail.Address{Name: a.Name, Address: a.Email}).String())
	}
	return out
}

func nilIfZero(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// demoProvider is a demo account's provider: everything stays in opendesk.
type demoProvider struct {
	s *service
	a *accountRow
}

// Folders answers what is stored, so a folder refresh changes nothing.
func (p *demoProvider) Folders(ctx context.Context) ([]RemoteFolder, error) {
	list, err := loadFolders(ctx, p.s.d.DB.Pool, p.a.ID)
	if err != nil {
		return nil, err
	}
	out := make([]RemoteFolder, 0, len(list))
	for _, f := range list {
		out = append(out, f.remote())
	}
	return out, nil
}

// Apply has nothing to change: opendesk already applied the action to what it keeps.
func (p *demoProvider) Apply(context.Context, ApplyOp) error { return nil }

// Send sends nothing; the caller stores the message in Sent.
func (p *demoProvider) Send(context.Context, []byte, SendInfo) (SentRef, error) {
	return SentRef{}, nil
}

func (p *demoProvider) FetchRaw(context.Context, MessageRef) ([]byte, error) {
	return nil, &providerError{msg: "This sample message has no original to download.", permanent: true, notFound: true}
}

func (p *demoProvider) CreateFolder(_ context.Context, name string, parent *RemoteFolder) (RemoteFolder, error) {
	path := name
	if parent != nil {
		path = parent.Path + "/" + name
	}
	return RemoteFolder{RemoteID: "demo:" + ids.New().String(), Name: name, Path: path, Kind: KindFolder}, nil
}

func (p *demoProvider) RenameFolder(_ context.Context, f RemoteFolder, name string) (RemoteFolder, error) {
	return RemoteFolder{RemoteID: f.RemoteID, Name: name}, nil
}

func (p *demoProvider) DeleteFolder(context.Context, RemoteFolder) error { return nil }

func (p *demoProvider) Close() {}

// SaveDraft keeps the draft in opendesk only (storeDraft puts it in Drafts).
func (p *demoProvider) SaveDraft(context.Context, draftSave) (draftRef, error) {
	return draftRef{RemoteID: "demo-draft:" + ids.New().String()}, nil
}

func (p *demoProvider) DeleteDraft(context.Context, MessageRef, string) error { return nil }

// SendDraft sends nothing; the caller stores the message in Sent.
func (p *demoProvider) SendDraft(context.Context, MessageRef, string, []byte, SendInfo) (SentRef, error) {
	return SentRef{}, nil
}
