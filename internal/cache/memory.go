package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryCache is a thread-safe in-memory cache implementation.
// It implements the Cacher interface using []byte values.
type MemoryCache struct {
	data       sync.Map
	defaultTTL time.Duration
	maxSize    int // Maximum number of entries (0 = unlimited)
	stopCh     chan struct{}
	closed     atomic.Bool

	// Statistics
	hits   atomic.Int64
	misses atomic.Int64
	sets   atomic.Int64
	size   atomic.Int64 // Approximate size in bytes
}

// memoryCacheEntry holds a cached value with its expiration time.
type memoryCacheEntry struct {
	value     []byte
	expiresAt time.Time
	size      int64
}

// MemoryCacheOptions configures the memory cache.
type MemoryCacheOptions struct {
	DefaultTTL      time.Duration
	MaxSize         int           // Maximum number of entries (0 = unlimited)
	CleanupInterval time.Duration // Interval for expired entry cleanup (0 = no cleanup)
}

// NewMemoryCache creates a new memory cache with the given options.
func NewMemoryCache(opts MemoryCacheOptions) *MemoryCache {
	c := &MemoryCache{
		defaultTTL: opts.DefaultTTL,
		maxSize:    opts.MaxSize,
		stopCh:     make(chan struct{}),
	}

	// Start cleanup goroutine if interval is set
	if opts.CleanupInterval > 0 {
		go c.cleanupLoop(opts.CleanupInterval)
	}

	return c
}

// NewSimpleMemoryCache creates a memory cache with just a TTL (for backwards compatibility).
func NewSimpleMemoryCache(ttl time.Duration) *MemoryCache {
	return NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      ttl,
		MaxSize:         0, // Unlimited
		CleanupInterval: time.Minute,
	})
}

// Get retrieves a value from the cache.
func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	if c.closed.Load() {
		return nil, ErrCacheClosed
	}

	val, ok := c.data.Load(key)
	if !ok {
		c.misses.Add(1)
		return nil, ErrCacheMiss
	}

	entry := val.(*memoryCacheEntry)
	if time.Now().After(entry.expiresAt) {
		// Entry expired, remove it
		c.deleteEntry(key, entry)
		c.misses.Add(1)
		return nil, ErrCacheMiss
	}

	c.hits.Add(1)
	// Return a copy to prevent mutation
	result := make([]byte, len(entry.value))
	copy(result, entry.value)
	return result, nil
}

// Set stores a value in the cache with the specified TTL.
func (c *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	if ttl == 0 {
		ttl = c.defaultTTL
	}

	// Check if we need to evict (simple LRU-ish: evict expired first, then oldest)
	if c.maxSize > 0 {
		count := c.count()
		if count >= c.maxSize {
			// Try to make room by removing expired entries
			c.removeExpired()
			// If still at capacity, we'll just overwrite (could implement better eviction)
		}
	}

	// Make a copy of the value to prevent external mutation
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	entry := &memoryCacheEntry{
		value:     valueCopy,
		expiresAt: time.Now().Add(ttl),
		size:      int64(len(value)),
	}

	// Check if we're replacing an existing entry
	if old, loaded := c.data.Swap(key, entry); loaded {
		oldEntry := old.(*memoryCacheEntry)
		c.size.Add(-oldEntry.size)
	}

	c.size.Add(entry.size)
	c.sets.Add(1)
	return nil
}

// Delete removes a key from the cache.
func (c *MemoryCache) Delete(_ context.Context, key string) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	if val, loaded := c.data.LoadAndDelete(key); loaded {
		entry := val.(*memoryCacheEntry)
		c.size.Add(-entry.size)
	}
	return nil
}

// Clear removes all entries from the cache.
func (c *MemoryCache) Clear(_ context.Context) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	c.data.Range(func(key, value any) bool {
		c.data.Delete(key)
		return true
	})
	c.size.Store(0)
	return nil
}

// Has checks if a key exists in the cache (and is not expired).
func (c *MemoryCache) Has(_ context.Context, key string) (bool, error) {
	if c.closed.Load() {
		return false, ErrCacheClosed
	}

	val, ok := c.data.Load(key)
	if !ok {
		return false, nil
	}

	entry := val.(*memoryCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.deleteEntry(key, entry)
		return false, nil
	}

	return true, nil
}

// Close stops the cleanup goroutine and releases resources.
func (c *MemoryCache) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.stopCh)
	}
	return nil
}

// Stats returns current cache statistics.
func (c *MemoryCache) Stats() CacheStats {
	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	return CacheStats{
		Hits:    hits,
		Misses:  misses,
		Sets:    c.sets.Load(),
		Items:   c.count(),
		HitRate: hitRate,
		Size:    c.size.Load(),
	}
}

// ResetStats resets the cache statistics.
func (c *MemoryCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.sets.Store(0)
}

// DeleteByPrefix removes all keys starting with the given prefix.
func (c *MemoryCache) DeleteByPrefix(_ context.Context, prefix string) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	c.data.Range(func(key, value any) bool {
		k := key.(string)
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			entry := value.(*memoryCacheEntry)
			c.deleteEntry(k, entry)
		}
		return true
	})
	return nil
}

// Keys returns all keys in the cache (including expired ones).
func (c *MemoryCache) Keys() []string {
	var keys []string
	c.data.Range(func(key, value any) bool {
		keys = append(keys, key.(string))
		return true
	})
	return keys
}

// count returns the number of items in the cache.
func (c *MemoryCache) count() int {
	count := 0
	c.data.Range(func(key, value any) bool {
		count++
		return true
	})
	return count
}

// deleteEntry removes an entry and updates the size counter.
func (c *MemoryCache) deleteEntry(key string, entry *memoryCacheEntry) {
	if _, loaded := c.data.LoadAndDelete(key); loaded {
		c.size.Add(-entry.size)
	}
}

// removeExpired removes all expired entries from the cache.
func (c *MemoryCache) removeExpired() {
	now := time.Now()
	c.data.Range(func(key, value any) bool {
		entry := value.(*memoryCacheEntry)
		if now.After(entry.expiresAt) {
			c.deleteEntry(key.(string), entry)
		}
		return true
	})
}

// cleanupLoop periodically removes expired entries.
func (c *MemoryCache) cleanupLoop(interval time.Duration) {
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
}

// Ensure MemoryCache implements Cacher and StatsProvider.
var (
	_ Cacher        = (*MemoryCache)(nil)
	_ StatsProvider = (*MemoryCache)(nil)
)
