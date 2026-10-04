// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// maxListedKeys bounds the API keys scanned for the admin overview.
const maxListedKeys = 500

// keyPlaceholder stands in for a real key in the client snippets: the admin
// page never displays key material.
const keyPlaceholder = "<YOUR_API_KEY>"

// handleDashboard handles GET /admin/mcp.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	lang := m.ctx.Render.GetAdminLang(r)
	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "mcp.title"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "mcp.title"), URL: adminPath, Active: true},
	})
	render.Templ(w, r, DashboardPage(pc, m.dashboardData(r.Context())))
}

// dashboardData collects everything the admin page shows.
func (m *Module) dashboardData(ctx context.Context) DashboardData {
	settings := m.currentSettings()
	siteURL := m.siteURL(ctx)
	endpoint := EndpointPath
	if siteURL != "" {
		endpoint = siteURL + EndpointPath
	}
	data := DashboardData{
		EndpointURL:       endpoint,
		SiteURLMissing:    siteURL == "",
		ProtocolVersions:  strings.Join(mcp.SupportedProtocolVersions(), ", "),
		Version:           moduleVersion,
		AllowDrafts:       settings.AllowDrafts,
		Instructions:      settings.Instructions,
		MaxInstructions:   maxInstructionsRunes,
		ClaudeCodeCommand: claudeCodeCommand(endpoint),
		ClientConfigJSON:  clientConfigJSON(endpoint),
	}
	for _, spec := range toolCatalog() {
		data.Tools = append(data.Tools, ToolRow{Name: spec.Name, Title: spec.Title, Description: spec.Description})
	}
	keys, err := m.mcpKeys(ctx)
	if err != nil {
		m.logger.Error("failed to list API keys for the MCP page", "error", err)
		data.KeysError = true
	}
	data.Keys = keys
	return data
}

// mcpKeys lists the API keys holding mcp:access. Only display metadata is
// returned; key hashes never leave the store layer.
func (m *Module) mcpKeys(ctx context.Context) ([]KeyRow, error) {
	keys, err := m.svc.queries.ListAPIKeys(ctx, store.ListAPIKeysParams{Limit: maxListedKeys})
	if err != nil {
		return nil, fmt.Errorf("listing API keys: %w", err)
	}
	now := time.Now()
	var rows []KeyRow
	for i := range keys {
		key := &keys[i]
		permissions := middleware.ParseAPIKeyPermissions(key)
		if !slices.Contains(permissions, model.PermissionMCPAccess) {
			continue
		}
		row := KeyRow{
			ID:          key.ID,
			Name:        key.Name,
			Prefix:      key.KeyPrefix,
			Permissions: permissions,
			Active:      key.IsActive,
		}
		if key.LastUsedAt.Valid {
			row.LastUsed = key.LastUsedAt.Time.Format("2006-01-02 15:04")
		}
		if key.ExpiresAt.Valid {
			row.Expires = key.ExpiresAt.Time.Format("2006-01-02")
			row.Expired = key.ExpiresAt.Time.Before(now)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// claudeCodeCommand renders the Claude Code CLI command for the endpoint.
func claudeCodeCommand(endpoint string) string {
	return fmt.Sprintf(`claude mcp add --transport http ocms %s --header "Authorization: Bearer %s"`,
		endpoint, keyPlaceholder)
}

// clientServerConfig is one remote server entry of an "mcpServers" client
// configuration; the field order is the order the snippet shows.
type clientServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// clientConfigJSON renders the JSON configuration most MCP clients accept
// for a remote HTTP server with a static bearer token.
func clientConfigJSON(endpoint string) string {
	cfg := map[string]map[string]clientServerConfig{
		"mcpServers": {
			"ocms": {
				Type:    "http",
				URL:     endpoint,
				Headers: map[string]string{"Authorization": "Bearer " + keyPlaceholder},
			},
		},
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	// The snippet is copied into a config file, so the placeholder's angle
	// brackets must stay literal; templ escapes the page output itself.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// handleSaveSettings handles POST /admin/mcp.
func (m *Module) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if middleware.IsDemoMode() {
		m.ctx.Render.SetFlash(r, middleware.DemoModeMessageDetailed(middleware.RestrictionModuleSettings), "error")
		http.Redirect(w, r, adminPath, http.StatusSeeOther)
		return
	}
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	lang := m.ctx.Render.GetAdminLang(r)
	fail := func(message string) {
		m.ctx.Render.SetFlash(r, message, "error")
		http.Redirect(w, r, adminPath, http.StatusSeeOther)
	}

	if err := r.ParseForm(); err != nil {
		m.logger.Warn("failed to parse MCP settings form", "error", err)
		fail(i18n.T(lang, "mcp.error_parse_form"))
		return
	}
	instructions, errKey := normalizeInstructions(r.FormValue("instructions"))
	if errKey != "" {
		fail(instructionsErrorMessage(lang, errKey))
		return
	}
	next := Settings{
		AllowDrafts:  r.FormValue("allow_drafts") == "1",
		Instructions: instructions,
	}
	prev := m.currentSettings()

	// Build first, persist second, swap last: a failure never leaves the
	// stored settings and the running server disagreeing.
	srv, err := m.buildServer(next)
	if err != nil {
		m.logger.Error("failed to rebuild MCP server with new settings", "error", err)
		fail(i18n.T(lang, "mcp.error_save"))
		return
	}
	if err := saveSettings(r.Context(), m.ctx.DB, next); err != nil {
		m.logger.Error("failed to save MCP settings", "error", err)
		fail(i18n.T(lang, "mcp.error_save"))
		return
	}
	m.settings.Store(&next)
	m.server.Store(srv)

	meta := map[string]any{
		"allow_drafts":          next.AllowDrafts,
		"previous_allow_drafts": prev.AllowDrafts,
		"instructions_length":   utf8.RuneCountInString(next.Instructions),
	}
	m.logger.Info("MCP settings updated",
		"user_id", user.ID,
		"allow_drafts", next.AllowDrafts,
		"previous_allow_drafts", prev.AllowDrafts,
		"instructions_length", meta["instructions_length"])
	if m.ctx.Events != nil {
		if err := m.ctx.Events.LogConfigEvent(r.Context(), model.EventLevelInfo, "MCP settings updated",
			&user.ID, middleware.GetClientIP(r), r.URL.Path, meta); err != nil {
			m.logger.Warn("failed to record MCP settings event", "error", err)
		}
	}

	m.ctx.Render.SetFlash(r, i18n.T(lang, "mcp.success_save"), "success")
	http.Redirect(w, r, adminPath, http.StatusSeeOther)
}

// instructionsErrorMessage translates a normalizeInstructions error key. Only
// the length message takes the limit as a format argument.
func instructionsErrorMessage(lang, key string) string {
	if key == errInstructionsTooLong {
		return i18n.T(lang, key, maxInstructionsRunes)
	}
	return i18n.T(lang, key)
}
