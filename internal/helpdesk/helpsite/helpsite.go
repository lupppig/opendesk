// Package helpsite renders the public help centers (contracts/teams.md "Help center
// addresses"): on <slug>.<HELP_DOMAIN>, on a brand's active custom domain, and at
// HelpPathBase()/help/<slug> without HELP_DOMAIN. Pages are server-rendered HTML so search
// engines and link previews read them, with a little script for the theme, dates and
// view counts.
package helpsite

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/xml"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/helpdesk/kb"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Budgets per client address and minute.
const (
	pagesPerMinute  = 300
	searchPerMinute = 60
	viewsPerMinute  = 120
	featuredLimit   = 6
	moreLimit       = 5
)

// Headers the edge proxy in front of help centers sets (deploy/cloudflare/help-worker).
const (
	headerHost   = "OD-Help-Host"
	headerIP     = "OD-Help-Client-IP"
	headerSecret = "OD-Help-Secret"
)

// Server serves help center sites.
type Server struct {
	d        *deps.Deps
	clientIP func(*http.Request) string
	own      map[string]bool
	scheme   string
	csp      string
}

// New returns the server for the instance's configuration.
func New(d *deps.Deps) *Server {
	cfg := d.Cfg
	s := &Server{d: d, clientIP: httpx.ClientIP(cfg.TrustProxy, cfg.ProxyHops), own: map[string]bool{}, scheme: "https"}
	if strings.HasPrefix(cfg.AppURL, "http://") {
		s.scheme = "http"
	}
	img := []string{"'self'", "https:", "data:"}
	for _, raw := range []string{cfg.AppURL, cfg.APIURL, cfg.GatewayURL, cfg.SiteURL} {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			s.own[strings.ToLower(u.Hostname())] = true
		}
	}
	if u, err := url.Parse(cfg.APIURL); err == nil && u.Scheme == "http" {
		img = append(img, "http://"+u.Host)
	}
	s.csp = strings.Join([]string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "img-src " + strings.Join(img, " "),
		"font-src 'self'", "connect-src 'self'", "manifest-src 'self'", "form-action 'self'", "base-uri 'none'", "frame-ancestors 'none'"}, "; ")
	return s
}

// Register adds the route a proxy that issues certificates on demand asks.
func Register(d *deps.Deps, r chi.Router) {
	s := New(d)
	r.Get("/v1/public/help-domains/tls", s.tlsAsk)
}

// Middleware answers help center requests and passes every other request on.
func Middleware(d *deps.Deps) func(http.Handler) http.Handler {
	s := New(d)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.serve(w, r) {
				next.ServeHTTP(w, r)
			}
		})
	}
}

func normHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

// helpSlug is the brand slug of <slug>.<HELP_DOMAIN>, or "".
func (s *Server) helpSlug(host string) string {
	hd := s.d.Cfg.HelpDomain
	if hd == "" {
		return ""
	}
	label, ok := strings.CutSuffix(host, "."+hd)
	if !ok || label == "" || strings.Contains(label, ".") {
		return ""
	}
	return label
}

// serve answers a help center request; false when the request is not one.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.d.Cfg
	if cfg.HelpProxySecret != "" {
		// Behind the edge proxy, only it names help center hosts.
		got := r.Header.Get(headerSecret)
		if got == "" {
			return s.servePathMode(w, r)
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(cfg.HelpProxySecret)) != 1 {
			return s.servePathMode(w, r)
		}
		ip := r.Header.Get(headerIP)
		if net.ParseIP(ip) == nil {
			ip = s.clientIP(r)
		}
		s.serveHost(w, r, normHost(r.Header.Get(headerHost)), ip)
		return true
	}
	host := normHost(r.Host)
	switch {
	case s.own[host]:
		return s.servePathMode(w, r)
	case s.helpSlug(host) != "":
		s.serveHost(w, r, host, s.clientIP(r))
		return true
	case strings.Contains(host, ".") && net.ParseIP(host) == nil && !strings.HasSuffix(host, ".localhost"):
		hc, err := kb.HelpCenterByHost(r.Context(), s.d.DB.Pool, host)
		if err != nil {
			if !db.IsNotFound(err) {
				s.d.Log.Error("help center host", "err", err)
			}
			return false
		}
		s.serveSite(w, r, hc, "https://"+host, r.URL.Path, s.clientIP(r))
		return true
	}
	return false
}

// servePathMode answers <HelpPathBase>/help/<slug>/... on the instance's own hosts.
func (s *Server) servePathMode(w http.ResponseWriter, r *http.Request) bool {
	cfg := s.d.Cfg
	rest, ok := strings.CutPrefix(r.URL.Path, "/help/")
	if !ok {
		return false
	}
	slug, sub, _ := strings.Cut(rest, "/")
	sub = "/" + sub
	base := cfg.HelpPathBase() + "/help/" + slug
	if s.staticAsset(w, r, sub) {
		return true
	}
	ip := s.clientIP(r)
	hc, err := kb.HelpCenterBySlug(r.Context(), s.d.DB.Pool, slug)
	if err != nil || !hc.Enabled {
		s.missing(w, r, err, base, ip)
		return true
	}
	switch {
	case hc.ActiveHost != "":
		s.redirect(w, r, "https://"+hc.ActiveHost+sub)
	case cfg.HelpDomain != "":
		s.redirect(w, r, cfg.HelpCenterURL(hc.Slug)+sub)
	default:
		s.serveSite(w, r, hc, base, sub, ip)
	}
	return true
}

// serveHost answers a request for a help center host: <slug>.<HELP_DOMAIN> or a custom domain.
func (s *Server) serveHost(w http.ResponseWriter, r *http.Request, host, ip string) {
	if s.staticAsset(w, r, r.URL.Path) {
		return
	}
	var hc kb.HelpCenter
	var err error
	scheme := s.scheme
	if slug := s.helpSlug(host); slug != "" {
		hc, err = kb.HelpCenterBySlug(r.Context(), s.d.DB.Pool, slug)
	} else {
		hc, err = kb.HelpCenterByHost(r.Context(), s.d.DB.Pool, host)
		scheme = "https"
	}
	if err != nil || !hc.Enabled {
		s.missing(w, r, err, scheme+"://"+host, ip)
		return
	}
	if hc.ActiveHost != "" && hc.ActiveHost != host {
		s.redirect(w, r, "https://"+hc.ActiveHost+r.URL.Path)
		return
	}
	s.serveSite(w, r, hc, scheme+"://"+host, r.URL.Path, ip)
}

// redirect answers 301 to target, keeping the query.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, target string) {
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// staticAsset answers /_help/<file> on any help center host, even one with no help
// center, so its 404 page is styled too. The view counter is not a file.
func (s *Server) staticAsset(w http.ResponseWriter, r *http.Request, sub string) bool {
	name, ok := strings.CutPrefix(sub, "/_help/")
	if !ok || name == "views" {
		return false
	}
	s.asset(w, r, name)
	return true
}

// missing answers a help center that does not exist or is off.
func (s *Server) missing(w http.ResponseWriter, r *http.Request, err error, base, ip string) {
	if err != nil && !db.IsNotFound(err) {
		s.d.Log.Error("help center", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	if !s.allow(w, "help-site:"+ip, pagesPerMinute) {
		return
	}
	tr := translator{lang: "en"}
	if l := matchLocale(r.URL.Query().Get("lang")); l != "" {
		tr.lang = l
	}
	p := &page{Kind: "notfound", Lang: tr.lang, Dir: dir(tr.lang), T: tr, Year: time.Now().Year(), NoIndex: true,
		DocTitle: tr.T("help.not_found_title"), OGTitle: tr.T("help.not_found_title"), OGType: "website",
		NotFound: &notFoundData{Title: tr.T("help.not_found_title"), Body: tr.T("help.not_found_body")},
		Site:     siteInfo{Title: tr.T("help.default_title"), Icon: glyphIcon("?"), Tone: "t-gray", Initial: "?"},
		base:     base, path: "/", defLang: "en"}
	s.languages(p)
	s.write(w, r, p, http.StatusNotFound)
}

// allow spends one request of a budget; false (after answering 429) when it is spent.
func (s *Server) allow(w http.ResponseWriter, key string, perMinute int) bool {
	if s.d.Limiter.Allow(key, perMinute, time.Minute) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "Too many requests. Please wait a minute.", http.StatusTooManyRequests)
	return false
}

// serveSite routes a path of one help center.
func (s *Server) serveSite(w http.ResponseWriter, r *http.Request, hc kb.HelpCenter, base, sub, ip string) {
	if sub == "/_help/views" {
		s.view(w, r, hc, ip)
		return
	}
	if s.staticAsset(w, r, sub) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	if !s.allow(w, "help-site:"+ip, pagesPerMinute) {
		return
	}
	ctx := r.Context()
	p := s.newPage(r, hc, base, sub)
	var err error
	status := http.StatusOK
	switch {
	case sub == "/" || sub == "":
		p.path = "/"
		err = s.home(ctx, p, hc)
	case sub == "/search":
		if !s.allow(w, "help-search:"+ip, searchPerMinute) {
			return
		}
		err = s.search(ctx, p, hc, r.URL.Query().Get("q"))
	case strings.HasPrefix(sub, "/c/") && !strings.Contains(sub[3:], "/"):
		err = s.collection(ctx, p, hc, sub[3:])
	case strings.HasPrefix(sub, "/a/") && !strings.Contains(sub[3:], "/"):
		err = s.article(ctx, p, hc, sub[3:])
	case sub == "/sitemap.xml":
		s.sitemap(w, r, hc, base)
		return
	case sub == "/robots.txt":
		s.robots(w, r, base)
		return
	default:
		err = errNotFound
	}
	if err == errNotFound || db.IsNotFound(err) {
		s.notFoundPage(p, sub)
		status, err = http.StatusNotFound, nil
	}
	if err != nil {
		s.d.Log.Error("help center page", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	s.write(w, r, p, status)
}

type notFound struct{}

func (notFound) Error() string { return "not found" }

var errNotFound error = notFound{}

// write renders a page with its caching and security headers.
func (s *Server) write(w http.ResponseWriter, r *http.Request, p *page, status int) {
	body, err := render(p)
	if err != nil {
		s.d.Log.Error("help center render", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", s.csp)
	h.Set("Content-Language", p.Lang)
	h.Set("Cache-Control", "public, max-age=60")
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	h.Set("ETag", etag)
	if status == http.StatusOK && r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// asset serves a static file; a versioned URL is cached for good.
func (s *Server) asset(w http.ResponseWriter, r *http.Request, name string) {
	a, ok := assets[name]
	if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("Access-Control-Allow-Origin", "*")
	if r.URL.Query().Get("v") == a.version {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "public, max-age=3600")
	}
	h.Set("ETag", `"`+a.version+`"`)
	if r.Header.Get("If-None-Match") == `"`+a.version+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write(a.body)
	}
}

// view counts an article view sent by the page's script.
func (s *Server) view(w http.ResponseWriter, r *http.Request, hc kb.HelpCenter, ip string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !s.allow(w, "help-views:"+ip, viewsPerMinute) {
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 128))
	id, err := uuid.Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		http.Error(w, "Expected an article id.", http.StatusBadRequest)
		return
	}
	if _, err := kb.CountView(r.Context(), s.d.DB.Pool, hc, id); err != nil {
		s.d.Log.Error("help center view", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// newPage starts a page of the help center in the reader's language.
func (s *Server) newPage(r *http.Request, hc kb.HelpCenter, base, sub string) *page {
	def := matchLocale(hc.Locale)
	if def == "" {
		def = "en"
	}
	explicit := matchLocale(r.URL.Query().Get("lang"))
	lang := def
	if explicit != "" {
		lang = explicit
	}
	p := &page{Lang: lang, Dir: dir(lang), T: translator{lang: lang}, Year: time.Now().Year(), OGType: "website",
		base: base, path: sub, explicit: explicit, defLang: def}
	if explicit != "" && explicit != def {
		p.langQ = "?lang=" + url.QueryEscape(explicit)
	}
	p.OGLocale = strings.ReplaceAll(lang, "-", "_")
	p.Site = siteInfo{Title: hc.Title, Tone: "t-" + toneFor(hc.Title), Initial: initial(hc.Title)}
	if u := hc.Logo.URL(); u != "" {
		p.Site.Logo, p.Site.Image = u, u
	}
	switch {
	case hc.Favicon.URL() != "":
		p.Site.Icon = template.URL(hc.Favicon.URL())
	case p.Site.Logo != "":
		p.Site.Icon = template.URL(p.Site.Logo)
	default:
		p.Site.Icon = glyphIcon(hc.Title)
	}
	p.ViewsURL = base + "/_help/views"
	s.languages(p)
	return p
}

// languages fills the language picker and the page's alternates.
func (s *Server) languages(p *page) {
	p.LangName = localeNames[p.Lang]
	p.LangList = strings.Join(supported, " ")
	p.Languages = make([]language, 0, len(supported))
	p.Alternates = make([]alternate, 0, len(supported))
	for _, l := range supported {
		u := p.langURL(l)
		p.Languages = append(p.Languages, language{Lang: l, Name: localeNames[l], Dir: dir(l), URL: u, Current: l == p.Lang})
		p.Alternates = append(p.Alternates, alternate{Lang: l, URL: u})
	}
	p.Canonical = p.langURL(p.Lang)
	p.XDefault = p.langURL(p.defLang)
}

// finish sets a page's titles, description and structured data.
func (p *page) finish(title, description string, ld any) {
	p.OGTitle = title
	p.DocTitle = title
	if title != p.Site.Title {
		p.DocTitle = title + " · " + p.Site.Title
	}
	p.Description = excerpt(description, 160)
	p.Canonical = p.langURL(p.Lang)
	p.XDefault = p.langURL(p.defLang)
	if ld != nil {
		p.JSONLD = jsonLD(ld)
	}
}

func (s *Server) home(ctx context.Context, p *page, hc kb.HelpCenter) error {
	cols, err := kb.Collections(ctx, s.d.DB.Pool, hc)
	if err != nil {
		return err
	}
	featured, err := kb.Featured(ctx, s.d.DB.Pool, hc, featuredLimit)
	if err != nil {
		return err
	}
	p.Kind = "home"
	p.Home = &homeData{Collections: cols, Featured: featured}
	s.languages(p)
	p.finish(p.Site.Title, p.T.T("help.hero_subtitle"), map[string]any{
		"@context": "https://schema.org", "@type": "WebSite", "name": p.Site.Title, "url": p.base + "/", "inLanguage": p.Lang,
		"potentialAction": map[string]any{"@type": "SearchAction", "target": p.base + "/search?q={search_term_string}",
			"query-input": "required name=search_term_string"},
	})
	return nil
}

func (s *Server) collection(ctx context.Context, p *page, hc kb.HelpCenter, slug string) error {
	c, arts, err := kb.CollectionBySlug(ctx, s.d.DB.Pool, hc, slug)
	if err != nil {
		return err
	}
	p.Kind = "collection"
	p.path = "/c/" + c.Slug
	p.Collection = &collectionData{Collection: c, Articles: arts}
	s.languages(p)
	desc := c.Description
	if desc == "" {
		desc = p.T.N("help.articles", len(arts))
	}
	p.finish(c.Name, desc, map[string]any{"@context": "https://schema.org", "@type": "BreadcrumbList",
		"itemListElement": breadcrumbs(p, crumb{c.Name, p.base + p.path})})
	return nil
}

func (s *Server) article(ctx context.Context, p *page, hc kb.HelpCenter, slug string) error {
	a, err := kb.ArticleBySlug(ctx, s.d.DB.Pool, hc, slug)
	if err != nil {
		return err
	}
	p.Kind = "article"
	p.path = "/a/" + a.Slug
	p.Article = &a
	p.OGType = "article"
	p.Body = articleBody(a.BodyHTML, strings.TrimRight(s.d.Cfg.APIURL, "/"))
	trail := []crumb{}
	if a.CollectionID != nil {
		cols, err := kb.Collections(ctx, s.d.DB.Pool, hc)
		if err != nil {
			return err
		}
		for i := range cols {
			if cols[i].ID == *a.CollectionID {
				p.ArticleCollection = &cols[i]
			}
		}
		if p.ArticleCollection != nil {
			_, siblings, err := kb.CollectionBySlug(ctx, s.d.DB.Pool, hc, p.ArticleCollection.Slug)
			if err != nil {
				return err
			}
			for _, x := range siblings {
				if x.ID != a.ID && len(p.More) < moreLimit {
					p.More = append(p.More, x)
				}
			}
			trail = append(trail, crumb{p.ArticleCollection.Name, p.base + "/c/" + p.ArticleCollection.Slug})
		}
	}
	s.languages(p)
	desc := a.Excerpt
	if desc == "" {
		desc = excerpt(stripTags(a.BodyHTML), 200)
	}
	art := map[string]any{"@type": "Article", "headline": a.Title, "description": excerpt(desc, 160), "dateModified": p.ISO(a.UpdatedAt),
		"mainEntityOfPage": p.base + p.path, "inLanguage": p.defLang,
		"publisher": map[string]any{"@type": "Organization", "name": p.Site.Title}}
	if a.PublishedAt != nil {
		art["datePublished"] = p.ISO(*a.PublishedAt)
	}
	if a.Author != nil && a.Author.Name != "" {
		art["author"] = map[string]any{"@type": "Person", "name": a.Author.Name}
	}
	if p.Site.Logo != "" {
		art["publisher"].(map[string]any)["logo"] = map[string]any{"@type": "ImageObject", "url": p.Site.Logo}
	}
	trail = append(trail, crumb{a.Title, p.base + p.path})
	p.finish(a.Title, desc, map[string]any{"@context": "https://schema.org", "@graph": []any{art,
		map[string]any{"@type": "BreadcrumbList", "itemListElement": breadcrumbs(p, trail...)}}})
	return nil
}

func (s *Server) search(ctx context.Context, p *page, hc kb.HelpCenter, q string) error {
	q = strings.TrimSpace(q)
	if len(q) > 200 {
		q = string([]rune(q)[:200])
	}
	p.Kind = "search"
	p.path = "/search"
	p.Query = q
	p.NoIndex = true
	if p.QueryLen() >= 2 {
		res, err := kb.SearchHelpCenter(ctx, s.d.DB.Pool, hc, q)
		if err != nil {
			return err
		}
		p.Results = res
	}
	s.languages(p)
	p.finish(p.T.T("help.search"), "", nil)
	return nil
}

// notFoundPage turns p into the help center's 404 page for a path.
func (s *Server) notFoundPage(p *page, sub string) {
	p.Kind = "notfound"
	p.NoIndex = true
	p.inSite = true
	title, body := p.T.T("help.not_found_title"), p.T.T("help.not_found_body")
	switch {
	case strings.HasPrefix(sub, "/a/"):
		title, body = p.T.T("help.article_not_found_title"), p.T.T("help.article_not_found_body")
	case strings.HasPrefix(sub, "/c/"):
		title, body = p.T.T("help.topic_not_found_title"), p.T.T("help.topic_not_found_body")
	}
	p.NotFound = &notFoundData{Title: title, Body: body}
	p.Home, p.Collection, p.Article, p.More = nil, nil, nil, nil
	p.finish(title, "", nil)
}

func breadcrumbs(p *page, trail ...crumb) []any {
	out := []any{map[string]any{"@type": "ListItem", "position": 1, "name": p.Site.Title, "item": p.base + "/"}}
	for i, c := range trail {
		out = append(out, map[string]any{"@type": "ListItem", "position": i + 2, "name": c.Label, "item": c.Href})
	}
	return out
}

func stripTags(html string) string {
	var b strings.Builder
	in := false
	for _, r := range html {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// robots lets every crawler in, away from search results, and names the sitemap.
func (s *Server) robots(w http.ResponseWriter, r *http.Request, base string) {
	u, _ := url.Parse(base)
	prefix := ""
	if u != nil {
		prefix = strings.TrimRight(u.Path, "/")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, "User-agent: *\nAllow: /\nDisallow: "+prefix+"/search\n\nSitemap: "+base+"/sitemap.xml\n")
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// sitemap lists the help center's home, topics and articles.
func (s *Server) sitemap(w http.ResponseWriter, r *http.Request, hc kb.HelpCenter, base string) {
	entries, err := kb.Sitemap(r.Context(), s.d.DB.Pool, hc)
	if err != nil {
		s.d.Log.Error("help center sitemap", "err", err)
		http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
		return
	}
	urls := []sitemapURL{{Loc: base + "/"}}
	var newest time.Time
	for _, e := range entries {
		seg := "/a/"
		if e.Kind == "collection" {
			seg = "/c/"
		}
		urls = append(urls, sitemapURL{Loc: base + seg + url.PathEscape(e.Slug), LastMod: e.UpdatedAt.UTC().Format("2006-01-02")})
		if e.UpdatedAt.After(newest) {
			newest = e.UpdatedAt
		}
	}
	if !newest.IsZero() {
		urls[0].LastMod = newest.UTC().Format("2006-01-02")
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=600")
	_, _ = io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	_ = enc.Encode(struct {
		XMLName xml.Name     `xml:"urlset"`
		NS      string       `xml:"xmlns,attr"`
		URLs    []sitemapURL `xml:"url"`
	}{NS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls})
}

// tlsAsk answers 200 for a hostname this instance serves a help center on, so a proxy
// issues its certificate on demand; 404 otherwise.
func (s *Server) tlsAsk(w http.ResponseWriter, r *http.Request) {
	host := normHost(r.URL.Query().Get("domain"))
	if !s.d.Limiter.Allow("help-tls:"+s.clientIP(r), 600, time.Minute) {
		http.Error(w, "Too many requests.", http.StatusTooManyRequests)
		return
	}
	var hc kb.HelpCenter
	var err error
	if slug := s.helpSlug(host); slug != "" {
		hc, err = kb.HelpCenterBySlug(r.Context(), s.d.DB.Pool, slug)
	} else if host != "" {
		hc, err = kb.HelpCenterByHost(r.Context(), s.d.DB.Pool, host)
	} else {
		err = errNotFound
	}
	if err != nil || !hc.Enabled {
		if err != nil && err != errNotFound && !db.IsNotFound(err) {
			s.d.Log.Error("help center tls ask", "err", err)
		}
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}
