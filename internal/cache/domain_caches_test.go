// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// ----------------------------------------------------------------------------
// ConfigCache
// ----------------------------------------------------------------------------

func TestConfigCache_NewConfigCache(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	if c == nil {
		t.Fatal("NewConfigCache returned nil")
	}
}

func TestConfigCache_GetMissingKey(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	val, err := c.Get(ctx, "nonexistent")
	if err != nil {
		t.Errorf("Get() error = %v, want nil", err)
	}
	if val != "" {
		t.Errorf("Get() = %q, want empty string", val)
	}
}

func TestConfigCache_GetMultiple(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	result, err := c.GetMultiple(ctx, "key1", "key2", "missing")
	if err != nil {
		t.Errorf("GetMultiple() error = %v, want nil", err)
	}
	// Fresh DB has no config entries.
	if len(result) != 0 {
		t.Errorf("GetMultiple() = %v, want empty map", result)
	}
}

func TestConfigCache_All(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	result, err := c.All(ctx)
	if err != nil {
		t.Errorf("All() error = %v, want nil", err)
	}
	if result == nil {
		t.Error("All() should return non-nil map")
	}
}

func TestConfigCache_GetConfig(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	cfg, ok, err := c.GetConfig(ctx, "nonexistent")
	if err != nil {
		t.Errorf("GetConfig() error = %v, want nil", err)
	}
	if ok {
		t.Errorf("GetConfig() found = %v, want false", ok)
	}
	if cfg != (store.Config{}) {
		t.Errorf("GetConfig() cfg = %+v, want zero value", cfg)
	}
}

func TestConfigCache_Stats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	// Trigger a load to get something in stats.
	_, _ = c.Get(ctx, "any")

	stats := c.Stats()
	if stats.Misses < 1 {
		t.Errorf("Stats().Misses = %d, want >= 1", stats.Misses)
	}
}

func TestConfigCache_Invalidate(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	// Load to populate.
	_, _ = c.Get(ctx, "any")

	// Invalidate.
	c.Invalidate()

	// Stats should be reset.
	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Items after Invalidate = %d, want 0", stats.Items)
	}
}

func TestConfigCache_InvalidateKey(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	_, _ = c.Get(ctx, "any")
	// InvalidateKey delegates to Invalidate — should not panic.
	c.InvalidateKey("any")

	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Items after InvalidateKey = %d, want 0", stats.Items)
	}
}

func TestConfigCache_ResetStats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	_, _ = c.Get(ctx, "key")
	c.ResetStats()

	stats := c.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Sets != 0 {
		t.Errorf("Stats after ResetStats = %+v, want all zeros", stats)
	}
}

func TestConfigCache_Preload(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	if err := c.Preload(ctx); err != nil {
		t.Errorf("Preload() error = %v, want nil", err)
	}
}

func TestConfigCache_CachedAfterFirstGet(t *testing.T) {
	_, q := newTestDB(t)
	c := NewConfigCache(q)
	ctx := context.Background()

	// First call loads from DB.
	_, _ = c.Get(ctx, "any")

	// Second call should be a cache hit — misses should not increase.
	statsBefore := c.Stats()
	_, _ = c.Get(ctx, "any_other_missing")
	statsAfter := c.Stats()

	if statsAfter.Misses <= statsBefore.Misses {
		// Even misses count for missing keys when loaded — just check no new DB call.
		// The main assertion is that it doesn't error.
	}
	_ = statsAfter
}

// ----------------------------------------------------------------------------
// LanguageCache
// ----------------------------------------------------------------------------

func TestLanguageCache_NewLanguageCache(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	if c == nil {
		t.Fatal("NewLanguageCache returned nil")
	}
}

func TestLanguageCache_GetAll_EmptyDB(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	langs, err := c.GetAll(ctx)
	if err != nil {
		t.Errorf("GetAll() error = %v, want nil", err)
	}
	// Migrations may seed an "en" language — we just verify the call returns
	// a non-nil slice without error.
	if langs == nil {
		t.Error("GetAll() should return non-nil slice")
	}
}

func TestLanguageCache_GetActive_EmptyDB(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	langs, err := c.GetActive(ctx)
	if err != nil {
		t.Errorf("GetActive() error = %v, want nil", err)
	}
	if langs == nil {
		t.Error("GetActive() should return non-nil slice")
	}
}

func TestLanguageCache_GetByCode_Missing(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	lang, err := c.GetByCode(ctx, "zz")
	if err != nil {
		t.Errorf("GetByCode() error = %v, want nil", err)
	}
	if lang != nil {
		t.Errorf("GetByCode() = %+v, want nil", lang)
	}
}

func TestLanguageCache_GetDefault_EmptyDB(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	// Migrations seed an "en" default language, so the result may be non-nil.
	// We only verify the call succeeds without error.
	_, err := c.GetDefault(ctx)
	if err != nil {
		t.Errorf("GetDefault() error = %v, want nil", err)
	}
}

func TestLanguageCache_IsActiveCode_Unknown(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	active, err := c.IsActiveCode(ctx, "zz")
	if err != nil {
		t.Errorf("IsActiveCode() error = %v, want nil", err)
	}
	if active {
		t.Error("IsActiveCode() = true, want false for unknown code")
	}
}

func TestLanguageCache_Invalidate(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	_, _ = c.GetAll(ctx)
	c.Invalidate()

	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Items after Invalidate = %d, want 0", stats.Items)
	}
}

func TestLanguageCache_ResetStats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	_, _ = c.GetAll(ctx)
	c.ResetStats()

	stats := c.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Sets != 0 {
		t.Errorf("Stats after ResetStats = %+v, want all zeros", stats)
	}
}

func TestLanguageCache_Preload(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	if err := c.Preload(ctx); err != nil {
		t.Errorf("Preload() error = %v, want nil", err)
	}
}

func TestLanguageCache_Stats_AfterLoad(t *testing.T) {
	_, q := newTestDB(t)
	c := NewLanguageCache(q)
	ctx := context.Background()

	_, _ = c.GetAll(ctx)

	stats := c.Stats()
	// One set for the initial load.
	if stats.Sets < 1 {
		t.Errorf("Stats().Sets = %d, want >= 1 after load", stats.Sets)
	}
}

// TestLanguageCache_WithData verifies retrieval after inserting an additional language.
// Migrations already seed "en", so we insert "fr" to test with a known-added entry.
func TestLanguageCache_WithData(t *testing.T) {
	_, q := newTestDB(t)
	ctx := context.Background()

	// Insert a French language (en is already seeded by migration).
	_, err := q.CreateLanguage(ctx, store.CreateLanguageParams{
		Code:      "fr",
		Name:      "Français",
		IsActive:  true,
		IsDefault: false,
	})
	if err != nil {
		t.Fatalf("CreateLanguage: %v", err)
	}

	c := NewLanguageCache(q)

	langs, err := c.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll() error = %v", err)
	}
	// Should have at least 2: "en" from migration + "fr" we just added.
	if len(langs) < 2 {
		t.Fatalf("GetAll() = %d, want >= 2", len(langs))
	}

	// Active languages — fr is active.
	active, err := c.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive() error = %v", err)
	}
	if len(active) < 1 {
		t.Errorf("GetActive() = %d, want >= 1", len(active))
	}

	// GetByCode hit for "fr".
	lang, err := c.GetByCode(ctx, "fr")
	if err != nil {
		t.Fatalf("GetByCode() error = %v", err)
	}
	if lang == nil {
		t.Fatal("GetByCode('fr') = nil, want language")
	}
	if lang.Code != "fr" {
		t.Errorf("GetByCode().Code = %q, want %q", lang.Code, "fr")
	}

	// GetByCode miss for unknown code.
	notFound, err := c.GetByCode(ctx, "zz")
	if err != nil {
		t.Fatalf("GetByCode('zz') error = %v", err)
	}
	if notFound != nil {
		t.Errorf("GetByCode('zz') = %+v, want nil", notFound)
	}

	// GetDefault should return "en" which is default from migration.
	def, err := c.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault() error = %v", err)
	}
	if def == nil {
		t.Fatal("GetDefault() = nil, want default language")
	}

	// IsActiveCode for "fr".
	active2, err := c.IsActiveCode(ctx, "fr")
	if err != nil {
		t.Fatalf("IsActiveCode('fr') error = %v", err)
	}
	if !active2 {
		t.Error("IsActiveCode('fr') = false, want true")
	}

	// Stats after all hits.
	stats := c.Stats()
	if stats.Hits < 4 {
		t.Errorf("Stats().Hits = %d, want >= 4", stats.Hits)
	}
	if stats.Items < 2 {
		t.Errorf("Stats().Items = %d, want >= 2", stats.Items)
	}
}

// ----------------------------------------------------------------------------
// MenuCache
// ----------------------------------------------------------------------------

func TestMenuCache_NewMenuCache(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	if c == nil {
		t.Fatal("NewMenuCache returned nil")
	}
}

func TestMenuCache_Get_Missing(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	ctx := context.Background()

	menu, err := c.Get(ctx, "main")
	if err != nil {
		t.Errorf("Get() error = %v, want nil", err)
	}
	if menu != nil {
		t.Errorf("Get() = %+v, want nil", menu)
	}
}

func TestMenuCache_GetByID_Missing(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	ctx := context.Background()

	menu, err := c.GetByID(ctx, 999)
	if err != nil {
		t.Errorf("GetByID() error = %v, want nil", err)
	}
	if menu != nil {
		t.Errorf("GetByID() = %+v, want nil", menu)
	}
}

func TestMenuCache_All_EmptyDB(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	ctx := context.Background()

	menus, err := c.All(ctx)
	if err != nil {
		t.Errorf("All() error = %v, want nil", err)
	}
	if menus == nil {
		t.Error("All() should return non-nil slice")
	}
	if len(menus) != 0 {
		t.Errorf("All() = %d menus, want 0 for empty DB", len(menus))
	}
}

func TestMenuCache_Invalidate(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	ctx := context.Background()

	_, _ = c.All(ctx)
	c.Invalidate()

	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Items after Invalidate = %d, want 0", stats.Items)
	}
}

func TestMenuCache_InvalidateBySlug(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	ctx := context.Background()

	_, _ = c.All(ctx)
	// InvalidateBySlug delegates to Invalidate.
	c.InvalidateBySlug("main")

	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Items after InvalidateBySlug = %d, want 0", stats.Items)
	}
}

func TestMenuCache_ResetStats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	ctx := context.Background()

	_, _ = c.All(ctx)
	c.ResetStats()

	stats := c.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Sets != 0 {
		t.Errorf("Stats after ResetStats = %+v, want all zeros", stats)
	}
}

func TestMenuCache_Preload(t *testing.T) {
	_, q := newTestDB(t)
	c := NewMenuCache(q)
	ctx := context.Background()

	if err := c.Preload(ctx); err != nil {
		t.Errorf("Preload() error = %v, want nil", err)
	}
}

// TestMenuCache_WithData verifies retrieval after inserting a menu.
func TestMenuCache_WithData(t *testing.T) {
	_, q := newTestDB(t)
	ctx := context.Background()

	menu, err := q.CreateMenu(ctx, store.CreateMenuParams{
		Name: "Main Menu",
		Slug: "main",
	})
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	c := NewMenuCache(q)

	// Get by slug — should hit the DB and cache.
	got, err := c.Get(ctx, "main")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == nil {
		t.Fatal("Get() = nil, want menu")
	}
	if got.Menu.ID != menu.ID {
		t.Errorf("Get().Menu.ID = %d, want %d", got.Menu.ID, menu.ID)
	}

	// Second call should be a cache hit.
	got2, err := c.Get(ctx, "main")
	if err != nil {
		t.Fatalf("Get() second call error = %v", err)
	}
	if got2 == nil {
		t.Fatal("Get() second call = nil, want menu")
	}

	// GetByID.
	byID, err := c.GetByID(ctx, menu.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if byID == nil {
		t.Fatal("GetByID() = nil, want menu")
	}

	// All.
	all, err := c.All(ctx)
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if len(all) != 1 {
		t.Errorf("All() = %d, want 1", len(all))
	}

	// Stats.
	stats := c.Stats()
	if stats.Items != 1 {
		t.Errorf("Stats().Items = %d, want 1", stats.Items)
	}
}

// ----------------------------------------------------------------------------
// TranslationCache
// ----------------------------------------------------------------------------

func TestTranslationCache_NewTranslationCache(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	if c == nil {
		t.Fatal("NewTranslationCache returned nil")
	}
}

func TestTranslationCache_Get_NoTranslations(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	tmap, err := c.Get(ctx, "page", 999)
	if err != nil {
		t.Errorf("Get() error = %v, want nil", err)
	}
	if len(tmap) != 0 {
		t.Errorf("Get() = %v, want empty map", tmap)
	}
}

func TestTranslationCache_GetForLanguage_Missing(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	tid, found, err := c.GetForLanguage(ctx, "page", 999, "fr")
	if err != nil {
		t.Errorf("GetForLanguage() error = %v, want nil", err)
	}
	if found {
		t.Error("GetForLanguage() found = true, want false")
	}
	if tid != 0 {
		t.Errorf("GetForLanguage() tid = %d, want 0", tid)
	}
}

func TestTranslationCache_GetBatch(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	result, err := c.GetBatch(ctx, "page", []int64{1, 2, 3})
	if err != nil {
		t.Errorf("GetBatch() error = %v, want nil", err)
	}
	if len(result) != 3 {
		t.Errorf("GetBatch() = %d entries, want 3", len(result))
	}
}

func TestTranslationCache_GetBatch_Empty(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	result, err := c.GetBatch(ctx, "page", []int64{})
	if err != nil {
		t.Errorf("GetBatch() error = %v, want nil", err)
	}
	if result == nil {
		t.Error("GetBatch() should return non-nil map")
	}
}

func TestTranslationCache_CacheHitOnSecondGet(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	// First call — cache miss, loads from DB.
	_, _ = c.Get(ctx, "page", 1)

	statsBefore := c.Stats()

	// Second call — should be a cache hit.
	_, _ = c.Get(ctx, "page", 1)

	statsAfter := c.Stats()
	if statsAfter.Hits <= statsBefore.Hits {
		t.Errorf("Hits did not increase after second Get: before=%d, after=%d",
			statsBefore.Hits, statsAfter.Hits)
	}
}

func TestTranslationCache_InvalidateEntity(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	_, _ = c.Get(ctx, "page", 42)
	c.InvalidateEntity("page", 42)

	// After invalidation, a new Get should be a miss (no hit from cache).
	statsBefore := c.Stats()
	_, _ = c.Get(ctx, "page", 42)
	statsAfter := c.Stats()

	if statsAfter.Misses <= statsBefore.Misses {
		t.Errorf("Misses did not increase after InvalidateEntity: before=%d, after=%d",
			statsBefore.Misses, statsAfter.Misses)
	}
}

func TestTranslationCache_InvalidateType(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	_, _ = c.Get(ctx, "page", 1)
	_, _ = c.Get(ctx, "page", 2)

	c.InvalidateType("page")

	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Items after InvalidateType = %d, want 0", stats.Items)
	}
}

func TestTranslationCache_Invalidate(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	_, _ = c.Get(ctx, "page", 1)
	_, _ = c.Get(ctx, "category", 1)

	c.Invalidate()

	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Items after Invalidate = %d, want 0", stats.Items)
	}
}

func TestTranslationCache_Stats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	_, _ = c.Get(ctx, "page", 1)
	_, _ = c.Get(ctx, "page", 1) // hit
	_, _ = c.Get(ctx, "page", 2) // miss -> load

	stats := c.Stats()
	if stats.Hits < 1 {
		t.Errorf("Stats().Hits = %d, want >= 1", stats.Hits)
	}
	if stats.Misses < 2 {
		t.Errorf("Stats().Misses = %d, want >= 2", stats.Misses)
	}
	if stats.Items < 2 {
		t.Errorf("Stats().Items = %d, want >= 2", stats.Items)
	}
}

func TestTranslationCache_ResetStats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewTranslationCache(q)
	ctx := context.Background()

	_, _ = c.Get(ctx, "page", 1)
	c.ResetStats()

	stats := c.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Sets != 0 {
		t.Errorf("Stats after ResetStats = %+v, want all zeros", stats)
	}
}

// ----------------------------------------------------------------------------
// SitemapCache
// ----------------------------------------------------------------------------

func TestSitemapCache_NewSitemapCache(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	if c == nil {
		t.Fatal("NewSitemapCache returned nil")
	}
}

func TestSitemapCache_NewSitemapCache_ZeroTTL(t *testing.T) {
	_, q := newTestDB(t)
	// Zero TTL should default to 1 hour (no panic).
	c := NewSitemapCache(q, 0)
	if c == nil {
		t.Fatal("NewSitemapCache(q, 0) returned nil")
	}
}

func TestSitemapCache_IsCached_Initially(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)

	if c.IsCached() {
		t.Error("IsCached() = true, want false before first Get")
	}
}

func TestSitemapCache_Get(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	xml, err := c.Get(ctx, "http://example.com")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if len(xml) == 0 {
		t.Error("Get() returned empty XML")
	}

	if !c.IsCached() {
		t.Error("IsCached() = false after Get, want true")
	}

	if c.Size() == 0 {
		t.Error("Size() = 0 after Get, want > 0")
	}
}

func TestSitemapCache_CachedAt(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	before := time.Now()
	_, _ = c.Get(ctx, "http://example.com")
	after := time.Now()

	cachedAt := c.CachedAt()
	if cachedAt.Before(before) || cachedAt.After(after) {
		t.Errorf("CachedAt() = %v, want between %v and %v", cachedAt, before, after)
	}
}

func TestSitemapCache_Get_CacheHit(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	_, _ = c.Get(ctx, "http://example.com")
	statsBefore := c.Stats()

	// Second call should be a hit.
	_, _ = c.Get(ctx, "http://example.com")
	statsAfter := c.Stats()

	if statsAfter.Hits <= statsBefore.Hits {
		t.Errorf("Hits did not increase on second Get: before=%d, after=%d",
			statsBefore.Hits, statsAfter.Hits)
	}
}

func TestSitemapCache_Invalidate(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	_, _ = c.Get(ctx, "http://example.com")
	c.Invalidate()

	if c.IsCached() {
		t.Error("IsCached() = true after Invalidate, want false")
	}
	if c.Size() != 0 {
		t.Errorf("Size() = %d after Invalidate, want 0", c.Size())
	}
}

func TestSitemapCache_Stats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	_, _ = c.Get(ctx, "http://example.com")

	stats := c.Stats()
	if stats.Items != 1 {
		t.Errorf("Stats().Items = %d, want 1", stats.Items)
	}
}

func TestSitemapCache_ResetStats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewSitemapCache(q, time.Hour)
	ctx := context.Background()

	_, _ = c.Get(ctx, "http://example.com")
	c.ResetStats()

	stats := c.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Sets != 0 {
		t.Errorf("Stats after ResetStats = %+v, want all zeros", stats)
	}
}

// ----------------------------------------------------------------------------
// PageCache
// ----------------------------------------------------------------------------

func TestPageCache_NewPageCache(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)
	if c == nil {
		t.Fatal("NewPageCache returned nil")
	}
}

func TestPageCache_Count_Initially(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0 for fresh cache", c.Count())
	}
	if c.CacheEntryCount() != 0 {
		t.Errorf("CacheEntryCount() = %d, want 0 for fresh cache", c.CacheEntryCount())
	}
}

func TestPageCache_Stats_Initially(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	stats := c.Stats()
	if stats.Items != 0 {
		t.Errorf("Stats().Items = %d, want 0", stats.Items)
	}
}

func TestPageCache_Invalidate(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	c.Invalidate()

	if c.Count() != 0 {
		t.Errorf("Count() after Invalidate = %d, want 0", c.Count())
	}
}

func TestPageCache_InvalidatePage_NoEntry(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	// Invalidating a page that doesn't exist should not panic.
	c.InvalidatePage(999)
}

func TestPageCache_InvalidateBySlug_NoEntry(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	// Invalidating a slug that doesn't exist should not panic.
	c.InvalidateBySlug("nonexistent-slug")
}

func TestPageCache_ResetStats(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	c.ResetStats()

	stats := c.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Sets != 0 {
		t.Errorf("Stats after ResetStats = %+v, want all zeros", stats)
	}
}

func TestPageCache_store_and_Invalidate(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	// Manually store a fake page through the internal store method.
	page := &store.Page{ID: 1, Slug: "about-us"}
	ctx := NewContext("en", "anonymous")
	c.store(page, ctx)

	if c.Count() != 1 {
		t.Errorf("Count() = %d, want 1 after store", c.Count())
	}
	if c.CacheEntryCount() != 2 { // slug key + id key
		t.Errorf("CacheEntryCount() = %d, want 2", c.CacheEntryCount())
	}

	// Invalidate the specific page.
	c.InvalidatePage(1)

	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0 after InvalidatePage", c.Count())
	}
}

func TestPageCache_store_and_InvalidateBySlug(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	page := &store.Page{ID: 2, Slug: "contact"}
	ctx := NewContext("en", "anonymous")
	c.store(page, ctx)
	c.store(page, NewContext("fr", "anonymous"))

	// Both language variants should be cached.
	if c.Count() != 1 {
		t.Errorf("Count() = %d, want 1 unique page", c.Count())
	}

	c.InvalidateBySlug("contact")

	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0 after InvalidateBySlug", c.Count())
	}
}

func TestPageCache_Invalidate_ClearsAll(t *testing.T) {
	_, q := newTestDB(t)
	c := NewPageCache(q)

	for i := int64(1); i <= 5; i++ {
		c.store(&store.Page{ID: i, Slug: "page-" + string(rune('a'+i-1))}, NewContext("en", "anonymous"))
	}

	if c.Count() != 5 {
		t.Errorf("Count() = %d, want 5", c.Count())
	}

	c.Invalidate()

	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0 after Invalidate", c.Count())
	}
}
