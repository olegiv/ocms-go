// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// postSettings submits the settings form as the given user (nil for none).
func postSettings(env *testEnv, form url.Values, user *store.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, adminPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if user != nil {
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, *user))
	}
	rec := httptest.NewRecorder()
	env.module.handleSaveSettings(rec, req)
	return rec
}

func TestHandleSaveSettingsUnauthorized(t *testing.T) {
	env := newTestEnv(t)
	rec := postSettings(env, url.Values{"allow_drafts": {"1"}}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if env.module.currentSettings().AllowDrafts {
		t.Error("settings must not change without a user")
	}
}

func TestHandleSaveSettingsRejectsLongInstructions(t *testing.T) {
	env := newTestEnv(t)
	admin := &store.User{ID: env.userID, Email: "author@example.com", Role: "admin"}
	rec := postSettings(env, url.Values{
		"allow_drafts": {"1"},
		"instructions": {strings.Repeat("x", maxInstructionsRunes+1)},
	}, admin)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != adminPath {
		t.Errorf("status %d location %q, want a redirect back", rec.Code, rec.Header().Get("Location"))
	}
	if env.module.currentSettings().AllowDrafts {
		t.Error("an invalid form must not change the active settings")
	}
	stored, err := loadSettings(context.Background(), env.db)
	if err != nil || stored.AllowDrafts {
		t.Errorf("stored settings = %+v, %v; want unchanged", stored, err)
	}
}

func TestHandleSaveSettingsAppliesAndAudits(t *testing.T) {
	env := newTestEnv(t)
	before := env.module.server.Load()
	admin := &store.User{ID: env.userID, Email: "author@example.com", Role: "admin"}

	rec := postSettings(env, url.Values{
		"allow_drafts": {"1"},
		"instructions": {"  Focus on recent posts.\r\n"},
	}, admin)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}

	want := Settings{AllowDrafts: true, Instructions: "Focus on recent posts."}
	if got := env.module.currentSettings(); got != want {
		t.Errorf("active settings = %+v, want %+v", got, want)
	}
	stored, err := loadSettings(context.Background(), env.db)
	if err != nil || stored != want {
		t.Errorf("stored settings = %+v, %v; want %+v", stored, err, want)
	}
	if env.module.server.Load() == before {
		t.Error("saving must swap in a server built from the new settings")
	}

	session := env.connect(env.createKey(model.PermissionMCPAccess))
	if init := session.InitializeResult(); init == nil || !strings.Contains(init.Instructions, "Focus on recent posts.") {
		t.Error("new instructions must reach clients")
	}

	var message string
	err = env.db.QueryRow(`SELECT message FROM events WHERE category = ? ORDER BY id DESC LIMIT 1`, model.EventCategoryConfig).Scan(&message)
	if err != nil || message != "MCP settings updated" {
		t.Errorf("audit event = %q, %v; want \"MCP settings updated\"", message, err)
	}
	if len(env.logs.find("MCP settings updated")) != 1 {
		t.Error("the change must be logged")
	}

	// Clearing the checkbox turns drafts back off.
	postSettings(env, url.Values{"instructions": {""}}, admin)
	if env.module.currentSettings().AllowDrafts {
		t.Error("an unchecked box must disable drafts")
	}
}

func TestDashboardData(t *testing.T) {
	env := newTestEnv(t)
	rawKeys := []string{
		env.createKey(model.PermissionMCPAccess, model.PermissionPagesRead),
		env.createKey(model.PermissionPagesRead), // a REST-only key must not be listed
		env.createKeyWith(func(p *store.CreateAPIKeyParams) {
			p.Name = "Old agent"
			p.ExpiresAt = sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
		}, model.PermissionMCPAccess),
	}

	data := env.module.dashboardData(context.Background())
	if !data.SiteURLMissing || data.EndpointURL != EndpointPath {
		t.Errorf("without a site URL: missing=%v endpoint=%q", data.SiteURLMissing, data.EndpointURL)
	}
	if len(data.Tools) != len(toolCatalog()) {
		t.Errorf("tools = %d, want %d", len(data.Tools), len(toolCatalog()))
	}
	if len(data.Keys) != 2 {
		t.Fatalf("keys = %d, want the 2 holding mcp:access", len(data.Keys))
	}
	expired := 0
	for _, key := range data.Keys {
		if key.Expired {
			expired++
		}
	}
	if expired != 1 {
		t.Errorf("expired keys = %d, want 1", expired)
	}
	if !strings.Contains(data.ClaudeCodeCommand, keyPlaceholder) || !strings.Contains(data.ClientConfigJSON, keyPlaceholder) {
		t.Errorf("snippets must use the key placeholder:\n%s\n%s", data.ClaudeCodeCommand, data.ClientConfigJSON)
	}
	var sb strings.Builder
	if err := DashboardPage(&adminviews.PageContext{}, data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, raw := range rawKeys {
		if strings.Contains(sb.String(), raw) {
			t.Error("the admin page must never contain API key material")
		}
	}

	env.setConfig(model.ConfigKeySiteURL, "https://example.com")
	data = env.module.dashboardData(context.Background())
	if data.SiteURLMissing || data.EndpointURL != "https://example.com"+EndpointPath {
		t.Errorf("with a site URL: missing=%v endpoint=%q", data.SiteURLMissing, data.EndpointURL)
	}
	if !strings.Contains(data.ClientConfigJSON, `"url": "https://example.com/api/mcp"`) {
		t.Errorf("client config = %s", data.ClientConfigJSON)
	}
}

func TestDashboardPageRenders(t *testing.T) {
	env := newTestEnv(t)
	env.createKey(model.PermissionMCPAccess)
	data := env.module.dashboardData(context.Background())

	var sb strings.Builder
	if err := DashboardPage(&adminviews.PageContext{}, data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()
	for _, want := range []string{
		`action="/admin/mcp"`,
		`name="allow_drafts"`,
		`name="instructions"`,
		"get_site_info",
		"search_pages",
		"claude mcp add --transport http ocms",
		"Agent key",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

func TestInstructionsErrorMessage(t *testing.T) {
	if got := instructionsErrorMessage("en", errInstructionsEncoding); strings.Contains(got, "%!") {
		t.Errorf("encoding message has a stray format verb: %q", got)
	}
	if got := instructionsErrorMessage("en", errInstructionsTooLong); strings.Contains(got, "%!") {
		t.Errorf("length message has a stray format verb: %q", got)
	}
}
