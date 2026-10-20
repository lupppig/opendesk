package ai

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
)

// AISearchArticle is KbArticle from contracts/api.md.
type AISearchArticle struct {
	ID           uuid.UUID       `json:"id"`
	CollectionID *uuid.UUID      `json:"collection_id"`
	Title        string          `json:"title"`
	Slug         string          `json:"slug"`
	Excerpt      string          `json:"excerpt"`
	BodyHTML     string          `json:"body_html"`
	Status       string          `json:"status" enum:"draft,published"`
	Author       *domain.UserRef `json:"author"`
	Views        int64           `json:"views"`
	PublishedAt  *time.Time      `json:"published_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// AISearchResults is the shape of GET /v1/search.
type AISearchResults struct {
	Tickets  []domain.Ticket   `json:"tickets"`
	Contacts []domain.Contact  `json:"contacts"`
	Articles []AISearchArticle `json:"articles"`
}

const (
	searchLimit = 5
	rrfK        = 60.0
	candidates  = 20
	minSimRank  = 0.25
)

// fuse ranks ids by reciprocal rank fusion over several ranked lists.
func fuse(lists ...[]uuid.UUID) []uuid.UUID {
	score := map[uuid.UUID]float64{}
	first := map[uuid.UUID]int{}
	n := 0
	for _, l := range lists {
		for rank, id := range l {
			score[id] += 1 / (rrfK + float64(rank+1))
			if _, ok := first[id]; !ok {
				first[id] = n
				n++
			}
		}
	}
	out := make([]uuid.UUID, 0, len(score))
	for id := range score {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		if score[out[i]] != score[out[j]] {
			return score[out[i]] > score[out[j]]
		}
		return first[out[i]] < first[out[j]]
	})
	return out
}

func collectIDs(rows pgx.Rows, err error) ([]uuid.UUID, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// ftsArgs returns the strict tsquery SQL over $2 (raw query) and $3 (every word as a
// prefix), and the arguments: raw, prefix AND, and an any-keyword query for fallback.
func ftsArgs(q string) (string, []any) {
	words := keywords(q)
	pq := prefixAND(q)
	if pq == "" {
		return "", nil
	}
	return `(websearch_to_tsquery('simple', $2) || to_tsquery('simple', $3))`, []any{q, pq, orQuery(words)}
}

func prefixAND(q string) string {
	words := queryWord.FindAllString(strings.ToLower(q), 8)
	for i, w := range words {
		words[i] = w + ":*"
	}
	return strings.Join(words, " & ")
}

// Search is GET /v1/ai/search: semantic matches fused with full-text matches; without
// embeddings, full-text only, falling back to any-keyword matching for natural questions.
func (s *Service) Search(ctx context.Context, p *auth.Principal, q string) (*AISearchResults, error) {
	res := &AISearchResults{Tickets: []domain.Ticket{}, Contacts: []domain.Contact{}, Articles: []AISearchArticle{}}
	// A person signed in directly sees what their role allows; an API key or a person
	// acting through an OAuth grant only what its scopes allow.
	allowed := func(scope string) bool {
		return (p.Kind == auth.KindUser && p.GrantID == uuid.Nil) || p.HasScope(scope)
	}
	var qvec string
	var dims int
	if s.semantic() {
		if v, err := s.embedder.Embed(ctx, []string{clip(q, 2000)}, true); err == nil {
			qvec, dims = vectorLiteral(v[0]), len(v[0])
		} else {
			s.d.Log.Warn("ai search embedding failed, using full-text search", "err", err)
		}
	}
	pool := s.d.DB.Pool
	tsq, args := ftsArgs(q)
	wid := p.WorkspaceID
	semantic := func(kind, filter string) ([]uuid.UUID, error) {
		if qvec == "" {
			return nil, nil
		}
		rows, err := pool.Query(ctx, `SELECT ref_id FROM (SELECT e.ref_id, min(e.embedding <=> $3::vector) AS dist
			FROM ai_embeddings e WHERE e.workspace_id = $1 AND e.kind = $2 AND e.model = $4 AND e.dims = $5 GROUP BY e.ref_id) x
			WHERE 1 - dist >= $6 `+filter+` ORDER BY dist LIMIT $7`, wid, kind, qvec, s.embedder.Model(), dims, minSimRank, candidates)
		return collectIDs(rows, err)
	}
	if allowed(auth.ScopeTicketsRead) && tsq != "" {
		fts, err := collectIDs(pool.Query(ctx, `SELECT t.id FROM tickets t WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam
			AND (t.search @@ `+tsq+` OR EXISTS (SELECT 1 FROM messages m WHERE m.workspace_id = $1 AND m.ticket_id = t.id AND m.search @@ `+tsq+`))
			ORDER BY ts_rank(t.search, `+tsq+`) DESC, t.last_message_at DESC LIMIT `+strconv.Itoa(candidates), wid, args[0], args[1]))
		if err != nil {
			return nil, err
		}
		sem, err := semantic("ticket", `AND EXISTS (SELECT 1 FROM tickets t WHERE t.id = x.ref_id AND t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam)`)
		if err != nil {
			return nil, err
		}
		if len(fts) == 0 && len(sem) == 0 {
			fts, err = collectIDs(pool.Query(ctx, `SELECT t.id FROM tickets t WHERE t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam
				AND t.search @@ to_tsquery('simple', $2) ORDER BY ts_rank_cd(t.search, to_tsquery('simple', $2)) DESC LIMIT $3`,
				wid, args[2], candidates))
			if err != nil {
				return nil, err
			}
		}
		for _, id := range fuse(sem, fts) {
			// A private team's ticket is left out for whoever may not see it.
			t, err := domain.LoadVisibleTicket(ctx, pool, p, id)
			if db.IsNotFound(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			res.Tickets = append(res.Tickets, t)
			if len(res.Tickets) == searchLimit {
				break
			}
		}
	}
	if allowed(auth.ScopeKBRead) && tsq != "" {
		fts, err := collectIDs(pool.Query(ctx, `SELECT a.id FROM kb_articles a WHERE a.workspace_id = $1 AND a.search @@ `+tsq+`
			ORDER BY ts_rank(a.search, `+tsq+`) DESC, a.updated_at DESC LIMIT `+strconv.Itoa(candidates), wid, args[0], args[1]))
		if err != nil {
			return nil, err
		}
		sem, err := semantic("article", `AND EXISTS (SELECT 1 FROM kb_articles a WHERE a.id = x.ref_id AND a.workspace_id = $1)`)
		if err != nil {
			return nil, err
		}
		if len(fts) == 0 && len(sem) == 0 {
			fts, err = collectIDs(pool.Query(ctx, `SELECT a.id FROM kb_articles a WHERE a.workspace_id = $1 AND a.search @@ to_tsquery('simple', $2)
				ORDER BY ts_rank_cd(a.search, to_tsquery('simple', $2)) DESC LIMIT $3`, wid, args[2], candidates))
			if err != nil {
				return nil, err
			}
		}
		for _, id := range fuse(sem, fts) {
			a, err := loadSearchArticle(ctx, pool, wid, id)
			if db.IsNotFound(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			res.Articles = append(res.Articles, a)
			if len(res.Articles) == searchLimit {
				break
			}
		}
	}
	if allowed(auth.ScopeContactsRead) {
		like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q)) + "%"
		cond := `ct.search @@ websearch_to_tsquery('simple', $2) OR lower(ct.email::text) LIKE $3 OR lower(ct.name) LIKE $3`
		cargs := []any{wid, q, like}
		if tsq != "" {
			cond += ` OR ct.search @@ to_tsquery('simple', $4)`
			cargs = append(cargs, args[1])
		}
		rows, err := pool.Query(ctx, domain.ContactSelect+` WHERE ct.workspace_id = $1 AND (`+cond+`) ORDER BY ct.updated_at DESC, ct.id LIMIT 5`, cargs...)
		if err != nil {
			return nil, err
		}
		cs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Contact, error) { return domain.ScanContact(r) })
		if err != nil {
			return nil, err
		}
		if cs != nil {
			res.Contacts = cs
		}
	}
	return res, nil
}

func loadSearchArticle(ctx context.Context, q db.Querier, wid, id uuid.UUID) (AISearchArticle, error) {
	var a AISearchArticle
	var uID *uuid.UUID
	var uName, uEmail, uAvatar *string
	err := q.QueryRow(ctx, `SELECT a.id, a.collection_id, a.title, a.slug, a.excerpt, a.body_html, a.status, a.views,
		a.published_at, a.updated_at, u.id, u.name, u.email, u.avatar_url
		FROM kb_articles a LEFT JOIN users u ON u.id = a.author_id WHERE a.workspace_id = $1 AND a.id = $2`, wid, id).
		Scan(&a.ID, &a.CollectionID, &a.Title, &a.Slug, &a.Excerpt, &a.BodyHTML, &a.Status, &a.Views, &a.PublishedAt, &a.UpdatedAt,
			&uID, &uName, &uEmail, &uAvatar)
	if err == nil && uID != nil {
		a.Author = &domain.UserRef{ID: *uID, Name: deref(uName), Email: deref(uEmail), AvatarURL: domain.Avatar(deref(uAvatar))}
	}
	return a, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
