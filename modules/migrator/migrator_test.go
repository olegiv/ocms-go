// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/imaging"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// mockSource implements Source for testing without MySQL dependency.
type mockSource struct {
	name string
}

func (s *mockSource) Name() string        { return s.name }
func (s *mockSource) DisplayName() string { return "Mock " + s.name }
func (s *mockSource) Description() string { return "Mock source for testing" }
func (s *mockSource) ConfigFields() []types.ConfigField {
	return []types.ConfigField{
		{Name: "host", Label: "Host", Type: "text", Required: true, Default: "localhost"},
		{Name: "port", Label: "Port", Type: "number", Default: "3306"},
	}
}
func (s *mockSource) TestConnection(_ map[string]string) error { return nil }
func (s *mockSource) Import(_ context.Context, _ *sql.DB, _ map[string]string, _ types.ImportOptions, _ types.ImportTracker) (*types.ImportResult, error) {
	return &types.ImportResult{}, nil
}

type canceledConnectionSource struct {
	mockSource
	observedCancellation bool
}

func (s *canceledConnectionSource) TestConnectionContext(ctx context.Context, _ map[string]string) error {
	<-ctx.Done()
	s.observedCancellation = true
	return ctx.Err()
}

func testModule(t *testing.T) *Module {
	t.Helper()
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	mctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(mctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return m
}

// --- Module properties ---

func TestModuleProperties(t *testing.T) {
	m := New()
	if m.Name() != "migrator" {
		t.Errorf("Name() = %q, want %q", m.Name(), "migrator")
	}
	if m.Version() != "1.1.0" {
		t.Errorf("Version() = %q, want %q", m.Version(), "1.1.0")
	}
	if m.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if m.AdminURL() != "/admin/migrator" {
		t.Errorf("AdminURL() = %q, want %q", m.AdminURL(), "/admin/migrator")
	}
	if m.SidebarLabel() != "nav.migrator" {
		t.Errorf("SidebarLabel() = %q, want %q", m.SidebarLabel(), "nav.migrator")
	}
	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Error("TemplateFuncs() should not be nil")
	}
	if len(funcs) != 0 {
		t.Errorf("TemplateFuncs() should be empty, got %d", len(funcs))
	}
}

// --- Migrations ---

func TestMigrations(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 6)
}

func TestMigrationUp(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Verify table exists
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='migrator_imported_items'`).Scan(&name)
	if err != nil {
		t.Fatalf("migrator_imported_items table should exist: %v", err)
	}
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='migrator_media_cleanup_queue'`).Scan(&name); err != nil {
		t.Fatalf("migrator_media_cleanup_queue table should exist: %v", err)
	}

	// Verify indexes exist
	for _, idx := range []string{"idx_migrator_source", "idx_migrator_entity"} {
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name)
		if err != nil {
			t.Errorf("index %s should exist: %v", idx, err)
		}
	}
}

func TestMediaCleanupMigrationColumns(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	moduleutil.RunMigrations(t, db, New().Migrations())

	want := []string{"source", "upload_root", "media_uuid", "attempts", "last_error", "created_at", "updated_at"}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('migrator_media_cleanup_queue') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cleanup queue columns = %v, want %v", got, want)
	}
}

func migrationByVersion(t *testing.T, version int64) module.Migration {
	t.Helper()
	for _, migration := range New().Migrations() {
		if migration.Version == version {
			return migration
		}
	}
	t.Fatalf("migration %d not found", version)
	return module.Migration{}
}

func legacyJobsTableDDL() string {
	ddl := strings.ReplaceAll(createPartialJobsTableSQL,
		"migrator_import_jobs_new", "migrator_import_jobs")
	return strings.Replace(ddl,
		"'running','completed','failed','partial','interrupted'",
		"'running','completed','failed','interrupted'", 1)
}

func TestMigrationV5RollsBackFailedRebuildAndRetries(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if _, err := db.Exec(legacyJobsTableDDL()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO migrator_import_jobs (source, status) VALUES ('drupal', 'corrupt')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA ignore_check_constraints = OFF`); err != nil {
		t.Fatal(err)
	}

	migration := migrationByVersion(t, 5)
	if err := migration.Up(db); err == nil {
		t.Fatal("migration 5 succeeded despite a row rejected by the replacement constraint")
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM migrator_import_jobs WHERE source = 'drupal'`).Scan(&status); err != nil {
		t.Fatalf("legacy table/data did not survive rollback: %v", err)
	}
	if status != "corrupt" {
		t.Fatalf("legacy status = %q, want corrupt", status)
	}
	var tempCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='migrator_import_jobs_new'`).Scan(&tempCount); err != nil {
		t.Fatal(err)
	}
	if tempCount != 0 {
		t.Fatal("temporary jobs table survived the rolled-back migration")
	}

	if _, err := db.Exec(`DELETE FROM migrator_import_jobs WHERE status = 'corrupt'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO migrator_import_jobs (source, status) VALUES ('drupal', 'completed')`); err != nil {
		t.Fatal(err)
	}
	if err := migration.Up(db); err != nil {
		t.Fatalf("migration retry failed: %v", err)
	}
	if !statusCheckAllows(db, "partial") {
		t.Fatal("rebuilt jobs table still rejects partial status")
	}
	if _, err := db.Exec(`INSERT INTO migrator_import_jobs (source, status) VALUES ('elefant', 'partial')`); err != nil {
		t.Fatalf("partial status insert failed after rebuild: %v", err)
	}
}

func TestMigrationV5RecoversRenameInterruptedLegacyState(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)
	if _, err := db.Exec(createPartialJobsTableSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO migrator_import_jobs_new (source, status) VALUES ('drupal', 'partial')`); err != nil {
		t.Fatal(err)
	}
	if err := migrationByVersion(t, 5).Up(db); err != nil {
		t.Fatalf("migration recovery failed: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM migrator_import_jobs WHERE source = 'drupal'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "partial" {
		t.Fatalf("recovered status = %q, want partial", status)
	}
	for _, index := range []string{"idx_migrator_jobs_one_running", "idx_migrator_jobs_source_started"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", index, count)
		}
	}
}

func TestMigrationDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	t.Cleanup(cleanup)

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	moduleutil.RunMigrationsDown(t, db, m.Migrations())
	moduleutil.AssertTableNotExists(t, db, "migrator_imported_items")
	moduleutil.AssertTableNotExists(t, db, "migrator_media_cleanup_queue")
}

// --- Init / Shutdown ---

func TestInit(t *testing.T) {
	m := testModule(t)
	if m.ctx == nil {
		t.Error("ctx should be set after Init")
	}
}

func TestShutdown(t *testing.T) {
	m := testModule(t)
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}

func TestShutdownBeforeInit(t *testing.T) {
	m := New()
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() before Init error = %v", err)
	}
}

func TestApplySafeDefaults_SkipsPasswordFields(t *testing.T) {
	config := make(map[string]string)
	fields := []types.ConfigField{
		{Name: "mysql_host", Type: "text", Default: "localhost"},
		{Name: "mysql_password", Type: "password", Default: "super-secret"},
		{Name: "mysql_port", Type: "number", Default: "3306"},
		{Name: "empty", Type: "text", Default: ""},
	}

	applySafeDefaults(config, fields)

	if got := config["mysql_host"]; got != "localhost" {
		t.Fatalf("mysql_host default mismatch: got %q, want %q", got, "localhost")
	}
	if got := config["mysql_port"]; got != "3306" {
		t.Fatalf("mysql_port default mismatch: got %q, want %q", got, "3306")
	}
	if _, ok := config["mysql_password"]; ok {
		t.Fatal("mysql_password should not be defaulted for password fields")
	}
	if _, ok := config["empty"]; ok {
		t.Fatal("empty default should not be copied")
	}
}

// --- Source registry ---

func TestSourceRegistry_RegisterAndGet(t *testing.T) {
	name := "test-mock-register-get"
	RegisterSource(&mockSource{name: name})
	t.Cleanup(func() {
		sourcesMu.Lock()
		delete(sources, name)
		sourcesMu.Unlock()
	})

	s, ok := GetSource(name)
	if !ok {
		t.Fatal("GetSource should return true for registered source")
	}
	if s.Name() != name {
		t.Errorf("source Name() = %q, want %q", s.Name(), name)
	}
}

func TestSourceRegistry_GetMissing(t *testing.T) {
	_, ok := GetSource("nonexistent-source-xyz")
	if ok {
		t.Error("GetSource should return false for unregistered source")
	}
}

func TestSourceRegistry_ListSources(t *testing.T) {
	// Register two mock sources in reverse order
	nameB := "test-mock-zzz-beta"
	nameA := "test-mock-aaa-alpha"
	RegisterSource(&mockSource{name: nameB})
	RegisterSource(&mockSource{name: nameA})
	t.Cleanup(func() {
		sourcesMu.Lock()
		delete(sources, nameA)
		delete(sources, nameB)
		sourcesMu.Unlock()
	})

	list := ListSources()
	if len(list) < 2 {
		t.Fatalf("ListSources should return at least 2, got %d", len(list))
	}

	// Verify sorted order
	for i := 1; i < len(list); i++ {
		if list[i-1].Name() > list[i].Name() {
			t.Errorf("ListSources not sorted: %q > %q", list[i-1].Name(), list[i].Name())
		}
	}
}

// --- TranslationsFS ---

func TestTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("ReadDir(locales) error: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "en" {
			found = true
			break
		}
	}
	if !found {
		t.Error("locales should contain 'en' directory")
	}
}

// --- Route registration ---

func TestRegisterRoutes(t *testing.T) {
	m := New()
	// RegisterRoutes is a no-op; nil should not panic.
	m.RegisterRoutes(nil)
}

func TestRegisterAdminRoutes(t *testing.T) {
	m := testModule(t)
	r := chi.NewRouter()
	m.RegisterAdminRoutes(r)
	// No panic = success
}

// --- DB helpers ---

func TestTrackImportedItem_And_GetImportedCounts(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	// Track items
	if err := m.TrackImportedItem(ctx, "test-src", "page", 1); err != nil {
		t.Fatalf("TrackImportedItem page 1: %v", err)
	}
	if err := m.TrackImportedItem(ctx, "test-src", "page", 2); err != nil {
		t.Fatalf("TrackImportedItem page 2: %v", err)
	}
	if err := m.TrackImportedItem(ctx, "test-src", "tag", 10); err != nil {
		t.Fatalf("TrackImportedItem tag 10: %v", err)
	}

	counts, err := m.getImportedCounts(ctx, "test-src")
	if err != nil {
		t.Fatalf("getImportedCounts: %v", err)
	}
	if counts["page"] != 2 {
		t.Errorf("page count = %d, want 2", counts["page"])
	}
	if counts["tag"] != 1 {
		t.Errorf("tag count = %d, want 1", counts["tag"])
	}
}

func TestGetImportedItems(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	_ = m.TrackImportedItem(ctx, "test-src2", "media", 100)
	_ = m.TrackImportedItem(ctx, "test-src2", "media", 200)

	ids, err := m.getImportedItems(ctx, m.ctx.DB, "test-src2", "media")
	if err != nil {
		t.Fatalf("getImportedItems: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("got %d items, want 2", len(ids))
	}
}

func TestGetImportedItems_Empty(t *testing.T) {
	m := testModule(t)
	ids, err := m.getImportedItems(context.Background(), m.ctx.DB, "nonexistent", "page")
	if err != nil {
		t.Fatalf("getImportedItems error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("got %d items, want 0", len(ids))
	}
}

func TestGetImportedCounts_Empty(t *testing.T) {
	m := testModule(t)
	counts, err := m.getImportedCounts(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("getImportedCounts error: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("got %d counts, want 0", len(counts))
	}
}

// --- collectSourceConfig ---

func TestCollectSourceConfig(t *testing.T) {
	src := &mockSource{name: "cfg-test"}
	body := "host=example.com&port=5432"
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	cfg := collectSourceConfig(req, src)
	if cfg["host"] != "example.com" {
		t.Errorf("host = %q, want %q", cfg["host"], "example.com")
	}
	if cfg["port"] != "5432" {
		t.Errorf("port = %q, want %q", cfg["port"], "5432")
	}
}

// --- Handler unauthenticated paths ---

func TestHandlerUnauthenticated_TestConnection(t *testing.T) {
	m := testModule(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/migrator/elefant/test", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("source", "elefant")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()

	m.handleTestConnection(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestConnectionCancellationWritesNoResponse(t *testing.T) {
	m := testModule(t)
	source := &canceledConnectionSource{mockSource: mockSource{name: "cancel-test"}}
	RegisterSource(source)
	t.Cleanup(func() {
		sourcesMu.Lock()
		delete(sources, source.Name())
		sourcesMu.Unlock()
	})

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/admin/migrator/cancel-test/test", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("source", source.Name())
	requestContext = context.WithValue(requestContext, chi.RouteCtxKey, rctx)
	requestContext = context.WithValue(requestContext, middleware.ContextKeyUser,
		store.User{ID: 1, Email: "admin@example.com", Role: "admin"})
	req = req.WithContext(requestContext)
	rr := httptest.NewRecorder()

	m.handleTestConnection(rr, req)
	if !source.observedCancellation {
		t.Fatal("context-aware source did not observe request cancellation")
	}
	if rr.Header().Get("Location") != "" || rr.Body.Len() != 0 {
		t.Fatalf("canceled connection test wrote response: headers=%v body=%q", rr.Header(), rr.Body.String())
	}
}

func TestHandlerUnauthenticated_Import(t *testing.T) {
	m := testModule(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/migrator/elefant/import", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("source", "elefant")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()

	m.handleImport(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestHandlerUnauthenticated_Delete(t *testing.T) {
	m := testModule(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/migrator/elefant/delete", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("source", "elefant")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()

	m.handleDeleteImported(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// TestDeleteMediaFilesRemovesEveryStorageDir asserts that deleting a media item
// removes everything creating one can produce.
//
// It walks model.MediaStorageDirs rather than a list of its own, so a variant
// added to model.ImageVariants is covered here the moment it exists. The
// hardcoded list this replaced had already drifted: it omitted "og", and since
// deleting an import also removes the media row and its tracking row, every
// imported image left an /uploads/og/<uuid> directory that nothing could ever
// find again.
func TestDeleteMediaFilesRemovesEveryStorageDir(t *testing.T) {
	uploadDir := t.TempDir()
	t.Setenv("OCMS_UPLOADS_DIR", uploadDir)

	const mediaUUID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	dirs := model.MediaStorageDirs()
	if len(dirs) < 2 {
		t.Fatalf("model.MediaStorageDirs() = %v, want originals plus every variant", dirs)
	}
	// The bug was a missing variant, so fail loudly if the source of truth
	// itself ever stops naming the one this test exists for.
	if !slices.Contains(dirs, model.VariantOG) {
		t.Fatalf("model.MediaStorageDirs() = %v, want it to include %q", dirs, model.VariantOG)
	}

	for _, dir := range dirs {
		path := filepath.Join(uploadDir, dir, mediaUUID)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("failed to create %s: %v", path, err)
		}
		if err := os.WriteFile(filepath.Join(path, "photo.jpg"), []byte("x"), 0o644); err != nil {
			t.Fatalf("failed to write into %s: %v", path, err)
		}
	}

	if err := imaging.DeleteMediaFiles(uploadDir, mediaUUID); err != nil {
		t.Fatalf("DeleteMediaFiles() error = %v", err)
	}

	for _, dir := range dirs {
		path := filepath.Join(uploadDir, dir, mediaUUID)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived deletion (stat err = %v); every media storage dir must be removed", path, err)
		}
	}
}

// --- View structs ---

func TestViewStructs(t *testing.T) {
	sv := MigratorSourceView{Name: "test", DisplayName: "Test", Description: "desc"}
	if sv.Name != "test" {
		t.Error("MigratorSourceView.Name mismatch")
	}

	lv := MigratorListViewData{Sources: []MigratorSourceView{sv}}
	if len(lv.Sources) != 1 {
		t.Error("MigratorListViewData.Sources length mismatch")
	}

	fv := MigratorSourceFormViewData{
		SourceName:     "test",
		DisplayName:    "Test",
		Description:    "desc",
		ConfigFields:   []types.ConfigField{{Name: "host"}},
		Config:         map[string]string{"host": "localhost"},
		ImportedCounts: map[string]int{"page": 5},
	}
	if fv.ImportedCounts["page"] != 5 {
		t.Error("MigratorSourceFormViewData.ImportedCounts mismatch")
	}
}
