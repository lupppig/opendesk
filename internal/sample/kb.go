package sample

import (
	"context"
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/ids"
)

// excerptLen is how much of an article's text its excerpt keeps (as the KB API derives it).
const excerptLen = 200

type kbArticle struct {
	collection int
	title      string
	body       string
	draft      bool
}

// seedKB adds collections and articles, sanitized like the knowledge base API does. A
// collection with the same name is reused. No embedding job is queued: sample articles
// spend nothing on AI.
func (g *gen) seedKB(ctx context.Context) error {
	nc := min(g.vol.collections, len(collections))
	list := g.kbArticles(nc)
	authors := []*member{g.owner}
	for _, m := range g.members {
		if m.role == "admin" || m.role == "owner" {
			authors = append(authors, m)
		}
	}
	return g.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		collIDs := make([]uuid.UUID, nc)
		for i := range nc {
			c := collections[i]
			name := g.fill(c.name, false, nil)
			id, created, err := g.ensureCollection(ctx, tx, name, g.fill(c.desc, false, nil))
			if err != nil {
				return err
			}
			if created {
				g.counts.Collections++
			}
			collIDs[i] = id
		}
		window := time.Duration(max(g.vol.days, 60)) * day
		for i, a := range list {
			title := hd.Truncate(strings.Join(strings.Fields(g.fill(a.title, false, nil)), " "), 200)
			body := hd.SanitizeRich(g.fill(a.body, true, nil))
			slug, err := hd.UniqueSlug(ctx, tx, "kb_articles", g.wid, hd.Slugify(title), nil)
			if err != nil {
				return err
			}
			status := "published"
			if a.draft || (i > 5 && g.chance(0.12)) {
				status = "draft"
			}
			created := g.when(g.ago(window), g.ago(time.Hour))
			updated := created
			if g.chance(0.5) {
				updated, _ = g.next(created, time.Duration(g.between(1, 40))*day)
			}
			views := 0
			if status == "published" {
				// A few articles get most of the views.
				views = 3 + int(4000*math.Pow(g.rng.Float64(), 3))
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kb_articles (id, workspace_id, collection_id, title, slug, excerpt, body_html, status,
				author_id, position, views, published_at, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, CASE WHEN $8 = 'published' THEN $12::timestamptz END, $12, $13)`,
				ids.New(), g.wid, collIDs[a.collection], title, slug, hd.Excerpt(hd.HTMLToText(body), excerptLen), body, status,
				pick(g, authors).id, i, views, created, updated); err != nil {
				return fmt.Errorf("add article: %w", err)
			}
			g.counts.Articles++
		}
		return nil
	})
}

// kbArticles is the size's articles across the first nc collections: the hand-written
// ones, then shorter topic articles, then monthly release notes.
func (g *gen) kbArticles(nc int) []kbArticle {
	var out []kbArticle
	for _, a := range articles {
		if a.collection < nc {
			out = append(out, kbArticle{a.collection, a.title, a.body, a.draft})
		}
	}
	for _, t := range topics {
		if t.collection >= nc {
			continue
		}
		var b strings.Builder
		b.WriteString("<p>" + html.EscapeString(t.summary) + "</p><h2>Steps</h2><ol>")
		for _, s := range t.steps {
			b.WriteString("<li>" + html.EscapeString(s) + "</li>")
		}
		b.WriteString("</ol><p>Still stuck? Write to us and we'll help.</p>")
		out = append(out, kbArticle{t.collection, t.title, b.String(), false})
	}
	if nc > 7 {
		for m := 1; m <= 24; m++ {
			month := time.Date(g.now.Year(), g.now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -m, 0)
			var b strings.Builder
			b.WriteString("<p>Here's what changed in {product} in " + month.Format("January 2006") + ".</p><ul>")
			for k := range 3 {
				b.WriteString("<li>" + html.EscapeString(releaseItems[(m*3+k)%len(releaseItems)]) + "</li>")
			}
			b.WriteString("</ul>")
			out = append(out, kbArticle{7, "What's new in " + month.Format("January 2006"), b.String(), false})
		}
	}
	// Spread the size's count over the collections, keeping the hand-written first.
	if len(out) > g.vol.articles {
		out = out[:g.vol.articles]
	}
	return out
}

// ensureCollection finds a collection by name or creates it.
func (g *gen) ensureCollection(ctx context.Context, tx pgx.Tx, name, desc string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM kb_collections WHERE workspace_id = $1 AND lower(name) = lower($2) LIMIT 1`, g.wid, name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !isNoRows(err) {
		return id, false, err
	}
	slug, err := hd.UniqueSlug(ctx, tx, "kb_collections", g.wid, hd.Slugify(name), nil)
	if err != nil {
		return id, false, err
	}
	id = ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO kb_collections (id, workspace_id, name, slug, description, position)
		VALUES ($1, $2, $3, $4, $5, (SELECT COALESCE(max(position) + 1, 0) FROM kb_collections WHERE workspace_id = $2))`,
		id, g.wid, name, slug, desc)
	if err != nil {
		return id, false, fmt.Errorf("add collection: %w", err)
	}
	return id, true, nil
}
