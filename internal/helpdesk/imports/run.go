package imports

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// runBudget is how long one job works before it snoozes to let the queue breathe; the
// next one picks up at the checkpoint.
const runBudget = 4 * time.Minute

// maxFailures in a row of the source itself fail the import.
const maxFailures = 5

// NotifyImportFinished tells who started an import that it completed, failed, or needs
// new credentials (contracts/imports.md).
const NotifyImportFinished = "import_finished"

type runWorker struct {
	river.WorkerDefaults[jobs.ImportRunArgs]
	s *Service
}

func (w *runWorker) Timeout(*river.Job[jobs.ImportRunArgs]) time.Duration {
	return runBudget + 5*time.Minute
}

func (w *runWorker) Work(ctx context.Context, job *river.Job[jobs.ImportRunArgs]) error {
	return w.s.run(ctx, job.Args)
}

// enqueueRun starts a job for an import's current generation, in tx.
func (s *Service) enqueueRun(ctx context.Context, tx pgx.Tx, x row) error {
	_, err := s.d.Jobs.InsertTx(ctx, tx, jobs.ImportRunArgs{ImportID: x.ID, Generation: x.Generation}, &river.InsertOpts{MaxAttempts: 10})
	return err
}

func loadByID(ctx context.Context, q db.Querier, id uuid.UUID) (row, error) {
	return scanRow(q.QueryRow(ctx, `SELECT `+rowCols+rowFrom+` WHERE i.id = $1`, id))
}

// open builds the source an import reads.
func (s *Service) open(ctx context.Context, x *row) (source.Source, error) {
	if x.Source == CSV {
		if x.StorageKey == nil || x.Mapping.CSV == nil {
			return nil, errors.New("The CSV file is no longer stored. Upload it again in a new import.")
		}
		f, err := s.readCSV(ctx, *x.StorageKey)
		if err != nil {
			return nil, err
		}
		return newCSVSource(f, *x.Mapping.CSV, x.Account.Name, x.AccountKey), nil
	}
	f, ok := s.Factories[x.Source]
	if !ok {
		return nil, fmt.Errorf("unknown source %q", x.Source)
	}
	if len(x.Credentials) == 0 {
		return nil, source.ErrAuth
	}
	var c source.Credentials
	if err := s.d.Box.OpenJSON(x.Credentials, &c); err != nil {
		return nil, fmt.Errorf("open credentials: %w", err)
	}
	return f(c, s.Options)
}

// uploadReadError is a failure to read a stored upload: its text names storage internals,
// so it is logged, never shown.
type uploadReadError struct{ err error }

func (e uploadReadError) Error() string { return "read upload: " + e.err.Error() }
func (e uploadReadError) Unwrap() error { return e.err }

// readCSV reads and parses an upload. Only a parse error's text is for people.
func (s *Service) readCSV(ctx context.Context, key string) (*csvFile, error) {
	rc, err := s.d.Storage.Get(ctx, key)
	if err != nil {
		return nil, uploadReadError{err}
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxCSVBytes+1))
	if err != nil {
		return nil, uploadReadError{err}
	}
	return parseCSV(raw)
}

// run works on an import until it is done, stopped, waiting or out of budget.
func (s *Service) run(ctx context.Context, args jobs.ImportRunArgs) error {
	x, err := loadByID(ctx, s.d.DB.Pool, args.ImportID)
	if db.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if x.Status != StatusRunning || x.Generation != args.Generation {
		return nil
	}
	if x.WaitingUntil != nil && time.Until(*x.WaitingUntil) > time.Second {
		return river.JobSnooze(time.Until(*x.WaitingUntil))
	}
	src, err := s.open(ctx, &x)
	if err != nil {
		return s.trouble(ctx, &x, err)
	}
	scan, err := src.Check(ctx)
	if err != nil {
		return s.trouble(ctx, &x, err)
	}
	w, err := s.newWriter(ctx, &x, src, scan)
	if err != nil {
		return err
	}
	if x.Source != CSV && !scan.HasArticles {
		x.Options.Articles = false
	}
	deadline := time.Now().Add(runBudget)
	// Copying a ticket's files may go on a minute past the budget: with one download in
	// flight it still ends before the job's timeout.
	w.copyUntil = deadline.Add(time.Minute)
	for time.Now().Before(deadline) {
		done, err := s.step(ctx, w)
		if err != nil {
			return s.trouble(ctx, &x, err)
		}
		if done {
			return s.finish(ctx, &x)
		}
	}
	return river.JobSnooze(0)
}

// step does one page of the current phase. done says there is nothing left.
func (s *Service) step(ctx context.Context, w *writer) (bool, error) {
	x := w.x
	switch x.Phase {
	case PhaseArticles:
		if !x.Options.Articles {
			return false, s.checkpoint(ctx, w, PhaseArticles, "")
		}
		page, err := w.src.Articles(ctx, x.Cursor[PhaseArticles])
		if errors.Is(err, source.ErrUnsupported) || errors.Is(err, source.ErrAuth) {
			// Reading tickets works (Check passed); the help center is out of reach.
			if errors.Is(err, source.ErrAuth) {
				w.problem("Help center", "These credentials cannot read the help center, so articles were left out.")
			}
			return false, s.checkpoint(ctx, w, PhaseArticles, "")
		}
		if err != nil {
			return false, err
		}
		for _, a := range page.Items {
			if err := w.writeArticle(ctx, a); err != nil {
				if stopping(err) {
					return false, err
				}
				s.d.Log.Warn("import article failed", "import_id", x.ID, "article", a.ID, "error", err)
				w.problem("Article "+hd.Truncate(a.Title, 80), plain(err))
				x.Counts.Failed++
			}
		}
		return false, s.checkpoint(ctx, w, PhaseArticles, page.Next)
	case PhaseTickets:
		if !x.Options.Tickets {
			return false, s.checkpoint(ctx, w, PhaseTickets, "")
		}
		page, err := w.src.Tickets(ctx, x.Cursor[PhaseTickets], sinceOf(x.Options), x.UpdatedSince)
		if err != nil {
			return false, err
		}
		for i := range page.Items {
			t := &page.Items[i]
			result, err := w.writeTicket(ctx, t)
			if err != nil {
				if stopping(err) {
					return false, err
				}
				s.d.Log.Warn("import ticket failed", "import_id", x.ID, "ticket", t.ID, "error", err)
				w.problem("Ticket "+t.Ref, plain(err))
				x.Counts.Failed++
				continue
			}
			if result == outcomeSkipped {
				x.Counts.Skipped++
			}
		}
		return false, s.checkpoint(ctx, w, PhaseTickets, page.Next)
	case PhaseThreading:
		return false, s.threadStep(ctx, w)
	}
	return true, nil
}

func sinceOf(o ImportOptions) *time.Time {
	if o.Since == nil {
		return nil
	}
	t, err := time.Parse(time.DateOnly, *o.Since)
	if err != nil {
		return nil
	}
	return &t
}

// checkpoint saves where a phase resumes; an empty next moves on to the next phase.
func (s *Service) checkpoint(ctx context.Context, w *writer, phase, next string) error {
	x := w.x
	cursor := map[string]string{}
	for k, v := range x.Cursor {
		cursor[k] = v
	}
	nextPhase := x.Phase
	if next == "" {
		delete(cursor, phase)
		switch phase {
		case PhaseArticles:
			nextPhase = PhaseTickets
		case PhaseTickets:
			nextPhase = PhaseThreading
		case PhaseThreading:
			nextPhase = PhaseDone
		}
	} else {
		cursor[phase] = next
	}
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := w.begin(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE imports SET cursor = $2, phase = $3, counts = $4, problems = $5, failures = 0,
			waiting_until = NULL, last_error = '', updated_at = now() WHERE id = $1`, x.ID, cursor, nextPhase, x.Counts, x.Problems); err != nil {
			return err
		}
		x.Cursor, x.Phase, x.Failures, x.WaitingUntil, x.LastError = cursor, nextPhase, 0, nil, ""
		return w.progress(ctx, tx, true)
	})
	return err
}

// finish completes an import: its CSV file goes, and who started it is told.
func (s *Service) finish(ctx context.Context, x *row) error {
	var file *string
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		cur, err := lockRow(ctx, tx, x.WorkspaceID, x.ID)
		if err != nil {
			return err
		}
		if cur.Status != StatusRunning || cur.Generation != x.Generation {
			return errStopped
		}
		if cur.Source == CSV {
			file = cur.StorageKey
		}
		if _, err := tx.Exec(ctx, `UPDATE imports SET status = 'completed', phase = 'done', finished_at = now(), last_error = '',
			waiting_until = NULL, failures = 0, storage_key = CASE WHEN source = 'csv' THEN NULL ELSE storage_key END, updated_at = now()
			WHERE id = $1`, x.ID); err != nil {
			return err
		}
		done, err := loadRow(ctx, tx, x.WorkspaceID, x.ID)
		if err != nil {
			return err
		}
		if err := s.notify(ctx, tx, done, "completed"); err != nil {
			return err
		}
		return publish(ctx, tx, "import.updated", done)
	})
	if errors.Is(err, errStopped) {
		return nil
	}
	if err == nil && file != nil {
		_ = s.d.Storage.Delete(context.WithoutCancel(ctx), *file)
	}
	return err
}

// trouble decides what an error means for the import: wait out a rate limit, pause for
// new credentials, retry the source a few times, or fail.
func (s *Service) trouble(ctx context.Context, x *row, cause error) error {
	var rl *source.RateLimited
	switch {
	case errors.Is(cause, errStopped):
		return nil
	case errors.Is(cause, errOutOfTime):
		return river.JobSnooze(0)
	case ctx.Err() != nil:
		// Shutting down: the next worker picks up at the checkpoint.
		return river.JobSnooze(5 * time.Second)
	case errors.As(cause, &rl):
		until := time.Now().Add(rl.RetryAfter).UTC()
		err := s.update(ctx, x, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE imports SET waiting_until = $2, counts = $3, problems = $4, updated_at = now() WHERE id = $1`,
				x.ID, until, x.Counts, x.Problems)
			return err
		}, "")
		if err != nil {
			return err
		}
		return river.JobSnooze(rl.RetryAfter)
	case errors.Is(cause, source.ErrAuth), errors.Is(cause, source.ErrInvalid):
		msg := sourceName(x.Source) + " refused the credentials. Enter new ones to go on where it stopped."
		return s.update(ctx, x, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE imports SET status = 'paused', needs_credentials = true, last_error = $2, waiting_until = NULL,
				counts = $3, problems = $4, updated_at = now() WHERE id = $1`, x.ID, msg, x.Counts, x.Problems)
			return err
		}, "needs_credentials")
	case errors.Is(cause, source.ErrNotFound):
		return s.fail(ctx, x, "The "+sourceName(x.Source)+" account was not found. It may have been closed or renamed.")
	}
	var un *source.Unreachable
	var re *source.RemoteError
	transient := errors.As(cause, &un) || (errors.As(cause, &re) && (re.Status >= 500 || re.Status == 408))
	if !transient || x.Failures+1 >= maxFailures {
		s.d.Log.Warn("import failed", "import_id", x.ID, "source", x.Source, "error", cause)
		return s.fail(ctx, x, failMessage(x.Source, cause))
	}
	failures := x.Failures + 1
	msg := sourceName(x.Source) + " is not answering right now. Trying again shortly."
	if err := s.update(ctx, x, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE imports SET failures = $2, last_error = $3, counts = $4, problems = $5, updated_at = now() WHERE id = $1`,
			x.ID, failures, msg, x.Counts, x.Problems)
		return err
	}, ""); err != nil {
		return err
	}
	return river.JobSnooze(time.Duration(30<<(failures-1)) * time.Second)
}

func failMessage(src string, cause error) string {
	var re *source.RemoteError
	var un *source.Unreachable
	switch {
	case errors.As(cause, &un):
		return sourceName(src) + " stopped answering. Resume the import to try again."
	case errors.As(cause, &re):
		return fmt.Sprintf("%s answered with an error (%d). Resume the import to try again.", sourceName(src), re.Status)
	}
	var he interface{ GetStatus() int }
	if errors.As(cause, &he) {
		return plain(cause)
	}
	if msg := cause.Error(); len(msg) > 0 && msg[0] >= 'A' && msg[0] <= 'Z' && len(msg) < 300 {
		return msg // a sentence of ours (a CSV that cannot be read)
	}
	return "Something went wrong on our side. Resume the import to try again."
}

// fail stops an import for good (until resumed) and tells who started it.
func (s *Service) fail(ctx context.Context, x *row, msg string) error {
	return s.update(ctx, x, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE imports SET status = 'failed', last_error = $2, finished_at = now(), waiting_until = NULL,
			counts = $3, problems = $4, updated_at = now() WHERE id = $1`, x.ID, msg, x.Counts, x.Problems)
		return err
	}, "failed")
}

// update changes a running import of this generation, announces it and, with a status,
// notifies who started it.
func (s *Service) update(ctx context.Context, x *row, change func(pgx.Tx) error, notify string) error {
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		cur, err := lockRow(ctx, tx, x.WorkspaceID, x.ID)
		if err != nil {
			return err
		}
		if cur.Status != StatusRunning || cur.Generation != x.Generation {
			return errStopped
		}
		if err := change(tx); err != nil {
			return err
		}
		next, err := loadRow(ctx, tx, x.WorkspaceID, x.ID)
		if err != nil {
			return err
		}
		if notify != "" {
			if err := s.notify(ctx, tx, next, notify); err != nil {
				return err
			}
		}
		return publish(ctx, tx, "import.updated", next)
	})
	if errors.Is(err, errStopped) {
		return nil
	}
	return err
}

// notify tells who started an import how it ended (NotifyImportFinished).
func (s *Service) notify(ctx context.Context, tx pgx.Tx, x row, status string) error {
	if x.StartedByID == nil {
		return nil
	}
	return hd.Notify(ctx, tx, x.WorkspaceID, *x.StartedByID, NotifyImportFinished, nil, map[string]any{
		"import_id": x.ID.String(), "source": x.Source, "source_name": sourceName(x.Source), "account": x.Account.Name, "status": status,
		"tickets": x.Counts.Tickets, "updated": x.Counts.Updated, "articles": x.Counts.Articles, "failed": x.Counts.Failed, "error": x.LastError})
}
