package model

import (
	"database/sql"
	"time"
)

// Default menu slugs
const (
	MenuMain   = "main"
	MenuFooter = "footer"
)

// Menu target values
const (
	TargetSelf   = "_self"
	TargetBlank  = "_blank"
	TargetParent = "_parent"
	TargetTop    = "_top"
)

// ValidTargets contains all valid link target values.
var ValidTargets = []string{TargetSelf, TargetBlank, TargetParent, TargetTop}

// Menu represents a navigation menu.
type Menu struct {
	ID        int64
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MenuItem represents an item in a navigation menu.
type MenuItem struct {
	ID        int64
	MenuID    int64
	ParentID  sql.NullInt64
	Title     string
	URL       string
	Target    string
	PageID    sql.NullInt64
	Position  int
	CSSClass  string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MenuItemWithChildren represents a menu item with its children for tree display.
type MenuItemWithChildren struct {
	MenuItem
	Children []MenuItemWithChildren
}

// IsValidTarget checks if a target value is valid.
func IsValidTarget(target string) bool {
	for _, t := range ValidTargets {
		if t == target {
			return true
		}
	}
	return false
}
