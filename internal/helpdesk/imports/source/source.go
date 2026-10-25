// Package source is what every import source implements (contracts/imports.md): it reads
// another helpdesk through its API and hands back tickets, messages and articles in one
// shape. The import engine (package imports) writes them; a source never touches the
// database.
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Credentials are the fields a user pasted for a source (contracts/imports.md).
type Credentials map[string]string

// Account is the source account an import reads.
type Account struct {
	// Key identifies the account for good: records of what was imported are keyed by it,
	// so a second import of the same account finds what the first one wrote.
	Key  string
	Name string // shown: "Acme Support"
	URL  string // the account's web address, https
}

// Person is a customer as the source knows them.
type Person struct {
	ID      string
	Email   string
	Name    string
	Phone   string
	Company string // company or organization name
	Domain  string // the company's domain, when the source has one
}

// Agent is a person who works in the source helpdesk.
type Agent struct {
	ID    string
	Name  string
	Email string
}

// Group is a source's team (Zendesk group, Help Scout mailbox, Freshdesk group, Intercom team).
type Group struct {
	ID   string
	Name string
}

// Scan is what Check found.
type Scan struct {
	Account  Account
	Tickets  int // -1 when the source does not say
	Articles int // -1 when the source does not say; 0 when it has none
	// HasArticles says the credentials can read a help center at all.
	HasArticles bool
	Agents      []Agent
	Groups      []Group
}

// Ticket is one conversation. Status, Priority and Channel are already opendesk values.
type Ticket struct {
	ID      string
	Ref     string // how the source shows it: "#1234"
	URL     string // the ticket in the source's app; "" when unknown
	Subject string
	// Status: open | pending | on_hold | solved | closed. Skip says to leave the ticket out
	// (deleted, spam).
	Status   string
	Skip     bool
	Priority string // low | normal | high | urgent
	Channel  string // email | chat | api | portal
	// Requester is the customer; nil when the source has none.
	Requester  *Person
	AssigneeID string // a source Agent.ID
	GroupID    string // a source Group.ID
	Tags       []string
	CSAT       int // 1..5; 0 = none
	CreatedAt  time.Time
	UpdatedAt  time.Time
	SolvedAt   *time.Time
	// Messages are oldest first. Loaded says Messages are complete; when false the engine
	// calls Source.Load first.
	Messages []Message
	Loaded   bool
}

// Message is a reply or an internal note.
type Message struct {
	ID string
	// Agent: written by the team (outbound). Otherwise written by the customer (inbound).
	Agent bool
	Note  bool
	// AuthorID is the source Agent.ID of an agent's message.
	AuthorID string
	Author   Person
	// HTML is the body as the source has it; the engine sanitizes it. Text is used when
	// there is no HTML.
	HTML        string
	Text        string
	To, Cc      []string
	CreatedAt   time.Time
	Attachments []Attachment
	// MessageID, InReplyTo and References are the email's own headers, as "<id>", when the
	// source has them: a reply to the message then threads into its ticket.
	MessageID  string
	InReplyTo  string
	References []string
}

// Threading is an email's Message-ID, In-Reply-To and References, each as "<id>".
type Threading struct {
	MessageID  string
	InReplyTo  string
	References []string
}

// Threader is a source whose listings leave out its emails' threading headers and that
// reads them one email at a time. The engine asks for them after the tickets are in, newest
// first (the import's threading phase), so an import is not held up by them. An error
// that is not a rate limit, refused credentials, or the source being unreachable or
// failing leaves that one email without headers.
type Threader interface {
	Threading(ctx context.Context, messageID string) (Threading, error)
}

// Attachment is a file on a message, fetched with Source.Download.
type Attachment struct {
	ID          string
	Name        string
	ContentType string
	Size        int64 // 0 when unknown
	URL         string
	Inline      bool
	// Data is the content when the source sends it in the listing (CSV, Help Scout).
	Data []byte
}

// Article is a help center article.
type Article struct {
	ID         string
	Title      string
	HTML       string
	Collection string // its section, folder or collection name; "" = none
	Published  bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Page is one page of a listing. Next is the cursor of the following page; "" = the end.
type Page[T any] struct {
	Items []T
	Next  string
}

// Source reads one account. Implementations are safe to call from one goroutine at a time.
type Source interface {
	// Check signs in and counts what is there. It is the first call; errors are the
	// sentinel errors below when the source answered.
	Check(ctx context.Context) (Scan, error)
	// Tickets lists tickets oldest first from cursor ("" = the start). since, when set,
	// keeps tickets created on or after it (an import's "since" option); updatedSince,
	// when set, keeps tickets updated on or after it (a sync).
	Tickets(ctx context.Context, cursor string, since, updatedSince *time.Time) (Page[Ticket], error)
	// Load fills a ticket's Messages (and anything else the listing left out).
	Load(ctx context.Context, t *Ticket) error
	// Articles lists help center articles from cursor; ErrUnsupported when there is none.
	Articles(ctx context.Context, cursor string) (Page[Article], error)
	// Download opens an attachment. The caller closes it and bounds what it reads.
	Download(ctx context.Context, a Attachment) (io.ReadCloser, error)
}

var (
	// ErrAuth: the source refused the credentials.
	ErrAuth = errors.New("the source refused the credentials")
	// ErrNotFound: no such account (subdomain, domain).
	ErrNotFound = errors.New("no such account")
	// ErrUnsupported: the source has no such thing (articles without a Docs key).
	ErrUnsupported = errors.New("not supported by this source")
	// ErrInvalid: the credentials are malformed (a field missing or not well formed).
	ErrInvalid = errors.New("invalid credentials")
)

// InvalidError names the field that is missing or malformed. It wraps ErrInvalid.
type InvalidError struct {
	Field  string
	Detail string
}

func (e *InvalidError) Error() string { return e.Detail }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// Invalid returns an InvalidError for field.
func Invalid(field, detail string) error { return &InvalidError{Field: field, Detail: detail} }

// RateLimited asks the caller to wait before the next request.
type RateLimited struct{ RetryAfter time.Duration }

func (e *RateLimited) Error() string {
	return fmt.Sprintf("rate limited for %s", e.RetryAfter.Round(time.Second))
}

// RemoteError is a source answering with an error status other than the ones above.
type RemoteError struct {
	Status int
	Detail string
}

func (e *RemoteError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("status %d: %s", e.Status, e.Detail)
	}
	return fmt.Sprintf("status %d", e.Status)
}

var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Subdomain reads an account subdomain the way people paste it: "acme", "acme.zendesk.com",
// "https://acme.zendesk.com/agent". suffix is the provider's domain (".zendesk.com").
func Subdomain(raw, suffix, field string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, suffix)
	if s == "" {
		return "", Invalid(field, "Enter your account's address.")
	}
	if !labelPattern.MatchString(s) {
		return "", Invalid(field, "Use the part before "+suffix+", letters, digits and hyphens only.")
	}
	return s, nil
}

// Required returns the trimmed field or an InvalidError naming it.
func Required(c Credentials, field, label string) (string, error) {
	v := strings.TrimSpace(c[field])
	if v == "" {
		return "", Invalid(field, "Enter the "+label+".")
	}
	if len(v) > 4096 {
		return "", Invalid(field, "The "+label+" is too long.")
	}
	return v, nil
}

// Retry reads a Retry-After header (seconds or an HTTP date), with a floor and a cap.
func Retry(h http.Header, fallback time.Duration) time.Duration {
	d := fallback
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		var secs int
		if _, err := fmt.Sscanf(v, "%d", &secs); err == nil && secs >= 0 {
			d = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			d = time.Until(t)
		}
	}
	if d < time.Second {
		d = time.Second
	}
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	return d
}

// Status turns an error status into one of the errors above. 401 and 403 are ErrAuth,
// 404 is notFound (ErrNotFound for an account lookup, a RemoteError otherwise) and 429
// is RateLimited.
func Status(resp *http.Response, notFound error) error {
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrAuth
	case resp.StatusCode == http.StatusTooManyRequests:
		return &RateLimited{RetryAfter: Retry(resp.Header, time.Minute)}
	case resp.StatusCode == http.StatusNotFound && notFound != nil:
		return notFound
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return &RemoteError{Status: resp.StatusCode, Detail: clip(strings.TrimSpace(string(body)), 300)}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Strings drops empty values and trims the rest.
func Strings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Unix turns seconds since the epoch into a UTC time; 0 is the zero time.
func Unix(secs int64) time.Time {
	if secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0).UTC()
}

// Time parses an RFC 3339 time, UTC; "" or a malformed value is the zero time.
func Time(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
