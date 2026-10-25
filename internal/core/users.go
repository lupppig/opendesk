package core

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
)

// CoreUser is the signed-in person's own record (contracts/api.md User).
type CoreUser struct {
	ID            uuid.UUID     `json:"id"`
	Email         string        `json:"email"`
	Name          string        `json:"name"`
	AvatarURL     domain.Avatar `json:"avatar_url"`
	AvatarSource  *string       `json:"avatar_source" enum:"upload,oauth"`
	EmailVerified bool          `json:"email_verified"`
	TOTPEnabled   bool          `json:"totp_enabled"`
	TimeFormat    string        `json:"time_format" enum:"auto,12h,24h" doc:"The clock times are shown in: auto follows the browser's language and region in the app and is 24-hour in emails"`
	CreatedAt     time.Time     `json:"created_at"`
}

const userCols = `id, email, name, avatar_url, avatar_source, email_verified_at IS NOT NULL, totp_enabled, time_format, created_at`

func scanUser(row pgx.Row) (CoreUser, error) {
	var u CoreUser
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.AvatarSource, &u.EmailVerified, &u.TOTPEnabled, &u.TimeFormat, &u.CreatedAt)
	return u, err
}

func loadUser(ctx context.Context, q db.Querier, id uuid.UUID) (CoreUser, error) {
	return scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// userWorkspaces returns the workspaces a user belongs to (bounded).
func userWorkspaces(ctx context.Context, q db.Querier, uid uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT workspace_id FROM members WHERE user_id = $1 ORDER BY created_at LIMIT 100`, uid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// auditUser records an account-level security event in every workspace the user belongs
// to, so their admins see it. It reports whether any row was written.
func auditUser(ctx context.Context, tx pgx.Tx, p *auth.Principal, uid uuid.UUID, action string, data map[string]any) (bool, error) {
	wids, err := userWorkspaces(ctx, tx, uid)
	if err != nil {
		return false, err
	}
	for _, wid := range wids {
		if err := audit.Log(ctx, tx, p, wid, action, "user", &uid, data); err != nil {
			return false, err
		}
	}
	return len(wids) > 0, nil
}

// userPrincipal is the attribution for events a user causes before a session exists.
func userPrincipal(uid uuid.UUID, ri reqInfo) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: uid, IP: ri.IP, UserAgent: ri.UserAgent}
}
