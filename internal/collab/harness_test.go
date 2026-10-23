package collab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/useopendesk/opendesk/internal/plan"
	"github.com/useopendesk/opendesk/internal/storage"
)

// The Collab test harness: a real Postgres (TEST_DATABASE_URL, default the dev database
// opendesk_collab on localhost:55432), local storage, billing off (so Collab is on).

const defaultTestDSN = "postgres://opendesk:opendesk@localhost:55432/opendesk_collab?sslmode=disable"

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
		dir, err := os.MkdirTemp("", "collabtest-files-")
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
			AuthSecret: testSecret, MailFrom: "opendesk <notifications@opendesk.test>", MailDriver: "log",
			Collab: true, CollabIMAP: true, CollabGoogleClientID: "gid", CollabGoogleClientSecret: "gsecret",
			CollabMicrosoftClientID: "mid", CollabMicrosoftClientSecret: "msecret", MicrosoftTenant: "common"}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		if os.Getenv("COLLABTEST_LOG") != "" {
			log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
		}
		shared = &deps.Deps{Cfg: cfg, Log: log, DB: database, Jobs: jc, Storage: st, Box: box,
			Tokens: auth.NewTokens(testSecret), Limiter: httpx.NewLimiter(), Plan: plan.New(false, 1)}
		domain.Configure(cfg.APIURL, testSecret)
	})
	if depsErr != nil {
		t.Skipf("collab tests need Postgres (TEST_DATABASE_URL): %v", depsErr)
	}
	return shared
}

// world is one isolated workspace with an owner (Olive), an agent (Adam), a second agent
// (Bea) and a viewer (Vic).
type world struct {
	t   *testing.T
	ctx context.Context
	d   *deps.Deps
	s   *service
	wid uuid.UUID

	owner, agent, agent2, viewer *auth.Principal
}

func newWorld(t *testing.T) *world {
	t.Helper()
	d := testDeps(t)
	ctx := context.Background()
	w := &world{t: t, ctx: ctx, d: d, s: newService(d), wid: ids.New()}
	slug := "acme-" + crypto.RandomString(6)
	mustExec(t, d, `INSERT INTO workspaces (id, name, slug, widget_key) VALUES ($1, 'Acme', $2, $3)`, w.wid, slug, "wk_"+crypto.RandomString(12))
	mustExec(t, d, `INSERT INTO brands (id, workspace_id, name, slug, is_default) VALUES ($1, $2, 'Acme', $3, true)`, ids.New(), w.wid, slug)
	w.owner = w.member("Olive Owner", auth.RoleOwner)
	w.agent = w.member("Adam Agent", auth.RoleAgent)
	w.agent2 = w.member("Bea Agent", auth.RoleAgent)
	w.viewer = w.member("Vic Viewer", auth.RoleViewer)
	return w
}

// member adds a user with a role and returns their principal.
func (w *world) member(name string, role auth.Role) *auth.Principal {
	w.t.Helper()
	uid := ids.New()
	mail := strings.ToLower(strings.Fields(name)[0]) + "-" + crypto.RandomString(6) + "@acme.test"
	mustExec(w.t, w.d, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, uid, mail, name)
	mustExec(w.t, w.d, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, $3)`, w.wid, uid, string(role))
	return &auth.Principal{Kind: auth.KindUser, UserID: uid, SessionID: ids.New(), WorkspaceID: w.wid, Role: role, IP: "127.0.0.1"}
}

// team adds a team with the given members.
func (w *world) team(name string, members ...*auth.Principal) uuid.UUID {
	w.t.Helper()
	id := ids.New()
	mustExec(w.t, w.d, `INSERT INTO teams (id, workspace_id, name) VALUES ($1, $2, $3)`, id, w.wid, name)
	for _, m := range members {
		mustExec(w.t, w.d, `INSERT INTO team_members (team_id, user_id) VALUES ($1, $2)`, id, m.UserID)
	}
	return id
}

func mustExec(t testing.TB, d *deps.Deps, sql string, args ...any) {
	t.Helper()
	if _, err := d.DB.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// account inserts an account directly, owned by owner, with its special folders
// (inbox, sent, archive, trash, spam, drafts) and the given audience when shared.
func (w *world) account(owner *auth.Principal, provider, visibility string, shared Audience) *accountRow {
	w.t.Helper()
	id := ids.New()
	addr := "box-" + crypto.RandomString(6) + "@acme.test"
	sealed, err := w.s.sealCreds(credentials{RefreshToken: "rt", AccessToken: "at", Expiry: time.Now().Add(time.Hour), Password: "pw"})
	if err != nil {
		w.t.Fatal(err)
	}
	mustExec(w.t, w.d, `INSERT INTO collab_accounts (id, workspace_id, owner_id, provider, email, name, display_name, visibility, credentials_enc, status)
		VALUES ($1, $2, $3, $4, $5, $5, 'Box', $6, $7, 'active')`, id, w.wid, owner.UserID, provider, addr, visibility, sealed)
	for _, role := range []string{RoleInbox, RoleSent, RoleArchive, RoleTrash, RoleSpam, RoleDrafts} {
		remote := strings.ToUpper(role)
		mustExec(w.t, w.d, `INSERT INTO collab_folders (id, workspace_id, account_id, remote_id, name, path, role, kind)
			VALUES ($1, $2, $3, $4, $4, $4, $5, 'folder')`, ids.New(), w.wid, id, remote, role)
	}
	if visibility == VisibilityShared {
		err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error { return saveAudience(w.ctx, tx, w.wid, &id, nil, shared, owner.UserID) })
		if err != nil {
			w.t.Fatal(err)
		}
	}
	a, err := loadAccount(w.ctx, w.d.DB.Pool, w.wid, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return a
}

// folder returns the account's folder with a role.
func (w *world) folder(a *accountRow, role string) *folderRow {
	w.t.Helper()
	f, err := scanFolder(w.d.DB.Pool.QueryRow(w.ctx, `SELECT `+folderColumns+` FROM collab_folders WHERE account_id = $1 AND role = $2`, a.ID, role))
	if err != nil {
		w.t.Fatalf("folder %s: %v", role, err)
	}
	return f
}

// seedThread inserts a conversation with one unread message from from@example.com in the
// given folders, the way a sync stores one, and returns the thread and message ids.
func (w *world) seedThread(a *accountRow, subject string, folders ...*folderRow) (uuid.UUID, uuid.UUID) {
	w.t.Helper()
	tid, mid := ids.New(), ids.New()
	now := time.Now().UTC().Truncate(time.Second)
	folderIDs := []uuid.UUID{}
	for _, f := range folders {
		folderIDs = append(folderIDs, f.ID)
	}
	from := Address{Name: "Frank Sender", Email: "from@example.com"}
	to := []Address{{Name: a.DisplayName, Email: a.Email}}
	mustExec(w.t, w.d, `INSERT INTO collab_threads (id, workspace_id, account_id, remote_id, subject, snippet, participants, last_message_at,
		message_count, unread, folder_ids, search) VALUES ($1, $2, $3, $4, $5, 'Hello there', $6, $7, 1, true, $8,
		to_tsvector('simple', $5 || ' Hello there from@example.com'))`, tid, w.wid, a.ID, "t-"+tid.String(), subject,
		[]Address{from, to[0]}, now, folderIDs)
	mustExec(w.t, w.d, `INSERT INTO collab_messages (id, workspace_id, account_id, thread_id, remote_id, message_id, from_addr, to_addrs,
		subject, snippet, body_html, body_text, sent_at, unread) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'Hello there',
		'<p>Hello there</p>', 'Hello there', $10, true)`, mid, w.wid, a.ID, tid, "m-"+mid.String(), mid.String()+"@example.com",
		from, to, subject, now)
	for _, f := range folders {
		mustExec(w.t, w.d, `INSERT INTO collab_message_folders (message_id, folder_id) VALUES ($1, $2)`, mid, f.ID)
	}
	return tid, mid
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.d.DB.Pool.QueryRow(w.ctx, sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

// api serves this area's routes as the given principal.
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
	api := humachi.New(r, huma.DefaultConfig("test", "0"))
	s := w.s
	s.registerStatusRoutes(api)
	s.registerAccountRoutes(api)
	s.registerSettingsRoutes(api)
	s.registerConnectRoutes(api, r)
	s.registerFolderRoutes(api)
	s.registerThreadRoutes(api)
	s.registerSendRoutes(api)
	s.registerDraftRoutes(api)
	s.registerCommentRoutes(api)
	s.registerDetailRoutes(api)
	s.registerSidebarRoutes(api)
	r.Get("/v1/collab/attachments/{message_id}/{index}", s.attachmentHandler)
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

// fakeProvider is an in-memory Provider that records what it was asked to do.
type fakeProvider struct {
	mu      sync.Mutex
	folders []RemoteFolder
	applied []ApplyOp
	sent    [][]byte
	sentRef SentRef
	raw     map[string][]byte // by remote id
	err     error             // returned by every call when set
	created []string
}

func (f *fakeProvider) Folders(context.Context) ([]RemoteFolder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.folders, f.err
}

func (f *fakeProvider) Apply(_ context.Context, op ApplyOp) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.applied = append(f.applied, op)
	return nil
}

func (f *fakeProvider) Send(_ context.Context, raw []byte, _ SendInfo) (SentRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return SentRef{}, f.err
	}
	f.sent = append(f.sent, raw)
	ref := f.sentRef
	if ref.RemoteID == "" {
		ref.RemoteID = "sent-" + crypto.RandomString(8)
	}
	return ref, nil
}

func (f *fakeProvider) FetchRaw(_ context.Context, ref MessageRef) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if b, ok := f.raw[ref.RemoteID]; ok {
		return b, nil
	}
	return nil, pgx.ErrNoRows
}

func (f *fakeProvider) CreateFolder(_ context.Context, name string, parent *RemoteFolder) (RemoteFolder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return RemoteFolder{}, f.err
	}
	path := name
	if parent != nil {
		path = parent.Path + "/" + name
	}
	f.created = append(f.created, path)
	return RemoteFolder{RemoteID: "r-" + path, Name: name, Path: path, Kind: KindFolder}, nil
}

func (f *fakeProvider) RenameFolder(_ context.Context, r RemoteFolder, name string) (RemoteFolder, error) {
	r.Name, r.Path = name, name
	return r, f.err
}

func (f *fakeProvider) DeleteFolder(context.Context, RemoteFolder) error { return f.err }

func (f *fakeProvider) Close() {}

// useFake makes every account use p.
func (w *world) useFake(p *fakeProvider) {
	w.s.newProvider = func(context.Context, *accountRow) (Provider, error) { return p, nil }
}
