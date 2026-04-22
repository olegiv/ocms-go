// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "testing"

// TestStandardConfigFieldsRegistration is a drift test: every config key that
// handlers read from the database MUST be registered in StandardConfigFields,
// otherwise /admin/config can't surface it and operators are stuck editing
// SQL by hand. When you add a new ConfigKey constant that is read at runtime,
// also add a row to StandardConfigFields and an entry to this table.
func TestStandardConfigFieldsRegistration(t *testing.T) {
	required := []string{
		ConfigKeySiteName,
		ConfigKeySiteDescription,
		ConfigKeySiteURL,
		ConfigKeyDefaultOGImage,
		ConfigKeyAdminEmail,
		ConfigKeyPostsPerPage,
		ConfigKeyPoweredBy,
		ConfigKeyCopyright,
		ConfigKeyExcludedIPs,
		ConfigKeyRobotsContentSignal,
		ConfigKeyMCPServerVersion,
	}

	registered := make(map[string]bool, len(StandardConfigFields))
	for _, def := range StandardConfigFields {
		if registered[def.Key] {
			t.Errorf("duplicate StandardConfigFields entry: %q", def.Key)
		}
		registered[def.Key] = true
		if def.Type == "" {
			t.Errorf("StandardConfigFields[%q]: empty Type", def.Key)
		}
	}

	for _, key := range required {
		if !registered[key] {
			t.Errorf("config key %q is not in StandardConfigFields — add it so /admin/config can expose it", key)
		}
	}
}
