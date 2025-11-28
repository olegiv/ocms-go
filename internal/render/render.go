package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
)

// Renderer handles template rendering with caching.
type Renderer struct {
	templates      map[string]*template.Template
	sessionManager *scs.SessionManager
	isDev          bool
}

// Config holds renderer configuration.
type Config struct {
	TemplatesFS    fs.FS
	SessionManager *scs.SessionManager
	IsDev          bool
}

// New creates a new Renderer with parsed templates.
func New(cfg Config) (*Renderer, error) {
	r := &Renderer{
		templates:      make(map[string]*template.Template),
		sessionManager: cfg.SessionManager,
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
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"seq": func(start, end int) []int {
			var result []int
			for i := start; i <= end; i++ {
				result = append(result, i)
			}
			return result
		},
	}
}

// TemplateData holds data passed to templates.
type TemplateData struct {
	Title       string
	Data        any
	Flash       string
	FlashType   string
	CurrentYear int
	CSRFToken   string
}

// Render renders a template with the given data.
func (r *Renderer) Render(w http.ResponseWriter, req *http.Request, name string, data TemplateData) error {
	tmpl, ok := r.templates[name]
	if !ok {
		return fmt.Errorf("template %s not found", name)
	}

	// Add default data
	data.CurrentYear = time.Now().Year()

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
	if err := tmpl.ExecuteTemplate(buf, "base", data); err != nil {
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
