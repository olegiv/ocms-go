// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

const cleanupTestUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

const queuedCleanupTestUUID = "11111111-2222-3333-4444-555555555555"

func TestQueueMediaCleanupRetriesAcrossModuleRestart(t *testing.T) {
	m := testModule(t)
	root := t.TempDir()

	if err := m.QueueMediaCleanup(context.Background(), "drupal", root, cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	m.removeMediaFiles = func(string, string) error { return errors.New("filesystem busy") }
	if err := m.drainMediaCleanup(context.Background(), "drupal"); err == nil {
		t.Fatal("drainMediaCleanup() error = nil, want pending cleanup")
	}

	var attempts int
	var lastError string
	if err := m.ctx.DB.QueryRow(`
		SELECT attempts, last_error FROM migrator_media_cleanup_queue
		WHERE source = 'drupal' AND media_uuid = ?`, cleanupTestUUID).Scan(&attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !strings.Contains(lastError, "filesystem busy") {
		t.Fatalf("queued failure = attempts %d, error %q", attempts, lastError)
	}

	// A fresh module over the same database models process restart. Its normal
	// cleanup implementation succeeds idempotently and removes the queue row.
	restarted := New()
	if err := restarted.Init(m.ctx); err != nil {
		t.Fatalf("restart module initialization: %v", err)
	}
	var count int
	if err := m.ctx.DB.QueryRow(`SELECT COUNT(*) FROM migrator_media_cleanup_queue`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cleanup queue rows after successful retry = %d, want 0", count)
	}
}

func TestPendingCleanupAppearsInImportedMediaCount(t *testing.T) {
	m := testModule(t)
	if err := m.QueueMediaCleanup(context.Background(), "elefant", t.TempDir(), cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	counts, err := m.getImportedCounts(context.Background(), "elefant")
	if err != nil {
		t.Fatal(err)
	}
	if counts["media"] != 1 {
		t.Fatalf("media count with pending cleanup = %d, want 1", counts["media"])
	}
}

func TestMissingUploadRootClearsQueuedCleanup(t *testing.T) {
	m := testModule(t)
	missingRoot := filepath.Join(t.TempDir(), "already-removed")
	if err := m.QueueMediaCleanup(context.Background(), "elefant", missingRoot, cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	if err := m.drainMediaCleanup(context.Background(), "elefant"); err != nil {
		t.Fatalf("drainMediaCleanup() for missing root = %v, want success", err)
	}
	var count int
	if err := m.ctx.DB.QueryRow(`SELECT COUNT(*) FROM migrator_media_cleanup_queue`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cleanup queue rows after missing-root drain = %d, want 0", count)
	}
}

func TestQueueMediaCleanupRejectsUploadSymlinkToFilesystemRoot(t *testing.T) {
	m := testModule(t)
	uploadDir := filepath.Join(t.TempDir(), "uploads")
	filesystemRoot := filepath.VolumeName(uploadDir) + string(filepath.Separator)
	if err := os.Symlink(filesystemRoot, uploadDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := m.QueueMediaCleanup(context.Background(), "elefant", uploadDir, cleanupTestUUID); err == nil {
		t.Fatal("QueueMediaCleanup() accepted an uploads symlink to filesystem root")
	}
}

func TestDrainRejectsTamperedBroadCleanupRoot(t *testing.T) {
	m := testModule(t)
	filesystemRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if _, err := m.ctx.DB.Exec(`
		INSERT INTO migrator_media_cleanup_queue
			(source, upload_root, media_uuid, created_at, updated_at)
		VALUES ('elefant', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, filesystemRoot, cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	called := false
	m.removeMediaFiles = func(string, string) error {
		called = true
		return nil
	}
	if err := m.drainMediaCleanup(context.Background(), "elefant"); err == nil {
		t.Fatal("drainMediaCleanup() accepted a broad root from persisted queue data")
	}
	if called {
		t.Fatal("filesystem removal ran for a rejected persisted cleanup root")
	}
}

func TestDrainRejectsCanonicalRootRetargetedBySymlink(t *testing.T) {
	m := testModule(t)
	queuedRoot := t.TempDir()
	if err := m.QueueMediaCleanup(context.Background(), "elefant", queuedRoot, cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	originalRoot := queuedRoot + "-original"
	if err := os.Rename(queuedRoot, originalRoot); err != nil {
		t.Fatal(err)
	}
	otherRoot := t.TempDir()
	outsideMedia := filepath.Join(otherRoot, "originals", cleanupTestUUID)
	if err := os.MkdirAll(outsideMedia, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideMedia, "must-remain.pdf")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(otherRoot, queuedRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := m.drainMediaCleanup(context.Background(), "elefant"); err == nil {
		t.Fatal("drainMediaCleanup() accepted a queued root retargeted by symlink")
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "outside" {
		t.Fatalf("retargeted cleanup changed outside file: data=%q error=%v", data, err)
	}
}

func TestDrainRejectsCanonicalRootRetargetedInsideRemoval(t *testing.T) {
	m := testModule(t)
	queuedRoot := filepath.Join(t.TempDir(), "uploads")
	if err := os.MkdirAll(queuedRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := m.QueueMediaCleanup(context.Background(), "elefant", queuedRoot, cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	outsideRoot := t.TempDir()
	outsideMedia := filepath.Join(outsideRoot, "originals", cleanupTestUUID)
	if err := os.MkdirAll(outsideMedia, 0o750); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideMedia, "must-remain.pdf")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.removeMediaFiles = func(root, mediaUUID string) error {
		if err := os.Rename(root, root+"-original"); err != nil {
			return err
		}
		if err := os.Symlink(outsideRoot, root); err != nil {
			return err
		}
		return imaging.DeleteMediaFilesFromCanonicalRoot(root, mediaUUID)
	}

	if err := m.drainMediaCleanup(context.Background(), "elefant"); err == nil {
		t.Fatal("drainMediaCleanup() accepted a root retargeted after its outer validation")
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "outside" {
		t.Fatalf("retargeted cleanup changed outside file: data=%q error=%v", data, err)
	}
	var pending int
	if err := m.ctx.DB.QueryRow(`SELECT COUNT(*) FROM migrator_media_cleanup_queue`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending cleanup rows = %d, want 1", pending)
	}
}

func TestDrainDoesNotEraseCleanupRefreshedWhileRunning(t *testing.T) {
	m := testModule(t)
	root := t.TempDir()
	if err := m.QueueMediaCleanup(context.Background(), "elefant", root, cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	m.removeMediaFiles = func(string, string) error {
		_, err := m.ctx.DB.Exec(`
			UPDATE migrator_media_cleanup_queue
			SET updated_at = '2099-01-01 00:00:00'
			WHERE source = 'elefant' AND media_uuid = ?
		`, cleanupTestUUID)
		return err
	}
	if err := m.drainMediaCleanup(context.Background(), "elefant"); err == nil {
		t.Fatal("drainMediaCleanup() error = nil after cleanup row refresh")
	}
	var count int
	if err := m.ctx.DB.QueryRow(`SELECT COUNT(*) FROM migrator_media_cleanup_queue`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("cleanup queue rows after concurrent refresh = %d, want 1", count)
	}
}

func TestCanceledDrainDoesNotStartFilesystemRemoval(t *testing.T) {
	m := testModule(t)
	if err := m.QueueMediaCleanup(context.Background(), "elefant", t.TempDir(), cleanupTestUUID); err != nil {
		t.Fatal(err)
	}
	called := false
	m.removeMediaFiles = func(string, string) error {
		called = true
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.drainMediaCleanup(ctx, "elefant"); err == nil {
		t.Fatal("drainMediaCleanup(canceled) error = nil")
	}
	if called {
		t.Fatal("canceled queue drain started filesystem removal")
	}
}

func TestDeleteRollsBackWhenCleanupQueueWriteFails(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	mediaID := createTrackedCleanupMedia(t, m)

	if _, err := m.ctx.DB.Exec(`DROP TABLE migrator_media_cleanup_queue`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.deleteImportedItems(ctx, "drupal"); err == nil {
		t.Fatal("deleteImportedItems() error = nil after cleanup queue removal")
	}
	if _, err := store.New(m.ctx.DB).GetMediaByID(ctx, mediaID); err != nil {
		t.Fatalf("media row was not rolled back after queue failure: %v", err)
	}
	var tracked int
	if err := m.ctx.DB.QueryRow(`SELECT COUNT(*) FROM migrator_imported_items WHERE source='drupal'`).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 1 {
		t.Fatalf("tracking rows after queue failure = %d, want 1", tracked)
	}
}

func TestDeleteRollsBackForMalformedMediaUUID(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	mediaID := createTrackedCleanupMedia(t, m)
	if _, err := m.ctx.DB.Exec(`UPDATE media SET uuid = '../outside' WHERE id = ?`, mediaID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.deleteImportedItems(ctx, "drupal"); err == nil {
		t.Fatal("deleteImportedItems() accepted a malformed stored media UUID")
	}
	if _, err := store.New(m.ctx.DB).GetMediaByID(ctx, mediaID); err != nil {
		t.Fatalf("media row was not rolled back after UUID validation failure: %v", err)
	}
	var tracked int
	if err := m.ctx.DB.QueryRow(`SELECT COUNT(*) FROM migrator_imported_items WHERE source='drupal'`).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 1 {
		t.Fatalf("tracking rows after UUID validation failure = %d, want 1", tracked)
	}
}

func TestAbortingDeleteStillDrainsPreviouslyQueuedCleanup(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	queuedRoot := t.TempDir()
	queuedDir := filepath.Join(queuedRoot, "originals", queuedCleanupTestUUID)
	if err := os.MkdirAll(queuedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	queuedFile := filepath.Join(queuedDir, "queued.pdf")
	if err := os.WriteFile(queuedFile, []byte("queued cleanup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.QueueMediaCleanup(ctx, "drupal", queuedRoot, queuedCleanupTestUUID); err != nil {
		t.Fatal(err)
	}

	mediaID := createTrackedCleanupMedia(t, m)
	if _, err := m.ctx.DB.Exec(`UPDATE media SET uuid = '../outside' WHERE id = ?`, mediaID); err != nil {
		t.Fatal(err)
	}

	if _, err := m.deleteImportedItems(ctx, "drupal"); err == nil {
		t.Fatal("deleteImportedItems() error = nil for malformed tracked media UUID")
	}

	var queued int
	if err := m.ctx.DB.QueryRow(`
		SELECT COUNT(*) FROM migrator_media_cleanup_queue
		WHERE source = 'drupal' AND media_uuid = ?`, queuedCleanupTestUUID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("previous cleanup rows after aborting delete = %d, want 0", queued)
	}
	if _, err := os.Stat(queuedFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previously queued file survived aborting delete: %v", err)
	}

	// The new deletion still rolls back atomically: draining old work must not
	// weaken the queue-write guarantee for the malformed row in this attempt.
	if _, err := store.New(m.ctx.DB).GetMediaByID(ctx, mediaID); err != nil {
		t.Fatalf("malformed media row was not rolled back: %v", err)
	}
	var tracked int
	if err := m.ctx.DB.QueryRow(`
		SELECT COUNT(*) FROM migrator_imported_items
		WHERE source = 'drupal' AND entity_type = 'media' AND entity_id = ?`, mediaID).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 1 {
		t.Fatalf("tracking rows after aborting delete = %d, want 1", tracked)
	}
}

func TestHandleDeleteWhileRunningStillDrainsPreviouslyQueuedCleanup(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	queuedRoot := t.TempDir()
	queuedDir := filepath.Join(queuedRoot, "originals", queuedCleanupTestUUID)
	if err := os.MkdirAll(queuedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	queuedFile := filepath.Join(queuedDir, "queued.pdf")
	if err := os.WriteFile(queuedFile, []byte("queued cleanup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.QueueMediaCleanup(ctx, "drupal", queuedRoot, queuedCleanupTestUUID); err != nil {
		t.Fatal(err)
	}

	mediaID := createTrackedCleanupMedia(t, m)
	if _, err := m.startJob(ctx, "drupal", "admin@example.com", 0, ImportOptions{}); err != nil {
		t.Fatalf("startJob() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/migrator/drupal/delete", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("source", "drupal")
	requestCtx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	requestCtx = context.WithValue(requestCtx, middleware.ContextKeyUser,
		store.User{ID: 1, Email: "admin@example.com", Role: "admin"})
	req = req.WithContext(requestCtx)
	rr := httptest.NewRecorder()

	m.handleDeleteImported(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("handleDeleteImported() status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if location := rr.Header().Get("Location"); location != "/admin/migrator/drupal" {
		t.Fatalf("handleDeleteImported() redirect = %q, want %q", location, "/admin/migrator/drupal")
	}
	var queued int
	if err := m.ctx.DB.QueryRow(`
		SELECT COUNT(*) FROM migrator_media_cleanup_queue
		WHERE source = 'drupal' AND media_uuid = ?`, queuedCleanupTestUUID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("previous cleanup rows after rejected delete = %d, want 0", queued)
	}
	if _, err := os.Stat(queuedFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previously queued file survived rejected delete: %v", err)
	}

	// The live job rejects database deletion, but that must not suppress the
	// independent retry of cleanup work committed by an earlier attempt.
	if _, err := store.New(m.ctx.DB).GetMediaByID(ctx, mediaID); err != nil {
		t.Fatalf("tracked media was deleted while import job was running: %v", err)
	}
	var tracked int
	if err := m.ctx.DB.QueryRow(`
		SELECT COUNT(*) FROM migrator_imported_items
		WHERE source = 'drupal' AND entity_type = 'media' AND entity_id = ?`, mediaID).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 1 {
		t.Fatalf("tracking rows after rejected delete = %d, want 1", tracked)
	}
}

func createTrackedCleanupMedia(t *testing.T, m *Module) int64 {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("OCMS_UPLOADS_DIR", root)

	var language string
	if err := m.ctx.DB.QueryRow(`SELECT code FROM languages WHERE is_default = 1`).Scan(&language); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := m.ctx.DB.QueryRow(`
		INSERT INTO users (email,password_hash,role,name,created_at,updated_at)
		VALUES ('cleanup@example.com','x','admin','Cleanup',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var mediaID int64
	if err := m.ctx.DB.QueryRow(`
		INSERT INTO media (uuid,filename,mime_type,size,uploaded_by,language_code,created_at,updated_at)
		VALUES (?, 'x.pdf', 'application/pdf', 1, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id`, cleanupTestUUID, userID, language).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	if err := m.TrackImportedItem(ctx, "drupal", "media", mediaID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "originals", cleanupTestUUID)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return mediaID
}

// TestDeleteRetainsMediaReferencedOutsidePages covers the links nothing was
// watching.
//
// Media URLs are plain text wherever they appear, with no foreign key behind
// them, so a menu item, category description, form, widget or
// config value can hold one exactly as a page body can. Delete Imported
// Content checked only page image columns and page bodies, so it removed the
// row and its files while those administrator-owned records kept pointing at
// them — broken images with nothing to explain them.
func TestDeleteRetainsMediaReferencedOutsidePages(t *testing.T) {
	mediaURL := "/uploads/originals/" + cleanupTestUUID + "/x.pdf"
	for name, seed := range map[string]string{
		"menu item": `INSERT INTO menus (name,slug,language_code,created_at,updated_at)
			VALUES ('Main','main-` + cleanupTestUUID[:8] + `','en',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
			INSERT INTO menu_items (menu_id,title,url,position)
			VALUES ((SELECT id FROM menus ORDER BY id DESC LIMIT 1),'Brochure','` + mediaURL + `',0);`,
		"category description": `INSERT INTO categories (name,slug,description,language_code)
			VALUES ('Docs','docs','<a href="` + mediaURL + `">brochure</a>','en');`,
		"config value": `INSERT INTO config (key,value,language_code) VALUES ('site_brochure','` + mediaURL + `','en');`,
	} {
		t.Run(name, func(t *testing.T) {
			m := testModule(t)
			ctx := context.Background()
			mediaID := createTrackedCleanupMedia(t, m)
			if _, err := m.ctx.DB.Exec(seed); err != nil {
				t.Fatalf("seed %s: %v", name, err)
			}

			if _, err := m.deleteImportedItems(ctx, "drupal"); err != nil {
				t.Fatalf("deleteImportedItems() error = %v", err)
			}
			if _, err := store.New(m.ctx.DB).GetMediaByID(ctx, mediaID); err != nil {
				t.Fatalf("media referenced by a %s was deleted: %v; the surviving record now "+
					"points at a file that is gone", name, err)
			}
		})
	}
}

// Public submissions are attacker-controlled and must not turn a submitted
// URL into a trusted reference that permanently exempts imported media from
// cleanup.
func TestDeleteIgnoresMediaURLsInPublicFormSubmissions(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	mediaID := createTrackedCleanupMedia(t, m)
	mediaURL := "/uploads/originals/" + cleanupTestUUID + "/x.pdf"

	if _, err := m.ctx.DB.Exec(`
		INSERT INTO forms (name,slug,title,language_code,created_at,updated_at)
		VALUES ('Contact','contact','Contact','en',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
		INSERT INTO form_submissions (form_id,data,language_code,created_at)
		VALUES ((SELECT id FROM forms ORDER BY id DESC LIMIT 1), ?, 'en', CURRENT_TIMESTAMP)`,
		`{"file":"`+mediaURL+`"}`); err != nil {
		t.Fatal(err)
	}

	if _, err := m.deleteImportedItems(ctx, "drupal"); err != nil {
		t.Fatalf("deleteImportedItems() error = %v", err)
	}
	if _, err := store.New(m.ctx.DB).GetMediaByID(ctx, mediaID); err == nil {
		t.Fatal("attacker-controlled public submission preserved imported media")
	}
}

// TestDeleteRemovesUnreferencedMedia is the other half: without a reference
// anywhere, imported media is still cleaned up.
func TestDeleteRemovesUnreferencedMedia(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	mediaID := createTrackedCleanupMedia(t, m)

	if _, err := m.deleteImportedItems(ctx, "drupal"); err != nil {
		t.Fatalf("deleteImportedItems() error = %v", err)
	}
	if _, err := store.New(m.ctx.DB).GetMediaByID(ctx, mediaID); err == nil {
		t.Fatal("unreferenced imported media survived the delete")
	}
}
