package collab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Boxes of the thread list (contracts/collab.md "GET /v1/collab/threads").
const (
	BoxInbox    = "inbox"
	BoxUnread   = "unread"
	BoxStarred  = "starred"
	BoxSent     = "sent"
	BoxDrafts   = "drafts"
	BoxArchive  = "archive"
	BoxTrash    = "trash"
	BoxSpam     = "spam"
	BoxAll      = "all"
	BoxShared   = "shared"
	BoxAssigned = "assigned"
)

// maxAssignees bounds a conversation's assignees.
const maxAssignees = 20

// roleSQL is the ids of the workspace's ($1) folders with one of the roles.
func roleSQL(roles ...string) string {
	return `ARRAY(SELECT id FROM collab_folders WHERE workspace_id = $1 AND role IN ('` + strings.Join(roles, `', '`) + `'))`
}

// boxSQL is the predicate of a box on collab_threads alias t; $1 is the workspace and u
// the caller's SQL parameter.
func boxSQL(box, u string) string {
	notJunk := `NOT (t.folder_ids && ` + roleSQL(RoleTrash, RoleSpam) + `)`
	switch box {
	case BoxUnread:
		return `t.unread AND ` + notJunk
	case BoxStarred:
		return `t.starred AND NOT (t.folder_ids && ` + roleSQL(RoleTrash) + `)`
	case BoxSent:
		return `t.folder_ids && ` + roleSQL(RoleSent)
	case BoxDrafts:
		return `t.folder_ids && ` + roleSQL(RoleDrafts)
	case BoxTrash:
		return `t.folder_ids && ` + roleSQL(RoleTrash)
	case BoxSpam:
		return `t.folder_ids && ` + roleSQL(RoleSpam)
	case BoxArchive:
		// An Archive folder, or for Gmail (where archiving removes INBOX) in none of the
		// folders a conversation lives in otherwise.
		return `(t.folder_ids && ` + roleSQL(RoleArchive) + ` OR (EXISTS (SELECT 1 FROM collab_accounts ga
			WHERE ga.id = t.account_id AND ga.provider = 'google') AND NOT (t.folder_ids && ` +
			roleSQL(RoleInbox, RoleTrash, RoleSpam, RoleDrafts) + `)))`
	case BoxAll:
		return notJunk
	case BoxShared:
		return `EXISTS (SELECT 1 FROM collab_shares xs WHERE xs.thread_id = t.id) AND ` + notJunk
	case BoxAssigned:
		return `EXISTS (SELECT 1 FROM collab_assignees xa WHERE xa.thread_id = t.id AND xa.user_id = ` + u + `) AND ` + notJunk
	}
	return `t.folder_ids && ` + inboxSQL
}

// visibleAccountsSQL is the accounts the caller (SQL parameter u) sees in the workspace ($1).
func visibleAccountsSQL(u string) string {
	return `(SELECT a.id FROM collab_accounts a WHERE a.workspace_id = $1 AND ` + accountVisibleSQL("a", u) + `)`
}

type listThreadsInput struct {
	Box       string `query:"box" enum:"inbox,unread,starred,sent,drafts,archive,trash,spam,all,shared,assigned" default:"inbox"`
	AccountID string `query:"account_id" doc:"Only this account's conversations"`
	FolderID  string `query:"folder_id" doc:"Only this folder's conversations; replaces box"`
	Q         string `query:"q" maxLength:"200" doc:"Matches each message's subject, sender and text, and the conversation's subject and participants"`
	To        string `query:"to" maxLength:"254" doc:"Only conversations with this address on them (one of the person's addresses)"`
	With      string `query:"with" maxLength:"254" doc:"Only conversations with this person on them; without account_id, every conversation the caller sees"`
	httpx.ListParams
}

type threadCursor struct {
	T time.Time `json:"t"`
	I uuid.UUID `json:"i"`
}

// CollabCounts are the sidebar's numbers (contracts/collab.md "GET /v1/collab/counts").
type CollabCounts struct {
	Boxes    CollabBoxCounts   `json:"boxes"`
	Accounts map[uuid.UUID]int `json:"accounts"`
	Folders  map[uuid.UUID]int `json:"folders"`
	// Addresses counts, for each account with addresses besides its own, the unread
	// conversations in its inbox with each of its addresses on a message (the threads
	// list's to=); an address without any is left out.
	Addresses map[uuid.UUID]map[string]int `json:"addresses"`
	// Assigned: boxes.assigned by account, for the row under each account.
	Assigned map[uuid.UUID]int `json:"assigned"`
}

// CollabBoxCounts are unread conversations; assigned and shared: open ones (in an inbox).
type CollabBoxCounts struct {
	Inbox    int `json:"inbox"`
	Unread   int `json:"unread"`
	Shared   int `json:"shared"`
	Assigned int `json:"assigned"`
	Drafts   int `json:"drafts"`
}

type threadIDInput struct {
	ID string `path:"id"`
}

type detailBody struct {
	Body MailThreadDetail
}

func (s *service) registerThreadRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "list-collab-threads", Method: http.MethodGet, Path: "/v1/collab/threads",
		Summary: "List conversations"},
		func(ctx context.Context, in *listThreadsInput) (*struct{ Body httpx.Page[MailThread] }, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var cur threadCursor
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			limit := in.Limit
			if limit <= 0 || limit > 100 {
				limit = 50
			}
			args := []any{p.WorkspaceID}
			arg := func(v any) string {
				args = append(args, v)
				return fmt.Sprintf("$%d", len(args))
			}
			// The caller's id is a parameter only when the query uses it.
			userArg := ""
			user := func() string {
				if userArg == "" {
					userArg = arg(p.UserID)
				}
				return userArg
			}
			where := []string{`t.workspace_id = $1`}
			box := in.Box
			if box == "" {
				box = BoxInbox
			}
			var accountID *uuid.UUID
			if in.AccountID != "" {
				id, err := parseID(in.AccountID, "Account")
				if err != nil {
					return nil, err
				}
				if _, err := visibleAccount(ctx, s.d.DB.Pool, p, id); err != nil {
					return nil, notFound(err, "Account")
				}
				accountID = &id
			}
			switch {
			case in.FolderID != "":
				id, err := parseID(in.FolderID, "Folder")
				if err != nil {
					return nil, err
				}
				f, err := loadFolder(ctx, s.d.DB.Pool, p.WorkspaceID, id)
				if err != nil {
					return nil, notFound(err, "Folder")
				}
				if _, err := visibleAccount(ctx, s.d.DB.Pool, p, f.AccountID); err != nil || (accountID != nil && *accountID != f.AccountID) {
					return nil, httpx.NotFound("Folder")
				}
				where = append(where, `t.account_id = `+arg(f.AccountID), `t.folder_ids @> ARRAY[`+arg(f.ID)+`::uuid]`)
			default:
				switch {
				case accountID != nil:
					where = append(where, `t.account_id = `+arg(*accountID))
				case box == BoxShared || box == BoxAssigned || in.With != "":
					// A person's conversations include those shared with the caller on their own.
					where = append(where, threadVisibleSQL("t", user()))
				default:
					where = append(where, `t.account_id IN `+visibleAccountsSQL(user()))
				}
				u := "" // only the assigned box names the caller
				if box == BoxAssigned {
					u = user()
				}
				where = append(where, boxSQL(box, u))
			}
			if q := strings.TrimSpace(in.Q); q != "" {
				// The conversation's subject and people, or any of its messages (contracts/collab.md "Search").
				tq := `websearch_to_tsquery('simple', ` + arg(q) + `)`
				where = append(where, `(t.search @@ `+tq+` OR t.subject ILIKE `+arg("%"+escapeLike(q)+"%")+`
					OR t.id IN (SELECT m.thread_id FROM collab_messages m WHERE m.workspace_id = $1 AND m.search @@ `+tq+`))`)
			}
			if to := strings.ToLower(strings.TrimSpace(in.To)); to != "" {
				where = append(where, arg(to)+` = ANY(t.addresses)`)
			}
			if with := strings.ToLower(strings.TrimSpace(in.With)); with != "" {
				// @> rather than = ANY, so the addresses index serves it.
				where = append(where, `t.addresses @> ARRAY[`+arg(with)+`::text]`)
			}
			if in.Cursor != "" {
				where = append(where, `(t.last_message_at, t.id) < (`+arg(cur.T)+`, `+arg(cur.I)+`)`)
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+threadColumns+` FROM collab_threads t WHERE `+strings.Join(where, " AND ")+`
				ORDER BY t.last_message_at DESC, t.id DESC LIMIT `+arg(limit+1), args...)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*threadRow, error) { return scanThread(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := threadsOut(ctx, s.d.DB.Pool, list)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page := httpx.NewPage(out, limit, func(t MailThread) any { return threadCursor{T: t.LastMessageAt, I: t.ID} })
			return &struct{ Body httpx.Page[MailThread] }{page}, nil
		})

	collabOp(api, huma.Operation{OperationID: "get-collab-counts", Method: http.MethodGet, Path: "/v1/collab/counts",
		Summary: "Count unread conversations for the sidebar"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body CollabCounts }, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.counts(ctx, s.d.DB.Pool, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CollabCounts }{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "get-collab-thread", Method: http.MethodGet, Path: "/v1/collab/threads/{id}",
		Summary: "Get a conversation with its messages and comments"},
		func(ctx context.Context, in *threadIDInput) (*detailBody, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Conversation")
			if err != nil {
				return nil, err
			}
			out, err := s.threadDetail(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Conversation")
			}
			return &detailBody{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "collab-thread-actions", Method: http.MethodPost, Path: "/v1/collab/threads/actions",
		Summary: "Act on conversations"},
		func(ctx context.Context, in *struct {
			Body struct {
				ThreadIDs []uuid.UUID `json:"thread_ids" minItems:"1" maxItems:"100"`
				Action    string      `json:"action" enum:"read,unread,star,unstar,archive,inbox,trash,spam,not_spam,move,label,unlabel"`
				FolderID  *uuid.UUID  `json:"folder_id,omitempty" doc:"The folder or label of move, label and unlabel"`
			}
		}) (*struct {
			Body struct {
				Threads []MailThread `json:"threads"`
			}
		}, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b := in.Body
			if !slices.Contains(Actions, b.Action) {
				return nil, httpx.Validation("Unknown action.")
			}
			tids := appendNew(nil, b.ThreadIDs...)
			if len(tids) == 0 || len(tids) > 100 {
				return nil, httpx.Validation("Choose 1 to 100 conversations.")
			}
			threads, err := visibleThreads(ctx, s.d.DB.Pool, p, tids)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if len(threads) != len(tids) {
				return nil, httpx.NotFound("Conversation")
			}
			var folder *folderRow
			if b.Action == ActionMove || b.Action == ActionLabel || b.Action == ActionUnlabel {
				if b.FolderID == nil {
					return nil, httpx.Validation("Choose a folder.")
				}
				if folder, err = loadFolder(ctx, s.d.DB.Pool, p.WorkspaceID, *b.FolderID); err != nil {
					return nil, notFound(err, "Folder")
				}
				for _, t := range threads {
					if t.AccountID != folder.AccountID {
						return nil, httpx.Validation("The folder is not in the conversation's account.")
					}
				}
			}
			byAccount := map[uuid.UUID][]uuid.UUID{}
			var order []uuid.UUID
			for _, t := range threads {
				if _, ok := byAccount[t.AccountID]; !ok {
					order = append(order, t.AccountID)
				}
				byAccount[t.AccountID] = append(byAccount[t.AccountID], t.ID)
			}
			for _, aid := range order {
				a, err := loadAccount(ctx, s.d.DB.Pool, p.WorkspaceID, aid)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if (b.Action == ActionLabel || b.Action == ActionUnlabel) && a.Provider == ProviderIMAP {
					return nil, errLabels
				}
				list := byAccount[aid]
				err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
					if err := s.applyLocal(ctx, tx, a, list, b.Action, folder); err != nil {
						return err
					}
					return s.publishChanged(ctx, tx, p.WorkspaceID, &a.ID, list, true)
				})
				if err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			fresh, err := visibleThreads(ctx, s.d.DB.Pool, p, tids)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			slices.SortFunc(fresh, func(x, y *threadRow) int { return slices.Index(tids, x.ID) - slices.Index(tids, y.ID) })
			out, err := threadsOut(ctx, s.d.DB.Pool, fresh)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			resp := &struct {
				Body struct {
					Threads []MailThread `json:"threads"`
				}
			}{}
			resp.Body.Threads = out
			return resp, nil
		})

	collabOp(api, huma.Operation{OperationID: "share-collab-thread", Method: http.MethodPut, Path: "/v1/collab/threads/{id}/shares",
		Summary: "Share a conversation on its own"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Shares Audience `json:"shares"`
			}
		}) (*detailBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Conversation")
			if err != nil {
				return nil, err
			}
			t, err := visibleThread(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Conversation")
			}
			shares, err := checkAudience(ctx, s.d.DB.Pool, p.WorkspaceID, in.Body.Shares)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				before, err := threadsAudience(ctx, tx, p.WorkspaceID, []uuid.UUID{t.ID})
				if err != nil {
					return err
				}
				if err := saveAudience(ctx, tx, p.WorkspaceID, nil, &t.ID, shares, p.UserID); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.thread_shared", "collab_thread", &t.ID, map[string]any{
					"thread_id": t.ID, "everyone": shares.Everyone, "team_ids": shares.TeamIDs, "user_ids": shares.UserIDs}); err != nil {
					return err
				}
				return s.publishThread(ctx, tx, p.WorkspaceID, t, before)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.detailResponse(ctx, p, t.ID)
		})

	collabOp(api, huma.Operation{OperationID: "assign-collab-thread", Method: http.MethodPut, Path: "/v1/collab/threads/{id}/assignees",
		Summary: "Assign a conversation"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				UserIDs []uuid.UUID `json:"user_ids" maxItems:"20"`
			}
		}) (*detailBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Conversation")
			if err != nil {
				return nil, err
			}
			t, err := visibleThread(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Conversation")
			}
			users := appendNew(nil, in.Body.UserIDs...)
			if len(users) > maxAssignees {
				return nil, httpx.Validation("Assign at most 20 people.")
			}
			if len(users) > 0 {
				var n int
				if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1 AND user_id = ANY($2)
					AND role IN ('owner', 'admin', 'agent')`, p.WorkspaceID, users).Scan(&n); err != nil {
					return nil, httpx.Wrap(err)
				}
				if n != len(users) {
					return nil, httpx.Validation("Assign members who are agents or above.")
				}
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				before, err := threadsAudience(ctx, tx, p.WorkspaceID, []uuid.UUID{t.ID})
				if err != nil {
					return err
				}
				if users == nil {
					users = []uuid.UUID{}
				}
				if _, err := tx.Exec(ctx, `DELETE FROM collab_assignees WHERE thread_id = $1 AND NOT (user_id = ANY($2))`, t.ID, users); err != nil {
					return err
				}
				rows, err := tx.Query(ctx, `INSERT INTO collab_assignees (thread_id, user_id, assigned_by)
					SELECT $1, u, $3 FROM unnest($2::uuid[]) u ON CONFLICT DO NOTHING RETURNING user_id`, t.ID, users, p.UserID)
				if err != nil {
					return err
				}
				added, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
				if err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.thread_assigned", "collab_thread", &t.ID, map[string]any{
					"thread_id": t.ID, "user_ids": users}); err != nil {
					return err
				}
				for _, uid := range added {
					if uid == p.UserID {
						continue
					}
					if err := s.notify(ctx, tx, p.WorkspaceID, uid, NotifyAssigned, map[string]any{
						"thread_id": t.ID.String(), "account_id": t.AccountID.String(), "by": p.UserID.String(), "subject": t.Subject}); err != nil {
						return err
					}
				}
				return s.publishThread(ctx, tx, p.WorkspaceID, t, before)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.detailResponse(ctx, p, t.ID)
		})
}

// escapeLike escapes LIKE wildcards in a search term.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// publishThread publishes collab.changed for one conversation to who sees it now and to
// who saw it on its own before (before), so a removed share or assignee refetches too.
func (s *service) publishThread(ctx context.Context, tx pgx.Tx, wid uuid.UUID, t *threadRow, before []uuid.UUID) error {
	if err := s.publishChanged(ctx, tx, wid, &t.AccountID, []uuid.UUID{t.ID}, false); err != nil {
		return err
	}
	now, err := accountAudience(ctx, tx, wid, t.AccountID)
	if err != nil {
		return err
	}
	list, err := threadsAudience(ctx, tx, wid, []uuid.UUID{t.ID})
	if err != nil {
		return err
	}
	now = appendNew(now, list...)
	var gone []uuid.UUID
	for _, u := range before {
		if !slices.Contains(now, u) {
			gone = appendNew(gone, u)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: EventChanged, Users: gone,
		Data: map[string]any{"account_id": t.AccountID, "thread_ids": []uuid.UUID{t.ID}, "folders": false}})
	return err
}

// threadDetail loads a conversation the caller sees with everything its page shows.
func (s *service) threadDetail(ctx context.Context, q db.Querier, p *auth.Principal, id uuid.UUID) (MailThreadDetail, error) {
	var out MailThreadDetail
	t, err := visibleThread(ctx, q, p, id)
	if err != nil {
		return out, err
	}
	a, err := loadAccount(ctx, q, p.WorkspaceID, t.AccountID)
	if err != nil {
		return out, err
	}
	threads, err := threadsOut(ctx, q, []*threadRow{t})
	if err != nil {
		return out, err
	}
	rows, err := q.Query(ctx, `SELECT `+messageColumns+` FROM collab_messages m WHERE m.workspace_id = $1 AND m.thread_id = $2
		ORDER BY m.sent_at, m.id`, p.WorkspaceID, t.ID)
	if err != nil {
		return out, err
	}
	msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*messageRow, error) { return scanMessage(r) })
	if err != nil {
		return out, err
	}
	messages, err := s.messagesOut(ctx, q, a, msgs)
	if err != nil {
		return out, err
	}
	rows, err = q.Query(ctx, `SELECT `+commentColumns+` FROM collab_comments WHERE workspace_id = $1 AND thread_id = $2
		ORDER BY created_at, id`, p.WorkspaceID, t.ID)
	if err != nil {
		return out, err
	}
	crows, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*commentRow, error) { return scanComment(r) })
	if err != nil {
		return out, err
	}
	comments, err := commentsOut(ctx, q, crows)
	if err != nil {
		return out, err
	}
	shares, err := loadAudience(ctx, q, p.WorkspaceID, nil, &t.ID)
	if err != nil {
		return out, err
	}
	name := a.Name
	if name == "" {
		name = a.Email
	}
	idents, err := accountIdentities(ctx, q, a)
	if err != nil {
		return out, err
	}
	account := ThreadAccount{ID: a.ID, Email: a.Email, Name: name, DisplayName: a.DisplayName, Color: a.Color, Provider: a.Provider,
		SignatureHTML: a.SignatureHTML}
	if account.Identities, account.DefaultFrom, err = identitiesOut(ctx, s.fillerFor(q, p.WorkspaceID, p.UserID), a, idents); err != nil {
		return out, err
	}
	ticket, err := threadTicket(ctx, q, p, t.ID)
	if err != nil {
		return out, err
	}
	return MailThreadDetail{Thread: threads[0], Messages: messages, Comments: comments, Shares: shares, CanAct: canAct(p),
		Account: account, Ticket: ticket}, nil
}

// threadTicket is the conversation's ticket when p sees it, else nil.
func threadTicket(ctx context.Context, q db.Querier, p *auth.Principal, threadID uuid.UUID) (*ThreadTicket, error) {
	var id *uuid.UUID
	if err := q.QueryRow(ctx, `SELECT ticket_id FROM collab_threads WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, threadID).Scan(&id); err != nil {
		return nil, err
	}
	if id == nil {
		return nil, nil
	}
	t, err := domain.LoadVisibleTicket(ctx, q, p, *id)
	if db.IsNotFound(err) || errors.Is(err, domain.ErrNotVisible) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ThreadTicket{ID: t.ID, Number: t.Number, Subject: t.Subject}, nil
}

func (s *service) detailResponse(ctx context.Context, p *auth.Principal, id uuid.UUID) (*detailBody, error) {
	out, err := s.threadDetail(ctx, s.d.DB.Pool, p, id)
	if err != nil {
		return nil, notFound(err, "Conversation")
	}
	return &detailBody{out}, nil
}

// counts are the sidebar's numbers for the caller.
func (s *service) counts(ctx context.Context, q db.Querier, p *auth.Principal) (CollabCounts, error) {
	out := CollabCounts{Accounts: map[uuid.UUID]int{}, Folders: map[uuid.UUID]int{}, Assigned: map[uuid.UUID]int{}, Addresses: map[uuid.UUID]map[string]int{}}
	inAccounts := `t.workspace_id = $1 AND t.account_id IN ` + visibleAccountsSQL("$2")
	err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM collab_threads t WHERE `+inAccounts+` AND t.unread AND t.folder_ids && `+inboxSQL+`),
		(SELECT count(*) FROM collab_threads t WHERE `+inAccounts+` AND `+boxSQL(BoxUnread, "$2")+`),
		(SELECT count(*) FROM collab_threads t WHERE `+inAccounts+` AND `+boxSQL(BoxDrafts, "$2")+`),
		(SELECT count(*) FROM collab_threads t WHERE t.workspace_id = $1 AND `+threadVisibleSQL("t", "$2")+`
			AND EXISTS (SELECT 1 FROM collab_shares xs WHERE xs.thread_id = t.id) AND t.folder_ids && `+inboxSQL+`),
		(SELECT count(*) FROM collab_threads t WHERE t.workspace_id = $1
			AND EXISTS (SELECT 1 FROM collab_assignees xa WHERE xa.thread_id = t.id AND xa.user_id = $2) AND t.folder_ids && `+inboxSQL+`)`,
		p.WorkspaceID, p.UserID).Scan(&out.Boxes.Inbox, &out.Boxes.Unread, &out.Boxes.Drafts, &out.Boxes.Shared, &out.Boxes.Assigned)
	if err != nil {
		return out, err
	}
	rows, err := q.Query(ctx, `SELECT t.account_id, count(*) FROM collab_threads t WHERE `+inAccounts+` AND t.unread
		AND t.folder_ids && `+inboxSQL+` GROUP BY t.account_id`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	if out.Accounts, err = collectCounts(rows); err != nil {
		return out, err
	}
	rows, err = q.Query(ctx, `SELECT fid, count(*) FROM collab_threads t, unnest(t.folder_ids) fid WHERE `+inAccounts+` AND t.unread
		GROUP BY fid`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	if out.Folders, err = collectCounts(rows); err != nil {
		return out, err
	}
	rows, err = q.Query(ctx, `SELECT t.account_id, count(*) FROM collab_threads t WHERE t.workspace_id = $1
		AND EXISTS (SELECT 1 FROM collab_assignees xa WHERE xa.thread_id = t.id AND xa.user_id = $2) AND t.folder_ids && `+inboxSQL+`
		GROUP BY t.account_id`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	if out.Assigned, err = collectCounts(rows); err != nil {
		return out, err
	}
	// Every address of the accounts with aliases (their own included), matched like to=.
	rows, err = q.Query(ctx, `SELECT t.account_id, x.email, count(*) FROM collab_threads t
		JOIN (SELECT i.account_id, i.email FROM collab_identities i WHERE i.workspace_id = $1
			UNION SELECT a.id, lower(a.email) FROM collab_accounts a WHERE a.workspace_id = $1
				AND EXISTS (SELECT 1 FROM collab_identities i WHERE i.account_id = a.id)) x
			ON x.account_id = t.account_id AND t.addresses @> ARRAY[x.email]
		WHERE `+inAccounts+` AND t.unread AND t.folder_ids && `+inboxSQL+`
		GROUP BY t.account_id, x.email`, p.WorkspaceID, p.UserID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var aid uuid.UUID
		var addr string
		var n int
		if err := rows.Scan(&aid, &addr, &n); err != nil {
			return out, err
		}
		if out.Addresses[aid] == nil {
			out.Addresses[aid] = map[string]int{}
		}
		out.Addresses[aid][addr] = n
	}
	return out, rows.Err()
}
