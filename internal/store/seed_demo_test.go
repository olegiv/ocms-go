// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"image"
	"os"
	"testing"
)

func TestSeedDemo(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	// First seed base data (needed for demo seeding)
	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Enable demo mode
	t.Setenv("OCMS_DEMO_MODE", "true")

	// Use temp dir for uploads to avoid polluting the workspace
	uploadsDir := t.TempDir()
	t.Setenv("OCMS_UPLOADS_DIR", uploadsDir)

	if err := SeedDemo(ctx, db); err != nil {
		t.Fatalf("SeedDemo: %v", err)
	}

	// Verify demo admin user was created
	admin, err := q.GetUserByEmail(ctx, DemoAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DemoAdminEmail, err)
	}
	if admin.Role != "admin" {
		t.Errorf("admin.Role = %q, want %q", admin.Role, "admin")
	}
	if admin.Name != DemoAdminName {
		t.Errorf("admin.Name = %q, want %q", admin.Name, DemoAdminName)
	}

	// Verify demo editor user was created
	editor, err := q.GetUserByEmail(ctx, DemoEditorEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DemoEditorEmail, err)
	}
	if editor.Role != "editor" {
		t.Errorf("editor.Role = %q, want %q", editor.Role, "editor")
	}

	// Verify categories were created
	catCount, err := q.CountCategories(ctx)
	if err != nil {
		t.Fatalf("CountCategories: %v", err)
	}
	if catCount < 4 {
		t.Errorf("category count = %d, want >= 4", catCount)
	}

	// Verify tags were created
	tagCount, err := q.CountTags(ctx)
	if err != nil {
		t.Fatalf("CountTags: %v", err)
	}
	if tagCount < 7 {
		t.Errorf("tag count = %d, want >= 7", tagCount)
	}

	// Verify pages were created
	pageCount, err := q.CountPages(ctx)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}
	if pageCount < 9 {
		t.Errorf("page count = %d, want >= 9", pageCount)
	}

	// Verify media was created
	mediaCount, err := q.CountMedia(ctx)
	if err != nil {
		t.Fatalf("CountMedia: %v", err)
	}
	if mediaCount < 10 {
		t.Errorf("media count = %d, want >= 10", mediaCount)
	}

	// Verify menu items were created (main menu should have items now)
	mainMenu, err := q.GetMenuBySlug(ctx, "main")
	if err != nil {
		t.Fatalf("GetMenuBySlug(main): %v", err)
	}
	items, err := q.ListMenuItems(ctx, mainMenu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) < 6 {
		t.Errorf("menu items count = %d, want >= 6", len(items))
	}
}

func TestSeedDemo_Disabled(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	// Seed base data
	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Demo mode not set (default)
	os.Unsetenv("OCMS_DEMO_MODE")

	if err := SeedDemo(ctx, db); err != nil {
		t.Fatalf("SeedDemo: %v", err)
	}

	// Verify no demo users were created
	_, err := q.GetUserByEmail(ctx, DemoAdminEmail)
	if err == nil {
		t.Error("expected no demo admin user when OCMS_DEMO_MODE is not set")
	}

	// Only the base seed admin user should exist
	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 1 {
		t.Errorf("user count = %d, want 1 (only base admin)", count)
	}
}

func TestSeedDemo_Idempotent(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	// Seed base data
	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	t.Setenv("OCMS_DEMO_MODE", "true")
	uploadsDir := t.TempDir()
	t.Setenv("OCMS_UPLOADS_DIR", uploadsDir)

	// First seed
	if err := SeedDemo(ctx, db); err != nil {
		t.Fatalf("SeedDemo (first): %v", err)
	}

	pageCount1, err := q.CountPages(ctx)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}

	userCount1, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}

	// Second seed should not create duplicates
	if err := SeedDemo(ctx, db); err != nil {
		t.Fatalf("SeedDemo (second): %v", err)
	}

	pageCount2, err := q.CountPages(ctx)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}
	if pageCount2 != pageCount1 {
		t.Errorf("page count changed from %d to %d after second seed", pageCount1, pageCount2)
	}

	userCount2, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if userCount2 != userCount1 {
		t.Errorf("user count changed from %d to %d after second seed", userCount1, userCount2)
	}
}

func TestGetDemoPages(t *testing.T) {
	pages := getDemoPages()

	if len(pages) == 0 {
		t.Fatal("getDemoPages() returned empty slice")
	}

	// Verify all pages have required fields
	slugs := make(map[string]bool)
	for _, p := range pages {
		if p.Title == "" {
			t.Errorf("page with slug %q has empty title", p.Slug)
		}
		if p.Slug == "" {
			t.Error("page has empty slug")
		}
		if p.Body == "" {
			t.Errorf("page %q has empty body", p.Slug)
		}
		if p.Status == "" {
			t.Errorf("page %q has empty status", p.Slug)
		}
		if p.PageType == "" {
			t.Errorf("page %q has empty page type", p.Slug)
		}

		// Check for duplicate slugs
		if slugs[p.Slug] {
			t.Errorf("duplicate slug: %q", p.Slug)
		}
		slugs[p.Slug] = true
	}

	// Verify expected page types exist
	hasPage := false
	hasPost := false
	for _, p := range pages {
		switch p.PageType {
		case "page":
			hasPage = true
		case "post":
			hasPost = true
		}
	}
	if !hasPage {
		t.Error("no pages with page_type 'page' found")
	}
	if !hasPost {
		t.Error("no pages with page_type 'post' found")
	}
}

func TestSeedDemoUsers(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	adminID, err := seedDemoUsers(ctx, q)
	if err != nil {
		t.Fatalf("seedDemoUsers: %v", err)
	}
	if adminID == 0 {
		t.Error("adminID should not be 0")
	}

	// Verify admin
	admin, err := q.GetUserByEmail(ctx, DemoAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DemoAdminEmail, err)
	}
	if admin.Role != "admin" {
		t.Errorf("admin role = %q, want %q", admin.Role, "admin")
	}

	// Verify editor
	editor, err := q.GetUserByEmail(ctx, DemoEditorEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DemoEditorEmail, err)
	}
	if editor.Role != "editor" {
		t.Errorf("editor role = %q, want %q", editor.Role, "editor")
	}

	// Running again should return existing admin ID
	adminID2, err := seedDemoUsers(ctx, q)
	if err != nil {
		t.Fatalf("seedDemoUsers (second): %v", err)
	}
	if adminID2 != adminID {
		t.Errorf("second call returned adminID=%d, want %d", adminID2, adminID)
	}
}

func TestSeedDemoCategories(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	langCode := getDefaultLangCode(t, q, ctx)

	ids, err := seedDemoCategories(ctx, q, langCode)
	if err != nil {
		t.Fatalf("seedDemoCategories: %v", err)
	}

	if len(ids) != 4 {
		t.Errorf("category count = %d, want 4", len(ids))
	}

	// Verify expected categories
	for _, slug := range []string{"blog", "portfolio", "services", "resources"} {
		if _, ok := ids[slug]; !ok {
			t.Errorf("missing expected category %q", slug)
		}
	}
}

func TestSeedDemoTags(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	langCode := getDefaultLangCode(t, q, ctx)

	ids, err := seedDemoTags(ctx, q, langCode)
	if err != nil {
		t.Fatalf("seedDemoTags: %v", err)
	}

	if len(ids) != 7 {
		t.Errorf("tag count = %d, want 7", len(ids))
	}

	// Verify expected tags
	for _, slug := range []string{"tutorial", "news", "featured", "go", "web-development", "design", "open-source"} {
		if _, ok := ids[slug]; !ok {
			t.Errorf("missing expected tag %q", slug)
		}
	}
}

func TestSeedDemoMedia(t *testing.T) {
	_, cleanup, ctx, q := testSetup(t)
	defer cleanup()

	user := createTestUser(t, q, ctx, "media@example.com")
	langCode := getDefaultLangCode(t, q, ctx)
	uploadsDir := t.TempDir()

	ids, err := seedDemoMedia(ctx, q, user.ID, langCode, uploadsDir)
	if err != nil {
		t.Fatalf("seedDemoMedia: %v", err)
	}

	if len(ids) < 10 {
		t.Errorf("media IDs count = %d, want >= 10", len(ids))
	}

	// Verify media records exist in database
	mediaCount, err := q.CountMedia(ctx)
	if err != nil {
		t.Fatalf("CountMedia: %v", err)
	}
	if mediaCount < 10 {
		t.Errorf("media count = %d, want >= 10", mediaCount)
	}
}

func TestSeedDemoMenuItems(t *testing.T) {
	db, cleanup, ctx, _ := testSetup(t)
	defer cleanup()

	// Need base menus first
	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	q := New(db)
	if err := seedDemoMenuItems(ctx, q); err != nil {
		t.Fatalf("seedDemoMenuItems: %v", err)
	}

	mainMenu, err := q.GetMenuBySlug(ctx, "main")
	if err != nil {
		t.Fatalf("GetMenuBySlug(main): %v", err)
	}

	items, err := q.ListMenuItems(ctx, mainMenu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items) != 7 {
		t.Errorf("menu items = %d, want 7", len(items))
	}

	// Running again should skip
	if err := seedDemoMenuItems(ctx, q); err != nil {
		t.Fatalf("seedDemoMenuItems (second): %v", err)
	}

	items2, err := q.ListMenuItems(ctx, mainMenu.ID)
	if err != nil {
		t.Fatalf("ListMenuItems: %v", err)
	}
	if len(items2) != len(items) {
		t.Errorf("second call changed menu items from %d to %d", len(items), len(items2))
	}
}

func TestEncodePNG(t *testing.T) {
	// Create a simple test image
	img := createTestImageRGBA(100, 100)

	data, err := encodePNG(img)
	if err != nil {
		t.Fatalf("encodePNG: %v", err)
	}

	if len(data) == 0 {
		t.Error("encodePNG returned empty data")
	}

	// Verify PNG magic bytes
	if len(data) < 8 {
		t.Fatalf("data too short: %d bytes", len(data))
	}
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	for i, b := range pngMagic {
		if data[i] != b {
			t.Errorf("byte[%d] = 0x%02X, want 0x%02X", i, data[i], b)
		}
	}
}

func TestResizeImage(t *testing.T) {
	src := createTestImageRGBA(800, 600)

	tests := []struct {
		name       string
		width      int
		height     int
		crop       bool
		wantWidth  int
		wantHeight int
	}{
		{"fit within bounds", 400, 300, false, 400, 300},
		{"crop to exact size", 150, 150, true, 150, 150},
		{"fit preserving aspect ratio", 200, 200, false, 200, 150},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, w, h, err := resizeImage(src, tt.width, tt.height, tt.crop)
			if err != nil {
				t.Fatalf("resizeImage: %v", err)
			}
			if len(data) == 0 {
				t.Error("resizeImage returned empty data")
			}
			if w != tt.wantWidth {
				t.Errorf("width = %d, want %d", w, tt.wantWidth)
			}
			if h != tt.wantHeight {
				t.Errorf("height = %d, want %d", h, tt.wantHeight)
			}
		})
	}
}

// createTestImageRGBA creates a simple RGBA test image.
func createTestImageRGBA(width, height int) *image.RGBA {
	rect := image.Rect(0, 0, width, height)
	return image.NewRGBA(rect)
}
