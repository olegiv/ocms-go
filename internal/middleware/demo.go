// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"os"
	"strings"
	"sync"
)

// Demo mode configuration
var (
	demoMode     bool
	demoModeOnce sync.Once
)

// IsDemoMode returns true if the application is running in demo mode.
// Demo mode is enabled when OCMS_DEMO_MODE=true environment variable is set.
func IsDemoMode() bool {
	demoModeOnce.Do(func() {
		demoMode = os.Getenv("OCMS_DEMO_MODE") == "true"
	})
	return demoMode
}

// DemoRestriction defines a type of restriction in demo mode.
type DemoRestriction string

// Demo mode restrictions - operations that are blocked in demo mode.
const (
	// Content restrictions (block all modifications)
	RestrictionContentReadOnly    DemoRestriction = "content_read_only"
	RestrictionUnpublishContent   DemoRestriction = "unpublish_content"
	RestrictionDeletePage      DemoRestriction = "delete_page"
	RestrictionDeleteMedia    DemoRestriction = "delete_media"
	RestrictionDeleteCategory DemoRestriction = "delete_category"
	RestrictionDeleteTag      DemoRestriction = "delete_tag"
	RestrictionDeleteMenu     DemoRestriction = "delete_menu"
	RestrictionDeleteMenuItem DemoRestriction = "delete_menu_item"
	RestrictionDeleteForm     DemoRestriction = "delete_form"
	RestrictionDeleteWidget   DemoRestriction = "delete_widget"

	// User management restrictions
	RestrictionCreateUser  DemoRestriction = "create_user"
	RestrictionDeleteUser  DemoRestriction = "delete_user"
	RestrictionEditUser    DemoRestriction = "edit_user"
	RestrictionChangeRole  DemoRestriction = "change_role"

	// System configuration restrictions
	RestrictionEditConfig    DemoRestriction = "edit_config"
	RestrictionEditLanguages DemoRestriction = "edit_languages"

	// Security-sensitive restrictions
	RestrictionAPIKeys       DemoRestriction = "api_keys"
	RestrictionWebhooks      DemoRestriction = "webhooks"
	RestrictionExportData    DemoRestriction = "export_data"
	RestrictionImportData    DemoRestriction = "import_data"
	RestrictionModules        DemoRestriction = "modules"
	RestrictionModuleSettings DemoRestriction = "module_settings"
	RestrictionThemeSettings  DemoRestriction = "theme_settings"
	RestrictionClearCache     DemoRestriction = "clear_cache"

	// Module-specific restrictions
	RestrictionSQLExecution DemoRestriction = "sql_execution"

	// Upload restrictions (size limits applied separately)
	RestrictionLargeUpload DemoRestriction = "large_upload"
)

// DemoModeMessage is the user-friendly message shown when an action is blocked.
const DemoModeMessage = "This action is disabled in demo mode"

// DemoModeMessageDetailed returns a detailed message for a specific restriction.
func DemoModeMessageDetailed(restriction DemoRestriction) string {
	messages := map[DemoRestriction]string{
		RestrictionContentReadOnly:  "Content is read-only in demo mode",
		RestrictionUnpublishContent: "Unpublishing content is disabled in demo mode",
		RestrictionDeletePage:      "Deleting pages is disabled in demo mode",
		RestrictionDeleteMedia:    "Deleting media is disabled in demo mode",
		RestrictionDeleteCategory: "Deleting categories is disabled in demo mode",
		RestrictionDeleteTag:      "Deleting tags is disabled in demo mode",
		RestrictionDeleteMenu:     "Deleting menus is disabled in demo mode",
		RestrictionDeleteMenuItem: "Deleting menu items is disabled in demo mode",
		RestrictionDeleteForm:     "Deleting forms is disabled in demo mode",
		RestrictionDeleteWidget:   "Deleting widgets is disabled in demo mode",
		RestrictionCreateUser:     "Creating users is disabled in demo mode",
		RestrictionDeleteUser:     "Deleting users is disabled in demo mode",
		RestrictionEditUser:       "Editing users is disabled in demo mode",
		RestrictionChangeRole:     "Changing user roles is disabled in demo mode",
		RestrictionEditConfig:     "Editing site configuration is disabled in demo mode",
		RestrictionEditLanguages:  "Editing languages is disabled in demo mode",
		RestrictionAPIKeys:        "API key management is disabled in demo mode",
		RestrictionWebhooks:       "Webhook management is disabled in demo mode",
		RestrictionExportData:     "Data export is disabled in demo mode",
		RestrictionImportData:     "Data import is disabled in demo mode",
		RestrictionModules:        "Module management is disabled in demo mode",
		RestrictionModuleSettings: "Changing module settings is disabled in demo mode",
		RestrictionThemeSettings:  "Changing theme settings is disabled in demo mode",
		RestrictionClearCache:     "Clearing cache is disabled in demo mode",
		RestrictionSQLExecution:   "SQL execution is disabled in demo mode",
		RestrictionLargeUpload:    "Large file uploads are disabled in demo mode (max 2MB)",
	}

	if msg, ok := messages[restriction]; ok {
		return msg
	}
	return DemoModeMessage
}

// BlockInDemoMode creates middleware that blocks the request in demo mode.
// Use this to wrap entire routes that should be completely disabled.
func BlockInDemoMode(restriction DemoRestriction) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsDemoMode() {
				next.ServeHTTP(w, r)
				return
			}

			// Check if this is an API request
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, DemoModeMessageDetailed(restriction), http.StatusForbidden)
				return
			}

			// For web requests, redirect with flash message
			// Set a cookie that the handler can read to show a flash message
			http.SetCookie(w, &http.Cookie{
				Name:     "demo_blocked",
				Value:    string(restriction),
				Path:     "/",
				MaxAge:   5, // Short-lived cookie
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   true,
			})

			// Redirect back to referrer or admin dashboard
			referer := r.Header.Get("Referer")
			if referer == "" {
				referer = "/admin/"
			}
			http.Redirect(w, r, referer, http.StatusSeeOther)
		})
	}
}

// BlockDeleteInDemoMode creates middleware that blocks DELETE requests in demo mode.
// Allows GET, POST (for create/edit), but blocks DELETE operations.
func BlockDeleteInDemoMode(restriction DemoRestriction) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsDemoMode() {
				next.ServeHTTP(w, r)
				return
			}

			// Block DELETE method and POST requests to /delete paths
			if r.Method == http.MethodDelete ||
				(r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/delete")) {

				if strings.HasPrefix(r.URL.Path, "/api/") {
					http.Error(w, DemoModeMessageDetailed(restriction), http.StatusForbidden)
					return
				}

				http.SetCookie(w, &http.Cookie{
					Name:     "demo_blocked",
					Value:    string(restriction),
					Path:     "/",
					MaxAge:   5,
					Secure:   true,
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				})

				referer := r.Header.Get("Referer")
				if referer == "" {
					referer = "/admin/"
				}
				http.Redirect(w, r, referer, http.StatusSeeOther)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// BlockWriteInDemoMode creates middleware that blocks POST/PUT/DELETE in demo mode.
// Use for routes where all modifications should be blocked.
func BlockWriteInDemoMode(restriction DemoRestriction) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsDemoMode() {
				next.ServeHTTP(w, r)
				return
			}

			// Allow GET requests (viewing)
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			// Block all write operations
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, DemoModeMessageDetailed(restriction), http.StatusForbidden)
				return
			}

			http.SetCookie(w, &http.Cookie{
				Name:     "demo_blocked",
				Value:    string(restriction),
				Path:     "/",
				MaxAge:   5,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				Secure:   true,
			})

			referer := r.Header.Get("Referer")
			if referer == "" {
				referer = "/admin/"
			}
			http.Redirect(w, r, referer, http.StatusSeeOther)
		})
	}
}

// DemoUploadMaxSize is the maximum upload size in demo mode (2MB).
const DemoUploadMaxSize = 2 * 1024 * 1024

// CheckDemoUploadSize checks if the upload size exceeds demo mode limits.
// Returns true if the upload should be blocked.
func CheckDemoUploadSize(r *http.Request) bool {
	if !IsDemoMode() {
		return false
	}
	return r.ContentLength > DemoUploadMaxSize
}

// GetDemoBlockedMessage reads and clears the demo_blocked cookie,
// returning the restriction message if present.
func GetDemoBlockedMessage(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie("demo_blocked")
	if err != nil {
		return ""
	}

	// Clear the cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "demo_blocked",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})

	return DemoModeMessageDetailed(DemoRestriction(cookie.Value))
}
