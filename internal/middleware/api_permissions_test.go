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
