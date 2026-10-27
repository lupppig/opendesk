package wiki

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// WikiSearchHit is a page that matched a search, with where it sits and a passage of
// plain text around the words found.
type WikiSearchHit struct {
	ID        uuid.UUID  `json:"id"`
	ParentID  *uuid.UUID `json:"parent_id"`
	Title     string     `json:"title"`
	Icon      string     `json:"icon"`
	Snippet   string     `json:"snippet" doc:"Plain text around the match"`
	Path      []WikiRef  `json:"path" doc:"Its ancestors, the top-level page first"`
	UpdatedAt time.Time  `json:"updated_at"`
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// prefixQuery matches every word as a prefix ("ref:* & esc:*"), so partly typed words
// match; only letters and digits survive, so it is always valid tsquery syntax. Like the
// knowledge base's.
func prefixQuery(q string) string {
	words := wordRe.FindAllString(strings.ToLower(q), 8)
	for i, w := range words {
		words[i] = w + ":*"
	}
	return strings.Join(words, " & ")
}

const tsq = `(websearch_to_tsquery('simple', $2) || to_tsquery('simple', $3))`

// Search returns the workspace's best matching pages, title matches first.
func Search(ctx context.Context, q db.Querier, wid uuid.UUID, query string, limit int) ([]WikiSearchHit, error) {
	query = strings.TrimSpace(query)
	pq := prefixQuery(query)
	if pq == "" {
		return []WikiSearchHit{}, nil
	}
	rows, err := q.Query(ctx, `SELECT p.id, p.parent_id, p.title, p.icon, p.updated_at,
			ts_headline('simple', regexp_replace(p.body_html, '<[^>]+>', ' ', 'g'), `+tsq+`,
				'MaxFragments=1, MaxWords=28, MinWords=10, ShortWord=2, StartSel="", StopSel=""')
		FROM wiki_pages p WHERE p.workspace_id = $1 AND p.search @@ `+tsq+`
		ORDER BY ts_rank(p.search, `+tsq+`) DESC, p.updated_at DESC, p.id LIMIT $4`, wid, query, pq, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WikiSearchHit, error) {
		var h WikiSearchHit
		err := r.Scan(&h.ID, &h.ParentID, &h.Title, &h.Icon, &h.UpdatedAt, &h.Snippet)
		h.Snippet = strings.Join(strings.Fields(html.UnescapeString(h.Snippet)), " ")
		return h, err
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Path, err = ancestors(ctx, q, wid, out[i].ID); err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = []WikiSearchHit{}
	}
	return out, nil
}

func registerSearch(d *deps.Deps, api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "search-wiki", Method: http.MethodGet, Path: "/v1/wiki/search",
		Summary: "Search the wiki", Description: "Full-text search over titles and bodies; every word also matches as a prefix. The best matches first.", Tags: tags},
		func(ctx context.Context, in *struct {
			Q     string `query:"q" required:"true" maxLength:"200"`
			Limit int    `query:"limit" minimum:"0" maximum:"50" doc:"Default 20"`
		}) (*struct{ Body httpx.Page[WikiSearchHit] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeWikiRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 20
			}
			out, err := Search(ctx, d.DB.Pool, p.WorkspaceID, in.Q, limit)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[WikiSearchHit] }{httpx.Page[WikiSearchHit]{Data: out}}, nil
		})
}
