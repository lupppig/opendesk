package discord

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

// fakeGateway serves /gateway/bot and a Gateway socket whose behaviour per
// connection is given by script.
func fakeGateway(t *testing.T, script func(n int, ws *websocket.Conn, first map[string]any)) Adapter {
	t.Helper()
	var mu sync.Mutex
	conns := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gateway/bot":
			_ = json.NewEncoder(w).Encode(map[string]string{"url": "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"})
		case strings.HasPrefix(r.URL.Path, "/ws"):
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer ws.CloseNow()
			mu.Lock()
			n := conns
			conns++
			mu.Unlock()
			ctx := r.Context()
			_ = ws.Write(ctx, websocket.MessageText, []byte(`{"op":10,"d":{"heartbeat_interval":45000}}`))
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var first map[string]any
			_ = json.Unmarshal(data, &first)
			script(n, ws, first)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return Adapter{BaseURL: srv.URL}
}

func TestSubscribeStopsOnRefusedToken(t *testing.T) {
	a := fakeGateway(t, func(_ int, ws *websocket.Conn, first map[string]any) {
		if first["op"] != float64(2) {
			t.Errorf("first payload = %v, want identify", first)
		}
		_ = ws.Close(4004, "Authentication failed.")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := a.Subscribe(ctx, &http.Client{}, map[string]string{"bot_token": "t", "guild_id": "G1"}, channel.Stream{Emit: func(context.Context, []channel.Inbound) error { return nil }})
	var fatal *channel.FatalError
	if !errors.As(err, &fatal) || fatal.Reason != reasonToken {
		t.Fatalf("err = %v, want the refused token", err)
	}
}

func TestSubscribeResumesAfterReconnectRequest(t *testing.T) {
	var connected int
	var events []channel.Inbound
	a := fakeGateway(t, func(n int, ws *websocket.Conn, first map[string]any) {
		ctx := context.Background()
		switch n {
		case 0:
			if first["op"] != float64(2) {
				t.Errorf("first payload = %v, want identify", first)
			}
			// The resume URL is this same server.
			_ = ws.Write(ctx, websocket.MessageText, []byte(`{"op":0,"t":"READY","s":1,"d":{"session_id":"S1","resume_gateway_url":"`+resumeURL+`","user":{"id":"BOT"}}}`))
			_ = ws.Write(ctx, websocket.MessageText, []byte(`{"op":7,"d":null}`))
		case 1:
			d, _ := first["d"].(map[string]any)
			if first["op"] != float64(6) || d["session_id"] != "S1" || d["seq"] != float64(1) {
				t.Errorf("second payload = %v, want resume of S1 at 1", first)
			}
			_ = ws.Write(ctx, websocket.MessageText, []byte(`{"op":0,"t":"RESUMED","s":2,"d":{}}`))
			_ = ws.Write(ctx, websocket.MessageText, []byte(`{"op":0,"t":"MESSAGE_CREATE","s":3,"d":{"id":"M1","guild_id":"G1","channel_id":"TH1","content":"<@BOT> summarize","author":{"id":"U1"},"mentions":[{"id":"BOT"}]}}`))
			time.Sleep(100 * time.Millisecond)
			_ = ws.Close(4014, "Disallowed intent(s).")
		}
	})
	resumeURL = "ws" + strings.TrimPrefix(a.BaseURL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := a.Subscribe(ctx, &http.Client{}, map[string]string{"bot_token": "t", "guild_id": "G1"}, channel.Stream{
		Emit:      func(_ context.Context, in []channel.Inbound) error { events = append(events, in...); return nil },
		Connected: func() { connected++ },
	})
	var fatal *channel.FatalError
	if !errors.As(err, &fatal) || fatal.Reason != reasonIntents {
		t.Fatalf("err = %v, want the refused intents", err)
	}
	if connected != 2 {
		t.Fatalf("connected = %d, want READY and RESUMED", connected)
	}
	if len(events) != 1 || events[0].Text != "summarize" || events[0].ExternalThreadID != "TH1" {
		t.Fatalf("events = %+v", events)
	}
}

var resumeURL string

func TestCloseErrorClassification(t *testing.T) {
	sess := &gatewaySession{ID: "S1", ResumeURL: "wss://resume"}
	for _, tc := range []struct {
		code   websocket.StatusCode
		fatal  bool
		resume bool
	}{
		{4000, false, true}, {4004, true, false}, {4008, false, true}, {4009, false, false},
		{4013, true, false}, {4014, true, false},
	} {
		err := closeError(websocket.CloseError{Code: tc.code}, sess)
		var fatal *channel.FatalError
		if errors.As(err, &fatal) != tc.fatal || errors.Is(err, errResume) != tc.resume {
			t.Fatalf("code %d: %v", tc.code, err)
		}
	}
	if sess.ID != "" {
		t.Fatal("an expired session was kept for resuming")
	}
}
