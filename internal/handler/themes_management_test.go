// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/theme"
)

func themeManagementHandler(t *testing.T) *ThemesHandler {
	t.Helper()
	return themeManagementHandlerInDir(t, t.TempDir())
}

func themeManagementHandlerInDir(t *testing.T, customDir string) *ThemesHandler {
	t.Helper()
	db, sm := testHandlerSetup(t)
	renderer, err := render.New(render.Config{TemplatesFS: fstest.MapFS{}, SessionManager: sm, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	m := theme.NewManager(embed.FS{}, customDir, testutil.TestLoggerSilent())
	m.SetFuncMap(testutil.MinimalThemeFuncMap())
	if err := i18n.Init(nil); err != nil {
		t.Fatal(err)
	}
	return NewThemesHandler(db, renderer, sm, m, cache.NewManager(store.New(db)), service.NewEventService(db), nil)
}

func starterThemeArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	source := filepath.Join(repositoryRoot(t), "custom", "themes", "starter")
	err := filepath.WalkDir(source, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, file)
		if err != nil {
			return err
		}
		name := "starter"
		if rel != "." {
			name += "/" + filepath.ToSlash(rel)
		}
		header := &tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0755}
		if !entry.IsDir() {
			content, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			header.Typeflag, header.Size, header.Mode = tar.TypeReg, int64(len(content)), 0644
			if err := writer.WriteHeader(header); err != nil {
				return err
			}
			_, err = writer.Write(content)
			return err
		}
		return writer.WriteHeader(header)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// themeImportPassword is the current password of themeImportUser.
const themeImportPassword = "theme-import-pass-1234"

var (
	themeImportHashOnce sync.Once
	themeImportHash     string
)

// themeImportUser returns an administrator whose stored hash matches
// themeImportPassword; the Argon2 hash is computed once per test binary.
func themeImportUser(t *testing.T) *store.User {
	t.Helper()
	themeImportHashOnce.Do(func() {
		hash, err := auth.HashPassword(themeImportPassword)
		if err != nil {
			t.Fatal(err)
		}
		themeImportHash = hash
	})
	return &store.User{ID: 1, Email: "admin@example.com", Role: "admin", PasswordHash: themeImportHash}
}

// themeUploadRequest builds an import request that passes the password
// confirmation, so the tests of archive handling are not affected by it.
func themeUploadRequest(t *testing.T, h *ThemesHandler, filename string, contents []byte) *http.Request {
	t.Helper()
	return themeUploadRequestAs(t, h, filename, contents, themeImportUser(t), themeImportPassword)
}

// themeUploadRequestAs builds an import request on behalf of user (nil for
// no user in context) carrying password as the confirmation field (empty
// omits the field).
func themeUploadRequestAs(t *testing.T, h *ThemesHandler, filename string, contents []byte, user *store.User, password string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if password != "" {
		if err := writer.WriteField("confirm_password", password); err != nil {
			t.Fatal(err)
		}
	}
	if filename != "" {
		part, err := writer.CreateFormFile("archive", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/themes/import", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r = requestWithSession(h.sessionManager, r)
	if user != nil {
		r = addUserToContext(r, user)
	}
	return r
}

// TestThemeImportRequiresPasswordConfirmation fails when an archive can be
// installed without the administrator re-entering their password: an
// imported theme is same-origin code with the site's full privileges.
func TestThemeImportRequiresPasswordConfirmation(t *testing.T) {
	archive := starterThemeArchive(t)
	for _, tt := range []struct {
		name         string
		user         *store.User
		password     string
		wantFlash    string
		wantLocation string
	}{
		{"missing password", themeImportUser(t), "", i18n.T("en", "themes.import_password_invalid"), "/admin/themes"},
		{"wrong password", themeImportUser(t), "not-" + themeImportPassword, i18n.T("en", "themes.import_password_invalid"), "/admin/themes"},
		{"no user in context", nil, themeImportPassword, "", "/login"},
		{"unverifiable stored hash", &store.User{ID: 1, Email: "admin@example.com", Role: "admin", PasswordHash: "not-a-hash"}, themeImportPassword, i18n.T("en", "themes.import_password_unverified"), "/admin/themes"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := themeManagementHandler(t)
			r := themeUploadRequestAs(t, h, "starter.tar.gz", archive, tt.user, tt.password)
			w := httptest.NewRecorder()
			h.Import(w, r)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != tt.wantLocation || h.themeManager.ThemeCount() != 0 {
				t.Fatalf("import without confirmation: status %d -> %s, themes %d", w.Code, w.Header().Get("Location"), h.themeManager.ThemeCount())
			}
			if got := h.sessionManager.GetString(r.Context(), "flash"); got != tt.wantFlash {
				t.Fatalf("flash = %q, want %q", got, tt.wantFlash)
			}
		})
	}

	h := themeManagementHandler(t)
	r := themeUploadRequestAs(t, h, "starter.tar.gz", archive, themeImportUser(t), themeImportPassword)
	h.Import(httptest.NewRecorder(), r)
	if !h.themeManager.HasTheme("starter") {
		t.Fatalf("import with the correct password failed: %q", h.sessionManager.GetString(r.Context(), "flash"))
	}
}

func TestThemeManagementHandlerLifecycle(t *testing.T) {
	h := themeManagementHandler(t)
	archive := starterThemeArchive(t)
	request := themeUploadRequest(t, h, "starter.tar.gz", archive)
	w := httptest.NewRecorder()
	h.Import(w, request)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/themes" || !h.themeManager.HasTheme("starter") {
		t.Fatalf("import: %d, %s", w.Code, w.Body.String())
	}
	if h.themeManager.GetActiveTheme() != nil {
		t.Fatal("import activated theme")
	}
	if got := h.sessionManager.GetString(request.Context(), "flash_type"); got != "success" {
		t.Fatalf("flash type: %s", got)
	}
	request = themeUploadRequest(t, h, "starter.tar.gz", archive)
	h.Import(httptest.NewRecorder(), request)
	if got := h.sessionManager.GetString(request.Context(), "flash_type"); got != "error" {
		t.Fatalf("duplicate import flash: %s", got)
	}

	// Persisted settings are a downstream consumer and must survive deletion.
	if _, err := h.queries.UpsertConfig(t.Context(), store.UpsertConfigParams{Key: "theme_settings_starter", Value: `{"accent_color":"red"}`, Type: "json", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	request = requestWithURLParams(httptest.NewRequest(http.MethodPost, "/admin/themes/starter/delete", nil), map[string]string{"name": "starter"})
	request = requestWithSession(h.sessionManager, request)
	w = httptest.NewRecorder()
	h.Delete(w, request)
	if w.Code != http.StatusSeeOther || h.themeManager.HasTheme("starter") {
		t.Fatalf("delete: %d", w.Code)
	}
	if settings := h.loadThemeSettings(request, "starter"); settings["accent_color"] != "red" {
		t.Fatal("deletion discarded settings")
	}
	request = themeUploadRequest(t, h, "starter.tar.gz", archive)
	h.Import(httptest.NewRecorder(), request)
	if !h.themeManager.HasTheme("starter") || h.loadThemeSettings(request, "starter")["accent_color"] != "red" {
		t.Fatal("reinstallation lost settings")
	}
}

func TestThemeUploadHandlerRejectsInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name, filename string
		data           []byte
	}{
		{"missing file", "", nil},
		{"wrong extension", "theme.zip", []byte("archive")},
		{"broken archive", "theme.tar.gz", []byte("archive")},
		{"file size limit", "theme.tar.gz", bytes.Repeat([]byte("x"), theme.MaxArchiveBytes+1)},
		{"request size limit", "theme.tar.gz", bytes.Repeat([]byte("x"), theme.MaxArchiveBytes+(1<<20)+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := themeManagementHandler(t)
			r := themeUploadRequest(t, h, tt.filename, tt.data)
			w := httptest.NewRecorder()
			h.Import(w, r)
			if w.Code != http.StatusSeeOther || h.themeManager.ThemeCount() != 0 {
				t.Fatalf("invalid upload: %d", w.Code)
			}
			if got := h.sessionManager.GetString(r.Context(), "flash_type"); got != "error" {
				t.Fatalf("flash: %s", got)
			}
		})
	}
}

func TestThemeHandlerFlashHidesInternalErrors(t *testing.T) {
	customDir := t.TempDir()
	h := themeManagementHandlerInDir(t, customDir)
	flash := func(r *http.Request) string { return h.sessionManager.GetString(r.Context(), "flash") }

	r := themeUploadRequest(t, h, "broken.tar.gz", []byte("not an archive"))
	h.Import(httptest.NewRecorder(), r)
	if got := flash(r); got != i18n.T("en", "themes.import_failed", "invalid gzip archive") {
		t.Fatalf("validation detail missing: %q", got)
	}
	r = themeUploadRequest(t, h, "starter.tar.gz", starterThemeArchive(t))
	h.Import(httptest.NewRecorder(), r)
	if got := flash(r); got != i18n.T("en", "themes.import_success") {
		t.Fatalf("import: %q", got)
	}

	// With a regular file where the custom directory should be, every
	// operation fails internally with an error that names the server path
	// (a missing directory would simply be recreated); the administrator
	// sees generic text.
	if err := os.RemoveAll(customDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(customDir, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	r = requestWithURLParams(httptest.NewRequest(http.MethodPost, "/admin/themes/starter/delete", nil), map[string]string{"name": "starter"})
	r = requestWithSession(h.sessionManager, r)
	h.Delete(httptest.NewRecorder(), r)
	if got := flash(r); got != i18n.T("en", "themes.delete_error") || strings.Contains(got, customDir) {
		t.Fatalf("internal delete error exposed: %q", got)
	}
	r = themeUploadRequest(t, h, "starter.tar.gz", starterThemeArchive(t))
	h.Import(httptest.NewRecorder(), r)
	if got := flash(r); got != i18n.T("en", "themes.import_error") || strings.Contains(got, customDir) {
		t.Fatalf("internal import error exposed: %q", got)
	}
	r = requestWithURLParams(httptest.NewRequest(http.MethodPost, "/admin/themes/default/delete", nil), map[string]string{"name": "default"})
	r = requestWithSession(h.sessionManager, r)
	h.Delete(httptest.NewRecorder(), r)
	if got := flash(r); got != i18n.T("en", "themes.delete_failed", "theme not found: default") {
		t.Fatalf("validation detail missing: %q", got)
	}
}

func TestThemeManagementAuthorizationAndCSRF(t *testing.T) {
	h := themeManagementHandler(t)
	if _, err := h.themeManager.ImportArchive(bytes.NewReader(starterThemeArchive(t))); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(middleware.CSRF(middleware.CSRFConfig{}))
	router.Use(middleware.RequireAdmin())
	router.Post("/admin/themes/import", h.Import)
	router.Post("/admin/themes/{name}/delete", h.Delete)
	for _, route := range []string{"/admin/themes/import", "/admin/themes/starter/delete"} {
		for _, tt := range []struct {
			name, role, site string
			status           int
		}{
			{"anonymous", "", "same-origin", http.StatusSeeOther},
			{"editor", "editor", "same-origin", http.StatusForbidden},
			{"cross site admin", "admin", "cross-site", http.StatusForbidden},
		} {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				r := requestWithSession(h.sessionManager, httptest.NewRequest(http.MethodPost, route, nil))
				if tt.role != "" {
					r = addUserToContext(r, &store.User{ID: 1, Role: tt.role})
				}
				r.Header.Set("Sec-Fetch-Site", tt.site)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != tt.status {
					t.Fatalf("status: %d, want %d", w.Code, tt.status)
				}
				if !h.themeManager.HasTheme("starter") {
					t.Fatal("blocked request deleted theme")
				}
			})
		}
	}
}

// demoChildMarker proves the demo-mode child process ran the scenario rather
// than matching no tests and exiting successfully.
const demoChildMarker = "theme demo scenario verified"

func TestThemeManagementDemoMode(t *testing.T) {
	// Demo mode is cached globally; run this scenario in a fresh test process.
	if os.Getenv("OCMS_THEME_DEMO_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestThemeManagementDemoMode$", "-test.v")
		command.Env = append(os.Environ(), "OCMS_DEMO_MODE=true", "OCMS_THEME_DEMO_CHILD=1")
		out, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(out), demoChildMarker) {
			t.Fatalf("demo scenario: %v\n%s", err, out)
		}
		return
	}
	h := themeManagementHandler(t)
	archive := starterThemeArchive(t)
	r := themeUploadRequest(t, h, "starter.tar.gz", archive)
	h.Import(httptest.NewRecorder(), r)
	if h.themeManager.ThemeCount() != 0 {
		t.Fatal("demo import succeeded")
	}
	if _, err := h.themeManager.ImportArchive(bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	r = requestWithSession(h.sessionManager, httptest.NewRequest(http.MethodPost, "/admin/themes/starter/delete", nil))
	r = requestWithURLParams(r, map[string]string{"name": "starter"})
	h.Delete(httptest.NewRecorder(), r)
	if !h.themeManager.HasTheme("starter") {
		t.Fatal("demo delete succeeded")
	}
	w := httptest.NewRecorder()
	h.List(w, requestWithSession(h.sessionManager, httptest.NewRequest(http.MethodGet, "/admin/themes", nil)))
	if strings.Contains(w.Body.String(), `action="/admin/themes/import"`) || strings.Contains(w.Body.String(), `action="/admin/themes/starter/delete"`) {
		t.Fatal("demo page exposed theme mutation forms")
	}
	t.Log(demoChildMarker)
}

// TestThemeManagementWritesEventLog fails when a theme import, a refused
// import or a theme deletion stops appearing in the admin event log.
func TestThemeManagementWritesEventLog(t *testing.T) {
	h := themeManagementHandler(t)
	archive := starterThemeArchive(t)

	h.Import(httptest.NewRecorder(), themeUploadRequestAs(t, h, "starter.tar.gz", archive, themeImportUser(t), "wrong-"+themeImportPassword))
	h.Import(httptest.NewRecorder(), themeUploadRequest(t, h, "starter.tar.gz", archive))
	r := requestWithURLParams(httptest.NewRequest(http.MethodPost, "/admin/themes/starter/delete", nil), map[string]string{"name": "starter"})
	h.Delete(httptest.NewRecorder(), addUserToContext(requestWithSession(h.sessionManager, r), themeImportUser(t)))

	events, err := h.queries.ListEventsByCategory(t.Context(), store.ListEventsByCategoryParams{Category: model.EventCategorySecurity, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range events {
		got[e.Message] = e.Level
	}
	for message, level := range map[string]string{
		"Theme import refused: password confirmation failed": model.EventLevelWarning,
		"Theme imported: starter":                            model.EventLevelInfo,
		"Theme deleted: starter":                             model.EventLevelInfo,
	} {
		if got[message] != level {
			t.Errorf("event %q: level = %q, want %q (events: %v)", message, got[message], level, got)
		}
	}
	for _, e := range events {
		if !e.UserID.Valid || e.UserID.Int64 != themeImportUser(t).ID {
			t.Errorf("event %q has no user id", e.Message)
		}
	}
}

// TestThemeImportPasswordConfirmationIsThrottled fails when a stolen session
// can guess the administrator's password through the import form faster than
// at the login form: the confirmation shares the login lockout.
func TestThemeImportPasswordConfirmationIsThrottled(t *testing.T) {
	h := themeManagementHandler(t)
	lockoutDB, cleanup := testutil.TestDB(t)
	defer cleanup()
	h.loginProtection = middleware.NewLoginProtection(lockoutDB, middleware.LoginProtectionConfig{MaxFailedAttempts: 2, LockoutDuration: time.Minute, AttemptWindow: time.Minute})
	archive := starterThemeArchive(t)

	for range 2 {
		h.Import(httptest.NewRecorder(), themeUploadRequestAs(t, h, "starter.tar.gz", archive, themeImportUser(t), "wrong-"+themeImportPassword))
	}
	r := themeUploadRequestAs(t, h, "starter.tar.gz", archive, themeImportUser(t), themeImportPassword)
	h.Import(httptest.NewRecorder(), r)
	if h.themeManager.ThemeCount() != 0 {
		t.Fatal("import succeeded while the account is locked")
	}
	if got := h.sessionManager.GetString(r.Context(), "flash"); got != i18n.T("en", "themes.import_locked") {
		t.Fatalf("flash = %q, want the lockout message", got)
	}
}

// TestThemeEventLogCarriesMetadata fails when the import event loses the
// archive details or the request context.
func TestThemeEventLogCarriesMetadata(t *testing.T) {
	h := themeManagementHandler(t)
	h.Import(httptest.NewRecorder(), themeUploadRequest(t, h, "starter.tar.gz", starterThemeArchive(t)))
	events, err := h.queries.ListEventsByCategory(t.Context(), store.ListEventsByCategoryParams{Category: model.EventCategorySecurity, Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %d, %v", len(events), err)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(events[0].Metadata), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["theme"] != "starter" || meta["archive"] != "starter.tar.gz" || meta["size"].(float64) <= 0 || events[0].RequestUrl == "" || events[0].IpAddress == "" {
		t.Fatalf("event metadata = %v, url %q, ip %q", meta, events[0].RequestUrl, events[0].IpAddress)
	}
}

// TestThemeImportPasswordConfirmationResetsLockout fails when a valid
// password confirmation leaves earlier failed confirmations counted: with a
// limit of two, a typo, a correct confirmation and another typo must not
// lock the account, because the correct confirmation proved identity.
func TestThemeImportPasswordConfirmationResetsLockout(t *testing.T) {
	h := themeManagementHandler(t)
	lockoutDB, cleanup := testutil.TestDB(t)
	defer cleanup()
	h.loginProtection = middleware.NewLoginProtection(lockoutDB, middleware.LoginProtectionConfig{MaxFailedAttempts: 2, LockoutDuration: time.Minute, AttemptWindow: time.Minute})
	archive := starterThemeArchive(t)
	user := themeImportUser(t)

	h.Import(httptest.NewRecorder(), themeUploadRequestAs(t, h, "starter.tar.gz", archive, user, "wrong-"+themeImportPassword))
	h.Import(httptest.NewRecorder(), themeUploadRequestAs(t, h, "starter.tar.gz", archive, user, themeImportPassword))
	if h.themeManager.ThemeCount() != 1 {
		t.Fatal("import with the correct password did not succeed")
	}
	r := themeUploadRequestAs(t, h, "starter.tar.gz", archive, user, "wrong-"+themeImportPassword)
	h.Import(httptest.NewRecorder(), r)

	if locked, _ := h.loginProtection.IsAccountLocked(user.Email, middleware.GetClientIP(r)); locked {
		t.Fatal("account locked after one failure on each side of a valid confirmation")
	}
	if remaining := h.loginProtection.GetRemainingAttempts(user.Email, middleware.GetClientIP(r)); remaining != 1 {
		t.Fatalf("remaining attempts = %d, want 1 (only the failure after the reset counts)", remaining)
	}
}
