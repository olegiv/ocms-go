// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/transfer"
)

// TestHealthPrefixGuessNeverReachesArgon2 fails when /health verifies bearer
// tokens by prefix again, outside the shared lookup-first verifier.
func TestHealthPrefixGuessNeverReachesArgon2(t *testing.T) {
	handler := newTestHealthHandler(t)
	rawKey := createTestAPIKey(t, handler)

	real := httptest.NewRequest(http.MethodGet, "/health", nil)
	addAPIKeyAuth(real, rawKey)
	if !handler.isAuthenticated(real) {
		t.Fatal("real key must authenticate (and backfill its lookup)")
	}

	guess := rawKey[:4] + "A" + rawKey[5:]
	if guess == rawKey {
		guess = rawKey[:4] + "B" + rawKey[5:]
	}
	before := middleware.APIKeyHashCheckCount()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	addAPIKeyAuth(req, guess)
	if handler.isAuthenticated(req) {
		t.Fatal("prefix guess authenticated")
	}
	if got := middleware.APIKeyHashCheckCount(); got != before {
		t.Fatalf("prefix guess ran %d Argon2 verification(s) through /health; want 0", got-before)
	}
}

// TestLoginRejectsControlCharactersInEmail fails when a crafted email with
// control characters reaches the lockout keys or the event log.
func TestLoginRejectsControlCharactersInEmail(t *testing.T) {
	db, sm := testHandlerSetup(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	h := NewAuthHandler(db, renderer, sm, nil, nil, testMFAService(t, db))
	form := url.Values{"email": {"owner@example.com\x1f203.0.113.9"}, "password": {"whatever"}}
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = requestWithSession(sm, r)
	w := httptest.NewRecorder()
	h.Login(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if got := sm.GetString(r.Context(), "flash"); got != i18n.T("en", "auth.email_password_required") {
		t.Fatalf("flash = %q, want the invalid-input message", got)
	}
}

// runImportFlow uploads payload through ImportValidate, commits the session
// and confirms the import through Import in a second request, the way the
// admin UI does. It returns the staged file path recorded after validation
// and the session context used for the confirmation.
func runImportFlow(t *testing.T, filename string, payload []byte, dryRun bool) (h *ImportExportHandler, db *sql.DB, stagedPath string, confirmCtx context.Context) {
	t.Helper()
	var sm *scs.SessionManager
	db, sm = testHandlerSetup(t)
	user := createTestAdminUser(t, db)
	renderer, err := render.New(render.Config{TemplatesFS: os.DirFS("../../web/templates"), SessionManager: sm, DB: db, IsDev: true})
	if err != nil {
		t.Fatal(err)
	}
	h = NewImportExportHandler(db, renderer, sm)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("import_file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	validate := httptest.NewRequest(http.MethodPost, "/admin/import/validate", &body)
	validate.Header.Set("Content-Type", writer.FormDataContentType())
	validate = addUserToContext(requestWithSession(sm, validate), &user)
	w := httptest.NewRecorder()
	h.ImportValidate(w, validate)
	if w.Code != http.StatusOK {
		t.Fatalf("validate: status %d: %s", w.Code, w.Body.String())
	}
	stagedPath = sm.GetString(validate.Context(), sessionKeyImportPayloadPath)
	if stagedPath == "" {
		t.Fatalf("validation did not stage the payload; page: %.300s", w.Body.String())
	}
	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("staged file missing: %v", err)
	}
	token, _, err := sm.Commit(validate.Context())
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"conflict_strategy": {string(transfer.ConflictSkip)}, "import_pages": {"on"}}
	if dryRun {
		form.Set("dry_run", "on")
	}
	confirm := httptest.NewRequest(http.MethodPost, "/admin/import", strings.NewReader(form.Encode()))
	confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirmCtx, err = sm.Load(confirm.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	confirm = addUserToContext(confirm.WithContext(confirmCtx), &user)
	w = httptest.NewRecorder()
	h.Import(w, confirm)
	if w.Code != http.StatusOK {
		t.Fatalf("import: status %d: %s", w.Code, w.Body.String())
	}
	return h, db, stagedPath, confirmCtx
}

func exportPayload(t *testing.T, slug string) []byte {
	t.Helper()
	data := transfer.ExportData{Version: transfer.ExportVersion, Pages: []transfer.ExportPage{{
		ID: 1, Title: "Imported " + slug, Slug: slug, Status: "draft",
	}}}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// TestImportFlowStagesAndConfirms fails when the two-step import no longer
// carries the upload from validation to confirmation through the staged file,
// or leaves that file behind after a confirmed import. Both upload formats
// are covered.
func TestImportFlowStagesAndConfirms(t *testing.T) {
	jsonPayload := exportPayload(t, "flow-json")
	var zipBuffer bytes.Buffer
	zw := zip.NewWriter(&zipBuffer)
	entry, err := zw.Create("export.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(exportPayload(t, "flow-zip")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, filename, slug string
		payload              []byte
	}{
		{"json", "export.json", "flow-json", jsonPayload},
		{"zip", "export.zip", "flow-zip", zipBuffer.Bytes()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db, stagedPath, ctx := runImportFlow(t, tc.filename, tc.payload, false)
			var pages int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pages WHERE slug = ?`, tc.slug).Scan(&pages); err != nil || pages != 1 {
				t.Fatalf("imported pages with slug %q = %d, %v; want 1", tc.slug, pages, err)
			}
			if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
				t.Fatalf("staged file survived the confirmed import: %v", err)
			}
			if h.sessionManager.Exists(ctx, sessionKeyImportPayloadPath) || h.sessionManager.Exists(ctx, "import_is_zip") {
				t.Fatal("session still carries import state after a confirmed import")
			}
		})
	}
}

// TestSweepStaleImportPayloads fails when the sweep removes fresh or foreign
// files, or keeps stale staged ones.
func TestSweepStaleImportPayloads(t *testing.T) {
	dir := os.TempDir()
	stale := filepath.Join(dir, importStagePrefix+"stale-test.bin")
	fresh := filepath.Join(dir, importStagePrefix+"fresh-test.bin")
	foreign := filepath.Join(dir, "other-stale-test.bin")
	for _, p := range []string{stale, fresh, foreign} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(p) })
	}
	old := time.Now().Add(-2 * importStageMaxAge)
	for _, p := range []string{stale, foreign} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	sweepStaleImportPayloads()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale staged file was not removed")
	}
	for _, p := range []string{fresh, foreign} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was removed by the sweep", filepath.Base(p))
		}
	}
}

// TestHandWrittenAPIKeysSchemaMatchesMigration fails when the api_keys table
// in the handler test fixture drifts from the real migrations.
func TestHandWrittenAPIKeysSchemaMatchesMigration(t *testing.T) {
	migrated, cleanup := testutil.TestDB(t)
	defer cleanup()
	handWritten := testDB(t)
	// ALTER TABLE appends columns, so only the set of name/type pairs is
	// compared; sqlc expands SELECT * into explicit column lists, so order
	// does not matter at runtime.
	a := pragmaColumns(t, migrated, "api_keys")
	b := pragmaColumns(t, handWritten, "api_keys")
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("api_keys columns differ:\n migration: %v\n fixture:   %v", a, b)
	}
}

// pragmaColumns returns "name type" for every column of table, in order.
func pragmaColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name+" "+strings.ToUpper(ctype))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return cols
}

// TestLoginLockoutIsScopedByClientAddress fails when the login handler stops
// passing the client address into the lockout, which would lock the account
// for every address after one attacker's failures.
func TestLoginLockoutIsScopedByClientAddress(t *testing.T) {
	db, sm := testHandlerSetup(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	lockoutDB, cleanup := testutil.TestDB(t)
	defer cleanup()
	lp := middleware.NewLoginProtection(lockoutDB, middleware.LoginProtectionConfig{MaxFailedAttempts: 3, LockoutDuration: time.Minute, AttemptWindow: time.Minute})
	h := NewAuthHandler(db, renderer, sm, lp, nil, testMFAService(t, db))

	attempt := func(remoteAddr string) string {
		form := url.Values{"email": {"owner@example.com"}, "password": {"wrong"}}
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = remoteAddr
		r = requestWithSession(sm, r)
		h.Login(httptest.NewRecorder(), r)
		return sm.GetString(r.Context(), "flash")
	}
	var last string
	for range 3 {
		last = attempt("198.51.100.7:1")
	}
	if !strings.Contains(last, i18n.T("en", "auth.too_many_attempts", "")[:10]) {
		t.Fatalf("attacker's third failure flash = %q, want the lockout message", last)
	}
	if got := attempt("203.0.113.9:1"); got != i18n.T("en", "auth.attempts_remaining", 2) {
		t.Fatalf("owner's address flash = %q, want %q (not locked by the attacker's address)", got, i18n.T("en", "auth.attempts_remaining", 2))
	}
}
