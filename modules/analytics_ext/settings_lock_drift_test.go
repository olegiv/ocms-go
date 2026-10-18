// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_ext

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/testutil"
)

// TestSettingsAccessHoldsTheLock fails when module code touches m.settings in
// a function (or closure) that does not itself take m.settingsMu.
//
// Templates and the Content-Security-Policy read the settings on every
// request while the settings form replaces them, so an unguarded access is a
// data race, the same one analytics_int had (security audit 2026-10-17,
// L-3). Readers go through currentSettings and writers through storeSettings.
//
// Bug state: make renderHeadScripts read m.settings directly again and this
// names the line.
func TestSettingsAccessHoldsTheLock(t *testing.T) {
	testutil.AssertFieldAccessHoldsLock(t, ".", "settings", "settingsMu")
}

// TestSaveSettingsWhileRendering saves the settings form while pages render
// tracking scripts and the CSP is assembled. Run with -race; without it the
// test still checks that the last save wins.
func TestSaveSettingsWhileRendering(t *testing.T) {
	m, cleanup := testModuleWithRenderer(t)
	defer cleanup()

	const saves = 20
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range saves {
			form := url.Values{"ga4_enabled": {"1"}, "ga4_measurement_id": {fmt.Sprintf("G-RACE%04d", i)}}
			req := httptest.NewRequest(http.MethodPost, "/admin/external-analytics", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withUser(req, store.User{ID: 1, Email: "admin@example.com", Role: "admin"})
			m.handleSaveSettings(httptest.NewRecorder(), req)
		}
	})
	// Each reader runs alone in its goroutine: a locked read elsewhere in the
	// same goroutine would order an unlocked one after the write and hide it
	// from the race detector.
	for _, read := range []func(){
		func() { _ = m.renderHeadScripts("nonce") },
		func() { _ = m.renderBodyScripts("nonce") },
		func() { _ = m.CSPSources() },
	} {
		wg.Go(func() {
			for range 10 * saves {
				read()
			}
		})
	}
	wg.Wait()

	if got, want := m.currentSettings().GA4MeasurementID, fmt.Sprintf("G-RACE%04d", saves-1); got != want {
		t.Errorf("GA4MeasurementID = %q, want %q", got, want)
	}
}

// TestCSPSourcesAllowsConfiguredMatomo checks that the Matomo server joins the
// CSP only while Matomo tracking is enabled with a valid URL and site ID.
// Before, the policy never allowed it, so the browser blocked matomo.js.
func TestCSPSourcesAllowsConfiguredMatomo(t *testing.T) {
	const origin = "https://stats.example.com:8443"
	for _, tt := range []struct {
		name     string
		settings *Settings
		want     bool
	}{
		{"enabled", &Settings{MatomoEnabled: true, MatomoURL: origin + "/matomo/", MatomoSiteID: "1"}, true},
		{"disabled", &Settings{MatomoEnabled: false, MatomoURL: origin + "/matomo/", MatomoSiteID: "1"}, false},
		{"malformed URL", &Settings{MatomoEnabled: true, MatomoURL: "javascript:alert(1)", MatomoSiteID: "1"}, false},
		{"wildcard host", &Settings{MatomoEnabled: true, MatomoURL: "https://*/", MatomoSiteID: "1"}, false},
		{"directive break", &Settings{MatomoEnabled: true, MatomoURL: origin + ";sandbox/", MatomoSiteID: "1"}, false},
		{"missing site ID", &Settings{MatomoEnabled: true, MatomoURL: origin + "/", MatomoSiteID: ""}, false},
		{"not loaded", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.storeSettings(tt.settings)
			sources := m.CSPSources()
			for _, directive := range []string{"script-src", "connect-src"} {
				if got := slices.Contains(sources[directive], origin); got != tt.want {
					t.Errorf("%s contains %s = %v, want %v (%v)", directive, origin, got, tt.want, sources[directive])
				}
				for _, source := range sources[directive] {
					if strings.ContainsAny(source, ";, ") || strings.HasSuffix(source, "://*") {
						t.Errorf("%s has unsafe source %q", directive, source)
					}
				}
			}
			if !slices.Contains(sources["script-src"], "https://www.googletagmanager.com") {
				t.Error("GTM host missing from script-src")
			}
		})
	}
}
