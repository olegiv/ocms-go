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
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
)

// Context keys for language data.
const (
	ContextKeyLanguage     ContextKey = "language"
	ContextKeyLanguageCode ContextKey = "language_code"
	ContextKeyLangPrefix   ContextKey = "language_prefix"
)

// LanguageCookieName is the cookie name for language preference.
const LanguageCookieName = "ocms_lang"

// secureCookies controls whether cookies are set with the Secure flag.
// In production mode (HTTPS), this should be true.
// In development mode (HTTP), this should be false.
var secureCookies = true // Default to secure (production mode)

// InitLanguageCookies configures the Secure flag for language cookies.
// Call this during application startup with isDev=true for development mode.
func InitLanguageCookies(isDev bool) {
	secureCookies = !isDev
}

// LanguageInfo holds language data for the request context.
type LanguageInfo struct {
	ID         int64
	Code       string
	Name       string
	NativeName string
	Direction  string
	IsDefault  bool
}

// Language creates middleware that detects and sets the current language.
// Priority order:
// 1. Active URL prefix (e.g., /ru/page-slug)
// 2. Query parameter ?lang=XX (explicit language switch, updates cookie)
// 3. For homepage only: Cookie preference, then Accept-Language header
// 4. Default language (for all non-prefixed content pages)
//
// This ensures that /page-slug always shows in default language,
// while /ru/page-slug shows in Russian, and the homepage uses user preference.
// The middleware must wrap the frontend child router so it can rewrite the
// child's RoutePath before that router selects an endpoint.
func Language(db *sql.DB) func(http.Handler) http.Handler {
	queries := store.New(db)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Get default language (cached would be better, but keeping it simple)
			defaultLang, err := queries.GetDefaultLanguage(ctx)
			if err != nil {
				// No default language configured, proceed without language context
				next.ServeHTTP(w, r)
				return
			}
			defaultRoutable := defaultLang.IsActive && util.IsValidLangCode(defaultLang.Code) &&
				!util.IsReservedLanguageCode(defaultLang.Code)

			// Get all active languages for matching. An inactive default is retained
			// in storage for administrator remediation, but it must never be installed
			// in public request context or used as an unprefixed fallback.
			activeLangs, err := queries.ListActiveLanguages(ctx)
			if err != nil || len(activeLangs) == 0 {
				if defaultRoutable {
					ctx = setLanguageContext(ctx, defaultLang)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			langMap := activeLanguageMap(activeLangs)

			// 1. An active, non-reserved URL prefix is authoritative. Strip it
			// from the child router's path before route matching, while retaining
			// the original request URL for logging and canonical URL handling.
			routePath := r.URL.Path
			if routeCtx := chi.RouteContext(ctx); routeCtx != nil && routeCtx.RoutePath != "" {
				routePath = routeCtx.RoutePath
			}
			if lang, strippedPath, ok := matchLanguagePrefix(routePath, langMap); ok {
				if routeCtx := chi.RouteContext(ctx); routeCtx != nil {
					routeCtx.RoutePath = strippedPath
				}
				ctx = setLanguageContext(ctx, lang)
				ctx = context.WithValue(ctx, ContextKeyLangPrefix, lang.Code)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 2. Check query parameter ?lang=XX (explicit language switch).
			queryLang := r.URL.Query().Get("lang")
			if queryLang != "" {
				code := strings.ToLower(queryLang)
				if lang, ok := langMap[code]; ok {
					// Update cookie to new language preference
					SetLanguageCookie(w, lang.Code)
					ctx = setLanguageContext(ctx, lang)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// 3. For homepage only, check cookie and Accept-Language header
			// Non-prefixed content pages (/{slug}) should use default language
			isHomepage := r.URL.Path == "/" || r.URL.Path == ""
			if isHomepage {
				// Check cookie preference
				if cookie, err := r.Cookie(LanguageCookieName); err == nil {
					code := strings.ToLower(cookie.Value)
					if lang, ok := langMap[code]; ok {
						ctx = setLanguageContext(ctx, lang)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}

				// Check Accept-Language header
				acceptLang := r.Header.Get("Accept-Language")
				if acceptLang != "" {
					if lang := matchAcceptLanguage(acceptLang, langMap); lang != nil {
						ctx = setLanguageContext(ctx, *lang)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}

			// 4. Fall back to default language
			if defaultRoutable {
				ctx = setLanguageContext(ctx, defaultLang)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// activeLanguageMap returns only codes that are safe to expose as URL
// prefixes. Invalid and reserved legacy rows stay manageable in the admin UI,
// but cannot shadow application routes.
func activeLanguageMap(activeLangs []store.Language) map[string]store.Language {
	langMap := make(map[string]store.Language, len(activeLangs))
	for _, lang := range activeLangs {
		code := lang.Code
		if !util.IsValidLangCode(code) {
			slog.Warn("ignoring active language with invalid code", "language_id", lang.ID, "code", lang.Code)
			continue
		}
		if util.IsReservedLanguageCode(code) {
			slog.Warn("ignoring active language with reserved route prefix", "language_id", lang.ID, "code", lang.Code)
			continue
		}
		langMap[code] = lang
	}
	return langMap
}

// matchLanguagePrefix matches the first path segment against the active
// language map and returns the path to route after removing that prefix.
func matchLanguagePrefix(path string, langMap map[string]store.Language) (store.Language, string, bool) {
	path = strings.TrimPrefix(path, "/")
	code, remainder, hasRemainder := strings.Cut(path, "/")
	if !util.IsValidLangCode(code) {
		return store.Language{}, "", false
	}

	lang, ok := langMap[code]
	if !ok {
		return store.Language{}, "", false
	}

	if !hasRemainder || remainder == "" {
		return lang, "/", true
	}
	return lang, "/" + remainder, true
}

// matchAcceptLanguage finds the best matching language from Accept-Language header.
// Returns the matched language or nil if no match found.
func matchAcceptLanguage(acceptLang string, langMap map[string]store.Language) *store.Language {
	// Parse Accept-Language header (simplified - ignores quality values)
	// Format: en-US,en;q=0.9,ru;q=0.8
	parts := strings.Split(acceptLang, ",")

	for _, part := range parts {
		// Remove quality value if present
		langPart := strings.TrimSpace(strings.Split(part, ";")[0])

		// Try exact match first (e.g., en-US)
		if lang, ok := langMap[strings.ToLower(langPart)]; ok {
			return &lang
		}

		// Try primary language code (e.g., en from en-US)
		if idx := strings.Index(langPart, "-"); idx > 0 {
			primaryCode := strings.ToLower(langPart[:idx])
			if lang, ok := langMap[primaryCode]; ok {
				return &lang
			}
		}
	}

	return nil
}

// setLanguageContext adds language info to the context.
func setLanguageContext(ctx context.Context, lang store.Language) context.Context {
	info := LanguageInfo{
		ID:         lang.ID,
		Code:       lang.Code,
		Name:       lang.Name,
		NativeName: lang.NativeName,
		Direction:  lang.Direction,
		IsDefault:  lang.IsDefault,
	}
	ctx = context.WithValue(ctx, ContextKeyLanguage, info)
	ctx = context.WithValue(ctx, ContextKeyLanguageCode, lang.Code)
	return ctx
}

// GetLanguage retrieves the current language from the request context.
// Returns nil if no language is in context.
func GetLanguage(r *http.Request) *LanguageInfo {
	info, ok := r.Context().Value(ContextKeyLanguage).(LanguageInfo)
	if !ok {
		return nil
	}
	return &info
}

// GetLanguagePrefix returns the explicit language URL prefix selected for the
// request, or an empty string when language selection came from another source.
func GetLanguagePrefix(r *http.Request) string {
	code, _ := r.Context().Value(ContextKeyLangPrefix).(string)
	return code
}

// SetLanguageCookie sets the language preference cookie.
// The Secure flag is set based on the configuration from InitLanguageCookies.
func SetLanguageCookie(w http.ResponseWriter, langCode string) {
	cookie := &http.Cookie{
		Name:     LanguageCookieName,
		Value:    langCode,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureCookies,
	}
	http.SetCookie(w, cookie)
}
