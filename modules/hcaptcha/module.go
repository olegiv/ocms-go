// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package hcaptcha provides hCaptcha integration for oCMS.
// Protects login forms from bots and automated attacks.
package hcaptcha

import (
	"context"
	"database/sql"
	"embed"
	"html/template"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Hook names for hCaptcha integration.
const (
	// HookAuthLoginWidget is called to render the captcha widget in login form.
	HookAuthLoginWidget = "auth.login_widget"
	// HookAuthBeforeLogin is called before login to verify captcha.
	HookAuthBeforeLogin = "auth.before_login"
	// HookFormCaptchaWidget is called to render captcha widget in public forms.
	HookFormCaptchaWidget = "form.captcha_widget"
	// HookFormCaptchaVerify is called to verify captcha on form submission.
	HookFormCaptchaVerify = "form.captcha_verify"
)

// Module implements the module.Module interface for the hCaptcha module.
type Module struct {
	module.BaseModule
	ctx      *module.Context
	settings *Settings
	// settingsMu guards settings: the login and form pages read them on every request while
	// the settings form and ReloadSettings replace them. Settings values are
	// replaced, never modified, so a reader may keep using the pointer it
	// got. TestSettingsAccessHoldsTheLock checks that every access to the
	// field takes the lock.
	settingsMu sync.RWMutex
	// reload records whether the stored settings are in effect. If they fail
	// to load, the module runs on the environment keys alone; currentSettings retries the
	// load and the settings form refuses to save until it succeeds.
	reload module.SettingsReloadGate
}

// currentSettings returns the settings in effect, or nil before Init. While
// the stored settings cannot be read it first retries loading them, at most
// once per module.SettingsRetryInterval.
func (m *Module) currentSettings() *Settings {
	if m.ctx != nil && !m.reload.Loaded() {
		switch loaded, err := m.reload.Retry(m.ReloadSettings); {
		case err != nil:
			m.ctx.Logger.Warn("hCaptcha settings still cannot be read; using the fallback", "error", err)
		case loaded:
			m.ctx.Logger.Info("hCaptcha settings loaded; the stored settings apply again")
		}
	}
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.settings
}

// storeSettings replaces the settings in effect.
func (m *Module) storeSettings(settings *Settings) {
	m.settingsMu.Lock()
	m.settings = settings
	m.settingsMu.Unlock()
}

// New creates a new instance of the hCaptcha module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"hcaptcha",
			"1.0.0",
			"hCaptcha integration for login protection",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	// Load settings from database. Without them, turn protection on wherever
	// it can run, even if the stored settings had it off: the environment keys
	// alone are enough to verify logins (OCMS_HCAPTCHA_DISABLED still wins).
	settings, err := loadSettings(ctx.DB)
	if err != nil {
		if ctx.Config.HCaptchaEnabled() && !ctx.Config.HCaptchaDisabled {
			ctx.Logger.Error("failed to load hCaptcha settings; using the environment keys until they load", "error", err)
		} else {
			ctx.Logger.Error("failed to load hCaptcha settings; login protection is off until they load", "error", err)
		}
		settings = &Settings{Enabled: true}
	} else {
		m.reload.MarkLoaded()
	}
	settings = m.withEnvOverrides(settings)
	m.storeSettings(settings)

	// Register hooks for login form integration
	m.registerHooks()

	m.ctx.Logger.Info("hCaptcha module initialized",
		"enabled", settings.Enabled,
		"env_disabled", ctx.Config.HCaptchaDisabled,
		"has_site_key", settings.SiteKey != "",
		"has_secret_key", settings.SecretKey != "",
	)
	return nil
}

// registerHooks registers the module's hooks.
func (m *Module) registerHooks() {
	// Register hook for rendering captcha widget in login form
	m.ctx.Hooks.Register(HookAuthLoginWidget, module.HookHandler{
		Name:     "hcaptcha_widget",
		Module:   m.Name(),
		Priority: 0,
		Fn: func(ctx context.Context, data any) (any, error) {
			return m.RenderWidget(), nil
		},
	})

	// Register hook for verifying captcha before login
	m.ctx.Hooks.Register(HookAuthBeforeLogin, module.HookHandler{
		Name:     "hcaptcha_verify",
		Module:   m.Name(),
		Priority: 0,
		Fn: func(ctx context.Context, data any) (any, error) {
			req, ok := data.(*VerifyRequest)
			if !ok {
				return data, nil
			}
			return m.VerifyFromRequest(req)
		},
	})

	// Register hook for rendering captcha widget in public forms
	m.ctx.Hooks.Register(HookFormCaptchaWidget, module.HookHandler{
		Name:     "hcaptcha_form_widget",
		Module:   m.Name(),
		Priority: 0,
		Fn: func(ctx context.Context, data any) (any, error) {
			return m.RenderWidget(), nil
		},
	})

	// Register hook for verifying captcha in form submissions
	m.ctx.Hooks.Register(HookFormCaptchaVerify, module.HookHandler{
		Name:     "hcaptcha_form_verify",
		Module:   m.Name(),
		Priority: 0,
		Fn: func(ctx context.Context, data any) (any, error) {
			req, ok := data.(*VerifyRequest)
			if !ok {
				return data, nil
			}
			return m.VerifyFromRequest(req)
		},
	})
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("hCaptcha module shutting down")
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for hCaptcha module
}

// RegisterAdminRoutes registers admin routes for the module.
// hCaptcha controls anti-bot protection on login and public forms, so the
// routes are gated to admins at the router level (defense-in-depth beyond the
// Editor-level module-admin route group they are mounted under).
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdmin())
		r.Get("/hcaptcha", m.handleDashboard)
		r.Post("/hcaptcha", m.handleSaveSettings)
	})
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// hcaptchaWidget returns the hCaptcha widget HTML
		"hcaptchaWidget": func() template.HTML {
			return m.RenderWidget()
		},
		// hcaptchaEnabled returns whether hCaptcha is enabled
		"hcaptchaEnabled": func() bool {
			return m.IsEnabled()
		},
	}
}

// CSPSources allows the hCaptcha widget while the module is active: its
// script, styles, challenge frame and API calls are served from hcaptcha.com.
func (m *Module) CSPSources() map[string][]string {
	hosts := []string{"https://hcaptcha.com", "https://*.hcaptcha.com"}
	return map[string][]string{
		"script-src":  hosts,
		"style-src":   hosts,
		"connect-src": hosts,
		"frame-src":   hosts,
	}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/hcaptcha"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "hCaptcha"
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
			Description: "Create hcaptcha_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS hcaptcha_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						enabled INTEGER NOT NULL DEFAULT 0,
						site_key TEXT NOT NULL DEFAULT '',
						secret_key TEXT NOT NULL DEFAULT '',
						theme TEXT NOT NULL DEFAULT 'light',
						size TEXT NOT NULL DEFAULT 'normal',
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT OR IGNORE INTO hcaptcha_settings (id) VALUES (1);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS hcaptcha_settings`)
				return err
			},
		},
		{
			Version:     2,
			Description: "Scrub persisted hCaptcha test keys from upgraded installs",
			Up: func(db *sql.DB) error {
				// v1 previously seeded the hCaptcha test keys as column
				// defaults, so upgraded installs keep them in the row even
				// after v1's defaults are changed. Wipe any row whose keys
				// still match the known test values and disable the module
				// so IsEnabled() returns false until a real key is set.
				_, err := db.Exec(`
					UPDATE hcaptcha_settings
					SET enabled = 0,
					    site_key = '',
					    secret_key = '',
					    updated_at = CURRENT_TIMESTAMP
					WHERE site_key = '10000000-ffff-ffff-ffff-000000000001'
					   OR secret_key = '0x0000000000000000000000000000000000000000'
				`)
				return err
			},
			// Rolling back would restore the insecure test keys, which
			// defeats the purpose of this scrub — keep Down as a no-op.
			Down: func(_ *sql.DB) error { return nil },
		},
	}
}

// ReloadSettings reloads settings from the database.
func (m *Module) ReloadSettings() error {
	settings, err := loadSettings(m.ctx.DB)
	if err != nil {
		return err
	}
	m.storeSettings(m.withEnvOverrides(settings))
	m.reload.MarkLoaded()
	return nil
}

// withEnvOverrides applies the OCMS_HCAPTCHA_* environment settings, which
// take precedence over the stored ones, and returns settings.
func (m *Module) withEnvOverrides(settings *Settings) *Settings {
	if m.ctx.Config.HCaptchaSiteKey != "" {
		settings.SiteKey = m.ctx.Config.HCaptchaSiteKey
	}
	if m.ctx.Config.HCaptchaSecretKey != "" {
		settings.SecretKey = m.ctx.Config.HCaptchaSecretKey
	}
	// Force-disable when OCMS_HCAPTCHA_DISABLED=true
	if m.ctx.Config.HCaptchaDisabled {
		settings.Enabled = false
	}
	return settings
}

// IsEnabled returns whether hCaptcha protection is enabled and configured.
func (m *Module) IsEnabled() bool {
	return settingsEnabled(m.currentSettings())
}

// settingsEnabled reports whether settings turn protection on with both keys set.
func settingsEnabled(settings *Settings) bool {
	return settings != nil && settings.Enabled && settings.SiteKey != "" && settings.SecretKey != ""
}

// GetSettings returns a copy of the current settings (for use by other packages).
func (m *Module) GetSettings() Settings {
	settings := m.currentSettings()
	if settings == nil {
		return Settings{}
	}
	return *settings
}
