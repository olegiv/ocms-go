// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package sentinel

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// handleAdminList handles GET /admin/sentinel - displays all sentinel data.
func (m *Module) handleAdminList(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Fetch all data
	bans, err := m.listBannedIPs()
	if err != nil {
		m.ctx.Logger.Error("failed to list banned IPs", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	paths, err := m.listAutoBanPaths()
	if err != nil {
		m.ctx.Logger.Error("failed to list auto-ban paths", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	whitelist, err := m.listWhitelist()
	if err != nil {
		m.ctx.Logger.Error("failed to list whitelist", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Bans            []BannedIP
		Paths           []AutoBanPath
		Whitelist       []WhitelistedIP
		Version         string
		BanCheckEnabled bool
		AutoBanEnabled  bool
	}{
		Bans:            bans,
		Paths:           paths,
		Whitelist:       whitelist,
		Version:         m.Version(),
		BanCheckEnabled: m.IsBanCheckEnabled(),
		AutoBanEnabled:  m.IsAutoBanEnabled(),
	}

	if err := m.ctx.Render.Render(w, r, "admin/module_sentinel", render.TemplateData{
		Title: i18n.T(lang, "sentinel.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
			{Label: i18n.T(lang, "sentinel.title"), URL: "/admin/sentinel", Active: true},
		},
	}); err != nil {
		m.ctx.Logger.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// ============================================================================
// Banned IPs handlers
// ============================================================================

// handleCreate handles POST /admin/sentinel - creates a new IP ban.
func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	ipPattern := strings.TrimSpace(r.FormValue("ip_pattern"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	urlField := strings.TrimSpace(r.FormValue("url"))

	if ipPattern == "" {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_ip_required"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	if !isValidIPPattern(ipPattern) {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_invalid_pattern"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	// Prevent admin from banning their own IP
	adminIP := getClientIP(r)
	if matchIPPattern(ipPattern, adminIP) {
		m.ctx.Logger.Warn("admin attempted to ban own IP", "ip_pattern", ipPattern, "admin_ip", adminIP, "user_id", user.ID)
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_self_ban"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	err := m.createBan(ipPattern, notes, urlField, user.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_duplicate"))
		} else {
			m.ctx.Logger.Error("failed to create ban", "error", err, "ip_pattern", ipPattern)
			m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_create_failed"))
		}
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("IP banned", "ip_pattern", ipPattern, "banned_by", user.ID)
	m.ctx.Render.SetFlashSuccess(r, i18n.T(lang, "sentinel.success_created"))
	http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
}

// handleDelete handles DELETE /admin/sentinel/{id} - removes an IP ban.
func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	ban, err := m.getBanByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		m.ctx.Logger.Error("failed to get ban", "error", err, "id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := m.deleteBan(id); err != nil {
		m.ctx.Logger.Error("failed to delete ban", "error", err, "id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	user := middleware.GetUser(r)
	m.ctx.Logger.Info("IP ban removed", "ip_pattern", ban.IPPattern, "removed_by", user.ID)

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// Auto-ban paths handlers
// ============================================================================

// handleCreatePath handles POST /admin/sentinel/paths - creates a new auto-ban path.
func (m *Module) handleCreatePath(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	pathPattern := strings.TrimSpace(r.FormValue("path_pattern"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	if pathPattern == "" {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_path_required"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	if !isValidPathPattern(pathPattern) {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_invalid_path_pattern"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	err := m.createAutoBanPath(pathPattern, notes, user.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_path_duplicate"))
		} else {
			m.ctx.Logger.Error("failed to create auto-ban path", "error", err, "path_pattern", pathPattern)
			m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_path_create_failed"))
		}
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("auto-ban path created", "path_pattern", pathPattern, "created_by", user.ID)
	m.ctx.Render.SetFlashSuccess(r, i18n.T(lang, "sentinel.success_path_created"))
	http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
}

// handleDeletePath handles DELETE /admin/sentinel/paths/{id} - removes an auto-ban path.
func (m *Module) handleDeletePath(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	path, err := m.getPathByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		m.ctx.Logger.Error("failed to get path", "error", err, "id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := m.deleteAutoBanPath(id); err != nil {
		m.ctx.Logger.Error("failed to delete path", "error", err, "id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	user := middleware.GetUser(r)
	m.ctx.Logger.Info("auto-ban path removed", "path_pattern", path.PathPattern, "removed_by", user.ID)

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// Whitelist handlers
// ============================================================================

// handleCreateWhitelist handles POST /admin/sentinel/whitelist - creates a whitelist entry.
func (m *Module) handleCreateWhitelist(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	ipPattern := strings.TrimSpace(r.FormValue("ip_pattern"))
	notes := strings.TrimSpace(r.FormValue("notes"))

	if ipPattern == "" {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_ip_required"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	if !isValidIPPattern(ipPattern) {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_invalid_pattern"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	err := m.createWhitelistEntry(ipPattern, notes, user.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_whitelist_duplicate"))
		} else {
			m.ctx.Logger.Error("failed to create whitelist entry", "error", err, "ip_pattern", ipPattern)
			m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_whitelist_create_failed"))
		}
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	m.ctx.Logger.Info("IP whitelisted", "ip_pattern", ipPattern, "created_by", user.ID)
	m.ctx.Render.SetFlashSuccess(r, i18n.T(lang, "sentinel.success_whitelist_created"))
	http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
}

// handleDeleteWhitelist handles DELETE /admin/sentinel/whitelist/{id} - removes whitelist entry.
func (m *Module) handleDeleteWhitelist(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	entry, err := m.getWhitelistByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		m.ctx.Logger.Error("failed to get whitelist entry", "error", err, "id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := m.deleteWhitelistEntry(id); err != nil {
		m.ctx.Logger.Error("failed to delete whitelist entry", "error", err, "id", id)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	user := middleware.GetUser(r)
	m.ctx.Logger.Info("whitelist entry removed", "ip_pattern", entry.IPPattern, "removed_by", user.ID)

	w.WriteHeader(http.StatusNoContent)
}

// ============================================================================
// Database operations - Banned IPs
// ============================================================================

func (m *Module) listBannedIPs() ([]BannedIP, error) {
	rows, err := m.ctx.DB.Query(`
		SELECT id, ip_pattern, country_code, notes, url, banned_at, created_by
		FROM sentinel_banned_ips
		ORDER BY banned_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var bans []BannedIP
	for rows.Next() {
		var ban BannedIP
		if err := rows.Scan(&ban.ID, &ban.IPPattern, &ban.CountryCode, &ban.Notes, &ban.URL, &ban.BannedAt, &ban.CreatedBy); err != nil {
			return nil, err
		}
		bans = append(bans, ban)
	}

	return bans, rows.Err()
}

func (m *Module) getBanByID(id int64) (*BannedIP, error) {
	var ban BannedIP
	err := m.ctx.DB.QueryRow(`
		SELECT id, ip_pattern, country_code, notes, url, banned_at, created_by
		FROM sentinel_banned_ips
		WHERE id = ?
	`, id).Scan(&ban.ID, &ban.IPPattern, &ban.CountryCode, &ban.Notes, &ban.URL, &ban.BannedAt, &ban.CreatedBy)
	if err != nil {
		return nil, err
	}
	return &ban, nil
}

func (m *Module) createBan(ipPattern, notes, urlField string, createdBy int64) error {
	// Lookup country for the IP pattern (only works for full IPs, not patterns with wildcards)
	countryCode := m.LookupCountry(ipPattern)
	_, err := m.ctx.DB.Exec(`
		INSERT INTO sentinel_banned_ips (ip_pattern, country_code, notes, url, banned_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?)
	`, ipPattern, countryCode, notes, urlField, time.Now(), createdBy)
	if err != nil {
		return err
	}
	return m.reloadBannedIPs()
}

func (m *Module) deleteBan(id int64) error {
	result, err := m.ctx.DB.Exec(`DELETE FROM sentinel_banned_ips WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return m.reloadBannedIPs()
}

// ============================================================================
// Database operations - Auto-ban paths
// ============================================================================

func (m *Module) listAutoBanPaths() ([]AutoBanPath, error) {
	rows, err := m.ctx.DB.Query(`
		SELECT id, path_pattern, notes, created_at, created_by
		FROM sentinel_autoban_paths
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var paths []AutoBanPath
	for rows.Next() {
		var p AutoBanPath
		if err := rows.Scan(&p.ID, &p.PathPattern, &p.Notes, &p.CreatedAt, &p.CreatedBy); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}

	return paths, rows.Err()
}

func (m *Module) getPathByID(id int64) (*AutoBanPath, error) {
	var p AutoBanPath
	err := m.ctx.DB.QueryRow(`
		SELECT id, path_pattern, notes, created_at, created_by
		FROM sentinel_autoban_paths
		WHERE id = ?
	`, id).Scan(&p.ID, &p.PathPattern, &p.Notes, &p.CreatedAt, &p.CreatedBy)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *Module) createAutoBanPath(pathPattern, notes string, createdBy int64) error {
	_, err := m.ctx.DB.Exec(`
		INSERT INTO sentinel_autoban_paths (path_pattern, notes, created_at, created_by)
		VALUES (?, ?, ?, ?)
	`, pathPattern, notes, time.Now(), createdBy)
	if err != nil {
		return err
	}
	return m.reloadAutoBanPaths()
}

func (m *Module) deleteAutoBanPath(id int64) error {
	result, err := m.ctx.DB.Exec(`DELETE FROM sentinel_autoban_paths WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return m.reloadAutoBanPaths()
}

// ============================================================================
// Database operations - Whitelist
// ============================================================================

func (m *Module) listWhitelist() ([]WhitelistedIP, error) {
	rows, err := m.ctx.DB.Query(`
		SELECT id, ip_pattern, notes, created_at, created_by
		FROM sentinel_whitelist
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []WhitelistedIP
	for rows.Next() {
		var e WhitelistedIP
		if err := rows.Scan(&e.ID, &e.IPPattern, &e.Notes, &e.CreatedAt, &e.CreatedBy); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

func (m *Module) getWhitelistByID(id int64) (*WhitelistedIP, error) {
	var e WhitelistedIP
	err := m.ctx.DB.QueryRow(`
		SELECT id, ip_pattern, notes, created_at, created_by
		FROM sentinel_whitelist
		WHERE id = ?
	`, id).Scan(&e.ID, &e.IPPattern, &e.Notes, &e.CreatedAt, &e.CreatedBy)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (m *Module) createWhitelistEntry(ipPattern, notes string, createdBy int64) error {
	_, err := m.ctx.DB.Exec(`
		INSERT INTO sentinel_whitelist (ip_pattern, notes, created_at, created_by)
		VALUES (?, ?, ?, ?)
	`, ipPattern, notes, time.Now(), createdBy)
	if err != nil {
		return err
	}
	return m.reloadWhitelist()
}

func (m *Module) deleteWhitelistEntry(id int64) error {
	result, err := m.ctx.DB.Exec(`DELETE FROM sentinel_whitelist WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return m.reloadWhitelist()
}

// ============================================================================
// Settings handlers
// ============================================================================

// handleUpdateSettings handles POST /admin/sentinel/settings - updates module settings.
func (m *Module) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Get form values - checkboxes return empty string if unchecked
	banCheckEnabled := r.FormValue("ban_check_enabled") == "on"
	autoBanEnabled := r.FormValue("autoban_enabled") == "on"

	// Update settings in database
	if err := m.updateSetting(settingBanCheckEnabled, banCheckEnabled); err != nil {
		m.ctx.Logger.Error("failed to update ban_check_enabled setting", "error", err)
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_settings_failed"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	if err := m.updateSetting(settingAutoBanEnabled, autoBanEnabled); err != nil {
		m.ctx.Logger.Error("failed to update autoban_enabled setting", "error", err)
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_settings_failed"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	// Reload settings into cache
	if err := m.reloadSettings(); err != nil {
		m.ctx.Logger.Error("failed to reload settings", "error", err)
	}

	m.ctx.Logger.Info("sentinel settings updated",
		"ban_check_enabled", banCheckEnabled,
		"autoban_enabled", autoBanEnabled,
		"updated_by", user.ID,
	)
	m.ctx.Render.SetFlashSuccess(r, i18n.T(lang, "sentinel.success_settings_updated"))
	http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
}

// ============================================================================
// Database operations - Settings
// ============================================================================

func (m *Module) updateSetting(key string, value bool) error {
	valueStr := "false"
	if value {
		valueStr = "true"
	}

	_, err := m.ctx.DB.Exec(`
		INSERT INTO sentinel_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, valueStr)
	return err
}

// ============================================================================
// Validation helpers
// ============================================================================

// isValidIPPattern validates the IP pattern format.
func isValidIPPattern(pattern string) bool {
	if pattern == "" || len(pattern) < 2 {
		return false
	}

	if pattern == "*" || pattern == "*.*.*.*" {
		return false
	}

	for _, c := range pattern {
		if !isValidIPChar(c) {
			return false
		}
	}

	return true
}

func isValidIPChar(c rune) bool {
	return (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'f') ||
		(c >= 'A' && c <= 'F') ||
		c == '.' || c == ':' || c == '*'
}

// isValidPathPattern validates the path pattern format.
// Accepts: /path, /path*, */path, */path*
func isValidPathPattern(pattern string) bool {
	if pattern == "" {
		return false
	}

	// Must contain at least one path character
	core := strings.TrimPrefix(pattern, "*")
	core = strings.TrimSuffix(core, "*")

	if core == "" {
		return false
	}

	// Path should start with /
	if !strings.HasPrefix(core, "/") {
		return false
	}

	// Basic character validation - allow alphanumeric, /, -, _, .
	for _, c := range core {
		if !isValidPathChar(c) {
			return false
		}
	}

	return true
}

func isValidPathChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '/' || c == '-' || c == '_' || c == '.'
}
