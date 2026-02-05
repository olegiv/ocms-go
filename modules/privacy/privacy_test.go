// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package privacy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	m := New()

	if m.Name() != "privacy" {
		t.Errorf("expected name 'privacy', got %q", m.Name())
	}

	if m.Version() != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", m.Version())
	}
}

func TestDefaultSettings(t *testing.T) {
	s := defaultSettings()

	if s.Enabled {
		t.Error("expected Enabled to be false by default")
	}

	if s.CookieName != "klaro" {
		t.Errorf("expected CookieName 'klaro', got %q", s.CookieName)
	}

	if s.CookieExpiresDays != 365 {
		t.Errorf("expected CookieExpiresDays 365, got %d", s.CookieExpiresDays)
	}

	if s.Theme != "light" {
		t.Errorf("expected Theme 'light', got %q", s.Theme)
	}

	if s.Position != "bottom-right" {
		t.Errorf("expected Position 'bottom-right', got %q", s.Position)
	}

	if !s.GCMEnabled {
		t.Error("expected GCMEnabled to be true by default")
	}

	if s.GCMWaitForUpdate != 500 {
		t.Errorf("expected GCMWaitForUpdate 500, got %d", s.GCMWaitForUpdate)
	}
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("expected boolToInt(true) = 1")
	}
	if boolToInt(false) != 0 {
		t.Error("expected boolToInt(false) = 0")
	}
}

func TestPredefinedServices(t *testing.T) {
	if len(PredefinedServices) == 0 {
		t.Error("expected predefined services to be non-empty")
	}

	// Check Google Analytics service
	var ga *Service
	for i := range PredefinedServices {
		if PredefinedServices[i].Name == "google-analytics" {
			ga = &PredefinedServices[i]
			break
		}
	}

	if ga == nil {
		t.Fatal("expected google-analytics predefined service")
	}

	if ga.GCMConsentType != "analytics_storage" {
		t.Errorf("expected GCMConsentType 'analytics_storage', got %q", ga.GCMConsentType)
	}
}

func TestServiceJSON(t *testing.T) {
	services := []Service{
		{
			Name:           "test-service",
			Title:          "Test Service",
			Description:    "A test service",
			Purposes:       []string{"analytics"},
			GCMConsentType: "analytics_storage",
		},
	}

	data, err := json.Marshal(services)
	if err != nil {
		t.Fatalf("failed to marshal services: %v", err)
	}

	var parsed []Service
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal services: %v", err)
	}

	if len(parsed) != 1 {
		t.Fatalf("expected 1 service, got %d", len(parsed))
	}

	if parsed[0].Name != "test-service" {
		t.Errorf("expected name 'test-service', got %q", parsed[0].Name)
	}
}

func TestBuildKlaroConfig(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:           true,
			CookieName:        "test-cookie",
			CookieExpiresDays: 30,
			Theme:             "dark",
			PrivacyPolicyURL:  "/privacy",
			GCMEnabled:        true,
			Services: []Service{
				{
					Name:           "google-analytics",
					Title:          "Google Analytics",
					Description:    "Analytics service",
					Purposes:       []string{"analytics"},
					GCMConsentType: "analytics_storage",
				},
			},
		},
	}

	config := m.buildKlaroConfig()

	// Check essential parts of the config
	if !strings.Contains(config, "var klaroConfig = {") {
		t.Error("config should start with 'var klaroConfig = {'")
	}

	if !strings.Contains(config, "storageName: 'test-cookie'") {
		t.Error("config should contain cookie name")
	}

	if !strings.Contains(config, "cookieExpiresAfterDays: 30") {
		t.Error("config should contain cookie expiration")
	}

	if !strings.Contains(config, "theme: ['dark']") {
		t.Error("config should contain dark theme")
	}

	if !strings.Contains(config, "privacyPolicy: '/privacy'") {
		t.Error("config should contain privacy policy URL")
	}

	if !strings.Contains(config, "name: 'google-analytics'") {
		t.Error("config should contain google-analytics service")
	}

	if !strings.Contains(config, "gtag('consent', 'update'") {
		t.Error("config should contain GCM update callback")
	}
}

func TestRenderGCMDefaults(t *testing.T) {
	m := &Module{
		settings: &Settings{
			GCMEnabled:                  true,
			GCMDefaultAnalytics:         false,
			GCMDefaultAdStorage:         false,
			GCMDefaultAdUserData:        false,
			GCMDefaultAdPersonalization: false,
			GCMWaitForUpdate:            500,
		},
	}

	script := m.renderGCMDefaults()

	if !strings.Contains(script, "gtag('consent', 'default'") {
		t.Error("script should contain GCM default consent")
	}

	if !strings.Contains(script, "'analytics_storage': 'denied'") {
		t.Error("script should default analytics_storage to denied")
	}

	if !strings.Contains(script, "'ad_storage': 'denied'") {
		t.Error("script should default ad_storage to denied")
	}

	if !strings.Contains(script, "'wait_for_update': 500") {
		t.Error("script should contain wait_for_update")
	}
}

func TestRenderGCMDefaultsGranted(t *testing.T) {
	m := &Module{
		settings: &Settings{
			GCMEnabled:          true,
			GCMDefaultAnalytics: true,
			GCMWaitForUpdate:    1000,
		},
	}

	script := m.renderGCMDefaults()

	if !strings.Contains(script, "'analytics_storage': 'granted'") {
		t.Error("script should have analytics_storage granted when enabled")
	}
}

func TestRenderHeadScriptsDisabled(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled: false,
		},
	}

	output := m.renderHeadScripts()
	if output != "" {
		t.Error("expected empty output when module is disabled")
	}
}

func TestRenderHeadScriptsEnabled(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:          true,
			GCMEnabled:       true,
			CookieName:       "klaro",
			GCMWaitForUpdate: 500,
		},
	}

	output := string(m.renderHeadScripts())

	if !strings.Contains(output, "gtag('consent', 'default'") {
		t.Error("output should contain GCM defaults script")
	}

	if !strings.Contains(output, "klaro.css") {
		t.Error("output should contain Klaro CSS link")
	}

	if !strings.Contains(output, "var klaroConfig") {
		t.Error("output should contain Klaro config")
	}

	if !strings.Contains(output, "klaro.min.js") {
		t.Error("output should contain Klaro script")
	}
}

func TestBuildGCMCallback(t *testing.T) {
	m := &Module{
		settings: &Settings{
			GCMEnabled: true,
		},
	}

	callback := m.buildGCMCallback("analytics_storage")
	if !strings.Contains(callback, "'analytics_storage': consent ? 'granted' : 'denied'") {
		t.Error("callback should update analytics_storage based on consent")
	}

	// Test multiple consent types
	callback = m.buildGCMCallback("ad_storage,ad_user_data")
	if !strings.Contains(callback, "'ad_storage': consent ? 'granted' : 'denied'") {
		t.Error("callback should update ad_storage")
	}
	if !strings.Contains(callback, "'ad_user_data': consent ? 'granted' : 'denied'") {
		t.Error("callback should update ad_user_data")
	}
}

func TestRenderDebugScript(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Debug: true,
		},
	}

	script := m.renderDebugScript()

	if !strings.Contains(script, "klaro_update") {
		t.Error("debug script should intercept klaro_update events")
	}

	if !strings.Contains(script, "dataLayer.push") {
		t.Error("debug script should intercept dataLayer.push")
	}

	if !strings.Contains(script, "klaro.getManager") {
		t.Error("debug script should use klaro.getManager")
	}

	if !strings.Contains(script, "Debug mode enabled") {
		t.Error("debug script should log that debug mode is enabled")
	}

	// GCM v2: should intercept gtag function for consent calls
	if !strings.Contains(script, "window.gtag = function()") {
		t.Error("debug script should intercept gtag function")
	}

	if !strings.Contains(script, "gtag consent") {
		t.Error("debug script should log gtag consent calls")
	}
}

func TestRenderHeadScriptsWithDebug(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:          true,
			Debug:            true,
			GCMEnabled:       true,
			CookieName:       "klaro",
			GCMWaitForUpdate: 500,
		},
	}

	output := string(m.renderHeadScripts())

	if !strings.Contains(output, "klaro_update") {
		t.Error("output should contain debug script when debug is enabled")
	}
}

func TestRenderHeadScriptsWithoutDebug(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:          true,
			Debug:            false,
			GCMEnabled:       true,
			CookieName:       "klaro",
			GCMWaitForUpdate: 500,
		},
	}

	output := string(m.renderHeadScripts())

	if strings.Contains(output, "klaro_update") {
		t.Error("output should NOT contain debug script when debug is disabled")
	}
}

func TestRenderFooterLinkDisabled(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled: false,
		},
	}

	output := m.renderFooterLink()
	if output != "" {
		t.Error("expected empty output when module is disabled")
	}
}

func TestRenderFooterLinkEnabled(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled: true,
		},
	}

	output := string(m.renderFooterLink())

	if !strings.Contains(output, "klaro.show()") {
		t.Error("output should contain klaro.show() call")
	}

	if !strings.Contains(output, "privacy-settings-link") {
		t.Error("output should contain privacy-settings-link class")
	}

	if !strings.Contains(output, "Cookie Settings") {
		t.Error("output should contain 'Cookie Settings' text")
	}
}

func TestGetPositionCSS(t *testing.T) {
	tests := []struct {
		position string
		contains string
	}{
		{"bottom-left", "left: 20px"},
		{"top-right", "top: 20px"},
		{"top-left", "top: 20px"},
		{"bottom-right", "@media"}, // Should have mobile CSS
		{"", "@media"},             // Should have mobile CSS
	}

	for _, tt := range tests {
		m := &Module{
			settings: &Settings{
				Position: tt.position,
			},
		}

		css := m.getPositionCSS()
		if !strings.Contains(css, tt.contains) {
			t.Errorf("position %q CSS should contain %q, got %q", tt.position, tt.contains, css)
		}
		// All positions should have mobile responsive CSS
		if !strings.Contains(css, "max-width: calc(100% - 20px)") {
			t.Errorf("position %q should have mobile responsive CSS", tt.position)
		}
	}
}
