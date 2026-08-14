// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/transfer"
)

func TestImportRequestLimitAllowsMultipartOverhead(t *testing.T) {
	const fileLimit int64 = 4 << 10

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("import_file", "archive.zip")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := io.CopyN(part, strings.NewReader(strings.Repeat("x", int(fileLimit))), fileLimit); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.WriteField("conflict_strategy", "skip"); err != nil {
		t.Fatalf("write multipart field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	if int64(body.Len()) <= fileLimit {
		t.Fatalf("multipart body size = %d, want framing beyond file limit %d", body.Len(), fileLimit)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/import/validate", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(recorder, req.Body, maxImportRequestBytes(fileLimit))
	if err := req.ParseMultipartForm(fileLimit); err != nil {
		t.Fatalf("parse multipart request at file limit: %v", err)
	}
	t.Cleanup(func() { _ = req.MultipartForm.RemoveAll() })

	file, header, err := req.FormFile("import_file")
	if err != nil {
		t.Fatalf("read multipart file: %v", err)
	}
	defer func() { _ = file.Close() }()
	if header.Size != fileLimit {
		t.Fatalf("multipart file size = %d, want %d", header.Size, fileLimit)
	}
}

func TestImportExportHandlerUploadDir(t *testing.T) {
	db, sm := testHandlerSetup(t)
	h := NewImportExportHandler(db, nil, sm)

	if got := h.configuredUploadDir(); got != defaultImportExportUploadDir {
		t.Fatalf("default uploadDir = %q, want %q", got, defaultImportExportUploadDir)
	}

	customUploadDir := t.TempDir()
	h.SetUploadDir(customUploadDir)
	if got := h.configuredUploadDir(); got != customUploadDir {
		t.Fatalf("configured uploadDir = %q, want %q", got, customUploadDir)
	}

	h.SetUploadDir("")
	if got := h.configuredUploadDir(); got != defaultImportExportUploadDir {
		t.Fatalf("reset uploadDir = %q, want %q", got, defaultImportExportUploadDir)
	}
}

func TestImportExportHandlerUsesConfiguredUploadDirForMediaExport(t *testing.T) {
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	h := NewImportExportHandler(db, nil, sm)

	uploadDir := t.TempDir()
	h.SetUploadDir(uploadDir)

	const (
		mediaUUID = "123e4567-e89b-42d3-a456-426614174000"
		filename  = "custom-root.txt"
		contents  = "configured upload root"
	)
	originalDir := filepath.Join(uploadDir, "originals", mediaUUID)
	if err := os.MkdirAll(originalDir, 0o750); err != nil {
		t.Fatalf("create original directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(originalDir, filename), []byte(contents), 0o640); err != nil {
		t.Fatalf("write original media: %v", err)
	}

	now := time.Now().UTC()
	if _, err := h.queries.CreateMedia(context.Background(), store.CreateMediaParams{
		Uuid:         mediaUUID,
		Filename:     filename,
		MimeType:     "text/plain",
		Size:         int64(len(contents)),
		Width:        sql.NullInt64{},
		Height:       sql.NullInt64{},
		Alt:          sql.NullString{},
		Caption:      sql.NullString{},
		FolderID:     sql.NullInt64{},
		UploadedBy:   user.ID,
		LanguageCode: "en",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("create media row: %v", err)
	}

	var archive bytes.Buffer
	if err := h.newExporter().ExportWithMedia(context.Background(), transfer.ExportOptions{
		IncludeMedia:      true,
		IncludeMediaFiles: true,
	}, &archive); err != nil {
		t.Fatalf("export media from configured root: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("open exported ZIP: %v", err)
	}
	wantPath := filepath.ToSlash(filepath.Join("media", "originals", mediaUUID, filename))
	for _, file := range reader.File {
		if file.Name != wantPath {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open exported media: %v", err)
		}
		got, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatalf("read exported media: %v", err)
		}
		if string(got) != contents {
			t.Fatalf("exported contents = %q, want %q", got, contents)
		}
		return
	}
	t.Fatalf("exported ZIP does not contain %q", wantPath)
}

func TestImportExportHandlerRejectsMediaFilesForJSONSession(t *testing.T) {
	db, sm := testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{
		TemplatesFS: os.DirFS("../../web/templates"), SessionManager: sm, DB: db, IsDev: true,
	})
	if err != nil {
		t.Fatalf("create renderer: %v", err)
	}
	h := NewImportExportHandler(db, renderer, sm)
	data := transfer.ExportData{Version: transfer.ExportVersion, Pages: []transfer.ExportPage{{
		ID: 1, Title: "JSON media files", Slug: "json-media-files", Status: "draft", AuthorEmail: user.Email,
	}}}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"conflict_strategy":  {string(transfer.ConflictSkip)},
		"import_pages":       {"on"},
		"import_media_files": {"on"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithSession(sm, req)
	req = addUserToContext(req, &user)
	sm.Put(req.Context(), "import_is_zip", false)
	sm.Put(req.Context(), "import_data", string(encoded))

	recorder := httptest.NewRecorder()
	h.Import(recorder, req)
	if message := sm.GetString(req.Context(), "flash_error"); !strings.Contains(message, "media file import requires a ZIP archive") {
		t.Fatalf("flash_error = %q", message)
	}
	if sm.GetString(req.Context(), "import_data") == "" {
		t.Fatal("rejected JSON payload was removed from the session")
	}
	var pageCount int
	if err := db.QueryRowContext(req.Context(), `SELECT COUNT(*) FROM pages WHERE slug = ?`, "json-media-files").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if pageCount != 0 {
		t.Fatalf("imported page count = %d, want 0", pageCount)
	}
}

func TestReadImportFileContent(t *testing.T) {
	content, err := readImportFileContent(strings.NewReader("abc"), 3)
	if err != nil {
		t.Fatalf("expected successful read, got %v", err)
	}
	if string(content) != "abc" {
		t.Fatalf("expected content to match, got %q", string(content))
	}
}

func TestReadImportFileContent_TooLarge(t *testing.T) {
	_, err := readImportFileContent(bytes.NewReader([]byte("abcd")), 3)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !strings.Contains(err.Error(), "file is too large") {
		t.Fatalf("expected too-large error, got %v", err)
	}
}

func TestCommittedImportInvalidatesDerivedCaches(t *testing.T) {
	cacheManager := cache.NewManager(nil)
	cacheManager.General.Set("stale-import-state", "old")
	h := &ImportExportHandler{cacheManager: cacheManager}

	h.invalidateCachesAfterImport(context.Background(), &transfer.ImportResult{DryRun: false})

	_, ok := cacheManager.General.Get("stale-import-state")
	if ok {
		t.Fatal("expected committed import to clear all derived caches")
	}
}

func TestDryRunDoesNotInvalidateCaches(t *testing.T) {
	cacheManager := cache.NewManager(nil)
	cacheManager.General.Set("dry-run-state", "preserved")
	h := &ImportExportHandler{cacheManager: cacheManager}

	h.invalidateCachesAfterImport(context.Background(), &transfer.ImportResult{DryRun: true})

	_, ok := cacheManager.General.Get("dry-run-state")
	if !ok {
		t.Fatal("expected dry run to preserve caches")
	}
}

func TestFindSuspiciousImportPages(t *testing.T) {
	data := &transfer.ExportData{
		Pages: []transfer.ExportPage{
			{Slug: "clean", Body: "<p>Hello</p>"},
			{Slug: "xss", Body: "<script>alert(1)</script>"},
			{Slug: "", Body: "<iframe src=\"https://evil.example\"></iframe>"},
		},
	}

	matches := findSuspiciousImportPages(data)
	if len(matches) != 2 {
		t.Fatalf("expected 2 suspicious pages, got %d", len(matches))
	}
	if matches[0].Slug != "xss" {
		t.Fatalf("expected first slug to be xss, got %q", matches[0].Slug)
	}
	if matches[1].Slug != "(empty-slug)" {
		t.Fatalf("expected empty slug placeholder, got %q", matches[1].Slug)
	}
}

func TestApplyImportPageSecurityPolicy_BlocksWhenEnabled(t *testing.T) {
	h := &ImportExportHandler{blockSuspiciousMarkup: true}
	data := &transfer.ExportData{
		Pages: []transfer.ExportPage{
			{Slug: "xss", Body: "<script>alert(1)</script>"},
		},
	}

	err := h.applyImportPageSecurityPolicy(nil, data, "import", true)
	if err == nil {
		t.Fatal("expected policy error for suspicious import page content")
	}
	if !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("expected blocked policy error, got %v", err)
	}
}

func TestApplyImportPageSecurityPolicy_AllowsWhenPagesNotImported(t *testing.T) {
	h := &ImportExportHandler{blockSuspiciousMarkup: true}
	data := &transfer.ExportData{
		Pages: []transfer.ExportPage{
			{Slug: "xss", Body: "<script>alert(1)</script>"},
		},
	}

	if err := h.applyImportPageSecurityPolicy(nil, data, "import", false); err != nil {
		t.Fatalf("expected no error when page import is disabled, got %v", err)
	}
}
