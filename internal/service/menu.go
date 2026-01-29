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

	// Get the language by code
	lang, err := s.queries.GetLanguageByCode(ctx, langCode)
	if err != nil {
		// Language not found, try default
		lang, err = s.queries.GetDefaultLanguage(ctx)
		if err != nil {
			// No default language, fall back to basic GetMenu
			return s.GetMenu(slug)
		}
	}

	// Try to get menu for specific language first
	menu, err := s.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug:       slug,
		LanguageID: lang.ID,
	})
	if err != nil {
		// Menu not found for this language, try default language
		defaultLang, err := s.queries.GetDefaultLanguage(ctx)
		if err != nil {
			return nil
		}

		if defaultLang.ID != lang.ID {
			// Try with default language
			menu, err = s.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
				Slug:       slug,
				LanguageID: defaultLang.ID,
			})
			if err != nil {
				// Still not found, try without language filter as last resort
				basicMenu, err := s.queries.GetMenuBySlug(ctx, slug)
				if err != nil {
					return nil
				}
				menu = menuToRow(basicMenu)
			}
		} else {
			// Already tried with default language, try basic lookup
			basicMenu, err := s.queries.GetMenuBySlug(ctx, slug)
			if err != nil {
				return nil
			}
			menu = menuToRow(basicMenu)
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

// GetMenuForLanguageID fetches a menu by slug for a specific language ID.
// This is useful when you have the language ID already (e.g., from a page).
func (s *MenuService) GetMenuForLanguageID(slug string, langID int64) []MenuItem {
	ctx := context.Background()

	// Build cache key that includes language ID
	cacheKey := fmt.Sprintf("%s:id:%d", slug, langID)

	// Check cache
	if cached, ok := s.cache.Load(cacheKey); ok {
		cm := cached.(cachedMenu)
		if time.Now().Before(cm.expiry) {
			return cm.items
		}
		s.cache.Delete(cacheKey)
	}

	// Try to get menu for specific language first
	menu, err := s.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
		Slug:       slug,
		LanguageID: langID,
	})
	if err != nil {
		// Menu not found for this language, try default language
		defaultLang, err := s.queries.GetDefaultLanguage(ctx)
		if err != nil {
			return nil
		}

		if defaultLang.ID != langID {
			menu, err = s.queries.GetMenuBySlugAndLanguage(ctx, store.GetMenuBySlugAndLanguageParams{
				Slug:       slug,
				LanguageID: defaultLang.ID,
			})
			if err != nil {
				// Fall back to basic lookup
				basicMenu, err := s.queries.GetMenuBySlug(ctx, slug)
				if err != nil {
					return nil
				}
				menu = menuToRow(basicMenu)
			}
		} else {
			basicMenu, err := s.queries.GetMenuBySlug(ctx, slug)
			if err != nil {
				return nil
			}
			menu = menuToRow(basicMenu)
		}
	}

	items, err := s.queries.ListMenuItemsWithPage(ctx, menu.ID)
	if err != nil {
		return nil
	}

	menuItems := s.buildMenuTree(items)

	s.cache.Store(cacheKey, cachedMenu{
		items:  menuItems,
		expiry: time.Now().Add(s.cacheTTL),
	})

	return menuItems
}

// InvalidateCache clears the cache for a specific menu or all menus.
func (s *MenuService) InvalidateCache(slug string) {
	if slug == "" {
		// Clear all
		s.cache.Range(func(key, _ any) bool {
			s.cache.Delete(key)
			return true
		})
	} else {
		s.cache.Delete(slug)
	}
}

// buildMenuTree converts flat list to nested tree structure.
func (s *MenuService) buildMenuTree(items []store.ListMenuItemsWithPageRow) []MenuItem {
	// Create a map of ID to MenuItem for quick lookup
	itemMap := make(map[int64]*MenuItem)
	var roots []MenuItem

	// First pass: create all menu items
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
	}

	// Second pass: build tree structure
	for _, item := range items {
		if !item.IsActive {
			continue
		}

		mi := itemMap[item.ID]
		if mi == nil {
			continue
		}

		if item.ParentID.Valid {
			// Has parent - add as child
			parent := itemMap[item.ParentID.Int64]
			if parent != nil {
				parent.Children = append(parent.Children, *mi)
			}
		} else {
			// Root item
			roots = append(roots, *mi)
		}
	}

	// Update root items with proper children (since we copied values)
	for i := range roots {
		if children := itemMap[roots[i].ID]; children != nil {
			roots[i].Children = children.Children
		}
	}

	return roots
}

// menuToRow converts a basic Menu to GetMenuBySlugAndLanguageRow.
func menuToRow(m store.Menu) store.GetMenuBySlugAndLanguageRow {
	return store.GetMenuBySlugAndLanguageRow{
		ID:        m.ID,
		Name:      m.Name,
		Slug:      m.Slug,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
