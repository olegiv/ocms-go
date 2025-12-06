// Package middleware provides HTTP middleware for authentication,
// authorization, and request context handling.
package middleware

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/alexedwards/scs/v2"

	"ocms-go/internal/cache"
	"ocms-go/internal/i18n"
	"ocms-go/internal/store"
)

// ContextKey is a type for context keys to avoid collisions.
type ContextKey string

// Context keys for user data.
const (
	ContextKeyUser     ContextKey = "user"
	ContextKeySiteName ContextKey = "site_name"
)

// Session keys for storing user data and preferences.
const (
	SessionKeyUserID    = "user_id"
	SessionKeyAdminLang = "admin_lang"
)

// Auth creates middleware that requires authentication.
// It checks for a valid user session and redirects to login if not authenticated.
func Auth(sm *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if user is authenticated
			userID := sm.GetInt64(r.Context(), SessionKeyUserID)
			if userID == 0 {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// LoadUser creates middleware that loads the current user into the request context.
// This should be used after Auth middleware.
func LoadUser(sm *scs.SessionManager, db *sql.DB) func(http.Handler) http.Handler {
	queries := store.New(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := sm.GetInt64(r.Context(), SessionKeyUserID)
			if userID == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Load user from database
			user, err := queries.GetUserByID(r.Context(), userID)
			if err != nil {
				// User not found or error - clear session and redirect to login
				_ = sm.Destroy(r.Context())
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			// Add user to context
			ctx := context.WithValue(r.Context(), ContextKeyUser, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetUser retrieves the current user from the request context.
// Returns nil if no user is in context.
func GetUser(r *http.Request) *store.User {
	user, ok := r.Context().Value(ContextKeyUser).(store.User)
	if !ok {
		return nil
	}
	return &user
}

// RequireAdmin creates middleware that requires admin role.
// This should be used after LoadUser middleware.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r)
		if user == nil || user.Role != "admin" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoadSiteConfig creates middleware that loads site configuration (like site_name) into context.
// If cacheManager is provided, it will be used for faster config lookups.
func LoadSiteConfig(db *sql.DB, cacheManager *cache.Manager) func(http.Handler) http.Handler {
	queries := store.New(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to get site_name from config (use cache if available)
			siteName := "oCMS" // default fallback

			if cacheManager != nil {
				if name, err := cacheManager.GetConfig(r.Context(), "site_name"); err == nil && name != "" {
					siteName = name
				}
			} else {
				cfg, err := queries.GetConfig(r.Context(), "site_name")
				if err == nil && cfg.Value != "" {
					siteName = cfg.Value
				}
			}

			// Add site name to context
			ctx := context.WithValue(r.Context(), ContextKeySiteName, siteName)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetSiteName retrieves the site name from the request context.
// Returns "oCMS" as default if not found.
func GetSiteName(r *http.Request) string {
	siteName, ok := r.Context().Value(ContextKeySiteName).(string)
	if !ok || siteName == "" {
		return "oCMS"
	}
	return siteName
}

// globalSessionManager is set by SetSessionManager and used by GetAdminLang.
var globalSessionManager *scs.SessionManager

// SetSessionManager sets the global session manager for admin language retrieval.
// This should be called during application initialization.
func SetSessionManager(sm *scs.SessionManager) {
	globalSessionManager = sm
}

// GetAdminLang retrieves the admin UI language preference from the session.
// Returns "en" as default if not found.
func GetAdminLang(r *http.Request) string {
	if globalSessionManager != nil {
		if lang := globalSessionManager.GetString(r.Context(), SessionKeyAdminLang); lang != "" && i18n.IsSupported(lang) {
			return lang
		}
	}
	return "en"
}
