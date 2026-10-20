package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

// Reasons shown to admins when Discord refuses the bot for good.
const (
	reasonToken    = "Discord refused the bot token. Disconnect this connection and connect it again with a new token."
	reasonIntents  = "Discord refused the Message Content intent. Turn it on for the bot in the Discord developer portal, then connect again."
	reasonSettings = "Discord refused the bot's connection settings. Disconnect this connection and connect it again."
)

// errResume asks Subscribe to reconnect at once and resume the same session.
var errResume = errors.New("discord gateway asked to reconnect")

// errZombie is a connection that stopped acknowledging heartbeats.
var errZombie = fmt.Errorf("discord gateway stopped acknowledging heartbeats: %w", errResume)

// maxResumes bounds back-to-back resumes before handing the retry to the engine's backoff.
const maxResumes = 3

// gatewaySession is what a resume needs: the session, where to resume it and
// the last sequence number seen.
type gatewaySession struct {
	ID        string
	ResumeURL string
	BotID     string
	Seq       atomic.Int64 // -1 before the first dispatch
}

func (s *gatewaySession) reset() {
	s.ID, s.ResumeURL = "", ""
	s.Seq.Store(-1)
}

type gatewayPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s"`
	T  string          `json:"t"`
}

// Subscribe receives bot mentions through Discord's Gateway. Discord's HTTP
// interactions endpoint only delivers application commands, so ordinary chat
// needs this optional long-lived adapter capability. A dropped connection is
// resumed where Discord allows it; close codes that mean the token or intents
// were refused end the subscription with a channel.FatalError.
func (a Adapter) Subscribe(ctx context.Context, c *http.Client, cr map[string]string, stream channel.Stream) error {
	var gateway struct {
		URL string `json:"url"`
	}
	if err := a.call(ctx, c, cr["bot_token"], http.MethodGet, "/gateway/bot", nil, &gateway); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.code == http.StatusUnauthorized {
			return &channel.FatalError{Reason: reasonToken}
		}
		return err
	}
	if gateway.URL == "" {
		return errors.New("discord gateway URL is empty")
	}
	sess := &gatewaySession{}
	sess.reset()
	resumes := 0
	for {
		url := gateway.URL
		if sess.ID != "" && sess.ResumeURL != "" {
			url = sess.ResumeURL
		}
		ready, err := a.session(ctx, c, url, cr, sess, stream)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ready {
			resumes = 0
		}
		if !errors.Is(err, errResume) || sess.ID == "" || resumes >= maxResumes {
			return err
		}
		resumes++
	}
}

// session runs one Gateway connection: identify or resume, heartbeat, and
// dispatch until it ends. ready reports that Discord accepted the session.
func (a Adapter) session(ctx context.Context, c *http.Client, url string, cr map[string]string, sess *gatewaySession, stream channel.Stream) (ready bool, err error) {
	ws, _, err := websocket.Dial(ctx, strings.TrimRight(url, "/")+"/?v=10&encoding=json", &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		return false, err
	}
	defer ws.CloseNow() // no close handshake, so Discord keeps the session resumable
	ws.SetReadLimit(1 << 20)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var hello gatewayPayload
	if err = readPayload(streamCtx, ws, &hello); err != nil {
		return false, closeError(err, sess)
	}
	var h struct {
		Interval float64 `json:"heartbeat_interval"`
	}
	if hello.Op != 10 || json.Unmarshal(hello.D, &h) != nil || h.Interval < 1000 {
		return false, errors.New("discord gateway sent an invalid hello")
	}
	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return ws.Write(streamCtx, websocket.MessageText, b)
	}
	if sess.ID != "" {
		err = send(map[string]any{"op": 6, "d": map[string]any{"token": cr["bot_token"], "session_id": sess.ID, "seq": sess.Seq.Load()}})
	} else {
		err = send(map[string]any{"op": 2, "d": map[string]any{
			"token": cr["bot_token"], "intents": gatewayIntents,
			"properties": map[string]string{"os": "linux", "browser": "opendesk", "device": "opendesk"},
		}})
	}
	if err != nil {
		return false, closeError(err, sess)
	}

	heartbeat := func() error {
		var seq any
		if s := sess.Seq.Load(); s >= 0 {
			seq = s
		}
		return send(map[string]any{"op": 1, "d": seq})
	}
	var acked atomic.Bool
	acked.Store(true)
	heartbeatErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Duration(h.Interval) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				// A missed acknowledgement means a dead connection: drop it and resume.
				err := errZombie
				if acked.Swap(false) {
					err = heartbeat()
				}
				if err != nil {
					heartbeatErr <- err
					ws.CloseNow()
					return
				}
			}
		}
	}()

	for {
		var event gatewayPayload
		if err = readPayload(streamCtx, ws, &event); err != nil {
			select {
			case herr := <-heartbeatErr:
				if errors.Is(herr, errZombie) {
					return ready, herr
				}
				err = herr
			default:
			}
			return ready, closeError(err, sess)
		}
		if event.S != nil {
			sess.Seq.Store(*event.S)
		}
		switch event.Op {
		case 1:
			if err = heartbeat(); err != nil {
				return ready, closeError(err, sess)
			}
			continue
		case 7:
			return ready, errResume
		case 9:
			var resumable bool
			_ = json.Unmarshal(event.D, &resumable)
			if !resumable {
				sess.reset()
				return ready, errors.New("discord gateway invalidated the session")
			}
			return ready, errResume
		case 11:
			acked.Store(true)
			continue
		case 0:
		default:
			continue
		}
		switch event.T {
		case "READY":
			var r struct {
				SessionID string `json:"session_id"`
				ResumeURL string `json:"resume_gateway_url"`
				User      struct {
					ID string `json:"id"`
				} `json:"user"`
			}
			if json.Unmarshal(event.D, &r) == nil {
				sess.ID, sess.ResumeURL, sess.BotID = r.SessionID, r.ResumeURL, r.User.ID
			}
			ready = true
			if stream.Connected != nil {
				stream.Connected()
			}
		case "RESUMED":
			ready = true
			if stream.Connected != nil {
				stream.Connected()
			}
		case "MESSAGE_CREATE":
			in, ok := mentionEvent(event.D, cr["guild_id"], sess.BotID)
			if !ok {
				continue
			}
			if err = stream.Emit(streamCtx, []channel.Inbound{in}); err != nil {
				return ready, err
			}
		}
	}
}

func readPayload(ctx context.Context, ws *websocket.Conn, v *gatewayPayload) error {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// closeError turns how a Gateway connection ended into what to do next:
// a FatalError for a refused token, intents or settings, errResume where the
// session can continue, and a plain error (fresh session after backoff) otherwise.
func closeError(err error, sess *gatewaySession) error {
	code := websocket.CloseStatus(err)
	switch code {
	case 4004:
		return &channel.FatalError{Reason: reasonToken}
	case 4014:
		return &channel.FatalError{Reason: reasonIntents}
	case 4010, 4011, 4012, 4013:
		return &channel.FatalError{Reason: reasonSettings}
	case 4007, 4009:
		sess.reset()
		return fmt.Errorf("discord gateway closed the session (%d)", code)
	case -1:
		return fmt.Errorf("discord gateway: %v: %w", err, errResume)
	default:
		return fmt.Errorf("discord gateway closed (%d): %w", code, errResume)
	}
}

// mentionEvent reads a MESSAGE_CREATE that mentions the bot in the configured
// server as a chat event for the engine.
func mentionEvent(raw json.RawMessage, guildID, botID string) (channel.Inbound, bool) {
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
	if json.Unmarshal(raw, &message) != nil || message.Author.Bot || message.GuildID != guildID || botID == "" {
		return channel.Inbound{}, false
	}
	mentioned := false
	for _, mention := range message.Mentions {
		mentioned = mentioned || mention.ID == botID
	}
	if !mentioned {
		return channel.Inbound{}, false
	}
	text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(message.Content, "<@"+botID+">", ""), "<@!"+botID+">", ""))
	if text == "" {
		text = "What can you tell me about this ticket?"
	}
	return channel.Inbound{EventID: message.ID, AccountID: message.GuildID, ExternalChannelID: message.ChannelID,
		ExternalThreadID: message.ChannelID, ExternalUserID: message.Author.ID, Command: "chat", Text: text, Value: text}, true
}
