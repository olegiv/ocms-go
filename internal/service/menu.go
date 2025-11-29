// Package service provides business logic and service layer functionality.
package service

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"ocms-go/internal/store"
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
