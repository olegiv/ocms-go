// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"testing"
)

// testUser is a mock user struct with the same Role field as store.User.
type testUser struct {
	ID   int64
	Role string
}

func TestGetUserRole(t *testing.T) {
	tests := []struct {
		name     string
		user     any
		expected string
	}{
		{"nil user", nil, ""},
		{"admin user", testUser{ID: 1, Role: "admin"}, "admin"},
		{"editor user", testUser{ID: 2, Role: "editor"}, "editor"},
		{"public user", testUser{ID: 3, Role: "public"}, "public"},
		{"pointer admin", &testUser{ID: 1, Role: "admin"}, "admin"},
		{"nil pointer", (*testUser)(nil), ""},
		{"wrong type (string)", "not a user", ""},
		{"wrong type (int)", 123, ""},
		{"struct without Role", struct{ Name string }{Name: "test"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getUserRole(tt.user)
			if got != tt.expected {
				t.Errorf("getUserRole(%v) = %q, want %q", tt.user, got, tt.expected)
			}
		})
	}
}

// runRoleCheckTests runs tests for role checking functions.
func runRoleCheckTests(t *testing.T, checkFn func(any) bool, tests []struct {
	name     string
	user     any
	expected bool
}) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkFn(tt.user)
			if got != tt.expected {
				t.Errorf("check(%v) = %v, want %v", tt.user, got, tt.expected)
			}
		})
	}
}

func TestRoleChecks(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	isAdmin := funcs["isAdmin"].(func(any) bool)
	isEditor := funcs["isEditor"].(func(any) bool)

	t.Run("isAdmin", func(t *testing.T) {
		runRoleCheckTests(t, isAdmin, []struct {
			name     string
			user     any
			expected bool
		}{
			{"nil user", nil, false},
			{"admin user", testUser{Role: "admin"}, true},
			{"editor user", testUser{Role: "editor"}, false},
			{"public user", testUser{Role: "public"}, false},
			{"unknown role", testUser{Role: "unknown"}, false},
		})
	})

	t.Run("isEditor", func(t *testing.T) {
		runRoleCheckTests(t, isEditor, []struct {
			name     string
			user     any
			expected bool
		}{
			{"nil user", nil, false},
			{"admin user", testUser{Role: "admin"}, true},
			{"editor user", testUser{Role: "editor"}, true},
			{"public user", testUser{Role: "public"}, false},
			{"unknown role", testUser{Role: "unknown"}, false},
		})
	})
}

func TestUserRole(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	userRole := funcs["userRole"].(func(any) string)

	tests := []struct {
		name     string
		user     any
		expected string
	}{
		{"nil user", nil, ""},
		{"admin user", testUser{Role: "admin"}, "admin"},
		{"editor user", testUser{Role: "editor"}, "editor"},
		{"public user", testUser{Role: "public"}, "public"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := userRole(tt.user)
			if got != tt.expected {
				t.Errorf("userRole(%v) = %q, want %q", tt.user, got, tt.expected)
			}
		})
	}
}

func TestGetUserRole_PointerTypes(t *testing.T) {
	// Test with pointer to struct
	user := &testUser{ID: 1, Role: "admin"}
	got := getUserRole(user)
	if got != "admin" {
		t.Errorf("getUserRole(pointer) = %q, want %q", got, "admin")
	}

	// Test with nil pointer
	var nilUser *testUser
	got = getUserRole(nilUser)
	if got != "" {
		t.Errorf("getUserRole(nil pointer) = %q, want empty", got)
	}

	// Test with double pointer (should return empty)
	doublePtr := &user
	got = getUserRole(doublePtr)
	if got != "" {
		t.Errorf("getUserRole(double pointer) = %q, want empty", got)
	}
}

func TestGetUserRole_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		user     any
		expected string
	}{
		{"empty role", testUser{ID: 1, Role: ""}, ""},
		{"whitespace role", testUser{ID: 1, Role: "  "}, "  "},
		{"role with spaces", testUser{ID: 1, Role: " admin "}, " admin "},
		{"numeric string role", testUser{ID: 1, Role: "123"}, "123"},
		{"special chars role", testUser{ID: 1, Role: "admin@#$"}, "admin@#$"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getUserRole(tt.user)
			if got != tt.expected {
				t.Errorf("getUserRole(%v) = %q, want %q", tt.user, got, tt.expected)
			}
		})
	}
}

func TestGetUserRole_DifferentStructTypes(t *testing.T) {
	// Struct with Role field but different type
	type userWithIntRole struct {
		ID   int64
		Role int
	}
	got := getUserRole(userWithIntRole{ID: 1, Role: 1})
	if got != "" {
		t.Errorf("getUserRole(int Role) = %q, want empty", got)
	}

	// Struct with lowercase role field (should not match)
	type userWithLowerRole struct {
		ID   int64
		role string
	}
	got = getUserRole(userWithLowerRole{ID: 1, role: "admin"})
	if got != "" {
		t.Errorf("getUserRole(lowercase role) = %q, want empty", got)
	}

	// Struct with additional fields
	type extendedUser struct {
		ID       int64
		Role     string
		Name     string
		Email    string
		IsActive bool
	}
	got = getUserRole(extendedUser{ID: 1, Role: "editor", Name: "Test", Email: "test@test.com", IsActive: true})
	if got != "editor" {
		t.Errorf("getUserRole(extended user) = %q, want %q", got, "editor")
	}
}

func TestIsAdmin_CaseSensitivity(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	isAdmin := funcs["isAdmin"].(func(any) bool)

	tests := []struct {
		role     string
		expected bool
	}{
		{"admin", true},
		{"Admin", false},
		{"ADMIN", false},
		{"aDmIn", false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := isAdmin(testUser{Role: tt.role})
			if got != tt.expected {
				t.Errorf("isAdmin(role=%q) = %v, want %v", tt.role, got, tt.expected)
			}
		})
	}
}

func TestIsEditor_CaseSensitivity(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	isEditor := funcs["isEditor"].(func(any) bool)

	tests := []struct {
		role     string
		expected bool
	}{
		{"editor", true},
		{"Editor", false},
		{"EDITOR", false},
		{"admin", true},  // Admin is also editor
		{"Admin", false}, // But not with wrong case
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := isEditor(testUser{Role: tt.role})
			if got != tt.expected {
				t.Errorf("isEditor(role=%q) = %v, want %v", tt.role, got, tt.expected)
			}
		})
	}
}

func TestRoleFunctions_WithPointers(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	isAdmin := funcs["isAdmin"].(func(any) bool)
	isEditor := funcs["isEditor"].(func(any) bool)
	userRole := funcs["userRole"].(func(any) string)

	// Test with pointer to user
	adminPtr := &testUser{Role: "admin"}
	editorPtr := &testUser{Role: "editor"}
	publicPtr := &testUser{Role: "public"}

	// isAdmin
	if !isAdmin(adminPtr) {
		t.Error("isAdmin(admin pointer) should be true")
	}
	if isAdmin(editorPtr) {
		t.Error("isAdmin(editor pointer) should be false")
	}
	if isAdmin(publicPtr) {
		t.Error("isAdmin(public pointer) should be false")
	}

	// isEditor
	if !isEditor(adminPtr) {
		t.Error("isEditor(admin pointer) should be true")
	}
	if !isEditor(editorPtr) {
		t.Error("isEditor(editor pointer) should be true")
	}
	if isEditor(publicPtr) {
		t.Error("isEditor(public pointer) should be false")
	}

	// userRole
	if userRole(adminPtr) != "admin" {
		t.Errorf("userRole(admin pointer) = %q, want %q", userRole(adminPtr), "admin")
	}
	if userRole(editorPtr) != "editor" {
		t.Errorf("userRole(editor pointer) = %q, want %q", userRole(editorPtr), "editor")
	}
	if userRole(publicPtr) != "public" {
		t.Errorf("userRole(public pointer) = %q, want %q", userRole(publicPtr), "public")
	}
}

func TestTemplateFuncsExist(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()

	requiredFuncs := []string{"isAdmin", "isEditor", "userRole"}

	for _, name := range requiredFuncs {
		if _, ok := funcs[name]; !ok {
			t.Errorf("TemplateFuncs missing required function: %s", name)
		}
	}
}

func TestRoleFunctions_AdminHasEditorAccess(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	isEditor := funcs["isEditor"].(func(any) bool)

	// Admin should always have editor access (role hierarchy)
	admin := testUser{Role: "admin"}
	if !isEditor(admin) {
		t.Error("admin should have editor access (isEditor should return true for admin)")
	}
}

func TestRoleFunctions_PublicNoAccess(t *testing.T) {
	funcs := (&Renderer{}).TemplateFuncs()
	isAdmin := funcs["isAdmin"].(func(any) bool)
	isEditor := funcs["isEditor"].(func(any) bool)

	// Public user should have no admin access
	public := testUser{Role: "public"}

	if isAdmin(public) {
		t.Error("public should not have admin access")
	}
	if isEditor(public) {
		t.Error("public should not have editor access")
	}
}
