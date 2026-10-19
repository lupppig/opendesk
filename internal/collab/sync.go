package collab

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// The sync engine (contracts/collab.md "Sync"): one job per account at a time, folders
// first, then the provider's changes. Failures are stored on the account with a safe
// message and retried with backoff; a refused grant waits for a reconnect.

// syncBatch is how many messages one transaction stores.
const syncBatch = 25

// defaultProvider opens the real provider client of an account.
func (s *service) defaultProvider(_ context.Context, a *accountRow) (Provider, error) {
	switch a.Provider {
	case ProviderGoogle:
		return &gmailProvider{s: s, a: a}, nil
	case ProviderMicrosoft:
		return &graphProvider{s: s, a: a}, nil
	case ProviderIMAP:
		return s.newIMAPProvider(a)
	}
	return nil, &providerError{msg: "Unknown mail provider.", permanent: true}
}

// provider opens the provider client of an account. Close it when done. A demo account's
// provider is local: it never dials or sends (demo.go).
func (s *service) provider(ctx context.Context, a *accountRow) (Provider, error) {
	if a.Config.Demo {
		return &demoProvider{s: s, a: a}, nil
	}
	return s.newProvider(ctx, a)
}

// syncInsertOpts: one queued or running sync per account.
func syncInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
		rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
		rivertype.JobStateRunning, rivertype.JobStateScheduled}}}
}

// queueSync queues a sync of the account now (unique per account), in tx when q is one.
func (s *service) queueSync(ctx context.Context, q db.Querier, accountID uuid.UUID) error {
	if s.d.Jobs == nil {
		return nil
	}
	args := jobs.CollabSyncArgs{AccountID: accountID}
	var err error
	if tx, ok := q.(pgx.Tx); ok {
		_, err = s.d.Jobs.InsertTx(ctx, tx, args, syncInsertOpts())
	} else {
		_, err = s.d.Jobs.Insert(ctx, args, syncInsertOpts())
	}
	return err
}

// collabActive reports whether the workspace has Collab and is not suspended; background
// work for an account stops while it is not.
func (s *service) collabActive(ctx context.Context, wid uuid.UUID) (bool, error) {
	e, err := s.d.Plan.Entitlements(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return false, err
	}
	if !e.Collab {
		return false, nil
	}
	suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid)
	return !suspended, err
}

// syncAccount runs one sync pass of an account and records how it went.
func (s *service) syncAccount(ctx context.Context, id uuid.UUID) error {
	a, err := loadAccountByID(ctx, s.d.DB.Pool, id)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.Reconnect {
		return nil // waits for a reconnect
	}
	if a.Config.Demo {
		return s.recordSyncOK(ctx, a) // nothing to fetch: its mail is all here
	}
	ok, err := s.collabActive(ctx, a.WorkspaceID)
	if err != nil || !ok {
		return err
	}
	if err := s.runSync(ctx, a); err != nil {
		if ctx.Err() != nil && !isPermanent(err) {
			return err // the job ran out of time: River tries again
		}
		return s.recordSyncError(ctx, a, err)
	}
	return s.recordSyncOK(ctx, a)
}

// runSync syncs the folders, then the mail.
func (s *service) runSync(ctx context.Context, a *accountRow) error {
	prov, err := s.provider(ctx, a)
	if err != nil {
		return err
	}
	defer prov.Close()
	p, err := s.newPass(ctx, s.d.DB.Pool, a)
	if err != nil {
		return err
	}
	remote, err := prov.Folders(ctx)
	if err != nil {
		return err
	}
	if err := p.syncFolders(ctx, remote); err != nil {
		return err
	}
	switch pr := prov.(type) {
	case *gmailProvider:
		err = s.syncGmail(ctx, p, pr)
	case *graphProvider:
		err = s.syncGraph(ctx, p, pr)
	case *imapProvider:
		err = s.syncIMAP(ctx, p, pr)
	}
	if err != nil {
		return err
	}
	// The account's own settings, when due; they never fail the sync.
	s.syncSettings(ctx, prov)
	return p.batch(ctx, func(pgx.Tx) error { return nil }) // anything left to publish
}

// saveAccountState stores the account's provider cursor.
func (s *service) saveAccountState(ctx context.Context, a *accountRow, st any) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE collab_accounts SET sync_state = $2, updated_at = now() WHERE id = $1`, a.ID, b); err != nil {
		return err
	}
	a.SyncState = b
	return nil
}

func (s *service) recordSyncOK(ctx context.Context, a *accountRow) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var changed bool
		err := tx.QueryRow(ctx, `UPDATE collab_accounts SET status = 'active', last_error = '', sync_failures = 0, next_sync_at = NULL,
			last_synced_at = now(), updated_at = now() WHERE id = $1 RETURNING $2::text <> 'active'`, a.ID, a.Status).Scan(&changed)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil || !changed {
			return err
		}
		return s.publishAccounts(ctx, tx, a.WorkspaceID, a.ID, nil)
	})
}

// syncBackoff is the wait after the nth failure in a row: 1 minute doubling to 1 hour.
func syncBackoff(failures int) time.Duration {
	d := time.Minute
	for i := 1; i < failures && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}

func (s *service) recordSyncError(ctx context.Context, a *accountRow, cause error) error {
	msg := safeError(cause)
	reconnect := isReconnect(cause)
	s.log.Info("collab sync failed", "account_id", a.ID, "workspace_id", a.WorkspaceID, "reason", msg, "reconnect", reconnect)
	failures := a.SyncFailures + 1
	next := time.Now().Add(syncBackoff(failures))
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE collab_accounts SET status = 'error', last_error = $2, reconnect = $3, sync_failures = $4,
			next_sync_at = $5, updated_at = now() WHERE id = $1`, a.ID, msg, reconnect, failures, next)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if a.Status == "error" && a.LastError == msg && a.Reconnect == reconnect {
			return nil
		}
		return s.publishAccounts(ctx, tx, a.WorkspaceID, a.ID, nil)
	})
}

// specialOrder is the order of special folders: they come first, then the rest by path.
var specialOrder = []string{RoleInbox, RoleSent, RoleDrafts, RoleArchive, RoleSpam, RoleTrash, RoleStarred, RoleImportant, RoleAll}

func compareFolders(a, b RemoteFolder) int {
	ra, rb := slices.Index(specialOrder, a.Role), slices.Index(specialOrder, b.Role)
	if ra < 0 {
		ra = len(specialOrder)
	}
	if rb < 0 {
		rb = len(specialOrder)
	}
	if ra != rb {
		return ra - rb
	}
	return strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path))
}

func sortFolders(list []RemoteFolder) { slices.SortStableFunc(list, compareFolders) }

// enqueueDue queues a sync for every account that is due: every minute while a member
// who sees it was active in the last 30 minutes, else every 10 minutes, never before its
// backoff ends, and not while it waits for a reconnect or its workspace lacks Collab.
func (s *service) enqueueDue(ctx context.Context) error {
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT a.id, a.workspace_id FROM collab_accounts a
		WHERE NOT a.reconnect AND NOT COALESCE((a.config->>'demo')::boolean, false)
		AND (a.next_sync_at IS NULL OR a.next_sync_at <= now())
		AND (a.last_synced_at IS NULL OR a.last_synced_at <= now() - make_interval(secs => $1)
			OR (a.last_synced_at <= now() - make_interval(secs => $2) AND EXISTS (
				SELECT 1 FROM members m JOIN users u ON u.id = m.user_id
				WHERE m.workspace_id = a.workspace_id AND u.last_seen_at > now() - make_interval(secs => $3)
				AND `+accountVisibleSQL("a", "m.user_id")+`)))`,
		idleInterval.Seconds(), (activeInterval - 10*time.Second).Seconds(), activeWindow.Seconds())
	if err != nil {
		return err
	}
	type due struct{ id, wid uuid.UUID }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.id, &d.wid)
	})
	if err != nil {
		return err
	}
	allowed := map[uuid.UUID]bool{}
	params := []river.InsertManyParams{}
	for _, d := range list {
		ok, seen := allowed[d.wid]
		if !seen {
			if ok, err = s.collabActive(ctx, d.wid); err != nil {
				return err
			}
			allowed[d.wid] = ok
		}
		if ok {
			params = append(params, river.InsertManyParams{Args: jobs.CollabSyncArgs{AccountID: d.id}, InsertOpts: syncInsertOpts()})
		}
	}
	if len(params) == 0 || s.d.Jobs == nil {
		return nil
	}
	_, err = s.d.Jobs.InsertMany(ctx, params)
	return err
}
