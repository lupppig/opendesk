// Package placetel implements Placetel PROFI's REST and Notify APIs for the phone engine.
// Notify callbacks use the engine's manual webhook URL authentication.
package placetel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/useopendesk/opendesk/internal/phone/provider"
)

const API = "https://api.placetel.de/v2"
const maxBody = 1 << 20

// Adapter is Placetel. APIURL is set by tests only, never from connection credentials.
type Adapter struct{ APIURL string }

var _ provider.Adapter = Adapter{}

func (Adapter) Describe() provider.Descriptor {
	return provider.Descriptor{Key: "placetel", Name: "Placetel", ManualWebhook: true, Fields: []provider.Field{{Name: "api_token", Label: "API token", Secret: true}}}
}

func (a Adapter) get(ctx context.Context, c *http.Client, creds map[string]string, path string, out any) error {
	base := API
	if a.APIURL != "" {
		base = strings.TrimRight(a.APIURL, "/")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return errors.New("placetel: invalid API request")
	}
	req.Header.Set("Authorization", "Bearer "+creds["api_token"])
	req.Header.Set("Accept", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return errors.New("placetel: API request failed")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		wait := time.Minute
		if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 120 {
			wait = time.Duration(seconds) * time.Second
		}
		return &provider.RateLimited{RetryAfter: wait}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &provider.StatusError{Op: "placetel API", Status: res.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return errors.New("placetel: invalid API response")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("placetel: invalid API response")
	}
	return nil
}

// Validate checks access to PROFI account users without retaining personal user details.
func (a Adapter) Validate(ctx context.Context, c *http.Client, creds map[string]string) (provider.Account, error) {
	var users []struct {
		ID int64 `json:"id"`
	}
	if err := a.get(ctx, c, creds, "/users?per_page=1", &users); err != nil {
		return provider.Account{}, err
	}
	if len(users) == 0 || users[0].ID <= 0 {
		return provider.Account{}, &provider.AccountRefused{Detail: "This Placetel API token opens no PROFI account users."}
	}
	return provider.Account{Name: "Placetel"}, nil
}

func (Adapter) RegisterWebhook(context.Context, *http.Client, map[string]string, string) (provider.Webhook, error) {
	return provider.Webhook{}, provider.ErrUnsupported
}
func (Adapter) WebhookActive(context.Context, *http.Client, map[string]string, string) (bool, error) {
	return false, provider.ErrUnsupported
}
func (Adapter) DeleteWebhook(context.Context, *http.Client, map[string]string, string) error {
	return provider.ErrUnsupported
}
func (Adapter) Verify([]byte, string) error { return provider.ErrUnsupported }
func (Adapter) Content(context.Context, *http.Client, map[string]string, string, string) (string, error) {
	return "", provider.ErrUnsupported
}
func (Adapter) PushCard(context.Context, *http.Client, map[string]string, string, provider.Card) error {
	return provider.ErrUnsupported
}

// Parse accepts only documented Notify fields. SIP peers and unrelated form data never
// cross the boundary. Placetel supplies no timestamp: times describe receipt of the event.
func (Adapter) Parse(body []byte) (provider.Event, error) {
	invalid := errors.New("placetel: invalid call notification")
	if len(body) > maxBody {
		return provider.Event{}, invalid
	}
	form, err := url.ParseQuery(strings.TrimSpace(string(body)))
	if err != nil {
		return provider.Event{}, invalid
	}
	for _, key := range []string{"event", "call_id", "direction", "from", "to", "type", "duration"} {
		if len(form[key]) > 1 {
			return provider.Event{}, invalid
		}
	}
	typ := form.Get("event")
	stage := ""
	switch typ {
	case "IncomingCall", "OutgoingCall":
		stage = provider.StageRinging
	case "CallAccepted":
		stage = provider.StageAnswered
	case "HungUp":
		stage = provider.StageEnded
	default:
		return provider.Event{}, provider.ErrIgnored
	}
	id := strings.ToLower(form.Get("call_id"))
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != sha256.Size {
		return provider.Event{}, invalid
	}
	direction := form.Get("direction")
	if direction != "in" && direction != "out" || typ == "IncomingCall" && direction != "in" || typ == "OutgoingCall" && direction != "out" || typ == "CallAccepted" && direction != "in" {
		return provider.Event{}, invalid
	}
	customer, line := form.Get("from"), form.Get("to")
	c := &provider.Call{Direction: "inbound", Stage: stage}
	if direction == "out" {
		c.Direction = "outbound"
		customer, line = line, customer
	}
	customer = strings.TrimSpace(customer)
	switch strings.ToLower(customer) {
	case "", "anonymous", "unknown", "private", "withheld", "restricted":
		c.Anonymous = true
	default:
		c.Number = number(customer)
		if c.Number == "" {
			return provider.Event{}, provider.ErrIgnored
		}
	}
	c.LineID = number(line)
	if c.LineID == "" {
		return provider.Event{}, provider.ErrIgnored
	}
	c.LineName = c.LineID
	at := time.Now().UTC()
	switch stage {
	case provider.StageRinging:
		c.StartedAt = &at
	case provider.StageAnswered:
		c.AnsweredAt = &at
	case provider.StageEnded:
		c.EndedAt = &at
		duration, err := strconv.Atoi(form.Get("duration"))
		if err != nil || duration < 0 || duration > int((365*24*time.Hour)/time.Second) {
			return provider.Event{}, invalid
		}
		c.Duration = duration
		switch form.Get("type") {
		case "accepted":
			answered := at.Add(-time.Duration(duration) * time.Second)
			c.AnsweredAt = &answered
		case "voicemail":
			c.Voicemail = true
		case "missed", "blocked", "busy", "canceled", "unavailable", "congestion":
			c.MissedReason = form.Get("type")
		default:
			return provider.Event{}, invalid
		}
	}
	// Hash the normalized event identity, so reordered forms and irrelevant fields do not
	// turn provider retries into extra timeline entries. No raw payload is persisted.
	identity, _ := json.Marshal([]string{id, typ, direction, c.Number, strconv.FormatBool(c.Anonymous), c.LineID, form.Get("type"), strconv.Itoa(c.Duration)})
	sum := sha256.Sum256(identity)
	return provider.Event{ID: hex.EncodeToString(sum[:]), Kind: provider.KindCall, Type: typ, CallID: id, Final: stage == provider.StageEnded, Call: c, At: at}, nil
}

// number keeps national numbers as Notify names them, and excludes internal extensions
// and SIP identities. It does not guess a country for a national number.
func number(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "00") {
		s = "+" + s[2:]
	}
	digits := s
	if strings.HasPrefix(digits, "+") {
		digits = digits[1:]
	}
	if len(digits) < 7 || len(digits) > 15 {
		return ""
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

// Lines uses the national number as its ID, matching Notify's from/to fields.
func (a Adapter) Lines(ctx context.Context, c *http.Client, creds map[string]string) ([]provider.Line, error) {
	out := []provider.Line{}
	for page := 1; page <= 10; page++ {
		var rows []struct {
			Number string `json:"number"`
			Name   string `json:"name"`
		}
		if err := a.get(ctx, c, creds, "/numbers?per_page=100&page="+strconv.Itoa(page), &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			id := number(row.Number)
			if id == "" {
				continue
			}
			name := strings.TrimSpace(strings.ToValidUTF8(row.Name, ""))
			if utf8.RuneCountInString(name) > 120 {
				name = string([]rune(name)[:120])
			}
			if name == "" {
				name = id
			}
			out = append(out, provider.Line{ID: id, Name: name})
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
	return nil, errors.New("placetel: too many number pages")
}
