// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegiv/ocms-go/internal/connectedapps"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	accountviews "github.com/olegiv/ocms-go/internal/views/account"
)

// keysPageSize is how many API keys the admin overview reads per query. It
// pages through all of them: a key with MCP access must never drop off the
// list just because many newer keys exist.
const keysPageSize = 500

// keyPlaceholder stands in for a real key in the client snippets: the admin
// page never displays key material.
const keyPlaceholder = "<YOUR_API_KEY>"

// handleDashboard handles GET /admin/mcp.
func (m *Module) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	pages, err := connectedapps.ParsePagination(r.URL.Query())
	if err != nil {
		http.Error(w, "Invalid page", http.StatusBadRequest)
		return
	}
	page, err := connectedapps.New(m.ctx.DB).List(r.Context(), connectedapps.Actor{UserID: user.ID, SessionVersion: user.SessionVersion}, true, pages, m.ctx.OAuth != nil)
	if err != nil {
		m.connectionError(w, err)
		return
	}
	lang := m.ctx.Render.GetAdminLang(r)
	pc := m.ctx.Render.BuildPageContext(r, i18n.T(lang, "mcp.title"), []render.Breadcrumb{
		{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
		{Label: i18n.T(lang, "nav.modules"), URL: "/admin/modules"},
		{Label: i18n.T(lang, "mcp.title"), URL: adminPath, Active: true},
	})
	data := m.dashboardData(r.Context(), false)
	data.Connections = &accountviews.ConnectionsData{Page: page, Admin: true, BasePath: adminPath}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	render.Templ(w, r, DashboardPage(pc, data))
}

// dashboardData collects everything the admin page shows.
func (m *Module) dashboardData(ctx context.Context, includeKeys bool) DashboardData {
	data := DashboardData{
		ProtocolVersions: strings.Join(mcp.SupportedProtocolVersions(), ", "),
		Version:          moduleVersion,
		MaxInstructions:  maxInstructionsRunes,
		OAuthEnabled:     m.ctx.OAuth != nil,
	}

	// The form shows what is stored, which also brings the running server
	// in line with it. If the settings cannot be read, the page says so and
	// disables the form rather than offering defaults to save over them.
	settings, err := m.reloadSettings(ctx)
	if err != nil {
		m.logger.Error("failed to load MCP settings for the admin page", "error", err)
		data.SettingsError = true
		settings = m.currentSettings()
	}
	data.AllowDrafts, data.Instructions = settings.AllowDrafts, settings.Instructions
	data.DraftToolsEnabled, data.ExternalLinks = settings.DraftToolsEnabled, linkPolicy(settings)

	endpoint := EndpointPath
	siteURL, status, err := m.resolveSiteURL(ctx)
	switch {
	case err != nil:
		m.logger.Error("failed to read the site URL for the MCP page", "error", err)
		data.ConfigError = true
	case status == siteURLValid:
		endpoint = siteURL + EndpointPath
	case status == siteURLInvalid:
		raw, rawErr := m.configValue(ctx, model.ConfigKeySiteURL)
		if rawErr != nil {
			m.logger.Error("failed to read the site URL for the MCP page", "error", rawErr)
			data.ConfigError = true
		} else {
			data.SiteURLInvalid = strings.TrimSpace(raw)
		}
	default:
		data.SiteURLMissing = true
	}
	data.EndpointURL = endpoint
	data.ClaudeCodeCommand = claudeCodeCommand(endpoint)
	data.ClientConfigJSON = clientConfigJSON(endpoint)

	for _, spec := range m.catalog() {
		data.Tools = append(data.Tools, ToolRow{Name: spec.Name, Title: spec.Title, Description: spec.Description})
	}
	// The connected-app overview supplies its own bounded, paginated metadata.
	// Standalone key-summary callers retain the existing complete key list.
	if includeKeys {
		keys, err := m.mcpKeys(ctx)
		if err != nil {
			m.logger.Error("failed to list API keys for the MCP page", "error", err)
			data.KeysError = true
		}
		data.Keys = keys
	}
	return data
}

func (m *Module) handleRevokeConnection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if middleware.IsDemoMode() {
		http.Error(w, middleware.DemoModeMessageDetailed(middleware.RestrictionAPIKeys), http.StatusForbidden)
		return
	}
	user := middleware.GetUser(r)
	if user == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	kind, id, err := connectedapps.ParseRevoke(w, r)
	if err == nil {
		err = connectedapps.New(m.ctx.DB).Revoke(r.Context(), connectedapps.Actor{UserID: user.ID, SessionVersion: user.SessionVersion}, true, kind, id)
	}
	if err != nil {
		m.connectionError(w, err)
		return
	}
	if m.ctx.Events != nil {
		if err := m.ctx.Events.LogEvent(r.Context(), model.EventLevelInfo, model.EventCategorySecurity,
			"Administrator revoked connected app access", &user.ID, middleware.GetClientIP(r), r.URL.Path,
			map[string]any{"kind": kind, "connection_id": id}); err != nil {
			m.logger.WarnContext(service.WithoutEventLog(r.Context()), "failed to record connection revocation", "error", err)
		}
	}
	http.Redirect(w, r, adminPath, http.StatusSeeOther)
}

func (m *Module) connectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, connectedapps.ErrForbidden):
		http.Error(w, "Forbidden", http.StatusForbidden)
	case errors.Is(err, connectedapps.ErrInvalid):
		http.Error(w, "Invalid connection request", http.StatusBadRequest)
	case errors.Is(err, connectedapps.ErrNotFound):
		http.Error(w, "Not found", http.StatusNotFound)
	default:
		m.logger.Error("connected app operation failed", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// mcpKeys lists the API keys holding mcp:access. Only display metadata is
// returned; key hashes never leave the store layer.
func (m *Module) mcpKeys(ctx context.Context) ([]KeyRow, error) {
	now := time.Now()
	var rows []KeyRow
	for offset := int64(0); ; offset += keysPageSize {
		keys, err := m.svc.queries.ListAPIKeys(ctx, store.ListAPIKeysParams{Limit: keysPageSize, Offset: offset})
		if err != nil {
			return nil, fmt.Errorf("listing API keys: %w", err)
		}
		for i := range keys {
			if row, ok := mcpKeyRow(&keys[i], now); ok {
				rows = append(rows, row)
			}
		}
		if len(keys) < keysPageSize {
			return rows, nil
		}
	}
}

// mcpKeyRow returns the overview row of a key holding mcp:access.
func mcpKeyRow(key *store.ApiKey, now time.Time) (KeyRow, bool) {
	permissions := middleware.ParseAPIKeyPermissions(key)
	if !slices.Contains(permissions, model.PermissionMCPAccess) {
		return KeyRow{}, false
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
	return row, true
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
		AllowDrafts:       r.FormValue("allow_drafts") == "1",
		Instructions:      instructions,
		DraftToolsEnabled: r.FormValue("draft_tools_enabled") == "1",
		ExternalLinks:     r.FormValue("external_links"),
	}
	if next.ExternalLinks == "" {
		next.ExternalLinks = linkPolicy(m.currentSettings())
	}
	if next.ExternalLinks != "keep" && next.ExternalLinks != "nofollow" && next.ExternalLinks != "strip" {
		fail(i18n.T(lang, "mcp.invalid_link_policy"))
		return
	}
	prev, err := m.saveAndApply(r.Context(), next)
	if err != nil {
		m.logger.Error("failed to save MCP settings", "error", err)
		fail(i18n.T(lang, "mcp.error_save"))
		return
	}

	meta := map[string]any{
		"draft_tools_enabled":   next.DraftToolsEnabled,
		"external_links":        next.ExternalLinks,
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
			m.logger.WarnContext(service.WithoutEventLog(r.Context()), "failed to record MCP settings event", "error", err)
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
