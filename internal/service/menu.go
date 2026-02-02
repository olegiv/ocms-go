// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package service provides business logic and service layer functionality.
package service

import (
	"context"
	"database/sql"
	"sort"

	"github.com/olegiv/ocms-go/internal/cache"
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
	Position int
	Children []MenuItem
}

// MenuService provides menu loading with tree building.
// It uses cache.MenuCache for data storage and statistics tracking.
type MenuService struct {
	db        *sql.DB
	queries   *store.Queries
	menuCache *cache.MenuCache
}

// NewMenuService creates a new MenuService.
// If menuCache is nil, a standalone service without caching is created.
func NewMenuService(db *sql.DB, menuCache *cache.MenuCache) *MenuService {
	return &MenuService{
		db:        db,
		queries:   store.New(db),
		menuCache: menuCache,
	}
}

// GetMenu fetches a menu by slug.
// Uses MenuCache for data storage if available, otherwise fetches directly from database.
func (s *MenuService) GetMenu(slug string) []MenuItem {
	ctx := context.Background()

	// Try to get from cache first
	if s.menuCache != nil {
		cached, err := s.menuCache.Get(ctx, slug)
		if err == nil && cached != nil {
			return s.buildMenuTree(cached.Items)
		}
	}

	// Cache miss or no cache - fetch directly from database
	menu, err := s.queries.GetMenuBySlug(ctx, slug)
	if err != nil {
		return nil
	}

	items, err := s.queries.ListMenuItemsWithPage(ctx, menu.ID)
	if err != nil {
		return nil
	}

	return s.buildMenuTree(items)
}

// GetMenuForLanguage fetches a menu by slug for a specific language.
// It first tries to find a menu with the given slug and language,
// then falls back to the default language menu if not found.
// Note: Language-specific menus are fetched directly from database since
// MenuCache only caches by slug (not by slug+language).
func (s *MenuService) GetMenuForLanguage(slug string, langCode string) []MenuItem {
	ctx := context.Background()

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

	return s.buildMenuTree(items)
}

// GetMenuForLanguageCode fetches a menu by slug for a specific language code.
// This is the preferred method when you have the language code (e.g., from a page).
func (s *MenuService) GetMenuForLanguageCode(slug string, langCode string) []MenuItem {
	// Delegate to GetMenuForLanguage which already handles language code
	return s.GetMenuForLanguage(slug, langCode)
}

// InvalidateCache clears the menu cache.
// When a slug is provided, it invalidates that specific menu.
// When empty, it invalidates all menus.
func (s *MenuService) InvalidateCache(slug string) {
	if s.menuCache == nil {
		return
	}
	if slug == "" {
		s.menuCache.Invalidate()
	} else {
		s.menuCache.InvalidateBySlug(slug)
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
			Position: int(item.Position),
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

	// Sort children by position for deterministic ordering
	for _, item := range itemMap {
		sort.Slice(item.Children, func(i, j int) bool {
			return item.Children[i].Position < item.Children[j].Position
		})
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

	// Sort roots by position for deterministic ordering
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Position < roots[j].Position
	})

	return roots
}

