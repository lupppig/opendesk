package core

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/emails"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/notifyprefs"
)

// Notification emails (contracts/api.md "Notification preferences and emails"): hd.Notify
// stores when a notification is due for email; a sweep every minute queues one job per
// user and workspace with something due, and that job emails everything still waiting
// there, at most once per notification. Pacing (notifyprefs.Pacing) decides when: a job
// that may not send yet records when to look again (notification_email_state.next_check_at)
// and the sweep leaves that member alone until then.

// notificationSweepEvery is how often due notification emails are looked for.
const notificationSweepEvery = time.Minute

// notificationSweepBatch bounds the users and workspaces queued per sweep.
const notificationSweepBatch = 500

type notificationSweepArgs struct{}

func (notificationSweepArgs) Kind() string { return "core.notification_email_sweep" }

type notificationSweepWorker struct {
	river.WorkerDefaults[notificationSweepArgs]
	s *svc
}

// uniqueWaiting dedupes a job while another for the same args waits or runs; a finished
// one does not block the next.
var uniqueWaiting = river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
	rivertype.JobStateRetryable, rivertype.JobStateScheduled,
}}

func (w *notificationSweepWorker) Work(ctx context.Context, _ *river.Job[notificationSweepArgs]) error {
	return w.s.sweepNotificationEmails(ctx)
}

func (s *svc) sweepNotificationEmails(ctx context.Context) error {
	if _, err := s.d.DB.Pool.Exec(ctx, `DELETE FROM notification_email_log WHERE sent_at < now() - interval '2 days'`); err != nil {
		return err
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT n.user_id, n.workspace_id FROM notifications n
		WHERE n.email_due_at <= now() AND n.emailed_at IS NULL AND n.read_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM notification_email_state st WHERE st.workspace_id = n.workspace_id
				AND st.user_id = n.user_id AND st.next_check_at > now())
		GROUP BY n.user_id, n.workspace_id ORDER BY min(n.email_due_at) LIMIT $1`, notificationSweepBatch)
	if err != nil {
		return err
	}
	due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (jobs.NotificationEmailArgs, error) {
		var a jobs.NotificationEmailArgs
		return a, r.Scan(&a.UserID, &a.WorkspaceID)
	})
	if err != nil {
		return err
	}
	for _, a := range due {
		if _, err := s.d.Jobs.Insert(ctx, a, &river.InsertOpts{MaxAttempts: 5, UniqueOpts: uniqueWaiting}); err != nil {
			return err
		}
	}
	return nil
}

type notificationEmailWorker struct {
	river.WorkerDefaults[jobs.NotificationEmailArgs]
	s *svc
}

func (w *notificationEmailWorker) Work(ctx context.Context, job *river.Job[jobs.NotificationEmailArgs]) error {
	return w.s.tx(ctx, func(tx pgx.Tx) error {
		return w.s.sendNotificationEmail(ctx, tx, job.Args.WorkspaceID, job.Args.UserID, time.Now())
	})
}

// waitingNote is a notification waiting for email, as loaded at send time.
type waitingNote struct {
	note emails.Note
	due  time.Time
	// gone: its ticket was deleted or the user may no longer see it.
	gone bool
}

// sendNotificationEmail emails the user what waits for them in the workspace when pacing
// allows. Everything is checked again here: read, preferences, pause, membership, a
// confirmed address and the ticket's visibility. Notifications that may not be emailed any
// more stop waiting; the rest are marked emailed in this transaction, which also queues
// the email, so each is emailed at most once. When pacing says later (a cooldown, a cap,
// the member active in the app, the instance's send rate), nothing is sent and the member
// is looked at again then; nothing is dropped.
func (s *svc) sendNotificationEmail(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT n.id, n.type, n.data, n.ticket_id, n.created_at, n.email_due_at, t.number, t.subject,
			n.ticket_id IS NOT NULL AND t.id IS NULL
		FROM notifications n LEFT JOIN tickets t ON t.id = n.ticket_id AND t.workspace_id = n.workspace_id AND t.deleted_at IS NULL
			AND `+domain.VisibleToUserSQL("t", 2)+`
		WHERE n.workspace_id = $1 AND n.user_id = $2 AND n.email_due_at IS NOT NULL AND n.emailed_at IS NULL AND n.read_at IS NULL
		ORDER BY n.created_at DESC, n.id DESC
		FOR UPDATE OF n SKIP LOCKED`, wid, uid)
	if err != nil {
		return err
	}
	waiting, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (waitingNote, error) {
		var x waitingNote
		var raw []byte
		var number *int64
		var subject *string
		if err := r.Scan(&x.note.ID, &x.note.Type, &raw, &x.note.TicketID, &x.note.CreatedAt, &x.due, &number, &subject, &x.gone); err != nil {
			return x, err
		}
		_ = json.Unmarshal(raw, &x.note.Data)
		if x.note.Data == nil {
			x.note.Data = map[string]any{}
		}
		if number != nil && subject != nil {
			x.note.TicketNumber, x.note.TicketSubject = *number, *subject
		}
		return x, nil
	})
	if err != nil || len(waiting) == 0 {
		return err
	}
	if !slices.ContainsFunc(waiting, func(x waitingNote) bool { return !x.due.After(now) }) {
		return nil // nothing due yet; a later sweep comes back
	}

	var email, workspace, clock string
	var verified bool
	err = tx.QueryRow(ctx, `SELECT u.email::text, u.email_verified_at IS NOT NULL, w.name, u.time_format
		FROM members m JOIN users u ON u.id = m.user_id JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.workspace_id = $1 AND m.user_id = $2`, wid, uid).Scan(&email, &verified, &workspace, &clock)
	member := err == nil
	if err != nil && !db.IsNotFound(err) {
		return err
	}
	set, err := notifyprefs.LoadSettings(ctx, tx, uid)
	if err != nil {
		return err
	}
	channels, err := notifyprefs.All(ctx, tx, wid, uid)
	if err != nil {
		return err
	}
	stop := !member || !verified || set.Cadence == notifyprefs.CadenceOff || set.Paused(now)

	var keep []emails.Note
	var keepIDs, dropIDs []uuid.UUID
	for _, x := range waiting {
		ch, known := channels[x.note.Type]
		if !known {
			ch = notifyprefs.Defaults(x.note.Type)
		}
		if stop || x.gone || !ch.Email {
			dropIDs = append(dropIDs, x.note.ID)
			continue
		}
		keep = append(keep, x.note)
		keepIDs = append(keepIDs, x.note.ID)
	}
	if len(dropIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE notifications SET email_due_at = NULL WHERE id = ANY($1)`, dropIDs); err != nil {
			return err
		}
	}
	if len(keep) == 0 {
		return nil
	}
	var pending []notifyprefs.Waiting
	for _, x := range waiting {
		if slices.Contains(keepIDs, x.note.ID) {
			pending = append(pending, notifyprefs.Waiting{Created: x.note.CreatedAt, Due: x.due, Urgent: notifyprefs.Urgent(x.note.Type)})
		}
	}
	pace := notifyprefs.Current()
	h, err := loadEmailHistory(ctx, tx, wid, uid, now)
	if err != nil {
		return err
	}
	d := pace.Decide(now, set.Cadence, pending, h)
	if d.At.After(now) {
		return holdEmail(ctx, tx, wid, uid, d.At)
	}
	// The instance's send rate: past it, wait a minute; the email is not lost.
	if pace.InstancePerMinute > 0 {
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_email_log WHERE sent_at > $1`, now.Add(-time.Minute)).Scan(&recent); err != nil {
			return err
		}
		if recent >= pace.InstancePerMinute {
			return holdEmail(ctx, tx, wid, uid, now.Add(time.Minute))
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET emailed_at = $2 WHERE id = ANY($1) AND emailed_at IS NULL`, keepIDs, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notification_email_log (user_id, workspace_id, sent_at, notifications, urgent) VALUES ($1, $2, $3, $4, $5)`,
		uid, wid, now, len(keepIDs), d.Urgent); err != nil {
		return err
	}
	var lastUrgent *time.Time
	if d.Urgent {
		lastUrgent = &now
	}
	if _, err := tx.Exec(ctx, `INSERT INTO notification_email_state (workspace_id, user_id, step, last_emailed_at, next_allowed_at, last_urgent_at, next_check_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULL, now())
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET step = EXCLUDED.step, last_emailed_at = EXCLUDED.last_emailed_at,
			next_allowed_at = EXCLUDED.next_allowed_at, last_urgent_at = COALESCE(EXCLUDED.last_urgent_at, notification_email_state.last_urgent_at),
			next_check_at = NULL, updated_at = now()`,
		wid, uid, d.Step, now, now.Add(pace.Cooldown(d.Step)), lastUrgent); err != nil {
		return err
	}
	b := emails.Batch{WorkspaceID: wid, Workspace: workspace, Cadence: set.Cadence, Location: set.Location(), Clock: clock, Now: now, Urgent: notifyprefs.Urgent}
	if len(keep) > emails.MaxListed {
		b.More = len(keep) - emails.MaxListed
		keep = keep[:emails.MaxListed]
	}
	b.Notes = keep
	if b.UnsubscribeToken, err = s.unsubscribeToken(uid, wid); err != nil {
		return err
	}
	return s.enqueueMail(ctx, tx, email, emails.Notifications(s.site(), b))
}

// loadEmailHistory is what pacing needs about the member's notification emails: the
// conversation in this workspace (locked for this send), the last day's emails across
// workspaces and when the member was last active here.
func loadEmailHistory(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID, now time.Time) (notifyprefs.History, error) {
	var h notifyprefs.History
	err := tx.QueryRow(ctx, `SELECT step, last_emailed_at, next_allowed_at, last_urgent_at FROM notification_email_state
		WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`, wid, uid).Scan(&h.Step, &h.LastEmailed, &h.NextAllowed, &h.LastUrgent)
	if err != nil && !db.IsNotFound(err) {
		return h, err
	}
	rows, err := tx.Query(ctx, `SELECT sent_at, urgent FROM notification_email_log WHERE user_id = $1 AND sent_at > $2 ORDER BY sent_at`,
		uid, now.Add(-24*time.Hour))
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		var urgent bool
		if err := rows.Scan(&at, &urgent); err != nil {
			return h, err
		}
		h.Sent = append(h.Sent, at)
		if urgent {
			h.UrgentSent++
		}
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	err = tx.QueryRow(ctx, `SELECT active_at FROM members WHERE workspace_id = $1 AND user_id = $2`, wid, uid).Scan(&h.ActiveAt)
	if err != nil && !db.IsNotFound(err) {
		return h, err
	}
	return h, nil
}

// holdEmail has the sweep come back to the member at at.
func holdEmail(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO notification_email_state (workspace_id, user_id, next_check_at, updated_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET next_check_at = EXCLUDED.next_check_at, updated_at = now()`, wid, uid, at)
	return err
}

func (s *svc) unsubscribeToken(uid, wid uuid.UUID) (string, error) {
	tok, _, err := s.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeNotificationUnsubscribe, Workspace: wid.String()}, uid.String(), auth.UnsubscribeTTL)
	return tok, err
}

// sendTestNotificationEmail queues a sample notification email to the user, whatever
// their preferences. Its ticket and names are made up.
func (s *svc) sendTestNotificationEmail(ctx context.Context, tx pgx.Tx, wid, uid uuid.UUID) error {
	var email, workspace, name, clock string
	var verified bool
	if err := tx.QueryRow(ctx, `SELECT u.email::text, u.email_verified_at IS NOT NULL, u.name, w.name, u.time_format
		FROM members m JOIN users u ON u.id = m.user_id JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.workspace_id = $1 AND m.user_id = $2`, wid, uid).Scan(&email, &verified, &name, &workspace, &clock); err != nil {
		return err
	}
	if !verified {
		return errEmailUnverified()
	}
	set, err := notifyprefs.LoadSettings(ctx, tx, uid)
	if err != nil {
		return err
	}
	tok, err := s.unsubscribeToken(uid, wid)
	if err != nil {
		return err
	}
	now := time.Now()
	b := emails.Batch{WorkspaceID: wid, Workspace: workspace, Cadence: notifyprefs.CadenceInstant, Location: set.Location(), Clock: clock, Now: now,
		Notes: emails.SampleNotes(now), UnsubscribeToken: tok, Test: true, Urgent: notifyprefs.Urgent}
	return s.enqueueMail(ctx, tx, email, emails.Notifications(s.site(), b))
}

func errEmailUnverified() *httpx.Error {
	return httpx.E(422, "email_unverified", "Confirm your email address first: notification emails go only to a confirmed address.")
}
