package cache

import (
	"context"
	"encoding/json"
	"time"
)

// TypedCache provides type-safe caching operations using generics.
// It wraps a Cacher implementation and handles JSON serialization/deserialization.
type TypedCache[T any] struct {
	cache      Cacher
	defaultTTL time.Duration
}

// NewTypedCache creates a new TypedCache wrapping the given cache implementation.
func NewTypedCache[T any](cache Cacher, defaultTTL time.Duration) *TypedCache[T] {
	return &TypedCache[T]{
		cache:      cache,
		defaultTTL: defaultTTL,
	}
}

// Get retrieves a value from the cache.
// Returns the value and true if found, zero value and false otherwise.
func (c *TypedCache[T]) Get(ctx context.Context, key string) (*T, bool) {
	data, err := c.cache.Get(ctx, key)
	if err != nil {
		return nil, false
	}

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, false
	}

	return &value, true
}

// Set stores a value in the cache with the default TTL.
func (c *TypedCache[T]) Set(ctx context.Context, key string, value *T) error {
	return c.SetWithTTL(ctx, key, value, c.defaultTTL)
}

// SetWithTTL stores a value in the cache with a custom TTL.
func (c *TypedCache[T]) SetWithTTL(ctx context.Context, key string, value *T, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.cache.Set(ctx, key, data, ttl)
}

// Delete removes a key from the cache.
func (c *TypedCache[T]) Delete(ctx context.Context, key string) error {
	return c.cache.Delete(ctx, key)
}

// Has checks if a key exists in the cache.
func (c *TypedCache[T]) Has(ctx context.Context, key string) bool {
	has, _ := c.cache.Has(ctx, key)
	return has
}

// GetOrSet retrieves a value from cache, or calls the provided function to compute
// and store it if not found.
func (c *TypedCache[T]) GetOrSet(ctx context.Context, key string, fn func() (*T, error)) (*T, error) {
	return c.GetOrSetWithTTL(ctx, key, c.defaultTTL, fn)
}

// GetOrSetWithTTL retrieves a value from cache, or calls the provided function to compute
// and store it if not found, using a custom TTL.
func (c *TypedCache[T]) GetOrSetWithTTL(ctx context.Context, key string, ttl time.Duration, fn func() (*T, error)) (*T, error) {
	// Try to get from cache first
	if value, ok := c.Get(ctx, key); ok {
		return value, nil
	}

	// Cache miss, compute the value
	value, err := fn()
	if err != nil {
		return nil, err
	}

	// Store in cache (ignore errors, value is still valid)
	_ = c.SetWithTTL(ctx, key, value, ttl)

	return value, nil
}

// MultiTypedCache provides multi-key operations for typed caches.
type MultiTypedCache[T any] struct {
	*TypedCache[T]
}

// NewMultiTypedCache creates a new MultiTypedCache.
func NewMultiTypedCache[T any](cache Cacher, defaultTTL time.Duration) *MultiTypedCache[T] {
	return &MultiTypedCache[T]{
		TypedCache: NewTypedCache[T](cache, defaultTTL),
	}
}

// GetMultiple retrieves multiple values from the cache.
// Returns a map of found keys to their values.
func (c *MultiTypedCache[T]) GetMultiple(ctx context.Context, keys []string) map[string]*T {
	result := make(map[string]*T, len(keys))
	for _, key := range keys {
		if value, ok := c.Get(ctx, key); ok {
			result[key] = value
		}
	}
	return result
}

// SetMultiple stores multiple values in the cache.
func (c *MultiTypedCache[T]) SetMultiple(ctx context.Context, items map[string]*T) error {
	for key, value := range items {
		if err := c.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

// DeleteMultiple removes multiple keys from the cache.
func (c *MultiTypedCache[T]) DeleteMultiple(ctx context.Context, keys []string) error {
	for _, key := range keys {
		if err := c.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}
