// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/olegiv/ocms-go/internal/i18n"
)

// Manager handles theme loading, switching, and rendering.
// It supports loading themes from both embedded filesystem (core themes)
// and external filesystem (custom themes). External themes override embedded
// themes with the same name.
type Manager struct {
	embeddedFS  embed.FS // Embedded core themes
	customDir   string   // Custom themes directory (e.g., "./custom/themes")
	activeTheme *Theme
	themes      map[string]*Theme
	mu          sync.RWMutex
	logger      *slog.Logger
	funcMap     template.FuncMap
}

// NewManager creates a new theme manager.
// embeddedFS contains core themes embedded in the binary.
// customDir is the path to custom/themes directory for user themes.
func NewManager(embeddedFS embed.FS, customDir string, logger *slog.Logger) *Manager {
	return &Manager{
		embeddedFS: embeddedFS,
		customDir:  customDir,
		themes:     make(map[string]*Theme),
		logger:     logger,
		funcMap:    make(template.FuncMap),
	}
}

// SetFuncMap sets the template function map to use when parsing templates.
func (m *Manager) SetFuncMap(funcMap template.FuncMap) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.funcMap = funcMap
}

// LoadThemes loads themes from both embedded and external sources.
// External themes override embedded themes with the same name.
func (m *Manager) LoadThemes() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// First, load embedded themes (core themes)
	if err := m.loadEmbeddedThemes(); err != nil {
		return fmt.Errorf("loading embedded themes: %w", err)
	}

	// Then, load external themes (custom themes override embedded)
	if err := m.loadExternalThemes(); err != nil {
		// Don't fail if custom directory doesn't exist
		m.logger.Debug("no custom themes loaded", "error", err)
	}

	m.logger.Info("themes loaded", "count", len(m.themes))
	return nil
}

// loadEmbeddedThemes loads themes from the embedded filesystem.
func (m *Manager) loadEmbeddedThemes() error {
	entries, err := fs.ReadDir(m.embeddedFS, ".")
	if err != nil {
		return fmt.Errorf("reading embedded themes: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		themeName := entry.Name()
		theme, err := m.loadEmbeddedTheme(themeName)
		if err != nil {
			m.logger.Warn("failed to load embedded theme", "theme", themeName, "error", err)
			continue
		}

		m.themes[themeName] = theme
		m.logger.Info("loaded embedded theme", "theme", themeName, "version", theme.Config.Version)
	}

	return nil
}

// loadExternalThemes loads themes from the custom directory.
// These override embedded themes with the same name.
func (m *Manager) loadExternalThemes() error {
	if m.customDir == "" {
		return nil
	}

	// Check if custom themes directory exists
	customThemesDir := filepath.Join(m.customDir, "themes")
	if _, err := os.Stat(customThemesDir); os.IsNotExist(err) {
		return nil // Not an error, just no custom themes
	}

	entries, err := os.ReadDir(customThemesDir)
	if err != nil {
		return fmt.Errorf("reading custom themes directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		themeName := entry.Name()
		themePath := filepath.Join(customThemesDir, themeName)

		theme, err := m.loadFilesystemTheme(themeName, themePath)
		if err != nil {
			m.logger.Warn("failed to load custom theme", "theme", themeName, "error", err)
			continue
		}

		// Check if this overrides an embedded theme
		if _, exists := m.themes[themeName]; exists {
			m.logger.Info("custom theme overrides embedded theme", "theme", themeName)
		}

		m.themes[themeName] = theme
		m.logger.Info("loaded custom theme", "theme", themeName, "version", theme.Config.Version)
	}

	return nil
}

// loadEmbeddedTheme loads a single theme from the embedded filesystem.
func (m *Manager) loadEmbeddedTheme(name string) (*Theme, error) {
	// Load theme.json from embedded FS
	configPath := name + "/theme.json"
	configData, err := fs.ReadFile(m.embeddedFS, configPath)
	if err != nil {
		return nil, fmt.Errorf("reading theme.json: %w", err)
	}

	var config Config
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("parsing theme.json: %w", err)
	}

	// Get theme subdirectory as fs.FS
	themeFS, err := fs.Sub(m.embeddedFS, name)
	if err != nil {
		return nil, fmt.Errorf("getting theme sub-filesystem: %w", err)
	}

	// Parse templates from embedded FS
	templates, err := m.parseTemplatesFromFS(themeFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	// Load theme-specific translations from embedded FS
	translations := m.loadThemeTranslationsFromFS(themeFS, "locales")

	return &Theme{
		Name:         name,
		Path:         "", // No filesystem path for embedded themes
		Config:       config,
		Templates:    templates,
		StaticPath:   "", // Static files served from embedded FS
		Translations: translations,
		IsEmbedded:   true,
		EmbeddedFS:   themeFS,
	}, nil
}

// loadFilesystemTheme loads a single theme from the filesystem.
func (m *Manager) loadFilesystemTheme(name, path string) (*Theme, error) {
	// Load theme.json
	configPath := filepath.Join(path, "theme.json")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading theme.json: %w", err)
	}

	var config Config
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("parsing theme.json: %w", err)
	}

	// Parse templates from filesystem
	templatesPath := filepath.Join(path, "templates")
	templates, err := m.parseTemplatesFromFilesystem(templatesPath)
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	// Load theme-specific translations from filesystem
	translations := m.loadThemeTranslationsFromFilesystem(path)

	return &Theme{
		Name:         name,
		Path:         path,
		Config:       config,
		Templates:    templates,
		StaticPath:   filepath.Join(path, "static"),
		Translations: translations,
		IsEmbedded:   false,
		EmbeddedFS:   nil,
	}, nil
}

// loadThemeTranslationsFromFS loads translations from an embedded filesystem.
func (m *Manager) loadThemeTranslationsFromFS(themeFS fs.FS, localesDir string) map[string]map[string]string {
	// Check if locales directory exists
	if _, err := fs.Stat(themeFS, localesDir); err != nil {
		return nil
	}

	translations := make(map[string]map[string]string)

	// Load translations for each supported language
	for _, lang := range i18n.SupportedLanguages {
		msgPath := localesDir + "/" + lang + "/messages.json"
		data, err := fs.ReadFile(themeFS, msgPath)
		if err != nil {
			// Skip if language file doesn't exist for this theme
			continue
		}

		var msgFile i18n.MessageFile
		if err := json.Unmarshal(data, &msgFile); err != nil {
			m.logger.Warn("failed to parse embedded theme translations",
				"path", msgPath, "error", err)
			continue
		}

		translations[lang] = make(map[string]string)
		for _, msg := range msgFile.Messages {
			translations[lang][msg.ID] = msg.Translation
		}

		m.logger.Debug("loaded embedded theme translations",
			"language", lang, "count", len(msgFile.Messages))
	}

	if len(translations) == 0 {
		return nil
	}

	return translations
}

// loadThemeTranslationsFromFilesystem loads translations from the filesystem.
func (m *Manager) loadThemeTranslationsFromFilesystem(themePath string) map[string]map[string]string {
	localesPath := filepath.Join(themePath, "locales")

	// Check if locales directory exists
	if _, err := os.Stat(localesPath); os.IsNotExist(err) {
		return nil
	}

	translations := make(map[string]map[string]string)

	// Load translations for each supported language
	for _, lang := range i18n.SupportedLanguages {
		msgPath := filepath.Join(localesPath, lang, "messages.json")
		data, err := os.ReadFile(msgPath)
		if err != nil {
			// Skip if language file doesn't exist for this theme
			continue
		}

		var msgFile i18n.MessageFile
		if err := json.Unmarshal(data, &msgFile); err != nil {
			m.logger.Warn("failed to parse theme translations",
				"path", msgPath, "error", err)
			continue
		}

		translations[lang] = make(map[string]string)
		for _, msg := range msgFile.Messages {
			translations[lang][msg.ID] = msg.Translation
		}

		m.logger.Debug("loaded theme translations",
			"theme", filepath.Base(themePath), "language", lang, "count", len(msgFile.Messages))
	}

	if len(translations) == 0 {
		return nil
	}

	return translations
}

// parseTemplatesFromFS parses templates from an embedded filesystem.
func (m *Manager) parseTemplatesFromFS(themeFS fs.FS, templatesDir string) (*template.Template, error) {
	// Check if templates directory exists
	if _, err := fs.Stat(themeFS, templatesDir); err != nil {
		// Return empty template if no templates directory
		return template.New("").Funcs(m.funcMap), nil
	}

	// Create root template with function map
	tmpl := template.New("").Funcs(m.funcMap)

	// Parse layouts
	layoutsDir := templatesDir + "/layouts"
	if entries, err := fs.ReadDir(themeFS, layoutsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".html" {
				continue
			}
			filePath := layoutsDir + "/" + entry.Name()
			content, err := fs.ReadFile(themeFS, filePath)
			if err != nil {
				return nil, fmt.Errorf("reading layout %s: %w", entry.Name(), err)
			}
			relPath := "layouts/" + entry.Name()
			if _, err := tmpl.New(relPath).Parse(string(content)); err != nil {
				return nil, fmt.Errorf("parsing layout %s: %w", relPath, err)
			}
		}
	}

	// Parse partials (named by filename only for {{template "header.html" .}})
	partialsDir := templatesDir + "/partials"
	if entries, err := fs.ReadDir(themeFS, partialsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".html" {
				continue
			}
			filePath := partialsDir + "/" + entry.Name()
			content, err := fs.ReadFile(themeFS, filePath)
			if err != nil {
				return nil, fmt.Errorf("reading partial %s: %w", entry.Name(), err)
			}
			// Use just the filename for partials
			if _, err := tmpl.New(entry.Name()).Parse(string(content)); err != nil {
				return nil, fmt.Errorf("parsing partial %s: %w", entry.Name(), err)
			}
		}
	}

	// Parse page templates
	pagesDir := templatesDir + "/pages"
	if entries, err := fs.ReadDir(themeFS, pagesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".html" {
				continue
			}
			filePath := pagesDir + "/" + entry.Name()
			content, err := fs.ReadFile(themeFS, filePath)
			if err != nil {
				return nil, fmt.Errorf("reading page %s: %w", entry.Name(), err)
			}
			if err := parsePageTemplate(tmpl, entry.Name(), content); err != nil {
				return nil, err
			}
		}
	}

	return tmpl, nil
}

// parseTemplatesFromFilesystem parses templates from the filesystem.
func (m *Manager) parseTemplatesFromFilesystem(templatesPath string) (*template.Template, error) {
	// Check if templates directory exists
	if _, err := os.Stat(templatesPath); os.IsNotExist(err) {
		// Return empty template if no templates directory
		return template.New("").Funcs(m.funcMap), nil
	}

	// Create root template with function map
	tmpl := template.New("").Funcs(m.funcMap)

	// Collect and parse layouts (named by relative path)
	layoutFiles := collectHTMLFiles(filepath.Join(templatesPath, "layouts"))
	if err := parseTemplateFiles(tmpl, templatesPath, layoutFiles, "layout", func(f string) string {
		relPath, _ := filepath.Rel(templatesPath, f)
		return relPath
	}); err != nil {
		return nil, err
	}

	// Collect and parse partials (named by filename only for {{template "header.html" .}})
	partialFiles := collectHTMLFiles(filepath.Join(templatesPath, "partials"))
	if err := parseTemplateFiles(tmpl, templatesPath, partialFiles, "partial", filepath.Base); err != nil {
		return nil, err
	}

	// Now parse each page template as a separate named template
	pageDir := filepath.Join(templatesPath, "pages")
	if entries, err := os.ReadDir(pageDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".html" {
				continue
			}

			pagePath := filepath.Join(pageDir, entry.Name())
			content, err := os.ReadFile(pagePath)
			if err != nil {
				return nil, fmt.Errorf("reading page %s: %w", entry.Name(), err)
			}
			if err := parsePageTemplate(tmpl, entry.Name(), content); err != nil {
				return nil, err
			}
		}
	}

	return tmpl, nil
}

// collectHTMLFiles returns all .html files from a directory.
func collectHTMLFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".html" {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files
}

// parsePageTemplate parses a single page template, wrapping its content block with a unique name.
func parsePageTemplate(tmpl *template.Template, entryName string, content []byte) error {
	baseName := strings.TrimSuffix(entryName, ".html")
	contentName := "content_" + baseName

	wrappedContent := strings.Replace(
		string(content),
		`{{define "content"}}`,
		fmt.Sprintf(`{{define "%s"}}`, contentName),
		1,
	)

	relPath := "pages/" + entryName
	if _, err := tmpl.New(relPath).Parse(wrappedContent); err != nil {
		return fmt.Errorf("parsing page %s: %w", relPath, err)
	}
	return nil
}

// parseTemplateFiles parses template files into the given template.
func parseTemplateFiles(tmpl *template.Template, templatesPath string, files []string, fileType string, nameFunc func(string) string) error {
	for _, f := range files {
		relPath, _ := filepath.Rel(templatesPath, f)
		content, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("reading %s %s: %w", fileType, relPath, err)
		}
		name := nameFunc(f)
		if _, err := tmpl.New(name).Parse(string(content)); err != nil {
			return fmt.Errorf("parsing %s %s: %w", fileType, relPath, err)
		}
	}
	return nil
}

// SetActiveTheme sets the active theme by name.
func (m *Manager) SetActiveTheme(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	theme, ok := m.themes[name]
	if !ok {
		return fmt.Errorf("theme not found: %s", name)
	}

	m.activeTheme = theme
	m.logger.Info("active theme set", "theme", name, "embedded", theme.IsEmbedded)
	return nil
}

// GetActiveTheme returns the currently active theme.
func (m *Manager) GetActiveTheme() *Theme {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeTheme
}

// GetTheme returns a theme by name.
func (m *Manager) GetTheme(name string) (*Theme, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	theme, ok := m.themes[name]
	if !ok {
		return nil, fmt.Errorf("theme not found: %s", name)
	}
	return theme, nil
}

// ListThemes returns all loaded theme configs, sorted by name.
func (m *Manager) ListThemes() []*Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Collect theme names and sort them
	names := make([]string, 0, len(m.themes))
	for name := range m.themes {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build configs in sorted order
	configs := make([]*Config, 0, len(m.themes))
	for _, name := range names {
		theme := m.themes[name]
		if theme == nil {
			continue
		}
		configs = append(configs, new(theme.Config))
	}
	return configs
}

// Info represents a theme with its configuration and active status.
type Info struct {
	Name       string
	Config     Config
	IsActive   bool
	IsEmbedded bool
}

// ListThemesWithActive returns all themes with active status, sorted by name.
func (m *Manager) ListThemesWithActive() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]Info, 0, len(m.themes))
	for name, theme := range m.themes {
		infos = append(infos, Info{
			Name:       name,
			Config:     theme.Config,
			IsActive:   m.activeTheme != nil && m.activeTheme.Name == name,
			IsEmbedded: theme.IsEmbedded,
		})
	}

	// Sort by name for consistent ordering
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})

	return infos
}

// ReloadTheme reloads a specific theme from disk.
// Only works for external (non-embedded) themes.
func (m *Manager) ReloadTheme(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existingTheme, exists := m.themes[name]
	if !exists {
		return fmt.Errorf("theme not found: %s", name)
	}

	// Cannot reload embedded themes
	if existingTheme.IsEmbedded {
		return fmt.Errorf("cannot reload embedded theme: %s", name)
	}

	themePath := existingTheme.Path
	theme, err := m.loadFilesystemTheme(name, themePath)
	if err != nil {
		return fmt.Errorf("reloading theme: %w", err)
	}

	m.themes[name] = theme

	// Update active theme if it was the one reloaded
	if m.activeTheme != nil && m.activeTheme.Name == name {
		m.activeTheme = theme
	}

	m.logger.Info("theme reloaded", "theme", name)
	return nil
}

// CustomDir returns the custom directory path.
func (m *Manager) CustomDir() string {
	return m.customDir
}

// HasTheme checks if a theme exists.
func (m *Manager) HasTheme(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.themes[name]
	return ok
}

// IsEmbedded checks if a theme is embedded (core) or external (custom).
func (m *Manager) IsEmbedded(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	theme, ok := m.themes[name]
	if !ok {
		return false
	}
	return theme.IsEmbedded
}

// ThemeCount returns the number of loaded themes.
func (m *Manager) ThemeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.themes)
}

// Translate returns a translation for the given key, checking the active theme first,
// then falling back to the global i18n catalog.
func (m *Manager) Translate(lang, key string, args ...any) string {
	m.mu.RLock()
	theme := m.activeTheme
	m.mu.RUnlock()

	// Check theme-specific translation first
	if theme != nil {
		if translation, ok := theme.Translate(lang, key); ok {
			if len(args) > 0 {
				return fmt.Sprintf(translation, args...)
			}
			return translation
		}
	}

	// Fall back to global i18n
	return i18n.T(lang, key, args...)
}

// TemplateFuncs returns template functions provided by the theme manager.
func (m *Manager) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// TTheme translates using theme-specific translations with global fallback.
		"TTheme": func(lang string, key string, args ...any) string {
			return m.Translate(lang, key, args...)
		},
	}
}

// EmbeddedFS returns the embedded filesystem for core themes.
func (m *Manager) EmbeddedFS() embed.FS {
	return m.embeddedFS
}
