// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package hcaptcha

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// TestSettingsLoadFailureKeepsEnvKeyProtection checks the fallback when the
// stored settings cannot be read at startup: login protection is turned on
// with the environment keys, the settings form refuses to save while the row
// stays unreadable, and the next read after the row is repaired brings the
// stored settings back. The module used to fall back to empty settings,
// which turned the captcha off.
func TestSettingsLoadFailureKeepsEnvKeyProtection(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := New()
	m.reload.RetryInterval = time.Nanosecond
	moduleutil.RunMigrations(t, db, m.Migrations())
	// A value that cannot be scanned into the enabled flag: loading fails.
	if _, err := db.Exec(`UPDATE hcaptcha_settings SET enabled = 'broken', theme = 'stored' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	ctx, _ := moduleutil.TestModuleContext(t, db)
	ctx.Config.HCaptchaSiteKey = "env-site-key"
	ctx.Config.HCaptchaSecretKey = "env-secret"
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !m.IsEnabled() || !strings.Contains(string(m.RenderWidget()), `data-sitekey="env-site-key"`) {
		t.Fatal("captcha is off after a settings load failure although environment keys are set")
	}

	body := strings.NewReader("enabled=1&site_key=form-key&secret_key=form-secret&theme=dark&size=compact")
	req := requestWithUser(body, store.User{ID: 1, Email: "admin@test.com", Role: "admin"})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	m.handleSaveSettings(httptest.NewRecorder(), req)
	var theme string
	if err := db.QueryRow(`SELECT theme FROM hcaptcha_settings WHERE id = 1`).Scan(&theme); err != nil {
		t.Fatal(err)
	}
	if theme != "stored" {
		t.Errorf("stored theme = %q: the settings form saved over settings it could not read", theme)
	}

	// Repair the row: the next read reloads it without a restart.
	if _, err := db.Exec(`UPDATE hcaptcha_settings SET enabled = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if got := m.GetSettings(); got.Enabled || got.Theme != "stored" || got.SiteKey != "env-site-key" {
		t.Errorf("after the repair: %+v, want the stored (disabled) settings with the env key", got)
	}
}

// TestSettingsLoadFailureWithoutKeys documents the limit: with no keys in the
// environment there is nothing to verify against, so the captcha stays off.
func TestSettingsLoadFailureWithoutKeys(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := New()
	ctx, _ := moduleutil.TestModuleContext(t, db)
	// No migrations: the settings table is missing, so loading fails.
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if m.IsEnabled() {
		t.Error("captcha reports enabled without any keys")
	}
}
