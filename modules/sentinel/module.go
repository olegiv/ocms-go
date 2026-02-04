// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sentinel provides IP banning functionality for oCMS.
// It allows administrators to ban IPs by full address or wildcard pattern.
package sentinel

import (
	"database/sql"
	"embed"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/module"
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

// Module implements the module.Module interface for IP banning.
type Module struct {
	module.BaseModule
	ctx *module.Context

	// Cache of banned IPs for fast lookup
	bannedPatterns []string
	bannedMu       sync.RWMutex
}

// New creates a new instance of the Sentinel module.
func New() *Module {
	return &Module{
		BaseModule: module.NewBaseModule(
			"sentinel",
			"1.0.0",
			"IP banning module for blocking malicious IPs",
		),
	}
}

// Init initializes the module with the given context.
func (m *Module) Init(ctx *module.Context) error {
	m.ctx = ctx

	// Load banned IPs into cache
	if err := m.reloadBannedIPs(); err != nil {
		return err
	}

	m.ctx.Logger.Info("Sentinel module initialized", "banned_count", len(m.bannedPatterns))
	return nil
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
	r.Get("/sentinel", m.handleAdminList)
	r.Post("/sentinel", m.handleCreate)
	r.Delete("/sentinel/{id}", m.handleDelete)
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
				// Create index for fast lookups
				_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_sentinel_ip_pattern ON sentinel_banned_ips(ip_pattern)`)
				return err
			},
			Down: func(db *sql.DB) error {
				_, err := db.Exec(`DROP TABLE IF EXISTS sentinel_banned_ips`)
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

// IsIPBanned checks if the given IP matches any banned pattern.
// Supports wildcards: "192.168.1.*" matches "192.168.1.123"
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

// matchIPPattern checks if an IP matches a pattern.
// Supports wildcard (*) at the end of octets.
// Examples:
//   - "192.168.1.1" matches "192.168.1.1" (exact match)
//   - "192.168.1.123" matches "192.168.1.*" (last octet wildcard)
//   - "192.168.5.10" matches "192.168.*" (last two octets wildcard)
//   - "192.168.1.123" matches "192.168.1.1*" (pattern starting with)
func matchIPPattern(pattern, ip string) bool {
	// Exact match
	if pattern == ip {
		return true
	}

	// Wildcard matching
	if strings.Contains(pattern, "*") {
		// Replace * with empty string for prefix matching
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasPrefix(ip, prefix) {
			return true
		}
	}

	return false
}

// GetMiddleware returns the IP ban checking middleware for use in router setup.
// This should be called after Init() to ensure banned IPs are loaded.
func (m *Module) GetMiddleware() func(http.Handler) http.Handler {
	return m.Middleware()
}

// Middleware returns HTTP middleware that blocks banned IPs.
// Should be applied early in the middleware chain.
func (m *Module) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip if module not initialized
			if m.ctx == nil {
				next.ServeHTTP(w, r)
				return
			}

			ip := getClientIP(r)

			if m.IsIPBanned(ip) {
				m.ctx.Logger.Warn("blocked banned IP", "ip", ip, "path", r.URL.Path)
				http.Error(w, i18n.T("en", "sentinel.access_denied"), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (can contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Fall back to RemoteAddr with port stripped
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}
