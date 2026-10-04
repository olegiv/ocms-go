// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// disabledAttr matches a rendered boolean disabled attribute (not the
// Tailwind "disabled:" class variants the components also carry).
var disabledAttr = regexp.MustCompile(`\sdisabled(\s|>|/)`)

// renderDashboard renders the admin page for data.
func renderDashboard(t *testing.T, data DashboardData) string {
	t.Helper()
	var sb strings.Builder
	if err := DashboardPage(&adminviews.PageContext{}, data).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

// TestDashboardSettingsLoadFailure verifies unreadable settings are reported
// and the form disabled, instead of defaults being offered to save over the
// stored values, and that Init reports the failure at Error level.
func TestDashboardSettingsLoadFailure(t *testing.T) {
	env := newTestEnv(t)
	env.setSettings(Settings{AllowDrafts: true, Instructions: "Keep me."})
	healthy := renderDashboard(t, env.module.dashboardData(context.Background()))

	if _, err := env.db.Exec(`DROP TABLE mcp_settings`); err != nil {
		t.Fatalf("drop settings: %v", err)
	}
	data := env.module.dashboardData(context.Background())
	if !data.SettingsError {
		t.Fatal("an unreadable settings table must be reported")
	}
	if !data.AllowDrafts || data.Instructions != "Keep me." {
		t.Errorf("the page must show the active settings, got %+v", data)
	}
	broken := renderDashboard(t, data)
	if !strings.Contains(broken, "mcp.settings_load_error") {
		t.Error("the page must show the load error")
	}
	// Checkbox, textarea and submit button.
	if got := len(disabledAttr.FindAllString(broken, -1)) - len(disabledAttr.FindAllString(healthy, -1)); got != 3 {
		t.Errorf("disabled form controls = %d, want 3", got)
	}

	logs := &logCapture{}
	m := New()
	if err := m.Init(&module.Context{DB: env.db, Logger: slog.New(logs)}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	failures := logs.find("failed to load MCP settings; serving defaults (drafts hidden, no instructions)")
	if len(failures) != 1 || failures[0].Level != slog.LevelError {
		t.Errorf("Init must log the load failure once at Error, got %d lines", len(failures))
	}
	if m.currentSettings() != (Settings{}) {
		t.Error("Init must fall back to the safe defaults")
	}
}

// TestDashboardReloadsStoredSettings verifies the admin page shows what is
// stored and brings the running server in line with it.
func TestDashboardReloadsStoredSettings(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.db.Exec(`UPDATE mcp_settings SET allow_drafts = 1, instructions = 'From another instance.' WHERE id = 1`); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	data := env.module.dashboardData(context.Background())
	if data.SettingsError || !data.AllowDrafts || data.Instructions != "From another instance." {
		t.Errorf("dashboard settings = %+v, want the stored ones", data)
	}
	if got := env.module.currentSettings(); !got.AllowDrafts || got.Instructions != "From another instance." {
		t.Errorf("active settings = %+v, want the stored ones", got)
	}
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	if init := session.InitializeResult(); init == nil || !strings.Contains(init.Instructions, "From another instance.") {
		t.Error("reloaded instructions must reach clients")
	}
}

// TestDashboardSiteURLStates verifies the page tells an unusable site URL
// apart from a missing one.
func TestDashboardSiteURLStates(t *testing.T) {
	env := newTestEnv(t)
	env.setConfig(model.ConfigKeySiteURL, "example.com")
	data := env.module.dashboardData(context.Background())
	if data.SiteURLInvalid != "example.com" || data.SiteURLMissing || data.ConfigError || data.EndpointURL != EndpointPath {
		t.Errorf("invalid site URL: %+v", data)
	}
	if html := renderDashboard(t, data); !strings.Contains(html, "mcp.site_url_invalid") {
		t.Error("the page must show the invalid site URL alert")
	}

	if _, err := env.db.Exec(`ALTER TABLE config RENAME TO config_gone`); err != nil {
		t.Fatalf("rename config: %v", err)
	}
	if data := env.module.dashboardData(context.Background()); !data.ConfigError {
		t.Error("an unreadable site configuration must be reported")
	}
}

// TestSiteInfoReportsConfigFailure verifies a failing config read is an
// internal error, not a successful answer with an empty site name.
func TestSiteInfoReportsConfigFailure(t *testing.T) {
	env := newTestEnv(t)
	session := env.connect(env.createKey(model.PermissionMCPAccess))
	if _, err := env.db.Exec(`ALTER TABLE config RENAME TO config_gone`); err != nil {
		t.Fatalf("rename config: %v", err)
	}
	res := call(t, session, "get_site_info", nil)
	if code, _, _ := toolErrorBody(t, res); code != codeInternal {
		t.Errorf("code = %q, want internal_error", code)
	}
	lines := env.logs.find("MCP tool call")
	if len(lines) != 1 {
		t.Fatalf("call log lines = %d, want 1", len(lines))
	}
	if cause, _ := recordAttr(lines[0], "error"); !strings.Contains(cause.String(), "reading config") {
		t.Errorf("logged cause = %q, want the config read failure", cause.String())
	}
}

// TestMCPKeysPagesThroughAllKeys verifies the overview lists every key with
// MCP access, however many newer keys exist.
func TestMCPKeysPagesThroughAllKeys(t *testing.T) {
	env := newTestEnv(t)
	base := time.Now().Add(-time.Hour)
	insert := func(i int, perms ...string) {
		created := base.Add(time.Duration(i) * time.Millisecond)
		if _, err := env.q.CreateAPIKey(context.Background(), store.CreateAPIKeyParams{
			Name:        fmt.Sprintf("key %d", i),
			KeyHash:     fmt.Sprintf("hash-%d", i),
			KeyPrefix:   fmt.Sprintf("%04d", i),
			Permissions: model.PermissionsToJSON(perms),
			IsActive:    true,
			CreatedBy:   env.userID,
			CreatedAt:   created,
			UpdatedAt:   created,
		}); err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
	}
	insert(0, model.PermissionMCPAccess) // the oldest key
	for i := 1; i <= keysPageSize; i++ {
		insert(i, model.PermissionPagesRead)
	}

	rows, err := env.module.mcpKeys(context.Background())
	if err != nil {
		t.Fatalf("mcpKeys: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "key 0" {
		t.Errorf("rows = %+v, want the oldest key, which holds mcp:access", rows)
	}
}

// TestConcurrentSettingsSavesStayConsistent checks that simultaneous saves
// leave the database and the running server with the same settings. It is a
// consistency check under the race detector rather than a deterministic
// reproduction: without settingsMu a mismatch needs an unlucky interleaving.
func TestConcurrentSettingsSavesStayConsistent(t *testing.T) {
	env := newTestEnv(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := Settings{AllowDrafts: i%2 == 0, Instructions: fmt.Sprintf("save %d", i)}
			if _, err := env.module.saveAndApply(context.Background(), next); err != nil {
				t.Errorf("saveAndApply: %v", err)
			}
		}()
	}
	wg.Wait()

	stored, err := loadSettings(context.Background(), env.db)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if active := env.module.currentSettings(); stored != active {
		t.Errorf("stored %+v != active %+v", stored, active)
	}
}
