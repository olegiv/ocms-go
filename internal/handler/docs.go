// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yuin/goldmark"

	"github.com/olegiv/ocms-go/internal/config"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/version"
)

// DocsDir is the default directory containing documentation files.
const DocsDir = "./docs"

// DocsHandler handles the site documentation admin page.
type DocsHandler struct {
	renderer    *render.Renderer
	cfg         *config.Config
	registry    *module.Registry
	versionInfo *version.Info
	docsDir     string
	startTime   time.Time
}

// NewDocsHandler creates a new DocsHandler.
func NewDocsHandler(renderer *render.Renderer, cfg *config.Config, registry *module.Registry, startTime time.Time, versionInfo *version.Info) *DocsHandler {
	return &DocsHandler{
		renderer:    renderer,
		cfg:         cfg,
		registry:    registry,
		versionInfo: versionInfo,
		docsDir:     DocsDir,
		startTime:   startTime,
	}
}

// DocsPageData holds data for the site docs overview page.
type DocsPageData struct {
	System    DocsSystemInfo
	Endpoints []DocsEndpointGroup
	Guides    []DocsGuide
}

// DocsSystemInfo contains system-level information for display.
type DocsSystemInfo struct {
	Version        string
	GitCommit      string
	BuildTime      string
	GoVersion      string
	Environment    string
	ServerPort     int
	DBPath         string
	ActiveTheme    string
	CacheType      string
	EnabledModules int
	TotalModules   int
	Uptime         string
}

// DocsEndpointGroup groups related endpoints.
type DocsEndpointGroup struct {
	Name      string
	Endpoints []DocsEndpoint
}

// DocsEndpoint describes a single API/route endpoint.
type DocsEndpoint struct {
	Method      string
	Path        string
	Description string
	Auth        string
}

// DocsGuide represents a documentation file available for viewing.
type DocsGuide struct {
	Slug  string
	Title string
}

// DocsGuideData holds data for the guide viewer page.
type DocsGuideData struct {
	Title   string
	Content template.HTML
}

// Overview handles GET /admin/docs - displays the site documentation overview.
func (h *DocsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	data := DocsPageData{
		System:    h.getSystemInfo(),
		Endpoints: h.getEndpoints(lang),
		Guides:    h.listGuides(),
	}

	h.renderer.RenderPage(w, r, "admin/docs", render.TemplateData{
		Title: i18n.T(lang, "docs.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "docs.title"), URL: redirectAdminDocs, Active: true},
		},
	})
}

// Guide handles GET /admin/docs/{slug} - displays a specific documentation guide.
func (h *DocsHandler) Guide(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	slug := chi.URLParam(r, "slug")

	// Validate slug: only allow alphanumeric, hyphens, and underscores
	if slug == "" || !isValidDocsSlug(slug) {
		http.NotFound(w, r)
		return
	}

	// Additional inline validation for CodeQL (isValidDocsSlug already ensures this)
	if strings.Contains(slug, "..") || strings.Contains(slug, "/") || strings.Contains(slug, "\\") {
		http.NotFound(w, r)
		return
	}

	// filePath is safe because isValidDocsSlug() above validates that slug
	// contains only [a-zA-Z0-9_-], preventing path traversal attacks.
	// The slug cannot contain '.', '/', or '\' characters.
	filePath := filepath.Join(h.docsDir, slug+".md")

	content, err := os.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Convert markdown to HTML
	var buf bytes.Buffer
	if err := goldmark.Convert(content, &buf); err != nil {
		http.Error(w, "Failed to render document", http.StatusInternalServerError)
		return
	}

	title := slugToTitle(slug)

	data := DocsGuideData{
		Title:   title,
		Content: template.HTML(buf.String()), //nolint:gosec // trusted local markdown files
	}

	h.renderer.RenderPage(w, r, "admin/docs_guide", render.TemplateData{
		Title: title,
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "docs.title"), URL: redirectAdminDocs},
			{Label: title, Active: true},
		},
	})
}

// isValidDocsSlug validates that a slug contains only safe characters.
func isValidDocsSlug(slug string) bool {
	if slug == "" {
		return false
	}
	for _, c := range slug {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// slugToTitle converts a filename slug to a human-readable title.
func slugToTitle(slug string) string {
	title := strings.ReplaceAll(slug, "-", " ")
	title = strings.ReplaceAll(title, "_", " ")

	// Capitalize first letter of each word
	words := strings.Fields(title)
	for idx, word := range words {
		if word != "" {
			words[idx] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// listGuides scans the docs directory and returns available guides.
func (h *DocsHandler) listGuides() []DocsGuide {
	entries, err := os.ReadDir(h.docsDir)
	if err != nil {
		return nil
	}

	var guides []DocsGuide
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		// Filter out internal planning docs
		if strings.HasPrefix(strings.ToUpper(name), "PHASE") {
			continue
		}

		slug := strings.TrimSuffix(name, ".md")
		guides = append(guides, DocsGuide{
			Slug:  slug,
			Title: slugToTitle(slug),
		})
	}

	sort.Slice(guides, func(i, j int) bool {
		return guides[i].Title < guides[j].Title
	})

	return guides
}

// getSystemInfo builds system information from runtime and config.
func (h *DocsHandler) getSystemInfo() DocsSystemInfo {
	cacheType := "Memory"
	if h.cfg.UseRedisCache() {
		cacheType = "Redis"
	}

	var enabledModules, totalModules int
	if h.registry != nil {
		modules := h.registry.ListInfo()
		totalModules = len(modules)
		for _, m := range modules {
			if m.Active {
				enabledModules++
			}
		}
	}

	// Get version info with defaults if not set
	ver, commit, buildTime := "dev", "unknown", "unknown"
	if h.versionInfo != nil {
		ver = h.versionInfo.Version
		commit = h.versionInfo.GitCommit
		buildTime = h.versionInfo.BuildTime
	}

	return DocsSystemInfo{
		Version:        ver,
		GitCommit:      commit,
		BuildTime:      buildTime,
		GoVersion:      runtime.Version(),
		Environment:    h.cfg.Env,
		ServerPort:     h.cfg.ServerPort,
		DBPath:         h.cfg.DBPath,
		ActiveTheme:    h.cfg.ActiveTheme,
		CacheType:      cacheType,
		EnabledModules: enabledModules,
		TotalModules:   totalModules,
		Uptime:         time.Since(h.startTime).Round(time.Second).String(),
	}
}

// getEndpoints returns the endpoint reference data grouped by category.
func (h *DocsHandler) getEndpoints(lang string) []DocsEndpointGroup {
	return []DocsEndpointGroup{
		{
			Name: i18n.T(lang, "docs.group_health"),
			Endpoints: []DocsEndpoint{
				{Method: "GET", Path: "/health", Description: i18n.T(lang, "docs.ep_health"), Auth: i18n.T(lang, "docs.auth_public_or_key")},
				{Method: "GET", Path: "/health/live", Description: i18n.T(lang, "docs.ep_health_live"), Auth: i18n.T(lang, "docs.auth_none")},
				{Method: "GET", Path: "/health/ready", Description: i18n.T(lang, "docs.ep_health_ready"), Auth: i18n.T(lang, "docs.auth_public_or_key")},
			},
		},
		{
			Name: i18n.T(lang, "docs.group_public"),
			Endpoints: []DocsEndpoint{
				{Method: "GET", Path: "/", Description: i18n.T(lang, "docs.ep_homepage"), Auth: i18n.T(lang, "docs.auth_none")},
				{Method: "GET", Path: "/blog", Description: i18n.T(lang, "docs.ep_blog"), Auth: i18n.T(lang, "docs.auth_none")},
				{Method: "GET", Path: "/sitemap.xml", Description: i18n.T(lang, "docs.ep_sitemap"), Auth: i18n.T(lang, "docs.auth_none")},
				{Method: "GET", Path: "/robots.txt", Description: i18n.T(lang, "docs.ep_robots"), Auth: i18n.T(lang, "docs.auth_none")},
			},
		},
		{
			Name: i18n.T(lang, "docs.group_api"),
			Endpoints: []DocsEndpoint{
				{Method: "GET", Path: "/api/v1/pages", Description: i18n.T(lang, "docs.ep_api_pages_list"), Auth: i18n.T(lang, "docs.auth_api_key")},
				{Method: "POST", Path: "/api/v1/pages", Description: i18n.T(lang, "docs.ep_api_pages_create"), Auth: "pages:write"},
				{Method: "GET", Path: "/api/v1/pages/{id}", Description: i18n.T(lang, "docs.ep_api_pages_get"), Auth: i18n.T(lang, "docs.auth_api_key")},
				{Method: "PUT", Path: "/api/v1/pages/{id}", Description: i18n.T(lang, "docs.ep_api_pages_update"), Auth: "pages:write"},
				{Method: "DELETE", Path: "/api/v1/pages/{id}", Description: i18n.T(lang, "docs.ep_api_pages_delete"), Auth: "pages:write"},
				{Method: "GET", Path: "/api/v1/media", Description: i18n.T(lang, "docs.ep_api_media"), Auth: i18n.T(lang, "docs.auth_api_key")},
				{Method: "GET", Path: "/api/v1/tags", Description: i18n.T(lang, "docs.ep_api_tags"), Auth: i18n.T(lang, "docs.auth_api_key")},
				{Method: "GET", Path: "/api/v1/categories", Description: i18n.T(lang, "docs.ep_api_categories"), Auth: i18n.T(lang, "docs.auth_api_key")},
				{Method: "GET", Path: "/api/v1/docs", Description: i18n.T(lang, "docs.ep_api_docs"), Auth: i18n.T(lang, "docs.auth_none")},
			},
		},
		{
			Name: i18n.T(lang, "docs.group_admin"),
			Endpoints: []DocsEndpoint{
				{Method: "GET", Path: "/admin/", Description: i18n.T(lang, "docs.ep_admin_dashboard"), Auth: i18n.T(lang, "docs.auth_session")},
				{Method: "GET", Path: "/admin/pages", Description: i18n.T(lang, "docs.ep_admin_pages"), Auth: i18n.T(lang, "docs.auth_editor")},
				{Method: "GET", Path: "/admin/media", Description: i18n.T(lang, "docs.ep_admin_media"), Auth: i18n.T(lang, "docs.auth_editor")},
				{Method: "GET", Path: "/admin/users", Description: i18n.T(lang, "docs.ep_admin_users"), Auth: i18n.T(lang, "docs.auth_admin")},
				{Method: "GET", Path: "/admin/config", Description: i18n.T(lang, "docs.ep_admin_config"), Auth: i18n.T(lang, "docs.auth_admin")},
				{Method: "GET", Path: "/admin/api-keys", Description: i18n.T(lang, "docs.ep_admin_api_keys"), Auth: i18n.T(lang, "docs.auth_admin")},
				{Method: "GET", Path: "/admin/webhooks", Description: i18n.T(lang, "docs.ep_admin_webhooks"), Auth: i18n.T(lang, "docs.auth_admin")},
				{Method: "GET", Path: "/admin/cache", Description: i18n.T(lang, "docs.ep_admin_cache"), Auth: i18n.T(lang, "docs.auth_admin")},
			},
		},
	}
}
