package email

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/storage"
)

const defaultTestDSN = "postgres://opendesk:opendesk@localhost:55432/opendesk_email?sslmode=disable"

var testSecret = []byte("test-only-auth-secret-0123456789abcdef")

var (
	depsOnce sync.Once
	shared   *deps.Deps
	depsErr  error
)

func testDeps(t testing.TB) *deps.Deps {
	t.Helper()
	depsOnce.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			dsn = defaultTestDSN
		}
		ctx := context.Background()
		if depsErr = db.Migrate(ctx, dsn); depsErr != nil {
			return
		}
		var database *db.DB
		if database, depsErr = db.Open(ctx, dsn, dsn); depsErr != nil {
			return
		}
		dir, err := os.MkdirTemp("", "emailtest-files-")
		if err != nil {
			depsErr = err
			return
		}
		st, err := storage.New(ctx, storage.Options{Driver: "local", Dir: dir})
		if err != nil {
			depsErr = err
			return
		}
		box, err := crypto.NewBox([]byte("development-only-credentials-key"))
		if err != nil {
			depsErr = err
			return
		}
		jc, err := river.NewClient[pgx.Tx](riverpgxv5.New(database.Pool), &river.Config{})
		if err != nil {
			depsErr = err
			return
		}
		cfg := &config.Config{Env: "development", Role: "all", APIURL: "http://api.test", AppURL: "http://app.test", ServeWeb: true,
			AuthSecret: testSecret, InboundDomain: "in.test", InboundSecret: "test", MailFrom: "opendesk <notifications@opendesk.test>",
			MailDriver: "log", GoogleClientID: "gid", GoogleClientSecret: "gsecret", GoogleMailboxClientID: "gid", GoogleMailboxClientSecret: "gsecret", MicrosoftClientID: "mid",
			MicrosoftClientSecret: "msecret", MicrosoftMailboxClientID: "mid", MicrosoftMailboxClientSecret: "msecret", MicrosoftTenant: "common", GoogleMailboxOAuth: true, MicrosoftMailboxOAuth: true, InboundAuthservIDs: []string{"mx.in.test", "mx.test"},
			MailboxIMAP: true, MailboxSystemSend: true}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		if os.Getenv("EMAILTEST_LOG") != "" {
			log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
		}
		shared = &deps.Deps{Cfg: cfg, Log: log, DB: database, Jobs: jc, Storage: st, Box: box,
			Tokens: auth.NewTokens(testSecret), Limiter: httpx.NewLimiter()}
		domain.Configure(cfg.APIURL, testSecret)
	})
	if depsErr != nil {
		t.Skipf("email tests need Postgres (TEST_DATABASE_URL): %v", depsErr)
	}
	return shared
}

// world is one isolated workspace with an admin.
type world struct {
	t     *testing.T
	ctx   context.Context
	d     *deps.Deps
	s     *service
	wid   uuid.UUID
	uid   uuid.UUID
	brand uuid.UUID // the default brand
	admin *auth.Principal
}

func newWorld(t *testing.T) *world {
	t.Helper()
	d := testDeps(t)
	ctx := context.Background()
	w := &world{t: t, ctx: ctx, d: d, s: newService(d), wid: ids.New(), uid: ids.New()}
	// No test reaches Google: a test that talks to Gmail points it at its fake.
	w.s.gmailBase = "http://127.0.0.1:1"
	slug := "acme-" + crypto.RandomString(6)
	mustExec(t, d, `INSERT INTO workspaces (id, name, slug, widget_key) VALUES ($1, 'Acme', $2, $3)`, w.wid, slug, "wk_"+crypto.RandomString(12))
	w.brand = ids.New()
	mustExec(t, d, `INSERT INTO brands (id, workspace_id, name, slug, is_default) VALUES ($1, $2, 'Acme', $3, true)`, w.brand, w.wid, slug)
	mustExec(t, d, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'Anna Agent')`, w.uid, "anna-"+crypto.RandomString(6)+"@acme.test")
	mustExec(t, d, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'admin')`, w.wid, w.uid)
	w.admin = &auth.Principal{Kind: auth.KindUser, UserID: w.uid, SessionID: ids.New(), WorkspaceID: w.wid, Role: auth.RoleAdmin, IP: "127.0.0.1"}
	return w
}

func mustExec(t testing.TB, d *deps.Deps, sql string, args ...any) {
	t.Helper()
	if _, err := d.DB.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// mailbox inserts a mailbox directly.
func (w *world) mailbox(inbound, outbound string, mutate func(*mailboxRow, *credentials)) *mailboxRow {
	w.t.Helper()
	m := &mailboxRow{WorkspaceID: w.wid}
	m.ID = ids.New()
	m.Name, m.Email, m.DisplayName = "Support", "support-"+crypto.RandomString(5)+"@acme.test", "Acme Support"
	m.InboundKind, m.OutboundKind, m.Status = inbound, outbound, "active"
	c := credentials{}
	if mutate != nil {
		mutate(m, &c)
	}
	sealed, err := w.s.sealCreds(c)
	if err != nil {
		w.t.Fatal(err)
	}
	err = w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return w.s.insertMailbox(w.ctx, tx, m, "acme", sealed) })
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func (w *world) forwardAddr(m *mailboxRow, tag string) string {
	if tag != "" {
		return m.ForwardingToken + "+" + tag + "@in.test"
	}
	return m.ForwardingToken + "@in.test"
}

// receive ingests raw mail for m and processes it.
func (w *world) receive(m *mailboxRow, raw string) uuid.UUID {
	w.t.Helper()
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", "\r\n")
	id, dup, err := w.s.ingest(w.ctx, m, []byte(raw), w.forwardAddr(m, ""), nil)
	if err != nil {
		w.t.Fatal(err)
	}
	if dup {
		w.t.Fatal("unexpected duplicate")
	}
	if err := w.s.processInbound(w.ctx, w.wid, id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) inbound(id uuid.UUID) (status, reason string) {
	w.t.Helper()
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT status, reason FROM inbound_emails WHERE id = $1`, id).Scan(&status, &reason); err != nil {
		w.t.Fatal(err)
	}
	return
}

type msgRow struct {
	ID         uuid.UUID
	TicketID   uuid.UUID
	BodyHTML   string
	BodyText   string
	FullHTML   string
	Subject    string
	FromEmail  string
	MsgID      *string
	InReplyTo  string
	References []string
	Delivery   string
	DelivError string
	Direction  string
}

func (w *world) messageFor(inboundID uuid.UUID) msgRow {
	w.t.Helper()
	var m msgRow
	err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id, ticket_id, body_html, body_text, full_html, subject, from_email, email_message_id,
		in_reply_to, "references", delivery_status, delivery_error, direction FROM messages WHERE workspace_id = $1 AND inbound_email_id = $2`,
		w.wid, inboundID).Scan(&m.ID, &m.TicketID, &m.BodyHTML, &m.BodyText, &m.FullHTML, &m.Subject, &m.FromEmail, &m.MsgID,
		&m.InReplyTo, &m.References, &m.Delivery, &m.DelivError, &m.Direction)
	if err != nil {
		w.t.Fatalf("message for inbound %s: %v", inboundID, err)
	}
	return m
}

func (w *world) message(id uuid.UUID) msgRow {
	w.t.Helper()
	var m msgRow
	err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id, ticket_id, body_html, body_text, full_html, subject, from_email, email_message_id,
		in_reply_to, "references", delivery_status, delivery_error, direction FROM messages WHERE workspace_id = $1 AND id = $2`,
		w.wid, id).Scan(&m.ID, &m.TicketID, &m.BodyHTML, &m.BodyText, &m.FullHTML, &m.Subject, &m.FromEmail, &m.MsgID,
		&m.InReplyTo, &m.References, &m.Delivery, &m.DelivError, &m.Direction)
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

func (w *world) ticket(id uuid.UUID) domain.Ticket {
	w.t.Helper()
	t, err := domain.LoadTicket(w.ctx, w.d.DB.Pool, w.wid, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return t
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.d.DB.Pool.QueryRow(w.ctx, sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (w *world) eventCount(typ string) int {
	return w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = $2`, w.wid, typ)
}

func (w *world) jobCount(kind string, argKey, argVal string) int {
	if argKey == "" {
		return w.count(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'workspace_id' = $2`, kind, w.wid.String())
	}
	return w.count(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>$2 = $3`, kind, argKey, argVal)
}

// agentReply adds an outbound agent reply the way the helpdesk API does.
func (w *world) agentReply(ticketID uuid.UUID, body string, msgID string) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		nm := domain.NewMessage{WorkspaceID: w.wid, TicketID: ticketID, Kind: "reply", Direction: "outbound", AuthorType: "user",
			AuthorUserID: &w.uid, BodyHTML: "<p>" + body + "</p>", BodyText: body, DeliveryStatus: "pending"}
		if msgID != "" {
			nm.EmailMessageID = &msgID
		}
		m, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, nm)
		id = m.ID
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// api serves this area's routes with the given principal.
func (w *world) api(p *auth.Principal) *httptest.Server {
	w.t.Helper()
	httpx.Install(w.d.Log)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if p != nil {
				req = req.WithContext(auth.WithPrincipal(req.Context(), p))
			}
			next.ServeHTTP(rw, req)
		})
	})
	cfg := huma.DefaultConfig("test", "0")
	api := humachi.New(r, cfg)
	s := w.s
	s.registerMailboxRoutes(api)
	s.registerTemplateRoutes(api)
	s.registerConnectRoutes(api)
	s.registerOAuthRoutes(api, r)
	r.Post("/v1/inbound/email", s.inboundHTTP)
	srv := httptest.NewServer(r)
	w.t.Cleanup(srv.Close)
	return srv
}

func doJSON(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return resp.StatusCode
}

func fixture(t testing.TB, name string, repl ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for i := 0; i+1 < len(repl); i += 2 {
		s = strings.ReplaceAll(s, repl[i], repl[i+1])
	}
	return s
}

// selfSigned returns a TLS config for 127.0.0.1/localhost and a pool trusting it.
func selfSigned(t testing.TB) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, pool
}

type pgxTx = pgx.Tx

func jobsSendTicketEmail(wid, tid uuid.UUID, subject, html string) jobs.SendTicketEmailArgs {
	return jobs.SendTicketEmailArgs{WorkspaceID: wid, TicketID: tid, Subject: subject, HTML: html, AutoGenerated: true}
}
