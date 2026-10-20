package telegram

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

type Adapter struct{ BaseURL string }

func (Adapter) Describe() channel.Descriptor {
	return channel.Descriptor{Key: "telegram", Name: "Telegram", Description: "Mirror email tickets into Telegram forum topics and work them by talking to Ollie.", Fields: []channel.Field{
		{Name: "bot_token", Label: "Bot token", Secret: true, Help: "Create a bot with BotFather and add it as an administrator to a forum group."},
		{Name: "webhook_secret", Label: "Webhook secret", Secret: true, Generated: true},
	}}
}
func (a Adapter) endpoint(token, method string) string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/") + "/bot" + token + "/" + method
	}
	return "https://api.telegram.org/bot" + token + "/" + method
}
func (a Adapter) call(ctx context.Context, c *http.Client, cr map[string]string, method string, input any, out any) error {
	token := cr["bot_token"]
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint(token, method), bytes.NewReader(b))
	if err != nil {
		return transportError(method, token, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return transportError(method, token, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return transportError(method, token, err)
	}
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &env) != nil || !env.OK {
		return fmt.Errorf("telegram %s: %s", method, redact(env.Description, token))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// botError is a Telegram transport failure. The Bot API carries the bot token in
// the request path, so the message names the method and the underlying cause
// only, never the URL, and any remaining copy of the token is masked.
type botError struct {
	msg   string
	cause error
}

func (e *botError) Error() string { return e.msg }
func (e *botError) Unwrap() error { return e.cause }

func transportError(method, token string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := redact(err.Error(), token)
	var cause error
	if msg == err.Error() {
		cause = err
	}
	return &botError{msg: "telegram " + method + ": " + msg, cause: cause}
}

func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[redacted]")
}

func (a Adapter) Validate(ctx context.Context, c *http.Client, cr map[string]string) (channel.Account, error) {
	if cr["bot_token"] == "" || cr["webhook_secret"] == "" {
		return channel.Account{}, errors.New("bot_token and webhook_secret are required")
	}
	var me struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := a.call(ctx, c, cr, "getMe", map[string]any{}, &me); err != nil {
		return channel.Account{}, err
	}
	return channel.Account{ID: strconv.FormatInt(me.ID, 10), Name: "@" + me.Username}, nil
}

func (a Adapter) Configure(ctx context.Context, c *http.Client, cr map[string]string, setup channel.Setup) error {
	return a.call(ctx, c, cr, "setWebhook", map[string]any{
		"url": setup.WebhookURL, "secret_token": cr["webhook_secret"],
		"allowed_updates": []string{"message", "my_chat_member"},
	}, nil)
}

func (a Adapter) Health(ctx context.Context, c *http.Client, cr map[string]string, setup channel.Setup) (channel.Health, error) {
	var info struct {
		URL              string `json:"url"`
		LastErrorMessage string `json:"last_error_message"`
	}
	if err := a.call(ctx, c, cr, "getWebhookInfo", map[string]any{}, &info); err != nil {
		return channel.Health{}, err
	}
	if info.URL != setup.WebhookURL {
		return channel.Health{Status: "action_required", Detail: "Telegram's webhook points somewhere else. Test the connection to repair it."}, nil
	}
	if info.LastErrorMessage != "" {
		return channel.Health{Status: "error", Detail: "Telegram reported a recent webhook delivery error."}, nil
	}
	return channel.Health{Status: "healthy", Detail: "Telegram's webhook is configured and accepting updates."}, nil
}
func (Adapter) Verify(h http.Header, _ []byte, cr map[string]string) error {
	a, b := []byte(h.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(cr["webhook_secret"])
	if len(a) != len(b) || subtle.ConstantTimeCompare(a, b) != 1 {
		return errors.New("invalid telegram secret")
	}
	return nil
}
func (Adapter) Parse(_ http.Header, body []byte) (channel.WebhookResult, error) {
	var x struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			MessageID       int64  `json:"message_id"`
			MessageThreadID int64  `json:"message_thread_id"`
			Text            string `json:"text"`
			From            struct {
				ID    int64 `json:"id"`
				IsBot bool  `json:"is_bot"`
			} `json:"from"`
			ViaBot *struct {
				ID int64 `json:"id"`
			} `json:"via_bot"`
			Chat struct {
				ID      int64  `json:"id"`
				Title   string `json:"title"`
				Type    string `json:"type"`
				IsForum bool   `json:"is_forum"`
			} `json:"chat"`
		} `json:"message"`
		MyChatMember *struct {
			Chat struct {
				ID      int64  `json:"id"`
				Title   string `json:"title"`
				Type    string `json:"type"`
				IsForum bool   `json:"is_forum"`
			} `json:"chat"`
			NewChatMember struct {
				Status string `json:"status"`
			} `json:"new_chat_member"`
		} `json:"my_chat_member"`
	}
	if err := json.Unmarshal(body, &x); err != nil {
		return channel.WebhookResult{}, err
	}
	if x.MyChatMember != nil {
		m := x.MyChatMember
		available := (m.NewChatMember.Status == "member" || m.NewChatMember.Status == "administrator") && m.Chat.IsForum
		return channel.WebhookResult{Destinations: []channel.Destination{{ID: strconv.FormatInt(m.Chat.ID, 10), Name: m.Chat.Title, Kind: m.Chat.Type, Available: available}}}, nil
	}
	if x.Message == nil {
		return channel.WebhookResult{}, nil
	}
	discovery := channel.Destination{ID: strconv.FormatInt(x.Message.Chat.ID, 10), Name: x.Message.Chat.Title, Kind: x.Message.Chat.Type, Available: x.Message.Chat.IsForum}
	if x.Message.From.IsBot || x.Message.ViaBot != nil {
		return channel.WebhookResult{Destinations: []channel.Destination{discovery}}, nil
	}
	p := strings.Fields(x.Message.Text)
	if len(p) == 0 || x.Message.MessageThreadID == 0 {
		return channel.WebhookResult{Destinations: []channel.Destination{discovery}}, nil
	}
	cmd, text := "chat", strings.TrimSpace(x.Message.Text)
	if strings.HasPrefix(p[0], "/") {
		candidate := strings.TrimPrefix(strings.SplitN(p[0], "@", 2)[0], "/")
		if explicitCommand(candidate) {
			cmd = candidate
			text = strings.TrimSpace(strings.TrimPrefix(x.Message.Text, p[0]))
		} else {
			text = strings.TrimSpace(strings.TrimPrefix(x.Message.Text, p[0]))
			if text == "" {
				text = candidate
			} else {
				text = candidate + " " + text
			}
		}
	}
	return channel.WebhookResult{Events: []channel.Inbound{{EventID: strconv.FormatInt(x.UpdateID, 10), ExternalChannelID: strconv.FormatInt(x.Message.Chat.ID, 10), ExternalThreadID: strconv.FormatInt(x.Message.MessageThreadID, 10), ExternalUserID: strconv.FormatInt(x.Message.From.ID, 10), Command: cmd, Text: text, Value: text}}, Destinations: []channel.Destination{discovery}}, nil
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
	var topic struct {
		MessageThreadID int64 `json:"message_thread_id"`
	}
	name := fmt.Sprintf("#%d %s", t.Number, t.Subject)
	if len([]rune(name)) > 120 {
		name = string([]rune(name)[:120])
	}
	if err := a.call(ctx, c, cr, "createForumTopic", map[string]any{"chat_id": destination, "name": name}, &topic); err != nil {
		return channel.Thread{}, err
	}
	id := strconv.FormatInt(topic.MessageThreadID, 10)
	if err := a.Post(ctx, c, withChannel(cr, destination), id, render(t)); err != nil {
		return channel.Thread{}, err
	}
	return channel.Thread{ID: id}, nil
}
func withChannel(cr map[string]string, id string) map[string]string {
	out := make(map[string]string, len(cr)+1)
	for k, v := range cr {
		out[k] = v
	}
	out["channel_id"] = id
	return out
}
func (a Adapter) Post(ctx context.Context, c *http.Client, cr map[string]string, thread, text string) error {
	tid, _ := strconv.ParseInt(thread, 10, 64)
	return a.call(ctx, c, cr, "sendMessage", map[string]any{
		"chat_id": cr["channel_id"], "message_thread_id": tid, "text": strings.ReplaceAll(text, "@", "@\u200b"),
		"link_preview_options": map[string]bool{"is_disabled": true},
	}, nil)
}
func (Adapter) Update(context.Context, *http.Client, map[string]string, string, channel.Ticket) error {
	return nil
}
func render(t channel.Ticket) string {
	return fmt.Sprintf("#%d %s\n%s · %s · %s\n%s", t.Number, t.Subject, t.Customer, t.Status, t.Priority, t.URL)
}
