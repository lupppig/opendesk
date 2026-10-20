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
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

type Adapter struct{ BaseURL string }

const gatewayIntents = 512 | 32768 // GUILD_MESSAGES | MESSAGE_CONTENT

func (Adapter) Describe() channel.Descriptor {
	return channel.Descriptor{Key: "discord", Name: "Discord", Description: "Mirror email tickets into Discord threads and work them by talking to Ollie.", Fields: []channel.Field{
		{Name: "bot_token", Label: "Bot token", Secret: true, Help: "Enable the Message Content intent so Ollie can receive mentions in ticket threads."},
		{Name: "public_key", Label: "Interactions public key", Secret: true},
		{Name: "guild_id", Label: "Server ID"},
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
		return fmt.Errorf("discord: status %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
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
		Events:      []channel.Inbound{{EventID: x.ID, AccountID: x.GuildID, ExternalChannelID: x.ChannelID, ExternalThreadID: x.ChannelID, ExternalUserID: x.Member.User.ID, Command: cmd, Text: text, Value: text}},
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

// Subscribe receives bot mentions through Discord's Gateway. Discord's HTTP
// interactions endpoint only delivers application commands, so ordinary chat
// needs this optional long-lived adapter capability.
func (a Adapter) Subscribe(ctx context.Context, c *http.Client, cr map[string]string, emit func(context.Context, []channel.Inbound) error) error {
	var gateway struct {
		URL string `json:"url"`
	}
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/gateway/bot", nil, &gateway); err != nil {
		return err
	}
	if gateway.URL == "" {
		return errors.New("discord gateway URL is empty")
	}
	cfg, err := websocket.NewConfig(strings.TrimRight(gateway.URL, "/")+"/?v=10&encoding=json", "https://opendesk.sh")
	if err != nil {
		return err
	}
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		return err
	}
	defer ws.Close()
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ws.MaxPayloadBytes = 1 << 20
	go func() {
		<-streamCtx.Done()
		_ = ws.Close()
	}()

	type payload struct {
		Op int             `json:"op"`
		D  json.RawMessage `json:"d"`
		S  *int64          `json:"s"`
		T  string          `json:"t"`
	}
	var hello payload
	if err = websocket.JSON.Receive(ws, &hello); err != nil || hello.Op != 10 {
		if err == nil {
			err = errors.New("discord gateway did not send hello")
		}
		return err
	}
	var h struct {
		Interval float64 `json:"heartbeat_interval"`
	}
	if err = json.Unmarshal(hello.D, &h); err != nil || h.Interval < 1000 {
		return errors.New("discord gateway sent invalid heartbeat interval")
	}
	identify := map[string]any{"op": 2, "d": map[string]any{
		"token": cr["bot_token"], "intents": gatewayIntents,
		"properties": map[string]string{"os": "linux", "browser": "opendesk", "device": "opendesk"},
	}}
	if err = websocket.JSON.Send(ws, identify); err != nil {
		return err
	}
	var sequence atomic.Int64
	sequence.Store(-1)
	sendHeartbeat := func() error {
		var data any
		if seq := sequence.Load(); seq >= 0 {
			data = seq
		}
		return websocket.JSON.Send(ws, map[string]any{"op": 1, "d": data})
	}
	heartbeats := time.NewTicker(time.Duration(h.Interval) * time.Millisecond)
	defer heartbeats.Stop()
	heartbeatErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-heartbeats.C:
				if sendErr := sendHeartbeat(); sendErr != nil {
					select {
					case heartbeatErr <- sendErr:
					default:
					}
					return
				}
			}
		}
	}()

	var botID string
	for {
		var event payload
		if err = websocket.JSON.Receive(ws, &event); err != nil {
			select {
			case err = <-heartbeatErr:
			default:
			}
			return err
		}
		if event.S != nil {
			sequence.Store(*event.S)
		}
		if event.Op == 7 || event.Op == 9 {
			return errors.New("discord gateway requested reconnect")
		}
		if event.Op == 1 {
			if err = sendHeartbeat(); err != nil {
				return err
			}
			continue
		}
		if event.Op != 0 {
			continue
		}
		switch event.T {
		case "READY":
			var ready struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			}
			if json.Unmarshal(event.D, &ready) == nil {
				botID = ready.User.ID
			}
		case "MESSAGE_CREATE":
			var message struct {
				ID        string `json:"id"`
				GuildID   string `json:"guild_id"`
				ChannelID string `json:"channel_id"`
				Content   string `json:"content"`
				Author    struct {
					ID  string `json:"id"`
					Bot bool   `json:"bot"`
				} `json:"author"`
				Mentions []struct {
					ID string `json:"id"`
				} `json:"mentions"`
			}
			if json.Unmarshal(event.D, &message) != nil || message.Author.Bot || message.GuildID != cr["guild_id"] || botID == "" {
				continue
			}
			mentioned := false
			for _, mention := range message.Mentions {
				mentioned = mentioned || mention.ID == botID
			}
			if !mentioned {
				continue
			}
			text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(message.Content, "<@"+botID+">", ""), "<@!"+botID+">", ""))
			if text == "" {
				text = "What can you tell me about this ticket?"
			}
			in := channel.Inbound{EventID: message.ID, AccountID: message.GuildID, ExternalChannelID: message.ChannelID,
				ExternalThreadID: message.ChannelID, ExternalUserID: message.Author.ID, Command: "chat", Text: text, Value: text}
			if err = emit(streamCtx, []channel.Inbound{in}); err != nil {
				return err
			}
		}
	}
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
