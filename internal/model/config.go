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
	ConfigTypeJSON   = "json"
)

// Config keys
const (
	ConfigKeySiteName        = "site_name"
	ConfigKeySiteDescription = "site_description"
	ConfigKeyAdminEmail      = "admin_email"
	ConfigKeyPostsPerPage    = "posts_per_page"
)

// Config represents a site configuration item.
type Config struct {
	Key         string
	Value       string
	Type        string
	Description string
	UpdatedAt   time.Time
	UpdatedBy   sql.NullInt64
}
