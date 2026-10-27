// Package collab is the Collab area (contracts/collab.md): members' own mail accounts
// (Google, Microsoft, IMAP), private or shared, synced both ways and read and handled in
// opendesk like a mailbox, with conversations shared, assigned and discussed on their own.
// It is the collab plan's: every route but GET /v1/collab and the member's sidebar
// (/v1/collab/sidebar) needs plan.Entitlements.Collab.
package collab

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
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

	// tickets is ticket email, for replies on tickets sent from an account ("Answering tickets").
	tickets *email.TicketMail

	// mailAI sorts mail into categories and suggests them ("Categories"); tests replace it.
	mailAI mailAI

	// busyWaits are the waits before asking a busy provider again (rest.go); tests shorten them.
	busyWaits []time.Duration
}

func newService(d *deps.Deps) *service {
	s := &service{
		d:         d,
		log:       d.Log.With("area", "collab"),
		guard:     email.NewGuard(d.Cfg.Env == "development"),
		http:      &http.Client{Timeout: providerTimeout},
		gmailBase: "https://gmail.googleapis.com",
		graphBase: "https://graph.microsoft.com/v1.0",
		busyWaits: []time.Duration{time.Second, 4 * time.Second},
	}
	s.oauth = oauthClients(d.Cfg)
	s.newProvider = s.defaultProvider
	s.tickets = email.NewTicketMail(d)
	s.mailAI = mailAIFor(d)
	return s
}

// Register adds this area's routes.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	s := newService(d)
	s.registerStatusRoutes(api)
	s.registerAccountRoutes(api)
	s.registerHistoryRoutes(api)
	s.registerSettingsRoutes(api)
	s.registerConnectRoutes(api, r)
	s.registerFolderRoutes(api)
	s.registerThreadRoutes(api)
	s.registerPeopleRoutes(api)
	s.registerSendRoutes(api)
	s.registerDraftRoutes(api)
	s.registerCommentRoutes(api)
	s.registerDetailRoutes(api)
	s.registerSidebarRoutes(api)
	s.registerCategoryRoutes(api)
	r.Get("/v1/collab/attachments/{message_id}/{index}", s.attachmentHandler)
	d.Overview.MailCounts = s.overviewCounts
}

// overviewCounts is GET /v1/collab/counts for one workspace of the caller's overview, or
// nil when that workspace has no Collab.
func (s *service) overviewCounts(ctx context.Context, p *auth.Principal) (any, error) {
	if p.Kind != auth.KindUser || p.Assistant || p.GrantID != uuid.Nil {
		return nil, nil
	}
	if err := s.d.Plan.RequireCollab(ctx, s.d.DB.Pool, p.WorkspaceID); err != nil {
		if errors.Is(err, plan.ErrCollab) {
			return nil, nil
		}
		return nil, err
	}
	return s.counts(ctx, s.d.DB.Pool, p)
}

// RegisterWorkers adds this area's River workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	s := newService(d)
	river.AddWorker(w, &syncDueWorker{s: s})
	river.AddWorker(w, &syncAccountWorker{s: s})
	river.AddWorker(w, &applyWorker{s: s})
	river.AddWorker(w, &cleanupWorker{s: s})
	river.AddWorker(w, &sendTicketReplyWorker{s: s})
	river.AddWorker(w, &ticketAnswerWorker{s: s})
	river.AddWorker(w, &categorizeWorker{s: s})
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

// require is the gate of every Collab route but GET /v1/collab and the sidebar: a member
// (see member) of a workspace that has Collab.
func (s *service) require(ctx context.Context, min auth.Role) (*auth.Principal, error) {
	p, err := s.member(ctx, min)
	if err != nil {
		return nil, err
	}
	if err := s.d.Plan.RequireCollab(ctx, s.d.DB.Pool, p.WorkspaceID); err != nil {
		return nil, err
	}
	return p, nil
}

// member is a signed-in member of the workspace: never an API key, an AI client or the
// assistant. Alone it gates the routes every member has with or without Collab: whether
// Collab is on, and the member's own sidebar.
func (s *service) member(ctx context.Context, min auth.Role) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, min, "")
	if err != nil {
		return nil, err
	}
	if p.Kind != auth.KindUser || p.Assistant || p.ViaAssistant || p.GrantID != uuid.Nil {
		return nil, auth.ErrForbidden
	}
	return p, nil
}

// requireDrafter is the gate of the few routes an AI credential may also call (contracts/
// collab.md "AI drafts"): listing and reading conversations and saving its own drafts.
// A person passes as with require. An API key with collab:draft acts as the member who
// created it, with their role now: it sees exactly what they see, and stops working the
// moment they leave the workspace, are suspended or fall below min. OAuth clients and
// the assistant stay refused. Every other Collab route keeps require, so a credential
// never sends, changes read or folder state, assigns, shares or comments.
func (s *service) requireDrafter(ctx context.Context, min auth.Role) (*auth.Principal, error) {
	p := auth.FromContext(ctx)
	if p == nil || p.Kind != auth.KindAPIKey {
		return s.require(ctx, min)
	}
	if _, err := auth.RequireWorkspace(ctx, min, auth.ScopeCollabDraft); err != nil {
		return nil, err
	}
	var member uuid.UUID
	var role string
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT m.user_id, m.role FROM api_keys k
		JOIN members m ON m.workspace_id = k.workspace_id AND m.user_id = k.created_by
		JOIN users u ON u.id = m.user_id AND u.suspended_at IS NULL
		WHERE k.id = $1 AND k.workspace_id = $2`, p.APIKeyID, p.WorkspaceID).Scan(&member, &role)
	if db.IsNotFound(err) || (err == nil && !auth.Role(role).AtLeast(min)) {
		return nil, auth.ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if err := s.d.Plan.RequireCollab(ctx, s.d.DB.Pool, p.WorkspaceID); err != nil {
		return nil, err
	}
	// The member, reached through the key: Collab's visibility follows UserID and Role.
	return &auth.Principal{Kind: auth.KindUser, UserID: member, Role: auth.Role(role), WorkspaceID: p.WorkspaceID,
		APIKeyID: p.APIKeyID, Scopes: p.Scopes, IP: p.IP, UserAgent: p.UserAgent, ResolvedAt: p.ResolvedAt}, nil
}

// viaAIClient reports a request an AI credential makes as a member (requireDrafter).
func viaAIClient(p *auth.Principal) bool { return p.Kind == auth.KindUser && p.APIKeyID != uuid.Nil }
