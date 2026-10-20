// Package channel is the stable provider boundary for agent-facing support
// channels. Providers translate their wire formats here; they never read or
// write helpdesk tables directly.
package channel

import (
	"context"
	"net/http"
)

type Field struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
	Help   string `json:"help,omitempty"`
}

type Descriptor struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
}

type Account struct {
	ID   string
	Name string
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
	Command           string // chat | link | reply | note | assign_self | status | priority
	Text              string
	Value             string
}

// WebhookResult separates provider protocol acknowledgements from normalized
// actions. Body is returned immediately after the events and their jobs have
// been committed.
type WebhookResult struct {
	Events      []Inbound
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

// Subscriber is an optional provider capability for products which deliver chat
// messages over a long-lived connection instead of the webhook in Adapter. The
// engine owns persistence, deduplication and jobs through emit, just as it does
// for webhook events.
type Subscriber interface {
	Subscribe(context.Context, *http.Client, map[string]string, func(context.Context, []Inbound) error) error
}
