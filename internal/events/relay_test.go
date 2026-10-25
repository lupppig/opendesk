package events

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDisconnectPayload(t *testing.T) {
	at := time.UnixMilli(1_760_000_000_123)
	var got map[string]any
	if err := json.Unmarshal(disconnectPayload("u1", "s1", at), &got); err != nil {
		t.Fatal(err)
	}
	if got["user_id"] != "u1" || got["session_id"] != "s1" || got["at"] != float64(1_760_000_000_123) {
		t.Fatalf("payload %v", got)
	}
	// A user-wide disconnect keeps an empty session_id.
	if err := json.Unmarshal(disconnectPayload("u1", "", at), &got); err != nil {
		t.Fatal(err)
	}
	if got["session_id"] != "" {
		t.Fatalf("user-wide payload %v", got)
	}
}
