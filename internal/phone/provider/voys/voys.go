// Package voys is the Voys adapter of the phone engine (contracts/phone.md). Voys is a
// cloud phone system on the VoIPGRID platform: its API (https://openapi.voys.dev) takes a
// user's API token, and its call notifications (https://help.voys.nl/gespreksnotificaties)
// are added by hand in the Freedom portal, unsigned, so the engine verifies them by the
// secret in their URL (Descriptor.ManualWebhook). Voys Intelligence transcripts and
// summaries are fetched from Voys' API, never taken from a notification.
package voys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/phone/provider"
)

// API is Voys' platform API; Storage holds Voys Intelligence transcripts and summaries.
const (
	API     = "https://api.voys.nl/api/v2"
	Storage = "https://api.eu-production.holodeck.voys.nl/transcription-storage"
)

// Limits of what an event or an answer brings into opendesk.
const (
	maxSummary    = 4000
	maxTranscript = 20000
	maxBody       = 1 << 20
	// throttled is how long a request Voys answered 429 waits: Voys names no reset.
	throttled = time.Minute
)

// Adapter is Voys. APIURL and StorageURL are set by tests only; they are never user input.
type Adapter struct{ APIURL, StorageURL string }

func (a Adapter) api() string     { return orDefault(a.APIURL, API) }
func (a Adapter) storage() string { return orDefault(a.StorageURL, Storage) }

func orDefault(v, def string) string {
	if v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

func (Adapter) Describe() provider.Descriptor {
	return provider.Descriptor{Key: "voys", Name: "Voys", ManualWebhook: true, Fields: []provider.Field{
		{Name: "api_token", Label: "API token", Secret: true},
	}}
}

func (a Adapter) get(ctx context.Context, client *http.Client, creds map[string]string, op, rawURL, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+creds["api_token"])
	req.Header.Set("Accept", accept)
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voys %s: %w", op, unwrapURL(err))
	}
	if res.StatusCode == http.StatusTooManyRequests {
		res.Body.Close()
		return nil, &provider.RateLimited{RetryAfter: retryAfter(res.Header)}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		res.Body.Close()
		return nil, &provider.StatusError{Op: "voys " + op, Status: res.StatusCode}
	}
	return res, nil
}

func (a Adapter) getJSON(ctx context.Context, client *http.Client, creds map[string]string, op, rawURL, accept string, out any) error {
	res, err := a.get(ctx, client, creds, op, rawURL, accept)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(out)
}

func unwrapURL(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}

// retryAfter reads Retry-After when Voys sends one, else waits a minute.
func retryAfter(h http.Header) time.Duration {
	if v, err := strconv.Atoi(h.Get("Retry-After")); err == nil && v > 0 && v <= 120 {
		return time.Duration(v) * time.Second
	}
	return throttled
}

// client is the Voys customer (client) the token's user belongs to, as its API names it
// in paths.
func client(creds map[string]string) (string, error) {
	id, err := uuid.Parse(creds[provider.AccountIDKey])
	if err != nil {
		return "", errors.New("voys: no client in the connection")
	}
	return id.String(), nil
}

// Validate reads who the token belongs to (GET /users/auth-context), which must be a user
// of a Voys customer, and names the account after the customer (GET /user/:uuid/details).
func (a Adapter) Validate(ctx context.Context, c *http.Client, creds map[string]string) (provider.Account, error) {
	var who struct {
		UUID       string  `json:"uuid"`
		ClientUUID *string `json:"client_uuid"`
	}
	if err := a.getJSON(ctx, c, creds, "auth context", a.api()+"/users/auth-context", "application/json", &who); err != nil {
		return provider.Account{}, err
	}
	if who.ClientUUID == nil || *who.ClientUUID == "" {
		return provider.Account{}, &provider.AccountRefused{Detail: "This API token belongs to a Voys partner. Use the API token of a user in the customer's own Voys account."}
	}
	id, err := uuid.Parse(*who.ClientUUID)
	if err != nil {
		return provider.Account{}, errors.New("voys auth context: no client id in the answer")
	}
	account := provider.Account{ID: id.String()}
	// The customer's name is a nicety: a token that may not read the user's details
	// still connects, named after Voys.
	if user, err := uuid.Parse(who.UUID); err == nil {
		var details struct {
			Client struct {
				Name string `json:"name"`
			} `json:"client"`
		}
		err := a.getJSON(ctx, c, creds, "user details", a.api()+"/user/"+user.String()+"/details", "application/json", &details)
		var limited *provider.RateLimited
		if errors.As(err, &limited) {
			return provider.Account{}, err
		}
		if err == nil {
			account.Name = clip(details.Client.Name, 120)
		}
	}
	return account, nil
}

// RegisterWebhook: Voys has no API for call notifications; an admin adds them in Freedom.
func (Adapter) RegisterWebhook(context.Context, *http.Client, map[string]string, string) (provider.Webhook, error) {
	return provider.Webhook{}, provider.ErrUnsupported
}

func (Adapter) WebhookActive(context.Context, *http.Client, map[string]string, string) (bool, error) {
	return false, provider.ErrUnsupported
}

func (Adapter) DeleteWebhook(context.Context, *http.Client, map[string]string, string) error {
	return provider.ErrUnsupported
}

// Verify: Voys signs nothing. The notification's URL carries the secret, which the engine
// checks (Descriptor.ManualWebhook).
func (Adapter) Verify([]byte, string) error { return provider.ErrUnsupported }

// party is a side of a call in a notification.
type party struct {
	Number     string   `json:"number"`
	Name       *string  `json:"name"`
	UserEmails []string `json:"user_emails"`
}

type notification struct {
	CallID      string `json:"call_id"`
	MergedID    string `json:"merged_id"`
	Timestamp   string `json:"timestamp"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	Direction   string `json:"direction"`
	Caller      party  `json:"caller"`
	Destination struct {
		Number  string  `json:"number"`
		Target  *party  `json:"target"`
		Targets []party `json:"targets"`
	} `json:"destination"`
	// Present on the events Voys sends after a call ended.
	Transcription json.RawMessage `json:"call_transcription"`
	Summary       json.RawMessage `json:"summary"`
	RecordingURL  json.RawMessage `json:"recording_url"`
}

// Parse normalizes a verified call notification. Transcription and summary events only
// announce their text: the inbound job fetches it from Voys (Content).
func (Adapter) Parse(body []byte) (provider.Event, error) {
	var n notification
	if err := json.Unmarshal(body, &n); err != nil {
		return provider.Event{}, err
	}
	n.CallID = strings.TrimSpace(n.CallID)
	if n.CallID == "" || len(n.CallID) > 200 {
		return provider.Event{}, errors.New("voys event without a call id")
	}
	sum := sha256.Sum256(body)
	ev := provider.Event{ID: hex.EncodeToString(sum[:16]), CallID: n.CallID, Type: n.Status, At: time.Now().UTC()}
	if t, err := time.Parse(time.RFC3339Nano, n.Timestamp); err == nil {
		ev.At = t.UTC()
	}
	if n.Status == "" {
		switch {
		case len(n.Transcription) > 0:
			ev.Kind, ev.Type = provider.KindTranscript, "call_transcription"
		case len(n.Summary) > 0:
			ev.Kind, ev.Type = provider.KindSummary, "summary"
		case len(n.RecordingURL) > 0:
			// The recording's URL is an API URL that needs the API token: it is not kept,
			// only that the call has a recording. The direction is not known, so the event
			// only updates a call the engine already has.
			ev.Kind, ev.Type = provider.KindCall, "recording"
			ev.Call = &provider.Call{Recording: true}
		default:
			return provider.Event{}, provider.ErrIgnored
		}
		return ev, nil
	}
	stage := ""
	switch n.Status {
	case "created", "ringing":
		stage = provider.StageRinging
	case "in-progress", "warm-transfer", "cold-transfer", "transfer":
		stage = provider.StageAnswered
	case "ended":
		stage = provider.StageEnded
	default:
		return provider.Event{}, provider.ErrIgnored
	}
	c := &provider.Call{Direction: n.Direction, Stage: stage}
	if c.Direction != "outbound" {
		c.Direction = "inbound"
	}
	// The customer is the caller of an inbound call and the one called on an outbound
	// call; the line is the company number on the other side.
	customer, line := n.Caller, n.Destination.Number
	if c.Direction == "outbound" {
		customer, line = party{Number: n.Destination.Number}, n.Caller.Number
		if n.Destination.Target != nil {
			customer.Name = n.Destination.Target.Name
			customer.UserEmails = n.Destination.Target.UserEmails
		}
		for _, t := range n.Destination.Targets {
			customer.UserEmails = append(customer.UserEmails, t.UserEmails...)
		}
	}
	// A call between colleagues reaches no customer (Voys marks it outbound): the other
	// side is an extension, or a user of the account.
	if extension(customer.Number) || len(customer.UserEmails) > 0 {
		return provider.Event{}, provider.ErrIgnored
	}
	if withheld(customer.Number) {
		c.Anonymous = true
	} else {
		c.Number = clip(customer.Number, 40)
	}
	if customer.Name != nil {
		if name := clip(*customer.Name, 120); name != "" {
			c.Caller = &provider.Person{Name: name}
		}
	}
	if l := e164(line); l != "" {
		c.LineID, c.LineName = l, l
	}
	at := ev.At
	switch {
	case stage == provider.StageAnswered:
		c.AnsweredAt = &at
	case stage == provider.StageEnded:
		c.EndedAt = &at
		if n.Reason != "completed" {
			c.MissedReason = reason(n.Reason)
		}
	}
	// The colleague who made an outbound call, or who took an inbound one (a warm
	// transfer's new target included).
	agent := (*party)(nil)
	if c.Direction == "outbound" {
		agent = &n.Caller
	} else if stage == provider.StageAnswered && n.Status != "cold-transfer" {
		agent = n.Destination.Target
	}
	if agent != nil {
		p := &provider.Person{}
		if agent.Name != nil {
			p.Name = clip(*agent.Name, 120)
		}
		if len(agent.UserEmails) > 0 {
			p.Email = clip(agent.UserEmails[0], 320)
		}
		if p.Name != "" || p.Email != "" {
			c.Agent = p
		}
	}
	if m := strings.TrimSpace(n.MergedID); m != "" && m != n.CallID && len(m) <= 200 {
		ev.Continues = m
	}
	ev.Kind, ev.Final, ev.Call = provider.KindCall, stage == provider.StageEnded, c
	return ev, nil
}

// extension reports whether a number is an internal extension: a few digits, no country.
func extension(n string) bool {
	n = strings.TrimSpace(n)
	if n == "" || len(n) > 6 {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return !strings.HasPrefix(n, "00")
}

// withheld reports whether the caller hid their number.
func withheld(n string) bool {
	switch strings.ToLower(strings.TrimSpace(n)) {
	case "", "anonymous", "unknown", "private", "withheld", "restricted":
		return true
	}
	return false
}

// e164 is a number with its country as +digits, else "".
func e164(n string) string {
	n = strings.TrimSpace(n)
	if strings.HasPrefix(n, "00") {
		n = "+" + n[2:]
	}
	if !strings.HasPrefix(n, "+") {
		return ""
	}
	var b strings.Builder
	b.WriteByte('+')
	for _, r := range n[1:] {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
		default:
			return ""
		}
	}
	if b.Len() < 4 || b.Len() > 16 {
		return ""
	}
	return b.String()
}

// reason is Voys' ended reason in the words of call_missed_reason, [a-z0-9_]
// (no-answer: no_answer).
func reason(r string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(strings.TrimSpace(r)) {
		if b.Len() >= 60 {
			break
		}
		switch {
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_':
			b.WriteRune(c)
		case c == '-' || c == ' ':
			b.WriteByte('_')
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

// Content retrieves a call's Voys Intelligence transcript (text) or summary (JSON) from
// the transcription storage, with the connection's own token.
func (a Adapter) Content(ctx context.Context, c *http.Client, creds map[string]string, kind, callID string) (string, error) {
	cu, err := client(creds)
	if err != nil {
		return "", err
	}
	base := a.storage() + "/clients/" + cu + "/calls/" + url.PathEscape(callID)
	switch kind {
	case provider.KindTranscript:
		res, err := a.get(ctx, c, creds, "transcription", base+"/transcriptions", "text/plain")
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
		if err != nil {
			return "", fmt.Errorf("voys transcription: %w", err)
		}
		text := clip(strings.ToValidUTF8(string(raw), ""), maxTranscript)
		if text == "" {
			return "", errors.New("voys transcription not available")
		}
		return text, nil
	case provider.KindSummary:
		var out struct {
			Summary string `json:"summary"`
		}
		if err := a.getJSON(ctx, c, creds, "summary", base+"/summaries", "application/json", &out); err != nil {
			return "", err
		}
		text := clip(out.Summary, maxSummary)
		if text == "" {
			return "", errors.New("voys summary not available")
		}
		return text, nil
	}
	return "", provider.ErrUnsupported
}

// Lines lists the customer's caller ID numbers (GET /clients/:uuid/callerid_numbers), up
// to ten pages. A line is known by its number in E.164, as calls name it.
func (a Adapter) Lines(ctx context.Context, c *http.Client, creds map[string]string) ([]provider.Line, error) {
	cu, err := client(creds)
	if err != nil {
		return nil, err
	}
	out := []provider.Line{}
	for page := 1; page <= 10; page++ {
		var res struct {
			Next  *string `json:"next"`
			Items []struct {
				Number      string `json:"number"`
				Description string `json:"description"`
			} `json:"items"`
		}
		if err := a.getJSON(ctx, c, creds, "caller id numbers", a.api()+"/clients/"+cu+"/callerid_numbers?page="+strconv.Itoa(page),
			"application/json;version=2", &res); err != nil {
			return nil, err
		}
		for _, n := range res.Items {
			id := e164(n.Number)
			if id == "" {
				continue
			}
			name := clip(n.Description, 120)
			if name == "" {
				name = id
			}
			out = append(out, provider.Line{ID: id, Name: name})
		}
		if res.Next == nil || *res.Next == "" {
			break
		}
	}
	return out, nil
}

// PushCard: Voys shows no cards from other apps.
func (Adapter) PushCard(context.Context, *http.Client, map[string]string, string, provider.Card) error {
	return provider.ErrUnsupported
}
