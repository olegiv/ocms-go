// Package cache provides an in-memory caching layer for oCMS.
package cache

import (
	"sync"
	"testing"
	"time"
)

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
	c := New(5 * time.Minute)
	defer c.Stop()

	// Test setting and getting a value
	c.Set("key1", "value1")

	val, found := c.Get("key1")
	if !found {
		t.Error("expected to find key1")
	}
	if val != "value1" {
		t.Errorf("expected value1, got %v", val)
	}
}

func TestGetMissing(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	val, found := c.Get("nonexistent")
	if found {
		t.Error("expected not to find nonexistent key")
	}
	if val != nil {
		t.Errorf("expected nil, got %v", val)
	}
}

func TestExpiration(t *testing.T) {
	c := New(50 * time.Millisecond)
	defer c.Stop()

	c.Set("expiring", "value")

	// Should exist immediately
	_, found := c.Get("expiring")
	if !found {
		t.Error("expected to find key immediately after setting")
	}

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	// Should be expired now
	_, found = c.Get("expiring")
	if found {
		t.Error("expected key to be expired")
	}
}

func TestSetWithTTL(t *testing.T) {
	c := New(5 * time.Minute) // default TTL
	defer c.Stop()

	// Set with custom short TTL
	c.SetWithTTL("short", "value", 50*time.Millisecond)

	// Set with custom long TTL
	c.SetWithTTL("long", "value", 1*time.Hour)

	// Short TTL should exist immediately
	_, found := c.Get("short")
	if !found {
		t.Error("expected short TTL key to exist immediately")
	}

	// Wait for short TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Short should be gone
	_, found = c.Get("short")
	if found {
		t.Error("expected short TTL key to be expired")
	}

	// Long should still exist
	_, found = c.Get("long")
	if !found {
		t.Error("expected long TTL key to still exist")
	}
}

func TestDelete(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	c.Set("to-delete", "value")

	// Verify it exists
	_, found := c.Get("to-delete")
	if !found {
		t.Error("expected key to exist before delete")
	}

	// Delete it
	c.Delete("to-delete")

	// Verify it's gone
	_, found = c.Get("to-delete")
	if found {
		t.Error("expected key to be deleted")
	}
}

func TestDeleteByPrefix(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	// Set multiple keys with same prefix
	c.Set("prefix:key1", "value1")
	c.Set("prefix:key2", "value2")
	c.Set("prefix:key3", "value3")
	c.Set("other:key", "other")

	// Delete by prefix
	c.DeleteByPrefix("prefix:")

	// Verify prefix keys are gone
	for _, key := range []string{"prefix:key1", "prefix:key2", "prefix:key3"} {
		_, found := c.Get(key)
		if found {
			t.Errorf("expected %s to be deleted", key)
		}
	}

	// Other key should still exist
	_, found := c.Get("other:key")
	if !found {
		t.Error("expected other:key to still exist")
	}
}

func TestClear(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	// Set multiple keys
	c.Set("key1", "value1")
	c.Set("key2", "value2")
	c.Set("key3", "value3")

	// Clear all
	c.Clear()

	// Verify all are gone
	for _, key := range []string{"key1", "key2", "key3"} {
		_, found := c.Get(key)
		if found {
			t.Errorf("expected %s to be cleared", key)
		}
	}
}

func TestStats(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	// Set some values
	c.Set("key1", "value1")
	c.Set("key2", "value2")

	// Hit
	c.Get("key1")
	c.Get("key1")

	// Miss
	c.Get("nonexistent")

	stats := c.Stats()

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

func TestResetStats(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	c.Set("key", "value")
	c.Get("key")
	c.Get("nonexistent")

	c.ResetStats()

	stats := c.Stats()

	if stats.Hits != 0 {
		t.Errorf("expected 0 hits after reset, got %d", stats.Hits)
	}

	if stats.Misses != 0 {
		t.Errorf("expected 0 misses after reset, got %d", stats.Misses)
	}

	if stats.Sets != 0 {
		t.Errorf("expected 0 sets after reset, got %d", stats.Sets)
	}
}

func TestKeys(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")
	c.Set("key2", "value2")
	c.Set("key3", "value3")

	keys := c.Keys()

	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}

	// Check all keys are present (order not guaranteed)
	keyMap := make(map[string]bool)
	for _, k := range keys {
		keyMap[k] = true
	}

	for _, expected := range []string{"key1", "key2", "key3"} {
		if !keyMap[expected] {
			t.Errorf("expected key %s to be present", expected)
		}
	}
}

func TestStartCleanup(t *testing.T) {
	c := New(50 * time.Millisecond)

	// Set an expiring key
	c.Set("expiring", "value")

	// Start cleanup with short interval
	c.StartCleanup(30 * time.Millisecond)
	defer c.Stop()

	// Wait for expiration and cleanup
	time.Sleep(100 * time.Millisecond)

	// Key should be gone (either by Get check or cleanup)
	keys := c.Keys()
	for _, k := range keys {
		if k == "expiring" {
			t.Error("expected cleanup to remove expired key")
		}
	}
}

func TestStopIdempotent(t *testing.T) {
	c := New(5 * time.Minute)
	c.StartCleanup(1 * time.Second)

	// Stop multiple times should not panic
	c.Stop()
	c.Stop()
	c.Stop()
}

func TestConcurrentAccess(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	var wg sync.WaitGroup
	numGoroutines := 100
	numOperations := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				c.Set("key", id*numOperations+j)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				c.Get("key")
			}
		}()
	}

	// Wait for all goroutines
	wg.Wait()

	// Should not panic and should have a value
	_, found := c.Get("key")
	if !found {
		t.Error("expected key to exist after concurrent access")
	}
}

func TestDifferentValueTypes(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	// String
	c.Set("string", "hello")
	val, found := c.Get("string")
	if !found || val != "hello" {
		t.Error("string value mismatch")
	}

	// Int
	c.Set("int", 42)
	val, found = c.Get("int")
	if !found || val != 42 {
		t.Error("int value mismatch")
	}

	// Struct
	type testStruct struct {
		Name  string
		Value int
	}
	c.Set("struct", testStruct{Name: "test", Value: 123})
	val, found = c.Get("struct")
	if !found {
		t.Error("struct not found")
	}
	ts, ok := val.(testStruct)
	if !ok || ts.Name != "test" || ts.Value != 123 {
		t.Error("struct value mismatch")
	}

	// Slice
	c.Set("slice", []int{1, 2, 3})
	val, found = c.Get("slice")
	if !found {
		t.Error("slice not found")
	}
	slice, ok := val.([]int)
	if !ok || len(slice) != 3 {
		t.Error("slice value mismatch")
	}

	// Map
	c.Set("map", map[string]int{"a": 1, "b": 2})
	val, found = c.Get("map")
	if !found {
		t.Error("map not found")
	}
	m, ok := val.(map[string]int)
	if !ok || m["a"] != 1 || m["b"] != 2 {
		t.Error("map value mismatch")
	}
}

func TestOverwrite(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	c.Set("key", "original")
	c.Set("key", "updated")

	val, found := c.Get("key")
	if !found {
		t.Error("expected key to exist")
	}
	if val != "updated" {
		t.Errorf("expected 'updated', got %v", val)
	}
}

func TestEmptyPrefixDelete(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")
	c.Set("key2", "value2")

	// Empty prefix should match all keys
	c.DeleteByPrefix("")

	keys := c.Keys()
	if len(keys) != 0 {
		t.Errorf("expected no keys after empty prefix delete, got %d", len(keys))
	}
}

func TestStatsWithNoRequests(t *testing.T) {
	c := New(5 * time.Minute)
	defer c.Stop()

	stats := c.Stats()

	if stats.Hits != 0 {
		t.Errorf("expected 0 hits, got %d", stats.Hits)
	}
	if stats.Misses != 0 {
		t.Errorf("expected 0 misses, got %d", stats.Misses)
	}
	if stats.HitRate != 0 {
		t.Errorf("expected 0 hit rate, got %f", stats.HitRate)
	}
}
