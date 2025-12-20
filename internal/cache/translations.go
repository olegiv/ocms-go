package cache

import (
	"context"
	"sync"
	"time"

	"ocms-go/internal/store"
)

// TranslationEntry represents a single translation link.
type TranslationEntry struct {
	EntityType    string
	EntityID      int64
	LanguageID    int64
	LanguageCode  string
	TranslationID int64
}

// TranslationMap maps language code to translation ID for a single entity.
type TranslationMap map[string]int64

// TranslationCache provides cached access to translation mappings.
// Caches are organized by entity type for efficient bulk operations.
type TranslationCache struct {
	cache   *SimpleCache // Use existing SimpleCache for stats tracking
	queries *store.Queries
	mu      sync.RWMutex
	// translations[entityType][entityID] = map[langCode]translationID
	translations map[string]map[int64]TranslationMap
	ttl          time.Duration
	maxEntries   int
}

// NewTranslationCache creates a new translation cache.
func NewTranslationCache(queries *store.Queries) *TranslationCache {
	return &TranslationCache{
		cache:        New(30 * time.Minute),
		queries:      queries,
		translations: make(map[string]map[int64]TranslationMap),
		ttl:          30 * time.Minute,
		maxEntries:   10000, // Limit memory usage
	}
}

// Get retrieves translation map for a specific entity.
// Returns a map of language code -> translated entity ID.
func (c *TranslationCache) Get(ctx context.Context, entityType string, entityID int64) (TranslationMap, error) {
	c.mu.RLock()
	if byType, ok := c.translations[entityType]; ok {
		if tmap, ok := byType[entityID]; ok {
			c.mu.RUnlock()
			c.cache.hits.Add(1)
			// Return a copy to prevent modification
			result := make(TranslationMap, len(tmap))
			for k, v := range tmap {
				result[k] = v
			}
			return result, nil
		}
	}
	c.mu.RUnlock()
	c.cache.misses.Add(1)

	// Load from database
	return c.loadEntity(ctx, entityType, entityID)
}

// GetForLanguage retrieves the translation ID for a specific entity and language.
func (c *TranslationCache) GetForLanguage(ctx context.Context, entityType string, entityID int64, langCode string) (int64, bool, error) {
	tmap, err := c.Get(ctx, entityType, entityID)
	if err != nil {
		return 0, false, err
	}
	if tid, ok := tmap[langCode]; ok {
		return tid, true, nil
	}
	return 0, false, nil
}

// GetBatch retrieves translations for multiple entities of the same type.
// This is more efficient than calling Get for each entity individually.
func (c *TranslationCache) GetBatch(ctx context.Context, entityType string, entityIDs []int64) (map[int64]TranslationMap, error) {
	result := make(map[int64]TranslationMap, len(entityIDs))
	var toLoad []int64

	c.mu.RLock()
	byType := c.translations[entityType]
	for _, id := range entityIDs {
		if byType != nil {
			if tmap, ok := byType[id]; ok {
				result[id] = tmap
				c.cache.hits.Add(1)
				continue
			}
		}
		toLoad = append(toLoad, id)
		c.cache.misses.Add(1)
	}
	c.mu.RUnlock()

	// Load missing entries
	if len(toLoad) > 0 {
		loaded, err := c.loadBatch(ctx, entityType, toLoad)
		if err != nil {
			return nil, err
		}
		for id, tmap := range loaded {
			result[id] = tmap
		}
	}

	return result, nil
}

// loadEntity loads translations for a single entity from the database.
func (c *TranslationCache) loadEntity(ctx context.Context, entityType string, entityID int64) (TranslationMap, error) {
	translations, err := c.queries.GetRelatedTranslations(ctx, store.GetRelatedTranslationsParams{
		EntityType:    entityType,
		EntityID:      entityID,
		TranslationID: entityID,
	})
	if err != nil {
		return nil, err
	}

	tmap := make(TranslationMap)
	for _, t := range translations {
		langCode := t.LanguageCode
		// Map to the translation target
		if t.EntityID == entityID {
			tmap[langCode] = t.TranslationID
		} else {
			// This entity is a translation target, map back to source
			tmap[langCode] = t.EntityID
		}
	}

	// Cache the result
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ensureCapacity()

	if c.translations[entityType] == nil {
		c.translations[entityType] = make(map[int64]TranslationMap)
	}
	c.translations[entityType][entityID] = tmap
	c.cache.sets.Add(1)

	return tmap, nil
}

// loadBatch loads translations for multiple entities from the database.
func (c *TranslationCache) loadBatch(ctx context.Context, entityType string, entityIDs []int64) (map[int64]TranslationMap, error) {
	result := make(map[int64]TranslationMap)

	// Initialize empty maps for all requested IDs
	for _, id := range entityIDs {
		result[id] = make(TranslationMap)
	}

	// Load translations for each entity
	// Note: We could optimize this with a batch query if sqlc supported it
	for _, id := range entityIDs {
		translations, err := c.queries.GetRelatedTranslations(ctx, store.GetRelatedTranslationsParams{
			EntityType:    entityType,
			EntityID:      id,
			TranslationID: id,
		})
		if err != nil {
			return nil, err
		}

		for _, t := range translations {
			langCode := t.LanguageCode
			if t.EntityID == id {
				result[id][langCode] = t.TranslationID
			} else {
				result[id][langCode] = t.EntityID
			}
		}
	}

	// Cache all results
	c.mu.Lock()
	defer c.mu.Unlock()

	c.ensureCapacity()

	if c.translations[entityType] == nil {
		c.translations[entityType] = make(map[int64]TranslationMap)
	}
	for id, tmap := range result {
		c.translations[entityType][id] = tmap
		c.cache.sets.Add(1)
	}

	return result, nil
}

// ensureCapacity ensures the cache doesn't exceed maximum entries.
// Must be called with write lock held.
func (c *TranslationCache) ensureCapacity() {
	total := 0
	for _, byType := range c.translations {
		total += len(byType)
	}

	if total >= c.maxEntries {
		// Clear half the cache (simple eviction strategy)
		for entityType, byType := range c.translations {
			count := 0
			for id := range byType {
				if count > len(byType)/2 {
					break
				}
				delete(byType, id)
				count++
			}
			if len(byType) == 0 {
				delete(c.translations, entityType)
			}
		}
	}
}

// InvalidateEntity invalidates cache for a specific entity.
func (c *TranslationCache) InvalidateEntity(entityType string, entityID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if byType, ok := c.translations[entityType]; ok {
		delete(byType, entityID)
	}
}

// InvalidateType invalidates all cache entries for an entity type.
func (c *TranslationCache) InvalidateType(entityType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.translations, entityType)
}

// Invalidate clears the entire translation cache.
func (c *TranslationCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.translations = make(map[string]map[int64]TranslationMap)
}

// Stats returns cache statistics.
func (c *TranslationCache) Stats() Stats {
	stats := c.cache.Stats()
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, byType := range c.translations {
		stats.Items += len(byType)
	}
	return stats
}

// ResetStats resets the cache statistics.
func (c *TranslationCache) ResetStats() {
	c.cache.ResetStats()
}
