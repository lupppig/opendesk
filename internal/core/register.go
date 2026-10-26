// Package core is the core area. Register wires its HTTP routes, RegisterWorkers its
// River workers, Periodic its scheduled jobs and Background its long-running loops.
//
// Core owns accounts and sign-in (password, OAuth/OIDC, TOTP, passkeys), sessions, workspaces,
// members, invitations, teams, API keys, webhooks, the audit log, notifications, the
// events replay API and realtime tokens.
package core

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/deps"
)

// Register adds this area's routes. Prefer huma operations; use r only for raw HTTP
// (redirects, raw bodies, streaming).
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	s := newSvc(d)
	s.registerAuth(api)
	s.registerAccounts(api)
	s.registerOAuth(api, r)
	s.registerTOTP(api)
	s.registerPasskeys(api)
	s.registerMe(api)
	s.registerWorkspaceOverview(api)
	s.registerAvatars(api)
	s.registerOnboarding(api)
	installImageHooks()
	s.registerWorkspaces(api)
	s.registerMembers(api)
	s.registerInvitations(api)
	s.registerTeams(api)
	s.registerAPIKeys(api)
	s.registerWebhooks(api)
	s.registerPlatform(api)
	s.registerAppAttest(api)
	s.registerDevices(api)
	s.registerPush(api)
	s.registerWebPush(api)
	s.registerNotificationPrefs(api, r)
	s.registerSpaces(api)
}

// RegisterWorkers adds this area's River workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	s := newSvc(d)
	installImageHooks()
	river.AddWorker(w, &systemEmailWorker{s: s})
	river.AddWorker(w, &forgotWorker{s: s})
	river.AddWorker(w, &dispatchWorker{s: s})
	river.AddWorker(w, &deliverWorker{s: s})
	river.AddWorker(w, &cleanupWorker{s: s})
	river.AddWorker(w, &pushNotificationWorker{s: s})
	river.AddWorker(w, &pushBadgeWorker{s: s})
	river.AddWorker(w, &notificationSweepWorker{s: s})
	river.AddWorker(w, &notificationEmailWorker{s: s})
}

// Periodic returns this area's scheduled jobs.
func Periodic(d *deps.Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(6*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return cleanupArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(notificationSweepEvery),
			func() (river.JobArgs, *river.InsertOpts) { return notificationSweepArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}

// Background runs until ctx is done, in the worker role only. Core has no loops.
func Background(ctx context.Context, d *deps.Deps) {}
