package imports

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// threader is the fake source with a Threader: headers by message id, answered in order.
type threader struct {
	*fake
	headers map[string]source.Threading
	// errs are answered by Threading for a message id, once each.
	errs map[string]error
	// asked is every message id Threading was called with, in order.
	asked []string
}

func (f *threader) Threading(_ context.Context, id string) (source.Threading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, id)
	if err, ok := f.errs[id]; ok {
		delete(f.errs, id)
		return source.Threading{}, err
	}
	if h, ok := f.headers[id]; ok {
		return h, nil
	}
	return source.Threading{}, &source.RemoteError{Status: 404}
}

// withThreader makes the env's source list emails without their headers and read them
// one at a time.
func withThreader(e *env) *threader {
	th := &threader{fake: e.f, errs: map[string]error{}, headers: map[string]source.Threading{
		"101-1": {MessageID: "<q1@customer.test>"},
		"101-2": {MessageID: "<a1@acme.test>", InReplyTo: "<q1@customer.test>", References: []string{"<q1@customer.test>"}},
		// 101-4 has no raw email: it stays without headers.
		"102-1": {MessageID: "<same@customer.test>"},
		"102-2": {MessageID: "<same@customer.test>", InReplyTo: "<earlier@customer.test>"},
	}}
	e.s.Factories[Zendesk] = func(c source.Credentials, o source.Options) (source.Source, error) {
		if _, err := e.f.factory(c, o); err != nil {
			return nil, err
		}
		return th, nil
	}
	return th
}

type threadingRow struct {
	ID, InReplyTo string
	Refs          []string
}

func (e *env) threading(t *testing.T, external string) threadingRow {
	t.Helper()
	var r threadingRow
	var id *string
	if err := e.D.DB.Pool.QueryRow(e.Ctx, `SELECT m.email_message_id, m.in_reply_to, m."references" FROM import_records r
		JOIN messages m ON m.id = r.local_id WHERE r.workspace_id = $1 AND r.kind = 'message' AND r.external_id = $2`,
		e.ws.ID, external).Scan(&id, &r.InReplyTo, &r.Refs); err != nil {
		t.Fatalf("%s: %v", external, err)
	}
	if id != nil {
		r.ID = *id
	}
	return r
}

func TestThreadingPhase(t *testing.T) {
	e := newEnv(t)
	th := withThreader(e)
	x := e.start(t, e.zendeskBody())
	done := e.drive(t, x.ID)
	if done.Status != StatusCompleted || done.Phase != PhaseDone {
		t.Fatalf("status %s phase %s %s", done.Status, done.Phase, done.LastError)
	}
	// The email replies, newest first; not the note (101-3) or the chat (104-1).
	if want := []string{"102-2", "102-1", "101-4", "101-2", "101-1"}; !slices.Equal(th.asked, want) {
		t.Errorf("asked %v, want %v", th.asked, want)
	}
	if done.Counts.Threaded != 5 || done.Counts.ThreadTotal != 5 || done.Counts.Failed != 0 {
		t.Errorf("counts %+v", done.Counts)
	}
	if r := e.threading(t, "101-2"); r.ID != "<a1@acme.test>" || r.InReplyTo != "<q1@customer.test>" || !slices.Equal(r.Refs, []string{"<q1@customer.test>"}) {
		t.Errorf("101-2 %+v", r)
	}
	if r := e.threading(t, "101-1"); r.ID != "<q1@customer.test>" {
		t.Errorf("101-1 %+v", r)
	}
	if r := e.threading(t, "101-4"); r.ID != "" || r.InReplyTo != "" {
		t.Errorf("101-4 without a raw email %+v", r)
	}
	// Read first, 102-2 keeps the Message-ID the two share; 102-1 gets only its other headers.
	if r := e.threading(t, "102-2"); r.ID != "<same@customer.test>" || r.InReplyTo != "<earlier@customer.test>" {
		t.Errorf("102-2 %+v", r)
	}
	if r := e.threading(t, "102-1"); r.ID != "" {
		t.Errorf("102-1 took a Message-ID already held %+v", r)
	}
	if n := e.count(t, `SELECT count(*) FROM import_records WHERE workspace_id = $1 AND kind = 'message' AND threaded_at IS NOT NULL`, e.ws.ID); n != 5 {
		t.Errorf("emails marked read %d", n)
	}

	// A sync reads only what is new: every email above was read once.
	th.asked = nil
	e.Do("POST", "/v1/imports/"+x.ID.String()+"/sync", e.admin, nil).Want(t, 200)
	if again := e.drive(t, x.ID); again.Status != StatusCompleted || len(th.asked) != 0 {
		t.Errorf("sync %s asked %v", again.Status, th.asked)
	}
}

// A rate limit in the threading phase waits, then goes on from the email it stopped at.
func TestThreadingPhaseWaitsOutRateLimit(t *testing.T) {
	e := newEnv(t)
	th := withThreader(e)
	th.errs["101-2"] = &source.RateLimited{RetryAfter: time.Minute}
	x := e.start(t, e.zendeskBody())
	raws := e.popJobs(t, jobs.ImportRunArgs{}.Kind(), x.ID)
	if len(raws) != 1 {
		t.Fatalf("queued %d", len(raws))
	}
	var a jobs.ImportRunArgs
	_ = json.Unmarshal(raws[0], &a)
	var snooze *rivertype.JobSnoozeError
	if err := work(t, func() error { return e.s.run(e.Ctx, a) }); !errors.As(err, &snooze) || snooze.Duration < 50*time.Second {
		t.Fatalf("rate limit: %v", err)
	}
	waiting := e.load(t, x.ID)
	if waiting.Status != StatusRunning || waiting.Phase != PhaseThreading || waiting.WaitingUntil == nil {
		t.Fatalf("waiting %s %s %v", waiting.Status, waiting.Phase, waiting.WaitingUntil)
	}
	// The three read before the limit are saved.
	if waiting.Counts.Threaded != 3 || waiting.Counts.ThreadTotal != 5 || e.threading(t, "102-2").ID != "<same@customer.test>" {
		t.Fatalf("before the limit %+v", waiting.Counts)
	}
	e.Exec(`UPDATE imports SET waiting_until = NULL WHERE id = $1`, x.ID)
	if err := work(t, func() error { return e.s.run(e.Ctx, a) }); err != nil {
		t.Fatal(err)
	}
	done := e.load(t, x.ID)
	if done.Status != StatusCompleted || done.Counts.Threaded != 5 || e.threading(t, "101-1").ID != "<q1@customer.test>" {
		t.Fatalf("done %s %+v", done.Status, done.Counts)
	}
	if want := []string{"102-2", "102-1", "101-4", "101-2", "101-2", "101-1"}; !slices.Equal(th.asked, want) {
		t.Errorf("asked %v, want %v", th.asked, want)
	}
}

// A source that lists emails with their headers has no threading phase to run.
func TestNoThreadingWithoutThreader(t *testing.T) {
	e := newEnv(t)
	x := e.start(t, e.zendeskBody())
	done := e.drive(t, x.ID)
	if done.Status != StatusCompleted || done.Counts.Threaded != 0 || done.Counts.ThreadTotal != 0 {
		t.Fatalf("done %s %+v", done.Status, done.Counts)
	}
}
