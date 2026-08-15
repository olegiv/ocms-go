// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

func createExportMedia(t *testing.T, ts *testSetup, mediaUUID, filename, mimeType string, size int64) store.Medium {
	t.Helper()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	medium, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
		Uuid: mediaUUID, Filename: filename, MimeType: mimeType, Size: size,
		Width: sql.NullInt64{}, Height: sql.NullInt64{}, UploadedBy: ts.User.ID,
		LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return medium
}

func TestExportWithMediaStagesBeforePublishingLaterOriginalFailure(t *testing.T) {
	const missingUUID = "550e8400-e29b-41d4-a716-446655440000"
	const existingUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	ts := setupTest(t)
	defer ts.Cleanup()
	uploadRoot := t.TempDir()

	// ListMediaSorted returns the later ID first. Insert the missing original
	// first so the valid media is fully written to the staged ZIP before the
	// second source failure is discovered.
	createExportMedia(t, ts, missingUUID, "missing.pdf", model.MimeTypePDF, 7)
	content := []byte("existing")
	createExportMedia(t, ts, existingUUID, "existing.pdf", model.MimeTypePDF, int64(len(content)))
	existingPath := filepath.Join(uploadRoot, model.OriginalsDir, existingUUID, "existing.pdf")
	if err := os.MkdirAll(filepath.Dir(existingPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	exporter := NewExporter(ts.Queries, slog.Default())
	exporter.SetUploadDir(uploadRoot)
	var output bytes.Buffer
	err := exporter.ExportWithMedia(ts.Ctx, ExportOptions{IncludeMedia: true, IncludeMediaFiles: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "missing.pdf") {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("failed staged export published %d bytes", output.Len())
	}
}

func TestExportWithMediaRequiresEveryDeclaredVariantFile(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	uploadRoot := t.TempDir()
	content := []byte("report")
	medium := createExportMedia(t, ts, mediaUUID, "report.pdf", model.MimeTypePDF, int64(len(content)))
	if _, err := ts.Queries.CreateMediaVariant(ts.Ctx, store.CreateMediaVariantParams{
		MediaID: medium.ID, Type: model.VariantThumbnail, Width: 10, Height: 10, Size: 5, CreatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "report.pdf")
	if err := os.MkdirAll(filepath.Dir(original), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, content, 0o600); err != nil {
		t.Fatal(err)
	}
	exporter := NewExporter(ts.Queries, slog.Default())
	exporter.SetUploadDir(uploadRoot)
	var output bytes.Buffer
	err := exporter.ExportWithMedia(ts.Ctx, ExportOptions{IncludeMedia: true, IncludeMediaFiles: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "thumbnail") {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("failed variant export published %d bytes", output.Len())
	}
}

func TestExportWithMediaRejectsCorruptImageBytesBeforePublishing(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	uploadRoot := t.TempDir()
	validPNG := transferTestPNG(t, 2, 2)
	corruptPNG := validPNG[:33] // Signature + IHDR: DecodeConfig works, full decode does not.
	medium := createExportMedia(t, ts, mediaUUID, "photo.png", model.MimeTypePNG, int64(len(corruptPNG)))
	if _, err := ts.Queries.UpdateMediaForImport(ts.Ctx, store.UpdateMediaForImportParams{
		ID: medium.ID, Filename: medium.Filename, MimeType: medium.MimeType, Size: medium.Size,
		Width: sql.NullInt64{Int64: 2, Valid: true}, Height: sql.NullInt64{Int64: 2, Valid: true},
		UploadedBy: medium.UploadedBy, Alt: medium.Alt, Caption: medium.Caption, FolderID: medium.FolderID,
		LanguageCode: medium.LanguageCode, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "photo.png")
	if err := os.MkdirAll(filepath.Dir(originalPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalPath, corruptPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	exporter := NewExporter(ts.Queries, slog.Default())
	exporter.SetUploadDir(uploadRoot)
	var output bytes.Buffer
	err := exporter.ExportWithMedia(ts.Ctx, ExportOptions{IncludeMedia: true, IncludeMediaFiles: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "validate image source") {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("corrupt image export published %d bytes", output.Len())
	}
}

func TestExportWithMediaRejectsCorruptDeclaredVariantBeforePublishing(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	uploadRoot := t.TempDir()
	original := transferTestPNG(t, 2, 2)
	medium := createExportMedia(t, ts, mediaUUID, "photo.png", model.MimeTypePNG, int64(len(original)))
	variantBytes := []byte("not an image")
	if _, err := ts.Queries.CreateMediaVariant(ts.Ctx, store.CreateMediaVariantParams{
		MediaID: medium.ID, Type: model.VariantThumbnail, Width: 1, Height: 1,
		Size: int64(len(variantBytes)), CreatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	for storageDir, content := range map[string][]byte{
		model.OriginalsDir: original, model.VariantThumbnail: variantBytes,
	} {
		filePath := filepath.Join(uploadRoot, storageDir, mediaUUID, "photo.png")
		if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exporter := NewExporter(ts.Queries, slog.Default())
	exporter.SetUploadDir(uploadRoot)
	var output bytes.Buffer
	err := exporter.ExportWithMedia(ts.Ctx, ExportOptions{IncludeMedia: true, IncludeMediaFiles: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "thumbnail") || !strings.Contains(err.Error(), "validate image source") {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("corrupt variant export published %d bytes", output.Len())
	}
}

func TestExportWithMediaRejectsImageMIMEBytesMismatch(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	uploadRoot := t.TempDir()
	pngBytes := transferTestPNG(t, 2, 2)
	createExportMedia(t, ts, mediaUUID, "photo.jpg", model.MimeTypeJPEG, int64(len(pngBytes)))
	originalPath := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "photo.jpg")
	if err := os.MkdirAll(filepath.Dir(originalPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalPath, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	exporter := NewExporter(ts.Queries, slog.Default())
	exporter.SetUploadDir(uploadRoot)
	var output bytes.Buffer
	err := exporter.ExportWithMedia(ts.Ctx, ExportOptions{IncludeMedia: true, IncludeMediaFiles: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "MIME type") {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("MIME-mismatched image export published %d bytes", output.Len())
	}
}

func TestExportWithMediaRejectsFilesWithoutMetadata(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	var output bytes.Buffer
	err := NewExporter(ts.Queries, slog.Default()).ExportWithMedia(
		ts.Ctx, ExportOptions{IncludeMediaFiles: true}, &output,
	)
	if err == nil || !strings.Contains(err.Error(), "without media metadata") {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("invalid option export published %d bytes", output.Len())
	}
}

func TestExportWithMediaToFilePreservesTargetOnFailure(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	createExportMedia(t, ts, mediaUUID, "missing.pdf", model.MimeTypePDF, 7)
	exporter := NewExporter(ts.Queries, slog.Default())
	exporter.SetUploadDir(t.TempDir())
	target := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(target, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exporter.ExportWithMediaToFile(ts.Ctx,
		ExportOptions{IncludeMedia: true, IncludeMediaFiles: true}, target); err == nil {
		t.Fatal("ExportWithMediaToFile() error = nil")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "previous" {
		t.Fatalf("target content = %q, error = %v", content, err)
	}
}

type zipDirectoryFailWriter struct {
	buffer bytes.Buffer
}

func (w *zipDirectoryFailWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte{'P', 'K', 1, 2}) {
		return 0, errors.New("injected central directory failure")
	}
	return w.buffer.Write(data)
}

func TestWriteMediaArchivePropagatesZipCloseFailure(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	writer := &zipDirectoryFailWriter{}
	err := NewExporter(ts.Queries, slog.Default()).writeMediaArchive(ts.Ctx, ExportOptions{}, writer)
	if err == nil || !strings.Contains(err.Error(), "injected central directory failure") {
		t.Fatalf("writeMediaArchive() error = %v", err)
	}
}

func TestMediaExportBudgetBoundaries(t *testing.T) {
	t.Run("per file", func(t *testing.T) {
		budget := &mediaExportBudget{}
		if err := budget.reserve(maxZipMediaFileUncompressedBytes); err != nil {
			t.Fatal(err)
		}
		if err := (&mediaExportBudget{}).reserve(maxZipMediaFileUncompressedBytes + 1); err == nil {
			t.Fatal("oversized media file accepted")
		}
	})
	t.Run("file count", func(t *testing.T) {
		budget := &mediaExportBudget{}
		for range maxZipMediaFiles {
			if err := budget.reserve(0); err != nil {
				t.Fatal(err)
			}
		}
		if err := budget.reserve(0); err == nil {
			t.Fatal("extra media entry accepted")
		}
	})
	t.Run("total", func(t *testing.T) {
		budget := &mediaExportBudget{}
		for range maxZipMediaTotalUncompressedBytes / maxZipMediaFileUncompressedBytes {
			if err := budget.reserve(maxZipMediaFileUncompressedBytes); err != nil {
				t.Fatal(err)
			}
		}
		if err := budget.reserve(1); err == nil {
			t.Fatal("media total above limit accepted")
		}
	})
}

func TestZipArchiveSizeBoundary(t *testing.T) {
	if err := validateZipArchiveSize(MaxZipArchiveBytes); err != nil {
		t.Fatal(err)
	}
	if err := validateZipArchiveSize(MaxZipArchiveBytes + 1); err == nil {
		t.Fatal("oversized compressed archive accepted")
	}
}

func TestExportWithMediaRejectsOversizedExportJSONWithoutPublishing(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.UpsertConfig(ts.Ctx, store.UpsertConfigParams{
		Key: "oversized_transfer_metadata", Value: strings.Repeat("a", maxZipExportJSONUncompressedBytes),
		Type: "string", Description: "test", LanguageCode: language.Code,
		UpdatedAt: ts.Now, UpdatedBy: sql.NullInt64{Int64: ts.User.ID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = NewExporter(ts.Queries, slog.Default()).ExportWithMedia(
		ts.Ctx, ExportOptions{IncludeConfig: true}, &output,
	)
	if err == nil || !strings.Contains(err.Error(), "export.json exceeds max size") {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("oversized metadata export published %d bytes", output.Len())
	}
}

func TestZipFileAPIsRejectOversizedArchiveBeforeOpening(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	archivePath := filepath.Join(t.TempDir(), "oversized.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxZipArchiveBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	if _, err := importer.ImportFromZipFile(ts.Ctx, archivePath, ImportOptions{}); err == nil || !strings.Contains(err.Error(), "exceeds max size") {
		t.Fatalf("ImportFromZipFile() error = %v", err)
	}
	if _, err := importer.ValidateZipFile(ts.Ctx, archivePath); err == nil || !strings.Contains(err.Error(), "exceeds max size") {
		t.Fatalf("ValidateZipFile() error = %v", err)
	}
}

func TestExportMediaRejectsLegacyInvalidUUIDWithoutPublishingMetadata(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	createExportMedia(t, ts, "legacy-media-1", "report.pdf", model.MimeTypePDF, 6)
	exporter := NewExporter(ts.Queries, slog.Default())
	var output bytes.Buffer
	err := exporter.ExportToWriter(ts.Ctx, ExportOptions{IncludeMedia: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "invalid canonical UUID") {
		t.Fatalf("ExportToWriter() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("invalid metadata export published %d bytes", output.Len())
	}
}

func TestExportMediaRejectsCaseFoldDuplicateUUIDsWithoutPublishingMetadata(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	createExportMedia(t, ts, lowerUUID, "lower.pdf", model.MimeTypePDF, 5)
	createExportMedia(t, ts, upperUUID, "upper.pdf", model.MimeTypePDF, 5)
	exporter := NewExporter(ts.Queries, slog.Default())
	var output bytes.Buffer
	err := exporter.ExportToWriter(ts.Ctx, ExportOptions{IncludeMedia: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "duplicates logical UUID") {
		t.Fatalf("ExportToWriter() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("duplicate identity export published %d bytes", output.Len())
	}
}

func TestMetadataOnlyTransferRejectsUnsafeMediaMetadata(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	zero := int64(0)
	for _, testCase := range []struct {
		name      string
		medium    ExportMedia
		wantError string
	}{
		{name: "filename", medium: ExportMedia{UUID: mediaUUID, Filename: "../../../outside.png"}, wantError: "safe path segment"},
		{name: "original width", medium: ExportMedia{UUID: mediaUUID, Filename: "photo.png", Width: &zero}, wantError: "width must be positive"},
		{name: "original height", medium: ExportMedia{UUID: mediaUUID, Filename: "photo.png", Height: &zero}, wantError: "height must be positive"},
		{name: "image extension", medium: ExportMedia{UUID: mediaUUID, Filename: "photo.jpg", MimeType: model.MimeTypePNG}, wantError: "does not match image MIME type"},
		{name: "variant width", medium: ExportMedia{UUID: mediaUUID, Filename: "photo.png", Variants: []ExportVariant{{
			Type: model.VariantThumbnail, Width: 0, Height: 1,
		}}}, wantError: "variant \"thumbnail\" width must be positive"},
		{name: "variant height", medium: ExportMedia{UUID: mediaUUID, Filename: "photo.png", Variants: []ExportVariant{{
			Type: model.VariantThumbnail, Width: 1, Height: 0,
		}}}, wantError: "variant \"thumbnail\" height must be positive"},
	} {
		t.Run("import "+testCase.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			errs := NewImporter(ts.Queries, ts.DB, slog.Default()).Validate(&ExportData{
				Version: ExportVersion, Media: []ExportMedia{testCase.medium},
			})
			if !importErrorsContain(errs, testCase.wantError) {
				t.Fatalf("Validate() errors = %+v, want %q", errs, testCase.wantError)
			}
		})
	}

	for _, testCase := range []struct {
		name      string
		filename  string
		mimeType  string
		size      int64
		width     sql.NullInt64
		height    sql.NullInt64
		variant   *store.CreateMediaVariantParams
		wantError string
	}{
		{name: "unsafe filename", filename: "../outside.pdf", size: 1, wantError: "safe path segment"},
		{name: "negative media size", filename: "report.pdf", size: -1, wantError: "media size must not be negative"},
		{name: "nonpositive media width", filename: "report.pdf", size: 1,
			width: sql.NullInt64{Int64: 0, Valid: true}, wantError: "media width must be positive"},
		{name: "nonpositive media height", filename: "report.pdf", size: 1,
			height: sql.NullInt64{Int64: 0, Valid: true}, wantError: "media height must be positive"},
		{name: "image extension mismatch", filename: "photo.jpg", mimeType: model.MimeTypePNG, size: 1,
			wantError: "does not match image MIME type"},
		{name: "unsupported variant", filename: "report.pdf", size: 1,
			variant:   &store.CreateMediaVariantParams{Type: "legacy", Width: 1, Height: 1, Size: 1},
			wantError: "unsupported media variant type"},
		{name: "negative variant size", filename: "report.pdf", size: 1,
			variant:   &store.CreateMediaVariantParams{Type: model.VariantThumbnail, Width: 1, Height: 1, Size: -1},
			wantError: "size must not be negative"},
		{name: "nonpositive variant height", filename: "report.pdf", size: 1,
			variant:   &store.CreateMediaVariantParams{Type: model.VariantThumbnail, Width: 1, Height: 0, Size: 1},
			wantError: "height must be positive"},
		{name: "nonpositive variant width", filename: "report.pdf", size: 1,
			variant:   &store.CreateMediaVariantParams{Type: model.VariantThumbnail, Width: 0, Height: 1, Size: 1},
			wantError: "width must be positive"},
	} {
		t.Run("export "+testCase.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			mimeType := testCase.mimeType
			if mimeType == "" {
				mimeType = model.MimeTypePDF
			}
			medium, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
				Uuid: mediaUUID, Filename: testCase.filename, MimeType: mimeType, Size: testCase.size,
				Width: testCase.width, Height: testCase.height, UploadedBy: ts.User.ID,
				LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
			})
			if err != nil {
				t.Fatal(err)
			}
			if testCase.variant != nil {
				params := *testCase.variant
				params.MediaID = medium.ID
				params.CreatedAt = ts.Now
				if _, err := ts.Queries.CreateMediaVariant(ts.Ctx, params); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			err = NewExporter(ts.Queries, slog.Default()).ExportToWriter(
				ts.Ctx, ExportOptions{IncludeMedia: true}, &output,
			)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("ExportToWriter() error = %v", err)
			}
			if output.Len() != 0 {
				t.Fatalf("invalid metadata export published %d bytes", output.Len())
			}
		})
	}
}

func TestPageOnlyExportRejectsOnlyReferencedInvalidMediaUUID(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy := createExportMedia(t, ts, "legacy-media-1", "legacy.pdf", model.MimeTypePDF, 6)
	if _, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Unrelated", Slug: "unrelated", Body: "body", Status: "draft",
		AuthorID: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	exporter := NewExporter(ts.Queries, slog.Default())
	var unrelated bytes.Buffer
	if err := exporter.ExportToWriter(ts.Ctx, ExportOptions{IncludePages: true}, &unrelated); err != nil {
		t.Fatalf("unrelated legacy media blocked page export: %v", err)
	}
	if _, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Referenced", Slug: "referenced", Body: "body", Status: "draft",
		AuthorID: ts.User.ID, FeaturedImageID: sql.NullInt64{Int64: legacy.ID, Valid: true},
		LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	var referenced bytes.Buffer
	err = exporter.ExportToWriter(ts.Ctx, ExportOptions{IncludePages: true}, &referenced)
	if err == nil || !strings.Contains(err.Error(), "featured image has invalid media UUID") {
		t.Fatalf("ExportToWriter() error = %v", err)
	}
	if referenced.Len() != 0 {
		t.Fatalf("invalid page reference export published %d bytes", referenced.Len())
	}
}

func TestPageOnlyExportRejectsCaseFoldAmbiguousMediaReferences(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	lower := createExportMedia(t, ts, lowerUUID, "lower.pdf", model.MimeTypePDF, 5)
	upper := createExportMedia(t, ts, upperUUID, "upper.pdf", model.MimeTypePDF, 5)
	for index, medium := range []store.Medium{lower, upper} {
		if _, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
			Title: fmt.Sprintf("Page %d", index), Slug: fmt.Sprintf("page-%d", index),
			Body: "body", Status: "draft", AuthorID: ts.User.ID,
			FeaturedImageID: sql.NullInt64{Int64: medium.ID, Valid: true},
			LanguageCode:    language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	err = NewExporter(ts.Queries, slog.Default()).ExportToWriter(
		ts.Ctx, ExportOptions{IncludePages: true}, &output,
	)
	if err == nil || !strings.Contains(err.Error(), "conflicts by case with referenced UUID") {
		t.Fatalf("ExportToWriter() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("ambiguous page references published %d bytes", output.Len())
	}
}

func TestPageOnlyExportRejectsInvalidEmbeddedMediaUUID(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Legacy image", Slug: "legacy-image",
		Body: `<img src="/uploads/originals/legacy-media-1/photo.jpg">`, Status: "draft",
		AuthorID: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = NewExporter(ts.Queries, slog.Default()).ExportToWriter(
		ts.Ctx, ExportOptions{IncludePages: true}, &output,
	)
	if err == nil || !strings.Contains(err.Error(), "invalid UUID") {
		t.Fatalf("ExportToWriter() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("invalid embedded media URL published %d bytes", output.Len())
	}
}

// TestExportIgnoresExternalUploadLikeURLs keeps the media scan to this site's
// own URLs. A page linking to another host that happens to serve a path under
// /uploads/originals is not referencing local media, and reading the segment
// after that prefix as a media UUID failed the whole export over a resource
// this installation does not own.
func TestExportIgnoresExternalUploadLikeURLs(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "External CDN", Slug: "external-cdn",
		Body: `<img src="https://cdn.example/uploads/originals/avatar/file.png">` +
			`<a href="//cdn.example/uploads/originals/team/photo.png">team</a>`,
		Status: "draft", AuthorID: ts.User.ID, LanguageCode: language.Code,
		CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := NewExporter(ts.Queries, slog.Default()).ExportToWriter(
		ts.Ctx, ExportOptions{IncludePages: true}, &output,
	); err != nil {
		t.Fatalf("ExportToWriter() error = %v, want external URLs ignored", err)
	}
	if output.Len() == 0 {
		t.Fatal("export published no bytes")
	}
}

// TestExportStillRejectsRootRelativeInvalidMediaURL is the other half of the
// above: narrowing the scan to root-relative URLs must not stop it seeing a
// malformed local one, in an attribute or in prose.
func TestExportStillRejectsRootRelativeInvalidMediaURL(t *testing.T) {
	for name, body := range map[string]string{
		"attribute": `<img src="/uploads/originals/not-a-uuid/photo.jpg">`,
		"prose":     `See /uploads/originals/not-a-uuid/photo.jpg for details.`,
		"css":       `<div style="background:url(/uploads/originals/not-a-uuid/photo.jpg)"></div>`,
	} {
		t.Run(name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
				Title: "Local", Slug: "local", Body: body, Status: "draft",
				AuthorID: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
			}); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err = NewExporter(ts.Queries, slog.Default()).ExportToWriter(
				ts.Ctx, ExportOptions{IncludePages: true}, &output,
			)
			if err == nil || !strings.Contains(err.Error(), "invalid UUID") {
				t.Fatalf("ExportToWriter() error = %v, want the local media URL rejected", err)
			}
			if output.Len() != 0 {
				t.Fatalf("invalid local media URL published %d bytes", output.Len())
			}
		})
	}
}

func TestExportRejectsCaseFoldAmbiguityAcrossSelectedContent(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Lower", Slug: "lower",
		Body: `<img src="/uploads/originals/` + lowerUUID + `/photo.jpg">`, Status: "draft",
		AuthorID: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{
		Name: "Upper", Slug: "upper",
		Description:  sql.NullString{String: `<img src="/uploads/originals/` + upperUUID + `/photo.jpg">`, Valid: true},
		LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = NewExporter(ts.Queries, slog.Default()).ExportToWriter(
		ts.Ctx, ExportOptions{IncludePages: true, IncludeCategories: true}, &output,
	)
	if err == nil || !strings.Contains(err.Error(), "conflicts by case") {
		t.Fatalf("ExportToWriter() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("case-ambiguous content published %d bytes", output.Len())
	}
}

func TestExportAllowsCarriedMediaToNormalizeMixedCaseReferences(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	source := setupTest(t)
	defer source.Cleanup()
	language, err := source.Queries.GetDefaultLanguage(source.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	medium := createExportMedia(t, source, upperUUID, "report.pdf", model.MimeTypePDF, 6)
	if _, err := source.Queries.CreatePage(source.Ctx, store.CreatePageParams{
		Title: "Mixed case", Slug: "mixed-case",
		Body: `<a href="/uploads/originals/` + lowerUUID + `/report.pdf">report</a>`, Status: "draft",
		AuthorID: source.User.ID, FeaturedImageID: sql.NullInt64{Int64: medium.ID, Valid: true},
		LanguageCode: language.Code, CreatedAt: source.Now, UpdatedAt: source.Now,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := NewExporter(source.Queries, slog.Default()).Export(source.Ctx, ExportOptions{
		IncludePages: true, IncludeMedia: true,
	})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(data.Media) != 1 || data.Media[0].UUID != upperUUID || len(data.Pages) != 1 ||
		data.Pages[0].FeaturedImage == nil || data.Pages[0].FeaturedImage.UUID != upperUUID ||
		!strings.Contains(data.Pages[0].Body, lowerUUID) {
		t.Fatalf("Export() data = %+v", data)
	}

	destination := setupTest(t)
	defer destination.Cleanup()
	result, err := NewImporter(destination.Queries, destination.DB, slog.Default()).Import(
		destination.Ctx, data,
		ImportOptions{ConflictStrategy: ConflictSkip, ImportPages: true, ImportMedia: true},
	)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("Import() = (%+v, %v)", result, err)
	}
	importedMedia, err := destination.Queries.GetMediaByUUID(destination.Ctx, lowerUUID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := destination.Queries.GetPageBySlug(destination.Ctx, "mixed-case")
	if err != nil || page.FeaturedImageID.Int64 != importedMedia.ID || !strings.Contains(page.Body, lowerUUID) {
		t.Fatalf("imported page = %+v, error = %v", page, err)
	}
}
