// Package deps is the dependency container handed to every area (core, email, helpdesk).
package deps

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/analytics"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/mailer"
	"github.com/useopendesk/opendesk/internal/natsx"
	"github.com/useopendesk/opendesk/internal/plan"
	"github.com/useopendesk/opendesk/internal/push"
	"github.com/useopendesk/opendesk/internal/storage"
)

type Deps struct {
	Cfg  *config.Config
	Log  *slog.Logger
	DB   *db.DB
	NATS *natsx.Conn
	// Jobs is set before any route or worker runs; enqueue with InsertTx inside the write's tx.
	Jobs      *river.Client[pgx.Tx]
	Storage   storage.Storage
	Mailer    mailer.Mailer
	Analytics *analytics.Client
	Box       *crypto.Box
	Tokens    *auth.Tokens
	Captcha   *captcha.Turnstile
	Limiter   *httpx.Limiter
	// Plan enforces plan limits and the AI balance; unlimited when billing is disabled.
	Plan *plan.Service
	// APNs sends pushes to the iPhone app; nil unless APNS_KEY is set (push = direct).
	APNs *push.APNs
	// Overview holds the areas' sidebar numbers for GET /v1/me/overview; each area sets
	// its own while registering.
	Overview OverviewParts
}

// OverviewParts load one workspace's numbers for the caller's overview (contracts/api.md
// "Several workspaces"). Each gets a principal for that workspace alone, with the caller's
// role there, and answers what the area's own route would answer it; nil when the area
// has nothing there (Collab off for that workspace).
type OverviewParts struct {
	TicketCounts func(ctx context.Context, p *auth.Principal) (any, error)
	MailCounts   func(ctx context.Context, p *auth.Principal) (any, error)
}
