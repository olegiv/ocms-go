// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func TestTypedCache_BasicOperations(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	user := &testUser{ID: 1, Name: "Alice", Email: "alice@example.com"}

	// Test Set and Get
	err := cache.Set(ctx, "user:1", user)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, found := cache.Get(ctx, "user:1")
	if !found {
		t.Fatal("expected to find user:1")
	}
	if got.ID != user.ID || got.Name != user.Name || got.Email != user.Email {
		t.Errorf("got %+v, want %+v", got, user)
	}
}

func TestTypedCache_CacheMiss(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	_, found := cache.Get(ctx, "nonexistent")
	if found {
		t.Error("expected not to find nonexistent key")
	}
}

func TestTypedCache_Delete(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	user := &testUser{ID: 1, Name: "Alice", Email: "alice@example.com"}
	_ = cache.Set(ctx, "user:1", user)

	err := cache.Delete(ctx, "user:1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, found := cache.Get(ctx, "user:1")
	if found {
		t.Error("expected user:1 to be deleted")
	}
}

func TestTypedCache_Has(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	user := &testUser{ID: 1, Name: "Alice", Email: "alice@example.com"}
	_ = cache.Set(ctx, "user:1", user)

	if !cache.Has(ctx, "user:1") {
		t.Error("expected user:1 to exist")
	}

	if cache.Has(ctx, "user:2") {
		t.Error("expected user:2 to not exist")
	}
}

func TestTypedCache_SetWithTTL(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	user := &testUser{ID: 1, Name: "Alice", Email: "alice@example.com"}

	// Set with short TTL
	err := cache.SetWithTTL(ctx, "user:1", user, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("SetWithTTL failed: %v", err)
	}

	// Should exist immediately
	if _, found := cache.Get(ctx, "user:1"); !found {
		t.Error("expected user:1 to exist immediately")
	}

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	// Should be expired
	if _, found := cache.Get(ctx, "user:1"); found {
		t.Error("expected user:1 to be expired")
	}
}

func TestTypedCache_GetOrSet(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	callCount := 0
	loader := func() (*testUser, error) {
		callCount++
		return &testUser{ID: 1, Name: "Alice", Email: "alice@example.com"}, nil
	}

	// First call should invoke the loader
	user, err := cache.GetOrSet(ctx, "user:1", loader)
	if err != nil {
		t.Fatalf("GetOrSet failed: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected loader to be called once, got %d", callCount)
	}
	if user.ID != 1 {
		t.Errorf("expected user ID 1, got %d", user.ID)
	}

	// Second call should use cached value
	user2, err := cache.GetOrSet(ctx, "user:1", loader)
	if err != nil {
		t.Fatalf("GetOrSet failed: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected loader to still be called once, got %d", callCount)
	}
	if user2.ID != 1 {
		t.Errorf("expected user ID 1, got %d", user2.ID)
	}
}

func TestTypedCache_GetOrSetError(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	expectedErr := errors.New("database error")
	loader := func() (*testUser, error) {
		return nil, expectedErr
	}

	_, err := cache.GetOrSet(ctx, "user:1", loader)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}

	// Key should not be cached after error
	if cache.Has(ctx, "user:1") {
		t.Error("expected key to not be cached after error")
	}
}

func TestMultiTypedCache_GetMultiple(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewMultiTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	// Set some users
	_ = cache.Set(ctx, "user:1", &testUser{ID: 1, Name: "Alice", Email: "alice@example.com"})
	_ = cache.Set(ctx, "user:2", &testUser{ID: 2, Name: "Bob", Email: "bob@example.com"})

	// Get multiple
	result := cache.GetMultiple(ctx, []string{"user:1", "user:2", "user:3"})

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}

	user1, ok := result["user:1"]
	if !ok || user1 == nil {
		t.Fatalf("expected user:1 to exist")
	}
	if user1.Name != "Alice" {
		t.Errorf("expected Alice, got %s", user1.Name)
	}

	user2, ok := result["user:2"]
	if !ok || user2 == nil {
		t.Fatalf("expected user:2 to exist")
	}
	if user2.Name != "Bob" {
		t.Errorf("expected Bob, got %s", user2.Name)
	}

	if _, exists := result["user:3"]; exists {
		t.Error("expected user:3 to not exist")
	}
}

func TestMultiTypedCache_SetMultiple(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewMultiTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	items := map[string]*testUser{
		"user:1": {ID: 1, Name: "Alice", Email: "alice@example.com"},
		"user:2": {ID: 2, Name: "Bob", Email: "bob@example.com"},
	}

	err := cache.SetMultiple(ctx, items)
	if err != nil {
		t.Fatalf("SetMultiple failed: %v", err)
	}

	// Verify all were set
	if !cache.Has(ctx, "user:1") {
		t.Error("expected user:1 to exist")
	}
	if !cache.Has(ctx, "user:2") {
		t.Error("expected user:2 to exist")
	}
}

func TestMultiTypedCache_DeleteMultiple(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	cache := NewMultiTypedCache[testUser](memCache, time.Hour)
	ctx := context.Background()

	// Set some users
	_ = cache.Set(ctx, "user:1", &testUser{ID: 1, Name: "Alice", Email: "alice@example.com"})
	_ = cache.Set(ctx, "user:2", &testUser{ID: 2, Name: "Bob", Email: "bob@example.com"})
	_ = cache.Set(ctx, "user:3", &testUser{ID: 3, Name: "Charlie", Email: "charlie@example.com"})

	// Delete multiple
	err := cache.DeleteMultiple(ctx, []string{"user:1", "user:2"})
	if err != nil {
		t.Fatalf("DeleteMultiple failed: %v", err)
	}

	// Verify deleted
	if cache.Has(ctx, "user:1") {
		t.Error("expected user:1 to be deleted")
	}
	if cache.Has(ctx, "user:2") {
		t.Error("expected user:2 to be deleted")
	}

	// Verify user:3 still exists
	if !cache.Has(ctx, "user:3") {
		t.Error("expected user:3 to still exist")
	}
}

func TestTypedCache_ComplexType(t *testing.T) {
	memCache := NewSimpleMemoryCache(time.Hour)
	defer func() { _ = memCache.Close() }()

	type complexType struct {
		Users    []testUser        `json:"users"`
		Metadata map[string]string `json:"metadata"`
		Count    int               `json:"count"`
	}

	cache := NewTypedCache[complexType](memCache, time.Hour)
	ctx := context.Background()

	complexVal := &complexType{
		Users: []testUser{
			{ID: 1, Name: "Alice", Email: "alice@example.com"},
			{ID: 2, Name: "Bob", Email: "bob@example.com"},
		},
		Metadata: map[string]string{"key": "value"},
		Count:    42,
	}

	err := cache.Set(ctx, "complex", complexVal)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, found := cache.Get(ctx, "complex")
	if !found {
		t.Fatal("expected to find complex")
	}

	if len(got.Users) != 2 {
		t.Errorf("expected 2 users, got %d", len(got.Users))
	}
	if got.Metadata["key"] != "value" {
		t.Errorf("expected metadata key=value, got %v", got.Metadata)
	}
	if got.Count != 42 {
		t.Errorf("expected count 42, got %d", got.Count)
	}
}
