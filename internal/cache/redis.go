package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache is a Redis-based cache implementation.
// It implements the Cacher interface for distributed caching.
type RedisCache struct {
	client     *redis.Client
	prefix     string
	defaultTTL time.Duration
	closed     atomic.Bool

	// Statistics
	hits   atomic.Int64
	misses atomic.Int64
	sets   atomic.Int64
}

// RedisCacheOptions configures the Redis cache.
type RedisCacheOptions struct {
	// URL is the Redis connection URL (e.g., redis://localhost:6379/0)
	URL string

	// Prefix is prepended to all keys (e.g., "ocms:")
	Prefix string

	// DefaultTTL is the default expiration time for cache entries
	DefaultTTL time.Duration

	// PoolSize is the maximum number of connections (0 = use default)
	PoolSize int

	// ConnectTimeout is the timeout for establishing a connection
	ConnectTimeout time.Duration

	// ReadTimeout is the timeout for read operations
	ReadTimeout time.Duration

	// WriteTimeout is the timeout for write operations
	WriteTimeout time.Duration
}

// DefaultRedisCacheOptions returns sensible defaults.
func DefaultRedisCacheOptions() RedisCacheOptions {
	return RedisCacheOptions{
		Prefix:         "ocms:",
		DefaultTTL:     time.Hour,
		PoolSize:       10,
		ConnectTimeout: 5 * time.Second,
		ReadTimeout:    3 * time.Second,
		WriteTimeout:   3 * time.Second,
	}
}

// NewRedisCache creates a new Redis cache with the given options.
func NewRedisCache(opts RedisCacheOptions) (*RedisCache, error) {
	if opts.URL == "" {
		return nil, errors.New("redis URL is required")
	}

	// Parse Redis URL
	redisOpts, err := redis.ParseURL(opts.URL)
	if err != nil {
		return nil, err
	}

	// Apply custom options
	if opts.PoolSize > 0 {
		redisOpts.PoolSize = opts.PoolSize
	}
	if opts.ConnectTimeout > 0 {
		redisOpts.DialTimeout = opts.ConnectTimeout
	}
	if opts.ReadTimeout > 0 {
		redisOpts.ReadTimeout = opts.ReadTimeout
	}
	if opts.WriteTimeout > 0 {
		redisOpts.WriteTimeout = opts.WriteTimeout
	}

	client := redis.NewClient(redisOpts)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), opts.ConnectTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, err
	}

	return &RedisCache{
		client:     client,
		prefix:     opts.Prefix,
		defaultTTL: opts.DefaultTTL,
	}, nil
}

// NewRedisCacheFromURL creates a Redis cache from just a URL with default options.
func NewRedisCacheFromURL(url string, prefix string, defaultTTL time.Duration) (*RedisCache, error) {
	opts := DefaultRedisCacheOptions()
	opts.URL = url
	if prefix != "" {
		opts.Prefix = prefix
	}
	if defaultTTL > 0 {
		opts.DefaultTTL = defaultTTL
	}
	return NewRedisCache(opts)
}

// prefixKey adds the cache prefix to a key.
func (c *RedisCache) prefixKey(key string) string {
	return c.prefix + key
}

// Get retrieves a value from the cache.
func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	if c.closed.Load() {
		return nil, ErrCacheClosed
	}

	val, err := c.client.Get(ctx, c.prefixKey(key)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			c.misses.Add(1)
			return nil, ErrCacheMiss
		}
		return nil, err
	}

	c.hits.Add(1)
	return val, nil
}

// Set stores a value in the cache with the specified TTL.
func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	if ttl == 0 {
		ttl = c.defaultTTL
	}

	err := c.client.Set(ctx, c.prefixKey(key), value, ttl).Err()
	if err != nil {
		return err
	}

	c.sets.Add(1)
	return nil
}

// Delete removes a key from the cache.
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	return c.client.Del(ctx, c.prefixKey(key)).Err()
}

// Clear removes all entries with the cache prefix.
// Note: This uses SCAN + DEL which is safer than KEYS for production use.
func (c *RedisCache) Clear(ctx context.Context) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	var cursor uint64
	pattern := c.prefix + "*"

	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return nil
}

// Has checks if a key exists in the cache.
func (c *RedisCache) Has(ctx context.Context, key string) (bool, error) {
	if c.closed.Load() {
		return false, ErrCacheClosed
	}

	exists, err := c.client.Exists(ctx, c.prefixKey(key)).Result()
	if err != nil {
		return false, err
	}

	return exists > 0, nil
}

// Close closes the Redis connection.
func (c *RedisCache) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		return c.client.Close()
	}
	return nil
}

// Stats returns current cache statistics.
// Note: Redis doesn't track per-prefix stats, so we use local counters.
func (c *RedisCache) Stats() CacheStats {
	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	// Count keys with prefix (approximate)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var itemCount int
	var cursor uint64
	pattern := c.prefix + "*"

	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 1000).Result()
		if err != nil {
			break
		}
		itemCount += len(keys)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return CacheStats{
		Hits:    hits,
		Misses:  misses,
		Sets:    c.sets.Load(),
		Items:   itemCount,
		HitRate: hitRate,
		Size:    0, // Redis doesn't easily provide this
	}
}

// ResetStats resets the cache statistics.
func (c *RedisCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.sets.Store(0)
}

// Ping checks if the Redis connection is healthy.
func (c *RedisCache) Ping(ctx context.Context) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}
	return c.client.Ping(ctx).Err()
}

// DeleteByPrefix removes all keys starting with the given prefix.
// The prefix is added to the cache's base prefix.
func (c *RedisCache) DeleteByPrefix(ctx context.Context, prefix string) error {
	if c.closed.Load() {
		return ErrCacheClosed
	}

	var cursor uint64
	pattern := c.prefix + prefix + "*"

	for {
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return nil
}

// Info returns Redis server info.
func (c *RedisCache) Info(ctx context.Context) (string, error) {
	if c.closed.Load() {
		return "", ErrCacheClosed
	}
	return c.client.Info(ctx).Result()
}

// Client returns the underlying Redis client for advanced operations.
// Use with caution.
func (c *RedisCache) Client() *redis.Client {
	return c.client
}

// Ensure RedisCache implements Cacher and StatsProvider.
var (
	_ Cacher        = (*RedisCache)(nil)
	_ StatsProvider = (*RedisCache)(nil)
)
