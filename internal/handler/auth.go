package handler

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/auth"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
)

// SessionKeyUserID is the session key for storing the authenticated user ID.
const SessionKeyUserID = "user_id"

// AuthHandler handles authentication routes.
type AuthHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *AuthHandler {
	return &AuthHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// LoginForm renders the login page.
func (h *AuthHandler) LoginForm(w http.ResponseWriter, r *http.Request) {
	// If already logged in, just show login page (let user decide where to go)
	if err := h.renderer.Render(w, r, "auth/login", render.TemplateData{
		Title: "Login",
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Login handles the login form submission.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	// Validate input
	if email == "" || password == "" {
		h.renderer.SetFlash(r, "Email and password are required", "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Find user by email
	user, err := h.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		if err == sql.ErrNoRows {
			slog.Debug("login attempt for non-existent user", "email", email)
		} else {
			slog.Error("database error during login", "error", err)
		}
		h.renderer.SetFlash(r, "Invalid email or password", "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Check password
	valid, err := auth.CheckPassword(password, user.PasswordHash)
	if err != nil {
		slog.Error("password check error", "error", err)
		h.renderer.SetFlash(r, "Invalid email or password", "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if !valid {
		slog.Debug("invalid password attempt", "email", email)
		h.renderer.SetFlash(r, "Invalid email or password", "error")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
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

	h.renderer.SetFlash(r, "Welcome back, "+user.Name+"!", "success")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// Logout handles user logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Get user ID for logging before destroying session
	userID := h.sessionManager.GetInt64(r.Context(), SessionKeyUserID)

	// Destroy the session
	if err := h.sessionManager.Destroy(r.Context()); err != nil {
		slog.Error("session destroy error", "error", err)
	}

	slog.Info("user logged out", "user_id", userID)

	h.renderer.SetFlash(r, "You have been logged out", "info")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
