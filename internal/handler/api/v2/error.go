// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ErrorBody is the shape of the JSON body returned for every error response.
// It matches the v1 envelope so clients that parse oCMS errors keep working.
//
//	{"error": {"code": "...", "message": "...", "details": {"field": "msg"}}}
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail holds the inner error payload.
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// statusError is the huma.StatusError implementation that serializes as ErrorBody.
type statusError struct {
	status int
	body   ErrorBody
}

// Error implements the error interface.
func (e *statusError) Error() string { return e.body.Error.Message }

// GetStatus returns the HTTP status code.
func (e *statusError) GetStatus() int { return e.status }

// ErrorModel returns the body for huma to marshal.
// huma v2 uses this via the encoding/json path on the statusError value itself;
// implement MarshalJSON so the outer error shape wraps the body.
func (e *statusError) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.body)
}

// newStatusError is wired into huma.NewError. Maps huma-provided error messages
// plus any inner errors (typically validation errors carrying a field path) into
// the ErrorBody shape.
func newStatusError(status int, msg string, errs ...error) huma.StatusError {
	code := codeForStatus(status)
	details := map[string]string{}
	// huma attaches field-level validation errors as ErrorDetailer implementations.
	// Collect their location -> message mapping into our Details map.
	for _, err := range errs {
		var detailer huma.ErrorDetailer
		if errors.As(err, &detailer) {
			d := detailer.ErrorDetail()
			if d != nil && d.Location != "" {
				details[d.Location] = d.Message
			}
			continue
		}
		if err != nil {
			details[""] = err.Error()
		}
	}
	body := ErrorBody{Error: ErrorDetail{Code: code, Message: msg}}
	if len(details) > 0 {
		body.Error.Details = details
	}
	return &statusError{status: status, body: body}
}

// codeForStatus returns the string error code for a given HTTP status. Matches
// the codes the v1 surface used so clients see the same "code" values.
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
