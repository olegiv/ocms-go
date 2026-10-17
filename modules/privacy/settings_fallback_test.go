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

	// Repair the row: the next read reloads it without a restart.
	if _, err := db.Exec(`UPDATE privacy_settings SET cookie_expires_days = 30 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if m.IsEnabled() || m.currentSettings().CookieName != "stored" {
		t.Errorf("after the repair: enabled=%v cookie %q, want the stored (disabled) settings", m.IsEnabled(), m.currentSettings().CookieName)
	}
}
