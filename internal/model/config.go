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
	ConfigKeySiteURL         = "site_url"
	ConfigKeyDefaultOGImage  = "default_og_image"
	ConfigKeyAdminEmail      = "admin_email"
	ConfigKeyPostsPerPage    = "posts_per_page"
	ConfigKeyPoweredBy       = "powered_by"
	ConfigKeyCopyright       = "copyright"
)

// TranslatableConfigKeys is the list of config keys that support per-language translations.
var TranslatableConfigKeys = []string{
	ConfigKeySiteName,
	ConfigKeySiteDescription,
	ConfigKeyPoweredBy,
	ConfigKeyCopyright,
}

// ConfigFieldDefinition defines a standard config field with its metadata.
type ConfigFieldDefinition struct {
	Key          string
	DefaultValue string
	Type         string
	Description  string
}

// StandardConfigFields defines all config fields that should always be shown,
// even on a newly created site without seeding. These fields are displayed
// in the admin config page and can be edited by administrators.
var StandardConfigFields = []ConfigFieldDefinition{
	{ConfigKeySiteName, "Opossum CMS", ConfigTypeString, "The name of your site"},
	{ConfigKeySiteDescription, "", ConfigTypeString, "A short description of your site"},
	{ConfigKeySiteURL, "", ConfigTypeString, "Full site URL for canonical links and OG tags (e.g., https://example.com)"},
	{ConfigKeyDefaultOGImage, "", ConfigTypeString, "Default Open Graph image URL for social sharing (1200x630px recommended)"},
	{ConfigKeyCopyright, "", ConfigTypeString, "Footer copyright text (leave empty for automatic)"},
	{ConfigKeyPoweredBy, "Powered by oCMS", ConfigTypeString, "Footer powered by text"},
	{ConfigKeyPostsPerPage, "10", ConfigTypeInt, "Number of posts to display per page"},
	{ConfigKeyAdminEmail, "admin@example.com", ConfigTypeString, "Administrator email address"},
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
