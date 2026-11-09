// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestDCRInitialAccessTokenConfiguration(t *testing.T) {
	const name = "OCMS_OAUTH_DCR_INITIAL_ACCESS_TOKEN"
	const valid = "a4bc19e5307480df69b6528f7253ee3346de303ff40d9408ee3cbd61a29c5f74"
	t.Setenv("OCMS_SESSION_SECRET", testSessionSecret)
	t.Setenv("OCMS_ENV", "development")
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"unset", "", true},
		{"random hex", valid, true},
		{"short", valid[:63], false},
		{"whitespace", " " + valid, false},
		{"non-hex", "!" + valid[1:], false},
		{"repeated", strings.Repeat("aB012345", 8), false},
		{"low diversity", strings.Repeat("0", 63) + "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(name, tc.value)
			cfg, err := Load()
			if tc.valid {
				if err != nil || cfg.OAuthDCRInitialAccessToken != tc.value {
					t.Fatalf("valid token configuration was rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), tc.value) {
				t.Fatal("malformed token must fail with a safe configuration error")
			}
		})
	}
}

func TestOAuthOptInConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, env, site, ttl, refresh string
		enabled, wantError            bool
	}{
		{name: "default", env: "development"},
		{name: "no_issuer", env: "development", enabled: true, wantError: true},
		{name: "enabled", env: "development", site: "http://127.0.0.1:8080", enabled: true},
		{name: "short_access", env: "development", site: "https://cms.test", ttl: "59", enabled: true, wantError: true},
		{name: "long_refresh", env: "development", site: "https://cms.test", refresh: "91", enabled: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OCMS_SESSION_SECRET", testSessionSecret)
			t.Setenv("OCMS_ENV", tc.env)
			t.Setenv("OCMS_SITE_URL", tc.site)
			t.Setenv("OCMS_OAUTH_ENABLED", "false")
			if tc.enabled {
				t.Setenv("OCMS_OAUTH_ENABLED", "true")
			}
			t.Setenv("OCMS_OAUTH_ACCESS_TTL", tc.ttl)
			t.Setenv("OCMS_OAUTH_REFRESH_TTL_DAYS", tc.refresh)
			cfg, err := Load()
			if (err != nil) != tc.wantError {
				t.Fatalf("Load error=%v", err)
			}
			if err == nil && cfg.OAuthEnabled != tc.enabled {
				t.Fatal("OAuth flag not applied")
			}
		})
	}
}

func TestDemoModeRejectsOAuthAtStartup(t *testing.T) {
	t.Setenv("OCMS_SESSION_SECRET", testSessionSecret)
	t.Setenv("OCMS_ENV", "development")
	t.Setenv("OCMS_SITE_URL", "https://cms.test")
	t.Setenv("OCMS_OAUTH_ALLOW_DCR", "false")
	t.Setenv("OCMS_DEMO_MODE", "true")
	t.Setenv("OCMS_OAUTH_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OCMS_OAUTH_ENABLED must be false when OCMS_DEMO_MODE is true") {
		t.Fatalf("demo OAuth configuration was not refused: %v", err)
	}
	t.Setenv("OCMS_OAUTH_ENABLED", "false")
	if _, err := Load(); err != nil {
		t.Fatalf("demo without OAuth failed: %v", err)
	}
	t.Setenv("OCMS_DEMO_MODE", "false")
	t.Setenv("OCMS_OAUTH_ENABLED", "true")
	if _, err := Load(); err != nil {
		t.Fatalf("OAuth outside demo mode failed: %v", err)
	}
}
