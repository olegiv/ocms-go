// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

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
		raw, err := json.Marshal(apiv2.ToHuma(domainErr))
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

	raw, err := json.Marshal(apiv2.ToHuma(domainErr))
	if err != nil {
		t.Fatalf("marshal REST error: %v", err)
	}
	if strings.Contains(string(raw), "querying pages") || strings.Contains(string(raw), "canceled") {
		t.Errorf("REST response leaks the cause: %s", raw)
	}
}
