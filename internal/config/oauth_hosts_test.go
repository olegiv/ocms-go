// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"testing"
)

// TestOAuthCIMDAllowedHostsUnsetAndEmpty pins audit A-4 at the layer that
// reads the environment: unset keeps the documented default, but an
// explicitly empty value must reach the OAuth service as "no hosts" instead of
// being replaced by that default.
func TestOAuthCIMDAllowedHostsUnsetAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   bool
		value string
		want  string
	}{
		{name: "unset", want: "claude.ai,chatgpt.com"},
		{name: "empty", set: true, value: "", want: ""},
		{name: "blank", set: true, value: "  ", want: ""},
		{name: "custom", set: true, value: "client.example,other.example", want: "client.example,other.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OCMS_SESSION_SECRET", testSessionSecret)
			t.Setenv("OCMS_ENV", "development")
			// t.Setenv records the original state and restores it on cleanup,
			// which also makes the unset case safe to apply with Unsetenv.
			t.Setenv(oauthCIMDHostsEnv, tc.value)
			if !tc.set {
				if err := os.Unsetenv(oauthCIMDHostsEnv); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.OAuthCIMDAllowedHosts != tc.want {
				t.Fatalf("OAuthCIMDAllowedHosts = %q, want %q", cfg.OAuthCIMDAllowedHosts, tc.want)
			}
		})
	}
}
