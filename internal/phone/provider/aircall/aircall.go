// Package aircall is the Aircall adapter of the phone engine (contracts/phone.md). It
// speaks Aircall's Public API (https://developers.aircall.io/api-references): Basic
// authentication with an API ID and token, webhooks for call events and Conversation
// Intelligence, and insight cards on ringing calls.
package aircall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
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

	"github.com/useopendesk/opendesk/internal/phone/provider"
)

// API is Aircall's Public API.
const API = "https://api.aircall.io/v1"

// Events opendesk subscribes a webhook to. Conversation Intelligence events need
// Aircall's AI add-on: a company without it gets the call events alone.
var (
	CallEvents = []string{"call.created", "call.ringing_on_agent", "call.answered", "call.hungup", "call.ended",
		"call.voicemail_left", "call.comm_assets_generated", "call.tagged", "call.untagged", "call.commented"}
	AIEvents = []string{"summary.created", "transcription.created"}
)

// Limits of what an event brings into opendesk.
const (
	maxSummary    = 4000
	maxTranscript = 20000
	maxBody       = 1 << 20
	maxCardText   = 60
	maxComments   = 20
	maxComment    = 2000
	maxTags       = 30
	// Aircall's direct recording and voicemail links last an hour, the short ones three
	// (api-references "Call" object).
	directValid = time.Hour
	shortValid  = 3 * time.Hour
)

// Adapter is Aircall. BaseURL is set by tests only; it is never user input.
type Adapter struct{ BaseURL string }

func (a Adapter) base() string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return API
}

func (Adapter) Describe() provider.Descriptor {
	return provider.Descriptor{Key: "aircall", Name: "Aircall", InsightCards: true, Fields: []provider.Field{
		{Name: "api_id", Label: "API ID", Secret: false},
		{Name: "api_token", Label: "API token", Secret: true},
	}}
}

func (a Adapter) do(ctx context.Context, client *http.Client, creds map[string]string, op, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, r)
	if err != nil {
		return err
	}
	req.SetBasicAuth(creds["api_id"], creds["api_token"])
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		// The transport error names the method only; a URL could carry no secret here,
		// but the message stays the same shape for every provider.
		return fmt.Errorf("aircall %s: %w", op, unwrapURL(err))
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return &provider.RateLimited{RetryAfter: retryAfter(res.Header)}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		return &provider.StatusError{Op: "aircall " + op, Status: res.StatusCode}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(out)
}

func unwrapURL(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}

// retryAfter reads Aircall's X-AircallApi-Reset (the reset as a Unix time), else
// Retry-After, else a minute: the limit is per minute.
func retryAfter(h http.Header) time.Duration {
	if v, err := strconv.ParseInt(h.Get("X-AircallApi-Reset"), 10, 64); err == nil {
		if d := time.Until(time.Unix(v, 0)); d > 0 && d <= 2*time.Minute {
			return d
		}
	}
	if v, err := strconv.Atoi(h.Get("Retry-After")); err == nil && v > 0 && v <= 120 {
		return time.Duration(v) * time.Second
	}
	return time.Minute
}

// Validate reads the company the credentials belong to (GET /v1/company).
func (a Adapter) Validate(ctx context.Context, client *http.Client, creds map[string]string) (provider.Account, error) {
	var out struct {
		Company struct {
			Name string `json:"name"`
		} `json:"company"`
	}
	if err := a.do(ctx, client, creds, "company", http.MethodGet, "/company", nil, &out); err != nil {
		return provider.Account{}, err
	}
	return provider.Account{Name: out.Company.Name}, nil
}

type webhookBody struct {
	Webhook struct {
		WebhookID string `json:"webhook_id"`
		ID        any    `json:"id"`
		Token     string `json:"token"`
		Active    *bool  `json:"active"`
	} `json:"webhook"`
}

func (w webhookBody) id() string {
	if w.Webhook.WebhookID != "" {
		return w.Webhook.WebhookID
	}
	switch v := w.Webhook.ID.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// RegisterWebhook creates a webhook for the call and AI events (POST /v1/webhooks). A
// company Aircall refuses the AI events for gets the call events alone.
func (a Adapter) RegisterWebhook(ctx context.Context, client *http.Client, creds map[string]string, hookURL string) (provider.Webhook, error) {
	var out webhookBody
	body := map[string]any{"custom_name": "opendesk", "url": hookURL, "events": append(append([]string{}, CallEvents...), AIEvents...)}
	err := a.do(ctx, client, creds, "create webhook", http.MethodPost, "/webhooks", body, &out)
	var refused *provider.StatusError
	if errors.As(err, &refused) && (refused.Status == http.StatusBadRequest || refused.Status == http.StatusUnprocessableEntity) {
		body["events"] = CallEvents
		err = a.do(ctx, client, creds, "create webhook", http.MethodPost, "/webhooks", body, &out)
	}
	if err != nil {
		return provider.Webhook{}, err
	}
	if out.id() == "" || out.Webhook.Token == "" {
		return provider.Webhook{}, errors.New("aircall create webhook: no id or token in the answer")
	}
	return provider.Webhook{ID: out.id(), Secret: out.Webhook.Token}, nil
}

// WebhookActive reads the webhook (GET /v1/webhooks/:id); Aircall disables one whose
// deliveries kept failing.
func (a Adapter) WebhookActive(ctx context.Context, client *http.Client, creds map[string]string, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	var out webhookBody
	err := a.do(ctx, client, creds, "get webhook", http.MethodGet, "/webhooks/"+url.PathEscape(id), nil, &out)
	var refused *provider.StatusError
	if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return out.Webhook.Active == nil || *out.Webhook.Active, nil
}

// DeleteWebhook removes the webhook (DELETE /v1/webhooks/:id).
func (a Adapter) DeleteWebhook(ctx context.Context, client *http.Client, creds map[string]string, id string) error {
	if id == "" {
		return nil
	}
	err := a.do(ctx, client, creds, "delete webhook", http.MethodDelete, "/webhooks/"+url.PathEscape(id), nil, nil)
	var refused *provider.StatusError
	if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// Verify compares the token Aircall puts in every webhook body with the webhook's, in
// constant time.
func (Adapter) Verify(body []byte, secret string) error {
	var env struct {
		Token string `json:"token"`
	}
	if secret == "" || json.Unmarshal(body, &env) != nil || env.Token == "" ||
		subtle.ConstantTimeCompare([]byte(env.Token), []byte(secret)) != 1 {
		return provider.ErrUnauthorized
	}
	return nil
}

// flexID is an Aircall id: a number in call payloads, a string in some others.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexID(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexID(n.String())
	return nil
}

type envelope struct {
	Resource  string          `json:"resource"`
	Event     string          `json:"event"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type user struct {
	ID    flexID `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type callData struct {
	ID           flexID  `json:"id"`
	Direction    string  `json:"direction"`
	Status       string  `json:"status"`
	MissedReason *string `json:"missed_call_reason"`
	StartedAt    *int64  `json:"started_at"`
	AnsweredAt   *int64  `json:"answered_at"`
	EndedAt      *int64  `json:"ended_at"`
	Duration     *int    `json:"duration"`
	RawDigits    string  `json:"raw_digits"`
	Voicemail    *string `json:"voicemail"`
	Recording    *string `json:"recording"`
	VoicemailSU  *string `json:"voicemail_short_url"`
	RecordingSU  *string `json:"recording_short_url"`
	Asset        *string `json:"asset"`
	Tags         *[]struct {
		Name string `json:"name"`
	} `json:"tags"`
	Comments *[]struct {
		ID       flexID `json:"id"`
		Content  string `json:"content"`
		PostedAt *int64 `json:"posted_at"`
		PostedBy *user  `json:"posted_by"`
	} `json:"comments"`
	User    *user `json:"user"`
	Contact *struct {
		FirstName   string `json:"first_name"`
		LastName    string `json:"last_name"`
		CompanyName string `json:"company_name"`
	} `json:"contact"`
	Number *struct {
		ID     flexID `json:"id"`
		Name   string `json:"name"`
		Digits string `json:"digits"`
	} `json:"number"`
}

type intelligenceData struct {
	CallID  flexID          `json:"call_id"`
	Content json.RawMessage `json:"content"`
}

// Parse normalizes a verified webhook body.
func (Adapter) Parse(body []byte) (provider.Event, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return provider.Event{}, err
	}
	sum := sha256.Sum256(body)
	ev := provider.Event{ID: hex.EncodeToString(sum[:16]), Type: env.Event, At: time.Now().UTC()}
	if env.Timestamp > 0 {
		ev.At = time.Unix(env.Timestamp, 0).UTC()
	}
	switch env.Event {
	case "summary.created", "transcription.created":
		var d intelligenceData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			return provider.Event{}, err
		}
		if d.CallID == "" {
			return provider.Event{}, provider.ErrIgnored
		}
		ev.CallID = string(d.CallID)
		if env.Event == "summary.created" {
			ev.Kind, ev.Summary = provider.KindSummary, clip(summaryText(d.Content), maxSummary)
			if ev.Summary == "" {
				return provider.Event{}, provider.ErrIgnored
			}
		} else {
			ev.Kind = provider.KindTranscript
		}
		return ev, nil
	}
	stage := ""
	switch env.Event {
	case "call.created", "call.ringing_on_agent":
		stage = provider.StageRinging
	case "call.answered":
		stage = provider.StageAnswered
	case "call.hungup", "call.ended", "call.voicemail_left":
		stage = provider.StageEnded
	case "call.comm_assets_generated", "call.tagged", "call.untagged", "call.commented":
	default:
		return provider.Event{}, provider.ErrIgnored
	}
	var d callData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return provider.Event{}, err
	}
	if d.ID == "" {
		return provider.Event{}, errors.New("aircall call event without a call id")
	}
	ev.Kind, ev.CallID, ev.Final = provider.KindCall, string(d.ID), env.Event == "call.ended"
	c := &provider.Call{Direction: d.Direction, Stage: stage}
	if c.Direction != "inbound" && c.Direction != "outbound" {
		c.Direction = "inbound"
	}
	if strings.EqualFold(strings.TrimSpace(d.RawDigits), "anonymous") {
		c.Anonymous = true
	} else {
		c.Number = strings.TrimSpace(d.RawDigits)
	}
	if d.Number != nil {
		c.LineID, c.LineName = clip(string(d.Number.ID), 60), clip(strings.TrimSpace(d.Number.Name), 120)
	}
	c.StartedAt, c.AnsweredAt, c.EndedAt = unix(d.StartedAt), unix(d.AnsweredAt), unix(d.EndedAt)
	if d.Duration != nil && *d.Duration > 0 {
		c.Duration = *d.Duration
	}
	if d.MissedReason != nil {
		c.MissedReason = clip(strings.TrimSpace(*d.MissedReason), 60)
	}
	c.Voicemail = env.Event == "call.voicemail_left" || nonEmpty(d.Voicemail)
	c.Recording = nonEmpty(d.Recording)
	if d.Asset != nil {
		c.AssetURL = httpsURL(*d.Asset)
	}
	c.RecordingURL, c.LinksValid = firstURL(d.RecordingSU, d.Recording)
	vm, valid := firstURL(d.VoicemailSU, d.Voicemail)
	if vm != "" {
		c.VoicemailURL = vm
		if c.LinksValid == 0 || valid < c.LinksValid {
			c.LinksValid = valid
		}
	}
	if d.Tags != nil {
		c.Tags = []string{}
		for _, t := range *d.Tags {
			if n := clip(t.Name, 60); n != "" && len(c.Tags) < maxTags {
				c.Tags = append(c.Tags, n)
			}
		}
	}
	if d.Comments != nil {
		c.Comments = []provider.Comment{}
		for _, m := range *d.Comments {
			text := clip(m.Content, maxComment)
			if text == "" || len(c.Comments) >= maxComments {
				continue
			}
			cm := provider.Comment{ID: string(m.ID), Text: text, At: unix(m.PostedAt)}
			if m.PostedBy != nil {
				cm.Author = clip(m.PostedBy.Name, 120)
			}
			c.Comments = append(c.Comments, cm)
		}
	}
	if d.User != nil && (d.User.ID != "" || d.User.Email != "") {
		c.Agent = &provider.Person{ID: string(d.User.ID), Name: clip(d.User.Name, 120), Email: clip(strings.TrimSpace(d.User.Email), 320)}
	}
	if d.Contact != nil {
		name := strings.TrimSpace(strings.TrimSpace(d.Contact.FirstName) + " " + strings.TrimSpace(d.Contact.LastName))
		if name == "" {
			name = strings.TrimSpace(d.Contact.CompanyName)
		}
		if name != "" {
			c.Caller = &provider.Person{Name: clip(name, 120)}
		}
	}
	ev.Call = c
	return ev, nil
}

func unix(v *int64) *time.Time {
	if v == nil || *v <= 0 {
		return nil
	}
	t := time.Unix(*v, 0).UTC()
	return &t
}

// firstURL is the short link when Aircall gave one, else the direct one, and how long it
// lasts.
func firstURL(short, direct *string) (string, time.Duration) {
	if short != nil {
		if u := httpsURL(*short); u != "" {
			return u, shortValid
		}
	}
	if direct != nil {
		if u := httpsURL(*direct); u != "" {
			return u, directValid
		}
	}
	return "", 0
}

func nonEmpty(s *string) bool { return s != nil && strings.TrimSpace(*s) != "" }

// httpsURL keeps an https link only.
func httpsURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || len(raw) > 2000 {
		return ""
	}
	return u.String()
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

// summaryText reads a summary's content: a string, or an object with one.
func summaryText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Content string `json:"content"`
		Text    string `json:"text"`
	}
	if json.Unmarshal(raw, &o) == nil {
		if o.Content != "" {
			return o.Content
		}
		return o.Text
	}
	return ""
}

// transcriptText turns a transcription's utterances into lines that say who spoke.
func transcriptText(raw json.RawMessage) string {
	var c struct {
		Utterances []struct {
			Text            string `json:"text"`
			ParticipantType string `json:"participant_type"`
		} `json:"utterances"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	var b strings.Builder
	for _, u := range c.Utterances {
		text := strings.Join(strings.Fields(u.Text), " ")
		if text == "" {
			continue
		}
		who := "Customer"
		if u.ParticipantType == "internal" {
			who = "Agent"
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(who + ": " + text)
		if b.Len() > maxTranscript {
			break
		}
	}
	return b.String()
}

// Content retrieves the transcript announced by transcription.created; summary.created
// carries its summary.
func (a Adapter) Content(ctx context.Context, client *http.Client, creds map[string]string, kind, callID string) (string, error) {
	if kind != provider.KindTranscript {
		return "", provider.ErrUnsupported
	}
	var out struct {
		Transcription struct {
			Content json.RawMessage `json:"content"`
		} `json:"transcription"`
	}
	if err := a.do(ctx, client, creds, "transcription", http.MethodGet, "/calls/"+url.PathEscape(callID)+"/transcription", nil, &out); err != nil {
		return "", err
	}
	text := clip(transcriptText(out.Transcription.Content), maxTranscript)
	if text == "" {
		return "", errors.New("aircall transcription not available")
	}
	return text, nil
}

// Lines lists the company's numbers (GET /v1/numbers), up to 500.
func (a Adapter) Lines(ctx context.Context, client *http.Client, creds map[string]string) ([]provider.Line, error) {
	out := []provider.Line{}
	for page := 1; page <= 10; page++ {
		var res struct {
			Numbers []struct {
				ID   flexID `json:"id"`
				Name string `json:"name"`
			} `json:"numbers"`
			Meta struct {
				NextPageLink *string `json:"next_page_link"`
			} `json:"meta"`
		}
		if err := a.do(ctx, client, creds, "numbers", http.MethodGet, "/numbers?per_page=50&page="+strconv.Itoa(page), nil, &res); err != nil {
			return nil, err
		}
		for _, n := range res.Numbers {
			if name := clip(n.Name, 120); name != "" {
				out = append(out, provider.Line{ID: string(n.ID), Name: name})
			}
		}
		if res.Meta.NextPageLink == nil || *res.Meta.NextPageLink == "" {
			break
		}
	}
	return out, nil
}

// PushCard shows the card on the call (POST /v1/calls/:id/insight_cards).
func (a Adapter) PushCard(ctx context.Context, client *http.Client, creds map[string]string, callID string, card provider.Card) error {
	contents := []map[string]any{}
	title := map[string]any{"type": "title", "text": clip(card.Title, maxCardText)}
	if l := httpsURL(card.TitleLink); l != "" {
		title["link"] = l
	}
	contents = append(contents, title)
	for _, line := range card.Lines {
		item := map[string]any{"type": "shortText", "label": clip(line.Label, maxCardText), "text": clip(line.Text, maxCardText)}
		if l := httpsURL(line.Link); l != "" {
			item["link"] = l
		}
		contents = append(contents, item)
	}
	return a.do(ctx, client, creds, "insight card", http.MethodPost, "/calls/"+url.PathEscape(callID)+"/insight_cards", map[string]any{"contents": contents}, nil)
}
