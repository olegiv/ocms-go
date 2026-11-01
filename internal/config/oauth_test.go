// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "testing"

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
