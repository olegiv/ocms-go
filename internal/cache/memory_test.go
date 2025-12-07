package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryCache_BasicOperations(t *testing.T) {
	cache := NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      time.Hour,
		MaxSize:         100,
		CleanupInterval: 0, // No background cleanup for tests
	})
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	// Test Set and Get
	err := cache.Set(ctx, "key1", []byte("value1"), 0)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := cache.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("expected value1, got %s", string(val))
	}

	// Test Has
	has, err := cache.Has(ctx, "key1")
	if err != nil {
		t.Fatalf("Has failed: %v", err)
	}
	if !has {
		t.Error("expected key1 to exist")
	}

	// Test Delete
	err = cache.Delete(ctx, "key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = cache.Get(ctx, "key1")
	if err != ErrCacheMiss {
		t.Errorf("expected ErrCacheMiss, got %v", err)
	}
}

func TestMemoryCache_CacheMiss(t *testing.T) {
	cache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	_, err := cache.Get(ctx, "nonexistent")
	if err != ErrCacheMiss {
		t.Errorf("expected ErrCacheMiss, got %v", err)
	}

	has, err := cache.Has(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Has failed: %v", err)
	}
	if has {
		t.Error("expected nonexistent key to not exist")
	}
}

func TestMemoryCache_Expiration(t *testing.T) {
	cache := NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      50 * time.Millisecond,
		CleanupInterval: 0,
	})
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	err := cache.Set(ctx, "expiring", []byte("value"), 0)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Should exist immediately
	_, err = cache.Get(ctx, "expiring")
	if err != nil {
		t.Error("expected key to exist immediately")
	}

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	// Should be expired
	_, err = cache.Get(ctx, "expiring")
	if err != ErrCacheMiss {
		t.Errorf("expected ErrCacheMiss after expiration, got %v", err)
	}
}

func TestMemoryCache_CustomTTL(t *testing.T) {
	cache := NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      time.Hour,
		CleanupInterval: 0,
	})
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	// Set with custom short TTL
	err := cache.Set(ctx, "short", []byte("value"), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Set with default TTL
	err = cache.Set(ctx, "default", []byte("value"), 0)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Wait for short TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Short should be expired
	_, err = cache.Get(ctx, "short")
	if err != ErrCacheMiss {
		t.Errorf("expected short TTL key to expire, got %v", err)
	}

	// Default should still exist
	_, err = cache.Get(ctx, "default")
	if err != nil {
		t.Error("expected default TTL key to still exist")
	}
}

func TestMemoryCache_Clear(t *testing.T) {
	cache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	// Set multiple keys
	cache.Set(ctx, "key1", []byte("value1"), 0)
	cache.Set(ctx, "key2", []byte("value2"), 0)
	cache.Set(ctx, "key3", []byte("value3"), 0)

	// Clear all
	err := cache.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify all are gone
	for _, key := range []string{"key1", "key2", "key3"} {
		_, err := cache.Get(ctx, key)
		if err != ErrCacheMiss {
			t.Errorf("expected %s to be cleared", key)
		}
	}
}

func TestMemoryCache_DeleteByPrefix(t *testing.T) {
	cache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	// Set multiple keys with same prefix
	cache.Set(ctx, "prefix:key1", []byte("value1"), 0)
	cache.Set(ctx, "prefix:key2", []byte("value2"), 0)
	cache.Set(ctx, "prefix:key3", []byte("value3"), 0)
	cache.Set(ctx, "other:key", []byte("other"), 0)

	// Delete by prefix
	err := cache.DeleteByPrefix(ctx, "prefix:")
	if err != nil {
		t.Fatalf("DeleteByPrefix failed: %v", err)
	}

	// Verify prefix keys are gone
	for _, key := range []string{"prefix:key1", "prefix:key2", "prefix:key3"} {
		_, err := cache.Get(ctx, key)
		if err != ErrCacheMiss {
			t.Errorf("expected %s to be deleted", key)
		}
	}

	// Other key should still exist
	_, err = cache.Get(ctx, "other:key")
	if err != nil {
		t.Error("expected other:key to still exist")
	}
}

func TestMemoryCache_Stats(t *testing.T) {
	cache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	// Set some values
	cache.Set(ctx, "key1", []byte("value1"), 0)
	cache.Set(ctx, "key2", []byte("value2"), 0)

	// Hit
	cache.Get(ctx, "key1")
	cache.Get(ctx, "key1")

	// Miss
	cache.Get(ctx, "nonexistent")

	stats := cache.Stats()

	if stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", stats.Hits)
	}

	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}

	if stats.Sets != 2 {
		t.Errorf("expected 2 sets, got %d", stats.Sets)
	}

	if stats.Items != 2 {
		t.Errorf("expected 2 items, got %d", stats.Items)
	}

	// Hit rate should be ~66.67%
	expectedHitRate := float64(2) / float64(3) * 100
	if stats.HitRate < expectedHitRate-0.01 || stats.HitRate > expectedHitRate+0.01 {
		t.Errorf("expected hit rate ~%.2f, got %.2f", expectedHitRate, stats.HitRate)
	}
}

func TestMemoryCache_ConcurrentAccess(t *testing.T) {
	cache := NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      time.Hour,
		MaxSize:         0, // Unlimited
		CleanupInterval: 0,
	})
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	var wg sync.WaitGroup
	numGoroutines := 100
	numOperations := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cache.Set(ctx, "key", []byte("value"), 0)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cache.Get(ctx, "key")
			}
		}()
	}

	wg.Wait()

	// Should not panic and should have a value
	_, err := cache.Get(ctx, "key")
	if err != nil {
		t.Error("expected key to exist after concurrent access")
	}
}

func TestMemoryCache_ValueCopy(t *testing.T) {
	cache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = cache.Close() }()
	ctx := context.Background()

	original := []byte("original")
	err := cache.Set(ctx, "key", original, 0)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Modify original after setting
	original[0] = 'X'

	// Get should return original value, not modified
	val, err := cache.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "original" {
		t.Errorf("expected original, got %s (cache didn't copy on set)", string(val))
	}

	// Modify returned value
	val[0] = 'Y'

	// Get again should still return original
	val2, _ := cache.Get(ctx, "key")
	if string(val2) != "original" {
		t.Errorf("expected original, got %s (cache didn't copy on get)", string(val2))
	}
}

func TestMemoryCache_Close(t *testing.T) {
	cache := NewMemoryCache(MemoryCacheOptions{
		DefaultTTL:      time.Hour,
		CleanupInterval: time.Second,
	})
	ctx := context.Background()

	cache.Set(ctx, "key", []byte("value"), 0)

	err := cache.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Operations should return ErrCacheClosed
	_, err = cache.Get(ctx, "key")
	if err != ErrCacheClosed {
		t.Errorf("expected ErrCacheClosed after close, got %v", err)
	}

	err = cache.Set(ctx, "key2", []byte("value"), 0)
	if err != ErrCacheClosed {
		t.Errorf("expected ErrCacheClosed on Set after close, got %v", err)
	}

	// Close again should be idempotent
	err = cache.Close()
	if err != nil {
		t.Errorf("second Close should succeed, got %v", err)
	}
}
