package panels

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/domain"
)

// Data is what a panel shows for one ticket. Commerce panels (shopify) fill Customer and
// Orders, the same shape whatever the store, so another commerce source plugs into the
// same section; http panels fill Blocks (an answer of version 2) or Sections (version 1).
type Data struct {
	PanelID   uuid.UUID    `json:"panel_id"`
	Kind      string       `json:"kind" enum:"shopify,http"`
	State     string       `json:"state" enum:"ok,empty,no_contact,error" doc:"empty: the product knows nothing about this contact; no_contact: the ticket's contact has no email"`
	Message   string       `json:"message,omitempty" doc:"error: what went wrong, in a few words"`
	Code      string       `json:"code,omitempty" enum:"timeout,rate_limited,ticket,refused,status,answer" doc:"error: why, when it is one of these, for the app to say in the reader's language: no answer in time, too many lookups, the ticket could not be read, the credentials were refused, the URL answered an error status, its answer was too large or not the expected JSON. Absent for other failures, whose message is the product's or the network's words"`
	Customer  *Customer    `json:"customer,omitempty"`
	Orders    []Order      `json:"orders,omitempty"`
	Sections  []Section    `json:"sections,omitempty" doc:"http: a version 1 answer's sections"`
	Blocks    []PanelBlock `json:"blocks,omitempty" doc:"http: a version 2 answer's blocks, cleaned"`
	FetchedAt time.Time    `json:"fetched_at"`
}

// Customer is the store's own record of the contact. Amounts are decimal strings. A field the
// panel does not show is left out, and a list is empty.
type Customer struct {
	Name        string   `json:"name"`
	URL         string   `json:"url" doc:"The customer in the store's admin"`
	Email       string   `json:"email,omitempty" doc:"The customer's default email in the store (field email)"`
	OrdersCount *int64   `json:"orders_count,omitempty" doc:"Field orders_count"`
	TotalSpent  string   `json:"total_spent,omitempty" doc:"Field total_spent"`
	Currency    string   `json:"currency,omitempty" doc:"total_spent's currency"`
	Tags        []string `json:"tags" doc:"Field tags; empty when the panel leaves them out"`
}

// Order is one of the contact's last orders, newest first. A field the panel does not show is
// left out, and a list is empty.
type Order struct {
	ID                string      `json:"id"`
	Number            string      `json:"number" doc:"As the store shows it, such as #1042"`
	URL               string      `json:"url" doc:"The order in the store's admin"`
	CreatedAt         time.Time   `json:"created_at"`
	Total             string      `json:"total"`
	Currency          string      `json:"currency"`
	FinancialStatus   string      `json:"financial_status,omitempty" doc:"Lowercase, such as paid, pending, refunded, partially_refunded"`
	FulfillmentStatus string      `json:"fulfillment_status,omitempty" doc:"Lowercase, such as fulfilled, unfulfilled, partially_fulfilled"`
	Cancelled         bool        `json:"cancelled"`
	CancelledAt       *time.Time  `json:"cancelled_at,omitempty"`
	Refunded          string      `json:"refunded,omitempty" doc:"Decimal, what was refunded; left out when nothing was"`
	DiscountCodes     []string    `json:"discount_codes"`
	Shipping          *Shipping   `json:"shipping,omitempty" doc:"Left out when the order has neither an address nor a shipping method (a pickup, a digital order)"`
	Items             []OrderItem `json:"items"`
	Tracking          []Tracking  `json:"tracking"`
}

// Shipping is where an order goes and how: the city and country only, never the street.
type Shipping struct {
	City    string `json:"city,omitempty"`
	Country string `json:"country,omitempty" doc:"ISO 3166-1 alpha-2, such as BE"`
	Method  string `json:"method,omitempty" doc:"The shipping method as the checkout named it"`
}

// OrderItem is one line of an order.
type OrderItem struct {
	Title    string `json:"title"`
	Variant  string `json:"variant,omitempty"`
	Quantity int    `json:"quantity"`
	Price    string `json:"price,omitempty" doc:"Unit price before discounts, decimal, in the order's currency (field line_prices)"`
}

// Tracking is one parcel of an order.
type Tracking struct {
	Company string `json:"company,omitempty"`
	Number  string `json:"number,omitempty"`
	URL     string `json:"url,omitempty"`
}

// Section is a titled list of labelled values, as an http panel answers it.
type Section struct {
	Title string        `json:"title"`
	Items []SectionItem `json:"items"`
}

// SectionItem is one value; a URL makes it a link.
type SectionItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
	URL   string `json:"url,omitempty"`
}

// subject is what a lookup is about: the ticket and its contact. An http panel's request also
// carries the references found in the ticket, the contact's addresses and phone (ticketContext),
// whether an agent asked again and the reader's language.
type subject struct {
	TicketID     uuid.UUID
	TicketNumber int64
	Subject      string
	ContactID    *uuid.UUID
	Email        string
	Name         string
	References   []string
	Emails       []string
	Phone        string
	Refresh      bool
	Locale       string
}

func subjectOf(t *domain.Ticket) subject {
	sub := subject{TicketID: t.ID, TicketNumber: t.Number, Subject: t.Subject}
	if t.Contact != nil {
		id := t.Contact.ID
		sub.ContactID, sub.Name = &id, t.Contact.Name
		if t.Contact.Email != nil {
			sub.Email = strings.ToLower(strings.TrimSpace(*t.Contact.Email))
		}
	}
	return sub
}

// read answers the panel for the ticket: from the cache when it answered in the last
// minute (a failure, the last 15 seconds; an http answer may choose its own time), else from
// the product, within fetchTimeout. A panel makes lookupsPerMinute lookups at most, and an http
// panel's reading of the ticket (kept per ticket version) counts as the lookup it leads to. A
// workspace's http panels make readingsPerMinute readings at most, together. locale is the
// reader's language (viewerLocale), told to http panels and kept apart in their cache.
func (s *Service) read(ctx context.Context, x *row, t *domain.Ticket, refresh bool, locale string) Data {
	sub := subjectOf(t)
	sub.Refresh = refresh
	base := Data{PanelID: x.ID, Kind: x.Kind, FetchedAt: time.Now().UTC()}
	if x.Kind == KindShopify && sub.Email == "" {
		base.State = "no_contact"
		return base
	}
	// allow takes the panel's lookup once: a reading of the ticket and the lookup it leads to
	// count as one.
	allowed := false
	allow := func() bool {
		allowed = allowed || s.d.Limiter.Allow("ticket-panel:lookup:"+x.ID.String(), lookupsPerMinute, time.Minute)
		return allowed
	}
	key := cacheKey{panel: x.ID, version: x.UpdatedAt.UnixNano(), email: sub.Email}
	if x.Kind == KindHTTP {
		// The URL is told the ticket too, so its answer may differ per ticket, and a new
		// reference in it asks again. A reading of the ticket the cache does not keep takes the
		// panel's lookup and one of the workspace's readings before it starts, so a workspace's
		// readings are bounded whatever its panels and credentials.
		ck := contextKey{panel: x.ID, version: x.UpdatedAt.UnixNano(), ticket: t.ID, ticketVersion: t.UpdatedAt.UnixNano(), email: sub.Email}
		if sub.ContactID != nil {
			ck.contact = *sub.ContactID
		}
		c, ok := s.contexts.get(ck)
		if refresh || !ok {
			if !allow() || !s.d.Limiter.Allow("ticket-panel:read:"+x.WorkspaceID.String(), readingsPerMinute, time.Minute) {
				base.State, base.Message, base.Code = "error", "Too many lookups. Try again in a minute.", "rate_limited"
				return base
			}
			var err error
			if c, err = s.lookupContext(ctx, x, t, sub); err != nil {
				s.d.Log.Warn("ticket panel context", "panel", x.ID, "err", err)
				base.State, base.Message, base.Code = "error", "Couldn't read the ticket.", "ticket"
				return base
			}
			s.contexts.set(ck, c, cacheTTL)
		}
		sub.References, sub.Emails, sub.Phone = c.References, c.Emails, c.Phone
		// The URL may answer in the reader's language: an answer is kept per language.
		sub.Locale = locale
		key.ticket, key.refs, key.locale = sub.TicketID, strings.Join(sub.References, "\n"), locale
	}
	if !refresh {
		if d, ok := s.cache.get(key); ok {
			return d
		}
	}
	if !allow() {
		base.State, base.Message, base.Code = "error", "Too many lookups. Try again in a minute.", "rate_limited"
		return base
	}
	lctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	var err error
	ttl := cacheTTL
	switch x.Kind {
	case KindShopify:
		err = s.readShopify(lctx, x, sub, &base)
	case KindHTTP:
		ttl, err = s.readHTTP(lctx, x, sub, &base)
	}
	if err != nil {
		base.State, base.Message, base.Code, base.Customer, base.Orders, base.Sections, base.Blocks = "error", lookupMessage(err), lookupCode(err), nil, nil, nil, nil
		ttl = errTTL
		s.d.Log.Info("ticket panel lookup failed", "panel", x.ID, "kind", x.Kind, "host", x.api().Host, "err", lookupMessage(err))
	}
	// A refused credential, or a lookup that works again, shows in settings (a write only
	// when the status changes).
	s.markStatus(context.WithoutCancel(ctx), x, err)
	if ttl > 0 {
		s.cache.set(key, base, ttl)
	}
	return base
}

// lookupCode is the reason the app says in the reader's language (Data.Code), or "" for a
// failure only the product's or the network's words describe.
func lookupCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errAuth):
		return "refused"
	}
	var ae *answerError
	if errors.As(err, &ae) {
		return ae.code
	}
	return ""
}

// answerError is an answer of the panel's URL that cannot be shown, said in a few words:
// code "status" for an error status, "answer" for one too large or not the expected JSON.
type answerError struct{ msg, code string }

func (e *answerError) Error() string { return e.msg }

// lookupMessage is what an agent reads when a lookup fails: the product's words or how
// reaching it failed, never a URL (a path or query can carry a secret).
func lookupMessage(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "No answer in time."
	case errors.Is(err, errAuth):
		return clip(err.Error(), 200)
	}
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 && strings.Contains(msg[:i], "://") {
		msg = msg[i+2:] // *url.Error: drop "Post \"https://…\""
	}
	return clip(msg, 200)
}

// cacheKey is one lookup: a panel at a version (a changed panel never answers from
// before the change), for an email (and a ticket, its references and the reader's language,
// for http panels).
type cacheKey struct {
	panel   uuid.UUID
	version int64
	email   string
	ticket  uuid.UUID
	refs    string
	locale  string
}

// contextKey is one reading of a ticket for an http panel: the panel at a version (its pattern)
// and the ticket at a version, for its contact. A new message, a deleted one or a note sent as a
// reply changes the ticket's updated_at, so the next reading leaves no hidden message in it.
type contextKey struct {
	panel         uuid.UUID
	version       int64
	ticket        uuid.UUID
	ticketVersion int64
	contact       uuid.UUID
	email         string
}

func (k cacheKey) panelID() uuid.UUID   { return k.panel }
func (k contextKey) panelID() uuid.UUID { return k.panel }

type panelKey interface {
	comparable
	panelID() uuid.UUID
}

type cached[V any] struct {
	v       V
	expires time.Time
}

// ttlCache keeps at most max recent values in this process: answers, and readings of tickets.
// Several API processes each keep their own.
type ttlCache[K panelKey, V any] struct {
	mu  sync.Mutex
	max int
	m   map[K]cached[V]
	now func() time.Time
}

func newTTLCache[K panelKey, V any](max int) *ttlCache[K, V] {
	return &ttlCache[K, V]{max: max, m: map[K]cached[V]{}, now: time.Now}
}

func (c *ttlCache[K, V]) get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	if !ok || c.now().After(v.expires) {
		var zero V
		return zero, false
	}
	return v.v, true
}

func (c *ttlCache[K, V]) set(k K, v V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		now := c.now()
		for key, v := range c.m {
			if now.After(v.expires) {
				delete(c.m, key)
			}
		}
		if len(c.m) >= c.max {
			c.m = map[K]cached[V]{}
		}
	}
	c.m[k] = cached[V]{v: v, expires: c.now().Add(ttl)}
}

// forget drops a removed panel's values.
func (c *ttlCache[K, V]) forget(panel uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.m {
		if key.panelID() == panel {
			delete(c.m, key)
		}
	}
}
