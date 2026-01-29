// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package service provides business logic and service layer functionality.
package service

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// MenuItem represents a menu item for frontend rendering.
type MenuItem struct {
	ID       int64
	Title    string
	URL      string
	Target   string
	PageID   *int64
	PageSlug string
	CSSClass string
	IsActive bool
	Children []MenuItem
}

// cachedMenu holds a menu with its expiry time.
type cachedMenu struct {
	items  []MenuItem
	expiry time.Time
}

// MenuService provides menu loading with caching.
type MenuService struct {
	db       *sql.DB
	cache    sync.Map
	cacheTTL time.Duration
	queries  *store.Queries
}

// NewMenuService creates a new MenuService.
func NewMenuService(db *sql.DB) *MenuService {
	return &MenuService{
		db:       db,
		cacheTTL: 5 * time.Minute,
		queries:  store.New(db),
	}
}

// GetMenu fetches a menu by slug with caching.
// This is the basic method that returns any menu with the given slug.
func (s *MenuService) GetMenu(slug string) []MenuItem {
	// Check cache
	if cached, ok := s.cache.Load(slug); ok {
		cm := cached.(cachedMenu)
		if time.Now().Before(cm.expiry) {
			return cm.items
		}
		// Cache expired, delete it
		s.cache.Delete(slug)
	}

	// Fetch from database
	ctx := context.Background()
	menu, err := s.queries.GetMenuBySlug(ctx, slug)
	if err != nil {
		return nil
	}

	items, err := s.queries.ListMenuItemsWithPage(ctx, menu.ID)
	if err != nil {
		return nil
	}

	// Build tree structure
	menuItems := s.buildMenuTree(items)

	// Cache the result
	s.cache.Store(slug, cachedMenu{
		items:  menuItems,
		expiry: time.Now().Add(s.cacheTTL),
	})

	return menuItems
}

// GetMenuForLanguage fetches a menu by slug for a specific language.
// It first tries to find a menu with the given slug and language,
// then falls back to the default language menu if not found.
func (s *MenuService) GetMenuForLanguage(slug string, langCode string) []MenuItem {
	ctx := context.Background()

	// Build cache key that includes language
	cacheKey := fmt.Sprintf("%s:%s", slug, langCode)

	// Check cache
	if cached, ok := s.cache.Load(cacheKey); ok {
		cm := cached.(cachedMenu)
		if time.Now().Before(cm.expiry) {
			return cm.items
		}
		// Cache expired, delete it
		s.cache.Delete(cacheKey)
	}

	// Validate language code exists
	_, err := s.queries.GetLanguageByCode(ctx, langCode)
	if err != nil {
		// Language not found, try default
		defaultLang, err := s.queries.GetDefaultLanguage(ctx)
		if err != nil {
			// No default language, fall back to basic GetMenu
			return s.GetMenu(slug)
		}
		langCode = defaultLang.Code
	}

	// Try to get menu for specific language first
	menu, err := s.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug:         slug,
		LanguageCode: langCode,
	})
	if err != nil {
		// Menu not found for this language, try default language
		defaultLang, err := s.queries.GetDefaultLanguage(ctx)
		if err != nil {
			return nil
		}

		if defaultLang.Code != langCode {
			// Try with default language
			menu, err = s.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
				Slug:         slug,
				LanguageCode: defaultLang.Code,
			})
			if err != nil {
				// Still not found, try without language filter as last resort
				menu, err = s.queries.GetMenuBySlug(ctx, slug)
				if err != nil {
					return nil
				}
			}
		} else {
			// Already tried with default language, try basic lookup
			menu, err = s.queries.GetMenuBySlug(ctx, slug)
			if err != nil {
				return nil
			}
		}
	}

	items, err := s.queries.ListMenuItemsWithPage(ctx, menu.ID)
	if err != nil {
		return nil
	}

	// Build tree structure
	menuItems := s.buildMenuTree(items)

	// Cache the result with language key
	s.cache.Store(cacheKey, cachedMenu{
		items:  menuItems,
		expiry: time.Now().Add(s.cacheTTL),
	})

	return menuItems
}

// GetMenuForLanguageCode fetches a menu by slug for a specific language code.
// This is the preferred method when you have the language code (e.g., from a page).
func (s *MenuService) GetMenuForLanguageCode(slug string, langCode string) []MenuItem {
	// Delegate to GetMenuForLanguage which already handles language code
	return s.GetMenuForLanguage(slug, langCode)
}

// InvalidateCache clears the cache for a specific menu or all menus.
// When a slug is provided, it clears both the base slug and all
// language-specific variants (e.g., "main", "main:en", "main:ru").
func (s *MenuService) InvalidateCache(slug string) {
	if slug == "" {
		// Clear all
		s.cache.Range(func(key, _ any) bool {
			s.cache.Delete(key)
			return true
		})
	} else {
		// Clear slug and all language-specific variants (slug:langCode)
		prefix := slug + ":"
		s.cache.Range(func(key, _ any) bool {
			keyStr := key.(string)
			if keyStr == slug || len(keyStr) > len(prefix) && keyStr[:len(prefix)] == prefix {
				s.cache.Delete(key)
			}
			return true
		})
	}
}

// buildMenuTree converts flat list to nested tree structure.
func (s *MenuService) buildMenuTree(items []store.ListMenuItemsWithPageRow) []MenuItem {
	// Create a map of ID to MenuItem for quick lookup
	itemMap := make(map[int64]*MenuItem)
	parentMap := make(map[int64]int64) // child ID -> parent ID
	var rootIDs []int64

	// First pass: create all menu items and record parent relationships
	for _, item := range items {
		if !item.IsActive {
			continue
		}

		mi := MenuItem{
			ID:       item.ID,
			Title:    item.Title,
			Target:   "_self",
			IsActive: item.IsActive,
			Children: []MenuItem{},
		}

		// Determine URL
		if item.PageID.Valid && item.PageSlug.Valid {
			// Internal page link
			mi.PageID = &item.PageID.Int64
			mi.PageSlug = item.PageSlug.String
			mi.URL = "/" + item.PageSlug.String
		} else if item.Url.Valid && item.Url.String != "" {
			// External URL
			mi.URL = item.Url.String
		}

		if item.Target.Valid && item.Target.String != "" {
			mi.Target = item.Target.String
		}

		if item.CssClass.Valid {
			mi.CSSClass = item.CssClass.String
		}

		itemMap[item.ID] = &mi

		if item.ParentID.Valid {
			parentMap[item.ID] = item.ParentID.Int64
		} else {
			rootIDs = append(rootIDs, item.ID)
		}
	}

	// Second pass: build tree using pointers (children reference the map items)
	for childID, parentID := range parentMap {
		child := itemMap[childID]
		parent := itemMap[parentID]
		if child != nil && parent != nil {
			parent.Children = append(parent.Children, *child)
		}
	}

	// Third pass: recursively copy children to ensure deep nesting works
	// This is needed because when we appended children above, grandchildren weren't populated yet
	var copyWithChildren func(id int64) MenuItem
	copyWithChildren = func(id int64) MenuItem {
		item := itemMap[id]
		if item == nil {
			return MenuItem{}
		}
		result := *item
		result.Children = make([]MenuItem, 0, len(item.Children))
		for _, child := range item.Children {
			result.Children = append(result.Children, copyWithChildren(child.ID))
		}
		return result
	}

	// Build roots with fully populated children
	roots := make([]MenuItem, 0, len(rootIDs))
	for _, id := range rootIDs {
		roots = append(roots, copyWithChildren(id))
	}

	return roots
}

