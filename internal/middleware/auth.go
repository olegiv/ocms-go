// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package middleware provides HTTP middleware for authentication,
// authorization, and request context handling.
package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// ContextKey is a type for context keys to avoid collisions.
type ContextKey string

// Context keys for user data.
const (
	ContextKeyUser        ContextKey = "user"
	ContextKeySiteName    ContextKey = "site_name"
	ContextKeyRequestPath ContextKey = "request_path"
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

// OptionalLoadUser creates middleware that optionally loads the current user into context.
// Unlike LoadUser, this does NOT redirect to login if the user is not found.
// Use this for frontend routes where authentication is optional but user context is useful.
func OptionalLoadUser(sm *scs.SessionManager, db *sql.DB) func(http.Handler) http.Handler {
	queries := store.New(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := sm.GetInt64(r.Context(), SessionKeyUserID)
			if userID == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Try to load user from database
			user, err := queries.GetUserByID(r.Context(), userID)
			if err != nil {
				// User not found or error - just continue without user context
				next.ServeHTTP(w, r)
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

// GetUserID returns the current user's ID from context, or 0 if not found.
// Safe to use in logging where a zero-value is acceptable.
func GetUserID(r *http.Request) int64 {
	if user := GetUser(r); user != nil {
		return user.ID
	}
	return 0
}

// GetUserIDPtr returns a pointer to the current user's ID from context, or nil if not found.
// Useful for optional user ID parameters in event logging.
func GetUserIDPtr(r *http.Request) *int64 {
	if user := GetUser(r); user != nil {
		id := user.ID
		return &id
	}
	return nil
}

// GetUserEmail returns the current user's email from context, or empty string if not found.
func GetUserEmail(r *http.Request) string {
	if user := GetUser(r); user != nil {
		return user.Email
	}
	return ""
}

// LoadSiteConfig creates middleware that loads site configuration (like site_name) into context.
// If cacheManager is provided, it will be used for faster config lookups.
// If both cacheManager and db are nil, the default "oCMS" will be used.
func LoadSiteConfig(db *sql.DB, cacheManager *cache.Manager) func(http.Handler) http.Handler {
	var queries *store.Queries
	if db != nil {
		queries = store.New(db)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to get site_name from config (use cache if available)
			siteName := "oCMS" // default fallback

			if cacheManager != nil {
				if name, err := cacheManager.GetConfig(r.Context(), "site_name"); err == nil && name != "" {
					siteName = name
				}
			} else if queries != nil {
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

// RequestPath creates middleware that stores the request path in the context.
// This is used by the logging handler to include the URL in error logs.
func RequestPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ContextKeyRequestPath, r.URL.Path)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestPath retrieves the request path from the context.
func GetRequestPath(ctx context.Context) string {
	path, ok := ctx.Value(ContextKeyRequestPath).(string)
	if !ok {
		return ""
	}
	return path
}

// User roles - must match handler.Role* constants.
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
)

// roleLevel returns a numeric level for role hierarchy.
// Higher level = more permissions. Public users have level 0 (no admin access).
func roleLevel(role string) int {
	switch role {
	case RoleAdmin:
		return 2
	case RoleEditor:
		return 1
	default:
		// Public and unknown roles have no admin access
		return 0
	}
}

// RequireRole creates middleware that requires a minimum user role.
// Roles are hierarchical: admin > editor. Public users have no admin access.
// For example, RequireRole("editor") allows both admin and editor users.
func RequireRole(minRole string) func(http.Handler) http.Handler {
	return RequireRoleWithEventLog(minRole, nil)
}

// RequireRoleWithEventLog creates middleware that requires a minimum user role and logs to event log.
// Roles are hierarchical: admin > editor. Public users have no admin access.
// If eventService is provided, 403 errors will be logged to the event log (visible in admin panel).
func RequireRoleWithEventLog(minRole string, eventService *service.EventService) func(http.Handler) http.Handler {
	minLevel := roleLevel(minRole)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetUser(r)
			if user == nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			// Check role hierarchy
			userLevel := roleLevel(user.Role)
			if userLevel < minLevel {
				// Log 403 for security monitoring (application logs)
				slog.Warn("access denied",
					"status", http.StatusForbidden,
					"method", r.Method,
					"path", r.URL.Path,
					"user_id", user.ID,
					"user_role", user.Role,
					"required_role", minRole,
					"remote_addr", r.RemoteAddr,
				)

				// Log 403 to event log (visible in admin panel)
				if eventService != nil {
					userID := user.ID
					metadata := map[string]any{
						"method":        r.Method,
						"status":        http.StatusForbidden,
						"user_role":     user.Role,
						"required_role": minRole,
					}
					_ = eventService.LogAuthEvent(r.Context(), "warning", "Access denied: insufficient permissions", &userID, r.RemoteAddr, r.URL.Path, metadata)
				}

				// Return 403 Forbidden for insufficient role
				http.Error(w, "Forbidden: insufficient permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin creates middleware that requires admin role.
// Shorthand for RequireRole(RoleAdmin).
func RequireAdmin() func(http.Handler) http.Handler {
	return RequireRole(RoleAdmin)
}

// RequireAdminWithEventLog creates middleware that requires admin role with event logging.
func RequireAdminWithEventLog(eventService *service.EventService) func(http.Handler) http.Handler {
	return RequireRoleWithEventLog(RoleAdmin, eventService)
}

// RequireEditor creates middleware that requires at least editor role.
// Allows both admin and editor users.
func RequireEditor() func(http.Handler) http.Handler {
	return RequireRole(RoleEditor)
}

// RequireEditorWithEventLog creates middleware that requires at least editor role with event logging.
func RequireEditorWithEventLog(eventService *service.EventService) func(http.Handler) http.Handler {
	return RequireRoleWithEventLog(RoleEditor, eventService)
}

// globalSessionManager is set by SetSessionManager and used by GetAdminLang.
var globalSessionManager *scs.SessionManager

// SetSessionManager sets the global session manager for admin language retrieval.
// This should be called during application initialization.
func SetSessionManager(sm *scs.SessionManager) {
	globalSessionManager = sm
}

// GetAdminLang retrieves the admin UI language preference from the session.
// Falls back to Accept-Language header, then database default language.
func GetAdminLang(r *http.Request) string {
	// First, check session for saved preference
	if globalSessionManager != nil {
		if lang := globalSessionManager.GetString(r.Context(), SessionKeyAdminLang); lang != "" && i18n.IsSupported(lang) {
			return lang
		}
	}
	// Fall back to browser's Accept-Language header
	if acceptLang := r.Header.Get("Accept-Language"); acceptLang != "" {
		if lang := i18n.MatchLanguage(acceptLang); lang != "" {
			return lang
		}
	}
	return i18n.GetDefaultLanguage()
}
