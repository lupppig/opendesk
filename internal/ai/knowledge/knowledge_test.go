package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"math"
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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/storage"
)

const testDSN = "postgres://opendesk:opendesk@localhost:55432/opendesk_knowledge?sslmode=disable"

func TestMain(m *testing.M) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		os.Setenv("TEST_DATABASE_URL", testDSN)
	}
	os.Exit(m.Run())
}

var (
	dbOnce sync.Once
	dbErr  error
)

// requireDB skips the test when the test database cannot be reached, creating the
// database when only it is missing.
func requireDB(t *testing.T) {
	t.Helper()
	dbOnce.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_URL")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, dsn)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "3D000" {
			cfg, perr := pgx.ParseConfig(dsn)
			if perr != nil {
				dbErr = perr
				return
			}
			name := cfg.Database
			cfg.Database = "postgres"
			admin, aerr := pgx.ConnectConfig(ctx, cfg)
			if aerr != nil {
				dbErr = aerr
				return
			}
			_, _ = admin.Exec(ctx, `CREATE DATABASE "`+strings.ReplaceAll(name, `"`, ``)+`"`)
			admin.Close(ctx)
			conn, err = pgx.Connect(ctx, dsn)
		}
		if err != nil {
			dbErr = err
			return
		}
		conn.Close(ctx)
	})
	if dbErr != nil {
		t.Skipf("no test database: %v", dbErr)
	}
}

// fakeEmbedder embeds words into a small vector space where a few synonyms share a
// dimension, so semantic retrieval finds what keyword search cannot.
type fakeEmbedder struct {
	mu    sync.Mutex
	calls int
	texts int
	fail  bool
}

var synonyms = map[string]string{"money": "refund", "back": "refund", "reimbursement": "refund", "refunds": "refund"}

func (f *fakeEmbedder) Model() string { return "fake-embed-knowledge" }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string, query bool) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return nil, ai.ErrUnavailable
	}
	if !query {
		f.calls++
		f.texts += len(texts)
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 64)
		for _, w := range queryWord.FindAllString(strings.ToLower(t), -1) {
			if stopwords[w] {
				continue
			}
			if s, ok := synonyms[w]; ok {
				w = s
			}
			h := fnv.New32a()
			_, _ = h.Write([]byte(w))
			v[h.Sum32()%64]++
		}
		var n float64
		for _, x := range v {
			n += float64(x * x)
		}
		if n == 0 {
			v[0], n = 1, 1
		}
		for j := range v {
			v[j] = float32(float64(v[j]) / math.Sqrt(n))
		}
		out[i] = v
	}
	return out, nil
}

func (f *fakeEmbedder) embedCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type env struct {
	*hdtest.Env
	S   *Service
	Emb *fakeEmbedder
	WS  hdtest.WS
}

func newEnv(t *testing.T, embeddings bool) *env {
	t.Helper()
	requireDB(t)
	base := hdtest.Deps(t)
	d := *base
	cfg := *base.Cfg
	d.Cfg = &cfg
	svc := ai.New(context.Background(), &d)
	var emb *fakeEmbedder
	if embeddings {
		emb = &fakeEmbedder{}
		svc.WithProvider(nil, emb, "", "")
	} else {
		svc.WithProvider(nil, nil, "", "")
	}
	s := newService(&d, svc)
	s.newCrawler = devCrawler

	httpx.Install(d.Log)
	r := chi.NewRouter()
	r.Use(httpx.RequestID)
	resolver := &auth.Resolver{Pool: d.DB.Pool, Tokens: d.Tokens, ClientIP: httpx.ClientIP(false, 0)}
	r.Use(resolver.Middleware)
	api := humachi.New(r, huma.DefaultConfig("opendesk test", "test"))
	s.register(api)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	e := &env{Env: &hdtest.Env{T: t, D: &d, Srv: srv, Ctx: context.Background()}, S: s, Emb: emb}
	e.WS = e.NewWorkspace()
	return e
}

func (e *env) admin() hdtest.Auth  { return e.WS.Admin.As(e.WS) }
func (e *env) viewer() hdtest.Auth { return e.WS.Viewer.As(e.WS) }

func (e *env) source(r *hdtest.Resp) Source {
	e.T.Helper()
	var s Source
	r.JSON(e.T, &s)
	return s
}

func (e *env) get(id uuid.UUID) Source {
	e.T.Helper()
	return e.source(e.Do(http.MethodGet, "/v1/ai/knowledge/"+id.String(), e.viewer(), nil).Want(e.T, 200))
}

// sync runs the queued sync job of a source, as the worker would.
func (e *env) sync(id uuid.UUID) Source {
	e.T.Helper()
	jobs := e.Jobs("ai.knowledge_sync", e.WS.ID)
	found := false
	for _, raw := range jobs {
		var a struct {
			SourceID uuid.UUID `json:"source_id"`
		}
		_ = json.Unmarshal(raw, &a)
		found = found || a.SourceID == id
	}
	if !found {
		e.T.Fatalf("no sync job queued for %s", id)
	}
	if err := e.S.Sync(e.Ctx, e.WS.ID, id); err != nil {
		e.T.Fatal(err)
	}
	return e.get(id)
}

func (e *env) retrieve(wid uuid.UUID, q string) []ai.Passage {
	e.T.Helper()
	ps, err := e.S.Retrieve(e.Ctx, wid, q, 6)
	if err != nil {
		e.T.Fatal(err)
	}
	return ps
}

func (e *env) docs(id uuid.UUID) []Doc {
	e.T.Helper()
	var page httpx.Page[Doc]
	e.Do(http.MethodGet, "/v1/ai/knowledge/"+id.String()+"/docs?limit=100", e.viewer(), nil).Want(e.T, 200).JSON(e.T, &page)
	return page.Data
}

func TestSnippetLifecycle(t *testing.T) {
	e := newEnv(t, false)
	_, seq := e.Events(e.WS.ID, 0)
	src := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), map[string]any{
		"kind": "snippet", "snippet": map[string]any{"question": "How do I cancel my subscription?",
			"answer": "Open **Billing** and choose *Cancel plan*. See [billing](https://acme.test/billing)."}}).Want(t, 201))
	if src.Kind != KindSnippet || src.Status != StatusReady || src.DocCount != 1 || src.Name != "How do I cancel my subscription?" ||
		src.Snippet == nil || src.LastSyncedAt == nil || src.Website != nil {
		t.Fatalf("source %+v", src)
	}
	types, _ := e.Events(e.WS.ID, seq)
	if len(types) != 1 || types[0] != "ai_knowledge.created" {
		t.Fatalf("events %v", types)
	}
	var n int
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.knowledge_created'`, &n, e.WS.ID)
	if n != 1 {
		t.Fatalf("audit %d", n)
	}
	ps := e.retrieve(e.WS.ID, "cancel subscription")
	if len(ps) != 1 || ps[0].Kind != "snippet" || ps[0].URL != "" || ps[0].Title != "How do I cancel my subscription?" ||
		!strings.Contains(ps[0].Text, "Open Billing and choose") || strings.Contains(ps[0].Text, "**") {
		t.Fatalf("passages %+v", ps)
	}
	// Without embeddings, no sync job is needed for a snippet.
	if jobs := e.Jobs("ai.knowledge_sync", e.WS.ID); len(jobs) != 0 {
		t.Fatalf("jobs %v", jobs)
	}

	upd := e.source(e.Do(http.MethodPatch, "/v1/ai/knowledge/"+src.ID.String(), e.admin(), map[string]any{
		"snippet": map[string]any{"answer": "Write to billing@acme.test to stop your plan."}}).Want(t, 200))
	if upd.Snippet.Answer != "Write to billing@acme.test to stop your plan." {
		t.Fatalf("update %+v", upd)
	}
	if ps := e.retrieve(e.WS.ID, "cancel subscription plan"); len(ps) != 1 || !strings.Contains(ps[0].Text, "billing@acme.test") {
		t.Fatalf("after update %+v", ps)
	}
	// Turning a source off removes it from answers at once.
	e.Do(http.MethodPatch, "/v1/ai/knowledge/"+src.ID.String(), e.admin(), map[string]any{"enabled": false}).Want(t, 200)
	if ps := e.retrieve(e.WS.ID, "cancel subscription"); len(ps) != 0 {
		t.Fatalf("disabled source retrieved: %+v", ps)
	}
	e.Do(http.MethodPatch, "/v1/ai/knowledge/"+src.ID.String(), e.admin(), map[string]any{"website": map[string]any{"max_pages": 3}}).Want(t, 422)

	e.Do(http.MethodDelete, "/v1/ai/knowledge/"+src.ID.String(), e.admin(), nil).Want(t, 204)
	e.Do(http.MethodGet, "/v1/ai/knowledge/"+src.ID.String(), e.viewer(), nil).Want(t, 404)
	e.Scalar(`SELECT count(*) FROM ai_knowledge_docs WHERE source_id = $1`, &n, src.ID)
	if n != 0 {
		t.Fatalf("docs left: %d", n)
	}
	e.Scalar(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'ai.knowledge_deleted'`, &n, e.WS.ID)
	if n != 1 {
		t.Fatalf("delete audit %d", n)
	}
	types, _ = e.Events(e.WS.ID, seq)
	if types[len(types)-1] != "ai_knowledge.deleted" {
		t.Fatalf("events %v", types)
	}
}

func TestValidationAndRoles(t *testing.T) {
	e := newEnv(t, false)
	agent := e.WS.Agent.As(e.WS)
	body := map[string]any{"kind": "snippet", "snippet": map[string]any{"question": "What are your hours?", "answer": "9 to 5."}}
	e.Do(http.MethodPost, "/v1/ai/knowledge", agent, body).Want(t, 403)
	e.Do(http.MethodPost, "/v1/ai/knowledge", e.viewer(), body).Want(t, 403)
	e.Do(http.MethodGet, "/v1/ai/knowledge", agent, nil).Want(t, 200)

	readKey := e.APIKey(e.WS, auth.ScopeSettingsRead)
	e.Do(http.MethodGet, "/v1/ai/knowledge", hdtest.Auth{Bearer: readKey}, nil).Want(t, 200)
	e.Do(http.MethodPost, "/v1/ai/knowledge", hdtest.Auth{Bearer: readKey}, body).Want(t, 403)
	writeKey := e.APIKey(e.WS, auth.ScopeSettingsWrite)
	e.Do(http.MethodPost, "/v1/ai/knowledge", hdtest.Auth{Bearer: writeKey}, body).Want(t, 201)

	for _, bad := range []map[string]any{
		{"kind": "file"},
		{"kind": "snippet"},
		{"kind": "snippet", "snippet": map[string]any{"question": "Hi", "answer": "x"}},
		{"kind": "website", "website": map[string]any{"url": "ftp://acme.test"}},
		{"kind": "website", "website": map[string]any{"url": "https://10.0.0.5/docs"}},
		{"kind": "website", "website": map[string]any{"url": "https://acme.test", "max_pages": 501}},
		{"kind": "website", "website": map[string]any{"url": "https://acme.test", "include": []string{"/a b"}}},
		{"kind": "snippet", "name": strings.Repeat("n", 101), "snippet": map[string]any{"question": "What are your hours?", "answer": "9 to 5."}},
	} {
		if r := e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), bad); r.Status != 422 {
			t.Errorf("%v: status %d %s", bad, r.Status, r.Body)
		}
	}
	e.Do(http.MethodGet, "/v1/ai/knowledge?kind=nope", e.viewer(), nil).Want(t, 422)
}

func TestWebsiteSync(t *testing.T) {
	e := newEnv(t, true)
	ts := newTestSite(t)
	src := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), map[string]any{
		"kind": "website", "website": map[string]any{"url": ts.srv.URL + "/", "include": []string{"docs"}, "max_pages": 20}}).Want(t, 201))
	if src.Status != StatusPending || src.Website == nil || src.Website.Recrawl != "weekly" || src.Website.Include[0] != "/docs" {
		t.Fatalf("source %+v", src.Website)
	}
	// Not answerable before its first sync.
	if ps := e.retrieve(e.WS.ID, "resetting password"); len(ps) != 0 {
		t.Fatalf("passages before sync %+v", ps)
	}
	e.Do(http.MethodPost, "/v1/ai/knowledge/"+src.ID.String()+"/sync", e.admin(), nil).Want(t, 409)

	src = e.sync(src.ID)
	if src.Status != StatusReady || src.DocCount != 3 || src.LastSyncedAt == nil || src.Error != nil {
		t.Fatalf("after sync %+v", src)
	}
	docs := e.docs(src.ID)
	if len(docs) != 3 {
		t.Fatalf("docs %+v", docs)
	}
	ps := e.retrieve(e.WS.ID, "resetting password")
	if len(ps) == 0 || ps[0].Kind != "page" || ps[0].URL != ts.srv.URL+"/docs/a" || ps[0].Title != "Docs A" ||
		!strings.Contains(ps[0].Text, "Resetting your password") || ps[0].Score <= 0 {
		t.Fatalf("passages %+v", ps)
	}
	if e.S.vectors {
		var rows int
		e.Scalar(`SELECT count(*) FROM ai_embeddings WHERE workspace_id = $1 AND kind = 'source'`, &rows, e.WS.ID)
		if rows != 3 {
			t.Fatalf("embedding rows %d", rows)
		}
	}
	calls := e.Emb.embedCalls()

	// Remove a page through the API: it is excluded from later crawls.
	var docB Doc
	for _, d := range docs {
		if d.URL != nil && strings.HasSuffix(*d.URL, "/docs/b") {
			docB = d
		}
	}
	e.Do(http.MethodDelete, "/v1/ai/knowledge/"+src.ID.String()+"/docs/"+docB.ID.String(), e.admin(), nil).Want(t, 204)
	if got := e.get(src.ID); got.DocCount != 2 {
		t.Fatalf("doc count %d", got.DocCount)
	}
	// A page disappears from the site.
	ts.set("/docs/c", "")
	e.Do(http.MethodPost, "/v1/ai/knowledge/"+src.ID.String()+"/sync", e.admin(), nil).Want(t, 200)
	src = e.sync(src.ID)
	docs = e.docs(src.ID)
	if src.Status != StatusReady || len(docs) != 1 || *docs[0].URL != ts.srv.URL+"/docs/a" {
		t.Fatalf("after resync %+v %+v", src, docs)
	}
	// The unchanged page was not embedded again.
	if e.S.vectors && e.Emb.embedCalls() != calls {
		t.Fatalf("embedded %d times, want %d", e.Emb.embedCalls(), calls)
	}

	// Changing the crawl settings syncs again.
	upd := e.source(e.Do(http.MethodPatch, "/v1/ai/knowledge/"+src.ID.String(), e.admin(), map[string]any{
		"website": map[string]any{"include": []string{}, "exclude": []string{"/blog"}}}).Want(t, 200))
	if upd.Status != StatusPending {
		t.Fatalf("status %s", upd.Status)
	}
	src = e.sync(src.ID)
	// "/" and /docs/a: /docs/b was removed through the API, /docs/c is gone, /blog excluded.
	if src.Status != StatusReady || src.DocCount != 2 {
		t.Fatalf("after settings change %+v", src)
	}
	for _, d := range e.docs(src.ID) {
		if strings.Contains(*d.URL, "/blog") || strings.HasSuffix(*d.URL, "/docs/b") {
			t.Fatalf("excluded page stored: %s", *d.URL)
		}
	}
	// Recrawl only: no new sync.
	upd = e.source(e.Do(http.MethodPatch, "/v1/ai/knowledge/"+src.ID.String(), e.admin(), map[string]any{
		"website": map[string]any{"recrawl": "off"}}).Want(t, 200))
	if upd.Status != StatusReady || upd.Website.Recrawl != "off" {
		t.Fatalf("recrawl change %+v", upd)
	}
}

func TestWebsiteUnreachable(t *testing.T) {
	e := newEnv(t, false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	src := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), map[string]any{
		"kind": "website", "website": map[string]any{"url": srv.URL}}).Want(t, 201))
	srv.Close()
	src = e.sync(src.ID)
	if src.Status != StatusError || src.Error == nil || *src.Error != msgUnreachable || src.LastSyncedAt != nil {
		t.Fatalf("source %+v", src)
	}
}

func TestFileSources(t *testing.T) {
	e := newEnv(t, true)
	r := e.Upload("/v1/ai/knowledge/files", e.admin(), "Router manual.pdf", readFixture(t, "manual.pdf")).Want(t, 201)
	src := e.source(r)
	if src.Kind != KindFile || src.Status != StatusPending || src.File == nil || src.File.ContentType != ctPDF ||
		src.File.Filename != "Router manual.pdf" || src.Name != "Router manual.pdf" {
		t.Fatalf("source %+v %+v", src, src.File)
	}
	src = e.sync(src.ID)
	if src.Status != StatusReady || src.DocCount != 1 || src.CharCount == 0 {
		t.Fatalf("after sync %+v", src)
	}
	ps := e.retrieve(e.WS.ID, "reset button router")
	if len(ps) == 0 || ps[0].Kind != "file" || ps[0].URL != "" || ps[0].Title != "Router manual.pdf" {
		t.Fatalf("passages %+v", ps)
	}

	docx := e.source(e.Upload("/v1/ai/knowledge/files", e.admin(), "shipping.docx", readFixture(t, "shipping.docx")).Want(t, 201))
	if docx = e.sync(docx.ID); docx.Status != StatusReady {
		t.Fatalf("docx %+v", docx)
	}
	if ps := e.retrieve(e.WS.ID, "orders ship business days"); len(ps) == 0 || ps[0].ID == "" || ps[0].Title != "shipping.docx" {
		t.Fatalf("docx passages %+v", ps)
	}

	empty := e.source(e.Upload("/v1/ai/knowledge/files", e.admin(), "scan.pdf", readFixture(t, "empty.pdf")).Want(t, 201))
	empty = e.sync(empty.ID)
	if empty.Status != StatusError || empty.Error == nil || *empty.Error != "No text found in this file" {
		t.Fatalf("empty %+v", empty)
	}

	e.Upload("/v1/ai/knowledge/files", e.admin(), "setup.exe", []byte("MZ\x90\x00binary")).Want(t, 422)
	e.Upload("/v1/ai/knowledge/files", e.admin(), "notes.txt", []byte{}).Want(t, 422)
	e.Upload("/v1/ai/knowledge/files", e.WS.Agent.As(e.WS), "notes.txt", []byte("hello")).Want(t, 403)

	// Deleting the source removes documents, embeddings and the stored file.
	var key string
	e.Scalar(`SELECT storage_key FROM ai_knowledge_sources WHERE id = $1`, &key, src.ID)
	if rc, err := e.D.Storage.Get(e.Ctx, key); err != nil {
		t.Fatalf("stored file: %v", err)
	} else {
		rc.Close()
	}
	e.Do(http.MethodDelete, "/v1/ai/knowledge/"+src.ID.String(), e.admin(), nil).Want(t, 204)
	if _, err := e.D.Storage.Get(e.Ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("file not removed: %v", err)
	}
	var n int
	e.Scalar(`SELECT count(*) FROM ai_knowledge_docs WHERE source_id = $1`, &n, src.ID)
	if n != 0 {
		t.Fatalf("docs left %d", n)
	}
	if e.S.vectors {
		e.Scalar(`SELECT count(*) FROM ai_embeddings e WHERE e.workspace_id = $1 AND e.kind = 'source' AND NOT EXISTS
			(SELECT 1 FROM ai_knowledge_docs d WHERE d.id = e.ref_id)`, &n, e.WS.ID)
		if n != 0 {
			t.Fatalf("embeddings left %d", n)
		}
	}
	if ps := e.retrieve(e.WS.ID, "reset button router"); len(ps) != 0 {
		t.Fatalf("deleted source retrieved %+v", ps)
	}
}

func TestSemanticRetrieval(t *testing.T) {
	e := newEnv(t, true)
	if !e.S.vectors {
		t.Skip("the test database has no pgvector")
	}
	for _, sn := range [][2]string{
		{"What is your refund policy?", "Refunds are issued within 14 days of purchase."},
		{"Where do you ship?", "We ship to Europe and North America."},
	} {
		src := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), map[string]any{
			"kind": "snippet", "snippet": map[string]any{"question": sn[0], "answer": sn[1]}}).Want(t, 201))
		e.sync(src.ID)
	}
	// "money back" shares no keyword with the refund snippet; only embeddings find it.
	if fts, _ := e.S.ftsHits(e.Ctx, e.WS.ID, keywords("money back"), 6); len(fts) != 0 {
		t.Fatalf("keyword search matched: %+v", fts)
	}
	ps := e.retrieve(e.WS.ID, "money back")
	if len(ps) == 0 || ps[0].Title != "What is your refund policy?" || ps[0].Score < minSimilarity {
		t.Fatalf("passages %+v", ps)
	}
	// Both lists agree on the top result: fused first.
	ps = e.retrieve(e.WS.ID, "refund policy purchase")
	if len(ps) == 0 || ps[0].Title != "What is your refund policy?" {
		t.Fatalf("fused %+v", ps)
	}
	// An embedder outage falls back to full-text search.
	e.Emb.mu.Lock()
	e.Emb.fail = true
	e.Emb.mu.Unlock()
	e.S.queries = queryCache{}
	ps = e.retrieve(e.WS.ID, "ship europe")
	if len(ps) != 1 || ps[0].Title != "Where do you ship?" {
		t.Fatalf("fallback %+v", ps)
	}
}

func TestWorkspaceIsolation(t *testing.T) {
	e := newEnv(t, false)
	other := e.NewWorkspace()
	src := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), map[string]any{
		"kind": "snippet", "snippet": map[string]any{"question": "How do I export invoices?", "answer": "Use the export button."}}).Want(t, 201))
	oa := other.Admin.As(other)
	e.Do(http.MethodGet, "/v1/ai/knowledge/"+src.ID.String(), oa, nil).Want(t, 404)
	e.Do(http.MethodPatch, "/v1/ai/knowledge/"+src.ID.String(), oa, map[string]any{"enabled": false}).Want(t, 404)
	e.Do(http.MethodDelete, "/v1/ai/knowledge/"+src.ID.String(), oa, nil).Want(t, 404)
	e.Do(http.MethodPost, "/v1/ai/knowledge/"+src.ID.String()+"/sync", oa, nil).Want(t, 404)
	e.Do(http.MethodGet, "/v1/ai/knowledge/"+src.ID.String()+"/docs", oa, nil).Want(t, 404)
	docs := e.docs(src.ID)
	e.Do(http.MethodDelete, "/v1/ai/knowledge/"+src.ID.String()+"/docs/"+docs[0].ID.String(), oa, nil).Want(t, 404)
	var list sourceList
	e.Do(http.MethodGet, "/v1/ai/knowledge", oa, nil).Want(t, 200).JSON(t, &list)
	if len(list.Data) != 0 {
		t.Fatalf("other workspace sees %+v", list.Data)
	}
	if ps := e.retrieve(other.ID, "export invoices"); len(ps) != 0 {
		t.Fatalf("other workspace retrieved %+v", ps)
	}
	if ps := e.retrieve(e.WS.ID, "export invoices"); len(ps) != 1 {
		t.Fatalf("own workspace %+v", ps)
	}
}

func TestLimits(t *testing.T) {
	e := newEnv(t, false)
	snippetBody := map[string]any{"kind": "snippet", "snippet": map[string]any{"question": "What are your hours?", "answer": "9 to 5."}}
	for i := 0; i < maxSources-1; i++ {
		e.Exec(`INSERT INTO ai_knowledge_sources (workspace_id, kind, name, config) VALUES ($1, 'snippet', 'x', '{}')`, e.WS.ID)
	}
	e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), snippetBody).Want(t, 201)
	r := e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), snippetBody).Want(t, 422)
	if !strings.Contains(string(r.Body), "knowledge_limit") {
		t.Fatalf("body %s", r.Body)
	}
	e.Upload("/v1/ai/knowledge/files", e.admin(), "notes.txt", []byte("hello")).Want(t, 422)

	// Characters: a workspace at 20 million characters cannot add more.
	other := e.NewWorkspace()
	oa := other.Admin.As(other)
	big := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", oa, snippetBody).Want(t, 201))
	e.Exec(`UPDATE ai_knowledge_docs SET chars = $2 WHERE source_id = $1`, big.ID, maxChars)
	e.Do(http.MethodPost, "/v1/ai/knowledge", oa, snippetBody).Want(t, 422)

	// Documents: a crawl stops at the workspace's remaining document budget.
	third := e.NewWorkspace()
	ta := third.Admin.As(third)
	filler := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", ta, snippetBody).Want(t, 201))
	e.Exec(`INSERT INTO ai_knowledge_docs (workspace_id, source_id, key, title, content, chars, content_hash)
		SELECT $1, $2, 'k' || g, 't', 'c', 1, 'h' FROM generate_series(1, $3) g`, third.ID, filler.ID, maxDocs-3)
	ts := newTestSite(t)
	site := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", ta, map[string]any{
		"kind": "website", "website": map[string]any{"url": ts.srv.URL + "/"}}).Want(t, 201))
	if err := e.S.Sync(e.Ctx, third.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	var got Source
	e.Do(http.MethodGet, "/v1/ai/knowledge/"+site.ID.String(), ta, nil).Want(t, 200).JSON(t, &got)
	if got.DocCount != 2 || got.Status != StatusReady {
		t.Fatalf("site under the document limit %+v", got)
	}
	e.Do(http.MethodPost, "/v1/ai/knowledge", ta, snippetBody).Want(t, 422)
}

func TestMaintainQueuesRecrawls(t *testing.T) {
	e := newEnv(t, false)
	ts := newTestSite(t)
	src := e.source(e.Do(http.MethodPost, "/v1/ai/knowledge", e.admin(), map[string]any{
		"kind": "website", "website": map[string]any{"url": ts.srv.URL + "/", "max_pages": 2}}).Want(t, 201))
	e.sync(src.ID)
	if err := e.S.queueRecrawls(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if jobs := e.Jobs("ai.knowledge_sync", e.WS.ID); len(jobs) != 0 {
		t.Fatalf("recrawl queued too early: %v", jobs)
	}
	e.Exec(`UPDATE ai_knowledge_sources SET sync_started_at = now() - interval '8 days' WHERE id = $1`, src.ID)
	if err := e.S.queueRecrawls(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(src.ID); got.Status != StatusPending {
		t.Fatalf("status %s", got.Status)
	}
	if got := e.sync(src.ID); got.Status != StatusReady {
		t.Fatalf("recrawl %+v", got)
	}
	if err := e.S.backfill(e.Ctx); err != nil {
		t.Fatal(err)
	}
}
