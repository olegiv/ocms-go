// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package admin provides templ-based views for the admin interface.
package admin

import (
	"strings"

	"github.com/olegiv/ocms-go/internal/i18n"
)

// LangOption represents a language option for the admin UI language switcher.
type LangOption struct {
	Code string
	Name string
}

// PageContext carries shared data for all admin templ views.
type PageContext struct {
	Title          string
	User           UserInfo
	Flash          string
	FlashType      string
	SiteName       string
	CurrentPath    string
	AdminLang      string
	LangOptions    []LangOption
	Breadcrumbs    []Breadcrumb
	SidebarModules []SidebarModule
}

// UserInfo holds user data for the admin views.
type UserInfo struct {
	ID    int64
	Name  string
	Email string
	Role  string
}

// Breadcrumb represents a breadcrumb navigation item.
type Breadcrumb struct {
	Label  string
	URL    string
	Active bool
}

// SidebarModule represents a module sidebar entry.
type SidebarModule struct {
	Name     string
	Label    string
	AdminURL string
}

// T translates a key using the admin language.
func (pc *PageContext) T(key string, args ...any) string {
	return i18n.T(pc.AdminLang, key, args...)
}

// TDefault translates a key, falling back to defaultVal if the key is not found.
func (pc *PageContext) TDefault(key string, defaultVal string) string {
	result := i18n.T(pc.AdminLang, key)
	if result == key {
		return defaultVal
	}
	return result
}

// IsAdmin returns true if the user has admin role.
func (pc *PageContext) IsAdmin() bool {
	return pc.User.Role == "admin"
}

// IsEditor returns true if the user has at least editor role.
func (pc *PageContext) IsEditor() bool {
	return pc.User.Role == "admin" || pc.User.Role == "editor"
}

// IsActive returns true if the given path matches the current path.
func (pc *PageContext) IsActive(path string) bool {
	return pc.CurrentPath == path
}

// HasPrefix returns true if the current path starts with the given prefix.
func (pc *PageContext) HasPrefix(prefix string) bool {
	return strings.HasPrefix(pc.CurrentPath, prefix)
}

// UserInitial returns the first character of the user's name for avatar.
func (pc *PageContext) UserInitial() string {
	if pc.User.Name == "" {
		return "A"
	}
	return string([]rune(pc.User.Name)[0])
}
