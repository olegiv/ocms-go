package cache

import (
	"context"
	"os"
	"testing"
	"time"
)

// getRedisURL returns the Redis URL for testing, or empty string if not configured.
func getRedisURL() string {
	return os.Getenv("OCMS_TEST_REDIS_URL")
}

// skipIfNoRedis skips the test if Redis is not configured.
func skipIfNoRedis(t *testing.T) string {
	url := getRedisURL()
	if url == "" {
		t.Skip("Skipping Redis tests: OCMS_TEST_REDIS_URL not set")
	}
	return url
}

func TestRedisCache_Basic(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()

	// Clear any existing test data
	_ = cache.Clear(ctx)

	// Test Set and Get
	key := "test-key"
	value := []byte("test-value")

	err = cache.Set(ctx, key, value, time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(got) != string(value) {
		t.Errorf("Get returned %q, want %q", got, value)
	}

	// Test Has
	exists, err := cache.Has(ctx, key)
	if err != nil {
		t.Fatalf("Has failed: %v", err)
	}
	if !exists {
		t.Error("Has returned false for existing key")
	}

	// Test Delete
	err = cache.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	_, err = cache.Get(ctx, key)
	if err != ErrCacheMiss {
		t.Errorf("Get after Delete returned error %v, want ErrCacheMiss", err)
	}
}

func TestRedisCache_Miss(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()

	_, err = cache.Get(ctx, "nonexistent-key-12345")
	if err != ErrCacheMiss {
		t.Errorf("Get nonexistent key returned %v, want ErrCacheMiss", err)
	}
}

func TestRedisCache_TTL(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()

	key := "ttl-test-key"
	value := []byte("ttl-test-value")

	// Set with very short TTL
	err = cache.Set(ctx, key, value, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Should exist immediately
	_, err = cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get immediately after Set failed: %v", err)
	}

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Should be expired
	_, err = cache.Get(ctx, key)
	if err != ErrCacheMiss {
		t.Errorf("Get after TTL expiration returned %v, want ErrCacheMiss", err)
	}
}

func TestRedisCache_Clear(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "clear-test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()

	// Set multiple keys
	for i := 0; i < 5; i++ {
		key := "key-" + string(rune('a'+i))
		err = cache.Set(ctx, key, []byte("value"), time.Minute)
		if err != nil {
			t.Fatalf("Set key %s failed: %v", key, err)
		}
	}

	// Clear
	err = cache.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify all keys are gone
	for i := 0; i < 5; i++ {
		key := "key-" + string(rune('a'+i))
		_, err = cache.Get(ctx, key)
		if err != ErrCacheMiss {
			t.Errorf("Get key %s after Clear returned %v, want ErrCacheMiss", key, err)
		}
	}
}

func TestRedisCache_DeleteByPrefix(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "prefix-test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()

	// Clear existing data
	_ = cache.Clear(ctx)

	// Set keys with different prefixes
	_ = cache.Set(ctx, "user:1", []byte("user1"), time.Minute)
	_ = cache.Set(ctx, "user:2", []byte("user2"), time.Minute)
	_ = cache.Set(ctx, "config:site", []byte("site"), time.Minute)

	// Delete only user: keys
	err = cache.DeleteByPrefix(ctx, "user:")
	if err != nil {
		t.Fatalf("DeleteByPrefix failed: %v", err)
	}

	// User keys should be gone
	_, err = cache.Get(ctx, "user:1")
	if err != ErrCacheMiss {
		t.Error("user:1 should be deleted")
	}
	_, err = cache.Get(ctx, "user:2")
	if err != ErrCacheMiss {
		t.Error("user:2 should be deleted")
	}

	// Config key should still exist
	_, err = cache.Get(ctx, "config:site")
	if err != nil {
		t.Errorf("config:site should still exist, got error: %v", err)
	}
}

func TestRedisCache_Stats(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "stats-test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()

	// Clear and reset
	_ = cache.Clear(ctx)
	cache.ResetStats()

	// Perform operations
	_ = cache.Set(ctx, "key1", []byte("value1"), time.Minute)
	_ = cache.Set(ctx, "key2", []byte("value2"), time.Minute)
	_, _ = cache.Get(ctx, "key1") // hit
	_, _ = cache.Get(ctx, "key1") // hit
	_, _ = cache.Get(ctx, "key3") // miss

	stats := cache.Stats()

	if stats.Sets != 2 {
		t.Errorf("Sets = %d, want 2", stats.Sets)
	}
	if stats.Hits != 2 {
		t.Errorf("Hits = %d, want 2", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}

	// Hit rate should be 2/3 = 66.67%
	expectedHitRate := float64(2) / float64(3) * 100
	if stats.HitRate < expectedHitRate-1 || stats.HitRate > expectedHitRate+1 {
		t.Errorf("HitRate = %.2f, want ~%.2f", stats.HitRate, expectedHitRate)
	}
}

func TestRedisCache_Ping(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "ping-test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()

	err = cache.Ping(ctx)
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

func TestRedisCache_Close(t *testing.T) {
	url := skipIfNoRedis(t)

	cache, err := NewRedisCacheFromURL(url, "close-test:", time.Minute)
	if err != nil {
		t.Fatalf("failed to create Redis cache: %v", err)
	}

	ctx := context.Background()

	// Close the cache
	err = cache.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Operations should fail after close
	_, err = cache.Get(ctx, "key")
	if err != ErrCacheClosed {
		t.Errorf("Get after Close returned %v, want ErrCacheClosed", err)
	}

	err = cache.Set(ctx, "key", []byte("value"), time.Minute)
	if err != ErrCacheClosed {
		t.Errorf("Set after Close returned %v, want ErrCacheClosed", err)
	}
}

func TestRedisCache_InvalidURL(t *testing.T) {
	// Should fail with invalid URL
	_, err := NewRedisCacheFromURL("invalid-url", "test:", time.Minute)
	if err == nil {
		t.Error("expected error with invalid URL, got nil")
	}
}

func TestRedisCache_EmptyURL(t *testing.T) {
	_, err := NewRedisCacheFromURL("", "test:", time.Minute)
	if err == nil {
		t.Error("expected error with empty URL, got nil")
	}
}

func TestNewCacheWithInfo_Memory(t *testing.T) {
	cfg := CacheConfig{
		Type:       "memory",
		DefaultTTL: time.Minute,
		MaxSize:    100,
	}

	result, err := NewCacheWithInfo(cfg)
	if err != nil {
		t.Fatalf("NewCacheWithInfo failed: %v", err)
	}
	defer result.Cache.Close()

	if result.BackendType != CacheBackendMemory {
		t.Errorf("BackendType = %s, want %s", result.BackendType, CacheBackendMemory)
	}
	if result.IsFallback {
		t.Error("IsFallback should be false for explicit memory config")
	}
}

func TestNewCacheWithInfo_RedisFallback(t *testing.T) {
	cfg := CacheConfig{
		Type:             "redis",
		RedisURL:         "redis://localhost:63999/0", // Non-existent Redis
		FallbackToMemory: true,
		DefaultTTL:       time.Minute,
		MaxSize:          100,
	}

	result, err := NewCacheWithInfo(cfg)
	if err != nil {
		t.Fatalf("NewCacheWithInfo should not error with fallback enabled: %v", err)
	}
	defer result.Cache.Close()

	if result.BackendType != CacheBackendMemory {
		t.Errorf("BackendType = %s, want %s (fallback)", result.BackendType, CacheBackendMemory)
	}
	if !result.IsFallback {
		t.Error("IsFallback should be true when Redis fails")
	}
}

func TestNewCacheWithInfo_RedisNoFallback(t *testing.T) {
	cfg := CacheConfig{
		Type:             "redis",
		RedisURL:         "redis://localhost:63999/0", // Non-existent Redis
		FallbackToMemory: false,
		DefaultTTL:       time.Minute,
	}

	_, err := NewCacheWithInfo(cfg)
	if err == nil {
		t.Error("NewCacheWithInfo should error when Redis fails and fallback disabled")
	}
}

func TestMaskRedisURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"redis://localhost:6379/0", "redis://localhost:6379/0"},
		{"redis://:password@localhost:6379/0", "redis://***@localhost:6379/0"},
		{"redis://user:pass@host:6379/0", "redis://***@host:6379/0"},
		{"short", "short"},
		{"", ""},
	}

	for _, tt := range tests {
		got := maskRedisURL(tt.input)
		if got != tt.expected {
			t.Errorf("maskRedisURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
