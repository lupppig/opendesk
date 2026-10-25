// Package admin is the instance admin area (contracts/admin.md): the operator surface of
// an instance, under /v1/admin/*. It has its own sign-in, sessions (admin_sessions) and
// token purpose; a workspace role never grants anything here, and an admin session never
// acts inside a workspace. Account and workspace removals go through core (core.Ops), so
// they are the same as the app's own.
package admin

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/billing"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Role is an instance admin's role.
type Role string

const (
	RoleViewer  Role = "viewer"
	RoleSupport Role = "support"
	RoleSuper   Role = "super"
)

// Valid reports a known role.
func (r Role) Valid() bool { return r == RoleViewer || r == RoleSupport || r == RoleSuper }

// Permissions. Every route needs one (contracts/admin.md "Admins and roles").
const (
	PermRead             = "read"
	PermUserSessions     = "users.sessions"
	PermUserSuspend      = "users.suspend"
	PermUserMFA          = "users.mfa"
	PermUserDelete       = "users.delete"
	PermWorkspaceSuspend = "workspaces.suspend"
	PermWorkspaceLimits  = "workspaces.limits"
	PermWorkspaceDelete  = "workspaces.delete"
	// PermWorkspaceTest creates, extends, fills and deletes test workspaces.
	PermWorkspaceTest = "workspaces.test"
	PermJobsManage    = "jobs.manage"
	PermAdminsManage  = "admins.manage"
)

var rolePermissions = map[Role][]string{
	RoleViewer: {PermRead},
	RoleSupport: {PermRead, PermUserSessions, PermUserSuspend, PermWorkspaceSuspend, PermWorkspaceLimits,
		PermWorkspaceTest, PermJobsManage},
	RoleSuper: {PermRead, PermUserSessions, PermUserSuspend, PermUserMFA, PermUserDelete, PermWorkspaceSuspend,
		PermWorkspaceLimits, PermWorkspaceTest, PermWorkspaceDelete, PermJobsManage, PermAdminsManage},
}

// Permissions lists what a role may do.
func (r Role) Permissions() []string { return slices.Clone(rolePermissions[r]) }

// Can reports whether r has permission perm.
func (r Role) Can(perm string) bool { return slices.Contains(rolePermissions[r], perm) }

// Admin is the caller of an admin route: a live admin session of a user who is still an
// admin, not suspended and has two-factor authentication on.
type Admin struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Email     string
	Name      string
	Role      Role
	IP        string
	UserAgent string
}

// principal attributes the admin's actions in a workspace's own audit log: no actor
// user (the data says instance_admin) and the admin's address.
func (a *Admin) principal() *auth.Principal { return &auth.Principal{IP: a.IP, UserAgent: a.UserAgent} }

type svc struct {
	d   *deps.Deps
	ops *core.Ops
	// billing publishes billing.updated after a change to a workspace's plan or limits.
	billing *billing.Publisher
	// started is when this process began, for the health page.
	started time.Time
	// releaseAPI and http are where and how the update worker looks for releases.
	releaseAPI string
	http       *http.Client
}

var processStart = time.Now()

func newSvc(d *deps.Deps) *svc {
	return &svc{d: d, ops: core.NewOps(d), billing: billing.NewPublisher(d), started: processStart, releaseAPI: latestReleaseAPI,
		http: &http.Client{Timeout: 20 * time.Second}}
}

// Register adds the /v1/admin/* routes. They are left out of the public OpenAPI spec.
func Register(d *deps.Deps, api huma.API, _ chi.Router) {
	s := newSvc(d)
	s.registerAuth(api)
	s.registerSummary(api)
	s.registerHealth(api)
	s.registerUpdates(api)
	s.registerUsers(api)
	s.registerWorkspaces(api)
	s.registerTestWorkspaces(api)
	s.registerJobs(api)
	s.registerAdmins(api)
	s.registerAudit(api)
}

// RegisterWorkers adds the admin area's workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	river.AddWorker(w, &cleanupWorker{d: d})
	river.AddWorker(w, &seedWorker{d: d})
	river.AddWorker(w, &expireTestWorker{d: d})
	river.AddWorker(w, &updatesWorker{s: newSvc(d)})
}

// Periodic prunes ended admin sessions and old admin audit rows, deletes test workspaces
// whose time is up, and keeps the update status current (updates.go).
func Periodic(_ *deps.Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(6*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return cleanupArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return expireTestArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return updatesArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}

// Background: the admin area has no loops.
func Background(context.Context, *deps.Deps) {}

// cleanupArgs prunes ended admin sessions and admin audit rows past retention.
type cleanupArgs struct{}

func (cleanupArgs) Kind() string { return "admin.cleanup" }

type cleanupWorker struct {
	river.WorkerDefaults[cleanupArgs]
	d *deps.Deps
}

// AuditRetention is how long the admin audit log is kept.
const AuditRetention = 2 * 365 * 24 * time.Hour

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[cleanupArgs]) error {
	if _, err := w.d.DB.Pool.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at < now() - interval '1 day'
		OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '1 day')`); err != nil {
		return err
	}
	_, err := w.d.DB.Pool.Exec(ctx, `DELETE FROM admin_audit_logs WHERE created_at < $1`, time.Now().Add(-AuditRetention))
	return err
}

// ── request plumbing ─────────────────────────────────────────────────────────

type reqKey struct{}

// reqInfo is what admin handlers need from the raw request.
type reqInfo struct {
	IP        string
	UserAgent string
	Origin    string
	Cookie    string // the od_admin refresh cookie
	admin     *Admin
	// resolveErr is why a Bearer token could not be checked (the database did not
	// answer): the request fails with it instead of reading as signed out.
	resolveErr error
}

func info(ctx context.Context) reqInfo {
	ri, _ := ctx.Value(reqKey{}).(reqInfo)
	return ri
}

// op registers an admin route: hidden from the public spec, tagged Admin, with the
// caller resolved from its Bearer token.
func op[I, O any](s *svc, api huma.API, o huma.Operation, h func(context.Context, *I) (*O, error)) {
	o.Hidden = true
	o.Tags = []string{"Admin"}
	o.Middlewares = append(o.Middlewares, s.resolve)
	huma.Register(api, o, h)
}

// resolve reads the request info and the admin behind the Bearer token, if any. It never
// refuses; handlers call require.
func (s *svc) resolve(ctx huma.Context, next func(huma.Context)) {
	ri := reqInfo{}
	if r, _ := humachi.Unwrap(ctx); r != nil {
		ri.IP = httpx.ClientIP(s.d.Cfg.TrustProxy, s.d.Cfg.ProxyHops)(r)
		ri.UserAgent = truncate(r.UserAgent(), 400)
		ri.Origin = r.Header.Get("Origin")
		name := RefreshCookie
		if s.d.Cfg.CookieSecure {
			name = HostRefreshCookie
		}
		if c, err := r.Cookie(name); err == nil {
			ri.Cookie = c.Value
		}
		scheme, cred, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if ok && strings.EqualFold(scheme, "Bearer") && cred != "" {
			ri.admin, ri.resolveErr = s.adminFromToken(r.Context(), cred, ri)
		}
	}
	next(huma.WithValue(ctx, reqKey{}, ri))
}

// adminFromToken is the admin behind an access token, or nil when it does not open a live
// session. An error means the session could not be checked.
func (s *svc) adminFromToken(ctx context.Context, token string, ri reqInfo) (*Admin, error) {
	c, err := s.d.Tokens.Verify(token, auth.PurposeAdmin)
	if err != nil {
		return nil, nil
	}
	uid, err1 := uuid.Parse(c.Subject)
	sid, err2 := uuid.Parse(c.SessionID)
	if err1 != nil || err2 != nil {
		return nil, nil
	}
	a := &Admin{UserID: uid, SessionID: sid, IP: ri.IP, UserAgent: ri.UserAgent}
	err = s.d.DB.Pool.QueryRow(ctx, `SELECT u.email, u.name, a.role FROM admin_sessions s
		JOIN users u ON u.id = s.user_id AND u.suspended_at IS NULL AND u.totp_enabled
		JOIN instance_admins a ON a.user_id = s.user_id
		WHERE s.id = $1 AND s.user_id = $2 AND s.revoked_at IS NULL AND s.expires_at > now()`, sid, uid).Scan(&a.Email, &a.Name, &a.Role)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// require demands an admin with permission perm.
func require(ctx context.Context, perm string) (*Admin, error) {
	ri := info(ctx)
	if ri.resolveErr != nil {
		return nil, httpx.Wrap(ri.resolveErr)
	}
	a := ri.admin
	if a == nil {
		return nil, httpx.E(401, "unauthenticated", "Sign in to the admin panel again.")
	}
	if !a.Role.Can(perm) {
		return nil, httpx.E(403, "forbidden", "Your admin role does not allow this.")
	}
	return a, nil
}

func (s *svc) limit(key string, n int, per time.Duration) error {
	if !s.d.Limiter.Allow(key, n, per) {
		return httpx.RateLimited()
	}
	return nil
}

// limitShared throttles sign-in attempts (per account or per address), counted across
// every API process and restart (ASVS 2.2.1).
func (s *svc) limitShared(ctx context.Context, key string, n int, per time.Duration) error {
	if !s.d.Limiter.Shared(ctx, key, n, per) {
		return httpx.RateLimited()
	}
	return nil
}

func parseID(raw, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httpx.NotFound(what)
	}
	return id, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
