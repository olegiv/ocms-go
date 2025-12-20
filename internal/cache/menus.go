package cache

import (
	"context"
	"sync"
	"time"

	"ocms-go/internal/store"
)

// MenuWithItems represents a menu with its items for caching.
type MenuWithItems struct {
	Menu  store.Menu
	Items []store.MenuItem
}

// MenuCache provides cached access to menus.
// Menus are cached by slug for efficient frontend lookups.
type MenuCache struct {
	cache   *Cache // Use existing Cache for backward compatibility
	queries *store.Queries
	mu      sync.RWMutex
	menus   map[string]*MenuWithItems // slug -> menu with items
	loaded  bool
}

// NewMenuCache creates a new menu cache.
// TTL defaults to 1 hour but cache is invalidated on any menu change.
func NewMenuCache(queries *store.Queries) *MenuCache {
	return &MenuCache{
		cache:   New(time.Hour),
		queries: queries,
		menus:   make(map[string]*MenuWithItems),
	}
}

// Get retrieves a menu by slug.
// Returns the menu with its items if found.
func (c *MenuCache) Get(ctx context.Context, slug string) (*MenuWithItems, error) {
	c.mu.RLock()
	if c.loaded {
		if menu, ok := c.menus[slug]; ok {
			c.mu.RUnlock()
			c.cache.hits.Add(1)
			return menu, nil
		}
		c.mu.RUnlock()
		c.cache.misses.Add(1)
		return nil, nil
	}
	c.mu.RUnlock()

	// Need to load
	if err := c.loadAll(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if menu, ok := c.menus[slug]; ok {
		c.cache.hits.Add(1)
		return menu, nil
	}
	c.cache.misses.Add(1)
	return nil, nil
}

// GetByID retrieves a menu by ID.
func (c *MenuCache) GetByID(ctx context.Context, id int64) (*MenuWithItems, error) {
	c.mu.RLock()
	if !c.loaded {
		c.mu.RUnlock()
		if err := c.loadAll(ctx); err != nil {
			return nil, err
		}
		c.mu.RLock()
	}
	defer c.mu.RUnlock()

	for _, menu := range c.menus {
		if menu.Menu.ID == id {
			c.cache.hits.Add(1)
			return menu, nil
		}
	}
	c.cache.misses.Add(1)
	return nil, nil
}

// All returns all cached menus.
func (c *MenuCache) All(ctx context.Context) ([]*MenuWithItems, error) {
	c.mu.RLock()
	if !c.loaded {
		c.mu.RUnlock()
		if err := c.loadAll(ctx); err != nil {
			return nil, err
		}
		c.mu.RLock()
	}
	defer c.mu.RUnlock()

	result := make([]*MenuWithItems, 0, len(c.menus))
	for _, menu := range c.menus {
		result = append(result, menu)
	}
	return result, nil
}

// loadAll loads all menus from database.
func (c *MenuCache) loadAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.loaded {
		return nil
	}

	menus, err := c.queries.ListMenus(ctx)
	if err != nil {
		return err
	}

	c.menus = make(map[string]*MenuWithItems, len(menus))

	for _, menu := range menus {
		items, err := c.queries.ListMenuItems(ctx, menu.ID)
		if err != nil {
			return err
		}

		c.menus[menu.Slug] = &MenuWithItems{
			Menu:  menu,
			Items: items,
		}
	}

	c.loaded = true
	c.cache.sets.Add(1)

	return nil
}

// Invalidate clears the cache, forcing a reload on next access.
func (c *MenuCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded = false
	c.menus = make(map[string]*MenuWithItems)
}

// InvalidateBySlug invalidates a specific menu by slug.
// For simplicity, this invalidates the entire cache.
func (c *MenuCache) InvalidateBySlug(_ string) {
	c.Invalidate()
}

// Stats returns cache statistics.
func (c *MenuCache) Stats() Stats {
	stats := c.cache.Stats()
	c.mu.RLock()
	stats.Items = len(c.menus)
	c.mu.RUnlock()
	return stats
}

// ResetStats resets the cache statistics.
func (c *MenuCache) ResetStats() {
	c.cache.ResetStats()
}

// Preload loads all menus into cache.
// Useful for warming up the cache on startup.
func (c *MenuCache) Preload(ctx context.Context) error {
	return c.loadAll(ctx)
}
