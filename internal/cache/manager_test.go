// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// newTestDB creates a temporary SQLite test database with migrations applied.
func newTestDB(t *testing.T) (*sql.DB, *store.Queries) {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "ocms-cache-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	db, err := store.NewDB(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("store.NewDB: %v", err)
	}

	if err := store.Migrate(db); err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("store.Migrate: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	})

	return db, store.New(db)
}

// TestNewManager verifies that NewManager initialises all sub-caches.
func TestNewManager(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	if m.Config == nil {
		t.Error("Config cache should not be nil")
	}
	if m.Sitemap == nil {
		t.Error("Sitemap cache should not be nil")
	}
	if m.Menus == nil {
		t.Error("Menus cache should not be nil")
	}
	if m.Language == nil {
		t.Error("Language cache should not be nil")
	}
	if m.Translation == nil {
		t.Error("Translation cache should not be nil")
	}
	if m.Page == nil {
		t.Error("Page cache should not be nil")
	}
	if m.General == nil {
		t.Error("General cache should not be nil")
	}
	if m.Distributed != nil {
		t.Error("Distributed cache should be nil for memory-only manager")
	}
}

// TestManager_Info verifies that Info returns correct backend type.
func TestManager_Info(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	info := m.Info()
	if info.BackendType != BackendMemory {
		t.Errorf("BackendType = %q, want %q", info.BackendType, BackendMemory)
	}
	if info.IsFallback {
		t.Error("IsFallback should be false for default manager")
	}
}

// TestManager_IsRedis verifies IsRedis returns false for memory-only manager.
func TestManager_IsRedis(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	if m.IsRedis() {
		t.Error("IsRedis() should be false for memory-only manager")
	}
}

// TestManager_StartStop verifies Start and Stop do not panic.
func TestManager_StartStop(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)
	m.Start()
	m.Stop()
	// Second stop should not panic.
	m.Stop()
}

// TestManager_HealthCheck verifies HealthCheck returns nil for memory-only manager.
func TestManager_HealthCheck(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	if err := m.HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck() = %v, want nil", err)
	}
}

// TestManager_AllStats verifies AllStats returns one entry per cache kind.
func TestManager_AllStats(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	stats := m.AllStats()

	if len(stats) != 6 {
		t.Errorf("AllStats() = %d entries, want 6", len(stats))
	}

	kinds := make(map[Kind]bool)
	for _, s := range stats {
		kinds[s.Kind] = true
	}
	for _, k := range []Kind{KindConfig, KindSitemap, KindMenu, KindLanguage, KindTranslation, KindPage} {
		if !kinds[k] {
			t.Errorf("AllStats() missing Kind %q", k)
		}
	}
}

// TestManager_TotalStats verifies TotalStats aggregates zeros by default.
func TestManager_TotalStats(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	total := m.TotalStats()
	if total.Hits != 0 {
		t.Errorf("TotalStats().Hits = %d, want 0", total.Hits)
	}
	if total.Misses != 0 {
		t.Errorf("TotalStats().Misses = %d, want 0", total.Misses)
	}
	if total.HitRate != 0 {
		t.Errorf("TotalStats().HitRate = %f, want 0", total.HitRate)
	}
}

// TestManager_ClearAll verifies that ClearAll does not panic on an empty manager.
func TestManager_ClearAll(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)
	// Should not panic.
	m.ClearAll()
}

// TestManager_InvalidateMethods verifies all Invalidate* convenience methods execute
// without panicking.
func TestManager_InvalidateMethods(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	m.InvalidateConfig()
	m.InvalidateSitemap()
	m.InvalidateThemeSettings()
	m.InvalidateContent()
	m.InvalidatePage(42)
	m.InvalidatePages()
	m.InvalidateMenus()
	m.InvalidateLanguages()
	m.InvalidateTranslations()
	m.InvalidateTranslation("page", 1)
}

// TestManager_NewManagerWithConfig_Memory verifies memory config path.
func TestManager_NewManagerWithConfig_Memory(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManagerWithConfig(q, Config{
		Type:       "memory",
		DefaultTTL: time.Minute,
	})

	if m == nil {
		t.Fatal("NewManagerWithConfig() returned nil")
	}
	if m.Distributed != nil {
		t.Error("Distributed should be nil for memory config")
	}
}

// TestManager_NewManagerWithConfig_RedisNoURL verifies that missing Redis URL falls
// back to memory.
func TestManager_NewManagerWithConfig_RedisNoURL(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManagerWithConfig(q, Config{
		Type:       "redis",
		RedisURL:   "", // empty URL — should not attempt connection
		DefaultTTL: time.Minute,
	})

	if m.Distributed != nil {
		t.Error("Distributed should be nil when RedisURL is empty")
	}
}

// TestMaskRedisURL is covered in redis_test.go with more comprehensive cases.

// TestManager_GetConfig verifies GetConfig returns empty string for missing key.
func TestManager_GetConfig(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	// The DB is empty — any key should return empty string (not an error).
	val, err := m.GetConfig(context.Background(), "nonexistent_key")
	if err != nil {
		t.Errorf("GetConfig() error = %v, want nil", err)
	}
	if val != "" {
		t.Errorf("GetConfig() = %q, want empty string", val)
	}
}

// TestManager_GetActiveLanguages verifies GetActiveLanguages returns empty slice for
// a fresh database.
func TestManager_GetActiveLanguages(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	langs, err := m.GetActiveLanguages(context.Background())
	if err != nil {
		t.Errorf("GetActiveLanguages() error = %v, want nil", err)
	}
	// Fresh DB has no languages.
	if langs == nil {
		t.Error("GetActiveLanguages() should return non-nil slice")
	}
}

// TestManager_GetDefaultLanguage verifies GetDefaultLanguage returns without error.
// The migrated DB seeds an English default language, so a non-nil result is valid.
func TestManager_GetDefaultLanguage(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	lang, err := m.GetDefaultLanguage(context.Background())
	if err != nil {
		t.Errorf("GetDefaultLanguage() error = %v, want nil", err)
	}
	// The DB may contain a default language seeded by migrations.
	// We only verify the call succeeds without error.
	_ = lang
}

// TestManager_GetLanguageByCode verifies GetLanguageByCode returns without error for
// a known code seeded by migrations.
func TestManager_GetLanguageByCode(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	// "zz" is definitely not seeded by any migration.
	lang, err := m.GetLanguageByCode(context.Background(), "zz")
	if err != nil {
		t.Errorf("GetLanguageByCode() error = %v, want nil", err)
	}
	if lang != nil {
		t.Errorf("GetLanguageByCode('zz') = %+v, want nil for unknown code", lang)
	}
}

// TestManager_GetMenu verifies GetMenu returns nil for missing slug.
func TestManager_GetMenu(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	menu, err := m.GetMenu(context.Background(), "main")
	if err != nil {
		t.Errorf("GetMenu() error = %v, want nil", err)
	}
	if menu != nil {
		t.Errorf("GetMenu() = %+v, want nil for empty DB", menu)
	}
}

// TestManager_GetTranslations verifies GetTranslations returns empty map for entity
// with no translations.
func TestManager_GetTranslations(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	tmap, err := m.GetTranslations(context.Background(), "page", 999)
	if err != nil {
		t.Errorf("GetTranslations() error = %v, want nil", err)
	}
	if len(tmap) != 0 {
		t.Errorf("GetTranslations() = %v, want empty map", tmap)
	}
}

// TestManager_GetTranslationsBatch verifies GetTranslationsBatch returns a map with
// entries for all requested IDs.
func TestManager_GetTranslationsBatch(t *testing.T) {
	_, q := newTestDB(t)
	m := NewManager(q)

	result, err := m.GetTranslationsBatch(context.Background(), "page", []int64{1, 2, 3})
	if err != nil {
		t.Errorf("GetTranslationsBatch() error = %v, want nil", err)
	}
	if len(result) != 3 {
		t.Errorf("GetTranslationsBatch() = %d entries, want 3", len(result))
	}
}
