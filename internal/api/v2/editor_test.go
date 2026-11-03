// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/olegiv/ocms-go/internal/pagewrite"
)

// TestEditorErrorKinds checks the REST mapping of a failed role re-check: a
// lost role is a refusal and a storage fault an internal error, never the
// other way round (2026-11-03 review R2-2).
func TestEditorErrorKinds(t *testing.T) {
	conflict := NewError(ErrConflict, "busy")
	for _, tc := range []struct {
		name string
		err  error
		kind ErrorKind
	}{
		{"role", pagewrite.ErrEditorRequired, ErrForbidden},
		{"wrapped role", fmt.Errorf("delete tag: %w", pagewrite.ErrEditorRequired), ErrForbidden},
		{"deleted account", fmt.Errorf("%w: %w", pagewrite.ErrEditorRequired, sql.ErrNoRows), ErrUnauthorized},
		{"api error", conflict, ErrConflict},
		{"storage", errors.New("database is locked"), ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var apiErr *Error
			if !errors.As(EditorError(tc.err, "Failed"), &apiErr) || apiErr.Kind != tc.kind {
				t.Fatalf("EditorError(%v) = %v, want kind %v", tc.err, apiErr, tc.kind)
			}
		})
	}
	if EditorError(nil, "Failed") != nil {
		t.Fatal("EditorError(nil) is not nil")
	}
}
