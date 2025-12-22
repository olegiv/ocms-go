// Package cache provides an in-memory caching layer for oCMS.
package cache

import (
	"sync"
	"testing"
	"time"
)

// newTestCache creates a cache with default TTL and registers cleanup.
func newTestCache(t *testing.T) *SimpleCache {
	t.Helper()
	c := New(5 * time.Minute)
	t.Cleanup(c.Stop)
	return c
}

// requireKey asserts that a key exists in the cache.
func requireKey(t *testing.T, c *SimpleCache, key string) any {
	t.Helper()
	val, found := c.Get(key)
	if !found {
		t.Fatalf("expected key %q to exist", key)
	}
	return val
}

// requireNoKey asserts that a key does not exist in the cache.
func requireNoKey(t *testing.T, c *SimpleCache, key string) {
	t.Helper()
	_, found := c.Get(key)
	if found {
		t.Fatalf("expected key %q to not exist", key)
	}
}

func TestNew(t *testing.T) {
	ttl := 5 * time.Minute
	c := New(ttl)

	if c == nil {
		t.Fatal("expected cache to be non-nil")
	}

	if c.ttl != ttl {
		t.Errorf("expected TTL %v, got %v", ttl, c.ttl)
	}

	if c.stopCh == nil {
		t.Error("expected stopCh to be initialized")
	}
}

func TestSetAndGet(t *testing.T) {
	c := newTestCache(t)
	c.Set("key1", "value1")

	val := requireKey(t, c, "key1")
	if val != "value1" {
		t.Errorf("expected value1, got %v", val)
	}
}

func TestGetMissing(t *testing.T) {
	c := newTestCache(t)
	requireNoKey(t, c, "nonexistent")
}

func TestExpiration(t *testing.T) {
	c := New(50 * time.Millisecond)
	t.Cleanup(c.Stop)

	c.Set("expiring", "value")
	requireKey(t, c, "expiring") // Should exist immediately

	time.Sleep(60 * time.Millisecond)
	requireNoKey(t, c, "expiring") // Should be expired
}

func TestSetWithTTL(t *testing.T) {
	c := newTestCache(t)

	c.SetWithTTL("short", "value", 50*time.Millisecond)
	c.SetWithTTL("long", "value", 1*time.Hour)

	requireKey(t, c, "short") // Should exist immediately

	time.Sleep(60 * time.Millisecond)

	requireNoKey(t, c, "short") // Should be expired
	requireKey(t, c, "long")    // Should still exist
}

func TestDelete(t *testing.T) {
	c := newTestCache(t)

	c.Set("to-delete", "value")
	requireKey(t, c, "to-delete")

	c.Delete("to-delete")
	requireNoKey(t, c, "to-delete")
}

func TestDeleteByPrefix(t *testing.T) {
	c := newTestCache(t)

	c.Set("prefix:key1", "value1")
	c.Set("prefix:key2", "value2")
	c.Set("prefix:key3", "value3")
	c.Set("other:key", "other")

	c.DeleteByPrefix("prefix:")

	for _, key := range []string{"prefix:key1", "prefix:key2", "prefix:key3"} {
		requireNoKey(t, c, key)
	}
	requireKey(t, c, "other:key")
}

func TestClear(t *testing.T) {
	c := newTestCache(t)

	c.Set("key1", "value1")
	c.Set("key2", "value2")
	c.Set("key3", "value3")

	c.Clear()

	for _, key := range []string{"key1", "key2", "key3"} {
		requireNoKey(t, c, key)
	}
}

func TestStats(t *testing.T) {
	c := newTestCache(t)

	c.Set("key1", "value1")
	c.Set("key2", "value2")
	c.Get("key1")        // Hit
	c.Get("key1")        // Hit
	c.Get("nonexistent") // Miss

	stats := c.Stats()

	if stats.Hits != 2 {
		t.Errorf("Hits = %d, want 2", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Misses = %d, want 1", stats.Misses)
	}
	if stats.Sets != 2 {
		t.Errorf("Sets = %d, want 2", stats.Sets)
	}
	if stats.Items != 2 {
		t.Errorf("Items = %d, want 2", stats.Items)
	}

	expectedHitRate := float64(2) / float64(3) * 100
	if stats.HitRate < expectedHitRate-0.01 || stats.HitRate > expectedHitRate+0.01 {
		t.Errorf("HitRate = %.2f, want ~%.2f", stats.HitRate, expectedHitRate)
	}
}

func TestResetStats(t *testing.T) {
	c := newTestCache(t)

	c.Set("key", "value")
	c.Get("key")
	c.Get("nonexistent")
	c.ResetStats()

	stats := c.Stats()
	if stats.Hits != 0 {
		t.Errorf("Hits = %d after reset, want 0", stats.Hits)
	}
	if stats.Misses != 0 {
		t.Errorf("Misses = %d after reset, want 0", stats.Misses)
	}
	if stats.Sets != 0 {
		t.Errorf("Sets = %d after reset, want 0", stats.Sets)
	}
}

func TestKeys(t *testing.T) {
	c := newTestCache(t)

	c.Set("key1", "value1")
	c.Set("key2", "value2")
	c.Set("key3", "value3")

	keys := c.Keys()
	if len(keys) != 3 {
		t.Errorf("len(keys) = %d, want 3", len(keys))
	}

	keyMap := make(map[string]bool)
	for _, k := range keys {
		keyMap[k] = true
	}
	for _, expected := range []string{"key1", "key2", "key3"} {
		if !keyMap[expected] {
			t.Errorf("key %s not present", expected)
		}
	}
}

func TestStartCleanup(t *testing.T) {
	c := New(50 * time.Millisecond)
	c.Set("expiring", "value")
	c.StartCleanup(30 * time.Millisecond)
	t.Cleanup(c.Stop)

	time.Sleep(100 * time.Millisecond)

	for _, k := range c.Keys() {
		if k == "expiring" {
			t.Error("expected cleanup to remove expired key")
		}
	}
}

func TestStopIdempotent(t *testing.T) {
	c := newTestCache(t)
	c.StartCleanup(1 * time.Second)

	// Stop multiple times should not panic
	c.Stop()
	c.Stop()
	c.Stop()
}

func TestConcurrentAccess(t *testing.T) {
	c := newTestCache(t)

	var wg sync.WaitGroup
	numGoroutines, numOperations := 100, 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				c.Set("key", id*numOperations+j)
			}
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				c.Get("key")
			}
		}()
	}

	wg.Wait()
	requireKey(t, c, "key")
}

func TestDifferentValueTypes(t *testing.T) {
	c := newTestCache(t)

	type testStruct struct {
		Name  string
		Value int
	}

	tests := []struct {
		name     string
		key      string
		value    any
		validate func(any) bool
	}{
		{"string", "string", "hello", func(v any) bool { return v == "hello" }},
		{"int", "int", 42, func(v any) bool { return v == 42 }},
		{"struct", "struct", testStruct{Name: "test", Value: 123}, func(v any) bool {
			ts, ok := v.(testStruct)
			return ok && ts.Name == "test" && ts.Value == 123
		}},
		{"slice", "slice", []int{1, 2, 3}, func(v any) bool {
			s, ok := v.([]int)
			return ok && len(s) == 3
		}},
		{"map", "map", map[string]int{"a": 1, "b": 2}, func(v any) bool {
			m, ok := v.(map[string]int)
			return ok && m["a"] == 1 && m["b"] == 2
		}},
	}

	for _, tt := range tests {
		c.Set(tt.key, tt.value)
		val := requireKey(t, c, tt.key)
		if !tt.validate(val) {
			t.Errorf("%s value mismatch", tt.name)
		}
	}
}

func TestOverwrite(t *testing.T) {
	c := newTestCache(t)

	c.Set("key", "original")
	c.Set("key", "updated")

	val := requireKey(t, c, "key")
	if val != "updated" {
		t.Errorf("value = %v, want 'updated'", val)
	}
}

func TestEmptyPrefixDelete(t *testing.T) {
	c := newTestCache(t)

	c.Set("key1", "value1")
	c.Set("key2", "value2")
	c.DeleteByPrefix("") // Empty prefix should match all keys

	if len(c.Keys()) != 0 {
		t.Errorf("len(keys) = %d after empty prefix delete, want 0", len(c.Keys()))
	}
}

func TestStatsWithNoRequests(t *testing.T) {
	c := newTestCache(t)

	stats := c.Stats()
	if stats.Hits != 0 {
		t.Errorf("Hits = %d, want 0", stats.Hits)
	}
	if stats.Misses != 0 {
		t.Errorf("Misses = %d, want 0", stats.Misses)
	}
	if stats.HitRate != 0 {
		t.Errorf("HitRate = %f, want 0", stats.HitRate)
	}
}
