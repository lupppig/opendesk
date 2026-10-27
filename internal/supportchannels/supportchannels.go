// Package supportchannels mirrors helpdesk email tickets into agent-facing
// services such as Slack, Discord and Telegram. Shared orchestration owns all
// authorization and ticket writes; provider plugins only translate protocols.
package supportchannels

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/supportchannels/providers/discord"
	"github.com/useopendesk/opendesk/internal/supportchannels/providers/slack"
	"github.com/useopendesk/opendesk/internal/supportchannels/providers/telegram"
)

type Service struct {
	d            *deps.Deps
	registry     *Registry
	http         *http.Client
	ai           *ai.Service
	subscriberID uuid.UUID
}

var services sync.Map

func For(d *deps.Deps) *Service {
	if got, ok := services.Load(d); ok {
		return got.(*Service)
	}
	r, err := NewRegistry(slack.Adapter{}, discord.Adapter{}, telegram.Adapter{})
	if err != nil {
		panic(err)
	}
	s := &Service{d: d, registry: r, ai: ai.For(d), subscriberID: uuid.New(), http: &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	got, _ := services.LoadOrStore(d, s)
	return got.(*Service)
}

func Register(d *deps.Deps, api huma.API, r chi.Router) { For(d).register(api, r) }
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	river.AddWorker(w, &dispatchWorker{s: For(d)})
	river.AddWorker(w, &inboundWorker{s: For(d)})
	river.AddWorker(w, &healthAlertWorker{s: For(d)})
}
func Periodic(*deps.Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) {
			return jobs.SupportChannelHealthAlertArgs{}, &river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: 5 * time.Minute}}
		}, &river.PeriodicJobOpts{RunOnStart: true, ID: jobs.SupportChannelHealthAlertArgs{}.Kind()})}
}
func Background(ctx context.Context, d *deps.Deps) {
	s := For(d)
	go s.reconfigurePending(ctx)
	s.runSubscribers(ctx)
}

type connection struct {
	ID                    uuid.UUID  `json:"id"`
	WorkspaceID           uuid.UUID  `json:"-"`
	Provider              string     `json:"provider"`
	Name                  string     `json:"name"`
	ExternalAccountID     string     `json:"external_account_id"`
	ExternalAccountName   string     `json:"external_account_name"`
	ExternalAccountHandle string     `json:"external_account_handle"`
	CredentialsEnc        []byte     `json:"-"`
	Status                string     `json:"status"`
	LastError             string     `json:"last_error"`
	HealthStatus          string     `json:"health_status"`
	HealthDetail          string     `json:"health_detail"`
	HealthCheckedAt       *time.Time `json:"health_checked_at,omitempty"`
	HealthUnhealthySince  *time.Time `json:"health_unhealthy_since,omitempty"`
	HealthAlertedAt       *time.Time `json:"health_alerted_at,omitempty"`
	CredentialsUpdatedAt  time.Time  `json:"-"`
	CreatedAt             time.Time  `json:"created_at"`
	WebhookURL            string     `json:"webhook_url"`
}

type destination struct {
	ID                  uuid.UUID  `json:"id"`
	ConnectionID        uuid.UUID  `json:"connection_id"`
	TeamID              *uuid.UUID `json:"team_id"`
	ExternalChannelID   string     `json:"external_channel_id"`
	ExternalChannelName string     `json:"external_channel_name"`
	Enabled             bool       `json:"enabled"`
	IncludeNotes        bool       `json:"include_notes"`
	MessageLimit        int        `json:"message_limit"`
	CreatedAt           time.Time  `json:"created_at"`
}

type delivery struct {
	EventSeq     int64      `json:"event_seq"`
	EventType    string     `json:"event_type"`
	TicketNumber *int64     `json:"ticket_number,omitempty"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
}

type operations struct {
	WindowStartedAt time.Time            `json:"window_started_at"`
	GeneratedAt     time.Time            `json:"generated_at"`
	Connections     operationConnections `json:"connections"`
	Deliveries      operationDeliveries  `json:"deliveries"`
	Webhooks        operationWebhooks    `json:"webhooks"`
}

type operationConnections struct {
	Total     int64 `json:"total"`
	Healthy   int64 `json:"healthy"`
	Unhealthy int64 `json:"unhealthy"`
	Alerted   int64 `json:"alerted"`
}

type operationDeliveries struct {
	Queued           int64      `json:"queued"`
	Retrying         int64      `json:"retrying"`
	Delivered        int64      `json:"delivered"`
	Failed           int64      `json:"failed"`
	AverageLatencyMS int64      `json:"average_latency_ms"`
	MaximumLatencyMS int64      `json:"maximum_latency_ms"`
	OldestPendingAt  *time.Time `json:"oldest_pending_at,omitempty"`
}

type operationWebhooks struct {
	Rejected             int64 `json:"rejected"`
	DuplicatesSuppressed int64 `json:"duplicates_suppressed"`
}

// AvailableDestination is a provider location discovered during onboarding.
// It is deliberately distinct from destination, which is a persisted team route.
type AvailableDestination struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Available bool   `json:"available"`
}
