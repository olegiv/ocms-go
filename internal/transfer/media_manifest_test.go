// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/HugoSmits86/nativewebp"

	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

type mediaArchiveEntry struct {
	name string
	body []byte
}

func mediaArchiveBytes(t *testing.T, data ExportData, entries []mediaArchiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	exportWriter, err := writer.Create("export.json")
	if err != nil {
		t.Fatalf("create export.json: %v", err)
	}
	if err := json.NewEncoder(exportWriter).Encode(data); err != nil {
		t.Fatalf("encode export.json: %v", err)
	}
	for _, entry := range entries {
		entryWriter, err := writer.Create(entry.name)
		if err != nil {
			t.Fatalf("create %q: %v", entry.name, err)
		}
		if _, err := entryWriter.Write(entry.body); err != nil {
			t.Fatalf("write %q: %v", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func mediaArchiveReader(t *testing.T, data ExportData, entries []mediaArchiveEntry) *zip.Reader {
	t.Helper()
	archive := mediaArchiveBytes(t, data, entries)
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	return reader
}

func zipArchiveWithRawExportJSON(t *testing.T, payload []byte, entries []mediaArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	exportWriter, err := writer.Create("export.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exportWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		entryWriter, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entryWriter.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestZipExportJSONIsBoundedAndStrict(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())

	t.Run("uncompressed limit", func(t *testing.T) {
		payload := []byte(`{"version":"` + ExportVersion + `","config":{"bomb":"` +
			strings.Repeat("a", maxZipExportJSONUncompressedBytes) + `"}}`)
		archive := zipArchiveWithRawExportJSON(t, payload, nil)
		if int64(len(archive)) > MaxZipArchiveBytes {
			t.Fatalf("compressed test archive = %d bytes", len(archive))
		}
		validation, err := importer.ValidateZipBytes(ts.Ctx, archive)
		if err != nil || validation == nil || validation.Valid || len(validation.Errors) == 0 ||
			!strings.Contains(validation.Errors[0].Message, "export.json exceeds max size") {
			t.Fatalf("ValidateZipBytes() = (%+v, %v)", validation, err)
		}
		if _, err := importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{}); err == nil ||
			!strings.Contains(err.Error(), "export.json exceeds max size") {
			t.Fatalf("ImportFromZipBytes() error = %v", err)
		}
	})

	t.Run("trailing data", func(t *testing.T) {
		archive := zipArchiveWithRawExportJSON(t,
			[]byte(`{"version":"`+ExportVersion+`"} trailing`), nil)
		validation, err := importer.ValidateZipBytes(ts.Ctx, archive)
		if err != nil || validation == nil || validation.Valid || len(validation.Errors) == 0 ||
			!strings.Contains(validation.Errors[0].Message, "failed to parse export.json") {
			t.Fatalf("ValidateZipBytes() = (%+v, %v)", validation, err)
		}
		if _, err := importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{}); err == nil ||
			!strings.Contains(err.Error(), "failed to parse export.json") {
			t.Fatalf("ImportFromZipBytes() error = %v", err)
		}
	})
}

func TestZipRejectsUnknownFilesAndDirectories(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	payload, err := json.Marshal(ExportData{Version: ExportVersion})
	if err != nil {
		t.Fatal(err)
	}
	for _, entryName := range []string{
		"payload.bin",
		"../payload.bin",
		"media/originals/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/",
	} {
		t.Run(entryName, func(t *testing.T) {
			archive := zipArchiveWithRawExportJSON(t, payload, []mediaArchiveEntry{{name: entryName}})
			validation, err := importer.ValidateZipBytes(ts.Ctx, archive)
			if err != nil || validation == nil || validation.Valid || len(validation.Errors) == 0 ||
				!strings.Contains(validation.Errors[0].Message, "archive") {
				t.Fatalf("ValidateZipBytes() = (%+v, %v)", validation, err)
			}
			if _, err := importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{}); err == nil {
				t.Fatal("ImportFromZipBytes() error = nil")
			}
		})
	}
}

func TestZipRawCentralDirectoryEntryCountIsBoundedBeforeParsing(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for index := 0; index < maxZipEntries+1; index++ {
		if _, err := writer.Create(fmt.Sprintf("entry-%05d", index)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buffer.Bytes()
	eocd := bytes.LastIndex(archive, []byte{'P', 'K', 5, 6})
	if eocd < 0 {
		t.Fatal("end-of-central-directory record not found")
	}
	// A malicious footer can claim a small 16-bit count. The raw central
	// directory still contains too many headers and must be bounded before
	// archive/zip constructs its File slice.
	binary.LittleEndian.PutUint16(archive[eocd+8:eocd+10], 1)
	binary.LittleEndian.PutUint16(archive[eocd+10:eocd+12], 1)
	reader := bytes.NewReader(archive)
	err := validateZipContainer(reader, int64(len(archive)))
	if err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("validateZipContainer() error = %v", err)
	}
}

func transferTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return output.Bytes()
}

func transferTestWebP(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var output bytes.Buffer
	if err := nativewebp.Encode(&output, img, nil); err != nil {
		t.Fatalf("encode WebP: %v", err)
	}
	return output.Bytes()
}

func TestBuildMediaZipManifestAcceptsCanonicalUUIDCase(t *testing.T) {
	for _, mediaUUID := range []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"550E8400-E29B-41D4-A716-446655440000",
	} {
		t.Run(mediaUUID, func(t *testing.T) {
			archivePath := "media/originals/" + mediaUUID + "/report.pdf"
			data := ExportData{Media: []ExportMedia{{
				UUID: mediaUUID, Filename: "report.pdf", Size: 6, FilePath: archivePath,
			}}}
			reader := mediaArchiveReader(t, data, []mediaArchiveEntry{{
				name: archivePath,
				body: []byte("report"),
			}})
			manifest, err := buildMediaZipManifest(reader, &data)
			if err != nil {
				t.Fatalf("buildMediaZipManifest() error = %v", err)
			}
			normalizedUUID := strings.ToLower(mediaUUID)
			if len(manifest.entries) != 1 || len(manifest.affectedUUIDs) != 1 || manifest.affectedUUIDs[0] != normalizedUUID {
				t.Fatalf("manifest = %+v, want one entry for %q", manifest, normalizedUUID)
			}
		})
	}
}

func TestExtractMediaFilesFirstEntryFailurePrunesReservedDirectory(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		FilePath: "media/originals/" + mediaUUID + "/report.pdf",
	}}}
	reader := mediaArchiveReader(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: []byte("report"),
	}})
	manifest, err := buildMediaZipManifest(reader, &data)
	if err != nil {
		t.Fatal(err)
	}
	manifest.entries[0].file.CRC32 ^= 1
	uploadDir := t.TempDir()
	uploadRoot, err := imaging.OpenUploadRoot(uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = uploadRoot.Close() }()
	ledger, err := (&Importer{}).extractMediaFiles(manifest, uploadRoot)
	if err == nil {
		t.Fatal("extractMediaFiles() error = nil after CRC corruption")
	}
	if cleanupErr := cleanupOwnedMediaFiles(uploadRoot, ledger, nil); cleanupErr != nil {
		t.Fatalf("cleanupOwnedMediaFiles() error = %v", cleanupErr)
	}
	if _, err := os.Stat(filepath.Join(uploadDir, model.OriginalsDir, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reserved UUID directory remains after first-entry failure: %v", err)
	}
}

func TestBuildMediaZipManifestRejectsIncoherentEntries(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	tests := []struct {
		name      string
		data      ExportData
		entries   []mediaArchiveEntry
		wantError string
	}{
		{
			name: "duplicate archive path",
			data: ExportData{Media: []ExportMedia{{
				UUID: lowerUUID, Filename: "report.pdf", Size: 3,
				FilePath: "media/originals/" + lowerUUID + "/report.pdf",
			}}},
			entries: []mediaArchiveEntry{
				{name: "media/originals/" + lowerUUID + "/report.pdf", body: []byte("one")},
				{name: "media/originals/" + lowerUUID + "/report.pdf", body: []byte("two")},
			},
			wantError: "duplicate media archive path",
		},
		{
			name:      "undeclared UUID",
			data:      ExportData{Media: []ExportMedia{{UUID: lowerUUID, Filename: "report.pdf", Size: 1, FilePath: "media/originals/" + lowerUUID + "/report.pdf"}}},
			entries:   []mediaArchiveEntry{{name: "media/originals/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/report.pdf", body: []byte("x")}},
			wantError: "undeclared UUID",
		},
		{
			name:      "filename mismatch",
			data:      ExportData{Media: []ExportMedia{{UUID: lowerUUID, Filename: "report.pdf", Size: 1, FilePath: "media/originals/" + lowerUUID + "/report.pdf"}}},
			entries:   []mediaArchiveEntry{{name: "media/originals/" + lowerUUID + "/other.pdf", body: []byte("x")}},
			wantError: "does not match declared filename",
		},
		{
			name:      "unsupported storage directory",
			data:      ExportData{Media: []ExportMedia{{UUID: lowerUUID, Filename: "report.pdf", Size: 1, FilePath: "media/originals/" + lowerUUID + "/report.pdf"}}},
			entries:   []mediaArchiveEntry{{name: "media/legacy/" + lowerUUID + "/report.pdf", body: []byte("x")}},
			wantError: "unsupported storage directory",
		},
		{
			name:      "declared size mismatch",
			data:      ExportData{Media: []ExportMedia{{UUID: lowerUUID, Filename: "report.pdf", Size: 2, FilePath: "media/originals/" + lowerUUID + "/report.pdf"}}},
			entries:   []mediaArchiveEntry{{name: "media/originals/" + lowerUUID + "/report.pdf", body: []byte("x")}},
			wantError: "does not match declared size",
		},
		{
			name: "case-insensitive duplicate metadata UUID",
			data: ExportData{Media: []ExportMedia{
				{UUID: lowerUUID, Filename: "report.pdf"},
				{UUID: upperUUID, Filename: "report.pdf"},
			}},
			wantError: "duplicate media UUID",
		},
		{
			name: "declared original is missing",
			data: ExportData{Media: []ExportMedia{{
				UUID: lowerUUID, Filename: "report.pdf",
				FilePath: "media/originals/" + lowerUUID + "/report.pdf",
			}}},
			wantError: "declares missing media archive entry",
		},
		{
			name: "declared variant is missing",
			data: ExportData{Media: []ExportMedia{{
				UUID: lowerUUID, Filename: "report.pdf",
				Variants: []ExportVariant{{
					Type:     model.VariantThumbnail,
					Width:    1,
					Height:   1,
					FilePath: "media/thumbnail/" + lowerUUID + "/report.pdf",
				}},
			}}},
			wantError: "declares missing media archive entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildMediaZipManifest(mediaArchiveReader(t, tt.data, tt.entries), &tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("buildMediaZipManifest() error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestValidateZipReportsFullMediaManifestFailure(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	data := ExportData{
		Version: ExportVersion,
		Media: []ExportMedia{{
			UUID: mediaUUID, Filename: "report.pdf", Size: 6,
			FilePath: "media/originals/" + mediaUUID + "/report.pdf",
		}},
	}
	archive := mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: "media/originals/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/report.pdf",
		body: []byte("orphan"),
	}})
	result, err := NewImporter(ts.Queries, ts.DB, slog.Default()).ValidateZipBytes(ts.Ctx, archive)
	if err != nil {
		t.Fatalf("ValidateZipBytes() error = %v", err)
	}
	if result.Valid || len(result.Errors) == 0 || !strings.Contains(result.Errors[0].Message, "undeclared UUID") {
		t.Fatalf("ValidateZipBytes() = %+v, want undeclared UUID failure", result)
	}
}

func TestImportFromZipManifestFailureDoesNotWrite(t *testing.T) {
	const canonicalUUID = "550e8400-e29b-41d4-a716-446655440000"
	tests := []struct {
		name    string
		data    ExportData
		entries []mediaArchiveEntry
	}{
		{
			name: "noncanonical metadata and path",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: "legacy-media-1", Filename: "report.pdf",
			}}},
			entries: []mediaArchiveEntry{{name: "media/originals/legacy-media-1/report.pdf", body: []byte("x")}},
		},
		{
			name: "noncanonical path",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: canonicalUUID, Filename: "report.pdf", Size: 1,
				FilePath: "media/originals/" + canonicalUUID + "/report.pdf",
			}}},
			entries: []mediaArchiveEntry{{name: "media/originals/legacy-media-1/report.pdf", body: []byte("x")}},
		},
		{
			name: "filename mismatch",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: canonicalUUID, Filename: "report.pdf", Size: 1,
				FilePath: "media/originals/" + canonicalUUID + "/report.pdf",
			}}},
			entries: []mediaArchiveEntry{{name: "media/originals/" + canonicalUUID + "/other.pdf", body: []byte("x")}},
		},
		{
			name: "unsupported storage directory",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: canonicalUUID, Filename: "report.pdf", Size: 1,
				FilePath: "media/originals/" + canonicalUUID + "/report.pdf",
			}}},
			entries: []mediaArchiveEntry{{name: "media/legacy/" + canonicalUUID + "/report.pdf", body: []byte("x")}},
		},
		{
			name: "undeclared UUID",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: canonicalUUID, Filename: "report.pdf", Size: 1,
				FilePath: "media/originals/" + canonicalUUID + "/report.pdf",
			}}},
			entries: []mediaArchiveEntry{{name: "media/originals/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/report.pdf", body: []byte("x")}},
		},
		{
			name: "duplicate path",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: canonicalUUID, Filename: "report.pdf", Size: 3,
				FilePath: "media/originals/" + canonicalUUID + "/report.pdf",
			}}},
			entries: []mediaArchiveEntry{
				{name: "media/originals/" + canonicalUUID + "/report.pdf", body: []byte("one")},
				{name: "media/originals/" + canonicalUUID + "/report.pdf", body: []byte("two")},
			},
		},
		{
			name: "missing original",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: canonicalUUID, Filename: "report.pdf", Size: 1,
				FilePath: "media/originals/" + canonicalUUID + "/report.pdf",
			}}},
		},
		{
			name: "variants only",
			data: ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: canonicalUUID, Filename: "report.pdf", Size: 1,
				Variants: []ExportVariant{{
					Type: model.VariantThumbnail, Width: 1, Height: 1, Size: 1,
					FilePath: "media/thumbnail/" + canonicalUUID + "/report.pdf",
				}},
			}}},
			entries: []mediaArchiveEntry{{name: "media/thumbnail/" + canonicalUUID + "/report.pdf", body: []byte("x")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			uploadRoot := filepath.Join(t.TempDir(), "uploads-not-created")
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			opts := ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true}
			_, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, tt.data, tt.entries), opts)
			if err == nil {
				t.Fatal("ImportFromZipBytes() error = nil")
			}
			if _, statErr := os.Stat(uploadRoot); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid manifest created upload root: %v", statErr)
			}
			count, countErr := ts.Queries.CountMedia(ts.Ctx)
			if countErr != nil || count != 0 {
				t.Fatalf("media count = %d, error = %v; want no database writes", count, countErr)
			}
		})
	}
}

func TestImportFromZipFullPreflightFailureDoesNotWrite(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	uploadRoot := filepath.Join(t.TempDir(), "uploads-not-created")
	data := ExportData{
		Version: ExportVersion,
		Media: []ExportMedia{{
			UUID: mediaUUID, Filename: "report.pdf", UploadedBy: ts.User.Email, Size: 6,
			FilePath: "media/originals/" + mediaUUID + "/report.pdf",
		}},
		Pages: []ExportPage{{
			ID: 1, Title: "Broken relation", Slug: "broken-relation", AuthorEmail: ts.User.Email,
			Categories: []string{"missing-category"},
		}},
	}
	archive := mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: "media/originals/" + mediaUUID + "/report.pdf",
		body: []byte("report"),
	}})
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	opts := ImportOptions{
		ConflictStrategy: ConflictSkip,
		ImportMedia:      true,
		ImportMediaFiles: true,
		ImportPages:      true,
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, archive, opts)
	if err == nil || result == nil || result.Success {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v), want failed relation preflight", result, err)
	}
	if _, statErr := os.Stat(uploadRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed relation preflight created upload root: %v", statErr)
	}
}

func TestImportFromZipAcceptsCanonicalUUIDCase(t *testing.T) {
	for _, mediaUUID := range []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"550E8400-E29B-41D4-A716-446655440000",
	} {
		t.Run(mediaUUID, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			uploadRoot := t.TempDir()
			data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
				FilePath: "media/originals/" + mediaUUID + "/report.pdf",
			}}}
			archive := mediaArchiveBytes(t, data, []mediaArchiveEntry{{
				name: "media/originals/" + mediaUUID + "/report.pdf",
				body: []byte("report"),
			}})
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			result, err := importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{
				ConflictStrategy: ConflictSkip,
				ImportMedia:      true,
				ImportMediaFiles: true,
			})
			if err != nil || result == nil || !result.Success {
				t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
			}
			normalizedUUID := strings.ToLower(mediaUUID)
			if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, normalizedUUID); err != nil {
				t.Fatalf("GetMediaByUUID() error = %v", err)
			}
			if content, err := os.ReadFile(filepath.Join(uploadRoot, model.OriginalsDir, normalizedUUID, "report.pdf")); err != nil || string(content) != "report" {
				t.Fatalf("imported file = %q, error = %v", content, err)
			}
			if mediaUUID != normalizedUUID {
				if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("uppercase database identity lookup = %v, want sql.ErrNoRows", err)
				}
				directories, err := os.ReadDir(filepath.Join(uploadRoot, model.OriginalsDir))
				if err != nil || len(directories) != 1 || directories[0].Name() != normalizedUUID {
					t.Fatalf("filesystem UUID directories = %+v, error = %v", directories, err)
				}
			}
		})
	}
}

func TestImportFromZipUppercaseArchiveMatchesLowercaseDestinationIdentity(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
		Uuid: lowerUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		FilePath: "media/originals/" + upperUUID + "/report.pdf",
	}}, Pages: []ExportPage{{
		ID: 1, Title: "Embedded media", Slug: "embedded-media", Status: "draft", AuthorEmail: ts.User.Email,
		Body:          `<a href="/uploads/originals/` + upperUUID + `/report.pdf">report</a>`,
		FeaturedImage: &ExportMediaRef{UUID: upperUUID, Filename: "report.pdf"},
	}}}
	uploadRoot := t.TempDir()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: []byte("report"),
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true, ImportPages: true})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
	}
	media, err := listAllMediaForLookup(ts.Ctx, ts.Queries)
	if err != nil || len(media) != 1 || media[0].ID != existing.ID || media[0].Uuid != lowerUUID {
		t.Fatalf("destination media = %+v, error = %v", media, err)
	}
	page, err := ts.Queries.GetPageBySlug(ts.Ctx, "embedded-media")
	if err != nil || !strings.Contains(page.Body, "/uploads/originals/"+lowerUUID+"/report.pdf") ||
		page.FeaturedImageID.Int64 != existing.ID {
		t.Fatalf("normalized page = %+v, error = %v", page, err)
	}
	if content, err := os.ReadFile(filepath.Join(uploadRoot, model.OriginalsDir, lowerUUID, "report.pdf")); err != nil || string(content) != "report" {
		t.Fatalf("lowercase restored file = %q, error = %v", content, err)
	}
	directories, err := os.ReadDir(filepath.Join(uploadRoot, model.OriginalsDir))
	if err != nil || len(directories) != 1 || directories[0].Name() != lowerUUID {
		t.Fatalf("filesystem UUID directories = %+v, error = %v", directories, err)
	}
}

func TestMediaImportRejectsLegacyDestinationCaseCollisionBeforeWrites(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
		Uuid: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		FilePath: "media/originals/" + upperUUID + "/report.pdf",
	}}}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	preview, err := importer.ValidateData(ts.Ctx, &data)
	if err != nil || preview.Valid || len(preview.Errors) == 0 ||
		!strings.Contains(preview.Errors[len(preview.Errors)-1].Message, "conflicts by case") {
		t.Fatalf("ValidateData() = (%+v, %v)", preview, err)
	}
	for _, dryRun := range []bool{true, false} {
		result, err := importer.Import(ts.Ctx, &data, ImportOptions{
			DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMedia: true,
		})
		if err == nil || result == nil || !strings.Contains(err.Error(), "conflicts by case") {
			t.Fatalf("dry_run=%t Import() = (%+v, %v)", dryRun, result, err)
		}
	}
	uploadRoot := filepath.Join(t.TempDir(), "uploads-not-created")
	importer.SetUploadDir(uploadRoot)
	archive := mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: []byte("report"),
	}})
	for _, dryRun := range []bool{true, false} {
		_, err := importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{
			DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true,
		})
		if err == nil || !strings.Contains(err.Error(), "conflicts by case") {
			t.Fatalf("dry_run=%t ImportFromZipBytes() error = %v", dryRun, err)
		}
	}
	if _, err := os.Stat(uploadRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("case collision created upload root: %v", err)
	}
	media, err := listAllMediaForLookup(ts.Ctx, ts.Queries)
	if err != nil || len(media) != 1 || media[0].Uuid != upperUUID || strings.EqualFold(media[0].Uuid, lowerUUID) == false {
		t.Fatalf("destination media = %+v, error = %v", media, err)
	}
}

func TestPageOnlyImportPreservesExactUppercaseMediaIdentityAndURLs(t *testing.T) {
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	medium, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
		Uuid: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	mediaURL := "/uploads/originals/" + upperUUID + "/report.pdf"
	data := &ExportData{Version: ExportVersion, Pages: []ExportPage{{
		ID: 1, Title: "Legacy media case", Slug: "legacy-media-case", Status: "draft",
		AuthorEmail: ts.User.Email, Body: `<a href="` + mediaURL + `">report</a>`,
		FeaturedImage: &ExportMediaRef{UUID: upperUUID, Filename: "report.pdf"},
		SEO: &ExportPageSEO{
			OgImage: &ExportMediaRef{UUID: upperUUID, Filename: "report.pdf"},
		},
	}}}
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	preview, err := importer.ValidateData(ts.Ctx, data)
	if err != nil || !preview.Valid {
		t.Fatalf("ValidateData() = (%+v, %v)", preview, err)
	}
	result, err := importer.Import(ts.Ctx, data, ImportOptions{
		ConflictStrategy: ConflictSkip, ImportPages: true,
	})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("Import() = (%+v, %v)", result, err)
	}
	page, err := ts.Queries.GetPageBySlug(ts.Ctx, "legacy-media-case")
	if err != nil || page.FeaturedImageID.Int64 != medium.ID || page.OgImageID.Int64 != medium.ID ||
		!strings.Contains(page.Body, mediaURL) {
		t.Fatalf("imported page = %+v, error = %v", page, err)
	}
	if data.Pages[0].FeaturedImage.UUID != upperUUID || !strings.Contains(data.Pages[0].Body, mediaURL) {
		t.Fatalf("caller payload was mutated: %+v", data.Pages[0])
	}
}

func TestFullArchiveMediaDeselectedPreservesExactUppercasePageReferences(t *testing.T) {
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry_run=%t", dryRun), func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			medium, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
				Uuid: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
				UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
			})
			if err != nil {
				t.Fatal(err)
			}
			mediaURL := "/uploads/originals/" + upperUUID + "/report.pdf"
			data := &ExportData{Version: ExportVersion,
				Media: []ExportMedia{{UUID: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6}},
				Pages: []ExportPage{{
					ID: 1, Title: "Media deselected", Slug: "media-deselected", Status: "draft",
					AuthorEmail: ts.User.Email, Body: mediaURL,
					FeaturedImage: &ExportMediaRef{UUID: upperUUID, Filename: "report.pdf"},
				}},
			}
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			preview, err := importer.ValidateZipBytes(ts.Ctx, mediaArchiveBytes(t, *data, nil))
			if err != nil || preview == nil || !preview.Valid {
				t.Fatalf("ValidateZipBytes() = (%+v, %v)", preview, err)
			}
			result, err := importer.Import(ts.Ctx, data, ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true,
			})
			if err != nil || result == nil || !result.Success {
				t.Fatalf("Import() = (%+v, %v)", result, err)
			}
			if dryRun {
				if _, err := ts.Queries.GetPageBySlug(ts.Ctx, "media-deselected"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("dry-run page lookup = %v, want sql.ErrNoRows", err)
				}
				return
			}
			page, err := ts.Queries.GetPageBySlug(ts.Ctx, "media-deselected")
			if err != nil || page.FeaturedImageID.Int64 != medium.ID || !strings.Contains(page.Body, mediaURL) {
				t.Fatalf("imported page = %+v, error = %v", page, err)
			}
			if data.Media[0].UUID != upperUUID || data.Pages[0].FeaturedImage.UUID != upperUUID ||
				!strings.Contains(data.Pages[0].Body, upperUUID) {
				t.Fatalf("caller payload was mutated: %+v", data)
			}
		})
	}
}

func TestPreviewAllowsInvalidOptionalMediaOnlyWithViableContentImport(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"

	t.Run("exact destination reference", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()
		language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		existing, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
			Uuid: mediaUUID, Filename: "safe.png", MimeType: model.MimeTypePNG, Size: 1,
			UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		data := &ExportData{Version: ExportVersion,
			Media: []ExportMedia{{
				UUID: mediaUUID, Filename: "../unsafe.png", MimeType: model.MimeTypePNG, Size: 1,
			}},
			Pages: []ExportPage{{
				ID: 1, Title: "Optional media", Slug: "optional-media", Status: "draft",
				AuthorEmail:   ts.User.Email,
				FeaturedImage: &ExportMediaRef{UUID: mediaUUID, Filename: "safe.png"},
			}},
		}
		importer := NewImporter(ts.Queries, ts.DB, slog.Default())
		preview, err := importer.ValidateData(ts.Ctx, data)
		if err != nil || preview == nil || !preview.Valid {
			t.Fatalf("ValidateData() = (%+v, %v), want viable media-deselected preview", preview, err)
		}

		selected, err := importer.Import(ts.Ctx, data, ImportOptions{
			DryRun: true, ConflictStrategy: ConflictSkip, ImportMedia: true, ImportPages: true,
		})
		if err == nil || selected == nil || !importErrorsContain(selected.Errors, "safe path segment") {
			t.Fatalf("media-selected Import() = (%+v, %v), want metadata rejection", selected, err)
		}
		for _, dryRun := range []bool{true, false} {
			result, err := importer.Import(ts.Ctx, data, ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportPages: true,
			})
			if err != nil || result == nil || !result.Success {
				t.Fatalf("dry_run=%t page-only Import() = (%+v, %v)", dryRun, result, err)
			}
		}
		page, err := ts.Queries.GetPageBySlug(ts.Ctx, "optional-media")
		if err != nil || !page.FeaturedImageID.Valid || page.FeaturedImageID.Int64 != existing.ID {
			t.Fatalf("imported page = %+v, error = %v", page, err)
		}
	})

	t.Run("unavailable dependency", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()
		data := &ExportData{Version: ExportVersion,
			Media: []ExportMedia{{
				UUID: mediaUUID, Filename: "../unsafe.png", MimeType: model.MimeTypePNG, Size: 1,
			}},
			Pages: []ExportPage{{
				ID: 1, Title: "Required bad media", Slug: "required-bad-media", Status: "draft",
				AuthorEmail:   ts.User.Email,
				FeaturedImage: &ExportMediaRef{UUID: mediaUUID, Filename: "safe.png"},
			}},
		}
		importer := NewImporter(ts.Queries, ts.DB, slog.Default())
		preview, err := importer.ValidateData(ts.Ctx, data)
		if err != nil || preview == nil || preview.Valid ||
			!importErrorsContain(preview.Errors, "references unavailable media UUID") {
			t.Fatalf("ValidateData() = (%+v, %v), want no viable option set", preview, err)
		}
		for _, importMedia := range []bool{true, false} {
			result, err := importer.Import(ts.Ctx, data, ImportOptions{
				DryRun: true, ConflictStrategy: ConflictSkip,
				ImportMedia: importMedia, ImportPages: true,
			})
			if err == nil || result == nil || result.Success {
				t.Fatalf("import_media=%t Import() = (%+v, %v), want rejection", importMedia, result, err)
			}
		}
	})
}

func TestFileOnlyRestoreNormalizesCarriedMediaReferences(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry_run=%t", dryRun), func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			medium, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
				Uuid: lowerUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
				UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
			})
			if err != nil {
				t.Fatal(err)
			}
			data := ExportData{Version: ExportVersion,
				Media: []ExportMedia{{
					UUID: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
					FilePath: "media/originals/" + upperUUID + "/report.pdf",
				}},
				Pages: []ExportPage{{
					ID: 1, Title: "File restore", Slug: "file-restore", Status: "draft",
					AuthorEmail:   ts.User.Email,
					Body:          `<a href="/uploads/originals/` + upperUUID + `/report.pdf">report</a>`,
					FeaturedImage: &ExportMediaRef{UUID: upperUUID, Filename: "report.pdf"},
				}},
			}
			uploadRoot := filepath.Join(t.TempDir(), "uploads")
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
				name: data.Media[0].FilePath, body: []byte("report"),
			}}), ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMediaFiles: true, ImportPages: true,
			})
			if err != nil || result == nil || !result.Success {
				t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
			}
			if dryRun {
				if _, err := os.Stat(uploadRoot); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("dry-run upload root = %v, want absent", err)
				}
				return
			}
			page, err := ts.Queries.GetPageBySlug(ts.Ctx, "file-restore")
			if err != nil || page.FeaturedImageID.Int64 != medium.ID ||
				!strings.Contains(page.Body, "/uploads/originals/"+lowerUUID+"/report.pdf") {
				t.Fatalf("imported page = %+v, error = %v", page, err)
			}
		})
	}
}

func TestFileOnlyRestoreRejectsInvalidMediaMetadataBeforeOpeningUploads(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	pngBytes := transferTestPNG(t, 2, 2)
	if _, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
		Uuid: mediaUUID, Filename: "photo.jpg", MimeType: model.MimeTypePNG, Size: int64(len(pngBytes)),
		UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "photo.jpg", MimeType: model.MimeTypePNG, Size: int64(len(pngBytes)),
		FilePath: "media/originals/" + mediaUUID + "/photo.jpg",
	}}}
	archive := mediaArchiveBytes(t, data, []mediaArchiveEntry{{name: data.Media[0].FilePath, body: pngBytes}})
	uploadRoot := filepath.Join(t.TempDir(), "uploads-not-created")
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	for _, dryRun := range []bool{true, false} {
		result, err := importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{
			DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMediaFiles: true,
		})
		if err == nil || result == nil || result.Success ||
			!strings.Contains(err.Error(), "does not match image MIME type") {
			t.Fatalf("dry_run=%t ImportFromZipBytes() = (%+v, %v)", dryRun, result, err)
		}
	}
	if _, err := os.Stat(uploadRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected file-only restore opened uploads root: %v", err)
	}
}

func TestURLOnlyContentRequiresExactDestinationMediaIdentity(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	lowerURL := "/uploads/originals/" + lowerUUID + "/report.pdf"
	for _, testCase := range []struct {
		name string
		data *ExportData
		opts ImportOptions
	}{
		{
			name: "page body",
			data: &ExportData{Version: ExportVersion, Pages: []ExportPage{{
				ID: 1, Title: "URL only", Slug: "url-only", Status: "draft", Body: lowerURL,
			}}},
			opts: ImportOptions{ConflictStrategy: ConflictSkip, ImportPages: true},
		},
		{
			name: "config value",
			data: &ExportData{Version: ExportVersion, Config: map[string]string{"site_logo": lowerURL}},
			opts: ImportOptions{ConflictStrategy: ConflictSkip, ImportConfig: true},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
				Uuid: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
				UploadedBy: ts.User.ID, LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
			}); err != nil {
				t.Fatal(err)
			}
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			preview, err := importer.ValidateData(ts.Ctx, testCase.data)
			if err != nil || preview.Valid || len(preview.Errors) == 0 ||
				!strings.Contains(preview.Errors[len(preview.Errors)-1].Message, "conflicts by case") {
				t.Fatalf("ValidateData() = (%+v, %v)", preview, err)
			}
			for _, dryRun := range []bool{true, false} {
				opts := testCase.opts
				opts.DryRun = dryRun
				result, err := importer.Import(ts.Ctx, testCase.data, opts)
				if err == nil || result == nil || !strings.Contains(err.Error(), "conflicts by case") {
					t.Fatalf("dry_run=%t Import() = (%+v, %v)", dryRun, result, err)
				}
			}
			if len(testCase.data.Pages) > 0 {
				if _, err := ts.Queries.GetPageBySlug(ts.Ctx, testCase.data.Pages[0].Slug); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("rejected page lookup error = %v", err)
				}
			}
			if len(testCase.data.Config) > 0 {
				if _, err := ts.Queries.GetConfigByKey(ts.Ctx, "site_logo"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("rejected config lookup error = %v", err)
				}
			}
		})
	}
}

func TestMediaImportRejectsCaseFoldStorageCollisionBeforeWrites(t *testing.T) {
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	uploadRoot := t.TempDir()
	sentinelPath := filepath.Join(uploadRoot, model.OriginalsDir, upperUUID, "sentinel.txt")
	if err := os.MkdirAll(filepath.Dir(sentinelPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinelPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: upperUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		FilePath: "media/originals/" + upperUUID + "/report.pdf",
	}}}
	archive := mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: []byte("report"),
	}})
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	for _, dryRun := range []bool{true, false} {
		_, err := importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{
			DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true,
		})
		if err == nil || !strings.Contains(err.Error(), "media storage already exists for UUID") {
			t.Fatalf("dry_run=%t ImportFromZipBytes() error = %v", dryRun, err)
		}
	}
	content, err := os.ReadFile(sentinelPath)
	if err != nil || string(content) != "keep" {
		t.Fatalf("case-fold collision sentinel = %q, error = %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(uploadRoot, model.OriginalsDir, lowerUUID, "report.pdf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("normalized media file was written: %v", err)
	}
}

func TestNormalizedTransferMediaIdentitiesRewritesKnownURLsWithoutMutatingCaller(t *testing.T) {
	const upperUUID = "550E8400-E29B-41D4-A716-446655440000"
	const lowerUUID = "550e8400-e29b-41d4-a716-446655440000"
	upperURL := "/uploads/originals/" + upperUUID + "/image.png"
	lowerURL := "/uploads/originals/" + lowerUUID + "/image.png"
	data := &ExportData{
		Media: []ExportMedia{{UUID: upperUUID, Filename: "image.png"}},
		Pages: []ExportPage{{Body: upperURL}},
		Menus: []ExportMenu{{Items: []ExportMenuItem{{Children: []ExportMenuItem{{URL: upperURL}}}}}},
		Forms: []ExportForm{{
			Description: upperURL,
			Fields:      []ExportFormField{{HelpText: upperURL}},
			Submissions: []ExportFormSubmission{{Data: upperURL}},
		}},
		Config: map[string]string{"logo": upperURL, "unknown": "/uploads/legacy/" + upperUUID + "/image.png"},
	}
	normalized := normalizedTransferMediaIdentities(data, true)
	if normalized.Media[0].UUID != lowerUUID || normalized.Pages[0].Body != lowerURL ||
		normalized.Menus[0].Items[0].Children[0].URL != lowerURL ||
		normalized.Forms[0].Description != lowerURL || normalized.Forms[0].Fields[0].HelpText != lowerURL ||
		normalized.Forms[0].Submissions[0].Data != lowerURL || normalized.Config["logo"] != lowerURL {
		t.Fatalf("normalized payload = %+v", normalized)
	}
	if normalized.Config["unknown"] != data.Config["unknown"] {
		t.Fatalf("unknown storage URL was rewritten: %q", normalized.Config["unknown"])
	}
	if data.Media[0].UUID != upperUUID || data.Pages[0].Body != upperURL ||
		data.Menus[0].Items[0].Children[0].URL != upperURL || data.Config["logo"] != upperURL {
		t.Fatalf("caller payload was mutated: %+v", data)
	}
}

func TestImportFromZipFileOnlyRestoreRequiresMatchingDestination(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		FilePath: "media/originals/" + mediaUUID + "/report.pdf",
	}}}
	archiveEntries := []mediaArchiveEntry{{
		name: "media/originals/" + mediaUUID + "/report.pdf",
		body: []byte("report"),
	}}

	t.Run("missing destination row fails before write", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()
		uploadRoot := filepath.Join(t.TempDir(), "uploads-not-created")
		importer := NewImporter(ts.Queries, ts.DB, slog.Default())
		importer.SetUploadDir(uploadRoot)
		_, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, archiveEntries), ImportOptions{
			ConflictStrategy: ConflictSkip,
			ImportMediaFiles: true,
		})
		if err == nil || !strings.Contains(err.Error(), "no existing destination row") {
			t.Fatalf("ImportFromZipBytes() error = %v", err)
		}
		if _, statErr := os.Stat(uploadRoot); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("file-only preflight created upload root: %v", statErr)
		}
	})

	t.Run("matching destination row accepts restore", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()
		language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
		if err != nil {
			t.Fatalf("GetDefaultLanguage() error = %v", err)
		}
		_, err = ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
			Uuid: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
			Width: sql.NullInt64{}, Height: sql.NullInt64{}, UploadedBy: ts.User.ID,
			LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatalf("CreateMedia() error = %v", err)
		}
		uploadRoot := t.TempDir()
		importer := NewImporter(ts.Queries, ts.DB, slog.Default())
		importer.SetUploadDir(uploadRoot)
		result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, archiveEntries), ImportOptions{
			ConflictStrategy: ConflictSkip,
			ImportMediaFiles: true,
		})
		if err != nil || result == nil || !result.Success {
			t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
		}
		if _, err := os.Stat(filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "report.pdf")); err != nil {
			t.Fatalf("restored file missing: %v", err)
		}
	})

	t.Run("existing UUID storage is never overwritten", func(t *testing.T) {
		ts := setupTest(t)
		defer ts.Cleanup()
		language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
		if err != nil {
			t.Fatalf("GetDefaultLanguage() error = %v", err)
		}
		_, err = ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
			Uuid: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
			Width: sql.NullInt64{}, Height: sql.NullInt64{}, UploadedBy: ts.User.ID,
			LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatalf("CreateMedia() error = %v", err)
		}
		uploadRoot := t.TempDir()
		existingPath := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "report.pdf")
		if err := os.MkdirAll(filepath.Dir(existingPath), 0o750); err != nil {
			t.Fatalf("MkdirAll(existing): %v", err)
		}
		if err := os.WriteFile(existingPath, []byte("old"), 0o600); err != nil {
			t.Fatalf("WriteFile(existing): %v", err)
		}
		importer := NewImporter(ts.Queries, ts.DB, slog.Default())
		importer.SetUploadDir(uploadRoot)
		archive := mediaArchiveBytes(t, data, archiveEntries)
		for _, dryRun := range []bool{true, false} {
			_, err = importer.ImportFromZipBytes(ts.Ctx, archive, ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMediaFiles: true,
			})
			if err == nil || !strings.Contains(err.Error(), "media storage already exists") {
				t.Fatalf("dry_run=%t ImportFromZipBytes() error = %v", dryRun, err)
			}
		}
		if content, err := os.ReadFile(existingPath); err != nil || string(content) != "old" {
			t.Fatalf("existing file = %q, error = %v; want preserved", content, err)
		}
	})
}

func TestImportFromZipFailureCleansEveryUUIDUsingCapturedRoot(t *testing.T) {
	const firstUUID = "550e8400-e29b-41d4-a716-446655440000"
	const secondUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	ts := setupTest(t)
	defer ts.Cleanup()
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{
		{UUID: firstUUID, Filename: "first.pdf", MimeType: model.MimeTypePDF, Size: 5,
			FilePath: "media/originals/" + firstUUID + "/first.pdf",
			Variants: []ExportVariant{{Type: model.VariantThumbnail, Width: 1, Height: 1, Size: 5,
				FilePath: "media/thumbnail/" + firstUUID + "/first.pdf"}}},
		{UUID: secondUUID, Filename: "second.pdf", MimeType: model.MimeTypePDF, Size: 6,
			FilePath: "media/originals/" + secondUUID + "/second.pdf"},
	}}
	entries := []mediaArchiveEntry{
		{name: "media/originals/" + firstUUID + "/first.pdf", body: []byte("first")},
		{name: "media/thumbnail/" + firstUUID + "/first.pdf", body: []byte("thumb")},
		{name: "media/originals/" + secondUUID + "/second.pdf", body: []byte("second")},
	}
	actualRoot := t.TempDir()
	linkParent := t.TempDir()
	configuredRoot := filepath.Join(linkParent, "uploads")
	if err := os.Symlink(actualRoot, configuredRoot); err != nil {
		t.Fatalf("Symlink(actual root): %v", err)
	}
	outsideRoot := t.TempDir()
	outsideSentinel := filepath.Join(outsideRoot, model.OriginalsDir, firstUUID, "must-remain.pdf")
	if err := os.MkdirAll(filepath.Dir(outsideSentinel), 0o750); err != nil {
		t.Fatalf("MkdirAll(outside): %v", err)
	}
	if err := os.WriteFile(outsideSentinel, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside): %v", err)
	}

	ctx, cancel := context.WithCancel(ts.Ctx)
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(configuredRoot)
	importer.beforeMediaImport = func() {
		for _, storageDir := range model.MediaStorageDirs() {
			seed := filepath.Join(actualRoot, storageDir, firstUUID, "seeded-before-failure.bin")
			if err := os.MkdirAll(filepath.Dir(seed), 0o750); err != nil {
				t.Fatalf("MkdirAll(seed %s): %v", storageDir, err)
			}
			if err := os.WriteFile(seed, []byte("seed"), 0o600); err != nil {
				t.Fatalf("WriteFile(seed %s): %v", storageDir, err)
			}
		}
		if err := os.Remove(configuredRoot); err != nil {
			t.Fatalf("remove configured symlink: %v", err)
		}
		if err := os.Symlink(outsideRoot, configuredRoot); err != nil {
			t.Fatalf("retarget configured symlink: %v", err)
		}
		cancel()
	}
	_, err := importer.ImportFromZipBytes(ctx, mediaArchiveBytes(t, data, entries), ImportOptions{
		ConflictStrategy: ConflictSkip,
		ImportMedia:      true,
		ImportMediaFiles: true,
	})
	if err == nil {
		t.Fatal("ImportFromZipBytes() error = nil after cancellation")
	}
	for _, storageDir := range model.MediaStorageDirs() {
		seed := filepath.Join(actualRoot, storageDir, firstUUID, "seeded-before-failure.bin")
		if content, err := os.ReadFile(seed); err != nil || string(content) != "seed" {
			t.Errorf("same-UUID sentinel %s = %q, error = %v; want preserved", storageDir, content, err)
		}
		if _, err := os.Stat(filepath.Join(actualRoot, storageDir, firstUUID, "first.pdf")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("cleanup left import-owned %s/%s/first.pdf: %v", storageDir, firstUUID, err)
		}
		if _, statErr := os.Stat(filepath.Join(actualRoot, storageDir, secondUUID)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("cleanup left import-owned %s/%s: %v", storageDir, secondUUID, statErr)
		}
	}
	if content, err := os.ReadFile(outsideSentinel); err != nil || string(content) != "outside" {
		t.Fatalf("cleanup touched retargeted root: content=%q error=%v", content, err)
	}
}

func TestImportFromZipPartialMediaFailureKeepsOwnedFilesAndCleansUnowned(t *testing.T) {
	const ownedUUID = "550e8400-e29b-41d4-a716-446655440000"
	const failedUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	ts := setupTest(t)
	defer ts.Cleanup()
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{
		{UUID: ownedUUID, Filename: "owned.pdf", MimeType: model.MimeTypePDF, Size: 5,
			FilePath: "media/originals/" + ownedUUID + "/owned.pdf"},
		{UUID: failedUUID, Filename: "failed.pdf", MimeType: model.MimeTypePDF, Size: 6,
			FilePath: "media/originals/" + failedUUID + "/failed.pdf"},
	}}
	entries := []mediaArchiveEntry{
		{name: "media/originals/" + ownedUUID + "/owned.pdf", body: []byte("owned")},
		{name: "media/originals/" + failedUUID + "/failed.pdf", body: []byte("failed")},
	}
	uploadRoot := t.TempDir()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	importer.beforeMediaImport = func() {
		statement := `CREATE TRIGGER fail_selected_transfer_media
			BEFORE INSERT ON media
			WHEN NEW.uuid = '` + failedUUID + `'
			BEGIN
				SELECT RAISE(FAIL, 'forced media insert failure');
			END`
		if _, err := ts.DB.ExecContext(ts.Ctx, statement); err != nil {
			t.Fatalf("create failure trigger: %v", err)
		}
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, entries), ImportOptions{
		ConflictStrategy: ConflictSkip,
		ImportMedia:      true,
		ImportMediaFiles: true,
	})
	if err == nil || result == nil || result.Success {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v), want explicit partial failure", result, err)
	}
	if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, ownedUUID); err != nil {
		t.Fatalf("owned media row missing: %v", err)
	}
	if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, failedUUID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed media row lookup = %v, want sql.ErrNoRows", err)
	}
	if content, err := os.ReadFile(filepath.Join(uploadRoot, model.OriginalsDir, ownedUUID, "owned.pdf")); err != nil || string(content) != "owned" {
		t.Fatalf("owned file = %q, error = %v; want preserved", content, err)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		if _, statErr := os.Stat(filepath.Join(uploadRoot, storageDir, failedUUID)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("unowned media cleanup left %s/%s: %v", storageDir, failedUUID, statErr)
		}
	}
}

func TestImportFromZipGeneratesMissingVariantsAndPersistsActualMetadata(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	for _, declaredThumbnail := range []bool{false, true} {
		name := "original only"
		if declaredThumbnail {
			name = "declared thumbnail survives"
		}
		t.Run(name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			original := transferTestPNG(t, 800, 600)
			width, height := int64(800), int64(600)
			data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
				Size: int64(len(original)), Width: &width, Height: &height,
				FilePath: "media/originals/" + mediaUUID + "/image.png",
			}}}
			entries := []mediaArchiveEntry{{
				name: data.Media[0].FilePath, body: original,
			}}
			var declaredBytes []byte
			if declaredThumbnail {
				declaredBytes = transferTestPNG(t, 150, 150)
				variantPath := "media/" + model.VariantThumbnail + "/" + mediaUUID + "/image.png"
				data.Media[0].Variants = []ExportVariant{{
					Type: model.VariantThumbnail, Width: 150, Height: 150,
					Size: int64(len(declaredBytes)), FilePath: variantPath,
				}}
				entries = append(entries, mediaArchiveEntry{name: variantPath, body: declaredBytes})
			}

			uploadRoot := t.TempDir()
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, entries), ImportOptions{
				ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true,
			})
			if err != nil || result == nil || !result.Success {
				t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
			}
			medium, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID)
			if err != nil {
				t.Fatal(err)
			}
			variants, err := ts.Queries.GetMediaVariants(ts.Ctx, medium.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(variants) != len(model.ImageVariants) {
				t.Fatalf("variant count = %d, want %d", len(variants), len(model.ImageVariants))
			}
			for _, variant := range variants {
				variantPath := filepath.Join(uploadRoot, variant.Type, mediaUUID, "image.png")
				info, err := os.Stat(variantPath)
				if err != nil {
					t.Fatalf("stat %s variant: %v", variant.Type, err)
				}
				file, err := os.Open(variantPath)
				if err != nil {
					t.Fatal(err)
				}
				config, _, decodeErr := image.DecodeConfig(file)
				closeErr := file.Close()
				if decodeErr != nil || closeErr != nil {
					t.Fatalf("decode %s variant: %v", variant.Type, errors.Join(decodeErr, closeErr))
				}
				if variant.Width != int64(config.Width) || variant.Height != int64(config.Height) || variant.Size != info.Size() {
					t.Errorf("%s metadata = %dx%d/%d, decoded/file = %dx%d/%d", variant.Type,
						variant.Width, variant.Height, variant.Size, config.Width, config.Height, info.Size())
				}
			}
			if declaredThumbnail {
				content, err := os.ReadFile(filepath.Join(uploadRoot, model.VariantThumbnail, mediaUUID, "image.png"))
				if err != nil || !bytes.Equal(content, declaredBytes) {
					t.Fatalf("declared thumbnail was overwritten: error=%v", err)
				}
			}
		})
	}
}

func TestImportFromZipVariantTargetCollisionPreservesUnownedFile(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	original := transferTestPNG(t, 800, 600)
	width, height := int64(800), int64(600)
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
		Size: int64(len(original)), Width: &width, Height: &height,
		FilePath: "media/originals/" + mediaUUID + "/image.png",
	}}}
	variantTypes := make([]string, 0, len(model.ImageVariants))
	for variantType := range model.ImageVariants {
		variantTypes = append(variantTypes, variantType)
	}
	sort.Strings(variantTypes)
	collisionType := variantTypes[len(variantTypes)-1]
	uploadRoot := t.TempDir()
	collisionPath := filepath.Join(uploadRoot, collisionType, mediaUUID, "image.png")
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	importer.beforeMediaImport = func() {
		if err := os.MkdirAll(filepath.Dir(collisionPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(collisionPath, []byte("unowned collision"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: original,
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true})
	if err == nil || !strings.Contains(err.Error(), collisionType) {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v), want %q collision", result, err, collisionType)
	}
	if content, err := os.ReadFile(collisionPath); err != nil || string(content) != "unowned collision" {
		t.Fatalf("collision target = %q, error = %v; want preserved", content, err)
	}
	if _, err := os.Stat(filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "image.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("import-owned original remains: %v", err)
	}
	for _, variantType := range variantTypes {
		if variantType == collisionType {
			continue
		}
		if _, err := os.Stat(filepath.Join(uploadRoot, variantType, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("import-owned %s variant directory remains: %v", variantType, err)
		}
	}
	if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("media row lookup = %v, want sql.ErrNoRows", err)
	}
}

func TestImportFromZipSameTargetReplacementSurvivesCompensation(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	original := transferTestPNG(t, 800, 600)
	width, height := int64(800), int64(600)
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
		Size: int64(len(original)), Width: &width, Height: &height,
		FilePath: "media/originals/" + mediaUUID + "/image.png",
	}}}
	uploadRoot := t.TempDir()
	originalPath := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "image.png")
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	importer.beforeMediaImport = func() {
		if err := os.Remove(originalPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(originalPath, original, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: original,
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true})
	if err == nil || !strings.Contains(err.Error(), "was replaced") {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v), want replacement rejection", result, err)
	}
	if content, err := os.ReadFile(originalPath); err != nil || !bytes.Equal(content, original) {
		t.Fatalf("same-target replacement changed: size=%d error=%v", len(content), err)
	}
	for variantType := range model.ImageVariants {
		if _, err := os.Stat(filepath.Join(uploadRoot, variantType, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("import-owned %s variant directory remains: %v", variantType, err)
		}
	}
	if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("media row lookup = %v, want sql.ErrNoRows", err)
	}
}

// TestImportFromZipSameInodeRewriteIsRejected covers the replacement an inode
// comparison cannot see. The extracted original is edited in place rather than
// deleted and recreated, so its inode number is unchanged on every platform
// and only the recorded size and modification time distinguish it. Without
// that comparison the import adopts another actor's bytes as its own.
func TestImportFromZipSameInodeRewriteIsRejected(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	original := transferTestPNG(t, 800, 600)
	width, height := int64(800), int64(600)
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
		Size: int64(len(original)), Width: &width, Height: &height,
		FilePath: "media/originals/" + mediaUUID + "/image.png",
	}}}
	uploadRoot := t.TempDir()
	originalPath := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "image.png")
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	// Trailing bytes keep the file a decodable 800x600 PNG, so the image
	// validation ahead of the identity check still passes and this test fails
	// only when the replacement itself goes unnoticed.
	appended := append(append([]byte(nil), original...), []byte("appended by another actor")...)
	importer.beforeMediaImport = func() {
		file, err := os.OpenFile(originalPath, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write([]byte("appended by another actor"))
		if err := errors.Join(writeErr, file.Close()); err != nil {
			t.Fatal(err)
		}
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: original,
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true})
	if err == nil || !strings.Contains(err.Error(), "was replaced") {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v), want replacement rejection", result, err)
	}
	if content, err := os.ReadFile(originalPath); err != nil || !bytes.Equal(content, appended) {
		t.Fatalf("rewritten file = %d bytes, error = %v; want the replacement preserved", len(content), err)
	}
	if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("media row lookup = %v, want sql.ErrNoRows", err)
	}
}

func TestImportFromZipGeneratesThroughCapturedRootBeforeReplacementRejection(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	original := transferTestPNG(t, 800, 600)
	width, height := int64(800), int64(600)
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
		Size: int64(len(original)), Width: &width, Height: &height,
		FilePath: "media/originals/" + mediaUUID + "/image.png",
	}}}
	parent := t.TempDir()
	uploadRoot := filepath.Join(parent, "uploads")
	renamedRoot := filepath.Join(parent, "captured-uploads")
	variantTypes := make([]string, 0, len(model.ImageVariants))
	for variantType := range model.ImageVariants {
		variantTypes = append(variantTypes, variantType)
	}
	sort.Strings(variantTypes)
	replacementPath := filepath.Join(uploadRoot, variantTypes[len(variantTypes)-1], mediaUUID, "image.png")
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	importer.beforeMediaImport = func() {
		if err := os.Rename(uploadRoot, renamedRoot); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(replacementPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacementPath, []byte("replacement root"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: original,
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true})
	if err == nil || result == nil || !strings.Contains(err.Error(), "pre-commit import validation failed") ||
		!strings.Contains(err.Error(), "was replaced") {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v), want pre-commit root replacement rejection", result, err)
	}
	if content, err := os.ReadFile(replacementPath); err != nil || string(content) != "replacement root" {
		t.Fatalf("replacement-root target = %q, error = %v; want preserved", content, err)
	}
	for _, storageDir := range model.MediaStorageDirs() {
		if _, err := os.Stat(filepath.Join(renamedRoot, storageDir, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("captured root retains import-owned %s directory: %v", storageDir, err)
		}
	}
	if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("media row lookup = %v, want sql.ErrNoRows", err)
	}
}

func TestWebPMediaImportGenerateExportRoundTrip(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	original := transferTestWebP(t, 800, 600)
	width, height := int64(800), int64(600)
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "image.webp", MimeType: model.MimeTypeWebP,
		Size: int64(len(original)), Width: &width, Height: &height,
		FilePath: "media/originals/" + mediaUUID + "/image.webp",
	}}}
	uploadRoot := t.TempDir()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: original,
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
	}

	exporter := NewExporter(ts.Queries, slog.Default())
	exporter.SetUploadDir(uploadRoot)
	var archive bytes.Buffer
	if err := exporter.ExportWithMedia(ts.Ctx, ExportOptions{
		IncludeMedia: true, IncludeMediaFiles: true,
	}, &archive); err != nil {
		t.Fatalf("ExportWithMedia() error = %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	mediaEntryCount := 0
	for _, entry := range reader.File {
		if !strings.HasPrefix(entry.Name, "media/") {
			continue
		}
		mediaEntryCount++
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, _, mimeType, validateErr := imaging.ValidateImage(file)
		closeErr := file.Close()
		if err := errors.Join(validateErr, closeErr); err != nil {
			t.Errorf("validate %q: %v", entry.Name, err)
			continue
		}
		if mimeType != model.MimeTypeWebP {
			t.Errorf("%q MIME type = %q, want %q", entry.Name, mimeType, model.MimeTypeWebP)
		}
	}
	if mediaEntryCount != 1+len(model.ImageVariants) {
		t.Fatalf("media entry count = %d, want %d", mediaEntryCount, 1+len(model.ImageVariants))
	}
}

func TestImportFromZipOverwriteReplacesMediaAndVariantMetadata(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	uploader, err := ts.Queries.CreateUser(ts.Ctx, store.CreateUserParams{
		Email: "archive-uploader@example.com", PasswordHash: "hash", Role: "editor", Name: "Archive Uploader",
		CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
		Uuid: mediaUUID, Filename: "old.pdf", MimeType: model.MimeTypePDF, Size: 3,
		Width: sql.NullInt64{}, Height: sql.NullInt64{}, Alt: sql.NullString{String: "old alt", Valid: true},
		Caption: sql.NullString{String: "old caption", Valid: true}, UploadedBy: ts.User.ID,
		LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.CreateMediaVariant(ts.Ctx, store.CreateMediaVariantParams{
		MediaID: existing.ID, Type: model.VariantThumbnail, Width: 1, Height: 1, Size: 1, CreatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	original := transferTestPNG(t, 800, 600)
	width, height := int64(800), int64(600)
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "new.png", MimeType: model.MimeTypePNG, Size: int64(len(original)),
		Width: &width, Height: &height, Alt: "new alt", Caption: "new caption", FolderPath: "Imported/Nested",
		UploadedBy: uploader.Email, LanguageCode: language.Code,
		FilePath: "media/originals/" + mediaUUID + "/new.png",
	}}}
	uploadRoot := t.TempDir()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: original,
	}}), ImportOptions{ConflictStrategy: ConflictOverwrite, ImportMedia: true, ImportMediaFiles: true})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
	}
	updated, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != existing.ID || updated.Filename != "new.png" || updated.MimeType != model.MimeTypePNG ||
		updated.Size != int64(len(original)) || !updated.Width.Valid || updated.Width.Int64 != width ||
		!updated.Height.Valid || updated.Height.Int64 != height || updated.Alt.String != "new alt" ||
		updated.Caption.String != "new caption" || updated.UploadedBy != uploader.ID ||
		updated.LanguageCode != language.Code || !updated.FolderID.Valid {
		t.Fatalf("updated media = %+v", updated)
	}
	folder, err := ts.Queries.GetMediaFolderByID(ts.Ctx, updated.FolderID.Int64)
	if err != nil || folder.Name != "Nested" || !folder.ParentID.Valid {
		t.Fatalf("updated folder = %+v, error = %v", folder, err)
	}
	variants, err := ts.Queries.GetMediaVariants(ts.Ctx, existing.ID)
	if err != nil || len(variants) != len(model.ImageVariants) {
		t.Fatalf("variants = %+v, error = %v", variants, err)
	}
	for _, variant := range variants {
		info, err := os.Stat(filepath.Join(uploadRoot, variant.Type, mediaUUID, "new.png"))
		if err != nil || info.Size() != variant.Size || variant.Width <= 1 || variant.Height <= 1 {
			t.Errorf("%s variant = %+v, stat=%v, error=%v", variant.Type, variant, info, err)
		}
	}
}

func TestImportFromZipDryRunAndRealRejectCorruptImageWithoutCollateralWrites(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	corrupt := []byte("not a PNG")
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG, Size: int64(len(corrupt)),
		FilePath: "media/originals/" + mediaUUID + "/image.png",
	}}}
	entries := []mediaArchiveEntry{{name: data.Media[0].FilePath, body: corrupt}}
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry run", false: "real"}[dryRun], func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			uploadRoot := t.TempDir()
			sentinel := filepath.Join(uploadRoot, "operator-owned.txt")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			_, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, entries), ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true,
			})
			if err == nil || !strings.Contains(err.Error(), "decode original") {
				t.Fatalf("ImportFromZipBytes() error = %v", err)
			}
			if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
				t.Fatalf("sentinel = %q, error = %v", content, err)
			}
			for _, storageDir := range model.MediaStorageDirs() {
				if _, err := os.Stat(filepath.Join(uploadRoot, storageDir, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("dry_run=%t left %s storage: %v", dryRun, storageDir, err)
				}
			}
			count, err := ts.Queries.CountMedia(ts.Ctx)
			if err != nil || count != 0 {
				t.Fatalf("media count = %d, error = %v", count, err)
			}
		})
	}
}

func TestImportFromZipRejectsCorruptImagesWhenVariantGenerationIsSkipped(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	corrupt := []byte("not a PNG")

	for _, mode := range []string{"new with all variants", "conflict skip", "files only"} {
		for _, dryRun := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/dry_run=%t", mode, dryRun), func(t *testing.T) {
				ts := setupTest(t)
				defer ts.Cleanup()
				data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
					UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
					Size: int64(len(corrupt)), FilePath: "media/originals/" + mediaUUID + "/image.png",
				}}}
				entries := []mediaArchiveEntry{{name: data.Media[0].FilePath, body: corrupt}}
				importMedia := true
				wantRows := int64(0)
				if mode == "new with all variants" {
					variantTypes := make([]string, 0, len(model.ImageVariants))
					for variantType := range model.ImageVariants {
						variantTypes = append(variantTypes, variantType)
					}
					sort.Strings(variantTypes)
					for _, variantType := range variantTypes {
						body := []byte("declared-" + variantType)
						archivePath := "media/" + variantType + "/" + mediaUUID + "/image.png"
						data.Media[0].Variants = append(data.Media[0].Variants, ExportVariant{
							Type: variantType, Width: 1, Height: 1, Size: int64(len(body)), FilePath: archivePath,
						})
						entries = append(entries, mediaArchiveEntry{name: archivePath, body: body})
					}
				} else {
					language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
						Uuid: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
						Size: int64(len(corrupt)), UploadedBy: ts.User.ID, LanguageCode: language.Code,
						CreatedAt: ts.Now, UpdatedAt: ts.Now,
					}); err != nil {
						t.Fatal(err)
					}
					wantRows = 1
					importMedia = mode != "files only"
				}

				uploadRoot := t.TempDir()
				importer := NewImporter(ts.Queries, ts.DB, slog.Default())
				importer.SetUploadDir(uploadRoot)
				_, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, entries), ImportOptions{
					DryRun: dryRun, ConflictStrategy: ConflictSkip,
					ImportMedia: importMedia, ImportMediaFiles: true,
				})
				if err == nil || !strings.Contains(err.Error(), "decode") {
					t.Fatalf("ImportFromZipBytes() error = %v", err)
				}
				for _, storageDir := range model.MediaStorageDirs() {
					if _, err := os.Stat(filepath.Join(uploadRoot, storageDir, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("left %s storage: %v", storageDir, err)
					}
				}
				count, err := ts.Queries.CountMedia(ts.Ctx)
				if err != nil || count != wantRows {
					t.Fatalf("media count = %d, want %d, error = %v", count, wantRows, err)
				}
			})
		}
	}
}

func TestImportFromZipRejectsCorruptDeclaredImageVariant(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry_run=%t", dryRun), func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			original := transferTestPNG(t, 2, 2)
			variant := []byte("not an image")
			width, height := int64(2), int64(2)
			data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: mediaUUID, Filename: "photo.png", MimeType: model.MimeTypePNG,
				Size: int64(len(original)), Width: &width, Height: &height,
				FilePath: "media/originals/" + mediaUUID + "/photo.png",
				Variants: []ExportVariant{{
					Type: model.VariantThumbnail, Width: 1, Height: 1, Size: int64(len(variant)),
					FilePath: "media/thumbnail/" + mediaUUID + "/photo.png",
				}},
			}}}
			uploadRoot := filepath.Join(t.TempDir(), "uploads")
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			_, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{
				{name: data.Media[0].FilePath, body: original},
				{name: data.Media[0].Variants[0].FilePath, body: variant},
			}), ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true,
			})
			if err == nil || !strings.Contains(err.Error(), "decode thumbnail") {
				t.Fatalf("ImportFromZipBytes() error = %v", err)
			}
			if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("media lookup = %v, want sql.ErrNoRows", err)
			}
			for _, storageDir := range model.MediaStorageDirs() {
				if _, err := os.Stat(filepath.Join(uploadRoot, storageDir, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed import left %s storage: %v", storageDir, err)
				}
			}
		})
	}
}

func TestImportFromZipRejectsImageMIMEBytesMismatch(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry_run=%t", dryRun), func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			pngBytes := transferTestPNG(t, 2, 2)
			width, height := int64(2), int64(2)
			data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
				UUID: mediaUUID, Filename: "photo.jpg", MimeType: model.MimeTypeJPEG,
				Size: int64(len(pngBytes)), Width: &width, Height: &height,
				FilePath: "media/originals/" + mediaUUID + "/photo.jpg",
			}}}
			uploadRoot := filepath.Join(t.TempDir(), "uploads")
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			_, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
				name: data.Media[0].FilePath, body: pngBytes,
			}}), ImportOptions{
				DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true,
			})
			if err == nil || !strings.Contains(err.Error(), "MIME type") {
				t.Fatalf("ImportFromZipBytes() error = %v", err)
			}
			if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("media lookup = %v, want sql.ErrNoRows", err)
			}
			for _, storageDir := range model.MediaStorageDirs() {
				if _, err := os.Stat(filepath.Join(uploadRoot, storageDir, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed import left %s storage: %v", storageDir, err)
				}
			}
		})
	}
}

func TestImportFromZipSkipAndFileOnlyDoNotGenerateVariants(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	original := transferTestPNG(t, 300, 200)
	width, height := int64(300), int64(200)
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
		Size: int64(len(original)), Width: &width, Height: &height,
		FilePath: "media/originals/" + mediaUUID + "/image.png",
	}}}
	entries := []mediaArchiveEntry{{name: data.Media[0].FilePath, body: original}}
	for _, importMedia := range []bool{true, false} {
		name := "conflict skip"
		if !importMedia {
			name = "file only"
		}
		t.Run(name, func(t *testing.T) {
			ts := setupTest(t)
			defer ts.Cleanup()
			language, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			medium, err := ts.Queries.CreateMedia(ts.Ctx, store.CreateMediaParams{
				Uuid: mediaUUID, Filename: "image.png", MimeType: model.MimeTypePNG,
				Size: int64(len(original)), Width: sql.NullInt64{Int64: width, Valid: true},
				Height: sql.NullInt64{Int64: height, Valid: true}, UploadedBy: ts.User.ID,
				LanguageCode: language.Code, CreatedAt: ts.Now, UpdatedAt: ts.Now,
			})
			if err != nil {
				t.Fatal(err)
			}
			uploadRoot := t.TempDir()
			importer := NewImporter(ts.Queries, ts.DB, slog.Default())
			importer.SetUploadDir(uploadRoot)
			result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, entries), ImportOptions{
				ConflictStrategy: ConflictSkip, ImportMedia: importMedia, ImportMediaFiles: true,
			})
			if err != nil || result == nil || !result.Success {
				t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
			}
			variants, err := ts.Queries.GetMediaVariants(ts.Ctx, medium.ID)
			if err != nil || len(variants) != 0 {
				t.Fatalf("variants = %+v, error = %v", variants, err)
			}
			for variantType := range model.ImageVariants {
				if _, err := os.Stat(filepath.Join(uploadRoot, variantType, mediaUUID)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("unexpected generated %s storage: %v", variantType, err)
				}
			}
		})
	}
}

func TestImportFromZipOwnerVariantQueryFailurePreservesFiles(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		FilePath: "media/originals/" + mediaUUID + "/report.pdf",
	}}}
	uploadRoot := t.TempDir()
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	importer.beforeMediaOwnerCheck = func() {
		if _, err := ts.DB.ExecContext(ts.Ctx, "ALTER TABLE media_variants RENAME TO media_variants_unavailable"); err != nil {
			t.Fatalf("rename media_variants: %v", err)
		}
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: []byte("report"),
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true})
	if err == nil || result == nil || !strings.Contains(err.Error(), "read destination variants") {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
	}
	if _, err := ts.Queries.GetMediaByUUID(ts.Ctx, mediaUUID); err != nil {
		t.Fatalf("committed media row missing: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "report.pdf"))
	if err != nil || string(content) != "report" {
		t.Fatalf("uncertain-owner file = %q, error = %v; want preserved", content, err)
	}
}

func TestImportFromZipReportsSamePathRootReplacementWithoutTouchingReplacement(t *testing.T) {
	const mediaUUID = "550e8400-e29b-41d4-a716-446655440000"
	ts := setupTest(t)
	defer ts.Cleanup()
	data := ExportData{Version: ExportVersion, Media: []ExportMedia{{
		UUID: mediaUUID, Filename: "report.pdf", MimeType: model.MimeTypePDF, Size: 6,
		FilePath: "media/originals/" + mediaUUID + "/report.pdf",
	}}}
	parent := t.TempDir()
	uploadRoot := filepath.Join(parent, "uploads")
	renamedRoot := filepath.Join(parent, "uploads-renamed")
	replacementSentinel := filepath.Join(uploadRoot, model.OriginalsDir, mediaUUID, "replacement.pdf")
	importer := NewImporter(ts.Queries, ts.DB, slog.Default())
	importer.SetUploadDir(uploadRoot)
	importer.beforeMediaOwnerCheck = func() {
		if err := os.Rename(uploadRoot, renamedRoot); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(replacementSentinel), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacementSentinel, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := importer.ImportFromZipBytes(ts.Ctx, mediaArchiveBytes(t, data, []mediaArchiveEntry{{
		name: data.Media[0].FilePath, body: []byte("report"),
	}}), ImportOptions{ConflictStrategy: ConflictSkip, ImportMedia: true, ImportMediaFiles: true})
	if err == nil || result == nil || !strings.Contains(err.Error(), "was replaced") {
		t.Fatalf("ImportFromZipBytes() = (%+v, %v)", result, err)
	}
	if content, err := os.ReadFile(filepath.Join(renamedRoot, model.OriginalsDir, mediaUUID, "report.pdf")); err != nil || string(content) != "report" {
		t.Fatalf("captured-root original = %q, error = %v", content, err)
	}
	if content, err := os.ReadFile(replacementSentinel); err != nil || string(content) != "replacement" {
		t.Fatalf("replacement sentinel = %q, error = %v", content, err)
	}
}
