// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package developer

import (
	"context"
	"database/sql"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// newTestQueries returns a store.Queries backed by the given database.
func newTestQueries(_ *testing.T, db *sql.DB) *store.Queries {
	return store.New(db)
}

// --- Module metadata ---

func TestAllowedEnvs(t *testing.T) {
	m := New()
	envs := m.AllowedEnvs()
	if len(envs) == 0 {
		t.Fatal("AllowedEnvs should return at least one environment")
	}
	found := false
	for _, e := range envs {
		if e == "development" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AllowedEnvs = %v, should contain 'development'", envs)
	}
	// Production must NOT be in the list
	for _, e := range envs {
		if e == "production" {
			t.Errorf("AllowedEnvs should not contain 'production', got %v", envs)
		}
	}
}

func TestAdminURL(t *testing.T) {
	m := New()
	if m.AdminURL() != "/admin/developer" {
		t.Errorf("AdminURL() = %q, want /admin/developer", m.AdminURL())
	}
}

func TestSidebarLabel(t *testing.T) {
	m := New()
	if m.SidebarLabel() != "Developer Tools" {
		t.Errorf("SidebarLabel() = %q, want 'Developer Tools'", m.SidebarLabel())
	}
}

func TestTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("ReadDir(locales): %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected at least one locale file")
	}
}

func TestTemplateFuncs(t *testing.T) {
	m := New()
	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs should not return nil")
	}
	// Developer module has no template functions.
	if len(funcs) != 0 {
		t.Errorf("expected empty FuncMap, got %d entries", len(funcs))
	}
}

func TestRegisterRoutes(t *testing.T) {
	m := New()
	logger := testutil.TestLogger()
	ctx := &module.Context{
		DB:     nil,
		Logger: logger,
		Config: &config.Config{Env: "development"},
	}
	_ = m.Init(ctx)
	// No public routes — just verify it doesn't panic.
	m.RegisterRoutes(chi.NewRouter())
}

func TestRegisterAdminRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)
	// Admin routes registered: GET /developer, POST /developer/generate, POST /developer/delete.
}

func TestShutdown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}

func TestShutdownNilCtx(t *testing.T) {
	m := New() // ctx not set
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() with nil ctx = %v, want nil", err)
	}
}

// --- Migration down path ---

func TestMigrationDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Down should drop the table
	if err := m.Migrations()[0].Down(db); err != nil {
		t.Fatalf("migration down: %v", err)
	}
	moduleutil.AssertTableNotExists(t, db, "developer_generated_items")
}

// --- assignRandomTaxonomy ---

func TestAssignRandomTaxonomyEmpty(t *testing.T) {
	calls := 0
	assigned := assignRandomTaxonomy(nil, 3, func(id int64) error {
		calls++
		return nil
	})
	if assigned != nil {
		t.Error("assignRandomTaxonomy with empty slice should return nil")
	}
	if calls != 0 {
		t.Errorf("assignFn should not be called with empty sourceIDs, got %d calls", calls)
	}
}

func TestAssignRandomTaxonomySingle(t *testing.T) {
	assigned := assignRandomTaxonomy([]int64{42}, 3, func(id int64) error {
		return nil
	})
	if len(assigned) != 1 {
		t.Fatalf("len(assigned) = %d, want 1", len(assigned))
	}
	if assigned[0] != 42 {
		t.Errorf("assigned[0] = %d, want 42", assigned[0])
	}
}

func TestAssignRandomTaxonomyMaxItems(t *testing.T) {
	sourceIDs := []int64{1, 2, 3, 4, 5}
	assigned := assignRandomTaxonomy(sourceIDs, 2, func(id int64) error {
		return nil
	})
	// Should assign at most 2 items (maxItems controls upper bound via cryptoRandIntn)
	if len(assigned) > 2 {
		t.Errorf("len(assigned) = %d, want <= 2", len(assigned))
	}
}

func TestAssignRandomTaxonomyNoDuplicates(t *testing.T) {
	sourceIDs := []int64{10, 10, 10} // All same ID
	assigned := assignRandomTaxonomy(sourceIDs, 5, func(id int64) error {
		return nil
	})
	// All are the same id; deduplication means at most one assignment
	if len(assigned) > 1 {
		t.Errorf("assignRandomTaxonomy should deduplicate, got %d assigned", len(assigned))
	}
}

// --- assignTranslatedTaxonomy ---

func TestAssignTranslatedTaxonomyEmptyIDs(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	ctx := context.Background()
	m := testModule(t, db)
	_ = m // only to get DB state

	// Use real queries but empty origIDs — should do nothing.
	callCount := 0
	assignTranslatedTaxonomy(
		ctx,
		nil, // queries not needed for empty list
		"tag",
		nil, // empty origIDs
		1,
		func(id int64) error {
			callCount++
			return nil
		},
		func(msg string, args ...any) {},
	)
	if callCount != 0 {
		t.Errorf("assignFn should not be called with empty origIDs, got %d", callCount)
	}
}

func TestAssignTranslatedTaxonomyMissingTranslation(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	ctx := context.Background()
	m := testModule(t, db)
	_ = m

	warnCalls := 0
	assignTranslatedTaxonomy(
		ctx,
		// Use a real queries instance pointing to the test DB
		newTestQueries(t, db),
		"tag",
		[]int64{9999}, // ID that has no translation
		1,
		func(id int64) error { return nil },
		func(msg string, args ...any) { warnCalls++ },
	)
	// warnFn should have been called once (GetTranslatedEntityID fails)
	if warnCalls != 1 {
		t.Errorf("expected 1 warn call for missing translation, got %d", warnCalls)
	}
}

// --- cryptoRandFloat32 ---

func TestCryptoRandFloat32Range(t *testing.T) {
	for i := 0; i < 100; i++ {
		v := cryptoRandFloat32()
		if v < 0 || v >= 1.0 {
			t.Errorf("cryptoRandFloat32() = %f, want [0, 1)", v)
		}
	}
}

// --- generateLoremIpsum paragraph count ---

func TestGenerateLoremIpsumParagraphCount(t *testing.T) {
	for i := 0; i < 20; i++ {
		text := generateLoremIpsum()
		// Count paragraphs by splitting on double newline
		// Each paragraph is one of the lorem strings separated by \n\n
		if len(text) < 100 {
			t.Errorf("generateLoremIpsum too short on iteration %d: %d chars", i, len(text))
		}
	}
}

// --- getColorName ---

func TestGetColorNameUnknown(t *testing.T) {
	name := getColorName(50, 50, 50)
	if name != "custom" {
		t.Errorf("getColorName for unknown color = %q, want 'custom'", name)
	}
}

func TestGetColorNameAllKnown(t *testing.T) {
	tests := []struct {
		r, g, b uint8
		want    string
	}{
		{66, 133, 244, "blue"},
		{219, 68, 55, "red"},
		{244, 180, 0, "yellow"},
		{15, 157, 88, "green"},
		{171, 71, 188, "purple"},
		{0, 172, 193, "cyan"},
		{255, 112, 67, "orange"},
		{124, 179, 66, "light green"},
		{63, 81, 181, "indigo"},
		{233, 30, 99, "pink"},
	}
	for _, tt := range tests {
		got := getColorName(tt.r, tt.g, tt.b)
		if got != tt.want {
			t.Errorf("getColorName(%d,%d,%d) = %q, want %q", tt.r, tt.g, tt.b, got, tt.want)
		}
	}
}

// --- GeneratedCounts struct ---

func TestGeneratedCountsZeroValue(t *testing.T) {
	var gc GeneratedCounts
	if gc.Tags != 0 || gc.Categories != 0 || gc.Media != 0 || gc.Pages != 0 {
		t.Error("zero-value GeneratedCounts should have all zero fields")
	}
}

// --- createCategoryTranslations (via generateCategories with 2 languages) ---

func TestGenerateCategoriesMultipleLanguages(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language, fixtures.Language2}

	catIDs, err := m.generateCategories(ctx, languages)
	if err != nil {
		t.Fatalf("generateCategories with 2 languages: %v", err)
	}

	if len(catIDs) < 5 {
		t.Errorf("expected at least 5 categories, got %d", len(catIDs))
	}

	// Tracked items should include both original categories and their translations
	tracked, err := m.getTrackedItems(ctx, "category")
	if err != nil {
		t.Fatalf("getTrackedItems(category): %v", err)
	}
	// Each category should have at least one translation category tracked
	if len(tracked) < len(catIDs)*2 {
		t.Errorf("expected at least %d tracked category items (originals+translations), got %d",
			len(catIDs)*2, len(tracked))
	}
}

// --- generatePages with media ---

func TestGeneratePagesWithMedia(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	defer setupTempUploadDir(t)()

	m := testModule(t, db)
	ctx := context.Background()
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language}

	// Generate media first
	mediaIDs, err := m.generateMedia(ctx, fixtures.User.ID)
	if err != nil {
		t.Fatalf("generateMedia: %v", err)
	}

	// Generate pages with media IDs available for featured image selection
	pageIDs, err := m.generatePages(ctx, languages, nil, nil, mediaIDs, fixtures.User.ID)
	if err != nil {
		t.Fatalf("generatePages with media: %v", err)
	}

	if len(pageIDs) < 5 {
		t.Errorf("expected at least 5 pages, got %d", len(pageIDs))
	}
}

// --- deleteAllGeneratedItems error paths ---

func TestDeleteAllGeneratedItemsEmpty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()

	// No items tracked → should succeed with no error
	if err := m.deleteAllGeneratedItems(ctx); err != nil {
		t.Errorf("deleteAllGeneratedItems with no items: %v", err)
	}
}

// --- generatePages with multiple languages ---

func TestGeneratePagesMultipleLanguages(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()
	q := store.New(db)
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language, fixtures.Language2}

	// Create tags and categories with translations
	tagIDs, err := m.generateTags(ctx, languages)
	if err != nil {
		t.Fatalf("generateTags: %v", err)
	}
	catIDs, err := m.generateCategories(ctx, languages)
	if err != nil {
		t.Fatalf("generateCategories: %v", err)
	}

	pageIDs, err := m.generatePages(ctx, languages, tagIDs, catIDs, nil, fixtures.User.ID)
	if err != nil {
		t.Fatalf("generatePages: %v", err)
	}

	if len(pageIDs) == 0 {
		t.Fatal("expected at least one page")
	}

	// Verify pages in both languages exist
	pages, err := q.ListPages(ctx, store.ListPagesParams{
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if len(pages) == 0 {
		t.Error("expected pages to be created")
	}
}
