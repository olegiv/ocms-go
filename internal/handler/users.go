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
	"path"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/mfa"
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
	mfa            *mfa.Service
	// loginProtection counts wrong current passwords toward the login
	// lockout; nil leaves them uncounted.
	loginProtection *middleware.LoginProtection
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

// SetLoginProtection makes wrong current passwords, given to confirm a user
// change that could hand an account over, count toward the login lockout.
func (h *UsersHandler) SetLoginProtection(lp *middleware.LoginProtection) {
	h.loginProtection = lp
}

// SetMFAService sets the two-factor service the edit page reads the user's
// status from and the reset action uses. Without it neither is shown.
func (h *UsersHandler) SetMFAService(s *mfa.Service) {
	h.mfa = s
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
	telegramURL := strings.TrimSpace(r.FormValue("telegram_url"))

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
		"telegram_url": telegramURL,
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
	validateProfileFields(avatar, bio, websiteURL, linkedinURL, githubURL, telegramURL, validationErrors)

	// A new administrator needs the acting administrator's password, so a
	// stolen session cannot mint one it controls.
	if role == model.RoleAdmin {
		actor := middleware.GetUser(r)
		if actor == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if msg := h.checkActorPassword(r, *actor, r.FormValue("current_password")); msg != "" {
			validationErrors["current_password"] = msg
		}
	}

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
		TelegramUrl:  telegramURL,
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
			TelegramURL: editUser.TelegramUrl,
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
			"telegram_url": editUser.TelegramUrl,
		},
		IsEdit: true,
	}
	if data.MFA, ok = h.userMFAStatus(w, r, editUser.ID, lang); !ok {
		return
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "users.edit"), userEditBreadcrumbs(lang, editUser.Name, editUser.ID))
	renderTempl(w, r, adminviews.UserFormPage(pc, data))
}

// actorPasswordNeeded reports whether a user edit needs the acting
// administrator's current password: one that could hand an account to
// whoever holds the session, or take an administrator's away. That is a new
// password for any user, a new email for any user (the email keys the login
// lockout, so a new one would give a stolen session fresh password guesses
// against that account), and giving or taking away the admin role: given,
// it would let a stolen session mint an administrator it controls and use
// that one to take over the others; taken, it would lock the real
// administrators out.
func actorPasswordNeeded(target store.User, email, password, role string) bool {
	switch {
	case password != "", email != target.Email:
		return true
	default:
		return (role == model.RoleAdmin) != (target.Role == model.RoleAdmin)
	}
}

// checkActorPassword verifies the current password the acting administrator
// gives to confirm a sensitive user change. It returns a validation message,
// or "" when it matches. Wrong passwords count toward the login lockout like
// failed sign-ins.
func (h *UsersHandler) checkActorPassword(r *http.Request, actor store.User, given string) string {
	c := confirmPassword(r, h.loginProtection, h.eventService, actor, given, func() {
		h.logActorRefusal(r, actor, "User change refused: wrong current password")
	})
	switch c.Outcome {
	case passwordOK:
		return ""
	case passwordMissing:
		h.logActorRefusal(r, actor, "User change refused: no current password")
		return "Enter your current password to make this change"
	case passwordUnverified:
		slog.Error("password check error", "error", c.Err, "user_id", actor.ID)
		return "Error checking your current password"
	case passwordLocked:
		h.logActorRefusal(r, actor, "User change refused: account locked")
		return "Too many failed attempts; try again later"
	}
	if c.LockedFor > 0 {
		// This wrong password locked the account.
		return "Too many failed attempts; try again later"
	}
	return "Your current password is incorrect"
}

// logActorRefusal logs message, a user change refused because the acting
// administrator's password did not confirm it, as a warning user event.
func (h *UsersHandler) logActorRefusal(r *http.Request, actor store.User, message string) {
	_ = h.eventService.LogUserEvent(r.Context(), model.EventLevelWarning, message, &actor.ID, middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"user_id": actor.ID})
}

// userMFAStatus is the edit page's view of userID's second factor: nil
// without a two-factor service. It answers a lookup failure itself and
// reports false.
func (h *UsersHandler) userMFAStatus(w http.ResponseWriter, r *http.Request, userID int64, lang string) (*adminviews.UserMFAStatus, bool) {
	if h.mfa == nil {
		return nil, true
	}
	status, err := h.mfa.Status(r.Context(), userID)
	if err != nil {
		logAndInternalError(w, "failed to load two-factor status", "error", err, "user_id", userID)
		return nil, false
	}
	view := &adminviews.UserMFAStatus{Enrolled: status.Enrolled, IsSelf: userID == middleware.GetUserID(r)}
	if status.Enrolled {
		view.EnrolledAt = h.renderer.FormatDateTimeLocale(status.EnrolledAt, lang)
	}
	return view, true
}

// ResetMFA handles POST /admin/users/{id}/mfa/reset - removes another user's
// second factor and signs them out everywhere, for a user who lost their
// authenticator and recovery codes. Where a second factor is required, they
// set up a new one at their next sign-in. It needs the acting
// administrator's current password, like the other user changes that could
// hand an account over. Administrators change their own second factor on
// the account security page, which asks for a current code.
func (h *UsersHandler) ResetMFA(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionTwoFactor, redirectAdminUsers) {
		return
	}
	lang := h.renderer.GetAdminLang(r)
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminUsers, "Invalid user ID")
		return
	}
	target, ok := h.requireUserWithRedirect(w, r, id)
	if !ok {
		return
	}
	back := fmt.Sprintf(redirectAdminUsersID, target.ID)
	if h.mfa == nil {
		flashError(w, r, h.renderer, back, i18n.T(lang, "mfa.not_configured"))
		return
	}
	if target.ID == middleware.GetUserID(r) {
		flashError(w, r, h.renderer, back, i18n.T(lang, "users.mfa_reset_self"))
		return
	}
	enrolled, err := h.mfa.Enrolled(r.Context(), target.ID)
	if err != nil {
		logAndInternalError(w, "failed to load two-factor status", "error", err, "user_id", target.ID)
		return
	}
	if !enrolled {
		flashError(w, r, h.renderer, back, i18n.T(lang, "users.mfa_not_enabled"))
		return
	}
	actor := middleware.GetUser(r)
	if actor == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if msg := h.checkActorPassword(r, *actor, r.FormValue("current_password")); msg != "" {
		flashError(w, r, h.renderer, back, msg)
		return
	}
	if _, err := h.mfa.Disable(r.Context(), target.ID); err != nil {
		logAndInternalError(w, "failed to reset two-factor authentication", "error", err, "user_id", target.ID)
		return
	}
	slog.Info("two-factor authentication reset", "user_id", target.ID, "reset_by", middleware.GetUserID(r))
	_ = h.eventService.LogUserEvent(r.Context(), model.EventLevelWarning, "Two-factor authentication reset by an administrator", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"user_id": target.ID, "email": target.Email})
	flashAndRedirect(w, r, h.renderer, back, i18n.T(lang, "users.mfa_reset_done"), "success")
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
	telegramURL := strings.TrimSpace(r.FormValue("telegram_url"))

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
		"telegram_url": telegramURL,
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
	if actorPasswordNeeded(editUser, email, password, role) {
		if msg := h.checkActorPassword(r, *currentUser, r.FormValue("current_password")); msg != "" {
			validationErrors["current_password"] = msg
		}
	}

	// Role validation
	if role == "" {
		validationErrors["role"] = "Role is required"
	} else if !isValidRole(role) {
		validationErrors["role"] = "Invalid role"
	}

	// Profile field validation
	validateProfileFields(avatar, bio, websiteURL, linkedinURL, githubURL, telegramURL, validationErrors)

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
				TelegramURL: editUser.TelegramUrl,
			},
			Roles:      model.ValidRoles,
			Errors:     validationErrors,
			FormValues: formValues,
			IsEdit:     true,
		}
		if data.MFA, ok = h.userMFAStatus(w, r, editUser.ID, lang); !ok {
			return
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
		TelegramUrl: telegramURL,
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

		// If the actor changed their own password, re-snapshot the
		// bumped session_version into the current session so the auth
		// middleware doesn't immediately log them out by their own
		// action. Other devices/browsers for the same user will see the
		// stale version and be invalidated on their next request, which
		// is the intended behavior.
		if id == currentUser.ID {
			updated, err := h.queries.GetUserByID(r.Context(), id)
			if err != nil {
				slog.Error("failed to reload user after self password change", "error", err, "user_id", id)
			} else if err := h.sessionManager.RenewToken(r.Context()); err != nil {
				slog.Error("failed to renew session token after self password change", "error", err, "user_id", id)
			} else {
				h.sessionManager.Put(r.Context(), middleware.SessionKeyUserSessionVersion, updated.SessionVersion)
			}
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

// validateAvatarURL checks that an avatar value is empty, a relative path,
// or an http/https URL. Relative paths like /uploads/avatar.jpg are allowed.
func validateAvatarURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// Allow site-relative paths, but sanitize against traversal
	if strings.HasPrefix(rawURL, "/") {
		cleaned := path.Clean(rawURL)
		if strings.Contains(cleaned, "..") {
			return "Invalid path"
		}
		return ""
	}
	return validateProfileURL(rawURL)
}

// validateDomainURL checks that a URL is empty or uses https and matches one
// of the allowed hosts. Rejects embedded credentials and path traversal.
// Returns an error message or empty string if valid.
func validateDomainURL(rawURL string, allowedHosts []string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "Invalid URL format"
	}
	if u.Scheme != "https" {
		return "URL must start with https://"
	}
	if u.User != nil {
		return "URL must not contain credentials"
	}
	if strings.Contains(u.Path, "..") {
		return "URL must not contain path traversal"
	}
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
func validateProfileFields(avatar, bio, websiteURL, linkedinURL, githubURL, telegramURL string, validationErrors map[string]string) {
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
	if len(telegramURL) > MaxProfileURLLength {
		validationErrors["telegram_url"] = fmt.Sprintf("Telegram URL must be at most %d characters", MaxProfileURLLength)
	}

	// URL scheme validation (only if no length error already set)
	if validationErrors["avatar"] == "" {
		if msg := validateAvatarURL(avatar); msg != "" {
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
	if validationErrors["telegram_url"] == "" {
		if msg := validateDomainURL(telegramURL, []string{"t.me", "telegram.me"}); msg != "" {
			validationErrors["telegram_url"] = msg
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
