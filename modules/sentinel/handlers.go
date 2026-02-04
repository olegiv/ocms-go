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

// handleAdminList handles GET /admin/sentinel - displays list of banned IPs.
func (m *Module) handleAdminList(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	// Fetch all banned IPs
	bans, err := m.listBannedIPs()
	if err != nil {
		m.ctx.Logger.Error("failed to list banned IPs", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Bans    []BannedIP
		Version string
	}{
		Bans:    bans,
		Version: m.Version(),
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

// handleCreate handles POST /admin/sentinel - creates a new IP ban.
func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := m.ctx.Render.GetAdminLang(r)

	ipPattern := strings.TrimSpace(r.FormValue("ip_pattern"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	urlField := strings.TrimSpace(r.FormValue("url"))

	// Validate IP pattern
	if ipPattern == "" {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_ip_required"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	// Validate pattern format (basic validation)
	if !isValidIPPattern(ipPattern) {
		m.ctx.Render.SetFlashError(r, i18n.T(lang, "sentinel.error_invalid_pattern"))
		http.Redirect(w, r, "/admin/sentinel", http.StatusSeeOther)
		return
	}

	// Create the ban
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

	// Get ban info before deleting for logging
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

// Database operations

func (m *Module) listBannedIPs() ([]BannedIP, error) {
	rows, err := m.ctx.DB.Query(`
		SELECT id, ip_pattern, notes, url, banned_at, created_by
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
		if err := rows.Scan(&ban.ID, &ban.IPPattern, &ban.Notes, &ban.URL, &ban.BannedAt, &ban.CreatedBy); err != nil {
			return nil, err
		}
		bans = append(bans, ban)
	}

	return bans, rows.Err()
}

func (m *Module) getBanByID(id int64) (*BannedIP, error) {
	var ban BannedIP
	err := m.ctx.DB.QueryRow(`
		SELECT id, ip_pattern, notes, url, banned_at, created_by
		FROM sentinel_banned_ips
		WHERE id = ?
	`, id).Scan(&ban.ID, &ban.IPPattern, &ban.Notes, &ban.URL, &ban.BannedAt, &ban.CreatedBy)
	if err != nil {
		return nil, err
	}
	return &ban, nil
}

func (m *Module) createBan(ipPattern, notes, urlField string, createdBy int64) error {
	_, err := m.ctx.DB.Exec(`
		INSERT INTO sentinel_banned_ips (ip_pattern, notes, url, banned_at, created_by)
		VALUES (?, ?, ?, ?, ?)
	`, ipPattern, notes, urlField, time.Now(), createdBy)
	if err != nil {
		return err
	}

	// Reload cache
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

	// Reload cache
	return m.reloadBannedIPs()
}

// isValidIPPattern validates the IP pattern format.
// Accepts:
// - Full IPv4: 192.168.1.1
// - Wildcards: 192.168.1.*, 192.168.*, 192.*, 10.0.0.1*
// - IPv6: 2001:db8::1 (basic validation)
func isValidIPPattern(pattern string) bool {
	if pattern == "" {
		return false
	}

	// Don't allow patterns that are too short or dangerous
	if len(pattern) < 2 {
		return false
	}

	// Don't allow banning everything
	if pattern == "*" || pattern == "*.*.*.*" {
		return false
	}

	// Basic character validation
	for _, c := range pattern {
		if !isValidIPChar(c) {
			return false
		}
	}

	return true
}

func isValidIPChar(c rune) bool {
	// Allow digits, dots, colons (for IPv6), and asterisks for wildcards
	return (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'f') ||
		(c >= 'A' && c <= 'F') ||
		c == '.' || c == ':' || c == '*'
}
