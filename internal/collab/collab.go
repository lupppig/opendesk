// Package collab is the Collab area (contracts/collab.md): members' own mail accounts
// (Google, Microsoft, IMAP), private or shared, synced both ways and read and handled in
// opendesk like a mailbox, with conversations shared, assigned and discussed on their own.
// It is the collab plan's: every route but GET /v1/collab needs plan.Entitlements.Collab.
package collab

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// Limits (contracts/collab.md "Sync").
const (
	initialWindow   = 30 * 24 * time.Hour // the first sync imports this much mail
	initialMax      = 500                 // and at most this many messages per account
	maxFolders      = 200                 // folders and labels kept per account
	maxIMAPFolders  = 30                  // IMAP folders synced
	maxRawBytes     = 30 << 20            // a larger message is skipped
	maxSendBytes    = 25 << 20            // files on one sent message
	maxRecipients   = 100                 // on one sent message
	activeInterval  = time.Minute         // sync interval while someone with access is active
	idleInterval    = 10 * time.Minute    // otherwise
	activeWindow    = 30 * time.Minute    // "active": seen in the app this recently
	providerTimeout = 60 * time.Second    // one provider HTTP call
	sessionTimeout  = 4 * time.Minute     // one IMAP or SMTP session
)

// service is the Collab area. It is cheap to construct and holds no per-request state.
type service struct {
	d     *deps.Deps
	log   *slog.Logger
	guard *email.Guard
	http  *http.Client

	// Provider endpoints, replaced in tests.
	gmailBase string // https://gmail.googleapis.com
	graphBase string // https://graph.microsoft.com/v1.0
	oauth     map[string]*oauthClient

	// newProvider builds the provider client of an account; tests replace it.
	newProvider func(ctx context.Context, a *accountRow) (Provider, error)
}

func newService(d *deps.Deps) *service {
	s := &service{
		d:         d,
		log:       d.Log.With("area", "collab"),
		guard:     email.NewGuard(d.Cfg.Env == "development"),
		http:      &http.Client{Timeout: providerTimeout},
		gmailBase: "https://gmail.googleapis.com",
		graphBase: "https://graph.microsoft.com/v1.0",
	}
	s.oauth = oauthClients(d.Cfg)
	s.newProvider = s.defaultProvider
	return s
}

// Register adds this area's routes.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	s := newService(d)
	s.registerStatusRoutes(api)
	s.registerAccountRoutes(api)
	s.registerSettingsRoutes(api)
	s.registerConnectRoutes(api, r)
	s.registerFolderRoutes(api)
	s.registerThreadRoutes(api)
	s.registerSendRoutes(api)
	s.registerDraftRoutes(api)
	s.registerCommentRoutes(api)
	s.registerSidebarRoutes(api)
	r.Get("/v1/collab/attachments/{message_id}/{index}", s.attachmentHandler)
}

// RegisterWorkers adds this area's River workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	s := newService(d)
	river.AddWorker(w, &syncDueWorker{s: s})
	river.AddWorker(w, &syncAccountWorker{s: s})
	river.AddWorker(w, &applyWorker{s: s})
	river.AddWorker(w, &cleanupWorker{s: s})
}

// Periodic returns this area's scheduled jobs: every minute, queue a sync for each due
// account; every hour, clean up.
func Periodic(d *deps.Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return syncDueArgs{}, &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 1,
				UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
		}, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return jobs.CollabCleanupArgs{}, &river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: time.Hour}}
		}, nil),
	}
}

// Background runs nothing: Collab works through jobs.
func Background(context.Context, *deps.Deps) {}

// require is the gate of every Collab route but GET /v1/collab: a signed-in member (never
// an API key, an AI client or the assistant) of a workspace that has Collab.
func (s *service) require(ctx context.Context, min auth.Role) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, min, "")
	if err != nil {
		return nil, err
	}
	if p.Kind != auth.KindUser || p.Assistant || p.GrantID != uuid.Nil {
		return nil, auth.ErrForbidden
	}
	if err := s.d.Plan.RequireCollab(ctx, s.d.DB.Pool, p.WorkspaceID); err != nil {
		return nil, err
	}
	return p, nil
}
