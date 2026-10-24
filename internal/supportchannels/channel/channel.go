// Package channel is the stable provider boundary for agent-facing support
// channels. Providers translate their wire formats here; they never read or
// write helpdesk tables directly.
package channel

import (
	"context"
	"errors"
	"io"
	"net/http"
)

type Field struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Secret     bool   `json:"secret"`
	Generated  bool   `json:"generated,omitempty"`
	Discovered bool   `json:"discovered,omitempty"`
	Help       string `json:"help,omitempty"`
}

type Descriptor struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
	// Configures is set by the registry for adapters that set themselves up
	// (Configurer); the others need an admin to enter webhook_url at the provider.
	Configures bool `json:"configures"`
}

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Handle is the bot's @username where people address it by one (Telegram).
	Handle string `json:"-"`
}

// Destination is a provider location which can receive mirrored tickets.
// Providers may discover these live, or report them through webhook events.
type Destination struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Available bool   `json:"available"`
}

// Setup contains the public endpoints a provider may configure for a
// connection. Credentials remain provider-owned and never cross this boundary.
type Setup struct {
	WebhookURL string
	// Foreign reports whether an address is another connection's webhook on this
	// instance, so one bot's provider-wide setting is never taken from it.
	Foreign func(url string) bool
}

// ErrInUse is returned by Configure when the provider-wide setting it would
// change belongs to another connection.
var ErrInUse = errors.New("the provider setting belongs to another connection")

type Health struct {
	Status string `json:"status"` // healthy | action_required | error
	Detail string `json:"detail"`
}

type Ticket struct {
	ID       string
	Number   int64
	Subject  string
	Customer string
	Status   string
	Priority string
	Assignee string
	URL      string
}

// Action is a provider-neutral control. The engine owns its meaning and every
// authorization check; adapters only translate it to their native UI.
type Action struct {
	Command string
	Value   string
	Label   string
	Style   string // default | primary | danger | link
	URL     string
}

// Message is a bounded piece of text with optional native controls.
type Message struct {
	Text    string
	Actions []Action
}

// Attachment is safe metadata at the provider boundary. URL is set only for an
// opendesk attachment being mirrored out; provider files arriving in an Inbound
// are opened by AttachmentDownloader so provider credentials never enter the
// engine or an event payload.
type Attachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	URL         string `json:"url,omitempty"`
	Preview     bool   `json:"preview,omitempty"`
}

// RichMessage is a mirrored ticket message. ViewURL is present when Text was
// truncated, and is also the durable fallback for attachment links.
type RichMessage struct {
	Text        string
	Attachments []Attachment
	ViewURL     string
}

type MessageRef struct{ ID string }

type Thread struct {
	ID     string
	RootID string
	// ChannelID is where the provider delivers messages written in the thread, when
	// that is not the destination channel (a Discord thread is its own channel).
	ChannelID string
}

// Inbound is a provider-neutral interaction. Command is either a deliberately
// small explicit command or chat, whose model output must cross the same typed
// action boundary before it can change a ticket.
type Inbound struct {
	EventID           string
	AccountID         string
	ExternalChannelID string
	ExternalThreadID  string
	ExternalUserID    string
	Command           string // chat | link | reply | note | assign_self | status | priority | confirm | cancel
	Text              string
	Value             string
	// Directed is true when the provider says the message explicitly addressed
	// the bot. It gates new conversations outside mapped ticket threads.
	Directed bool `json:",omitempty"`
	// Mention is the @handle a message opens with, for providers that cannot tell
	// whether a handle names the bot (Telegram). The engine treats the message as
	// directed, and drops the handle from Text, only when it is the connection's
	// own account.
	Mention string `json:",omitempty"`
	// TicketNumber names the ticket when the provider can't deliver the command
	// inside its thread (a Slack slash command). The engine resolves it to the
	// ticket's mirrored thread on the same connection.
	TicketNumber int64 `json:",omitempty"`
	// SourceMessageID is the provider message which held the native control that
	// sent this action. It is set only for controls (buttons and selects), so the
	// engine can tell a card action from a typed command and remove consumed
	// Confirm and Cancel buttons. A confirm or cancel carries its preview's nonce
	// as Value.
	SourceMessageID string `json:",omitempty"`
	// SourceText is the provider's copy of that message's text, for providers
	// whose edits must restate it to remove the controls (Slack).
	SourceText string `json:",omitempty"`
	// ExternalMessageID names an ordinary provider message. It maps a typed
	// reply or note back to the ticket message it created without making the
	// message look like a native control.
	ExternalMessageID string `json:",omitempty"`
	// Respond is an opaque provider handle for answering the sender privately
	// (Slack's response_url, Discord's interaction, Telegram's callback query),
	// used by a Responder.
	Respond string `json:",omitempty"`
	// Mutation is edit or delete for a provider-side change to ExternalMessageID.
	// The engine applies normal ticket permissions and never rewrites a reply
	// which may already have reached a customer.
	Mutation  string       `json:",omitempty"`
	Files     []Attachment `json:",omitempty"`
	UploadIDs []string     `json:"-"`
	// DestinationID is filled by the engine after it resolves the mapped thread.
	DestinationID string `json:"-"`
}

// WebhookResult separates provider protocol acknowledgements from normalized
// actions. Body is returned immediately after the events and their jobs have
// been committed.
type WebhookResult struct {
	Events       []Inbound
	Destinations []Destination
	// Control reports a callback from one of opendesk's native controls, which
	// proves the provider's interactivity setting reaches opendesk.
	Control     bool
	Status      int
	ContentType string
	Body        []byte
}

type Adapter interface {
	Describe() Descriptor
	Validate(context.Context, *http.Client, map[string]string) (Account, error)
	Verify(http.Header, []byte, map[string]string) error
	Parse(http.Header, []byte) (WebhookResult, error)
	CreateThread(context.Context, *http.Client, map[string]string, string, Ticket) (Thread, error)
	Post(context.Context, *http.Client, map[string]string, string, string) error
	Update(context.Context, *http.Client, map[string]string, string, Ticket) error
}

// InteractiveAdapter is implemented by providers with native buttons or
// selects. The base Adapter remains text-compatible for future providers.
// ClearActions removes the controls from a message the bot posted and keeps
// its text; it is given the Inbound that a control on that message sent.
type InteractiveAdapter interface {
	PostInteractive(context.Context, *http.Client, map[string]string, string, Message) (MessageRef, error)
	ClearActions(context.Context, *http.Client, map[string]string, Inbound) error
}

// RichAdapter posts and mutates mirrored ticket messages. Adapters which do
// not implement it keep the text-only Post behavior.
type RichAdapter interface {
	PostRich(context.Context, *http.Client, map[string]string, string, RichMessage) (MessageRef, error)
	UpdateRich(context.Context, *http.Client, map[string]string, string, string, RichMessage) error
	DeleteRich(context.Context, *http.Client, map[string]string, string, string) error
}

// RichTextLimiter reports a provider's safe text budget after its own framing.
// The engine applies the lower of this and the destination setting so provider
// clipping always gets a View in opendesk fallback.
type RichTextLimiter interface {
	RichTextLimit() int
}

// AttachmentDownloader opens a provider file. The engine owns size limits,
// executable refusal, content sniffing and storage.
type AttachmentDownloader interface {
	DownloadAttachment(context.Context, *http.Client, map[string]string, Attachment) (io.ReadCloser, error)
}

// Responder answers the person behind an Inbound privately, through its
// Respond handle. Respond with empty text only acknowledges the action, for
// providers that wait for an answer (Telegram's callback queries).
type Responder interface {
	Respond(context.Context, *http.Client, map[string]string, string, string) error
}

// Discoverer is an optional capability for providers which expose destinations
// through an API. Providers without it may discover destinations from signed
// webhook events instead (Telegram, for example).
type Discoverer interface {
	Discover(context.Context, *http.Client, map[string]string) ([]Destination, error)
}

// AccountDiscoverer lists provider accounts selectable during onboarding. It
// is used when a connection is scoped to an account such as a Discord server.
type AccountDiscoverer interface {
	DiscoverAccounts(context.Context, *http.Client, map[string]string) ([]Account, error)
}

// Configurer is an optional capability for providers that allow opendesk to
// register its webhook endpoint or commands with the existing credentials.
type Configurer interface {
	Configure(context.Context, *http.Client, map[string]string, Setup) error
}

// HealthChecker verifies the provider-side setup without changing it.
type HealthChecker interface {
	Health(context.Context, *http.Client, map[string]string, Setup) (Health, error)
}

// Subscriber is an optional provider capability for products which deliver chat
// messages over a long-lived connection instead of the webhook in Adapter. The
// engine owns persistence, deduplication and jobs through emit, just as it does
// for webhook events.
type Subscriber interface {
	// Subscribe runs one subscription until ctx ends or it fails. The engine
	// reconnects after an error, with backoff, unless the error is a FatalError.
	Subscribe(context.Context, *http.Client, map[string]string, Stream) error
}

// Stream is what the engine hands a Subscriber.
type Stream struct {
	// Emit persists events and enqueues their jobs; an error ends the subscription.
	Emit func(context.Context, []Inbound) error
	// Connected reports that the provider accepted the session.
	Connected func()
}

// FatalError ends a subscription until an admin changes the connection: the
// provider refused the credentials or settings, so reconnecting can't help.
// Reason is shown to admins as the connection's error.
type FatalError struct{ Reason string }

func (e *FatalError) Error() string { return e.Reason }
