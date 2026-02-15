// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cache provides an in-memory caching layer for oCMS.
package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// SimpleCache is a thread-safe in-memory cache with TTL support.
type SimpleCache struct {
	data    sync.Map
	ttl     time.Duration
	stopCh  chan struct{}
	stopped atomic.Bool

	// Stats
	hits         atomic.Int64
	misses       atomic.Int64
	sets         atomic.Int64
	statsResetAt atomic.Pointer[time.Time]
}

// cacheEntry holds a cached value with its expiration time.
type cacheEntry struct {
	value     any
	expiresAt time.Time
}

// Stats holds cache statistics.
type Stats struct {
	Hits    int64      `json:"hits"`
	Misses  int64      `json:"misses"`
	Sets    int64      `json:"sets"`
	Items   int        `json:"items"`
	HitRate float64    `json:"hit_rate"`
	Size    int64      `json:"size_bytes,omitempty"` // Approximate size in bytes (used by distributed caches)
	ResetAt *time.Time `json:"reset_at,omitempty"`   // when stats were last reset (nil if never reset)
}

// New creates a new cache with the specified TTL.
func New(ttl time.Duration) *SimpleCache {
	return &SimpleCache{
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}
}

// Get retrieves a value from the cache.
// Returns the value and true if found and not expired, nil and false otherwise.
func (c *SimpleCache) Get(key string) (any, bool) {
	val, ok := c.data.Load(key)
	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	entry := val.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		// Entry expired, remove it
		c.data.Delete(key)
		c.misses.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	return entry.value, true
}

// Set stores a value in the cache with the default TTL.
func (c *SimpleCache) Set(key string, value any) {
	c.SetWithTTL(key, value, c.ttl)
}

// SetWithTTL stores a value in the cache with a custom TTL.
func (c *SimpleCache) SetWithTTL(key string, value any, ttl time.Duration) {
	entry := &cacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	c.data.Store(key, entry)
	c.sets.Add(1)
}

// Delete removes a key from the cache.
func (c *SimpleCache) Delete(key string) {
	c.data.Delete(key)
}

// DeleteByPrefix removes all keys starting with the given prefix.
func (c *SimpleCache) DeleteByPrefix(prefix string) {
	c.data.Range(func(key, _ any) bool {
		k := key.(string)
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			c.data.Delete(key)
		}
		return true
	})
}

// Clear removes all entries from the cache.
func (c *SimpleCache) Clear() {
	c.data.Range(func(key, _ any) bool {
		c.data.Delete(key)
		return true
	})
}

// StartCleanup starts a background goroutine that periodically removes expired entries.
func (c *SimpleCache) StartCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.removeExpired()
			case <-c.stopCh:
				return
			}
		}
	}()
}

// Stop stops the cleanup goroutine.
func (c *SimpleCache) Stop() {
	if c.stopped.CompareAndSwap(false, true) {
		close(c.stopCh)
	}
}

// removeExpired removes all expired entries from the cache.
func (c *SimpleCache) removeExpired() {
	now := time.Now()
	c.data.Range(func(key, value any) bool {
		entry := value.(*cacheEntry)
		if now.After(entry.expiresAt) {
			c.data.Delete(key)
		}
		return true
	})
}

// Stats returns current cache statistics.
func (c *SimpleCache) Stats() Stats {
	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	// Count items
	itemCount := 0
	c.data.Range(func(_, _ any) bool {
		itemCount++
		return true
	})

	return Stats{
		Hits:    hits,
		Misses:  misses,
		Sets:    c.sets.Load(),
		Items:   itemCount,
		HitRate: hitRate,
		ResetAt: c.statsResetAt.Load(),
	}
}

// ResetStats resets the cache statistics.
func (c *SimpleCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.sets.Store(0)
	c.statsResetAt.Store(new(time.Now()))
}

// Keys returns all keys in the cache (including expired ones).
func (c *SimpleCache) Keys() []string {
	var keys []string
	c.data.Range(func(key, _ any) bool {
		keys = append(keys, key.(string))
		return true
	})
	return keys
}
