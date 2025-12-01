package cache

import (
	"time"
)

// CacheConfig holds configuration for cache creation.
type CacheConfig struct {
	// Type is the cache backend type: "memory" or "redis"
	Type string

	// RedisURL is the Redis connection URL (only for redis type)
	// Example: redis://localhost:6379/0
	RedisURL string

	// Prefix is the key prefix for Redis (only for redis type)
	Prefix string

	// DefaultTTL is the default TTL for cache entries
	DefaultTTL time.Duration

	// MaxSize is the maximum number of entries for memory cache (0 = unlimited)
	MaxSize int

	// CleanupInterval is the interval for expired entry cleanup
	CleanupInterval time.Duration
}

// DefaultCacheConfig returns default cache configuration.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		Type:            "memory",
		DefaultTTL:      time.Hour,
		MaxSize:         10000,
		CleanupInterval: time.Minute,
	}
}

// NewCache creates a cache based on the provided configuration.
// If RedisURL is provided, creates a Redis cache (future implementation).
// Otherwise, creates an in-memory cache.
func NewCache(cfg CacheConfig) (Cacher, error) {
	// For now, only memory cache is implemented
	// Redis cache will be added in Iteration 14
	if cfg.Type == "redis" && cfg.RedisURL != "" {
		// Redis cache not yet implemented - fall back to memory
		// return NewRedisCache(cfg.RedisURL, cfg.Prefix, cfg.DefaultTTL)
	}

	return NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      cfg.DefaultTTL,
		MaxSize:         cfg.MaxSize,
		CleanupInterval: cfg.CleanupInterval,
	}), nil
}

// NewDefaultCache creates a cache with default configuration.
func NewDefaultCache() Cacher {
	cache, _ := NewCache(DefaultCacheConfig())
	return cache
}

// NewCacheWithTTL creates a simple memory cache with the specified TTL.
// This is a convenience function for common use cases.
func NewCacheWithTTL(ttl time.Duration) Cacher {
	return NewSimpleMemoryCache(ttl)
}
