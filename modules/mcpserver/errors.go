// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	v2 "github.com/olegiv/ocms-go/internal/api/v2"
)

// Error codes that originate in the MCP layer. Domain errors keep the codes
// v2.Error.Code reports, so REST and MCP name a failure the same way.
const (
	codeInternal     = "internal_error"
	codeUnauthorized = "unauthorized"
	codeValidation   = "validation_error"
	codeForbidden    = "forbidden"
	// codeTimeout has no REST counterpart: REST timeouts surface as the
	// global middleware's 503, while a tool call reports them in-band.
	codeTimeout = "timeout"
)

// Messages for errors raised in the MCP layer.
const (
	msgInternal        = "Internal server error"
	msgUnauthenticated = "API key required"
	msgValidation      = "Validation failed"
	msgTimeout         = "The request was cancelled or took too long; retry, and narrow the query if it is large"
)

// toolError is the error a tool handler reports to the agent. The SDK puts
// Error() into the tool result's text content with isError set, so it renders
// the same JSON envelope REST v2 uses:
//
//	{"error":{"code":"...","message":"...","details":{"field":"msg"}}}
//
// An agent therefore gets machine-readable details it can act on (fix an
// argument, try another id) instead of a protocol failure.
type toolError struct {
	Code    string
	Message string
	Details map[string]string
}

// Error renders the REST error envelope.
func (e *toolError) Error() string {
	body := v2.ErrorBody{Error: v2.ErrorDetail{Code: e.Code, Message: e.Message, Details: e.Details}}
	raw, err := json.Marshal(body)
	if err != nil {
		// A string-only struct cannot fail to marshal; keep a valid fallback.
		return `{"error":{"code":"internal_error","message":"Internal server error"}}`
	}
	return string(raw)
}

// newValidationError reports invalid arguments for one field.
func newValidationError(field, message string) *toolError {
	return &toolError{Code: codeValidation, Message: msgValidation, Details: map[string]string{field: message}}
}

// internalToolError is the scrubbed error for failures whose details must
// not reach the agent; the cause is logged server-side instead.
func internalToolError() *toolError {
	return &toolError{Code: codeInternal, Message: msgInternal}
}

// timeoutToolError reports a call cut short by cancellation or the per-call
// time limit.
func timeoutToolError() *toolError {
	return &toolError{Code: codeTimeout, Message: msgTimeout}
}

// internalRPCError is the scrubbed JSON-RPC error for failures outside tool
// code (the SDK's own validation of tool output, or a panic in its request
// pipeline): their messages can quote server data, so they never reach the
// client verbatim.
func internalRPCError() *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
}

// rpcErrorCode names a JSON-RPC error code for the call log.
func rpcErrorCode(code int64) string {
	switch code {
	case jsonrpc.CodeParseError:
		return "parse_error"
	case jsonrpc.CodeInvalidRequest:
		return "invalid_request"
	case jsonrpc.CodeMethodNotFound:
		return "method_not_found"
	case jsonrpc.CodeInvalidParams:
		return "invalid_params"
	case jsonrpc.CodeInternalError:
		return codeInternal
	}
	return "jsonrpc_error"
}

// errorDetail renders an error for the server log, including the cause a v2
// domain error keeps out of its client-facing message.
func errorDetail(err error) string {
	var de *v2.Error
	if errors.As(err, &de) && de.Wrap != nil {
		return err.Error() + ": " + de.Wrap.Error()
	}
	return err.Error()
}

// toToolError converts a handler error into the error the agent sees and
// reports whether it is an internal failure (which the caller logs with the
// original error). Domain errors keep their curated message and details, as
// REST does; anything else is replaced by a generic message so file paths,
// SQL or driver diagnostics never leak into a model's context.
func toToolError(err error) (*toolError, bool) {
	var te *toolError
	if errors.As(err, &te) {
		return te, te.Code == codeInternal
	}
	var de *v2.Error
	if errors.As(err, &de) {
		return &toolError{Code: de.Code(), Message: de.Msg, Details: de.Fields}, de.Kind == v2.ErrInternal
	}
	return internalToolError(), true
}

// isCancellation reports whether err comes from the request being cancelled
// or timing out, which is logged as a warning rather than a server fault.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
