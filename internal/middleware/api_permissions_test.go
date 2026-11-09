// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"slices"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestMalformedAPIKeyPermissionsFailClosed(t *testing.T) {
	for _, raw := range []string{`["mcp:access",7]`, `["mcp:access",null]`, `["mcp:access",null,{}]`, `["mcp:access"] trailing`, `{"permission":"mcp:access"}`, `"mcp:access"`} {
		if got := ParseAPIKeyPermissions(&store.ApiKey{Permissions: raw}); len(got) != 0 {
			t.Errorf("malformed JSON granted partial permissions: %q", raw)
		}
	}
	want := []string{"mcp:access", "mcp:drafts"}
	if got := ParseAPIKeyPermissions(&store.ApiKey{Permissions: `["mcp:access","mcp:drafts"]`}); !slices.Equal(got, want) {
		t.Fatalf("valid permissions changed: %v", got)
	}
}

// TestParseAPIKeyPermissionsDecodesStoredArrays covers the cases the removed
// model.APIKey.GetPermissions tests did, against the one decoder that remains.
func TestParseAPIKeyPermissionsDecodesStoredArrays(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"empty string", "", nil},
		{"empty array", "[]", nil},
		{"single item", `["pages:read"]`, []string{"pages:read"}},
		{"multiple items", `["pages:read","pages:write","media:read"]`, []string{"pages:read", "pages:write", "media:read"}},
		{"partial decode", `["pages:read",1]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseAPIKeyPermissions(&store.ApiKey{Permissions: tc.raw}); !slices.Equal(got, tc.want) {
				t.Fatalf("ParseAPIKeyPermissions(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
	if got := ParseAPIKeyPermissions(nil); got != nil {
		t.Fatalf("ParseAPIKeyPermissions(nil) = %v", got)
	}
}
