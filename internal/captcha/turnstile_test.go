package captcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func verifyAgainst(t *testing.T, body string, cfg TurnstileConfig) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	cfg.Secret = "s"
	cfg.SiteVerifyURL = srv.URL
	turn := NewTurnstileFromConfig(cfg)
	return turn.Verify(context.Background(), "token", "")
}

func TestTurnstileSuccess(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	err := verifyAgainst(t, `{"success":true,"challenge_ts":"`+ts+`","hostname":"example.com"}`, TurnstileConfig{})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestTurnstileSuccessNoTimestamp(t *testing.T) {
	// challenge_ts is documented as omitempty; its absence must not be treated as stale.
	err := verifyAgainst(t, `{"success":true,"hostname":"example.com"}`, TurnstileConfig{})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestTurnstileFailure(t *testing.T) {
	err := verifyAgainst(t, `{"success":false,"error-codes":["invalid-input-response"]}`, TurnstileConfig{})
	if err != ErrCaptcha {
		t.Fatalf("expected ErrCaptcha, got %v", err)
	}
}

func TestTurnstileHostnameMatches(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	err := verifyAgainst(t, `{"success":true,"challenge_ts":"`+ts+`","hostname":"app.opendesk.sh"}`,
		TurnstileConfig{Hostnames: []string{"app.opendesk.sh", "admin.opendesk.sh"}})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestTurnstileHostnameMismatch(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	err := verifyAgainst(t, `{"success":true,"challenge_ts":"`+ts+`","hostname":"attacker.example"}`,
		TurnstileConfig{Hostnames: []string{"app.opendesk.sh", "admin.opendesk.sh"}})
	if err != ErrCaptcha {
		t.Fatalf("expected ErrCaptcha for host mismatch, got %v", err)
	}
}

func TestTurnstileReplayStaleChallenge(t *testing.T) {
	ts := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	err := verifyAgainst(t, `{"success":true,"challenge_ts":"`+ts+`","hostname":"example.com"}`, TurnstileConfig{})
	if err != ErrCaptcha {
		t.Fatalf("expected ErrCaptcha for a stale challenge, got %v", err)
	}
}

func TestTurnstileChallengeJustUnderLimit(t *testing.T) {
	ts := time.Now().UTC().Add(-4 * time.Minute).Format(time.RFC3339)
	err := verifyAgainst(t, `{"success":true,"challenge_ts":"`+ts+`","hostname":"example.com"}`, TurnstileConfig{})
	if err != nil {
		t.Fatalf("expected success for a 4-minute-old challenge, got %v", err)
	}
}

func TestTurnstileMalformedTimestamp(t *testing.T) {
	err := verifyAgainst(t, `{"success":true,"challenge_ts":"not-a-timestamp"}`, TurnstileConfig{})
	if err != ErrCaptcha {
		t.Fatalf("expected ErrCaptcha for a malformed timestamp, got %v", err)
	}
}

func TestTurnstileMalformedJSON(t *testing.T) {
	if err := verifyAgainst(t, `not json`, TurnstileConfig{}); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestTurnstileNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	turn := NewTurnstileFromConfig(TurnstileConfig{Secret: "s", SiteVerifyURL: srv.URL})
	if err := turn.Verify(context.Background(), "token", ""); err != ErrCaptcha {
		t.Fatalf("expected ErrCaptcha for a non-200 response, got %v", err)
	}
}

func TestTurnstileDisabled(t *testing.T) {
	turn := NewTurnstileFromConfig(TurnstileConfig{Disabled: true})
	if err := turn.Verify(context.Background(), "", ""); err != nil {
		t.Fatalf("expected nil when captcha is disabled, got %v", err)
	}
}

func TestTurnstileBypassToken(t *testing.T) {
	turn := NewTurnstileFromConfig(TurnstileConfig{BypassToken: "dev-secret"})
	if err := turn.Verify(context.Background(), "dev-secret", ""); err != nil {
		t.Fatalf("expected nil for a matching bypass token, got %v", err)
	}
}

func TestTurnstileEmptyToken(t *testing.T) {
	turn := NewTurnstileFromConfig(TurnstileConfig{})
	if err := turn.Verify(context.Background(), "", ""); err != ErrCaptcha {
		t.Fatalf("expected ErrCaptcha for an empty token, got %v", err)
	}
}

func TestTurnstileInvalidRemoteIP(t *testing.T) {
	turn := NewTurnstileFromConfig(TurnstileConfig{})
	if err := turn.Verify(context.Background(), "token", "not-an-ip"); err != ErrCaptcha {
		t.Fatalf("expected ErrCaptcha for an invalid remote IP, got %v", err)
	}
}

func TestTurnstileIsTestSecret(t *testing.T) {
	for _, s := range []string{"1x0000000000000000000000000000000AA", "2x0000000000000000000000000000000AA", "3x0000000000000000000000000000000AA"} {
		if !IsTestSecret(s) {
			t.Errorf("%s is a test secret", s)
		}
	}
	for _, s := range []string{"", "0x4AAAAAAABkMYinukE8nzYS_real-secret", "1x0000000000000000000000000000000AB"} {
		if IsTestSecret(s) {
			t.Errorf("%q is not a test secret", s)
		}
	}
}
