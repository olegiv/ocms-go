// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"database/sql"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/demo"
	"github.com/olegiv/ocms-go/internal/model"
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
	pages, err := q.ListPages(ctx, ListPagesParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		version, versionErr := q.GetPageVersionByRevision(ctx, GetPageVersionByRevisionParams{PageID: page.ID, Revision: sql.NullInt64{Int64: 1, Valid: true}})
		if versionErr != nil {
			t.Fatal(versionErr)
		}
		snapshot, snapshotErr := q.GetPageSnapshot(ctx, page.ID)
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if page.Revision != 1 || version.Legacy || version.SnapshotJson.String != snapshot {
			t.Fatalf("demo page %d lacks a complete initial revision", page.ID)
		}
		if _, err = q.GetContentProvenance(ctx, GetContentProvenanceParams{PageID: page.ID, Revision: 1}); err != nil {
			t.Fatal(err)
		}
	}

	// Verify media was created
	mediaCount, err := q.CountMedia(ctx)
	if err != nil {
		t.Fatalf("CountMedia: %v", err)
	}
	if mediaCount < 10 {
		t.Errorf("media count = %d, want >= 10", mediaCount)
	}

	assertDemoEditorialFixtures(t, q, uploadsDir)

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

func assertDemoEditorialFixtures(t *testing.T, q *Queries, uploadsDir string) {
	t.Helper()
	ctx := t.Context()
	drafts, err := q.ListPagesByStatus(ctx, ListPagesByStatusParams{Status: "draft", Limit: 100})
	if err != nil || len(drafts) != 1 || drafts[0].Slug != "planning-the-next-issue" ||
		drafts[0].Title == "" || drafts[0].Body == "" || drafts[0].PublishedAt.Valid || drafts[0].ScheduledAt.Valid {
		t.Fatalf("invalid seeded drafts: %v, %v", drafts, err)
	}
	if _, err := q.GetPublishedPageBySlug(ctx, drafts[0].Slug); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("public draft lookup: %v", err)
	}
	published, err := q.ListPublishedPages(ctx, ListPublishedPagesParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	feed, err := q.ListPublishedPosts(ctx, ListPublishedPostsParams{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range append(published, feed...) {
		if page.ID == drafts[0].ID {
			t.Fatal("draft leaked into public listings")
		}
	}
	form, err := q.GetFormBySlug(ctx, DemoContactFormSlug)
	if err != nil || !IsDemoContactForm(form) || !form.IsActive || form.EmailTo.Valid {
		t.Fatalf("invalid demo contact preview: %v, %v", form, err)
	}
	fields, err := q.GetFormFields(ctx, form.ID)
	if err != nil || len(fields) != 3 {
		t.Fatalf("got %d fields: %v", len(fields), err)
	}
	for i, kind := range []string{"text", "email", "textarea"} {
		field := fields[i]
		if field.Type != kind || field.IsRequired != (i != 0) || field.Position != int64(i+1) || !field.Validation.Valid {
			t.Errorf("invalid preview field %d: %+v", i, field)
		}
	}
	contact, err := q.GetPublishedPageBySlug(ctx, "contact")
	if err != nil || !strings.Contains(contact.Body, `href="/forms/contact"`) {
		t.Fatalf("contact page does not link to preview: %v", err)
	}
	media, err := q.ListMedia(ctx, ListMediaParams{Limit: 100})
	if err != nil || len(media) != len(demoPhotoLibrary) {
		t.Fatalf("media count %d, want %d: %v", len(media), len(demoPhotoLibrary), err)
	}
	article, err := q.GetPublishedPageBySlug(ctx, DemoArticleSlug)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range media {
		if item.MimeType != model.MimeTypeJPEG || item.Alt.String == "" || item.Caption.String == "" {
			t.Fatalf("missing photo metadata: %+v", item)
		}
		if item.ID == article.FeaturedImageID.Int64 && item.Filename != "planning-workshop.jpg" {
			t.Fatalf("guide has unexpected featured photo %q", item.Filename)
		}
		variants, err := q.GetMediaVariants(ctx, item.ID)
		if err != nil || len(variants) != len(model.ImageVariants) {
			t.Fatalf("variants for %s: %d, %v", item.Filename, len(variants), err)
		}
		for _, v := range append(variants, MediaVariant{Type: model.OriginalsDir, Width: item.Width.Int64, Height: item.Height.Int64, Size: item.Size}) {
			b, err := os.ReadFile(filepath.Join(uploadsDir, v.Type, item.Uuid, item.Filename))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
			if err != nil || int64(len(b)) != v.Size || int64(cfg.Width) != v.Width || int64(cfg.Height) != v.Height {
				t.Fatalf("invalid %s/%s: got %dx%d and %d bytes: %v", v.Type, item.Filename, cfg.Width, cfg.Height, len(b), err)
			}
		}
	}
	if !article.FeaturedImageID.Valid {
		t.Fatal("guide has no featured photo")
	}
}

func TestSeedDemoSnapshotFailureRollsBackDatabase(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	t.Cleanup(cleanup)
	if err := Seed(ctx, db, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCMS_DEMO_MODE", "true")
	t.Setenv("OCMS_UPLOADS_DIR", t.TempDir())
	if _, err := db.Exec(`CREATE TRIGGER fail_demo_snapshot BEFORE INSERT ON page_versions
		BEGIN SELECT RAISE(FAIL, 'injected demo snapshot failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SeedDemo(ctx, db); err == nil {
		t.Fatal("injected snapshot failure succeeded")
	}
	count, err := q.CountPages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var records int
	if err = db.QueryRow(`SELECT (SELECT COUNT(*) FROM page_versions) + (SELECT COUNT(*) FROM content_provenance)`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if count != 0 || records != 0 {
		t.Fatalf("demo database partially committed: pages=%d history/provenance=%d", count, records)
	}
}

func TestSeedDemoInformerSettingsRefreshesLegacyNotice(t *testing.T) {
	db, cleanup := testDB(t)
	t.Cleanup(cleanup)
	// The informer module owns this table and creates its singleton before demo seeding.
	if _, err := db.Exec(`CREATE TABLE informer_settings (
		id INTEGER PRIMARY KEY, enabled INTEGER, text TEXT, bg_color TEXT,
		text_color TEXT, version INTEGER, updated_at DATETIME
	)`); err != nil {
		t.Fatal(err)
	}
	const legacyNotice = `This is a demo instance. Admin panel: <a href="/admin/">/admin/</a> &mdash; Login: <strong>demo@example.com</strong> / <strong class="ocms-demo-pw">&hellip;</strong>`
	const customNotice = "Registration opens on Monday."
	for _, tc := range []struct {
		mode, initial string
	}{
		{"true", legacyNotice},
		{"false", customNotice},
	} {
		t.Run("demo="+tc.mode, func(t *testing.T) {
			t.Setenv("OCMS_DEMO_MODE", tc.mode)
			if _, err := db.Exec(`INSERT OR REPLACE INTO informer_settings (id, enabled, text, version)
				VALUES (1, 1, ?, 7)`, tc.initial); err != nil {
				t.Fatal(err)
			}
			if err := SeedDemoInformerSettings(db); err != nil {
				t.Fatal(err)
			}
			var notice string
			var version int
			if err := db.QueryRow("SELECT text, version FROM informer_settings WHERE id = 1").Scan(&notice, &version); err != nil {
				t.Fatal(err)
			}
			if tc.mode == "false" {
				if notice != customNotice || version != 7 {
					t.Errorf("ordinary startup changed the custom notice: %q (version %d)", notice, version)
				}
				return
			}
			if version <= 7 || !strings.Contains(notice, `href="/admin/"`) || !strings.Contains(notice, ">Open the back office</a>") {
				t.Errorf("demo startup did not replace the old notice with the back-office link: %q (version %d)", notice, version)
			}
			for _, obsolete := range []string{"login", DemoAdminEmail, "ocms-demo-pw"} {
				if strings.Contains(strings.ToLower(notice), obsolete) {
					t.Errorf("refreshed demo notice still contains %q", obsolete)
				}
			}
		})
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
	t.Setenv("OCMS_DEMO_MODE", "")

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
	assertDemoEditorialFixtures(t, q, uploadsDir)
}

func TestSeedDemoUpgradesLegacyFixturesAndRestoresAfterReset(t *testing.T) {
	dir := t.TempDir()
	path, uploads := filepath.Join(dir, "ocms.db"), filepath.Join(dir, "uploads")
	t.Setenv("OCMS_DEMO_MODE", "true")
	t.Setenv("OCMS_UPLOADS_DIR", uploads)
	ctx := t.Context()

	openDB := func() (*sql.DB, *Queries) {
		db, err := NewDB(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
		if err := Seed(ctx, db, true); err != nil {
			t.Fatal(err)
		}
		return db, New(db)
	}
	db, q := openDB()
	adminID, err := seedDemoUsers(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	photo := demoPhotoLibrary[2]
	legacy, err := q.CreateMedia(ctx, CreateMediaParams{
		Uuid: uuid.NewString(), Filename: photo.legacyFilename, MimeType: "image/png",
		Width: sql.NullInt64{Int64: 2400, Valid: true}, Height: sql.NullInt64{Int64: 1600, Valid: true},
		Alt: sql.NullString{String: photo.legacyAlt, Valid: true}, UploadedBy: adminID, LanguageCode: "en",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateMediaVariant(ctx, CreateMediaVariantParams{MediaID: legacy.ID, Type: "thumbnail", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	article, err := q.CreatePage(ctx, CreatePageParams{
		Slug: DemoArticleSlug, Title: "Getting Started with oCMS", Body: "<p>Previous demo guide.</p>",
		Status: "published", PageType: "post", LanguageCode: "en", AuthorID: adminID,
		FeaturedImageID: sql.NullInt64{Int64: legacy.ID, Valid: true}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CreateContentProvenance(ctx, CreateContentProvenanceParams{
		PageID: article.ID, Revision: 1, Source: "system", Transport: "demo_seed",
		ValidatedJson: "{}", ReportedJson: "{}", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_seed_upgrade BEFORE INSERT ON page_versions
		WHEN NEW.revision = 2 BEGIN SELECT RAISE(FAIL, 'injected upgrade failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SeedDemo(ctx, db); err == nil {
		t.Fatal("upgrade committed without a new revision record")
	}
	unchanged, err := q.GetPageByID(ctx, article.ID)
	if err != nil || unchanged.Body != article.Body || unchanged.Revision != 1 {
		t.Fatalf("failed upgrade modified the old page: %v, %v", unchanged, err)
	}
	if _, err := db.Exec("DROP TRIGGER fail_seed_upgrade"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := SeedDemo(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := q.GetMediaByID(ctx, legacy.ID)
	if err != nil || updated.Uuid != legacy.Uuid || updated.Filename != photo.filename {
		t.Fatalf("legacy photo/reference lost: %+v, %v", updated, err)
	}
	article, err = q.GetPageByID(ctx, article.ID)
	if err != nil || article.FeaturedImageID.Int64 != legacy.ID || article.Body != getGettingStartedBody() || article.Revision != 2 {
		t.Fatalf("legacy article was not refreshed once: %#v, %v", article, err)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM pages WHERE slug = 'planning-the-next-issue' AND status = 'draft'",
		"SELECT COUNT(*) FROM forms WHERE slug = 'contact'",
	} {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: got %d, %v", query, n, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := demo.Reset(path, uploads, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(uploads, "originals", legacy.Uuid)); !os.IsNotExist(err) {
		t.Fatalf("reset did not clear old uploads: %v", err)
	}
	db, q = openDB()
	if err := SeedDemo(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertDemoEditorialFixtures(t, q, uploads)
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

func TestRotateDemoAdminPassword(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()
	t.Setenv("OCMS_DEMO_MODE", "true")
	uploadsDir := t.TempDir()
	t.Setenv("OCMS_UPLOADS_DIR", uploadsDir)

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := SeedDemo(ctx, db); err != nil {
		t.Fatalf("SeedDemo: %v", err)
	}

	defaultAdmin, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatal(err)
	}
	password, err := RotateDemoAdminPassword(ctx, db)
	if err != nil {
		t.Fatalf("RotateDemoAdminPassword: %v", err)
	}
	if password == "" {
		t.Fatal("expected rotated demo admin password to be returned")
	}
	if password == DemoAdminPassword {
		t.Fatal("expected rotated password to differ from seeded default demo password")
	}

	admin, err := q.GetUserByEmail(ctx, DemoAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DemoAdminEmail, err)
	}
	valid, err := auth.CheckPassword(password, admin.PasswordHash)
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if !valid {
		t.Fatal("expected rotated password to authenticate against stored hash")
	}
	if valid, err := auth.CheckPassword(DemoAdminPassword, admin.PasswordHash); err != nil || valid {
		t.Fatalf("seeded public password still valid after rotation: valid=%v err=%v", valid, err)
	}
	other, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil || other.PasswordHash != defaultAdmin.PasswordHash || other.SessionVersion != defaultAdmin.SessionVersion {
		t.Fatal("demo rotation changed a different administrator", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE users SET role = 'editor' WHERE id = ?", admin.ID); err != nil {
		t.Fatal(err)
	}
	if password, err := RotateDemoAdminPassword(ctx, db); err != nil || password != "" {
		t.Fatal("an account without the demo admin role received a demo password", err)
	}
}

func TestRotateDemoAdminPassword_DisabledWhenNotDemoMode(t *testing.T) {
	db, cleanup, ctx, _ := testSetup(t)
	defer cleanup()
	t.Setenv("OCMS_DEMO_MODE", "")

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := SeedDemo(ctx, db); err != nil {
		t.Fatalf("SeedDemo: %v", err)
	}

	password, err := RotateDemoAdminPassword(ctx, db)
	if err != nil {
		t.Fatalf("RotateDemoAdminPassword: %v", err)
	}
	if password != "" {
		t.Fatalf("expected no rotated password outside demo mode, got %q", password)
	}
}

func TestRotateDefaultAdminPassword(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()
	t.Setenv("OCMS_DEMO_MODE", "true")

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	before, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail (before): %v", err)
	}

	password, err := RotateDefaultAdminPassword(ctx, db)
	if err != nil {
		t.Fatalf("RotateDefaultAdminPassword: %v", err)
	}
	if password == "" {
		t.Fatal("expected rotated default admin password to be returned")
	}
	if password == DefaultAdminPassword {
		t.Fatal("expected rotated password to differ from seeded default admin password")
	}

	admin, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DefaultAdminEmail, err)
	}
	valid, err := auth.CheckPassword(password, admin.PasswordHash)
	if err != nil {
		t.Fatalf("CheckPassword(rotated): %v", err)
	}
	if !valid {
		t.Fatal("expected rotated password to authenticate against stored hash")
	}

	stillDefault, err := auth.CheckPassword(DefaultAdminPassword, admin.PasswordHash)
	if err != nil {
		t.Fatalf("CheckPassword(default): %v", err)
	}
	if stillDefault {
		t.Fatal("expected default password to no longer authenticate after rotation")
	}

	// Drift guard: rotation must route through the version-bumping query so
	// any session held under the old password is invalidated on next request.
	if admin.SessionVersion != before.SessionVersion+1 {
		t.Fatalf("session_version = %d, want %d (before+1) — rotation must use UpdateUserPassword (which bumps)", admin.SessionVersion, before.SessionVersion+1)
	}
}

func TestRotateDefaultAdminPassword_DisabledWhenNotDemoMode(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()
	t.Setenv("OCMS_DEMO_MODE", "")

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	password, err := RotateDefaultAdminPassword(ctx, db)
	if err != nil {
		t.Fatalf("RotateDefaultAdminPassword: %v", err)
	}
	if password != "" {
		t.Fatalf("expected no rotated password outside demo mode, got %q", password)
	}

	admin, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DefaultAdminEmail, err)
	}
	valid, err := auth.CheckPassword(DefaultAdminPassword, admin.PasswordHash)
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if !valid {
		t.Fatal("expected default password to still authenticate when rotation is disabled")
	}
}

func TestRotateDefaultAdminPassword_LeavesNonDefaultPasswordIntact(t *testing.T) {
	db, cleanup, ctx, q := testSetup(t)
	defer cleanup()
	t.Setenv("OCMS_DEMO_MODE", "true")

	if err := Seed(ctx, db, true); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	const operatorPassword = "operator-changed-this-1234"
	operatorHash, err := auth.HashPassword(operatorPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	admin, err := q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s): %v", DefaultAdminEmail, err)
	}
	if err := q.UpdateUserPassword(ctx, UpdateUserPasswordParams{
		PasswordHash: operatorHash,
		UpdatedAt:    time.Now(),
		ID:           admin.ID,
	}); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}

	password, err := RotateDefaultAdminPassword(ctx, db)
	if err != nil {
		t.Fatalf("RotateDefaultAdminPassword: %v", err)
	}
	if password != "" {
		t.Fatalf("expected no rotation when password is non-default, got %q", password)
	}

	admin, err = q.GetUserByEmail(ctx, DefaultAdminEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail(%s, post): %v", DefaultAdminEmail, err)
	}
	valid, err := auth.CheckPassword(operatorPassword, admin.PasswordHash)
	if err != nil {
		t.Fatalf("CheckPassword(operator): %v", err)
	}
	if !valid {
		t.Fatal("expected operator-set password to remain after rotation skip")
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
