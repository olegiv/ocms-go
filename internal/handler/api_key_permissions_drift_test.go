// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"slices"
	"testing"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
)

// TestPermissionGroupsCoverAllPermissions fails when a scope is added to
// model.AllPermissions without a checkbox in the API key form, or when the
// form offers a value validateAPIKeyPermissions would reject. Without it a new
// scope (mcp:access was the first added after the form existed) can ship that
// no administrator is able to grant.
func TestPermissionGroupsCoverAllPermissions(t *testing.T) {
	all := model.AllPermissions()
	offered := make(map[string]int)
	for _, group := range buildPermissionGroups("") {
		if group.TitleKey == "" {
			t.Error("permission group has an empty TitleKey")
		}
		for _, opt := range group.Permissions {
			offered[opt.Value]++
			if opt.DescKey == "" {
				t.Errorf("permission %q has an empty DescKey", opt.Value)
			}
			if !slices.Contains(all, opt.Value) {
				t.Errorf("form offers %q, which model.AllPermissions does not accept", opt.Value)
			}
		}
	}
	for _, perm := range all {
		if offered[perm] != 1 {
			t.Errorf("permission %q appears %d times in the API key form, want exactly 1", perm, offered[perm])
		}
	}
}

// TestPermissionGroupsTranslated fails when a permission group or option label
// is missing from any supported admin language: the form would render the raw
// message key instead of a label.
func TestPermissionGroupsTranslated(t *testing.T) {
	if err := i18n.Init(nil); err != nil {
		t.Fatalf("i18n.Init: %v", err)
	}
	for _, lang := range i18n.SupportedLanguages {
		for _, group := range buildPermissionGroups("") {
			keys := []string{group.TitleKey}
			for _, opt := range group.Permissions {
				keys = append(keys, opt.DescKey)
			}
			for _, key := range keys {
				if got := i18n.T(lang, key); got == key || got == "" {
					t.Errorf("%s: missing translation for %q", lang, key)
				}
			}
		}
	}
}

// TestPermissionGroupsCheckMCPAccess verifies the edit form re-checks the MCP
// scope for a key that already holds it.
func TestPermissionGroupsCheckMCPAccess(t *testing.T) {
	groups := buildPermissionGroups(`["pages:read","mcp:access"]`)
	checked := make(map[string]bool)
	for _, group := range groups {
		for _, opt := range group.Permissions {
			checked[opt.Value] = opt.Checked
		}
	}
	if !checked[model.PermissionMCPAccess] {
		t.Error("mcp:access should be checked for a key that holds it")
	}
	if checked[model.PermissionPagesWrite] {
		t.Error("pages:write should not be checked for a key that lacks it")
	}
}
