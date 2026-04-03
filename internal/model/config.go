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
	ConfigTypeText   = "text" // multi-line text, renders as textarea
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
	ConfigKeyExcludedIPs     = "excluded_ips"
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
	{Key: ConfigKeySiteName, DefaultValue: "Opossum CMS", Type: ConfigTypeString, Description: "The name of your site"},
	{Key: ConfigKeySiteDescription, DefaultValue: "", Type: ConfigTypeString, Description: "A short description of your site"},
	{Key: ConfigKeySiteURL, DefaultValue: "", Type: ConfigTypeString, Description: "Full site URL for canonical links and OG tags (e.g., https://example.com)"},
	{Key: ConfigKeyDefaultOGImage, DefaultValue: "", Type: ConfigTypeString, Description: "Default Open Graph image URL for social sharing (1200x630px recommended)"},
	{Key: ConfigKeyCopyright, DefaultValue: "", Type: ConfigTypeString, Description: "Footer copyright text (leave empty for automatic)"},
	{Key: ConfigKeyPoweredBy, DefaultValue: "Powered by oCMS", Type: ConfigTypeString, Description: "Footer powered by text"},
	{Key: ConfigKeyPostsPerPage, DefaultValue: "10", Type: ConfigTypeInt, Description: "Number of posts to display per page"},
	{Key: ConfigKeyAdminEmail, DefaultValue: "admin@example.com", Type: ConfigTypeString, Description: "Administrator email address"},
	{Key: ConfigKeyExcludedIPs, DefaultValue: "", Type: ConfigTypeText, Description: "IPs or CIDRs to exclude from analytics and event logging (one per line)"},
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
