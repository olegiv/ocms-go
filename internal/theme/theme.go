// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package theme provides theme loading, switching, and rendering for the frontend.
package theme

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"regexp"
	"strings"
)

// Theme engine constants.
const (
	ThemeEngineTempl = "templ"
	ThemeEngineHTML  = "html"
)

// blankLinesRegex matches two or more consecutive newlines (with optional whitespace between).
var blankLinesRegex = regexp.MustCompile(`(\r?\n\s*){2,}`)

// Config represents the configuration loaded from theme.json.
type Config struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Author      string            `json:"author"`
	Description string            `json:"description"`
	Screenshot  string            `json:"screenshot"`
	Engine      string            `json:"engine,omitempty"`
	Templates   map[string]string `json:"templates"`
	Settings    []Setting         `json:"settings"`
	WidgetAreas []WidgetArea      `json:"widget_areas,omitempty"`
}

// Setting represents a configurable option for a theme.
type Setting struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // text, color, image, select
	Default string   `json:"default"`
	Options []string `json:"options,omitempty"`
}

// WidgetArea represents a location in the theme where widgets can be placed.
type WidgetArea struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Theme represents a loaded theme with its templates and configuration.
type Theme struct {
	Name         string                       // directory name (used as identifier)
	Path         string                       // filesystem path to theme directory (empty for embedded)
	Config       Config                       // parsed theme.json
	Templates    *template.Template           // parsed templates
	StaticPath   string                       // path to static files (empty for embedded)
	Translations map[string]map[string]string // lang -> key -> translation (optional overrides)
	IsEmbedded   bool                         // true if theme is embedded in binary
	EmbeddedFS   fs.FS                        // embedded filesystem for static files (nil for external themes)
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

// RenderPage renders a page template within the base layout.
// It handles the template composition by:
// 1. Getting the content block for the specific page
// 2. Injecting it into the base layout
// 3. Executing the combined template
func (t *Theme) RenderPage(w io.Writer, pageName string, data any) error {
	// Get the base layout
	baseLayout := t.Templates.Lookup("layouts/base.html")
	if baseLayout == nil {
		return fmt.Errorf("base layout not found")
	}

	// Determine the content template name
	// pageName is like "home", "page", "list", "404", "category", "tag", "search"
	contentName := "content_" + pageName

	// Check if the content template exists
	contentTmpl := t.Templates.Lookup(contentName)
	if contentTmpl == nil {
		// Try with the page path
		pagePath := "pages/" + pageName + ".html"
		contentTmpl = t.Templates.Lookup(pagePath)
		if contentTmpl == nil {
			return fmt.Errorf("content template not found: %s", contentName)
		}
	}

	// Clone the template to avoid modifying the original
	clone, err := t.Templates.Clone()
	if err != nil {
		return fmt.Errorf("cloning template: %w", err)
	}

	// Add the "content" template that points to our specific page's content
	// This allows {{template "content" .}} in base.html to work
	contentDef := fmt.Sprintf(`{{define "content"}}{{template "%s" .}}{{end}}`, contentName)
	if _, err := clone.Parse(contentDef); err != nil {
		return fmt.Errorf("parsing content definition: %w", err)
	}

	// Execute the base layout which will include the content
	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, "layouts/base.html", data); err != nil {
		return err
	}

	// Strip consecutive blank lines from the rendered HTML
	compacted := blankLinesRegex.ReplaceAll(buf.Bytes(), []byte("\n"))
	_, err = w.Write(compacted)
	return err
}

// RenderContent renders just the content portion (for AJAX/partial rendering).
func (t *Theme) RenderContent(w io.Writer, pageName string, data any) error {
	contentName := "content_" + pageName
	contentTmpl := t.Templates.Lookup(contentName)
	if contentTmpl == nil {
		return fmt.Errorf("content template not found: %s", contentName)
	}

	// Render just the content block
	var buf bytes.Buffer
	if err := t.Templates.ExecuteTemplate(&buf, contentName, data); err != nil {
		return err
	}

	// Strip consecutive blank lines from the rendered HTML
	compacted := blankLinesRegex.ReplaceAll(buf.Bytes(), []byte("\n"))
	_, err := w.Write(compacted)
	return err
}

// GetContentTemplateName returns the content template name for a given page.
func (t *Theme) GetContentTemplateName(pageName string) string {
	// Convert template path to content name
	// "pages/home.html" -> "content_home"
	// "home" -> "content_home"
	baseName := strings.TrimPrefix(pageName, "pages/")
	baseName = strings.TrimSuffix(baseName, ".html")
	return "content_" + baseName
}

// Translate returns a translation for the given key in the specified language.
// Returns empty string if no theme-specific translation exists (caller should fall back to global).
func (t *Theme) Translate(lang, key string) (string, bool) {
	if t.Translations == nil {
		return "", false
	}
	if langMap, ok := t.Translations[lang]; ok {
		if translation, ok := langMap[key]; ok {
			return translation, true
		}
	}
	return "", false
}

// findSetting returns a setting by key, or nil if not found.
func (t *Theme) findSetting(key string) *Setting {
	for i := range t.Config.Settings {
		if t.Config.Settings[i].Key == key {
			return &t.Config.Settings[i]
		}
	}
	return nil
}

// HasSetting returns true if the theme has a setting with the given key.
func (t *Theme) HasSetting(key string) bool {
	return t.findSetting(key) != nil
}

// GetSettingDefault returns the default value for a setting.
func (t *Theme) GetSettingDefault(key string) string {
	if s := t.findSetting(key); s != nil {
		return s.Default
	}
	return ""
}

// RenderEngine returns the normalized render engine for this theme.
// If Config.Engine is explicitly "templ" or "html" (case-insensitive), that
// value is returned. Otherwise embedded themes default to "templ" and
// custom filesystem themes default to "html".
func (t *Theme) RenderEngine() string {
	switch strings.ToLower(t.Config.Engine) {
	case ThemeEngineTempl:
		return ThemeEngineTempl
	case ThemeEngineHTML:
		return ThemeEngineHTML
	default:
		if t.IsEmbedded {
			return ThemeEngineTempl
		}
		return ThemeEngineHTML
	}
}
