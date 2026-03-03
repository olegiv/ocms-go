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

func TestNormalizePerPageOptions(t *testing.T) {
	options := normalizePerPageOptions([]int{10, 20, 20, 0, 50, 101}, 24)
	if !slices.Equal(options, []int{10, 20, 50, 24}) {
		t.Fatalf("normalizePerPageOptions() = %v, want %v", options, []int{10, 20, 50, 24})
	}
}

func TestPerPageSelector(t *testing.T) {
	t.Run("creates selector and appends current when missing", func(t *testing.T) {
		selector := perPageSelector(24, []int{10, 20, 50, 100})
		if selector == nil {
			t.Fatal("perPageSelector() returned nil")
		}
		if selector.Param != perPageQueryParam {
			t.Fatalf("selector.Param = %q, want %q", selector.Param, perPageQueryParam)
		}
		if selector.Current != 24 {
			t.Fatalf("selector.Current = %d, want 24", selector.Current)
		}
		if !slices.Equal(selector.Options, []int{10, 20, 50, 100, 24}) {
			t.Fatalf("selector.Options = %v, want %v", selector.Options, []int{10, 20, 50, 100, 24})
		}
	})

	t.Run("falls back to first option for invalid current", func(t *testing.T) {
		selector := perPageSelector(0, []int{10, 20, 50})
		if selector == nil {
			t.Fatal("perPageSelector() returned nil")
		}
		if selector.Current != 10 {
			t.Fatalf("selector.Current = %d, want 10", selector.Current)
		}
	})

	t.Run("returns nil for empty normalized options", func(t *testing.T) {
		selector := perPageSelector(0, []int{0, -1, 1000})
		if selector != nil {
			t.Fatalf("perPageSelector() = %#v, want nil", selector)
		}
	})
}
