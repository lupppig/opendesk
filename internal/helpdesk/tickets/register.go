// Package tickets is the ticket side of the helpdesk: tickets, messages, activities,
// the ticket trigger worker (SLA, automations, round-robin, notifications), the
// periodic jobs, CSAT, global search and reports.
package tickets

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/deps"
)

// Register adds this package's routes.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	registerTickets(d, api)
	registerMessages(d, api)
	registerThreads(d, api)
	registerTemplates(d, api)
	registerCSAT(d, api)
	registerInsights(d, api)
}

// RegisterWorkers adds the ticket trigger and periodic workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	river.AddWorker(w, &TriggerWorker{D: d})
	river.AddWorker(w, &SLAScanWorker{D: d})
	river.AddWorker(w, &SnoozeWakeWorker{D: d})
	river.AddWorker(w, &AutoCloseWorker{D: d})
	river.AddWorker(w, &PurgeAttachmentsWorker{D: d})
}

// Periodic returns the scheduled helpdesk jobs.
func Periodic(d *deps.Deps) []*river.PeriodicJob { return periodicJobs() }
