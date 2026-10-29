package supportchannels

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

const deliverySummary = `{"summary":"Printer needs help","tried":[],"sentiment":"neutral","urgency":"normal","urgency_reason":""}`

type deliveryModel struct {
	*fakeModel
	beforeReturn func()
	input        string
}

func (m *deliveryModel) Complete(ctx context.Context, req ai.Request) (ai.Response, error) {
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			m.input += b.Text
		}
	}
	if m.beforeReturn != nil {
		m.beforeReturn()
	}
	return m.fakeModel.Complete(ctx, req)
}
func setDeliveryModel(w *workflow, before func()) *deliveryModel {
	m := &deliveryModel{fakeModel: &fakeModel{complete: deliverySummary}, beforeReturn: before}
	w.s.ai = ai.For(w.e.D).WithProvider(m, nil, "fake-model", "fake-model")
	return m
}
func summaryJob(w *workflow, destination uuid.UUID) *river.Job[jobs.SupportChannelSummaryArgs] {
	return &river.Job[jobs.SupportChannelSummaryArgs]{Args: jobs.SupportChannelSummaryArgs{DestinationID: destination, TicketID: w.ticket.ID}}
}
func addDueSLA(w *workflow) {
	w.e.T.Cleanup(func() { w.e.Exec(`UPDATE tickets SET sla_policy_id=NULL WHERE id=$1`, w.ticket.ID) })
	policy := ids.New()
	w.e.Exec(`INSERT INTO sla_policies(id,workspace_id,name,first_response_minutes,resolution_minutes) VALUES($1,$2,'Default',60,480)`, policy, w.ws.ID)
	w.e.Exec(`UPDATE tickets SET sla_policy_id=$2,first_response_due_at=now()+interval '10 minutes' WHERE id=$1`, w.ticket.ID, policy)
}

// beforeMessageRead runs between the worker's eligibility and message SELECTs.
// It changes real rows on a different connection, while the worker keeps reading.
type beforeMessageRead struct {
	once sync.Once
	run  func()
}

func (p *beforeMessageRead) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(d.SQL, domain.MessageSelect) {
		p.once.Do(p.run)
	}
	return ctx
}
func (*beforeMessageRead) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestSummarySnapshotExcludesMessagesCommittedAfterPrivateTransfer(t *testing.T) {
	w := newWorkflow(t)
	destination := threadDestination(t, w)
	team := ids.New()
	w.e.Exec(`INSERT INTO teams(id,workspace_id,name,private) VALUES($1,$2,'Restricted',true)`, team, w.ws.ID)
	original := w.s.d.DB
	cfg := original.Pool.Config()
	cfg.ConnConfig.Tracer = &beforeMessageRead{run: func() {
		// Complete both writes before allowing the worker's message query to run.
		err := original.Tx(w.e.Ctx, func(tx pgx.Tx) error {
			if _, err := tickets.ApplyPatch(w.e.Ctx, tx, w.e.D, &auth.Principal{Kind: auth.KindUser, UserID: w.ws.Admin.ID, WorkspaceID: w.ws.ID, Role: auth.RoleAdmin}, w.ticket.ID, tickets.Patch{Team: tickets.Opt[uuid.UUID]{Set: true, Value: team}}); err != nil {
				return err
			}
			_, err := domain.AddMessage(w.e.Ctx, tx, w.e.D.Jobs, domain.NewMessage{WorkspaceID: w.ws.ID, TicketID: w.ticket.ID, Kind: "reply", Direction: "inbound", AuthorType: "system", BodyText: "confidential-after-private-transfer"})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}}
	pool, err := pgxpool.NewWithConfig(w.e.Ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	depsCopy := *w.s.d
	dbCopy := *original
	dbCopy.Pool = pool
	depsCopy.DB = &dbCopy
	w.s.d = &depsCopy
	model := setDeliveryModel(w, nil)
	if err := (&summaryWorker{s: w.s}).Work(w.e.Ctx, summaryJob(w, destination)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(model.input, "confidential-after-private-transfer") {
		t.Fatal("model received a message committed after the ticket became private")
	}
	if model.input == "" {
		t.Fatal("model was not called with the eligible snapshot")
	}
	if len(w.fake.interactive) != 0 {
		t.Fatalf("posted %d summaries after private transfer", len(w.fake.interactive))
	}
}

type eligibilityChange struct {
	name, sql string
	id        func(*workflow, uuid.UUID, uuid.UUID) uuid.UUID
}

func eligibilityChanges() []eligibilityChange {
	ticketID := func(w *workflow, _, _ uuid.UUID) uuid.UUID { return w.ticket.ID }
	destID := func(_ *workflow, d, _ uuid.UUID) uuid.UUID { return d }
	return []eligibilityChange{
		{"private transfer", `UPDATE tickets SET team_id=$2 WHERE id=$1`, ticketID},
		{"team privacy", `UPDATE teams SET private=true WHERE id=$1`, func(_ *workflow, _, team uuid.UUID) uuid.UUID { return team }},
		{"feature disabled", `UPDATE support_channel_destinations SET ollie_summary=false,ollie_warnings=false WHERE id=$1`, destID},
		{"destination disabled", `UPDATE support_channel_destinations SET enabled=false WHERE id=$1`, destID},
		{"destination deleted", `DELETE FROM support_channel_destinations WHERE id=$1`, destID},
		{"connection deleted", `DELETE FROM support_channel_connections WHERE id=$1`, func(w *workflow, _, _ uuid.UUID) uuid.UUID { return w.c.ID }},
		{"workspace suspended", `UPDATE workspaces SET suspended_at=now() WHERE id=$1`, func(w *workflow, _, _ uuid.UUID) uuid.UUID { return w.ws.ID }},
	}
}
func prepareChange(w *workflow) (uuid.UUID, uuid.UUID, uuid.UUID) {
	var dest uuid.UUID
	w.e.Scalar(`SELECT destination_id FROM support_channel_threads WHERE ticket_id=$1`, &dest, w.ticket.ID)
	public, private := ids.New(), ids.New()
	w.e.Exec(`INSERT INTO teams(id,workspace_id,name,private) VALUES($1,$3,'Public',false),($2,$3,'Private',true)`, public, private, w.ws.ID)
	w.e.Exec(`UPDATE tickets SET team_id=$2 WHERE id=$1`, w.ticket.ID, public)
	return dest, public, private
}
func changeArgs(change eligibilityChange, w *workflow, dest, public, private uuid.UUID) []any {
	args := []any{change.id(w, dest, public)}
	if change.name == "private transfer" {
		args = append(args, private)
	}
	return args
}
func TestSummaryRevalidatesEligibilityAfterModel(t *testing.T) {
	for _, change := range eligibilityChanges() {
		t.Run(change.name, func(t *testing.T) {
			w := newWorkflow(t)
			dest, public, private := prepareChange(w)
			setDeliveryModel(w, func() { w.e.Exec(change.sql, changeArgs(change, w, dest, public, private)...) })
			if err := (&summaryWorker{s: w.s}).Work(w.e.Ctx, summaryJob(w, dest)); err != nil {
				t.Fatal(err)
			}
			if len(w.fake.interactive) != 0 {
				t.Fatalf("posted summary after %s", change.name)
			}
		})
	}
}

type heldPostAdapter struct {
	*fakeAdapter
	entered, release chan struct{}
	once             sync.Once
}

func (a *heldPostAdapter) hold(ctx context.Context) error {
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *heldPostAdapter) Post(ctx context.Context, c *http.Client, creds map[string]string, thread, text string) error {
	if err := a.hold(ctx); err != nil {
		return err
	}
	return a.fakeAdapter.Post(ctx, c, creds, thread, text)
}
func (a *heldPostAdapter) PostInteractive(ctx context.Context, c *http.Client, creds map[string]string, thread string, m channel.Message) (channel.MessageRef, error) {
	if err := a.hold(ctx); err != nil {
		return channel.MessageRef{}, err
	}
	return a.fakeAdapter.PostInteractive(ctx, c, creds, thread, m)
}
func waitResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("operation did not finish")
		return nil
	}
}
func waitBlocked(t *testing.T, pool *pgxpool.Pool, pid int, finished <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-finished:
			t.Fatalf("eligibility writer finished before provider returned: %v", err)
		case <-ctx.Done():
			t.Fatal("writer never blocked on delivery")
		case <-ticker.C:
		}
	}
}
func TestOllieDeliverySerializesEligibilityWriters(t *testing.T) {
	for _, kind := range []string{"summary", "sla", "sentiment"} {
		for _, change := range eligibilityChanges() {
			t.Run(kind+"/"+change.name, func(t *testing.T) {
				w := newWorkflow(t)
				dest, public, private := prepareChange(w)
				setDeliveryModel(w, nil)
				addDueSLA(w)
				w.e.Exec(`UPDATE tickets SET custom='{"ai_sentiment":"frustrated"}' WHERE id=$1`, w.ticket.ID)
				a := &heldPostAdapter{fakeAdapter: w.fake, entered: make(chan struct{}), release: make(chan struct{})}
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(a.release) }) }
				defer release()
				var err error
				w.s.registry, err = NewRegistry(a)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(w.e.Ctx, 10*time.Second)
				defer cancel()
				work := func() error {
					switch kind {
					case "summary":
						return (&summaryWorker{s: w.s}).Work(ctx, summaryJob(w, dest))
					case "sla":
						return (&slaWarningWorker{s: w.s}).Work(ctx, nil)
					default:
						return w.s.warnSentiment(ctx, deliveryTarget{destination: destination{ID: dest, ConnectionID: w.c.ID}}, w.ticket)
					}
				}
				done := make(chan error, 1)
				go func() { done <- work() }()
				select {
				case <-a.entered:
				case err := <-done:
					t.Fatalf("worker finished before provider call: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				conn, err := w.e.D.DB.Pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Release()
				var pid int
				if err = conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
					t.Fatal(err)
				}
				changed := make(chan error, 1)
				go func() {
					_, err := conn.Exec(ctx, change.sql, changeArgs(change, w, dest, public, private)...)
					changed <- err
				}()
				waitBlocked(t, w.e.D.DB.Pool, pid, changed)
				release()
				if err = waitResult(t, done); err != nil {
					t.Fatal(err)
				}
				if err = waitResult(t, changed); err != nil {
					t.Fatal(err)
				}
				if err = work(); err != nil {
					t.Fatal(err)
				}
				if got := len(w.fake.posts) + len(w.fake.interactive); got != 1 {
					t.Fatalf("provider calls=%d, want exactly one before eligibility changed", got)
				}
			})
		}
	}
}

func TestSummarySnoozesWhenEligibilityWriterOwnsRow(t *testing.T) {
	for _, row := range []string{"tickets", "teams", "support_channel_destinations", "support_channel_connections", "workspaces", "support_channel_threads"} {
		t.Run(row, func(t *testing.T) {
			w := newWorkflow(t)
			dest, public, _ := prepareChange(w)
			var hold pgx.Tx
			setDeliveryModel(w, func() {
				var err error
				hold, err = w.e.D.DB.Pool.Begin(w.e.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				id := w.ticket.ID
				switch row {
				case "teams":
					id = public
				case "support_channel_destinations":
					id = dest
				case "support_channel_connections":
					id = w.c.ID
				case "workspaces":
					id = w.ws.ID
				case "support_channel_threads":
					w.e.Scalar(`SELECT id FROM support_channel_threads WHERE destination_id=$1`, &id, dest)
				}
				var locked uuid.UUID
				if err = hold.QueryRow(w.e.Ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id=$1 FOR UPDATE`, row), id).Scan(&locked); err != nil {
					t.Fatal(err)
				}
			})
			defer func() {
				if hold != nil {
					_ = hold.Rollback(w.e.Ctx)
				}
			}()
			ctx, cancel := context.WithTimeout(w.e.Ctx, 5*time.Second)
			defer cancel()
			err := (&summaryWorker{s: w.s}).Work(ctx, summaryJob(w, dest))
			var snooze *rivertype.JobSnoozeError
			if !errors.As(err, &snooze) {
				t.Fatalf("worker error=%v, want contention snooze", err)
			}
			if len(w.fake.interactive) != 0 {
				t.Fatal("posted while eligibility writer owned a row")
			}
			_ = hold.Rollback(w.e.Ctx)
			hold = nil
			setDeliveryModel(w, nil)
			if row == "support_channel_threads" {
				// Cleanup can time out behind a writer. The job must keep retrying
				// until its original three-minute claim expires.
				err = (&summaryWorker{s: w.s}).Work(ctx, summaryJob(w, dest))
				if err != nil && !errors.As(err, &snooze) {
					t.Fatalf("retry error=%v", err)
				}
				w.e.Exec(`UPDATE support_channel_threads SET summary_processing_expires_at=now()-interval '1 second' WHERE destination_id=$1`, dest)
			}
			if err = (&summaryWorker{s: w.s}).Work(ctx, summaryJob(w, dest)); err != nil {
				t.Fatal(err)
			}
			if len(w.fake.interactive) != 1 {
				t.Fatal("summary did not retry after writer released row")
			}
		})
	}
}

func TestSummaryKeepsRetryingLiveClaim(t *testing.T) {
	w := newWorkflow(t)
	dest := threadDestination(t, w)
	model := setDeliveryModel(w, nil)
	w.e.Exec(`UPDATE support_channel_threads SET summary_processing_expires_at=now()+interval '3 minutes' WHERE destination_id=$1`, dest)
	err := (&summaryWorker{s: w.s}).Work(w.e.Ctx, summaryJob(w, dest))
	var snooze *rivertype.JobSnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("live claim error=%v, want snooze", err)
	}
	if model.input != "" || len(w.fake.interactive) != 0 {
		t.Fatal("a second worker used a live claim")
	}
	w.e.Exec(`UPDATE support_channel_threads SET summary_processing_expires_at=now()-interval '1 second' WHERE destination_id=$1`, dest)
	if err := (&summaryWorker{s: w.s}).Work(w.e.Ctx, summaryJob(w, dest)); err != nil {
		t.Fatal(err)
	}
	if len(w.fake.interactive) != 1 {
		t.Fatal("expired claim did not retry")
	}
}

type beforeDeliveryRead struct {
	once sync.Once
	run  func()
}

func (p *beforeDeliveryRead) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(d.SQL, "SELECT h.workspace_id,c.id,") {
		p.once.Do(p.run)
	}
	return ctx
}
func (*beforeDeliveryRead) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestSLARechecksEligibilityAfterSelection(t *testing.T) {
	for _, change := range eligibilityChanges() {
		t.Run(change.name, func(t *testing.T) {
			w := newWorkflow(t)
			dest, public, private := prepareChange(w)
			addDueSLA(w)
			cfg := w.e.D.DB.Pool.Config()
			cfg.ConnConfig.Tracer = &beforeDeliveryRead{run: func() { w.e.Exec(change.sql, changeArgs(change, w, dest, public, private)...) }}
			pool, err := pgxpool.NewWithConfig(w.e.Ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			depsCopy := *w.s.d
			dbCopy := *w.s.d.DB
			dbCopy.Pool = pool
			depsCopy.DB = &dbCopy
			w.s.d = &depsCopy
			if err := (&slaWarningWorker{s: w.s}).Work(w.e.Ctx, nil); err != nil {
				t.Fatal(err)
			}
			if len(w.fake.posts) != 0 {
				t.Fatalf("posted warning after %s", change.name)
			}
		})
	}
}

func TestSummaryKeepsNotesWithinDestinationPolicy(t *testing.T) {
	for _, include := range []bool{false, true} {
		t.Run(fmt.Sprint(include), func(t *testing.T) {
			w := newWorkflow(t)
			dest := threadDestination(t, w)
			w.e.Exec(`UPDATE support_channel_destinations SET include_notes=$2 WHERE id=$1`, dest, include)
			if err := w.e.D.DB.Tx(w.e.Ctx, func(tx pgx.Tx) error {
				_, err := domain.AddMessage(w.e.Ctx, tx, w.e.D.Jobs, domain.NewMessage{WorkspaceID: w.ws.ID, TicketID: w.ticket.ID, Kind: "note", Direction: "inbound", AuthorType: "system", BodyText: "internal-note-marker"})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			model := setDeliveryModel(w, func() { w.e.Exec(`UPDATE support_channel_destinations SET include_notes=false WHERE id=$1`, dest) })
			if err := (&summaryWorker{s: w.s}).Work(w.e.Ctx, summaryJob(w, dest)); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(model.input, "internal-note-marker") != include {
				t.Fatalf("notes in model input=%v, include_notes=%v", strings.Contains(model.input, "internal-note-marker"), include)
			}
			want := 1
			if include {
				want = 0
			}
			if len(w.fake.interactive) != want {
				t.Fatalf("posts=%d, want %d after note policy changed", len(w.fake.interactive), want)
			}
		})
	}
}
func TestConcurrentWarningWorkersPostOnce(t *testing.T) {
	for _, kind := range []string{"sla", "sentiment"} {
		t.Run(kind, func(t *testing.T) {
			w := newWorkflow(t)
			dest := threadDestination(t, w)
			addDueSLA(w)
			w.e.Exec(`UPDATE tickets SET custom='{"ai_sentiment":"frustrated"}' WHERE id=$1`, w.ticket.ID)
			a := &heldPostAdapter{fakeAdapter: w.fake, entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(a.release) }) }
			defer release()
			var err error
			w.s.registry, err = NewRegistry(a)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(w.e.Ctx, 8*time.Second)
			defer cancel()
			work := func() error {
				if kind == "sla" {
					return (&slaWarningWorker{s: w.s}).Work(ctx, nil)
				}
				return w.s.warnSentiment(ctx, deliveryTarget{destination: destination{ID: dest, ConnectionID: w.c.ID}}, w.ticket)
			}
			first := make(chan error, 1)
			go func() { first <- work() }()
			select {
			case <-a.entered:
			case err := <-first:
				t.Fatalf("no provider call: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err = work(); err != nil {
				t.Fatal(err)
			}
			release()
			if err = waitResult(t, first); err != nil {
				t.Fatal(err)
			}
			if err = work(); err != nil {
				t.Fatal(err)
			}
			if len(w.fake.posts) != 1 {
				t.Fatalf("concurrent posts=%d, want one", len(w.fake.posts))
			}
		})
	}
}
