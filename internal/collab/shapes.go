package collab

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Rows to API shapes (contracts/collab.md "Shapes"), loaded in batches.

// collabOp registers a Collab route: tagged Collab and left out of the public spec, since
// Collab is for signed-in members only (never API keys).
func collabOp[I, O any](api huma.API, o huma.Operation, h func(context.Context, *I) (*O, error)) {
	o.Hidden = true
	o.Tags = []string{"Collab"}
	huma.Register(api, o, h)
}

// parseID reads a path id; anything that is not one is not found.
func parseID(s, what string) (uuid.UUID, error) {
	id, ok := ids.Parse(s)
	if !ok {
		return uuid.Nil, httpx.NotFound(what)
	}
	return id, nil
}

// notFound answers a missing row as what is not found, and passes other errors on.
func notFound(err error, what string) error {
	if db.IsNotFound(err) {
		return httpx.NotFound(what)
	}
	return httpx.Wrap(err)
}

// userRefs loads people's public fields by id.
func userRefs(ctx context.Context, q db.Querier, list []uuid.UUID) (map[uuid.UUID]domain.UserRef, error) {
	out := map[uuid.UUID]domain.UserRef{}
	if len(list) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, name, email::text, avatar_url FROM users WHERE id = ANY($1)`, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u domain.UserRef
		var avatar string
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &avatar); err != nil {
			return nil, err
		}
		u.AvatarURL = domain.Avatar(avatar)
		out[u.ID] = u
	}
	return out, rows.Err()
}

func refOf(refs map[uuid.UUID]domain.UserRef, id *uuid.UUID) *domain.UserRef {
	if id == nil {
		return nil
	}
	if u, ok := refs[*id]; ok {
		return &u
	}
	return nil
}

// accountsOut converts accounts for the caller.
func (s *service) accountsOut(ctx context.Context, q db.Querier, p *auth.Principal, list []*accountRow) ([]MailAccount, error) {
	out := make([]MailAccount, 0, len(list))
	if len(list) == 0 {
		return out, nil
	}
	var accountIDs, owners []uuid.UUID
	for _, a := range list {
		accountIDs = append(accountIDs, a.ID)
		if a.OwnerID != nil {
			owners = append(owners, *a.OwnerID)
		}
	}
	refs, err := userRefs(ctx, q, owners)
	if err != nil {
		return nil, err
	}
	unread, err := inboxUnread(ctx, q, p.WorkspaceID, accountIDs)
	if err != nil {
		return nil, err
	}
	identities, err := loadIdentities(ctx, q, p.WorkspaceID, accountIDs)
	if err != nil {
		return nil, err
	}
	hist, oldest, leftOut, err := historiesOut(ctx, q, p.WorkspaceID, list)
	if err != nil {
		return nil, err
	}
	fill := s.fillerFor(q, p.WorkspaceID, p.UserID)
	cats, err := loadCategorySets(ctx, q, p.WorkspaceID, accountIDs, owners)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		m := MailAccount{ID: a.ID, Provider: a.Provider, Email: a.Email, Name: a.Name, DisplayName: a.DisplayName, Color: a.Color,
			Owner: refOf(refs, a.OwnerID), Visibility: a.Visibility, SharedWith: Audience{TeamIDs: []uuid.UUID{}, UserIDs: []uuid.UUID{}},
			SignatureHTML: a.SignatureHTML, Status: a.Status, Reconnect: a.Reconnect, LastError: a.LastError,
			LastSyncedAt: a.LastSyncedAt, CanManage: canManageAccount(p, a), Unread: unread[a.ID], HandlingEnabled: a.HandlingEnabled, CreatedAt: a.CreatedAt,
			History: hist[a.ID], OldestMessageAt: oldest[a.ID], FoldersLeftOut: leftOut[a.ID]}
		if m.Name == "" {
			m.Name = a.Email
		}
		applySettings(&m, a)
		m.Categories = cats.resolve(a).forCaller(p, cats)
		if m.Identities, m.DefaultFrom, err = identitiesOut(ctx, fill, a, identities[a.ID]); err != nil {
			return nil, err
		}
		if a.Visibility == VisibilityShared {
			id := a.ID
			if m.SharedWith, err = loadAudience(ctx, q, p.WorkspaceID, &id, nil); err != nil {
				return nil, err
			}
		}
		if a.Provider == ProviderIMAP && m.CanManage {
			c := a.Config
			m.IMAP = &IMAPSettings{IMAPHost: c.IMAPHost, IMAPPort: c.IMAPPort, IMAPTLS: c.IMAPTLS, IMAPUsername: c.IMAPUsername,
				SMTPHost: c.SMTPHost, SMTPPort: c.SMTPPort, SMTPTLS: c.SMTPTLS, SMTPUsername: c.SMTPUsername}
		}
		out = append(out, m)
	}
	return out, nil
}

// accountOut converts one account.
func (s *service) accountOut(ctx context.Context, q db.Querier, p *auth.Principal, a *accountRow) (MailAccount, error) {
	list, err := s.accountsOut(ctx, q, p, []*accountRow{a})
	if err != nil {
		return MailAccount{}, err
	}
	return list[0], nil
}

// inboxSQL is the ids of the workspace's (SQL parameter $1) inbox folders.
const inboxSQL = `ARRAY(SELECT id FROM collab_folders WHERE workspace_id = $1 AND role = 'inbox')`

// inboxUnread counts the unread conversations in each account's inbox.
func inboxUnread(ctx context.Context, q db.Querier, wid uuid.UUID, accountIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := q.Query(ctx, `SELECT t.account_id, count(*) FROM collab_threads t
		WHERE t.workspace_id = $1 AND t.account_id = ANY($2) AND t.unread AND t.folder_ids && `+inboxSQL+`
		GROUP BY t.account_id`, wid, accountIDs)
	if err != nil {
		return nil, err
	}
	return collectCounts(rows)
}

func collectCounts(rows pgx.Rows) (map[uuid.UUID]int, error) {
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// threadsOut converts conversations, in their order, with shares, assignees and comment counts.
func threadsOut(ctx context.Context, q db.Querier, list []*threadRow) ([]MailThread, error) {
	out := make([]MailThread, 0, len(list))
	if len(list) == 0 {
		return out, nil
	}
	tids := make([]uuid.UUID, 0, len(list))
	for _, t := range list {
		tids = append(tids, t.ID)
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT thread_id FROM collab_shares WHERE thread_id = ANY($1)`, tids)
	if err != nil {
		return nil, err
	}
	sharedList, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `SELECT thread_id, count(*) FROM collab_comments WHERE thread_id = ANY($1) GROUP BY thread_id`, tids)
	if err != nil {
		return nil, err
	}
	comments, err := collectCounts(rows)
	if err != nil {
		return nil, err
	}
	assignees := map[uuid.UUID][]domain.UserRef{}
	rows, err = q.Query(ctx, `SELECT ca.thread_id, u.id, u.name, u.email::text, u.avatar_url FROM collab_assignees ca
		JOIN users u ON u.id = ca.user_id WHERE ca.thread_id = ANY($1) ORDER BY ca.created_at, u.id`, tids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid uuid.UUID
		var u domain.UserRef
		var avatar string
		if err := rows.Scan(&tid, &u.ID, &u.Name, &u.Email, &avatar); err != nil {
			return nil, err
		}
		u.AvatarURL = domain.Avatar(avatar)
		assignees[tid] = append(assignees[tid], u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	aliases, err := accountAliases(ctx, q, list)
	if err != nil {
		return nil, err
	}
	keys, err := accountCategoryKeys(ctx, q, list)
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		m := MailThread{ID: t.ID, AccountID: t.AccountID, Subject: t.Subject, Snippet: t.Snippet, Participants: t.Participants,
			LastMessageAt: t.LastMessageAt, MessageCount: t.MessageCount, Unread: t.Unread, Starred: t.Starred,
			HasAttachments: t.HasAttachments, NeedsAction: t.NeedsAction, FolderIDs: t.FolderIDs, Shared: slices.Contains(sharedList, t.ID),
			Assignees: assignees[t.ID], CommentCount: comments[t.ID], Category: effectiveCategory(keys[t.AccountID], t.Category)}
		if m.Assignees == nil {
			m.Assignees = []domain.UserRef{}
		}
		for _, addr := range t.Addresses {
			if slices.Contains(aliases[t.AccountID], addr) {
				m.ToAddress = &addr
				break
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// accountCategoryKeys is the category keys the conversations' accounts are sorted with.
func accountCategoryKeys(ctx context.Context, q db.Querier, list []*threadRow) (map[uuid.UUID][]string, error) {
	var accountIDs []uuid.UUID
	for _, t := range list {
		accountIDs = appendNew(accountIDs, t.AccountID)
	}
	rows, err := q.Query(ctx, `SELECT id, category_keys FROM collab_accounts WHERE id = ANY($1) AND cardinality(category_keys) > 0`, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]string{}
	for rows.Next() {
		var id uuid.UUID
		var keys []string
		if err := rows.Scan(&id, &keys); err != nil {
			return nil, err
		}
		out[id] = keys
	}
	return out, rows.Err()
}

// accountAliases is the addresses besides their own of the conversations' accounts.
func accountAliases(ctx context.Context, q db.Querier, list []*threadRow) (map[uuid.UUID][]string, error) {
	var accountIDs []uuid.UUID
	for _, t := range list {
		accountIDs = appendNew(accountIDs, t.AccountID)
	}
	rows, err := q.Query(ctx, `SELECT account_id, email FROM collab_identities WHERE workspace_id = $1 AND account_id = ANY($2)`,
		list[0].WorkspaceID, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]string{}
	for rows.Next() {
		var id uuid.UUID
		var addr string
		if err := rows.Scan(&id, &addr); err != nil {
			return nil, err
		}
		out[id] = append(out[id], addr)
	}
	return out, rows.Err()
}

// messagesOut converts a conversation's messages with signed attachment links.
func (s *service) messagesOut(ctx context.Context, q db.Querier, a *accountRow, list []*messageRow) ([]MailMessage, error) {
	var senders []uuid.UUID
	for _, m := range list {
		if m.SentBy != nil {
			senders = append(senders, *m.SentBy)
		}
	}
	refs, err := userRefs(ctx, q, senders)
	if err != nil {
		return nil, err
	}
	replied, err := repliedIDs(ctx, q, a, list)
	if err != nil {
		return nil, err
	}
	idents, err := accountIdentities(ctx, q, a)
	if err != nil {
		return nil, err
	}
	own := ownAddresses(a, idents)
	out := make([]MailMessage, 0, len(list))
	for _, m := range list {
		mm := s.messageOut(a, m, refOf(refs, m.SentBy))
		if m.Draft {
			id := m.ID
			mm.DraftID = &id
		}
		if id, ok := replied[m.ThreadID][bare(m.InReplyTo)]; ok && id != m.ID {
			mm.InReplyToID = &id
		}
		if addr := ownAddressOf(m, own); addr != "" {
			mm.OwnAddress = &addr
		}
		out = append(out, mm)
	}
	return out, nil
}

// repliedIDs finds, per conversation, the stored messages the given ones answer (their
// In-Reply-To), by Message-ID: from the list itself, else one query.
func repliedIDs(ctx context.Context, q db.Querier, a *accountRow, list []*messageRow) (map[uuid.UUID]map[string]uuid.UUID, error) {
	out := map[uuid.UUID]map[string]uuid.UUID{}
	put := func(thread uuid.UUID, msgID string, id uuid.UUID) {
		if out[thread] == nil {
			out[thread] = map[string]uuid.UUID{}
		}
		if _, ok := out[thread][msgID]; !ok {
			out[thread][msgID] = id
		}
	}
	for _, m := range list {
		if id := bare(m.MessageID); id != "" {
			put(m.ThreadID, id, m.ID)
		}
	}
	var threads []uuid.UUID
	var missing []string
	for _, m := range list {
		want := bare(m.InReplyTo)
		if want == "" {
			continue
		}
		if _, ok := out[m.ThreadID][want]; !ok {
			threads = appendNew(threads, m.ThreadID)
			missing = append(missing, want)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, thread_id, message_id FROM collab_messages WHERE account_id = $1 AND thread_id = ANY($2)
		AND message_id = ANY($3) ORDER BY sent_at`, a.ID, threads, missing)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, thread uuid.UUID
		var msgID string
		if err := rows.Scan(&id, &thread, &msgID); err != nil {
			return nil, err
		}
		put(thread, bare(msgID), id)
	}
	return out, rows.Err()
}

func (s *service) messageOut(a *accountRow, m *messageRow, sentBy *domain.UserRef) MailMessage {
	out := MailMessage{ID: m.ID, ThreadID: m.ThreadID, AccountID: m.AccountID, MessageID: strings.Trim(m.MessageID, "<>"),
		From: m.From, To: m.To, Cc: m.Cc, Bcc: []Address{}, ReplyTo: m.ReplyTo, Subject: m.Subject, Snippet: m.Snippet,
		BodyHTML: s.resignAttachmentLinks(m.ID, m.BodyHTML), BodyText: m.BodyText, SentAt: m.SentAt, Unread: m.Unread,
		Starred: m.Starred, Draft: m.Draft, FolderIDs: m.FolderIDs, Attachments: make([]MailAttachment, 0, len(m.Attachments)),
		SentBy: sentBy, Outgoing: m.outgoing()}
	// Bcc is only ever shown on mail the account sent.
	if out.Outgoing {
		out.Bcc = m.Bcc
	}
	if m.Draft && m.AIClient != nil {
		out.DraftedByAI = m.AIClient
	}
	for _, at := range m.Attachments {
		out.Attachments = append(out.Attachments, MailAttachment{ID: strconv.Itoa(at.Index), Filename: at.Filename, ContentType: at.ContentType,
			Size: at.Size, Inline: at.Inline, ContentID: at.ContentID, URL: s.attachmentURL(m.ID, at.Index)})
	}
	return out
}

// commentRow is a stored comment.
type commentRow struct {
	ID        uuid.UUID
	ThreadID  uuid.UUID
	AuthorID  *uuid.UUID
	BodyHTML  string
	Mentions  []uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

const commentColumns = `id, thread_id, author_id, body_html, mentions, created_at, updated_at`

func scanComment(row pgx.Row) (*commentRow, error) {
	var c commentRow
	if err := row.Scan(&c.ID, &c.ThreadID, &c.AuthorID, &c.BodyHTML, &c.Mentions, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if c.Mentions == nil {
		c.Mentions = []uuid.UUID{}
	}
	return &c, nil
}

// commentsOut converts comments.
func commentsOut(ctx context.Context, q db.Querier, list []*commentRow) ([]MailComment, error) {
	var authors []uuid.UUID
	for _, c := range list {
		if c.AuthorID != nil {
			authors = append(authors, *c.AuthorID)
		}
	}
	refs, err := userRefs(ctx, q, authors)
	if err != nil {
		return nil, err
	}
	out := make([]MailComment, 0, len(list))
	for _, c := range list {
		out = append(out, MailComment{ID: c.ID, ThreadID: c.ThreadID, Author: refOf(refs, c.AuthorID), BodyHTML: c.BodyHTML,
			Mentions: c.Mentions, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt})
	}
	return out, nil
}
