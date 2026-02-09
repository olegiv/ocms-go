// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"database/sql"
	"time"
)

// Event levels
const (
	EventLevelInfo    = "info"
	EventLevelWarning = "warning"
	EventLevelError   = "error"
)

// Event categories
const (
	EventCategoryAuth     = "auth"
	EventCategoryPage     = "page"
	EventCategoryUser     = "user"
	EventCategoryConfig   = "config"
	EventCategorySystem   = "system"
	EventCategoryCache    = "cache"
	EventCategoryMigrator = "migrator"
	EventCategoryMedia    = "media"
	EventCategoryTag      = "tag"
	EventCategoryCategory = "category"
	EventCategoryMenu     = "menu"
	EventCategoryAPIKey   = "api_key"
	EventCategoryWebhook  = "webhook"
	EventCategorySecurity  = "security"
	EventCategoryScheduler = "scheduler"
)

// Event represents a system event log entry.
type Event struct {
	ID         int64
	Level      string
	Category   string
	Message    string
	UserID     sql.NullInt64
	Metadata   string // JSON string
	IPAddress  string
	RequestURL string
	CreatedAt  time.Time
}
