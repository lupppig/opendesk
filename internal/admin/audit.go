package admin

import (
	"context"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// audit writes an admin audit row. data must not hold a password, code, token, message
// content or a target's name or email (contracts/admin.md "Admin audit log").
func (s *svc) audit(ctx context.Context, q db.Querier, a *Admin, action, targetType, targetID string, data map[string]any) error {
	return writeAudit(ctx, q, "admin", &a.UserID, a.Email, action, targetType, targetID, data, a.IP)
}

// WriteAudit writes an admin audit row as by (the command line, or the system).
func WriteAudit(ctx context.Context, q db.Querier, by Actor, action, targetType, targetID string, data map[string]any) error {
	return writeAudit(ctx, q, by.Type, by.UserID, by.Email, action, targetType, targetID, data, by.IP)
}

func writeAudit(ctx context.Context, q db.Querier, actorType string, actor *uuid.UUID, actorEmail, action, targetType, targetID string, data map[string]any, ip string) error {
	if data == nil {
		data = map[string]any{}
	}
	var target *string
	if targetID != "" {
		target = &targetID
	}
	_, err := q.Exec(ctx, `INSERT INTO admin_audit_logs (id, actor_user_id, actor_type, actor_email, action, target_type, target_id, data, ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, ids.New(), actor, actorType, actorEmail, action, targetType, target, data, ip)
	return err
}

type AdminAuditActor struct {
	Type  string     `json:"type" enum:"admin,cli,system"`
	ID    *uuid.UUID `json:"id"`
	Email string     `json:"email"`
}

type AdminAuditLog struct {
	ID         uuid.UUID       `json:"id"`
	Actor      AdminAuditActor `json:"actor"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type" enum:"user,workspace,admin,job,instance"`
	TargetID   *string         `json:"target_id"`
	Data       map[string]any  `json:"data"`
	IP         string          `json:"ip"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (s *svc) registerAudit(api huma.API) {
	op(s, api, huma.Operation{OperationID: "admin-audit", Method: http.MethodGet, Path: "/v1/admin/audit", Summary: "The admin audit log"},
		func(ctx context.Context, in *struct {
			httpx.ListParams
			Action   string `query:"action" maxLength:"64"`
			TargetID string `query:"target_id" maxLength:"64"`
			ActorID  string `query:"actor_id" maxLength:"36"`
		}) (*struct{ Body httpx.Page[AdminAuditLog] }, error) {
			if _, err := require(ctx, PermRead); err != nil {
				return nil, err
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
				cur.ID = uuid.Max
			}
			var actor *uuid.UUID
			if in.ActorID != "" {
				id, err := uuid.Parse(in.ActorID)
				if err != nil {
					return nil, httpx.Validation("actor_id must be a user id")
				}
				actor = &id
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT id, actor_type, actor_user_id, actor_email, action, target_type, target_id, data, ip, created_at
				FROM admin_audit_logs
				WHERE (created_at, id) < ($1, $2) AND ($3 = '' OR action = $3) AND ($4 = '' OR target_id = $4)
				AND ($5::uuid IS NULL OR actor_user_id = $5)
				ORDER BY created_at DESC, id DESC LIMIT $6`, cur.T, cur.ID, in.Action, in.TargetID, actor, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminAuditLog, error) {
				var x AdminAuditLog
				err := r.Scan(&x.ID, &x.Actor.Type, &x.Actor.ID, &x.Actor.Email, &x.Action, &x.TargetType, &x.TargetID, &x.Data, &x.IP, &x.CreatedAt)
				return x, err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page := httpx.NewPage(list, in.Limit, func(x AdminAuditLog) any { return map[string]any{"t": x.CreatedAt, "id": x.ID} })
			return &struct{ Body httpx.Page[AdminAuditLog] }{page}, nil
		})
}

// normEmail validates and normalizes an address; "" when invalid.
func normEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 {
		return ""
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || !strings.Contains(s, "@") {
		return ""
	}
	return strings.ToLower(s)
}
