// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/auth"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/modules/hcaptcha"
)

// SessionKeyUserID is the session key for storing the authenticated user ID.
const SessionKeyUserID = "user_id"

// AuthHandler handles authentication routes.
type AuthHandler struct {
	queries         *store.Queries
	renderer        *render.Renderer
	sessionManager  *scs.SessionManager
	eventService    *service.EventService
	loginProtection *middleware.LoginProtection
	hookRegistry    *module.HookRegistry
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, lp *middleware.LoginProtection, hr *module.HookRegistry) *AuthHandler {
	return &AuthHandler{
		queries:         store.New(db),
		renderer:        renderer,
		sessionManager:  sm,
		eventService:    service.NewEventService(db),
		loginProtection: lp,
		hookRegistry:    hr,
	}
}

// LoginForm renders the login page.
// Redirects already-authenticated users: admin/editor → dashboard, others → homepage.
func (h *AuthHandler) LoginForm(w http.ResponseWriter, r *http.Request) {
	// Redirect already-authenticated users
	if userID := h.sessionManager.GetInt64(r.Context(), SessionKeyUserID); userID > 0 {
		user, err := h.queries.GetUserByID(r.Context(), userID)
		if err == nil {
			if user.Role == RoleAdmin || user.Role == RoleEditor {
				http.Redirect(w, r, redirectAdmin, http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, RouteRoot, http.StatusSeeOther)
			return
		}
	}

	lang := middleware.GetAdminLang(r)

	// Build login data for templ view
	data := adminviews.LoginData{
		Title:     i18n.T(lang, "auth.login"),
		AdminLang: lang,
	}

	// Get language options
	for _, opt := range h.renderer.AdminLangOptions() {
		data.LangOptions = append(data.LangOptions, adminviews.LangOption{
			Code: opt.Code,
			Name: opt.Name,
		})
	}

	// Get hCaptcha state
	data.HcaptchaEnabled = h.renderer.HcaptchaEnabled()
	if data.HcaptchaEnabled {
		data.HcaptchaWidget = h.renderer.HcaptchaWidgetHTML()
	}

	// Get flash message from session
	if flash := h.sessionManager.PopString(r.Context(), "flash"); flash != "" {
		data.Flash = flash
		data.FlashType = h.sessionManager.PopString(r.Context(), "flash_type")
		if data.FlashType == "" {
			data.FlashType = "info"
		}
	}

	renderTempl(w, r, adminviews.LoginPage(data))
}

// Login handles the login form submission.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.invalid_form_data"))
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	// Validate input
	if email == "" || password == "" {
		flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.email_password_required"))
		return
	}

	// Verify hCaptcha if hook registry is available
	if h.hookRegistry != nil && h.hookRegistry.HasHandlers(hcaptcha.HookAuthBeforeLogin) {
		verifyReq := &hcaptcha.VerifyRequest{
			Response: hcaptcha.GetResponseFromForm(r),
			RemoteIP: hcaptcha.GetRemoteIP(r),
		}

		result, err := h.hookRegistry.Call(r.Context(), hcaptcha.HookAuthBeforeLogin, verifyReq)
		if err != nil {
			slog.Error("captcha hook error", "error", err)
			flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "hcaptcha.error_verification"))
			return
		}

		if req, ok := result.(*hcaptcha.VerifyRequest); ok && !req.Verified {
			errorMsg := i18n.T(lang, req.ErrorCode)
			if errorMsg == req.ErrorCode {
				errorMsg = req.Error
			}
			flashError(w, r, h.renderer, redirectLogin, errorMsg)
			return
		}
	}

	// Get client IP for event logging
	clientIP := hcaptcha.GetRemoteIP(r)

	// Check if account is locked
	if h.loginProtection != nil {
		if locked, remaining := h.loginProtection.IsAccountLocked(email); locked {
			_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Login attempt on locked account", nil, clientIP, middleware.GetRequestURL(r), map[string]any{"email": email})
			flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.account_locked", formatDuration(remaining)))
			return
		}
	}

	// Find user by email
	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Debug("login attempt for non-existent user", "email", email)
			_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Login failed: user not found", nil, clientIP, middleware.GetRequestURL(r), map[string]any{"email": email})
		} else {
			slog.Error("database error during login", "error", err)
		}
		// Record failed attempt even for non-existent users to prevent enumeration
		if h.loginProtection != nil {
			if locked, lockDuration := h.loginProtection.RecordFailedAttempt(email); locked {
				flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.too_many_attempts", formatDuration(lockDuration)))
				return
			}
			remaining := h.loginProtection.GetRemainingAttempts(email)
			if remaining <= 3 && remaining > 0 {
				flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.attempts_remaining", remaining))
				return
			}
		}
		flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.invalid_credentials"))
		return
	}

	// Check password
	valid, err := auth.CheckPassword(password, user.PasswordHash)
	if err != nil {
		slog.Error("password check error", "error", err)
		flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.invalid_credentials"))
		return
	}

	if !valid {
		slog.Debug("invalid password attempt", "email", email)
		_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Login failed: invalid password", &user.ID, clientIP, middleware.GetRequestURL(r), map[string]any{"email": email})
		// Record failed attempt
		if h.loginProtection != nil {
			if locked, lockDuration := h.loginProtection.RecordFailedAttempt(email); locked {
				_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Account locked due to failed attempts", &user.ID, clientIP, middleware.GetRequestURL(r), map[string]any{"email": email, "duration": lockDuration.String()})
				flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.too_many_attempts", formatDuration(lockDuration)))
				return
			}
			remaining := h.loginProtection.GetRemainingAttempts(email)
			if remaining <= 3 && remaining > 0 {
				flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.attempts_remaining", remaining))
				return
			}
		}
		flashError(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.invalid_credentials"))
		return
	}

	// Clear failed attempts on successful login
	if h.loginProtection != nil {
		h.loginProtection.RecordSuccessfulLogin(email)
	}

	// Re-hash password if it uses old/expensive parameters (e.g., 64MB → 19MB)
	if auth.NeedsRehash(user.PasswordHash) {
		if newHash, err := auth.HashPassword(password); err == nil {
			if err := h.queries.UpdateUserPassword(r.Context(), store.UpdateUserPasswordParams{
				PasswordHash: newHash,
				UpdatedAt:    time.Now(),
				ID:           user.ID,
			}); err != nil {
				slog.Error("failed to re-hash password", "error", err, "user_id", user.ID)
			} else {
				slog.Info("password re-hashed with updated parameters", "user_id", user.ID)
			}
		}
	}

	// Update last login timestamp
	if err := h.queries.UpdateUserLastLogin(r.Context(), store.UpdateUserLastLoginParams{
		LastLoginAt: sql.NullTime{Time: time.Now(), Valid: true},
		ID:          user.ID,
	}); err != nil {
		slog.Error("failed to update last login time", "error", err, "user_id", user.ID)
		// Don't block login on this error
	}

	// Regenerate session ID to prevent session fixation
	if err := h.sessionManager.RenewToken(r.Context()); err != nil {
		logAndInternalError(w, "session renewal error", "error", err)
		return
	}

	// Store user ID in session
	h.sessionManager.Put(r.Context(), SessionKeyUserID, user.ID)

	slog.Info("user logged in", "user_id", user.ID, "email", user.Email)
	_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelInfo, "User logged in", &user.ID, clientIP, middleware.GetRequestURL(r), map[string]any{"email": user.Email})

	h.renderer.SetFlash(r, i18n.T(lang, "auth.welcome_back", user.Name), "success")

	// Redirect based on user role
	if user.Role == "admin" {
		http.Redirect(w, r, redirectAdmin, http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// Logout handles user logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Get user ID for logging before destroying session
	userID := h.sessionManager.GetInt64(r.Context(), SessionKeyUserID)

	// Log the event before destroying session
	if userID > 0 {
		clientIP := hcaptcha.GetRemoteIP(r)
		_ = h.eventService.LogAuthEvent(r.Context(), model.EventLevelInfo, "User logged out", &userID, clientIP, middleware.GetRequestURL(r), nil)
	}

	// Destroy the session
	if err := h.sessionManager.Destroy(r.Context()); err != nil {
		slog.Error("session destroy error", "error", err)
	}

	slog.Info("user logged out", "user_id", userID)

	lang := middleware.GetAdminLang(r)
	flashAndRedirect(w, r, h.renderer, redirectLogin, i18n.T(lang, "auth.logged_out"), "info")
}

// SetLanguage changes the UI language preference on the login page.
// POST /language
func (h *AuthHandler) SetLanguage(w http.ResponseWriter, r *http.Request) {
	setLanguagePreference(w, r, h.renderer, redirectLogin)
}

// formatDuration formats a duration into a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	}
	hours := int(d.Hours())
	if hours == 1 {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", hours)
}
