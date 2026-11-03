// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"strings"
)

// oauthCIMDHostsEnv names the client metadata host allowlist variable.
const oauthCIMDHostsEnv = "OCMS_OAUTH_CIMD_ALLOWED_HOSTS"

// applyOAuthCIMDHostsSetting makes an explicitly empty
// OCMS_OAUTH_CIMD_ALLOWED_HOSTS mean "no client metadata hosts" (pre-registered
// clients and dynamic registration only). The env package cannot tell it from
// an unset variable and substitutes the envDefault of Config.OAuthCIMDAllowedHosts
// for both, so an operator who emptied the list silently kept
// claude.ai,chatgpt.com (security audit 2026-11-03, A-4). Unset still means
// the documented default.
func applyOAuthCIMDHostsSetting(cfg *Config) {
	if value, set := os.LookupEnv(oauthCIMDHostsEnv); set && strings.TrimSpace(value) == "" {
		cfg.OAuthCIMDAllowedHosts = ""
	}
}
