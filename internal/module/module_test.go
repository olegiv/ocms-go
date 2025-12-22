package module

import (
	"database/sql"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestNewBaseModule(t *testing.T) {
	base := NewBaseModule("test-module", "1.0.0", "A test module")

	if base.name != "test-module" {
		t.Errorf("name = %q, want %q", base.name, "test-module")
	}
	if base.version != "1.0.0" {
		t.Errorf("version = %q, want %q", base.version, "1.0.0")
	}
	if base.description != "A test module" {
		t.Errorf("description = %q, want %q", base.description, "A test module")
	}
}

func TestBaseModuleName(t *testing.T) {
	base := NewBaseModule("my-module", "2.0.0", "Description")
	if name := base.Name(); name != "my-module" {
		t.Errorf("Name() = %q, want %q", name, "my-module")
	}
}

func TestBaseModuleVersion(t *testing.T) {
	base := NewBaseModule("my-module", "2.5.0", "Description")
	if version := base.Version(); version != "2.5.0" {
		t.Errorf("Version() = %q, want %q", version, "2.5.0")
	}
}

func TestBaseModuleDescription(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "My module description")
	if desc := base.Description(); desc != "My module description" {
		t.Errorf("Description() = %q, want %q", desc, "My module description")
	}
}

func TestBaseModuleDependencies(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	deps := base.Dependencies()
	if deps != nil {
		t.Errorf("Dependencies() = %v, want nil", deps)
	}
}

func TestBaseModuleInit(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")

	// Context is nil initially
	if ctx := base.Context(); ctx != nil {
		t.Error("Context() should be nil before Init")
	}

	// Init with mock context
	mockCtx := &Context{}
	err := base.Init(mockCtx)
	if err != nil {
		t.Errorf("Init() error = %v", err)
	}

	// Context should be set after Init
	if ctx := base.Context(); ctx != mockCtx {
		t.Error("Context() should return the context passed to Init")
	}
}

func TestBaseModuleShutdown(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	err := base.Shutdown()
	if err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}

func TestBaseModuleRegisterRoutes(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	r := chi.NewRouter()

	// Should not panic
	base.RegisterRoutes(r)
}

func TestBaseModuleRegisterAdminRoutes(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	r := chi.NewRouter()

	// Should not panic
	base.RegisterAdminRoutes(r)
}

func TestBaseModuleTemplateFuncs(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	funcs := base.TemplateFuncs()
	if funcs != nil {
		t.Errorf("TemplateFuncs() = %v, want nil", funcs)
	}
}

func TestBaseModuleMigrations(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	migrations := base.Migrations()
	if migrations != nil {
		t.Errorf("Migrations() = %v, want nil", migrations)
	}
}

func TestBaseModuleAdminURL(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	url := base.AdminURL()
	if url != "" {
		t.Errorf("AdminURL() = %q, want empty", url)
	}
}

func TestBaseModuleSidebarLabel(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	label := base.SidebarLabel()
	if label != "" {
		t.Errorf("SidebarLabel() = %q, want empty", label)
	}
}

func TestBaseModuleTranslationsFS(t *testing.T) {
	base := NewBaseModule("my-module", "1.0.0", "Description")
	fs := base.TranslationsFS()

	// Should return empty embed.FS (not nil, but empty)
	entries, err := fs.ReadDir(".")
	// This should return an error because the empty embed.FS has no root
	if err == nil && len(entries) > 0 {
		t.Error("TranslationsFS() should return empty embed.FS")
	}
}

func TestMigrationStruct(t *testing.T) {
	migration := Migration{
		Version:     1,
		Description: "Create users table",
		Up:          func(db *sql.DB) error { return nil },
		Down:        func(db *sql.DB) error { return nil },
	}

	if migration.Version != 1 {
		t.Errorf("Version = %d, want 1", migration.Version)
	}
	if migration.Description != "Create users table" {
		t.Errorf("Description = %q, want %q", migration.Description, "Create users table")
	}
	if migration.Up == nil {
		t.Error("Up should not be nil")
	}
	if migration.Down == nil {
		t.Error("Down should not be nil")
	}
}

func TestContextStruct(t *testing.T) {
	ctx := &Context{}

	// All fields should be nil/zero by default
	if ctx.DB != nil {
		t.Error("DB should be nil")
	}
	if ctx.Store != nil {
		t.Error("Store should be nil")
	}
	if ctx.Logger != nil {
		t.Error("Logger should be nil")
	}
	if ctx.Config != nil {
		t.Error("Config should be nil")
	}
	if ctx.Render != nil {
		t.Error("Render should be nil")
	}
	if ctx.Events != nil {
		t.Error("Events should be nil")
	}
	if ctx.Hooks != nil {
		t.Error("Hooks should be nil")
	}
}
