package wiki

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

var tags = []string{"Wiki"}

type pageCreate struct {
	ParentID *string `json:"parent_id,omitempty" required:"false" nullable:"true" doc:"The page to put it under; null or empty for the top level"`
	Title    string  `json:"title,omitempty" maxLength:"200"`
	Icon     string  `json:"icon,omitempty" maxLength:"64" doc:"One emoji, or empty"`
	BodyHTML string  `json:"body_html,omitempty" doc:"Rich text, sanitized like an article's; up to 512 KB"`
	Position *int    `json:"position,omitempty" minimum:"0" doc:"Where among its siblings, from 0; the end when left out"`
}

type pagePatch struct {
	Title    *string `json:"title,omitempty" maxLength:"200"`
	Icon     *string `json:"icon,omitempty" maxLength:"64" doc:"One emoji; empty removes it"`
	BodyHTML *string `json:"body_html,omitempty" doc:"Replaces the body; up to 512 KB"`
	Version  *int    `json:"version,omitempty" minimum:"1" doc:"The version this edit started from; a page changed since answers 409 page_changed"`
}

type pageMove struct {
	ParentID *string `json:"parent_id" required:"false" nullable:"true" doc:"The new parent page; null or empty for the top level"`
	Position int     `json:"position" minimum:"0" doc:"Where among the new siblings, from 0; past the end puts it last"`
}

type pageOut struct{ Body WikiPage }

// eventPage is what wiki_page.created, .updated and .moved carry: the page without its
// body (bodies are large and autosaved often), and who changed it.
func eventPage(pg WikiPage) map[string]any {
	return map[string]any{"wiki_page": pg.node(), "updated_by": pg.UpdatedBy}
}

func registerPages(d *deps.Deps, api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "list-wiki-pages", Method: http.MethodGet, Path: "/v1/wiki/pages",
		Summary: "List the wiki's pages", Description: "Every page of the workspace, without bodies: the tree, ordered by parent and position.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body httpx.Page[WikiNode] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeWikiRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			rows, err := d.DB.Pool.Query(ctx, nodeSelect+` WHERE p.workspace_id = $1 ORDER BY p.parent_id NULLS FIRST, p.position, p.id`, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WikiNode, error) { return scanNode(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if out == nil {
				out = []WikiNode{}
			}
			return &struct{ Body httpx.Page[WikiNode] }{httpx.Page[WikiNode]{Data: out}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-wiki-page", Method: http.MethodGet, Path: "/v1/wiki/pages/{id}",
		Summary: "Get a wiki page", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*pageOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeWikiRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Page")
			if err != nil {
				return nil, err
			}
			pg, err := loadPage(ctx, d.DB.Pool, p.WorkspaceID, id)
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Page")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &pageOut{pg}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-wiki-page", Method: http.MethodPost, Path: "/v1/wiki/pages",
		Summary: "Create a wiki page", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct{ Body pageCreate }) (*pageOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeWikiWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b := in.Body
			title := cleanTitle(b.Title)
			icon, err := cleanIcon(b.Icon)
			if err != nil {
				return nil, err
			}
			body, err := cleanBody(b.BodyHTML)
			if err != nil {
				return nil, err
			}
			var pg WikiPage
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockTree(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM wiki_pages WHERE workspace_id = $1`, p.WorkspaceID).Scan(&n); err != nil {
					return err
				}
				if n >= maxPages {
					return httpx.E(http.StatusUnprocessableEntity, "wiki_full", "A workspace's wiki holds up to 5,000 pages. Delete pages you no longer need first.")
				}
				parent, up, err := parentRef(ctx, tx, p.WorkspaceID, b.ParentID)
				if err != nil {
					return err
				}
				if parent != nil && len(up)+2 > maxDepth {
					return tooDeep()
				}
				id := ids.New()
				if _, err := tx.Exec(ctx, `INSERT INTO wiki_pages (id, workspace_id, parent_id, title, icon, body_html, position, created_by, updated_by)
					VALUES ($1, $2, $3, $4, $5, $6, 2147483647, $7, $7)`, id, p.WorkspaceID, parent, title, icon, body, p.ActorUserID()); err != nil {
					return err
				}
				if err := place(ctx, tx, p.WorkspaceID, parent, &id, b.Position); err != nil {
					return err
				}
				if err := recordRevision(ctx, tx, p, id, title, icon, body, false); err != nil {
					return err
				}
				if pg, err = loadPage(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "wiki_page.created", "wiki_page", &id, map[string]any{"title": title, "parent_id": parent}); err != nil {
					return err
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "wiki_page.created", Data: eventPage(pg)})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &pageOut{pg}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-wiki-page", Method: http.MethodPatch, Path: "/v1/wiki/pages/{id}",
		Summary: "Update a wiki page", Tags: tags,
		Description: "Send the `version` the edit started from: when someone changed the page since, it answers `409 page_changed` and changes nothing."},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body pagePatch
		}) (*pageOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeWikiWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Page")
			if err != nil {
				return nil, err
			}
			b := in.Body
			var pg WikiPage
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var title, icon, body string
				var version int
				err := tx.QueryRow(ctx, `SELECT title, icon, body_html, version FROM wiki_pages WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
					p.WorkspaceID, id).Scan(&title, &icon, &body, &version)
				if db.IsNotFound(err) {
					return httpx.NotFound("Page")
				}
				if err != nil {
					return err
				}
				if b.Version != nil && *b.Version != version {
					return pageChanged()
				}
				next := struct{ title, icon, body string }{title, icon, body}
				if b.Title != nil {
					next.title = cleanTitle(*b.Title)
				}
				if b.Icon != nil {
					if next.icon, err = cleanIcon(*b.Icon); err != nil {
						return err
					}
				}
				if b.BodyHTML != nil {
					if next.body, err = cleanBody(*b.BodyHTML); err != nil {
						return err
					}
				}
				if next.title != title || next.icon != icon || next.body != body {
					if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET title = $3, icon = $4, body_html = $5, version = version + 1,
						updated_by = $6, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
						p.WorkspaceID, id, next.title, next.icon, next.body, p.ActorUserID()); err != nil {
						return err
					}
					if err := recordRevision(ctx, tx, p, id, next.title, next.icon, next.body, true); err != nil {
						return err
					}
				}
				if pg, err = loadPage(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				if pg.Version == version {
					return nil
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "wiki_page.updated", Data: eventPage(pg)})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &pageOut{pg}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "move-wiki-page", Method: http.MethodPost, Path: "/v1/wiki/pages/{id}/move",
		Summary: "Move a wiki page", Tags: tags,
		Description: "Puts the page, with its subpages, under another page (or at the top level) at a position. A page can't move under itself or one of its subpages."},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body pageMove
		}) (*pageOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeWikiWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Page")
			if err != nil {
				return nil, err
			}
			var pg WikiPage
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockTree(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				var old *uuid.UUID
				err := tx.QueryRow(ctx, `SELECT parent_id FROM wiki_pages WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, p.WorkspaceID, id).Scan(&old)
				if db.IsNotFound(err) {
					return httpx.NotFound("Page")
				}
				if err != nil {
					return err
				}
				parent, up, err := parentRef(ctx, tx, p.WorkspaceID, in.Body.ParentID)
				if err != nil {
					return err
				}
				if parent != nil {
					if *parent == id {
						return cycle()
					}
					for _, a := range up {
						if a.ID == id {
							return cycle()
						}
					}
				}
				_, height, err := subtree(ctx, tx, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				depth := 0
				if parent != nil {
					depth = len(up) + 1
				}
				if depth+height > maxDepth {
					return tooDeep()
				}
				if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET parent_id = $3 WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id, parent); err != nil {
					return err
				}
				pos := in.Body.Position
				if err := place(ctx, tx, p.WorkspaceID, parent, &id, &pos); err != nil {
					return err
				}
				if !sameParent(old, parent) {
					if err := place(ctx, tx, p.WorkspaceID, old, nil, nil); err != nil {
						return err
					}
				}
				if pg, err = loadPage(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "wiki_page.moved", Data: eventPage(pg)})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &pageOut{pg}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-wiki-page", Method: http.MethodDelete, Path: "/v1/wiki/pages/{id}",
		Summary: "Delete a wiki page", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Removes the page, its subpages and their history for good. Admins delete any page; agents a page when they created it and every subpage under it."},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*hd.Empty, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeWikiWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Page")
			if err != nil {
				return nil, err
			}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockTree(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				var parent *uuid.UUID
				var title string
				err := tx.QueryRow(ctx, `SELECT parent_id, title FROM wiki_pages WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
					p.WorkspaceID, id).Scan(&parent, &title)
				if db.IsNotFound(err) {
					return httpx.NotFound("Page")
				}
				if err != nil {
					return err
				}
				gone, _, err := subtree(ctx, tx, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				if err := canDelete(ctx, tx, p, gone); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM wiki_pages WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id); err != nil {
					return err
				}
				if err := place(ctx, tx, p.WorkspaceID, parent, nil, nil); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "wiki_page.deleted", "wiki_page", &id, map[string]any{"title": title, "subpages": len(gone) - 1}); err != nil {
					return err
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "wiki_page.deleted",
					Data: map[string]any{"wiki_page_id": id, "wiki_page_ids": gone}})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

// canDelete checks a delete of pages (the page first, then everything under it): admins
// and owners delete any page; agents a page when they created it and every page under it,
// so moving pages under one's own never makes them deletable; an API key with wiki:write
// any (contracts/wiki.md "Access").
func canDelete(ctx context.Context, tx pgx.Tx, p *auth.Principal, pages []uuid.UUID) error {
	if p.Kind != auth.KindUser || p.Role.AtLeast(auth.RoleAdmin) {
		return nil
	}
	var page, under bool
	if err := tx.QueryRow(ctx, `SELECT coalesce(bool_or(id = $4), false), coalesce(bool_or(id <> $4), false) FROM wiki_pages
		WHERE workspace_id = $1 AND id = ANY($2) AND created_by IS DISTINCT FROM $3`,
		p.WorkspaceID, pages, p.UserID, pages[0]).Scan(&page, &under); err != nil {
		return err
	}
	switch {
	case page:
		return httpx.E(http.StatusForbidden, "forbidden", "Someone else created this page, so only an admin can delete it.")
	case under:
		return httpx.E(http.StatusForbidden, "forbidden", "Someone else created pages under this one, so only an admin can delete it.")
	}
	return nil
}

func sameParent(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func pageChanged() error {
	return httpx.E(http.StatusConflict, "page_changed", "Someone else changed this page since you opened it. Reload it to see their changes.")
}

func cycle() error {
	return httpx.E(http.StatusUnprocessableEntity, "page_cycle", "A page can't move under itself or one of its subpages.")
}

func tooDeep() error {
	return httpx.E(http.StatusUnprocessableEntity, "page_too_deep", "Pages nest up to 10 levels deep.")
}
