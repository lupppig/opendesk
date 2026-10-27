package collab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// History imports (contracts/collab.md "History"): an account's older mail, read as a step
// of each sync pass after the live changes, from a place stored with every page, within the
// workspace's Collab storage ("Storage"). The provider steps are in history_providers.go.

// History import statuses.
const (
	HistoryRunning  = "running"
	HistoryPaused   = "paused"
	HistoryLimited  = "limited" // at the storage allowance; goes on when there is room
	HistoryDone     = "done"
	HistoryCanceled = "canceled"
	HistoryError    = "error"
)

const (
	historyPassTime = 50 * time.Second // the most one pass spends on history
	historyPassMax  = 1000             // and the most messages it reads
	historyFetchers = 4                // messages read at once (Gmail, Microsoft)
	historyPage     = syncBatch        // messages stored in one transaction
)

// historyRow is an account's latest history import (collab_history).
type historyRow struct {
	AccountID        uuid.UUID
	WorkspaceID      uuid.UUID
	RequestedBy      *uuid.UUID
	Status           string
	Since            *time.Time
	IncludeSpamTrash bool
	StartedAt        time.Time
	FinishedAt       *time.Time
	Cursor           json.RawMessage
	Reached          *time.Time
	Imported         int
	Present          int
	TooLarge         int
	Unreadable       int
	Total            *int
	LastError        string
}

const historyColumns = `account_id, workspace_id, requested_by, status, since, include_spam_trash, started_at, finished_at, cursor,
	reached, imported, present, too_large, unreadable, total, last_error`

func scanHistory(row pgx.Row) (*historyRow, error) {
	var h historyRow
	err := row.Scan(&h.AccountID, &h.WorkspaceID, &h.RequestedBy, &h.Status, &h.Since, &h.IncludeSpamTrash, &h.StartedAt, &h.FinishedAt,
		&h.Cursor, &h.Reached, &h.Imported, &h.Present, &h.TooLarge, &h.Unreadable, &h.Total, &h.LastError)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// loadHistory loads an account's import, nil when none was started.
func loadHistory(ctx context.Context, q db.Querier, accountID uuid.UUID) (*historyRow, error) {
	h, err := scanHistory(q.QueryRow(ctx, `SELECT `+historyColumns+` FROM collab_history WHERE account_id = $1`, accountID))
	if db.IsNotFound(err) {
		return nil, nil
	}
	return h, err
}

// active reports whether the import still reads (or waits for room to).
func (h *historyRow) active() bool { return h.Status == HistoryRunning || h.Status == HistoryLimited }

// ended reports whether the import is over.
func (h *historyRow) ended() bool {
	return h.Status == HistoryDone || h.Status == HistoryCanceled || h.Status == HistoryError
}

// historyRun is one pass's share of an import.
type historyRun struct {
	h        *historyRow
	p        *pass
	deadline time.Time
	left     int   // messages it may still read
	room     int64 // bytes it may still store; negative = unlimited
	limited  bool  // stopped at the storage allowance
	done     bool  // read everything in scope
}

// errHistoryMoved: the import was paused, stopped or started again during the pass. The
// page being stored is rolled back and the pass leaves the import alone.
var errHistoryMoved = errors.New("the history import changed meanwhile")

// more reports whether the run may read another page.
func (r *historyRun) more(ctx context.Context) bool {
	return ctx.Err() == nil && !r.limited && !r.done && r.left > 0 && time.Now().Before(r.deadline)
}

// pageCounts is what a page found besides the mail it stores (the pass counts that).
type pageCounts struct {
	present, tooLarge int
	reached           time.Time // the oldest message the page read
	total             *int      // the scope's size, when the provider just said
}

func (c *pageCounts) saw(t time.Time) {
	if !t.IsZero() && (c.reached.IsZero() || t.Before(c.reached)) {
		c.reached = t
	}
}

// store runs fn (the page's writes) in one transaction with the import's new place and
// counts, so an import cut off goes on after the last page stored.
func (r *historyRun) store(ctx context.Context, pc pageCounts, cursor any, fn func(tx pgx.Tx) error) error {
	b, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	p := r.p
	imported, unreadable, added, oldest := p.imported, p.unreadable, p.addedBytes, p.oldest
	p.oldest = time.Time{}
	err = p.batch(ctx, func(tx pgx.Tx) error {
		if fn != nil {
			if err := fn(tx); err != nil {
				return err
			}
		}
		pc.saw(p.oldest)
		var reached *time.Time
		if !pc.reached.IsZero() {
			reached = &pc.reached
		}
		tag, err := tx.Exec(ctx, `UPDATE collab_history SET cursor = $3, imported = imported + $4, present = present + $5,
			too_large = too_large + $6, unreadable = unreadable + $7, reached = LEAST(reached, $8), total = COALESCE($9, total),
			last_error = '', updated_at = now()
			WHERE account_id = $1 AND started_at = $2 AND status = 'running'`,
			r.h.AccountID, r.h.StartedAt, b, p.imported-imported, pc.present, pc.tooLarge, p.unreadable-unreadable, reached, pc.total)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errHistoryMoved
		}
		return nil
	})
	if err != nil {
		// Nothing of the page was kept.
		p.imported, p.unreadable, p.addedBytes, p.oldest = imported, unreadable, added, oldest
		return err
	}
	p.oldest = earliest(oldest, p.oldest)
	read := pc.tooLarge + (p.imported - imported) + (p.unreadable - unreadable)
	r.left -= max(read, 1)
	r.h.Cursor = b
	r.h.Imported += p.imported - imported
	r.h.Present += pc.present
	r.h.TooLarge += pc.tooLarge
	r.h.Unreadable += p.unreadable - unreadable
	if !pc.reached.IsZero() && (r.h.Reached == nil || pc.reached.Before(*r.h.Reached)) {
		t := pc.reached
		r.h.Reached = &t
	}
	if r.room >= 0 {
		if r.room -= p.addedBytes - added; r.room <= 0 {
			r.limited = true
		}
	}
	return nil
}

func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero() || a.Before(b):
		return a
	}
	return b
}

// runHistory runs the account's history import for a while, when one is running. used is
// the workspace's Collab storage with this pass's new mail, limit its allowance (0 =
// unlimited). Only a refused grant or the job's own time running out fails the pass: any
// other failure is kept on the import and the next pass tries again.
func (s *service) runHistory(ctx context.Context, p *pass, prov Provider, used, limit int64) error {
	h, err := loadHistory(ctx, s.d.DB.Pool, p.a.ID)
	if err != nil || h == nil || !h.active() {
		return err
	}
	if limit > 0 && used >= limit {
		return s.setHistoryStatus(ctx, p.a, h, HistoryLimited, "")
	}
	if h.Status == HistoryLimited {
		if err := s.setHistoryStatus(ctx, p.a, h, HistoryRunning, ""); err != nil {
			return err
		}
	}
	r := &historyRun{h: h, p: p, deadline: time.Now().Add(historyPassTime), left: historyPassMax, room: -1}
	if limit > 0 {
		// On an instance with an allowance, history keeps no raw copy: its files are read
		// from the provider when opened.
		r.room, p.noRaw = limit-used, true
	}
	// Older mail is told about once, at the end of the step.
	p.quiet = true
	defer func() { p.noRaw, p.quiet, p.held = false, false, false }()
	defer func() {
		if p.held {
			id, bg := p.a.ID, context.WithoutCancel(ctx)
			if err := s.d.DB.Tx(bg, func(tx pgx.Tx) error {
				return s.publishChanged(bg, tx, p.a.WorkspaceID, &id, []uuid.UUID{}, true)
			}); err != nil {
				s.log.Warn("collab history change event", "account_id", id, "err", err)
			}
		}
	}()
	switch pr := prov.(type) {
	case *gmailProvider:
		err = s.gmailHistory(ctx, r, pr)
	case *graphProvider:
		err = s.graphHistory(ctx, r, pr)
	case *imapProvider:
		err = s.imapHistory(ctx, r, pr)
	default:
		r.done = true // a sample account's mail is all here
	}
	switch {
	case errors.Is(err, errHistoryMoved):
		return nil
	case err != nil && (isReconnect(err) || ctx.Err() != nil):
		return err
	case err != nil && isPermanent(err):
		s.log.Info("collab history import stopped", "account_id", p.a.ID, "reason", safeError(err))
		return s.setHistoryStatus(ctx, p.a, h, HistoryError, safeError(err))
	case err != nil:
		s.log.Info("collab history import failed, trying again next pass", "account_id", p.a.ID, "reason", safeError(err))
		_, err := s.d.DB.Pool.Exec(ctx, `UPDATE collab_history SET last_error = $3, updated_at = now()
			WHERE account_id = $1 AND started_at = $2 AND status = 'running'`, h.AccountID, h.StartedAt, safeError(err))
		if err != nil {
			return err
		}
		return s.publishHistoryProgress(ctx, p.a)
	case r.done:
		return s.setHistoryStatus(ctx, p.a, h, HistoryDone, "")
	case r.limited:
		return s.setHistoryStatus(ctx, p.a, h, HistoryLimited, "")
	}
	return s.publishHistoryProgress(ctx, p.a)
}

// setHistoryStatus moves a running or limited import to status and tells everyone who sees
// the account. An import paused, stopped or started again meanwhile is left as it is.
func (s *service) setHistoryStatus(ctx context.Context, a *accountRow, h *historyRow, status, lastError string) error {
	if h.Status == status && lastError == "" {
		return nil
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE collab_history SET status = $3, last_error = $4,
			finished_at = CASE WHEN $3 IN ('done', 'canceled', 'error') THEN now() END, updated_at = now()
			WHERE account_id = $1 AND started_at = $2 AND status IN ('running', 'limited')`, h.AccountID, h.StartedAt, status, lastError)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		h.Status = status
		return s.publishAccounts(ctx, tx, a.WorkspaceID, a.ID, nil)
	})
}

// publishHistoryProgress tells the account's managers how far its import got: the
// settings page follows it, and nobody else needs a refetch a minute.
func (s *service) publishHistoryProgress(ctx context.Context, a *accountRow) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		users, err := accountManagers(ctx, tx, a)
		if err != nil || len(users) == 0 {
			return err
		}
		return s.publishAccountsTo(ctx, tx, a.WorkspaceID, a.ID, users)
	})
}

// accountManagers is who manages an account: its owner, and for a shared account the
// workspace's admins (canManageAccount).
func accountManagers(ctx context.Context, q db.Querier, a *accountRow) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT owner_id FROM collab_accounts WHERE id = $1 AND owner_id IS NOT NULL
		UNION SELECT m.user_id FROM members m WHERE m.workspace_id = $2 AND $3 AND m.role IN ('owner', 'admin')`,
		a.ID, a.WorkspaceID, a.Visibility == VisibilityShared)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// fetchEach runs fetch for each id, at most historyFetchers at once, each with a provider
// of its own (a token refresh writes to its account copy). It returns the results and the
// errors in the order of ids.
func fetchEach[P any, T any](ctx context.Context, ids []string, open func() P, fetch func(context.Context, P, string) (T, error)) ([]T, []error) {
	out := make([]T, len(ids))
	errs := make([]error, len(ids))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(historyFetchers, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prov := open()
			for i := range next {
				out[i], errs[i] = fetch(ctx, prov, ids[i])
			}
		}()
	}
	for i := range ids {
		next <- i
	}
	close(next)
	wg.Wait()
	return out, errs
}

// copyAccount is a copy of an account row for a fetcher of its own.
func copyAccount(a *accountRow) *accountRow {
	c := *a
	return &c
}

// Storage (contracts/collab.md "Storage").

// storageLimit is the workspace's Collab storage allowance, 0 when unlimited.
func (s *service) storageLimit(ctx context.Context, wid uuid.UUID) (int64, error) {
	e, err := s.d.Plan.Entitlements(ctx, s.d.DB.Pool, wid)
	return e.CollabStorageBytes, err
}

// storageUsed is what the workspace's Collab mail keeps, as last summed by the sync.
func storageUsed(ctx context.Context, q db.Querier, wid uuid.UUID) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COALESCE(sum(stored_bytes), 0) FROM collab_accounts WHERE workspace_id = $1`, wid).Scan(&n)
	return n, err
}

// recountStorage sums what the account's messages keep (collab_accounts.stored_bytes),
// at the end of each pass.
func recountStorage(ctx context.Context, q db.Querier, accountID uuid.UUID) error {
	_, err := q.Exec(ctx, `WITH s AS (SELECT COALESCE(sum(size), 0) AS n FROM collab_messages WHERE account_id = $1)
		UPDATE collab_accounts a SET stored_bytes = s.n FROM s WHERE a.id = $1 AND a.stored_bytes <> s.n`, accountID)
	return err
}

// storageStatus is CollabStatus.storage for a workspace with its allowance (limit).
func (s *service) storageStatus(ctx context.Context, wid uuid.UUID, limit int64) (CollabStorage, error) {
	used, err := storageUsed(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return CollabStorage{}, err
	}
	return CollabStorage{UsedBytes: used, LimitBytes: limit, HistoryLimited: limit > 0 && used >= limit, Full: limit > 0 && used >= 2*limit}, nil
}

// Output.

// historiesOut loads what MailAccount says about each account's history: its import, the
// oldest mail stored and, for IMAP, the folders past the ones synced.
func historiesOut(ctx context.Context, q db.Querier, wid uuid.UUID, list []*accountRow) (map[uuid.UUID]*MailHistory, map[uuid.UUID]*time.Time, map[uuid.UUID]int, error) {
	ids := make([]uuid.UUID, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	hist := map[uuid.UUID]*MailHistory{}
	rows, err := q.Query(ctx, `SELECT `+historyColumns+` FROM collab_history WHERE workspace_id = $1 AND account_id = ANY($2)`, wid, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	hrows, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*historyRow, error) { return scanHistory(r) })
	if err != nil {
		return nil, nil, nil, err
	}
	var users []uuid.UUID
	for _, h := range hrows {
		if h.RequestedBy != nil {
			users = append(users, *h.RequestedBy)
		}
	}
	refs, err := userRefs(ctx, q, users)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, h := range hrows {
		hist[h.AccountID] = &MailHistory{Status: h.Status, Since: h.Since, IncludeSpamTrash: h.IncludeSpamTrash, RequestedBy: refOf(refs, h.RequestedBy),
			StartedAt: h.StartedAt, FinishedAt: h.FinishedAt, Reached: h.Reached, Imported: h.Imported, Present: h.Present,
			TooLarge: h.TooLarge, Unreadable: h.Unreadable, Total: h.Total, LastError: h.LastError}
	}
	oldest := map[uuid.UUID]*time.Time{}
	rows, err = q.Query(ctx, `SELECT a.id, (SELECT m.sent_at FROM collab_messages m WHERE m.account_id = a.id ORDER BY m.sent_at LIMIT 1)
		FROM unnest($1::uuid[]) a(id)`, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var t *time.Time
		if err := rows.Scan(&id, &t); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		oldest[id] = t
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	leftOut := map[uuid.UUID]int{}
	rows, err = q.Query(ctx, `SELECT f.account_id, count(*) FROM collab_folders f JOIN collab_accounts a ON a.id = f.account_id
		WHERE f.workspace_id = $1 AND f.account_id = ANY($2) AND a.provider = 'imap' AND (f.role IS NULL OR f.role NOT IN ('all', 'starred', 'important'))
		GROUP BY f.account_id`, wid, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	counts, err := collectCounts(rows)
	if err != nil {
		return nil, nil, nil, err
	}
	for id, n := range counts {
		leftOut[id] = max(n-maxIMAPFolders, 0)
	}
	return hist, oldest, leftOut, nil
}

// historySince checks the start of an import's scope: in the past, from 1970 on.
func historySince(since *time.Time) (*time.Time, error) {
	if since == nil {
		return nil, nil
	}
	if !since.Before(time.Now()) || since.Year() < 1970 {
		return nil, httpx.Validation("Choose a date in the past.")
	}
	t := since.UTC()
	return &t, nil
}

// startHistory starts the account's history import in tx (contracts/collab.md "History"),
// audited. again starts one that exists again with this scope; otherwise an existing import
// is left as it is and started reports false. The caller queues the sync and publishes.
func startHistory(ctx context.Context, tx pgx.Tx, p *auth.Principal, accountID uuid.UUID, since *time.Time, spamTrash, again bool) (bool, error) {
	conflict := `DO NOTHING`
	if again {
		conflict = `DO UPDATE SET requested_by = $3, status = 'running', since = $4, include_spam_trash = $5,
			started_at = now(), finished_at = NULL, cursor = '{}', reached = NULL, imported = 0, present = 0, too_large = 0,
			unreadable = 0, total = NULL, last_error = '', updated_at = now()`
	}
	tag, err := tx.Exec(ctx, `INSERT INTO collab_history (account_id, workspace_id, requested_by, status, since, include_spam_trash)
		VALUES ($1, $2, $3, 'running', $4, $5) ON CONFLICT (account_id) `+conflict,
		accountID, p.WorkspaceID, p.UserID, since, spamTrash)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	return true, audit.Log(ctx, tx, p, p.WorkspaceID, "collab.history_import", "collab_account", &accountID,
		map[string]any{"action": "started", "since": since, "include_spam_trash": spamTrash})
}

// Routes.

type historyStartInput struct {
	ID   string `path:"id"`
	Body struct {
		Since            *time.Time `json:"since,omitempty" doc:"Mail received from then on; null or left out imports all mail"`
		IncludeSpamTrash bool       `json:"include_spam_trash,omitempty" doc:"Also spam and trash"`
	}
}

type historyPauseInput struct {
	ID   string `path:"id"`
	Body struct {
		Paused bool `json:"paused"`
	}
}

func (s *service) registerHistoryRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "start-collab-history", Method: http.MethodPut, Path: "/v1/collab/accounts/{id}/history",
		Summary: "Import a mail account's older mail"},
		func(ctx context.Context, in *historyStartInput) (*accountBody, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if a.Config.Demo {
				return nil, httpx.E(422, "history_unavailable", "A sample account has no older mail to import.")
			}
			since, err := historySince(in.Body.Since)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := startHistory(ctx, tx, p, a.ID, since, in.Body.IncludeSpamTrash, true); err != nil {
					return err
				}
				if err := s.queueSync(ctx, tx, a.ID); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "pause-collab-history", Method: http.MethodPatch, Path: "/v1/collab/accounts/{id}/history",
		Summary: "Pause or resume a history import"},
		func(ctx context.Context, in *historyPauseInput) (*accountBody, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				h, err := scanHistory(tx.QueryRow(ctx, `SELECT `+historyColumns+` FROM collab_history WHERE account_id = $1 FOR UPDATE`, a.ID))
				if err != nil {
					return err
				}
				if h.ended() {
					return httpx.Validation("This history import has ended. Start it again instead.")
				}
				status, action := HistoryPaused, "paused"
				if !in.Body.Paused {
					status, action = HistoryRunning, "resumed"
				}
				if in.Body.Paused == (h.Status == HistoryPaused) {
					return nil // already so
				}
				if _, err := tx.Exec(ctx, `UPDATE collab_history SET status = $2, last_error = '', updated_at = now() WHERE account_id = $1`,
					a.ID, status); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.history_import", "collab_account", &a.ID,
					map[string]any{"action": action, "since": h.Since, "include_spam_trash": h.IncludeSpamTrash}); err != nil {
					return err
				}
				if status == HistoryRunning {
					if err := s.queueSync(ctx, tx, a.ID); err != nil {
						return err
					}
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("History import")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "stop-collab-history", Method: http.MethodDelete, Path: "/v1/collab/accounts/{id}/history",
		Summary: "Stop a history import; what it imported stays"},
		func(ctx context.Context, in *accountIDInput) (*accountBody, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				h, err := scanHistory(tx.QueryRow(ctx, `SELECT `+historyColumns+` FROM collab_history WHERE account_id = $1 FOR UPDATE`, a.ID))
				if err != nil || h.ended() {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE collab_history SET status = 'canceled', finished_at = now(), updated_at = now()
					WHERE account_id = $1`, a.ID); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.history_import", "collab_account", &a.ID,
					map[string]any{"action": "canceled", "since": h.Since, "include_spam_trash": h.IncludeSpamTrash}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("History import")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})
}
