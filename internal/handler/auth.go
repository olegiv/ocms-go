package handler

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/auth"
	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/service"
	"ocms-go/internal/store"
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
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, lp *middleware.LoginProtection) *AuthHandler {
	return &AuthHandler{
		queries:         store.New(db),
		renderer:        renderer,
		sessionManager:  sm,
		eventService:    service.NewEventService(db),
		loginProtection: lp,
	}
}

// LoginForm renders the login page.
func (h *AuthHandler) LoginForm(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)
	// If already logged in, just show login page (let user decide where to go)
	if err := h.renderer.Render(w, r, "auth/login", render.TemplateData{
		Title: i18n.T(lang, "auth.login"),
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Login handles the login form submission.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	lang := middleware.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, i18n.T(lang, "auth.invalid_form_data"), "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	// Validate input
	if email == "" || password == "" {
		h.renderer.SetFlash(r, i18n.T(lang, "auth.email_password_required"), "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Check if account is locked
	if h.loginProtection != nil {
		if locked, remaining := h.loginProtection.IsAccountLocked(email); locked {
			h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Login attempt on locked account", nil, map[string]any{"email": email})
			h.renderer.SetFlash(r, i18n.T(lang, "auth.account_locked", formatDuration(remaining)), "error")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
	}

	// Find user by email
	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		if err == sql.ErrNoRows {
			slog.Debug("login attempt for non-existent user", "email", email)
			h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Login failed: user not found", nil, map[string]any{"email": email})
		} else {
			slog.Error("database error during login", "error", err)
		}
		// Record failed attempt even for non-existent users to prevent enumeration
		if h.loginProtection != nil {
			if locked, lockDuration := h.loginProtection.RecordFailedAttempt(email); locked {
				h.renderer.SetFlash(r, i18n.T(lang, "auth.too_many_attempts", formatDuration(lockDuration)), "error")
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			remaining := h.loginProtection.GetRemainingAttempts(email)
			if remaining <= 3 && remaining > 0 {
				h.renderer.SetFlash(r, i18n.T(lang, "auth.attempts_remaining", remaining), "error")
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}
		h.renderer.SetFlash(r, i18n.T(lang, "auth.invalid_credentials"), "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Check password
	valid, err := auth.CheckPassword(password, user.PasswordHash)
	if err != nil {
		slog.Error("password check error", "error", err)
		h.renderer.SetFlash(r, i18n.T(lang, "auth.invalid_credentials"), "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if !valid {
		slog.Debug("invalid password attempt", "email", email)
		h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Login failed: invalid password", &user.ID, map[string]any{"email": email})
		// Record failed attempt
		if h.loginProtection != nil {
			if locked, lockDuration := h.loginProtection.RecordFailedAttempt(email); locked {
				h.eventService.LogAuthEvent(r.Context(), model.EventLevelWarning, "Account locked due to failed attempts", &user.ID, map[string]any{"email": email, "duration": lockDuration.String()})
				h.renderer.SetFlash(r, i18n.T(lang, "auth.too_many_attempts", formatDuration(lockDuration)), "error")
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			remaining := h.loginProtection.GetRemainingAttempts(email)
			if remaining <= 3 && remaining > 0 {
				h.renderer.SetFlash(r, i18n.T(lang, "auth.attempts_remaining", remaining), "error")
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}
		h.renderer.SetFlash(r, i18n.T(lang, "auth.invalid_credentials"), "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Clear failed attempts on successful login
	if h.loginProtection != nil {
		h.loginProtection.RecordSuccessfulLogin(email)
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
		slog.Error("session renewal error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Store user ID in session
	h.sessionManager.Put(r.Context(), SessionKeyUserID, user.ID)

	slog.Info("user logged in", "user_id", user.ID, "email", user.Email)
	h.eventService.LogAuthEvent(r.Context(), model.EventLevelInfo, "User logged in", &user.ID, map[string]any{"email": user.Email})

	h.renderer.SetFlash(r, "Welcome back, "+user.Name+"!", "success")

	// Redirect based on user role
	if user.Role == "admin" {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
		h.eventService.LogAuthEvent(r.Context(), model.EventLevelInfo, "User logged out", &userID, nil)
	}

	// Destroy the session
	if err := h.sessionManager.Destroy(r.Context()); err != nil {
		slog.Error("session destroy error", "error", err)
	}

	slog.Info("user logged out", "user_id", userID)

	h.renderer.SetFlash(r, "You have been logged out", "info")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
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
