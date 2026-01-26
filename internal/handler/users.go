// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// User roles
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RolePublic = "public" // Public users have no admin access
)

// ValidRoles contains all valid user roles.
var ValidRoles = []string{RoleAdmin, RoleEditor, RolePublic}

// UsersPerPage is the number of users to display per page.
const UsersPerPage = 10

// UsersHandler handles user management routes.
type UsersHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	dispatcher     *webhook.Dispatcher
}

// NewUsersHandler creates a new UsersHandler.
func NewUsersHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *UsersHandler {
	return &UsersHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// SetDispatcher sets the webhook dispatcher for event dispatching.
func (h *UsersHandler) SetDispatcher(d *webhook.Dispatcher) {
	h.dispatcher = d
}

// dispatchUserEvent dispatches a user-related webhook event.
func (h *UsersHandler) dispatchUserEvent(ctx context.Context, eventType string, user store.User) {
	if h.dispatcher == nil {
		return
	}

	data := webhook.UserEventData{
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
		Role:  user.Role,
	}

	if err := h.dispatcher.DispatchEvent(ctx, eventType, data); err != nil {
		slog.Error("failed to dispatch webhook event",
			"error", err,
			"event_type", eventType,
			"user_id", user.ID)
	}
}

// UsersListData holds data for the users list template.
type UsersListData struct {
	Users         []store.User
	CurrentUserID int64
	TotalUsers    int64
	Pagination    AdminPagination
}

// List handles GET /admin/users - displays a paginated list of users.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	page := ParsePageParam(r)

	// Get total user count
	totalUsers, err := h.queries.CountUsers(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to count users", "error", err)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalUsers), UsersPerPage)
	offset := int64((page - 1) * UsersPerPage)

	// Fetch users for current page
	users, err := h.queries.ListUsers(r.Context(), store.ListUsersParams{
		Limit:  UsersPerPage,
		Offset: offset,
	})
	if err != nil {
		logAndInternalError(w, "failed to list users", "error", err)
		return
	}

	data := UsersListData{
		Users:         users,
		CurrentUserID: middleware.GetUserID(r),
		TotalUsers:    totalUsers,
		Pagination:    BuildAdminPagination(page, int(totalUsers), UsersPerPage, redirectAdminUsers, r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/users_list", render.TemplateData{
		Title: i18n.T(lang, "nav.users"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.users"), URL: redirectAdminUsers, Active: true},
		},
	})
}

// UserFormData holds data for the user form template.
type UserFormData struct {
	User       *store.User
	Roles      []string
	Errors     map[string]string
	FormValues map[string]string
	IsEdit     bool
}

// NewForm handles GET /admin/users/new - displays the new user form.
func (h *UsersHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	data := UserFormData{
		Roles:      ValidRoles,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     false,
	}

	h.renderNewUserForm(w, r, data)
}

// Create handles POST /admin/users - creates a new user.
func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminUsersNew) {
		return
	}

	// Get form values
	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	role := r.FormValue("role")

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"email": email,
		"name":  name,
		"role":  role,
	}

	// Validate
	validationErrors := make(map[string]string)

	// Email validation
	if email == "" {
		validationErrors["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(email); err != nil {
		validationErrors["email"] = "Invalid email format"
	} else {
		// Check if email already exists
		_, err := h.queries.GetUserByEmail(r.Context(), email)
		if err == nil {
			validationErrors["email"] = "Email already exists"
		} else if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("database error checking email", "error", err)
			validationErrors["email"] = "Error checking email"
		}
	}

	// Name validation
	if name == "" {
		validationErrors["name"] = "Name is required"
	} else if len(name) < 2 {
		validationErrors["name"] = "Name must be at least 2 characters"
	}

	// Password validation
	if password == "" {
		validationErrors["password"] = "Password is required"
	} else if len(password) < 8 {
		validationErrors["password"] = "Password must be at least 8 characters"
	} else if password != passwordConfirm {
		validationErrors["password_confirm"] = "Passwords do not match"
	}

	// Role validation
	if role == "" {
		validationErrors["role"] = "Role is required"
	} else if !isValidRole(role) {
		validationErrors["role"] = "Invalid role"
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		data := UserFormData{
			Roles:      ValidRoles,
			Errors:     validationErrors,
			FormValues: formValues,
			IsEdit:     false,
		}
		h.renderNewUserForm(w, r, data)
		return
	}

	// Hash password
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		slog.Error("failed to hash password", "error", err)
		flashError(w, r, h.renderer, redirectAdminUsersNew, "Error creating user")
		return
	}

	// Create user
	now := time.Now()
	newUser, err := h.queries.CreateUser(r.Context(), store.CreateUserParams{
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
		Name:         name,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to create user", "error", err)
		flashError(w, r, h.renderer, redirectAdminUsersNew, "Error creating user")
		return
	}

	slog.Info("user created", "user_id", newUser.ID, "email", newUser.Email, "created_by", middleware.GetUserID(r))

	// Dispatch user.created webhook event
	h.dispatchUserEvent(r.Context(), model.EventUserCreated, newUser)

	flashSuccess(w, r, h.renderer, redirectAdminUsers, "User created successfully")
}

// EditForm handles GET /admin/users/{id} - displays the edit user form.
func (h *UsersHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminUsers, "Invalid user ID")
		return
	}

	editUser, ok := h.requireUserWithRedirect(w, r, id)
	if !ok {
		return
	}

	data := UserFormData{
		User:   &editUser,
		Roles:  ValidRoles,
		Errors: make(map[string]string),
		FormValues: map[string]string{
			"email": editUser.Email,
			"name":  editUser.Name,
			"role":  editUser.Role,
		},
		IsEdit: true,
	}

	h.renderer.RenderPage(w, r, "admin/users_form", render.TemplateData{
		Title: i18n.T(lang, "users.edit"),
		User:  currentUser,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.users"), URL: redirectAdminUsers},
			{Label: editUser.Name, URL: fmt.Sprintf(redirectAdminUsersID, editUser.ID), Active: true},
		},
	})
}

// Update handles PUT /admin/users/{id} - updates an existing user.
func (h *UsersHandler) Update(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminUsers, "Invalid user ID")
		return
	}

	editUser, ok := h.requireUserWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminUsersID, id)) {
		return
	}

	// Get form values
	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	role := r.FormValue("role")

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"email": email,
		"name":  name,
		"role":  role,
	}

	// Validate
	validationErrors := make(map[string]string)

	// Email validation
	if email == "" {
		validationErrors["email"] = "Email is required"
	} else if _, err := mail.ParseAddress(email); err != nil {
		validationErrors["email"] = "Invalid email format"
	} else if email != editUser.Email {
		// Check if email already exists (only if changed)
		_, err := h.queries.GetUserByEmail(r.Context(), email)
		if err == nil {
			validationErrors["email"] = "Email already exists"
		} else if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("database error checking email", "error", err)
			validationErrors["email"] = "Error checking email"
		}
	}

	// Name validation
	if name == "" {
		validationErrors["name"] = "Name is required"
	} else if len(name) < 2 {
		validationErrors["name"] = "Name must be at least 2 characters"
	}

	// Password validation (optional on edit)
	if password != "" {
		if len(password) < 8 {
			validationErrors["password"] = "Password must be at least 8 characters"
		} else if password != passwordConfirm {
			validationErrors["password_confirm"] = "Passwords do not match"
		}
	}

	// Role validation
	if role == "" {
		validationErrors["role"] = "Role is required"
	} else if !isValidRole(role) {
		validationErrors["role"] = "Invalid role"
	}

	// Business rule: Cannot demote yourself from admin if you're the last admin
	if currentUser.ID == id && editUser.Role == RoleAdmin && role != RoleAdmin {
		adminCount, err := h.queries.CountUsersByRole(r.Context(), RoleAdmin)
		if err != nil {
			slog.Error("failed to count admins", "error", err)
			validationErrors["role"] = "Error checking admin count"
		} else if adminCount <= 1 {
			validationErrors["role"] = "Cannot demote the last admin"
		}
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		data := UserFormData{
			User:       &editUser,
			Roles:      ValidRoles,
			Errors:     validationErrors,
			FormValues: formValues,
			IsEdit:     true,
		}

		h.renderer.RenderPage(w, r, "admin/users_form", render.TemplateData{
			Title: i18n.T(lang, "users.edit"),
			User:  currentUser,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.users"), URL: redirectAdminUsers},
				{Label: editUser.Name, URL: fmt.Sprintf(redirectAdminUsersID, id), Active: true},
			},
		})
		return
	}

	// Update user
	now := time.Now()
	_, err = h.queries.UpdateUser(r.Context(), store.UpdateUserParams{
		Email:     email,
		Role:      role,
		Name:      name,
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		slog.Error("failed to update user", "error", err)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminUsersID, id), "Error updating user")
		return
	}

	// Update password if provided
	if password != "" {
		passwordHash, err := auth.HashPassword(password)
		if err != nil {
			slog.Error("failed to hash password", "error", err)
			flashAndRedirect(w, r, h.renderer, redirectAdminUsers, "User updated but password change failed", "warning")
			return
		}

		err = h.queries.UpdateUserPassword(r.Context(), store.UpdateUserPasswordParams{
			PasswordHash: passwordHash,
			UpdatedAt:    now,
			ID:           id,
		})
		if err != nil {
			slog.Error("failed to update password", "error", err)
			flashAndRedirect(w, r, h.renderer, redirectAdminUsers, "User updated but password change failed", "warning")
			return
		}
	}

	slog.Info("user updated", "user_id", id, "updated_by", currentUser.ID)
	flashSuccess(w, r, h.renderer, redirectAdminUsers, "User updated successfully")
}

// Delete handles DELETE /admin/users/{id} - deletes a user.
func (h *UsersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	currentUser := middleware.GetUser(r)
	if currentUser == nil {
		h.sendDeleteError(w, "Unauthorized")
		return
	}

	// Get user ID from URL
	id, err := ParseIDParam(r)
	if err != nil {
		h.sendDeleteError(w, "Invalid user ID")
		return
	}

	// Business rule: Cannot delete yourself
	if currentUser.ID == id {
		h.sendDeleteError(w, "Cannot delete your own account")
		return
	}

	// Fetch the user being deleted
	deleteUser, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.sendDeleteError(w, "User not found")
		} else {
			slog.Error("failed to get user", "error", err)
			h.sendDeleteError(w, "Error loading user")
		}
		return
	}

	// Business rule: Cannot delete the last admin
	if deleteUser.Role == RoleAdmin {
		adminCount, err := h.queries.CountUsersByRole(r.Context(), RoleAdmin)
		if err != nil {
			slog.Error("failed to count admins", "error", err)
			h.sendDeleteError(w, "Error checking admin count")
			return
		}
		if adminCount <= 1 {
			h.sendDeleteError(w, "Cannot delete the last admin")
			return
		}
	}

	// Delete user
	err = h.queries.DeleteUser(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete user", "error", err)
		h.sendDeleteError(w, "Error deleting user")
		return
	}

	slog.Info("user deleted", "user_id", id, "email", deleteUser.Email, "deleted_by", currentUser.ID)

	// Dispatch user.deleted webhook event
	h.dispatchUserEvent(r.Context(), model.EventUserDeleted, deleteUser)

	// Check if this is an HTMX request
	if r.Header.Get("HX-Request") == "true" {
		// Return empty response for HTMX to remove the row
		w.Header().Set("HX-Trigger", `{"showToast": "User deleted successfully"}`)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Regular request - redirect with flash message
	flashSuccess(w, r, h.renderer, redirectAdminUsers, "User deleted successfully")
}

// sendDeleteError sends an error response for delete operations.
func (h *UsersHandler) sendDeleteError(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast": "`+message+`", "toastType": "error"}`)
	w.WriteHeader(http.StatusBadRequest)
}

// isValidRole checks if a role is valid.
func isValidRole(role string) bool {
	for _, r := range ValidRoles {
		if r == role {
			return true
		}
	}
	return false
}

// requireUserWithRedirect fetches a user by ID and redirects with flash on error.
func (h *UsersHandler) requireUserWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.User, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminUsers, "User", id,
		func(id int64) (store.User, error) { return h.queries.GetUserByID(r.Context(), id) })
}

// renderNewUserForm renders the new user form with the given data.
func (h *UsersHandler) renderNewUserForm(w http.ResponseWriter, r *http.Request, data UserFormData) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	h.renderer.RenderPage(w, r, "admin/users_form", render.TemplateData{
		Title: i18n.T(lang, "users.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.users"), URL: redirectAdminUsers},
			{Label: i18n.T(lang, "users.new"), URL: redirectAdminUsersNew, Active: true},
		},
	})
}
