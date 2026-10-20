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
	"strings"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

type Adapter struct{ BaseURL string }

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
		io.Copy(io.Discard, resp.Body)
		return &statusError{code: resp.StatusCode}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// statusError is a Discord API answer outside 2xx.
type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("discord: status %d", e.code) }

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
	{"name": "reply", "description": "Reply to the customer", "options": []map[string]any{{"name": "text", "description": "Reply text", "type": 3, "required": true}}},
	{"name": "note", "description": "Add an internal note", "options": []map[string]any{{"name": "text", "description": "Note text", "type": 3, "required": true}}},
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
		if err := a.call(ctx, c, cr["bot_token"], http.MethodPatch, "/applications/@me", map[string]string{"interactions_endpoint_url": setup.WebhookURL}, nil); err != nil {
			return err
		}
	}
	for _, command := range commands {
		if err := a.call(ctx, c, cr["bot_token"], http.MethodPost, "/applications/"+app.ID+"/guilds/"+cr["guild_id"]+"/commands", command, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a Adapter) Health(ctx context.Context, c *http.Client, cr map[string]string, setup channel.Setup) (channel.Health, error) {
	var app application
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/applications/@me", nil, &app); err != nil {
		return channel.Health{}, err
	}
	if app.InteractionsEndpointURL != setup.WebhookURL {
		return channel.Health{Status: "action_required", Detail: "Discord's interactions endpoint points somewhere else. Test the connection to repair it."}, nil
	}
	var registered []struct {
		Name string `json:"name"`
	}
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/applications/"+app.ID+"/guilds/"+cr["guild_id"]+"/commands", nil, &registered); err != nil {
		return channel.Health{}, err
	}
	want := map[string]bool{}
	for _, command := range commands {
		want[command["name"].(string)] = true
	}
	for _, command := range registered {
		delete(want, command.Name)
	}
	if len(want) > 0 {
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
		ID        string `json:"id"`
		GuildID   string `json:"guild_id"`
		ChannelID string `json:"channel_id"`
		Type      int    `json:"type"`
		Member    struct {
			User struct{ ID string } `json:"user"`
		} `json:"member"`
		Data struct {
			Name     string `json:"name"`
			CustomID string `json:"custom_id"`
			Options  []struct {
				Name  string
				Value any
			} `json:"options"`
		} `json:"data"`
		Message struct{ ID string } `json:"message"`
	}
	if err := json.Unmarshal(body, &x); err != nil {
		return channel.WebhookResult{}, err
	}
	if x.Type == 1 {
		return channel.WebhookResult{Status: http.StatusOK, ContentType: "application/json", Body: []byte(`{"type":1}`)}, nil
	}
	cmd := x.Data.Name
	if x.Data.CustomID != "" {
		cmd = x.Data.CustomID
	}
	cmd = strings.TrimPrefix(cmd, "opendesk:")
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
	return channel.WebhookResult{
		Events:      []channel.Inbound{{EventID: x.ID, AccountID: x.GuildID, ExternalChannelID: x.ChannelID, ExternalThreadID: x.ChannelID, ExternalUserID: x.Member.User.ID, Command: cmd, Text: text, Value: text, Directed: true}},
		Status:      http.StatusOK,
		ContentType: "application/json",
		Body:        []byte(`{"type":4,"data":{"content":"Command received.","flags":64}}`),
	}, nil
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
	if err := a.Post(ctx, c, cr, th.ID, render(t)); err != nil {
		return channel.Thread{}, err
	}
	return channel.Thread{ID: th.ID, ChannelID: th.ID}, nil
}
func (a Adapter) Post(ctx context.Context, c *http.Client, cr map[string]string, thread, text string) error {
	if thread == "" {
		thread = cr["channel_id"]
	}
	return a.call(ctx, c, cr["bot_token"], http.MethodPost, "/channels/"+thread+"/messages", map[string]any{
		"content": clip(text, 1900), "allowed_mentions": map[string]any{"parse": []string{}}, "flags": 4,
	}, nil)
}
func (a Adapter) Update(context.Context, *http.Client, map[string]string, string, channel.Ticket) error {
	return nil
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
