package theme

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ocms-go/internal/i18n"
)

// Manager handles theme loading, switching, and rendering.
type Manager struct {
	themesDir   string
	activeTheme *Theme
	themes      map[string]*Theme
	mu          sync.RWMutex
	logger      *slog.Logger
	funcMap     template.FuncMap
}

// NewManager creates a new theme manager.
func NewManager(themesDir string, logger *slog.Logger) *Manager {
	return &Manager{
		themesDir: themesDir,
		themes:    make(map[string]*Theme),
		logger:    logger,
		funcMap:   make(template.FuncMap),
	}
}

// SetFuncMap sets the template function map to use when parsing templates.
func (m *Manager) SetFuncMap(funcMap template.FuncMap) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.funcMap = funcMap
}

// LoadThemes scans the themes directory and loads all themes.
func (m *Manager) LoadThemes() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if themes directory exists
	if _, err := os.Stat(m.themesDir); os.IsNotExist(err) {
		m.logger.Warn("themes directory does not exist", "path", m.themesDir)
		return nil
	}

	entries, err := os.ReadDir(m.themesDir)
	if err != nil {
		return fmt.Errorf("reading themes directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		themeName := entry.Name()
		themePath := filepath.Join(m.themesDir, themeName)

		theme, err := m.loadTheme(themeName, themePath)
		if err != nil {
			m.logger.Warn("failed to load theme", "theme", themeName, "error", err)
			continue
		}

		m.themes[themeName] = theme
		m.logger.Info("loaded theme", "theme", themeName, "version", theme.Config.Version)
	}

	m.logger.Info("themes loaded", "count", len(m.themes))
	return nil
}

// loadTheme loads a single theme from the given path.
func (m *Manager) loadTheme(name, path string) (*Theme, error) {
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

	// Parse templates
	templatesPath := filepath.Join(path, "templates")
	templates, err := m.parseTemplates(templatesPath)
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	// Load theme-specific translations (optional)
	translations := m.loadThemeTranslations(path)

	return &Theme{
		Name:         name,
		Path:         path,
		Config:       config,
		Templates:    templates,
		StaticPath:   filepath.Join(path, "static"),
		Translations: translations,
	}, nil
}

// loadThemeTranslations loads translations from the theme's locales directory.
// Returns nil if no locales directory exists (translations are optional).
// Structure: themes/{name}/locales/{lang}/messages.json
func (m *Manager) loadThemeTranslations(themePath string) map[string]map[string]string {
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

// parseTemplates parses all HTML templates in the templates directory.
// Each page template is parsed together with layouts and partials to create
// a complete template set that can be rendered independently.
func (m *Manager) parseTemplates(templatesPath string) (*template.Template, error) {
	// Check if templates directory exists
	if _, err := os.Stat(templatesPath); os.IsNotExist(err) {
		// Return empty template if no templates directory
		return template.New("").Funcs(m.funcMap), nil
	}

	// Create root template with function map
	tmpl := template.New("").Funcs(m.funcMap)

	// First, collect all layout and partial templates
	var layoutFiles []string
	var partialFiles []string

	// Get layouts
	layoutDir := filepath.Join(templatesPath, "layouts")
	if entries, err := os.ReadDir(layoutDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".html" {
				layoutFiles = append(layoutFiles, filepath.Join(layoutDir, entry.Name()))
			}
		}
	}

	// Get partials
	partialDir := filepath.Join(templatesPath, "partials")
	if entries, err := os.ReadDir(partialDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".html" {
				partialFiles = append(partialFiles, filepath.Join(partialDir, entry.Name()))
			}
		}
	}

	// Parse layouts and partials into the root template
	for _, f := range layoutFiles {
		relPath, _ := filepath.Rel(templatesPath, f)
		content, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading layout %s: %w", relPath, err)
		}
		if _, err := tmpl.New(relPath).Parse(string(content)); err != nil {
			return nil, fmt.Errorf("parsing layout %s: %w", relPath, err)
		}
	}

	for _, f := range partialFiles {
		relPath, _ := filepath.Rel(templatesPath, f)
		content, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading partial %s: %w", relPath, err)
		}
		// Parse partials with just the filename (e.g., "header.html") for {{template "header.html" .}}
		filename := filepath.Base(f)
		if _, err := tmpl.New(filename).Parse(string(content)); err != nil {
			return nil, fmt.Errorf("parsing partial %s: %w", relPath, err)
		}
	}

	// Now parse each page template as a separate named template
	// Each page template will be named like "pages/home" and will have its "content" block
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

			// Create a unique template name for each page's content
			// e.g., "pages/home.html" -> "content_home"
			baseName := strings.TrimSuffix(entry.Name(), ".html")
			contentName := "content_" + baseName

			// Wrap the content definition with a unique name
			wrappedContent := strings.Replace(
				string(content),
				`{{define "content"}}`,
				fmt.Sprintf(`{{define "%s"}}`, contentName),
				1,
			)

			relPath := "pages/" + entry.Name()
			if _, err := tmpl.New(relPath).Parse(wrappedContent); err != nil {
				return nil, fmt.Errorf("parsing page %s: %w", relPath, err)
			}
		}
	}

	return tmpl, nil
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
	m.logger.Info("active theme set", "theme", name)
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

// ListThemes returns all loaded theme configs.
func (m *Manager) ListThemes() []*Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	configs := make([]*Config, 0, len(m.themes))
	for _, theme := range m.themes {
		cfg := theme.Config
		configs = append(configs, &cfg)
	}
	return configs
}

// Info represents a theme with its configuration and active status.
type Info struct {
	Name     string
	Config   Config
	IsActive bool
}

// ListThemesWithActive returns all themes with active status.
func (m *Manager) ListThemesWithActive() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]Info, 0, len(m.themes))
	for name, theme := range m.themes {
		infos = append(infos, Info{
			Name:     name,
			Config:   theme.Config,
			IsActive: m.activeTheme != nil && m.activeTheme.Name == name,
		})
	}
	return infos
}

// ReloadTheme reloads a specific theme from disk.
func (m *Manager) ReloadTheme(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	themePath := filepath.Join(m.themesDir, name)
	theme, err := m.loadTheme(name, themePath)
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

// ThemesDir returns the themes directory path.
func (m *Manager) ThemesDir() string {
	return m.themesDir
}

// HasTheme checks if a theme exists.
func (m *Manager) HasTheme(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.themes[name]
	return ok
}

// ThemeCount returns the number of loaded themes.
func (m *Manager) ThemeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.themes)
}

// Translate returns a translation for the given key, checking the active theme first,
// then falling back to the global i18n catalog.
// This is the recommended function for frontend templates.
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
// These should be merged with the renderer's template functions.
func (m *Manager) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// TTheme translates using theme-specific translations with global fallback.
		// Usage in theme templates: {{TTheme .LangCode "frontend.read_more"}}
		// With arguments: {{TTheme .LangCode "pagination.page_of" .Page .TotalPages}}
		"TTheme": func(lang string, key string, args ...any) string {
			return m.Translate(lang, key, args...)
		},
	}
}
