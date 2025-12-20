package cache

import (
	"log/slog"
	"time"
)

// BackendType identifies the type of cache backend.
type BackendType string

const (
	BackendMemory BackendType = "memory"
	BackendRedis  BackendType = "redis"
)

// Config holds configuration for cache creation.
type Config struct {
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

	// FallbackToMemory enables automatic fallback to memory cache if Redis is unavailable
	FallbackToMemory bool
}

// Result holds the created cache and metadata about it.
type Result struct {
	Cache       Cache
	BackendType BackendType
	IsFallback  bool // True if fell back to memory due to Redis failure
}

// NewCacheWithInfo creates a cache and returns additional metadata about the cache type.
// If RedisURL is provided and type is "redis", attempts to create a Redis cache.
// Falls back to memory cache if Redis is unavailable and FallbackToMemory is true.
func NewCacheWithInfo(cfg Config) (*Result, error) {
	// Try Redis if configured
	if cfg.Type == "redis" && cfg.RedisURL != "" {
		redisCache, err := NewRedisCacheFromURL(cfg.RedisURL, cfg.Prefix, cfg.DefaultTTL)
		if err != nil {
			slog.Warn("failed to connect to Redis cache",
				"error", err,
				"url", cfg.RedisURL,
				"fallback", cfg.FallbackToMemory,
			)

			if !cfg.FallbackToMemory {
				return nil, err
			}

			// Fall back to memory cache
			slog.Info("falling back to memory cache")
			memCache := NewMemoryCache(MemoryCacheOptions{
				DefaultTTL:      cfg.DefaultTTL,
				MaxSize:         cfg.MaxSize,
				CleanupInterval: cfg.CleanupInterval,
			})

			return &Result{
				Cache:       memCache,
				BackendType: BackendMemory,
				IsFallback:  true,
			}, nil
		}

		slog.Info("connected to Redis cache",
			"url", cfg.RedisURL,
			"prefix", cfg.Prefix,
		)

		return &Result{
			Cache:       redisCache,
			BackendType: BackendRedis,
			IsFallback:  false,
		}, nil
	}

	// Default to memory cache
	memCache := NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      cfg.DefaultTTL,
		MaxSize:         cfg.MaxSize,
		CleanupInterval: cfg.CleanupInterval,
	})

	return &Result{
		Cache:       memCache,
		BackendType: BackendMemory,
		IsFallback:  false,
	}, nil
}
