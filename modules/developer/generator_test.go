package developer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ocms-go/internal/module"
	"ocms-go/internal/store"
	"ocms-go/internal/testutil"
	"ocms-go/internal/testutil/moduleutil"
)

// setupTempUploadDir creates a temporary directory for uploads and changes to it.
// Returns a cleanup function that restores the original working directory.
func setupTempUploadDir(t *testing.T) func() {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "ocms-upload-test-*")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("getting working directory: %v", err)
	}

	if err := os.Chdir(tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("changing to temp dir: %v", err)
	}

	return func() {
		_ = os.Chdir(oldWd)
		_ = os.RemoveAll(tmpDir)
	}
}

// testModule creates a test Module with database access.
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()
	m := New()
	logger := testutil.TestLogger()
	ctx := &module.Context{
		DB:     db,
		Logger: logger,
	}
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	moduleutil.RunMigrations(t, db, m.Migrations())
	return m
}

// testFixtures holds common test data needed for generator tests
type testFixtures struct {
	Language  store.Language
	Language2 store.Language
	User      store.User
	Menu      store.Menu
}

// createTestFixtures creates the necessary test fixtures for generator tests
func createTestFixtures(t *testing.T, db *sql.DB) *testFixtures {
	t.Helper()

	ctx := context.Background()
	q := store.New(db)
	now := time.Now()

	// Try to get existing default language (seeded by migrations)
	lang, err := q.GetLanguageByCode(ctx, "en")
	if err != nil {
		// Create default language if not exists
		lang, err = q.CreateLanguage(ctx, store.CreateLanguageParams{
			Code:      "en",
			Name:      "English",
			IsDefault: true,
			IsActive:  true,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateLanguage (en): %v", err)
		}
	}

	// Create second language (use unique code to avoid conflicts)
	lang2, err := q.CreateLanguage(ctx, store.CreateLanguageParams{
		Code:      "xx",
		Name:      "Test Language",
		IsDefault: false,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateLanguage (xx): %v", err)
	}

	// Create user
	user, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:        "devtest@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Dev Test User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Try to get existing main menu (seeded by migrations) or create
	menu, err := q.GetMenuBySlug(ctx, "main")
	if err != nil {
		menu, err = q.CreateMenu(ctx, store.CreateMenuParams{
			Name:      "Main Menu",
			Slug:      "main",
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateMenu: %v", err)
		}
	}

	return &testFixtures{
		Language:  lang,
		Language2: lang2,
		User:      user,
		Menu:      menu,
	}
}

func TestGenerateRandomCount(t *testing.T) {
	// Test that generateRandomCount returns values between 5 and 20
	for i := 0; i < 100; i++ {
		count := generateRandomCount()
		if count < 5 || count > 20 {
			t.Errorf("generateRandomCount() = %d, want 5-20", count)
		}
	}
}

func TestRandomElement(t *testing.T) {
	slice := []string{"a", "b", "c"}

	// Test that randomElement returns elements from the slice
	for i := 0; i < 100; i++ {
		elem := randomElement(slice)
		found := false
		for _, s := range slice {
			if s == elem {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("randomElement returned %q which is not in the slice", elem)
		}
	}
}

func TestGenerateLoremIpsum(t *testing.T) {
	text := generateLoremIpsum()

	// Should contain at least one paragraph
	if len(text) == 0 {
		t.Error("generateLoremIpsum returned empty string")
	}

	// Should contain Lorem ipsum text
	if len(text) < 100 {
		t.Errorf("generateLoremIpsum returned too short text: %d chars", len(text))
	}
}

func TestCreatePlaceholderImage(t *testing.T) {
	// Test creating a placeholder image
	data, err := createPlaceholderImage(100, 100, 255, 0, 0)
	if err != nil {
		t.Fatalf("createPlaceholderImage: %v", err)
	}

	// Should return JPEG data
	if len(data) == 0 {
		t.Error("createPlaceholderImage returned empty data")
	}

	// JPEG files start with 0xFF 0xD8
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Error("createPlaceholderImage did not return valid JPEG data")
	}
}

func TestGetColorName(t *testing.T) {
	tests := []struct {
		r, g, b uint8
		want    string
	}{
		{66, 133, 244, "blue"},
		{219, 68, 55, "red"},
		{244, 180, 0, "yellow"},
		{15, 157, 88, "green"},
		{171, 71, 188, "purple"},
		{100, 100, 100, "custom"}, // Unknown color
	}

	for _, tt := range tests {
		got := getColorName(tt.r, tt.g, tt.b)
		if got != tt.want {
			t.Errorf("getColorName(%d, %d, %d) = %q, want %q", tt.r, tt.g, tt.b, got, tt.want)
		}
	}
}

func TestTrackItem(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()

	// Track some items
	if err := m.trackItem(ctx, "tag", 1); err != nil {
		t.Fatalf("trackItem: %v", err)
	}
	if err := m.trackItem(ctx, "tag", 2); err != nil {
		t.Fatalf("trackItem: %v", err)
	}
	if err := m.trackItem(ctx, "category", 1); err != nil {
		t.Fatalf("trackItem: %v", err)
	}

	// Get tracked items
	tagIDs, err := m.getTrackedItems(ctx, "tag")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	if len(tagIDs) != 2 {
		t.Errorf("len(tagIDs) = %d, want 2", len(tagIDs))
	}

	catIDs, err := m.getTrackedItems(ctx, "category")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	if len(catIDs) != 1 {
		t.Errorf("len(catIDs) = %d, want 1", len(catIDs))
	}
}

func TestGetTrackedCounts(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()

	// Track items
	for i := 0; i < 5; i++ {
		if err := m.trackItem(ctx, "tag", int64(i+1)); err != nil {
			t.Fatalf("trackItem: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := m.trackItem(ctx, "category", int64(i+1)); err != nil {
			t.Fatalf("trackItem: %v", err)
		}
	}

	// Get counts
	counts, err := m.getTrackedCounts(ctx)
	if err != nil {
		t.Fatalf("getTrackedCounts: %v", err)
	}

	if counts["tag"] != 5 {
		t.Errorf("tag count = %d, want 5", counts["tag"])
	}
	if counts["category"] != 3 {
		t.Errorf("category count = %d, want 3", counts["category"])
	}
}

func TestClearTrackedItems(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()

	// Track items
	if err := m.trackItem(ctx, "tag", 1); err != nil {
		t.Fatalf("trackItem: %v", err)
	}
	if err := m.trackItem(ctx, "category", 1); err != nil {
		t.Fatalf("trackItem: %v", err)
	}

	// Clear
	if err := m.clearTrackedItems(ctx); err != nil {
		t.Fatalf("clearTrackedItems: %v", err)
	}

	// Verify empty
	counts, err := m.getTrackedCounts(ctx)
	if err != nil {
		t.Fatalf("getTrackedCounts: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("counts should be empty after clear, got %v", counts)
	}
}

func TestGenerateTags(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language}

	// Generate tags
	tagIDs, err := m.generateTags(ctx, languages)
	if err != nil {
		t.Fatalf("generateTags: %v", err)
	}

	// Should have 5-20 tags
	if len(tagIDs) < 5 || len(tagIDs) > 20 {
		t.Errorf("len(tagIDs) = %d, want 5-20", len(tagIDs))
	}

	// Verify tracked
	tracked, err := m.getTrackedItems(ctx, "tag")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	if len(tracked) != len(tagIDs) {
		t.Errorf("tracked tags = %d, generated = %d", len(tracked), len(tagIDs))
	}
}

func TestGenerateTagsWithMultipleLanguages(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language, fixtures.Language2}

	// Generate tags
	tagIDs, err := m.generateTags(ctx, languages)
	if err != nil {
		t.Fatalf("generateTags: %v", err)
	}

	// Should have tags for both languages (tracked separately)
	tracked, err := m.getTrackedItems(ctx, "tag")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	// Each tag has a translation for the second language
	expectedTags := len(tagIDs) * 2 // base tags + translations
	if len(tracked) != expectedTags {
		t.Errorf("tracked tags = %d, expected = %d", len(tracked), expectedTags)
	}

	// Should have translation records
	translations, err := m.getTrackedItems(ctx, "translation")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	if len(translations) != len(tagIDs) {
		t.Errorf("translation records = %d, expected = %d", len(translations), len(tagIDs))
	}
}

func TestGenerateCategories(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()
	q := store.New(db)
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language}

	// Generate categories
	catIDs, err := m.generateCategories(ctx, languages)
	if err != nil {
		t.Fatalf("generateCategories: %v", err)
	}

	// Should have 5-20 categories
	if len(catIDs) < 5 || len(catIDs) > 20 {
		t.Errorf("len(catIDs) = %d, want 5-20", len(catIDs))
	}

	// Check that some have parents (nested)
	hasParent := false
	for _, id := range catIDs {
		cat, err := q.GetCategoryByID(ctx, id)
		if err != nil {
			continue
		}
		if cat.ParentID.Valid {
			hasParent = true
			break
		}
	}
	if len(catIDs) > 5 && !hasParent {
		t.Error("expected some categories to have parents (nested structure)")
	}
}

func TestGenerateMedia(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	defer setupTempUploadDir(t)()

	m := testModule(t, db)
	ctx := context.Background()
	q := store.New(db)
	fixtures := createTestFixtures(t, db)

	// Generate media
	mediaIDs, err := m.generateMedia(ctx, fixtures.User.ID)
	if err != nil {
		t.Fatalf("generateMedia: %v", err)
	}

	// Should have 5-20 media items
	if len(mediaIDs) < 5 || len(mediaIDs) > 20 {
		t.Errorf("len(mediaIDs) = %d, want 5-20", len(mediaIDs))
	}

	// Verify files were created
	for _, id := range mediaIDs {
		media, err := q.GetMediaByID(ctx, id)
		if err != nil {
			t.Errorf("GetMediaByID(%d): %v", id, err)
			continue
		}

		// Check original exists
		originalPath := filepath.Join("uploads", "originals", media.Uuid, media.Filename)
		if _, err := os.Stat(originalPath); os.IsNotExist(err) {
			t.Errorf("original file not created: %s", originalPath)
		}
	}

	// Verify tracked
	tracked, err := m.getTrackedItems(ctx, "media")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	if len(tracked) != len(mediaIDs) {
		t.Errorf("tracked media = %d, generated = %d", len(tracked), len(mediaIDs))
	}
}

func TestGeneratePages(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()
	q := store.New(db)
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language}

	// Create some tags and categories first
	now := time.Now()
	tag1, err := q.CreateTag(ctx, store.CreateTagParams{
		Name: "Tag1", Slug: "tag1", LanguageID: sql.NullInt64{Int64: fixtures.Language.ID, Valid: true},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	cat1, err := q.CreateCategory(ctx, store.CreateCategoryParams{
		Name: "Cat1", Slug: "cat1", LanguageID: sql.NullInt64{Int64: fixtures.Language.ID, Valid: true},
		Position: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	tagIDs := []int64{tag1.ID}
	catIDs := []int64{cat1.ID}
	var mediaIDs []int64 // No media for this test

	// Generate pages
	pageIDs, err := m.generatePages(ctx, languages, tagIDs, catIDs, mediaIDs, fixtures.User.ID)
	if err != nil {
		t.Fatalf("generatePages: %v", err)
	}

	// Should have 5-20 pages
	if len(pageIDs) < 5 || len(pageIDs) > 20 {
		t.Errorf("len(pageIDs) = %d, want 5-20", len(pageIDs))
	}

	// Verify pages are published
	for _, id := range pageIDs {
		page, err := q.GetPageByID(ctx, id)
		if err != nil {
			t.Errorf("GetPageByID(%d): %v", id, err)
			continue
		}
		if page.Status != "published" {
			t.Errorf("page %d status = %q, want published", id, page.Status)
		}
	}

	// Verify tracked
	tracked, err := m.getTrackedItems(ctx, "page")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	if len(tracked) != len(pageIDs) {
		t.Errorf("tracked pages = %d, generated = %d", len(tracked), len(pageIDs))
	}
}

func TestGenerateMenuItems(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)
	ctx := context.Background()
	q := store.New(db)
	fixtures := createTestFixtures(t, db)

	// Create some pages
	now := time.Now()
	var pageIDs []int64
	for i := 0; i < 5; i++ {
		page, err := q.CreatePage(ctx, store.CreatePageParams{
			Title: "Test Page", Slug: "test-page-" + string(rune('0'+i)),
			Body: "<p>Content</p>", Status: "published", AuthorID: fixtures.User.ID,
			LanguageID: sql.NullInt64{Int64: fixtures.Language.ID, Valid: true},
			CreatedAt:  now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		pageIDs = append(pageIDs, page.ID)
	}

	// Generate menu items
	menuItemIDs, err := m.generateMenuItems(ctx, pageIDs)
	if err != nil {
		t.Fatalf("generateMenuItems: %v", err)
	}

	// Should have 5-20 menu items
	if len(menuItemIDs) < 5 || len(menuItemIDs) > 20 {
		t.Errorf("len(menuItemIDs) = %d, want 5-20", len(menuItemIDs))
	}

	// Verify all items are in Main Menu (ID=1)
	items, err := q.ListMenuItems(ctx, fixtures.Menu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) != len(menuItemIDs) {
		t.Errorf("menu items in DB = %d, generated = %d", len(items), len(menuItemIDs))
	}

	// Verify tracked
	tracked, err := m.getTrackedItems(ctx, "menu_item")
	if err != nil {
		t.Fatalf("getTrackedItems: %v", err)
	}
	if len(tracked) != len(menuItemIDs) {
		t.Errorf("tracked menu items = %d, generated = %d", len(tracked), len(menuItemIDs))
	}
}

func TestDeleteAllGeneratedItems(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	defer setupTempUploadDir(t)()

	m := testModule(t, db)
	ctx := context.Background()
	q := store.New(db)
	fixtures := createTestFixtures(t, db)

	languages := []store.Language{fixtures.Language}

	// Generate all content
	tagIDs, err := m.generateTags(ctx, languages)
	if err != nil {
		t.Fatalf("generateTags: %v", err)
	}
	catIDs, err := m.generateCategories(ctx, languages)
	if err != nil {
		t.Fatalf("generateCategories: %v", err)
	}
	mediaIDs, err := m.generateMedia(ctx, fixtures.User.ID)
	if err != nil {
		t.Fatalf("generateMedia: %v", err)
	}
	pageIDs, err := m.generatePages(ctx, languages, tagIDs, catIDs, mediaIDs, fixtures.User.ID)
	if err != nil {
		t.Fatalf("generatePages: %v", err)
	}
	_, err = m.generateMenuItems(ctx, pageIDs)
	if err != nil {
		t.Fatalf("generateMenuItems: %v", err)
	}

	// Verify content was created
	counts, err := m.getTrackedCounts(ctx)
	if err != nil {
		t.Fatalf("getTrackedCounts: %v", err)
	}
	if len(counts) == 0 {
		t.Fatal("no content was generated")
	}

	// Delete all generated items
	if err := m.deleteAllGeneratedItems(ctx); err != nil {
		t.Fatalf("deleteAllGeneratedItems: %v", err)
	}

	// Verify tracking table is empty
	counts, err = m.getTrackedCounts(ctx)
	if err != nil {
		t.Fatalf("getTrackedCounts: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("tracking table not empty after delete: %v", counts)
	}

	// Verify main menu still exists
	_, err = q.GetMenuByID(ctx, fixtures.Menu.ID)
	if err != nil {
		t.Error("Main Menu was deleted, should have been preserved")
	}

	// Verify menu items were deleted
	items, err := q.ListMenuItems(ctx, fixtures.Menu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("menu items still exist: %d", len(items))
	}
}

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "developer" {
		t.Errorf("Name() = %q, want developer", m.Name())
	}
	if m.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want 1.0.0", m.Version())
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 1)
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{5, 3, 3},
		{10, 10, 10},
		{0, 5, 0},
		{-1, 5, -1},
	}

	for _, tt := range tests {
		got := min(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
