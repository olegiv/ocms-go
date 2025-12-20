// Package cache provides caching infrastructure for oCMS.
package cache

import (
	"context"
	"time"
)

// Cache defines the interface for cache implementations.
// All implementations must be thread-safe.
// This interface uses []byte for values to support both in-memory and Redis caches.
type Cache interface {
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

// StatsProvider is an optional interface for caches that provide statistics.
type StatsProvider interface {
	Stats() Stats
	ResetStats()
}

// Error represents an error type for cache operations.
type Error string

func (e Error) Error() string {
	return string(e)
}

const (
	// ErrCacheMiss indicates the key was not found in cache or has expired.
	ErrCacheMiss Error = "cache miss"

	// ErrCacheClosed indicates the cache has been closed.
	ErrCacheClosed Error = "cache closed"
)
