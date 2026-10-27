package wiki

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// WikiRevision is a snapshot of a page (contracts/wiki.md "History"). The list leaves
// body_html out.
type WikiRevision struct {
	ID        uuid.UUID       `json:"id"`
	Title     string          `json:"title"`
	Icon      string          `json:"icon"`
	BodyHTML  *string         `json:"body_html,omitempty"`
	Author    *domain.UserRef `json:"author"`
	CreatedAt time.Time       `json:"created_at" doc:"When the first edit it holds was made"`
	UpdatedAt time.Time       `json:"updated_at" doc:"When the last edit it holds was made"`
}

// recordRevision snapshots a page after a change. With merge, a person's edit joins their
// own latest revision when it is the page's latest and recent (coalesceGap, coalesceSpan);
// otherwise, and for API keys, it starts a new one. The oldest beyond maxRevisions go.
func recordRevision(ctx context.Context, tx pgx.Tx, p *auth.Principal, page uuid.UUID, title, icon, body string, merge bool) error {
	author := p.ActorUserID()
	if merge && author != nil {
		tag, err := tx.Exec(ctx, `UPDATE wiki_page_revisions SET title = $4, icon = $5, body_html = $6, updated_at = now()
			WHERE id = (SELECT id FROM wiki_page_revisions WHERE workspace_id = $1 AND page_id = $2 ORDER BY updated_at DESC, id DESC LIMIT 1)
			AND author_id = $3 AND updated_at > now() - make_interval(secs => $7) AND created_at > now() - make_interval(secs => $8)`,
			p.WorkspaceID, page, author, title, icon, body, coalesceGap.Seconds(), coalesceSpan.Seconds())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_revisions (id, workspace_id, page_id, title, icon, body_html, author_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), p.WorkspaceID, page, title, icon, body, author); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM wiki_page_revisions WHERE workspace_id = $1 AND page_id = $2 AND id NOT IN (
		SELECT id FROM wiki_page_revisions WHERE workspace_id = $1 AND page_id = $2 ORDER BY updated_at DESC, id DESC LIMIT $3)`,
		p.WorkspaceID, page, maxRevisions)
	return err
}

const revisionSelect = `SELECT r.id, r.title, r.icon, r.created_at, r.updated_at, u.id, u.name, u.email, u.avatar_url
FROM wiki_page_revisions r LEFT JOIN users u ON u.id = r.author_id`

func scanRevision(row pgx.Row, body *string) (WikiRevision, error) {
	var r WikiRevision
	var u userCols
	dst := []any{&r.ID, &r.Title, &r.Icon, &r.CreatedAt, &r.UpdatedAt, &u.id, &u.name, &u.email, &u.avatar}
	if body != nil {
		dst = append(dst, body)
	}
	if err := row.Scan(dst...); err != nil {
		return r, err
	}
	r.Author = u.ref()
	return r, nil
}

// pageExists answers 404 for a page outside the workspace.
func pageExists(ctx context.Context, q db.Querier, wid, id uuid.UUID) error {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wiki_pages WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.NotFound("Page")
	}
	return nil
}

func registerRevisions(d *deps.Deps, api huma.API) {
	type revPath struct {
		ID         string `path:"id"`
		RevisionID string `path:"revisionId"`
	}
	parseRev := func(in *revPath) (uuid.UUID, uuid.UUID, error) {
		id, err := hd.ParseID(in.ID, "Page")
		if err != nil {
			return id, id, err
		}
		rid, err := hd.ParseID(in.RevisionID, "Revision")
		return id, rid, err
	}

	huma.Register(api, huma.Operation{OperationID: "list-wiki-page-revisions", Method: http.MethodGet, Path: "/v1/wiki/pages/{id}/revisions",
		Summary: "List a wiki page's history", Description: "Up to 100 revisions, the newest first, without their bodies.", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body httpx.Page[WikiRevision] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeWikiRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Page")
			if err != nil {
				return nil, err
			}
			if err := pageExists(ctx, d.DB.Pool, p.WorkspaceID, id); err != nil {
				return nil, httpx.Wrap(err)
			}
			rows, err := d.DB.Pool.Query(ctx, revisionSelect+` WHERE r.workspace_id = $1 AND r.page_id = $2 ORDER BY r.updated_at DESC, r.id DESC LIMIT $3`,
				p.WorkspaceID, id, maxRevisions)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WikiRevision, error) { return scanRevision(r, nil) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if out == nil {
				out = []WikiRevision{}
			}
			return &struct{ Body httpx.Page[WikiRevision] }{httpx.Page[WikiRevision]{Data: out}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-wiki-page-revision", Method: http.MethodGet, Path: "/v1/wiki/pages/{id}/revisions/{revisionId}",
		Summary: "Get a revision of a wiki page", Tags: tags},
		func(ctx context.Context, in *revPath) (*struct{ Body WikiRevision }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeWikiRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, rid, err := parseRev(in)
			if err != nil {
				return nil, err
			}
			var body string
			r, err := scanRevision(d.DB.Pool.QueryRow(ctx, `SELECT r.id, r.title, r.icon, r.created_at, r.updated_at, u.id, u.name, u.email, u.avatar_url, r.body_html
				FROM wiki_page_revisions r LEFT JOIN users u ON u.id = r.author_id WHERE r.workspace_id = $1 AND r.page_id = $2 AND r.id = $3`,
				p.WorkspaceID, id, rid), &body)
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Revision")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			r.BodyHTML = &body
			return &struct{ Body WikiRevision }{r}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "restore-wiki-page-revision", Method: http.MethodPost, Path: "/v1/wiki/pages/{id}/revisions/{revisionId}/restore",
		Summary: "Restore a revision of a wiki page", Tags: tags,
		Description: "Puts the revision's title, icon and body back as a new version. The history keeps what it replaced."},
		func(ctx context.Context, in *revPath) (*pageOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeWikiWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, rid, err := parseRev(in)
			if err != nil {
				return nil, err
			}
			var pg WikiPage
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var one int
				err := tx.QueryRow(ctx, `SELECT 1 FROM wiki_pages WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, p.WorkspaceID, id).Scan(&one)
				if db.IsNotFound(err) {
					return httpx.NotFound("Page")
				}
				if err != nil {
					return err
				}
				var title, icon, body string
				err = tx.QueryRow(ctx, `SELECT title, icon, body_html FROM wiki_page_revisions WHERE workspace_id = $1 AND page_id = $2 AND id = $3`,
					p.WorkspaceID, id, rid).Scan(&title, &icon, &body)
				if db.IsNotFound(err) {
					return httpx.NotFound("Revision")
				}
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET title = $3, icon = $4, body_html = $5, version = version + 1,
					updated_by = $6, updated_at = now() WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id, title, icon, body, p.ActorUserID()); err != nil {
					return err
				}
				if err := recordRevision(ctx, tx, p, id, title, icon, body, false); err != nil {
					return err
				}
				if pg, err = loadPage(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "wiki_page.updated", Data: eventPage(pg)})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &pageOut{pg}, nil
		})
}
