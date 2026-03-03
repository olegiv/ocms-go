// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestParseBulkActionIDs(t *testing.T) {
	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/admin/pages/bulk-delete", strings.NewReader(`{"ids":[1,2,3]}`))
		w := httptest.NewRecorder()

		ids, err := parseBulkActionIDs(w, req, 10)
		if err != nil {
			t.Fatalf("parseBulkActionIDs() error = %v", err)
		}
		if !slices.Equal(ids, []int64{1, 2, 3}) {
			t.Fatalf("ids = %v, want %v", ids, []int64{1, 2, 3})
		}
	})

	t.Run("duplicate normalization", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/admin/pages/bulk-delete", strings.NewReader(`{"ids":[2,2,1,2,3,1]}`))
		w := httptest.NewRecorder()

		ids, err := parseBulkActionIDs(w, req, 10)
		if err != nil {
			t.Fatalf("parseBulkActionIDs() error = %v", err)
		}
		if !slices.Equal(ids, []int64{2, 1, 3}) {
			t.Fatalf("ids = %v, want %v", ids, []int64{2, 1, 3})
		}
	})

	t.Run("empty ids rejection", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/admin/pages/bulk-delete", strings.NewReader(`{"ids":[]}`))
		w := httptest.NewRecorder()

		if _, err := parseBulkActionIDs(w, req, 10); err == nil {
			t.Fatal("expected error for empty ids")
		}
	})

	t.Run("malformed json rejection", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/admin/pages/bulk-delete", strings.NewReader(`{"ids":[1,2`))
		w := httptest.NewRecorder()

		if _, err := parseBulkActionIDs(w, req, 10); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})

	t.Run("max batch size enforcement", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/admin/pages/bulk-delete", strings.NewReader(`{"ids":[1,2,3,4]}`))
		w := httptest.NewRecorder()

		if _, err := parseBulkActionIDs(w, req, 3); err == nil {
			t.Fatal("expected max batch size error")
		}
	})
}
