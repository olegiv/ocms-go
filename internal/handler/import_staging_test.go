// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStagedImportExpiresOnRead fails when a staged payload older than
// importStageMaxAge can still be confirmed: an administrator who stays active
// keeps the session alive, so the file's age, not the session, must enforce
// the advertised limit.
func TestStagedImportExpiresOnRead(t *testing.T) {
	db, sm := testHandlerSetup(t)
	h := NewImportExportHandler(db, nil, sm)
	req := requestWithSession(sm, httptest.NewRequest(http.MethodPost, "/admin/import", nil))
	ctx := req.Context()

	if err := h.stageImportPayload(ctx, []byte(`{"version":"1"}`)); err != nil {
		t.Fatal(err)
	}
	path := sm.GetString(ctx, sessionKeyImportPayloadPath)
	expired := time.Now().Add(-importStageMaxAge - time.Minute)
	if err := os.Chtimes(path, expired, expired); err != nil {
		t.Fatal(err)
	}

	if _, staged, err := h.loadStagedImport(ctx); err != nil || staged {
		t.Fatalf("expired payload loaded: staged=%v err=%v", staged, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired staged file still exists: %v", err)
	}
	if sm.Exists(ctx, sessionKeyImportPayloadPath) {
		t.Fatal("session still references the expired payload")
	}
}

// TestImportPayloadStagedOnDiskNotInSession fails when an uploaded import
// goes back into the session row, or when the staged file outlives the
// confirmation step.
func TestImportPayloadStagedOnDiskNotInSession(t *testing.T) {
	db, sm := testHandlerSetup(t)
	h := NewImportExportHandler(db, nil, sm)
	req := requestWithSession(sm, httptest.NewRequest(http.MethodPost, "/admin/import", nil))
	ctx := req.Context()

	payload := []byte(`{"version":"1"}`)
	if err := h.stageImportPayload(ctx, payload); err != nil {
		t.Fatal(err)
	}
	path := sm.GetString(ctx, sessionKeyImportPayloadPath)
	if path == "" || !isStagedImportPath(path) {
		t.Fatalf("session holds %q, want a staged file path", path)
	}
	for _, key := range []string{"import_zip_data", "import_data"} {
		if sm.Exists(ctx, key) {
			t.Fatalf("payload was stored in the session under %q", key)
		}
	}
	got, staged, err := h.loadStagedImport(ctx)
	if err != nil || !staged || string(got) != string(payload) {
		t.Fatalf("loadStagedImport = %q, %v, %v", got, staged, err)
	}

	h.discardStagedImport(ctx)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("staged file still exists after discard: %v", err)
	}
	if _, staged, _ := h.loadStagedImport(ctx); staged {
		t.Fatal("discarded payload still loads")
	}
}

// TestStagedImportPathGuard fails when the handler would read or delete a
// path outside its own staging files.
func TestStagedImportPathGuard(t *testing.T) {
	for path, want := range map[string]bool{
		filepath.Join(os.TempDir(), importStagePrefix+"abc.bin"): true,
		filepath.Join(os.TempDir(), "other.bin"):                 false,
		filepath.Join(t.TempDir(), importStagePrefix+"abc.bin"):  false,
		"/etc/passwd": false,
		"":            false,
	} {
		if got := isStagedImportPath(path); got != want {
			t.Errorf("isStagedImportPath(%q) = %v, want %v", path, got, want)
		}
	}
}
