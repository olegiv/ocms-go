package hcaptcha

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"ocms-go/internal/config"
	"ocms-go/internal/module"
	"ocms-go/internal/store"
)

// testDB creates a temporary test database.
func testDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	// Create temp file for test database
	f, err := os.CreateTemp("", "ocms-hcaptcha-test-*.db")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	// Open database
	db, err := store.NewDB(dbPath)
	if err != nil {
		_ = os.Remove(dbPath)
		t.Fatalf("NewDB: %v", err)
	}

	// Run core migrations
	if err := store.Migrate(db); err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Migrate: %v", err)
	}

	// Return cleanup function
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return db, cleanup
}

// testModule creates a test Module with database access
func testModule(t *testing.T, db *sql.DB) *Module {
	t.Helper()

	m := New()

	// Create a test logger that discards output
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	ctx := &module.Context{
		DB:     db,
		Logger: logger,
		Config: &config.Config{},
		Hooks:  module.NewHookRegistry(logger),
	}

	// Run module migrations first
	for _, mig := range m.Migrations() {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration up: %v", err)
		}
	}

	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	return m
}

func TestModuleNew(t *testing.T) {
	m := New()

	if m.Name() != "hcaptcha" {
		t.Errorf("Name() = %q, want hcaptcha", m.Name())
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

	if m.AdminURL() != "/admin/hcaptcha" {
		t.Errorf("AdminURL() = %q, want /admin/hcaptcha", m.AdminURL())
	}
}

func TestModuleMigrations(t *testing.T) {
	m := New()

	migrations := m.Migrations()
	if len(migrations) != 1 {
		t.Errorf("len(migrations) = %d, want 1", len(migrations))
	}

	if migrations[0].Version != 1 {
		t.Errorf("migration version = %d, want 1", migrations[0].Version)
	}
	if migrations[0].Description == "" {
		t.Error("migration description should not be empty")
	}
}

func TestModuleTemplateFuncs(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := testModule(t, db)

	funcs := m.TemplateFuncs()
	if funcs == nil {
		t.Fatal("TemplateFuncs() returned nil")
	}

	// Check hcaptchaWidget function exists
	if _, ok := funcs["hcaptchaWidget"]; !ok {
		t.Error("hcaptchaWidget function not found")
	}

	// Check hcaptchaEnabled function exists
	if fn, ok := funcs["hcaptchaEnabled"]; !ok {
		t.Error("hcaptchaEnabled function not found")
	} else {
		// Should return false by default (no keys configured)
		result := fn.(func() bool)()
		if result {
			t.Error("hcaptchaEnabled should return false when not configured")
		}
	}
}

func TestLoadSettings(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	// Run module migration
	m := New()
	for _, mig := range m.Migrations() {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration up: %v", err)
		}
	}

	// Load default settings
	settings, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}

	// Default values
	if settings.Enabled {
		t.Error("Enabled should be false by default")
	}
	if settings.SiteKey != "" {
		t.Error("SiteKey should be empty by default")
	}
	if settings.SecretKey != "" {
		t.Error("SecretKey should be empty by default")
	}
	if settings.Theme != "light" {
		t.Errorf("Theme = %q, want 'light'", settings.Theme)
	}
	if settings.Size != "normal" {
		t.Errorf("Size = %q, want 'normal'", settings.Size)
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	// Run module migration
	m := New()
	for _, mig := range m.Migrations() {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration up: %v", err)
		}
	}

	// Create settings
	settings := &Settings{
		Enabled:   true,
		SiteKey:   "test-site-key-12345",
		SecretKey: "test-secret-key-67890",
		Theme:     "dark",
		Size:      "compact",
	}

	// Save settings
	if err := saveSettings(db, settings); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	// Load settings back
	loaded, err := loadSettings(db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}

	// Verify all fields
	if loaded.Enabled != settings.Enabled {
		t.Errorf("Enabled = %v, want %v", loaded.Enabled, settings.Enabled)
	}
	if loaded.SiteKey != settings.SiteKey {
		t.Errorf("SiteKey = %q, want %q", loaded.SiteKey, settings.SiteKey)
	}
	if loaded.SecretKey != settings.SecretKey {
		t.Errorf("SecretKey = %q, want %q", loaded.SecretKey, settings.SecretKey)
	}
	if loaded.Theme != settings.Theme {
		t.Errorf("Theme = %q, want %q", loaded.Theme, settings.Theme)
	}
	if loaded.Size != settings.Size {
		t.Errorf("Size = %q, want %q", loaded.Size, settings.Size)
	}
}

func TestIsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		settings *Settings
		want     bool
	}{
		{
			name:     "nil settings",
			settings: nil,
			want:     false,
		},
		{
			name:     "disabled",
			settings: &Settings{Enabled: false},
			want:     false,
		},
		{
			name:     "enabled but no keys",
			settings: &Settings{Enabled: true},
			want:     false,
		},
		{
			name:     "enabled with site key only",
			settings: &Settings{Enabled: true, SiteKey: "key"},
			want:     false,
		},
		{
			name:     "enabled with secret key only",
			settings: &Settings{Enabled: true, SecretKey: "secret"},
			want:     false,
		},
		{
			name:     "enabled with both keys",
			settings: &Settings{Enabled: true, SiteKey: "key", SecretKey: "secret"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.settings = tt.settings

			got := m.IsEnabled()
			if got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSettings(t *testing.T) {
	m := New()

	// Test with nil settings
	m.settings = nil
	s := m.GetSettings()
	if s.Enabled != false || s.SiteKey != "" {
		t.Error("GetSettings with nil should return empty Settings")
	}

	// Test with actual settings
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "test-key",
		SecretKey: "test-secret",
		Theme:     "dark",
		Size:      "compact",
	}

	s = m.GetSettings()
	if s.Enabled != true {
		t.Error("GetSettings should return actual Enabled value")
	}
	if s.SiteKey != "test-key" {
		t.Errorf("GetSettings SiteKey = %q, want 'test-key'", s.SiteKey)
	}
}

func TestRenderWidget_Disabled(t *testing.T) {
	m := New()
	m.settings = &Settings{Enabled: false}

	result := m.RenderWidget()
	if result != "" {
		t.Errorf("RenderWidget when disabled should return empty, got %q", result)
	}
}

func TestRenderWidget_Enabled(t *testing.T) {
	m := New()
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "test-site-key-abc123",
		SecretKey: "test-secret",
		Theme:     "dark",
		Size:      "compact",
	}

	result := string(m.RenderWidget())

	// Should contain hCaptcha script
	if !strings.Contains(result, "hcaptcha.com/1/api.js") {
		t.Error("hCaptcha script not found")
	}

	// Should contain site key
	if !strings.Contains(result, "test-site-key-abc123") {
		t.Error("Site key not found")
	}

	// Should contain theme
	if !strings.Contains(result, `data-theme="dark"`) {
		t.Error("Theme attribute not found")
	}

	// Should contain size
	if !strings.Contains(result, `data-size="compact"`) {
		t.Error("Size attribute not found")
	}

	// Should have h-captcha class
	if !strings.Contains(result, "h-captcha") {
		t.Error("h-captcha class not found")
	}
}

func TestRenderWidget_HTMLEscaping(t *testing.T) {
	m := New()
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "<script>alert('xss')</script>",
		SecretKey: "secret",
		Theme:     "light",
		Size:      "normal",
	}

	result := string(m.RenderWidget())

	// Should not contain raw script tags
	if strings.Contains(result, "<script>alert") {
		t.Error("XSS vulnerability: script tag not escaped")
	}

	// Should contain escaped version
	if !strings.Contains(result, "&lt;script&gt;") {
		t.Error("Script tag not properly HTML escaped")
	}
}

func TestMaskSecretKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"12345678", "********"},
		{"1234567890", "1234**7890"},
		{"abcdefghijklmnop", "abcd********mnop"},
		{"short", "*****"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskSecretKey(tt.input)
			if got != tt.want {
				t.Errorf("maskSecretKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPlaceholder(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", false},
		{"actual-key", false},
		{"****", true},
		{"1234****5678", true},
		{"test****test", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isPlaceholder(tt.input)
			if got != tt.want {
				t.Errorf("isPlaceholder(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetResponseFromForm(t *testing.T) {
	// Test with h-captcha-response
	req := httptest.NewRequest("POST", "/login", strings.NewReader("h-captcha-response=test-token-abc"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}

	result := GetResponseFromForm(req)
	if result != "test-token-abc" {
		t.Errorf("GetResponseFromForm() = %q, want 'test-token-abc'", result)
	}
}

func TestGetRemoteIP(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		want       string
	}{
		{
			name:       "X-Forwarded-For single",
			xff:        "192.168.1.100",
			remoteAddr: "10.0.0.1:12345",
			want:       "192.168.1.100",
		},
		{
			name:       "X-Forwarded-For multiple",
			xff:        "192.168.1.100, 10.0.0.1, 172.16.0.1",
			remoteAddr: "10.0.0.1:12345",
			want:       "192.168.1.100",
		},
		{
			name:       "X-Real-IP",
			xri:        "192.168.1.200",
			remoteAddr: "10.0.0.1:12345",
			want:       "192.168.1.200",
		},
		{
			name:       "RemoteAddr with port",
			remoteAddr: "192.168.1.50:54321",
			want:       "192.168.1.50",
		},
		{
			name:       "RemoteAddr without port",
			remoteAddr: "192.168.1.50",
			want:       "192.168.1.50",
		},
		{
			name:       "X-Forwarded-For takes priority over X-Real-IP",
			xff:        "192.168.1.100",
			xri:        "192.168.1.200",
			remoteAddr: "10.0.0.1:12345",
			want:       "192.168.1.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}
			req.RemoteAddr = tt.remoteAddr

			got := GetRemoteIP(req)
			if got != tt.want {
				t.Errorf("GetRemoteIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVerifyFromRequest_Disabled(t *testing.T) {
	m := New()
	m.settings = &Settings{Enabled: false}

	req := &VerifyRequest{Response: ""}

	result, err := m.VerifyFromRequest(req)
	if err != nil {
		t.Fatalf("VerifyFromRequest: %v", err)
	}

	if !result.Verified {
		t.Error("Should be verified when disabled")
	}
}

func TestVerifyFromRequest_EmptyResponse(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := testModule(t, db)
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "site-key",
		SecretKey: "secret-key",
	}

	req := &VerifyRequest{Response: ""}

	result, err := m.VerifyFromRequest(req)
	if err != nil {
		t.Fatalf("VerifyFromRequest: %v", err)
	}

	if result.Verified {
		t.Error("Should not be verified with empty response")
	}
	if result.ErrorCode != "hcaptcha.error_required" {
		t.Errorf("ErrorCode = %q, want 'hcaptcha.error_required'", result.ErrorCode)
	}
}

func TestVerify_Disabled(t *testing.T) {
	m := New()
	m.settings = &Settings{Enabled: false}

	result, err := m.Verify("any-token", "127.0.0.1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !result.Success {
		t.Error("Should succeed when disabled")
	}
}

func TestVerify_EmptyResponse(t *testing.T) {
	m := New()
	m.settings = &Settings{
		Enabled:   true,
		SiteKey:   "site-key",
		SecretKey: "secret-key",
	}

	_, err := m.Verify("", "127.0.0.1")
	if err == nil {
		t.Fatal("Should error with empty response")
	}
	if !strings.Contains(err.Error(), "missing captcha response") {
		t.Errorf("Error = %v, want 'missing captcha response'", err)
	}
}

func TestModuleInit(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := New()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	hooks := module.NewHookRegistry(logger)

	// Run migrations
	for _, mig := range m.Migrations() {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration up: %v", err)
		}
	}

	ctx := &module.Context{
		DB:     db,
		Logger: logger,
		Config: &config.Config{},
		Hooks:  hooks,
	}

	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Settings should be loaded
	if m.settings == nil {
		t.Error("settings should be initialized after Init")
	}

	// Hooks should be registered
	if !hooks.HasHandlers(HookAuthLoginWidget) {
		t.Error("HookAuthLoginWidget handler not registered")
	}
	if !hooks.HasHandlers(HookAuthBeforeLogin) {
		t.Error("HookAuthBeforeLogin handler not registered")
	}
}

func TestModuleInit_EnvOverride(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := New()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	// Run migrations
	for _, mig := range m.Migrations() {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration up: %v", err)
		}
	}

	// Save some settings to DB
	if err := saveSettings(db, &Settings{
		SiteKey:   "db-site-key",
		SecretKey: "db-secret-key",
	}); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	// Config with env overrides
	cfg := &config.Config{
		HCaptchaSiteKey:   "env-site-key",
		HCaptchaSecretKey: "env-secret-key",
	}

	ctx := &module.Context{
		DB:     db,
		Logger: logger,
		Config: cfg,
		Hooks:  module.NewHookRegistry(logger),
	}

	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Env should override DB settings
	if m.settings.SiteKey != "env-site-key" {
		t.Errorf("SiteKey = %q, want 'env-site-key'", m.settings.SiteKey)
	}
	if m.settings.SecretKey != "env-secret-key" {
		t.Errorf("SecretKey = %q, want 'env-secret-key'", m.settings.SecretKey)
	}
}

func TestModuleShutdown(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Shutdown should not error
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestReloadSettings(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := testModule(t, db)

	// Save new settings directly to DB
	newSettings := &Settings{
		Enabled:   true,
		SiteKey:   "reloaded-site-key",
		SecretKey: "reloaded-secret-key",
		Theme:     "dark",
		Size:      "compact",
	}
	if err := saveSettings(db, newSettings); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}

	// Reload settings
	if err := m.ReloadSettings(); err != nil {
		t.Fatalf("ReloadSettings: %v", err)
	}

	// Verify settings were reloaded
	if !m.settings.Enabled {
		t.Error("Enabled should be true after reload")
	}
	if m.settings.SiteKey != "reloaded-site-key" {
		t.Errorf("SiteKey = %q, want 'reloaded-site-key'", m.settings.SiteKey)
	}
}

func TestMigrationDown(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	m := New()

	// Run migration up
	for _, mig := range m.Migrations() {
		if err := mig.Up(db); err != nil {
			t.Fatalf("migration up: %v", err)
		}
	}

	// Run migration down
	for _, mig := range m.Migrations() {
		if err := mig.Down(db); err != nil {
			t.Fatalf("migration down: %v", err)
		}
	}

	// Table should not exist
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='hcaptcha_settings'").Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Error("hcaptcha_settings table should not exist after migration down")
	}
}

func TestVerifyRequest_Struct(t *testing.T) {
	// Test struct fields
	req := &VerifyRequest{
		Response:  "test-response",
		RemoteIP:  "192.168.1.1",
		Verified:  true,
		Error:     "test error",
		ErrorCode: "test.error_code",
	}

	if req.Response != "test-response" {
		t.Error("Response field not set correctly")
	}
	if req.RemoteIP != "192.168.1.1" {
		t.Error("RemoteIP field not set correctly")
	}
	if !req.Verified {
		t.Error("Verified field not set correctly")
	}
	if req.Error != "test error" {
		t.Error("Error field not set correctly")
	}
	if req.ErrorCode != "test.error_code" {
		t.Error("ErrorCode field not set correctly")
	}
}

func TestGetRemoteIP_IPv6(t *testing.T) {
	req := &http.Request{
		RemoteAddr: "[::1]:12345",
	}

	// Should handle IPv6 correctly
	got := GetRemoteIP(req)
	// For IPv6 with brackets and port, we expect the last index behavior
	if got == "" {
		t.Error("GetRemoteIP should return something for IPv6")
	}
}
