// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/themes"
)

// emptyFS is an empty embed.FS for tests that don't need embedded themes.
var emptyFS embed.FS

// testCustomDir creates a temporary custom directory and registers cleanup.
func testCustomDir(t *testing.T) string {
	t.Helper()
	customDir := t.TempDir()
	// Create themes subdirectory
	if err := os.MkdirAll(filepath.Join(customDir, "themes"), 0755); err != nil {
		t.Fatalf("failed to create themes dir: %v", err)
	}
	return customDir
}

// testManager creates a theme manager with empty embedded FS and a temporary custom directory.
func testManager(t *testing.T) (*Manager, string) {
	t.Helper()
	customDir := testCustomDir(t)
	return NewManager(emptyFS, customDir, testutil.TestLoggerSilent()), customDir
}

// testManagerWithEmbedded creates a theme manager with the actual embedded themes.
func testManagerWithEmbedded(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(themes.FS, "", testutil.TestLoggerSilent())
	// Set up minimal template functions required by embedded themes
	m.SetFuncMap(testutil.MinimalThemeFuncMap())
	return m
}

// createTestTheme creates a test theme in a temporary directory.
func createTestTheme(t *testing.T, customDir, themeName string, config Config) string {
	t.Helper()

	themePath := filepath.Join(customDir, "themes", themeName)
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
	m := NewManager(emptyFS, "/tmp/custom", testutil.TestLoggerSilent())

	if m == nil {
		t.Fatal("expected manager to be non-nil")
	}
	if m.customDir != "/tmp/custom" {
		t.Errorf("customDir = %s, want /tmp/custom", m.customDir)
	}
	if m.themes == nil {
		t.Error("expected themes map to be initialized")
	}
}

func TestLoadEmbeddedThemes(t *testing.T) {
	m := testManagerWithEmbedded(t)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	// Should have the embedded default and developer themes
	if !m.HasTheme("default") {
		t.Error("expected embedded 'default' theme to be loaded")
	}
	if !m.HasTheme("developer") {
		t.Error("expected embedded 'developer' theme to be loaded")
	}

	// Check that embedded themes are marked as embedded
	if !m.IsEmbedded("default") {
		t.Error("expected 'default' theme to be marked as embedded")
	}
	if !m.IsEmbedded("developer") {
		t.Error("expected 'developer' theme to be marked as embedded")
	}
}

func TestLoadExternalThemes(t *testing.T) {
	m, customDir := testManager(t)

	createTestTheme(t, customDir, "test1", Config{Name: "Test Theme 1", Version: "1.0.0", Author: "Test Author"})
	createTestTheme(t, customDir, "test2", Config{Name: "Test Theme 2", Version: "2.0.0", Author: "Another Author"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	if m.ThemeCount() != 2 {
		t.Errorf("ThemeCount = %d, want 2", m.ThemeCount())
	}
	if !m.HasTheme("test1") {
		t.Error("expected theme test1 to be loaded")
	}
	if !m.HasTheme("test2") {
		t.Error("expected theme test2 to be loaded")
	}

	// External themes should not be marked as embedded
	if m.IsEmbedded("test1") {
		t.Error("expected 'test1' theme to not be marked as embedded")
	}
}

func TestExternalThemeOverridesEmbedded(t *testing.T) {
	customDir := testCustomDir(t)
	m := NewManager(themes.FS, customDir, testutil.TestLoggerSilent())

	// Create a custom theme that overrides the embedded 'default' theme
	createTestTheme(t, customDir, "default", Config{Name: "Custom Default", Version: "2.0.0", Author: "Override"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	// Should have the 'default' theme
	if !m.HasTheme("default") {
		t.Error("expected 'default' theme to be loaded")
	}

	// The custom version should override the embedded version
	theme, err := m.GetTheme("default")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	if theme.Config.Name != "Custom Default" {
		t.Errorf("theme.Config.Name = %s, want 'Custom Default'", theme.Config.Name)
	}
	if theme.IsEmbedded {
		t.Error("expected overridden 'default' theme to not be marked as embedded")
	}
}

func TestLoadThemesNoCustomDirectory(t *testing.T) {
	m := NewManager(themes.FS, "", testutil.TestLoggerSilent())
	m.SetFuncMap(testutil.MinimalThemeFuncMap())

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	// Should still load embedded themes
	if !m.HasTheme("default") {
		t.Error("expected embedded 'default' theme to be loaded")
	}
}

func TestSetActiveTheme(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "test", Config{Name: "Test", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if err := m.SetActiveTheme("test"); err != nil {
		t.Errorf("SetActiveTheme: %v", err)
	}

	active := m.GetActiveTheme()
	if active == nil {
		t.Fatal("expected active theme to be set")
	}
	if active.Name != "test" {
		t.Errorf("active.Name = %s, want test", active.Name)
	}
}

func TestSetActiveThemeNotFound(t *testing.T) {
	m := NewManager(emptyFS, "/tmp/custom", testutil.TestLoggerSilent())

	if err := m.SetActiveTheme("nonexistent"); err == nil {
		t.Error("expected error for nonexistent theme")
	}
}

func TestGetTheme(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "gettest", Config{Name: "Get Test", Version: "1.0.0", Author: "Author"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	theme, err := m.GetTheme("gettest")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}
	if theme == nil {
		t.Fatal("GetTheme returned nil theme")
	}
	if theme.Config.Name != "Get Test" {
		t.Errorf("theme.Config.Name = %s, want Get Test", theme.Config.Name)
	}

	if _, err = m.GetTheme("nonexistent"); err == nil {
		t.Error("expected error for nonexistent theme")
	}
}

func TestListThemes(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "theme1", Config{Name: "Theme 1", Version: "1.0.0"})
	createTestTheme(t, customDir, "theme2", Config{Name: "Theme 2", Version: "2.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	if len(m.ListThemes()) != 2 {
		t.Errorf("len(ListThemes) = %d, want 2", len(m.ListThemes()))
	}
}

func TestListThemesWithActive(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "active", Config{Name: "Active Theme", Version: "1.0.0"})
	createTestTheme(t, customDir, "inactive", Config{Name: "Inactive Theme", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if err := m.SetActiveTheme("active"); err != nil {
		t.Fatalf("SetActiveTheme: %v", err)
	}

	infos := m.ListThemesWithActive()
	if len(infos) != 2 {
		t.Errorf("len(infos) = %d, want 2", len(infos))
	}

	for _, info := range infos {
		if info.Name == "active" && !info.IsActive {
			t.Error("expected 'active' theme to be marked as active")
		}
		if info.Name == "inactive" && info.IsActive {
			t.Error("expected 'inactive' theme to not be marked as active")
		}
	}
}

func TestReloadTheme(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "reload", Config{Name: "Original", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	theme, err := m.GetTheme("reload")
	if err != nil || theme == nil {
		t.Fatalf("GetTheme failed: %v", err)
	}
	if theme.Config.Name != "Original" {
		t.Errorf("initial name = %s, want Original", theme.Config.Name)
	}

	// Update config on disk
	newConfig := Config{Name: "Updated", Version: "2.0.0"}
	configData, _ := json.Marshal(newConfig)
	if err := os.WriteFile(filepath.Join(customDir, "themes", "reload", "theme.json"), configData, 0644); err != nil {
		t.Fatalf("failed to update theme.json: %v", err)
	}

	if err := m.ReloadTheme("reload"); err != nil {
		t.Fatalf("ReloadTheme: %v", err)
	}

	theme, err = m.GetTheme("reload")
	if err != nil || theme == nil {
		t.Fatalf("GetTheme after reload failed: %v", err)
	}
	if theme.Config.Name != "Updated" {
		t.Errorf("updated name = %s, want Updated", theme.Config.Name)
	}
}

func TestReloadEmbeddedThemeFails(t *testing.T) {
	m := testManagerWithEmbedded(t)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	// Trying to reload an embedded theme should fail
	if err := m.ReloadTheme("default"); err == nil {
		t.Error("expected error when reloading embedded theme")
	}
}

func TestReloadActiveTheme(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "active-reload", Config{Name: "Active Reload", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if err := m.SetActiveTheme("active-reload"); err != nil {
		t.Fatalf("SetActiveTheme: %v", err)
	}

	// Update and reload
	newConfig := Config{Name: "Updated Active", Version: "2.0.0"}
	configData, _ := json.Marshal(newConfig)
	if err := os.WriteFile(filepath.Join(customDir, "themes", "active-reload", "theme.json"), configData, 0644); err != nil {
		t.Fatalf("failed to update theme.json: %v", err)
	}

	if err := m.ReloadTheme("active-reload"); err != nil {
		t.Fatalf("ReloadTheme: %v", err)
	}

	if active := m.GetActiveTheme(); active.Config.Name != "Updated Active" {
		t.Errorf("active.Config.Name = %s, want Updated Active", active.Config.Name)
	}
}

func TestHasTheme(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "exists", Config{Name: "Exists", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	if !m.HasTheme("exists") {
		t.Error("HasTheme(exists) = false, want true")
	}
	if m.HasTheme("nonexistent") {
		t.Error("HasTheme(nonexistent) = true, want false")
	}
}

func TestCustomDir(t *testing.T) {
	m := NewManager(emptyFS, "/custom/path", testutil.TestLoggerSilent())

	if m.CustomDir() != "/custom/path" {
		t.Errorf("CustomDir = %s, want /custom/path", m.CustomDir())
	}
}

func TestSetFuncMap(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "funcmap", Config{Name: "FuncMap Test", Version: "1.0.0"})

	m.SetFuncMap(map[string]any{"upper": func(s string) string { return s }})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if !m.HasTheme("funcmap") {
		t.Error("expected theme to be loaded with custom func map")
	}
}

func TestInvalidThemeJson(t *testing.T) {
	customDir := testCustomDir(t)

	// Create theme with invalid JSON
	themePath := filepath.Join(customDir, "themes", "invalid")
	if err := os.MkdirAll(themePath, 0755); err != nil {
		t.Fatalf("failed to create theme dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(themePath, "theme.json"), []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("failed to write invalid theme.json: %v", err)
	}

	m := NewManager(emptyFS, customDir, testutil.TestLoggerSilent())
	if err := m.LoadThemes(); err != nil {
		t.Errorf("LoadThemes: %v (expected no error)", err)
	}
	if m.HasTheme("invalid") {
		t.Error("expected invalid theme to be skipped")
	}
}

func TestMissingThemeJson(t *testing.T) {
	customDir := testCustomDir(t)

	// Create theme directory without theme.json
	if err := os.MkdirAll(filepath.Join(customDir, "themes", "missing"), 0755); err != nil {
		t.Fatalf("failed to create theme dir: %v", err)
	}

	m := NewManager(emptyFS, customDir, testutil.TestLoggerSilent())
	if err := m.LoadThemes(); err != nil {
		t.Errorf("LoadThemes: %v (expected no error)", err)
	}
	if m.HasTheme("missing") {
		t.Error("expected theme without config to be skipped")
	}
}

func TestThemeWithSettings(t *testing.T) {
	m, customDir := testManager(t)
	config := Config{
		Name:    "Settings Theme",
		Version: "1.0.0",
		Settings: []Setting{
			{Key: "primary_color", Label: "Primary Color", Type: "color", Default: "#3b82f6"},
			{Key: "show_sidebar", Label: "Show Sidebar", Type: "select", Default: "yes", Options: []string{"yes", "no"}},
		},
	}
	createTestTheme(t, customDir, "settings", config)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	theme, err := m.GetTheme("settings")
	if err != nil || theme == nil {
		t.Fatalf("GetTheme failed: %v", err)
	}
	if len(theme.Config.Settings) != 2 {
		t.Errorf("len(Settings) = %d, want 2", len(theme.Config.Settings))
	}
	if theme.Config.Settings[0].Key != "primary_color" {
		t.Error("expected first setting to be primary_color")
	}
	if theme.Config.Settings[1].Options[0] != "yes" {
		t.Error("expected show_sidebar options to include 'yes'")
	}
}

func TestEmbeddedDeveloperThemeSettings(t *testing.T) {
	m := testManagerWithEmbedded(t)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	theme, err := m.GetTheme("developer")
	if err != nil || theme == nil {
		t.Fatalf("GetTheme(developer): %v", err)
	}

	// Build a map of settings by key for easy lookup
	settings := make(map[string]Setting)
	for _, s := range theme.Config.Settings {
		settings[s.Key] = s
	}

	// Verify show_hero_text setting
	s, ok := settings["show_hero_text"]
	if !ok {
		t.Fatal("expected developer theme to have show_hero_text setting")
	}
	if s.Type != "select" {
		t.Errorf("show_hero_text type = %s, want select", s.Type)
	}
	if s.Default != "yes" {
		t.Errorf("show_hero_text default = %s, want yes", s.Default)
	}
	if len(s.Options) != 2 || s.Options[0] != "yes" || s.Options[1] != "no" {
		t.Errorf("show_hero_text options = %v, want [yes no]", s.Options)
	}

	// Verify hero_image setting
	s, ok = settings["hero_image"]
	if !ok {
		t.Fatal("expected developer theme to have hero_image setting")
	}
	if s.Type != "image" {
		t.Errorf("hero_image type = %s, want image", s.Type)
	}
	if s.Default != "" {
		t.Errorf("hero_image default = %q, want empty", s.Default)
	}

	// Verify hero_image_alt setting
	s, ok = settings["hero_image_alt"]
	if !ok {
		t.Fatal("expected developer theme to have hero_image_alt setting")
	}
	if s.Type != "text" {
		t.Errorf("hero_image_alt type = %s, want text", s.Type)
	}
	if s.Default != "" {
		t.Errorf("hero_image_alt default = %q, want empty", s.Default)
	}
}

func TestThemeCount(t *testing.T) {
	m, customDir := testManager(t)

	if m.ThemeCount() != 0 {
		t.Errorf("ThemeCount = %d initially, want 0", m.ThemeCount())
	}

	createTestTheme(t, customDir, "theme1", Config{Name: "Theme 1", Version: "1.0.0"})
	createTestTheme(t, customDir, "theme2", Config{Name: "Theme 2", Version: "1.0.0"})
	createTestTheme(t, customDir, "theme3", Config{Name: "Theme 3", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if m.ThemeCount() != 3 {
		t.Errorf("ThemeCount = %d, want 3", m.ThemeCount())
	}
}

// createTestThemeWithLocales creates a test theme with translation files.
func createTestThemeWithLocales(t *testing.T, customDir, themeName string, config Config, translations map[string]map[string]string) {
	t.Helper()

	// Create base theme first
	themePath := createTestTheme(t, customDir, themeName, config)

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
	if err := i18n.Init(testutil.TestLoggerSilent()); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}

	customDir := testCustomDir(t)
	translations := map[string]map[string]string{
		"en": {"frontend.read_more": "Continue reading →", "frontend.home": "Home Page"},
		"ru": {"frontend.read_more": "Продолжить →"},
	}
	createTestThemeWithLocales(t, customDir, "translated", Config{Name: "Translated Theme", Version: "1.0.0"}, translations)

	m := NewManager(emptyFS, customDir, testutil.TestLoggerSilent())
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	theme, err := m.GetTheme("translated")
	if err != nil {
		t.Fatalf("GetTheme: %v", err)
	}

	if theme.Translations == nil {
		t.Fatal("expected translations to be loaded")
	}
	if len(theme.Translations) != 2 {
		t.Errorf("len(Translations) = %d, want 2", len(theme.Translations))
	}

	if trans, ok := theme.Translate("en", "frontend.read_more"); !ok || trans != "Continue reading →" {
		t.Errorf("en translation = %q, want 'Continue reading →'", trans)
	}
	if trans, ok := theme.Translate("ru", "frontend.read_more"); !ok || trans != "Продолжить →" {
		t.Errorf("ru translation = %q, want 'Продолжить →'", trans)
	}
}

func TestLoadThemeWithoutTranslations(t *testing.T) {
	m, customDir := testManager(t)
	createTestTheme(t, customDir, "no-locales", Config{Name: "No Locales Theme", Version: "1.0.0"})

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	theme, err := m.GetTheme("no-locales")
	if err != nil || theme == nil {
		t.Fatalf("GetTheme failed: %v", err)
	}
	if theme.Translations != nil {
		t.Error("expected translations to be nil for theme without locales")
	}
}

func TestManagerTranslateWithFallback(t *testing.T) {
	if err := i18n.Init(testutil.TestLoggerSilent()); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}

	customDir := testCustomDir(t)
	translations := map[string]map[string]string{"en": {"frontend.read_more": "Theme Override"}}
	createTestThemeWithLocales(t, customDir, "partial", Config{Name: "Partial Theme", Version: "1.0.0"}, translations)

	m := NewManager(emptyFS, customDir, testutil.TestLoggerSilent())
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if err := m.SetActiveTheme("partial"); err != nil {
		t.Fatalf("SetActiveTheme: %v", err)
	}

	if result := m.Translate("en", "frontend.read_more"); result != "Theme Override" {
		t.Errorf("Translate = %s, want Theme Override", result)
	}
	if result := m.Translate("en", "nav.dashboard"); result != "Dashboard" {
		t.Errorf("Translate (fallback) = %s, want Dashboard", result)
	}
}

func TestManagerTranslateNoActiveTheme(t *testing.T) {
	if err := i18n.Init(testutil.TestLoggerSilent()); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}

	m := NewManager(emptyFS, testCustomDir(t), testutil.TestLoggerSilent())
	if result := m.Translate("en", "nav.dashboard"); result != "Dashboard" {
		t.Errorf("Translate = %s, want Dashboard", result)
	}
}

func TestManagerTemplateFuncs(t *testing.T) {
	m := NewManager(emptyFS, "/tmp/custom", testutil.TestLoggerSilent())

	funcs := m.TemplateFuncs()
	if ttheme, ok := funcs["TTheme"]; !ok || ttheme == nil {
		t.Fatal("expected TTheme function to be in TemplateFuncs")
	}
}

func TestManagerTranslateWithArgs(t *testing.T) {
	if err := i18n.Init(testutil.TestLoggerSilent()); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}

	customDir := testCustomDir(t)
	translations := map[string]map[string]string{"en": {"greeting": "Hello, %s!"}}
	createTestThemeWithLocales(t, customDir, "args", Config{Name: "Args Theme", Version: "1.0.0"}, translations)

	m := NewManager(emptyFS, customDir, testutil.TestLoggerSilent())
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if err := m.SetActiveTheme("args"); err != nil {
		t.Fatalf("SetActiveTheme: %v", err)
	}

	if result := m.Translate("en", "greeting", "World"); result != "Hello, World!" {
		t.Errorf("Translate = %s, want Hello, World!", result)
	}
}

func TestInvalidThemeLocaleJson(t *testing.T) {
	customDir := testCustomDir(t)
	createTestTheme(t, customDir, "invalid-locale", Config{Name: "Invalid Locale Theme", Version: "1.0.0"})

	// Create invalid locale file
	localeDir := filepath.Join(customDir, "themes", "invalid-locale", "locales", "en")
	if err := os.MkdirAll(localeDir, 0755); err != nil {
		t.Fatalf("failed to create locale dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localeDir, "messages.json"), []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("failed to write invalid messages.json: %v", err)
	}

	m := NewManager(emptyFS, customDir, testutil.TestLoggerSilent())
	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	theme, err := m.GetTheme("invalid-locale")
	if err != nil || theme == nil {
		t.Fatalf("GetTheme failed: %v", err)
	}
	if len(theme.Translations) > 0 {
		t.Error("expected no translations loaded for theme with invalid locale")
	}
}

func TestBlankLinesRegex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no blank lines", "line1\nline2\nline3", "line1\nline2\nline3"},
		{"one blank line", "line1\n\nline2", "line1\nline2"},
		{"multiple blank lines", "line1\n\n\n\nline2", "line1\nline2"},
		{"blank lines with spaces", "line1\n  \n\t\nline2", "line1\nline2"},
		{"windows line endings", "line1\r\n\r\n\r\nline2", "line1\nline2"},
		{"html output", "<div>\n\n\n<p>text</p>\n\n\n</div>", "<div>\n<p>text</p>\n</div>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(blankLinesRegex.ReplaceAll([]byte(tt.input), []byte("\n")))
			if got != tt.expected {
				t.Errorf("blankLinesRegex.ReplaceAll(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestEmbeddedFS(t *testing.T) {
	m := NewManager(themes.FS, "", testutil.TestLoggerSilent())

	embFS := m.EmbeddedFS()
	if _, err := embFS.ReadFile("default/theme.json"); err != nil {
		t.Errorf("expected to read embedded default/theme.json: %v", err)
	}
}

func TestIsEmbedded(t *testing.T) {
	m := testManagerWithEmbedded(t)

	if err := m.LoadThemes(); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}

	if !m.IsEmbedded("default") {
		t.Error("expected 'default' to be embedded")
	}
	if m.IsEmbedded("nonexistent") {
		t.Error("expected 'nonexistent' to not be embedded")
	}
}
