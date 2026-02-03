// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"fmt"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// CacheContext holds context information for cache key generation.
// It captures language and role to create context-aware cache keys.
type CacheContext struct {
	LanguageCode string // "en", "ru", etc.
	Role         string // anonymous, public, editor, admin
}

// PageKey generates a cache key for a page by slug.
// Format: {lang}:{role}:{slug}
func (c CacheContext) PageKey(slug string) string {
	return fmt.Sprintf("%s:%s:%s", c.LanguageCode, c.Role, slug)
}

// PageIDKey generates a cache key for a page by ID.
// Format: {lang}:{role}:{id}
func (c CacheContext) PageIDKey(id int64) string {
	return fmt.Sprintf("%s:%s:%d", c.LanguageCode, c.Role, id)
}

// NewCacheContext creates a new CacheContext with the given language and role.
// If role is empty, defaults to "anonymous".
func NewCacheContext(langCode string, role string) CacheContext {
	if langCode == "" {
		langCode = "en" // Default language
	}
	if role == "" {
		role = model.RoleAnonymous
	}
	return CacheContext{
		LanguageCode: langCode,
		Role:         role,
	}
}

// RoleFromUser extracts the role from a store.User.
// Returns "anonymous" if user is nil.
func RoleFromUser(user *store.User) string {
	if user == nil {
		return model.RoleAnonymous
	}
	return user.Role
}

// DefaultContext returns a default cache context for anonymous English users.
func DefaultContext() CacheContext {
	return CacheContext{
		LanguageCode: "en",
		Role:         model.RoleAnonymous,
	}
}
