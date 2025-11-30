// Package render provides HTML template rendering with layout support,
// flash message handling, and helper functions for the admin interface.
package render

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/middleware"
	"ocms-go/internal/service"
)

// Renderer handles template rendering with caching.
type Renderer struct {
	templates      map[string]*template.Template
	sessionManager *scs.SessionManager
	menuService    *service.MenuService
	isDev          bool
}

// Config holds renderer configuration.
type Config struct {
	TemplatesFS    fs.FS
	SessionManager *scs.SessionManager
	DB             *sql.DB
	IsDev          bool
}

// New creates a new Renderer with parsed templates.
func New(cfg Config) (*Renderer, error) {
	var menuSvc *service.MenuService
	if cfg.DB != nil {
		menuSvc = service.NewMenuService(cfg.DB)
	}

	r := &Renderer{
		templates:      make(map[string]*template.Template),
		sessionManager: cfg.SessionManager,
		menuService:    menuSvc,
		isDev:          cfg.IsDev,
	}

	if err := r.parseTemplates(cfg.TemplatesFS); err != nil {
		return nil, err
	}

	return r, nil
}

// parseTemplates parses all templates from the filesystem.
func (r *Renderer) parseTemplates(templatesFS fs.FS) error {
	// Get all partials
	partials, err := r.getTemplateFiles(templatesFS, "partials")
	if err != nil {
		return fmt.Errorf("getting partials: %w", err)
	}

	// Get base layout
	baseLayout := "layouts/base.html"

	// Parse admin templates with admin layout
	adminTemplates, err := r.getTemplateFiles(templatesFS, "admin")
	if err != nil {
		return fmt.Errorf("getting admin templates: %w", err)
	}

	adminLayout := "layouts/admin.html"

	for _, tmplPath := range adminTemplates {
		name := filepath.Base(tmplPath)
		name = strings.TrimSuffix(name, ".html")
		name = "admin/" + name

		// Parse in order: base layout, admin layout, partials, page template
		files := []string{baseLayout, adminLayout}
		files = append(files, partials...)
		files = append(files, tmplPath)

		tmpl, err := template.New("").Funcs(r.templateFuncs()).ParseFS(templatesFS, files...)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", name, err)
		}

		r.templates[name] = tmpl
	}

	// Parse auth templates with base layout only
	authTemplates, err := r.getTemplateFiles(templatesFS, "auth")
	if err != nil {
		return fmt.Errorf("getting auth templates: %w", err)
	}

	for _, tmplPath := range authTemplates {
		name := filepath.Base(tmplPath)
		name = strings.TrimSuffix(name, ".html")
		name = "auth/" + name

		files := []string{baseLayout}
		files = append(files, partials...)
		files = append(files, tmplPath)

		tmpl, err := template.New("").Funcs(r.templateFuncs()).ParseFS(templatesFS, files...)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", name, err)
		}

		r.templates[name] = tmpl
	}

	// Parse error templates with admin layout
	errorTemplates, err := r.getTemplateFiles(templatesFS, "errors")
	if err != nil {
		return fmt.Errorf("getting error templates: %w", err)
	}

	for _, tmplPath := range errorTemplates {
		name := filepath.Base(tmplPath)
		name = strings.TrimSuffix(name, ".html")
		name = "errors/" + name

		files := []string{baseLayout, adminLayout}
		files = append(files, partials...)
		files = append(files, tmplPath)

		tmpl, err := template.New("").Funcs(r.templateFuncs()).ParseFS(templatesFS, files...)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", name, err)
		}

		r.templates[name] = tmpl
	}

	// Parse public templates (standalone, with their own body)
	publicTemplates, err := r.getTemplateFiles(templatesFS, "public")
	if err != nil {
		return fmt.Errorf("getting public templates: %w", err)
	}

	for _, tmplPath := range publicTemplates {
		name := filepath.Base(tmplPath)
		name = strings.TrimSuffix(name, ".html")
		name = "public/" + name

		// Public templates define their own "body" template that is self-contained
		files := []string{tmplPath}
		files = append(files, partials...)

		tmpl, err := template.New("").Funcs(r.templateFuncs()).ParseFS(templatesFS, files...)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", name, err)
		}

		r.templates[name] = tmpl
	}

	return nil
}

// getTemplateFiles returns all .html files in a directory.
func (r *Renderer) getTemplateFiles(templatesFS fs.FS, dir string) ([]string, error) {
	var files []string

	entries, err := fs.ReadDir(templatesFS, dir)
	if err != nil {
		// Directory might not exist yet, that's ok
		return files, nil
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".html") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	return files, nil
}

// templateFuncs returns custom template functions.
func (r *Renderer) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDate": func(t time.Time) string {
			return t.Format("Jan 2, 2006")
		},
		"formatDateTime": func(t time.Time) string {
			return t.Format("Jan 2, 2006 3:04 PM")
		},
		"truncate": func(s string, length int) string {
			if len(s) <= length {
				return s
			}
			return s[:length] + "..."
		},
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"multiply": func(a, b int) int {
			return a * b
		},
		"repeat": func(s string, n int) string {
			return strings.Repeat(s, n)
		},
		"seq": func(start, end int) []int {
			var result []int
			for i := start; i <= end; i++ {
				result = append(result, i)
			}
			return result
		},
		"toJSON": func(v any) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("[]")
			}
			return template.JS(b)
		},
		"formatBytes": func(bytes int64) string {
			const unit = 1024
			if bytes < unit {
				return fmt.Sprintf("%d B", bytes)
			}
			div, exp := int64(unit), 0
			for n := bytes / unit; n >= unit; n /= unit {
				div *= unit
				exp++
			}
			return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
		},
		"deref": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"safeURL": func(s string) template.URL {
			return template.URL(s)
		},
		"getMenu": func(slug string) []service.MenuItem {
			if r.menuService == nil {
				return nil
			}
			return r.menuService.GetMenu(slug)
		},
		"dict": func(values ...any) map[string]any {
			if len(values)%2 != 0 {
				return nil
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					continue
				}
				dict[key] = values[i+1]
			}
			return dict
		},
		"parseJSON": func(s string) []string {
			if s == "" || s == "[]" {
				return []string{}
			}
			var result []string
			if err := json.Unmarshal([]byte(s), &result); err != nil {
				return []string{}
			}
			return result
		},
		"contains": func(s, substr string) bool {
			return strings.Contains(s, substr)
		},
	}
}

// Breadcrumb represents a single breadcrumb item.
type Breadcrumb struct {
	Label  string
	URL    string
	Active bool
}

// TemplateData holds data passed to templates.
type TemplateData struct {
	Title       string
	Data        any
	User        any // Current authenticated user (available in all admin templates)
	Flash       string
	FlashType   string
	CurrentYear int
	CSRFToken   string
	SiteName    string       // Site name from config
	Breadcrumbs []Breadcrumb // Breadcrumb navigation
	CurrentPath string       // Current request path for active link detection
}

// Render renders a template with the given data.
func (r *Renderer) Render(w http.ResponseWriter, req *http.Request, name string, data TemplateData) error {
	tmpl, ok := r.templates[name]
	if !ok {
		return fmt.Errorf("template %s not found", name)
	}

	// Add default data
	data.CurrentYear = time.Now().Year()
	data.CurrentPath = req.URL.Path

	// Get site name from context if not already set
	if data.SiteName == "" {
		data.SiteName = middleware.GetSiteName(req)
	}

	// Get flash message from session
	if r.sessionManager != nil {
		if flash := r.sessionManager.PopString(req.Context(), "flash"); flash != "" {
			data.Flash = flash
			data.FlashType = r.sessionManager.PopString(req.Context(), "flash_type")
			if data.FlashType == "" {
				data.FlashType = "info"
			}
		}
	}

	// Render to buffer first to catch errors
	buf := new(bytes.Buffer)

	// Public templates use "body" instead of "base"
	templateName := "base"
	if strings.HasPrefix(name, "public/") {
		templateName = "body"
	}

	if err := tmpl.ExecuteTemplate(buf, templateName, data); err != nil {
		return fmt.Errorf("executing template %s: %w", name, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
	return nil
}

// SetFlash sets a flash message in the session.
func (r *Renderer) SetFlash(req *http.Request, message, flashType string) {
	if r.sessionManager != nil {
		r.sessionManager.Put(req.Context(), "flash", message)
		r.sessionManager.Put(req.Context(), "flash_type", flashType)
	}
}

// RenderError renders an error page with the specified status code.
func (r *Renderer) RenderError(w http.ResponseWriter, req *http.Request, statusCode int, title string) {
	templateName := fmt.Sprintf("errors/%d", statusCode)

	// Check if template exists, fallback to 500 if not
	if _, ok := r.templates[templateName]; !ok {
		templateName = "errors/500"
	}

	w.WriteHeader(statusCode)
	r.Render(w, req, templateName, TemplateData{
		Title: title,
	})
}

// RenderNotFound renders a 404 Not Found page.
func (r *Renderer) RenderNotFound(w http.ResponseWriter, req *http.Request) {
	r.RenderError(w, req, http.StatusNotFound, "Page Not Found")
}

// RenderForbidden renders a 403 Forbidden page.
func (r *Renderer) RenderForbidden(w http.ResponseWriter, req *http.Request) {
	r.RenderError(w, req, http.StatusForbidden, "Access Denied")
}

// RenderInternalError renders a 500 Internal Server Error page.
func (r *Renderer) RenderInternalError(w http.ResponseWriter, req *http.Request) {
	r.RenderError(w, req, http.StatusInternalServerError, "Internal Server Error")
}

// InvalidateMenuCache clears the cached menu by slug, or all menus if slug is empty.
func (r *Renderer) InvalidateMenuCache(slug string) {
	if r.menuService != nil {
		r.menuService.InvalidateCache(slug)
	}
}
