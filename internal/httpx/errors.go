// Package httpx is the HTTP layer: huma setup, errors, middleware and helpers.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/captcha"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/plan"
)

// Error is the RFC 9457 problem body every route returns. Code is a stable machine string.
type Error struct {
	Status    int                 `json:"status"`
	Title     string              `json:"title"`
	Detail    string              `json:"detail,omitempty"`
	Code      string              `json:"code,omitempty"`
	RequestID string              `json:"request_id,omitempty"`
	Errors    []*huma.ErrorDetail `json:"errors,omitempty"`
}

func (e *Error) Error() string  { return e.Detail }
func (e *Error) GetStatus() int { return e.Status }
func (e *Error) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

// E builds an error with a stable code.
func E(status int, code, detail string) *Error {
	return &Error{Status: status, Title: http.StatusText(status), Code: code, Detail: detail}
}

// WriteError answers a request with e from outside a huma handler (middleware).
func WriteError(w http.ResponseWriter, r *http.Request, e *Error) {
	out := *e
	out.RequestID = RequestIDFrom(r.Context())
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(out.Status)
	_ = json.NewEncoder(w).Encode(&out)
}

func NotFound(what string) *Error     { return E(404, "not_found", what+" not found") }
func BadRequest(detail string) *Error { return E(400, "bad_request", detail) }
func Conflict(detail string) *Error   { return E(409, "conflict", detail) }
func Forbidden() *Error               { return E(403, "forbidden", auth.ErrForbidden.Error()) }
func Validation(detail string) *Error {
	return E(422, "validation_failed", detail)
}

// internalMessage is the only text a caller sees for a server-class error.
const internalMessage = "Something went wrong on our side. Quote the request id if you contact support."

// Install sets huma's error constructor so every error has our shape, and server
// errors never leak their cause.
func Install(log *slog.Logger) {
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		e := &Error{Status: status, Title: http.StatusText(status), Detail: msg}
		if status >= 500 {
			for _, err := range errs {
				log.Error("internal error", "status", status, "err", err)
			}
			e.Detail, e.Code = internalMessage, "internal"
			return e
		}
		for _, err := range errs {
			if err == nil {
				continue
			}
			var d *huma.ErrorDetail
			if errors.As(err, &d) {
				e.Errors = append(e.Errors, d)
			} else {
				e.Errors = append(e.Errors, &huma.ErrorDetail{Message: err.Error()})
			}
		}
		switch status {
		case 422, 400:
			if e.Code == "" && len(e.Errors) > 0 {
				e.Code = "validation_failed"
			}
		case 404:
			e.Code = "not_found"
		}
		return e
	}
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		e := huma.NewError(status, msg, errs...)
		if ae, ok := e.(*Error); ok {
			ae.RequestID = ctx.Header("X-Request-Id")
			if ae.RequestID == "" {
				ae.RequestID = RequestIDFrom(ctx.Context())
			}
		}
		return e
	}
}

// Wrap converts a domain error into an HTTP error. Unknown errors become a logged 500.
// Handlers return Wrap(err) for anything that is not already an *Error.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	var he *Error
	if errors.As(err, &he) {
		return he
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return se
	}
	var dn *displayname.InvalidError
	var le *plan.LimitError
	switch {
	case errors.As(err, &le):
		return E(402, "plan_limit", "Your plan includes "+plural(le.Limit, le.Resource)+". Upgrade to add more.")
	case errors.Is(err, plan.ErrCollab):
		return E(402, "collab_required", "Collab is not on this workspace's plan.")
	case errors.Is(err, plan.ErrNoBalance):
		return E(402, "ai_balance_empty", "Your workspace's AI balance is too low for this. Add AI usage or upgrade.")
	case errors.Is(err, auth.ErrUnauthenticated):
		return E(401, "unauthenticated", err.Error())
	case errors.Is(err, auth.ErrForbidden):
		return E(403, "forbidden", err.Error())
	case errors.Is(err, auth.ErrNoWorkspace):
		return E(400, "workspace_required", err.Error())
	case errors.Is(err, auth.ErrWorkspaceSuspended):
		return E(403, "workspace_suspended", "This workspace is suspended. Contact support to restore it.")
	case errors.Is(err, auth.ErrMFARequired):
		return E(403, "mfa_required", err.Error())
	case errors.Is(err, captcha.ErrCaptcha):
		return E(400, "captcha_failed", "Complete the challenge and try again.")
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong), errors.Is(err, auth.ErrPasswordBreached):
		return E(422, "weak_password", err.Error())
	case errors.As(err, &dn):
		return E(422, displayname.ErrorCode, dn.Message)
	case db.IsNotFound(err):
		return NotFound("Resource")
	case errors.Is(err, context.Canceled):
		return E(499, "canceled", "request canceled")
	}
	return huma.Error500InternalServerError(internalMessage, err)
}

func plural(n int, noun string) string {
	if n == 1 {
		noun = strings.TrimSuffix(noun, "es")
		if noun == "mailbox" {
			return "1 mailbox"
		}
		return "1 " + strings.TrimSuffix(noun, "s")
	}
	return strconv.Itoa(n) + " " + noun
}
