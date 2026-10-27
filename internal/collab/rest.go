package collab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Provider HTTPS calls (Gmail and Microsoft Graph): the account's access token, one
// refresh on 401, a short wait and another try while the provider is busy, and provider
// errors reduced to safe, classified messages.

// restRequest is one provider call.
type restRequest struct {
	method      string
	url         string
	body        []byte
	contentType string
	headers     map[string]string
	// limit bounds the response body read; 1 MB when zero.
	limit int64
}

// errTooLarge is a response over the request's limit (a message over maxRawBytes).
var errTooLarge = errors.New("the message is too large to import")

// call runs a provider request and returns the response body of a 2xx answer. A busy
// provider (a rate limit, or a 5xx to a read) is asked again after each of s.busyWaits, or
// after its Retry-After when that is no longer than maxBusyWait.
func (s *service) call(ctx context.Context, a *accountRow, label string, r restRequest) ([]byte, error) {
	limit := r.limit
	if limit == 0 {
		limit = 1 << 20
	}
	refreshed, waited := false, 0
	for {
		token, err := s.accessToken(ctx, a)
		if err != nil {
			return nil, err
		}
		var rd io.Reader
		if r.body != nil {
			rd = bytes.NewReader(r.body)
		}
		req, err := http.NewRequestWithContext(ctx, r.method, r.url, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if r.contentType != "" {
			req.Header.Set("Content-Type", r.contentType)
		}
		for k, v := range r.headers {
			req.Header.Add(k, v)
		}
		resp, err := s.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errProviderUnavailable
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if err != nil {
			return nil, errProviderUnavailable
		}
		if resp.StatusCode == http.StatusUnauthorized {
			if refreshed {
				return nil, errReconnect
			}
			if err := s.expireToken(ctx, a); err != nil {
				return nil, err
			}
			refreshed = true
			continue
		}
		if resp.StatusCode/100 != 2 {
			err := httpFailure(label, resp.StatusCode, body)
			var pe *providerError
			if waited < len(s.busyWaits) && errors.As(err, &pe) && pe.busy && (pe.limited || r.method == http.MethodGet) {
				wait, ok := retryAfter(resp.Header, s.busyWaits[waited])
				if ok {
					waited++
					t := time.NewTimer(wait)
					select {
					case <-ctx.Done():
						t.Stop()
						return nil, ctx.Err()
					case <-t.C:
					}
					continue
				}
			}
			return nil, err
		}
		if int64(len(body)) > limit {
			return nil, errTooLarge
		}
		return body, nil
	}
}

// maxBusyWait is the longest Retry-After a call waits out; a longer one is left to the
// next sync pass.
const maxBusyWait = 15 * time.Second

// retryAfter is the wait before asking again: the provider's Retry-After in seconds when
// it gives one (false past maxBusyWait), else def.
func retryAfter(h http.Header, def time.Duration) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def, true
	}
	d := time.Duration(n) * time.Second
	return d, d <= maxBusyWait
}

// callJSON sends in as JSON (when not nil) and decodes the answer into out (when not nil).
func (s *service) callJSON(ctx context.Context, a *accountRow, label, method, url string, in, out any, headers map[string]string) error {
	r := restRequest{method: method, url: url, headers: headers}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		r.body, r.contentType = b, "application/json"
	}
	body, err := s.call(ctx, a, label, r)
	if err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return &providerError{msg: label + " returned an unreadable answer."}
		}
	}
	return nil
}

// httpFailure turns a provider HTTP error into a safe, classified error: the status and
// the provider's error code, never its message (it can echo addresses or content).
func httpFailure(label string, status int, body []byte) error {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	var detail struct {
		Code   any    `json:"code"`
		Status string `json:"status"`
		Errors []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	}
	_ = json.Unmarshal(e.Error, &detail)
	code := ""
	switch c := detail.Code.(type) {
	case string:
		code = c // Graph: "ErrorItemNotFound"
	}
	reasons := []string{code, detail.Status}
	for _, r := range detail.Errors {
		reasons = append(reasons, r.Reason)
	}
	for _, r := range detail.Details {
		reasons = append(reasons, r.Reason)
	}
	has := func(list ...string) bool {
		for _, r := range reasons {
			for _, want := range list {
				if r != "" && strings.EqualFold(r, want) {
					return true
				}
			}
		}
		return false
	}
	shown := ""
	for _, r := range reasons {
		if r != "" {
			shown = safeCode(r)
			break
		}
	}
	msg := fmt.Sprintf("%s refused the request (%d", label, status)
	if shown != "" {
		msg += " " + shown
	}
	msg += ")."
	switch {
	case status == http.StatusTooManyRequests || has("rateLimitExceeded", "userRateLimitExceeded", "ApplicationThrottled"):
		return &providerError{msg: label + " is busy. We will try again.", status: status, busy: true, limited: true}
	case status >= 500:
		return &providerError{msg: label + " is busy. We will try again.", status: status, busy: true}
	case status == http.StatusNotFound || has("ErrorItemNotFound"):
		return &providerError{msg: msg, status: status, permanent: true, notFound: true}
	case status == http.StatusForbidden && has("insufficientPermissions", "ACCESS_TOKEN_SCOPE_INSUFFICIENT", "ErrorAccessDenied", "authError"):
		return errReconnect
	case has("ErrorSendAsDenied"):
		// Exchange sends from an alias only where the organization allows it (SendFromAlias).
		return &providerError{msg: label + " refused to send from this address. Its organization has to allow sending from aliases, " +
			"or the address is not one of the account's (ErrorSendAsDenied).", status: status, permanent: true}
	}
	return &providerError{msg: msg, status: status, permanent: status >= 400 && status < 500}
}

// isNotFound reports a provider 404.
func isNotFound(err error) bool {
	var pe *providerError
	return errors.As(err, &pe) && pe.notFound
}

// isPermanent reports a failure that retrying will not fix.
func isPermanent(err error) bool {
	var pe *providerError
	var ie *imapError
	switch {
	case errors.Is(err, errReconnect):
		return true
	case errors.As(err, &pe):
		return pe.permanent
	case errors.As(err, &ie):
		return ie.permanent || ie.auth
	}
	return false
}
