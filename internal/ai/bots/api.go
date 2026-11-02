package bots

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/images"
)

// The /v1/bots API and the public avatar endpoints (contracts/bots.md "Endpoints").

// Register wires the bots routes.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	h := &handler{d: d}
	images.SetHooks(images.Hooks{Bot: func(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID) error {
		_, err := publish(ctx, tx, wid, id, "bot.updated")
		return err
	}})
	h.register(api)
	r.Get("/v1/public/bots/{id}/avatar.svg", h.avatar(false))
	r.Get("/v1/public/bots/{id}/avatar.png", h.avatar(true))
}

// RegisterWorkers, Periodic and Background: bots have none.
func RegisterWorkers(*deps.Deps, *river.Workers) {}
func Periodic(*deps.Deps) []*river.PeriodicJob   { return nil }
func Background(context.Context, *deps.Deps)     {}

type handler struct{ d *deps.Deps }

// BotInput is the body of POST /v1/bots.
type BotInput struct {
	Name        string            `json:"name" maxLength:"200"`
	Role        *string           `json:"role,omitempty" maxLength:"400"`
	Language    *string           `json:"language,omitempty" maxLength:"40"`
	Look        json.RawMessage   `json:"look,omitempty" doc:"BotLook (contracts/bots.md); every key, from its allow-list"`
	Personality *PersonalityPatch `json:"personality,omitempty"`
}

// BotPatch is the body of PATCH /v1/bots/{id}.
type BotPatch struct {
	Name        *string           `json:"name,omitempty" maxLength:"200"`
	Role        *string           `json:"role,omitempty" maxLength:"400"`
	Language    *string           `json:"language,omitempty" maxLength:"40"`
	Look        json.RawMessage   `json:"look,omitempty" doc:"BotLook, replaced whole"`
	Personality *PersonalityPatch `json:"personality,omitempty" doc:"Keys given replace the bot's"`
}

type botOut struct{ Body Bot }
type botList struct{ Body httpx.Page[Bot] }
type idPath struct {
	ID string `path:"id"`
}

func op(id, method, path, summary string, status int) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"Bots"}, DefaultStatus: status}
}

func parseID(raw string) (uuid.UUID, error) {
	id, ok := ids.Parse(raw)
	if !ok {
		return uuid.Nil, httpx.NotFound("Bot")
	}
	return id, nil
}

// publish loads the bot in tx and publishes typ with it.
func publish(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID, typ string) (Bot, error) {
	b, err := Get(ctx, tx, wid, id)
	if err != nil {
		return b, err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Data: map[string]any{"bot": b}})
	return b, err
}

func encodeLook(l Look) ([]byte, error) { return json.Marshal(l) }

// lookFor validates a look for a bot: an image look needs an uploaded picture.
func lookFor(raw json.RawMessage, avatar domain.Avatar) (Look, error) {
	l, err := ParseLook(raw)
	if err != nil {
		return l, err
	}
	if l.Kind == "image" && avatar.URL() == "" {
		return l, httpx.Validation("look.kind image needs an uploaded picture: PUT /v1/bots/{id}/image")
	}
	return l, nil
}

func (h *handler) register(api huma.API) {
	huma.Register(api, op("list-bots", http.MethodGet, "/v1/bots", "List bots", 0),
		func(ctx context.Context, _ *struct{}) (*botList, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := List(ctx, h.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if len(list) == 0 {
				// A workspace made before it had bots gets its default one now.
				if err := h.d.DB.Tx(ctx, func(tx pgx.Tx) error { return EnsureDefault(ctx, tx, p.WorkspaceID) }); err != nil {
					return nil, httpx.Wrap(err)
				}
				if list, err = List(ctx, h.d.DB.Pool, p.WorkspaceID); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			return &botList{httpx.Page[Bot]{Data: list}}, nil
		})

	huma.Register(api, op("get-bot", http.MethodGet, "/v1/bots/{id}", "Get a bot", 0),
		func(ctx context.Context, in *idPath) (*botOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			b, err := Get(ctx, h.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botOut{b}, nil
		})

	huma.Register(api, op("create-bot", http.MethodPost, "/v1/bots", "Create a bot", http.StatusCreated),
		func(ctx context.Context, in *struct{ Body BotInput }) (*botOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b, err := h.create(ctx, p, in.Body)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botOut{b}, nil
		})

	huma.Register(api, op("update-bot", http.MethodPatch, "/v1/bots/{id}", "Update a bot", 0),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body BotPatch
		}) (*botOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			b, err := h.update(ctx, p, id, in.Body)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botOut{b}, nil
		})

	huma.Register(api, op("delete-bot", http.MethodDelete, "/v1/bots/{id}", "Delete a bot", http.StatusNoContent),
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			if err := h.remove(ctx, p, id); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("duplicate-bot", http.MethodPost, "/v1/bots/{id}/duplicate", "Duplicate a bot", http.StatusCreated),
		func(ctx context.Context, in *idPath) (*botOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			b, err := h.duplicate(ctx, p, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botOut{b}, nil
		})

	huma.Register(api, op("set-default-bot", http.MethodPost, "/v1/bots/{id}/default", "Make a bot the default", 0),
		func(ctx context.Context, in *idPath) (*botOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			b, err := h.setDefault(ctx, p, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botOut{b}, nil
		})

	huma.Register(api, op("reorder-bots", http.MethodPost, "/v1/bots/reorder", "Reorder bots", 0),
		func(ctx context.Context, in *struct {
			Body struct {
				IDs []string `json:"ids" maxItems:"100" doc:"Every bot of the workspace once, in order"`
			}
		}) (*botList, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := h.reorder(ctx, p, in.Body.IDs)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botList{httpx.Page[Bot]{Data: list}}, nil
		})

	huma.Register(api, images.UploadOperation(api, op("upload-bot-image", http.MethodPut, "/v1/bots/{id}/image", "Upload a bot's picture", 0)),
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
			images.UploadInput
		}) (*botOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			if !h.d.Limiter.Allow("botimage:"+p.WorkspaceID.String(), 60, time.Hour) {
				return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many uploads. Try again later.")
			}
			if _, err := Get(ctx, h.d.DB.Pool, p.WorkspaceID, id); err != nil {
				return nil, httpx.Wrap(err)
			}
			img, err := images.ReadUpload(&in.UploadInput, images.Cover)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			o := images.Owner{Type: images.OwnerBot, ID: id, WorkspaceID: &p.WorkspaceID, Slot: images.SlotAvatar}
			if err := images.Set(ctx, h.d, o, img, "upload", images.SetOptions{After: func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE bots SET look = jsonb_set(look, '{kind}', '"image"') WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, id); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "bot.image_updated", "bot", &id, nil)
			}}); err != nil {
				return nil, httpx.Wrap(err)
			}
			b, err := Get(ctx, h.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botOut{b}, nil
		})

	huma.Register(api, op("delete-bot-image", http.MethodDelete, "/v1/bots/{id}/image", "Remove a bot's picture", 0),
		func(ctx context.Context, in *idPath) (*botOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			if _, err := Get(ctx, h.d.DB.Pool, p.WorkspaceID, id); err != nil {
				return nil, httpx.Wrap(err)
			}
			o := images.Owner{Type: images.OwnerBot, ID: id, WorkspaceID: &p.WorkspaceID, Slot: images.SlotAvatar}
			if err := images.Clear(ctx, h.d, o, false, images.SetOptions{After: func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `UPDATE bots SET look = jsonb_set(look, '{kind}', '"built"') WHERE workspace_id = $1 AND id = $2
					AND look ->> 'kind' = 'image'`, p.WorkspaceID, id); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "bot.image_removed", "bot", &id, nil)
			}}); err != nil {
				return nil, httpx.Wrap(err)
			}
			b, err := Get(ctx, h.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &botOut{b}, nil
		})
}

func (h *handler) create(ctx context.Context, p *auth.Principal, in BotInput) (Bot, error) {
	wid := p.WorkspaceID
	name, err := cleanName(in.Name)
	if err != nil {
		return Bot{}, err
	}
	role, lang := "", ""
	if in.Role != nil {
		if role, err = plainText("role", *in.Role, roleMax, false); err != nil {
			return Bot{}, err
		}
	}
	if in.Language != nil {
		if lang, err = cleanLanguage(*in.Language); err != nil {
			return Bot{}, err
		}
	}
	look := OllieLook
	if len(in.Look) > 0 && string(in.Look) != "null" {
		if look, err = lookFor(in.Look, ""); err != nil {
			return Bot{}, err
		}
	}
	pers := DefaultPersonality()
	if in.Personality != nil {
		if pers, _, err = in.Personality.Apply(pers); err != nil {
			return Bot{}, err
		}
	}
	var out Bot
	err = h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bots:' || $1::text, 0))`, wid); err != nil {
			return err
		}
		if err := EnsureDefault(ctx, tx, wid); err != nil {
			return err
		}
		var n, last int
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(max(position), 0) FROM bots WHERE workspace_id = $1`, wid).Scan(&n, &last); err != nil {
			return err
		}
		if n >= MaxBots {
			return httpx.E(http.StatusUnprocessableEntity, "limit_reached", "A workspace can have at most 50 bots.")
		}
		id := ids.New()
		lj, _ := encodeLook(look)
		pj, _ := json.Marshal(pers)
		if _, err := tx.Exec(ctx, `INSERT INTO bots (id, workspace_id, name, role, language, look, personality, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, wid, name, role, lang, lj, pj, last+1); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "bot.created", "bot", &id, map[string]any{"name": name}); err != nil {
			return err
		}
		out, err = publish(ctx, tx, wid, id, "bot.created")
		return err
	})
	return out, err
}

func (h *handler) update(ctx context.Context, p *auth.Principal, id uuid.UUID, in BotPatch) (Bot, error) {
	wid := p.WorkspaceID
	var out Bot
	err := h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		b, err := load(ctx, tx, wid, `b.id = $2 FOR UPDATE`, id)
		if db.IsNotFound(err) {
			return httpx.NotFound("Bot")
		}
		if err != nil {
			return err
		}
		var changed []string
		if in.Name != nil {
			n, err := cleanName(*in.Name)
			if err != nil {
				return err
			}
			if n != b.Name {
				b.Name, changed = n, append(changed, "name")
			}
		}
		if in.Role != nil {
			r, err := plainText("role", *in.Role, roleMax, false)
			if err != nil {
				return err
			}
			if r != b.Role {
				b.Role, changed = r, append(changed, "role")
			}
		}
		if in.Language != nil {
			l, err := cleanLanguage(*in.Language)
			if err != nil {
				return err
			}
			if l != b.Language {
				b.Language, changed = l, append(changed, "language")
			}
		}
		if len(in.Look) > 0 && string(in.Look) != "null" {
			l, err := lookFor(in.Look, b.avatar)
			if err != nil {
				return err
			}
			if l != b.Look {
				b.Look, changed = l, append(changed, "look")
			}
		}
		if in.Personality != nil {
			pers, keys, err := in.Personality.Apply(b.Personality)
			if err != nil {
				return err
			}
			b.Personality, changed = pers, append(changed, keys...)
		}
		if len(changed) == 0 {
			out, err = Get(ctx, tx, wid, id)
			return err
		}
		lj, _ := encodeLook(b.Look)
		pj, _ := json.Marshal(b.Personality)
		if _, err := tx.Exec(ctx, `UPDATE bots SET name = $3, role = $4, language = $5, look = $6, personality = $7, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wid, id, b.Name, b.Role, b.Language, lj, pj); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "bot.updated", "bot", &id, map[string]any{"changes": changed}); err != nil {
			return err
		}
		out, err = publish(ctx, tx, wid, id, "bot.updated")
		return err
	})
	return out, err
}

func (h *handler) remove(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	wid := p.WorkspaceID
	var oldPrefix string
	err := h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		b, err := load(ctx, tx, wid, `b.id = $2 FOR UPDATE`, id)
		if db.IsNotFound(err) {
			return httpx.NotFound("Bot")
		}
		if err != nil {
			return err
		}
		if b.IsDefault {
			return httpx.E(http.StatusConflict, "bot_is_default", "The default bot cannot be deleted. Make another bot the default first.")
		}
		var users []string
		rows, err := tx.Query(ctx, `SELECT 'the ' || name || ' widget' FROM widgets WHERE workspace_id = $1 AND bot_id = $2
			UNION ALL SELECT email FROM mailboxes WHERE workspace_id = $1 AND bot_id = $2
			UNION ALL SELECT 'the ' || name || ' procedure' FROM ai_procedures WHERE workspace_id = $1 AND $2 = ANY(bot_ids)`, wid, id)
		if err != nil {
			return err
		}
		if users, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return err
		}
		if len(users) > 0 {
			shown := users[:min(len(users), 3)]
			more := ""
			if len(users) > 3 {
				more = " and " + strconv.Itoa(len(users)-3) + " more"
			}
			return httpx.E(http.StatusConflict, "bot_in_use", b.Name+" still answers on "+strings.Join(shown, ", ")+more+
				". Pick another bot there first.")
		}
		if oldPrefix, err = images.Forget(ctx, tx, b.avatar); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bots WHERE workspace_id = $1 AND id = $2`, wid, id); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "bot.deleted", "bot", &id, map[string]any{"name": b.Name}); err != nil {
			return err
		}
		_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "bot.deleted", Data: map[string]any{"bot_id": id}})
		return err
	})
	if err == nil && oldPrefix != "" {
		images.Discard(h.d.Storage, oldPrefix)
	}
	return err
}

func (h *handler) duplicate(ctx context.Context, p *auth.Principal, id uuid.UUID) (Bot, error) {
	wid := p.WorkspaceID
	var out Bot
	err := h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bots:' || $1::text, 0))`, wid); err != nil {
			return err
		}
		b, err := load(ctx, tx, wid, `b.id = $2`, id)
		if db.IsNotFound(err) {
			return httpx.NotFound("Bot")
		}
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM bots WHERE workspace_id = $1`, wid).Scan(&n); err != nil {
			return err
		}
		if n >= MaxBots {
			return httpx.E(http.StatusUnprocessableEntity, "limit_reached", "A workspace can have at most 50 bots.")
		}
		name := []rune(b.Name + " copy")
		if len(name) > nameMax {
			name = name[:nameMax]
		}
		look := b.Look
		if look.Kind == "image" {
			look.Kind = "built"
		}
		lj, _ := encodeLook(look)
		pj, _ := json.Marshal(b.Personality)
		nid := ids.New()
		if _, err := tx.Exec(ctx, `UPDATE bots SET position = position + 1 WHERE workspace_id = $1 AND position > $2`, wid, b.Position); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bots (id, workspace_id, name, role, language, look, personality, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, nid, wid, strings.TrimSpace(string(name)), b.Role, b.Language, lj, pj, b.Position+1); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "bot.duplicated", "bot", &nid, map[string]any{"from": id}); err != nil {
			return err
		}
		out, err = publish(ctx, tx, wid, nid, "bot.created")
		return err
	})
	return out, err
}

func (h *handler) setDefault(ctx context.Context, p *auth.Principal, id uuid.UUID) (Bot, error) {
	wid := p.WorkspaceID
	var out Bot
	err := h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		b, err := load(ctx, tx, wid, `b.id = $2 FOR UPDATE`, id)
		if db.IsNotFound(err) {
			return httpx.NotFound("Bot")
		}
		if err != nil {
			return err
		}
		if b.IsDefault {
			out, err = Get(ctx, tx, wid, id)
			return err
		}
		var prev *uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE bots SET is_default = false, updated_at = now() WHERE workspace_id = $1 AND is_default RETURNING id`,
			wid).Scan(&prev); err != nil && !db.IsNotFound(err) {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bots SET is_default = true, updated_at = now() WHERE workspace_id = $1 AND id = $2`, wid, id); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "bot.default_changed", "bot", &id, map[string]any{"from": prev}); err != nil {
			return err
		}
		if prev != nil {
			if _, err := publish(ctx, tx, wid, *prev, "bot.updated"); err != nil {
				return err
			}
		}
		out, err = publish(ctx, tx, wid, id, "bot.updated")
		return err
	})
	return out, err
}

func (h *handler) reorder(ctx context.Context, p *auth.Principal, raw []string) ([]Bot, error) {
	wid := p.WorkspaceID
	err := h.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, position FROM bots WHERE workspace_id = $1 FOR UPDATE`, wid)
		if err != nil {
			return err
		}
		cur := map[uuid.UUID]int{}
		for rows.Next() {
			var id uuid.UUID
			var pos int
			if err := rows.Scan(&id, &pos); err != nil {
				rows.Close()
				return err
			}
			cur[id] = pos
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		order := make([]uuid.UUID, 0, len(raw))
		for _, s := range raw {
			id, ok := ids.Parse(s)
			if _, known := cur[id]; !ok || !known || slices.Contains(order, id) {
				return httpx.Validation("ids must list every bot of the workspace once")
			}
			order = append(order, id)
		}
		if len(order) != len(cur) {
			return httpx.Validation("ids must list every bot of the workspace once")
		}
		moved := false
		for i, id := range order {
			if cur[id] == i {
				continue
			}
			moved = true
			if _, err := tx.Exec(ctx, `UPDATE bots SET position = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`, wid, id, i); err != nil {
				return err
			}
			if _, err := publish(ctx, tx, wid, id, "bot.updated"); err != nil {
				return err
			}
		}
		if !moved {
			return nil
		}
		return audit.Log(ctx, tx, p, wid, "bot.reordered", "bot", nil, map[string]any{"ids": order})
	})
	if err != nil {
		return nil, err
	}
	return List(ctx, h.d.DB.Pool, wid)
}

// avatar serves a bot's look as SVG or PNG (public: visitors and email clients load it).
func (h *handler) avatar(png bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		raw := chi.URLParam(r, "id")
		look, name := OllieLook, DefaultName
		if raw != "ollie" {
			id, ok := ids.Parse(raw)
			var lookRaw []byte
			var avatar string
			if ok {
				err := h.d.DB.Pool.QueryRow(r.Context(), `SELECT name, look, avatar FROM bots WHERE id = $1`, id).Scan(&name, &lookRaw, &avatar)
				ok = err == nil
				if err != nil && !db.IsNotFound(err) {
					http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
					return
				}
			}
			if !ok {
				hdr.Set("Cache-Control", "no-store")
				hdr.Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"status":404,"title":"Not Found","code":"not_found","detail":"Bot not found"}`))
				return
			}
			l, err := ParseLook(lookRaw)
			if err == nil {
				look = l
			}
			if u := domain.Avatar(avatar).URL(); u != "" && look.Kind == "image" {
				if s := r.URL.Query().Get("s"); png && s == "64" {
					u += "?s=64"
				}
				hdr.Set("Cache-Control", "public, max-age=300")
				http.Redirect(w, r, u, http.StatusFound)
				return
			}
		}
		hdr.Set("Cache-Control", "public, max-age=3600")
		if !png {
			hdr.Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(RenderSVG(look)))
			return
		}
		size := 256
		switch r.URL.Query().Get("s") {
		case "64":
			size = 64
		case "128":
			size = 128
		}
		initial := ""
		if rs := []rune(strings.TrimSpace(name)); len(rs) > 0 {
			initial = strings.ToUpper(string(rs[0]))
		}
		b, err := RenderPNG(look, size, initial)
		if err != nil {
			http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
			return
		}
		hdr.Set("Content-Type", "image/png")
		_, _ = w.Write(b)
	}
}

// Assignment is a bot_id body field of a widget or mailbox patch: absent leaves it as it
// is, null or "" makes it follow the default bot, an id picks that bot (checked with ParseRef).
type Assignment struct {
	Set   bool
	Value *string
}

func (a *Assignment) UnmarshalJSON(b []byte) error {
	a.Set = true
	if string(b) == "null" {
		a.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	a.Value = &s
	return nil
}

// Schema is a nullable string.
func (Assignment) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Nullable: true, Description: "A bot of the workspace, or null for the default bot"}
}

// Same reports whether two bot references are the same (both nil counts).
func Same(a, b *uuid.UUID) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}
