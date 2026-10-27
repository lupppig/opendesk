package wiki

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Limits (contracts/wiki.md "Limits").
const (
	maxTitle     = 200
	maxBodyBytes = 512 << 10
	maxDepth     = 10
	maxPages     = 5000
	maxRevisions = 100
	// One author's edits less than coalesceGap apart share a revision, for at most coalesceSpan.
	coalesceGap  = 10 * time.Minute
	coalesceSpan = time.Hour
)

// WikiNode is a page in the tree: everything but its body.
type WikiNode struct {
	ID        uuid.UUID  `json:"id"`
	ParentID  *uuid.UUID `json:"parent_id" doc:"The page it sits under; null at the top level"`
	Title     string     `json:"title" doc:"Empty until named; shown as Untitled"`
	Icon      string     `json:"icon" doc:"An emoji, or empty"`
	Position  int        `json:"position" doc:"Order among its siblings, from 0"`
	Version   int        `json:"version" doc:"Moves on every change of title, icon or body"`
	UpdatedAt time.Time  `json:"updated_at"`
	CreatedBy *uuid.UUID `json:"created_by_id" doc:"Who created it; null for an API key or a deleted account"`
}

// WikiRef names a page: breadcrumbs, subpages, search paths.
type WikiRef struct {
	ID    uuid.UUID `json:"id"`
	Title string    `json:"title"`
	Icon  string    `json:"icon"`
}

// WikiPage is one page with its body, from contracts/wiki.md.
type WikiPage struct {
	ID          uuid.UUID       `json:"id"`
	ParentID    *uuid.UUID      `json:"parent_id"`
	Title       string          `json:"title"`
	Icon        string          `json:"icon"`
	BodyHTML    string          `json:"body_html"`
	Position    int             `json:"position"`
	Version     int             `json:"version"`
	CreatedBy   *domain.UserRef `json:"created_by"`
	UpdatedBy   *domain.UserRef `json:"updated_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Breadcrumbs []WikiRef       `json:"breadcrumbs" doc:"Its ancestors, the top-level page first"`
	Children    []WikiRef       `json:"children" doc:"Its subpages, in order"`
}

func (p WikiPage) node() WikiNode {
	n := WikiNode{ID: p.ID, ParentID: p.ParentID, Title: p.Title, Icon: p.Icon, Position: p.Position, Version: p.Version, UpdatedAt: p.UpdatedAt}
	if p.CreatedBy != nil {
		n.CreatedBy = &p.CreatedBy.ID
	}
	return n
}

const nodeSelect = `SELECT p.id, p.parent_id, p.title, p.icon, p.position, p.version, p.updated_at, p.created_by FROM wiki_pages p`

func scanNode(row pgx.Row) (WikiNode, error) {
	var n WikiNode
	err := row.Scan(&n.ID, &n.ParentID, &n.Title, &n.Icon, &n.Position, &n.Version, &n.UpdatedAt, &n.CreatedBy)
	return n, err
}

const pageSelect = `SELECT p.id, p.parent_id, p.title, p.icon, p.body_html, p.position, p.version, p.created_at, p.updated_at,
	c.id, c.name, c.email, c.avatar_url, u.id, u.name, u.email, u.avatar_url
FROM wiki_pages p LEFT JOIN users c ON c.id = p.created_by LEFT JOIN users u ON u.id = p.updated_by`

// userCols receives a LEFT JOINed user.
type userCols struct {
	id                  *uuid.UUID
	name, email, avatar *string
}

func (u userCols) ref() *domain.UserRef {
	if u.id == nil {
		return nil
	}
	return &domain.UserRef{ID: *u.id, Name: deref(u.name), Email: deref(u.email), AvatarURL: domain.Avatar(deref(u.avatar))}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loadPage reads a page of the workspace with its breadcrumbs and subpages.
func loadPage(ctx context.Context, q db.Querier, wid, id uuid.UUID) (WikiPage, error) {
	var p WikiPage
	var c, u userCols
	err := q.QueryRow(ctx, pageSelect+` WHERE p.workspace_id = $1 AND p.id = $2`, wid, id).Scan(
		&p.ID, &p.ParentID, &p.Title, &p.Icon, &p.BodyHTML, &p.Position, &p.Version, &p.CreatedAt, &p.UpdatedAt,
		&c.id, &c.name, &c.email, &c.avatar, &u.id, &u.name, &u.email, &u.avatar)
	if err != nil {
		return p, err
	}
	p.CreatedBy, p.UpdatedBy = c.ref(), u.ref()
	if p.Breadcrumbs, err = ancestors(ctx, q, wid, id); err != nil {
		return p, err
	}
	rows, err := q.Query(ctx, `SELECT id, title, icon FROM wiki_pages WHERE workspace_id = $1 AND parent_id = $2 ORDER BY position, id`, wid, id)
	p.Children, err = collectRefs(rows, err)
	return p, err
}

// ancestors are the pages above id, the top-level one first.
func ancestors(ctx context.Context, q db.Querier, wid, id uuid.UUID) ([]WikiRef, error) {
	rows, err := q.Query(ctx, `WITH RECURSIVE up(id, parent_id, title, icon, depth) AS (
			SELECT p.id, p.parent_id, p.title, p.icon, 0 FROM wiki_pages p WHERE p.workspace_id = $1 AND p.id = $2
			UNION ALL
			SELECT p.id, p.parent_id, p.title, p.icon, up.depth + 1 FROM wiki_pages p JOIN up ON p.id = up.parent_id
			WHERE p.workspace_id = $1 AND up.depth < 64
		)
		SELECT id, title, icon FROM up WHERE depth > 0 ORDER BY depth DESC`, wid, id)
	return collectRefs(rows, err)
}

func collectRefs(rows pgx.Rows, err error) ([]WikiRef, error) {
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WikiRef, error) {
		var x WikiRef
		err := r.Scan(&x.ID, &x.Title, &x.Icon)
		return x, err
	})
	if out == nil {
		out = []WikiRef{}
	}
	return out, err
}

// subtree is id and every page under it, and how many levels it spans (1: no subpages).
func subtree(ctx context.Context, q db.Querier, wid, id uuid.UUID) ([]uuid.UUID, int, error) {
	rows, err := q.Query(ctx, `WITH RECURSIVE down(id, depth) AS (
			SELECT id, 1 FROM wiki_pages WHERE workspace_id = $1 AND id = $2
			UNION ALL
			SELECT p.id, down.depth + 1 FROM wiki_pages p JOIN down ON p.parent_id = down.id
			WHERE p.workspace_id = $1 AND down.depth < 64
		)
		SELECT id, depth FROM down ORDER BY depth`, wid, id)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []uuid.UUID
	height := 0
	for rows.Next() {
		var x uuid.UUID
		var depth int
		if err := rows.Scan(&x, &depth); err != nil {
			return nil, 0, err
		}
		out = append(out, x)
		height = max(height, depth)
	}
	return out, height, rows.Err()
}

// lockTree serializes the workspace's changes to the tree (create, move, delete), so
// counts, depths and cycle checks see each other's results.
func lockTree(ctx context.Context, tx pgx.Tx, wid uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text || ':wiki'))`, wid)
	return err
}

// parentRef resolves a parent page id of the workspace ("" is the top level) and the
// depth a page placed under it would have.
func parentRef(ctx context.Context, q db.Querier, wid uuid.UUID, raw *string) (*uuid.UUID, []WikiRef, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil, nil
	}
	id, err := hd.ParseRef(*raw, "parent_id")
	if err != nil {
		return nil, nil, err
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wiki_pages WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&exists); err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, httpx.Validation("parent page not found in this workspace")
	}
	up, err := ancestors(ctx, q, wid, id)
	if err != nil {
		return nil, nil, err
	}
	return &id, up, nil
}

// place puts id at index among the children of parent (the end when index is past it, or
// nil) and numbers the siblings from 0. With id nil it only renumbers them.
func place(ctx context.Context, tx pgx.Tx, wid uuid.UUID, parent *uuid.UUID, id *uuid.UUID, index *int) error {
	rows, err := tx.Query(ctx, `SELECT id FROM wiki_pages WHERE workspace_id = $1 AND parent_id IS NOT DISTINCT FROM $2
		AND ($3::uuid IS NULL OR id <> $3) ORDER BY position, created_at, id FOR UPDATE`, wid, parent, id)
	if err != nil {
		return err
	}
	order, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if id != nil {
		at := len(order)
		if index != nil && *index >= 0 && *index < at {
			at = *index
		}
		order = append(order[:at], append([]uuid.UUID{*id}, order[at:]...)...)
	}
	_, err = tx.Exec(ctx, `UPDATE wiki_pages p SET position = o.n - 1 FROM unnest($2::uuid[]) WITH ORDINALITY AS o(id, n)
		WHERE p.workspace_id = $1 AND p.id = o.id AND p.position <> o.n - 1`, wid, order)
	return err
}

// cleanTitle folds whitespace; a page may stay untitled.
func cleanTitle(s string) string {
	return hd.Truncate(strings.Join(strings.Fields(s), " "), maxTitle)
}

// cleanIcon accepts one emoji or nothing.
func cleanIcon(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > 64 || !isEmoji(s) {
		return "", httpx.Validation("icon must be one emoji")
	}
	return s, nil
}

// cleanBody sanitizes a page body like an article's (contracts/wiki.md "Body").
func cleanBody(raw string) (string, error) {
	if len(raw) > maxBodyBytes {
		return "", httpx.E(422, "page_too_large", "A page can hold up to 512 KB of text and formatting. Split it into subpages.")
	}
	return hd.SanitizeRich(raw), nil
}
