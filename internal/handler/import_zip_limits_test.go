// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/transfer"
)

func metadataArchive(t *testing.T, payload string, duplicate bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	count := 1
	if duplicate {
		count++
	}
	for range count {
		entry, err := w.Create("export.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func entryDenseMetadataArchive(t *testing.T, payload string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entry, err := w.Create("export.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	for i := range 10016 {
		if _, err := w.Create(fmt.Sprintf("entry%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportZipMetadataLimitsAcrossPreviewAndStagedImport(t *testing.T) {
	valid := `{"version":"` + transfer.ExportVersion + `","config":{"r17_zip_fixture":"Archive settings"}}`
	for _, tc := range []struct {
		name, payload string
		duplicate     bool
	}{
		{"oversized", `{"version":"` + transfer.ExportVersion + `","config":{"oversized":"` + strings.Repeat("a", 32<<20) + `"}}`, false},
		{"trailing_document", valid + `{}`, false},
		{"duplicate_metadata", valid, true},
		{"too_many_entries", valid, false},
		{"zip64", valid, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := metadataArchive(t, tc.payload, tc.duplicate)
			if tc.name == "too_many_entries" {
				archive = entryDenseMetadataArchive(t, valid)
			}
			if tc.name == "zip64" {
				// ZIP64 sentinel in the footer must be rejected before zip.NewReader.
				binary.LittleEndian.PutUint16(archive[len(archive)-12:len(archive)-10], 0xffff)
				binary.LittleEndian.PutUint16(archive[len(archive)-14:len(archive)-12], 0xffff)
			}
			db, sm := testHandlerSetup(t)
			user := createTestAdminUser(t, db)
			renderer, err := render.New(render.Config{TemplatesFS: os.DirFS("../../web/templates"), SessionManager: sm, DB: db, IsDev: true})
			if err != nil {
				t.Fatal(err)
			}
			h := NewImportExportHandler(db, renderer, sm)
			if _, err := h.extractExportDataFromZip(archive); err == nil {
				t.Fatal("unsafe metadata accepted by browser reader")
			}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("import_file", "archive.zip")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(archive); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/admin/import/validate", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req = addUserToContext(requestWithSession(sm, req), &user)
			h.ImportValidate(httptest.NewRecorder(), req)
			if _, staged, err := h.loadStagedImport(req.Context()); err != nil || staged {
				t.Fatalf("rejected preview staged its payload: %v, %v", staged, err)
			}
			form := url.Values{"import_config": {"on"}, "conflict_strategy": {"overwrite"}}
			confirm := httptest.NewRequest(http.MethodPost, "/admin/import", strings.NewReader(form.Encode())).WithContext(req.Context())
			confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := h.stageImportPayload(confirm.Context(), archive); err != nil {
				t.Fatal(err)
			}
			sm.Put(confirm.Context(), "import_is_zip", true)
			h.Import(httptest.NewRecorder(), confirm)
			if message := sm.GetString(confirm.Context(), "flash_error"); message != importZipUnreadableMessage {
				t.Fatalf("staged metadata reached later import processing: %q", message)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM config WHERE key IN ('oversized', 'r17_zip_fixture')`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected import wrote settings: %d, %v", count, err)
			}
		})
	}
	t.Run("valid_preview_and_import", func(t *testing.T) {
		db, sm := testHandlerSetup(t)
		user := createTestAdminUser(t, db)
		renderer, err := render.New(render.Config{TemplatesFS: os.DirFS("../../web/templates"), SessionManager: sm, DB: db, IsDev: true})
		if err != nil {
			t.Fatal(err)
		}
		h := NewImportExportHandler(db, renderer, sm)
		archive := metadataArchive(t, valid, false)
		if data, err := h.extractExportDataFromZip(archive); err != nil || data.Config["r17_zip_fixture"] != "Archive settings" {
			t.Fatalf("valid metadata = %+v, %v", data, err)
		}
		form := url.Values{"import_config": {"on"}, "conflict_strategy": {"overwrite"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/import", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = addUserToContext(requestWithSession(sm, req), &user)
		if err := h.stageImportPayload(req.Context(), archive); err != nil {
			t.Fatal(err)
		}
		sm.Put(req.Context(), "import_is_zip", true)
		h.Import(httptest.NewRecorder(), req)
		var value string
		if err := db.QueryRow(`SELECT value FROM config WHERE key = 'r17_zip_fixture'`).Scan(&value); err != nil || value != "Archive settings" {
			t.Fatalf("valid import = %q, %v", value, err)
		}
	})
}
