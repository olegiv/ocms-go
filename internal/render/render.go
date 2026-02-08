// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

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
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/geoip"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/service"
)

// SessionKeyAdminLang is the session key for storing admin UI language preference.
const SessionKeyAdminLang = "admin_lang"

// blankLinesRegex matches two or more consecutive newlines (with optional whitespace between).
var blankLinesRegex = regexp.MustCompile(`(\r?\n\s*){2,}`)

// SidebarModule represents a module to display in the admin sidebar.
type SidebarModule struct {
	Name     string
	Label    string
	AdminURL string
}

// SidebarModuleProvider provides sidebar modules for the renderer.
type SidebarModuleProvider interface {
	ListSidebarModules() []SidebarModule
}

// Renderer handles template rendering with caching.
type Renderer struct {
	templates             map[string]*template.Template
	sessionManager        *scs.SessionManager
	menuService           *service.MenuService
	sidebarModuleProvider SidebarModuleProvider
	db                    *sql.DB
	isDev                 bool
	extraFuncs            template.FuncMap
	templatesFS           fs.FS // Stored for reloading after adding module funcs
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
		// Create MenuService without cache (stats won't be tracked)
		menuSvc = service.NewMenuService(cfg.DB, nil)
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

// SetSidebarModuleProvider sets the provider for sidebar modules.
// This is called after modules are initialized since they're registered after the renderer is created.
func (r *Renderer) SetSidebarModuleProvider(provider SidebarModuleProvider) {
	r.sidebarModuleProvider = provider
}

// templateParseConfig defines how to parse a group of templates.
type templateParseConfig struct {
	dir     string   // Directory containing templates
	prefix  string   // Prefix for template names (e.g., "admin/")
	layouts []string // Layout files to include before partials
}

// parseTemplateGroup parses all templates in a directory with the given configuration.
func (r *Renderer) parseTemplateGroup(templatesFS fs.FS, partials []string, cfg templateParseConfig) error {
	templates := r.getTemplateFiles(templatesFS, cfg.dir)

	for _, tmplPath := range templates {
		name := cfg.prefix + strings.TrimSuffix(filepath.Base(tmplPath), ".html")

		files := append([]string{}, cfg.layouts...)
		files = append(files, partials...)
		files = append(files, tmplPath)

		tmpl, err := template.New("").Funcs(r.TemplateFuncs()).ParseFS(templatesFS, files...)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", name, err)
		}

		r.templates[name] = tmpl
	}
	return nil
}

// parseTemplates parses all templates from the filesystem.
func (r *Renderer) parseTemplates(templatesFS fs.FS) error {
	partials := r.getTemplateFiles(templatesFS, "partials")
	baseLayout := "layouts/base.html"
	adminLayout := "layouts/admin.html"

	// Define template groups with their layouts
	groups := []templateParseConfig{
		{dir: "admin", prefix: "admin/", layouts: []string{baseLayout, adminLayout}},
		{dir: "auth", prefix: "auth/", layouts: []string{baseLayout}},
		{dir: "errors", prefix: "errors/", layouts: []string{baseLayout, adminLayout}},
		{dir: "public", prefix: "public/", layouts: nil}, // Public templates are standalone
	}

	for _, cfg := range groups {
		if err := r.parseTemplateGroup(templatesFS, partials, cfg); err != nil {
			return err
		}
	}

	return nil
}

// getTemplateFiles returns all .html files in a directory.
// If the directory doesn't exist, an empty slice is returned.
func (r *Renderer) getTemplateFiles(templatesFS fs.FS, dir string) []string {
	var files []string

	entries, err := fs.ReadDir(templatesFS, dir)
	if err != nil {
		// Directory might not exist yet, that's ok
		return files
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".html") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	return files
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
		"lower": strings.ToLower,
		"upper": strings.ToUpper,
		"now": time.Now,
		"timeBefore": func(t1, t2 time.Time) bool {
			return t1.Before(t2)
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
			return applyTimeFormatter(t, lang, formatDateForLocale)
		},
		// formatDateTimeLocale formats a datetime according to the specified language.
		// Usage: {{formatDateTimeLocale .UpdatedAt .AdminLang}}
		// Handles both time.Time and *time.Time (returns empty string for nil)
		"formatDateTimeLocale": func(t any, lang string) string {
			return applyTimeFormatter(t, lang, formatDateTimeForLocale)
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
		"repeat": strings.Repeat,
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
				return "[]"
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
		"formatNumber": func(n int64) string {
			if n < 1000 {
				return strconv.FormatInt(n, 10)
			}
			s := strconv.FormatInt(n, 10)
			var result strings.Builder
			for i, c := range s {
				if i > 0 && (len(s)-i)%3 == 0 {
					result.WriteRune(',')
				}
				result.WriteRune(c)
			}
			return result.String()
		},
		"countryName": geoip.CountryName,
		// Sentinel no-op placeholders; the module overrides them via AddTemplateFuncs.
		"sentinelIsActive":        func() bool { return false },
		"sentinelIsIPBanned":      func(ip string) bool { return false },
		"sentinelIsIPWhitelisted": func(ip string) bool { return false },
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
		"hasPrefix": strings.HasPrefix,
		"isDemoMode": middleware.IsDemoMode,
		"maskIP": func(ip string) string {
			if !middleware.IsDemoMode() {
				return ip
			}
			if strings.Contains(ip, ":") && strings.Count(ip, ":") > 1 {
				return "****:****:****:****"
			}
			host := ip
			if idx := strings.LastIndex(ip, ":"); idx != -1 && strings.Count(ip, ":") == 1 {
				host = ip[:idx]
			}
			parts := strings.Split(host, ".")
			if len(parts) == 4 {
				return parts[0] + ".*.*.*"
			}
			return "***"
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
		"T": i18n.T,
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
		// mediaAlt returns the translated alt text for a media item.
		// Falls back to the default alt text if no translation exists for the language.
		// Usage in theme templates: {{mediaAlt .FeaturedImage.ID .LangCode .FeaturedImage.Alt.String}}
		"mediaAlt": func(mediaID int64, langCode string, defaultAlt string) string {
			return r.getMediaTranslation(mediaID, langCode, "alt", defaultAlt)
		},
		// mediaCaption returns the translated caption for a media item.
		// Falls back to the default caption if no translation exists for the language.
		// Usage in theme templates: {{mediaCaption .FeaturedImage.ID .LangCode .FeaturedImage.Caption.String}}
		"mediaCaption": func(mediaID int64, langCode string, defaultCaption string) string {
			return r.getMediaTranslation(mediaID, langCode, "caption", defaultCaption)
		},
		// Placeholder functions for hCaptcha module (will be overwritten if module is loaded)
		"hcaptchaEnabled": func() bool {
			return false
		},
		"hcaptchaWidget": func() template.HTML {
			return ""
		},
		// isAdmin checks if the user has admin role.
		// Usage: {{if isAdmin .User}}...{{end}}
		"isAdmin": func(user any) bool {
			return getUserRole(user) == "admin"
		},
		// isEditor checks if the user has at least editor role (editor or admin).
		// Public users have no admin access.
		// Usage: {{if isEditor .User}}...{{end}}
		"isEditor": func(user any) bool {
			role := getUserRole(user)
			return role == "admin" || role == "editor"
		},
		// userRole returns the user's role string.
		// Usage: {{userRole .User}}
		"userRole": getUserRole,
	}
}

// getMediaTranslation fetches a translated field for a media item.
// Returns the default value if the database is unavailable, parameters are invalid,
// or no translation exists.
func (r *Renderer) getMediaTranslation(mediaID int64, langCode, field, defaultValue string) string {
	if r.db == nil || mediaID == 0 || langCode == "" {
		return defaultValue
	}
	var value string
	query := fmt.Sprintf(`
		SELECT mt.%s FROM media_translations mt
		JOIN languages l ON l.id = mt.language_id
		WHERE mt.media_id = ? AND l.code = ? AND mt.%s != ''
	`, field, field)
	err := r.db.QueryRow(query, mediaID, langCode).Scan(&value)
	if err != nil || value == "" {
		return defaultValue
	}
	return value
}

// Breadcrumb represents a single breadcrumb item.
type Breadcrumb struct {
	Label  string
	URL    string
	Active bool
}

// TemplateData holds data passed to templates.
type TemplateData struct {
	Title          string
	Data           any
	User           any // Current authenticated user (available in all admin templates)
	Flash          string
	FlashType      string
	CurrentYear    int
	CSRFToken      string          // CSRF token value
	CSRFField      template.HTML   // Hidden input field with CSRF token
	SiteName       string          // Site name from config
	Breadcrumbs    []Breadcrumb    // Breadcrumb navigation
	CurrentPath    string          // Current request path for active link detection
	AdminLang      string          // Admin UI language code (en, ru, etc.)
	SidebarModules []SidebarModule // Modules to display in admin sidebar
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

	// CSRF token and field are no longer needed with filippo.io/csrf/gorilla
	// as it uses Fetch metadata headers for protection instead of tokens.
	// These fields are kept empty for backward compatibility with templates.
	data.CSRFToken = ""
	data.CSRFField = ""

	// Get site name from context if not already set
	if data.SiteName == "" {
		data.SiteName = middleware.GetSiteName(req)
	}

	// Get language from context (set by page/form handlers based on content language)
	if data.AdminLang == "" {
		if langInfo := middleware.GetLanguage(req); langInfo != nil {
			data.AdminLang = langInfo.Code
		}
	}
	// Fall back to admin language from session if not already set
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

	// Populate sidebar modules for admin templates
	if r.sidebarModuleProvider != nil && strings.HasPrefix(name, "admin/") {
		data.SidebarModules = r.sidebarModuleProvider.ListSidebarModules()
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

	// Strip consecutive blank lines from the rendered HTML
	compacted := blankLinesRegex.ReplaceAll(buf.Bytes(), []byte("\n"))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(compacted)
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

// RenderPage renders a template and handles errors by logging and returning a 500 response.
// This is a convenience wrapper around Render that eliminates boilerplate error handling.
func (r *Renderer) RenderPage(w http.ResponseWriter, req *http.Request, name string, data TemplateData) {
	if err := r.Render(w, req, name, data); err != nil {
		slog.Error("render error", "error", err, "template", name)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
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

// SetSessionData stores arbitrary data in the session under the given key.
// Data is stored as JSON-encoded string.
func (r *Renderer) SetSessionData(req *http.Request, key string, data map[string]string) {
	if r.sessionManager != nil && data != nil {
		r.sessionManager.Put(req.Context(), key, data)
	}
}

// PopSessionData retrieves and removes data from the session.
// Returns nil if no data found.
func (r *Renderer) PopSessionData(req *http.Request, key string) map[string]string {
	if r.sessionManager != nil {
		if data, ok := r.sessionManager.Pop(req.Context(), key).(map[string]string); ok {
			return data
		}
	}
	return nil
}

// monthsRu contains Russian month names in genitive case.
var monthsRu = []string{
	"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

// applyTimeFormatter applies a time formatting function to a value that may be time.Time or *time.Time.
func applyTimeFormatter(t any, lang string, formatter func(time.Time, string) string) string {
	switch v := t.(type) {
	case time.Time:
		return formatter(v, lang)
	case *time.Time:
		if v == nil {
			return ""
		}
		return formatter(*v, lang)
	default:
		return ""
	}
}

// formatDateForLocale formats a date according to the specified language.
func formatDateForLocale(t time.Time, lang string) string {
	if lang == "ru" {
		return fmt.Sprintf("%d %s %d", t.Day(), monthsRu[t.Month()-1], t.Year())
	}
	return t.Format("Jan 2, 2006")
}

// formatDateTimeForLocale formats a time.Time as a localized datetime string.
func formatDateTimeForLocale(t time.Time, lang string) string {
	if lang == "ru" {
		return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), monthsRu[t.Month()-1], t.Year(), t.Hour(), t.Minute())
	}
	return t.Format("Jan 2, 2006 3:04 PM")
}

// getUserRole extracts the role from a user object.
// Accepts store.User, *store.User, or any struct with a Role field.
func getUserRole(user any) string {
	if user == nil {
		return ""
	}

	// Use reflection to get the Role field since we can't import store
	// package here (would create circular dependency).
	// The user is typically store.User passed from middleware.GetUser().
	v := reflect.ValueOf(user)

	// Handle pointer types
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}

	// Must be a struct
	if v.Kind() != reflect.Struct {
		return ""
	}

	// Get the Role field
	roleField := v.FieldByName("Role")
	if !roleField.IsValid() {
		return ""
	}

	if roleField.Kind() != reflect.String {
		return ""
	}

	return roleField.String()
}
