package cache

import (
	"context"
	"sync"
	"time"

	"ocms-go/internal/store"
)

// LanguageCache provides cached access to languages.
// Languages are cached for efficient lookups during request processing.
type LanguageCache struct {
	cache       *Cache // Use existing Cache for backward compatibility
	queries     *store.Queries
	mu          sync.RWMutex
	languages   []store.Language          // All languages
	active      []store.Language          // Active languages only
	byCode      map[string]store.Language // code -> language
	defaultLang *store.Language           // Default language
	loaded      bool
}

// NewLanguageCache creates a new language cache.
// TTL defaults to 1 hour but cache is invalidated on any language change.
func NewLanguageCache(queries *store.Queries) *LanguageCache {
	return &LanguageCache{
		cache:   New(time.Hour),
		queries: queries,
		byCode:  make(map[string]store.Language),
	}
}

// GetAll retrieves all languages.
func (c *LanguageCache) GetAll(ctx context.Context) ([]store.Language, error) {
	c.mu.RLock()
	if c.loaded {
		result := make([]store.Language, len(c.languages))
		copy(result, c.languages)
		c.mu.RUnlock()
		c.cache.hits.Add(1)
		return result, nil
	}
	c.mu.RUnlock()

	// Need to load
	if err := c.loadAll(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]store.Language, len(c.languages))
	copy(result, c.languages)
	c.cache.hits.Add(1)
	return result, nil
}

// GetActive retrieves only active languages.
func (c *LanguageCache) GetActive(ctx context.Context) ([]store.Language, error) {
	c.mu.RLock()
	if c.loaded {
		result := make([]store.Language, len(c.active))
		copy(result, c.active)
		c.mu.RUnlock()
		c.cache.hits.Add(1)
		return result, nil
	}
	c.mu.RUnlock()

	// Need to load
	if err := c.loadAll(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]store.Language, len(c.active))
	copy(result, c.active)
	c.cache.hits.Add(1)
	return result, nil
}

// GetByCode retrieves a language by its code.
func (c *LanguageCache) GetByCode(ctx context.Context, code string) (*store.Language, error) {
	c.mu.RLock()
	if c.loaded {
		if lang, ok := c.byCode[code]; ok {
			c.mu.RUnlock()
			c.cache.hits.Add(1)
			return &lang, nil
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
	if lang, ok := c.byCode[code]; ok {
		c.cache.hits.Add(1)
		return &lang, nil
	}
	c.cache.misses.Add(1)
	return nil, nil
}

// GetDefault retrieves the default language.
func (c *LanguageCache) GetDefault(ctx context.Context) (*store.Language, error) {
	c.mu.RLock()
	if c.loaded {
		if c.defaultLang != nil {
			lang := *c.defaultLang
			c.mu.RUnlock()
			c.cache.hits.Add(1)
			return &lang, nil
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
	if c.defaultLang != nil {
		lang := *c.defaultLang
		c.cache.hits.Add(1)
		return &lang, nil
	}
	c.cache.misses.Add(1)
	return nil, nil
}

// IsActiveCode checks if a language code is active.
func (c *LanguageCache) IsActiveCode(ctx context.Context, code string) (bool, error) {
	c.mu.RLock()
	if !c.loaded {
		c.mu.RUnlock()
		if err := c.loadAll(ctx); err != nil {
			return false, err
		}
		c.mu.RLock()
	}
	defer c.mu.RUnlock()

	if lang, ok := c.byCode[code]; ok {
		return lang.IsActive, nil
	}
	return false, nil
}

// loadAll loads all languages from database.
func (c *LanguageCache) loadAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.loaded {
		return nil
	}

	languages, err := c.queries.ListLanguages(ctx)
	if err != nil {
		return err
	}

	c.languages = languages
	c.byCode = make(map[string]store.Language, len(languages))
	c.active = make([]store.Language, 0)
	c.defaultLang = nil

	for _, lang := range languages {
		c.byCode[lang.Code] = lang
		if lang.IsActive {
			c.active = append(c.active, lang)
		}
		if lang.IsDefault {
			langCopy := lang
			c.defaultLang = &langCopy
		}
	}

	c.loaded = true
	c.cache.sets.Add(1)

	return nil
}

// Invalidate clears the cache, forcing a reload on next access.
func (c *LanguageCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded = false
	c.languages = nil
	c.active = nil
	c.byCode = make(map[string]store.Language)
	c.defaultLang = nil
}

// Stats returns cache statistics.
func (c *LanguageCache) Stats() Stats {
	stats := c.cache.Stats()
	c.mu.RLock()
	stats.Items = len(c.languages)
	c.mu.RUnlock()
	return stats
}

// ResetStats resets the cache statistics.
func (c *LanguageCache) ResetStats() {
	c.cache.ResetStats()
}

// Preload loads all languages into cache.
// Useful for warming up the cache on startup.
func (c *LanguageCache) Preload(ctx context.Context) error {
	return c.loadAll(ctx)
}
