package transfer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportOptions_Defaults(t *testing.T) {
	opts := DefaultImportOptions()

	assert.False(t, opts.DryRun)
	assert.Equal(t, ConflictSkip, opts.ConflictStrategy)
	assert.True(t, opts.ImportUsers)
	assert.True(t, opts.ImportPages)
	assert.True(t, opts.ImportCategories)
	assert.True(t, opts.ImportTags)
	assert.True(t, opts.ImportMedia)
	assert.True(t, opts.ImportMenus)
	assert.True(t, opts.ImportForms)
	assert.True(t, opts.ImportConfig)
	assert.True(t, opts.ImportLanguages)
}

func TestImportResult_Operations(t *testing.T) {
	result := NewImportResult(false)

	// Test initial state
	assert.True(t, result.Success)
	assert.False(t, result.DryRun)
	assert.Empty(t, result.Errors)

	// Test increment operations
	result.IncrementCreated("pages")
	result.IncrementCreated("pages")
	result.IncrementUpdated("pages")
	result.IncrementSkipped("tags")

	assert.Equal(t, 2, result.Created["pages"])
	assert.Equal(t, 1, result.Updated["pages"])
	assert.Equal(t, 1, result.Skipped["tags"])

	// Test totals
	assert.Equal(t, 2, result.TotalCreated())
	assert.Equal(t, 1, result.TotalUpdated())
	assert.Equal(t, 1, result.TotalSkipped())

	// Test ID mapping
	idMap := result.GetIDMap("pages")
	idMap[1] = 100
	idMap[2] = 200

	retrieved := result.GetIDMap("pages")
	assert.Equal(t, int64(100), retrieved[1])
	assert.Equal(t, int64(200), retrieved[2])

	// Test error adding
	result.AddError("page", "test-page", "test error")
	assert.False(t, result.Success)
	assert.Len(t, result.Errors, 1)
	assert.Equal(t, "page", result.Errors[0].Entity)
	assert.Equal(t, "test-page", result.Errors[0].ID)
	assert.Equal(t, "test error", result.Errors[0].Message)
}

func TestImporter_Validate(t *testing.T) {
	importer := NewImporter(nil, nil, nil)

	tests := []struct {
		name          string
		data          *ExportData
		expectErrors  bool
		errorContains string
	}{
		{
			name: "valid data",
			data: &ExportData{
				Version:    "1.0",
				ExportedAt: time.Now(),
				Languages: []ExportLanguage{
					{Code: "en", Name: "English", NativeName: "English"},
				},
				Users: []ExportUser{
					{Email: "test@example.com", Name: "Test", Role: "admin"},
				},
				Categories: []ExportCategory{
					{ID: 1, Name: "Test", Slug: "test"},
				},
				Tags: []ExportTag{
					{ID: 1, Name: "Test", Slug: "test"},
				},
				Pages: []ExportPage{
					{ID: 1, Title: "Test", Slug: "test"},
				},
				Media: []ExportMedia{
					{UUID: "test-uuid", Filename: "test.jpg"},
				},
				Menus: []ExportMenu{
					{ID: 1, Name: "Test", Slug: "test"},
				},
				Forms: []ExportForm{
					{ID: 1, Name: "Test", Slug: "test"},
				},
			},
			expectErrors: false,
		},
		{
			name: "missing version",
			data: &ExportData{
				ExportedAt: time.Now(),
			},
			expectErrors:  true,
			errorContains: "version",
		},
		{
			name: "missing language code",
			data: &ExportData{
				Version: "1.0",
				Languages: []ExportLanguage{
					{Code: "", Name: "English"},
				},
			},
			expectErrors:  true,
			errorContains: "language code",
		},
		{
			name: "missing user email",
			data: &ExportData{
				Version: "1.0",
				Users: []ExportUser{
					{Email: "", Name: "Test", Role: "admin"},
				},
			},
			expectErrors:  true,
			errorContains: "user email",
		},
		{
			name: "missing category slug",
			data: &ExportData{
				Version: "1.0",
				Categories: []ExportCategory{
					{Name: "Test", Slug: ""},
				},
			},
			expectErrors:  true,
			errorContains: "category slug",
		},
		{
			name: "missing tag slug",
			data: &ExportData{
				Version: "1.0",
				Tags: []ExportTag{
					{Name: "Test", Slug: ""},
				},
			},
			expectErrors:  true,
			errorContains: "tag slug",
		},
		{
			name: "missing page slug",
			data: &ExportData{
				Version: "1.0",
				Pages: []ExportPage{
					{Title: "Test", Slug: ""},
				},
			},
			expectErrors:  true,
			errorContains: "page slug",
		},
		{
			name: "missing media UUID",
			data: &ExportData{
				Version: "1.0",
				Media: []ExportMedia{
					{UUID: "", Filename: "test.jpg"},
				},
			},
			expectErrors:  true,
			errorContains: "media UUID",
		},
		{
			name: "missing menu slug",
			data: &ExportData{
				Version: "1.0",
				Menus: []ExportMenu{
					{Name: "Test", Slug: ""},
				},
			},
			expectErrors:  true,
			errorContains: "menu slug",
		},
		{
			name: "missing form slug",
			data: &ExportData{
				Version: "1.0",
				Forms: []ExportForm{
					{Name: "Test", Slug: ""},
				},
			},
			expectErrors:  true,
			errorContains: "form slug",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := importer.Validate(tt.data)

			if tt.expectErrors {
				assert.NotEmpty(t, errors, "expected validation errors")
				found := false
				for _, err := range errors {
					if containsIgnoreCase(err.Message, tt.errorContains) {
						found = true
						break
					}
				}
				assert.True(t, found, "expected error containing %q, got %v", tt.errorContains, errors)
			} else {
				assert.Empty(t, errors, "expected no validation errors")
			}
		})
	}
}

func TestImporter_ImportFromFile_NotExists(t *testing.T) {
	importer := NewImporter(nil, nil, nil)

	_, err := importer.ImportFromFile(context.Background(), "/nonexistent/file.json", DefaultImportOptions())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open file")
}

func TestImporter_ImportFromFile_InvalidJSON(t *testing.T) {
	// Create temp file with invalid JSON
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "invalid.json")
	err := os.WriteFile(tmpFile, []byte("not valid json"), 0644)
	require.NoError(t, err)

	importer := NewImporter(nil, nil, nil)

	_, err = importer.ImportFromFile(context.Background(), tmpFile, DefaultImportOptions())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse JSON")
}

func TestConflictStrategy_Values(t *testing.T) {
	assert.Equal(t, ConflictStrategy("skip"), ConflictSkip)
	assert.Equal(t, ConflictStrategy("overwrite"), ConflictOverwrite)
	assert.Equal(t, ConflictStrategy("rename"), ConflictRename)
}

func TestValidationResult_Structure(t *testing.T) {
	result := &ValidationResult{
		Valid:     true,
		Version:   "1.0",
		Entities:  map[string]int{"pages": 5, "categories": 3},
		Conflicts: map[string][]string{"pages": {"test-page"}},
		Errors:    []ImportError{},
	}

	assert.True(t, result.Valid)
	assert.Equal(t, "1.0", result.Version)
	assert.Equal(t, 5, result.Entities["pages"])
	assert.Equal(t, 3, result.Entities["categories"])
	assert.Contains(t, result.Conflicts["pages"], "test-page")
}

func TestExportData_WithTranslations(t *testing.T) {
	now := time.Now()
	data := &ExportData{
		Version:    "1.0",
		ExportedAt: now,
		Pages: []ExportPage{
			{
				ID:           1,
				Title:        "English Page",
				Slug:         "english-page",
				LanguageCode: "en",
				Translations: map[string]int64{"ru": 2},
			},
			{
				ID:           2,
				Title:        "Russian Page",
				Slug:         "russian-page",
				LanguageCode: "ru",
				Translations: map[string]int64{"en": 1},
			},
		},
	}

	assert.Len(t, data.Pages, 2)
	assert.Equal(t, "en", data.Pages[0].LanguageCode)
	assert.Equal(t, int64(2), data.Pages[0].Translations["ru"])
}

func TestToNullString(t *testing.T) {
	// Test empty string
	result := toNullString("")
	assert.False(t, result.Valid)
	assert.Equal(t, "", result.String)

	// Test non-empty string
	result = toNullString("test")
	assert.True(t, result.Valid)
	assert.Equal(t, "test", result.String)
}

func TestImporter_DryRun(t *testing.T) {
	// Skip if no database available
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// This test demonstrates dry run behavior
	data := &ExportData{
		Version: "1.0",
		Pages: []ExportPage{
			{ID: 1, Title: "Test", Slug: "test"},
		},
	}

	opts := DefaultImportOptions()
	opts.DryRun = true

	// Without database, we can only test validation
	importer := NewImporter(nil, nil, nil)

	// Validate would work without database
	errors := importer.Validate(data)
	assert.Empty(t, errors)
}

// containsIgnoreCase checks if s contains substr (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			(len(s) > 0 && len(substr) > 0 &&
				(s[0] == substr[0] || s[0]+32 == substr[0] || s[0] == substr[0]+32) &&
				containsIgnoreCase(s[1:], substr[1:])) ||
			(len(s) > 0 && containsIgnoreCase(s[1:], substr)))
}

// Integration tests (require database)

func TestImporter_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// This would require setting up a test database
	// For now, we just verify the importer can be created
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skip("sqlite3 not available")
	}
	defer func() { _ = db.Close() }()

	importer := NewImporter(nil, db, nil)
	assert.NotNil(t, importer)
}
