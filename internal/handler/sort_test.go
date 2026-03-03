// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http/httptest"
	"testing"
)

func TestParseSortParams(t *testing.T) {
	allowed := map[string]SortConfig{
		"name":       {DefaultDir: sortDirAsc},
		"created_at": {DefaultDir: sortDirDesc},
	}

	tests := []struct {
		name         string
		query        string
		defaultField string
		defaultDir   string
		wantField    string
		wantDir      string
	}{
		{
			name:         "uses defaults when query is missing",
			query:        "",
			defaultField: "created_at",
			defaultDir:   sortDirDesc,
			wantField:    "created_at",
			wantDir:      sortDirDesc,
		},
		{
			name:         "accepts valid field and dir",
			query:        "sort=name&dir=desc",
			defaultField: "created_at",
			defaultDir:   sortDirDesc,
			wantField:    "name",
			wantDir:      sortDirDesc,
		},
		{
			name:         "invalid dir falls back to field default",
			query:        "sort=name&dir=invalid",
			defaultField: "created_at",
			defaultDir:   sortDirDesc,
			wantField:    "name",
			wantDir:      sortDirAsc,
		},
		{
			name:         "unknown field falls back to defaults",
			query:        "sort=unknown&dir=asc",
			defaultField: "created_at",
			defaultDir:   sortDirDesc,
			wantField:    "created_at",
			wantDir:      sortDirDesc,
		},
		{
			name:         "missing dir uses requested field default",
			query:        "sort=name",
			defaultField: "created_at",
			defaultDir:   sortDirDesc,
			wantField:    "name",
			wantDir:      sortDirAsc,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/admin/test?"+tt.query, nil)
			gotField, gotDir := parseSortParams(req, tt.defaultField, tt.defaultDir, allowed)
			if gotField != tt.wantField {
				t.Fatalf("field = %q, want %q", gotField, tt.wantField)
			}
			if gotDir != tt.wantDir {
				t.Fatalf("dir = %q, want %q", gotDir, tt.wantDir)
			}
		})
	}
}

func TestParseSortParamsPagesDefaultFallback(t *testing.T) {
	allowed := map[string]SortConfig{
		"title":      {DefaultDir: sortDirAsc},
		"updated_at": {DefaultDir: sortDirDesc},
	}

	req := httptest.NewRequest("GET", "/admin/pages?sort=invalid&dir=asc", nil)
	gotField, gotDir := parseSortParams(req, "updated_at", sortDirDesc, allowed)

	if gotField != "updated_at" {
		t.Fatalf("field = %q, want %q", gotField, "updated_at")
	}
	if gotDir != sortDirDesc {
		t.Fatalf("dir = %q, want %q", gotDir, sortDirDesc)
	}
}
