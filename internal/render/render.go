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
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/gorilla/csrf"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/service"
)

// SessionKeyAdminLang is the session key for storing admin UI language preference.
const SessionKeyAdminLang = "admin_lang"

// Renderer handles template rendering with caching.
type Renderer struct {
	templates      map[string]*template.Template
	sessionManager *scs.SessionManager
	menuService    *service.MenuService
	db             *sql.DB
	isDev          bool
	extraFuncs     template.FuncMap
	templatesFS    fs.FS // Stored for reloading after adding module funcs
}

// Config holds renderer configuration.
type Config struct {
	TemplatesFS    fs.FS
	SessionManager *scs.SessionManager
	DB             *sql.DB
	IsDev          bool
	MenuService    *service.MenuService // Optional: shared menu service for cache consistency
}

// New creates a new Renderer with parsed templates.
func New(cfg Config) (*Renderer, error) {
	var menuSvc *service.MenuService
	if cfg.MenuService != nil {
		menuSvc = cfg.MenuService
	} else if cfg.DB != nil {
		menuSvc = service.NewMenuService(cfg.DB)
	}

	r := &Renderer{
		templates:      make(map[string]*template.Template),
		sessionManager: cfg.SessionManager,
		menuService:    menuSvc,
		db:             cfg.DB,
		isDev:          cfg.IsDev,
		templatesFS:    cfg.TemplatesFS,
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

		tmpl, err := template.New("").Funcs(r.TemplateFuncs()).ParseFS(templatesFS, files...)
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

		tmpl, err := template.New("").Funcs(r.TemplateFuncs()).ParseFS(templatesFS, files...)
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

		tmpl, err := template.New("").Funcs(r.TemplateFuncs()).ParseFS(templatesFS, files...)
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

		tmpl, err := template.New("").Funcs(r.TemplateFuncs()).ParseFS(templatesFS, files...)
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

// TemplateFuncs returns custom template functions for external use.
func (r *Renderer) TemplateFuncs() template.FuncMap {
	funcs := r.templateFuncs()
	// Merge extra functions (from modules, etc.)
	for k, v := range r.extraFuncs {
		funcs[k] = v
	}
	return funcs
}

// AddTemplateFuncs adds additional template functions (e.g., from modules).
func (r *Renderer) AddTemplateFuncs(funcs template.FuncMap) {
	if r.extraFuncs == nil {
		r.extraFuncs = make(template.FuncMap)
	}
	for k, v := range funcs {
		r.extraFuncs[k] = v
	}
}

// ReloadTemplates re-parses all templates with the current template functions.
// This should be called after adding module template functions.
func (r *Renderer) ReloadTemplates() error {
	if r.templatesFS == nil {
		return fmt.Errorf("templatesFS is nil, cannot reload templates")
	}
	return r.parseTemplates(r.templatesFS)
}

// templateFuncs returns custom template functions.
func (r *Renderer) templateFuncs() template.FuncMap {
	return template.FuncMap{
		"lower": func(s string) string {
			return strings.ToLower(s)
		},
		"upper": func(s string) string {
			return strings.ToUpper(s)
		},
		"now": func() time.Time {
			return time.Now()
		},
		"formatDate": func(t time.Time) string {
			return t.Format("Jan 2, 2006")
		},
		"formatDateTime": func(t time.Time) string {
			return t.Format("Jan 2, 2006 3:04 PM")
		},
		// formatDateLocale formats a date according to the specified language.
		// Usage: {{formatDateLocale .PublishedAt .LangCode}}
		// Handles both time.Time and *time.Time (returns empty string for nil)
		"formatDateLocale": func(t any, lang string) string {
			switch v := t.(type) {
			case time.Time:
				return formatDateForLocale(v, lang)
			case *time.Time:
				if v == nil {
					return ""
				}
				return formatDateForLocale(*v, lang)
			default:
				return ""
			}
		},
		// formatDateTimeLocale formats a datetime according to the specified language.
		// Usage: {{formatDateTimeLocale .UpdatedAt .AdminLang}}
		// Handles both time.Time and *time.Time (returns empty string for nil)
		"formatDateTimeLocale": func(t any, lang string) string {
			switch v := t.(type) {
			case time.Time:
				return formatDateTimeForLocale(v, lang)
			case *time.Time:
				if v == nil {
					return ""
				}
				return formatDateTimeForLocale(*v, lang)
			default:
				return ""
			}
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
		// getMenuForLanguage returns menu items for a specific language with fallback.
		// Usage in templates: {{range getMenuForLanguage "main" "ru"}}...{{end}}
		"getMenuForLanguage": func(slug string, langCode string) []service.MenuItem {
			if r.menuService == nil {
				return nil
			}
			return r.menuService.GetMenuForLanguage(slug, langCode)
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
		"contains": func(collection, element any) bool {
			// Handle string slice
			if slice, ok := collection.([]string); ok {
				if elem, ok := element.(string); ok {
					for _, s := range slice {
						if s == elem {
							return true
						}
					}
				}
				return false
			}
			// Handle string contains substring
			if s, ok := collection.(string); ok {
				if substr, ok := element.(string); ok {
					return strings.Contains(s, substr)
				}
			}
			return false
		},
		"hasPrefix": func(s, prefix string) bool {
			return strings.HasPrefix(s, prefix)
		},
		"prettyJSON": func(s string) string {
			var data any
			if err := json.Unmarshal([]byte(s), &data); err != nil {
				return s
			}
			pretty, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				return s
			}
			return string(pretty)
		},
		"int64": func(v any) int64 {
			switch val := v.(type) {
			case int:
				return int64(val)
			case int32:
				return int64(val)
			case int64:
				return val
			case float64:
				return int64(val)
			case string:
				i, _ := strconv.ParseInt(val, 10, 64)
				return i
			default:
				return 0
			}
		},
		"atoi": func(s string) int64 {
			if s == "" {
				return 0
			}
			i, _ := strconv.ParseInt(s, 10, 64)
			return i
		},
		// pagesListURL builds a URL for admin pages list with proper encoding.
		// Parameters: status (string), category (int64), search (string), page (int)
		"pagesListURL": func(status string, category int64, search string, page int) string {
			params := url.Values{}
			if status != "" {
				params.Set("status", status)
			}
			if category > 0 {
				params.Set("category", fmt.Sprintf("%d", category))
			}
			if search != "" {
				params.Set("search", search)
			}
			if page > 1 {
				params.Set("page", fmt.Sprintf("%d", page))
			}
			if len(params) == 0 {
				return "/admin/pages"
			}
			return "/admin/pages?" + params.Encode()
		},
		// T translates a message key to the specified language.
		// Usage in templates: {{T .AdminLang "btn.save"}}
		// With arguments: {{T .AdminLang "msg.deleted" "Page"}}
		"T": func(lang string, key string, args ...any) string {
			return i18n.T(lang, key, args...)
		},
		// TDefault translates a message key, falling back to a default value if not found.
		// Usage in templates: {{TDefault .AdminLang "key" "Default Value"}}
		"TDefault": func(lang string, key string, defaultVal string) string {
			result := i18n.T(lang, key)
			// If the key wasn't found, T returns the key itself
			if result == key {
				return defaultVal
			}
			return result
		},
		// adminLangOptions returns the list of admin UI languages.
		// Only returns languages that are:
		// 1. Active in the database
		// 2. Supported by the i18n system (have locale files)
		"adminLangOptions": func() []struct {
			Code string
			Name string
		} {
			// Fallback to all i18n supported languages if DB unavailable
			if r.db == nil {
				return []struct {
					Code string
					Name string
				}{
					{"en", "English"},
				}
			}

			// Query active languages from database
			rows, err := r.db.Query("SELECT code, native_name FROM languages WHERE is_active = 1 ORDER BY position")
			if err != nil {
				return []struct {
					Code string
					Name string
				}{
					{"en", "English"},
				}
			}
			defer func() { _ = rows.Close() }()

			var options []struct {
				Code string
				Name string
			}

			for rows.Next() {
				var code, name string
				if err := rows.Scan(&code, &name); err != nil {
					continue
				}
				// Only include if i18n supports this language
				if i18n.IsSupported(code) {
					options = append(options, struct {
						Code string
						Name string
					}{code, name})
				}
			}

			// Fallback to English if no matching languages found
			if len(options) == 0 {
				return []struct {
					Code string
					Name string
				}{
					{"en", "English"},
				}
			}

			return options
		},
		// TLang translates a language code to its localized name.
		// Usage in templates: {{TLang .AdminLang .LanguageCode}}
		// Falls back to the original code if no translation exists.
		"TLang": func(adminLang string, langCode string) string {
			translationKey := "language." + langCode
			translated := i18n.T(adminLang, translationKey)
			// If translation exists (different from key), return it
			if translated != translationKey {
				return translated
			}
			// Fallback to language code
			return langCode
		},
		// Placeholder functions for hCaptcha module (will be overwritten if module is loaded)
		"hcaptchaEnabled": func() bool {
			return false
		},
		"hcaptchaWidget": func() template.HTML {
			return ""
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
	CSRFToken   string        // CSRF token value
	CSRFField   template.HTML // Hidden input field with CSRF token
	SiteName    string        // Site name from config
	Breadcrumbs []Breadcrumb  // Breadcrumb navigation
	CurrentPath string        // Current request path for active link detection
	AdminLang   string        // Admin UI language code (en, ru, etc.)
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

	// Get CSRF token and field from gorilla/csrf
	// This will be empty if CSRF middleware is not applied (e.g., for API routes)
	data.CSRFToken = csrf.Token(req)
	data.CSRFField = csrf.TemplateField(req)

	// Get site name from context if not already set
	if data.SiteName == "" {
		data.SiteName = middleware.GetSiteName(req)
	}

	// Get admin language from session if not already set
	if data.AdminLang == "" && r.sessionManager != nil {
		data.AdminLang = r.sessionManager.GetString(req.Context(), SessionKeyAdminLang)
	}
	// Fall back to browser's Accept-Language header
	if data.AdminLang == "" {
		if acceptLang := req.Header.Get("Accept-Language"); acceptLang != "" {
			data.AdminLang = i18n.MatchLanguage(acceptLang)
		}
	}
	// Fall back to database default language
	if data.AdminLang == "" {
		data.AdminLang = i18n.GetDefaultLanguage()
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
	_, _ = buf.WriteTo(w)
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
	_ = r.Render(w, req, templateName, TemplateData{
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

// GetMenuService returns the menu service for sharing with other handlers.
func (r *Renderer) GetMenuService() *service.MenuService {
	return r.menuService
}

// SetAdminLang sets the admin UI language preference in the session.
func (r *Renderer) SetAdminLang(req *http.Request, lang string) {
	if r.sessionManager != nil && i18n.IsSupported(lang) {
		r.sessionManager.Put(req.Context(), SessionKeyAdminLang, lang)
	}
}

// GetAdminLang gets the admin UI language preference from the session.
func (r *Renderer) GetAdminLang(req *http.Request) string {
	if r.sessionManager != nil {
		if lang := r.sessionManager.GetString(req.Context(), SessionKeyAdminLang); lang != "" {
			return lang
		}
	}
	return i18n.GetDefaultLanguage()
}

// formatDateForLocale formats a date according to the specified language.
func formatDateForLocale(t time.Time, lang string) string {
	switch lang {
	case "ru":
		// Russian date format: "2 января 2006"
		monthsRu := []string{
			"января", "февраля", "марта", "апреля", "мая", "июня",
			"июля", "августа", "сентября", "октября", "ноября", "декабря",
		}
		return fmt.Sprintf("%d %s %d", t.Day(), monthsRu[t.Month()-1], t.Year())
	default:
		// English date format: "Jan 2, 2006"
		return t.Format("Jan 2, 2006")
	}
}

// formatDateTimeForLocale formats a time.Time as a localized datetime string.
func formatDateTimeForLocale(t time.Time, lang string) string {
	switch lang {
	case "ru":
		// Russian datetime format: "2 января 2006, 15:04"
		monthsRu := []string{
			"января", "февраля", "марта", "апреля", "мая", "июня",
			"июля", "августа", "сентября", "октября", "ноября", "декабря",
		}
		return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), monthsRu[t.Month()-1], t.Year(), t.Hour(), t.Minute())
	default:
		// English datetime format: "Jan 2, 2006 3:04 PM"
		return t.Format("Jan 2, 2006 3:04 PM")
	}
}
