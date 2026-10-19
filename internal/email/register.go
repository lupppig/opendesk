// Package email is the email area: mailboxes (forwarding, IMAP, Google and Microsoft),
// inbound mail (HTTP from the Cloudflare Email Worker, the built-in SMTP receiver and
// IMAP sync) and outbound replies through the customer's own mailbox.
package email

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// Register adds this area's routes.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	s := newService(d)
	s.registerMailboxRoutes(api)
	s.registerTemplateRoutes(api)
	s.registerConnectRoutes(api)
	s.registerOAuthRoutes(api, r)
	// Raw RFC 822 body; authenticated by the shared inbound secret.
	r.Post("/v1/inbound/email", s.inboundHTTP)
}

// RegisterWorkers adds this area's River workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	s := newService(d)
	river.AddWorker(w, &syncDueWorker{s: s})
	river.AddWorker(w, &syncMailboxWorker{s: s})
	river.AddWorker(w, &processInboundWorker{s: s})
	river.AddWorker(w, &sendMessageWorker{s: s})
	river.AddWorker(w, &sendTicketEmailWorker{s: s})
	river.AddWorker(w, &chatFollowUpWorker{s: s})
	river.AddWorker(w, &providerTidyWorker{s: s})
}

// Periodic returns this area's scheduled jobs: every minute, queue a sync for each
// IMAP/OAuth mailbox that is due.
func Periodic(d *deps.Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return syncDueArgs{}, &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 1,
				UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
		}, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}

// Background runs the built-in SMTP receiver when INBOUND_SMTP_ADDR is set.
func Background(ctx context.Context, d *deps.Deps) {
	if d.Cfg.InboundSMTPAddr == "" {
		return
	}
	newService(d).runSMTP(ctx, d.Cfg.InboundSMTPAddr)
}
