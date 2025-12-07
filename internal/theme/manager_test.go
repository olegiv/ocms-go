package theme

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"ocms-go/internal/i18n"
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

// createTestThemeWithLocales creates a test theme with translation files.
func createTestThemeWithLocales(t *testing.T, themesDir, themeName string, config ThemeConfig, translations map[string]map[string]string) string {
	t.Helper()

	// Create base theme first
	themePath := createTestTheme(t, themesDir, themeName, config)

	// Create locales directory and translation files
	for lang, msgs := range translations {
		localeDir := filepath.Join(themePath, "locales", lang)
		if err := os.MkdirAll(localeDir, 0755); err != nil {
			t.Fatalf("failed to create locale dir: %v", err)
		}

		// Build messages array for JSON
		var messages []map[string]string
		for id, translation := range msgs {
			messages = append(messages, map[string]string{
				"id":          id,
				"message":     translation,
				"translation": translation,
			})
		}

		msgFile := map[string]any{
			"language": lang,
			"messages": messages,
		}

		data, err := json.Marshal(msgFile)
		if err != nil {
			t.Fatalf("failed to marshal locale: %v", err)
		}

		if err := os.WriteFile(filepath.Join(localeDir, "messages.json"), data, 0644); err != nil {
			t.Fatalf("failed to write messages.json: %v", err)
		}
	}

	return themePath
}

func TestThemeTranslate(t *testing.T) {
	theme := &Theme{
		Name: "test",
		Translations: map[string]map[string]string{
			"en": {
				"frontend.read_more": "Continue reading",
				"frontend.home":      "Home",
			},
			"ru": {
				"frontend.read_more": "Продолжить чтение",
			},
		},
	}

	// Test existing translation
	trans, ok := theme.Translate("en", "frontend.read_more")
	if !ok {
		t.Error("expected translation to be found")
	}
	if trans != "Continue reading" {
		t.Errorf("expected 'Continue reading', got %s", trans)
	}

	// Test Russian translation
	trans, ok = theme.Translate("ru", "frontend.read_more")
	if !ok {
		t.Error("expected Russian translation to be found")
	}
	if trans != "Продолжить чтение" {
		t.Errorf("expected 'Продолжить чтение', got %s", trans)
	}

	// Test missing key
	_, ok = theme.Translate("en", "nonexistent.key")
	if ok {
		t.Error("expected translation not to be found for nonexistent key")
	}

	// Test missing language
	_, ok = theme.Translate("de", "frontend.read_more")
	if ok {
		t.Error("expected translation not to be found for unsupported language")
	}

	// Test nil translations
	nilTheme := &Theme{Name: "nil"}
	_, ok = nilTheme.Translate("en", "any.key")
	if ok {
		t.Error("expected translation not to be found for theme with nil translations")
	}
}

func TestLoadThemeWithTranslations(t *testing.T) {
	// Initialize i18n first (required for theme translations)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := i18n.Init(logger); err != nil {
		t.Fatalf("failed to init i18n: %v", err)
	}

	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create theme with translations
	translations := map[string]map[string]string{
		"en": {
			"frontend.read_more": "Continue reading →",
			"frontend.home":      "Home Page",
		},
		"ru": {
			"frontend.read_more": "Продолжить →",
		},
	}
	createTestThemeWithLocales(t, themesDir, "translated", ThemeConfig{Name: "Translated Theme", Version: "1.0.0"}, translations)

	m := NewManager(themesDir, logger)
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	theme, err := m.GetTheme("translated")
	if err != nil {
		t.Fatalf("failed to get theme: %v", err)
	}

	// Verify translations were loaded
	if theme.Translations == nil {
		t.Fatal("expected translations to be loaded")
	}

	if len(theme.Translations) != 2 {
		t.Errorf("expected 2 languages, got %d", len(theme.Translations))
	}

	// Check English translation
	trans, ok := theme.Translate("en", "frontend.read_more")
	if !ok {
		t.Error("expected English translation to be found")
	}
	if trans != "Continue reading →" {
		t.Errorf("expected 'Continue reading →', got %s", trans)
	}

	// Check Russian translation
	trans, ok = theme.Translate("ru", "frontend.read_more")
	if !ok {
		t.Error("expected Russian translation to be found")
	}
	if trans != "Продолжить →" {
		t.Errorf("expected 'Продолжить →', got %s", trans)
	}
}

func TestLoadThemeWithoutTranslations(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create theme without locales directory
	createTestTheme(t, themesDir, "no-locales", ThemeConfig{Name: "No Locales Theme", Version: "1.0.0"})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	theme, err := m.GetTheme("no-locales")
	if err != nil {
		t.Fatalf("failed to get theme: %v", err)
	}

	// Theme should load successfully with nil translations
	if theme.Translations != nil {
		t.Error("expected translations to be nil for theme without locales")
	}
}

func TestManagerTranslateWithFallback(t *testing.T) {
	// Initialize i18n
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := i18n.Init(logger); err != nil {
		t.Fatalf("failed to init i18n: %v", err)
	}

	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create theme with partial translations (only overrides some keys)
	translations := map[string]map[string]string{
		"en": {
			"frontend.read_more": "Theme Override",
		},
	}
	createTestThemeWithLocales(t, themesDir, "partial", ThemeConfig{Name: "Partial Theme", Version: "1.0.0"}, translations)

	m := NewManager(themesDir, logger)
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	if err := m.SetActiveTheme("partial"); err != nil {
		t.Fatalf("failed to set active theme: %v", err)
	}

	// Test theme-specific translation (should return theme override)
	result := m.Translate("en", "frontend.read_more")
	if result != "Theme Override" {
		t.Errorf("expected 'Theme Override', got %s", result)
	}

	// Test global fallback (key not in theme translations)
	// This should fall back to global i18n
	result = m.Translate("en", "nav.dashboard")
	if result != "Dashboard" {
		t.Errorf("expected 'Dashboard' from global i18n, got %s", result)
	}
}

func TestManagerTranslateNoActiveTheme(t *testing.T) {
	// Initialize i18n
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := i18n.Init(logger); err != nil {
		t.Fatalf("failed to init i18n: %v", err)
	}

	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	m := NewManager(themesDir, logger)

	// No active theme - should fall back to global i18n
	result := m.Translate("en", "nav.dashboard")
	if result != "Dashboard" {
		t.Errorf("expected 'Dashboard' from global i18n, got %s", result)
	}
}

func TestManagerTemplateFuncs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager("/tmp/themes", logger)

	funcs := m.TemplateFuncs()

	// Verify TTheme function exists
	ttheme, ok := funcs["TTheme"]
	if !ok {
		t.Fatal("expected TTheme function to be in TemplateFuncs")
	}

	// Verify it's callable
	if ttheme == nil {
		t.Error("expected TTheme to be non-nil")
	}
}

func TestManagerTranslateWithArgs(t *testing.T) {
	// Initialize i18n
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := i18n.Init(logger); err != nil {
		t.Fatalf("failed to init i18n: %v", err)
	}

	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create theme with a translation containing format placeholder
	translations := map[string]map[string]string{
		"en": {
			"greeting": "Hello, %s!",
		},
	}
	createTestThemeWithLocales(t, themesDir, "args", ThemeConfig{Name: "Args Theme", Version: "1.0.0"}, translations)

	m := NewManager(themesDir, logger)
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	if err := m.SetActiveTheme("args"); err != nil {
		t.Fatalf("failed to set active theme: %v", err)
	}

	// Test translation with arguments
	result := m.Translate("en", "greeting", "World")
	if result != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %s", result)
	}
}

func TestInvalidThemeLocaleJson(t *testing.T) {
	themesDir, err := os.MkdirTemp("", "ocms-themes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(themesDir)

	// Create theme first
	createTestTheme(t, themesDir, "invalid-locale", ThemeConfig{Name: "Invalid Locale Theme", Version: "1.0.0"})

	// Create invalid locale file
	localeDir := filepath.Join(themesDir, "invalid-locale", "locales", "en")
	if err := os.MkdirAll(localeDir, 0755); err != nil {
		t.Fatalf("failed to create locale dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localeDir, "messages.json"), []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("failed to write invalid messages.json: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(themesDir, logger)

	// Should load theme but skip invalid locale
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("failed to load themes: %v", err)
	}

	theme, err := m.GetTheme("invalid-locale")
	if err != nil {
		t.Fatalf("failed to get theme: %v", err)
	}

	// Theme should be loaded with nil translations (invalid locale skipped)
	if theme.Translations != nil && len(theme.Translations) > 0 {
		t.Error("expected no translations loaded for theme with invalid locale")
	}
}
