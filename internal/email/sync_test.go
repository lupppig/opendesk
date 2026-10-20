package email

import (
	"bytes"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/jobs"
)

type literal struct {
	*bytes.Reader
	n int64
}

func (l literal) Size() int64 { return l.n }

func appendMail(t *testing.T, u *imapmemserver.User, raw string, at time.Time) {
	t.Helper()
	raw = strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\n", "\r\n")
	if _, err := u.Append("INBOX", literal{bytes.NewReader([]byte(raw)), int64(len(raw))}, &imap.AppendOptions{Time: at}); err != nil {
		t.Fatal(err)
	}
}

// imapServer runs an in-memory IMAP server over implicit TLS on 127.0.0.1.
func imapServer(t *testing.T, w *world) (*imapmemserver.User, int) {
	t.Helper()
	tlsCfg, pool := selfSigned(t)
	w.s.guard.rootCAs = pool
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("agent@acme.test", "imap-pass-1")
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}},
	})
	l, err := tlsListen(tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return u, l.Addr().(*net.TCPAddr).Port
}

func testMail(from, subject string) string {
	return "From: " + from + "\nTo: agent@acme.test\nSubject: " + subject + "\nMessage-ID: <" + crypto.RandomString(10) +
		"@customer.test>\nDate: " + time.Now().Format(time.RFC1123Z) + "\nContent-Type: text/plain\n\nHello from " + subject + "\n"
}

func TestIMAPSync(t *testing.T) {
	w := newWorld(t)
	u, port := imapServer(t, w)
	appendMail(t, u, testMail(customer(), "old one"), time.Now().Add(-10*24*time.Hour))
	appendMail(t, u, testMail(customer(), "recent one"), time.Now().Add(-time.Hour))
	appendMail(t, u, testMail(customer(), "recent two"), time.Now())

	m := w.mailbox(InboundIMAP, OutboundSystem, func(m *mailboxRow, c *credentials) {
		m.Email = "agent@acme.test"
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: port, IMAPTLS: TLSImplicit, IMAPUsername: "agent@acme.test"}
		c.Password = "imap-pass-1"
	})
	inboundCount := func() int {
		return w.count(`SELECT count(*) FROM inbound_emails WHERE mailbox_id = $1`, m.ID)
	}
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	m2, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if m2.Status != "active" || m2.LastError != "" {
		t.Fatalf("status %s %q", m2.Status, m2.LastError)
	}
	if n := inboundCount(); n != 2 {
		t.Fatalf("initial import: %d messages, want 2 (7-day window)", n)
	}
	if m2.LastUID != 3 || m2.UIDValidity == 0 || m2.LastSyncedAt == nil {
		t.Fatalf("cursor uid=%d validity=%d", m2.LastUID, m2.UIDValidity)
	}
	if w.count(`SELECT count(*) FROM river_job WHERE kind = 'email.process_inbound' AND args->>'workspace_id' = $1`, w.wid.String()) != 2 {
		t.Fatal("processing jobs missing")
	}

	// Incremental pass picks up only the new one.
	appendMail(t, u, testMail(customer(), "third"), time.Now())
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	m2, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if n := inboundCount(); n != 3 || m2.LastUID != 4 {
		t.Fatalf("incremental: %d messages, cursor %d", n, m2.LastUID)
	}
	// Nothing new: the last-synced time advances without an event.
	before := w.eventCount("mailbox.updated")
	mustExec(t, w.d, `UPDATE mailboxes SET last_synced_at = now() - interval '1 hour' WHERE id = $1`, m.ID)
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil || inboundCount() != 3 {
		t.Fatalf("idle pass: %v %d", err, inboundCount())
	}
	if got := w.eventCount("mailbox.updated") - before; got != 0 {
		t.Fatalf("idle sync published %d mailbox.updated events, want 0", got)
	}
	if got := w.count(`SELECT count(*) FROM mailboxes WHERE id = $1 AND last_synced_at > now() - interval '1 minute'`, m.ID); got != 1 {
		t.Fatal("idle sync did not advance last_synced_at")
	}

	// A changed UIDVALIDITY restarts the import; dedupe keeps one row per message.
	mustExec(t, w.d, `UPDATE mailboxes SET imap_uid_validity = 12345 WHERE id = $1`, m.ID)
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	m2, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if inboundCount() != 3 || m2.UIDValidity == 12345 || m2.LastUID != 4 {
		t.Fatalf("validity reset: %d rows, validity %d, cursor %d", inboundCount(), m2.UIDValidity, m2.LastUID)
	}

	// Wrong password: error state, safe message, one event, backoff.
	before = w.eventCount("mailbox.updated")
	sealed, _ := w.s.sealCreds(credentials{Password: "wrong-pass-zz"})
	mustExec(t, w.d, `UPDATE mailboxes SET credentials_enc = $2 WHERE id = $1`, m.ID, sealed)
	for range 2 {
		if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	m2, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if m2.Status != "error" || m2.LastError == "" || strings.Contains(m2.LastError, "wrong-pass-zz") || m2.SyncFailures != 2 {
		t.Fatalf("error state %s %q %d", m2.Status, m2.LastError, m2.SyncFailures)
	}
	if got := w.eventCount("mailbox.updated") - before; got != 1 {
		t.Fatalf("mailbox.updated published %d times, want 1", got)
	}
	if w.count(`SELECT count(*) FROM mailboxes WHERE id = $1 AND next_sync_at > now()`, m.ID) != 1 {
		t.Fatal("no backoff")
	}
	// Not due while backing off.
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.count(`SELECT count(*) FROM river_job WHERE kind = 'email.sync_mailbox' AND args->>'mailbox_id' = $1 AND state = 'available'`, m.ID.String()) != 0 {
		t.Fatal("backing-off mailbox enqueued")
	}

	// Recovery clears the error.
	sealed, _ = w.s.sealCreds(credentials{Password: "imap-pass-1"})
	mustExec(t, w.d, `UPDATE mailboxes SET credentials_enc = $2 WHERE id = $1`, m.ID, sealed)
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	m2, _ = loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if m2.Status != "active" || m2.LastError != "" || m2.SyncFailures != 0 {
		t.Fatalf("recovery %s %q", m2.Status, m2.LastError)
	}
}

// A mailbox whose history came over by import starts with only new mail.
func TestIMAPSyncStartNew(t *testing.T) {
	w := newWorld(t)
	u, port := imapServer(t, w)
	appendMail(t, u, testMail(customer(), "recent one"), time.Now().Add(-time.Hour))
	appendMail(t, u, testMail(customer(), "recent two"), time.Now())
	m := w.mailbox(InboundIMAP, OutboundSystem, func(m *mailboxRow, c *credentials) {
		m.Email = "agent@acme.test"
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: port, IMAPTLS: TLSImplicit, IMAPUsername: "agent@acme.test",
			SyncStart: SyncStartNew}
		c.Password = "imap-pass-1"
	})
	inboundCount := func() int {
		return w.count(`SELECT count(*) FROM inbound_emails WHERE mailbox_id = $1`, m.ID)
	}
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	m2, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if n := inboundCount(); n != 0 || m2.LastUID != 2 || m2.UIDValidity == 0 || m2.Status != "active" {
		t.Fatalf("first sync: %d messages, cursor %d, validity %d, %s", n, m2.LastUID, m2.UIDValidity, m2.Status)
	}
	appendMail(t, u, testMail(customer(), "new one"), time.Now())
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := inboundCount(); n != 1 {
		t.Fatalf("after new mail: %d messages, want 1", n)
	}
	// A changed UIDVALIDITY on a mailbox that already syncs reads the window again: mail
	// may have arrived since the last pass.
	mustExec(t, w.d, `UPDATE mailboxes SET imap_uid_validity = 12345 WHERE id = $1`, m.ID)
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := inboundCount(); n != 3 {
		t.Fatalf("validity reset: %d messages, want 3", n)
	}
}

func TestEnqueueDueRequeuesAfterCompletedSync(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundIMAP, OutboundSystem, nil)

	// Seed the job shape created before recurring syncs excluded completed jobs
	// from River uniqueness. Existing self-hosted instances retain these rows.
	legacyOpts := &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 1,
		UniqueOpts: river.UniqueOpts{ByArgs: true}}
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		_, err := w.d.Jobs.InsertTx(w.ctx, tx, jobs.SyncMailboxArgs{MailboxID: m.ID}, legacyOpts)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, w.d, `UPDATE river_job SET state = 'completed', finalized_at = now()
		WHERE kind = 'email.sync_mailbox' AND args->>'mailbox_id' = $1 AND state = 'available'`, m.ID.String())

	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM river_job WHERE kind = 'email.sync_mailbox'
		AND args->>'mailbox_id' = $1 AND state = 'available'`, m.ID.String()); got != 1 {
		t.Fatalf("due syncs after a legacy completed job: %d available jobs, want 1", got)
	}
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM river_job WHERE kind = 'email.sync_mailbox'
		AND args->>'mailbox_id' = $1 AND state = 'available'`, m.ID.String()); got != 1 {
		t.Fatalf("duplicate due sweep: %d available jobs, want 1", got)
	}

	mustExec(t, w.d, `UPDATE river_job SET state = 'completed', finalized_at = now()
		WHERE kind = 'email.sync_mailbox' AND args->>'mailbox_id' = $1 AND state = 'available'`, m.ID.String())
	if err := w.s.enqueueDue(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*) FROM river_job WHERE kind = 'email.sync_mailbox'
		AND args->>'mailbox_id' = $1 AND state = 'available'`, m.ID.String()); got != 1 {
		t.Fatalf("due syncs after a completed job: %d available jobs, want 1", got)
	}
}

func TestIMAPRefusesUntrustedCertificate(t *testing.T) {
	w := newWorld(t)
	_, port := imapServer(t, w)
	w.s.guard.rootCAs = nil // system roots do not trust the test certificate
	m := w.mailbox(InboundIMAP, OutboundSystem, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: port, IMAPTLS: TLSImplicit, IMAPUsername: "agent@acme.test"}
		c.Password = "imap-pass-1"
	})
	_ = w.s.syncMailbox(w.ctx, m.ID)
	m2, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if m2.Status != "error" || !strings.Contains(strings.ToLower(m2.LastError), "tls") {
		t.Fatalf("status %s %q", m2.Status, m2.LastError)
	}
}

func TestPlanSync(t *testing.T) {
	cases := []struct {
		validity, last uint32
		sel            selectInfo
		initial, upTo  bool
	}{
		{0, 0, selectInfo{Validity: 7, UIDNext: 5, Exists: 4}, true, false},
		{0, 0, selectInfo{Validity: 7, Exists: 0}, true, true},
		{6, 10, selectInfo{Validity: 7, UIDNext: 5, Exists: 4}, true, false},
		{7, 4, selectInfo{Validity: 7, UIDNext: 5, Exists: 4}, false, true},
		{7, 4, selectInfo{Validity: 7, UIDNext: 9, Exists: 6}, false, false},
		{7, 4, selectInfo{Validity: 7, Exists: 6}, false, false},
		{7, 4, selectInfo{Validity: 7, UIDNext: 9, Exists: 0}, false, true},
	}
	for i, c := range cases {
		in, up := planSync(c.validity, c.last, c.sel)
		if in != c.initial || up != c.upTo {
			t.Errorf("case %d: got %v %v", i, in, up)
		}
	}
	if got := newUIDs([]uint32{9, 3, 7, 7, 5}, 5); len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Errorf("newUIDs %v", got)
	}
	if got := newUIDs([]uint32{5}, 5); len(got) != 0 {
		t.Errorf("UID n:* echo of the last message must be dropped: %v", got)
	}
	if finalCursor(true, 3, selectInfo{UIDNext: 20}, true) != 19 || finalCursor(true, 3, selectInfo{UIDNext: 20}, false) != 3 ||
		finalCursor(false, 3, selectInfo{UIDNext: 20}, true) != 3 {
		t.Error("finalCursor")
	}
}

func tlsListen(cfg *tls.Config) (net.Listener, error) { return tls.Listen("tcp", "127.0.0.1:0", cfg) }
