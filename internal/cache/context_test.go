// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

func TestCacheContext_PageKey(t *testing.T) {
	tests := []struct {
		name     string
		ctx      CacheContext
		slug     string
		expected string
	}{
		{
			name:     "english anonymous",
			ctx:      CacheContext{LanguageCode: "en", Role: "anonymous"},
			slug:     "about-us",
			expected: "en:anonymous:about-us",
		},
		{
			name:     "russian admin",
			ctx:      CacheContext{LanguageCode: "ru", Role: "admin"},
			slug:     "about-us",
			expected: "ru:admin:about-us",
		},
		{
			name:     "empty language uses as-is",
			ctx:      CacheContext{LanguageCode: "", Role: "editor"},
			slug:     "page",
			expected: ":editor:page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctx.PageKey(tt.slug)
			if got != tt.expected {
				t.Errorf("PageKey(%q) = %q, want %q", tt.slug, got, tt.expected)
			}
		})
	}
}

func TestCacheContext_PageIDKey(t *testing.T) {
	ctx := CacheContext{LanguageCode: "en", Role: "admin"}

	tests := []struct {
		id       int64
		expected string
	}{
		{1, "en:admin:1"},
		{12345, "en:admin:12345"},
		{0, "en:admin:0"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := ctx.PageIDKey(tt.id)
			if got != tt.expected {
				t.Errorf("PageIDKey(%d) = %q, want %q", tt.id, got, tt.expected)
			}
		})
	}
}

func TestNewCacheContext(t *testing.T) {
	t.Run("with values", func(t *testing.T) {
		ctx := NewCacheContext("ru", "editor")
		if ctx.LanguageCode != "ru" {
			t.Errorf("LanguageCode = %q, want %q", ctx.LanguageCode, "ru")
		}
		if ctx.Role != "editor" {
			t.Errorf("Role = %q, want %q", ctx.Role, "editor")
		}
	})

	t.Run("empty language defaults to en", func(t *testing.T) {
		ctx := NewCacheContext("", "admin")
		if ctx.LanguageCode != "en" {
			t.Errorf("LanguageCode = %q, want %q", ctx.LanguageCode, "en")
		}
	})

	t.Run("empty role defaults to anonymous", func(t *testing.T) {
		ctx := NewCacheContext("en", "")
		if ctx.Role != model.RoleAnonymous {
			t.Errorf("Role = %q, want %q", ctx.Role, model.RoleAnonymous)
		}
	})

	t.Run("both empty use defaults", func(t *testing.T) {
		ctx := NewCacheContext("", "")
		if ctx.LanguageCode != "en" {
			t.Errorf("LanguageCode = %q, want %q", ctx.LanguageCode, "en")
		}
		if ctx.Role != model.RoleAnonymous {
			t.Errorf("Role = %q, want %q", ctx.Role, model.RoleAnonymous)
		}
	})
}

func TestRoleFromUser(t *testing.T) {
	t.Run("nil user returns anonymous", func(t *testing.T) {
		role := RoleFromUser(nil)
		if role != model.RoleAnonymous {
			t.Errorf("RoleFromUser(nil) = %q, want %q", role, model.RoleAnonymous)
		}
	})

	t.Run("user with role returns role", func(t *testing.T) {
		user := &store.User{Role: "admin"}
		role := RoleFromUser(user)
		if role != "admin" {
			t.Errorf("RoleFromUser() = %q, want %q", role, "admin")
		}
	})

	t.Run("user with editor role", func(t *testing.T) {
		user := &store.User{Role: "editor"}
		role := RoleFromUser(user)
		if role != "editor" {
			t.Errorf("RoleFromUser() = %q, want %q", role, "editor")
		}
	})
}

func TestDefaultContext(t *testing.T) {
	ctx := DefaultContext()
	if ctx.LanguageCode != "en" {
		t.Errorf("LanguageCode = %q, want %q", ctx.LanguageCode, "en")
	}
	if ctx.Role != model.RoleAnonymous {
		t.Errorf("Role = %q, want %q", ctx.Role, model.RoleAnonymous)
	}
}

func TestCacheContext_DifferentContextsProduceDifferentKeys(t *testing.T) {
	slug := "about-us"

	contexts := []CacheContext{
		{LanguageCode: "en", Role: "anonymous"},
		{LanguageCode: "en", Role: "admin"},
		{LanguageCode: "ru", Role: "anonymous"},
		{LanguageCode: "ru", Role: "admin"},
	}

	keys := make(map[string]bool)
	for _, ctx := range contexts {
		key := ctx.PageKey(slug)
		if keys[key] {
			t.Errorf("duplicate key: %q", key)
		}
		keys[key] = true
	}

	if len(keys) != len(contexts) {
		t.Errorf("expected %d unique keys, got %d", len(contexts), len(keys))
	}
}
