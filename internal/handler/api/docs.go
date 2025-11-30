package api

import (
	"bytes"
	"context"
	"database/sql"
	"html/template"
	"io/fs"
	"net/http"
	"sync"

	"ocms-go/internal/store"
)

// DocsHandler handles API documentation rendering.
type DocsHandler struct {
	db         *sql.DB
	queries    *store.Queries
	template   *template.Template
	templateFS fs.FS
	mu         sync.RWMutex
	isDev      bool
}

// DocsConfig holds configuration for the docs handler.
type DocsConfig struct {
	DB         *sql.DB
	TemplateFS fs.FS
	IsDev      bool
}

// NewDocsHandler creates a new documentation handler.
func NewDocsHandler(cfg DocsConfig) (*DocsHandler, error) {
	h := &DocsHandler{
		db:         cfg.DB,
		queries:    store.New(cfg.DB),
		templateFS: cfg.TemplateFS,
		isDev:      cfg.IsDev,
	}

	// Parse template on startup
	if err := h.parseTemplate(); err != nil {
		return nil, err
	}

	return h, nil
}

// parseTemplate parses the API documentation template.
func (h *DocsHandler) parseTemplate() error {
	tmpl, err := template.ParseFS(h.templateFS, "api/docs.html")
	if err != nil {
		return err
	}
	h.template = tmpl
	return nil
}

// docsData holds data passed to the documentation template.
type docsData struct {
	SiteName string
	BaseURL  string
}

// getSiteName retrieves the site name from the database.
func (h *DocsHandler) getSiteName(ctx context.Context) string {
	siteName := "oCMS"
	cfg, err := h.queries.GetConfig(ctx, "site_name")
	if err == nil && cfg.Value != "" {
		siteName = cfg.Value
	}
	return siteName
}

// ServeDocs serves the API documentation page.
func (h *DocsHandler) ServeDocs(w http.ResponseWriter, r *http.Request) {
	// In development mode, reparse template on each request for hot reload
	if h.isDev {
		h.mu.Lock()
		if err := h.parseTemplate(); err != nil {
			h.mu.Unlock()
			http.Error(w, "Failed to parse template: "+err.Error(), http.StatusInternalServerError)
			return
		}
		h.mu.Unlock()
	}

	// Build template data
	siteName := h.getSiteName(r.Context())

	// Build base URL from request
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwdProto := r.Header.Get("X-Forwarded-Proto"); fwdProto != "" {
		scheme = fwdProto
	}
	baseURL := scheme + "://" + r.Host

	data := docsData{
		SiteName: siteName,
		BaseURL:  baseURL,
	}

	// Render to buffer first
	h.mu.RLock()
	tmpl := h.template
	h.mu.RUnlock()

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		http.Error(w, "Failed to render template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour
	buf.WriteTo(w)
}
