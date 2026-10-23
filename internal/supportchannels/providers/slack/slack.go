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
	return channel.Health{Status: "action_required", Detail: "Credentials work. Verify the Events API, interactivity and /opendesk request URLs in Slack."}, nil
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
		if payload := v.Get("payload"); payload != "" {
			return parseInteraction(payload)
		}
		// Slash commands never carry a thread, so they name the ticket by number,
		// like `/opendesk 123 status solved`.
		number, cmd, text := parseSlash(v.Get("text"))
		return channel.WebhookResult{Events: []channel.Inbound{{EventID: v.Get("trigger_id"), AccountID: v.Get("team_id"), ExternalChannelID: v.Get("channel_id"), ExternalUserID: v.Get("user_id"), Command: cmd, Text: text, Value: text, TicketNumber: number, Respond: v.Get("response_url")}}}, nil
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

// controls are the action ids opendesk's own blocks send. Any other action,
// including the "Open in opendesk" link button, is acknowledged and ignored.
var controls = map[string]bool{"assign_self": true, "status": true, "priority": true, "confirm": true, "cancel": true}

// parseInteraction reads a block_actions payload from a ticket card or a
// confirmation preview.
func parseInteraction(payload string) (channel.WebhookResult, error) {
	var x struct {
		Type        string `json:"type"`
		TriggerID   string `json:"trigger_id"`
		ResponseURL string `json:"response_url"`
		Team        struct {
			ID string `json:"id"`
		} `json:"team"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
		Message struct {
			TS       string `json:"ts"`
			ThreadTS string `json:"thread_ts"`
			Text     string `json:"text"`
		} `json:"message"`
		Container struct {
			MessageTS string `json:"message_ts"`
			ThreadTS  string `json:"thread_ts"`
		} `json:"container"`
		Actions []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
			Selected *struct {
				Value string `json:"value"`
			} `json:"selected_option"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(payload), &x); err != nil {
		return channel.WebhookResult{}, err
	}
	if x.Type != "block_actions" || len(x.Actions) == 0 {
		return channel.WebhookResult{}, nil
	}
	action := x.Actions[0]
	cmd, ok := strings.CutPrefix(action.ActionID, "opendesk:")
	if !ok || !controls[cmd] {
		return channel.WebhookResult{Control: ok}, nil
	}
	value := action.Value
	if action.Selected != nil {
		value = action.Selected.Value
	}
	text := value
	if cmd == "confirm" || cmd == "cancel" {
		text = cmd
	}
	thread := x.Container.ThreadTS
	if thread == "" {
		thread = x.Message.ThreadTS
	}
	if thread == "" {
		thread = x.Container.MessageTS
	}
	if thread == "" {
		thread = x.Message.TS
	}
	source := x.Container.MessageTS
	if source == "" {
		source = x.Message.TS
	}
	return channel.WebhookResult{Control: true, Events: []channel.Inbound{{
		EventID: x.TriggerID, AccountID: x.Team.ID, ExternalChannelID: x.Channel.ID,
		ExternalThreadID: thread, ExternalUserID: x.User.ID, Command: cmd, Text: text,
		Value: value, SourceMessageID: source, SourceText: x.Message.Text, Respond: x.ResponseURL,
	}}}, nil
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
	msg := ticketPayload(destination, "", t)
	if err := a.call(ctx, c, cr["bot_token"], "chat.postMessage", msg, &out); err != nil {
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
	return a.call(ctx, c, cr["bot_token"], "chat.update", ticketPayload(cr["channel_id"], root, t), nil)
}

func (a Adapter) PostInteractive(ctx context.Context, c *http.Client, cr map[string]string, thread string, message channel.Message) (channel.MessageRef, error) {
	var out struct {
		TS string `json:"ts"`
	}
	payload := interactivePayload(cr["channel_id"], thread, message)
	err := a.call(ctx, c, cr["bot_token"], "chat.postMessage", payload, &out)
	return channel.MessageRef{ID: out.TS}, err
}

// ClearActions restates the message's own text without its buttons. The text
// is Slack's stored copy of what opendesk posted, already escaped.
func (a Adapter) ClearActions(ctx context.Context, c *http.Client, cr map[string]string, in channel.Inbound) error {
	if in.SourceMessageID == "" || strings.TrimSpace(in.SourceText) == "" {
		return nil
	}
	return a.call(ctx, c, cr["bot_token"], "chat.update", map[string]any{
		"channel": cr["channel_id"], "ts": in.SourceMessageID, "text": in.SourceText,
		"blocks": []any{map[string]any{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": in.SourceText}}},
	}, nil)
}

// Respond posts an ephemeral answer through the interaction's response_url,
// which only Slack's own hooks host may be.
func (a Adapter) Respond(ctx context.Context, c *http.Client, _ map[string]string, handle, text string) error {
	if text == "" {
		return nil
	}
	if !a.responseURL(handle) {
		return errors.New("slack response_url is not a Slack hook")
	}
	b, _ := json.Marshal(map[string]any{"response_type": "ephemeral", "replace_original": false, "text": slackText(text)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, handle, bytes.NewReader(b))
	if err != nil {
		return errors.New("slack response_url is invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		// The URL is a credential for this interaction; keep it out of the error.
		return errors.New("slack response_url: request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack response_url: status %d", resp.StatusCode)
	}
	return nil
}

func (a Adapter) responseURL(raw string) bool {
	if a.BaseURL != "" {
		return strings.HasPrefix(raw, strings.TrimRight(a.BaseURL, "/")+"/")
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "hooks.slack.com" && u.User == nil
}

func ticketPayload(channelID, root string, t channel.Ticket) map[string]any {
	payload := map[string]any{
		"channel": channelID, "text": render(t), "unfurl_links": false, "unfurl_media": false,
		"blocks": []any{
			map[string]any{"type": "header", "text": map[string]string{"type": "plain_text", "text": fmt.Sprintf("#%d %s", t.Number, clipText(t.Subject, 120))}},
			map[string]any{"type": "section", "fields": []any{
				map[string]string{"type": "mrkdwn", "text": "*Customer*\n" + slackText(t.Customer)},
				map[string]string{"type": "mrkdwn", "text": "*Assignee*\n" + slackText(t.Assignee)},
				map[string]string{"type": "mrkdwn", "text": "*Status*\n" + slackText(strings.ReplaceAll(t.Status, "_", " "))},
				map[string]string{"type": "mrkdwn", "text": "*Priority*\n" + slackText(t.Priority)},
			}},
			map[string]any{"type": "actions", "elements": []any{
				map[string]any{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Assign to me"}, "action_id": "opendesk:assign_self"},
				selectElement("Status", "opendesk:status", []string{"open", "pending", "on_hold", "solved", "closed"}),
				selectElement("Priority", "opendesk:priority", []string{"low", "normal", "high", "urgent"}),
				map[string]any{"type": "button", "text": map[string]string{"type": "plain_text", "text": "Open in opendesk"}, "url": t.URL, "action_id": "opendesk:open"},
			}},
		},
	}
	if root != "" {
		payload["ts"] = root
	}
	return payload
}

func selectElement(label, actionID string, values []string) map[string]any {
	options := make([]any, 0, len(values))
	for _, value := range values {
		options = append(options, map[string]any{"text": map[string]string{"type": "plain_text", "text": strings.ReplaceAll(value, "_", " ")}, "value": value})
	}
	return map[string]any{"type": "static_select", "placeholder": map[string]string{"type": "plain_text", "text": label}, "action_id": actionID, "options": options}
}

func interactivePayload(channelID, thread string, message channel.Message) map[string]any {
	payload := map[string]any{"channel": channelID, "text": slackText(message.Text), "unfurl_links": false, "unfurl_media": false}
	if thread != "" {
		payload["thread_ts"] = thread
	}
	blocks := []any{map[string]any{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": slackText(message.Text)}}}
	if len(message.Actions) > 0 {
		elements := make([]any, 0, len(message.Actions))
		for _, action := range message.Actions {
			button := map[string]any{"type": "button", "text": map[string]string{"type": "plain_text", "text": action.Label}, "action_id": "opendesk:" + action.Command, "value": action.Value}
			if action.Style == "primary" || action.Style == "danger" {
				button["style"] = action.Style
			}
			elements = append(elements, button)
		}
		blocks = append(blocks, map[string]any{"type": "actions", "elements": elements})
	}
	payload["blocks"] = blocks
	return payload
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

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
