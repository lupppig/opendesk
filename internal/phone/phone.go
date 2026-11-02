// Package phone connects cloud phone systems (Aircall, Voys) to the helpdesk
// (contracts/phone.md). Every call is documented on the caller's ticket as one call entry
// in its timeline, kept current as the call moves on, and its end runs the call_ended
// automations. Provider adapters (internal/phone/provider) only translate protocols: the
// engine owns connections, sealed credentials, matching, placement and every ticket write.
package phone

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/phone/provider"
	"github.com/useopendesk/opendesk/internal/phone/provider/aircall"
	"github.com/useopendesk/opendesk/internal/phone/provider/voys"
)

// Service is the phone engine of one set of dependencies.
type Service struct {
	d        *deps.Deps
	adapters map[string]provider.Adapter
	http     *http.Client
	// lineCache holds each connection's provider numbers for linesTTL (by connection id).
	lineCache sync.Map
}

// linesTTL is how long a connection's numbers are reused before the provider is asked
// again: the automation editor asks for them on every open, and numbers rarely change.
const linesTTL = 10 * time.Minute

// cachedLines is a connection's numbers as the provider listed them, for the credentials
// they were listed with (a hash of the sealed credentials, so new ones list again).
type cachedLines struct {
	creds [32]byte
	at    time.Time
	lines []provider.Line
}

var services sync.Map

// For returns the engine for d, with every provider compiled in.
func For(d *deps.Deps) *Service {
	if got, ok := services.Load(d); ok {
		return got.(*Service)
	}
	got, _ := services.LoadOrStore(d, New(d, aircall.Adapter{}, voys.Adapter{}))
	return got.(*Service)
}

// New builds an engine with these adapters (tests give theirs).
func New(d *deps.Deps, adapters ...provider.Adapter) *Service {
	s := &Service{d: d, adapters: map[string]provider.Adapter{}, http: &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	for _, a := range adapters {
		s.adapters[a.Describe().Key] = a
	}
	return s
}

func Register(d *deps.Deps, api huma.API, r chi.Router) { For(d).Register(api, r) }

func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	s := For(d)
	river.AddWorker(w, &inboundWorker{s: s})
	river.AddWorker(w, &cardWorker{s: s})
	river.AddWorker(w, &pruneWorker{s: s})
}

func Periodic(*deps.Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(6*time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return jobs.PhonePruneArgs{}, nil }, nil)}
}

func Background(context.Context, *deps.Deps) {}

// descriptors lists the providers, by name.
func (s *Service) descriptors() []provider.Descriptor {
	out := make([]provider.Descriptor, 0, len(s.adapters))
	for _, a := range s.adapters {
		out = append(out, a.Describe())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// describe is a provider's descriptor; one not compiled in can do nothing.
func (s *Service) describe(key string) provider.Descriptor {
	if a, ok := s.adapters[key]; ok {
		return a.Describe()
	}
	return provider.Descriptor{Key: key}
}

// PhoneConnection is a workspace's connection to a phone provider (contracts/phone.md).
type PhoneConnection struct {
	ID              uuid.UUID  `json:"id"`
	WorkspaceID     uuid.UUID  `json:"-"`
	Provider        string     `json:"provider"`
	Name            string     `json:"name"`
	AccountName     string     `json:"account_name"`
	Status          string     `json:"status" enum:"connected,error"`
	LastError       string     `json:"last_error"`
	HealthStatus    string     `json:"health_status" enum:"unknown,healthy,action_required,error"`
	HealthDetail    string     `json:"health_detail"`
	HealthCheckedAt *time.Time `json:"health_checked_at"`
	ReuseDays       int        `json:"reuse_days"`
	AssignAnswered  bool       `json:"assign_answered"`
	InsightCards    bool       `json:"insight_cards"`
	LastEventAt     *time.Time `json:"last_event_at"`
	// WebhookURL is where the provider sends events: admins only, as it reaches the
	// connection (contracts/phone.md "Security").
	WebhookURL string    `json:"webhook_url,omitempty" doc:"Admins only"`
	CreatedAt  time.Time `json:"created_at"`

	credentialsEnc   []byte
	webhookID        string
	webhookSecretEnc []byte
}
