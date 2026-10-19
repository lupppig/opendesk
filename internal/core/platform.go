package core

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
)

type CoreAuditActor struct {
	Type     string          `json:"type" enum:"user,api_key,system"`
	User     *domain.UserRef `json:"user"`
	APIKeyID *uuid.UUID      `json:"api_key_id"`
}

type CoreAuditLog struct {
	ID         uuid.UUID      `json:"id"`
	Actor      CoreAuditActor `json:"actor"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type"`
	EntityID   *uuid.UUID     `json:"entity_id"`
	Data       map[string]any `json:"data"`
	IP         string         `json:"ip"`
	CreatedAt  time.Time      `json:"created_at"`
}

type CoreNotificationTicket struct {
	ID      uuid.UUID `json:"id"`
	Number  int64     `json:"number"`
	Subject string    `json:"subject"`
}

type CoreNotification struct {
	ID        uuid.UUID               `json:"id"`
	Type      string                  `json:"type"`
	Ticket    *CoreNotificationTicket `json:"ticket"`
	Data      map[string]any          `json:"data"`
	ReadAt    *time.Time              `json:"read_at"`
	CreatedAt time.Time               `json:"created_at"`
}

type coreEventsResponse struct {
	Data      []events.Envelope `json:"data"`
	LatestSeq int64             `json:"latest_seq"`
	Resync    bool              `json:"resync"`
}

type coreRealtimeToken struct {
	Token     string    `json:"token"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuditLogs reads a workspace's audit log, newest first, before (t, id).
func AuditLogs(ctx context.Context, q db.Querier, wid uuid.UUID, t time.Time, id uuid.UUID, action, entityType string, limit int) ([]CoreAuditLog, error) {
	rows, err := q.Query(ctx, `SELECT a.id, a.actor_user_id, a.actor_api_key_id, a.action, a.entity_type, a.entity_id,
		a.data, a.ip, a.created_at, u.name, u.email, u.avatar_url
		FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE a.workspace_id = $1 AND (a.created_at, a.id) < ($2, $3)
		AND ($4 = '' OR a.action = $4) AND ($5 = '' OR a.entity_type = $5)
		ORDER BY a.created_at DESC, a.id DESC LIMIT $6`, wid, t, id, action, entityType, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreAuditLog, error) {
		var a CoreAuditLog
		var uid *uuid.UUID
		var name, email, avatar *string
		var raw []byte
		if err := r.Scan(&a.ID, &uid, &a.Actor.APIKeyID, &a.Action, &a.EntityType, &a.EntityID, &raw, &a.IP, &a.CreatedAt,
			&name, &email, &avatar); err != nil {
			return a, err
		}
		_ = json.Unmarshal(raw, &a.Data)
		if a.Data == nil {
			a.Data = map[string]any{}
		}
		switch {
		case uid != nil && name != nil:
			a.Actor.Type = "user"
			a.Actor.User = &domain.UserRef{ID: *uid, Name: *name, Email: *email, AvatarURL: domain.Avatar(*avatar)}
		case a.Actor.APIKeyID != nil:
			a.Actor.Type = "api_key"
		default:
			a.Actor.Type = "system"
		}
		return a, nil
	})
}

func (s *svc) registerPlatform(api huma.API) {
	register(s, api, huma.Operation{OperationID: "list-audit-logs", Method: http.MethodGet, Path: "/v1/audit-logs",
		Summary: "List the workspace audit log", Tags: []string{"Audit log"}},
		func(ctx context.Context, in *struct {
			httpx.ListParams
			Action     string `query:"action" maxLength:"100" doc:"Only this action"`
			EntityType string `query:"entity_type" maxLength:"100"`
		}) (*struct{ Body httpx.Page[CoreAuditLog] }, error) {
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
			if cur.T.IsZero() {
				cur.T = time.Now().Add(time.Hour)
			}
			list, err := AuditLogs(ctx, s.d.DB.Pool, p.WorkspaceID, cur.T, cur.ID, in.Action, in.EntityType, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[CoreAuditLog] }{httpx.NewPage(list, in.Limit, func(a CoreAuditLog) any {
				return map[string]any{"t": a.CreatedAt, "id": a.ID}
			})}, nil
		})

	register(s, api, huma.Operation{OperationID: "list-notifications", Method: http.MethodGet, Path: "/v1/notifications",
		Summary: "List your notifications in this workspace", Tags: []string{"Notifications"}},
		func(ctx context.Context, in *struct {
			httpx.ListParams
			Unread bool `query:"unread" doc:"Only unread notifications"`
		}) (*struct{ Body httpx.Page[CoreNotification] }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
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
			if cur.T.IsZero() {
				cur.T = time.Now().Add(time.Hour)
			}
			// A notification about a ticket the user may no longer see names no ticket and
			// keeps none of its content (contracts/teams.md "Visibility").
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT n.id, n.type, n.data, n.read_at, n.created_at, t.id, t.number, t.subject,
					n.ticket_id IS NOT NULL AND t.id IS NULL
				FROM notifications n LEFT JOIN tickets t ON t.id = n.ticket_id AND t.workspace_id = n.workspace_id AND t.deleted_at IS NULL
					AND `+domain.VisibleToUserSQL("t", 2)+`
				WHERE n.workspace_id = $1 AND n.user_id = $2 AND (n.created_at, n.id) < ($3, $4) AND (NOT $5 OR n.read_at IS NULL)
				ORDER BY n.created_at DESC, n.id DESC LIMIT $6`, p.WorkspaceID, p.UserID, cur.T, cur.ID, in.Unread, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CoreNotification, error) {
				var n CoreNotification
				var raw []byte
				var tid *uuid.UUID
				var num *int64
				var subj *string
				var gone bool
				if err := r.Scan(&n.ID, &n.Type, &raw, &n.ReadAt, &n.CreatedAt, &tid, &num, &subj, &gone); err != nil {
					return n, err
				}
				_ = json.Unmarshal(raw, &n.Data)
				if n.Data == nil {
					n.Data = map[string]any{}
				}
				if gone {
					n.Data = withoutTicketContent(n.Data)
				}
				if tid != nil {
					n.Ticket = &CoreNotificationTicket{ID: *tid, Number: *num, Subject: *subj}
				}
				return n, nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[CoreNotification] }{httpx.NewPage(list, in.Limit, func(n CoreNotification) any {
				return map[string]any{"t": n.CreatedAt, "id": n.ID}
			})}, nil
		})

	register(s, api, huma.Operation{OperationID: "read-notifications", Method: http.MethodPost, Path: "/v1/notifications/read",
		Summary: "Mark notifications read", Tags: []string{"Notifications"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				IDs []uuid.UUID `json:"ids,omitempty" maxItems:"500"`
				All bool        `json:"all,omitempty"`
			}
		}) (*struct{}, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !in.Body.All && len(in.Body.IDs) == 0 {
				return nil, httpx.Validation("Pass ids or all: true.")
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `UPDATE notifications SET read_at = now()
					WHERE workspace_id = $1 AND user_id = $2 AND read_at IS NULL AND ($3 OR id = ANY($4))`,
					p.WorkspaceID, p.UserID, in.Body.All, in.Body.IDs)
				if err != nil || tag.RowsAffected() == 0 {
					return err
				}
				// The phone's badge follows the unread count.
				return s.enqueueBadge(ctx, tx, p.UserID)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	register(s, api, huma.Operation{OperationID: "list-events", Method: http.MethodGet, Path: "/v1/events",
		Summary: "Replay workspace events after a sequence number", Tags: []string{"Realtime"}},
		func(ctx context.Context, in *struct {
			Since int64 `query:"since" minimum:"0" doc:"Last seq the client applied"`
			Limit int   `query:"limit" minimum:"1" maximum:"1000" default:"500"`
		}) (*struct{ Body coreEventsResponse }, error) {
			p, err := requireEventsReader(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			hidden, err := domain.HiddenTeams(ctx, s.d.DB.Pool, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			evs, latest, resync, err := events.Since(ctx, s.d.DB.Pool, p.WorkspaceID, in.Since, in.Limit, true, hidden, p.ActorUserID())
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			for i := range evs {
				evs[i].OccurredAt = evs[i].OccurredAt.UTC()
			}
			return &struct{ Body coreEventsResponse }{coreEventsResponse{Data: evs, LatestSeq: latest, Resync: resync}}, nil
		})

	register(s, api, huma.Operation{OperationID: "realtime-token", Method: http.MethodPost, Path: "/v1/realtime/token",
		Summary: "Get a short-lived token for the realtime gateway", Tags: []string{"Realtime"}},
		func(ctx context.Context, _ *struct{}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         coreRealtimeToken
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeRealtime)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			c := auth.Claims{Purpose: auth.PurposeWS, Workspace: p.WorkspaceID.String()}
			var sub string
			switch p.Kind {
			case auth.KindUser:
				var name, avatar string
				if err := s.d.DB.Pool.QueryRow(ctx, `SELECT name, avatar_url FROM users WHERE id = $1`, p.UserID).Scan(&name, &avatar); err != nil {
					return nil, httpx.Wrap(err)
				}
				sub, c.Kind, c.Role, c.Name, c.Avatar = p.UserID.String(), string(auth.KindUser), string(p.Role), name, domain.Avatar(avatar).URL()
			case auth.KindAPIKey:
				var name string
				if err := s.d.DB.Pool.QueryRow(ctx, `SELECT name FROM api_keys WHERE id = $1 AND workspace_id = $2`, p.APIKeyID, p.WorkspaceID).Scan(&name); err != nil {
					return nil, httpx.Wrap(err)
				}
				sub, c.Kind, c.Role, c.Name = p.APIKeyID.String(), string(auth.KindAPIKey), apiKeyRole(p), name
			default:
				return nil, httpx.Forbidden()
			}
			teams, err := domain.VisibleTeams(ctx, s.d.DB.Pool, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			for _, t := range teams {
				c.Teams = append(c.Teams, t.String())
			}
			tok, exp, err := s.d.Tokens.Sign(c, sub, auth.WSTTL)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct {
				CacheControl string `header:"Cache-Control"`
				Body         coreRealtimeToken
			}{"no-store", coreRealtimeToken{Token: tok, URL: s.d.Cfg.GatewayURL, ExpiresAt: exp.UTC()}}, nil
		})
}

// apiKeyRole is the role a key's socket carries: agent when it may write tickets.
func apiKeyRole(p *auth.Principal) string {
	if p.HasScope(auth.ScopeTicketsWrite) {
		return string(auth.RoleAgent)
	}
	return string(auth.RoleViewer)
}

// requireEventsReader accepts members and API keys with the realtime or tickets:read scope.
func requireEventsReader(ctx context.Context) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeRealtime)
	if err == nil {
		return p, nil
	}
	return auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
}

// withoutTicketContent keeps only who acted from a notification's data: what a notification
// about a ticket the user can no longer see may still say.
func withoutTicketContent(data map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"by", "actor", "actor_type", "via", "kind"} {
		if v, ok := data[k]; ok {
			out[k] = v
		}
	}
	return out
}
