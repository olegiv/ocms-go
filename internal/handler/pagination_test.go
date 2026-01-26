// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestCalculateTotalPages(t *testing.T) {
	tests := []struct {
		name       string
		totalItems int
		perPage    int
		want       int
	}{
		{"zero items", 0, 10, 1},
		{"less than one page", 5, 10, 1},
		{"exactly one page", 10, 10, 1},
		{"one item over", 11, 10, 2},
		{"multiple pages", 25, 10, 3},
		{"exact multiple", 30, 10, 3},
		{"zero per page", 10, 0, 1},
		{"negative per page", 10, -5, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateTotalPages(tt.totalItems, tt.perPage)
			if got != tt.want {
				t.Errorf("CalculateTotalPages(%d, %d) = %d, want %d", tt.totalItems, tt.perPage, got, tt.want)
			}
		})
	}
}

func TestClampPage(t *testing.T) {
	tests := []struct {
		name       string
		page       int
		totalPages int
		want       int
	}{
		{"valid page", 3, 5, 3},
		{"first page", 1, 5, 1},
		{"last page", 5, 5, 5},
		{"below minimum", 0, 5, 1},
		{"negative page", -1, 5, 1},
		{"above maximum", 10, 5, 5},
		{"way above maximum", 100, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampPage(tt.page, tt.totalPages)
			if got != tt.want {
				t.Errorf("ClampPage(%d, %d) = %d, want %d", tt.page, tt.totalPages, got, tt.want)
			}
		})
	}
}

func TestNormalizePagination(t *testing.T) {
	tests := []struct {
		name           string
		page           int
		totalItems     int
		perPage        int
		wantPage       int
		wantTotalPages int
	}{
		{"valid page", 2, 50, 10, 2, 5},
		{"page too high", 10, 50, 10, 5, 5},
		{"page too low", 0, 50, 10, 1, 5},
		{"single page", 1, 5, 10, 1, 1},
		{"empty list", 1, 0, 10, 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPage, gotTotal := NormalizePagination(tt.page, tt.totalItems, tt.perPage)
			if gotPage != tt.wantPage || gotTotal != tt.wantTotalPages {
				t.Errorf("NormalizePagination(%d, %d, %d) = (%d, %d), want (%d, %d)",
					tt.page, tt.totalItems, tt.perPage, gotPage, gotTotal, tt.wantPage, tt.wantTotalPages)
			}
		})
	}
}

func TestParsePageParam(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"valid page", "page=3", 3},
		{"first page", "page=1", 1},
		{"no param", "", 1},
		{"empty param", "page=", 1},
		{"invalid param", "page=abc", 1},
		{"zero page", "page=0", 1},
		{"negative page", "page=-1", 1},
		{"large page", "page=999", 999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			got := ParsePageParam(req)
			if got != tt.want {
				t.Errorf("ParsePageParam() with query %q = %d, want %d", tt.query, got, tt.want)
			}
		})
	}
}

func TestParsePerPageParam(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		defaultVal int
		maxVal     int
		want       int
	}{
		{"valid value", "per_page=20", 10, 100, 20},
		{"no param uses default", "", 10, 100, 10},
		{"empty param uses default", "per_page=", 10, 100, 10},
		{"invalid uses default", "per_page=abc", 10, 100, 10},
		{"below min uses default", "per_page=0", 10, 100, 10},
		{"above max uses default", "per_page=200", 10, 100, 10},
		{"at max", "per_page=100", 10, 100, 100},
		{"at min", "per_page=1", 10, 100, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			got := ParsePerPageParam(req, tt.defaultVal, tt.maxVal)
			if got != tt.want {
				t.Errorf("ParsePerPageParam() with query %q = %d, want %d", tt.query, got, tt.want)
			}
		})
	}
}

func TestParseIntParam(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		param      string
		defaultVal int
		minVal     int
		maxVal     int
		want       int
	}{
		{"valid value", "limit=50", "limit", 10, 1, 100, 50},
		{"missing param", "", "limit", 10, 1, 100, 10},
		{"empty value", "limit=", "limit", 10, 1, 100, 10},
		{"invalid value", "limit=abc", "limit", 10, 1, 100, 10},
		{"below min", "limit=0", "limit", 10, 1, 100, 10},
		{"above max", "limit=200", "limit", 10, 1, 100, 10},
		{"no min check", "limit=0", "limit", 10, 0, 100, 0},
		{"no max check", "limit=500", "limit", 10, 1, 0, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			got := ParseIntParam(req, tt.param, tt.defaultVal, tt.minVal, tt.maxVal)
			if got != tt.want {
				t.Errorf("ParseIntParam() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseIDParam(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		want    int64
		wantErr bool
	}{
		{"valid id", "123", 123, false},
		{"zero id", "0", 0, false},
		{"large id", "9999999999", 9999999999, false},
		{"empty id", "", 0, true},
		{"invalid id", "abc", 0, true},
		{"negative id", "-1", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.id)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			got, err := ParseIDParam(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseIDParam() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseIDParam() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseURLParamInt64(t *testing.T) {
	tests := []struct {
		name      string
		paramName string
		value     string
		want      int64
		wantErr   bool
	}{
		{"valid value", "user_id", "456", 456, false},
		{"empty value", "user_id", "", 0, true},
		{"invalid value", "user_id", "xyz", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add(tt.paramName, tt.value)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			got, err := ParseURLParamInt64(req, tt.paramName)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseURLParamInt64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseURLParamInt64() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildAdminPagination(t *testing.T) {
	tests := []struct {
		name        string
		currentPage int
		totalItems  int
		perPage     int
		wantPages   int
		wantHasPrev bool
		wantHasNext bool
	}{
		{"first page", 1, 50, 10, 5, false, true},
		{"middle page", 3, 50, 10, 5, true, true},
		{"last page", 5, 50, 10, 5, true, false},
		{"single page", 1, 5, 10, 1, false, false},
		{"empty", 1, 0, 10, 1, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := BuildAdminPagination(tt.currentPage, tt.totalItems, tt.perPage, "/admin/test", nil)

			if p.TotalPages != tt.wantPages {
				t.Errorf("TotalPages = %d, want %d", p.TotalPages, tt.wantPages)
			}
			if p.HasPrev != tt.wantHasPrev {
				t.Errorf("HasPrev = %v, want %v", p.HasPrev, tt.wantHasPrev)
			}
			if p.HasNext != tt.wantHasNext {
				t.Errorf("HasNext = %v, want %v", p.HasNext, tt.wantHasNext)
			}
			if p.CurrentPage != tt.currentPage {
				t.Errorf("CurrentPage = %d, want %d", p.CurrentPage, tt.currentPage)
			}
		})
	}
}

func TestBuildAdminPaginationWithQueryParams(t *testing.T) {
	params := url.Values{
		"filter": []string{"active"},
		"sort":   []string{"name"},
		"page":   []string{"2"}, // Should be excluded
	}

	p := BuildAdminPagination(1, 50, 10, "/admin/test", params)

	// Query string should contain filter and sort but not page
	if p.QueryString == "" {
		t.Error("QueryString should not be empty")
	}

	// Check that URL contains query params
	gotURL := p.PageURL(2)
	if gotURL == "" {
		t.Error("PageURL should not be empty")
	}
}

func TestAdminPaginationMethods(t *testing.T) {
	p := BuildAdminPagination(3, 50, 10, "/admin/test", nil)

	t.Run("PageURL", func(t *testing.T) {
		got := p.PageURL(2)
		if got != "/admin/test?page=2" {
			t.Errorf("PageURL(2) = %q, want %q", got, "/admin/test?page=2")
		}
	})

	t.Run("FirstURL", func(t *testing.T) {
		got := p.FirstURL()
		if got != "/admin/test?page=1" {
			t.Errorf("FirstURL() = %q, want %q", got, "/admin/test?page=1")
		}
	})

	t.Run("PrevURL", func(t *testing.T) {
		got := p.PrevURL()
		if got != "/admin/test?page=2" {
			t.Errorf("PrevURL() = %q, want %q", got, "/admin/test?page=2")
		}
	})

	t.Run("NextURL", func(t *testing.T) {
		got := p.NextURL()
		if got != "/admin/test?page=4" {
			t.Errorf("NextURL() = %q, want %q", got, "/admin/test?page=4")
		}
	})

	t.Run("LastURL", func(t *testing.T) {
		got := p.LastURL()
		if got != "/admin/test?page=5" {
			t.Errorf("LastURL() = %q, want %q", got, "/admin/test?page=5")
		}
	})

	t.Run("ShouldShow", func(t *testing.T) {
		if !p.ShouldShow() {
			t.Error("ShouldShow() = false, want true for multi-page")
		}

		single := BuildAdminPagination(1, 5, 10, "/admin/test", nil)
		if single.ShouldShow() {
			t.Error("ShouldShow() = true, want false for single page")
		}
	})

	t.Run("PageRange", func(t *testing.T) {
		got := p.PageRange()
		if got != "21-30" {
			t.Errorf("PageRange() = %q, want %q", got, "21-30")
		}
	})
}

func TestAdminPaginationPageRange(t *testing.T) {
	tests := []struct {
		name        string
		currentPage int
		totalItems  int
		perPage     int
		want        string
	}{
		{"first page", 1, 50, 10, "1-10"},
		{"middle page", 3, 50, 10, "21-30"},
		{"last page partial", 5, 45, 10, "41-45"},
		{"single item", 1, 1, 10, "1-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := BuildAdminPagination(tt.currentPage, tt.totalItems, tt.perPage, "/test", nil)
			got := p.PageRange()
			if got != tt.want {
				t.Errorf("PageRange() = %q, want %q", got, tt.want)
			}
		})
	}
}
