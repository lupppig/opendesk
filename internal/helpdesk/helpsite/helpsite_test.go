package helpsite_test

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/helpsite"
)

type site struct {
	*hdtest.Env
	srv     *httptest.Server
	ws      hdtest.WS
	slug    string
	brandID uuid.UUID
	article uuid.UUID
}

const passed = "passed to the API"

// newSite seeds a help center with a topic, two articles and a draft, and serves the
// help site in front of a stand-in for the rest of the API.
func newSite(t *testing.T, configure func(s *site)) *site {
	e := hdtest.New(t, func(d *deps.Deps, _ huma.API, r chi.Router) { helpsite.Register(d, r) })
	s := &site{Env: e, ws: e.NewWorkspace()}
	e.Scalar(`SELECT id FROM brands WHERE workspace_id = $1 AND is_default`, &s.brandID, s.ws.ID)
	e.Scalar(`SELECT slug FROM brands WHERE id = $1`, &s.slug, s.brandID)
	e.Exec(`UPDATE brands SET help_center_enabled = true, help_center_title = 'Acme help' WHERE id = $1`, s.brandID)
	col := uuid.New()
	s.article = uuid.New()
	sfx := strings.ToLower(crypto.RandomString(6))
	e.Exec(`INSERT INTO kb_collections (id, workspace_id, name, slug, description) VALUES ($1, $2, 'Billing', $3, 'Invoices and payments')`,
		col, s.ws.ID, "billing-"+sfx)
	e.Exec(`INSERT INTO kb_articles (id, workspace_id, collection_id, title, slug, excerpt, body_html, status, published_at)
		VALUES ($1, $2, $3, 'Refund a payment', $4, 'How refunds work', $5, 'published', now())`, s.article, s.ws.ID, col, "refunds-"+sfx,
		`<p>Open <strong>Billing</strong>.</p><img src="/v1/attachments/0b4b6c1e-8f0e-4a43-9a7a-0d5f0a2c4e11?sig=x"><script>alert(1)</script>`)
	e.Exec(`INSERT INTO kb_articles (workspace_id, collection_id, title, slug, status, published_at) VALUES ($1, $2, 'Change your plan', $3, 'published', now())`,
		s.ws.ID, col, "plans-"+sfx)
	e.Exec(`INSERT INTO kb_articles (workspace_id, collection_id, title, slug, status) VALUES ($1, $2, 'Secret draft', $3, 'draft')`,
		s.ws.ID, col, "draft-"+sfx)
	cfg := e.D.Cfg
	saved := *cfg
	t.Cleanup(func() { *cfg = saved })
	if configure != nil {
		configure(s)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, passed) })
	s.srv = httptest.NewServer(helpsite.Middleware(e.D)(next))
	t.Cleanup(s.srv.Close)
	return s
}

type resp struct {
	status int
	header http.Header
	body   string
}

func (s *site) get(t *testing.T, host, path string, headers ...string) resp {
	t.Helper()
	return s.req(t, http.MethodGet, host, path, "", headers...)
}

func (s *site) req(t *testing.T, method, host, path, body string, headers ...string) resp {
	t.Helper()
	req, err := http.NewRequest(method, s.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, string(b)}
}

func (s *site) articleSlug(t *testing.T) string {
	var slug string
	s.Scalar(`SELECT slug FROM kb_articles WHERE id = $1`, &slug, s.article)
	return slug
}

func TestPathModeRendersTheHelpCenter(t *testing.T) {
	s := newSite(t, func(s *site) { s.D.Cfg.ServeWeb = true })
	base := "http://app.test/help/" + s.slug

	home := s.get(t, "app.test", "/help/"+s.slug)
	if home.status != 200 || !strings.Contains(home.body, "Acme help") || !strings.Contains(home.body, "How can we help?") ||
		!strings.Contains(home.body, "Billing") || !strings.Contains(home.body, "2 articles") || strings.Contains(home.body, "Secret draft") {
		t.Fatalf("home %d\n%s", home.status, home.body)
	}
	if !strings.Contains(home.body, `<link rel="canonical" href="`+base+`/">`) || !strings.Contains(home.body, `"@type":"WebSite"`) ||
		!strings.Contains(home.body, `hreflang="de" href="`+base+`/?lang=de"`) {
		t.Fatalf("home head\n%s", home.body)
	}
	csp := home.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe") {
		t.Fatalf("csp %q", csp)
	}
	if home.header.Get("Cache-Control") != "public, max-age=60" || home.header.Get("ETag") == "" {
		t.Fatalf("caching %v", home.header)
	}
	if again := s.get(t, "app.test", "/help/"+s.slug+"/", "If-None-Match", home.header.Get("ETag")); again.status != 304 {
		t.Fatalf("revalidation %d", again.status)
	}

	slug := s.articleSlug(t)
	art := s.get(t, "app.test", "/help/"+s.slug+"/a/"+slug)
	if art.status != 200 || !strings.Contains(art.body, "Refund a payment") || !strings.Contains(art.body, "<strong>Billing</strong>") ||
		strings.Contains(art.body, "alert(1)") || !strings.Contains(art.body, `src="http://api.test/v1/attachments/`) ||
		!strings.Contains(art.body, "Change your plan") || !strings.Contains(art.body, `"@type":"Article"`) ||
		!strings.Contains(art.body, `"@type":"BreadcrumbList"`) || !strings.Contains(art.body, `data-article="`+s.article.String()+`"`) {
		t.Fatalf("article\n%s", art.body)
	}
	if !strings.Contains(art.body, `<meta name="description" content="How refunds work">`) {
		t.Fatal("article description")
	}

	search := s.get(t, "app.test", "/help/"+s.slug+"/search?q=refund")
	if search.status != 200 || !strings.Contains(search.body, "1 result") || !strings.Contains(search.body, `content="noindex"`) {
		t.Fatalf("search\n%s", search.body)
	}
	if none := s.get(t, "app.test", "/help/"+s.slug+"/search?q=zzzzqqq"); !strings.Contains(html.UnescapeString(none.body), "No results for “zzzzqqq”") {
		t.Fatalf("no results\n%s", none.body)
	}

	for _, p := range []string{"/a/nope", "/c/nope", "/elsewhere", "/a/draft-x"} {
		if r := s.get(t, "app.test", "/help/"+s.slug+p); r.status != 404 || !strings.Contains(r.body, `content="noindex"`) {
			t.Fatalf("%s: %d", p, r.status)
		}
	}
	if r := s.get(t, "app.test", "/help/no-such-brand-"+s.slug); r.status != 404 || !strings.Contains(r.body, "Help center not found") {
		t.Fatalf("unknown help center %d", r.status)
	}
	if r := s.get(t, "app.test", "/v1/brands"); r.body != passed {
		t.Fatal("API paths must pass through")
	}

	sm := s.get(t, "app.test", "/help/"+s.slug+"/sitemap.xml")
	if sm.status != 200 || !strings.Contains(sm.body, "<loc>"+base+"/a/"+slug+"</loc>") || strings.Contains(sm.body, "draft-") ||
		!strings.Contains(sm.body, "<loc>"+base+"/c/billing-") {
		t.Fatalf("sitemap\n%s", sm.body)
	}
	if rb := s.get(t, "app.test", "/help/"+s.slug+"/robots.txt"); !strings.Contains(rb.body, "Sitemap: "+base+"/sitemap.xml") {
		t.Fatalf("robots\n%s", rb.body)
	}

	css := s.get(t, "app.test", "/help/"+s.slug+"/_help/help.css")
	v := regexp.MustCompile(`help\.css\?v=([0-9a-f]+)`).FindStringSubmatch(home.body)
	versioned := s.get(t, "app.test", "/help/"+s.slug+"/_help/help.css?v="+v[1])
	if css.status != 200 || !strings.HasPrefix(css.header.Get("Content-Type"), "text/css") ||
		versioned.header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("css %d %v", css.status, versioned.header)
	}

	s.Exec(`UPDATE brands SET help_center_enabled = false WHERE id = $1`, s.brandID)
	if r := s.get(t, "app.test", "/help/"+s.slug); r.status != 404 {
		t.Fatalf("a help center that is off: %d", r.status)
	}
}

func TestLanguages(t *testing.T) {
	s := newSite(t, func(s *site) { s.D.Cfg.ServeWeb = true })
	de := s.get(t, "app.test", "/help/"+s.slug+"/?lang=de")
	if !strings.Contains(de.body, `<html lang="de" dir="ltr"`) || !strings.Contains(de.body, "Wie können wir Ihnen helfen?") ||
		!strings.Contains(de.body, `<link rel="canonical" href="http://app.test/help/`+s.slug+`/?lang=de">`) ||
		!strings.Contains(de.body, `/c/billing-`) || !strings.Contains(de.body, `?lang=de"`) {
		t.Fatalf("german\n%s", de.body)
	}
	ar := s.get(t, "app.test", "/help/"+s.slug+"/?lang=ar")
	if !strings.Contains(ar.body, `<html lang="ar" dir="rtl"`) {
		t.Fatal("arabic is right to left")
	}
	// Azerbaijani dates are written with its month names, which browsers may not have.
	s.Exec(`UPDATE kb_articles SET published_at = '2024-10-22T12:00:00Z' WHERE id = $1`, s.article)
	az := s.get(t, "app.test", "/help/"+s.slug+"/?lang=az")
	if !strings.Contains(az.body, `data-format="short">22 okt 2024</time>`) {
		t.Fatalf("azerbaijani dates\n%s", az.body)
	}
	// The brand's language is the default, without a parameter.
	s.Exec(`UPDATE brands SET locale = 'fr' WHERE id = $1`, s.brandID)
	fr := s.get(t, "app.test", "/help/"+s.slug+"/")
	if !strings.Contains(fr.body, `<html lang="fr"`) || !strings.Contains(fr.body, `<link rel="canonical" href="http://app.test/help/`+s.slug+`/">`) ||
		!strings.Contains(fr.body, `hreflang="x-default" href="http://app.test/help/`+s.slug+`/"`) {
		t.Fatalf("french default\n%s", fr.body)
	}
	// A fixed language is the only one: no other in the list, the menu or a parameter.
	s.Exec(`UPDATE workspaces SET settings = settings || '{"locale_fixed": true}' WHERE id = $1`, s.ws.ID)
	only := s.get(t, "app.test", "/help/"+s.slug+"/?lang=de")
	if !strings.Contains(only.body, `<html lang="fr"`) || !strings.Contains(only.body, `data-langs="fr"`) ||
		strings.Contains(only.body, `hreflang="de"`) || strings.Contains(only.body, `class="lang"`) {
		t.Fatalf("fixed french\n%s", only.body)
	}
}

func TestHelpDomainHosts(t *testing.T) {
	s := newSite(t, func(s *site) {
		s.D.Cfg.ServeWeb = true
		s.D.Cfg.HelpDomain = "help.test"
	})
	host := s.slug + ".help.test"
	if r := s.get(t, host, "/"); r.status != 200 || !strings.Contains(r.body, `<link rel="canonical" href="http://`+host+`/">`) {
		t.Fatalf("subdomain %d\n%s", r.status, r.body)
	}
	if r := s.get(t, "app.test", "/help/"+s.slug+"/a/x?lang=de"); r.status != 301 || r.header.Get("Location") != "http://"+host+"/a/x?lang=de" {
		t.Fatalf("old path %d %q", r.status, r.header.Get("Location"))
	}
	if r := s.get(t, "nope-"+s.slug+".help.test", "/"); r.status != 404 {
		t.Fatalf("unknown slug %d", r.status)
	}
	if r := s.get(t, "deep."+host, "/"); r.body != passed {
		t.Fatal("only one label under the help domain names a help center")
	}

	// An active custom domain is the address; the others redirect to it.
	custom := "help-" + strings.ToLower(crypto.RandomString(6)) + ".example.com"
	s.Exec(`INSERT INTO help_domains (workspace_id, brand_id, hostname, token, status, verified_at) VALUES ($1, $2, $3, 't', 'active', now())`,
		s.ws.ID, s.brandID, custom)
	if r := s.get(t, host, "/c/x?lang=de"); r.status != 301 || r.header.Get("Location") != "https://"+custom+"/c/x?lang=de" {
		t.Fatalf("subdomain to custom %d %q", r.status, r.header.Get("Location"))
	}
	if r := s.get(t, "app.test", "/help/"+s.slug); r.status != 301 || r.header.Get("Location") != "https://"+custom+"/" {
		t.Fatalf("path to custom %d %q", r.status, r.header.Get("Location"))
	}
	r := s.get(t, custom, "/")
	if r.status != 200 || !strings.Contains(r.body, `<link rel="canonical" href="https://`+custom+`/">`) {
		t.Fatalf("custom %d\n%s", r.status, r.body)
	}
	if r := s.get(t, "other.example.com", "/"); r.body != passed {
		t.Fatal("an unknown host passes through")
	}

	// TLS on demand: only hosts with a help center.
	for h, want := range map[string]int{host: 200, custom: 200, "other.example.com": 404, "nope-" + s.slug + ".help.test": 404} {
		res, err := http.Get(s.Srv.URL + "/v1/public/help-domains/tls?domain=" + h)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("tls ask %s: %d, want %d", h, res.StatusCode, want)
		}
	}
}

func TestBehindTheEdgeProxy(t *testing.T) {
	secret := "proxy-secret-0123456789abcdef0123456789"
	s := newSite(t, func(s *site) {
		s.D.Cfg.HelpDomain = "help.test"
		s.D.Cfg.HelpProxySecret = secret
	})
	host := s.slug + ".help.test"
	if r := s.get(t, "api.test", "/", "OD-Help-Host", host, "OD-Help-Secret", secret); r.status != 200 || !strings.Contains(r.body, "Acme help") {
		t.Fatalf("proxied %d", r.status)
	}
	if r := s.get(t, "api.test", "/", "OD-Help-Host", host, "OD-Help-Secret", "wrong"+secret); r.body != passed {
		t.Fatal("a wrong secret is not the proxy")
	}
	if r := s.get(t, host, "/"); r.body != passed {
		t.Fatal("behind the proxy, the Host header names no help center")
	}

	// The page's script counts a view.
	slug := s.articleSlug(t)
	page := s.get(t, "api.test", "/a/"+slug, "OD-Help-Host", host, "OD-Help-Secret", secret)
	if !strings.Contains(page.body, `data-views="http://`+host+`/_help/views"`) {
		t.Fatalf("views url\n%s", page.body)
	}
	if r := s.req(t, http.MethodPost, "api.test", "/_help/views", s.article.String(), "OD-Help-Host", host, "OD-Help-Secret", secret,
		"OD-Help-Client-IP", "198.51.100.4"); r.status != 204 {
		t.Fatalf("view %d %s", r.status, r.body)
	}
	var views int
	s.Scalar(`SELECT views FROM kb_articles WHERE id = $1`, &views, s.article)
	if views != 1 {
		t.Fatalf("views %d", views)
	}
	if r := s.req(t, http.MethodPost, "api.test", "/a/"+slug, "", "OD-Help-Host", host, "OD-Help-Secret", secret); r.status != 405 {
		t.Fatalf("post to a page %d", r.status)
	}
}

func TestStructuredDataIsValidJSON(t *testing.T) {
	s := newSite(t, func(s *site) { s.D.Cfg.ServeWeb = true })
	s.Exec(`UPDATE kb_articles SET title = $2 WHERE id = $1`, s.article, `Refunds </script><script>alert(1)</script>`)
	r := s.get(t, "app.test", "/help/"+s.slug+"/a/"+s.articleSlug(t))
	m := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(r.body)
	if m == nil {
		t.Fatal("no structured data")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
		t.Fatalf("json-ld: %v\n%s", err, m[1])
	}
	if strings.Contains(r.body, "<script>alert(1)") {
		t.Fatal("a title broke out of the page")
	}
}

func TestChangelogPages(t *testing.T) {
	s := newSite(t, func(s *site) { s.D.Cfg.ServeWeb = true })
	root := "/help/" + s.slug
	base := "http://app.test" + root

	// Without a public entry the header has no link and the home page no updates.
	s.Exec(`INSERT INTO changelog_entries (workspace_id, title, slug, status, published_at) VALUES ($1, 'Not yet', 'not-yet', 'published', now() + interval '1 day')`, s.ws.ID)
	s.Exec(`INSERT INTO changelog_entries (workspace_id, title, slug, status) VALUES ($1, 'Draft update', 'draft-update', 'draft')`, s.ws.ID)
	if home := s.get(t, "app.test", root); strings.Contains(home.body, `class="hc-nav"`) || strings.Contains(home.body, "Latest updates") {
		t.Fatalf("home before any entry\n%s", home.body)
	}
	if r := s.get(t, "app.test", root+"/changelog/not-yet"); r.status != 404 || !strings.Contains(r.body, "Update not found") {
		t.Fatalf("scheduled entry: %d", r.status)
	}

	for i := 1; i <= 12; i++ {
		cats := "{}"
		if i%3 == 0 {
			cats = "{Fixed}"
		}
		s.Exec(`INSERT INTO changelog_entries (workspace_id, title, slug, body_html, categories, status, published_at)
			VALUES ($1, $2, $3, $4, $5, 'published', $6)`, s.ws.ID, hdtest.Fmt("Update %02d", i), hdtest.Fmt("update-%02d", i),
			`<p>Body <strong>bold</strong></p><img src="https://cdn.example.com/shot.png"><script>alert(1)</script>`, cats,
			time.Date(2026, 2, i, 9, 0, 0, 0, time.UTC))
	}
	home := s.get(t, "app.test", root)
	if !strings.Contains(home.body, `class="hc-nav" href="`+base+`/changelog"`) || !strings.Contains(home.body, "Latest updates") ||
		!strings.Contains(home.body, "Update 12") || strings.Contains(home.body, "Update 09") ||
		!strings.Contains(home.body, `type="application/atom+xml"`) {
		t.Fatalf("home\n%s", home.body)
	}

	cl := s.get(t, "app.test", root+"/changelog")
	if cl.status != 200 || !strings.Contains(cl.body, "Update 12") || !strings.Contains(cl.body, "Update 03") || strings.Contains(cl.body, "Update 02") ||
		strings.Contains(cl.body, "Draft update") || strings.Contains(cl.body, "Not yet") || strings.Contains(cl.body, "alert(1)") ||
		!strings.Contains(cl.body, `href="`+base+`/changelog?page=2" rel="next"`) || !strings.Contains(cl.body, `href="`+base+`/changelog?category=Fixed"`) {
		t.Fatalf("changelog\n%s", cl.body)
	}
	if p2 := s.get(t, "app.test", root+"/changelog?page=2"); !strings.Contains(p2.body, "Update 01") || !strings.Contains(p2.body, `rel="prev"`) ||
		!strings.Contains(p2.body, `<link rel="canonical" href="`+base+`/changelog?page=2">`) {
		t.Fatalf("page 2\n%s", p2.body)
	}
	if r := s.get(t, "app.test", root+"/changelog?page=9"); r.status != 404 {
		t.Fatalf("past the last page: %d", r.status)
	}
	fixed := s.get(t, "app.test", root+"/changelog?category=fixed")
	if !strings.Contains(fixed.body, "Update 12") || strings.Contains(fixed.body, "Update 11") || !strings.Contains(fixed.body, `content="noindex"`) {
		t.Fatalf("category\n%s", fixed.body)
	}
	if none := s.get(t, "app.test", root+"/changelog?category=nope"); !strings.Contains(html.UnescapeString(none.body), "No updates in “nope”") {
		t.Fatalf("empty category\n%s", none.body)
	}

	one := s.get(t, "app.test", root+"/changelog/update-05")
	if one.status != 200 || !strings.Contains(one.body, "<strong>bold</strong>") || !strings.Contains(one.body, `"@type":"BlogPosting"`) ||
		!strings.Contains(one.body, `<meta property="og:image" content="https://cdn.example.com/shot.png">`) ||
		!strings.Contains(one.body, "summary_large_image") || !strings.Contains(one.body, "Update 12") {
		t.Fatalf("entry\n%s", one.body)
	}

	feed := s.get(t, "app.test", root+"/changelog/feed.xml")
	if feed.status != 200 || !strings.HasPrefix(feed.header.Get("Content-Type"), "application/atom+xml") ||
		!strings.Contains(feed.body, "<id>"+base+"/changelog/update-12</id>") || !strings.Contains(feed.body, `<category term="Fixed">`) ||
		!strings.Contains(feed.body, "&lt;strong&gt;bold&lt;/strong&gt;") || strings.Contains(feed.body, "Draft update") {
		t.Fatalf("feed\n%s", feed.body)
	}

	sm := s.get(t, "app.test", root+"/sitemap.xml")
	if !strings.Contains(sm.body, "<loc>"+base+"/changelog</loc>") || !strings.Contains(sm.body, "<loc>"+base+"/changelog/update-07</loc>") ||
		strings.Contains(sm.body, "not-yet") {
		t.Fatalf("sitemap\n%s", sm.body)
	}

	m := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(one.body)
	var v map[string]any
	if m == nil || json.Unmarshal([]byte(m[1]), &v) != nil {
		t.Fatal("entry structured data")
	}
}
