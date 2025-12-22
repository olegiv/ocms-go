package transfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"ocms-go/internal/store"
	"ocms-go/internal/testutil"

	_ "github.com/mattn/go-sqlite3"
)

func TestExportEmptyDatabase(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	logger := slog.Default()
	exporter := NewExporter(queries, logger)

	ctx := context.Background()
	opts := DefaultExportOptions()

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify basic structure
	if data.Version != ExportVersion {
		t.Errorf("Expected version %s, got %s", ExportVersion, data.Version)
	}

	if data.ExportedAt.IsZero() {
		t.Error("ExportedAt should not be zero")
	}

	// Languages should include default English from migration
	if len(data.Languages) != 1 {
		t.Errorf("Expected 1 language (default English), got %d", len(data.Languages))
	}

	if len(data.Languages) > 0 && data.Languages[0].Code != "en" {
		t.Errorf("Expected language code 'en', got '%s'", data.Languages[0].Code)
	}
}

func TestExportToWriter(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	logger := slog.Default()
	exporter := NewExporter(queries, logger)

	ctx := context.Background()
	opts := DefaultExportOptions()

	var buf bytes.Buffer
	if err := exporter.ExportToWriter(ctx, opts, &buf); err != nil {
		t.Fatalf("ExportToWriter failed: %v", err)
	}

	// Verify output is valid JSON
	var data ExportData
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}

	if data.Version != ExportVersion {
		t.Errorf("Expected version %s, got %s", ExportVersion, data.Version)
	}
}

func TestExportWithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()

	// Create test user
	now := time.Now()
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "test@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Test User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Get default language
	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create test page
	_, err = queries.CreatePage(ctx, store.CreatePageParams{
		Title:      "Test Page",
		Slug:       "test-page",
		Body:       "Test content",
		Status:     "published",
		AuthorID:   user.ID,
		LanguageID: sql.NullInt64{Int64: lang.ID, Valid: true},
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}

	// Create test category
	_, err = queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:      "Test Category",
		Slug:      "test-category",
		Position:  0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	// Create test tag
	_, err = queries.CreateTag(ctx, store.CreateTagParams{
		Name:      "Test Tag",
		Slug:      "test-tag",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := DefaultExportOptions()

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify data
	if len(data.Users) != 1 {
		t.Errorf("Expected 1 user, got %d", len(data.Users))
	}
	if len(data.Users) > 0 && data.Users[0].Email != "test@example.com" {
		t.Errorf("Expected user email 'test@example.com', got '%s'", data.Users[0].Email)
	}

	if len(data.Pages) != 1 {
		t.Errorf("Expected 1 page, got %d", len(data.Pages))
	}
	if len(data.Pages) > 0 && data.Pages[0].Slug != "test-page" {
		t.Errorf("Expected page slug 'test-page', got '%s'", data.Pages[0].Slug)
	}
	if len(data.Pages) > 0 && data.Pages[0].AuthorEmail != "test@example.com" {
		t.Errorf("Expected author email 'test@example.com', got '%s'", data.Pages[0].AuthorEmail)
	}

	if len(data.Categories) != 1 {
		t.Errorf("Expected 1 category, got %d", len(data.Categories))
	}
	if len(data.Categories) > 0 && data.Categories[0].Slug != "test-category" {
		t.Errorf("Expected category slug 'test-category', got '%s'", data.Categories[0].Slug)
	}

	if len(data.Tags) != 1 {
		t.Errorf("Expected 1 tag, got %d", len(data.Tags))
	}
	if len(data.Tags) > 0 && data.Tags[0].Slug != "test-tag" {
		t.Errorf("Expected tag slug 'test-tag', got '%s'", data.Tags[0].Slug)
	}
}

func TestExportOptionsFiltering(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()

	// Create test user
	now := time.Now()
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "test@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Test User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create pages with different statuses
	_, err = queries.CreatePage(ctx, store.CreatePageParams{
		Title:     "Published Page",
		Slug:      "published-page",
		Body:      "Published content",
		Status:    "published",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create published page: %v", err)
	}

	_, err = queries.CreatePage(ctx, store.CreatePageParams{
		Title:     "Draft Page",
		Slug:      "draft-page",
		Body:      "Draft content",
		Status:    "draft",
		AuthorID:  user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create draft page: %v", err)
	}

	logger := slog.Default()
	exporter := NewExporter(queries, logger)

	t.Run("ExportOnlyPublished", func(t *testing.T) {
		opts := ExportOptions{
			IncludePages: true,
			PageStatus:   "published",
		}

		data, err := exporter.Export(ctx, opts)
		if err != nil {
			t.Fatalf("Export failed: %v", err)
		}

		if len(data.Pages) != 1 {
			t.Errorf("Expected 1 published page, got %d", len(data.Pages))
		}
		if len(data.Pages) > 0 && data.Pages[0].Slug != "published-page" {
			t.Errorf("Expected 'published-page', got '%s'", data.Pages[0].Slug)
		}
	})

	t.Run("ExportOnlyDraft", func(t *testing.T) {
		opts := ExportOptions{
			IncludePages: true,
			PageStatus:   "draft",
		}

		data, err := exporter.Export(ctx, opts)
		if err != nil {
			t.Fatalf("Export failed: %v", err)
		}

		if len(data.Pages) != 1 {
			t.Errorf("Expected 1 draft page, got %d", len(data.Pages))
		}
		if len(data.Pages) > 0 && data.Pages[0].Slug != "draft-page" {
			t.Errorf("Expected 'draft-page', got '%s'", data.Pages[0].Slug)
		}
	})

	t.Run("ExportAllPages", func(t *testing.T) {
		opts := ExportOptions{
			IncludePages: true,
			PageStatus:   "all",
		}

		data, err := exporter.Export(ctx, opts)
		if err != nil {
			t.Fatalf("Export failed: %v", err)
		}

		if len(data.Pages) != 2 {
			t.Errorf("Expected 2 pages, got %d", len(data.Pages))
		}
	})

	t.Run("ExportWithoutPages", func(t *testing.T) {
		opts := ExportOptions{
			IncludePages: false,
			IncludeUsers: true,
		}

		data, err := exporter.Export(ctx, opts)
		if err != nil {
			t.Fatalf("Export failed: %v", err)
		}

		if len(data.Pages) != 0 {
			t.Errorf("Expected 0 pages, got %d", len(data.Pages))
		}
		if len(data.Users) != 1 {
			t.Errorf("Expected 1 user, got %d", len(data.Users))
		}
	})
}

func TestDefaultExportOptions(t *testing.T) {
	opts := DefaultExportOptions()

	if !opts.IncludeUsers {
		t.Error("IncludeUsers should be true by default")
	}
	if !opts.IncludePages {
		t.Error("IncludePages should be true by default")
	}
	if !opts.IncludeCategories {
		t.Error("IncludeCategories should be true by default")
	}
	if !opts.IncludeTags {
		t.Error("IncludeTags should be true by default")
	}
	if !opts.IncludeMedia {
		t.Error("IncludeMedia should be true by default")
	}
	if !opts.IncludeMenus {
		t.Error("IncludeMenus should be true by default")
	}
	if !opts.IncludeForms {
		t.Error("IncludeForms should be true by default")
	}
	if opts.IncludeSubmissions {
		t.Error("IncludeSubmissions should be false by default")
	}
	if !opts.IncludeConfig {
		t.Error("IncludeConfig should be true by default")
	}
	if !opts.IncludeLanguages {
		t.Error("IncludeLanguages should be true by default")
	}
	if opts.PageStatus != "all" {
		t.Errorf("PageStatus should be 'all' by default, got '%s'", opts.PageStatus)
	}
}

func TestExportCategoryHierarchy(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Create parent category
	parent, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:      "Parent Category",
		Slug:      "parent-category",
		Position:  0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create parent category: %v", err)
	}

	// Create child category
	_, err = queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:      "Child Category",
		Slug:      "child-category",
		ParentID:  sql.NullInt64{Int64: parent.ID, Valid: true},
		Position:  0,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create child category: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := ExportOptions{IncludeCategories: true}

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify hierarchy is exported
	if len(data.Categories) != 2 {
		t.Errorf("Expected 2 categories, got %d", len(data.Categories))
	}

	// Find child category and verify parent slug is set
	var childCat *ExportCategory
	for i := range data.Categories {
		if data.Categories[i].Slug == "child-category" {
			childCat = &data.Categories[i]
			break
		}
	}

	if childCat == nil {
		t.Fatal("Child category not found in export")
	}

	if childCat.ParentSlug != "parent-category" {
		t.Errorf("Expected parent slug 'parent-category', got '%s'", childCat.ParentSlug)
	}
}

func TestExportMenuWithItems(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Create menu
	menu, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name:      "Test Menu",
		Slug:      "test-menu",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu: %v", err)
	}

	// Create menu items
	_, err = queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "Home",
		Url:       sql.NullString{String: "/", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}

	_, err = queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "About",
		Url:       sql.NullString{String: "/about", Valid: true},
		Position:  1,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := ExportOptions{IncludeMenus: true}

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify menu is exported
	if len(data.Menus) != 1 {
		t.Errorf("Expected 1 menu, got %d", len(data.Menus))
	}

	if len(data.Menus) > 0 {
		if data.Menus[0].Slug != "test-menu" {
			t.Errorf("Expected menu slug 'test-menu', got '%s'", data.Menus[0].Slug)
		}
		if len(data.Menus[0].Items) != 2 {
			t.Errorf("Expected 2 menu items, got %d", len(data.Menus[0].Items))
		}
	}
}

func TestExportFormWithFields(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Create form
	form, err := queries.CreateForm(ctx, store.CreateFormParams{
		Name:      "Contact Form",
		Slug:      "contact",
		Title:     "Contact Us",
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create form: %v", err)
	}

	// Create form fields
	_, err = queries.CreateFormField(ctx, store.CreateFormFieldParams{
		FormID:     form.ID,
		Type:       "text",
		Name:       "name",
		Label:      "Your Name",
		IsRequired: true,
		Position:   0,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create form field: %v", err)
	}

	_, err = queries.CreateFormField(ctx, store.CreateFormFieldParams{
		FormID:     form.ID,
		Type:       "email",
		Name:       "email",
		Label:      "Your Email",
		IsRequired: true,
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create form field: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := ExportOptions{IncludeForms: true}

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify form is exported
	if len(data.Forms) != 1 {
		t.Errorf("Expected 1 form, got %d", len(data.Forms))
	}

	if len(data.Forms) > 0 {
		if data.Forms[0].Slug != "contact" {
			t.Errorf("Expected form slug 'contact', got '%s'", data.Forms[0].Slug)
		}
		if len(data.Forms[0].Fields) != 2 {
			t.Errorf("Expected 2 form fields, got %d", len(data.Forms[0].Fields))
		}
	}
}

func TestExportPageWithTranslations(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Create user
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "test@example.com",
		PasswordHash: "hash",
		Role:         "admin",
		Name:         "Test User",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Get default language (English)
	enLang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create another language (Russian)
	ruLang, err := queries.CreateLanguage(ctx, store.CreateLanguageParams{
		Code:       "ru",
		Name:       "Russian",
		NativeName: "Русский",
		IsDefault:  false,
		IsActive:   true,
		Direction:  "ltr",
		Position:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create language: %v", err)
	}

	// Create English page
	enPage, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title:      "Hello World",
		Slug:       "hello-world",
		Body:       "English content",
		Status:     "published",
		AuthorID:   user.ID,
		LanguageID: sql.NullInt64{Int64: enLang.ID, Valid: true},
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create English page: %v", err)
	}

	// Create Russian page
	ruPage, err := queries.CreatePage(ctx, store.CreatePageParams{
		Title:      "Привет мир",
		Slug:       "privet-mir",
		Body:       "Russian content",
		Status:     "published",
		AuthorID:   user.ID,
		LanguageID: sql.NullInt64{Int64: ruLang.ID, Valid: true},
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create Russian page: %v", err)
	}

	// Create translation link
	_, err = queries.CreateTranslation(ctx, store.CreateTranslationParams{
		EntityType:    "page",
		EntityID:      enPage.ID,
		LanguageID:    ruLang.ID,
		TranslationID: ruPage.ID,
		CreatedAt:     now,
	})
	if err != nil {
		t.Fatalf("failed to create translation: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := DefaultExportOptions()

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify pages are exported
	if len(data.Pages) != 2 {
		t.Errorf("Expected 2 pages, got %d", len(data.Pages))
	}

	// Find English page and verify translations
	var enExportPage *ExportPage
	for i := range data.Pages {
		if data.Pages[i].Slug == "hello-world" {
			enExportPage = &data.Pages[i]
			break
		}
	}

	if enExportPage == nil {
		t.Fatal("English page not found in export")
	}

	if enExportPage.LanguageCode != "en" {
		t.Errorf("Expected language code 'en', got '%s'", enExportPage.LanguageCode)
	}

	if len(enExportPage.Translations) != 1 {
		t.Errorf("Expected 1 translation, got %d", len(enExportPage.Translations))
	}

	if enExportPage.Translations["ru"] != ruPage.ID {
		t.Errorf("Expected translation to Russian page ID %d, got %d", ruPage.ID, enExportPage.Translations["ru"])
	}
}
