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

	"github.com/olegiv/ocms-go/internal/model"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
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
	expected := []string{model.RoleAdmin, model.RoleEditor, model.RolePublic}

	if len(model.ValidRoles) != len(expected) {
		t.Errorf("ValidRoles has %d elements; want %d", len(model.ValidRoles), len(expected))
	}

	for i, role := range expected {
		if model.ValidRoles[i] != role {
			t.Errorf("ValidRoles[%d] = %q; want %q", i, model.ValidRoles[i], role)
		}
	}
}

// Note: List() method requires a renderer for GetAdminLang.
// Full handler testing would require a mock renderer or integration tests.

func TestUsersHandler_Delete_SelfDelete(t *testing.T) {
	db, sm := testHandlerSetup(t)

	handler := NewUsersHandler(db, nil, sm)

	// Try to delete self - requires user context
	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/1",
		map[string]string{"id": "1"}, new(createTestUser(t, db, testUser{
			Email: "admin@example.com",
			Name:  "Admin User",
			Role:  "admin",
		})))

	handler.Delete(w, req)

	// Should return error - cannot delete self
	assertStatus(t, w.Code, http.StatusBadRequest)

	// Check HTMX error header
	if trigger := w.Header().Get("HX-Trigger"); trigger == "" {
		t.Error("expected HX-Trigger header")
	}
}

func TestUsersHandler_Delete_BadRequest(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"InvalidID", "abc"},
		{"UserNotFound", "9999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, sm := testHandlerSetup(t)

			handler := NewUsersHandler(db, nil, sm)

			req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/"+tt.id,
				map[string]string{"id": tt.id}, new(createTestUser(t, db, testUser{
					Email: "admin@example.com",
					Name:  "Admin User",
					Role:  "admin",
				})))

			handler.Delete(w, req)

			assertStatus(t, w.Code, http.StatusBadRequest)
		})
	}
}

func TestUsersHandler_Delete_LastAdmin_Blocked(t *testing.T) {
	db, sm := testHandlerSetup(t)

	// Create only one admin user
	admin := createTestUser(t, db, testUser{
		Email: "admin@example.com",
		Name:  "Admin User",
		Role:  "admin",
	})

	handler := NewUsersHandler(db, nil, sm)

	// Try to delete the last admin using HTMX request
	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/1",
		map[string]string{"id": "1"}, new(createTestUser(t, db, testUser{
			Email: "deleter@example.com",
			Name:  "Deleter User",
			Role:  "editor",
		})))
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

	target := createTestUser(t, db, testUser{
		Email: "target@example.com",
		Name:  "Target User",
		Role:  "editor",
	})

	handler := NewUsersHandler(db, nil, sm)

	// Use the actual target ID
	targetIDStr := fmt.Sprintf("%d", target.ID)
	req, w := newAuthenticatedDeleteRequest(t, sm, "/admin/users/"+targetIDStr,
		map[string]string{"id": targetIDStr}, new(createTestUser(t, db, testUser{
			Email: "admin@example.com",
			Name:  "Admin User",
			Role:  "admin",
		})))
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
	data := adminviews.UsersListData{
		Users:      []adminviews.UserListItem{},
		TotalCount: 50,
		Pagination: adminviews.PaginationData{
			CurrentPage: 2,
			TotalPages:  5,
			HasPrev:     true,
			HasNext:     true,
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
	data := adminviews.UserFormData{
		User:       nil,
		Roles:      model.ValidRoles,
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

func TestUserItem_ProfileFields(t *testing.T) {
	item := adminviews.UserItem{
		ID:          1,
		Name:        "Test User",
		Email:       "test@example.com",
		Role:        "editor",
		Avatar:      "/uploads/avatar.jpg",
		Bio:         "Software engineer",
		WebsiteURL:  "https://example.com",
		LinkedInURL: "https://linkedin.com/in/test",
		GitHubURL:   "https://github.com/test",
	}

	if item.Avatar != "/uploads/avatar.jpg" {
		t.Errorf("Avatar = %q; want /uploads/avatar.jpg", item.Avatar)
	}
	if item.Bio != "Software engineer" {
		t.Errorf("Bio = %q; want Software engineer", item.Bio)
	}
	if item.WebsiteURL != "https://example.com" {
		t.Errorf("WebsiteURL = %q; want https://example.com", item.WebsiteURL)
	}
	if item.LinkedInURL != "https://linkedin.com/in/test" {
		t.Errorf("LinkedInURL = %q; want https://linkedin.com/in/test", item.LinkedInURL)
	}
	if item.GitHubURL != "https://github.com/test" {
		t.Errorf("GitHubURL = %q; want https://github.com/test", item.GitHubURL)
	}
}

func TestUserProfileFields_CreateAndRetrieve(t *testing.T) {
	db, _ := testHandlerSetup(t)

	// Insert user with profile fields
	_, err := db.Exec(
		`INSERT INTO users (email, password_hash, role, name, avatar, bio, website_url, linkedin_url, github_url, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"profile@example.com", "$argon2id$v=19$m=65536,t=1,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG",
		"editor", "Profile User",
		"/uploads/avatar.jpg", "A short bio",
		"https://example.com", "https://linkedin.com/in/user", "https://github.com/user",
	)
	if err != nil {
		t.Fatalf("failed to insert user with profile fields: %v", err)
	}

	// Read back and verify
	var avatar, bio, websiteURL, linkedinURL, githubURL string
	err = db.QueryRow(
		"SELECT avatar, bio, website_url, linkedin_url, github_url FROM users WHERE email = ?",
		"profile@example.com",
	).Scan(&avatar, &bio, &websiteURL, &linkedinURL, &githubURL)
	if err != nil {
		t.Fatalf("failed to read user profile fields: %v", err)
	}

	if avatar != "/uploads/avatar.jpg" {
		t.Errorf("avatar = %q; want /uploads/avatar.jpg", avatar)
	}
	if bio != "A short bio" {
		t.Errorf("bio = %q; want 'A short bio'", bio)
	}
	if websiteURL != "https://example.com" {
		t.Errorf("website_url = %q; want https://example.com", websiteURL)
	}
	if linkedinURL != "https://linkedin.com/in/user" {
		t.Errorf("linkedin_url = %q; want https://linkedin.com/in/user", linkedinURL)
	}
	if githubURL != "https://github.com/user" {
		t.Errorf("github_url = %q; want https://github.com/user", githubURL)
	}
}

func TestUserProfileFields_DefaultEmpty(t *testing.T) {
	db, _ := testHandlerSetup(t)

	// Create user without profile fields — defaults should be empty strings
	user := createTestUser(t, db, testUser{
		Email: "nofields@example.com",
		Name:  "No Fields",
		Role:  "editor",
	})

	var avatar, bio, websiteURL, linkedinURL, githubURL string
	err := db.QueryRow(
		"SELECT avatar, bio, website_url, linkedin_url, github_url FROM users WHERE id = ?",
		user.ID,
	).Scan(&avatar, &bio, &websiteURL, &linkedinURL, &githubURL)
	if err != nil {
		t.Fatalf("failed to read user: %v", err)
	}

	if avatar != "" {
		t.Errorf("avatar should default to empty, got %q", avatar)
	}
	if bio != "" {
		t.Errorf("bio should default to empty, got %q", bio)
	}
	if websiteURL != "" {
		t.Errorf("website_url should default to empty, got %q", websiteURL)
	}
	if linkedinURL != "" {
		t.Errorf("linkedin_url should default to empty, got %q", linkedinURL)
	}
	if githubURL != "" {
		t.Errorf("github_url should default to empty, got %q", githubURL)
	}
}

func TestUserProfileFields_UpdateViaSQL(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestUser(t, db, testUser{
		Email: "update@example.com",
		Name:  "Update User",
		Role:  "editor",
	})

	// Update profile fields
	_, err := db.Exec(
		`UPDATE users SET avatar = ?, bio = ?, website_url = ?, linkedin_url = ?, github_url = ? WHERE id = ?`,
		"/uploads/new-avatar.png", "Updated bio",
		"https://mysite.com", "https://linkedin.com/in/updated", "https://github.com/updated",
		user.ID,
	)
	if err != nil {
		t.Fatalf("failed to update profile fields: %v", err)
	}

	var avatar, bio, websiteURL, linkedinURL, githubURL string
	err = db.QueryRow(
		"SELECT avatar, bio, website_url, linkedin_url, github_url FROM users WHERE id = ?",
		user.ID,
	).Scan(&avatar, &bio, &websiteURL, &linkedinURL, &githubURL)
	if err != nil {
		t.Fatalf("failed to read updated user: %v", err)
	}

	if avatar != "/uploads/new-avatar.png" {
		t.Errorf("avatar = %q; want /uploads/new-avatar.png", avatar)
	}
	if bio != "Updated bio" {
		t.Errorf("bio = %q; want 'Updated bio'", bio)
	}
	if websiteURL != "https://mysite.com" {
		t.Errorf("website_url = %q; want https://mysite.com", websiteURL)
	}
	if linkedinURL != "https://linkedin.com/in/updated" {
		t.Errorf("linkedin_url = %q; want https://linkedin.com/in/updated", linkedinURL)
	}
	if githubURL != "https://github.com/updated" {
		t.Errorf("github_url = %q; want https://github.com/updated", githubURL)
	}
}
