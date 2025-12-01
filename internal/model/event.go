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
	EventCategoryAuth   = "auth"
	EventCategoryPage   = "page"
	EventCategoryUser   = "user"
	EventCategoryConfig = "config"
	EventCategorySystem = "system"
	EventCategoryCache  = "cache"
)

// Event represents a system event log entry.
type Event struct {
	ID        int64
	Level     string
	Category  string
	Message   string
	UserID    sql.NullInt64
	Metadata  string // JSON string
	CreatedAt time.Time
}
