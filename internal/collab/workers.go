package collab

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/jobs"
)

// syncDueArgs is the minute tick that queues a sync for every due account.
type syncDueArgs struct{}

func (syncDueArgs) Kind() string { return "collab.sync_due" }

type syncDueWorker struct {
	river.WorkerDefaults[syncDueArgs]
	s *service
}

func (w *syncDueWorker) Work(ctx context.Context, _ *river.Job[syncDueArgs]) error {
	return w.s.enqueueDue(ctx)
}

// syncAccountWorker syncs one account (collab.sync_account).
type syncAccountWorker struct {
	river.WorkerDefaults[jobs.CollabSyncArgs]
	s *service
}

// Timeout leaves a first sync (up to 500 messages, one request each) time to finish.
func (w *syncAccountWorker) Timeout(*river.Job[jobs.CollabSyncArgs]) time.Duration {
	return 15 * time.Minute
}

func (w *syncAccountWorker) Work(ctx context.Context, job *river.Job[jobs.CollabSyncArgs]) error {
	return w.s.syncAccount(ctx, job.Args.AccountID)
}

// applyWorker makes an action at the provider (collab.apply).
type applyWorker struct {
	river.WorkerDefaults[jobs.CollabApplyArgs]
	s *service
}

func (w *applyWorker) Timeout(*river.Job[jobs.CollabApplyArgs]) time.Duration {
	return sessionTimeout + time.Minute
}

func (w *applyWorker) Work(ctx context.Context, job *river.Job[jobs.CollabApplyArgs]) error {
	return w.s.applyRemote(ctx, job)
}

// cleanupWorker removes the accounts of people who left and stored raw messages of
// deleted mail (collab.cleanup).
type cleanupWorker struct {
	river.WorkerDefaults[jobs.CollabCleanupArgs]
	s *service
}

func (w *cleanupWorker) Timeout(*river.Job[jobs.CollabCleanupArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[jobs.CollabCleanupArgs]) error {
	return w.s.cleanup(ctx)
}
