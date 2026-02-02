// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// PageCache provides cached access to published pages.
// Pages are cached by context-aware keys: {lang}:{role}:{slug} or {lang}:{role}:{id}
type PageCache struct {
	cache   *SimpleCache
	queries *store.Queries
	mu      sync.RWMutex

	// Caches for different lookup patterns (context-aware keys)
	bySlug map[string]*store.Page // "en:anonymous:about-us" -> page
	byID   map[string]*store.Page // "en:anonymous:123" -> page

	// Reverse index: page ID -> list of cache keys (for invalidation)
	keysByPageID map[int64][]string
}

// NewPageCache creates a new page cache.
// TTL defaults to 1 hour but cache is invalidated on any page change.
func NewPageCache(queries *store.Queries) *PageCache {
	return &PageCache{
		cache:        New(time.Hour),
		queries:      queries,
		bySlug:       make(map[string]*store.Page),
		byID:         make(map[string]*store.Page),
		keysByPageID: make(map[int64][]string),
	}
}

// GetBySlug retrieves a published page by slug with context awareness.
// Returns the page if found in cache or database, nil if not found.
func (c *PageCache) GetBySlug(ctx context.Context, cacheCtx CacheContext, slug string) (*store.Page, error) {
	key := cacheCtx.PageKey(slug)

	c.mu.RLock()
	if page, ok := c.bySlug[key]; ok {
		c.mu.RUnlock()
		c.cache.hits.Add(1)
		return page, nil
	}
	c.mu.RUnlock()

	// Cache miss - fetch from database
	c.cache.misses.Add(1)

	page, err := c.queries.GetPublishedPageBySlug(ctx, slug)
	if err != nil {
		return nil, err // Return error (including sql.ErrNoRows) to caller
	}

	// Store in cache with context-aware key
	c.store(&page, cacheCtx)

	return &page, nil
}

// GetByID retrieves a published page by ID with context awareness.
// Returns the page if found in cache or database, nil if not found.
func (c *PageCache) GetByID(ctx context.Context, cacheCtx CacheContext, id int64) (*store.Page, error) {
	key := cacheCtx.PageIDKey(id)

	c.mu.RLock()
	if page, ok := c.byID[key]; ok {
		c.mu.RUnlock()
		c.cache.hits.Add(1)
		return page, nil
	}
	c.mu.RUnlock()

	// Cache miss - fetch from database
	c.cache.misses.Add(1)

	page, err := c.queries.GetPublishedPageByID(ctx, id)
	if err != nil {
		return nil, err // Return error (including sql.ErrNoRows) to caller
	}

	// Store in cache with context-aware key
	c.store(&page, cacheCtx)

	return &page, nil
}

// store adds a page to caches with context-aware keys.
func (c *PageCache) store(page *store.Page, cacheCtx CacheContext) {
	slugKey := cacheCtx.PageKey(page.Slug)
	idKey := cacheCtx.PageIDKey(page.ID)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.bySlug[slugKey] = page
	c.byID[idKey] = page

	// Track keys for this page ID (for invalidation)
	c.keysByPageID[page.ID] = append(c.keysByPageID[page.ID], slugKey, idKey)

	c.cache.sets.Add(1)
}

// InvalidatePage removes ALL cached variants of a page by ID.
// This clears all language/role variants at once.
func (c *PageCache) InvalidatePage(pageID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Get all keys for this page ID
	keys := c.keysByPageID[pageID]
	for _, key := range keys {
		// Determine if it's a slug key or ID key and delete appropriately
		if strings.Contains(key, ":") {
			// Could be either bySlug or byID - try both
			delete(c.bySlug, key)
			delete(c.byID, key)
		}
	}

	// Clear the reverse index
	delete(c.keysByPageID, pageID)
}

// InvalidateBySlug removes all cached variants of a page by slug pattern.
// This clears all language/role variants for pages with matching slugs.
func (c *PageCache) InvalidateBySlug(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find all keys that end with the slug
	var keysToDelete []string
	var pageID int64

	for key, page := range c.bySlug {
		if strings.HasSuffix(key, ":"+slug) {
			keysToDelete = append(keysToDelete, key)
			pageID = page.ID
		}
	}

	// Delete from bySlug
	for _, key := range keysToDelete {
		delete(c.bySlug, key)
	}

	// Also delete corresponding byID entries
	if pageID > 0 {
		idSuffix := fmt.Sprintf(":%d", pageID)
		for key := range c.byID {
			if strings.HasSuffix(key, idSuffix) {
				delete(c.byID, key)
			}
		}
		delete(c.keysByPageID, pageID)
	}
}

// Invalidate clears the entire page cache and resets statistics.
func (c *PageCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.bySlug = make(map[string]*store.Page)
	c.byID = make(map[string]*store.Page)
	c.keysByPageID = make(map[int64][]string)
	c.cache.ResetStats()
}

// Stats returns cache statistics.
func (c *PageCache) Stats() Stats {
	stats := c.cache.Stats()
	c.mu.RLock()
	// Count cache entries (each lang:role:slug combination is a separate entry)
	stats.Items = len(c.bySlug)
	c.mu.RUnlock()
	return stats
}

// ResetStats resets the cache statistics.
func (c *PageCache) ResetStats() {
	c.cache.ResetStats()
}

// Preload loads popular pages into cache for a specific context.
// This can be called on startup to warm the cache with frequently accessed pages.
func (c *PageCache) Preload(ctx context.Context, cacheCtx CacheContext, limit int) error {
	if limit <= 0 {
		limit = 20 // Default to top 20 pages
	}

	// Load recent published pages
	pages, err := c.queries.ListPublishedPages(ctx, store.ListPublishedPagesParams{
		Limit:  int64(limit),
		Offset: 0,
	})
	if err != nil {
		return fmt.Errorf("failed to preload pages: %w", err)
	}

	for i := range pages {
		c.store(&pages[i], cacheCtx)
	}

	return nil
}

// Count returns the number of unique cached pages (not cache entries).
func (c *PageCache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.keysByPageID)
}

// CacheEntryCount returns the total number of cache entries (all context variants).
func (c *PageCache) CacheEntryCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.bySlug) + len(c.byID)
}
