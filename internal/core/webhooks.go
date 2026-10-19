package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/version"
)

const (
	webhookMaxAttempts  = 10
	webhookResponseKeep = 2048
	webhookTestEvent    = "webhook.test"
)

type CoreWebhook struct {
	ID          uuid.UUID `json:"id"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	Events      []string  `json:"events"`
	Enabled     bool      `json:"enabled"`
	// PrivateTeamIDs are the private teams whose events this webhook receives
	// (contracts/teams.md "Visibility").
	PrivateTeamIDs []uuid.UUID `json:"private_team_ids"`
	// PrivateTeams are the same teams, by name, with who granted each.
	PrivateTeams []PrivateTeamGrant `json:"private_teams"`
	CreatedAt    time.Time          `json:"created_at"`
}

type CoreWebhookWithSecret struct {
	CoreWebhook
	Secret string `json:"secret" doc:"Signing secret. Shown once."`
}

type CoreWebhookDelivery struct {
	ID           uuid.UUID `json:"id"`
	EventID      uuid.UUID `json:"event_id"`
	EventType    string    `json:"event_type"`
	Status       string    `json:"status" enum:"pending,succeeded,failed"`
	Attempts     int       `json:"attempts"`
	ResponseCode *int      `json:"response_code"`
	DurationMS   *int      `json:"duration_ms"`
	CreatedAt    time.Time `json:"created_at"`
}

var webhookSelect = `SELECT id, url, description, events, enabled, created_at, ` +
	grantedTeamsSQL("webhooks.private_team_ids", "webhooks.workspace_id") + `, ` +
	teamGrantsSQL("webhooks.private_team_ids", "webhooks.workspace_id", grantWebhook, "webhooks.id") + ` FROM webhooks`

func scanWebhook(r pgx.Row) (CoreWebhook, error) {
	var w CoreWebhook
	var grants []byte
	if err := r.Scan(&w.ID, &w.URL, &w.Description, &w.Events, &w.Enabled, &w.CreatedAt, &w.PrivateTeamIDs, &grants); err != nil {
		return w, err
	}
	if w.PrivateTeamIDs == nil {
		w.PrivateTeamIDs = []uuid.UUID{}
	}
	var err error
	w.PrivateTeams, err = scanGrants(grants)
	return w, err
}

func loadWebhook(ctx context.Context, q db.Querier, wid, id uuid.UUID) (CoreWebhook, error) {
	return scanWebhook(q.QueryRow(ctx, webhookSelect+` WHERE workspace_id = $1 AND id = $2`, wid, id))
}

var eventTypeRe = regexp.MustCompile(`^[a-z][a-z_]*(\.[a-z_*]+)+$`)

// validEvents normalizes an events filter: ["*"] or a list of event types.
func validEvents(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{"*"}, nil
	}
	if len(in) > 100 {
		return nil, httpx.Validation("Choose at most 100 event types.")
	}
	out := make([]string, 0, len(in))
	for _, e := range in {
		if e == "*" {
			return []string{"*"}, nil
		}
		if len(e) > 64 || !eventTypeRe.MatchString(e) {
			return nil, httpx.Validation("Unknown event type: " + truncate(e, 64))
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	slices.Sort(out)
	return out, nil
}

// eventMatches reports whether a webhook filter selects an event type.
func eventMatches(filter []string, typ string) bool {
	return slices.Contains(filter, "*") || slices.Contains(filter, typ)
}

// webhookSeesTeam reports whether a webhook granted teams receives an event of team (nil for
// events outside a private team).
func webhookSeesTeam(teams []uuid.UUID, team *uuid.UUID) bool {
	return team == nil || slices.Contains(teams, *team)
}

// signWebhook returns the OD-Signature header value for body at t.
func signWebhook(secret []byte, t int64, body []byte) string {
	ts := strconv.FormatInt(t, 10)
	return "t=" + ts + ",v1=" + crypto.HMACHex(secret, ts+"."+string(body))
}

// webhookPayload is the envelope a webhook receives: the event minus topics.
type webhookPayload struct {
	ID          uuid.UUID       `json:"id"`
	Seq         int64           `json:"seq"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	Type        string          `json:"type"`
	Visibility  string          `json:"visibility"`
	Data        json.RawMessage `json:"data"`
	OccurredAt  time.Time       `json:"occurred_at"`
}

func (s *svc) validateWebhookURL(ctx context.Context, raw string) (string, error) {
	u, err := outboundGuard{dev: s.devURL}.validateURL(ctx, raw)
	if err != nil {
		return "", httpx.E(422, "invalid_url", err.Error())
	}
	return u, nil
}

func (s *svc) registerWebhooks(api huma.API) {
	tags := []string{"Webhooks"}

	register(s, api, huma.Operation{OperationID: "list-webhooks", Method: http.MethodGet, Path: "/v1/webhooks",
		Summary: "List webhooks", Tags: tags},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[CoreWebhook] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var cur struct {
				T  time.Time `json:"t"`
				ID uuid.UUID `json:"id"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			rows, err := s.d.DB.Pool.Query(ctx, webhookSelect+` WHERE workspace_id = $1 AND (created_at, id) > ($2, $3)
				ORDER BY created_at, id LIMIT $4`, p.WorkspaceID, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreWebhook, error) { return scanWebhook(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[CoreWebhook] }{httpx.NewPage(list, in.Limit, func(w CoreWebhook) any {
				return map[string]any{"t": w.CreatedAt, "id": w.ID}
			})}, nil
		})

	register(s, api, huma.Operation{OperationID: "get-webhook", Method: http.MethodGet, Path: "/v1/webhooks/{id}",
		Summary: "Get a webhook", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body CoreWebhook }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Webhook")
			if err != nil {
				return nil, err
			}
			w, err := loadWebhook(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreWebhook }{w}, nil
		})

	register(s, api, huma.Operation{OperationID: "create-webhook", Method: http.MethodPost, Path: "/v1/webhooks",
		Summary: "Create a webhook", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				URL         string   `json:"url" maxLength:"2000"`
				Description string   `json:"description,omitempty" maxLength:"500"`
				Events      []string `json:"events,omitempty" maxItems:"100"`
				Enabled     *bool    `json:"enabled,omitempty"`
				// PrivateTeamIDs: private teams whose events it receives; only their members add them.
				PrivateTeamIDs []string `json:"private_team_ids,omitempty" maxItems:"200"`
			}
		}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         CoreWebhookWithSecret
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.limit("webhook:write:"+p.WorkspaceID.String(), 60, time.Hour); err != nil {
				return nil, err
			}
			u, err := s.validateWebhookURL(ctx, in.Body.URL)
			if err != nil {
				return nil, err
			}
			evs, err := validEvents(in.Body.Events)
			if err != nil {
				return nil, err
			}
			enabled := in.Body.Enabled == nil || *in.Body.Enabled
			secret := "whsec_" + crypto.RandomToken(32)
			sealed, err := s.d.Box.Seal([]byte(secret))
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var w CoreWebhook
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhooks WHERE workspace_id = $1`, p.WorkspaceID).Scan(&n); err != nil {
					return err
				}
				if n >= 50 {
					return httpx.Conflict("A workspace can have at most 50 webhooks.")
				}
				teams, err := checkPrivateTeams(ctx, tx, p, nil, in.Body.PrivateTeamIDs)
				if err != nil {
					return err
				}
				id := ids.New()
				if _, err := tx.Exec(ctx, `INSERT INTO webhooks (id, workspace_id, url, description, secret_enc, events, enabled, created_by, private_team_ids)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, p.WorkspaceID, u, cleanDescription(in.Body.Description), sealed, evs, enabled,
					p.ActorUserID(), teams); err != nil {
					return err
				}
				if err := saveGrants(ctx, tx, p, grantWebhook, id, teams); err != nil {
					return err
				}
				if w, err = loadWebhook(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "webhook.created", Data: map[string]any{"webhook": w}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "webhook.created", "webhook", &id,
					map[string]any{"url": u, "events": evs, "private_team_ids": uuidStrings(teams)})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			distinct := p.WorkspaceID.String()
			if p.Kind == auth.KindUser {
				distinct = p.UserID.String()
			}
			s.d.Analytics.Capture(distinct, p.WorkspaceID.String(), "webhook_created", map[string]any{"all_events": evs[0] == "*", "event_count": len(evs)})
			return &struct {
				CacheControl string `header:"Cache-Control"`
				Body         CoreWebhookWithSecret
			}{"no-store", CoreWebhookWithSecret{CoreWebhook: w, Secret: secret}}, nil
		})

	register(s, api, huma.Operation{OperationID: "update-webhook", Method: http.MethodPatch, Path: "/v1/webhooks/{id}",
		Summary: "Update a webhook", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				URL         *string   `json:"url,omitempty" maxLength:"2000"`
				Description *string   `json:"description,omitempty" maxLength:"500"`
				Events      *[]string `json:"events,omitempty" maxItems:"100"`
				Enabled     *bool     `json:"enabled,omitempty"`
				// PrivateTeamIDs replaces the list. Only a team's members add it; admins may remove any.
				PrivateTeamIDs *[]string `json:"private_team_ids,omitempty" maxItems:"200"`
			}
		}) (*struct{ Body CoreWebhook }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Webhook")
			if err != nil {
				return nil, err
			}
			var u, desc *string
			var evs []string
			if in.Body.URL != nil {
				v, err := s.validateWebhookURL(ctx, *in.Body.URL)
				if err != nil {
					return nil, err
				}
				u = &v
			}
			if in.Body.Description != nil {
				desc = ptr(cleanDescription(*in.Body.Description))
			}
			if in.Body.Events != nil {
				if evs, err = validEvents(*in.Body.Events); err != nil {
					return nil, err
				}
			}
			var w CoreWebhook
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var current, curEvents []string
				var curURL string
				var curEnabled bool
				err := tx.QueryRow(ctx, `SELECT private_team_ids::text[], url, events, enabled FROM webhooks
					WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, id, p.WorkspaceID).Scan(&current, &curURL, &curEvents, &curEnabled)
				if db.IsNotFound(err) {
					return httpx.NotFound("Webhook")
				}
				if err != nil {
					return err
				}
				var teams []uuid.UUID
				if in.Body.PrivateTeamIDs != nil {
					if teams, err = checkPrivateTeams(ctx, tx, p, current, *in.Body.PrivateTeamIDs); err != nil {
						return err
					}
				}
				// A webhook granted private teams sends their tickets to its URL: only someone on
				// every one of those teams may point it elsewhere, widen its events or turn it back on.
				granted := teams
				if in.Body.PrivateTeamIDs == nil {
					if granted, err = grantedTeams(ctx, tx, p.WorkspaceID, current); err != nil {
						return err
					}
				}
				if (u != nil && *u != curURL) || (evs != nil && eventsWiden(curEvents, evs)) ||
					(in.Body.Enabled != nil && *in.Body.Enabled && !curEnabled) {
					if err := requireOnTeams(ctx, tx, p, granted); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE webhooks SET url = COALESCE($3, url), description = COALESCE($4, description),
					events = COALESCE($5, events), enabled = COALESCE($6, enabled), private_team_ids = COALESCE($7, private_team_ids),
					updated_at = now() WHERE id = $1 AND workspace_id = $2`, id, p.WorkspaceID, u, desc, evs, in.Body.Enabled, teams); err != nil {
					return err
				}
				if in.Body.PrivateTeamIDs != nil {
					if err := saveGrants(ctx, tx, p, grantWebhook, id, teams); err != nil {
						return err
					}
				}
				if w, err = loadWebhook(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "webhook.updated", Data: map[string]any{"webhook": w}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "webhook.updated", "webhook", &id,
					map[string]any{"url": w.URL, "events": w.Events, "enabled": w.Enabled, "private_team_ids": uuidStrings(w.PrivateTeamIDs)})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreWebhook }{w}, nil
		})

	register(s, api, huma.Operation{OperationID: "delete-webhook", Method: http.MethodDelete, Path: "/v1/webhooks/{id}",
		Summary: "Delete a webhook", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Webhook")
			if err != nil {
				return nil, err
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				var u string
				err := tx.QueryRow(ctx, `DELETE FROM webhooks WHERE id = $1 AND workspace_id = $2 RETURNING url`, id, p.WorkspaceID).Scan(&u)
				if db.IsNotFound(err) {
					return httpx.NotFound("Webhook")
				}
				if err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "webhook.deleted", Data: map[string]any{"webhook_id": id}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "webhook.deleted", "webhook", &id, map[string]any{"url": u})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "list-webhook-deliveries", Method: http.MethodGet, Path: "/v1/webhooks/{id}/deliveries",
		Summary: "List a webhook's recent deliveries", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
			httpx.ListParams
		}) (*struct {
			Body httpx.Page[CoreWebhookDelivery]
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Webhook")
			if err != nil {
				return nil, err
			}
			if _, err := loadWebhook(ctx, s.d.DB.Pool, p.WorkspaceID, id); err != nil {
				return nil, httpx.Wrap(err)
			}
			var cur struct {
				T  time.Time `json:"t"`
				ID uuid.UUID `json:"id"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			if cur.T.IsZero() {
				cur.T = time.Now().Add(time.Hour)
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT id, event_id, event_type, status, attempts, response_code, duration_ms, created_at
				FROM webhook_deliveries WHERE workspace_id = $1 AND webhook_id = $2 AND (created_at, id) < ($3, $4)
				ORDER BY created_at DESC, id DESC LIMIT $5`, p.WorkspaceID, id, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreWebhookDelivery, error) {
				var d CoreWebhookDelivery
				err := r.Scan(&d.ID, &d.EventID, &d.EventType, &d.Status, &d.Attempts, &d.ResponseCode, &d.DurationMS, &d.CreatedAt)
				return d, err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct {
				Body httpx.Page[CoreWebhookDelivery]
			}{httpx.NewPage(list, in.Limit, func(d CoreWebhookDelivery) any {
				return map[string]any{"t": d.CreatedAt, "id": d.ID}
			})}, nil
		})

	register(s, api, huma.Operation{OperationID: "test-webhook", Method: http.MethodPost, Path: "/v1/webhooks/{id}/test",
		Summary: "Send a webhook.test event", Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body CoreWebhookDelivery }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Webhook")
			if err != nil {
				return nil, err
			}
			if err := s.limit("webhook:test:"+id.String(), 20, time.Hour); err != nil {
				return nil, err
			}
			var del CoreWebhookDelivery
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if _, err := loadWebhook(ctx, tx, p.WorkspaceID, id); err != nil {
					return err
				}
				data, _ := json.Marshal(map[string]any{"webhook_id": id, "message": "This is a test event from opendesk."})
				pl := webhookPayload{ID: ids.New(), Seq: 0, WorkspaceID: p.WorkspaceID, Type: webhookTestEvent,
					Visibility: events.VisibilityAgents, Data: data, OccurredAt: time.Now().UTC()}
				did, err := s.createDelivery(ctx, tx, p.WorkspaceID, id, pl)
				if err != nil {
					return err
				}
				return tx.QueryRow(ctx, `SELECT id, event_id, event_type, status, attempts, response_code, duration_ms, created_at
					FROM webhook_deliveries WHERE id = $1`, did).Scan(&del.ID, &del.EventID, &del.EventType, &del.Status, &del.Attempts,
					&del.ResponseCode, &del.DurationMS, &del.CreatedAt)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body CoreWebhookDelivery }{del}, nil
		})
}

func cleanDescription(s string) string {
	b := []rune(s)
	out := make([]rune, 0, len(b))
	for _, r := range b {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			out = append(out, r)
		}
	}
	return truncate(string(out), 500)
}

// createDelivery stores a delivery with its fixed payload and enqueues it in tx.
func (s *svc) createDelivery(ctx context.Context, tx pgx.Tx, wid, hookID uuid.UUID, pl webhookPayload) (uuid.UUID, error) {
	body, err := json.Marshal(pl)
	if err != nil {
		return uuid.Nil, err
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries (id, workspace_id, webhook_id, event_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5, $6)`, id, wid, hookID, pl.ID, pl.Type, body); err != nil {
		return uuid.Nil, err
	}
	_, err = s.d.Jobs.InsertTx(ctx, tx, jobs.WebhookDeliverArgs{DeliveryID: id},
		&river.InsertOpts{Queue: jobs.QueueWebhooks, MaxAttempts: webhookMaxAttempts})
	return id, err
}

// dispatchWorker fans one event out to the workspace's matching enabled webhooks.
type dispatchWorker struct {
	river.WorkerDefaults[jobs.WebhookDispatchArgs]
	s *svc
}

func (w *dispatchWorker) Work(ctx context.Context, job *river.Job[jobs.WebhookDispatchArgs]) error {
	a := job.Args
	// A suspended workspace sends no webhooks (contracts/admin.md).
	if suspended, err := domain.WorkspaceSuspended(ctx, w.s.d.DB.Pool, a.WorkspaceID); err != nil || suspended {
		return err
	}
	return w.s.tx(ctx, func(tx pgx.Tx) error {
		var pl webhookPayload
		var team *uuid.UUID
		var scoped bool
		err := tx.QueryRow(ctx, `SELECT id, seq, workspace_id, type, visibility, data, created_at, team_id, user_ids IS NOT NULL FROM events
			WHERE workspace_id = $1 AND seq = $2`, a.WorkspaceID, a.EventSeq).
			Scan(&pl.ID, &pl.Seq, &pl.WorkspaceID, &pl.Type, &pl.Visibility, &pl.Data, &pl.OccurredAt, &team, &scoped)
		if db.IsNotFound(err) {
			return nil // pruned or workspace deleted
		}
		if err != nil {
			return err
		}
		// An event scoped to people is theirs alone (contracts/events.md); no webhook receives it.
		if scoped {
			return nil
		}
		pl.OccurredAt = pl.OccurredAt.UTC()
		rows, err := tx.Query(ctx, `SELECT id, events, private_team_ids FROM webhooks WHERE workspace_id = $1 AND enabled`, a.WorkspaceID)
		if err != nil {
			return err
		}
		type hook struct {
			id     uuid.UUID
			filter []string
			teams  []uuid.UUID
		}
		hooks, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hook, error) {
			var h hook
			err := r.Scan(&h.id, &h.filter, &h.teams)
			return h, err
		})
		if err != nil {
			return err
		}
		for _, h := range hooks {
			if !eventMatches(h.filter, pl.Type) || !webhookSeesTeam(h.teams, team) {
				continue
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM webhook_deliveries WHERE webhook_id = $1 AND event_id = $2)`,
				h.id, pl.ID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := w.s.createDelivery(ctx, tx, a.WorkspaceID, h.id, pl); err != nil {
				return err
			}
		}
		return nil
	})
}

// deliverWorker POSTs one delivery. River retries failures with backoff.
type deliverWorker struct {
	river.WorkerDefaults[jobs.WebhookDeliverArgs]
	s *svc
}

func (w *deliverWorker) Timeout(*river.Job[jobs.WebhookDeliverArgs]) time.Duration {
	return 30 * time.Second
}

func (w *deliverWorker) Work(ctx context.Context, job *river.Job[jobs.WebhookDeliverArgs]) error {
	s := w.s
	var wid, hookID uuid.UUID
	var url, status, eventType string
	var eventID uuid.UUID
	var enabled bool
	var sealed, body []byte
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT d.workspace_id, d.webhook_id, d.status, d.event_id, d.event_type, d.payload,
		h.url, h.enabled, h.secret_enc FROM webhook_deliveries d JOIN webhooks h ON h.id = d.webhook_id AND h.workspace_id = d.workspace_id
		WHERE d.id = $1`, job.Args.DeliveryID).Scan(&wid, &hookID, &status, &eventID, &eventType, &body, &url, &enabled, &sealed)
	if db.IsNotFound(err) {
		return nil // webhook or workspace deleted
	}
	if err != nil {
		return err
	}
	if status != "pending" {
		return nil
	}
	if !enabled && eventType != webhookTestEvent {
		_, err := s.d.DB.Pool.Exec(ctx, `UPDATE webhook_deliveries SET status = 'failed', last_error = 'webhook disabled' WHERE id = $1`, job.Args.DeliveryID)
		return err
	}
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, wid); err != nil || suspended {
		if err != nil {
			return err
		}
		_, err := s.d.DB.Pool.Exec(ctx, `UPDATE webhook_deliveries SET status = 'failed', last_error = 'workspace suspended' WHERE id = $1`, job.Args.DeliveryID)
		return err
	}
	secret, err := s.d.Box.Open(sealed)
	if err != nil {
		return river.JobCancel(errors.New("webhook secret unreadable"))
	}
	code, respBody, dur, sendErr := s.sendWebhook(ctx, url, secret, eventID, eventType, body)
	final := job.Attempt >= job.MaxAttempts
	newStatus := "pending"
	switch {
	case sendErr == nil && code >= 200 && code < 300:
		newStatus = "succeeded"
	case errors.Is(sendErr, errBlockedAddress):
		newStatus, final = "failed", true
	case final:
		newStatus = "failed"
	}
	lastErr := ""
	if sendErr != nil {
		lastErr = truncate(sendErr.Error(), 300)
		if errors.Is(sendErr, errBlockedAddress) {
			lastErr = errBlockedAddress.Error()
		}
	} else if newStatus != "succeeded" {
		lastErr = "HTTP " + strconv.Itoa(code)
	}
	var codePtr *int
	if code > 0 {
		codePtr = &code
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE webhook_deliveries SET status = $2, attempts = attempts + 1, response_code = $3,
		response_body = $4, duration_ms = $5, last_error = $6, delivered_at = CASE WHEN $2 = 'succeeded' THEN now() ELSE delivered_at END
		WHERE id = $1`, job.Args.DeliveryID, newStatus, codePtr, respBody, int(dur.Milliseconds()), lastErr); err != nil {
		return err
	}
	switch {
	case newStatus == "succeeded":
		return nil
	case errors.Is(sendErr, errBlockedAddress):
		return river.JobCancel(errBlockedAddress)
	case final:
		return river.JobCancel(fmt.Errorf("webhook delivery failed after %d attempts", job.Attempt))
	}
	return fmt.Errorf("webhook delivery attempt %d: %s", job.Attempt, lastErr)
}

// sendWebhook POSTs a signed body and returns the status, a truncated response body
// and how long it took.
func (s *svc) sendWebhook(ctx context.Context, url string, secret []byte, eventID uuid.UUID, eventType string, body []byte) (int, string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "opendesk-webhooks/"+version.Version)
	req.Header.Set("OD-Signature", signWebhook(secret, time.Now().Unix(), body))
	req.Header.Set("OD-Event-Id", eventID.String())
	req.Header.Set("OD-Event-Type", eventType)
	start := time.Now()
	resp, err := s.hooks.Do(req)
	if err != nil {
		// The URL may carry a token in its query; report only the cause.
		var ue *neturl.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, "", time.Since(start), err
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, webhookResponseKeep))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	buf = bytes.ReplaceAll(bytes.ToValidUTF8(buf, []byte("?")), []byte{0}, nil)
	return resp.StatusCode, string(buf), time.Since(start), nil
}
