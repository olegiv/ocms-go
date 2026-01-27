// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewUsersHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	handler := NewUsersHandler(db, nil, sm)

	if handler == nil {
		t.Fatal("NewUsersHandler returned nil")
	}
	if handler.queries == nil {
		t.Error("queries should not be nil")
	}
	if handler.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
}

func TestUsersHandler_SetDispatcher(t *testing.T) {
	db, sm := testHandlerSetup(t)

	handler := NewUsersHandler(db, nil, sm)

	if handler.dispatcher != nil {
		t.Error("dispatcher should be nil initially")
	}

	// SetDispatcher with nil should not panic
	handler.SetDispatcher(nil)

	if handler.dispatcher != nil {
		t.Error("dispatcher should still be nil after setting nil")
	}
}

func TestIsValidRole(t *testing.T) {
	tests := []struct {
		role  string
		valid bool
	}{
		{"admin", true},
		{"editor", true},
		{"public", true},
		{"", false},
		{"invalid", false},
		{"Admin", false},
		{"ADMIN", false},
		{"superadmin", false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := isValidRole(tt.role)
			if got != tt.valid {
				t.Errorf("isValidRole(%q) = %v; want %v", tt.role, got, tt.valid)
			}
		})
	}
}

func TestValidRoles(t *testing.T) {
	expected := []string{RoleAdmin, RoleEditor, RolePublic}

	if len(ValidRoles) != len(expected) {
		t.Errorf("ValidRoles has %d elements; want %d", len(ValidRoles), len(expected))
	}

	for i, role := range expected {
		if ValidRoles[i] != role {
			t.Errorf("ValidRoles[%d] = %q; want %q", i, ValidRoles[i], role)
		}
	}
}

// Note: List() method requires a renderer for GetAdminLang.
// Full handler testing would require a mock renderer or integration tests.

func TestUsersHandler_Delete_SelfDelete(t *testing.T) {
	db, sm := testHandlerSetup(t)

	// Create a test user
	user := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin User",
		Role:  "admin",
	})

	handler := NewUsersHandler(db, nil, sm)

	// Try to delete self - requires user context
	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/1",
		map[string]string{"id": "1"}, &user)

	handler.Delete(w, req)

	// Should return error - cannot delete self
	assertStatus(t, w.Code, http.StatusBadRequest)

	// Check HTMX error header
	if trigger := w.Header().Get("HX-Trigger"); trigger == "" {
		t.Error("expected HX-Trigger header")
	}
}

func TestUsersHandler_Delete_InvalidID(t *testing.T) {
	db, sm := testHandlerSetup(t)

	user := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin User",
		Role:  "admin",
	})

	handler := NewUsersHandler(db, nil, sm)

	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/abc",
		map[string]string{"id": "abc"}, &user)

	handler.Delete(w, req)

	assertStatus(t, w.Code, http.StatusBadRequest)
}

func TestUsersHandler_Delete_UserNotFound(t *testing.T) {
	db, sm := testHandlerSetup(t)

	user := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin User",
		Role:  "admin",
	})

	handler := NewUsersHandler(db, nil, sm)

	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/9999",
		map[string]string{"id": "9999"}, &user)

	handler.Delete(w, req)

	assertStatus(t, w.Code, http.StatusBadRequest)
}

func TestUsersHandler_Delete_LastAdmin_Blocked(t *testing.T) {
	db, sm := testHandlerSetup(t)

	// Create only one admin user
	admin := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin User",
		Role:  "admin",
	})

	// Create a different user to perform the delete (editor)
	deleter := createTestUser(t, db, testUser{
		Email: "deleter@example.com",
		Name:  "Deleter User",
		Role:  "editor",
	})

	handler := NewUsersHandler(db, nil, sm)

	// Try to delete the last admin using HTMX request
	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/1",
		map[string]string{"id": "1"}, &deleter)
	req.Header.Set("HX-Request", "true")

	handler.Delete(w, req)

	// Should fail - cannot delete last admin
	assertStatus(t, w.Code, http.StatusBadRequest)

	// Verify admin still exists
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", admin.ID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to check user: %v", err)
	}
	if count != 1 {
		t.Error("admin user should not have been deleted")
	}
}

func TestUsersHandler_Delete_Unauthorized(t *testing.T) {
	db, sm := testHandlerSetup(t)

	handler := NewUsersHandler(db, nil, sm)

	req := httptest.NewRequest(http.MethodDelete, "/admin/users/1", nil)
	req = requestWithURLParams(req, map[string]string{"id": "1"})
	req = requestWithSession(sm, req)
	// No user in context

	w := httptest.NewRecorder()

	handler.Delete(w, req)

	assertStatus(t, w.Code, http.StatusBadRequest)
}

func TestUsersHandler_Delete_HTMX(t *testing.T) {
	db, sm := testHandlerSetup(t)

	// Create two users
	admin := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin User",
		Role:  "admin",
	})

	target := createTestUser(t, db, testUser{
		Email: "target@example.com",
		Name:  "Target User",
		Role:  "editor",
	})

	handler := NewUsersHandler(db, nil, sm)

	// Use the actual target ID
	targetIDStr := fmt.Sprintf("%d", target.ID)
	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/"+targetIDStr,
		map[string]string{"id": targetIDStr}, &admin)
	req.Header.Set("HX-Request", "true")

	handler.Delete(w, req)

	assertStatus(t, w.Code, http.StatusOK)

	// Check HTMX trigger header
	if trigger := w.Header().Get("HX-Trigger"); trigger == "" {
		t.Error("expected HX-Trigger header for HTMX response")
	}

	// Verify user was deleted
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", target.ID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to check user deletion: %v", err)
	}
	if count != 0 {
		t.Error("user should have been deleted")
	}
}

// Note: Create, Update, EditForm, NewForm methods require a renderer for GetAdminLang.
// Full handler testing would require a mock renderer or integration tests.

func TestUsersHandler_Update_Unauthorized(t *testing.T) {
	db, sm := testHandlerSetup(t)

	handler := NewUsersHandler(db, nil, sm)

	form := url.Values{}
	form.Set("email", "updated@example.com")
	form.Set("name", "Updated Name")
	form.Set("role", "editor")

	req := httptest.NewRequest(http.MethodPut, "/admin/users/1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithURLParams(req, map[string]string{"id": "1"})
	req = requestWithSession(sm, req)
	// No user in context

	w := httptest.NewRecorder()

	handler.Update(w, req)

	assertStatus(t, w.Code, http.StatusUnauthorized)
}

func TestUsersListData(t *testing.T) {
	data := UsersListData{
		Users:         []store.User{},
		CurrentUserID: 1,
		TotalUsers:    50,
		Pagination: AdminPagination{
			CurrentPage: 2,
			TotalPages:  5,
			HasPrev:     true,
			HasNext:     true,
			PrevPage:    1,
			NextPage:    3,
		},
	}

	if data.Pagination.CurrentPage != 2 {
		t.Error("CurrentPage not set correctly")
	}
	if data.Pagination.TotalPages != 5 {
		t.Error("TotalPages not set correctly")
	}
	if !data.Pagination.HasPrev {
		t.Error("HasPrev should be true")
	}
	if !data.Pagination.HasNext {
		t.Error("HasNext should be true")
	}
}

func TestUserFormData(t *testing.T) {
	data := UserFormData{
		User:       nil,
		Roles:      ValidRoles,
		Errors:     map[string]string{"email": "Invalid"},
		FormValues: map[string]string{"email": "test@example.com"},
		IsEdit:     true,
	}

	if !data.IsEdit {
		t.Error("IsEdit should be true")
	}
	if len(data.Roles) != 3 {
		t.Errorf("expected 3 roles, got %d", len(data.Roles))
	}
	if data.Errors["email"] != "Invalid" {
		t.Error("Errors not set correctly")
	}
}
