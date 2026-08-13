// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"database/sql"
	"strings"
	"time"
)

// Default menu slugs
const (
	MenuMain   = "main"
	MenuFooter = "footer"
)

// AllowedMenuURLSchemes are the absolute-URL schemes a menu item may point at.
//
// This is the single list: the admin form validator accepts exactly these, and
// any importer producing menu items must too. Keeping a second copy is what let
// the Drupal source reject mailto: and tel: links as unsupported and drop those
// items, even though oCMS stores them happily.
var AllowedMenuURLSchemes = []string{"http", "https", "mailto", "tel"}

// IsAllowedMenuURLScheme reports whether an absolute URL's scheme may be used
// as a menu item target. The scheme is compared case-insensitively.
func IsAllowedMenuURLScheme(scheme string) bool {
	for _, allowed := range AllowedMenuURLSchemes {
		if strings.EqualFold(scheme, allowed) {
			return true
		}
	}
	return false
}

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
