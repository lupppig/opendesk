package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TypeSafe is the client for Jev, TypeSafe's decision model:
// POST {base}/v1/systemone with a bearer key (docs.typesafe.ai/api), or the same request
// through OpenRouter (NewOpenRouter). Errors never
// carry the key, the request or the response body.
type TypeSafe struct {
	base, key, model string
	// path is the decisions endpoint under base; extra is merged into each body.
	path    string
	extra   map[string]any
	headers map[string]string
	hc      *http.Client
	// Timeout bounds each attempt; Attempts counts the first try.
	Timeout  time.Duration
	Attempts int
}

// Defaults: a decision is a short call, so attempts are bounded tighter than LLM calls.
const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"
	attemptTimeout = 15 * time.Second
	maxAttempts    = 3
	maxBody        = 4 << 20
)

// NewTypeSafe builds a client. base is the API origin (TypeSafe, or a compatible
// gateway such as https://ai-gateway.vercel.sh/typesafe).
func NewTypeSafe(base, key, model string) *TypeSafe {
	if base == "" {
		base = DefaultBaseURL
	}
	if model == "" {
		model = DefaultModel
	}
	return &TypeSafe{base: strings.TrimRight(base, "/"), key: key, model: model, path: "/v1/systemone",
		hc: &http.Client{}, Timeout: attemptTimeout, Attempts: maxAttempts}
}

// NewOpenRouter reaches Jev through OpenRouter's Decisions API
// ({base}/alpha/decisions, base = https://openrouter.ai/api). The request is the same;
// the response also reports the call's cost.
func NewOpenRouter(base, key, model, appURL string) *TypeSafe {
	if model == "" {
		model = "~typesafe/jev-latest"
	}
	c := NewTypeSafe(base, key, model)
	c.path = "/alpha/decisions"
	c.extra = map[string]any{"provider": map[string]any{"data_collection": "deny"}}
	c.headers = map[string]string{"HTTP-Referer": appURL, "X-Title": "opendesk"}
	return c
}

func (c *TypeSafe) Name() string     { return "jev" }
func (c *TypeSafe) Model() string    { return c.model }
func (c *TypeSafe) Calibrated() bool { return true }

type tsRequest struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
	Provider  any                 `json:"provider,omitempty"`
}

type tsResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Cost         float64 `json:"cost"`
	} `json:"usage"`
}

// Decide asks every question in one request.
func (c *TypeSafe) Decide(ctx context.Context, req Request) (Result, error) {
	if len(req.Questions) == 0 {
		return Result{Model: c.model, Answers: map[string]Answer{}}, nil
	}
	for _, q := range req.Questions {
		if err := q.Validate(); err != nil {
			return Result{}, err
		}
	}
	body, err := json.Marshal(tsRequest{State: req.State, Model: c.model, Questions: req.Questions, Provider: c.extra["provider"]})
	if err != nil {
		return Result{}, err
	}
	var last error = ErrUnavailable
	for n := 1; n <= c.Attempts; n++ {
		res, status, h, err := c.once(ctx, body)
		if err == nil {
			if err := Check(req, res); err != nil {
				return Result{}, err
			}
			return res, nil
		}
		if ctx.Err() != nil {
			return Result{}, ErrUnavailable
		}
		last = err
		if status != 0 && !retryable(status) {
			return Result{}, err
		}
		if n < c.Attempts {
			if sleep(ctx, backoff(n, h)) != nil {
				return Result{}, ErrUnavailable
			}
		}
	}
	return Result{}, last
}

func (c *TypeSafe) once(ctx context.Context, body []byte) (Result, int, http.Header, error) {
	actx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	r, err := http.NewRequestWithContext(actx, http.MethodPost, c.base+c.path, bytes.NewReader(body))
	if err != nil {
		return Result{}, 0, nil, ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+c.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	for k, v := range c.headers {
		if v != "" {
			r.Header.Set(k, v)
		}
	}
	resp, err := c.hc.Do(r)
	if err != nil {
		// Transport errors can quote the URL but never the key or body; still map to a
		// fixed error so nothing request-specific reaches a log.
		return Result{}, 0, nil, ErrUnavailable
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Result{}, 0, resp.Header, ErrUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, resp.StatusCode, resp.Header, statusError(resp.StatusCode)
	}
	var out tsResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.Answers == nil {
		return Result{}, resp.StatusCode, nil, ErrBadOutput
	}
	model := out.Model
	if model == "" {
		model = c.model
	}
	return Result{Model: model, Answers: out.Answers, InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens,
			CostUSD: out.Usage.Cost},
		resp.StatusCode, resp.Header, nil
}

// retryable: rate limits (429), overload (529), timeouts (408) and server errors.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == 529 || status == http.StatusRequestTimeout || status >= 500
}

func statusError(status int) error {
	switch {
	case status == http.StatusTooManyRequests || status == 529:
		return ErrBusy
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errors.Join(ErrRejected, ErrAuth)
	case status == http.StatusPaymentRequired:
		return errors.Join(ErrUnavailable, ErrNoFunds)
	case status == http.StatusUnprocessableEntity || status == http.StatusBadRequest || status == http.StatusNotFound:
		return ErrRejected
	}
	return ErrUnavailable
}

// backoff waits before attempt n+1, honouring retry-after-ms and Retry-After (capped).
func backoff(n int, h http.Header) time.Duration {
	if h != nil {
		if ms, err := strconv.Atoi(h.Get("retry-after-ms")); err == nil && ms >= 0 && ms <= 30000 {
			return time.Duration(ms) * time.Millisecond
		}
		if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s >= 0 && s <= 30 {
			return time.Duration(s) * time.Second
		}
	}
	base := time.Duration(1<<(n-1)) * 400 * time.Millisecond
	return base + time.Duration(rand.Int64N(int64(base/2)+1))
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
