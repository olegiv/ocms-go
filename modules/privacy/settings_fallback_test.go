// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
	"github.com/olegiv/ocms-go/internal/testutil/moduleutil"
)

// TestSettingsLoadFailureDeniesConsent checks the fallback when the stored
// settings cannot be read at startup: the consent banner is turned on with
// ad and analytics storage denied, the settings form refuses to save while the
// row stays unreadable, and the next read after the row is repaired brings
// the stored settings back. The module used to fall back to empty settings,
// which dropped the banner and the defaults while other modules' trackers
// kept loading.
func TestSettingsLoadFailureDeniesConsent(t *testing.T) {
	db := testutil.TestMemoryDB(t)
	t.Cleanup(func() { _ = db.Close() })
	m := New()
	m.reload.RetryInterval = time.Nanosecond
	moduleutil.RunMigrations(t, db, m.Migrations())
	// A value that cannot be scanned into the expiry: loading fails.
	if _, err := db.Exec(`UPDATE privacy_settings SET enabled = 0, cookie_expires_days = 'broken', cookie_name = 'stored' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	ctx, _ := moduleutil.TestModuleContext(t, db)
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !m.IsEnabled() {
		t.Fatal("the consent banner is off after a settings load failure")
	}
	if page := dashboardBody(t, m); !strings.Contains(page, `name="from_fallback"`) || !strings.Contains(page, `role="alert"`) {
		t.Error("the settings page does not warn or mark its form while showing fallback values")
	}
	head := string(m.renderHeadScripts("nonce"))
	for _, purpose := range []string{"ad_storage", "ad_user_data", "ad_personalization", "analytics_storage"} {
		if !strings.Contains(head, "'"+purpose+"': 'denied'") {
			t.Errorf("Consent Mode default for %s is not denied:\n%s", purpose, head)
		}
	}

	form := url.Values{"enabled": {"0"}, "cookie_name": {"form-cookie"}, "cookie_expires_days": {"30"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/privacy", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	admin := store.User{ID: 1, Email: "admin@test.com", Role: "admin"}
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
	m.handleSaveSettings(httptest.NewRecorder(), req)
	var cookieName string
	if err := db.QueryRow(`SELECT cookie_name FROM privacy_settings WHERE id = 1`).Scan(&cookieName); err != nil {
		t.Fatal(err)
	}
	if cookieName != "stored" {
		t.Errorf("stored cookie name = %q: the settings form saved over settings it could not read", cookieName)
	}

	// Repair the row. A form rendered from the fallback is still refused: its
	// values (no services, banner on) would replace the recovered settings.
	if _, err := db.Exec(`UPDATE privacy_settings SET cookie_expires_days = 30 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	form.Set("from_fallback", "1")
	req = httptest.NewRequest(http.MethodPost, "/admin/privacy", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, admin))
	m.handleSaveSettings(httptest.NewRecorder(), req)
	if err := db.QueryRow(`SELECT cookie_name FROM privacy_settings WHERE id = 1`).Scan(&cookieName); err != nil {
		t.Fatal(err)
	}
	if cookieName != "stored" {
		t.Errorf("stored cookie name = %q: a form rendered from fallback values replaced the recovered settings", cookieName)
	}
	// The next read reloads the row without a restart. The settings page is
	// that read here, so it must retry before deciding whether to warn.
	if strings.Contains(dashboardBody(t, m), `name="from_fallback"`) {
		t.Error("the first settings page after the repair still marks its form as fallback")
	}
	if m.IsEnabled() || m.currentSettings().CookieName != "stored" {
		t.Errorf("after the repair: enabled=%v cookie %q, want the stored (disabled) settings", m.IsEnabled(), m.currentSettings().CookieName)
	}
}

// TestSaveRefusesInvalidServicesJSON checks that a services list that does
// not parse is refused. It used to be logged and saved as no services,
// silently dropping every configured service.
func TestSaveRefusesInvalidServicesJSON(t *testing.T) {
	m, db := newTestModule(t)
	if _, err := db.Exec(`UPDATE privacy_settings SET services = '[{"name":"ga","title":"GA"}]' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"enabled": {"1"}, "cookie_name": {"klaro"}, "cookie_expires_days": {"30"}, "services": {`[{"name": "ga",`}}
	req := httptest.NewRequest(http.MethodPost, "/admin/privacy", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{ID: 1, Email: "admin@test.com", Role: "admin"}))
	m.ctx.Render = &render.Renderer{}
	m.handleSaveSettings(httptest.NewRecorder(), req)
	var services string
	if err := db.QueryRow(`SELECT services FROM privacy_settings WHERE id = 1`).Scan(&services); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(services, `"ga"`) {
		t.Errorf("stored services = %q: an invalid list replaced them", services)
	}
}

// dashboardBody renders the settings page for an admin.
func dashboardBody(t *testing.T, m *Module) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/privacy", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{ID: 1, Email: "admin@test.com", Role: "admin"}))
	rr := httptest.NewRecorder()
	m.handleDashboard(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings page: status %d", rr.Code)
	}
	return rr.Body.String()
}

// TestSaveRefusesMalformedForm checks that a settings form whose body does
// not parse is refused instead of saved from the part that parsed. Reading
// from_fallback parsed the form first and dropped the error, and the later
// ParseForm returned nil, so a bad escape turned the consent banner off
// (Codex review of PR #189).
//
// Bug state: read from_fallback before r.ParseForm in handleSaveSettings and
// this fails.
func TestSaveRefusesMalformedForm(t *testing.T) {
	m, db := newTestModule(t)
	if _, err := db.Exec(`UPDATE privacy_settings SET enabled = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadSettings(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/privacy", strings.NewReader("enabled=0&cookie_name=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, store.User{ID: 1, Email: "admin@test.com", Role: "admin"}))
	m.ctx.Render = &render.Renderer{}
	m.handleSaveSettings(httptest.NewRecorder(), req)
	var enabled bool
	if err := db.QueryRow(`SELECT enabled FROM privacy_settings WHERE id = 1`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Error("the consent banner was turned off by a form that did not parse")
	}
}
