// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// ErrorKind classifies domain errors so services can express semantic meaning
// without depending on HTTP. Huma adapters translate ErrorKind into status
// codes; the on-wire "code" string stays stable across transports.
type ErrorKind int

// Error kinds in rough order of severity.
const (
	ErrValidation   ErrorKind = iota + 1 // 422
	ErrNotFound                          // 404
	ErrForbidden                         // 403
	ErrConflict                          // 409
	ErrUnauthorized                      // 401
	ErrInternal                          // 500
)

// Error is the domain error type returned from services. Callers (v2 huma
// operation handlers) translate it via ToHuma.
type Error struct {
	MachineCode       string // Optional stable domain-specific code.
	Kind              ErrorKind
	Fields            map[string]string // per-field validation errors, surfaced as error.details
	Msg               string
	Wrap              error
	retryAfterSeconds int64
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Msg }

// Unwrap exposes an underlying error for errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.Wrap }

// Code returns the stable machine-readable code ("validation_error",
// "not_found", …) that REST responses carry in error.code. Non-HTTP
// transports such as MCP report it too, so a failure has the same code
// whichever transport surfaced it.
func (e *Error) Code() string {
	if e.MachineCode != "" {
		return e.MachineCode
	}
	_, code := statusAndCodeForKind(e.Kind)
	return code
}

// NewError constructs a domain error with the given kind and message.
func NewError(kind ErrorKind, msg string) *Error {
	return &Error{Kind: kind, Msg: msg}
}

// NewRetryableWriteError reports a write or precondition check that can be
// attempted again with the same request ID after a short delay.
func NewRetryableWriteError(msg string, cause error) *Error {
	return &Error{Kind: ErrConflict, MachineCode: "write_retryable", Msg: msg, Wrap: cause, retryAfterSeconds: 1}
}

// RetryAfterSeconds returns the retry delay, or zero for a definitive error.
func (e *Error) RetryAfterSeconds() int64 { return e.retryAfterSeconds }

// NewInternalError constructs an internal error that keeps its cause. Only
// msg reaches clients (ToHuma and the MCP server render Msg alone); the cause
// stays available to logs and to errors.Is, which is how callers recognise a
// cancelled or timed-out request underneath a failed query.
func NewInternalError(msg string, cause error) *Error {
	return &Error{Kind: ErrInternal, Msg: msg, Wrap: cause}
}

// NewValidationError constructs a validation error carrying per-field messages.
func NewValidationError(fields map[string]string, msg string) *Error {
	return &Error{Kind: ErrValidation, Fields: fields, Msg: msg}
}

// ErrorBody is the JSON envelope emitted for every error response.
//
//	{"error": {"code": "...", "message": "...", "details": {"field": "msg"}}}
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the inner error payload.
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// statusError implements huma.StatusError and marshals as ErrorBody so the
// wire format is identical across domain errors and huma framework errors.
type statusError struct {
	status int
	ErrorBody
}

// Error implements error.
func (e *statusError) Error() string { return e.ErrorBody.Error.Message }

// GetStatus reports the HTTP status to huma.
func (e *statusError) GetStatus() int { return e.status }

// MarshalJSON emits the body (huma serializes the error value itself).
func (e *statusError) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.ErrorBody)
}

// ToHuma translates any error into a huma.StatusError with the project's
// ErrorBody envelope. Domain errors preserve their kind/fields; anything else
// becomes a 500 with a generic message so internals don't leak. Internal
// errors are logged here with their cause, because the response carries only
// a generic or curated message, never the cause; the log names the request
// (ctx's request ID, method and route) so it can be matched to the access
// log. A request the client cancelled is logged as a warning: the client went
// away, nothing failed. A timeout stays an error, because the deadline on this
// path is the server's own request timeout (a slow query or a locked
// database).
func ToHuma(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	level := slog.LevelError
	if errors.Is(err, context.Canceled) {
		level = slog.LevelWarn
	}
	var de *Error
	if errors.As(err, &de) {
		if de.Kind == ErrInternal {
			slog.Log(ctx, level, "v2 internal error", append(requestLogAttrs(ctx), "error", de.Msg, "cause", de.Wrap)...)
		}
		return domainToStatusError(de)
	}
	slog.Log(ctx, level, "v2 unexpected error", append(requestLogAttrs(ctx), "error", err)...)
	return &statusError{
		status:    http.StatusInternalServerError,
		ErrorBody: ErrorBody{Error: ErrorDetail{Code: "internal_error", Message: "Internal server error"}},
	}
}

// requestLogAttrs returns the request ID, method and route pattern that chi
// recorded in ctx, for logging an error against its request.
func requestLogAttrs(ctx context.Context) []any {
	attrs := []any{"request_id", chimw.GetReqID(ctx)}
	if rctx := chi.RouteContext(ctx); rctx != nil {
		attrs = append(attrs, "method", rctx.RouteMethod, "route", rctx.RoutePattern())
	}
	return attrs
}

func domainToStatusError(e *Error) huma.StatusError {
	status, _ := statusAndCodeForKind(e.Kind)
	code := e.Code()
	body := ErrorBody{Error: ErrorDetail{Code: code, Message: e.Msg}}
	if len(e.Fields) > 0 {
		body.Error.Details = e.Fields
	}
	return &statusError{status: status, ErrorBody: body}
}

func statusAndCodeForKind(kind ErrorKind) (int, string) {
	switch kind {
	case ErrValidation:
		return http.StatusUnprocessableEntity, "validation_error"
	case ErrNotFound:
		return http.StatusNotFound, "not_found"
	case ErrForbidden:
		return http.StatusForbidden, "forbidden"
	case ErrConflict:
		return http.StatusConflict, "conflict"
	case ErrUnauthorized:
		return http.StatusUnauthorized, "unauthorized"
	case ErrInternal:
		return http.StatusInternalServerError, "internal_error"
	}
	return http.StatusInternalServerError, "internal_error"
}

// newStatusError is wired into huma.NewError so huma-emitted errors (input
// parsing, validation failures it detects itself) render via the same
// envelope. Per-field errors attached to the framework error via the
// ErrorDetailer interface surface under error.details.
func newStatusError(status int, msg string, errs ...error) huma.StatusError {
	code := codeForStatus(status)
	body := ErrorBody{Error: ErrorDetail{Code: code, Message: msg}}
	details := map[string]string{}
	for _, err := range errs {
		var detailer huma.ErrorDetailer
		if errors.As(err, &detailer) {
			if d := detailer.ErrorDetail(); d != nil && d.Location != "" {
				details[d.Location] = d.Message
			}
			continue
		}
		if err != nil {
			details[""] = err.Error()
		}
	}
	if len(details) > 0 {
		body.Error.Details = details
	}
	return &statusError{status: status, ErrorBody: body}
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "validation_error"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusInternalServerError:
		return "internal_error"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	}
	return "error"
}
