// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package react_headless

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// testModule creates a test Module with database access.
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()
	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m
}

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "react_headless" {
		t.Errorf("Name() = %q, want react_headless", m.Name())
	}
	if m.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want 1.0.0", m.Version())
	}
	if m.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestModuleAdminURL(t *testing.T) {
	m := New()

	if got := m.AdminURL(); got != "/admin/react-headless" {
		t.Errorf("AdminURL() = %q, want /admin/react-headless", got)
	}
}

func TestModuleSidebarLabel(t *testing.T) {
	m := New()

	if got := m.SidebarLabel(); got != "React Headless" {
		t.Errorf("SidebarLabel() = %q, want 'React Headless'", got)
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()
	moduleutil.AssertMigrations(t, m.Migrations(), 1)
}

func TestModuleTemplateFuncs(t *testing.T) {
	m := New()

	funcs := m.TemplateFuncs()
	if funcs != nil {
		t.Error("TemplateFuncs() should return nil")
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if m.settings == nil {
		t.Error("settings should be initialized after Init")
	}
	if m.ctx == nil {
		t.Error("ctx should be set after Init")
	}
}

func TestModuleInit_DefaultSettings(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	settings := m.GetSettings()
	if settings.AllowedOrigins != "http://localhost:5173" {
		t.Errorf("default AllowedOrigins = %q, want http://localhost:5173", settings.AllowedOrigins)
	}
	if settings.MaxAge != 3600 {
		t.Errorf("default MaxAge = %d, want 3600", settings.MaxAge)
	}
	if settings.AllowCredentials {
		t.Error("default AllowCredentials should be false")
	}
}

func TestModuleShutdown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestModuleShutdown_NilCtx(t *testing.T) {
	m := New()

	// Shutdown with nil ctx should not panic
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown with nil ctx: %v", err)
	}
}

func TestGetSettings_Nil(t *testing.T) {
	m := New()
	m.settings = nil

	s := m.GetSettings()
	if s.AllowedOrigins != "" || s.AllowCredentials || s.MaxAge != 0 {
		t.Error("GetSettings with nil should return zero-value Settings")
	}
}

func TestGetSettings_Copy(t *testing.T) {
	m := New()
	m.settings = &Settings{
		AllowedOrigins:   "http://example.com",
		AllowCredentials: true,
		MaxAge:           7200,
	}

	s := m.GetSettings()
	if s.AllowedOrigins != "http://example.com" {
		t.Errorf("AllowedOrigins = %q, want http://example.com", s.AllowedOrigins)
	}
	if !s.AllowCredentials {
		t.Error("AllowCredentials should be true")
	}
	if s.MaxAge != 7200 {
		t.Errorf("MaxAge = %d, want 7200", s.MaxAge)
	}
}

func TestGetAllowedOrigins(t *testing.T) {
	tests := []struct {
		name     string
		settings *Settings
		want     []string
	}{
		{
			name:     "nil settings",
			settings: nil,
			want:     nil,
		},
		{
			name:     "empty origins",
			settings: &Settings{AllowedOrigins: ""},
			want:     nil,
		},
		{
			name:     "single origin",
			settings: &Settings{AllowedOrigins: "http://localhost:5173"},
			want:     []string{"http://localhost:5173"},
		},
		{
			name:     "multiple origins",
			settings: &Settings{AllowedOrigins: "http://localhost:5173, http://example.com"},
			want:     []string{"http://localhost:5173", "http://example.com"},
		},
		{
			name:     "origins with extra spaces",
			settings: &Settings{AllowedOrigins: "  http://localhost:5173 ,  http://example.com  "},
			want:     []string{"http://localhost:5173", "http://example.com"},
		},
		{
			name:     "wildcard origin",
			settings: &Settings{AllowedOrigins: "*"},
			want:     []string{"*"},
		},
		{
			name:     "origins with empty entries",
			settings: &Settings{AllowedOrigins: "http://localhost:5173,,http://example.com"},
			want:     []string{"http://localhost:5173", "http://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.settings = tt.settings

			got := m.GetAllowedOrigins()
			if len(got) != len(tt.want) {
				t.Fatalf("GetAllowedOrigins() = %v (len %d), want %v (len %d)", got, len(got), tt.want, len(tt.want))
			}
			for i, o := range got {
				if o != tt.want[i] {
					t.Errorf("origin[%d] = %q, want %q", i, o, tt.want[i])
				}
			}
		})
	}
}

func TestIsOriginAllowed(t *testing.T) {
	tests := []struct {
		name    string
		origins string
		origin  string
		want    bool
	}{
		{
			name:    "exact match",
			origins: "http://localhost:5173",
			origin:  "http://localhost:5173",
			want:    true,
		},
		{
			name:    "case insensitive match",
			origins: "http://LOCALHOST:5173",
			origin:  "http://localhost:5173",
			want:    true,
		},
		{
			name:    "no match",
			origins: "http://localhost:5173",
			origin:  "http://example.com",
			want:    false,
		},
		{
			name:    "wildcard allows all",
			origins: "*",
			origin:  "http://anything.com",
			want:    true,
		},
		{
			name:    "multiple origins match second",
			origins: "http://localhost:3000, http://localhost:5173",
			origin:  "http://localhost:5173",
			want:    true,
		},
		{
			name:    "empty origins",
			origins: "",
			origin:  "http://localhost:5173",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.settings = &Settings{AllowedOrigins: tt.origins}

			got := m.isOriginAllowed(tt.origin)
			if got != tt.want {
				t.Errorf("isOriginAllowed(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := testModule(t, db)

	newSettings := &Settings{
		AllowedOrigins:   "http://example.com, http://app.example.com",
		AllowCredentials: true,
		MaxAge:           7200,
	}

	if err := m.saveSettings(newSettings); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	loaded, err := m.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}

	if loaded.AllowedOrigins != newSettings.AllowedOrigins {
		t.Errorf("AllowedOrigins = %q, want %q", loaded.AllowedOrigins, newSettings.AllowedOrigins)
	}
	if loaded.AllowCredentials != newSettings.AllowCredentials {
		t.Errorf("AllowCredentials = %v, want %v", loaded.AllowCredentials, newSettings.AllowCredentials)
	}
	if loaded.MaxAge != newSettings.MaxAge {
		t.Errorf("MaxAge = %d, want %d", loaded.MaxAge, newSettings.MaxAge)
	}
}

func TestMigrationUpDown(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()

	m := New()
	moduleutil.RunMigrations(t, db, m.Migrations())

	// Verify table exists by inserting and querying
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM react_headless_settings").Scan(&count)
	if err != nil {
		t.Fatalf("query after migration up: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 default row, got %d", count)
	}

	// Run migration down
	moduleutil.RunMigrationsDown(t, db, m.Migrations())
	moduleutil.AssertTableNotExists(t, db, "react_headless_settings")
}

func TestCORSMiddleware_InactiveModule(t *testing.T) {
	m := New()
	m.settings = &Settings{AllowedOrigins: "http://localhost:5173"}

	// isActive returns false
	middleware := m.GetCORSMiddleware(func(_ string) bool { return false })

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/pages", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should NOT have CORS headers when module is inactive
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set CORS headers when module is inactive")
	}
}

func TestCORSMiddleware_NoOriginHeader(t *testing.T) {
	m := New()
	m.settings = &Settings{AllowedOrigins: "http://localhost:5173"}

	middleware := m.GetCORSMiddleware(func(_ string) bool { return true })

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/pages", nil)
	// No Origin header
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should NOT have CORS headers when no Origin header
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set CORS headers when no Origin header")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	m := New()
	m.settings = &Settings{
		AllowedOrigins:   "http://localhost:5173",
		AllowCredentials: false,
		MaxAge:           3600,
	}

	middleware := m.GetCORSMiddleware(func(_ string) bool { return true })

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/pages", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Access-Control-Allow-Origin = %q, want http://localhost:5173", got)
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
	if got := rr.Header().Get("Access-Control-Expose-Headers"); got != "X-Total-Count, X-Page, X-Per-Page" {
		t.Errorf("Access-Control-Expose-Headers = %q, want 'X-Total-Count, X-Page, X-Per-Page'", got)
	}
	// No credentials header when AllowCredentials is false
	if rr.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("should not set Access-Control-Allow-Credentials when disabled")
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	m := New()
	m.settings = &Settings{AllowedOrigins: "http://localhost:5173"}

	middleware := m.GetCORSMiddleware(func(_ string) bool { return true })

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/pages", nil)
	req.Header.Set("Origin", "http://evil.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should NOT have CORS headers for disallowed origin
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set CORS headers for disallowed origin")
	}
	// But request should still be served
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	m := New()
	m.settings = &Settings{
		AllowedOrigins:   "http://localhost:5173",
		AllowCredentials: true,
		MaxAge:           7200,
	}

	middleware := m.GetCORSMiddleware(func(_ string) bool { return true })

	nextCalled := false
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("OPTIONS", "/api/v1/pages", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Preflight should return 204
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	// Should NOT call next handler
	if nextCalled {
		t.Error("preflight should not call next handler")
	}
	// Check headers
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Access-Control-Allow-Origin = %q, want http://localhost:5173", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, DELETE, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type, X-Requested-With" {
		t.Errorf("Access-Control-Allow-Headers = %q", got)
	}
	if got := rr.Header().Get("Access-Control-Max-Age"); got != "7200" {
		t.Errorf("Access-Control-Max-Age = %q, want 7200", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

func TestCORSMiddleware_PreflightDefaultMaxAge(t *testing.T) {
	m := New()
	m.settings = &Settings{
		AllowedOrigins: "http://localhost:5173",
		MaxAge:         0, // Zero should default to 3600
	}

	middleware := m.GetCORSMiddleware(func(_ string) bool { return true })

	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest("OPTIONS", "/api/v1/pages", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Errorf("Access-Control-Max-Age = %q, want 3600 (default)", got)
	}
}

func TestCORSMiddleware_WithCredentials(t *testing.T) {
	m := New()
	m.settings = &Settings{
		AllowedOrigins:   "http://localhost:5173",
		AllowCredentials: true,
	}

	middleware := m.GetCORSMiddleware(func(_ string) bool { return true })

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/pages", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

func TestCORSMiddleware_WildcardOrigin(t *testing.T) {
	m := New()
	m.settings = &Settings{AllowedOrigins: "*"}

	middleware := m.GetCORSMiddleware(func(_ string) bool { return true })

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/pages", nil)
	req.Header.Set("Origin", "http://any-domain.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://any-domain.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want http://any-domain.com", got)
	}
}

func TestDependencies(t *testing.T) {
	m := New()

	deps := m.Dependencies()
	if len(deps) != 0 {
		t.Errorf("Dependencies() = %v, want nil or empty", deps)
	}
}

func TestRegisterRoutes(t *testing.T) {
	m := New()

	// RegisterRoutes should not panic with nil router behavior
	// (it's a no-op for this module)
	m.RegisterRoutes(nil)
}

func TestTranslationsFS(t *testing.T) {
	m := New()
	fs := m.TranslationsFS()

	// Should be able to read the locales directory
	entries, err := fs.ReadDir("locales")
	if err != nil {
		t.Fatalf("ReadDir(locales): %v", err)
	}

	if len(entries) < 2 {
		t.Errorf("expected at least 2 locale directories, got %d", len(entries))
	}

	// Check English translations exist
	data, err := fs.ReadFile("locales/en/messages.json")
	if err != nil {
		t.Fatalf("ReadFile(en/messages.json): %v", err)
	}
	if len(data) == 0 {
		t.Error("English translations file is empty")
	}

	// Check Russian translations exist
	data, err = fs.ReadFile("locales/ru/messages.json")
	if err != nil {
		t.Fatalf("ReadFile(ru/messages.json): %v", err)
	}
	if len(data) == 0 {
		t.Error("Russian translations file is empty")
	}
}
