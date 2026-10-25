package collab

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
)

// accountConfig holds an account's non-secret connection settings (collab_accounts.config).
type accountConfig struct {
	IMAPHost     string `json:"imap_host,omitempty"`
	IMAPPort     int    `json:"imap_port,omitempty"`
	IMAPTLS      string `json:"imap_tls,omitempty"`
	IMAPUsername string `json:"imap_username,omitempty"`
	SMTPHost     string `json:"smtp_host,omitempty"`
	SMTPPort     int    `json:"smtp_port,omitempty"`
	SMTPTLS      string `json:"smtp_tls,omitempty"`
	SMTPUsername string `json:"smtp_username,omitempty"`
	// Scopes are the OAuth scopes the provider granted, from the code exchange and each
	// refresh; nil for an account connected before they were recorded (the mail scopes only).
	Scopes []string `json:"scopes,omitempty"`
	// Settings is how the account's own settings sync with the provider (settings.go).
	// Written only with jsonb_set under the row lock, so it never overwrites the rest.
	Settings *settingsState `json:"settings,omitempty"`
	// Demo marks sample data (demo.go): its mail lives only in opendesk, and it never
	// connects to a provider or sends.
	Demo bool `json:"demo,omitempty"`
}

// credentials is the sealed secret blob (collab_accounts.credentials_enc, d.Box).
type credentials struct {
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Password     string    `json:"password,omitempty"`
	SMTPPassword string    `json:"smtp_password,omitempty"`
}

// smtpPassword is the SMTP password, the IMAP one when it has none of its own.
func (c credentials) smtpPassword() string {
	if c.SMTPPassword != "" {
		return c.SMTPPassword
	}
	return c.Password
}

// accountRow is a stored account, including what the API never returns.
type accountRow struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	OwnerID        *uuid.UUID
	Provider       string
	Email          string
	Name           string
	DisplayName    string
	Color          string
	Visibility     string
	SignatureHTML  string
	Config         accountConfig
	CredentialsEnc []byte
	SyncState      json.RawMessage
	Status         string
	Reconnect      bool
	LastError      string
	LastSyncedAt   *time.Time
	SyncFailures   int
	NextSyncAt     *time.Time
	CreatedAt      time.Time
}

const accountColumns = `id, workspace_id, owner_id, provider, email, name, display_name, color, visibility, signature_html,
	config, credentials_enc, sync_state, status, reconnect, last_error, last_synced_at, sync_failures, next_sync_at, created_at`

func scanAccount(row pgx.Row) (*accountRow, error) {
	var a accountRow
	err := row.Scan(&a.ID, &a.WorkspaceID, &a.OwnerID, &a.Provider, &a.Email, &a.Name, &a.DisplayName, &a.Color, &a.Visibility,
		&a.SignatureHTML, &a.Config, &a.CredentialsEnc, &a.SyncState, &a.Status, &a.Reconnect, &a.LastError, &a.LastSyncedAt,
		&a.SyncFailures, &a.NextSyncAt, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// loadAccount loads one account of the workspace, whoever may see it.
func loadAccount(ctx context.Context, q db.Querier, wid, id uuid.UUID) (*accountRow, error) {
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM collab_accounts WHERE workspace_id = $1 AND id = $2`, wid, id))
}

// loadAccountByID loads an account for a background job.
func loadAccountByID(ctx context.Context, q db.Querier, id uuid.UUID) (*accountRow, error) {
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM collab_accounts WHERE id = $1`, id))
}

// loadAccountForUpdate locks the row (token refresh, sync state).
func loadAccountForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*accountRow, error) {
	return scanAccount(tx.QueryRow(ctx, `SELECT `+accountColumns+` FROM collab_accounts WHERE id = $1 FOR UPDATE`, id))
}

func (s *service) openCreds(a *accountRow) (credentials, error) {
	var c credentials
	if len(a.CredentialsEnc) == 0 {
		return c, nil
	}
	if err := s.d.Box.OpenJSON(a.CredentialsEnc, &c); err != nil {
		return c, errors.New("the account credentials could not be opened")
	}
	return c, nil
}

func (s *service) sealCreds(c credentials) ([]byte, error) { return s.d.Box.SealJSON(c) }

// folderRow is a stored folder or label.
type folderRow struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	AccountID   uuid.UUID
	RemoteID    string
	Name        string
	Path        string
	Role        *string
	Kind        string
	Color       *string
	SyncState   json.RawMessage
}

const folderColumns = `id, workspace_id, account_id, remote_id, name, path, role, kind, color, sync_state`

func scanFolder(row pgx.Row) (*folderRow, error) {
	var f folderRow
	if err := row.Scan(&f.ID, &f.WorkspaceID, &f.AccountID, &f.RemoteID, &f.Name, &f.Path, &f.Role, &f.Kind, &f.Color, &f.SyncState); err != nil {
		return nil, err
	}
	return &f, nil
}

// loadFolders loads an account's folders.
func loadFolders(ctx context.Context, q db.Querier, accountID uuid.UUID) ([]*folderRow, error) {
	rows, err := q.Query(ctx, `SELECT `+folderColumns+` FROM collab_folders WHERE account_id = $1 ORDER BY path`, accountID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*folderRow, error) { return scanFolder(r) })
}

// loadFolder loads one folder of the workspace.
func loadFolder(ctx context.Context, q db.Querier, wid, id uuid.UUID) (*folderRow, error) {
	return scanFolder(q.QueryRow(ctx, `SELECT `+folderColumns+` FROM collab_folders WHERE workspace_id = $1 AND id = $2`, wid, id))
}

// remote is the folder as the provider names it.
func (f *folderRow) remote() RemoteFolder {
	r := RemoteFolder{RemoteID: f.RemoteID, Name: f.Name, Path: f.Path, Kind: f.Kind}
	if f.Role != nil {
		r.Role = *f.Role
	}
	if f.Color != nil {
		r.Color = *f.Color
	}
	return r
}

// threadRow is a stored conversation.
type threadRow struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	AccountID      uuid.UUID
	RemoteID       string
	Subject        string
	Snippet        string
	Participants   []Address
	LastMessageAt  time.Time
	MessageCount   int
	Unread         bool
	Starred        bool
	HasAttachments bool
	FolderIDs      []uuid.UUID
	// Addresses are every address on its messages, lower case, newest first.
	Addresses []string
}

const threadColumns = `id, workspace_id, account_id, remote_id, subject, snippet, participants, last_message_at, message_count,
	unread, starred, has_attachments, folder_ids, addresses`

func scanThread(row pgx.Row) (*threadRow, error) {
	var t threadRow
	if err := row.Scan(&t.ID, &t.WorkspaceID, &t.AccountID, &t.RemoteID, &t.Subject, &t.Snippet, &t.Participants, &t.LastMessageAt,
		&t.MessageCount, &t.Unread, &t.Starred, &t.HasAttachments, &t.FolderIDs, &t.Addresses); err != nil {
		return nil, err
	}
	if t.Participants == nil {
		t.Participants = []Address{}
	}
	if t.FolderIDs == nil {
		t.FolderIDs = []uuid.UUID{}
	}
	if t.Addresses == nil {
		t.Addresses = []string{}
	}
	return &t, nil
}

// storedAttachment is one entry of collab_messages.attachments: a part of the raw message.
type storedAttachment struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Inline      bool   `json:"inline"`
	ContentID   string `json:"content_id"`
	// RemoteID is the provider's attachment id when the raw message is not stored (Graph).
	RemoteID string `json:"remote_id,omitempty"`
}

// messageRow is a stored message.
type messageRow struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	AccountID   uuid.UUID
	ThreadID    uuid.UUID
	RemoteID    string
	MessageID   string
	InReplyTo   string
	Refs        []string
	From        Address
	To          []Address
	Cc          []Address
	Bcc         []Address
	ReplyTo     []Address
	Subject     string
	Snippet     string
	BodyHTML    string
	BodyText    string
	SentAt      time.Time
	Unread      bool
	Starred     bool
	Draft       bool
	Attachments []storedAttachment
	RawKey      string
	Location    json.RawMessage
	SentBy      *uuid.UUID
	FolderIDs   []uuid.UUID
	// DeliveredTo is where it was delivered (Delivered-To, X-Original-To), lower case.
	DeliveredTo []string
	// Filed is set when the provider files the message in the account's Sent or Drafts.
	Filed bool
}

// outgoing reports mail the account wrote: sent from opendesk, a draft, or filed by the
// provider in Sent or Drafts. A From naming the account is not enough, since a sender
// writes their own From.
func (m *messageRow) outgoing() bool {
	return m.SentBy != nil || m.Draft || m.Filed
}

// messageColumns reads a message with its folders (alias m).
const messageColumns = `m.id, m.workspace_id, m.account_id, m.thread_id, m.remote_id, m.message_id, m.in_reply_to, m.refs,
	m.from_addr, m.to_addrs, m.cc_addrs, m.bcc_addrs, m.reply_to, m.subject, m.snippet, m.body_html, m.body_text, m.sent_at,
	m.unread, m.starred, m.draft, m.attachments, m.raw_key, m.location, m.sent_by,
	ARRAY(SELECT folder_id FROM collab_message_folders mf WHERE mf.message_id = m.id), m.delivered_to,
	EXISTS (SELECT 1 FROM collab_message_folders mf JOIN collab_folders f ON f.id = mf.folder_id
		WHERE mf.message_id = m.id AND f.role IN ('sent', 'drafts'))`

func scanMessage(row pgx.Row) (*messageRow, error) {
	var m messageRow
	if err := row.Scan(&m.ID, &m.WorkspaceID, &m.AccountID, &m.ThreadID, &m.RemoteID, &m.MessageID, &m.InReplyTo, &m.Refs,
		&m.From, &m.To, &m.Cc, &m.Bcc, &m.ReplyTo, &m.Subject, &m.Snippet, &m.BodyHTML, &m.BodyText, &m.SentAt,
		&m.Unread, &m.Starred, &m.Draft, &m.Attachments, &m.RawKey, &m.Location, &m.SentBy, &m.FolderIDs, &m.DeliveredTo, &m.Filed); err != nil {
		return nil, err
	}
	for _, list := range []*[]Address{&m.To, &m.Cc, &m.Bcc, &m.ReplyTo} {
		if *list == nil {
			*list = []Address{}
		}
	}
	if m.Attachments == nil {
		m.Attachments = []storedAttachment{}
	}
	if m.FolderIDs == nil {
		m.FolderIDs = []uuid.UUID{}
	}
	if m.Refs == nil {
		m.Refs = []string{}
	}
	if m.DeliveredTo == nil {
		m.DeliveredTo = []string{}
	}
	return &m, nil
}

// ref locates the message at the provider.
func (m *messageRow) ref(threadRemoteID string) MessageRef {
	return MessageRef{RemoteID: m.RemoteID, ThreadRemoteID: threadRemoteID, Location: m.Location}
}
