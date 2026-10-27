package googlechat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

const (
	chatScope    = "https://www.googleapis.com/auth/chat.bot"
	chatIssuer   = "chat@system.gserviceaccount.com"
	chatKeysURL  = "https://www.googleapis.com/oauth2/v3/certs"
	chatTokenURL = "https://oauth2.googleapis.com/token"
)

var chatKeys = oidc.NewRemoteKeySet(context.Background(), chatKeysURL)

type Adapter struct {
	BaseURL  string
	TokenURL string
	KeySet   oidc.KeySet
	tokens   *tokenCache
}

type tokenCache struct {
	mu      sync.Mutex
	sources map[string]cachedToken
}

type cachedToken struct {
	fingerprint [32]byte
	source      oauth2.TokenSource
}

func New() *Adapter {
	return &Adapter{tokens: &tokenCache{sources: map[string]cachedToken{}}}
}

func (Adapter) RichTextLimit() int { return 6000 }

func (Adapter) Describe() channel.Descriptor {
	return channel.Descriptor{Key: "google_chat", Name: "Google Chat", Description: "Mirror email tickets into Google Chat spaces and work them by talking to Ollie.", Fields: []channel.Field{
		{Name: "service_account_json", Label: "Service account JSON", Secret: true},
	}}
}

func (a Adapter) endpoint(path string) string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/") + path
	}
	return "https://chat.googleapis.com/v1" + path
}

func (a Adapter) client(ctx context.Context, base *http.Client, credentials map[string]string) (*http.Client, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, base)
	key := []byte(credentials["service_account_json"])
	config, err := google.JWTConfigFromJSON(key, chatScope)
	if err != nil {
		return nil, errors.New("service_account_json must be a Google service account key")
	}
	config.TokenURL = chatTokenURL
	if a.TokenURL != "" {
		config.TokenURL = a.TokenURL
	}
	fingerprint := sha256.Sum256(key)
	a.tokens.mu.Lock()
	defer a.tokens.mu.Unlock()
	cached, ok := a.tokens.sources[config.Email]
	if !ok || cached.fingerprint != fingerprint {
		tokenContext := context.WithValue(context.Background(), oauth2.HTTPClient, base)
		cached = cachedToken{fingerprint: fingerprint, source: oauth2.ReuseTokenSource(nil, config.TokenSource(tokenContext))}
		a.tokens.sources[config.Email] = cached
	}
	return oauth2.NewClient(ctx, cached.source), nil
}

func (a Adapter) call(ctx context.Context, base *http.Client, credentials map[string]string, method, path string, query url.Values, input, output any) error {
	client, err := a.client(ctx, base, credentials)
	if err != nil {
		return err
	}
	endpoint := a.endpoint(path)
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("google chat: %w", &channel.RateLimited{RetryAfter: channel.RateLimitDelay(resp.Header, 0)})
	}
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("google chat: status %d", resp.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (a Adapter) Validate(ctx context.Context, client *http.Client, credentials map[string]string) (channel.Account, error) {
	var key struct {
		Email   string `json:"client_email"`
		Project string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(credentials["service_account_json"]), &key); err != nil || key.Email == "" {
		return channel.Account{}, errors.New("service_account_json must be a Google service account key")
	}
	var spaces struct {
		Spaces []json.RawMessage `json:"spaces"`
	}
	if err := a.call(ctx, client, credentials, http.MethodGet, "/spaces", url.Values{"pageSize": {"1"}}, nil, &spaces); err != nil {
		return channel.Account{}, err
	}
	name := key.Project
	if name == "" {
		name = key.Email
	}
	return channel.Account{ID: key.Email, Name: name}, nil
}

func (a Adapter) Discover(ctx context.Context, client *http.Client, credentials map[string]string) ([]channel.Destination, error) {
	var out []channel.Destination
	page := ""
	for n := 0; n < 50; n++ {
		query := url.Values{"pageSize": {"1000"}, "filter": {`spaceType = "SPACE"`}}
		if page != "" {
			query.Set("pageToken", page)
		}
		var result struct {
			Spaces []struct {
				Name                string `json:"name"`
				DisplayName         string `json:"displayName"`
				SpaceThreadingState string `json:"spaceThreadingState"`
			} `json:"spaces"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := a.call(ctx, client, credentials, http.MethodGet, "/spaces", query, nil, &result); err != nil {
			return nil, err
		}
		for _, space := range result.Spaces {
			if !validSpace(space.Name) || (space.SpaceThreadingState != "THREADED_MESSAGES" && space.SpaceThreadingState != "GROUPED_MESSAGES") {
				continue
			}
			name := space.DisplayName
			if name == "" {
				name = space.Name
			}
			out = append(out, channel.Destination{ID: space.Name, Name: name, Kind: "space", Available: true})
		}
		page = result.NextPageToken
		if page == "" {
			return out, nil
		}
	}
	return nil, errors.New("google chat spaces.list returned too many pages")
}

func (a Adapter) Health(context.Context, *http.Client, map[string]string, channel.Setup) (channel.Health, error) {
	return channel.Health{Status: "action_required", Detail: "Set the Chat app's connection URL and authentication audience to the webhook address, then add the app to a space."}, nil
}

func (a Adapter) Verify(headers http.Header, _ []byte, credentials map[string]string) error {
	token, ok := strings.CutPrefix(headers.Get("Authorization"), "Bearer ")
	if !ok || token == "" || credentials["webhook_url"] == "" {
		return errors.New("invalid Google Chat authorization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys := a.KeySet
	if keys == nil {
		keys = chatKeys
	}
	payload, err := keys.VerifySignature(ctx, token)
	if err != nil {
		return errors.New("invalid Google Chat token")
	}
	var claims struct {
		Issuer        string          `json:"iss"`
		Audience      json.RawMessage `json:"aud"`
		Email         string          `json:"email"`
		EmailVerified bool            `json:"email_verified"`
		Expires       int64           `json:"exp"`
		IssuedAt      int64           `json:"iat"`
		NotBefore     int64           `json:"nbf"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return errors.New("invalid Google Chat token")
	}
	var audience string
	if err := json.Unmarshal(claims.Audience, &audience); err != nil {
		var audiences []string
		if json.Unmarshal(claims.Audience, &audiences) != nil {
			return errors.New("invalid Google Chat token")
		}
		for _, candidate := range audiences {
			if candidate == credentials["webhook_url"] {
				audience = candidate
				break
			}
		}
	}
	now := time.Now().Unix()
	if (claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com") || audience != credentials["webhook_url"] ||
		claims.Email != chatIssuer || !claims.EmailVerified || claims.Expires <= now || claims.IssuedAt == 0 || claims.IssuedAt > now+5*60 || claims.NotBefore > now {
		return errors.New("invalid Google Chat token")
	}
	return nil
}

func (a Adapter) Parse(_ http.Header, body []byte) (channel.WebhookResult, error) {
	var event chatEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return channel.WebhookResult{}, err
	}
	spaceName := event.Space.Name
	if !validSpace(spaceName) {
		return channel.WebhookResult{}, nil
	}
	if event.Type == "ADDED_TO_SPACE" || event.Type == "REMOVED_FROM_SPACE" {
		available := event.Type == "ADDED_TO_SPACE" && (event.Space.Type == "ROOM" || event.Space.SpaceType == "SPACE") &&
			(event.Space.SpaceThreadingState == "THREADED_MESSAGES" || event.Space.SpaceThreadingState == "GROUPED_MESSAGES")
		return channel.WebhookResult{Destinations: []channel.Destination{{ID: spaceName, Name: event.Space.DisplayName, Kind: event.Space.SpaceThreadingState, Available: available}}}, nil
	}
	if event.Type == "CARD_CLICKED" {
		m := event.Message
		if !validMessage(m.Name) || !validThread(m.Thread.Name) {
			return channel.WebhookResult{Control: true, Status: http.StatusOK, ContentType: "application/json", Body: []byte("{}")}, nil
		}
		command, value := "", ""
		if event.Action.Method == "opendesk" {
			for _, parameter := range event.Action.Parameters {
				switch parameter.Key {
				case "command":
					command = parameter.Value
				case "value":
					value = parameter.Value
				}
			}
		}
		switch command {
		case "assign_self", "status", "priority", "confirm", "cancel":
		default:
			return channel.WebhookResult{Control: true, Status: http.StatusOK, ContentType: "application/json", Body: []byte("{}")}, nil
		}
		user := event.User.Name
		if user == "" {
			user = m.Sender.Name
		}
		return channel.WebhookResult{Control: true, Status: http.StatusOK, ContentType: "application/json", Body: []byte("{}"), Events: []channel.Inbound{{
			EventID: eventID(event, m), ExternalChannelID: spaceName, ExternalThreadID: m.Thread.Name,
			ExternalUserID: user, Command: command, Value: value, Text: value,
			SourceMessageID: m.Name, SourceText: m.Text,
		}}}, nil
	}
	if (event.Type != "MESSAGE" && event.Type != "MESSAGE_UPDATED" && event.Type != "MESSAGE_DELETED") || event.Message.Sender.Type == "BOT" {
		return channel.WebhookResult{}, nil
	}
	m := event.Message
	directMessage := event.Space.Type == "DM" || event.Space.Type == "DIRECT_MESSAGE"
	if !validMessage(m.Name) || (!validThread(m.Thread.Name) && !(directMessage && m.Thread.Name == "")) {
		return channel.WebhookResult{}, nil
	}
	text := strings.TrimSpace(m.ArgumentText)
	if text == "" {
		text = strings.TrimSpace(m.Text)
	}
	directed := event.Space.Type == "DIRECT_MESSAGE" || event.Space.Type == "DM"
	for _, annotation := range m.Annotations {
		if annotation.UserMention.User.Type == "BOT" {
			directed = true
			text = strings.TrimSpace(m.ArgumentText)
			break
		}
	}
	command := "chat"
	var ticketNumber int64
	if m.SlashCommand != nil {
		ticketNumber, command, text = parseCommand(m.ArgumentText, true)
		directed = true
	} else if directed || strings.HasPrefix(text, "/") {
		ticketNumber, command, text = parseCommand(text, false)
	}
	mutation := ""
	if event.Type == "MESSAGE_UPDATED" {
		mutation = "edit"
	} else if event.Type == "MESSAGE_DELETED" {
		mutation = "delete"
	}
	user := event.User.Name
	if user == "" {
		user = m.Sender.Name
	}
	return channel.WebhookResult{Events: []channel.Inbound{{
		EventID: eventID(event, m), ExternalChannelID: spaceName, ExternalThreadID: m.Thread.Name,
		ExternalUserID: user, Command: command, Text: text, Value: text, Directed: directed,
		TicketNumber: ticketNumber, ExternalMessageID: m.Name, Mutation: mutation,
	}}}, nil
}

func (a Adapter) CreateThread(ctx context.Context, client *http.Client, credentials map[string]string, destination string, ticket channel.Ticket) (channel.Thread, error) {
	if !validSpace(destination) {
		return channel.Thread{}, errors.New("invalid Google Chat space")
	}
	var message struct {
		Name   string `json:"name"`
		Thread struct {
			Name string `json:"name"`
		} `json:"thread"`
	}
	body := map[string]any{"text": ticketText(ticket), "thread": map[string]string{"threadKey": "opendesk-" + ticket.ID}}
	requestID := fmt.Sprintf("opendesk-%x", sha256.Sum256([]byte(ticket.ID+"\x00"+destination)))
	query := url.Values{"messageReplyOption": {"REPLY_MESSAGE_OR_FAIL"}, "requestId": {requestID}}
	err := a.call(ctx, client, credentials, http.MethodPost, "/"+destination+"/messages", query, body, &message)
	if err != nil {
		return channel.Thread{}, err
	}
	if message.Name == "" || !validThread(message.Thread.Name) {
		return channel.Thread{}, errors.New("Google Chat did not return the created thread")
	}
	return channel.Thread{ID: message.Thread.Name, RootID: message.Name, ChannelID: destination}, nil
}

func (a Adapter) Post(ctx context.Context, client *http.Client, credentials map[string]string, thread, text string) error {
	if thread == "" {
		space := credentials["channel_id"]
		if !validSpace(space) {
			return errors.New("invalid Google Chat space")
		}
		return a.call(ctx, client, credentials, http.MethodPost, "/"+space+"/messages", nil, map[string]string{"text": text}, nil)
	}
	space, ok := spaceForThread(thread)
	if !ok {
		return errors.New("invalid Google Chat thread")
	}
	query := url.Values{"messageReplyOption": {"REPLY_MESSAGE_OR_FAIL"}}
	return a.call(ctx, client, credentials, http.MethodPost, "/"+space+"/messages", query, map[string]any{"text": text, "thread": map[string]string{"name": thread}}, nil)
}

func (a Adapter) Update(ctx context.Context, client *http.Client, credentials map[string]string, root string, ticket channel.Ticket) error {
	if !validMessage(root) {
		return errors.New("invalid Google Chat message")
	}
	return a.call(ctx, client, credentials, http.MethodPatch, "/"+root, url.Values{"updateMask": {"text"}}, map[string]string{"name": root, "text": ticketText(ticket)}, nil)
}

func (a Adapter) PostRich(ctx context.Context, client *http.Client, credentials map[string]string, thread string, message channel.RichMessage) (channel.MessageRef, error) {
	space, ok := spaceForThread(thread)
	if !ok {
		return channel.MessageRef{}, errors.New("invalid Google Chat thread")
	}
	var out struct {
		Name string `json:"name"`
	}
	query := url.Values{"messageReplyOption": {"REPLY_MESSAGE_OR_FAIL"}}
	err := a.call(ctx, client, credentials, http.MethodPost, "/"+space+"/messages", query,
		map[string]any{"text": richText(message), "thread": map[string]string{"name": thread}}, &out)
	if err != nil {
		return channel.MessageRef{}, err
	}
	return channel.MessageRef{ID: out.Name}, nil
}

func (a Adapter) UpdateRich(ctx context.Context, client *http.Client, credentials map[string]string, _ string, id string, message channel.RichMessage) error {
	if !validMessage(id) {
		return errors.New("invalid Google Chat message")
	}
	return a.call(ctx, client, credentials, http.MethodPatch, "/"+id, url.Values{"updateMask": {"text"}}, map[string]string{"name": id, "text": richText(message)}, nil)
}

func (a Adapter) DeleteRich(ctx context.Context, client *http.Client, credentials map[string]string, _ string, id string) error {
	if !validMessage(id) {
		return errors.New("invalid Google Chat message")
	}
	return a.call(ctx, client, credentials, http.MethodDelete, "/"+id, nil, nil, nil)
}

func (a Adapter) PostInteractive(ctx context.Context, client *http.Client, credentials map[string]string, thread string, message channel.Message) (channel.MessageRef, error) {
	space, ok := spaceForThread(thread)
	if !ok {
		return channel.MessageRef{}, errors.New("invalid Google Chat thread")
	}
	buttons := make([]map[string]any, 0, len(message.Actions))
	for _, action := range message.Actions {
		button := map[string]any{"text": action.Label}
		if action.URL != "" {
			button["onClick"] = map[string]any{"openLink": map[string]string{"url": action.URL}}
		} else {
			button["onClick"] = map[string]any{"action": map[string]any{"function": "opendesk", "parameters": []map[string]string{{"key": "command", "value": action.Command}, {"key": "value", "value": action.Value}}}}
		}
		buttons = append(buttons, button)
	}
	var out struct {
		Name string `json:"name"`
	}
	query := url.Values{"messageReplyOption": {"REPLY_MESSAGE_OR_FAIL"}}
	body := map[string]any{
		"text":   message.Text,
		"thread": map[string]string{"name": thread},
		"cardsV2": []any{map[string]any{
			"cardId": "opendesk",
			"card":   map[string]any{"sections": []any{map[string]any{"widgets": []any{map[string]any{"buttonList": map[string]any{"buttons": buttons}}}}}},
		}},
	}
	if err := a.call(ctx, client, credentials, http.MethodPost, "/"+space+"/messages", query, body, &out); err != nil {
		return channel.MessageRef{}, err
	}
	return channel.MessageRef{ID: out.Name}, nil
}

func (a Adapter) ClearActions(ctx context.Context, client *http.Client, credentials map[string]string, inbound channel.Inbound) error {
	if !validMessage(inbound.SourceMessageID) {
		return errors.New("invalid Google Chat message")
	}
	return a.call(ctx, client, credentials, http.MethodPatch, "/"+inbound.SourceMessageID, url.Values{"updateMask": {"cardsV2"}}, map[string]any{"name": inbound.SourceMessageID, "cardsV2": []any{}}, nil)
}

func richText(message channel.RichMessage) string {
	text := message.Text
	for _, attachment := range message.Attachments {
		text += fmt.Sprintf("\n%s · %d bytes: %s", attachment.Name, attachment.SizeBytes, attachment.URL)
	}
	if message.ViewURL != "" {
		text += "\nView in opendesk: " + message.ViewURL
	}
	return text
}

func ticketText(ticket channel.Ticket) string {
	return fmt.Sprintf("Ticket #%d: %s\nCustomer: %s\nStatus: %s · Priority: %s\nAssignee: %s\n%s",
		ticket.Number, ticket.Subject, ticket.Customer, ticket.Status, ticket.Priority, ticket.Assignee, ticket.URL)
}

func eventID(event chatEvent, message chatMessage) string {
	at := event.EventTime
	if at == "" {
		at = message.CreateTime
	}
	return event.Type + ":" + message.Name + ":" + at
}

func parseCommand(text string, slash bool) (int64, string, string) {
	words := strings.Fields(strings.TrimSpace(text))
	var number int64
	if len(words) > 0 {
		if n, err := strconv.ParseInt(strings.TrimPrefix(words[0], "#"), 10, 64); err == nil && n > 0 {
			number = n
			words = words[1:]
		} else {
			number = 0
		}
	}
	if len(words) == 0 {
		return number, "chat", ""
	}
	command := strings.TrimPrefix(strings.ToLower(words[0]), "/")
	switch command {
	case "link", "reply", "note", "assign", "assign_self", "status", "priority", "confirm", "cancel":
		return number, command, strings.Join(words[1:], " ")
	default:
		if slash && command == "opendesk" {
			return parseCommand(strings.Join(words[1:], " "), false)
		}
		return number, "chat", strings.TrimSpace(strings.Join(words, " "))
	}
}

func validSpace(name string) bool {
	return validResource(name, "spaces/")
}

func validThread(name string) bool {
	parts := strings.Split(name, "/")
	return len(parts) == 4 && parts[0] == "spaces" && parts[1] != "" && parts[2] == "threads" && parts[3] != ""
}

func validMessage(name string) bool {
	parts := strings.Split(name, "/")
	return len(parts) == 4 && parts[0] == "spaces" && parts[1] != "" && parts[2] == "messages" && parts[3] != ""
}

func validResource(name, prefix string) bool {
	return strings.HasPrefix(name, prefix) && !strings.ContainsAny(strings.TrimPrefix(name, prefix), "/?#") && strings.TrimPrefix(name, prefix) != ""
}

func spaceForThread(thread string) (string, bool) {
	if !validThread(thread) {
		return "", false
	}
	return strings.Join(strings.Split(thread, "/")[:2], "/"), true
}

type chatEvent struct {
	Type      string      `json:"type"`
	EventTime string      `json:"eventTime"`
	User      chatUser    `json:"user"`
	Space     chatSpace   `json:"space"`
	Message   chatMessage `json:"message"`
	Action    struct {
		Method     string `json:"actionMethodName"`
		Parameters []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"parameters"`
	} `json:"action"`
}

type chatSpace struct {
	Name                string `json:"name"`
	DisplayName         string `json:"displayName"`
	Type                string `json:"type"`
	SpaceType           string `json:"spaceType"`
	SpaceThreadingState string `json:"spaceThreadingState"`
}

type chatUser struct {
	Name string `json:"name"`
}

type chatMessage struct {
	Name         string `json:"name"`
	Text         string `json:"text"`
	ArgumentText string `json:"argumentText"`
	CreateTime   string `json:"createTime"`
	Thread       struct {
		Name string `json:"name"`
	} `json:"thread"`
	Sender struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"sender"`
	SlashCommand *struct {
		CommandName string `json:"commandName"`
	} `json:"slashCommand"`
	Annotations []struct {
		UserMention struct {
			User struct {
				Type string `json:"type"`
			} `json:"user"`
		} `json:"userMention"`
	} `json:"annotations"`
}
