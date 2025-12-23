// Package hcaptcha provides hCaptcha integration for oCMS.
// Protects login forms from bots and automated attacks.
package hcaptcha

import (
	"context"
	"database/sql"
	"embed"
	"html/template"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Hook names for hCaptcha integration.
const (
	// HookAuthLoginWidget is called to render the captcha widget in login form.
	HookAuthLoginWidget = "auth.login_widget"
	// HookAuthBeforeLogin is called before login to verify captcha.
	HookAuthBeforeLogin = "auth.before_login"
)

// Module implements the module.Module interface for the hCaptcha module.
type Module struct {
	module.BaseModule
	ctx      *module.Context
	settings *Settings
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

	// Load settings from database
	settings, err := loadSettings(ctx.DB)
	if err != nil {
		ctx.Logger.Warn("failed to load hCaptcha settings, using defaults", "error", err)
		settings = &Settings{}
	}

	// Override with environment variables if set
	if ctx.Config.HCaptchaSiteKey != "" {
		settings.SiteKey = ctx.Config.HCaptchaSiteKey
	}
	if ctx.Config.HCaptchaSecretKey != "" {
		settings.SecretKey = ctx.Config.HCaptchaSecretKey
	}

	m.settings = settings

	// Register hooks for login form integration
	m.registerHooks()

	m.ctx.Logger.Info("hCaptcha module initialized",
		"enabled", settings.Enabled,
		"has_site_key", settings.SiteKey != "",
		"has_secret_key", settings.SecretKey != "",
	)
	return nil
}

// registerHooks registers the module's hooks.
func (m *Module) registerHooks() {
	// Register hook for rendering captcha widget
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
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/hcaptcha", m.handleDashboard)
	r.Post("/hcaptcha", m.handleSaveSettings)
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
				// Use test keys as defaults for development
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS hcaptcha_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						enabled INTEGER NOT NULL DEFAULT 0,
						site_key TEXT NOT NULL DEFAULT '` + TestSiteKey + `',
						secret_key TEXT NOT NULL DEFAULT '` + TestSecretKey + `',
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
	}
}

// ReloadSettings reloads settings from the database.
func (m *Module) ReloadSettings() error {
	settings, err := loadSettings(m.ctx.DB)
	if err != nil {
		return err
	}

	// Override with environment variables if set
	if m.ctx.Config.HCaptchaSiteKey != "" {
		settings.SiteKey = m.ctx.Config.HCaptchaSiteKey
	}
	if m.ctx.Config.HCaptchaSecretKey != "" {
		settings.SecretKey = m.ctx.Config.HCaptchaSecretKey
	}

	m.settings = settings
	return nil
}

// IsEnabled returns whether hCaptcha protection is enabled and configured.
func (m *Module) IsEnabled() bool {
	return m.settings != nil && m.settings.Enabled && m.settings.SiteKey != "" && m.settings.SecretKey != ""
}

// GetSettings returns a copy of the current settings (for use by other packages).
func (m *Module) GetSettings() Settings {
	if m.settings == nil {
		return Settings{}
	}
	return *m.settings
}
