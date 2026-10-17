// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	apiv2 "github.com/olegiv/ocms-go/internal/api/v2"
)

// TestErrorCodeMatchesRESTEnvelope fails when Error.Code and the REST error
// envelope disagree for any kind. MCP reports Code() while REST reports the
// envelope's error.code; the two must never drift apart.
func TestErrorCodeMatchesRESTEnvelope(t *testing.T) {
	kinds := []apiv2.ErrorKind{
		apiv2.ErrValidation,
		apiv2.ErrNotFound,
		apiv2.ErrForbidden,
		apiv2.ErrConflict,
		apiv2.ErrUnauthorized,
		apiv2.ErrInternal,
	}
	for _, kind := range kinds {
		domainErr := apiv2.NewError(kind, "message")
		raw, err := json.Marshal(apiv2.ToHuma(context.Background(), domainErr))
		if err != nil {
			t.Fatalf("marshal REST error for kind %d: %v", kind, err)
		}
		var body apiv2.ErrorBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal REST error for kind %d: %v", kind, err)
		}
		if body.Error.Code != domainErr.Code() {
			t.Errorf("kind %d: REST envelope code %q != Error.Code() %q", kind, body.Error.Code, domainErr.Code())
		}
		if body.Error.Code == "" {
			t.Errorf("kind %d: empty error code", kind)
		}
	}
}

// TestNewInternalErrorKeepsCauseOutOfResponses fails when an internal error
// loses its cause or leaks it: errors.Is must reach the cause (MCP relies on
// it to recognise cancellation), while the REST envelope carries only the
// curated message.
func TestNewInternalErrorKeepsCauseOutOfResponses(t *testing.T) {
	cause := fmt.Errorf("querying pages: %w", context.Canceled)
	domainErr := apiv2.NewInternalError("Failed to list pages", cause)

	if !errors.Is(domainErr, context.Canceled) {
		t.Error("errors.Is must see the cause through NewInternalError")
	}
	if domainErr.Code() != "internal_error" || domainErr.Error() != "Failed to list pages" {
		t.Errorf("code %q message %q, want internal_error and the curated message", domainErr.Code(), domainErr.Error())
	}

	raw, err := json.Marshal(apiv2.ToHuma(context.Background(), domainErr))
	if err != nil {
		t.Fatalf("marshal REST error: %v", err)
	}
	if strings.Contains(string(raw), "querying pages") || strings.Contains(string(raw), "canceled") {
		t.Errorf("REST response leaks the cause: %s", raw)
	}
}

// TestToHumaLogsInternalErrors checks that a 500 leaves its cause in the log.
// The response says only "Internal server error", and ToHuma used to log
// nothing, so a failed database lookup behind a v2 operation left no trace.
func TestToHumaLogsInternalErrors(t *testing.T) {
	var buf strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for name, err := range map[string]error{
		"plain error":    fmt.Errorf("checking page route conflicts: %w", errors.New("database is locked")),
		"internal error": apiv2.NewInternalError("Error loading page", errors.New("disk I/O error")),
	} {
		buf.Reset()
		_ = apiv2.ToHuma(context.Background(), err)
		cause := "database is locked"
		if name == "internal error" {
			cause = "disk I/O error"
		}
		if !strings.Contains(buf.String(), cause) {
			t.Errorf("%s: log = %q, want the cause %q", name, buf.String(), cause)
		}
	}
	buf.Reset()
	_ = apiv2.ToHuma(context.Background(), apiv2.NewError(apiv2.ErrNotFound, "page not found"))
	if buf.Len() != 0 {
		t.Errorf("a not-found error was logged: %q", buf.String())
	}
	// The log names the request, so a 500 can be matched to its access-log
	// line by request ID.
	buf.Reset()
	rctx := chi.NewRouteContext()
	rctx.RouteMethod = http.MethodPatch
	rctx.RoutePatterns = []string{"/api/v2/pages/{id}"}
	reqCtx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	reqCtx = context.WithValue(reqCtx, chimw.RequestIDKey, "host/abc-000042")
	for _, err := range []error{
		errors.New("database is locked"),
		apiv2.NewInternalError("Error updating page", errors.New("database is locked")),
	} {
		buf.Reset()
		_ = apiv2.ToHuma(reqCtx, err)
		for _, want := range []string{"request_id=host/abc-000042", "method=PATCH", "route=/api/v2/pages/{id}"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("log = %q, want %s", buf.String(), want)
			}
		}
	}
	// A client that disconnects is not an error in the event log.
	buf.Reset()
	_ = apiv2.ToHuma(context.Background(), apiv2.NewInternalError("Error loading page", context.Canceled))
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("a cancelled request was logged as %q, want a warning", buf.String())
	}
}
