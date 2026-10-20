package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
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

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

type Adapter struct{ BaseURL string }

func (Adapter) Describe() channel.Descriptor {
	return channel.Descriptor{Key: "slack", Name: "Slack", Description: "Mirror email tickets into Slack threads and work them by talking to Ollie.", Fields: []channel.Field{
		{Name: "bot_token", Label: "Bot token", Secret: true, Help: "A bot token with chat:write, app_mentions:read, channels:read and groups:read."},
		{Name: "signing_secret", Label: "Signing secret", Secret: true},
	}}
}

func (a Adapter) Discover(ctx context.Context, c *http.Client, cr map[string]string) ([]channel.Destination, error) {
	var destinations []channel.Destination
	cursor := ""
	for page := 0; page < 50; page++ {
		var out struct {
			Channels []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				IsMember   bool   `json:"is_member"`
				IsArchived bool   `json:"is_archived"`
			} `json:"channels"`
			Metadata struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		input := map[string]any{"types": "public_channel,private_channel", "exclude_archived": true, "limit": 200}
		if cursor != "" {
			input["cursor"] = cursor
		}
		if err := a.call(ctx, c, cr["bot_token"], "conversations.list", input, &out); err != nil {
			return nil, err
		}
		for _, item := range out.Channels {
			if item.IsMember && !item.IsArchived {
				destinations = append(destinations, channel.Destination{ID: item.ID, Name: item.Name, Kind: "channel", Available: true})
			}
		}
		cursor = strings.TrimSpace(out.Metadata.NextCursor)
		if cursor == "" {
			return destinations, nil
		}
	}
	return nil, errors.New("slack conversations.list returned too many pages")
}

func (a Adapter) Health(ctx context.Context, c *http.Client, cr map[string]string, _ channel.Setup) (channel.Health, error) {
	if _, err := a.Validate(ctx, c, cr); err != nil {
		return channel.Health{}, err
	}
	return channel.Health{Status: "action_required", Detail: "Credentials work. Verify the Events API and /opendesk request URLs in Slack."}, nil
}

func (a Adapter) endpoint(path string) string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/") + path
	}
	return "https://slack.com/api" + path
}

func (a Adapter) call(ctx context.Context, c *http.Client, token, method string, input any, out any) error {
	b, _ := json.Marshal(input)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint("/"+method), bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("slack %s: status %d", method, resp.StatusCode)
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	_ = json.Unmarshal(raw, &envelope)
	if !envelope.OK {
		return fmt.Errorf("slack %s: %s", method, envelope.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (a Adapter) Validate(ctx context.Context, c *http.Client, cr map[string]string) (channel.Account, error) {
	var out struct {
		TeamID string `json:"team_id"`
		Team   string `json:"team"`
	}
	if cr["bot_token"] == "" || cr["signing_secret"] == "" {
		return channel.Account{}, errors.New("bot_token and signing_secret are required")
	}
	if err := a.call(ctx, c, cr["bot_token"], "auth.test", map[string]any{}, &out); err != nil {
		return channel.Account{}, err
	}
	return channel.Account{ID: out.TeamID, Name: out.Team}, nil
}

func (Adapter) Verify(h http.Header, body []byte, cr map[string]string) error {
	ts := h.Get("X-Slack-Request-Timestamp")
	at, err := time.ParseDuration(ts + "s")
	if err != nil || time.Since(time.Unix(0, 0).Add(at)).Abs() > 5*time.Minute {
		return errors.New("invalid slack timestamp")
	}
	mac := hmac.New(sha256.New, []byte(cr["signing_secret"]))
	mac.Write([]byte("v0:" + ts + ":"))
	mac.Write(body)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(h.Get("X-Slack-Signature"))) {
		return errors.New("invalid slack signature")
	}
	return nil
}

func (Adapter) Parse(h http.Header, body []byte) (channel.WebhookResult, error) {
	if strings.Contains(h.Get("Content-Type"), "application/x-www-form-urlencoded") {
		v, err := url.ParseQuery(string(body))
		if err != nil {
			return channel.WebhookResult{}, err
		}
		// Slash commands never carry a thread, so they name the ticket by number,
		// like `/opendesk 123 status solved`.
		number, cmd, text := parseSlash(v.Get("text"))
		return channel.WebhookResult{Events: []channel.Inbound{{EventID: v.Get("trigger_id"), AccountID: v.Get("team_id"), ExternalChannelID: v.Get("channel_id"), ExternalUserID: v.Get("user_id"), Command: cmd, Text: text, Value: text, TicketNumber: number}}}, nil
	}
	var x struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		EventID   string `json:"event_id"`
		TeamID    string `json:"team_id"`
		Event     struct {
			Type     string `json:"type"`
			User     string `json:"user"`
			Channel  string `json:"channel"`
			ThreadTS string `json:"thread_ts"`
			TS       string `json:"ts"`
			Text     string `json:"text"`
			BotID    string `json:"bot_id"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &x); err != nil {
		return channel.WebhookResult{}, err
	}
	if x.Type == "url_verification" {
		response, _ := json.Marshal(map[string]string{"challenge": x.Challenge})
		return channel.WebhookResult{Status: http.StatusOK, ContentType: "application/json", Body: response}, nil
	}
	if (x.Event.Type != "message" && x.Event.Type != "app_mention") || x.Event.BotID != "" {
		return channel.WebhookResult{}, nil
	}
	// A root app mention starts a new Ollie conversation in its own Slack
	// thread. Ordinary root messages remain ignored.
	if x.Event.ThreadTS == "" {
		if x.Event.Type != "app_mention" {
			return channel.WebhookResult{}, nil
		}
		x.Event.ThreadTS = x.Event.TS
	}
	cmd, text := parseCommand(x.Event.Text)
	if cmd == "" {
		return channel.WebhookResult{}, nil
	}
	return channel.WebhookResult{Events: []channel.Inbound{{EventID: x.EventID + ":" + x.Event.TS, AccountID: x.TeamID, ExternalChannelID: x.Event.Channel, ExternalThreadID: x.Event.ThreadTS, ExternalUserID: x.Event.User, Command: cmd, Text: text, Value: text, Directed: x.Event.Type == "app_mention"}}}, nil
}

func parseCommand(s string) (string, string) {
	p := strings.Fields(strings.TrimSpace(s))
	if len(p) == 0 {
		return "", ""
	}
	first := strings.ToLower(p[0])
	switch {
	case first == "/opendesk" && len(p) > 1:
		p = p[1:]
	case strings.HasPrefix(first, "<@") && strings.HasSuffix(first, ">") && len(p) > 1:
		p = p[1:]
	case strings.HasPrefix(first, "/"):
		p[0] = strings.TrimPrefix(p[0], "/")
	default:
		return "", ""
	}
	if len(p) == 0 {
		return "", ""
	}
	first = strings.ToLower(p[0])
	if explicitCommand(first) {
		return first, strings.TrimSpace(strings.Join(p[1:], " "))
	}
	return "chat", strings.TrimSpace(strings.Join(p, " "))
}

// parseSlash reads a slash command's text: an optional ticket number (123 or
// #123), then an explicit command or a question for Ollie.
func parseSlash(s string) (int64, string, string) {
	p := strings.Fields(s)
	var number int64
	if len(p) > 0 {
		if n, err := strconv.ParseInt(strings.TrimPrefix(p[0], "#"), 10, 64); err == nil && n > 0 {
			number, p = n, p[1:]
		}
	}
	if len(p) == 0 {
		return number, "chat", ""
	}
	if first := strings.ToLower(p[0]); explicitCommand(first) {
		return number, first, strings.TrimSpace(strings.Join(p[1:], " "))
	}
	return number, "chat", strings.TrimSpace(strings.Join(p, " "))
}

func explicitCommand(s string) bool {
	switch s {
	case "link", "reply", "note", "assign", "assign_self", "status", "priority":
		return true
	default:
		return false
	}
}

func (a Adapter) CreateThread(ctx context.Context, c *http.Client, cr map[string]string, destination string, t channel.Ticket) (channel.Thread, error) {
	var out struct {
		TS string `json:"ts"`
	}
	if err := a.call(ctx, c, cr["bot_token"], "chat.postMessage", map[string]any{"channel": destination, "text": render(t), "unfurl_links": false, "unfurl_media": false}, &out); err != nil {
		return channel.Thread{}, err
	}
	return channel.Thread{ID: out.TS, RootID: out.TS}, nil
}
func (a Adapter) Post(ctx context.Context, c *http.Client, cr map[string]string, thread, text string) error {
	msg := map[string]any{"channel": cr["channel_id"], "text": slackText(text), "unfurl_links": false, "unfurl_media": false}
	if thread != "" {
		msg["thread_ts"] = thread
	}
	return a.call(ctx, c, cr["bot_token"], "chat.postMessage", msg, nil)
}
func (a Adapter) Update(ctx context.Context, c *http.Client, cr map[string]string, root string, t channel.Ticket) error {
	return a.call(ctx, c, cr["bot_token"], "chat.update", map[string]any{"channel": cr["channel_id"], "ts": root, "text": render(t)}, nil)
}
func render(t channel.Ticket) string {
	return fmt.Sprintf("*#%d %s*\n%s · %s · %s\n%s", t.Number, slackText(t.Subject), slackText(t.Customer), slackText(t.Status), slackText(t.Priority), slackText(t.URL))
}

// Slack treats angle-bracket sequences as links and mentions. Escape provider
// text so a customer-controlled subject or message cannot notify a channel.
func slackText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}
