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
}
func Periodic(*deps.Deps) []*river.PeriodicJob { return nil }
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

// AvailableDestination is a provider location discovered during onboarding.
// It is deliberately distinct from destination, which is a persisted team route.
type AvailableDestination struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Available bool   `json:"available"`
}
