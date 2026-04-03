// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"database/sql"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/geoip"
	"github.com/olegiv/ocms-go/internal/module"
)

//go:embed locales
var localesFS embed.FS

// Module implements the internal analytics module.
type Module struct {
	module.BaseModule
	ctx      *module.Context
	settings *Settings
	geoIP    *geoip.Lookup
	cron     *cron.Cron
	saltMu   sync.RWMutex
}

// New creates a new internal analytics module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"analytics_int",
			"1.0.1",
			"Internal Analytics",
		),
		geoIP: geoip.NewLookup(),
	}
}

// Init initializes the module.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	// Load settings
	settings, err := m.loadSettings()
	if err != nil {
		ctx.Logger.Warn("failed to load analytics_int settings, using defaults", "error", err)
		settings = &Settings{
			Enabled:           true,
			RetentionDays:     365,
			SaltRotationHours: 24,
			ExcludePaths:      []string{},
		}
	}
	m.settings = settings

	// Ensure we have a salt
	if m.settings.CurrentSalt == "" {
		m.settings.CurrentSalt = generateRandomSalt()
		m.settings.SaltCreatedAt = timeNow()
		_ = m.saveSettings()
	}

	// Initialize GeoIP lookup with path from config
	geoIPPath := ""
	if ctx.Config != nil {
		geoIPPath = ctx.Config.GeoIPDBPath
	}
	if err := m.geoIP.Init(geoIPPath); err != nil {
		ctx.Logger.Warn("GeoIP database not available, country detection disabled",
			"error", err,
			"path", geoIPPath,
		)
	} else if geoIPPath == "" {
		ctx.Logger.Info("GeoIP not configured, country detection disabled. Set OCMS_GEOIP_DB_PATH to enable.")
	} else {
		ctx.Logger.Info("GeoIP database loaded", "path", geoIPPath)
	}

	// Start aggregation scheduler
	m.StartAggregator()

	// Schedule periodic GeoIP database reload (check for updates every hour)
	m.scheduleGeoIPReload()

	ctx.Logger.Info("Internal Analytics module initialized",
		"enabled", settings.Enabled,
		"retention_days", settings.RetentionDays,
		"geoip_enabled", m.geoIP.IsEnabled(),
	)

	return nil
}

// Shutdown cleans up resources.
func (m *Module) Shutdown() error {
	if m.cron != nil {
		m.cron.Stop()
	}
	if m.geoIP != nil {
		_ = m.geoIP.Close()
	}
	if m.ctx != nil {
		m.ctx.Logger.Info("Internal Analytics module shutting down")
	}
	return nil
}

// scheduleGeoIPReload schedules periodic GeoIP database reloads.
// This allows the database to be updated without restarting the application.
func (m *Module) scheduleGeoIPReload() {
	if m.cron == nil || !m.geoIP.IsEnabled() {
		return
	}

	const (
		defaultSchedule = "0 * * * *"
		jobName         = "geoip_reload"
	)

	schedule := defaultSchedule
	if m.ctx.SchedulerRegistry != nil {
		schedule = m.ctx.SchedulerRegistry.GetEffectiveSchedule("analytics_int", jobName, defaultSchedule)
	}

	cronFunc := func() {
		if err := m.geoIP.Reload(); err != nil {
			m.ctx.Logger.Debug("GeoIP reload check", "error", err)
		}
	}

	entryID, err := m.cron.AddFunc(schedule, cronFunc)
	if err != nil {
		m.ctx.Logger.Warn("failed to schedule GeoIP reload", "error", err)
		return
	}

	if m.ctx.SchedulerRegistry != nil {
		m.ctx.SchedulerRegistry.Register(
			"analytics_int", jobName,
			"Reload GeoIP database for country detection",
			defaultSchedule,
			m.cron, entryID, cronFunc, nil,
		)
	}
}

// RegisterRoutes registers public routes (none for this module).
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes
}

// RegisterAdminRoutes registers admin routes.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	r.Get("/internal-analytics", m.handleDashboard)
	r.Get("/internal-analytics/api/stats", m.handleAPIStats)
	r.Get("/internal-analytics/api/realtime", m.handleRealtime)
	r.Post("/internal-analytics/settings", m.handleSaveSettings)
	r.Post("/internal-analytics/aggregate", m.handleRunAggregation)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{}
}

// AdminURL returns the admin dashboard URL.
func (m *Module) AdminURL() string {
	return "/admin/internal-analytics"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Analytics"
}

// TranslationsFS returns module translations.
func (m *Module) TranslationsFS() embed.FS {
	return localesFS
}

// Migrations returns database migrations.
func (m *Module) Migrations() []module.Migration {
	return []module.Migration{
		{
			Version:     1,
			Description: "Create page_analytics_views table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS page_analytics_views (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						visitor_hash TEXT NOT NULL,
						path TEXT NOT NULL,
						page_id INTEGER,
						referrer_domain TEXT NOT NULL DEFAULT '',
						country_code TEXT NOT NULL DEFAULT '',
						browser TEXT NOT NULL DEFAULT '',
						os TEXT NOT NULL DEFAULT '',
						device_type TEXT NOT NULL DEFAULT 'desktop',
						language TEXT NOT NULL DEFAULT '',
						session_hash TEXT NOT NULL,
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					CREATE INDEX IF NOT EXISTS idx_pav_created_at ON page_analytics_views(created_at);
					CREATE INDEX IF NOT EXISTS idx_pav_path ON page_analytics_views(path);
					CREATE INDEX IF NOT EXISTS idx_pav_session ON page_analytics_views(session_hash, created_at);
					CREATE INDEX IF NOT EXISTS idx_pav_visitor ON page_analytics_views(visitor_hash, created_at);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS page_analytics_views`)
				return err
			},
		},
		{
			Version:     2,
			Description: "Create page_analytics_hourly table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS page_analytics_hourly (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						hour_start DATETIME NOT NULL,
						path TEXT NOT NULL,
						views INTEGER NOT NULL DEFAULT 0,
						unique_visitors INTEGER NOT NULL DEFAULT 0,
						UNIQUE(hour_start, path)
					);
					CREATE INDEX IF NOT EXISTS idx_pah_hour ON page_analytics_hourly(hour_start);
					CREATE INDEX IF NOT EXISTS idx_pah_path ON page_analytics_hourly(path, hour_start);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS page_analytics_hourly`)
				return err
			},
		},
		{
			Version:     3,
			Description: "Create page_analytics_daily table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS page_analytics_daily (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						date DATE NOT NULL,
						path TEXT NOT NULL,
						views INTEGER NOT NULL DEFAULT 0,
						unique_visitors INTEGER NOT NULL DEFAULT 0,
						bounces INTEGER NOT NULL DEFAULT 0,
						UNIQUE(date, path)
					);
					CREATE INDEX IF NOT EXISTS idx_pad_date ON page_analytics_daily(date);
					CREATE INDEX IF NOT EXISTS idx_pad_path ON page_analytics_daily(path, date);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS page_analytics_daily`)
				return err
			},
		},
		{
			Version:     4,
			Description: "Create page_analytics_referrers table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS page_analytics_referrers (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						date DATE NOT NULL,
						referrer_domain TEXT NOT NULL,
						views INTEGER NOT NULL DEFAULT 0,
						unique_visitors INTEGER NOT NULL DEFAULT 0,
						UNIQUE(date, referrer_domain)
					);
					CREATE INDEX IF NOT EXISTS idx_par_date ON page_analytics_referrers(date);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS page_analytics_referrers`)
				return err
			},
		},
		{
			Version:     5,
			Description: "Create page_analytics_tech table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS page_analytics_tech (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						date DATE NOT NULL,
						browser TEXT NOT NULL,
						os TEXT NOT NULL,
						device_type TEXT NOT NULL,
						views INTEGER NOT NULL DEFAULT 0,
						UNIQUE(date, browser, os, device_type)
					);
					CREATE INDEX IF NOT EXISTS idx_pat_date ON page_analytics_tech(date);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS page_analytics_tech`)
				return err
			},
		},
		{
			Version:     6,
			Description: "Create page_analytics_geo table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS page_analytics_geo (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						date DATE NOT NULL,
						country_code TEXT NOT NULL,
						views INTEGER NOT NULL DEFAULT 0,
						unique_visitors INTEGER NOT NULL DEFAULT 0,
						UNIQUE(date, country_code)
					);
					CREATE INDEX IF NOT EXISTS idx_pag_date ON page_analytics_geo(date);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS page_analytics_geo`)
				return err
			},
		},
		{
			Version:     7,
			Description: "Create page_analytics_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS page_analytics_settings (
						id INTEGER PRIMARY KEY CHECK (id = 1),
						enabled INTEGER NOT NULL DEFAULT 1,
						retention_days INTEGER NOT NULL DEFAULT 365,
						exclude_paths TEXT NOT NULL DEFAULT '[]',
						current_salt TEXT NOT NULL DEFAULT '',
						salt_created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						salt_rotation_hours INTEGER NOT NULL DEFAULT 24,
						updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
					);
					INSERT OR IGNORE INTO page_analytics_settings (id) VALUES (1);
				`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS page_analytics_settings`)
				return err
			},
		},
		{
			Version:     8,
			Description: "Add exclude_ips column to page_analytics_settings",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`ALTER TABLE page_analytics_settings ADD COLUMN exclude_ips TEXT NOT NULL DEFAULT '[]'`)
				return err
			},
			Down: func(db *sql.DB) error {
				// SQLite doesn't support DROP COLUMN before 3.35.0; recreate is complex.
				// Since this is just a column addition, the down migration is a no-op.
				return nil
			},
		},
		{
			Version:     9,
			Description: "Purge self-referral data matching site domain",
			Up: func(db *sql.DB) error {
				var siteURL string
				err := db.QueryRow(`SELECT value FROM config WHERE key = 'site_url'`).Scan(&siteURL)
				if err != nil || siteURL == "" {
					return nil // site_url not configured, nothing to purge
				}

				parsed, _ := url.Parse(siteURL)
				host := ""
				if parsed != nil {
					host = parsed.Host
				}
				if host == "" {
					host = siteURL
				}
				if idx := strings.LastIndex(host, ":"); idx > 0 {
					host = host[:idx]
				}
				host = strings.TrimSpace(host)
				if host == "" {
					return nil
				}

				domain := strings.TrimPrefix(strings.ToLower(host), "www.")
				wwwDomain := "www." + domain

				if _, err := db.Exec(`UPDATE page_analytics_views SET referrer_domain = '' WHERE LOWER(referrer_domain) IN (?, ?)`, domain, wwwDomain); err != nil {
					slog.Error("migration 9: failed to purge self-referral views", "error", err, "domain", domain)
				}
				if _, err := db.Exec(`DELETE FROM page_analytics_referrers WHERE LOWER(referrer_domain) IN (?, ?)`, domain, wwwDomain); err != nil {
					slog.Error("migration 9: failed to purge self-referral referrers", "error", err, "domain", domain)
				}
				return nil
			},
			Down: func(db *sql.DB) error {
				return nil // data purge is not reversible
			},
		},
	}
}

// GetTrackingMiddleware returns the tracking middleware for use in router setup.
// This should be called after Init() to ensure settings are loaded.
func (m *Module) GetTrackingMiddleware() func(next http.Handler) http.Handler {
	return m.TrackingMiddleware()
}

// IsEnabled returns whether analytics tracking is enabled.
func (m *Module) IsEnabled() bool {
	if m.settings == nil {
		return false
	}
	return m.settings.Enabled
}

// getSiteDomain reads the site domain from the config table on each call.
// This ensures config changes take effect immediately without restart.
func (m *Module) getSiteDomain() string {
	if m.ctx == nil || m.ctx.DB == nil {
		return ""
	}

	var siteURL string
	err := m.ctx.DB.QueryRow(`SELECT value FROM config WHERE key = 'site_url'`).Scan(&siteURL)
	if err != nil || siteURL == "" {
		return ""
	}

	return extractDomainFromURL(siteURL)
}

// extractDomainFromURL extracts the hostname from a URL string,
// stripping the scheme, port, and path.
func extractDomainFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	host := parsed.Host
	if host == "" {
		// Might be a plain domain without scheme
		host = rawURL
	}

	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}

	return strings.TrimSpace(host)
}

// getExcludedIPs reads the excluded IPs list from the global config table.
// This ensures config changes take effect immediately without restart.
func (m *Module) getExcludedIPs() []string {
	if m.ctx == nil || m.ctx.DB == nil {
		return nil
	}

	var value string
	err := m.ctx.DB.QueryRow(`SELECT value FROM config WHERE key = 'excluded_ips'`).Scan(&value)
	if err != nil || value == "" {
		return nil
	}

	var ips []string
	for _, line := range strings.Split(value, "\n") {
		ip := strings.TrimSpace(line)
		if ip != "" {
			ips = append(ips, ip)
		}
	}
	return ips
}
