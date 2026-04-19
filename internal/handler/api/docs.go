// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

//go:embed openapi.yaml
var openAPISpecYAML []byte

// DocsHandler handles API documentation rendering.
type DocsHandler struct {
	db             *sql.DB
	queries        *store.Queries
	template       *template.Template
	templateFS     fs.FS
	mu             sync.RWMutex
	isDev          bool
	v2OpenAPISrc   func() any // supplies the live huma-built OpenAPI 3.1 doc for /api/v2
	v2OpenAPISrcMu sync.RWMutex
}

// SetV2OpenAPISource wires the v2 huma.OpenAPI() accessor. Called from main
// after the v2 router has been registered so the spec endpoints can marshal
// the current document on each request.
func (h *DocsHandler) SetV2OpenAPISource(src func() any) {
	h.v2OpenAPISrcMu.Lock()
	h.v2OpenAPISrc = src
	h.v2OpenAPISrcMu.Unlock()
}

// v2SpecOrNil returns the current v2 OpenAPI document, or nil if v2 is not
// mounted (e.g. in unit tests that don't boot the whole router).
func (h *DocsHandler) v2SpecOrNil() any {
	h.v2OpenAPISrcMu.RLock()
	defer h.v2OpenAPISrcMu.RUnlock()
	if h.v2OpenAPISrc == nil {
		return nil
	}
	return h.v2OpenAPISrc()
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
		templateFS: cfg.TemplateFS,
		isDev:      cfg.IsDev,
	}
	if cfg.DB != nil {
		h.queries = store.New(cfg.DB)
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
	CSPNonce string
}

// getSiteName retrieves the site name from the database.
func (h *DocsHandler) getSiteName(ctx context.Context) string {
	siteName := "oCMS"
	if h.queries == nil {
		return siteName
	}
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
		CSPNonce: middleware.GetCSPNonce(r),
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
	_, _ = buf.WriteTo(w)
}

// ServeOpenAPIYAML returns the embedded OpenAPI 3.1 spec as YAML.
func (h *DocsHandler) ServeOpenAPIYAML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(openAPISpecYAML)
}

// ServeOpenAPIJSON returns the embedded OpenAPI 3.1 spec converted to JSON.
func (h *DocsHandler) ServeOpenAPIJSON(w http.ResponseWriter, _ *http.Request) {
	var spec any
	if err := yaml.Unmarshal(openAPISpecYAML, &spec); err != nil {
		slog.Error("parsing embedded OpenAPI spec", "error", err)
		WriteInternalError(w, "Error loading API specification")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(spec); err != nil {
		slog.Error("encoding OpenAPI spec", "error", err)
	}
}

// ServeV2OpenAPIJSON serves the huma-generated OpenAPI 3.1 document for /api/v2.
func (h *DocsHandler) ServeV2OpenAPIJSON(w http.ResponseWriter, _ *http.Request) {
	spec := h.v2SpecOrNil()
	if spec == nil {
		WriteInternalError(w, "v2 OpenAPI spec is not available")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(spec); err != nil {
		slog.Error("encoding v2 OpenAPI spec", "error", err)
	}
}

// ServeV2OpenAPIYAML serves the huma-generated OpenAPI 3.1 document for /api/v2
// as YAML, round-tripped through JSON so the output uses the same key order.
func (h *DocsHandler) ServeV2OpenAPIYAML(w http.ResponseWriter, _ *http.Request) {
	spec := h.v2SpecOrNil()
	if spec == nil {
		WriteInternalError(w, "v2 OpenAPI spec is not available")
		return
	}
	// huma's OpenAPI struct marshals JSON natively; round-trip through JSON →
	// generic map → YAML so we don't need YAML tags on the upstream types.
	jsonBytes, err := json.Marshal(spec)
	if err != nil {
		slog.Error("marshaling v2 OpenAPI spec to JSON", "error", err)
		WriteInternalError(w, "Error rendering API specification")
		return
	}
	var generic any
	if err := json.Unmarshal(jsonBytes, &generic); err != nil {
		slog.Error("round-tripping v2 OpenAPI spec", "error", err)
		WriteInternalError(w, "Error rendering API specification")
		return
	}
	yamlBytes, err := yaml.Marshal(generic)
	if err != nil {
		slog.Error("encoding v2 OpenAPI spec as YAML", "error", err)
		WriteInternalError(w, "Error rendering API specification")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(yamlBytes)
}
