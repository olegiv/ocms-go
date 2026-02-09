// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"testing"

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

func TestDependencies(t *testing.T) {
	m := New()

	deps := m.Dependencies()
	if len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want nil or empty", deps)
	}
}
