// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package informer provides a dismissible notification bar at the top of the page.
package informer

import (
	"database/sql"
	"embed"
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// cookieName is the cookie used to track dismissal.
const cookieName = "ocms_informer_dismissed"

// Module implements the module.Module interface for the informer module.
type Module struct {
	module.BaseModule
	ctx      *module.Context
	settings *Settings
}

// New creates a new instance of the informer module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"informer",
			"1.0.0",
			"Dismissible notification bar at the top of the page",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	settings, err := loadSettings(ctx.DB)
	if err != nil {
		ctx.Logger.Warn("failed to load informer settings, using defaults", "error", err)
		settings = defaultSettings()
	}
	m.settings = settings

	m.ctx.Logger.Info("Informer module initialized",
		"enabled", settings.Enabled,
		"text", settings.Text,
	)
	return nil
}

// ReloadSettings reloads settings from the database.
// Used after demo seeding updates the informer_settings table.
func (m *Module) ReloadSettings() error {
	if m.ctx == nil {
		return nil
	}
	settings, err := loadSettings(m.ctx.DB)
	if err != nil {
		return fmt.Errorf("reloading informer settings: %w", err)
	}
	m.settings = settings
	return nil
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Informer module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/informer", m.handleDashboard)
	r.Post("/informer", m.handleSaveSettings)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"informerBar": func() template.HTML {
			return m.renderBar()
		},
	}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/informer"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Informer"
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
			Description: "Create informer_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS informer_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						enabled INTEGER NOT NULL DEFAULT 0,
						text TEXT NOT NULL DEFAULT '',
						bg_color TEXT NOT NULL DEFAULT '#1e40af',
						text_color TEXT NOT NULL DEFAULT '#ffffff',
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT OR IGNORE INTO informer_settings (id) VALUES (1);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS informer_settings`)
				return err
			},
		},
		{
			Version:     2,
			Description: "Add version column for cookie invalidation",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`ALTER TABLE informer_settings ADD COLUMN version INTEGER NOT NULL DEFAULT 1`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE informer_settings_new (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						enabled INTEGER NOT NULL DEFAULT 0,
						text TEXT NOT NULL DEFAULT '',
						bg_color TEXT NOT NULL DEFAULT '#1e40af',
						text_color TEXT NOT NULL DEFAULT '#ffffff',
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT INTO informer_settings_new SELECT id, enabled, text, bg_color, text_color, updated_at FROM informer_settings;
					DROP TABLE informer_settings;
					ALTER TABLE informer_settings_new RENAME TO informer_settings;
				`)
				return err
			},
		},
	}
}

// renderBar generates the HTML for the informer notification bar.
// SECURITY: Output is cast to template.HTML. All admin-controlled values are
// escaped with html.EscapeString before embedding.
func (m *Module) renderBar() template.HTML {
	if m.settings == nil || !m.settings.Enabled || m.settings.Text == "" {
		return ""
	}

	bgColor := html.EscapeString(m.settings.BgColor)
	textColor := html.EscapeString(m.settings.TextColor)
	text := html.EscapeString(m.settings.Text)

	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<div id="informer-bar" style="display:none;background:%s;color:%s;" class="informer-bar">`, bgColor, textColor))
	b.WriteString(`<div class="informer-bar-content">`)
	b.WriteString(`<span class="informer-bar-spinner"></span>`)
	b.WriteString(fmt.Sprintf(`<span class="informer-bar-text">%s</span>`, text))
	b.WriteString(`</div>`)
	b.WriteString(fmt.Sprintf(`<button type="button" class="informer-bar-close" aria-label="Close" style="color:%s;" onclick="dismissInformer()">`, textColor))
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>`)
	b.WriteString(`</button>`)
	b.WriteString(`</div>`)

	// CSS for the bar and spinner animation
	b.WriteString(`<style>
.informer-bar{display:flex;align-items:center;justify-content:space-between;padding:8px 16px;font-size:14px;line-height:1.4;box-shadow:0 2px 4px rgba(0,0,0,0.15)}
.informer-bar-content{display:flex;align-items:center;gap:10px;flex:1;justify-content:center}
.informer-bar-spinner{display:inline-block;width:16px;height:16px;border:2px solid currentColor;border-top-color:transparent;border-radius:50%;animation:informer-spin 0.8s linear infinite;flex-shrink:0}
@keyframes informer-spin{to{transform:rotate(360deg)}}
.informer-bar-text{font-weight:500}
.informer-bar-close{background:none;border:none;cursor:pointer;padding:4px;display:flex;align-items:center;opacity:0.8;transition:opacity 0.2s}
.informer-bar-close:hover{opacity:1}
</style>`)

	// JavaScript for cookie-based dismissal (version-aware: resets on settings change)
	version := html.EscapeString(m.settings.Version)
	b.WriteString(fmt.Sprintf(`<script>
(function(){
var cn="%s",ver="%s";
function getCookie(n){var m=document.cookie.match(new RegExp('(?:^|; )'+n+'=([^;]*)'));return m?decodeURIComponent(m[1]):null}
function setCookie(n,v,d){var e=new Date();e.setTime(e.getTime()+d*864e5);document.cookie=n+'='+encodeURIComponent(v)+';expires='+e.toUTCString()+';path=/;SameSite=Lax'}
if(getCookie(cn)!==ver){var el=document.getElementById('informer-bar');if(el)el.style.display='flex'}
window.dismissInformer=function(){var el=document.getElementById('informer-bar');if(el)el.style.display='none';setCookie(cn,ver,365)}
})();
</script>`, cookieName, version))

	return template.HTML(b.String())
}
