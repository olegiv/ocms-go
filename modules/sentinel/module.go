// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sentinel provides IP banning functionality for oCMS.
// It allows administrators to ban IPs by full address or wildcard pattern,
// automatically ban IPs accessing certain paths, and whitelist trusted IPs.
package sentinel

import (
	"context"
	"database/sql"
	"embed"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/geoip"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
)

//go:embed locales
var localesFS embed.FS

// BannedIP represents a banned IP record.
type BannedIP struct {
	ID          int64     `json:"id"`
	IPPattern   string    `json:"ip_pattern"`
	CountryCode string    `json:"country_code"`
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	BannedAt    time.Time `json:"banned_at"`
	CreatedBy   int64     `json:"created_by"`
}

// AutoBanPath represents a path pattern that triggers automatic IP banning.
type AutoBanPath struct {
	ID          int64     `json:"id"`
	PathPattern string    `json:"path_pattern"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   int64     `json:"created_by"`
}

// WhitelistedIP represents a whitelisted IP that bypasses all checks.
type WhitelistedIP struct {
	ID        int64     `json:"id"`
	IPPattern string    `json:"ip_pattern"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy int64     `json:"created_by"`
}

// ActiveChecker checks whether a named module is currently active.
// Used by the middleware to check active status at runtime.
type ActiveChecker interface {
	IsActive(name string) bool
}

// EventLogger logs events to the admin event log.
// Satisfied by *service.EventService.
type EventLogger interface {
	LogEvent(ctx context.Context, level, category, message string, userID *int64, ipAddress, requestURL string, metadata map[string]any) error
}

// Session keys for authentication check.
const (
	sessionKeyUserID = "user_id"
	roleAdmin        = "admin"
	roleEditor       = "editor"
)

// Settings keys.
const (
	settingBanCheckEnabled = "ban_check_enabled"
	settingAutoBanEnabled  = "autoban_enabled"
)

// Module implements the module.Module interface for IP banning.
type Module struct {
	module.BaseModule
	ctx *module.Context

	// Session manager for checking authenticated users
	sessionManager *scs.SessionManager

	// activeChecker checks module active status at runtime (set from main.go)
	activeChecker ActiveChecker

	// eventLogger logs security events to the admin event log
	eventLogger EventLogger

	// GeoIP lookup for country resolution
	geoIP *geoip.Lookup

	// Settings cache
	banCheckEnabled bool
	autoBanEnabled  bool
	settingsMu      sync.RWMutex

	// Cache of banned IPs for fast lookup
	bannedPatterns []string
	bannedMu       sync.RWMutex

	// Cache of auto-ban path patterns
	autoBanPaths []string
	pathsMu      sync.RWMutex

	// Cache of whitelisted IPs
	whitelistPatterns []string
	whitelistMu       sync.RWMutex
}

// New creates a new instance of the Sentinel module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"sentinel",
			"1.4.0",
			"IP banning module with auto-ban paths and whitelist support",
		),
		geoIP: geoip.NewLookup(),
		// Default settings (enabled)
		banCheckEnabled: true,
		autoBanEnabled:  true,
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

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
		ctx.Logger.Info("GeoIP not configured for Sentinel, country detection disabled. Set OCMS_GEOIP_DB_PATH to enable.")
	} else {
		ctx.Logger.Info("Sentinel GeoIP database loaded", "path", geoIPPath)
	}

	// Load settings first
	if err := m.reloadSettings(); err != nil {
		return err
	}

	// Load all caches
	if err := m.reloadBannedIPs(); err != nil {
		return err
	}
	if err := m.reloadAutoBanPaths(); err != nil {
		return err
	}
	if err := m.reloadWhitelist(); err != nil {
		return err
	}

	m.ctx.Logger.Info("Sentinel module initialized",
		"banned_count", len(m.bannedPatterns),
		"autoban_paths", len(m.autoBanPaths),
		"whitelisted", len(m.whitelistPatterns),
		"ban_check_enabled", m.banCheckEnabled,
		"autoban_enabled", m.autoBanEnabled,
		"geoip_enabled", m.geoIP.IsEnabled(),
	)
	return nil
}

// SetSessionManager sets the session manager for checking authenticated users.
// This allows the middleware to skip auto-banning for admin/editor users.
func (m *Module) SetSessionManager(sm *scs.SessionManager) {
	m.sessionManager = sm
}

// SetActiveChecker sets the active status checker for runtime middleware checks.
// The middleware uses this to skip processing when the module is deactivated at runtime.
func (m *Module) SetActiveChecker(checker ActiveChecker) {
	m.activeChecker = checker
}

// SetEventLogger sets the event logger for logging security events to the admin event log.
func (m *Module) SetEventLogger(logger EventLogger) {
	m.eventLogger = logger
}

// isAdminOrEditor checks if the current request is from an authenticated admin or editor user.
// Returns true if the user should be exempt from auto-banning.
// Uses named return with recover because the sentinel middleware runs before the session
// middleware (LoadAndSave), so SCS may panic with "no session data in context" for
// public requests. In that case, we treat the user as unauthenticated.
func (m *Module) isAdminOrEditor(r *http.Request) (isAdmin bool) {
	if m.sessionManager == nil || m.ctx == nil {
		return false
	}

	// Recover from SCS panic when session middleware hasn't loaded yet.
	defer func() {
		if rec := recover(); rec != nil {
			isAdmin = false
		}
	}()

	// Get user ID from session
	userID := m.sessionManager.GetInt64(r.Context(), sessionKeyUserID)
	if userID == 0 {
		return false
	}

	// Query database for user role
	queries := store.New(m.ctx.DB)
	user, err := queries.GetUserByID(r.Context(), userID)
	if err != nil {
		return false
	}

	// Check if user is admin or editor
	return user.Role == roleAdmin || user.Role == roleEditor
}

// Shutdown performs cleanup when the module is shutting down.
func (m *Module) Shutdown() error {
	if m.ctx != nil {
		m.ctx.Logger.Info("Sentinel module shutting down")
	}
	if m.geoIP != nil {
		_ = m.geoIP.Close()
	}
	return nil
}

// RegisterRoutes registers public routes for the module.
func (m *Module) RegisterRoutes(_ chi.Router) {
	// No public routes for Sentinel
}

// RegisterAdminRoutes registers admin routes for the module.
func (m *Module) RegisterAdminRoutes(r chi.Router) {
	// Banned IPs
	r.Get("/sentinel", m.handleAdminList)
	r.Post("/sentinel", m.handleCreate)
	r.Post("/sentinel/ban", m.handleBanAjax)
	r.Delete("/sentinel/{id}", m.handleDelete)

	// Auto-ban paths
	r.Post("/sentinel/paths", m.handleCreatePath)
	r.Delete("/sentinel/paths/{id}", m.handleDeletePath)

	// Whitelist
	r.Post("/sentinel/whitelist", m.handleCreateWhitelist)
	r.Delete("/sentinel/whitelist/{id}", m.handleDeleteWhitelist)

	// Settings
	r.Post("/sentinel/settings", m.handleUpdateSettings)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"sentinelVersion": func() string {
			return m.Version()
		},
		"countryName":     geoip.CountryName,
		"sentinelIsActive": func() bool { return true },
		"sentinelIsIPBanned": func(ip string) bool {
			if ip == "" {
				return false
			}
			return m.IsIPBanned(ip)
		},
		"sentinelIsIPWhitelisted": func(ip string) bool {
			if ip == "" {
				return false
			}
			return m.IsIPWhitelisted(ip)
		},
	}
}

// AdminURL returns the admin dashboard URL for the module.
func (m *Module) AdminURL() string {
	return "/admin/sentinel"
}

// SidebarLabel returns the display label for the admin sidebar.
func (m *Module) SidebarLabel() string {
	return "Sentinel"
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
			Description: "Create sentinel_banned_ips table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS sentinel_banned_ips (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						ip_pattern TEXT NOT NULL,
						notes TEXT DEFAULT '',
						url TEXT DEFAULT '',
						banned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						created_by INTEGER DEFAULT 0,
						UNIQUE(ip_pattern)
					)
				`)
				if err != nil {
					return err
				}
				_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_sentinel_ip_pattern ON sentinel_banned_ips(ip_pattern)`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS sentinel_banned_ips`)
				return err
			},
		},
		{
			Version:     2,
			Description: "Create sentinel_autoban_paths table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS sentinel_autoban_paths (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						path_pattern TEXT NOT NULL,
						notes TEXT DEFAULT '',
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						created_by INTEGER DEFAULT 0,
						UNIQUE(path_pattern)
					)
				`)
				if err != nil {
					return err
				}
				_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_sentinel_path_pattern ON sentinel_autoban_paths(path_pattern)`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS sentinel_autoban_paths`)
				return err
			},
		},
		{
			Version:     3,
			Description: "Create sentinel_whitelist table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS sentinel_whitelist (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						ip_pattern TEXT NOT NULL,
						notes TEXT DEFAULT '',
						created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
						created_by INTEGER DEFAULT 0,
						UNIQUE(ip_pattern)
					)
				`)
				if err != nil {
					return err
				}
				_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_sentinel_whitelist_pattern ON sentinel_whitelist(ip_pattern)`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS sentinel_whitelist`)
				return err
			},
		},
		{
			Version:     4,
			Description: "Create sentinel_settings table",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`
					CREATE TABLE IF NOT EXISTS sentinel_settings (
						key TEXT PRIMARY KEY,
						value TEXT NOT NULL DEFAULT ''
					)
				`)
				if err != nil {
					return err
				}
				// Insert default settings (both enabled)
				_, err = db.Exec(`INSERT OR IGNORE INTO sentinel_settings (key, value) VALUES (?, ?)`, settingBanCheckEnabled, "true")
				if err != nil {
					return err
				}
				_, err = db.Exec(`INSERT OR IGNORE INTO sentinel_settings (key, value) VALUES (?, ?)`, settingAutoBanEnabled, "true")
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS sentinel_settings`)
				return err
			},
		},
		{
			Version:     5,
			Description: "Add country_code column to sentinel_banned_ips",
			Up: func(db *sql.DB) error {
				_, err := db.Exec(`ALTER TABLE sentinel_banned_ips ADD COLUMN country_code TEXT NOT NULL DEFAULT ''`)
				return err
			},
			Down: func(db *sql.DB) error {
				// SQLite doesn't support DROP COLUMN, so we'd need to recreate the table
				// For simplicity, we'll just leave the column
				return nil
			},
		},
		{
			Version:     6,
			Description: "Seed default auto-ban paths for common attack vectors",
			Up: func(db *sql.DB) error {
				defaults := []struct {
					pattern, notes string
				}{
					{"/wp-admin*", "WordPress admin - common attack target"},
					{"/wp-login*", "WordPress login - common attack target"},
					{"*/.env", "Environment files - sensitive data exposure"},
					{"*/xmlrpc.php", "WordPress XML-RPC - brute force target"},
					{"/wp-includes*", "WordPress includes - common probe"},
					{"*/phpmyadmin*", "phpMyAdmin - database management probe"},
					{"*/wp-content/plugins*", "WordPress plugins - vulnerability scan"},
				}
				for _, d := range defaults {
					_, err := db.Exec(
						`INSERT OR IGNORE INTO sentinel_autoban_paths (path_pattern, notes, created_at, created_by) VALUES (?, ?, CURRENT_TIMESTAMP, 0)`,
						d.pattern, d.notes,
					)
					if err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DELETE FROM sentinel_autoban_paths WHERE created_by = 0`)
				return err
			},
		},
	}
}

// reloadPatterns loads string patterns from a database query into the given slice behind a mutex.
func (m *Module) reloadPatterns(query string, mu *sync.RWMutex, dest *[]string) error {
	rows, err := m.ctx.DB.Query(query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var patterns []string
	for rows.Next() {
		var pattern string
		if err := rows.Scan(&pattern); err != nil {
			return err
		}
		patterns = append(patterns, pattern)
	}

	mu.Lock()
	*dest = patterns
	mu.Unlock()

	return rows.Err()
}

// reloadBannedIPs loads banned IP patterns from the database into memory.
func (m *Module) reloadBannedIPs() error {
	return m.reloadPatterns(`SELECT ip_pattern FROM sentinel_banned_ips`, &m.bannedMu, &m.bannedPatterns)
}

// reloadAutoBanPaths loads auto-ban path patterns from the database into memory.
func (m *Module) reloadAutoBanPaths() error {
	return m.reloadPatterns(`SELECT path_pattern FROM sentinel_autoban_paths`, &m.pathsMu, &m.autoBanPaths)
}

// reloadWhitelist loads whitelisted IP patterns from the database into memory.
func (m *Module) reloadWhitelist() error {
	return m.reloadPatterns(`SELECT ip_pattern FROM sentinel_whitelist`, &m.whitelistMu, &m.whitelistPatterns)
}

// reloadSettings loads settings from the database into memory.
func (m *Module) reloadSettings() error {
	// Try to read settings from database
	rows, err := m.ctx.DB.Query(`SELECT key, value FROM sentinel_settings`)
	if err != nil {
		// Table might not exist yet (before migration), use defaults
		m.settingsMu.Lock()
		m.banCheckEnabled = true
		m.autoBanEnabled = true
		m.settingsMu.Unlock()
		return nil
	}
	defer func() { _ = rows.Close() }()

	// Start with defaults, override from DB rows
	banCheck := true
	autoBan := true

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		switch key {
		case settingBanCheckEnabled:
			banCheck = value == "true"
		case settingAutoBanEnabled:
			autoBan = value == "true"
		}
	}

	m.settingsMu.Lock()
	m.banCheckEnabled = banCheck
	m.autoBanEnabled = autoBan
	m.settingsMu.Unlock()

	return rows.Err()
}

// IsBanCheckEnabled returns whether IP ban checking is enabled.
func (m *Module) IsBanCheckEnabled() bool {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.banCheckEnabled
}

// IsAutoBanEnabled returns whether auto-ban by path is enabled.
func (m *Module) IsAutoBanEnabled() bool {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.autoBanEnabled
}

// IsIPWhitelisted checks if the given IP matches any whitelisted pattern.
func (m *Module) IsIPWhitelisted(ip string) bool {
	m.whitelistMu.RLock()
	patterns := m.whitelistPatterns
	m.whitelistMu.RUnlock()

	for _, pattern := range patterns {
		if matchIPPattern(pattern, ip) {
			return true
		}
	}
	return false
}

// IsIPBanned checks if the given IP matches any banned pattern.
func (m *Module) IsIPBanned(ip string) bool {
	m.bannedMu.RLock()
	patterns := m.bannedPatterns
	m.bannedMu.RUnlock()

	for _, pattern := range patterns {
		if matchIPPattern(pattern, ip) {
			return true
		}
	}
	return false
}

// CheckAutoBanPath checks if the path matches any auto-ban pattern.
// Returns the matched pattern if found, empty string otherwise.
func (m *Module) CheckAutoBanPath(path string) string {
	m.pathsMu.RLock()
	patterns := m.autoBanPaths
	m.pathsMu.RUnlock()

	for _, pattern := range patterns {
		if matchPathPattern(pattern, path) {
			return pattern
		}
	}
	return ""
}

// matchIPPattern checks if an IP matches a pattern.
// Supports wildcards (*) in any octet position:
//   - "192.168.1.*" matches any IP in 192.168.1.0/24
//   - "192.168.*.*" matches any IP in 192.168.0.0/16
//   - "192.*.1.1" matches 192.x.1.1 for any x
//   - "192.168.*" short form matches any IP starting with 192.168.
//   - "10*" partial wildcard matches "100", "101", etc.
func matchIPPattern(pattern, ip string) bool {
	if pattern == "" || ip == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if pattern == ip {
		return true
	}

	patternParts := strings.Split(pattern, ".")
	ipParts := strings.Split(ip, ".")

	// If pattern has fewer parts than IP and ends with *, do prefix matching
	// e.g., "192.168.*" matches "192.168.1.100"
	if len(patternParts) < len(ipParts) {
		lastPart := patternParts[len(patternParts)-1]
		if lastPart == "*" {
			// Check all parts before the wildcard match exactly
			for i := 0; i < len(patternParts)-1; i++ {
				if patternParts[i] != ipParts[i] {
					return false
				}
			}
			return true
		}
		// Pattern like "10*" - partial match on first octet, rest is prefix
		if prefix, ok := strings.CutSuffix(lastPart, "*"); ok {
			for i := 0; i < len(patternParts)-1; i++ {
				if patternParts[i] != ipParts[i] {
					return false
				}
			}
			return strings.HasPrefix(ipParts[len(patternParts)-1], prefix)
		}
		return false
	}

	// Must have same number of parts for full octet matching
	if len(patternParts) != len(ipParts) {
		return false
	}

	for i, pp := range patternParts {
		if pp == "*" {
			continue // wildcard matches any octet
		}
		// Handle partial wildcard like "10*" matching "100", "101", etc.
		if prefix, ok := strings.CutSuffix(pp, "*"); ok {
			if !strings.HasPrefix(ipParts[i], prefix) {
				return false
			}
			continue
		}
		if pp != ipParts[i] {
			return false
		}
	}
	return true
}

// matchPathPattern checks if a path matches a pattern.
// Supported patterns:
//   - "/something" - exact match
//   - "/something*" - starts with /something
//   - "*/something" - ends with /something
//   - "*/something*" - contains /something
func matchPathPattern(pattern, path string) bool {
	if pattern == "" || path == "" {
		return false
	}

	startsWithWildcard := strings.HasPrefix(pattern, "*")
	endsWithWildcard := strings.HasSuffix(pattern, "*")

	// Remove wildcards to get the core pattern
	core := strings.TrimPrefix(pattern, "*")
	core = strings.TrimSuffix(core, "*")

	switch {
	case startsWithWildcard && endsWithWildcard:
		// */something* - contains
		return strings.Contains(path, core)
	case startsWithWildcard:
		// */something - ends with
		return strings.HasSuffix(path, core)
	case endsWithWildcard:
		// /something* - starts with
		return strings.HasPrefix(path, core)
	default:
		// /something - exact match
		return path == pattern
	}
}

// autoBanIP automatically bans an IP that triggered an auto-ban path.
func (m *Module) autoBanIP(ip, triggeredPath, matchedPattern string) error {
	notes := "Auto-banned for accessing: " + triggeredPath
	countryCode := m.geoIP.LookupCountry(ip)
	_, err := m.ctx.DB.Exec(`
		INSERT OR IGNORE INTO sentinel_banned_ips (ip_pattern, country_code, notes, url, banned_at, created_by)
		VALUES (?, ?, ?, ?, ?, 0)
	`, ip, countryCode, notes, triggeredPath, time.Now())
	if err != nil {
		return err
	}

	// Log to admin event log
	if m.eventLogger != nil {
		_ = m.eventLogger.LogEvent(context.Background(), model.EventLevelWarning, model.EventCategorySecurity,
			"IP auto-banned by Sentinel", nil, ip, triggeredPath, map[string]any{
				"pattern": matchedPattern,
				"country": countryCode,
			})
	}

	// Reload cache
	return m.reloadBannedIPs()
}

// LookupCountry returns the country code for an IP address.
func (m *Module) LookupCountry(ip string) string {
	return m.geoIP.LookupCountry(ip)
}

// GetMiddleware returns the IP ban checking middleware for use in router setup.
func (m *Module) GetMiddleware() func(http.Handler) http.Handler {
	return m.Middleware()
}

// normalizePath strips trailing slashes for consistent pattern matching.
// Root path "/" is preserved as-is.
func normalizePath(path string) string {
	if len(path) > 1 && path[len(path)-1] == '/' {
		return path[:len(path)-1]
	}
	return path
}

// Middleware returns HTTP middleware that checks whitelist, bans, and auto-ban paths.
func (m *Module) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip if module not initialized
			if m.ctx == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Skip if module is deactivated at runtime
			if m.activeChecker != nil && !m.activeChecker.IsActive("sentinel") {
				next.ServeHTTP(w, r)
				return
			}

			ip := getClientIP(r)

			// Normalize path: strip trailing slash so /wp-admin/ matches /wp-admin patterns
			path := normalizePath(r.URL.Path)

			// 1. Check whitelist first - whitelisted IPs bypass all checks
			if m.IsIPWhitelisted(ip) {
				next.ServeHTTP(w, r)
				return
			}

			// 2. Check if IP is already banned (if ban check is enabled)
			if m.IsBanCheckEnabled() && m.IsIPBanned(ip) {
				m.ctx.Logger.Info("blocked banned IP", "ip", ip, "path", path)
				http.Error(w, i18n.T("en", "sentinel.access_denied"), http.StatusForbidden)
				return
			}

			// 3. Check auto-ban paths - ban IP if accessing forbidden path (if auto-ban is enabled)
			// Skip auto-ban for authenticated admin/editor users
			if m.IsAutoBanEnabled() {
				if matchedPattern := m.CheckAutoBanPath(path); matchedPattern != "" {
					// Check if user is admin or editor before auto-banning
					if m.isAdminOrEditor(r) {
						m.ctx.Logger.Debug("skipping auto-ban for admin/editor user",
							"ip", ip,
							"path", path,
							"pattern", matchedPattern,
						)
						next.ServeHTTP(w, r)
						return
					}

					m.ctx.Logger.Info("auto-banning IP for forbidden path",
						"ip", ip,
						"path", path,
						"pattern", matchedPattern,
					)
					if err := m.autoBanIP(ip, path, matchedPattern); err != nil {
						m.ctx.Logger.Error("failed to auto-ban IP", "error", err, "ip", ip)
					}
					http.Error(w, i18n.T("en", "sentinel.access_denied"), http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if before, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(before)
		}
		return strings.TrimSpace(xff)
	}

	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}
