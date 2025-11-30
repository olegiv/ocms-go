package theme

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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

	var config ThemeConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("parsing theme.json: %w", err)
	}

	// Parse templates
	templatesPath := filepath.Join(path, "templates")
	templates, err := m.parseTemplates(templatesPath)
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	return &Theme{
		Name:       name,
		Path:       path,
		Config:     config,
		Templates:  templates,
		StaticPath: filepath.Join(path, "static"),
	}, nil
}

// parseTemplates parses all HTML templates in the templates directory.
func (m *Manager) parseTemplates(templatesPath string) (*template.Template, error) {
	// Check if templates directory exists
	if _, err := os.Stat(templatesPath); os.IsNotExist(err) {
		// Return empty template if no templates directory
		return template.New("").Funcs(m.funcMap), nil
	}

	// Create root template with function map
	tmpl := template.New("").Funcs(m.funcMap)

	// Walk templates directory and parse all .html files
	err := filepath.WalkDir(templatesPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if filepath.Ext(path) != ".html" {
			return nil
		}

		// Get relative path for template name
		relPath, err := filepath.Rel(templatesPath, path)
		if err != nil {
			return err
		}

		// Read and parse template
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading template %s: %w", relPath, err)
		}

		// Parse with the relative path as the name
		_, err = tmpl.New(relPath).Parse(string(content))
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", relPath, err)
		}

		return nil
	})

	if err != nil {
		return nil, err
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
func (m *Manager) ListThemes() []*ThemeConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	configs := make([]*ThemeConfig, 0, len(m.themes))
	for _, theme := range m.themes {
		cfg := theme.Config
		configs = append(configs, &cfg)
	}
	return configs
}

// ListThemesWithActive returns all themes with an indicator of which is active.
type ThemeInfo struct {
	Name     string
	Config   ThemeConfig
	IsActive bool
}

// ListThemesWithActive returns all themes with active status.
func (m *Manager) ListThemesWithActive() []ThemeInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]ThemeInfo, 0, len(m.themes))
	for name, theme := range m.themes {
		infos = append(infos, ThemeInfo{
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
