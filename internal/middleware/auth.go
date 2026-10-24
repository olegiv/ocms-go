// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package middleware provides HTTP middleware for authentication,
// authorization, and request context handling.
package middleware

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// ContextKey is a type for context keys to avoid collisions.
type ContextKey string

// Context keys for user data.
const (
	ContextKeyUser          ContextKey = "user"
	ContextKeySiteName      ContextKey = "site_name"
	ContextKeyStatusPageURL ContextKey = "status_page_url"
	ContextKeyRequestPath   ContextKey = "request_path"
	ContextKeyCSPNonce      ContextKey = "csp_nonce"
)

// Session keys for storing user data and preferences.
const (
	SessionKeyUserID             = "user_id"
	SessionKeyUserSessionVersion = "user_session_version"
	SessionKeyAdminLang          = "admin_lang"
	// SessionKeyMFAVerified is true on a session whose sign-in passed a
	// second factor.
	SessionKeyMFAVerified = "mfa_verified"
)

// mfaRequired mirrors OCMS_REQUIRE_MFA for isSessionStale.
var mfaRequired atomic.Bool

// SetMFARequired tells the session checks whether every sign-in must pass a
// second factor. When it must, a session without SessionKeyMFAVerified is
// stale, so turning the requirement on also signs out the sessions that
// started before it. Call it once at startup.
func SetMFARequired(required bool) {
	mfaRequired.Store(required)
}

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

			if isSessionStale(r.Context(), sm, user) {
				slog.Info("session invalidated by a credential change or the two-factor requirement", "user_id", user.ID)
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

			if isSessionStale(r.Context(), sm, user) {
				_ = sm.Destroy(r.Context())
				next.ServeHTTP(w, r)
				return
			}

			// Add user to context
			ctx := context.WithValue(r.Context(), ContextKeyUser, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isSessionStale reports whether the session's recorded user_session_version
// is older than the user's current session_version (which is bumped on every
// real credential change, the second factor included). Sessions issued before
// the session_version column existed have no recorded value (zero), which
// matches the column's default for never-rotated users — so they remain valid
// until the first password change for that user. While a second factor is
// required, a session whose sign-in did not pass one is stale as well.
func isSessionStale(ctx context.Context, sm *scs.SessionManager, user store.User) bool {
	sessionVersion := sm.GetInt64(ctx, SessionKeyUserSessionVersion)
	if sessionVersion < user.SessionVersion {
		return true
	}
	return mfaRequired.Load() && !sm.GetBool(ctx, SessionKeyMFAVerified)
}

// SessionUser returns the user this session is signed in as, after the
// checks LoadUser applies on every protected route: the user still exists
// and the session is not stale (an older session_version, or no second
// factor while one is required). It reports false for an anonymous, stale or
// orphaned session and changes nothing. Code outside this package that asks
// "is this request signed in" uses it rather than reading SessionKeyUserID
// (TestSessionUserReadsGoThroughMiddleware).
func SessionUser(ctx context.Context, sm *scs.SessionManager, queries *store.Queries) (store.User, bool) {
	userID := sm.GetInt64(ctx, SessionKeyUserID)
	if userID <= 0 {
		return store.User{}, false
	}
	user, err := queries.GetUserByID(ctx, userID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Error("failed to load the session user", "error", err, "user_id", userID)
		}
		return store.User{}, false
	}
	if isSessionStale(ctx, sm, user) {
		return store.User{}, false
	}
	return user, true
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
		return new(user.ID)
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

// LoadSiteConfig creates middleware that loads site configuration (site_name and
// status_page_url) into context.
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
			var statusPageURL string

			if cacheManager != nil {
				if name, err := cacheManager.GetConfig(r.Context(), "site_name"); err == nil && name != "" {
					siteName = name
				}
				if statusURL, err := cacheManager.GetConfig(r.Context(), model.ConfigKeyStatusPageURL); err == nil {
					statusPageURL = statusURL
				}
			} else if queries != nil {
				cfg, err := queries.GetConfig(r.Context(), "site_name")
				if err == nil && cfg.Value != "" {
					siteName = cfg.Value
				}
				if statusCfg, statusErr := queries.GetConfig(r.Context(), model.ConfigKeyStatusPageURL); statusErr == nil {
					statusPageURL = statusCfg.Value
				}
			}

			// Add site name and status page URL to context
			ctx := context.WithValue(r.Context(), ContextKeySiteName, siteName)
			ctx = context.WithValue(ctx, ContextKeyStatusPageURL, statusPageURL)
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

// GetStatusPageURL retrieves the public status page URL from the request context.
// Returns an empty string when none is configured.
func GetStatusPageURL(r *http.Request) string {
	statusPageURL, _ := r.Context().Value(ContextKeyStatusPageURL).(string)
	return statusPageURL
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

// User roles - canonical definitions in model package.

// roleLevel returns a numeric level for role hierarchy.
// Higher level = more permissions. Public users have level 0 (no admin access).
func roleLevel(role string) int {
	switch role {
	case model.RoleAdmin:
		return 2
	case model.RoleEditor:
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
				clientIP := GetClientIP(r)

				// Log 403 for security monitoring (application logs)
				slog.Warn("access denied",
					"status", http.StatusForbidden,
					"method", r.Method,
					"path", r.URL.Path,
					"user_id", user.ID,
					"user_role", user.Role,
					"required_role", minRole,
					"ip", clientIP,
				)

				// Log 403 to event log (visible in admin panel)
				if eventService != nil {
					metadata := map[string]any{
						"method":        r.Method,
						"status":        http.StatusForbidden,
						"user_role":     user.Role,
						"required_role": minRole,
					}
					_ = eventService.LogAuthEvent(r.Context(), "warning", "Access denied: insufficient permissions", new(user.ID), clientIP, r.URL.Path, metadata)
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
// Shorthand for RequireRole(model.RoleAdmin).
func RequireAdmin() func(http.Handler) http.Handler {
	return RequireRole(model.RoleAdmin)
}

// RequireAdminWithEventLog creates middleware that requires admin role with event logging.
func RequireAdminWithEventLog(eventService *service.EventService) func(http.Handler) http.Handler {
	return RequireRoleWithEventLog(model.RoleAdmin, eventService)
}

// RequireEditor creates middleware that requires at least editor role.
// Allows both admin and editor users.
func RequireEditor() func(http.Handler) http.Handler {
	return RequireRole(model.RoleEditor)
}

// RequireEditorWithEventLog creates middleware that requires at least editor role with event logging.
func RequireEditorWithEventLog(eventService *service.EventService) func(http.Handler) http.Handler {
	return RequireRoleWithEventLog(model.RoleEditor, eventService)
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
