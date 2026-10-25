package imports

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// threadBatch is how many emails one step of the threading phase reads.
const threadBatch = 25

// threadStarted marks, in the cursor, that this run's threading phase counted what it has
// to read.
const threadStarted = "started"

// unthreadedFrom picks the emails an import brought whose threading headers were never
// read: replies on email tickets with no Message-ID, newest first.
const unthreadedFrom = ` FROM import_records r
	JOIN messages m ON m.workspace_id = r.workspace_id AND m.id = r.local_id
	JOIN tickets t ON t.workspace_id = m.workspace_id AND t.id = m.ticket_id
	WHERE r.workspace_id = $1 AND r.source = $2 AND r.account_key = $3 AND r.kind = 'message' AND r.threaded_at IS NULL
		AND m.kind = 'reply' AND m.email_message_id IS NULL AND t.channel = 'email'`

// fillThreading gives an email the headers its source read. A Message-ID another message
// already holds (the live email, or a row repeating it) stays with that one.
const fillThreading = `UPDATE messages m SET
	email_message_id = CASE WHEN EXISTS (SELECT 1 FROM messages o WHERE o.workspace_id = m.workspace_id AND o.email_message_id = $3)
		THEN NULL ELSE $3 END,
	in_reply_to = $4, "references" = $5
	WHERE m.workspace_id = $1 AND m.id = $2 AND m.email_message_id IS NULL`

// threadEmail is an imported email waiting for its threading headers.
type threadEmail struct {
	external string
	local    uuid.UUID
	headers  source.Threading
}

// threadStep reads the threading headers of the next emails, for a source whose listings
// leave them out (source.Threader). It runs after the tickets are in, so they can be
// worked while it goes, and newest first, since a reply most likely answers a recent
// email. Each email is read once: one the source cannot read is left without headers.
func (s *Service) threadStep(ctx context.Context, w *writer) error {
	x := w.x
	th, ok := w.src.(source.Threader)
	if !ok || !x.Options.Tickets {
		return s.checkpoint(ctx, w, PhaseThreading, "")
	}
	if x.Cursor[PhaseThreading] == "" {
		var left int
		if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*)`+unthreadedFrom, x.WorkspaceID, x.Source, x.AccountKey).Scan(&left); err != nil {
			return err
		}
		if left == 0 {
			return s.checkpoint(ctx, w, PhaseThreading, "")
		}
		x.Counts.ThreadTotal = x.Counts.Threaded + left
		return s.checkpoint(ctx, w, PhaseThreading, threadStarted)
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT r.external_id, r.local_id`+unthreadedFrom+` ORDER BY m.created_at DESC, m.id DESC LIMIT $4`,
		x.WorkspaceID, x.Source, x.AccountKey, threadBatch)
	if err != nil {
		return err
	}
	batch, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (threadEmail, error) {
		var e threadEmail
		return e, r.Scan(&e.external, &e.local)
	})
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		return s.checkpoint(ctx, w, PhaseThreading, "")
	}
	read := 0
	var stop error
	for i := range batch {
		t, err := th.Threading(ctx, batch[i].external)
		if err != nil && haltsThreading(ctx, err) {
			stop = err
			break
		}
		if err != nil {
			s.d.Log.Debug("import threading headers unread", "import_id", x.ID, "message", batch[i].external, "error", err)
		}
		batch[i].headers = t
		read++
	}
	if read > 0 {
		if err := s.saveThreading(ctx, w, batch[:read]); err != nil {
			return err
		}
	}
	return stop
}

// saveThreading writes what a step read and marks those emails read, with the counts.
func (s *Service) saveThreading(ctx context.Context, w *writer, batch []threadEmail) error {
	x := w.x
	before := x.Counts.Threaded
	x.Counts.Threaded += len(batch)
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := w.begin(ctx, tx); err != nil {
			return err
		}
		ext := make([]string, 0, len(batch))
		for _, e := range batch {
			ext = append(ext, e.external)
			h := e.headers
			if h.MessageID == "" {
				// Without a Message-ID nothing threads onto this email; its reply headers
				// alone change nothing.
				continue
			}
			refs := h.References
			if refs == nil {
				refs = []string{}
			}
			if _, err := tx.Exec(ctx, fillThreading, x.WorkspaceID, e.local, h.MessageID, h.InReplyTo, refs); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE import_records SET threaded_at = now()
			WHERE workspace_id = $1 AND source = $2 AND account_key = $3 AND kind = 'message' AND external_id = ANY($4)`,
			x.WorkspaceID, x.Source, x.AccountKey, ext); err != nil {
			return err
		}
		return w.progress(ctx, tx, false)
	})
	if err != nil {
		x.Counts.Threaded = before
	}
	return err
}

// haltsThreading says an error stops the phase, to wait or retry as the import does,
// rather than leave one email without headers.
func haltsThreading(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var rl *source.RateLimited
	var un *source.Unreachable
	var re *source.RemoteError
	switch {
	case errors.As(err, &rl), errors.As(err, &un), errors.Is(err, source.ErrAuth), errors.Is(err, source.ErrInvalid):
		return true
	case errors.As(err, &re):
		return re.Status >= 500 || re.Status == 408
	}
	return false
}
