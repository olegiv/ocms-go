// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"database/sql"
	"time"
)

// Config types
const (
	ConfigTypeString = "string"
	ConfigTypeInt    = "int"
	ConfigTypeBool   = "bool"
)

// Config keys
const (
	ConfigKeySiteName        = "site_name"
	ConfigKeySiteDescription = "site_description"
	ConfigKeyAdminEmail      = "admin_email"
	ConfigKeyPostsPerPage    = "posts_per_page"
)

// TranslatableConfigKeys is the list of config keys that support per-language translations.
var TranslatableConfigKeys = []string{
	ConfigKeySiteName,
	ConfigKeySiteDescription,
}

// IsTranslatableConfigKey checks if a config key supports translations.
func IsTranslatableConfigKey(key string) bool {
	for _, k := range TranslatableConfigKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Config represents a site configuration item.
type Config struct {
	Key         string
	Value       string
	Type        string
	Description string
	UpdatedAt   time.Time
	UpdatedBy   sql.NullInt64
}
