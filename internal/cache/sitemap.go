package cache

import (
	"context"
	"sync"
	"time"

	"ocms-go/internal/seo"
	"ocms-go/internal/store"
)

// SitemapCache provides cached sitemap XML generation.
// The sitemap is regenerated when invalidated or when TTL expires.
type SitemapCache struct {
	cache   *SimpleCache
	queries *store.Queries
	mu      sync.RWMutex

	// Cached sitemap data
	xml      []byte
	cachedAt time.Time
	ttl      time.Duration
}

// NewSitemapCache creates a new sitemap cache.
// TTL defaults to 1 hour.
func NewSitemapCache(queries *store.Queries, ttl time.Duration) *SitemapCache {
	if ttl == 0 {
		ttl = time.Hour
	}
	return &SitemapCache{
		cache:   New(ttl),
		queries: queries,
		ttl:     ttl,
	}
}

// Get returns the cached sitemap XML, generating it if needed.
func (c *SitemapCache) Get(ctx context.Context, siteURL string) ([]byte, error) {
	c.mu.RLock()
	if c.xml != nil && time.Since(c.cachedAt) < c.ttl {
		xml := c.xml
		c.mu.RUnlock()
		c.cache.hits.Add(1)
		return xml, nil
	}
	c.mu.RUnlock()

	// Need to regenerate
	return c.regenerate(ctx, siteURL)
}

// regenerate generates the sitemap and caches it.
func (c *SitemapCache) regenerate(ctx context.Context, siteURL string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.xml != nil && time.Since(c.cachedAt) < c.ttl {
		c.cache.hits.Add(1)
		return c.xml, nil
	}

	c.cache.misses.Add(1)

	// Build sitemap
	builder := seo.NewSitemapBuilder(siteURL)
	builder.AddHomepage()

	// Add published pages (excluding noindex pages)
	pages, err := c.queries.ListPublishedPagesForSitemap(ctx)
	if err == nil {
		for _, p := range pages {
			builder.AddPage(seo.SitemapPage{
				Slug:      p.Slug,
				UpdatedAt: p.UpdatedAt,
			})
		}
	}

	// Add categories
	categories, err := c.queries.ListCategoriesForSitemap(ctx)
	if err == nil {
		for _, cat := range categories {
			builder.AddCategory(seo.SitemapCategory{
				Slug:      cat.Slug,
				UpdatedAt: cat.UpdatedAt,
			})
		}
	}

	// Add tags
	tags, err := c.queries.ListTagsForSitemap(ctx)
	if err == nil {
		for _, t := range tags {
			builder.AddTag(seo.SitemapTag{
				Slug:      t.Slug,
				UpdatedAt: t.UpdatedAt,
			})
		}
	}

	// Generate XML
	xml, err := builder.Build()
	if err != nil {
		return nil, err
	}

	// Cache it
	c.xml = xml
	c.cachedAt = time.Now()
	c.cache.sets.Add(1)

	return xml, nil
}

// Invalidate clears the cached sitemap, forcing regeneration on next request.
func (c *SitemapCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.xml = nil
	c.cachedAt = time.Time{}
}

// Stats returns cache statistics.
func (c *SitemapCache) Stats() Stats {
	stats := c.cache.Stats()
	c.mu.RLock()
	if c.xml != nil {
		stats.Items = 1
	}
	c.mu.RUnlock()
	return stats
}

// ResetStats resets the cache statistics.
func (c *SitemapCache) ResetStats() {
	c.cache.ResetStats()
}

// IsCached returns true if the sitemap is currently cached.
func (c *SitemapCache) IsCached() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.xml != nil && time.Since(c.cachedAt) < c.ttl
}

// CachedAt returns when the sitemap was last cached.
func (c *SitemapCache) CachedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cachedAt
}

// Size returns the size of the cached sitemap in bytes.
func (c *SitemapCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.xml)
}
