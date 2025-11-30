// Package theme provides theme loading, switching, and rendering for the frontend.
package theme

import (
	"html/template"
	"io"
)

// ThemeConfig represents the configuration loaded from theme.json.
type ThemeConfig struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Author      string            `json:"author"`
	Description string            `json:"description"`
	Screenshot  string            `json:"screenshot"`
	Templates   map[string]string `json:"templates"`
	Settings    []ThemeSetting    `json:"settings"`
}

// ThemeSetting represents a configurable option for a theme.
type ThemeSetting struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // text, color, image, select
	Default string   `json:"default"`
	Options []string `json:"options,omitempty"`
}

// Theme represents a loaded theme with its templates and configuration.
type Theme struct {
	Name       string             // directory name (used as identifier)
	Path       string             // filesystem path to theme directory
	Config     ThemeConfig        // parsed theme.json
	Templates  *template.Template // parsed templates
	StaticPath string             // path to static files
}

// GetTemplate returns the template file path for a given template name.
// Falls back to a default if not specified in theme config.
func (t *Theme) GetTemplate(name string) string {
	if path, ok := t.Config.Templates[name]; ok {
		return path
	}
	// Default mappings
	defaults := map[string]string{
		"home":     "pages/home.html",
		"page":     "pages/page.html",
		"list":     "pages/list.html",
		"404":      "pages/404.html",
		"category": "pages/category.html",
		"tag":      "pages/tag.html",
		"search":   "pages/search.html",
	}
	if path, ok := defaults[name]; ok {
		return path
	}
	return "pages/page.html"
}

// Render executes a template with the given data.
func (t *Theme) Render(w io.Writer, templateName string, data any) error {
	return t.Templates.ExecuteTemplate(w, templateName, data)
}

// HasSetting returns true if the theme has a setting with the given key.
func (t *Theme) HasSetting(key string) bool {
	for _, s := range t.Config.Settings {
		if s.Key == key {
			return true
		}
	}
	return false
}

// GetSettingDefault returns the default value for a setting.
func (t *Theme) GetSettingDefault(key string) string {
	for _, s := range t.Config.Settings {
		if s.Key == key {
			return s.Default
		}
	}
	return ""
}
