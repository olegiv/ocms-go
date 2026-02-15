// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package elefant

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestGetMimeTypeFromExt(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		// Image types
		{"jpeg extension", "photo.jpeg", "image/jpeg"},
		{"jpg extension", "photo.jpg", "image/jpeg"},
		{"png extension", "image.png", "image/png"},
		{"gif extension", "animation.gif", "image/gif"},
		{"webp extension", "modern.webp", "image/webp"},

		// Video types
		{"mp4 extension", "video.mp4", "video/mp4"},
		{"webm extension", "video.webm", "video/webm"},

		// Document types
		{"pdf extension", "document.pdf", "application/pdf"},

		// Case insensitivity
		{"uppercase JPG", "PHOTO.JPG", "image/jpeg"},
		{"mixed case Png", "Image.Png", "image/png"},

		// Path with directories
		{"path with dirs", "/path/to/files/image.jpg", "image/jpeg"},
		{"relative path", "uploads/2024/photo.png", "image/png"},

		// Edge cases
		{"no extension", "filename", ""},
		{"empty string", "", ""},
		{"dot only", ".", ""},
		{"hidden file no ext", ".gitignore", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getMimeTypeFromExt(tt.path)
			if got != tt.expected {
				t.Errorf("getMimeTypeFromExt(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestAllowedMediaMimeTypes(t *testing.T) {
	// Verify expected types are allowed
	allowedTypes := []string{
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/webp",
		"application/pdf",
		"video/mp4",
		"video/webm",
	}

	for _, mimeType := range allowedTypes {
		if !allowedMediaMimeTypes[mimeType] {
			t.Errorf("expected %q to be an allowed MIME type", mimeType)
		}
	}

	// Verify some types are NOT allowed
	disallowedTypes := []string{
		"text/plain",
		"text/html",
		"application/javascript",
		"application/x-executable",
		"",
	}

	for _, mimeType := range disallowedTypes {
		if allowedMediaMimeTypes[mimeType] {
			t.Errorf("expected %q to NOT be an allowed MIME type", mimeType)
		}
	}
}

func TestScanMediaFiles(t *testing.T) {
	// Create a temporary directory structure for testing
	tempDir := t.TempDir()

	// Create test files
	testFiles := map[string]string{
		"image1.jpg":           "fake jpeg content",
		"image2.png":           "fake png content",
		"subdir/image3.gif":    "fake gif content",
		"subdir/deep/video.mp4": "fake mp4 content",
		"document.pdf":         "fake pdf content",
		"ignored.txt":          "text file - should be ignored",
		"ignored.html":         "html file - should be ignored",
		".hidden.jpg":          "hidden file with valid extension",
	}

	for path, content := range testFiles {
		fullPath := filepath.Join(tempDir, path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to create test file %s: %v", path, err)
		}
	}

	// Run scan
	files, err := ScanMediaFiles(tempDir)
	if err != nil {
		t.Fatalf("ScanMediaFiles() error = %v", err)
	}

	// Should find: image1.jpg, image2.png, subdir/image3.gif, subdir/deep/video.mp4, document.pdf, .hidden.jpg
	// Should NOT find: ignored.txt, ignored.html
	expectedCount := 6
	if len(files) != expectedCount {
		t.Errorf("ScanMediaFiles() found %d files, want %d", len(files), expectedCount)
		for _, f := range files {
			t.Logf("  found: %s (%s)", f.Path, f.MimeType)
		}
	}

	// Verify file properties
	fileMap := make(map[string]MediaFile)
	for _, f := range files {
		fileMap[f.Path] = f
	}

	// Check specific file
	if img, ok := fileMap["image1.jpg"]; ok {
		if img.MimeType != "image/jpeg" {
			t.Errorf("image1.jpg MimeType = %q, want %q", img.MimeType, "image/jpeg")
		}
		if img.Filename != "image1.jpg" {
			t.Errorf("image1.jpg Filename = %q, want %q", img.Filename, "image1.jpg")
		}
		if img.Size != int64(len("fake jpeg content")) {
			t.Errorf("image1.jpg Size = %d, want %d", img.Size, len("fake jpeg content"))
		}
	} else {
		t.Error("image1.jpg not found in scan results")
	}

	// Check nested file
	nestedPath := filepath.Join("subdir", "deep", "video.mp4")
	if video, ok := fileMap[nestedPath]; ok {
		if video.MimeType != "video/mp4" {
			t.Errorf("video.mp4 MimeType = %q, want %q", video.MimeType, "video/mp4")
		}
	} else {
		t.Errorf("nested video file not found, expected path: %s", nestedPath)
	}
}

func TestScanMediaFiles_EmptyPath(t *testing.T) {
	_, err := ScanMediaFiles("")
	if err == nil {
		t.Error("ScanMediaFiles(\"\") should return error")
	}
}

func TestScanMediaFiles_NonExistentPath(t *testing.T) {
	_, err := ScanMediaFiles("/nonexistent/path/12345")
	if err == nil {
		t.Error("ScanMediaFiles with nonexistent path should return error")
	}
}

func TestScanMediaFiles_FileNotDirectory(t *testing.T) {
	// Create a temp file (not directory)
	tempFile, err := os.CreateTemp(t.TempDir(), "test-file-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	_ = tempFile.Close()

	_, err = ScanMediaFiles(tempFile.Name())
	if err == nil {
		t.Error("ScanMediaFiles with file path should return error")
	}
}

func TestScanMediaFiles_EmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()

	files, err := ScanMediaFiles(tempDir)
	if err != nil {
		t.Fatalf("ScanMediaFiles() error = %v", err)
	}

	if len(files) != 0 {
		t.Errorf("ScanMediaFiles() found %d files in empty directory, want 0", len(files))
	}
}

func TestReplaceMediaURLs(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		mediaMap map[string]string
		expected string
	}{
		{
			name: "single replacement",
			body: `<img src="/files/image.jpg">`,
			mediaMap: map[string]string{
				"/files/image.jpg": "/uploads/originals/abc-123/image.jpg",
			},
			expected: `<img src="/uploads/originals/abc-123/image.jpg">`,
		},
		{
			name: "multiple replacements",
			body: `<p><img src="/files/photo1.jpg"></p><p><img src="/files/photo2.png"></p>`,
			mediaMap: map[string]string{
				"/files/photo1.jpg": "/uploads/originals/uuid1/photo1.jpg",
				"/files/photo2.png": "/uploads/originals/uuid2/photo2.png",
			},
			expected: `<p><img src="/uploads/originals/uuid1/photo1.jpg"></p><p><img src="/uploads/originals/uuid2/photo2.png"></p>`,
		},
		{
			name: "same file multiple times",
			body: `<img src="/files/logo.png"> ... <img src="/files/logo.png">`,
			mediaMap: map[string]string{
				"/files/logo.png": "/uploads/originals/uuid/logo.png",
			},
			expected: `<img src="/uploads/originals/uuid/logo.png"> ... <img src="/uploads/originals/uuid/logo.png">`,
		},
		{
			name: "nested path replacement",
			body: `<img src="/files/2024/01/photo.jpg">`,
			mediaMap: map[string]string{
				"/files/2024/01/photo.jpg": "/uploads/originals/uuid/photo.jpg",
			},
			expected: `<img src="/uploads/originals/uuid/photo.jpg">`,
		},
		{
			name:     "empty map",
			body:     `<img src="/files/image.jpg">`,
			mediaMap: map[string]string{},
			expected: `<img src="/files/image.jpg">`,
		},
		{
			name:     "nil map",
			body:     `<img src="/files/image.jpg">`,
			mediaMap: nil,
			expected: `<img src="/files/image.jpg">`,
		},
		{
			name: "no matching paths",
			body: `<img src="/other/image.jpg">`,
			mediaMap: map[string]string{
				"/files/image.jpg": "/uploads/originals/uuid/image.jpg",
			},
			expected: `<img src="/other/image.jpg">`,
		},
		{
			name:     "empty body",
			body:     "",
			mediaMap: map[string]string{"/files/a.jpg": "/uploads/b.jpg"},
			expected: "",
		},
		{
			name: "link in href",
			body: `<a href="/files/document.pdf">Download</a>`,
			mediaMap: map[string]string{
				"/files/document.pdf": "/uploads/originals/uuid/document.pdf",
			},
			expected: `<a href="/uploads/originals/uuid/document.pdf">Download</a>`,
		},
		{
			name: "css background",
			body: `<div style="background: url('/files/bg.jpg')">`,
			mediaMap: map[string]string{
				"/files/bg.jpg": "/uploads/originals/uuid/bg.jpg",
			},
			expected: `<div style="background: url('/uploads/originals/uuid/bg.jpg')">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceMediaURLs(tt.body, tt.mediaMap)
			if got != tt.expected {
				t.Errorf("replaceMediaURLs() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMediaFileStruct(t *testing.T) {
	file := MediaFile{
		Path:     "images/photo.jpg",
		FullPath: "/var/www/elefant/files/images/photo.jpg",
		Filename: "photo.jpg",
		Size:     12345,
		MimeType: "image/jpeg",
	}

	if file.Path != "images/photo.jpg" {
		t.Errorf("Path = %q, want %q", file.Path, "images/photo.jpg")
	}
	if file.FullPath != "/var/www/elefant/files/images/photo.jpg" {
		t.Errorf("FullPath = %q, want %q", file.FullPath, "/var/www/elefant/files/images/photo.jpg")
	}
	if file.Filename != "photo.jpg" {
		t.Errorf("Filename = %q, want %q", file.Filename, "photo.jpg")
	}
	if file.Size != 12345 {
		t.Errorf("Size = %d, want %d", file.Size, 12345)
	}
	if file.MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q, want %q", file.MimeType, "image/jpeg")
	}
}

func TestConfigFields_FilesPath(t *testing.T) {
	s := NewSource()
	fields := s.ConfigFields()

	// Check that files_path field exists
	var found bool
	for _, f := range fields {
		if f.Name == "files_path" {
			found = true
			if f.Type != "text" {
				t.Errorf("files_path Type = %q, want %q", f.Type, "text")
			}
			if f.Required {
				t.Error("files_path should not be required")
			}
			if f.Label == "" {
				t.Error("files_path Label should not be empty")
			}
			break
		}
	}

	if !found {
		t.Error("ConfigFields() missing files_path field")
	}
}

func TestMakeUniqueSlug(t *testing.T) {
	// Create test database with pages table
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create full pages table matching the schema
	_, err = db.Exec(`
		CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL DEFAULT '',
			slug TEXT NOT NULL UNIQUE,
			body TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			author_id INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			published_at DATETIME,
			featured_image_id INTEGER,
			meta_title TEXT NOT NULL DEFAULT '',
			meta_description TEXT NOT NULL DEFAULT '',
			meta_keywords TEXT NOT NULL DEFAULT '',
			og_image_id INTEGER,
			no_index INTEGER NOT NULL DEFAULT 0,
			no_follow INTEGER NOT NULL DEFAULT 0,
			canonical_url TEXT NOT NULL DEFAULT '',
			scheduled_at DATETIME,
			language_code TEXT NOT NULL DEFAULT '',
			hide_featured_image INTEGER NOT NULL DEFAULT 0,
			page_type TEXT NOT NULL DEFAULT 'page',
			exclude_from_lists INTEGER NOT NULL DEFAULT 0
		)
	`)
	if err != nil {
		t.Fatalf("failed to create pages table: %v", err)
	}

	queries := store.New(db)
	ctx := context.Background()

	tests := []struct {
		name         string
		baseSlug     string
		existingSlugs []string
		expected     string
	}{
		{
			name:         "no existing slug",
			baseSlug:     "my-post",
			existingSlugs: nil,
			expected:     "my-post",
		},
		{
			name:         "one existing slug",
			baseSlug:     "duplicate",
			existingSlugs: []string{"duplicate"},
			expected:     "duplicate-2",
		},
		{
			name:         "multiple existing slugs",
			baseSlug:     "popular",
			existingSlugs: []string{"popular", "popular-2", "popular-3"},
			expected:     "popular-4",
		},
		{
			name:         "gap in sequence",
			baseSlug:     "gap",
			existingSlugs: []string{"gap", "gap-3"},
			expected:     "gap-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up pages table
			_, _ = db.Exec("DELETE FROM pages")

			// Insert existing pages
			for _, slug := range tt.existingSlugs {
				_, err := db.Exec("INSERT INTO pages (slug, title) VALUES (?, ?)", slug, "Test")
				if err != nil {
					t.Fatalf("failed to insert existing page: %v", err)
				}
			}

			got := makeUniqueSlug(ctx, queries, tt.baseSlug)
			if got != tt.expected {
				t.Errorf("makeUniqueSlug(%q) = %q, want %q", tt.baseSlug, got, tt.expected)
			}
		})
	}
}
