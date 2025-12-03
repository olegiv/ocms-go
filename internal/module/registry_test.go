package module

import (
	"database/sql"
	"html/template"
	"log/slog"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
)

// mockModule is a mock implementation of the Module interface for testing.
type mockModule struct {
	name         string
	version      string
	description  string
	dependencies []string
	migrations   []Migration
	initCalled   bool
	shutdownErr  error
	routesCalled bool
	adminCalled  bool
	funcMap      template.FuncMap
}

func newMockModule(name, version string) *mockModule {
	return &mockModule{
		name:    name,
		version: version,
		funcMap: make(template.FuncMap),
	}
}

func (m *mockModule) Name() string                     { return m.name }
func (m *mockModule) Version() string                  { return m.version }
func (m *mockModule) Description() string              { return m.description }
func (m *mockModule) Dependencies() []string           { return m.dependencies }
func (m *mockModule) Migrations() []Migration          { return m.migrations }
func (m *mockModule) Init(ctx *ModuleContext) error    { m.initCalled = true; return nil }
func (m *mockModule) Shutdown() error                  { return m.shutdownErr }
func (m *mockModule) RegisterRoutes(r chi.Router)      { m.routesCalled = true }
func (m *mockModule) RegisterAdminRoutes(r chi.Router) { m.adminCalled = true }
func (m *mockModule) TemplateFuncs() template.FuncMap  { return m.funcMap }
func (m *mockModule) AdminURL() string                 { return "" }

// errorModule returns an error on Init for testing error handling.
type errorModule struct {
	*mockModule
	initErr error
}

func (m *errorModule) Init(ctx *ModuleContext) error { return m.initErr }

func createTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	return db
}

func TestNewRegistry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	if r == nil {
		t.Fatal("expected registry to be non-nil")
	}

	if r.modules == nil {
		t.Error("expected modules map to be initialized")
	}

	if r.order == nil {
		t.Error("expected order slice to be initialized")
	}
}

func TestRegister(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m := newMockModule("test", "1.0.0")

	err := r.Register(m)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if r.Count() != 1 {
		t.Errorf("expected 1 module, got %d", r.Count())
	}
}

func TestRegisterDuplicate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("test", "1.0.0")
	m2 := newMockModule("test", "2.0.0") // same name

	if err := r.Register(m1); err != nil {
		t.Fatalf("failed to register first module: %v", err)
	}

	err := r.Register(m2)
	if err == nil {
		t.Error("expected error for duplicate module")
	}
}

func TestGet(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m := newMockModule("gettest", "1.0.0")
	_ = r.Register(m)

	// Get existing module
	found, ok := r.Get("gettest")
	if !ok {
		t.Error("expected to find module")
	}
	if found.Name() != "gettest" {
		t.Errorf("expected name 'gettest', got %s", found.Name())
	}

	// Get nonexistent module
	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent module")
	}
}

func TestList(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("mod1", "1.0.0")
	m2 := newMockModule("mod2", "1.0.0")
	m3 := newMockModule("mod3", "1.0.0")

	_ = r.Register(m1)
	_ = r.Register(m2)
	_ = r.Register(m3)

	list := r.List()
	if len(list) != 3 {
		t.Errorf("expected 3 modules, got %d", len(list))
	}

	// Verify order is preserved
	if list[0].Name() != "mod1" || list[1].Name() != "mod2" || list[2].Name() != "mod3" {
		t.Error("expected modules in registration order")
	}
}

func TestCount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	if r.Count() != 0 {
		t.Errorf("expected 0 modules initially, got %d", r.Count())
	}

	_ = r.Register(newMockModule("m1", "1.0.0"))
	_ = r.Register(newMockModule("m2", "1.0.0"))

	if r.Count() != 2 {
		t.Errorf("expected 2 modules, got %d", r.Count())
	}
}

func TestInitAll(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("init1", "1.0.0")
	m2 := newMockModule("init2", "1.0.0")

	_ = r.Register(m1)
	_ = r.Register(m2)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}
	err := r.InitAll(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !m1.initCalled {
		t.Error("expected m1 Init to be called")
	}
	if !m2.initCalled {
		t.Error("expected m2 Init to be called")
	}
}

func TestInitAllWithDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("base", "1.0.0")
	m2 := newMockModule("dependent", "1.0.0")
	m2.dependencies = []string{"base"}

	_ = r.Register(m1)
	_ = r.Register(m2)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}
	err := r.InitAll(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestInitAllMissingDependency(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m := newMockModule("dependent", "1.0.0")
	m.dependencies = []string{"nonexistent"}

	_ = r.Register(m)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}
	err := r.InitAll(ctx)
	if err == nil {
		t.Error("expected error for missing dependency")
	}
}

func TestShutdownAll(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("shut1", "1.0.0")
	m2 := newMockModule("shut2", "1.0.0")

	_ = r.Register(m1)
	_ = r.Register(m2)

	db := createTestDB(t)
	defer db.Close()

	// Initialize first
	ctx := &ModuleContext{DB: db, Logger: logger}
	_ = r.InitAll(ctx)

	// Shutdown
	err := r.ShutdownAll()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestRouteAll(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("route1", "1.0.0")
	m2 := newMockModule("route2", "1.0.0")

	_ = r.Register(m1)
	_ = r.Register(m2)

	router := chi.NewRouter()
	r.RouteAll(router)

	if !m1.routesCalled {
		t.Error("expected m1 RegisterRoutes to be called")
	}
	if !m2.routesCalled {
		t.Error("expected m2 RegisterRoutes to be called")
	}
}

func TestAdminRouteAll(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("admin1", "1.0.0")
	m2 := newMockModule("admin2", "1.0.0")

	_ = r.Register(m1)
	_ = r.Register(m2)

	router := chi.NewRouter()
	r.AdminRouteAll(router)

	if !m1.adminCalled {
		t.Error("expected m1 RegisterAdminRoutes to be called")
	}
	if !m2.adminCalled {
		t.Error("expected m2 RegisterAdminRoutes to be called")
	}
}

func TestAllTemplateFuncs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m1 := newMockModule("func1", "1.0.0")
	m1.funcMap = template.FuncMap{"func1": func() string { return "1" }}

	m2 := newMockModule("func2", "1.0.0")
	m2.funcMap = template.FuncMap{"func2": func() string { return "2" }}

	_ = r.Register(m1)
	_ = r.Register(m2)

	funcs := r.AllTemplateFuncs()

	if _, ok := funcs["func1"]; !ok {
		t.Error("expected func1 to be in combined funcs")
	}
	if _, ok := funcs["func2"]; !ok {
		t.Error("expected func2 to be in combined funcs")
	}
}

func TestListInfo(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m := newMockModule("info", "1.0.0")
	m.description = "Test description"

	_ = r.Register(m)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}
	_ = r.InitAll(ctx)

	infos := r.ListInfo()
	if len(infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(infos))
	}

	info := infos[0]
	if info.Name != "info" {
		t.Errorf("expected name 'info', got %s", info.Name)
	}
	if info.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %s", info.Version)
	}
	if info.Description != "Test description" {
		t.Errorf("expected description 'Test description', got %s", info.Description)
	}
	if !info.Initialized {
		t.Error("expected initialized to be true")
	}
}

func TestMigrations(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m := newMockModule("migrate", "1.0.0")
	migrationCalled := false
	m.migrations = []Migration{
		{
			Version:     1,
			Description: "Create test table",
			Up: func(db *sql.DB) error {
				migrationCalled = true
				_, err := db.Exec("CREATE TABLE test_table (id INTEGER PRIMARY KEY)")
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec("DROP TABLE test_table")
				return err
			},
		},
	}

	_ = r.Register(m)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}
	err := r.InitAll(ctx)
	if err != nil {
		t.Fatalf("failed to init: %v", err)
	}

	if !migrationCalled {
		t.Error("expected migration to be called")
	}

	// Verify table was created
	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table'").Scan(&name)
	if err != nil {
		t.Errorf("expected test_table to exist: %v", err)
	}
}

func TestMigrationNotRerun(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	runCount := 0
	m := newMockModule("rerun", "1.0.0")
	m.migrations = []Migration{
		{
			Version:     1,
			Description: "Test migration",
			Up: func(db *sql.DB) error {
				runCount++
				return nil
			},
		},
	}

	_ = r.Register(m)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}

	// First init - should run migration
	_ = r.InitAll(ctx)
	if runCount != 1 {
		t.Errorf("expected migration to run once, ran %d times", runCount)
	}

	// Create new registry and init again - should not rerun
	r2 := NewRegistry(logger)
	m2 := newMockModule("rerun", "1.0.0")
	m2.migrations = []Migration{
		{
			Version:     1,
			Description: "Test migration",
			Up: func(db *sql.DB) error {
				runCount++
				return nil
			},
		},
	}
	_ = r2.Register(m2)
	_ = r2.InitAll(ctx)

	if runCount != 1 {
		t.Errorf("expected migration not to rerun, ran %d times", runCount)
	}
}

func TestGetMigrationInfo(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	m := newMockModule("miginfo", "1.0.0")
	m.migrations = []Migration{
		{
			Version:     1,
			Description: "First migration",
			Up:          func(db *sql.DB) error { return nil },
		},
		{
			Version:     2,
			Description: "Second migration",
			Up:          func(db *sql.DB) error { return nil },
		},
	}

	_ = r.Register(m)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}
	_ = r.InitAll(ctx)

	infos, err := r.GetMigrationInfo("miginfo")
	if err != nil {
		t.Fatalf("failed to get migration info: %v", err)
	}

	if len(infos) != 2 {
		t.Errorf("expected 2 migrations, got %d", len(infos))
	}

	if !infos[0].Applied || !infos[1].Applied {
		t.Error("expected both migrations to be marked as applied")
	}
}

func TestGetMigrationInfoNotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	_, err := r.GetMigrationInfo("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent module")
	}
}

func TestBaseModule(t *testing.T) {
	base := NewBaseModule("test", "1.0.0", "Test module")

	if base.Name() != "test" {
		t.Errorf("expected name 'test', got %s", base.Name())
	}
	if base.Version() != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %s", base.Version())
	}
	if base.Description() != "Test module" {
		t.Errorf("expected description 'Test module', got %s", base.Description())
	}
	if base.Dependencies() != nil {
		t.Error("expected nil dependencies")
	}
	if base.Migrations() != nil {
		t.Error("expected nil migrations")
	}
	if base.TemplateFuncs() != nil {
		t.Error("expected nil template funcs")
	}

	// Init should work
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx := &ModuleContext{Logger: logger}
	if err := base.Init(ctx); err != nil {
		t.Errorf("expected no error from Init, got %v", err)
	}

	if base.Context() != ctx {
		t.Error("expected context to be stored")
	}

	// Shutdown should work
	if err := base.Shutdown(); err != nil {
		t.Errorf("expected no error from Shutdown, got %v", err)
	}

	// Route registration should not panic
	router := chi.NewRouter()
	base.RegisterRoutes(router)
	base.RegisterAdminRoutes(router)
}

func TestMultipleMigrations(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	r := NewRegistry(logger)

	order := []int{}
	m := newMockModule("multi", "1.0.0")
	m.migrations = []Migration{
		{
			Version:     1,
			Description: "First",
			Up:          func(db *sql.DB) error { order = append(order, 1); return nil },
		},
		{
			Version:     2,
			Description: "Second",
			Up:          func(db *sql.DB) error { order = append(order, 2); return nil },
		},
		{
			Version:     3,
			Description: "Third",
			Up:          func(db *sql.DB) error { order = append(order, 3); return nil },
		},
	}

	_ = r.Register(m)

	db := createTestDB(t)
	defer db.Close()

	ctx := &ModuleContext{DB: db, Logger: logger}
	_ = r.InitAll(ctx)

	if len(order) != 3 {
		t.Errorf("expected 3 migrations, got %d", len(order))
	}

	// Verify order
	for i, v := range order {
		if v != i+1 {
			t.Errorf("expected migration %d at position %d, got %d", i+1, i, v)
		}
	}
}
