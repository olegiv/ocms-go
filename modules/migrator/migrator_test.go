// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
	"github.com/olegiv/ocms-go/modules/migrator/types"
)

// mockSource implements Source for testing without MySQL dependency.
type mockSource struct {
	name string
}

func (s *mockSource) Name() string        { return s.name }
func (s *mockSource) DisplayName() string  { return "Mock " + s.name }
func (s *mockSource) Description() string  { return "Mock source for testing" }
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
	if m.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want %q", m.Version(), "1.0.0")
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
	moduleutil.AssertMigrations(t, m.Migrations(), 1)
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

	// Verify indexes exist
	for _, idx := range []string{"idx_migrator_source", "idx_migrator_entity"} {
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name)
		if err != nil {
			t.Errorf("index %s should exist: %v", idx, err)
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

	ids, err := m.getImportedItems(ctx, "test-src2", "media")
	if err != nil {
		t.Fatalf("getImportedItems: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("got %d items, want 2", len(ids))
	}
}

func TestGetImportedItems_Empty(t *testing.T) {
	m := testModule(t)
	ids, err := m.getImportedItems(context.Background(), "nonexistent", "page")
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

// --- deleteMediaFiles ---

func TestDeleteMediaFiles_NoPanic(t *testing.T) {
	m := testModule(t)
	// Random UUID that doesn't exist on disk — os.RemoveAll on missing dirs is a no-op.
	m.deleteMediaFiles("00000000-0000-0000-0000-000000000000")
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
