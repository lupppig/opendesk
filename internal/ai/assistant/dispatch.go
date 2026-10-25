package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// caller runs the assistant's tools through the public API itself, in process, as the
// user: every tool gets exactly the role checks, validation, events, activities and
// audit entries the same request from the web app would. Writes carry the Assistant
// flag so they are attributed to ai on the user's behalf. Every request, reads included,
// carries ViaAssistant, which the person's own routes (Collab mail) refuse.
type caller struct {
	h http.Handler
	p *auth.Principal
}

func newCaller(h http.Handler, p *auth.Principal, write bool) *caller {
	cp := *p
	cp.Assistant = write
	cp.ViaAssistant = true
	return &caller{h: h, p: &cp}
}

// apiError is a non-2xx answer from the API, kept as its problem body.
type apiError struct {
	Status int
	Code   string
	Detail string
}

func (e *apiError) Error() string { return e.Detail }

// do sends one request and decodes a 2xx JSON answer into out (when non-nil).
func (c *caller) do(ctx context.Context, method, path string, query url.Values, body any, out any, idemKey string) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	// The run's context may carry the outer request's chi routing state, which the
	// router would reuse instead of routing this request afresh.
	ctx = context.WithValue(ctx, chi.RouteCtxKey, (*chi.Context)(nil))
	req := httptest.NewRequestWithContext(auth.WithPrincipal(ctx, c.p), method, target, rd)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" && method == http.MethodPost {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	req.RemoteAddr = "127.0.0.1:0"
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	if rec.Code < 200 || rec.Code > 299 {
		var p struct {
			Status int    `json:"status"`
			Code   string `json:"code"`
			Detail string `json:"detail"`
			Title  string `json:"title"`
			Errors []struct {
				Message  string `json:"message"`
				Location string `json:"location"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		detail := p.Detail
		if len(p.Errors) > 0 && p.Errors[0].Message != "" {
			detail = strings.TrimSpace(detail + " (" + p.Errors[0].Location + ": " + p.Errors[0].Message + ")")
		}
		if detail == "" {
			detail = p.Title
		}
		return &apiError{Status: rec.Code, Code: p.Code, Detail: detail}
	}
	if out != nil && rec.Body.Len() > 0 {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
}

func (c *caller) get(ctx context.Context, path string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, q, nil, out, "")
}

var ticketNumber = regexp.MustCompile(`^#?([1-9][0-9]{0,11})$`)

// ticketRef validates a ticket reference from the model ("#123", "123" or a uuid) and
// returns it as a safe path segment.
func ticketRef(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if id, err := uuid.Parse(raw); err == nil {
		return id.String(), nil
	}
	if m := ticketNumber.FindStringSubmatch(raw); m != nil {
		return url.PathEscape("#" + m[1]), nil
	}
	return "", httpx.Validation("ticket must be a ticket number like #123 or a ticket id")
}

func idRef(raw, what string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", httpx.Validation(what + " must be an id")
	}
	return id.String(), nil
}
