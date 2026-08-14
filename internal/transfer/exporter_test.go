// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package transfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"

	_ "github.com/mattn/go-sqlite3"
)

func TestExportEmptyDatabase(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	logger := slog.Default()
	exporter := NewExporter(queries, logger)

	ctx := context.Background()
	opts := DefaultExportOptions()

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify basic structure
	if data.Version != ExportVersion {
		t.Errorf("Expected version %s, got %s", ExportVersion, data.Version)
	}

	if data.ExportedAt.IsZero() {
		t.Error("ExportedAt should not be zero")
	}

	// Languages should include default English from migration
	if len(data.Languages) != 1 {
		t.Errorf("Expected 1 language (default English), got %d", len(data.Languages))
	}

	if len(data.Languages) > 0 && data.Languages[0].Code != "en" {
		t.Errorf("Expected language code 'en', got '%s'", data.Languages[0].Code)
	}
}

func TestExportToWriter(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	logger := slog.Default()
	exporter := NewExporter(queries, logger)

	ctx := context.Background()
	opts := DefaultExportOptions()

	var buf bytes.Buffer
	if err := exporter.ExportToWriter(ctx, opts, &buf); err != nil {
		t.Fatalf("ExportToWriter failed: %v", err)
	}

	// Verify output is valid JSON
	var data ExportData
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}

	if data.Version != ExportVersion {
		t.Errorf("Expected version %s, got %s", ExportVersion, data.Version)
	}
}

func TestExportWithData(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()

	// Get default language
	lang, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create test page
	_, err = ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title:        "Test Page",
		Slug:         "test-page",
		Body:         "Test content",
		Status:       "published",
		AuthorID:     ts.User.ID,
		LanguageCode: lang.Code,
		CreatedAt:    ts.Now,
		UpdatedAt:    ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create page: %v", err)
	}

	// Create test category
	_, err = ts.Queries.CreateCategory(ts.Ctx, store.CreateCategoryParams{
		Name:         "Test Category",
		Slug:         "test-category",
		LanguageCode: lang.Code,
		Position:     0,
		CreatedAt:    ts.Now,
		UpdatedAt:    ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	// Create test tag
	_, err = ts.Queries.CreateTag(ts.Ctx, store.CreateTagParams{
		Name:         "Test Tag",
		Slug:         "test-tag",
		LanguageCode: lang.Code,
		CreatedAt:    ts.Now,
		UpdatedAt:    ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(ts.Queries, logger)
	opts := DefaultExportOptions()

	data, err := exporter.Export(ts.Ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify data
	if len(data.Users) != 1 {
		t.Errorf("Expected 1 user, got %d", len(data.Users))
	}
	if len(data.Users) > 0 && data.Users[0].Email != "test@example.com" {
		t.Errorf("Expected user email 'test@example.com', got '%s'", data.Users[0].Email)
	}

	if len(data.Pages) != 1 {
		t.Errorf("Expected 1 page, got %d", len(data.Pages))
	}
	if len(data.Pages) > 0 && data.Pages[0].Slug != "test-page" {
		t.Errorf("Expected page slug 'test-page', got '%s'", data.Pages[0].Slug)
	}
	if len(data.Pages) > 0 && data.Pages[0].AuthorEmail != "test@example.com" {
		t.Errorf("Expected author email 'test@example.com', got '%s'", data.Pages[0].AuthorEmail)
	}

	if len(data.Categories) != 1 {
		t.Errorf("Expected 1 category, got %d", len(data.Categories))
	}
	if len(data.Categories) > 0 && data.Categories[0].Slug != "test-category" {
		t.Errorf("Expected category slug 'test-category', got '%s'", data.Categories[0].Slug)
	}

	if len(data.Tags) != 1 {
		t.Errorf("Expected 1 tag, got %d", len(data.Tags))
	}
	if len(data.Tags) > 0 && data.Tags[0].Slug != "test-tag" {
		t.Errorf("Expected tag slug 'test-tag', got '%s'", data.Tags[0].Slug)
	}
}

func TestFilteredPageExportOmitsTranslationsToExcludedPages(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()
	fr, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code: "fr", Name: "French", NativeName: "Français", IsActive: true,
		Direction: "ltr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Published", Slug: "published-translation-source", Status: "published",
		AuthorID: ts.User.ID, LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title: "Draft", Slug: "draft-translation-target", Status: "draft",
		AuthorID: ts.User.ID, LanguageCode: "fr", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Queries.CreateTranslation(ts.Ctx, store.CreateTranslationParams{
		EntityType: "page", EntityID: published.ID, LanguageID: fr.ID,
		TranslationID: draft.ID, CreatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}

	opts := DefaultExportOptions()
	opts.PageStatus = "published"
	data, err := NewExporter(ts.Queries, slog.Default()).Export(ts.Ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Pages) != 1 || data.Pages[0].ID != published.ID {
		t.Fatalf("filtered pages = %+v, want only %d", data.Pages, published.ID)
	}
	if len(data.Pages[0].Translations) != 0 {
		t.Fatalf("filtered archive retained translation to omitted page: %v", data.Pages[0].Translations)
	}
	validation, err := NewImporter(ts.Queries, ts.DB, slog.Default()).ValidateData(ts.Ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if !validation.Valid {
		t.Fatalf("exporter produced a self-invalid filtered archive: %v", validation.Errors)
	}
}

func TestFilteredPageExportOmitsMenuItemsLinkedOnlyToExcludedPages(t *testing.T) {
	source := setupTest(t)
	defer source.Cleanup()
	draft, err := source.Queries.CreatePage(source.Ctx, store.CreatePageParams{
		Title: "Draft", Slug: "draft-menu-target", Status: "draft", AuthorID: source.User.ID,
		LanguageCode: "en", CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	menu, err := source.Queries.CreateMenu(source.Ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main", LanguageCode: "en", CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Queries.CreateMenuItem(source.Ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Draft", PageID: sql.NullInt64{Int64: draft.ID, Valid: true},
		IsActive: true, CreatedAt: source.Now, UpdatedAt: source.Now,
	}); err != nil {
		t.Fatal(err)
	}

	opts := DefaultExportOptions()
	opts.PageStatus = "published"
	data, err := NewExporter(source.Queries, slog.Default()).Export(source.Ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Menus) != 1 || len(data.Menus[0].Items) != 0 {
		t.Fatalf("filtered archive retained broken draft-page menu reference: %+v", data.Menus)
	}

	destination := setupTest(t)
	defer destination.Cleanup()
	importer := NewImporter(destination.Queries, destination.DB, slog.Default())
	importOpts := DefaultImportOptions()
	importOpts.DryRun = true
	dry, err := importer.Import(destination.Ctx, data, importOpts)
	if err != nil {
		t.Fatalf("dry-run rejected filtered archive: %v (%+v)", err, dry.Errors)
	}
	importOpts.DryRun = false
	actual, err := importer.Import(destination.Ctx, data, importOpts)
	if err != nil {
		t.Fatalf("real import rejected filtered archive: %v (%+v)", err, actual.Errors)
	}
	importedMenu, err := destination.Queries.GetMenuBySlugAndLanguage(destination.Ctx,
		store.GetMenuBySlugAndLanguageParams{Slug: "main", LanguageCode: "en"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := destination.Queries.ListTopLevelMenuItems(destination.Ctx, importedMenu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("import created inert menu items: %+v", items)
	}
}

func TestMenuOnlyExportPreservesPageBackedHierarchy(t *testing.T) {
	data := exportMenuOnlyHierarchy(t)

	if len(data.Pages) != 0 {
		t.Fatalf("menu-only export included pages: %+v", data.Pages)
	}
	if len(data.Menus) != 1 || len(data.Menus[0].Items) != 1 {
		t.Fatalf("menu-only export lost its hierarchy: %+v", data.Menus)
	}
	parent := data.Menus[0].Items[0]
	if parent.PageSlug != "menu-parent-page" || parent.URL != "" || parent.Target != "_blank" ||
		parent.CSSClass != "nav-parent" || parent.IsActive || parent.Position != 3 {
		t.Fatalf("page-backed parent = %+v, want preserved slug and fields", parent)
	}
	if len(parent.Children) != 2 {
		t.Fatalf("page-backed parent children = %+v, want URL and page-backed children", parent.Children)
	}
	support := parent.Children[0]
	if support.Title != "Support" || support.URL != "/support" || support.Target != "_self" ||
		support.CSSClass != "nav-support" || !support.IsActive || support.Position != 2 {
		t.Fatalf("nested URL child = %+v, want preserved hierarchy and fields", support)
	}
	pageChild := parent.Children[1]
	if pageChild.Title != "Child page" || pageChild.PageSlug != "menu-child-page" || pageChild.URL != "" ||
		pageChild.Target != "_blank" || pageChild.CSSClass != "nav-child-page" || pageChild.IsActive ||
		pageChild.Position != 4 {
		t.Fatalf("nested page child = %+v, want preserved slug, hierarchy, and fields", pageChild)
	}
}

func TestMenuOnlyExportResolvesExistingDestinationPage(t *testing.T) {
	data := exportMenuOnlyHierarchy(t)
	destination := setupTest(t)
	t.Cleanup(destination.Cleanup)
	destinationPage, err := destination.Queries.CreatePage(destination.Ctx, store.CreatePageParams{
		Title:        "Destination parent",
		Slug:         "menu-parent-page",
		Status:       "published",
		AuthorID:     destination.User.ID,
		LanguageCode: "en",
		CreatedAt:    destination.Now,
		UpdatedAt:    destination.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	destinationChildPage, err := destination.Queries.CreatePage(destination.Ctx, store.CreatePageParams{
		Title:        "Destination child",
		Slug:         "menu-child-page",
		Status:       "published",
		AuthorID:     destination.User.ID,
		LanguageCode: "en",
		CreatedAt:    destination.Now,
		UpdatedAt:    destination.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	importer := NewImporter(destination.Queries, destination.DB, slog.Default())
	preview, err := importer.ValidateData(destination.Ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Valid {
		t.Fatalf("preview rejected destination-resolvable menu: %+v", preview.Errors)
	}

	opts := ImportOptions{DryRun: true, ConflictStrategy: ConflictSkip, ImportMenus: true}
	dry, err := importer.Import(destination.Ctx, data, opts)
	if err != nil {
		t.Fatalf("dry run rejected destination-resolvable menu: %v (%+v)", err, dry.Errors)
	}
	if dry.Created["menus"] != 1 {
		t.Fatalf("dry-run menu counts = %+v, want one created menu", dry.Created)
	}
	if _, err := destination.Queries.GetMenuBySlugAndLanguage(destination.Ctx,
		store.GetMenuBySlugAndLanguageParams{Slug: "main", LanguageCode: "en"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("dry run wrote a menu: %v", err)
	}

	opts.DryRun = false
	actual, err := importer.Import(destination.Ctx, data, opts)
	if err != nil {
		t.Fatalf("real import rejected destination-resolvable menu: %v (%+v)", err, actual.Errors)
	}
	menu, err := destination.Queries.GetMenuBySlugAndLanguage(destination.Ctx,
		store.GetMenuBySlugAndLanguageParams{Slug: "main", LanguageCode: "en"})
	if err != nil {
		t.Fatal(err)
	}
	topLevel, err := destination.Queries.ListTopLevelMenuItems(destination.Ctx, menu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(topLevel) != 1 || !topLevel[0].PageID.Valid || topLevel[0].PageID.Int64 != destinationPage.ID ||
		!topLevel[0].Target.Valid || topLevel[0].Target.String != "_blank" ||
		!topLevel[0].CssClass.Valid || topLevel[0].CssClass.String != "nav-parent" ||
		topLevel[0].IsActive || topLevel[0].Position != 3 {
		t.Fatalf("imported parent = %+v, want destination page %d", topLevel, destinationPage.ID)
	}
	children, err := destination.Queries.ListChildMenuItems(destination.Ctx,
		sql.NullInt64{Int64: topLevel[0].ID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Fatalf("imported child hierarchy = %+v, want two children", children)
	}
	if !children[0].ParentID.Valid || children[0].ParentID.Int64 != topLevel[0].ID ||
		!children[0].Url.Valid || children[0].Url.String != "/support" ||
		!children[0].Target.Valid || children[0].Target.String != "_self" ||
		!children[0].CssClass.Valid || children[0].CssClass.String != "nav-support" ||
		!children[0].IsActive || children[0].Position != 2 {
		t.Fatalf("imported URL child = %+v, want child of %d", children[0], topLevel[0].ID)
	}
	if !children[1].ParentID.Valid || children[1].ParentID.Int64 != topLevel[0].ID ||
		!children[1].PageID.Valid || children[1].PageID.Int64 != destinationChildPage.ID ||
		children[1].Url.Valid || !children[1].Target.Valid || children[1].Target.String != "_blank" ||
		!children[1].CssClass.Valid || children[1].CssClass.String != "nav-child-page" ||
		children[1].IsActive || children[1].Position != 4 {
		t.Fatalf("imported page child = %+v, want destination page %d and parent %d",
			children[1], destinationChildPage.ID, topLevel[0].ID)
	}
	var pageCount int
	if err := destination.DB.QueryRowContext(destination.Ctx,
		`SELECT COUNT(*) FROM pages WHERE slug IN (?, ?)`, "menu-parent-page", "menu-child-page").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if pageCount != 2 {
		t.Fatalf("destination page count = %d, want the two existing pages only", pageCount)
	}
}

func TestMenuOnlyExportMissingDestinationPageRejectsWithoutWrites(t *testing.T) {
	data := exportMenuOnlyHierarchy(t)
	destination := setupTest(t)
	t.Cleanup(destination.Cleanup)
	importer := NewImporter(destination.Queries, destination.DB, slog.Default())

	preview, err := importer.ValidateData(destination.Ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Valid || !importErrorsContain(preview.Errors, `referenced page "menu-parent-page"`) ||
		!importErrorsContain(preview.Errors, `referenced page "menu-child-page"`) {
		t.Fatalf("preview errors = %+v, want missing destination page", preview.Errors)
	}

	for _, dryRun := range []bool{true, false} {
		result, importErr := importer.Import(destination.Ctx, data, ImportOptions{
			DryRun: dryRun, ConflictStrategy: ConflictSkip, ImportMenus: true,
		})
		if importErr == nil {
			t.Fatalf("dry_run=%t accepted missing destination page: %+v", dryRun, result)
		}
		if !importErrorsContain(result.Errors, `referenced page "menu-parent-page"`) ||
			!importErrorsContain(result.Errors, `referenced page "menu-child-page"`) {
			t.Fatalf("dry_run=%t errors = %+v, want missing destination page", dryRun, result.Errors)
		}
		menuCount, countErr := destination.Queries.CountMenus(destination.Ctx)
		if countErr != nil {
			t.Fatal(countErr)
		}
		if menuCount != 0 {
			t.Fatalf("dry_run=%t wrote %d menus before relation validation", dryRun, menuCount)
		}
	}
}

func TestMenuOnlyExportPropagatesPageLookupFailure(t *testing.T) {
	ts := setupTest(t)
	t.Cleanup(ts.Cleanup)
	if _, err := ts.DB.ExecContext(ts.Ctx, `DROP TABLE pages`); err != nil {
		t.Fatal(err)
	}

	data, err := NewExporter(ts.Queries, slog.Default()).Export(ts.Ctx, ExportOptions{IncludeMenus: true})
	if err == nil || !strings.Contains(err.Error(), "build menu page reference map") {
		t.Fatalf("Export() = (%+v, %v), want page lookup failure", data, err)
	}
}

func TestMenuOnlyExportRejectsUnresolvedPageReference(t *testing.T) {
	item, included, err := NewExporter(nil, slog.Default()).exportMenuItem(
		store.MenuItem{
			ID:     43,
			Title:  "Stale page",
			Url:    sql.NullString{String: "/fallback", Valid: true},
			PageID: sql.NullInt64{Int64: 999, Valid: true},
		}, nil, map[int64]string{}, true)
	if err == nil || !strings.Contains(err.Error(), "menu item 43 references missing page 999") {
		t.Fatalf("exportMenuItem() = (%+v, %t, %v), want unresolved page failure", item, included, err)
	}
}

func TestExportMenusPropagatesListMenuItemsFailure(t *testing.T) {
	ts := setupTest(t)
	t.Cleanup(ts.Cleanup)
	if _, err := ts.Queries.CreateMenu(ts.Ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.DB.ExecContext(ts.Ctx, `DROP TABLE menu_items`); err != nil {
		t.Fatal(err)
	}

	data, err := NewExporter(ts.Queries, slog.Default()).Export(ts.Ctx, ExportOptions{IncludeMenus: true})
	if err == nil || !strings.Contains(err.Error(), `list items for menu "main"`) {
		t.Fatalf("Export() = (%+v, %v), want menu item list failure", data, err)
	}
}

func TestCombinedPageMenuExportPropagatesPageFailure(t *testing.T) {
	ts := setupTest(t)
	t.Cleanup(ts.Cleanup)
	if _, err := ts.DB.ExecContext(ts.Ctx, `DROP TABLE pages`); err != nil {
		t.Fatal(err)
	}

	data, err := NewExporter(ts.Queries, slog.Default()).Export(ts.Ctx, ExportOptions{
		IncludePages: true,
		IncludeMenus: true,
	})
	if err == nil || !strings.Contains(err.Error(), "export pages") {
		t.Fatalf("Export() = (%+v, %v), want requested page export failure", data, err)
	}
}

func TestBuildMenuItemTreeRejectsInvalidGraphs(t *testing.T) {
	const menuID int64 = 7
	tests := []struct {
		name  string
		items []store.MenuItem
		want  string
	}{
		{
			name:  "foreign row",
			items: []store.MenuItem{{ID: 1, MenuID: menuID + 1}},
			want:  "belongs to menu 8, not menu 7",
		},
		{
			name: "missing or foreign parent",
			items: []store.MenuItem{{
				ID: 1, MenuID: menuID, ParentID: sql.NullInt64{Int64: 99, Valid: true},
			}},
			want: "references missing or foreign parent 99",
		},
		{
			name: "disconnected cycle",
			items: []store.MenuItem{
				{ID: 1, MenuID: menuID, ParentID: sql.NullInt64{Int64: 2, Valid: true}},
				{ID: 2, MenuID: menuID, ParentID: sql.NullInt64{Int64: 1, Valid: true}},
			},
			want: "contains a cycle",
		},
		{
			name: "duplicate ID",
			items: []store.MenuItem{
				{ID: 1, MenuID: menuID},
				{ID: 1, MenuID: menuID},
			},
			want: "duplicate menu item ID 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := buildMenuItemTree(menuID, tt.items)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("buildMenuItemTree() = (%+v, %v), want %q", tree, err, tt.want)
			}
		})
	}
}

func TestExportMenusRejectsInvalidStoredHierarchy(t *testing.T) {
	t.Run("foreign parent", func(t *testing.T) {
		ts := setupTest(t)
		t.Cleanup(ts.Cleanup)
		childMenu, err := ts.Queries.CreateMenu(ts.Ctx, store.CreateMenuParams{
			Name: "A child menu", Slug: "child", LanguageCode: "en",
			CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		parentMenu, err := ts.Queries.CreateMenu(ts.Ctx, store.CreateMenuParams{
			Name: "B parent menu", Slug: "parent", LanguageCode: "en",
			CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		parent, err := ts.Queries.CreateMenuItem(ts.Ctx, store.CreateMenuItemParams{
			MenuID: parentMenu.ID, Title: "Foreign parent", IsActive: true,
			CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		child, err := ts.Queries.CreateMenuItem(ts.Ctx, store.CreateMenuItemParams{
			MenuID: childMenu.ID, ParentID: sql.NullInt64{Int64: parent.ID, Valid: true},
			Title: "Child", IsActive: true, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}

		data, exportErr := NewExporter(ts.Queries, slog.Default()).Export(
			ts.Ctx, ExportOptions{IncludeMenus: true})
		want := fmt.Sprintf("menu item %d references missing or foreign parent %d", child.ID, parent.ID)
		if exportErr == nil || !strings.Contains(exportErr.Error(), want) {
			t.Fatalf("Export() = (%+v, %v), want %q", data, exportErr, want)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		ts := setupTest(t)
		t.Cleanup(ts.Cleanup)
		menu, err := ts.Queries.CreateMenu(ts.Ctx, store.CreateMenuParams{
			Name: "Main", Slug: "main", LanguageCode: "en", CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		first, err := ts.Queries.CreateMenuItem(ts.Ctx, store.CreateMenuItemParams{
			MenuID: menu.ID, Title: "First", IsActive: true, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		second, err := ts.Queries.CreateMenuItem(ts.Ctx, store.CreateMenuItemParams{
			MenuID: menu.ID, ParentID: sql.NullInt64{Int64: first.ID, Valid: true},
			Title: "Second", IsActive: true, CreatedAt: ts.Now, UpdatedAt: ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ts.Queries.UpdateMenuItem(ts.Ctx, store.UpdateMenuItemParams{
			ID: first.ID, ParentID: sql.NullInt64{Int64: second.ID, Valid: true},
			Title: first.Title, Url: first.Url, Target: first.Target, PageID: first.PageID,
			Position: first.Position, CssClass: first.CssClass, IsActive: first.IsActive, UpdatedAt: ts.Now,
		}); err != nil {
			t.Fatal(err)
		}

		data, exportErr := NewExporter(ts.Queries, slog.Default()).Export(
			ts.Ctx, ExportOptions{IncludeMenus: true})
		if exportErr == nil || !strings.Contains(exportErr.Error(), "contains a cycle") {
			t.Fatalf("Export() = (%+v, %v), want cycle rejection", data, exportErr)
		}
	})
}

func TestBuildMenuItemTreeSortsSiblingsByPositionThenID(t *testing.T) {
	const menuID int64 = 7
	tree, err := buildMenuItemTree(menuID, []store.MenuItem{
		{ID: 5, MenuID: menuID, Position: 2},
		{ID: 3, MenuID: menuID, Position: 1},
		{ID: 9, MenuID: menuID, ParentID: sql.NullInt64{Int64: 2, Valid: true}, Position: 4},
		{ID: 2, MenuID: menuID, Position: 1},
		{ID: 8, MenuID: menuID, ParentID: sql.NullInt64{Int64: 2, Valid: true}, Position: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := menuItemIDs(tree.roots); fmt.Sprint(got) != "[2 3 5]" {
		t.Fatalf("root order = %v, want [2 3 5]", got)
	}
	if got := menuItemIDs(tree.children[2]); fmt.Sprint(got) != "[8 9]" {
		t.Fatalf("child order = %v, want [8 9]", got)
	}
}

func TestPageLookupMapsPaginateWithStableTimestampTie(t *testing.T) {
	ts := setupTest(t)
	t.Cleanup(ts.Cleanup)
	const pageCount = pageLookupBatchSize + 1
	created := make([]store.Page, 0, pageCount)
	for index := int64(0); index < pageCount; index++ {
		page, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
			Title:        fmt.Sprintf("Page %d", index),
			Slug:         fmt.Sprintf("page-%d", index),
			Status:       "published",
			AuthorID:     ts.User.ID,
			LanguageCode: "en",
			CreatedAt:    ts.Now,
			UpdatedAt:    ts.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, page)
	}

	pages, err := listAllPagesForLookup(ts.Ctx, ts.Queries)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != int(pageCount) {
		t.Fatalf("lookup page count = %d, want %d", len(pages), pageCount)
	}
	for index, page := range pages {
		wantID := created[len(created)-1-index].ID
		if page.ID != wantID {
			t.Fatalf("lookup page %d ID = %d, want stable ID-desc tie-break %d", index, page.ID, wantID)
		}
	}

	exportMap, err := NewExporter(ts.Queries, slog.Default()).buildIDLookupMap(ts.Ctx, exportPage)
	if err != nil {
		t.Fatal(err)
	}
	importMap, err := NewImporter(ts.Queries, ts.DB, slog.Default()).buildLookupMap(
		ts.Ctx, ts.Queries, entityPage)
	if err != nil {
		t.Fatal(err)
	}
	if len(exportMap) != int(pageCount) || len(importMap) != int(pageCount) {
		t.Fatalf("lookup map sizes = export %d, import %d; want %d each",
			len(exportMap), len(importMap), pageCount)
	}
	for _, page := range created {
		if exportMap[page.ID] != page.Slug || importMap[page.Slug] != page.ID {
			t.Fatalf("page %d/%q missing from lookup maps", page.ID, page.Slug)
		}
	}
}

func menuItemIDs(items []store.MenuItem) []int64 {
	ids := make([]int64, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

func exportMenuOnlyHierarchy(t *testing.T) *ExportData {
	t.Helper()
	source := setupTest(t)
	t.Cleanup(source.Cleanup)
	page, err := source.Queries.CreatePage(source.Ctx, store.CreatePageParams{
		Title:        "Menu parent page",
		Slug:         "menu-parent-page",
		Status:       "published",
		AuthorID:     source.User.ID,
		LanguageCode: "en",
		CreatedAt:    source.Now,
		UpdatedAt:    source.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	childPage, err := source.Queries.CreatePage(source.Ctx, store.CreatePageParams{
		Title:        "Menu child page",
		Slug:         "menu-child-page",
		Status:       "published",
		AuthorID:     source.User.ID,
		LanguageCode: "en",
		CreatedAt:    source.Now,
		UpdatedAt:    source.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	menu, err := source.Queries.CreateMenu(source.Ctx, store.CreateMenuParams{
		Name: "Main", Slug: "main", LanguageCode: "en", CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := source.Queries.CreateMenuItem(source.Ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, Title: "Parent", PageID: sql.NullInt64{Int64: page.ID, Valid: true},
		Target:   sql.NullString{String: "_blank", Valid: true},
		CssClass: sql.NullString{String: "nav-parent", Valid: true},
		Position: 3, IsActive: false, CreatedAt: source.Now, UpdatedAt: source.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Queries.CreateMenuItem(source.Ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, ParentID: sql.NullInt64{Int64: parent.ID, Valid: true}, Title: "Support",
		Url:      sql.NullString{String: "/support", Valid: true},
		Target:   sql.NullString{String: "_self", Valid: true},
		CssClass: sql.NullString{String: "nav-support", Valid: true},
		Position: 2, IsActive: true,
		CreatedAt: source.Now, UpdatedAt: source.Now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Queries.CreateMenuItem(source.Ctx, store.CreateMenuItemParams{
		MenuID: menu.ID, ParentID: sql.NullInt64{Int64: parent.ID, Valid: true}, Title: "Child page",
		PageID:   sql.NullInt64{Int64: childPage.ID, Valid: true},
		Target:   sql.NullString{String: "_blank", Valid: true},
		CssClass: sql.NullString{String: "nav-child-page", Valid: true},
		Position: 4, IsActive: false, CreatedAt: source.Now, UpdatedAt: source.Now,
	}); err != nil {
		t.Fatal(err)
	}

	data, err := NewExporter(source.Queries, slog.Default()).Export(source.Ctx, ExportOptions{IncludeMenus: true})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func importErrorsContain(errs []ImportError, text string) bool {
	for _, err := range errs {
		if strings.Contains(err.Message, text) {
			return true
		}
	}
	return false
}

func TestExportOptionsFiltering(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()

	lang, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create pages with different statuses
	_, err = ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title:        "Published Page",
		Slug:         "published-page",
		Body:         "Published content",
		Status:       "published",
		AuthorID:     ts.User.ID,
		LanguageCode: lang.Code,
		CreatedAt:    ts.Now,
		UpdatedAt:    ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create published page: %v", err)
	}

	_, err = ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title:        "Draft Page",
		Slug:         "draft-page",
		Body:         "Draft content",
		Status:       "draft",
		AuthorID:     ts.User.ID,
		LanguageCode: lang.Code,
		CreatedAt:    ts.Now,
		UpdatedAt:    ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create draft page: %v", err)
	}

	logger := slog.Default()
	exporter := NewExporter(ts.Queries, logger)

	pageStatusTests := []struct {
		name      string
		status    string
		wantCount int
		wantSlug  string
	}{
		{"ExportOnlyPublished", "published", 1, "published-page"},
		{"ExportOnlyDraft", "draft", 1, "draft-page"},
		{"ExportAllPages", "all", 2, ""},
	}

	for _, tt := range pageStatusTests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ExportOptions{
				IncludePages: true,
				PageStatus:   tt.status,
			}

			data, err := exporter.Export(ts.Ctx, opts)
			if err != nil {
				t.Fatalf("Export failed: %v", err)
			}

			if len(data.Pages) != tt.wantCount {
				t.Errorf("Expected %d pages, got %d", tt.wantCount, len(data.Pages))
			}
			if tt.wantSlug != "" && len(data.Pages) > 0 && data.Pages[0].Slug != tt.wantSlug {
				t.Errorf("Expected '%s', got '%s'", tt.wantSlug, data.Pages[0].Slug)
			}
		})
	}

	t.Run("ExportWithoutPages", func(t *testing.T) {
		opts := ExportOptions{
			IncludePages: false,
			IncludeUsers: true,
		}

		data, err := exporter.Export(ts.Ctx, opts)
		if err != nil {
			t.Fatalf("Export failed: %v", err)
		}

		if len(data.Pages) != 0 {
			t.Errorf("Expected 0 pages, got %d", len(data.Pages))
		}
		if len(data.Users) != 1 {
			t.Errorf("Expected 1 user, got %d", len(data.Users))
		}
	})
}

func TestDefaultExportOptions(t *testing.T) {
	opts := DefaultExportOptions()

	if !opts.IncludeUsers {
		t.Error("IncludeUsers should be true by default")
	}
	if !opts.IncludePages {
		t.Error("IncludePages should be true by default")
	}
	if !opts.IncludeCategories {
		t.Error("IncludeCategories should be true by default")
	}
	if !opts.IncludeTags {
		t.Error("IncludeTags should be true by default")
	}
	if !opts.IncludeMedia {
		t.Error("IncludeMedia should be true by default")
	}
	if !opts.IncludeMenus {
		t.Error("IncludeMenus should be true by default")
	}
	if !opts.IncludeForms {
		t.Error("IncludeForms should be true by default")
	}
	if opts.IncludeSubmissions {
		t.Error("IncludeSubmissions should be false by default")
	}
	if !opts.IncludeConfig {
		t.Error("IncludeConfig should be true by default")
	}
	if !opts.IncludeLanguages {
		t.Error("IncludeLanguages should be true by default")
	}
	if opts.PageStatus != "all" {
		t.Errorf("PageStatus should be 'all' by default, got '%s'", opts.PageStatus)
	}
}

func TestExportCategoryHierarchy(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create parent category
	parent, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:         "Parent Category",
		Slug:         "parent-category",
		LanguageCode: lang.Code,
		Position:     0,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create parent category: %v", err)
	}

	// Create child category
	_, err = queries.CreateCategory(ctx, store.CreateCategoryParams{
		Name:         "Child Category",
		Slug:         "child-category",
		LanguageCode: lang.Code,
		ParentID:     sql.NullInt64{Int64: parent.ID, Valid: true},
		Position:     0,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create child category: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := ExportOptions{IncludeCategories: true}

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify hierarchy is exported
	if len(data.Categories) != 2 {
		t.Errorf("Expected 2 categories, got %d", len(data.Categories))
	}

	// Find child category and verify parent slug is set
	var childCat *ExportCategory
	for i := range data.Categories {
		if data.Categories[i].Slug == "child-category" {
			childCat = &data.Categories[i]
			break
		}
	}

	if childCat == nil {
		t.Fatal("Child category not found in export")
	}

	if childCat.ParentSlug != "parent-category" {
		t.Errorf("Expected parent slug 'parent-category', got '%s'", childCat.ParentSlug)
	}
}

func TestExportMenuWithItems(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create menu
	menu, err := queries.CreateMenu(ctx, store.CreateMenuParams{
		Name:         "Test Menu",
		Slug:         "test-menu",
		LanguageCode: lang.Code,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create menu: %v", err)
	}

	// Create menu items
	_, err = queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "Home",
		Url:       sql.NullString{String: "/", Valid: true},
		Position:  0,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}

	_, err = queries.CreateMenuItem(ctx, store.CreateMenuItemParams{
		MenuID:    menu.ID,
		Title:     "About",
		Url:       sql.NullString{String: "/about", Valid: true},
		Position:  1,
		IsActive:  true,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create menu item: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := ExportOptions{IncludeMenus: true}

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify menu is exported
	if len(data.Menus) != 1 {
		t.Errorf("Expected 1 menu, got %d", len(data.Menus))
	}

	if len(data.Menus) > 0 {
		if data.Menus[0].Slug != "test-menu" {
			t.Errorf("Expected menu slug 'test-menu', got '%s'", data.Menus[0].Slug)
		}
		if len(data.Menus[0].Items) != 2 {
			t.Errorf("Expected 2 menu items, got %d", len(data.Menus[0].Items))
		}
	}
}

func TestExportFormWithFields(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	queries := store.New(db)
	ctx := context.Background()
	now := time.Now()

	// Get default language
	lang, err := queries.GetDefaultLanguage(ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create form
	form, err := queries.CreateForm(ctx, store.CreateFormParams{
		Name:         "Contact Form",
		Slug:         "contact",
		Title:        "Contact Us",
		IsActive:     true,
		LanguageCode: lang.Code,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create form: %v", err)
	}

	// Create form fields
	_, err = queries.CreateFormField(ctx, store.CreateFormFieldParams{
		FormID:       form.ID,
		Type:         "text",
		Name:         "name",
		Label:        "Your Name",
		IsRequired:   true,
		Position:     0,
		LanguageCode: lang.Code,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create form field: %v", err)
	}

	_, err = queries.CreateFormField(ctx, store.CreateFormFieldParams{
		FormID:       form.ID,
		Type:         "email",
		Name:         "email",
		Label:        "Your Email",
		IsRequired:   true,
		Position:     1,
		LanguageCode: lang.Code,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to create form field: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(queries, logger)
	opts := ExportOptions{IncludeForms: true}

	data, err := exporter.Export(ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify form is exported
	if len(data.Forms) != 1 {
		t.Errorf("Expected 1 form, got %d", len(data.Forms))
	}

	if len(data.Forms) > 0 {
		if data.Forms[0].Slug != "contact" {
			t.Errorf("Expected form slug 'contact', got '%s'", data.Forms[0].Slug)
		}
		if len(data.Forms[0].Fields) != 2 {
			t.Errorf("Expected 2 form fields, got %d", len(data.Forms[0].Fields))
		}
	}
}

func TestExportPageWithTranslations(t *testing.T) {
	ts := setupTest(t)
	defer ts.Cleanup()

	// Get default language (English)
	enLang, err := ts.Queries.GetDefaultLanguage(ts.Ctx)
	if err != nil {
		t.Fatalf("failed to get default language: %v", err)
	}

	// Create another language (Russian)
	ruLang, err := ts.Queries.CreateLanguage(ts.Ctx, store.CreateLanguageParams{
		Code:       "ru",
		Name:       "Russian",
		NativeName: "Русский",
		IsDefault:  false,
		IsActive:   true,
		Direction:  "ltr",
		Position:   1,
		CreatedAt:  ts.Now,
		UpdatedAt:  ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create language: %v", err)
	}

	// Create English page
	enPage, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title:        "Hello World",
		Slug:         "hello-world",
		Body:         "English content",
		Status:       "published",
		AuthorID:     ts.User.ID,
		LanguageCode: enLang.Code,
		CreatedAt:    ts.Now,
		UpdatedAt:    ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create English page: %v", err)
	}

	// Create Russian page
	ruPage, err := ts.Queries.CreatePage(ts.Ctx, store.CreatePageParams{
		Title:        "Привет мир",
		Slug:         "privet-mir",
		Body:         "Russian content",
		Status:       "published",
		AuthorID:     ts.User.ID,
		LanguageCode: ruLang.Code,
		CreatedAt:    ts.Now,
		UpdatedAt:    ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create Russian page: %v", err)
	}

	// Create translation link
	_, err = ts.Queries.CreateTranslation(ts.Ctx, store.CreateTranslationParams{
		EntityType:    "page",
		EntityID:      enPage.ID,
		LanguageID:    ruLang.ID,
		TranslationID: ruPage.ID,
		CreatedAt:     ts.Now,
	})
	if err != nil {
		t.Fatalf("failed to create translation: %v", err)
	}

	// Export
	logger := slog.Default()
	exporter := NewExporter(ts.Queries, logger)
	opts := DefaultExportOptions()

	data, err := exporter.Export(ts.Ctx, opts)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify pages are exported
	if len(data.Pages) != 2 {
		t.Errorf("Expected 2 pages, got %d", len(data.Pages))
	}

	// Find English page and verify translations
	var enExportPage *ExportPage
	for i := range data.Pages {
		if data.Pages[i].Slug == "hello-world" {
			enExportPage = &data.Pages[i]
			break
		}
	}

	if enExportPage == nil {
		t.Fatal("English page not found in export")
	}

	if enExportPage.LanguageCode != "en" {
		t.Errorf("Expected language code 'en', got '%s'", enExportPage.LanguageCode)
	}

	if len(enExportPage.Translations) != 1 {
		t.Errorf("Expected 1 translation, got %d", len(enExportPage.Translations))
	}

	if enExportPage.Translations["ru"] != ruPage.ID {
		t.Errorf("Expected translation to Russian page ID %d, got %d", ruPage.ID, enExportPage.Translations["ru"])
	}
}
