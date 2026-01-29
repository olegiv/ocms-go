// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"

	_ "github.com/mattn/go-sqlite3"
)

func TestExportWithMediaToZip(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()

	// Get default language
	lang, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create temp upload directory
	uploadDir, err := os.MkdirTemp("", "ocms-test-uploads-*")
	if err != nil {
		t.Fatalf("failed to create temp upload dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(uploadDir) }()

	// Create test media file on disk
	testMediaUUID := "550e8400-e29b-41d4-a716-446655440000"
	originalDir := filepath.Join(uploadDir, "originals", testMediaUUID)
	if err := os.MkdirAll(originalDir, 0755); err != nil {
		t.Fatalf("failed to create original dir: %v", err)
	}

	testFilename := "test-image.jpg"
	testContent := []byte("fake image content for testing")
	if err := os.WriteFile(filepath.Join(originalDir, testFilename), testContent, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Create media record in database
	media, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
		Uuid:       testMediaUUID,
		Filename:   testFilename,
		MimeType:   "image/jpeg",
		Size:       int64(len(testContent)),
		UploadedBy: ts.User.ID,
		LanguageID: lang.ID,
		CreatedAt:  ts.Now,
		UpdatedAt:  ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create media record: %v", err)
	}

	// Create variant directory and file (variants use same filename as original)
	variantDir := filepath.Join(uploadDir, "thumbnail", testMediaUUID)
	if err := os.MkdirAll(variantDir, 0755); err != nil {
		t.Fatalf("failed to create variant dir: %v", err)
	}

	variantContent := []byte("fake thumbnail content")
	if err := os.WriteFile(filepath.Join(variantDir, testFilename), variantContent, 0644); err != nil {
		t.Fatalf("failed to write variant file: %v", err)
	}

	_, err = ts.Queries.CreateMediaVariant(ts.Ctx, store.CreateMediaVariantParams{
		MediaID:   media.ID,
		Type:      "thumbnail",
		Size:      int64(len(variantContent)),
		Width:     150,
		Height:    150,
		CreatedAt: ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create media variant: %v", err)
	}

	// Export with media files
	logger := slog.Default()
	exporter := NewExporter(ts.Queries, logger)
	exporter.SetUploadDir(uploadDir)

	opts := ExportOptions{
		IncludeMedia:      true,
		IncludeMediaFiles: true,
	}

	var buf bytes.Buffer
	if err := exporter.ExportWithMedia(ts.Ctx, opts, &buf); err != nil {
		t.Fatalf("ExportWithMedia failed: %v", err)
	}

	// Verify zip structure
	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}

	// Expected files in zip
	expectedFiles := map[string]bool{
		"export.json": false,
		"media/originals/" + testMediaUUID + "/" + testFilename: false,
		"media/thumbnail/" + testMediaUUID + "/" + testFilename: false,
	}

	for _, f := range zipReader.File {
		if _, ok := expectedFiles[f.Name]; ok {
			expectedFiles[f.Name] = true
		}
	}

	for name, found := range expectedFiles {
		if !found {
			t.Errorf("expected file %s not found in zip", name)
		}
	}

	// Verify export.json is valid
	for _, f := range zipReader.File {
		if f.Name == "export.json" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("failed to open export.json: %v", err)
			}
			var data ExportData
			if err := json.NewDecoder(rc).Decode(&data); err != nil {
				_ = rc.Close()
				t.Fatalf("failed to decode export.json: %v", err)
			}
			_ = rc.Close()

			if len(data.Media) != 1 {
				t.Errorf("expected 1 media item, got %d", len(data.Media))
			}
			if len(data.Media) > 0 && data.Media[0].UUID != testMediaUUID {
				t.Errorf("expected media UUID %s, got %s", testMediaUUID, data.Media[0].UUID)
			}
			break
		}
	}
}

func TestImportFromZip(t *testing.T) {
	// Create source database with data
	srcDB, srcCleanup := testutil.TestDB(t)
	defer srcCleanup()

	srcQueries := store.New(srcDB)
	ctx := context.Background()
	now := time.Now()

	// Get default language
	lang, err := srcQueries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create test user in source DB
	user, err := srcQueries.CreateUser(ctx, store.CreateUserParams{
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

	// Create temp upload directory for source
	srcUploadDir, err := os.MkdirTemp("", "ocms-test-src-uploads-*")
	if err != nil {
		t.Fatalf("failed to create temp src upload dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(srcUploadDir) }()

	// Create test media file
	testMediaUUID := "550e8400-e29b-41d4-a716-446655440001"
	originalDir := filepath.Join(srcUploadDir, "originals", testMediaUUID)
	if err := os.MkdirAll(originalDir, 0755); err != nil {
		t.Fatalf("failed to create original dir: %v", err)
	}

	testFilename := "imported-image.png"
	testContent := []byte("fake PNG image content for import test")
	if err := os.WriteFile(filepath.Join(originalDir, testFilename), testContent, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Create media record
	_, err = srcQueries.CreateMedia(ctx, store.CreateMediaParams{
		Uuid:       testMediaUUID,
		Filename:   testFilename,
		MimeType:   "image/png",
		Size:       int64(len(testContent)),
		UploadedBy: user.ID,
		LanguageID: lang.ID,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("failed to create media record: %v", err)
	}

	// Export to zip
	logger := slog.Default()
	exporter := NewExporter(srcQueries, logger)
	exporter.SetUploadDir(srcUploadDir)

	exportOpts := ExportOptions{
		IncludeMedia:      true,
		IncludeMediaFiles: true,
		IncludeUsers:      true,
	}

	var zipBuf bytes.Buffer
	if err := exporter.ExportWithMedia(ctx, exportOpts, &zipBuf); err != nil {
		t.Fatalf("ExportWithMedia failed: %v", err)
	}

	// Create destination database
	dstDB, dstCleanup := testutil.TestDB(t)
	defer dstCleanup()

	dstQueries := store.New(dstDB)

	// Create temp upload directory for destination
	dstUploadDir, err := os.MkdirTemp("", "ocms-test-dst-uploads-*")
	if err != nil {
		t.Fatalf("failed to create temp dst upload dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dstUploadDir) }()

	// Import from zip
	importer := NewImporter(dstQueries, dstDB, logger)
	importer.SetUploadDir(dstUploadDir)

	importOpts := ImportOptions{
		ImportMedia:      true,
		ImportMediaFiles: true,
		ImportUsers:      true,
	}

	result, err := importer.ImportFromZipBytes(ctx, zipBuf.Bytes(), importOpts)
	if err != nil {
		t.Fatalf("ImportFromZipBytes failed: %v", err)
	}

	// Verify import results
	mediaCreated := result.Created["media"]
	if mediaCreated != 1 {
		t.Errorf("expected 1 media created, got %d", mediaCreated)
	}

	// Verify media file was copied to destination
	dstFilePath := filepath.Join(dstUploadDir, "originals", testMediaUUID, testFilename)
	if _, err := os.Stat(dstFilePath); os.IsNotExist(err) {
		t.Error("media file was not copied to destination upload dir")
	} else {
		// Verify content
		content, err := os.ReadFile(dstFilePath)
		if err != nil {
			t.Fatalf("failed to read destination file: %v", err)
		}
		if !bytes.Equal(content, testContent) {
			t.Error("media file content does not match original")
		}
	}

	// Verify media record was created in database
	media, err := dstQueries.GetMediaByUUID(ctx, testMediaUUID)
	if err != nil {
		t.Fatalf("failed to get imported media: %v", err)
	}
	if media.Filename != testFilename {
		t.Errorf("expected filename %s, got %s", testFilename, media.Filename)
	}
}

func TestValidateZipFile(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Create a valid export zip in memory
	var zipBuf bytes.Buffer
	zipWriter := zip.NewWriter(&zipBuf)

	// Add export.json with minimal valid data
	exportData := ExportData{
		Version:    ExportVersion,
		ExportedAt: now,
		Pages: []ExportPage{
			{
				Title:  "Test Page",
				Slug:   "test-page",
				Body:   "Content",
				Status: "published",
			},
		},
	}

	jsonData, err := json.Marshal(exportData)
	if err != nil {
		t.Fatalf("failed to marshal export data: %v", err)
	}

	w, err := zipWriter.Create("export.json")
	if err != nil {
		t.Fatalf("failed to create export.json in zip: %v", err)
	}
	if _, err := w.Write(jsonData); err != nil {
		t.Fatalf("failed to write export.json: %v", err)
	}

	// Add a fake media file
	mediaW, err := zipWriter.Create("media/originals/test-uuid/test.jpg")
	if err != nil {
		t.Fatalf("failed to create media file in zip: %v", err)
	}
	if _, err := mediaW.Write([]byte("fake media content")); err != nil {
		t.Fatalf("failed to write media file: %v", err)
	}

	if err := zipWriter.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}

	// Validate the zip
	logger := slog.Default()
	importer := NewImporter(queries, db, logger)

	result, err := importer.ValidateZipBytes(ctx, zipBuf.Bytes())
	if err != nil {
		t.Fatalf("ValidateZipBytes failed: %v", err)
	}

	if !result.Valid {
		t.Errorf("expected zip to be valid, but got errors: %v", result.Errors)
	}

	// Check entities count for pages
	pagesCount := result.Entities["pages"]
	if pagesCount != 1 {
		t.Errorf("expected 1 page in entities, got %d", pagesCount)
	}
}

func TestValidateInvalidZip(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	logger := slog.Default()
	importer := NewImporter(queries, db, logger)

	t.Run("NotAZip", func(t *testing.T) {
		result, err := importer.ValidateZipBytes(ctx, []byte("not a zip file"))
		if err != nil {
			t.Fatalf("ValidateZipBytes failed unexpectedly: %v", err)
		}
		if result.Valid {
			t.Error("expected result to be invalid for non-zip data")
		}
		if len(result.Errors) == 0 {
			t.Error("expected errors to be populated for invalid zip data")
		}
	})

	t.Run("ZipWithoutExportJSON", func(t *testing.T) {
		var zipBuf bytes.Buffer
		zipWriter := zip.NewWriter(&zipBuf)

		// Add some file that is not export.json
		w, err := zipWriter.Create("other.txt")
		if err != nil {
			t.Fatalf("failed to create file in zip: %v", err)
		}
		if _, err := w.Write([]byte("some content")); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		if err := zipWriter.Close(); err != nil {
			t.Fatalf("failed to close zip writer: %v", err)
		}

		result, err := importer.ValidateZipBytes(ctx, zipBuf.Bytes())
		if err != nil {
			t.Fatalf("ValidateZipBytes failed: %v", err)
		}

		if result.Valid {
			t.Error("expected zip without export.json to be invalid")
		}
	})
}

func TestExportWithMediaToFile(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()

	logger := slog.Default()
	exporter := NewExporter(queries, logger)

	opts := ExportOptions{
		IncludeMedia:      true,
		IncludeMediaFiles: true,
	}

	// Create temp file for export
	tmpFile, err := os.CreateTemp("", "ocms-export-test-*.zip")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := exporter.ExportWithMediaToFile(ctx, opts, tmpPath); err != nil {
		t.Fatalf("ExportWithMediaToFile failed: %v", err)
	}

	// Verify file exists and is a valid zip
	zipReader, err := zip.OpenReader(tmpPath)
	if err != nil {
		t.Fatalf("failed to open exported zip file: %v", err)
	}
	defer func() { _ = zipReader.Close() }()

	// Check for export.json
	hasExportJSON := false
	for _, f := range zipReader.File {
		if f.Name == "export.json" {
			hasExportJSON = true
			break
		}
	}

	if !hasExportJSON {
		t.Error("exported zip file does not contain export.json")
	}
}
