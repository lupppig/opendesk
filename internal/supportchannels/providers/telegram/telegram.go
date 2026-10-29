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

func (Adapter) RichTextLimit() int { return 3000 }

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
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
		Result json.RawMessage `json:"result"`
	}
	decodeErr := json.Unmarshal(raw, &env)
	if resp.StatusCode == http.StatusTooManyRequests || (decodeErr == nil && env.ErrorCode == http.StatusTooManyRequests) {
		return fmt.Errorf("telegram %s: %w", method, &channel.RateLimited{RetryAfter: channel.RateLimitDelay(resp.Header, float64(env.Parameters.RetryAfter))})
	}
	if decodeErr != nil || !env.OK {
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
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	}
	if err := a.call(ctx, c, cr, "getMe", map[string]any{}, &me); err != nil {
		return channel.Account{}, err
	}
	return channel.Account{ID: strconv.FormatInt(me.ID, 10), Name: me.FirstName, Handle: "@" + me.Username}, nil
}

func (a Adapter) Configure(ctx context.Context, c *http.Client, cr map[string]string, setup channel.Setup) error {
	// A bot has one webhook, whichever workspace connected it.
	if setup.Foreign != nil {
		var info struct {
			URL string `json:"url"`
		}
		if err := a.call(ctx, c, cr, "getWebhookInfo", map[string]any{}, &info); err != nil {
			return err
		}
		if info.URL != setup.WebhookURL && setup.Foreign(info.URL) {
			return channel.ErrInUse
		}
	}
	return a.call(ctx, c, cr, "setWebhook", map[string]any{
		"url": setup.WebhookURL, "secret_token": cr["webhook_secret"],
		"allowed_updates": []string{"message", "edited_message", "callback_query", "my_chat_member"},
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
		if setup.Foreign != nil && setup.Foreign(info.URL) {
			return channel.Health{}, channel.ErrInUse
		}
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

type telegramInboundMessage struct {
	MessageID       int64  `json:"message_id"`
	MessageThreadID int64  `json:"message_thread_id"`
	Text            string `json:"text"`
	Caption         string `json:"caption"`
	Document        *struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
		MimeType string `json:"mime_type"`
		FileSize int64  `json:"file_size"`
	} `json:"document"`
	Photo []struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
	} `json:"photo"`
	From struct {
		ID    int64 `json:"id"`
		IsBot bool  `json:"is_bot"`
	} `json:"from"`
	ViaBot *struct {
		ID int64 `json:"id"`
	} `json:"via_bot"`
	ReplyTo *struct {
		From struct {
			IsBot    bool   `json:"is_bot"`
			Username string `json:"username"`
		} `json:"from"`
		TopicCreated json.RawMessage `json:"forum_topic_created"`
	} `json:"reply_to_message"`
	Chat struct {
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		Type    string `json:"type"`
		IsForum bool   `json:"is_forum"`
	} `json:"chat"`
}

func (Adapter) Parse(_ http.Header, body []byte) (channel.WebhookResult, error) {
	var x struct {
		UpdateID      int64                   `json:"update_id"`
		Message       *telegramInboundMessage `json:"message"`
		EditedMessage *telegramInboundMessage `json:"edited_message"`
		MyChatMember  *struct {
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
		CallbackQuery *struct {
			ID   string `json:"id"`
			Data string `json:"data"`
			From struct {
				ID int64 `json:"id"`
			} `json:"from"`
			Message struct {
				MessageID       int64 `json:"message_id"`
				MessageThreadID int64 `json:"message_thread_id"`
				Chat            struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"callback_query"`
	}
	if err := json.Unmarshal(body, &x); err != nil {
		return channel.WebhookResult{}, err
	}
	mutation := ""
	if x.Message == nil && x.EditedMessage != nil {
		x.Message, mutation = x.EditedMessage, "edit"
	}
	if x.MyChatMember != nil {
		m := x.MyChatMember
		available := (m.NewChatMember.Status == "member" || m.NewChatMember.Status == "administrator") && m.Chat.IsForum
		return channel.WebhookResult{Destinations: []channel.Destination{{ID: strconv.FormatInt(m.Chat.ID, 10), Name: m.Chat.Title, Kind: m.Chat.Type, Available: available}}}, nil
	}
	if x.CallbackQuery != nil {
		q := x.CallbackQuery
		cmd, ok := strings.CutPrefix(q.Data, "opendesk:")
		value := ""
		if before, after, found := strings.Cut(cmd, ":"); found {
			cmd, value = before, after
		}
		if !ok || !controls[cmd] {
			// Not one of opendesk's controls: answer it so the button stops loading.
			ack, _ := json.Marshal(map[string]any{"method": "answerCallbackQuery", "callback_query_id": q.ID})
			return channel.WebhookResult{Status: http.StatusOK, ContentType: "application/json", Body: ack}, nil
		}
		text := value
		if cmd == "confirm" || cmd == "cancel" {
			text = cmd
		}
		// The worker answers the callback query (Respond), so an error can reach
		// the person who pressed the button alone.
		return channel.WebhookResult{Control: true, Events: []channel.Inbound{{EventID: q.ID, ExternalChannelID: strconv.FormatInt(q.Message.Chat.ID, 10), ExternalThreadID: strconv.FormatInt(q.Message.MessageThreadID, 10), ExternalUserID: strconv.FormatInt(q.From.ID, 10), Command: cmd, Text: text, Value: value, SourceMessageID: strconv.FormatInt(q.Message.MessageID, 10), Respond: q.ID}}}, nil
	}
	if x.Message == nil {
		return channel.WebhookResult{}, nil
	}
	discovery := channel.Destination{ID: strconv.FormatInt(x.Message.Chat.ID, 10), Name: x.Message.Chat.Title, Kind: x.Message.Chat.Type, Available: x.Message.Chat.IsForum}
	if x.Message.From.IsBot || x.Message.ViaBot != nil {
		return channel.WebhookResult{Destinations: []channel.Destination{discovery}}, nil
	}
	content := x.Message.Text
	if content == "" {
		content = x.Message.Caption
	}
	p := strings.Fields(content)
	if x.Message.MessageThreadID == 0 {
		return channel.WebhookResult{Destinations: []channel.Destination{discovery}}, nil
	}
	if len(p) == 0 {
		if mutation == "" {
			return channel.WebhookResult{Destinations: []channel.Destination{discovery}}, nil
		}
		return channel.WebhookResult{Events: []channel.Inbound{{EventID: strconv.FormatInt(x.UpdateID, 10), ExternalChannelID: strconv.FormatInt(x.Message.Chat.ID, 10), ExternalThreadID: strconv.FormatInt(x.Message.MessageThreadID, 10), ExternalUserID: strconv.FormatInt(x.Message.From.ID, 10), ExternalMessageID: strconv.FormatInt(x.Message.MessageID, 10), Mutation: mutation, Files: telegramFiles(x.Message)}}, Destinations: []channel.Destination{discovery}}, nil
	}
	cmd, text := "chat", strings.TrimSpace(content)
	mention := ""
	switch r := x.Message.ReplyTo; {
	case len(p[0]) > 1 && strings.HasPrefix(p[0], "@"):
		mention = p[0]
	case r != nil && r.From.IsBot && r.From.Username != "" && r.TopicCreated == nil:
		// A reply to a bot's message names that bot. Every message in a topic also
		// replies to the topic's first message, which is not a reply to anyone.
		mention = "@" + r.From.Username
	}
	if strings.HasPrefix(p[0], "/") {
		candidate := strings.TrimPrefix(strings.SplitN(p[0], "@", 2)[0], "/")
		if explicitCommand(candidate) {
			cmd = candidate
			text = strings.TrimSpace(strings.TrimPrefix(content, p[0]))
		} else {
			text = strings.TrimSpace(strings.TrimPrefix(content, p[0]))
			if text == "" {
				text = candidate
			} else {
				text = candidate + " " + text
			}
		}
	}
	files := telegramFiles(x.Message)
	return channel.WebhookResult{Events: []channel.Inbound{{EventID: strconv.FormatInt(x.UpdateID, 10), ExternalChannelID: strconv.FormatInt(x.Message.Chat.ID, 10), ExternalThreadID: strconv.FormatInt(x.Message.MessageThreadID, 10), ExternalUserID: strconv.FormatInt(x.Message.From.ID, 10), Command: cmd, Text: text, Value: text, Mention: mention, ExternalMessageID: strconv.FormatInt(x.Message.MessageID, 10), Mutation: mutation, Files: files}}, Destinations: []channel.Destination{discovery}}, nil
}

func telegramFiles(message *telegramInboundMessage) []channel.Attachment {
	var files []channel.Attachment
	if d := message.Document; d != nil {
		files = append(files, channel.Attachment{ID: d.FileID, Name: d.FileName, ContentType: d.MimeType, SizeBytes: d.FileSize})
	} else if n := len(message.Photo); n > 0 {
		photo := message.Photo[n-1]
		files = append(files, channel.Attachment{ID: photo.FileID, Name: "photo.jpg", ContentType: "image/jpeg", SizeBytes: photo.FileSize})
	}
	return files
}

func (a Adapter) DownloadAttachment(ctx context.Context, c *http.Client, cr map[string]string, file channel.Attachment) (io.ReadCloser, error) {
	var got struct {
		FilePath string `json:"file_path"`
	}
	if err := a.call(ctx, c, cr, "getFile", map[string]any{"file_id": file.ID}, &got); err != nil {
		return nil, err
	}
	if got.FilePath == "" || strings.Contains(got.FilePath, "..") || strings.HasPrefix(got.FilePath, "/") {
		return nil, errors.New("telegram attachment path is invalid")
	}
	base := strings.TrimRight(a.BaseURL, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/file/bot"+url.PathEscape(cr["bot_token"])+"/"+got.FilePath, nil)
	if err != nil {
		return nil, errors.New("telegram attachment request is invalid")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, errors.New("telegram attachment download failed")
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("telegram attachment: status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// controls are the callback commands opendesk's own keyboards send.
var controls = map[string]bool{"assign_self": true, "status": true, "priority": true, "confirm": true, "cancel": true, "suggest": true, "ask": true}

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
	var root struct {
		MessageID int64 `json:"message_id"`
	}
	payload := ticketPayload(destination, id, t)
	if err := a.call(ctx, c, withChannel(cr, destination), "sendMessage", payload, &root); err != nil {
		return channel.Thread{}, err
	}
	return channel.Thread{ID: id, RootID: strconv.FormatInt(root.MessageID, 10)}, nil
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

func (a Adapter) PostRich(ctx context.Context, c *http.Client, cr map[string]string, thread string, message channel.RichMessage) (channel.MessageRef, error) {
	var out struct {
		MessageID int64 `json:"message_id"`
	}
	payload := richPayload(cr["channel_id"], thread, message)
	err := a.call(ctx, c, cr, "sendMessage", payload, &out)
	return channel.MessageRef{ID: strconv.FormatInt(out.MessageID, 10)}, err
}

func (a Adapter) UpdateRich(ctx context.Context, c *http.Client, cr map[string]string, _ string, id string, message channel.RichMessage) error {
	payload := richPayload(cr["channel_id"], "", message)
	payload["message_id"], _ = strconv.ParseInt(id, 10, 64)
	delete(payload, "message_thread_id")
	return a.edit(ctx, c, cr, "editMessageText", payload)
}

func (a Adapter) DeleteRich(ctx context.Context, c *http.Client, cr map[string]string, _ string, id string) error {
	mid, _ := strconv.ParseInt(id, 10, 64)
	return a.call(ctx, c, cr, "deleteMessage", map[string]any{"chat_id": cr["channel_id"], "message_id": mid}, nil)
}

func richPayload(chatID, thread string, message channel.RichMessage) map[string]any {
	text := telegramText(message.Text)
	previewURL := ""
	view := ""
	if message.ViewURL != "" {
		view = "\nView in opendesk: " + message.ViewURL
	}
	for i, file := range message.Attachments {
		line := fmt.Sprintf("\n%s · %d bytes\n%s", telegramText(file.Name), file.SizeBytes, file.URL)
		if len([]rune(text+line+view)) > 4000 {
			text += fmt.Sprintf("\n%d more attachment(s)", len(message.Attachments)-i)
			break
		}
		text += line
		if file.Preview && previewURL == "" {
			previewURL = file.URL
		}
	}
	text += view
	tid, _ := strconv.ParseInt(thread, 10, 64)
	preview := map[string]any{"is_disabled": true}
	if previewURL != "" {
		preview = map[string]any{"is_disabled": false, "url": previewURL}
	}
	return map[string]any{"chat_id": chatID, "message_thread_id": tid, "text": text, "link_preview_options": preview}
}
func (a Adapter) Update(ctx context.Context, c *http.Client, cr map[string]string, root string, t channel.Ticket) error {
	messageID, _ := strconv.ParseInt(root, 10, 64)
	payload := ticketPayload(cr["channel_id"], "", t)
	payload["message_id"] = messageID
	delete(payload, "message_thread_id")
	return a.edit(ctx, c, cr, "editMessageText", payload)
}

// edit treats Telegram's refusal to apply an edit that changes nothing as done.
func (a Adapter) edit(ctx context.Context, c *http.Client, cr map[string]string, method string, payload map[string]any) error {
	err := a.call(ctx, c, cr, method, payload, nil)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

func (a Adapter) PostInteractive(ctx context.Context, c *http.Client, cr map[string]string, thread string, message channel.Message) (channel.MessageRef, error) {
	tid, _ := strconv.ParseInt(thread, 10, 64)
	var out struct {
		MessageID int64 `json:"message_id"`
	}
	payload := interactivePayload(cr["channel_id"], tid, message)
	err := a.call(ctx, c, cr, "sendMessage", payload, &out)
	return channel.MessageRef{ID: strconv.FormatInt(out.MessageID, 10)}, err
}

func (a Adapter) ClearActions(ctx context.Context, c *http.Client, cr map[string]string, in channel.Inbound) error {
	id, err := strconv.ParseInt(in.SourceMessageID, 10, 64)
	if err != nil {
		return nil
	}
	return a.edit(ctx, c, cr, "editMessageReplyMarkup", map[string]any{
		"chat_id": cr["channel_id"], "message_id": id, "reply_markup": map[string]any{"inline_keyboard": []any{}},
	})
}

// Respond answers the callback query, with text shown to its sender alone.
func (a Adapter) Respond(ctx context.Context, c *http.Client, cr map[string]string, handle, text string) error {
	input := map[string]any{"callback_query_id": handle}
	if text != "" {
		if r := []rune(text); len(r) > 200 {
			text = string(r[:199]) + "…"
		}
		input["text"] = text
	}
	return a.call(ctx, c, cr, "answerCallbackQuery", input, nil)
}

func ticketPayload(chatID, thread string, t channel.Ticket) map[string]any {
	tid, _ := strconv.ParseInt(thread, 10, 64)
	rows := []any{
		[]any{callbackButton("Assign to me", "assign_self", ""), map[string]any{"text": "Open in opendesk", "url": t.URL}},
		[]any{callbackButton("Open", "status", "open"), callbackButton("Pending", "status", "pending"), callbackButton("On hold", "status", "on_hold")},
		[]any{callbackButton("Solved", "status", "solved"), callbackButton("Closed", "status", "closed")},
		[]any{callbackButton("Low", "priority", "low"), callbackButton("Normal", "priority", "normal"), callbackButton("High", "priority", "high"), callbackButton("Urgent", "priority", "urgent")},
	}
	return map[string]any{
		"chat_id": chatID, "message_thread_id": tid, "text": telegramText(render(t)),
		"link_preview_options": map[string]bool{"is_disabled": true},
		"reply_markup":         map[string]any{"inline_keyboard": rows},
	}
}

func callbackButton(label, command, value string) map[string]any {
	data := "opendesk:" + command
	if value != "" {
		data += ":" + value
	}
	return map[string]any{"text": label, "callback_data": data}
}

func interactivePayload(chatID string, thread int64, message channel.Message) map[string]any {
	payload := map[string]any{"chat_id": chatID, "message_thread_id": thread, "text": telegramText(message.Text), "link_preview_options": map[string]bool{"is_disabled": true}}
	if len(message.Actions) > 0 {
		buttons := make([]any, 0, len(message.Actions))
		for _, action := range message.Actions {
			buttons = append(buttons, callbackButton(action.Label, action.Command, action.Value))
		}
		payload["reply_markup"] = map[string]any{"inline_keyboard": []any{buttons}}
	}
	return payload
}
func render(t channel.Ticket) string {
	return fmt.Sprintf("#%d %s\n\nCustomer: %s\nAssignee: %s\nStatus: %s\nPriority: %s\n\n%s", t.Number, t.Subject, t.Customer, t.Assignee, strings.ReplaceAll(t.Status, "_", " "), t.Priority, t.URL)
}

func telegramText(s string) string { return strings.ReplaceAll(s, "@", "@\u200b") }
