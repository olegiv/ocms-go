// Package cache provides caching infrastructure for oCMS.
package cache

import (
	"context"
	"time"
)

// Cacher defines the interface for cache implementations.
// All implementations must be thread-safe.
// This interface uses []byte for values to support both in-memory and Redis caches.
type Cacher interface {
	// Get retrieves a value from the cache.
	// Returns the value and nil error if found.
	// Returns nil and ErrCacheMiss if not found or expired.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value in the cache with the specified TTL.
	// If TTL is 0, uses the default TTL.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a key from the cache.
	Delete(ctx context.Context, key string) error

	// Clear removes all entries from the cache.
	Clear(ctx context.Context) error

	// Has checks if a key exists in the cache (and is not expired).
	Has(ctx context.Context, key string) (bool, error)

	// Close releases any resources held by the cache.
	Close() error
}

// CacheStats holds statistics for a cache implementation.
type CacheStats struct {
	Hits    int64   `json:"hits"`
	Misses  int64   `json:"misses"`
	Sets    int64   `json:"sets"`
	Items   int     `json:"items"`
	HitRate float64 `json:"hit_rate"`
	Size    int64   `json:"size_bytes"` // Approximate size in bytes
}

// StatsProvider is an optional interface for caches that provide statistics.
type StatsProvider interface {
	Stats() CacheStats
	ResetStats()
}

// CacheError represents an error type for cache operations.
type CacheError string

func (e CacheError) Error() string {
	return string(e)
}

const (
	// ErrCacheMiss indicates the key was not found in cache or has expired.
	ErrCacheMiss CacheError = "cache miss"

	// ErrCacheFull indicates the cache is at maximum capacity.
	ErrCacheFull CacheError = "cache full"

	// ErrCacheClosed indicates the cache has been closed.
	ErrCacheClosed CacheError = "cache closed"
)
