package theme

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// createTestTheme creates a test theme in a temporary directory.
func createTestTheme(t *testing.T, themesDir, themeName string, config ThemeConfig) string {
	t.Helper()

	themePath := filepath.Join(themesDir, themeName)
	if err := os.MkdirAll(themePath, 0755); err != nil {
		t.Fatalf("failed to create theme dir: %v", err)
	}

	// Create theme.json
	configData, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themePath, "theme.json"), configData, 0644); err != nil {
		t.Fatalf("failed to write theme.json: %v", err)
	}

	// Create templates directory
	templatesPath := filepath.Join(themePath, "templates")
	if err := os.MkdirAll(filepath.Join(templatesPath, "layouts"), 0755); err != nil {
		t.Fatalf("failed to create layouts dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(templatesPath, "partials"), 0755); err != nil {
		t.Fatalf("failed to create partials dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(templatesPath, "pages"), 0755); err != nil {
		t.Fatalf("failed to create pages dir: %v", err)
	}

	// Create base layout
	baseLayout := `<!DOCTYPE html>
<html>
<head><title>{{.Title}}</title></head>
<body>
{{template "content" .}}
</body>
</html>`
	if err := os.WriteFile(filepath.Join(templatesPath, "layouts", "base.html"), []byte(baseLayout), 0644); err != nil {
		t.Fatalf("failed to write base.html: %v", err)
	}

	// Create header partial
	headerPartial := `<header>Header</header>`
	if err := os.WriteFile(filepath.Join(templatesPath, "partials", "header.html"), []byte(headerPartial), 0644); err != nil {
		t.Fatalf("failed to write header.html: %v", err)
	}

	// Create home page
	homePage := `{{define "content"}}
<h1>Home Page</h1>
{{template "header.html" .}}
{{end}}`
	if err := os.WriteFile(filepath.Join(templatesPath, "pages", "home.html"), []byte(homePage), 0644); err != nil {
		t.Fatalf("failed to write home.html: %v", err)
	}

	// Create static directory
	staticPath := filepath.Join(themePath, "static")
	if err := os.MkdirAll(filepath.Join(staticPath, "css"), 0755); err != nil {
		t.Fatalf("failed to create static/css dir: %v", err)
	}

	return themePath
}

func TestNewManager(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager("/tmp/themes", logger)

	if m == nil {
		t.Fatal("expected manager to be non-nil")
	}

	if m.themesDir != "/tmp/themes" {
		t.Errorf("expected themesDir /tmp/themes, got %s", m.themesDir)
	}

	if m.themes == nil {
		t.Error("expected themes map to be initialized")
	}
}

func TestLoadThemes(t *testing.T) {
	// Create temp directory
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create test themes
	config1 := ThemeConfig{
		Name:        "Test Theme 1",
		Version:     "1.0.0",
		Author:      "Test Author",
		Description: "Test theme description",
	}
	createTestTheme(t, themesDir, "test1", config1)

	config2 := ThemeConfig{
		Name:        "Test Theme 2",
		Version:     "2.0.0",
		Author:      "Another Author",
		Description: "Another description",
	}
	createTestTheme(t, themesDir, "test2", config2)

	// Create manager and load themes
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	// Verify themes loaded
	if m.ThemeCount() != 2 {
		t.Errorf("expected 2 themes, got %d", m.ThemeCount())
	}

	if !m.HasTheme("test1") {
		t.Error("expected theme test1 to be loaded")
	}

	if !m.HasTheme("test2") {
		t.Error("expected theme test2 to be loaded")
	}
}

func TestLoadThemesNonExistentDirectory(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager("/nonexistent/path", logger)

	// Should not error, just log a warning
	if err := m.LoadThemes(); err != nil {
		t.Errorf("expected no error for nonexistent dir, got %v", err)
	}

	if m.ThemeCount() != 0 {
		t.Errorf("expected 0 themes, got %d", m.ThemeCount())
	}
}

func TestSetActiveTheme(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	config := ThemeConfig{Name: "Test", Version: "1.0.0"}
	createTestTheme(t, themesDir, "test", config)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	// Set active theme
	if err := m.SetActiveTheme("test"); err != nil {
		t.Errorf("failed to set active theme: %v", err)
	}

	// Verify active theme
	active := m.GetActiveTheme()
	if active == nil {
		t.Fatal("expected active theme to be set")
	}

	if active.Name != "test" {
		t.Errorf("expected active theme name 'test', got %s", active.Name)
	}
}

func TestSetActiveThemeNotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager("/tmp/themes", logger)

	err := m.SetActiveTheme("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent theme")
	}
}

func TestGetTheme(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	config := ThemeConfig{
		Name:    "Get Test",
		Version: "1.0.0",
		Author:  "Author",
	}
	createTestTheme(t, themesDir, "gettest", config)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	// Get existing theme
	theme, err := m.GetTheme("gettest")
	if err != nil {
		t.Errorf("failed to get theme: %v", err)
	}

	if theme.Config.Name != "Get Test" {
		t.Errorf("expected theme name 'Get Test', got %s", theme.Config.Name)
	}

	// Get nonexistent theme
	_, err = m.GetTheme("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent theme")
	}
}

func TestListThemes(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	createTestTheme(t, themesDir, "theme1", ThemeConfig{Name: "Theme 1", Version: "1.0.0"})
	createTestTheme(t, themesDir, "theme2", ThemeConfig{Name: "Theme 2", Version: "2.0.0"})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	configs := m.ListThemes()
	if len(configs) != 2 {
		t.Errorf("expected 2 theme configs, got %d", len(configs))
	}
}

func TestListThemesWithActive(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	createTestTheme(t, themesDir, "active", ThemeConfig{Name: "Active Theme", Version: "1.0.0"})
	createTestTheme(t, themesDir, "inactive", ThemeConfig{Name: "Inactive Theme", Version: "1.0.0"})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	if err := m.SetActiveTheme("active"); err != nil {
		t.Fatalf("failed to set active theme: %v", err)
	}

	infos := m.ListThemesWithActive()
	if len(infos) != 2 {
		t.Errorf("expected 2 theme infos, got %d", len(infos))
	}

	// Find active theme
	var activeFound, inactiveFound bool
	for _, info := range infos {
		if info.Name == "active" {
			activeFound = true
			if !info.IsActive {
				t.Error("expected 'active' theme to be marked as active")
			}
		}
		if info.Name == "inactive" {
			inactiveFound = true
			if info.IsActive {
				t.Error("expected 'inactive' theme to not be marked as active")
			}
		}
	}

	if !activeFound {
		t.Error("active theme not found in list")
	}
	if !inactiveFound {
		t.Error("inactive theme not found in list")
	}
}

func TestReloadTheme(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Initial config
	config := ThemeConfig{Name: "Original", Version: "1.0.0"}
	createTestTheme(t, themesDir, "reload", config)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	// Verify initial name
	theme, _ := m.GetTheme("reload")
	if theme.Config.Name != "Original" {
		t.Errorf("expected initial name 'Original', got %s", theme.Config.Name)
	}

	// Update config
	newConfig := ThemeConfig{Name: "Updated", Version: "2.0.0"}
	configData, _ := json.Marshal(newConfig)
	if err := os.WriteFile(filepath.Join(themesDir, "reload", "theme.json"), configData, 0644); err != nil {
		t.Fatalf("failed to update theme.json: %v", err)
	}

	// Reload theme
	if err := m.ReloadTheme("reload"); err != nil {
		t.Fatalf("failed to reload theme: %v", err)
	}

	// Verify updated name
	theme, _ = m.GetTheme("reload")
	if theme.Config.Name != "Updated" {
		t.Errorf("expected updated name 'Updated', got %s", theme.Config.Name)
	}
}

func TestReloadActiveTheme(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	config := ThemeConfig{Name: "Active Reload", Version: "1.0.0"}
	createTestTheme(t, themesDir, "active-reload", config)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	if err := m.SetActiveTheme("active-reload"); err != nil {
		t.Fatalf("failed to set active theme: %v", err)
	}

	// Update and reload
	newConfig := ThemeConfig{Name: "Updated Active", Version: "2.0.0"}
	configData, _ := json.Marshal(newConfig)
	if err := os.WriteFile(filepath.Join(themesDir, "active-reload", "theme.json"), configData, 0644); err != nil {
		t.Fatalf("failed to update theme.json: %v", err)
	}

	if err := m.ReloadTheme("active-reload"); err != nil {
		t.Fatalf("failed to reload theme: %v", err)
	}

	// Verify active theme was updated
	active := m.GetActiveTheme()
	if active.Config.Name != "Updated Active" {
		t.Errorf("expected active theme name 'Updated Active', got %s", active.Config.Name)
	}
}

func TestHasTheme(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	createTestTheme(t, themesDir, "exists", ThemeConfig{Name: "Exists", Version: "1.0.0"})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	if !m.HasTheme("exists") {
		t.Error("expected HasTheme to return true for existing theme")
	}

	if m.HasTheme("nonexistent") {
		t.Error("expected HasTheme to return false for nonexistent theme")
	}
}

func TestThemesDir(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager("/custom/themes/path", logger)

	if m.ThemesDir() != "/custom/themes/path" {
		t.Errorf("expected ThemesDir '/custom/themes/path', got %s", m.ThemesDir())
	}
}

func TestSetFuncMap(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	config := ThemeConfig{Name: "FuncMap Test", Version: "1.0.0"}
	createTestTheme(t, themesDir, "funcmap", config)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	// Set function map before loading
	funcMap := map[string]any{
		"upper": func(s string) string { return s },
	}
	m.SetFuncMap(funcMap)

	// Load themes (should use the func map)
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	// Theme should be loaded successfully with func map
	if !m.HasTheme("funcmap") {
		t.Error("expected theme to be loaded with custom func map")
	}
}

func TestInvalidThemeJson(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create theme with invalid JSON
	themePath := filepath.Join(themesDir, "invalid")
	if err := os.MkdirAll(themePath, 0755); err != nil {
		t.Fatalf("failed to create theme dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themePath, "theme.json"), []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("failed to write invalid theme.json: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	// Should not error, just skip invalid theme
	if err := m.LoadThemes(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if m.HasTheme("invalid") {
		t.Error("expected invalid theme to be skipped")
	}
}

func TestMissingThemeJson(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create theme directory without theme.json
	themePath := filepath.Join(themesDir, "missing")
	if err := os.MkdirAll(themePath, 0755); err != nil {
		t.Fatalf("failed to create theme dir: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	// Should not error, just skip theme without config
	if err := m.LoadThemes(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if m.HasTheme("missing") {
		t.Error("expected theme without config to be skipped")
	}
}

func TestThemeWithSettings(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	config := ThemeConfig{
		Name:    "Settings Theme",
		Version: "1.0.0",
		Settings: []ThemeSetting{
			{
				Key:     "primary_color",
				Label:   "Primary Color",
				Type:    "color",
				Default: "#3b82f6",
			},
			{
				Key:     "show_sidebar",
				Label:   "Show Sidebar",
				Type:    "select",
				Default: "yes",
				Options: []string{"yes", "no"},
			},
		},
	}
	createTestTheme(t, themesDir, "settings", config)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	theme, _ := m.GetTheme("settings")
	if len(theme.Config.Settings) != 2 {
		t.Errorf("expected 2 settings, got %d", len(theme.Config.Settings))
	}

	// Verify settings
	if theme.Config.Settings[0].Key != "primary_color" {
		t.Error("expected first setting to be primary_color")
	}
	if theme.Config.Settings[1].Options[0] != "yes" {
		t.Error("expected show_sidebar options to include 'yes'")
	}
}

func TestThemeCount(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if m.ThemeCount() != 0 {
		t.Errorf("expected 0 themes initially, got %d", m.ThemeCount())
	}

	createTestTheme(t, themesDir, "theme1", ThemeConfig{Name: "Theme 1", Version: "1.0.0"})
	createTestTheme(t, themesDir, "theme2", ThemeConfig{Name: "Theme 2", Version: "1.0.0"})
	createTestTheme(t, themesDir, "theme3", ThemeConfig{Name: "Theme 3", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	if m.ThemeCount() != 3 {
		t.Errorf("expected 3 themes, got %d", m.ThemeCount())
	}
}
