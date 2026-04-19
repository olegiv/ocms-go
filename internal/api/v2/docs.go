// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

// DocsServer renders /api/v2/docs (Swagger UI) and serves /api/v2/openapi.json
// and /api/v2/openapi.yaml. The spec is pulled live from the huma API so it
// always matches the currently-registered operations.
type DocsServer struct {
	tmpl    *template.Template
	api     *Handler
	queries *store.Queries
}

// NewDocsServer parses the embedded Swagger UI template and binds it to the
// v2 huma handler.
func NewDocsServer(templatesFS fs.FS, h *Handler) (*DocsServer, error) {
	tmpl, err := template.ParseFS(templatesFS, "api/docs.html")
	if err != nil {
		return nil, fmt.Errorf("parsing api docs template: %w", err)
	}
	return &DocsServer{tmpl: tmpl, api: h, queries: h.Deps.Queries}, nil
}

// ServeDocs renders the Swagger UI page.
func (s *DocsServer) ServeDocs(w http.ResponseWriter, r *http.Request) {
	data := struct {
		SiteName string
		BaseURL  string
		CSPNonce string
	}{
		SiteName: s.siteName(r.Context()),
		BaseURL:  buildBaseURL(r),
		CSPNonce: middleware.GetCSPNonce(r),
	}
	var buf bytes.Buffer
	if err := s.tmpl.Execute(&buf, data); err != nil {
		slog.Error("v2 docs template execute", "error", err)
		http.Error(w, "Failed to render API docs", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = buf.WriteTo(w)
}

// ServeOpenAPIJSON emits the live huma-built OpenAPI 3.1 document as JSON.
func (s *DocsServer) ServeOpenAPIJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(s.api.OpenAPI()); err != nil {
		slog.Error("v2 openapi.json encode", "error", err)
	}
}

// ServeOpenAPIYAML emits the same document as YAML, round-tripped through
// JSON so upstream types don't need YAML struct tags.
func (s *DocsServer) ServeOpenAPIYAML(w http.ResponseWriter, _ *http.Request) {
	jsonBytes, err := json.Marshal(s.api.OpenAPI())
	if err != nil {
		slog.Error("v2 openapi.yaml marshal json", "error", err)
		http.Error(w, "Failed to render API specification", http.StatusInternalServerError)
		return
	}
	var generic any
	if err := json.Unmarshal(jsonBytes, &generic); err != nil {
		slog.Error("v2 openapi.yaml round trip", "error", err)
		http.Error(w, "Failed to render API specification", http.StatusInternalServerError)
		return
	}
	yamlBytes, err := yaml.Marshal(generic)
	if err != nil {
		slog.Error("v2 openapi.yaml marshal", "error", err)
		http.Error(w, "Failed to render API specification", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(yamlBytes)
}

func (s *DocsServer) siteName(ctx context.Context) string {
	if s.queries == nil {
		return "oCMS"
	}
	cfg, err := s.queries.GetConfig(ctx, "site_name")
	if err == nil && cfg.Value != "" {
		return cfg.Value
	}
	return "oCMS"
}

func buildBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	switch strings.ToLower(r.Header.Get("X-Forwarded-Proto")) {
	case "https":
		scheme = "https"
	case "http":
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}
