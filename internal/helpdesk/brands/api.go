package brands

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
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
	"github.com/useopendesk/opendesk/internal/images"
	"github.com/useopendesk/opendesk/internal/jobs"
)

type helpCenterIn struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Title   *string `json:"title,omitempty" maxLength:"200" doc:"Up to 100 characters; empty = the brand name"`
}

type brandCreate struct {
	Name       string        `json:"name" maxLength:"200"`
	Slug       *string       `json:"slug,omitempty" maxLength:"100" doc:"3..40 of a-z, 0-9 and -; derived from the name when absent"`
	Color      *string       `json:"color,omitempty" maxLength:"7" doc:"#rrggbb, or empty for the workspace brand_color"`
	Locale     *string       `json:"locale,omitempty" maxLength:"35"`
	HelpCenter *helpCenterIn `json:"help_center,omitempty"`
}

type brandPatch struct {
	Name       *string       `json:"name,omitempty" maxLength:"200"`
	Slug       *string       `json:"slug,omitempty" maxLength:"100"`
	Color      *string       `json:"color,omitempty" maxLength:"7"`
	Locale     *string       `json:"locale,omitempty" maxLength:"35"`
	HelpCenter *helpCenterIn `json:"help_center,omitempty"`
	Position   *int          `json:"position,omitempty" minimum:"0" maximum:"10000"`
}

type brandOut struct{ Body Brand }

// Register adds the brand routes and publishes brand.updated when a brand image changes.
func Register(d *deps.Deps, api huma.API, _ chi.Router) {
	cfg := d.Cfg
	images.SetHooks(images.Hooks{Brand: func(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID) error {
		return Publish(ctx, tx, cfg, wid, id, "brand.updated")
	}})
	h := &handler{d: d}
	h.register(api)
}

type handler struct{ d *deps.Deps }

func op(id, method, path, summary string, status int) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"Brands"}, DefaultStatus: status}
}

func (h *handler) load(ctx context.Context, q db.Querier, wid, id uuid.UUID) (Brand, error) {
	b, err := Load(ctx, q, h.d.Cfg, wid, id)
	if db.IsNotFound(err) {
		return b, httpx.NotFound("Brand")
	}
	return b, err
}

// nameTaken maps the per-workspace name constraint to its answer.
func nameTaken(err error) error {
	if db.IsUniqueViolation(err, "brands_workspace_id_name_key") {
		return httpx.E(http.StatusConflict, "name_taken", "A brand with this name already exists.")
	}
	if db.IsUniqueViolation(err, "brands_slug_key") {
		return ErrSlugTaken
	}
	return err
}

func (h *handler) register(api huma.API) {
	huma.Register(api, op("list-brands", http.MethodGet, "/v1/brands", "List brands", 0),
		func(ctx context.Context, _ *struct{}) (*struct{ Body httpx.Page[Brand] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := List(ctx, h.d.DB.Pool, h.d.Cfg, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[Brand] }{httpx.Page[Brand]{Data: list}}, nil
		})

	huma.Register(api, op("get-brand", http.MethodGet, "/v1/brands/{id}", "Get a brand", 0),
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*brandOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Brand")
			if err != nil {
				return nil, err
			}
			b, err := h.load(ctx, h.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &brandOut{b}, nil
		})

	huma.Register(api, op("create-brand", http.MethodPost, "/v1/brands", "Create a brand", http.StatusCreated),
		func(ctx context.Context, in *struct{ Body brandCreate }) (*brandOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			body := in.Body
			name, err := cleanBrandName(body.Name)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			color, locale, title, enabled := "", "", "", true
			if body.Color != nil {
				if color, err = cleanColor(*body.Color); err != nil {
					return nil, err
				}
			}
			if body.Locale != nil {
				if locale, err = cleanLocale(*body.Locale); err != nil {
					return nil, err
				}
			}
			if hc := body.HelpCenter; hc != nil {
				if hc.Enabled != nil {
					enabled = *hc.Enabled
				}
				if hc.Title != nil {
					if title, err = cleanTitle(*hc.Title); err != nil {
						return nil, err
					}
				}
			}
			var b Brand
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				// Serializes brand writes of the workspace, so the limit holds under concurrency.
				if _, err := tx.Exec(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR UPDATE`, p.WorkspaceID); err != nil {
					return err
				}
				var n, pos int
				if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(max(position), 0) + 1 FROM brands WHERE workspace_id = $1`,
					p.WorkspaceID).Scan(&n, &pos); err != nil {
					return err
				}
				if n >= MaxBrands {
					return httpx.E(http.StatusUnprocessableEntity, "limit_reached", "A workspace can have at most 50 brands.")
				}
				slug, err := pickSlug(ctx, tx, body.Slug, name, nil)
				if err != nil {
					return err
				}
				id := ids.New()
				if _, err := tx.Exec(ctx, `INSERT INTO brands (id, workspace_id, name, slug, color, locale, help_center_enabled, help_center_title, position)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, p.WorkspaceID, name, slug, color, locale, enabled, title, pos); err != nil {
					return nameTaken(err)
				}
				if b, err = Load(ctx, tx, h.d.Cfg, p.WorkspaceID, id); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "brand.created", "brand", &id, map[string]any{"name": name, "slug": slug}); err != nil {
					return err
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "brand.created", Data: map[string]any{"brand": b}})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &brandOut{b}, nil
		})

	huma.Register(api, op("update-brand", http.MethodPatch, "/v1/brands/{id}", "Update a brand", 0),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body brandPatch
		}) (*brandOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Brand")
			if err != nil {
				return nil, err
			}
			body := in.Body
			var b Brand
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var cur struct {
					name, slug, color, locale, title string
					enabled, isDefault               bool
					position                         int
				}
				err := tx.QueryRow(ctx, `SELECT name, slug, color, locale, help_center_title, help_center_enabled, is_default, position
					FROM brands WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, p.WorkspaceID, id).
					Scan(&cur.name, &cur.slug, &cur.color, &cur.locale, &cur.title, &cur.enabled, &cur.isDefault, &cur.position)
				if db.IsNotFound(err) {
					return httpx.NotFound("Brand")
				}
				if err != nil {
					return err
				}
				var changed []string
				if body.Name != nil {
					n, err := cleanBrandName(*body.Name)
					if err != nil {
						return err
					}
					if n != cur.name {
						cur.name = n
						changed = append(changed, "name")
					}
				}
				if body.Slug != nil {
					s, err := CleanSlug(*body.Slug)
					if err != nil {
						return err
					}
					if s != cur.slug {
						taken, err := SlugTaken(ctx, tx, s, &id)
						if err != nil {
							return err
						}
						if taken {
							return ErrSlugTaken
						}
						cur.slug = s
						changed = append(changed, "slug")
					}
				}
				if body.Color != nil {
					c, err := cleanColor(*body.Color)
					if err != nil {
						return err
					}
					if c != cur.color {
						cur.color = c
						changed = append(changed, "color")
					}
				}
				if body.Locale != nil {
					l, err := cleanLocale(*body.Locale)
					if err != nil {
						return err
					}
					if l != cur.locale {
						cur.locale = l
						changed = append(changed, "locale")
					}
				}
				if hc := body.HelpCenter; hc != nil {
					if hc.Enabled != nil && *hc.Enabled != cur.enabled {
						cur.enabled = *hc.Enabled
						changed = append(changed, "help_center.enabled")
					}
					if hc.Title != nil {
						t, err := cleanTitle(*hc.Title)
						if err != nil {
							return err
						}
						if t != cur.title {
							cur.title = t
							changed = append(changed, "help_center.title")
						}
					}
				}
				if body.Position != nil && *body.Position != cur.position {
					cur.position = *body.Position
					changed = append(changed, "position")
				}
				if len(changed) > 0 {
					if _, err := tx.Exec(ctx, `UPDATE brands SET name = $3, slug = $4, color = $5, locale = $6, help_center_title = $7,
						help_center_enabled = $8, position = $9, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
						p.WorkspaceID, id, cur.name, cur.slug, cur.color, cur.locale, cur.title, cur.enabled, cur.position); err != nil {
						return nameTaken(err)
					}
					if cur.isDefault {
						// The workspace's help center settings read the default brand.
						if _, err := tx.Exec(ctx, `UPDATE workspaces SET settings = settings || jsonb_build_object('help_center_enabled', $2::boolean,
							'help_center_title', $3::text), updated_at = now() WHERE id = $1`, p.WorkspaceID, cur.enabled, cur.title); err != nil {
							return err
						}
					}
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "brand.updated", "brand", &id, map[string]any{"changes": changed}); err != nil {
						return err
					}
				}
				if b, err = Load(ctx, tx, h.d.Cfg, p.WorkspaceID, id); err != nil {
					return err
				}
				if len(changed) == 0 {
					return nil
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "brand.updated", Data: map[string]any{"brand": b}})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &brandOut{b}, nil
		})

	huma.Register(api, op("delete-brand", http.MethodDelete, "/v1/brands/{id}", "Delete a brand", http.StatusNoContent),
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*hd.Empty, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Brand")
			if err != nil {
				return nil, err
			}
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				return h.delete(ctx, tx, p, id)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	huma.Register(api, op("set-default-brand", http.MethodPost, "/v1/brands/{id}/default", "Make a brand the default", 0),
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*brandOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Brand")
			if err != nil {
				return nil, err
			}
			var b Brand
			err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR UPDATE`, p.WorkspaceID); err != nil {
					return err
				}
				var isDefault bool
				var enabled bool
				var title string
				err := tx.QueryRow(ctx, `SELECT is_default, help_center_enabled, help_center_title FROM brands WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, id).Scan(&isDefault, &enabled, &title)
				if db.IsNotFound(err) {
					return httpx.NotFound("Brand")
				}
				if err != nil {
					return err
				}
				if !isDefault {
					var old uuid.UUID
					err := tx.QueryRow(ctx, `UPDATE brands SET is_default = false, updated_at = now() WHERE workspace_id = $1 AND is_default RETURNING id`,
						p.WorkspaceID).Scan(&old)
					if err != nil && !db.IsNotFound(err) {
						return err
					}
					if _, err := tx.Exec(ctx, `UPDATE brands SET is_default = true, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
						p.WorkspaceID, id); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `UPDATE workspaces SET settings = settings || jsonb_build_object('help_center_enabled', $2::boolean,
						'help_center_title', $3::text), updated_at = now() WHERE id = $1`, p.WorkspaceID, enabled, title); err != nil {
						return err
					}
					if err := mirrorPrimaryWidget(ctx, tx, p.WorkspaceID); err != nil {
						return err
					}
					// The workspace's widget key, help center settings and pictures read the default brand.
					if err := PublishWorkspace(ctx, tx, p.WorkspaceID); err != nil {
						return err
					}
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "brand.default_changed", "brand", &id, map[string]any{"from_brand_id": old}); err != nil {
						return err
					}
					if old != uuid.Nil {
						if err := Publish(ctx, tx, h.d.Cfg, p.WorkspaceID, old, "brand.updated"); err != nil {
							return err
						}
					}
				}
				if b, err = Load(ctx, tx, h.d.Cfg, p.WorkspaceID, id); err != nil {
					return err
				}
				if isDefault {
					return nil
				}
				_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "brand.updated", Data: map[string]any{"brand": b}})
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &brandOut{b}, nil
		})

	slots := "logo, help_center_logo or help_center_favicon"
	huma.Register(api, images.UploadOperation(api, huma.Operation{OperationID: "upload-brand-image", Method: http.MethodPut,
		Path: "/v1/brands/{id}/images/{slot}", Summary: "Upload a brand image", Tags: []string{"Brands"},
		Description: "slot: " + slots + ". PNG, JPEG, GIF or WebP, at most 5 MB."}),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Slot string `path:"slot" enum:"logo,help_center_logo,help_center_favicon"`
			images.UploadInput
		}) (*brandOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Brand")
			if err != nil {
				return nil, err
			}
			if !h.d.Limiter.Allow("brandimage:"+p.WorkspaceID.String(), 60, time.Hour) {
				return nil, httpx.RateLimited()
			}
			img, err := images.ReadUpload(&in.UploadInput, images.Contain)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			o := images.Owner{Type: images.OwnerBrand, ID: id, WorkspaceID: &p.WorkspaceID, Slot: in.Slot}
			err = images.Set(ctx, h.d, o, img, "upload", images.SetOptions{After: func(ctx context.Context, tx pgx.Tx) error {
				return audit.Log(ctx, tx, p, p.WorkspaceID, "brand.image_updated", "brand", &id, map[string]any{"slot": in.Slot})
			}})
			return h.imageResult(ctx, p.WorkspaceID, id, err)
		})

	huma.Register(api, op("delete-brand-image", http.MethodDelete, "/v1/brands/{id}/images/{slot}", "Remove a brand image", 0),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Slot string `path:"slot" enum:"logo,help_center_logo,help_center_favicon"`
		}) (*brandOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := hd.ParseID(in.ID, "Brand")
			if err != nil {
				return nil, err
			}
			o := images.Owner{Type: images.OwnerBrand, ID: id, WorkspaceID: &p.WorkspaceID, Slot: in.Slot}
			err = images.Clear(ctx, h.d, o, false, images.SetOptions{After: func(ctx context.Context, tx pgx.Tx) error {
				return audit.Log(ctx, tx, p, p.WorkspaceID, "brand.image_removed", "brand", &id, map[string]any{"slot": in.Slot})
			}})
			return h.imageResult(ctx, p.WorkspaceID, id, err)
		})
}

func (h *handler) imageResult(ctx context.Context, wid, id uuid.UUID, err error) (*brandOut, error) {
	if errors.Is(err, images.ErrOwnerGone) {
		return nil, httpx.NotFound("Brand")
	}
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	b, err := h.load(ctx, h.d.DB.Pool, wid, id)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	return &brandOut{b}, nil
}

// delete removes a brand with its widgets, collections and articles. The default brand
// and a brand with addresses stay. Its tickets move to the default brand.
func (h *handler) delete(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) error {
	wid := p.WorkspaceID
	var isDefault bool
	var name string
	err := tx.QueryRow(ctx, `SELECT is_default, name FROM brands WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, wid, id).Scan(&isDefault, &name)
	if db.IsNotFound(err) {
		return httpx.NotFound("Brand")
	}
	if err != nil {
		return err
	}
	if isDefault {
		return httpx.E(http.StatusConflict, "default_brand", "The default brand cannot be deleted. Make another brand the default first.")
	}
	var inUse bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes m WHERE m.workspace_id = $1 AND (m.brand_id = $2
		OR EXISTS (SELECT 1 FROM jsonb_each(m.alias_settings) a WHERE a.value ->> 'brand_id' = $2::text)))`, wid, id).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return httpx.E(http.StatusConflict, "brand_in_use", "Move or remove this brand's addresses and aliases first.")
	}
	def, err := DefaultID(ctx, tx, wid)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tickets SET brand_id = $3 WHERE workspace_id = $1 AND brand_id = $2`, wid, id, def); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `DELETE FROM kb_articles WHERE workspace_id = $1 AND (brand_id = $2
		OR collection_id IN (SELECT k.id FROM kb_collections k WHERE k.workspace_id = $1 AND k.brand_id = $2)) RETURNING id`, wid, id)
	if err != nil {
		return err
	}
	articles, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, a := range articles {
		if err := hd.EnqueueAIEmbed(ctx, tx, h.d.Jobs, h.d.Cfg, wid, "article", a); err != nil {
			return err
		}
		if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "kb_article.deleted", Data: map[string]any{"kb_article_id": a}}); err != nil {
			return err
		}
	}
	rows, err = tx.Query(ctx, `DELETE FROM kb_collections WHERE workspace_id = $1 AND brand_id = $2 RETURNING id`, wid, id)
	if err != nil {
		return err
	}
	collections, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, c := range collections {
		if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "kb_collection.deleted", Data: map[string]any{"kb_collection_id": c}}); err != nil {
			return err
		}
	}
	rows, err = tx.Query(ctx, `DELETE FROM widgets WHERE workspace_id = $1 AND brand_id = $2 RETURNING id`, wid, id)
	if err != nil {
		return err
	}
	widgets, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, g := range widgets {
		if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "widget.deleted", Data: map[string]any{"widget_id": g}}); err != nil {
			return err
		}
	}
	var provider *string
	if err := tx.QueryRow(ctx, `SELECT provider_id FROM help_domains WHERE workspace_id = $1 AND brand_id = $2`, wid, id).Scan(&provider); err != nil && !db.IsNotFound(err) {
		return err
	}
	if provider != nil {
		// Its custom domain's certificate is released (contracts/teams.md "Help center addresses").
		if _, err := h.d.Jobs.InsertTx(ctx, tx, jobs.HelpDomainReleaseArgs{ProviderID: *provider}, nil); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM brands WHERE workspace_id = $1 AND id = $2`, wid, id); err != nil {
		return err
	}
	if err := audit.Log(ctx, tx, p, wid, "brand.deleted", "brand", &id, map[string]any{"name": name, "widgets": len(widgets),
		"collections": len(collections), "articles": len(articles)}); err != nil {
		return err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "brand.deleted", Data: map[string]any{"brand_id": id}})
	return err
}
