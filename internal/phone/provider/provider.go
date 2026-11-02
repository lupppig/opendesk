// Package provider is the boundary between the phone engine (internal/phone) and a cloud
// phone system such as Aircall (contracts/phone.md). An adapter speaks its provider's
// protocol and nothing else: it never imports a helpdesk package, reads a table or
// decides anything about tickets. The engine owns connections, sealed credentials,
// matching, placement, the timeline and automations.
package provider

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// Descriptor describes a provider for the settings page, and what it can do.
type Descriptor struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
	// ManualWebhook: the provider has no API for webhooks, so an admin adds the
	// connection's webhook URL in the provider by hand. The URL then carries the webhook's
	// secret, which the engine checks; the adapter's webhook methods and Verify answer
	// ErrUnsupported.
	ManualWebhook bool `json:"manual_webhook"`
	// InsightCards: the provider shows a card on a ringing call (PushCard).
	InsightCards bool `json:"insight_cards"`
}

// Field is one credential an admin pastes.
type Field struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

// Account is the provider account the credentials open.
type Account struct {
	// ID is the provider's id of the account, when its API needs it in paths: the engine
	// seals it with the credentials, under AccountIDKey, for the adapter's later requests.
	ID   string
	Name string
}

// AccountIDKey is where the engine keeps Account.ID in a connection's credentials.
const AccountIDKey = "account_id"

// Webhook is the provider-side webhook opendesk registered: its id, to remove it, and the
// secret its events carry, to verify them.
type Webhook struct {
	ID     string
	Secret string
}

// Stages of a call, in the order they happen. A call only ever moves forward.
const (
	StageRinging  = "ringing"
	StageAnswered = "answered"
	StageEnded    = "ended"
)

// Event kinds.
const (
	KindCall       = "call"
	KindSummary    = "summary"
	KindTranscript = "transcript"
)

// Event is one provider webhook, normalized. Only what the engine needs crosses the
// boundary; the raw payload is never stored.
type Event struct {
	// ID deduplicates the provider's retries of the same event.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Type is the provider's event name (call.answered), for logs.
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	// Final marks the event after which the call's data no longer changes (Aircall's
	// call.ended); call_ended automations run on it, once.
	Final bool `json:"final,omitempty"`
	// Continues is the provider's id of a call this event's call continues: a transfer
	// joins two calls, which go on under one of their ids (CallID). The engine documents
	// them as the one call it already has.
	Continues  string    `json:"continues,omitempty"`
	Call       *Call     `json:"call,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Transcript string    `json:"transcript,omitempty"`
	At         time.Time `json:"at"`
}

// Call is the call's state as one event saw it. Empty values mean "not known yet".
type Call struct {
	Direction string `json:"direction"` // inbound | outbound
	Stage     string `json:"stage"`
	// Number is the customer's number as the provider gave it; Anonymous when withheld.
	Number    string `json:"number"`
	Anonymous bool   `json:"anonymous,omitempty"`
	// LineID and LineName are the provider number the call came in on or went out from.
	LineID     string     `json:"line_id,omitempty"`
	LineName   string     `json:"line_name,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	Duration   int        `json:"duration,omitempty"`
	// MissedReason is the provider's reason a call was missed (out_of_opening_hours,
	// no_available_agent, ...), in its own words.
	MissedReason string `json:"missed_reason,omitempty"`
	Voicemail    bool   `json:"voicemail,omitempty"`
	Recording    bool   `json:"recording,omitempty"`
	// AssetURL is a lasting provider page for the call's recording or voicemail, which
	// the app also offers as the call at the provider; RecordingURL and VoicemailURL are
	// direct links that expire (LinksValid after the event). https only.
	AssetURL     string        `json:"asset_url,omitempty"`
	RecordingURL string        `json:"recording_url,omitempty"`
	VoicemailURL string        `json:"voicemail_url,omitempty"`
	LinksValid   time.Duration `json:"links_valid,omitempty"`
	// Tags are the provider's labels on the call as of the event, nil when the event did
	// not say. An empty list says there are none, so it survives storage (no omitempty).
	Tags []string `json:"tags"`
	// Comments are the provider users' notes on the call as of the event, nil when the
	// event did not say.
	Comments []Comment `json:"comments"`
	// Agent is the provider user who took or made the call.
	Agent *Person `json:"agent,omitempty"`
	// Caller is the provider's own contact for the number, if it has one.
	Caller *Person `json:"caller,omitempty"`
}

// Comment is a provider user's note on a call.
type Comment struct {
	ID     string     `json:"id"`
	Text   string     `json:"text"`
	Author string     `json:"author,omitempty"`
	At     *time.Time `json:"at,omitempty"`
}

// Line is a provider number calls come in on.
type Line struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Person is a provider user or contact.
type Person struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Card is what an agent sees about a ringing call in the provider's app.
type Card struct {
	Title     string
	TitleLink string
	Lines     []CardLine
}

// CardLine is one labelled line of a Card.
type CardLine struct {
	Label string
	Text  string
	Link  string
}

// ErrIgnored is Parse's answer for an event the engine has no use for: it is
// acknowledged and dropped.
var ErrIgnored = errors.New("phone event ignored")

// ErrUnauthorized is Verify's answer for an event that does not carry the secret.
var ErrUnauthorized = errors.New("phone event not verified")

// ErrUnsupported is an adapter's answer for what its provider does not do (Descriptor).
var ErrUnsupported = errors.New("not supported by this phone provider")

// StatusError is a request the provider answered with an error status. It names the
// operation and the status only, never credentials or the provider's answer.
type StatusError struct {
	Op     string
	Status int
}

func (e *StatusError) Error() string { return e.Op + ": status " + strconv.Itoa(e.Status) }

// AccountRefused is Validate's answer for credentials the provider accepts that open no
// account a connection can use. Detail says why, for the admin; it names no credential.
type AccountRefused struct{ Detail string }

func (e *AccountRefused) Error() string { return e.Detail }

// Refused reports whether err is the provider refusing the credentials (401 or 403), or
// credentials of an account a connection can't use.
func Refused(err error) bool {
	var se *StatusError
	var ar *AccountRefused
	return errors.As(err, &ar) || errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden)
}

// RateLimited is returned by an adapter when the provider asked to wait.
type RateLimited struct{ RetryAfter time.Duration }

func (e *RateLimited) Error() string { return "provider rate limit reached" }

// Adapter is a cloud phone provider.
type Adapter interface {
	Describe() Descriptor
	// Validate checks the credentials and returns the account they open.
	Validate(ctx context.Context, client *http.Client, credentials map[string]string) (Account, error)
	// RegisterWebhook asks the provider to send call events to url.
	RegisterWebhook(ctx context.Context, client *http.Client, credentials map[string]string, url string) (Webhook, error)
	// WebhookActive reports whether a registered webhook still exists and is enabled.
	WebhookActive(ctx context.Context, client *http.Client, credentials map[string]string, id string) (bool, error)
	// DeleteWebhook removes a registered webhook; one already gone is not an error.
	DeleteWebhook(ctx context.Context, client *http.Client, credentials map[string]string, id string) error
	// Verify checks that a webhook body carries the webhook's secret.
	Verify(body []byte, secret string) error
	// Parse normalizes a verified webhook body; ErrIgnored for events of no use.
	Parse(body []byte) (Event, error)
	// Content retrieves the text a summary or transcript event (kind) announced;
	// ErrUnsupported for a kind whose events carry their text.
	Content(ctx context.Context, client *http.Client, credentials map[string]string, kind, callID string) (string, error)
	// Lines lists the account's numbers, for automation conditions.
	Lines(ctx context.Context, client *http.Client, credentials map[string]string) ([]Line, error)
	// PushCard shows a card on a live call in the provider's app.
	PushCard(ctx context.Context, client *http.Client, credentials map[string]string, callID string, card Card) error
}
