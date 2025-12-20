// Package middleware provides HTTP middleware for authentication,
// authorization, and request context handling.
package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"ocms-go/internal/store"
)

// Context keys for language data.
const (
	ContextKeyLanguage     ContextKey = "language"
	ContextKeyLanguageCode ContextKey = "language_code"
)

// LanguageCookieName is the cookie name for language preference.
const LanguageCookieName = "ocms_lang"

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
// 1. Query parameter ?lang=XX (explicit language switch, updates cookie)
// 2. URL parameter {lang} from chi router (e.g., /ru/page-slug)
// 3. For homepage only: Cookie preference, then Accept-Language header
// 4. Default language (for all non-prefixed content pages)
//
// This ensures that /page-slug always shows in default language,
// while /ru/page-slug shows in Russian, and the homepage uses user preference.
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

			// Get all active languages for matching
			activeLangs, err := queries.ListActiveLanguages(ctx)
			if err != nil || len(activeLangs) == 0 {
				// Set default language and proceed
				ctx = setLanguageContext(ctx, defaultLang)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Build a map of language codes for quick lookup
			langMap := make(map[string]store.Language)
			for _, lang := range activeLangs {
				langMap[strings.ToLower(lang.Code)] = lang
			}

			// 1. Check query parameter ?lang=XX (explicit language switch)
			// This takes highest priority and updates the cookie
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

			// 2. Check URL parameter {lang} from chi router (explicit language in URL)
			langParam := chi.URLParam(r, "lang")
			if langParam != "" {
				code := strings.ToLower(langParam)
				if lang, ok := langMap[code]; ok {
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
			ctx = setLanguageContext(ctx, defaultLang)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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

// SetLanguageCookie sets the language preference cookie.
func SetLanguageCookie(w http.ResponseWriter, langCode string) {
	cookie := &http.Cookie{
		Name:     LanguageCookieName,
		Value:    langCode,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
}
