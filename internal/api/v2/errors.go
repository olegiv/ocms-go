// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
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
	Kind   ErrorKind
	Fields map[string]string // per-field validation errors, surfaced as error.details
	Msg    string
	Wrap   error
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Msg }

// Unwrap exposes an underlying error for errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.Wrap }

// NewError constructs a domain error with the given kind and message.
func NewError(kind ErrorKind, msg string) *Error {
	return &Error{Kind: kind, Msg: msg}
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
	body   ErrorBody
}

// Error implements error.
func (e *statusError) Error() string { return e.body.Error.Message }

// GetStatus reports the HTTP status to huma.
func (e *statusError) GetStatus() int { return e.status }

// MarshalJSON emits the body (huma serializes the error value itself).
func (e *statusError) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.body)
}

// ToHuma translates any error into a huma.StatusError with the project's
// ErrorBody envelope. Domain errors preserve their kind/fields; anything else
// becomes a 500 with a generic message so internals don't leak.
func ToHuma(err error) error {
	if err == nil {
		return nil
	}
	var de *Error
	if errors.As(err, &de) {
		return domainToStatusError(de)
	}
	return &statusError{
		status: http.StatusInternalServerError,
		body:   ErrorBody{Error: ErrorDetail{Code: "internal_error", Message: "Internal server error"}},
	}
}

func domainToStatusError(e *Error) huma.StatusError {
	status, code := statusAndCodeForKind(e.Kind)
	body := ErrorBody{Error: ErrorDetail{Code: code, Message: e.Msg}}
	if len(e.Fields) > 0 {
		body.Error.Details = e.Fields
	}
	return &statusError{status: status, body: body}
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
	return &statusError{status: status, body: body}
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
