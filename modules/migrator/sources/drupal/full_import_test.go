// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build drupal_integration

package drupal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// countingTracker satisfies types.ImportTracker without recording anything: the
// assertions here read the resulting database, not the tracker.
type countingTracker struct{ items int }

func (c *countingTracker) TrackImportedItem(context.Context, string, string, int64) error {
	c.items++
	return nil
}

func (c *countingTracker) ReportProgress(context.Context, types.Progress) {}

// TestFullImportIntoScratchDB runs a complete Drupal import against the live
// source database into a throwaway SQLite, then asserts the outcomes the media
// fixes were made for.
//
// It imports into a scratch database on purpose: it is a verification of the
// importer, not a migration of anyone's site, so it must be safe to run
// repeatedly without touching real content.
//
//	OCMS_SESSION_SECRET=... DRUPAL_HOST=127.0.0.1 DRUPAL_USER=… DRUPAL_PASSWORD=… \
//	DRUPAL_DB=… DRUPAL_FILES=/path/to/sites/default/files \
//	go test -tags drupal_integration -run TestFullImportIntoScratchDB -v ./modules/migrator/sources/drupal/
func TestFullImportIntoScratchDB(t *testing.T) {
	cfg := liveConfig(t)
	if cfg["files_path"] == "" {
		t.Skip("DRUPAL_FILES not set; the media assertions need a files directory")
	}

	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	queries := store.New(db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// The importer attributes imported content to an existing admin.
	hash, err := auth.HashPassword("admin-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "owner@example.com",
		PasswordHash: hash,
		Role:         model.RoleAdmin,
		Name:         "Owner",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	uploadDir := t.TempDir()
	t.Setenv("OCMS_UPLOADS_DIR", uploadDir)

	opts := types.ImportOptions{
		ImportTags:       true,
		ImportCategories: true,
		ImportMedia:      true,
		ImportPosts:      true,
		ImportPages:      true,
		ImportMenus:      true,
	}

	result, err := NewSource().Import(ctx, db, cfg, opts, &countingTracker{})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	t.Logf("imported: media=%d posts=%d pages=%d | skipped media=%d | errors=%d notices=%d",
		result.MediaImported, result.PostsImported, result.PagesImported,
		result.MediaSkipped, len(result.Errors), len(result.Notices))
	for _, e := range result.Errors {
		t.Logf("  error:  %s", e)
	}

	// Oversized images are downscaled now rather than rejected, so the two
	// files that used to fail must no longer appear as errors.
	for _, e := range result.Errors {
		if strings.Contains(e, "maximum allowed") {
			t.Errorf("an oversized image was still rejected instead of downscaled: %s", e)
		}
	}

	// Every stored image must respect the caps, including the downscaled ones.
	rows, err := db.QueryContext(ctx, `SELECT filename, width, height FROM media
		WHERE width IS NOT NULL AND height IS NOT NULL`)
	if err != nil {
		t.Fatalf("query media: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var withAlt, total int
	for rows.Next() {
		var filename string
		var width, height int64
		if err := rows.Scan(&filename, &width, &height); err != nil {
			t.Fatalf("scan media: %v", err)
		}
		total++
		if width > 10000 || height > 10000 || width*height > 50000000 {
			t.Errorf("%s stored at %dx%d (%d px), which exceeds the media caps",
				filename, width, height, width*height)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate media: %v", err)
	}
	if total == 0 {
		t.Fatal("no media rows with dimensions were created; the import did not read the files directory")
	}

	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media WHERE COALESCE(alt, '') <> ''`).Scan(&withAlt); err != nil {
		t.Fatalf("count alt: %v", err)
	}
	t.Logf("media rows: %d with dimensions, %d carrying alt text", total, withAlt)
	if withAlt == 0 {
		t.Error("no imported media carries alt text; the alt fallback is not reaching the media row")
	}

	// A page whose Drupal node has a featured image must end up with one.
	var pagesMissingImage int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pages WHERE featured_image_id IS NULL
		  AND slug IN ('evropa-privlechet-severnuyu-afriku-k-proektu-goluboy-ekonomiki',
		               'tunis-primet-zakon-po-stimulirovaniyu-investiciy-dlya-razvitiya-ekonomiki')
	`).Scan(&pagesMissingImage); err != nil {
		t.Fatalf("count pages missing image: %v", err)
	}
	if pagesMissingImage != 0 {
		t.Errorf("%d page(s) whose source node has a featured image still have none; "+
			"their images are the ones that used to fail the size caps", pagesMissingImage)
	}
}
