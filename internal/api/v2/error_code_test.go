// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2_test

import (
	"encoding/json"
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
