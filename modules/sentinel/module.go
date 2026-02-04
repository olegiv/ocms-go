// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sentinel provides IP banning functionality for oCMS.
// It allows administrators to ban IPs by full address or wildcard pattern,
// automatically ban IPs accessing certain paths, and whitelist trusted IPs.
package sentinel

import (
	"database/sql"
	"embed"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
)

//go:embed locales
var localesFS embed.FS

// BannedIP represents a banned IP record.
type BannedIP struct {
	ID        int64     `json:"id"`
	IPPattern string    `json:"ip_pattern"`
	Notes     string    `json:"notes"`
	URL       string    `json:"url"`
	BannedAt  time.Time `json:"banned_at"`
	CreatedBy int64     `json:"created_by"`
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

// Session keys for authentication check.
const (
	sessionKeyUserID = "user_id"
	roleAdmin        = "admin"
	roleEditor       = "editor"
)

// Module implements the module.Module interface for IP banning.
type Module struct {
	module.BaseModule
	ctx *module.Context

	// Session manager for checking authenticated users
	sessionManager *scs.SessionManager

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
			"1.1.0",
			"IP banning module with auto-ban paths and whitelist support",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

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
	)
	return nil
}

// SetSessionManager sets the session manager for checking authenticated users.
// This allows the middleware to skip auto-banning for admin/editor users.
func (m *Module) SetSessionManager(sm *scs.SessionManager) {
	m.sessionManager = sm
}

// isAdminOrEditor checks if the current request is from an authenticated admin or editor user.
// Returns true if the user should be exempt from auto-banning.
func (m *Module) isAdminOrEditor(r *http.Request) bool {
	if m.sessionManager == nil || m.ctx == nil {
		return false
	}

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
	r.Delete("/sentinel/{id}", m.handleDelete)

	// Auto-ban paths
	r.Post("/sentinel/paths", m.handleCreatePath)
	r.Delete("/sentinel/paths/{id}", m.handleDeletePath)

	// Whitelist
	r.Post("/sentinel/whitelist", m.handleCreateWhitelist)
	r.Delete("/sentinel/whitelist/{id}", m.handleDeleteWhitelist)
}

// TemplateFuncs returns template functions provided by the module.
func (m *Module) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"sentinelVersion": func() string {
			return m.Version()
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
	}
}

// reloadBannedIPs loads banned IP patterns from the database into memory.
func (m *Module) reloadBannedIPs() error {
	rows, err := m.ctx.DB.Query(`SELECT ip_pattern FROM sentinel_banned_ips`)
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

	m.bannedMu.Lock()
	m.bannedPatterns = patterns
	m.bannedMu.Unlock()

	return rows.Err()
}

// reloadAutoBanPaths loads auto-ban path patterns from the database into memory.
func (m *Module) reloadAutoBanPaths() error {
	rows, err := m.ctx.DB.Query(`SELECT path_pattern FROM sentinel_autoban_paths`)
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

	m.pathsMu.Lock()
	m.autoBanPaths = patterns
	m.pathsMu.Unlock()

	return rows.Err()
}

// reloadWhitelist loads whitelisted IP patterns from the database into memory.
func (m *Module) reloadWhitelist() error {
	rows, err := m.ctx.DB.Query(`SELECT ip_pattern FROM sentinel_whitelist`)
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

	m.whitelistMu.Lock()
	m.whitelistPatterns = patterns
	m.whitelistMu.Unlock()

	return rows.Err()
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
// Supports wildcard (*) at the end of octets.
func matchIPPattern(pattern, ip string) bool {
	if pattern == ip {
		return true
	}

	if strings.Contains(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasPrefix(ip, prefix) {
			return true
		}
	}

	return false
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
	_, err := m.ctx.DB.Exec(`
		INSERT OR IGNORE INTO sentinel_banned_ips (ip_pattern, notes, url, banned_at, created_by)
		VALUES (?, ?, ?, ?, 0)
	`, ip, notes, triggeredPath, time.Now())
	if err != nil {
		return err
	}

	// Reload cache
	return m.reloadBannedIPs()
}

// GetMiddleware returns the IP ban checking middleware for use in router setup.
func (m *Module) GetMiddleware() func(http.Handler) http.Handler {
	return m.Middleware()
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

			ip := getClientIP(r)
			path := r.URL.Path

			// 1. Check whitelist first - whitelisted IPs bypass all checks
			if m.IsIPWhitelisted(ip) {
				next.ServeHTTP(w, r)
				return
			}

			// 2. Check if IP is already banned
			if m.IsIPBanned(ip) {
				m.ctx.Logger.Warn("blocked banned IP", "ip", ip, "path", path)
				http.Error(w, i18n.T("en", "sentinel.access_denied"), http.StatusForbidden)
				return
			}

			// 3. Check auto-ban paths - ban IP if accessing forbidden path
			// Skip auto-ban for authenticated admin/editor users
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

				m.ctx.Logger.Warn("auto-banning IP for forbidden path",
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

			next.ServeHTTP(w, r)
		})
	}
}

// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
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
