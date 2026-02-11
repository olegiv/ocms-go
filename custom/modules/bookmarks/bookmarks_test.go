// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// testModule creates a test Module with database access.
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

// testModuleWithHooks creates a test Module and returns the hooks registry.
func testModuleWithHooks(t *testing.T, db *sql.DB) *module.HookRegistry {
	t.Helper()
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, hooks := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return hooks
}

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "bookmarks" {
		t.Errorf("Name() = %q, want bookmarks", m.Name())
	}
	if m.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want 1.0.0", m.Version())
	}
	if m.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestModuleAdminURL(t *testing.T) {
	m := New()

	if m.AdminURL() != "/admin/bookmarks" {
		t.Errorf("AdminURL() = %q, want /admin/bookmarks", m.AdminURL())
	}
}

func TestModuleSidebarLabel(t *testing.T) {
	m := New()

	if m.SidebarLabel() != "Bookmarks" {
		t.Errorf("SidebarLabel() = %q, want Bookmarks", m.SidebarLabel())
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 1)
}

func TestModuleTemplateFuncs(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}

	// Check bookmarkCount exists and returns 0 initially
	if fn, ok := funcs["bookmarkCount"]; !ok {
		t.Error("bookmarkCount not found")
	} else {
		result := fn.(func() int)()
		if result != 0 {
			t.Errorf("bookmarkCount() = %d, want 0", result)
		}
	}

	// Check bookmarkFavorites exists and returns nil initially
	if fn, ok := funcs["bookmarkFavorites"]; !ok {
		t.Error("bookmarkFavorites not found")
	} else {
		result := fn.(func() []Bookmark)()
		if len(result) != 0 {
			t.Errorf("bookmarkFavorites() returned %d items, want 0", len(result))
		}
	}
}

func TestModuleTemplateFuncsWithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create some bookmarks
	_, err := m.createBookmark("Test 1", "https://test1.com", "desc", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}
	_, err = m.createBookmark("Fav 1", "https://fav1.com", "fav", true)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	funcs := m.TemplateFuncs()

	// bookmarkCount should return 2
	countFn := funcs["bookmarkCount"].(func() int)
	if got := countFn(); got != 2 {
		t.Errorf("bookmarkCount() = %d, want 2", got)
	}

	// bookmarkFavorites should return 1
	favFn := funcs["bookmarkFavorites"].(func() []Bookmark)
	favs := favFn()
	if len(favs) != 1 {
		t.Errorf("bookmarkFavorites() returned %d items, want 1", len(favs))
	}
	if len(favs) > 0 && favs[0].Title != "Fav 1" {
		t.Errorf("favorite title = %q, want Fav 1", favs[0].Title)
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	hooks := testModuleWithHooks(t, db)

	if !hooks.HasHandlers(module.HookPageAfterSave) {
		t.Error("HookPageAfterSave handler not registered")
	}
}

func TestModuleShutdown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestCreateBookmark(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	bookmark, err := m.createBookmark("Go Docs", "https://go.dev", "Official Go documentation", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	if bookmark.ID == 0 {
		t.Error("bookmark ID should not be 0")
	}
	if bookmark.Title != "Go Docs" {
		t.Errorf("bookmark.Title = %q, want 'Go Docs'", bookmark.Title)
	}
	if bookmark.URL != "https://go.dev" {
		t.Errorf("bookmark.URL = %q, want 'https://go.dev'", bookmark.URL)
	}
	if bookmark.Description != "Official Go documentation" {
		t.Errorf("bookmark.Description = %q, want 'Official Go documentation'", bookmark.Description)
	}
	if bookmark.IsFavorite {
		t.Error("bookmark.IsFavorite should be false")
	}
	if bookmark.CreatedAt.IsZero() {
		t.Error("bookmark.CreatedAt should not be zero")
	}
}

func TestCreateBookmarkFavorite(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	bookmark, err := m.createBookmark("Favorite", "https://example.com", "", true)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	if !bookmark.IsFavorite {
		t.Error("bookmark.IsFavorite should be true")
	}
}

func TestListBookmarks(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// List empty
	items, err := m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0", len(items))
	}

	// Create some bookmarks
	_, err = m.createBookmark("Link 1", "https://link1.com", "First", false)
	if err != nil {
		t.Fatalf("createBookmark 1: %v", err)
	}
	_, err = m.createBookmark("Link 2", "https://link2.com", "Second", true)
	if err != nil {
		t.Fatalf("createBookmark 2: %v", err)
	}

	// List again
	items, err = m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	// Favorites should be sorted first
	if items[0].Title != "Link 2" {
		t.Errorf("first item should be favorite 'Link 2', got %q", items[0].Title)
	}
}

func TestListFavorites(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	_, err := m.createBookmark("Regular", "https://regular.com", "", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}
	_, err = m.createBookmark("Fav 1", "https://fav1.com", "", true)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}
	_, err = m.createBookmark("Fav 2", "https://fav2.com", "", true)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	favs, err := m.listFavorites()
	if err != nil {
		t.Fatalf("listFavorites: %v", err)
	}
	if len(favs) != 2 {
		t.Errorf("len(favs) = %d, want 2", len(favs))
	}

	for _, f := range favs {
		if !f.IsFavorite {
			t.Errorf("favorite %q has IsFavorite=false", f.Title)
		}
	}
}

func TestCountBookmarks(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	count, err := m.countBookmarks()
	if err != nil {
		t.Fatalf("countBookmarks: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	_, err = m.createBookmark("Test", "https://test.com", "", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	count, err = m.countBookmarks()
	if err != nil {
		t.Fatalf("countBookmarks: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestToggleFavorite(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	bookmark, err := m.createBookmark("Toggle Me", "https://toggle.com", "", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	// Toggle to favorite
	if err := m.toggleFavorite(bookmark.ID); err != nil {
		t.Fatalf("toggleFavorite: %v", err)
	}

	items, err := m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if !items[0].IsFavorite {
		t.Error("bookmark should be favorite after toggle")
	}

	// Toggle back
	if err := m.toggleFavorite(bookmark.ID); err != nil {
		t.Fatalf("toggleFavorite: %v", err)
	}

	items, err = m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if items[0].IsFavorite {
		t.Error("bookmark should not be favorite after second toggle")
	}
}

func TestToggleFavoriteNotFound(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	err := m.toggleFavorite(99999)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("toggleFavorite(99999) = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteBookmark(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	bookmark, err := m.createBookmark("To Delete", "https://delete.com", "", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	if err := m.deleteBookmark(bookmark.ID); err != nil {
		t.Fatalf("deleteBookmark: %v", err)
	}

	items, err := m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0 after delete", len(items))
	}
}

func TestDeleteBookmarkNotFound(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	err := m.deleteBookmark(99999)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("deleteBookmark(99999) = %v, want sql.ErrNoRows", err)
	}
}

func TestMigrationDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	moduleutil.RunMigrationsDown(t, db, m.Migrations())

	moduleutil.AssertTableNotExists(t, db, "bookmarks")
}

func TestHookRegistration(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	hooks := testModuleWithHooks(t, db)

	result, err := hooks.Call(context.Background(), module.HookPageAfterSave, "test data")
	if err != nil {
		t.Errorf("HookPageAfterSave: %v", err)
	}
	if result != "test data" {
		t.Errorf("HookPageAfterSave result = %v, want 'test data'", result)
	}
}

func TestHookHandlerInfo(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	hooks := testModuleWithHooks(t, db)

	afterSaveCount := hooks.HandlerCount(module.HookPageAfterSave)
	if afterSaveCount != 1 {
		t.Errorf("HookPageAfterSave handler count = %d, want 1", afterSaveCount)
	}
}

func TestMultipleBookmarksCRUD(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Create multiple bookmarks
	bookmarkCount := 5
	var createdIDs []int64
	for idx := 1; idx <= bookmarkCount; idx++ {
		bookmark, err := m.createBookmark("Bookmark", "https://example.com", "", false)
		if err != nil {
			t.Fatalf("createBookmark %d: %v", idx, err)
		}
		createdIDs = append(createdIDs, bookmark.ID)
	}

	// List all
	items, err := m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if len(items) != bookmarkCount {
		t.Errorf("len(items) = %d, want %d", len(items), bookmarkCount)
	}

	// Delete every other bookmark
	for idx := 0; idx < len(createdIDs); idx += 2 {
		if err := m.deleteBookmark(createdIDs[idx]); err != nil {
			t.Errorf("deleteBookmark(%d): %v", createdIDs[idx], err)
		}
	}

	// Check remaining count
	items, err = m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks after delete: %v", err)
	}
	expectedRemaining := bookmarkCount / 2
	if len(items) != expectedRemaining {
		t.Errorf("len(items) after delete = %d, want %d", len(items), expectedRemaining)
	}
}

func TestAdminTemplateParsed(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if m.adminTmpl == nil {
		t.Error("admin template should be parsed after Init")
	}
}

func TestDependencies(t *testing.T) {
	m := New()

	deps := m.Dependencies()
	if len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want nil or empty", deps)
	}
}

func TestTranslationsFS(t *testing.T) {
	m := New()
	tfs := m.TranslationsFS()

	// Verify English translations exist
	data, err := fs.ReadFile(tfs, "locales/en/messages.json")
	if err != nil {
		t.Fatalf("reading en translations: %v", err)
	}
	if len(data) == 0 {
		t.Error("en translations file is empty")
	}

	// Verify Russian translations exist
	data, err = fs.ReadFile(tfs, "locales/ru/messages.json")
	if err != nil {
		t.Fatalf("reading ru translations: %v", err)
	}
	if len(data) == 0 {
		t.Error("ru translations file is empty")
	}

	// Verify JSON is valid and has messages
	var parsed struct {
		Language string `json:"language"`
		Messages []struct {
			ID          string `json:"id"`
			Translation string `json:"translation"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parsing ru translations: %v", err)
	}
	if parsed.Language != "ru" {
		t.Errorf("language = %q, want ru", parsed.Language)
	}
	if len(parsed.Messages) == 0 {
		t.Error("ru translations has no messages")
	}
}

func TestTranslationsKeys(t *testing.T) {
	m := New()
	tfs := m.TranslationsFS()

	data, err := fs.ReadFile(tfs, "locales/en/messages.json")
	if err != nil {
		t.Fatalf("reading en translations: %v", err)
	}

	var parsed struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parsing en translations: %v", err)
	}

	// Verify essential translation keys exist
	requiredKeys := []string{
		"nav.bookmarks",
		"bookmarks.title",
		"bookmarks.description",
		"bookmarks.add_bookmark",
		"bookmarks.field_title",
		"bookmarks.field_url",
		"bookmarks.no_bookmarks",
		"bookmarks.confirm_delete",
		"bookmarks.table_actions",
		"bookmarks.title_required",
		"bookmarks.url_required",
	}

	keySet := make(map[string]bool)
	for _, msg := range parsed.Messages {
		keySet[msg.ID] = true
	}

	for _, key := range requiredKeys {
		if !keySet[key] {
			t.Errorf("missing required translation key: %s", key)
		}
	}
}

func TestRegisterRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterRoutes(r)

	// Walk routes and check /bookmarks is registered
	var found bool
	walkErr := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == "GET" && route == "/bookmarks" {
			found = true
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking routes: %v", walkErr)
	}
	if !found {
		t.Error("GET /bookmarks route not registered")
	}
}

func TestRegisterAdminRoutes(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	expectedRoutes := map[string]string{
		"GET /bookmarks":            "admin list",
		"POST /bookmarks":           "create",
		"POST /bookmarks/{id}/toggle": "toggle favorite",
		"DELETE /bookmarks/{id}":     "delete",
	}

	foundRoutes := make(map[string]bool)
	walkErr := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		foundRoutes[key] = true
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking routes: %v", walkErr)
	}

	for route, desc := range expectedRoutes {
		if !foundRoutes[route] {
			t.Errorf("route %q (%s) not registered", route, desc)
		}
	}
}

func TestHandlePublicListEmpty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		Bookmarks []Bookmark `json:"bookmarks"`
		Total     int        `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Total != 0 {
		t.Errorf("total = %d, want 0", body.Total)
	}
	if len(body.Bookmarks) != 0 {
		t.Errorf("bookmarks count = %d, want 0", len(body.Bookmarks))
	}
}

func TestHandlePublicListWithData(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	_, err := m.createBookmark("Test Link", "https://test.com", "A test", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}
	_, err = m.createBookmark("Fav Link", "https://fav.com", "", true)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	r := chi.NewRouter()
	m.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Bookmarks []Bookmark `json:"bookmarks"`
		Total     int        `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.Total != 2 {
		t.Errorf("total = %d, want 2", body.Total)
	}
	// Favorites sorted first
	if len(body.Bookmarks) > 0 && body.Bookmarks[0].Title != "Fav Link" {
		t.Errorf("first bookmark = %q, want Fav Link", body.Bookmarks[0].Title)
	}
}

func TestHandleAdminList(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	_, err := m.createBookmark("Admin Link", "https://admin.com", "Admin test", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html*", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Admin Link") {
		t.Error("response should contain bookmark title 'Admin Link'")
	}
	if !strings.Contains(body, "https://admin.com") {
		t.Error("response should contain bookmark URL")
	}
	if !strings.Contains(body, m.Version()) {
		t.Error("response should contain module version")
	}
}

func TestHandleAdminListEmpty(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "No bookmarks yet") {
		t.Error("empty state should show 'No bookmarks yet'")
	}
}

func TestHandleCreateFormPost(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	form := url.Values{}
	form.Set("title", "New Bookmark")
	form.Set("url", "https://new.example.com")
	form.Set("description", "Created via form")

	req := httptest.NewRequest(http.MethodPost, "/bookmarks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Should redirect
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Verify bookmark was created
	items, err := m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Title != "New Bookmark" {
		t.Errorf("title = %q, want New Bookmark", items[0].Title)
	}
	if items[0].URL != "https://new.example.com" {
		t.Errorf("url = %q, want https://new.example.com", items[0].URL)
	}
}

func TestHandleCreateJSON(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	form := url.Values{}
	form.Set("title", "JSON Bookmark")
	form.Set("url", "https://json.example.com")

	req := httptest.NewRequest(http.MethodPost, "/bookmarks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var bookmark Bookmark
	if err := json.NewDecoder(rec.Body).Decode(&bookmark); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if bookmark.Title != "JSON Bookmark" {
		t.Errorf("title = %q, want JSON Bookmark", bookmark.Title)
	}
}

func TestHandleCreateWithFavorite(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	form := url.Values{}
	form.Set("title", "Fav Bookmark")
	form.Set("url", "https://fav.example.com")
	form.Set("is_favorite", "on")

	req := httptest.NewRequest(http.MethodPost, "/bookmarks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var bookmark Bookmark
	if err := json.NewDecoder(rec.Body).Decode(&bookmark); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !bookmark.IsFavorite {
		t.Error("bookmark should be marked as favorite")
	}
}

func TestHandleCreateMissingTitle(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	form := url.Values{}
	form.Set("url", "https://notitle.com")

	req := httptest.NewRequest(http.MethodPost, "/bookmarks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleCreateMissingURL(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	form := url.Values{}
	form.Set("title", "No URL")

	req := httptest.NewRequest(http.MethodPost, "/bookmarks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleToggleFavoriteHTTP(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	bookmark, err := m.createBookmark("Toggle HTTP", "https://toggle.com", "", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/bookmarks/%d/toggle", bookmark.ID), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// Verify it was toggled
	items, err := m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if !items[0].IsFavorite {
		t.Error("bookmark should be favorite after toggle")
	}
}

func TestHandleToggleFavoriteInvalidID(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/bookmarks/abc/toggle", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleDeleteHTTP(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	bookmark, err := m.createBookmark("Delete HTTP", "https://delete.com", "", false)
	if err != nil {
		t.Fatalf("createBookmark: %v", err)
	}

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/bookmarks/%d", bookmark.ID), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	// Verify it was deleted
	items, err := m.listBookmarks()
	if err != nil {
		t.Fatalf("listBookmarks: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0", len(items))
	}
}

func TestHandleDeleteInvalidID(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)

	req := httptest.NewRequest(http.MethodDelete, "/bookmarks/notanumber", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
