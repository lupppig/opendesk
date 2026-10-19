package captcha

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// ErrCaptcha means the challenge was missing, invalid or stale.
var ErrCaptcha = errors.New("captcha verification failed")

type TurnstileConfig struct {
	Secret        string
	SiteVerifyURL string
	HTTPClient    *http.Client
	Hostnames     []string // the hostnames the challenge may be solved on; empty accepts any
	BypassToken   string   // optional: local/dev bypass token
	Disabled      bool     // CAPTCHA_PROVIDER=none: accept every request
}

type Response struct {
	Success     bool     `json:"success"`
	ChallengeTs string   `json:"challenge_ts,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	ErrorCodes  []string `json:"error-codes,omitempty"`
	Action      string   `json:"action,omitempty"`
	CData       string   `json:"cdata,omitempty"`
}

type Turnstile struct {
	cfg TurnstileConfig
}

// IsTestSecret reports whether secret is one of Cloudflare's test secrets, which answer
// for a placeholder hostname rather than the page's.
func IsTestSecret(secret string) bool {
	return len(secret) == 35 && strings.HasSuffix(secret, "x"+strings.Repeat("0", 31)+"AA")
}

const CloudflareTurnstileVerifyEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

func NewTurnstile(secret string) *Turnstile {
	return NewTurnstileFromConfig(TurnstileConfig{
		Secret:        secret,
		SiteVerifyURL: CloudflareTurnstileVerifyEndpoint,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	})
}

func NewTurnstileFromConfig(cfg TurnstileConfig) *Turnstile {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.SiteVerifyURL == "" {
		cfg.SiteVerifyURL = CloudflareTurnstileVerifyEndpoint
	}
	return &Turnstile{cfg: cfg}
}

func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) error {
	if t.cfg.Disabled {
		return nil
	}

	if t.cfg.BypassToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(t.cfg.BypassToken)) == 1 {
		return nil
	}

	if token == "" {
		return ErrCaptcha
	}

	data := url.Values{
		"secret":   {t.cfg.Secret},
		"response": {token},
	}
	if remoteIP != "" {
		if net.ParseIP(remoteIP) == nil {
			return ErrCaptcha
		}
		data.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		t.cfg.SiteVerifyURL,
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := t.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<12))
		return ErrCaptcha
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB max
	if err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}

	var r Response
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("turnstile: %w", err)
	}

	if !r.Success {
		return ErrCaptcha
	}

	// A token solved on another site is not one of ours.
	if len(t.cfg.Hostnames) > 0 && !slices.Contains(t.cfg.Hostnames, r.Hostname) {
		return ErrCaptcha
	}

	// Optional: verify timestamp is recent (prevent replay)
	if r.ChallengeTs != "" {
		ts, err := time.Parse(time.RFC3339, r.ChallengeTs)
		if err != nil {
			return ErrCaptcha
		}
		if time.Since(ts) > 5*time.Minute {
			return ErrCaptcha
		}
	}

	return nil
}
