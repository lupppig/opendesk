package collab

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/ids"
)

// The store keeps synced mail (contracts/collab.md "Sync", "Conversations"): each new
// message parsed and sanitized once, its flags and folders kept in step with the
// provider, and each touched conversation's state recomputed from its messages. Every
// batch of writes publishes one collab.changed for the conversations it touched.

const (
	maxBodyText   = 200_000 // runes of a message's text kept
	maxSearchText = 60_000  // characters of a conversation's subject and participants fed to its search vector
)

// pass is one batch of synced changes to an account: its folders by remote id and the
// conversations touched since the last commit.
type pass struct {
	s       *service
	a       *accountRow
	folders map[string]*folderRow // by remote id
	touched []uuid.UUID
	// foldersChanged marks a change of folders or counts for the next event.
	foldersChanged bool
	// imported counts new messages stored in this pass.
	imported int
	// stored are the raw messages put in storage by the current batch.
	stored []string
	// removedFolders are the provider ids of folders gone at the provider in this pass.
	removedFolders []string
	// full: the workspace is past twice its Collab storage allowance, so no new mail is
	// imported (contracts/collab.md "Storage").
	full bool
	// noRaw: new messages keep no raw copy; their files are read from the provider (history
	// on an instance with a storage allowance).
	noRaw bool
	// addedBytes is what the messages stored in this pass keep; unreadable counts the ones
	// that could not be parsed; oldest is the earliest of them.
	addedBytes int64
	unreadable int
	oldest     time.Time
	// quiet: batches publish nothing; held records that one would have (a history import
	// tells once a pass, not once a page).
	quiet, held bool
	// categorize: new inbox mail waits to be sorted into categories ("Categories").
	categorize bool
}

func (s *service) newPass(ctx context.Context, q db.Querier, a *accountRow) (*pass, error) {
	p := &pass{s: s, a: a}
	return p, p.reloadFolders(ctx, q)
}

func (p *pass) reloadFolders(ctx context.Context, q db.Querier) error {
	list, err := loadFolders(ctx, q, p.a.ID)
	if err != nil {
		return err
	}
	p.folders = make(map[string]*folderRow, len(list))
	for _, f := range list {
		p.folders[f.RemoteID] = f
	}
	return nil
}

// folderIDs maps provider folder ids to stored folders, dropping unknown ones.
func (p *pass) folderIDs(remote []string) []uuid.UUID {
	out := []uuid.UUID{}
	for _, r := range remote {
		if f, ok := p.folders[r]; ok && !slices.Contains(out, f.ID) {
			out = append(out, f.ID)
		}
	}
	return out
}

// roleFolder is the account's folder with a role, or nil.
func (p *pass) roleFolder(role string) *folderRow {
	var best *folderRow
	for _, f := range p.folders {
		if f.Role != nil && *f.Role == role && (best == nil || f.Path < best.Path) {
			best = f
		}
	}
	return best
}

func (p *pass) touch(threadID uuid.UUID) {
	if threadID != uuid.Nil && !slices.Contains(p.touched, threadID) {
		p.touched = append(p.touched, threadID)
	}
}

// batch runs fn in a transaction, then recomputes the touched conversations and
// publishes collab.changed in the same transaction.
func (p *pass) batch(ctx context.Context, fn func(tx pgx.Tx) error) error {
	err := p.s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		p.stored = nil
		if err := fn(tx); err != nil {
			return err
		}
		return p.commit(ctx, tx)
	})
	if err != nil {
		p.touched = nil
		// Raw messages stored for rows that were rolled back are removed by the cleanup.
		for _, key := range p.stored {
			_, _ = p.s.d.DB.Pool.Exec(context.WithoutCancel(ctx), `INSERT INTO collab_storage_trash (key) VALUES ($1) ON CONFLICT DO NOTHING`, key)
		}
	}
	p.stored = nil
	return err
}

// commit recomputes the touched conversations and publishes one event, in tx.
func (p *pass) commit(ctx context.Context, tx pgx.Tx) error {
	if len(p.touched) == 0 && !p.foldersChanged {
		return nil
	}
	if err := recomputeThreads(ctx, tx, p.a, p.touched); err != nil {
		return err
	}
	threads := p.touched
	if threads == nil {
		threads = []uuid.UUID{}
	}
	if p.categorize {
		if err := p.s.queueCategorize(ctx, tx, p.a.ID); err != nil {
			return err
		}
		p.categorize = false
	}
	if p.quiet {
		p.touched, p.foldersChanged, p.held = nil, false, true
		return nil
	}
	id := p.a.ID
	if err := p.s.publishChanged(ctx, tx, p.a.WorkspaceID, &id, threads, p.foldersChanged || len(threads) > 0); err != nil {
		return err
	}
	p.touched, p.foldersChanged = nil, false
	return nil
}

// storedMsg is what the sync compares a provider message with.
type storedMsg struct {
	ID        uuid.UUID
	ThreadID  uuid.UUID
	RemoteID  string
	MessageID string
	Unread    bool
	Starred   bool
	Draft     bool
	RawKey    string
	Location  json.RawMessage
	FolderIDs []uuid.UUID
	SentAt    time.Time
}

const storedColumns = `m.id, m.thread_id, m.remote_id, m.message_id, m.unread, m.starred, m.draft, m.raw_key, m.location,
	ARRAY(SELECT folder_id FROM collab_message_folders mf WHERE mf.message_id = m.id), m.sent_at`

func scanStored(row pgx.Row) (*storedMsg, error) {
	var m storedMsg
	if err := row.Scan(&m.ID, &m.ThreadID, &m.RemoteID, &m.MessageID, &m.Unread, &m.Starred, &m.Draft, &m.RawKey, &m.Location, &m.FolderIDs,
		&m.SentAt); err != nil {
		return nil, err
	}
	if m.FolderIDs == nil {
		m.FolderIDs = []uuid.UUID{}
	}
	return &m, nil
}

// lookup finds a stored message of the account by provider id, nil when unknown.
func (p *pass) lookup(ctx context.Context, q db.Querier, remoteID string) (*storedMsg, error) {
	m, err := scanStored(q.QueryRow(ctx, `SELECT `+storedColumns+` FROM collab_messages m WHERE m.account_id = $1 AND m.remote_id = $2`,
		p.a.ID, remoteID))
	if db.IsNotFound(err) {
		return nil, nil
	}
	return m, err
}

// lookupMany finds stored messages of the account by provider id.
func (p *pass) lookupMany(ctx context.Context, q db.Querier, remoteIDs []string) (map[string]*storedMsg, error) {
	out := map[string]*storedMsg{}
	if len(remoteIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+storedColumns+` FROM collab_messages m WHERE m.account_id = $1 AND m.remote_id = ANY($2)`, p.a.ID, remoteIDs)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*storedMsg, error) { return scanStored(r) })
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		out[m.RemoteID] = m
	}
	return out, nil
}

// state is a message's flags and folders at the provider.
type state struct {
	Unread, Starred, Draft bool
	FolderIDs              []uuid.UUID
	// Location replaces the stored one when not nil.
	Location json.RawMessage
}

// update writes a known message's state when it changed.
func (p *pass) update(ctx context.Context, tx pgx.Tx, m *storedMsg, st state) error {
	if st.FolderIDs == nil {
		st.FolderIDs = []uuid.UUID{}
	}
	sameFolders := sameSet(m.FolderIDs, st.FolderIDs)
	sameLoc := st.Location == nil || sameJSON(m.Location, st.Location)
	sameState := m.Unread == st.Unread && m.Starred == st.Starred && m.Draft == st.Draft && sameFolders
	if sameState && sameLoc {
		return nil
	}
	loc := m.Location
	if st.Location != nil {
		loc = st.Location
	}
	if _, err := tx.Exec(ctx, `UPDATE collab_messages SET unread = $2, starred = $3, draft = $4, location = $5, updated_at = now()
		WHERE id = $1`, m.ID, st.Unread, st.Starred, st.Draft, loc); err != nil {
		return err
	}
	if !sameFolders {
		if err := setMessageFolders(ctx, tx, m.ID, st.FolderIDs); err != nil {
			return err
		}
		p.foldersChanged = true
	}
	if m.Unread != st.Unread {
		p.foldersChanged = true // unread counts
	}
	m.Unread, m.Starred, m.Draft, m.FolderIDs, m.Location = st.Unread, st.Starred, st.Draft, st.FolderIDs, loc
	if !sameState {
		p.touch(m.ThreadID) // a new location alone shows nothing new
	}
	return nil
}

// sameJSON compares two JSON documents by value (Postgres stores jsonb normalized).
func sameJSON(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func setMessageFolders(ctx context.Context, tx pgx.Tx, messageID uuid.UUID, folderIDs []uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM collab_message_folders WHERE message_id = $1 AND NOT (folder_id = ANY($2))`, messageID, folderIDs); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO collab_message_folders (message_id, folder_id) SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`,
		messageID, folderIDs)
	return err
}

// remove deletes a message gone from the provider; its raw source goes to the storage trash.
func (p *pass) remove(ctx context.Context, tx pgx.Tx, m *storedMsg) error {
	if m.RawKey != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO collab_storage_trash (key) VALUES ($1) ON CONFLICT DO NOTHING`, m.RawKey); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM collab_messages WHERE id = $1 AND account_id = $2`, m.ID, p.a.ID); err != nil {
		return err
	}
	p.touch(m.ThreadID)
	p.foldersChanged = true
	return nil
}

// removeOrphans deletes the account's messages gone from every folder at the provider,
// at the end of a pass: for Microsoft those in no folder (categories are labels, not
// folders), for IMAP those with no location
// left (their folders can briefly differ from their locations after an action). Gmail
// mail is never an orphan: archived mail is in no folder.
func (p *pass) removeOrphans(ctx context.Context, tx pgx.Tx) error {
	orphan := `NOT EXISTS (SELECT 1 FROM collab_message_folders mf JOIN collab_folders f ON f.id = mf.folder_id
		WHERE mf.message_id = m.id AND f.kind = 'folder')`
	switch p.a.Provider {
	case ProviderGoogle:
		return nil
	case ProviderIMAP:
		orphan = `COALESCE(jsonb_array_length(m.location->'imap'), 0) = 0`
	}
	rows, err := tx.Query(ctx, `SELECT `+storedColumns+` FROM collab_messages m WHERE m.account_id = $1 AND `+orphan, p.a.ID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*storedMsg, error) { return scanStored(r) })
	if err != nil {
		return err
	}
	for _, m := range list {
		if err := p.remove(ctx, tx, m); err != nil {
			return err
		}
	}
	return nil
}

// newMessage is a message to store for the first time.
type newMessage struct {
	RemoteID       string
	ThreadRemoteID string // Gmail threadId, Graph conversationId; "" = thread by headers
	Raw            []byte
	Date           time.Time // when the provider received it; zero = the Date header
	State          state
	SentBy         *uuid.UUID
	ThreadID       *uuid.UUID // a reply sent from opendesk stays in its conversation
	Signals        []string   // the provider's own, besides the headers' (Gmail's tab)
	// ID is the stored id to use (a draft saved again keeps its id); nil = a new one.
	ID *uuid.UUID
	// AI is the API key that saved a draft ("AI drafts"); nil = a person.
	AI *aiDrafter
}

// aiDrafter is the AI credential a draft is attributed to.
type aiDrafter struct {
	APIKeyID uuid.UUID
	Client   string          // the key's name, for "AI draft by <name>"
	P        *auth.Principal // for the audit entry
}

// insert parses, sanitizes and stores a new message and returns its id. A message that
// cannot be parsed is skipped (uuid.Nil, nil).
func (p *pass) insert(ctx context.Context, tx pgx.Tx, in *newMessage) (uuid.UUID, error) {
	if len(in.Raw) == 0 || len(in.Raw) > maxRawBytes {
		return uuid.Nil, nil
	}
	pm, err := email.ParseMail(in.Raw)
	if err != nil {
		p.s.log.Info("collab message not parsed", "account_id", p.a.ID)
		p.unreadable++
		return uuid.Nil, nil
	}
	if in.RemoteID == "" {
		in.RemoteID = imapRemoteID(pm.MessageID, in.Raw)
	}
	id := ids.New()
	if in.ID != nil {
		id = *in.ID
	}
	wid := p.a.WorkspaceID

	atts := []storedAttachment{}
	cids := map[string]string{}
	for i, part := range pm.Parts {
		ct := strings.ToLower(part.ContentType)
		if mt, _, err := mime.ParseMediaType(ct); err == nil {
			ct = mt
		} else {
			ct = "application/octet-stream"
		}
		cid := email.NormalizeCID(part.ContentID)
		inline := strings.EqualFold(part.Disposition, "inline") || cid != "" && part.Disposition == ""
		atts = append(atts, storedAttachment{Index: i, Filename: email.CleanFilename(part.FileName, ct), ContentType: ct,
			Size: int64(len(part.Content)), Inline: inline, ContentID: cid})
		if cid != "" {
			cids[cid] = p.s.attachmentURL(id, i)
		}
	}
	src := pm.HTML
	if strings.TrimSpace(src) == "" {
		src = email.TextToHTML(pm.Text)
	}
	if len(cids) > 0 {
		src, _ = email.RewriteCID(src, cids)
	}
	bodyHTML := email.SanitizeHTML(src)
	text := strings.TrimSpace(pm.Text)
	if text == "" {
		text = strings.TrimSpace(email.HTMLToText(bodyHTML))
	}
	if r := []rune(text); len(r) > maxBodyText {
		text = string(r[:maxBodyText])
	}

	rawKey := ""
	size := int64(len(bodyHTML) + len(text))
	if len(atts) > 0 && !p.noRaw {
		size += int64(len(in.Raw))
		rawKey = "collab/" + wid.String() + "/" + p.a.ID.String() + "/" + id.String() + ".eml"
		if err := p.s.d.Storage.Put(ctx, rawKey, bytes.NewReader(in.Raw), int64(len(in.Raw)), "message/rfc822"); err != nil {
			return uuid.Nil, err
		}
		p.stored = append(p.stored, rawKey)
	}

	msgID := bare(pm.MessageID)
	refs := []string{}
	for _, r := range pm.References {
		refs = append(refs, bare(r))
	}
	inReplyTo := bare(pm.InReplyTo)
	threadID, err := p.threadFor(ctx, tx, in, msgID, inReplyTo, refs)
	if err != nil {
		return uuid.Nil, err
	}

	sentAt := in.Date
	if sentAt.IsZero() {
		sentAt = pm.Date()
	}
	if sentAt.IsZero() || sentAt.After(time.Now().Add(24*time.Hour)) {
		sentAt = time.Now()
	}
	from := Address{Name: cleanName(pm.FromName), Email: pm.FromAddr}
	var aiKey *uuid.UUID
	var aiClient *string
	if in.AI != nil && in.State.Draft {
		aiKey, aiClient = &in.AI.APIKeyID, &in.AI.Client
	}
	var inserted uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO collab_messages (id, workspace_id, account_id, thread_id, remote_id, message_id, in_reply_to, refs,
		from_addr, to_addrs, cc_addrs, bcc_addrs, reply_to, subject, snippet, body_html, body_text, sent_at, unread, starred, draft,
		attachments, raw_key, location, sent_by, delivered_to, size, signals, ai_api_key_id, ai_client)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30)
		ON CONFLICT (account_id, remote_id) DO NOTHING RETURNING id`,
		id, wid, p.a.ID, threadID, in.RemoteID, msgID, inReplyTo, refs, from, addressList(pm, "To"), addressList(pm, "Cc"),
		addressList(pm, "Bcc"), addressList(pm, "Reply-To"), truncateRunes(pm.Subject, 998), cleanLine(text, 200), bodyHTML, text,
		sentAt, in.State.Unread, in.State.Starred, in.State.Draft, atts, rawKey, locationOrEmpty(in.State.Location), in.SentBy, deliveredTo(pm),
		size, append(mailSignals(pm), in.Signals...), aiKey, aiClient).Scan(&inserted)
	if db.IsNotFound(err) {
		// Stored meanwhile (a sync and a send racing): keep that row.
		if rawKey != "" {
			_, _ = tx.Exec(ctx, `INSERT INTO collab_storage_trash (key) VALUES ($1) ON CONFLICT DO NOTHING`, rawKey)
		}
		m, err := p.lookup(ctx, tx, in.RemoteID)
		if err != nil || m == nil {
			return uuid.Nil, err
		}
		if in.SentBy != nil {
			if _, err := tx.Exec(ctx, `UPDATE collab_messages SET sent_by = $2 WHERE id = $1 AND sent_by IS NULL`, m.ID, in.SentBy); err != nil {
				return uuid.Nil, err
			}
			// A draft saved from opendesk is whoever saved it last: an AI key or a person.
			if in.State.Draft {
				if _, err := tx.Exec(ctx, `UPDATE collab_messages SET ai_api_key_id = $2, ai_client = $3 WHERE id = $1 AND draft`,
					m.ID, aiKey, aiClient); err != nil {
					return uuid.Nil, err
				}
			}
		}
		p.touch(threadID) // the conversation row may be new and empty
		return m.ID, p.update(ctx, tx, m, in.State)
	}
	if err != nil {
		return uuid.Nil, err
	}
	if err := setMessageFolders(ctx, tx, id, nonNil(in.State.FolderIDs)); err != nil {
		return uuid.Nil, err
	}
	if p.a.Visibility == VisibilityShared && p.a.HandlingEnabled && !in.State.Draft {
		needsAction, updateHandling := false, false
		if in.SentBy != nil {
			updateHandling = true
		} else if sent := p.roleFolder(RoleSent); sent != nil && slices.Contains(in.State.FolderIDs, sent.ID) {
			updateHandling = true
		} else if inbox := p.roleFolder(RoleInbox); inbox != nil && slices.Contains(in.State.FolderIDs, inbox.ID) {
			needsAction, updateHandling = true, true
		}
		if updateHandling {
			if _, err := tx.Exec(ctx, `UPDATE collab_threads SET needs_action = $2, updated_at = now()
				WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM collab_messages WHERE thread_id = $1 AND (sent_at, id) > ($3, $4))`,
				threadID, needsAction, sentAt, id); err != nil {
				return uuid.Nil, err
			}
		}
	}
	if !in.State.Draft {
		// An answer to a ticket reply sent from the account goes to the ticket too ("Answering tickets").
		if err := p.answeredTicket(ctx, tx, id, threadID, from.Email, append(append([]string{}, refs...), inReplyTo)); err != nil {
			return uuid.Nil, err
		}
		if err := p.sortLater(ctx, tx, threadID, in); err != nil {
			return uuid.Nil, err
		}
	}
	p.touch(threadID)
	p.foldersChanged = true
	p.imported++
	p.addedBytes += size
	if p.oldest.IsZero() || sentAt.Before(p.oldest) {
		p.oldest = sentAt
	}
	return id, nil
}

// sortLater marks a conversation to sort when new mail from someone else reaches the
// inbox of an account whose inbox is split, and moves one the account answered to Primary
// ("Categories"). History imports are not sorted.
func (p *pass) sortLater(ctx context.Context, tx pgx.Tx, threadID uuid.UUID, in *newMessage) error {
	if len(p.a.CategoryKeys) == 0 || p.quiet {
		return nil
	}
	has := func(role string) bool {
		f := p.roleFolder(role)
		return f != nil && slices.Contains(in.State.FolderIDs, f.ID)
	}
	if in.SentBy != nil || has(RoleSent) {
		_, err := tx.Exec(ctx, `UPDATE collab_threads SET category = 'primary', category_source = 'reply', category_pending = false
			WHERE id = $1 AND category IS DISTINCT FROM 'primary' AND category_source IS DISTINCT FROM 'person'`, threadID)
		return err
	}
	if !has(RoleInbox) {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE collab_threads SET category_pending = true WHERE id = $1 AND category IS NULL AND NOT category_pending`, threadID)
	if err == nil && tag.RowsAffected() > 0 {
		p.categorize = true
	}
	return err
}

// threadFor finds or creates the message's conversation (contracts/collab.md
// "Conversations"): the provider's thread when it has one; for IMAP the first message
// its References / In-Reply-To name that the account already has, else a conversation
// keyed by the root of its references.
func (p *pass) threadFor(ctx context.Context, tx pgx.Tx, in *newMessage, msgID, inReplyTo string, refs []string) (uuid.UUID, error) {
	if in.ThreadID != nil {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM collab_threads WHERE id = $1 AND account_id = $2`, *in.ThreadID, p.a.ID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !db.IsNotFound(err) {
			return uuid.Nil, err
		}
	}
	remote := in.ThreadRemoteID
	if remote == "" {
		named := append(append([]string{}, refs...), inReplyTo)
		named = slices.DeleteFunc(named, func(s string) bool { return s == "" })
		if len(named) > 0 {
			var id uuid.UUID
			err := tx.QueryRow(ctx, `SELECT thread_id FROM collab_messages WHERE account_id = $1 AND message_id = ANY($2)
				ORDER BY array_position($2, message_id), sent_at LIMIT 1`, p.a.ID, named).Scan(&id)
			if err == nil {
				return id, nil
			}
			if !db.IsNotFound(err) {
				return uuid.Nil, err
			}
			remote = named[0]
		} else {
			remote = msgID
		}
		if remote == "" {
			remote = "msg:" + in.RemoteID
		}
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO collab_threads (id, workspace_id, account_id, remote_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (account_id, remote_id) DO UPDATE SET updated_at = now() RETURNING id`,
		ids.New(), p.a.WorkspaceID, p.a.ID, truncateRunes(remote, 500)).Scan(&id)
	return id, err
}

// recomputeThreads sets each conversation's state from its messages and deletes the
// conversations left without any.
func recomputeThreads(ctx context.Context, tx pgx.Tx, a *accountRow, threadIDs []uuid.UUID) error {
	if len(threadIDs) == 0 {
		return nil
	}
	idents, err := accountIdentities(ctx, tx, a)
	if err != nil {
		return err
	}
	own := ownAddresses(a, idents)
	rows, err := tx.Query(ctx, `SELECT m.thread_id, m.subject, m.snippet, m.from_addr, m.to_addrs, m.cc_addrs, m.sent_at, m.unread, m.starred,
		EXISTS (SELECT 1 FROM jsonb_array_elements(m.attachments) e WHERE NOT COALESCE((e->>'inline')::boolean, false)),
		ARRAY(SELECT folder_id FROM collab_message_folders mf WHERE mf.message_id = m.id), m.delivered_to
		FROM collab_messages m WHERE m.account_id = $1 AND m.thread_id = ANY($2) ORDER BY m.thread_id, m.sent_at, m.id`, a.ID, threadIDs)
	if err != nil {
		return err
	}
	type msg struct {
		subject, snippet       string
		from                   Address
		to, cc                 []Address
		sentAt                 time.Time
		unread, starred, files bool
		folders                []uuid.UUID
		delivered              []string
	}
	byThread := map[uuid.UUID][]msg{}
	for rows.Next() {
		var tid uuid.UUID
		var m msg
		if err := rows.Scan(&tid, &m.subject, &m.snippet, &m.from, &m.to, &m.cc, &m.sentAt, &m.unread, &m.starred, &m.files, &m.folders, &m.delivered); err != nil {
			rows.Close()
			return err
		}
		byThread[tid] = append(byThread[tid], m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	empty := []uuid.UUID{}
	for _, tid := range threadIDs {
		list := byThread[tid]
		if len(list) == 0 {
			empty = append(empty, tid)
			continue
		}
		subject := ""
		for _, m := range list {
			if strings.TrimSpace(m.subject) != "" {
				subject = m.subject
				break
			}
		}
		newest := list[len(list)-1]
		var unread, starred, files bool
		folders := []uuid.UUID{}
		var people []Address
		addresses := []string{}
		var search strings.Builder
		for i := len(list) - 1; i >= 0; i-- {
			m := list[i]
			unread, starred, files = unread || m.unread, starred || m.starred, files || m.files
			for _, f := range m.folders {
				if !slices.Contains(folders, f) {
					folders = append(folders, f)
				}
			}
			people = append(people, m.from)
			people = append(people, m.to...)
			people = append(people, m.cc...)
			for _, x := range append(append(append([]Address{m.from}, m.to...), m.cc...), addressesOf(m.delivered)...) {
				if e := strings.ToLower(strings.TrimSpace(x.Email)); e != "" && !slices.Contains(addresses, e) && len(addresses) < maxThreadAddresses {
					addresses = append(addresses, e)
				}
			}
		}
		participants := participantList(people, own)
		// The conversation's own vector is its subject and the people in it; each message's
		// text is searched on its own (collab_messages.search).
		search.WriteString(subject)
		for _, a := range addresses {
			search.WriteString(" " + a)
		}
		for _, p := range participants {
			search.WriteString(" " + p.Name)
		}
		doc := search.String()
		if len(doc) > maxSearchText {
			doc = strings.ToValidUTF8(doc[:maxSearchText], "")
		}
		if _, err := tx.Exec(ctx, `UPDATE collab_threads SET subject = $2, snippet = $3, participants = $4, last_message_at = $5,
			message_count = $6, unread = $7, starred = $8, has_attachments = $9, folder_ids = $10,
			search = to_tsvector('simple', $11), addresses = $12, updated_at = now() WHERE id = $1`,
			tid, subject, newest.snippet, participants, newest.sentAt, len(list), unread, starred, files, folders, doc, addresses); err != nil {
			return err
		}
	}
	if len(empty) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM collab_threads t WHERE t.id = ANY($1) AND t.account_id = $2
			AND NOT EXISTS (SELECT 1 FROM collab_messages m WHERE m.thread_id = t.id)`, empty, a.ID); err != nil {
			return err
		}
	}
	return nil
}

// maxThreadAddresses bounds the addresses a conversation keeps for its filter.
const maxThreadAddresses = 100

func addressesOf(list []string) []Address {
	out := make([]Address, 0, len(list))
	for _, e := range list {
		out = append(out, Address{Email: e})
	}
	return out
}

// participantList is who a conversation is between, newest first (people is in that
// order), each address once, at most 10, with one of the account's own addresses (own:
// its own and its aliases) last.
func participantList(people []Address, own []string) []Address {
	others := []Address{}
	var self *Address
	seen := map[string]bool{}
	for _, p := range people {
		addr := strings.ToLower(strings.TrimSpace(p.Email))
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		if slices.Contains(own, addr) {
			if self == nil {
				cp := p
				self = &cp
			}
			continue
		}
		others = append(others, p)
	}
	limit := 10
	if self != nil {
		limit = 9
	}
	if len(others) > limit {
		others = others[:limit]
	}
	if self != nil {
		others = append(others, *self)
	}
	return others
}

func addressList(pm *email.Parsed, key string) []Address {
	out := []Address{}
	for _, a := range pm.AddressList(key) {
		out = append(out, Address{Name: cleanName(a.Name), Email: a.Address})
	}
	return out
}

// cleanName keeps a name from a mail header (untrusted) displayable.
func cleanName(s string) string { return displayname.Clean(s, displayname.Workspace) }

// bare drops the angle brackets of a Message-ID.
func bare(id string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(id), "<"), ">")
}

// imapRemoteID is an IMAP message's identity: its Message-ID, else a hash of its content.
func imapRemoteID(messageID string, raw []byte) string {
	if id := bare(messageID); id != "" {
		return truncateRunes(id, 900)
	}
	if len(raw) == 0 {
		return ""
	}
	return bare(email.HeaderMessageID(raw))
}

func sameSet(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func nonNil(list []uuid.UUID) []uuid.UUID {
	if list == nil {
		return []uuid.UUID{}
	}
	return list
}

func locationOrEmpty(loc json.RawMessage) json.RawMessage {
	if len(loc) == 0 {
		return json.RawMessage(`{}`)
	}
	return loc
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// upsertFolder stores a provider folder of the account and returns it.
func (s *service) upsertFolder(ctx context.Context, q db.Querier, a *accountRow, f RemoteFolder) (*folderRow, error) {
	var role, color *string
	if f.Role != "" {
		role = &f.Role
	}
	if f.Color != "" {
		color = &f.Color
	}
	kind := f.Kind
	if kind == "" {
		kind = KindFolder
	}
	name, path := truncateRunes(f.Name, 200), truncateRunes(f.Path, 1000)
	if path == "" {
		path = name
	}
	return scanFolder(q.QueryRow(ctx, `INSERT INTO collab_folders (id, workspace_id, account_id, remote_id, name, path, role, kind, color)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (account_id, remote_id) DO UPDATE SET name = EXCLUDED.name, path = EXCLUDED.path, role = EXCLUDED.role,
			kind = EXCLUDED.kind, color = EXCLUDED.color
		RETURNING `+folderColumns, ids.New(), a.WorkspaceID, a.ID, f.RemoteID, name, path, role, kind, color))
}

// syncFolders stores the provider's folders (at most maxFolders) and removes the ones
// gone; their messages leave them.
func (p *pass) syncFolders(ctx context.Context, remote []RemoteFolder) error {
	if len(remote) > maxFolders {
		remote = remote[:maxFolders]
	}
	return p.batch(ctx, func(tx pgx.Tx) error {
		if err := p.reloadFolders(ctx, tx); err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, f := range remote {
			if f.RemoteID == "" || keep[f.RemoteID] {
				continue
			}
			keep[f.RemoteID] = true
			old := p.folders[f.RemoteID]
			if old != nil && old.remote() == f {
				continue
			}
			row, err := p.s.upsertFolder(ctx, tx, p.a, f)
			if err != nil {
				return err
			}
			p.folders[f.RemoteID] = row
			p.foldersChanged = true
		}
		for rid, f := range p.folders {
			if keep[rid] {
				continue
			}
			rows, err := tx.Query(ctx, `SELECT DISTINCT m.thread_id FROM collab_message_folders mf JOIN collab_messages m ON m.id = mf.message_id
				WHERE mf.folder_id = $1`, f.ID)
			if err != nil {
				return err
			}
			threads, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM collab_folders WHERE id = $1 AND account_id = $2`, f.ID, p.a.ID); err != nil {
				return err
			}
			for _, t := range threads {
				p.touch(t)
			}
			delete(p.folders, rid)
			p.removedFolders = append(p.removedFolders, rid)
			p.foldersChanged = true
		}
		return nil
	})
}
