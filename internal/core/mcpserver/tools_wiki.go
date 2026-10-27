package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

// --- wiki ----------------------------------------------------------------------------

// The team's internal pages (contracts/wiki.md): runbooks, policies, how-tos. Never
// for customers; an AI client reads them as the member or key it acts for.

func wikiTools() []*tool {
	return []*tool{
		{
			Name: "search_wiki", Title: "Search the wiki", Scopes: []string{auth.ScopeWikiRead}, ReadOnly: true, Idempotent: true,
			Description: "Search the team's internal wiki (runbooks, policies, escalation steps, how-tos) by text. " +
				"Returns the best matching pages with where they sit and a passage around the match; read one with get_wiki_page.",
			Schema: obj(map[string]any{
				"query": strN("What to search for.", 1, 200),
				"limit": integer("How many pages to return.", 1, 20, 8),
			}, "query"),
			Run: searchWiki,
		},
		{
			Name: "get_wiki_page", Title: "Read a wiki page", Scopes: []string{auth.ScopeWikiRead}, ReadOnly: true, Idempotent: true,
			Description: "Read one page of the team's internal wiki: its text, where it sits, its subpages, who changed it last and its version.",
			Schema:      obj(map[string]any{"page": str("The page id.")}, "page"),
			Run:         getWikiPage,
		},
		{
			Name: "create_wiki_page", Title: "Create a wiki page", Scopes: []string{auth.ScopeWikiWrite},
			Description: "Add a page to the team's internal wiki, at the top level or under another page. Markdown body.",
			Schema: obj(map[string]any{
				"title":  strN("The page title.", 1, 200),
				"body":   strN("The page, in markdown.", 0, 50000),
				"parent": str("Put it under this page (an id; optional)."),
			}, "title"),
			Run: createWikiPage,
		},
		{
			Name: "update_wiki_page", Title: "Update a wiki page", Scopes: []string{auth.ScopeWikiWrite}, Destructive: true,
			Description: "Rename a wiki page or replace its text. Pass the version get_wiki_page returned: when a teammate changed the page since, " +
				"the update is refused (page_changed) instead of overwriting their work. Markdown body.",
			Schema: obj(map[string]any{
				"page":    str("The page id."),
				"title":   strN("A new title.", 1, 200),
				"body":    strN("The whole new text, in markdown. It replaces the page's text.", 0, 50000),
				"version": map[string]any{"type": "integer", "description": "The version this change is based on, from get_wiki_page.", "minimum": 1},
			}, "page"),
			Run: updateWikiPage,
		},
	}
}

// pageChars is the most text get_wiki_page returns.
const pageChars = 20000

type apiWikiRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon"`
}

type apiWikiPage struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	BodyHTML    string       `json:"body_html"`
	Version     int          `json:"version"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Breadcrumbs []apiWikiRef `json:"breadcrumbs"`
	Children    []apiWikiRef `json:"children"`
	UpdatedBy   *struct {
		Name string `json:"name"`
	} `json:"updated_by"`
}

type outWikiRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type outWikiPage struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Path      string       `json:"path,omitempty"`
	Text      string       `json:"text,omitempty"`
	Subpages  []outWikiRef `json:"subpages,omitempty"`
	Version   int          `json:"version"`
	UpdatedBy string       `json:"updated_by,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func untitled(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Untitled"
	}
	return s
}

func wikiPath(refs []apiWikiRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = untitled(r.Title)
	}
	return strings.Join(parts, " / ")
}

func compactWikiPage(p apiWikiPage, text bool) outWikiPage {
	o := outWikiPage{ID: p.ID, Title: untitled(p.Title), Path: wikiPath(p.Breadcrumbs), Version: p.Version, UpdatedAt: p.UpdatedAt}
	if text {
		o.Text = clip(hd.HTMLToText(p.BodyHTML), pageChars)
		for _, c := range p.Children {
			o.Subpages = append(o.Subpages, outWikiRef{ID: c.ID, Title: untitled(c.Title)})
		}
	}
	if p.UpdatedBy != nil {
		o.UpdatedBy = p.UpdatedBy.Name
	}
	return o
}

func searchWiki(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}](raw)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return nil, invalid("query is empty")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 8
	}
	var pg page[struct {
		ID        string       `json:"id"`
		Title     string       `json:"title"`
		Snippet   string       `json:"snippet"`
		Path      []apiWikiRef `json:"path"`
		UpdatedAt time.Time    `json:"updated_at"`
	}]
	if err := c.get(ctx, "/v1/wiki/search", url.Values{"q": {clip(query, 200)}, "limit": {strconv.Itoa(min(limit, 20))}}, &pg); err != nil {
		return nil, err
	}
	type hit struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Path      string    `json:"path,omitempty"`
		Snippet   string    `json:"snippet,omitempty"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	out := struct {
		Pages []hit `json:"pages"`
	}{Pages: []hit{}}
	for _, h := range pg.Data {
		out.Pages = append(out.Pages, hit{ID: h.ID, Title: untitled(h.Title), Path: wikiPath(h.Path), Snippet: clip(h.Snippet, 400), UpdatedAt: h.UpdatedAt})
	}
	return out, nil
}

func getWikiPage(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Page string `json:"page"`
	}](raw)
	if err != nil {
		return nil, err
	}
	id, err := idRef(a.Page, "page")
	if err != nil {
		return nil, err
	}
	var p apiWikiPage
	if err := c.get(ctx, "/v1/wiki/pages/"+id, nil, &p); err != nil {
		return nil, err
	}
	return compactWikiPage(p, true), nil
}

// wikiText checks a markdown body and turns it into page HTML.
func wikiText(body string) (string, error) {
	if utf8.RuneCountInString(body) > 50000 {
		return "", invalid("body must be at most 50,000 characters")
	}
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	return ai.MarkdownToHTML(body), nil
}

func wikiTitle(raw string) (string, error) {
	title := strings.Join(strings.Fields(raw), " ")
	if title == "" || utf8.RuneCountInString(title) > 200 {
		return "", invalid("title must be 1 to 200 characters")
	}
	return title, nil
}

func createWikiPage(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Parent string `json:"parent"`
	}](raw)
	if err != nil {
		return nil, err
	}
	title, err := wikiTitle(a.Title)
	if err != nil {
		return nil, err
	}
	html, err := wikiText(a.Body)
	if err != nil {
		return nil, err
	}
	req := map[string]any{"title": title, "body_html": html}
	if strings.TrimSpace(a.Parent) != "" {
		if req["parent_id"], err = idRef(a.Parent, "parent"); err != nil {
			return nil, err
		}
	}
	var p apiWikiPage
	if err := c.do(ctx, http.MethodPost, "/v1/wiki/pages", nil, req, &p); err != nil {
		return nil, err
	}
	return compactWikiPage(p, false), nil
}

func updateWikiPage(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Page    string  `json:"page"`
		Title   *string `json:"title"`
		Body    *string `json:"body"`
		Version int     `json:"version"`
	}](raw)
	if err != nil {
		return nil, err
	}
	id, err := idRef(a.Page, "page")
	if err != nil {
		return nil, err
	}
	req := map[string]any{}
	if a.Title != nil {
		if req["title"], err = wikiTitle(*a.Title); err != nil {
			return nil, err
		}
	}
	if a.Body != nil {
		if req["body_html"], err = wikiText(*a.Body); err != nil {
			return nil, err
		}
	}
	if len(req) == 0 {
		return nil, invalid("give a title, a body or both")
	}
	if a.Version > 0 {
		req["version"] = a.Version
	}
	var p apiWikiPage
	if err := c.do(ctx, http.MethodPatch, "/v1/wiki/pages/"+id, nil, req, &p); err != nil {
		return nil, err
	}
	return compactWikiPage(p, false), nil
}
