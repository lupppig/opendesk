package discord

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

type Adapter struct{ BaseURL string }

func (Adapter) RichTextLimit() int { return 1500 }

const gatewayIntents = 512 | 32768 // GUILD_MESSAGES | MESSAGE_CONTENT

func (Adapter) Describe() channel.Descriptor {
	return channel.Descriptor{Key: "discord", Name: "Discord", Description: "Mirror email tickets into Discord threads and work them by talking to Ollie.", Fields: []channel.Field{
		{Name: "bot_token", Label: "Bot token", Secret: true, Help: "Enable the Message Content intent so Ollie can receive mentions in ticket threads."},
		{Name: "public_key", Label: "Interactions public key", Secret: true},
		{Name: "guild_id", Label: "Server", Discovered: true},
	}}
}
func (a Adapter) endpoint(path string) string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/") + path
	}
	return "https://discord.com/api/v10" + path
}
func (a Adapter) call(ctx context.Context, c *http.Client, token, method, path string, input any, out any) error {
	var body io.Reader
	if input != nil {
		b, _ := json.Marshal(input)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, a.endpoint(path), body)
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var refusal struct {
			Code int `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&refusal)
		io.Copy(io.Discard, resp.Body)
		return &statusError{code: resp.StatusCode, api: refusal.Code}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// statusError is a Discord API answer outside 2xx, with Discord's own error
// code when it sent one.
type statusError struct{ code, api int }

func (e *statusError) Error() string { return fmt.Sprintf("discord: status %d", e.code) }

// archived reports Discord refusing an edit in an archived thread (50083). The
// message keeps its last state; the next post to the thread unarchives it.
func archived(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.api == 50083
}

func (a Adapter) Validate(ctx context.Context, c *http.Client, cr map[string]string) (channel.Account, error) {
	if cr["bot_token"] == "" || cr["public_key"] == "" || cr["guild_id"] == "" {
		return channel.Account{}, errors.New("bot_token, public_key and guild_id are required")
	}
	if b, err := hex.DecodeString(cr["public_key"]); err != nil || len(b) != ed25519.PublicKeySize {
		return channel.Account{}, errors.New("public_key must be a Discord Ed25519 public key")
	}
	var g struct{ ID, Name string }
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/guilds/"+cr["guild_id"], nil, &g); err != nil {
		return channel.Account{}, err
	}
	return channel.Account{ID: g.ID, Name: g.Name}, nil
}

func (a Adapter) DiscoverAccounts(ctx context.Context, c *http.Client, cr map[string]string) ([]channel.Account, error) {
	if cr["bot_token"] == "" {
		return nil, errors.New("bot_token is required")
	}
	var guilds []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/users/@me/guilds", nil, &guilds); err != nil {
		return nil, err
	}
	out := make([]channel.Account, 0, len(guilds))
	for _, guild := range guilds {
		if guild.ID != "" {
			out = append(out, channel.Account{ID: guild.ID, Name: guild.Name})
		}
	}
	return out, nil
}

func (a Adapter) Discover(ctx context.Context, c *http.Client, cr map[string]string) ([]channel.Destination, error) {
	var items []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type int    `json:"type"`
	}
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/guilds/"+cr["guild_id"]+"/channels", nil, &items); err != nil {
		return nil, err
	}
	out := make([]channel.Destination, 0, len(items))
	for _, item := range items {
		// Ticket threads are created beneath ordinary guild text channels.
		if item.Type == 0 {
			out = append(out, channel.Destination{ID: item.ID, Name: item.Name, Kind: "text", Available: true})
		}
	}
	return out, nil
}

type application struct {
	ID                      string `json:"id"`
	InteractionsEndpointURL string `json:"interactions_endpoint_url"`
}

var commands = []map[string]any{
	{"name": "ask", "description": "Ask Ollie about the ticket", "options": []map[string]any{{"name": "text", "description": "What would you like to know?", "type": 3, "required": true}}},
	{"name": "link", "description": "Link your Discord account to opendesk", "options": []map[string]any{{"name": "text", "description": "The one-time link code", "type": 3, "required": true}}},
	{"name": "reply", "description": "Reply to the customer", "options": []map[string]any{{"name": "text", "description": "Reply text", "type": 3}, {"name": "file", "description": "File to attach", "type": 11}}},
	{"name": "note", "description": "Add an internal note", "options": []map[string]any{{"name": "text", "description": "Note text", "type": 3}, {"name": "file", "description": "File to attach", "type": 11}}},
	{"name": "assign", "description": "Assign the ticket to yourself"},
	{"name": "status", "description": "Change the ticket status", "options": []map[string]any{{"name": "value", "description": "The new status", "type": 3, "required": true}}},
	{"name": "priority", "description": "Change the ticket priority", "options": []map[string]any{{"name": "value", "description": "The new priority", "type": 3, "required": true}}},
}

func (a Adapter) Configure(ctx context.Context, c *http.Client, cr map[string]string, setup channel.Setup) error {
	var app application
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/applications/@me", nil, &app); err != nil {
		return err
	}
	if app.ID == "" {
		return errors.New("discord application has no id")
	}
	if app.InteractionsEndpointURL != setup.WebhookURL {
		// The interactions endpoint is one per application, not per server.
		if setup.Foreign != nil && setup.Foreign(app.InteractionsEndpointURL) {
			return channel.ErrInUse
		}
		if err := a.call(ctx, c, cr["bot_token"], http.MethodPatch, "/applications/@me", map[string]string{"interactions_endpoint_url": setup.WebhookURL}, nil); err != nil {
			return err
		}
	}
	// Only missing commands are registered: a command of the same name is left as it
	// is, and each registration counts against Discord's daily limit.
	missing, err := a.missingCommands(ctx, c, cr, app.ID)
	if err != nil {
		return err
	}
	for _, command := range commands {
		if !missing[command["name"].(string)] {
			continue
		}
		if err := a.call(ctx, c, cr["bot_token"], http.MethodPost, "/applications/"+app.ID+"/guilds/"+cr["guild_id"]+"/commands", command, nil); err != nil {
			return err
		}
	}
	return nil
}

// missingCommands are the names of opendesk's commands the server does not have.
func (a Adapter) missingCommands(ctx context.Context, c *http.Client, cr map[string]string, appID string) (map[string]bool, error) {
	var registered []struct {
		Name string `json:"name"`
	}
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/applications/"+appID+"/guilds/"+cr["guild_id"]+"/commands", nil, &registered); err != nil {
		return nil, err
	}
	missing := map[string]bool{}
	for _, command := range commands {
		missing[command["name"].(string)] = true
	}
	for _, command := range registered {
		delete(missing, command.Name)
	}
	return missing, nil
}

func (a Adapter) Health(ctx context.Context, c *http.Client, cr map[string]string, setup channel.Setup) (channel.Health, error) {
	var app application
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/applications/@me", nil, &app); err != nil {
		return channel.Health{}, err
	}
	if app.InteractionsEndpointURL != setup.WebhookURL {
		if setup.Foreign != nil && setup.Foreign(app.InteractionsEndpointURL) {
			return channel.Health{}, channel.ErrInUse
		}
		return channel.Health{Status: "action_required", Detail: "Discord's interactions endpoint points somewhere else. Test the connection to repair it."}, nil
	}
	missing, err := a.missingCommands(ctx, c, cr, app.ID)
	if err != nil {
		return channel.Health{}, err
	}
	if len(missing) > 0 {
		return channel.Health{Status: "action_required", Detail: "Some opendesk commands are missing. Test the connection to register them."}, nil
	}
	return channel.Health{Status: "healthy", Detail: "Discord's interactions endpoint and commands are configured."}, nil
}
func (Adapter) Verify(h http.Header, body []byte, cr map[string]string) error {
	pk, err := hex.DecodeString(cr["public_key"])
	if err != nil || len(pk) != ed25519.PublicKeySize {
		return errors.New("invalid discord public key")
	}
	sig, err := hex.DecodeString(h.Get("X-Signature-Ed25519"))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("invalid discord signature")
	}
	msg := append([]byte(h.Get("X-Signature-Timestamp")), body...)
	if !ed25519.Verify(pk, msg, sig) {
		return errors.New("invalid discord signature")
	}
	return nil
}
func (Adapter) Parse(_ http.Header, body []byte) (channel.WebhookResult, error) {
	var x struct {
		ID            string `json:"id"`
		ApplicationID string `json:"application_id"`
		Token         string `json:"token"`
		GuildID       string `json:"guild_id"`
		ChannelID     string `json:"channel_id"`
		Type          int    `json:"type"`
		Member        struct {
			User struct{ ID string } `json:"user"`
		} `json:"member"`
		Data struct {
			Name     string   `json:"name"`
			CustomID string   `json:"custom_id"`
			Values   []string `json:"values"`
			Options  []struct {
				Name  string
				Value any
			} `json:"options"`
			Resolved struct {
				Attachments map[string]struct {
					ID          string `json:"id"`
					Filename    string `json:"filename"`
					ContentType string `json:"content_type"`
					Size        int64  `json:"size"`
					URL         string `json:"url"`
				} `json:"attachments"`
			} `json:"resolved"`
		} `json:"data"`
		Message struct{ ID string } `json:"message"`
	}
	if err := json.Unmarshal(body, &x); err != nil {
		return channel.WebhookResult{}, err
	}
	if x.Type == 1 {
		return channel.WebhookResult{Status: http.StatusOK, ContentType: "application/json", Body: []byte(`{"type":1}`)}, nil
	}
	respond := ""
	if x.ApplicationID != "" && x.Token != "" {
		respond = x.ApplicationID + "/" + x.Token
	}
	if x.Type == 3 {
		return parseComponent(x.ID, x.GuildID, x.ChannelID, x.Member.User.ID, x.Data.CustomID, x.Data.Values, x.Message.ID, respond), nil
	}
	cmd := x.Data.Name
	var text string
	for _, o := range x.Data.Options {
		if o.Name == "text" || o.Name == "value" {
			text = fmt.Sprint(o.Value)
		}
		if o.Name == "action" {
			cmd = fmt.Sprint(o.Value)
		}
	}
	if cmd == "ask" || cmd == "chat" {
		cmd = "chat"
	}
	files := make([]channel.Attachment, 0, len(x.Data.Resolved.Attachments))
	for _, file := range x.Data.Resolved.Attachments {
		files = append(files, channel.Attachment{ID: file.ID, Name: file.Filename, ContentType: file.ContentType, SizeBytes: file.Size, URL: file.URL})
	}
	return channel.WebhookResult{
		Events:      []channel.Inbound{{EventID: x.ID, AccountID: x.GuildID, ExternalChannelID: x.ChannelID, ExternalThreadID: x.ChannelID, ExternalUserID: x.Member.User.ID, Command: cmd, Text: text, Value: text, Directed: true, Respond: respond, Files: files}},
		Status:      http.StatusOK,
		ContentType: "application/json",
		Body:        []byte(`{"type":4,"data":{"content":"Command received.","flags":64}}`),
	}, nil
}

func (a Adapter) DownloadAttachment(ctx context.Context, c *http.Client, _ map[string]string, file channel.Attachment) (io.ReadCloser, error) {
	u, err := url.Parse(file.URL)
	if err != nil || u.User != nil || !a.discordFileHost(u) {
		return nil, errors.New("discord attachment URL is invalid")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("discord attachment request is invalid")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, errors.New("discord attachment download failed")
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("discord attachment: status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (a Adapter) discordFileHost(u *url.URL) bool {
	if a.BaseURL != "" {
		base, err := url.Parse(a.BaseURL)
		return err == nil && u.Scheme == base.Scheme && u.Host == base.Host
	}
	return u.Scheme == "https" && (u.Host == "cdn.discordapp.com" || u.Host == "media.discordapp.net")
}

// controls are the custom ids opendesk's own components send.
var controls = map[string]bool{"assign_self": true, "status": true, "priority": true, "confirm": true, "cancel": true}

// parseComponent reads a button or select on a ticket card or confirmation
// preview. It is acknowledged without a visible answer (type 6): the card's
// edit, or a private follow-up, is the feedback.
func parseComponent(id, guild, channelID, user, customID string, values []string, message, respond string) channel.WebhookResult {
	result := channel.WebhookResult{Status: http.StatusOK, ContentType: "application/json", Body: []byte(`{"type":6}`)}
	cmd, ok := strings.CutPrefix(customID, "opendesk:")
	value := ""
	if before, after, found := strings.Cut(cmd, ":"); found {
		cmd, value = before, after
	}
	if !ok || !controls[cmd] {
		return result
	}
	if len(values) > 0 {
		value = values[0]
	}
	text := value
	if cmd == "confirm" || cmd == "cancel" {
		text = cmd
	}
	result.Control = true
	result.Events = []channel.Inbound{{EventID: id, AccountID: guild, ExternalChannelID: channelID, ExternalThreadID: channelID, ExternalUserID: user,
		Command: cmd, Text: text, Value: value, Directed: true, SourceMessageID: message, Respond: respond}}
	return result
}
func (a Adapter) CreateThread(ctx context.Context, c *http.Client, cr map[string]string, destination string, t channel.Ticket) (channel.Thread, error) {
	name := fmt.Sprintf("%d-%s", t.Number, t.Subject)
	if r := []rune(name); len(r) > 90 {
		name = string(r[:90])
	}
	var th struct{ ID string }
	if err := a.call(ctx, c, cr["bot_token"], http.MethodPost, "/channels/"+destination+"/threads", map[string]any{"name": name, "type": 11, "auto_archive_duration": 1440}, &th); err != nil {
		return channel.Thread{}, err
	}
	var root struct {
		ID string `json:"id"`
	}
	if err := a.call(ctx, c, cr["bot_token"], http.MethodPost, "/channels/"+th.ID+"/messages", ticketPayload(t), &root); err != nil {
		return channel.Thread{}, err
	}
	return channel.Thread{ID: th.ID, RootID: root.ID, ChannelID: th.ID}, nil
}
func (a Adapter) Post(ctx context.Context, c *http.Client, cr map[string]string, thread, text string) error {
	if thread == "" {
		thread = cr["channel_id"]
	}
	return a.call(ctx, c, cr["bot_token"], http.MethodPost, "/channels/"+thread+"/messages", map[string]any{
		"content": clip(text, 1900), "allowed_mentions": map[string]any{"parse": []string{}}, "flags": 4,
	}, nil)
}

func (a Adapter) PostRich(ctx context.Context, c *http.Client, cr map[string]string, thread string, message channel.RichMessage) (channel.MessageRef, error) {
	if thread == "" {
		thread = cr["channel_id"]
	}
	var out struct {
		ID string `json:"id"`
	}
	err := a.call(ctx, c, cr["bot_token"], http.MethodPost, "/channels/"+thread+"/messages", richPayload(message), &out)
	return channel.MessageRef{ID: out.ID}, err
}

func (a Adapter) UpdateRich(ctx context.Context, c *http.Client, cr map[string]string, thread, id string, message channel.RichMessage) error {
	err := a.call(ctx, c, cr["bot_token"], http.MethodPatch, "/channels/"+thread+"/messages/"+id, richPayload(message), nil)
	if archived(err) {
		return nil
	}
	return err
}

func (a Adapter) DeleteRich(ctx context.Context, c *http.Client, cr map[string]string, thread, id string) error {
	err := a.call(ctx, c, cr["bot_token"], http.MethodDelete, "/channels/"+thread+"/messages/"+id, nil, nil)
	if archived(err) {
		return nil
	}
	return err
}

func richPayload(message channel.RichMessage) map[string]any {
	text := message.Text
	var embeds []any
	view := ""
	if message.ViewURL != "" {
		view = "\nView in opendesk: " + message.ViewURL
	}
	for i, file := range message.Attachments {
		line := fmt.Sprintf("\n%s · %d bytes: %s", file.Name, file.SizeBytes, file.URL)
		if len([]rune(text+line+view)) > 1900 {
			text += fmt.Sprintf("\n%d more attachment(s)", len(message.Attachments)-i)
			break
		}
		text += line
		if file.Preview && len(embeds) < 10 {
			embeds = append(embeds, map[string]any{"image": map[string]string{"url": file.URL}})
		}
	}
	text += view
	return map[string]any{"content": clip(text, 1900), "embeds": embeds, "allowed_mentions": map[string]any{"parse": []string{}}, "flags": 4}
}
func (a Adapter) Update(ctx context.Context, c *http.Client, cr map[string]string, root string, t channel.Ticket) error {
	err := a.call(ctx, c, cr["bot_token"], http.MethodPatch, "/channels/"+cr["channel_id"]+"/messages/"+root, ticketPayload(t), nil)
	if archived(err) {
		return nil
	}
	return err
}

func (a Adapter) PostInteractive(ctx context.Context, c *http.Client, cr map[string]string, thread string, message channel.Message) (channel.MessageRef, error) {
	if thread == "" {
		thread = cr["channel_id"]
	}
	var out struct {
		ID string `json:"id"`
	}
	err := a.call(ctx, c, cr["bot_token"], http.MethodPost, "/channels/"+thread+"/messages", interactivePayload(message), &out)
	return channel.MessageRef{ID: out.ID}, err
}

// ClearActions removes the message's components and leaves its content.
func (a Adapter) ClearActions(ctx context.Context, c *http.Client, cr map[string]string, in channel.Inbound) error {
	if in.SourceMessageID == "" {
		return nil
	}
	err := a.call(ctx, c, cr["bot_token"], http.MethodPatch, "/channels/"+cr["channel_id"]+"/messages/"+in.SourceMessageID, map[string]any{"components": []any{}}, nil)
	if archived(err) {
		return nil
	}
	return err
}

// Respond sends an ephemeral follow-up to the interaction. The handle is the
// application id and the interaction's token, which authorizes the follow-up
// and so never appears in an error.
func (a Adapter) Respond(ctx context.Context, c *http.Client, _ map[string]string, handle, text string) error {
	appID, token, ok := strings.Cut(handle, "/")
	if text == "" || !ok || appID == "" || token == "" || strings.ContainsAny(appID, "/?#") || strings.ContainsAny(token, "/?#") {
		return nil
	}
	b, _ := json.Marshal(map[string]any{"content": clip(text, 1900), "flags": 64, "allowed_mentions": map[string]any{"parse": []string{}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint("/webhooks/"+appID+"/"+token), bytes.NewReader(b))
	if err != nil {
		return errors.New("discord follow-up: invalid interaction")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("discord follow-up: request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return &statusError{code: resp.StatusCode}
	}
	return nil
}

func ticketPayload(t channel.Ticket) map[string]any {
	return map[string]any{
		"content": "", "allowed_mentions": map[string]any{"parse": []string{}},
		"embeds": []any{map[string]any{
			"title": fmt.Sprintf("#%d %s", t.Number, clip(t.Subject, 220)), "url": t.URL,
			"description": fmt.Sprintf("**Customer**\n%s\n\n**Assignee**\n%s", clip(t.Customer, 300), clip(t.Assignee, 300)),
			"fields": []any{
				map[string]any{"name": "Status", "value": strings.ReplaceAll(t.Status, "_", " "), "inline": true},
				map[string]any{"name": "Priority", "value": t.Priority, "inline": true},
			},
		}},
		"components": []any{
			map[string]any{"type": 1, "components": []any{
				map[string]any{"type": 2, "style": 1, "label": "Assign to me", "custom_id": "opendesk:assign_self"},
				map[string]any{"type": 2, "style": 5, "label": "Open in opendesk", "url": t.URL},
			}},
			map[string]any{"type": 1, "components": []any{discordSelect("Status", "opendesk:status", []string{"open", "pending", "on_hold", "solved", "closed"})}},
			map[string]any{"type": 1, "components": []any{discordSelect("Priority", "opendesk:priority", []string{"low", "normal", "high", "urgent"})}},
		},
	}
}

func discordSelect(label, id string, values []string) map[string]any {
	options := make([]any, 0, len(values))
	for _, value := range values {
		options = append(options, map[string]any{"label": strings.ReplaceAll(value, "_", " "), "value": value})
	}
	return map[string]any{"type": 3, "custom_id": id, "placeholder": label, "options": options}
}

func interactivePayload(message channel.Message) map[string]any {
	payload := map[string]any{"content": clip(message.Text, 1900), "allowed_mentions": map[string]any{"parse": []string{}}, "components": []any{}}
	if len(message.Actions) > 0 {
		buttons := make([]any, 0, len(message.Actions))
		for _, action := range message.Actions {
			style := 2
			if action.Style == "primary" {
				style = 3
			} else if action.Style == "danger" {
				style = 4
			}
			id := "opendesk:" + action.Command
			if action.Value != "" {
				id += ":" + action.Value
			}
			buttons = append(buttons, map[string]any{"type": 2, "style": style, "label": action.Label, "custom_id": id})
		}
		payload["components"] = []any{map[string]any{"type": 1, "components": buttons}}
	}
	return payload
}

func render(t channel.Ticket) string {
	return fmt.Sprintf("**#%d %s**\n%s · %s · %s\n%s", t.Number, t.Subject, t.Customer, t.Status, t.Priority, t.URL)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
