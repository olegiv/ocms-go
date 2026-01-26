// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// ConfigCache provides cached access to site configuration.
// It loads all config values once and serves them from memory,
// with automatic invalidation on updates.
type ConfigCache struct {
	cache   *SimpleCache
	queries *store.Queries
	mu      sync.RWMutex
	loaded  bool

	// Store all config as map for bulk access
	allConfig map[string]store.Config
}

// NewConfigCache creates a new config cache.
// TTL is set to 1 hour but cache is invalidated on any config change.
func NewConfigCache(queries *store.Queries) *ConfigCache {
	return &ConfigCache{
		cache:     New(time.Hour), // Long TTL, manually invalidated
		queries:   queries,
		allConfig: make(map[string]store.Config),
	}
}

// Get retrieves a config value by key.
// Returns empty string if not found.
func (c *ConfigCache) Get(ctx context.Context, key string) (string, error) {
	c.mu.RLock()
	if c.loaded {
		if cfg, ok := c.allConfig[key]; ok {
			c.mu.RUnlock()
			c.cache.hits.Add(1)
			return cfg.Value, nil
		}
		c.mu.RUnlock()
		c.cache.misses.Add(1)
		return "", nil
	}
	c.mu.RUnlock()

	// Need to load
	if err := c.loadAll(ctx); err != nil {
		return "", err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if cfg, ok := c.allConfig[key]; ok {
		c.cache.hits.Add(1)
		return cfg.Value, nil
	}
	c.cache.misses.Add(1)
	return "", nil
}

// GetConfig retrieves a full config entry by key.
func (c *ConfigCache) GetConfig(ctx context.Context, key string) (store.Config, bool, error) {
	c.mu.RLock()
	if c.loaded {
		cfg, ok := c.allConfig[key]
		c.mu.RUnlock()
		if ok {
			c.cache.hits.Add(1)
		} else {
			c.cache.misses.Add(1)
		}
		return cfg, ok, nil
	}
	c.mu.RUnlock()

	// Need to load
	if err := c.loadAll(ctx); err != nil {
		return store.Config{}, false, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	cfg, ok := c.allConfig[key]
	if ok {
		c.cache.hits.Add(1)
	} else {
		c.cache.misses.Add(1)
	}
	return cfg, ok, nil
}

// GetMultiple retrieves multiple config values by keys.
func (c *ConfigCache) GetMultiple(ctx context.Context, keys ...string) (map[string]string, error) {
	c.mu.RLock()
	if !c.loaded {
		c.mu.RUnlock()
		if err := c.loadAll(ctx); err != nil {
			return nil, err
		}
		c.mu.RLock()
	}
	defer c.mu.RUnlock()

	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if cfg, ok := c.allConfig[key]; ok {
			result[key] = cfg.Value
		}
	}
	return result, nil
}

// All returns all config values.
func (c *ConfigCache) All(ctx context.Context) (map[string]string, error) {
	c.mu.RLock()
	if !c.loaded {
		c.mu.RUnlock()
		if err := c.loadAll(ctx); err != nil {
			return nil, err
		}
		c.mu.RLock()
	}
	defer c.mu.RUnlock()

	result := make(map[string]string, len(c.allConfig))
	for key, cfg := range c.allConfig {
		result[key] = cfg.Value
	}
	return result, nil
}

// loadAll loads all config from database.
func (c *ConfigCache) loadAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.loaded {
		return nil
	}

	configs, err := c.queries.ListConfig(ctx)
	if err != nil {
		return err
	}

	c.allConfig = make(map[string]store.Config, len(configs))
	for _, cfg := range configs {
		c.allConfig[cfg.Key] = cfg
	}
	c.loaded = true
	c.cache.sets.Add(1)

	return nil
}

// Invalidate clears the cache, forcing a reload on next access.
func (c *ConfigCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded = false
	c.allConfig = make(map[string]store.Config)
}

// InvalidateKey marks a specific key as needing reload.
// For simplicity, this invalidates the entire cache.
func (c *ConfigCache) InvalidateKey(_ string) {
	c.Invalidate()
}

// Stats returns cache statistics.
func (c *ConfigCache) Stats() Stats {
	stats := c.cache.Stats()
	c.mu.RLock()
	stats.Items = len(c.allConfig)
	c.mu.RUnlock()
	return stats
}

// ResetStats resets the cache statistics.
func (c *ConfigCache) ResetStats() {
	c.cache.ResetStats()
}

// Preload loads all config into cache.
// Useful for warming up the cache on startup.
func (c *ConfigCache) Preload(ctx context.Context) error {
	return c.loadAll(ctx)
}
