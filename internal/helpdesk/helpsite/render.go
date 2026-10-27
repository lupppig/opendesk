package helpsite

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/kb"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets
var assetFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{"lower": strings.ToLower}).ParseFS(templateFS, "templates/*.html"))

// asset is a static file under /_help/, with a content hash its URLs carry so they can
// be cached for good.
type asset struct {
	body    []byte
	ctype   string
	version string
}

var assets = func() map[string]asset {
	out := map[string]asset{}
	err := fs.WalkDir(assetFS, "assets", func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := assetFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		ctype := map[string]string{".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8",
			".woff2": "font/woff2", ".txt": "text/plain; charset=utf-8"}[path.Ext(p)]
		out[path.Base(p)] = asset{body: b, ctype: ctype, version: hex.EncodeToString(sum[:])[:10]}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return out
}()

// identityTones and toneSolids follow toneFor in web/src/lib/tones.ts and the light theme.
var identityTones = []string{"blue", "indigo", "violet", "pink", "orange", "amber", "lime", "green", "teal", "cyan"}

var toneSolids = map[string]string{"blue": "#2b6cf0", "indigo": "#4c5fd5", "violet": "#7048e8", "pink": "#d6336c", "orange": "#e8590c",
	"amber": "#d48806", "lime": "#5c940d", "green": "#12a150", "teal": "#0c9488", "cyan": "#0c8599"}

// toneFor is the stable hue of a name, as the web app picks it.
func toneFor(name string) string {
	if name == "" {
		name = "?"
	}
	var h int32
	for _, c := range utf16.Encode([]rune(name)) {
		h = h*31 + int32(c)
	}
	n := int64(h)
	if n < 0 {
		n = -n
	}
	return identityTones[n%int64(len(identityTones))]
}

func initial(name string) string {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(name))
	if r == utf8.RuneError {
		return "?"
	}
	return string(unicode.ToUpper(r))
}

// glyphIcon is the help center's letter on its hue, as an SVG favicon.
func glyphIcon(name string) template.URL {
	var b strings.Builder
	template.HTMLEscape(&b, []byte(initial(name)))
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="7" fill="` + toneSolids[toneFor(name)] +
		`"/><text x="16" y="22" font-family="system-ui,sans-serif" font-size="17" font-weight="600" fill="#fff" text-anchor="middle">` + b.String() + `</text></svg>`
	return template.URL("data:image/svg+xml," + url.PathEscape(svg))
}

// siteInfo is what every page shows of the help center.
type siteInfo struct {
	Title   string
	Logo    string // the logo's URL, or ""
	Image   string // the link preview picture, or ""
	Icon    template.URL
	Tone    string // the glyph's hue class
	Initial string
	// Changelog: the help center has a public changelog entry, so the header links it.
	Changelog bool
}

type alternate struct{ Lang, URL string }

type language struct {
	Lang, Name, Dir, URL string
	Current              bool
}

type homeData struct {
	Collections []kb.KbCollection
	Featured    []kb.KbArticle
	Latest      []kb.ChangelogEntry
}

type collectionData struct {
	Collection kb.KbCollection
	Articles   []kb.KbArticle
}

type notFoundData struct{ Title, Body string }

// page is one rendered help center page.
type page struct {
	Kind      string // home | collection | article | search | changelog | entry | notfound
	Lang, Dir string
	T         translator
	Site      siteInfo
	Year      int

	DocTitle, OGTitle, Description string
	Canonical, XDefault            string
	Alternates                     []alternate
	OGType, OGLocale               string
	LargeCard                      bool // the link preview shows Site.Image large
	NoIndex                        bool
	JSONLD                         template.JS
	Languages                      []language
	LangName, LangList             string
	ViewsURL                       string

	Home              *homeData
	Collection        *collectionData
	Article           *kb.KbArticle
	ArticleCollection *kb.KbCollection
	More              []kb.KbArticle
	Body              template.HTML
	Query             string
	Results           []kb.KbArticle
	NotFound          *notFoundData
	Changelog         *changelogData
	Entry             *entryView
	MoreEntries       []kb.ChangelogEntry

	base     string // the help center's root URL, without a trailing slash
	path     string // this page's path under base
	langQ    string // "?lang=xx" when the reader chose a language other than the default
	fixed    bool   // the help center speaks only its default language
	explicit string
	defLang  string
	inSite   bool // a page of an existing help center (its 404 links home)
}

// Link is a URL of the help center, keeping the reader's language choice.
func (p *page) Link(sub string) string { return p.base + sub + p.langQ }

// Asset is the URL of a static file, versioned by its content.
func (p *page) Asset(name string) string {
	return p.base + "/_help/" + name + "?v=" + assets[name].version
}

// langURL is this page in another language: the default language has no parameter.
func (p *page) langURL(lang string) string {
	u := p.base + p.path
	q := url.Values{}
	if p.Kind == "search" && p.Query != "" {
		q.Set("q", p.Query)
	}
	if c := p.Changelog; p.Kind == "changelog" && c != nil {
		if c.Category != "" {
			q.Set("category", c.Category)
		}
		if c.Page > 1 {
			q.Set("page", strconv.Itoa(c.Page))
		}
	}
	if lang != p.defLang {
		q.Set("lang", lang)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

type searchBox struct {
	Action, Value, Placeholder, Lang string
	Large, Autofocus                 bool
}

// SearchBox is the search form; the large one leads the home and search pages.
func (p *page) SearchBox(large bool) searchBox {
	lang := ""
	if p.langQ != "" {
		lang = p.Lang
	}
	return searchBox{Action: p.base + "/search", Value: p.Query, Placeholder: p.T.T("help.search_placeholder"), Lang: lang,
		Large: large, Autofocus: large && p.Kind == "home"}
}

type rowsData struct {
	Page     *page
	Articles []kb.KbArticle
	Compact  bool
}

// Delay staggers a list's entrance.
func (r rowsData) Delay(i int) string { return delay(i) }

// Rows is an article list.
func (p *page) Rows(list []kb.KbArticle, compact bool) rowsData {
	return rowsData{Page: p, Articles: list, Compact: compact}
}

// Delay staggers a grid's entrance.
func (p *page) Delay(i int) string { return delay(i) }

func delay(i int) string { return fmt.Sprintf("d%d", min(i, 8)) }

// ToneFor is the hue class of a name.
func (p *page) ToneFor(name string) string { return "t-" + toneFor(name) }

type emptyData struct {
	Cells       []int
	Lit         int
	Tone        string
	Icon        template.HTML
	Title, Body string
	Compact     bool
	Action      string
	ActionHref  string
}

// emptyArt are the empty state's lit tiles (EmptyState in the web app): the hue and icon
// for each.
var emptyArt = []struct{ tone, icon string }{
	{"indigo", `<circle cx="12" cy="8" r="5"/><path d="M20 21a8 8 0 0 0-16 0"/>`},
	{"green", `<circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/>`},
	{"orange", `<path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/>`},
	{"amber", `<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>`},
}

// Empty is an empty state with tile lit lit (0..3) lit.
func (p *page) Empty(lit int, compact bool, title, body string) emptyData {
	a := emptyArt[lit]
	e := emptyData{Cells: []int{0, 1, 2, 3}, Lit: lit, Tone: "t-" + a.tone, Title: title, Body: body, Compact: compact,
		Icon: template.HTML(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + a.icon + `</svg>`)}
	if p.Kind == "notfound" && p.inSite {
		e.Action, e.ActionHref = p.T.T("help.default_title"), p.Link("/")
	}
	return e
}

type updatesData struct {
	Page    *page
	Entries []kb.ChangelogEntry
}

// Updates is a list of changelog entries.
func (p *page) Updates(list []kb.ChangelogEntry) updatesData {
	return updatesData{Page: p, Entries: list}
}

type catsData struct {
	Page  *page
	Entry kb.ChangelogEntry
}

// EntryCats are an entry's categories, each linking its timeline.
func (p *page) EntryCats(e kb.ChangelogEntry) catsData { return catsData{Page: p, Entry: e} }

type crumb struct{ Label, Href string }

type crumbsData struct {
	Page  *page
	Items []crumb
}

// Crumbs is the trail above a page.
func (p *page) Crumbs() crumbsData {
	c := crumbsData{Page: p}
	switch p.Kind {
	case "collection":
		c.Items = []crumb{{Label: p.Collection.Collection.Name}}
	case "article":
		if p.ArticleCollection != nil {
			c.Items = append(c.Items, crumb{Label: p.ArticleCollection.Name, Href: p.Link("/c/" + p.ArticleCollection.Slug)})
		}
		c.Items = append(c.Items, crumb{Label: p.Article.Title})
	case "search":
		c.Items = []crumb{{Label: p.T.T("help.search")}}
	case "changelog":
		c.Items = []crumb{{Label: p.T.T("help.changelog")}}
	case "entry":
		c.Items = []crumb{{Label: p.T.T("help.changelog"), Href: p.CategoryURL("")}, {Label: p.Entry.Title}}
	}
	return c
}

type avatarData struct{ Src, Tone, Initial string }

// Avatar is a person's picture, or their initial on their hue.
func (p *page) Avatar(name, src string) avatarData {
	return avatarData{Src: src, Tone: "t-" + toneFor(name), Initial: initial(name)}
}

// Published is when an article came out, else when it last changed.
func (p *page) Published(a kb.KbArticle) time.Time {
	if a.PublishedAt != nil {
		return *a.PublishedAt
	}
	return a.UpdatedAt
}

// ISO is a time for a datetime attribute; the page's script shows it in the reader's language.
func (p *page) ISO(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// monthNames are the long and short month names of languages whose dates browsers may
// not know ("22 oktyabr 2026", "22 okt"). The page's script keeps the date as the page
// wrote it when the browser has no data for the page's language.
var monthNames = map[string][2][12]string{
	"az": {
		{"yanvar", "fevral", "mart", "aprel", "may", "iyun", "iyul", "avqust", "sentyabr", "oktyabr", "noyabr", "dekabr"},
		{"yan", "fev", "mar", "apr", "may", "iyn", "iyl", "avq", "sen", "okt", "noy", "dek"},
	},
}

// LongDate and ShortDate are what a page shows before its script runs: English words in
// English, day month year in a language of monthNames, the ISO date otherwise.
func (p *page) LongDate(t time.Time) string {
	t = t.UTC()
	if p.Lang == "en" {
		return t.Format("January 2, 2006")
	}
	if names, ok := monthNames[p.Lang]; ok {
		return fmt.Sprintf("%d %s %d", t.Day(), names[0][t.Month()-1], t.Year())
	}
	return t.Format("2006-01-02")
}

func (p *page) ShortDate(t time.Time) string {
	if names, ok := monthNames[p.Lang]; ok {
		t = t.UTC()
		if t.Year() == time.Now().UTC().Year() {
			return fmt.Sprintf("%d %s", t.Day(), names[1][t.Month()-1])
		}
		return fmt.Sprintf("%d %s %d", t.Day(), names[1][t.Month()-1], t.Year())
	}
	if p.Lang != "en" {
		return t.UTC().Format("2006-01-02")
	}
	if t.UTC().Year() == time.Now().UTC().Year() {
		return t.UTC().Format("Jan 2")
	}
	return t.UTC().Format("Jan 2, 2006")
}

// QueryLen is the search term's length in characters.
func (p *page) QueryLen() int { return utf8.RuneCountInString(strings.TrimSpace(p.Query)) }

// articleBody is an article's stored HTML for the page: sanitized again, with images the
// API stores addressed at the API.
func articleBody(raw, apiURL string) template.HTML {
	body := hd.SanitizeRich(raw)
	body = strings.ReplaceAll(body, `src="/v1/attachments/`, `src="`+apiURL+`/v1/attachments/`)
	return template.HTML(body)
}

// render executes the page into a buffer.
func render(p *page) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page", p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jsonLD encodes structured data for a page. json.Marshal escapes <, > and &, so the
// script element cannot be closed from inside.
func jsonLD(v any) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return template.JS(b)
}

// excerpt is a short plain description for meta tags.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	if i := strings.LastIndexByte(string(r), ' '); i > n/2 {
		return string(r)[:i] + "…"
	}
	return string(r) + "…"
}
