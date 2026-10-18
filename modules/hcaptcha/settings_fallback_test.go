// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package hcaptcha

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
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
	if page := dashboardBody(t, m); !strings.Contains(page, `name="from_fallback"`) || !strings.Contains(page, `role="alert"`) {
		t.Error("the settings page does not warn or mark its form while showing fallback values")
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

	// Repair the row. A form rendered from the fallback is still refused: its
	// values would replace the settings that can now be read.
	if _, err := db.Exec(`UPDATE hcaptcha_settings SET enabled = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	body = strings.NewReader("from_fallback=1&enabled=1&site_key=form-key&secret_key=form-secret&theme=dark&size=compact")
	req = requestWithUser(body, store.User{ID: 1, Email: "admin@test.com", Role: "admin"})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	m.handleSaveSettings(httptest.NewRecorder(), req)
	if err := db.QueryRow(`SELECT theme FROM hcaptcha_settings WHERE id = 1`).Scan(&theme); err != nil {
		t.Fatal(err)
	}
	if theme != "stored" {
		t.Errorf("stored theme = %q: a form rendered from fallback values replaced the recovered settings", theme)
	}
	// The next read reloads the row without a restart. The settings page is
	// that read here, so it must retry before deciding whether to warn.
	if strings.Contains(dashboardBody(t, m), `name="from_fallback"`) {
		t.Error("the first settings page after the repair still marks its form as fallback")
	}
	if got := m.GetSettings(); got.Enabled || got.Theme != "stored" || got.SiteKey != "env-site-key" {
		t.Errorf("after the repair: %+v, want the stored (disabled) settings with the env key", got)
	}
}

// dashboardBody renders the settings page for an admin.
func dashboardBody(t *testing.T, m *Module) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/hcaptcha", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{ID: 1, Email: "admin@test.com", Role: "admin"}))
	rr := httptest.NewRecorder()
	m.handleDashboard(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings page: status %d", rr.Code)
	}
	return rr.Body.String()
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

	// OCMS_HCAPTCHA_DISABLED wins over the fallback even with keys set, so an
	// hCaptcha outage can still be bypassed while the settings are unreadable.
	disabled := New()
	disabledCtx, _ := moduleutil.TestModuleContext(t, db)
	disabledCtx.Config.HCaptchaSiteKey = "env-site-key"
	disabledCtx.Config.HCaptchaSecretKey = "env-secret"
	disabledCtx.Config.HCaptchaDisabled = true
	if err := disabled.Init(disabledCtx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if disabled.IsEnabled() {
		t.Error("captcha is on although OCMS_HCAPTCHA_DISABLED is set")
	}
}

// TestSaveRefusesMalformedForm checks that a settings form whose body does
// not parse is refused instead of saved from the part that parsed. Reading
// from_fallback parsed the form first and dropped the error, and the later
// ParseForm returned nil, so a bad escape saved empty keys and turned
// hCaptcha off (Codex review of PR #189).
//
// Bug state: read from_fallback before r.ParseForm in handleSaveSettings and
// this fails.
func TestSaveRefusesMalformedForm(t *testing.T) {
	db, cleanup := testutil.TestDB(t)
	defer cleanup()
	m := testModule(t, db)
	if _, err := db.Exec(`UPDATE hcaptcha_settings SET enabled = 1, site_key = 'stored-key', secret_key = 'stored-secret' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	req := requestWithUser(strings.NewReader("enabled=0&site_key=%zz"), store.User{ID: 1, Email: "admin@test.com", Role: "admin"})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	m.handleSaveSettings(httptest.NewRecorder(), req)
	var enabled bool
	var siteKey string
	if err := db.QueryRow(`SELECT enabled, site_key FROM hcaptcha_settings WHERE id = 1`).Scan(&enabled, &siteKey); err != nil {
		t.Fatal(err)
	}
	if !enabled || siteKey != "stored-key" {
		t.Errorf("stored settings = enabled %v, site key %q: a form that did not parse was saved", enabled, siteKey)
	}
}
