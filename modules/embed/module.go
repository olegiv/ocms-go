// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package embed provides external embed integration for oCMS.
// Supports embedding third-party services like Dify AI chat.
package embed

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"golang.org/x/time/rate"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/modules/embed/providers"
)

//go:embed locales
var localesFS embed.FS

const (
	embedProxyRateLimitRPS         = 2.0
	embedProxyRateLimitBurst       = 10
	embedProxyGlobalRateLimitRPS   = 20.0
	embedProxyGlobalRateLimitBurst = 40
	embedProxyMaxConcurrent        = 32
)

// Module implements the module.Module interface for the embed module.
type Module struct {
	module.BaseModule
	ctx                  *module.Context
	providers            []providers.Provider
	settings             []*ProviderSettings
	publicRateLimiter    *middleware.GlobalRateLimiter
	globalRateLimiter    *rate.Limiter
	proxySemaphore       chan struct{}
	allowedOrigins       map[string]struct{}
	allowedUpstreamHosts map[string]struct{}
	requireOriginPolicy  bool
	proxyToken           string
	requireProxyToken    bool
	mu                   sync.RWMutex
}

// New creates a new instance of the embed module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"embed",
			"1.0.0",
			"External Embed",
		),
		providers: []providers.Provider{
			providers.NewDify(),
		},
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx
	m.publicRateLimiter = middleware.NewGlobalRateLimiter(embedProxyRateLimitRPS, embedProxyRateLimitBurst)
	m.globalRateLimiter = rate.NewLimiter(rate.Limit(embedProxyGlobalRateLimitRPS), embedProxyGlobalRateLimitBurst)
	m.proxySemaphore = make(chan struct{}, embedProxyMaxConcurrent)
	if ctx.Config != nil {
		m.requireOriginPolicy = ctx.Config.Env == "production"
		origins, err := parseAllowedOrigins(ctx.Config.EmbedAllowedOrigins)
		if err != nil {
			return fmt.Errorf("parsing embed allowed origins: %w", err)
		}
		m.allowedOrigins = origins
		upstreamHosts, err := parseAllowedHosts(ctx.Config.EmbedAllowedUpstreamHosts)
		if err != nil {
			return fmt.Errorf("parsing embed allowed upstream hosts: %w", err)
		}
		m.allowedUpstreamHosts = upstreamHosts
		m.proxyToken = strings.TrimSpace(ctx.Config.EmbedProxyToken)
		m.requireProxyToken = ctx.Config.RequireEmbedProxyToken
		if m.requireProxyToken && m.proxyToken == "" {
			return fmt.Errorf("embed proxy token is required but OCMS_EMBED_PROXY_TOKEN is empty")
		}
	}

	// Load enabled provider settings
	if err := m.reloadSettings(); err != nil {
		ctx.Logger.Warn("failed to load embed settings", "error", err)
	}

	m.ctx.Logger.Info("Embed module initialized",
		"providers", len(m.providers),
		"enabled", m.countEnabled(),
		"proxy_rate_limit_rps", embedProxyRateLimitRPS,
		"proxy_rate_limit_burst", embedProxyRateLimitBurst,
		"proxy_global_rate_limit_rps", embedProxyGlobalRateLimitRPS,
		"proxy_global_rate_limit_burst", embedProxyGlobalRateLimitBurst,
		"proxy_max_concurrent", embedProxyMaxConcurrent,
		"allowed_origins", len(m.allowedOrigins),
		"allowed_upstream_hosts", len(m.allowedUpstreamHosts),
		"require_origin_policy", m.requireOriginPolicy,
		"require_proxy_token", m.requireProxyToken,
		"proxy_token_configured", m.proxyToken != "",
		"proxy_token_enforced", m.requireProxyToken || m.proxyToken != "",
	)
	return nil
}

// reloadSettings reloads all enabled provider settings.
func (m *Module) reloadSettings() error {
	settings, err := loadAllEnabledSettings(m.ctx.DB)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()

	return nil
}

// countEnabled returns the number of enabled providers.
func (m *Module) countEnabled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.settings)
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Embed module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(r chi.Router) {
	// Public proxy routes used by frontend widgets.
	// Apply dedicated rate limiting to reduce upstream abuse.
	r.Group(func(r chi.Router) {
		if m.publicRateLimiter != nil {
			r.Use(m.publicRateLimiter.HTMLMiddleware())
		}
		r.Post("/embed/dify/chat-messages", m.handleDifyChatMessagesProxy)
		r.Get("/embed/dify/messages/{messageID}/suggested", m.handleDifySuggestedProxy)
	})
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdmin())
		r.Get("/embed", m.handleList)
		r.Get("/embed/{provider}", m.handleProviderSettings)
		r.Post("/embed/{provider}", m.handleSaveProviderSettings)
		r.Post("/embed/{provider}/toggle", m.handleToggleProvider)
	})
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// embedHead returns scripts for the <head> section
		"embedHead": func() template.HTML {
			return m.renderHead()
		},
		// embedBody returns scripts for before </body>
		"embedBody": func() template.HTML {
			return m.renderBody()
		},
	}
}

// renderScripts generates all enabled provider scripts using the provided render function.
// SECURITY: Output is cast to template.HTML. Provider settings are admin-controlled
// and individual values are escaped with template.HTMLEscapeString before embedding.
func (m *Module) renderScripts(renderFn func(providers.Provider, map[string]string) template.HTML) template.HTML {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var scripts strings.Builder
	for _, ps := range m.settings {
		provider := m.getProvider(ps.ProviderID)
		if provider == nil {
			continue
		}
		scripts.WriteString(string(renderFn(provider, ps.Settings)))
	}
	return template.HTML(scripts.String())
}

// renderHead generates all enabled provider head scripts.
func (m *Module) renderHead() template.HTML {
	return m.renderScripts(func(p providers.Provider, s map[string]string) template.HTML {
		return p.RenderHead(s)
	})
}

// renderBody generates all enabled provider body scripts.
func (m *Module) renderBody() template.HTML {
	return m.renderScripts(func(p providers.Provider, s map[string]string) template.HTML {
		return p.RenderBody(s)
	})
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/embed"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Embed"
}

// TranslationsFS returns the embedded filesystem containing module translations.
func (m *Module) TranslationsFS() embed.FS {
	return localesFS
}

// Migrations returns database migrations for the module.
func (m *Module) Migrations() []module.Migration {
	return []module.Migration{
		{
			Version:     1,
			Description: "Create embed_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS embed_settings (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						provider TEXT NOT NULL UNIQUE,
						settings TEXT NOT NULL DEFAULT '{}',
						is_enabled INTEGER NOT NULL DEFAULT 0,
						position INTEGER NOT NULL DEFAULT 0,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					CREATE INDEX IF NOT EXISTS idx_embed_settings_enabled ON embed_settings(is_enabled);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS embed_settings`)
				return err
			},
		},
	}
}

// ReloadSettings reloads settings from the database.
func (m *Module) ReloadSettings() error {
	return m.reloadSettings()
}

// getEnabledProviderSettings returns settings for an enabled provider.
func (m *Module) getEnabledProviderSettings(providerID string) (map[string]string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ps := range m.settings {
		if ps.ProviderID == providerID && ps.IsEnabled {
			return ps.Settings, true
		}
	}

	return nil, false
}
