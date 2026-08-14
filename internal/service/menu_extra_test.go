// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/olegiv/ocms-go/internal/store"
)

// menuTestDB creates a minimal in-memory SQLite database with the tables
// required by MenuService (menus, menu_items, pages, languages).
func menuTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE languages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			native_name TEXT NOT NULL DEFAULT '',
			is_default INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			direction TEXT NOT NULL DEFAULT 'ltr',
			position INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE menus (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL,
			language_code TEXT NOT NULL DEFAULT 'en',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE pages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			body TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			author_id INTEGER NOT NULL DEFAULT 1,
			published_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			featured_image_id INTEGER,
			language_code TEXT NOT NULL DEFAULT 'en',
			meta_title TEXT NOT NULL DEFAULT '',
			meta_description TEXT NOT NULL DEFAULT '',
			meta_keywords TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE menu_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			menu_id INTEGER NOT NULL,
			parent_id INTEGER,
			title TEXT NOT NULL,
			url TEXT,
			target TEXT,
			page_id INTEGER,
			position INTEGER NOT NULL DEFAULT 0,
			css_class TEXT,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("failed to create tables: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestNewMenuService(t *testing.T) {
	db := menuTestDB(t)
	svc := NewMenuService(db, nil)
	if svc == nil {
		t.Fatal("NewMenuService returned nil")
	}
	if svc.db != db {
		t.Error("db field not set correctly")
	}
	if svc.queries == nil {
		t.Error("queries field should be initialized")
	}
	if svc.menuCache != nil {
		t.Error("menuCache should be nil when passed nil")
	}
}

func TestMenuServiceInvalidateCache_NilCache(t *testing.T) {
	svc := &MenuService{}
	// Should not panic when menuCache is nil
	svc.InvalidateCache("")
	svc.InvalidateCache("main")
}

func TestGetMenuForLanguageCode_DelegatesToGetMenuForLanguage(t *testing.T) {
	db := menuTestDB(t)

	// Insert a language, menu, and menu item
	_, err := db.Exec(`INSERT INTO languages (code, name, is_active, is_default) VALUES ('en', 'English', 1, 1)`)
	if err != nil {
		t.Fatalf("failed to insert language: %v", err)
	}
	_, err = db.Exec(`INSERT INTO menus (name, slug, language_code) VALUES ('Main', 'main', 'en')`)
	if err != nil {
		t.Fatalf("failed to insert menu: %v", err)
	}
	_, err = db.Exec(`INSERT INTO menu_items (menu_id, title, url, is_active, position) VALUES (1, 'Home', '/', 1, 0)`)
	if err != nil {
		t.Fatalf("failed to insert menu item: %v", err)
	}

	svc := NewMenuService(db, nil)
	items := svc.GetMenuForLanguageCode("main", "en")
	if len(items) == 0 {
		t.Fatal("GetMenuForLanguageCode should return items for the 'main' menu in 'en'")
	}
	if items[0].Title != "Home" {
		t.Errorf("items[0].Title = %q, want %q", items[0].Title, "Home")
	}
}

func TestGetMenu_NoCacheHit(t *testing.T) {
	db := menuTestDB(t)

	_, err := db.Exec(`INSERT INTO menus (name, slug, language_code) VALUES ('Site Nav', 'site-nav', 'en')`)
	if err != nil {
		t.Fatalf("failed to insert menu: %v", err)
	}
	_, err = db.Exec(`INSERT INTO menu_items (menu_id, title, url, is_active, position) VALUES (1, 'Blog', '/blog', 1, 0)`)
	if err != nil {
		t.Fatalf("failed to insert menu item: %v", err)
	}

	svc := NewMenuService(db, nil)
	items := svc.GetMenu("site-nav")

	if len(items) == 0 {
		t.Fatal("expected at least 1 menu item")
	}
	if items[0].Title != "Blog" {
		t.Errorf("items[0].Title = %q, want %q", items[0].Title, "Blog")
	}
	if items[0].URL != "/blog" {
		t.Errorf("items[0].URL = %q, want %q", items[0].URL, "/blog")
	}
}

func TestGetMenu_NotFound(t *testing.T) {
	db := menuTestDB(t)
	svc := NewMenuService(db, nil)

	items := svc.GetMenu("nonexistent-menu")
	if items != nil {
		t.Errorf("GetMenu for nonexistent slug should return nil, got %v", items)
	}
}

func TestBuildMenuTree_EmptyInput(t *testing.T) {
	svc := &MenuService{}
	tree := svc.buildMenuTree(nil)
	if len(tree) != 0 {
		t.Errorf("buildMenuTree(nil) = %d items, want 0", len(tree))
	}
}

func TestBuildMenuTree_PositionOrdering(t *testing.T) {
	svc := &MenuService{}
	now := time.Now()

	items := []store.ListMenuItemsWithPageRow{
		{ID: 1, MenuID: 1, Title: "Third", Url: sql.NullString{String: "/c", Valid: true}, IsActive: true, Position: 2, CreatedAt: now, UpdatedAt: now},
		{ID: 2, MenuID: 1, Title: "First", Url: sql.NullString{String: "/a", Valid: true}, IsActive: true, Position: 0, CreatedAt: now, UpdatedAt: now},
		{ID: 3, MenuID: 1, Title: "Second", Url: sql.NullString{String: "/b", Valid: true}, IsActive: true, Position: 1, CreatedAt: now, UpdatedAt: now},
	}

	tree := svc.buildMenuTree(items)
	if len(tree) != 3 {
		t.Fatalf("len(tree) = %d, want 3", len(tree))
	}
	if tree[0].Title != "First" {
		t.Errorf("tree[0].Title = %q, want First", tree[0].Title)
	}
	if tree[1].Title != "Second" {
		t.Errorf("tree[1].Title = %q, want Second", tree[1].Title)
	}
	if tree[2].Title != "Third" {
		t.Errorf("tree[2].Title = %q, want Third", tree[2].Title)
	}
}

func TestBuildMenuTree_ChildrenOrdering(t *testing.T) {
	svc := &MenuService{}
	now := time.Now()

	items := []store.ListMenuItemsWithPageRow{
		{ID: 1, MenuID: 1, Title: "Parent", Url: sql.NullString{String: "/parent", Valid: true}, IsActive: true, Position: 0, CreatedAt: now, UpdatedAt: now},
		{ID: 2, MenuID: 1, ParentID: sql.NullInt64{Int64: 1, Valid: true}, Title: "Child B", Url: sql.NullString{String: "/b", Valid: true}, IsActive: true, Position: 1, CreatedAt: now, UpdatedAt: now},
		{ID: 3, MenuID: 1, ParentID: sql.NullInt64{Int64: 1, Valid: true}, Title: "Child A", Url: sql.NullString{String: "/a", Valid: true}, IsActive: true, Position: 0, CreatedAt: now, UpdatedAt: now},
	}

	tree := svc.buildMenuTree(items)
	if len(tree) != 1 {
		t.Fatalf("len(tree) = %d, want 1", len(tree))
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("len(children) = %d, want 2", len(tree[0].Children))
	}
	if tree[0].Children[0].Title != "Child A" {
		t.Errorf("children[0].Title = %q, want Child A", tree[0].Children[0].Title)
	}
	if tree[0].Children[1].Title != "Child B" {
		t.Errorf("children[1].Title = %q, want Child B", tree[0].Children[1].Title)
	}
}

func TestBuildMenuTree_CSSClass(t *testing.T) {
	svc := &MenuService{}
	now := time.Now()

	items := []store.ListMenuItemsWithPageRow{
		{
			ID:        1,
			MenuID:    1,
			Title:     "Styled Item",
			Url:       sql.NullString{String: "/styled", Valid: true},
			CssClass:  sql.NullString{String: "highlight bold", Valid: true},
			IsActive:  true,
			Position:  0,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	tree := svc.buildMenuTree(items)
	if len(tree) != 1 {
		t.Fatalf("len(tree) = %d, want 1", len(tree))
	}
	if tree[0].CSSClass != "highlight bold" {
		t.Errorf("CSSClass = %q, want %q", tree[0].CSSClass, "highlight bold")
	}
}

func TestBuildMenuTree_TargetBlank(t *testing.T) {
	svc := &MenuService{}
	now := time.Now()

	items := []store.ListMenuItemsWithPageRow{
		{
			ID:        1,
			MenuID:    1,
			Title:     "External",
			Url:       sql.NullString{String: "https://example.com", Valid: true},
			Target:    sql.NullString{String: "_blank", Valid: true},
			IsActive:  true,
			Position:  0,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	tree := svc.buildMenuTree(items)
	if len(tree) != 1 {
		t.Fatalf("len(tree) = %d, want 1", len(tree))
	}
	if tree[0].Target != "_blank" {
		t.Errorf("Target = %q, want _blank", tree[0].Target)
	}
}

func TestBuildMenuTree_ThreeLevelNesting(t *testing.T) {
	svc := &MenuService{}
	now := time.Now()

	// Root -> Parent -> Grandchild
	items := []store.ListMenuItemsWithPageRow{
		{ID: 1, MenuID: 1, Title: "Root", Url: sql.NullString{String: "/", Valid: true}, IsActive: true, Position: 0, CreatedAt: now, UpdatedAt: now},
		{ID: 2, MenuID: 1, ParentID: sql.NullInt64{Int64: 1, Valid: true}, Title: "Parent", Url: sql.NullString{String: "/parent", Valid: true}, IsActive: true, Position: 0, CreatedAt: now, UpdatedAt: now},
		{ID: 3, MenuID: 1, ParentID: sql.NullInt64{Int64: 2, Valid: true}, Title: "Grandchild", Url: sql.NullString{String: "/gc", Valid: true}, IsActive: true, Position: 0, CreatedAt: now, UpdatedAt: now},
	}

	tree := svc.buildMenuTree(items)
	if len(tree) != 1 {
		t.Fatalf("len(tree) = %d, want 1", len(tree))
	}
	if len(tree[0].Children) != 1 {
		t.Fatalf("root children = %d, want 1", len(tree[0].Children))
	}
	if len(tree[0].Children[0].Children) != 1 {
		t.Fatalf("parent children = %d, want 1", len(tree[0].Children[0].Children))
	}
	if tree[0].Children[0].Children[0].Title != "Grandchild" {
		t.Errorf("grandchild title = %q, want Grandchild", tree[0].Children[0].Children[0].Title)
	}
}

func TestGetMenuForLanguage_FailsClosedOnLanguageLookupError(t *testing.T) {
	db := menuTestDB(t)

	_, err := db.Exec(`INSERT INTO menus (name, slug, language_code) VALUES ('Nav', 'nav', 'en')`)
	if err != nil {
		t.Fatalf("insert menu: %v", err)
	}
	_, err = db.Exec(`INSERT INTO menu_items (menu_id, title, url, is_active, position) VALUES (1, 'Home', '/', 1, 0)`)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}

	if _, err := db.Exec(`DROP TABLE languages`); err != nil {
		t.Fatalf("drop languages: %v", err)
	}
	items := NewMenuService(db, nil).GetMenuForLanguage("nav", "en")
	if items != nil {
		t.Fatalf("operational language lookup error leaked unrelated menu: %v", items)
	}
}

func TestGetMenuForLanguage_UnknownSlugReturnsNil(t *testing.T) {
	db := menuTestDB(t)
	_, err := db.Exec(`INSERT INTO languages (code, name, is_active, is_default) VALUES ('en', 'English', 1, 1)`)
	if err != nil {
		t.Fatalf("insert language: %v", err)
	}

	svc := NewMenuService(db, nil)
	items := svc.GetMenuForLanguage("does-not-exist", "en")
	if items != nil {
		t.Errorf("GetMenuForLanguage with unknown slug should return nil, got %v", items)
	}
}

func TestGetMenuForLanguage_DoesNotFallBackToUnrelatedLanguage(t *testing.T) {
	db := menuTestDB(t)
	_, err := db.Exec(`
		INSERT INTO languages (code, name, is_active, is_default) VALUES
			('en', 'English', 1, 1),
			('fr', 'French', 1, 0),
			('de', 'German', 1, 0);
		INSERT INTO menus (name, slug, language_code) VALUES ('German main', 'main', 'de');
		INSERT INTO menu_items (menu_id, title, url, is_active, position)
		VALUES (1, 'Deutsch', '/de', 1, 0);
	`)
	if err != nil {
		t.Fatalf("seed unrelated-language menu: %v", err)
	}

	items := NewMenuService(db, nil).GetMenuForLanguage("main", "fr")
	if items != nil {
		t.Fatalf("expected no menu instead of German fallback, got %#v", items)
	}
}

func TestGetMenuForLanguage_DoesNotFallBackToUnsafeDefault(t *testing.T) {
	for _, defaultLanguage := range []struct {
		code     string
		active   int
		menuPath string
	}{
		{code: "admin", active: 1, menuPath: "/admin-owned"},
		{code: "en", active: 0, menuPath: "/inactive-owned"},
	} {
		t.Run(defaultLanguage.code, func(t *testing.T) {
			db := menuTestDB(t)
			_, err := db.Exec(`
				INSERT INTO languages (code, name, is_active, is_default) VALUES
					(?, 'Unsafe default', ?, 1),
					('fr', 'French', 1, 0);
				INSERT INTO menus (name, slug, language_code) VALUES ('Unsafe main', 'main', ?);
				INSERT INTO menu_items (menu_id, title, url, is_active, position)
				VALUES (1, 'Unsafe', ?, 1, 0);
			`, defaultLanguage.code, defaultLanguage.active, defaultLanguage.code, defaultLanguage.menuPath)
			if err != nil {
				t.Fatal(err)
			}
			if items := NewMenuService(db, nil).GetMenuForLanguage("main", "fr"); items != nil {
				t.Fatalf("unsafe default menu leaked into French namespace: %#v", items)
			}
		})
	}
}

func TestGetMenuForLanguageFailsClosedWithAmbiguousDefaults(t *testing.T) {
	db := menuTestDB(t)
	if _, err := db.Exec(`
		INSERT INTO languages (code, name, is_active, is_default) VALUES
			('en', 'English', 1, 1), ('fr', 'French', 1, 1);
		INSERT INTO menus (name, slug, language_code) VALUES ('French main', 'main', 'fr');
		INSERT INTO menu_items (menu_id, title, url, is_active, position)
		VALUES (1, 'French', '/fr', 1, 0);
	`); err != nil {
		t.Fatalf("seed ambiguous defaults: %v", err)
	}
	if items := NewMenuService(db, nil).GetMenuForLanguage("main", "fr"); items != nil {
		t.Fatalf("menu items = %+v; want nil with multiple defaults", items)
	}
}

func TestGetMenuForLanguage_PageLinksUsePageLanguage(t *testing.T) {
	db := menuTestDB(t)

	_, err := db.Exec(`
		INSERT INTO languages (code, name, native_name, is_default, is_active, position) VALUES
			('en', 'English', 'English', 1, 1, 0),
			('fr', 'French', 'Français', 0, 1, 1),
			('de', 'German', 'Deutsch', 0, 1, 2),
			('es', 'Spanish', 'Español', 0, 0, 3),
			('admin', 'Legacy reserved', 'Legacy reserved', 0, 1, 4);
		INSERT INTO menus (name, slug, language_code) VALUES
			('Default main', 'main', 'en');
		INSERT INTO pages (title, slug, status, language_code) VALUES
			('English page', 'english-page', 'published', 'en'),
			('French page', 'french-page', 'published', 'fr'),
			('Inactive page', 'inactive-page', 'published', 'es'),
			('Reserved page', 'reserved-page', 'published', 'admin'),
			('Orphan page', 'orphan-page', 'published', 'zz');
		INSERT INTO menu_items (menu_id, title, url, page_id, is_active, position) VALUES
			(1, 'English page', '/stale-english', 1, 1, 0),
			(1, 'French page', '/stale-french', 2, 1, 1),
			(1, 'Inactive page', '/stale-inactive', 3, 1, 2),
			(1, 'Reserved page', '/stale-reserved', 4, 1, 3),
			(1, 'Orphan page', '/stale-orphan', 5, 1, 4);
	`)
	if err != nil {
		t.Fatalf("seed multilingual menu: %v", err)
	}

	// No German menu exists, so the service falls back to the English menu.
	// Page-backed URLs must still follow each linked page's canonical language
	// instead of inheriting the requested German prefix.
	items := NewMenuService(db, nil).GetMenuForLanguage("main", "de")
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 safe page links: %#v", len(items), items)
	}
	if items[0].URL != "/english-page" {
		t.Errorf("default page URL = %q, want /english-page", items[0].URL)
	}
	if items[1].URL != "/fr/french-page" {
		t.Errorf("non-default page URL = %q, want /fr/french-page", items[1].URL)
	}
}

func TestGetMenuWithContext(t *testing.T) {
	db := menuTestDB(t)

	_, err := db.Exec(`INSERT INTO menus (name, slug, language_code) VALUES ('Footer', 'footer', 'en')`)
	if err != nil {
		t.Fatalf("insert menu: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO menu_items (menu_id, title, url, target, is_active, position) VALUES
			(1, 'Privacy', '/privacy', '_self', 1, 0),
			(1, 'Terms', '/terms', '_blank', 1, 1)
	`)
	if err != nil {
		t.Fatalf("insert items: %v", err)
	}

	svc := NewMenuService(db, nil)
	items := svc.GetMenu("footer")

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Title != "Privacy" {
		t.Errorf("items[0].Title = %q, want Privacy", items[0].Title)
	}
	if items[1].Target != "_blank" {
		t.Errorf("items[1].Target = %q, want _blank", items[1].Target)
	}
}
