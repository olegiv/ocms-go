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
	"net/url"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// UsersPerPage is the number of users to display per page.
const UsersPerPage = 10

var usersSortableFields = map[string]SortConfig{
	"name":          {DefaultDir: sortDirAsc},
	"email":         {DefaultDir: sortDirAsc},
	"role":          {DefaultDir: sortDirAsc},
	"created_at":    {DefaultDir: sortDirDesc},
	"last_login_at": {DefaultDir: sortDirDesc},
}

// MinPasswordLength is the minimum required password length.
const MinPasswordLength = 12

// UsersHandler handles user management routes.
type UsersHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	dispatcher     *webhook.Dispatcher
	eventService   *service.EventService
}

// NewUsersHandler creates a new UsersHandler.
func NewUsersHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *UsersHandler {
	return &UsersHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		eventService:   service.NewEventService(db),
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

// List handles GET /admin/users - displays a paginated list of users.
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)
	page := ParsePageParam(r)
	perPage := ParsePerPageParam(r, UsersPerPage, maxPerPageSelectionValue)
	sortField, sortDir := parseSortParams(r, "created_at", sortDirDesc, usersSortableFields)
	currentUserID := middleware.GetUserID(r)

	// Get total user count
	totalUsers, err := h.queries.CountUsers(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to count users", "error", err)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalUsers), perPage)
	offset := int64((page - 1) * perPage)

	// Fetch users for current page
	users, err := h.queries.ListUsersSorted(r.Context(), store.ListUsersSortedParams{
		Limit:     int64(perPage),
		Offset:    offset,
		SortField: sortField,
		SortDir:   sortDir,
	})
	if err != nil {
		logAndInternalError(w, "failed to list users", "error", err)
		return
	}

	// Convert to view types
	var viewUsers []adminviews.UserListItem
	for _, u := range users {
		item := adminviews.UserListItem{
			ID:            u.ID,
			Name:          u.Name,
			Email:         u.Email,
			Role:          u.Role,
			CreatedAt:     u.CreatedAt,
			IsCurrentUser: u.ID == currentUserID,
		}
		if u.LastLoginAt.Valid {
			item.LastLoginAt = new(u.LastLoginAt.Time)
		}
		viewUsers = append(viewUsers, item)
	}

	handlerPagination := BuildAdminPagination(page, int(totalUsers), perPage, redirectAdminUsers, r.URL.Query())
	handlerPagination.SortField = sortField
	handlerPagination.SortDir = sortDir
	pagination := convertPagination(handlerPagination)
	pagination.BulkAction = bulkPaginationAction(bulkScopeUsers, redirectAdminUsers+RouteSuffixBulkDelete)
	pagination.PerPageSelector = perPageSelector(perPage, perPageOptionsStandard)

	viewData := adminviews.UsersListData{
		Users:      viewUsers,
		TotalCount: totalUsers,
		Pagination: pagination,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "nav.users"), usersBreadcrumbs(lang))
	renderTempl(w, r, adminviews.UsersListPage(pc, viewData))
}

// NewForm handles GET /admin/users/new - displays the new user form.
func (h *UsersHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionCreateUser, redirectAdminUsers) {
		return
	}

	lang := h.renderer.GetAdminLang(r)
	data := adminviews.UserFormData{
		Roles:      model.ValidRoles,
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     false,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "users.new"), userFormBreadcrumbs(lang, false))
	renderTempl(w, r, adminviews.UserFormPage(pc, data))
}

// Create handles POST /admin/users - creates a new user.
func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionCreateUser, redirectAdminUsers) {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminUsersNew) {
		return
	}

	// Get form values
	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	role := r.FormValue("role")
	avatar := strings.TrimSpace(r.FormValue("avatar"))
	bio := strings.TrimSpace(r.FormValue("bio"))
	websiteURL := strings.TrimSpace(r.FormValue("website_url"))
	linkedinURL := strings.TrimSpace(r.FormValue("linkedin_url"))
	githubURL := strings.TrimSpace(r.FormValue("github_url"))

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"email":        email,
		"name":         name,
		"role":         role,
		"avatar":       avatar,
		"bio":          bio,
		"website_url":  websiteURL,
		"linkedin_url": linkedinURL,
		"github_url":   githubURL,
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
	switch {
	case password == "":
		validationErrors["password"] = "Password is required"
	case len(password) < MinPasswordLength:
		validationErrors["password"] = fmt.Sprintf("Password must be at least %d characters", MinPasswordLength)
	case password != passwordConfirm:
		validationErrors["password_confirm"] = "Passwords do not match"
	}

	// Role validation
	if role == "" {
		validationErrors["role"] = "Role is required"
	} else if !isValidRole(role) {
		validationErrors["role"] = "Invalid role"
	}

	// Profile field validation
	validateProfileFields(avatar, bio, websiteURL, linkedinURL, githubURL, validationErrors)

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		lang := h.renderer.GetAdminLang(r)
		data := adminviews.UserFormData{
			Roles:      model.ValidRoles,
			Errors:     validationErrors,
			FormValues: formValues,
			IsEdit:     false,
		}
		pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "users.new"), userFormBreadcrumbs(lang, false))
		renderTempl(w, r, adminviews.UserFormPage(pc, data))
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
		Avatar:       avatar,
		Bio:          bio,
		WebsiteUrl:   websiteURL,
		LinkedinUrl:  linkedinURL,
		GithubUrl:    githubURL,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to create user", "error", err)
		flashError(w, r, h.renderer, redirectAdminUsersNew, "Error creating user")
		return
	}

	slog.Info("user created", "user_id", newUser.ID, "email", newUser.Email, "created_by", middleware.GetUserID(r))
	_ = h.eventService.LogUserEvent(r.Context(), model.EventLevelInfo, "User created", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"user_id": newUser.ID, "email": newUser.Email, "role": newUser.Role})

	// Dispatch user.created webhook event
	h.dispatchUserEvent(r.Context(), model.EventUserCreated, newUser)

	flashSuccess(w, r, h.renderer, redirectAdminUsers, "User created successfully")
}

// EditForm handles GET /admin/users/{id} - displays the edit user form.
func (h *UsersHandler) EditForm(w http.ResponseWriter, r *http.Request) {
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

	data := adminviews.UserFormData{
		User: &adminviews.UserItem{
			ID:          editUser.ID,
			Name:        editUser.Name,
			Email:       editUser.Email,
			Role:        editUser.Role,
			Avatar:      editUser.Avatar,
			Bio:         editUser.Bio,
			WebsiteURL:  editUser.WebsiteUrl,
			LinkedInURL: editUser.LinkedinUrl,
			GitHubURL:   editUser.GithubUrl,
		},
		Roles:  model.ValidRoles,
		Errors: make(map[string]string),
		FormValues: map[string]string{
			"email":        editUser.Email,
			"name":         editUser.Name,
			"role":         editUser.Role,
			"avatar":       editUser.Avatar,
			"bio":          editUser.Bio,
			"website_url":  editUser.WebsiteUrl,
			"linkedin_url": editUser.LinkedinUrl,
			"github_url":   editUser.GithubUrl,
		},
		IsEdit: true,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "users.edit"), userEditBreadcrumbs(lang, editUser.Name, editUser.ID))
	renderTempl(w, r, adminviews.UserFormPage(pc, data))
}

// Update handles PUT /admin/users/{id} - updates an existing user.
func (h *UsersHandler) Update(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionEditUser, redirectAdminUsers) {
		return
	}

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
	avatar := strings.TrimSpace(r.FormValue("avatar"))
	bio := strings.TrimSpace(r.FormValue("bio"))
	websiteURL := strings.TrimSpace(r.FormValue("website_url"))
	linkedinURL := strings.TrimSpace(r.FormValue("linkedin_url"))
	githubURL := strings.TrimSpace(r.FormValue("github_url"))

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"email":        email,
		"name":         name,
		"role":         role,
		"avatar":       avatar,
		"bio":          bio,
		"website_url":  websiteURL,
		"linkedin_url": linkedinURL,
		"github_url":   githubURL,
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
		if len(password) < MinPasswordLength {
			validationErrors["password"] = fmt.Sprintf("Password must be at least %d characters", MinPasswordLength)
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

	// Profile field validation
	validateProfileFields(avatar, bio, websiteURL, linkedinURL, githubURL, validationErrors)

	// Business rule: Cannot demote yourself from admin if you're the last admin
	if currentUser.ID == id && editUser.Role == model.RoleAdmin && role != model.RoleAdmin {
		adminCount, err := h.queries.CountUsersByRole(r.Context(), model.RoleAdmin)
		if err != nil {
			slog.Error("failed to count admins", "error", err)
			validationErrors["role"] = "Error checking admin count"
		} else if adminCount <= 1 {
			validationErrors["role"] = "Cannot demote the last admin"
		}
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		data := adminviews.UserFormData{
			User: &adminviews.UserItem{
				ID:          editUser.ID,
				Name:        editUser.Name,
				Email:       editUser.Email,
				Role:        editUser.Role,
				Avatar:      editUser.Avatar,
				Bio:         editUser.Bio,
				WebsiteURL:  editUser.WebsiteUrl,
				LinkedInURL: editUser.LinkedinUrl,
				GitHubURL:   editUser.GithubUrl,
			},
			Roles:      model.ValidRoles,
			Errors:     validationErrors,
			FormValues: formValues,
			IsEdit:     true,
		}

		pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "users.edit"), userEditBreadcrumbs(lang, editUser.Name, id))
		renderTempl(w, r, adminviews.UserFormPage(pc, data))
		return
	}

	// Update user
	now := time.Now()
	_, err = h.queries.UpdateUser(r.Context(), store.UpdateUserParams{
		Email:       email,
		Role:        role,
		Name:        name,
		Avatar:      avatar,
		Bio:         bio,
		WebsiteUrl:  websiteURL,
		LinkedinUrl: linkedinURL,
		GithubUrl:   githubURL,
		UpdatedAt:   now,
		ID:          id,
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
	_ = h.eventService.LogUserEvent(r.Context(), model.EventLevelInfo, "User updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"user_id": id})
	flashSuccess(w, r, h.renderer, redirectAdminUsers, "User updated successfully")
}

// Delete handles DELETE /admin/users/{id} - deletes a user.
func (h *UsersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		h.sendDeleteError(w, middleware.DemoModeMessageDetailed(middleware.RestrictionDeleteUser))
		return
	}

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
	if deleteUser.Role == model.RoleAdmin {
		adminCount, err := h.queries.CountUsersByRole(r.Context(), model.RoleAdmin)
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
	_ = h.eventService.LogUserEvent(r.Context(), model.EventLevelInfo, "User deleted", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"user_id": id, "email": deleteUser.Email})

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

// BulkDelete handles POST /admin/users/bulk-delete - deletes multiple users.
func (h *UsersHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	if middleware.IsDemoMode() {
		writeJSONError(w, http.StatusForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionDeleteUser))
		return
	}

	currentUser := middleware.GetUser(r)
	if currentUser == nil {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	ids, err := parseBulkActionIDs(w, r, defaultBulkActionMaxBatch)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	failed := make([]bulkActionFailedItem, 0)
	deleted := 0

	for _, id := range ids {
		if currentUser.ID == id {
			failed = append(failed, bulkActionFailedItem{ID: id, Reason: "Cannot delete your own account"})
			continue
		}

		deleteUser, err := h.queries.GetUserByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				failed = append(failed, bulkActionFailedItem{ID: id, Reason: "User not found"})
				continue
			}
			slog.Error("failed to get user for bulk delete", "error", err, "user_id", id)
			failed = append(failed, bulkActionFailedItem{ID: id, Reason: "Error loading user"})
			continue
		}

		if deleteUser.Role == model.RoleAdmin {
			adminCount, err := h.queries.CountUsersByRole(r.Context(), model.RoleAdmin)
			if err != nil {
				slog.Error("failed to count admins for bulk delete", "error", err, "user_id", id)
				failed = append(failed, bulkActionFailedItem{ID: id, Reason: "Error checking admin count"})
				continue
			}
			if adminCount <= 1 {
				failed = append(failed, bulkActionFailedItem{ID: id, Reason: "Cannot delete the last admin"})
				continue
			}
		}

		if err := h.queries.DeleteUser(r.Context(), id); err != nil {
			slog.Error("failed to bulk delete user", "error", err, "user_id", id)
			failed = append(failed, bulkActionFailedItem{ID: id, Reason: "Error deleting user"})
			continue
		}

		slog.Info("user deleted", "user_id", id, "email", deleteUser.Email, "deleted_by", currentUser.ID)
		_ = h.eventService.LogUserEvent(r.Context(), model.EventLevelInfo, "User deleted", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"user_id": id, "email": deleteUser.Email})
		h.dispatchUserEvent(r.Context(), model.EventUserDeleted, deleteUser)
		deleted++
	}

	writeBulkActionSuccess(w, deleted, failed)
}

// sendDeleteError sends an error response for delete operations.
func (h *UsersHandler) sendDeleteError(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast": "`+message+`", "toastType": "error"}`)
	w.WriteHeader(http.StatusBadRequest)
}

// MaxBioLength is the maximum allowed length for a user bio.
const MaxBioLength = 500

// MaxProfileURLLength is the maximum allowed length for profile URL fields.
const MaxProfileURLLength = 255

// validateProfileURL checks that a URL is empty or has an http/https scheme.
// Returns an error message string, or empty string if valid.
func validateProfileURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "Invalid URL format"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "URL must start with http:// or https://"
	}
	return ""
}

// validateDomainURL checks that a URL is empty or uses http/https and matches
// one of the allowed hosts. Returns an error message or empty string if valid.
func validateDomainURL(rawURL string, allowedHosts []string) string {
	if rawURL == "" {
		return ""
	}
	if msg := validateProfileURL(rawURL); msg != "" {
		return msg
	}
	u, _ := url.Parse(rawURL) // already validated above
	host := strings.ToLower(u.Hostname())
	for _, h := range allowedHosts {
		if host == h {
			return ""
		}
	}
	return fmt.Sprintf("URL must be on %s", allowedHosts[0])
}

// validateProfileFields validates avatar, bio, and social URL fields.
// It writes any errors into the provided validationErrors map.
func validateProfileFields(avatar, bio, websiteURL, linkedinURL, githubURL string, validationErrors map[string]string) {
	// Length checks
	if len(avatar) > MaxProfileURLLength {
		validationErrors["avatar"] = fmt.Sprintf("Avatar URL must be at most %d characters", MaxProfileURLLength)
	}
	if len(bio) > MaxBioLength {
		validationErrors["bio"] = fmt.Sprintf("Bio must be at most %d characters", MaxBioLength)
	}
	if len(websiteURL) > MaxProfileURLLength {
		validationErrors["website_url"] = fmt.Sprintf("Website URL must be at most %d characters", MaxProfileURLLength)
	}
	if len(linkedinURL) > MaxProfileURLLength {
		validationErrors["linkedin_url"] = fmt.Sprintf("LinkedIn URL must be at most %d characters", MaxProfileURLLength)
	}
	if len(githubURL) > MaxProfileURLLength {
		validationErrors["github_url"] = fmt.Sprintf("GitHub URL must be at most %d characters", MaxProfileURLLength)
	}

	// URL scheme validation (only if no length error already set)
	if validationErrors["avatar"] == "" {
		if msg := validateProfileURL(avatar); msg != "" {
			validationErrors["avatar"] = msg
		}
	}
	if validationErrors["website_url"] == "" {
		if msg := validateProfileURL(websiteURL); msg != "" {
			validationErrors["website_url"] = msg
		}
	}
	if validationErrors["linkedin_url"] == "" {
		if msg := validateDomainURL(linkedinURL, []string{"linkedin.com", "www.linkedin.com"}); msg != "" {
			validationErrors["linkedin_url"] = msg
		}
	}
	if validationErrors["github_url"] == "" {
		if msg := validateDomainURL(githubURL, []string{"github.com", "www.github.com"}); msg != "" {
			validationErrors["github_url"] = msg
		}
	}
}

// isValidRole checks if a role is valid.
func isValidRole(role string) bool {
	for _, r := range model.ValidRoles {
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
