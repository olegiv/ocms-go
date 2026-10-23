// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package util provides general-purpose utility functions including
// URL slug generation and validation with Unicode normalization support.
package util

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/mozillazg/go-unidecode"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var (
	// slugRegex matches non-alphanumeric characters (except hyphens)
	slugRegex = regexp.MustCompile(`[^a-z0-9-]+`)
	// multipleHyphens matches multiple consecutive hyphens
	multipleHyphens = regexp.MustCompile(`-{2,}`)
)

// Slugify converts a string to a URL-friendly ASCII slug.
// Non-ASCII characters (Cyrillic, CJK, etc.) are transliterated to their
// closest ASCII equivalent. Accents are removed, spaces become hyphens,
// and only alphanumeric characters and hyphens are kept.
func Slugify(s string) string {
	// Transliterate non-ASCII to ASCII (Cyrillic, CJK, etc.)
	result := unidecode.Unidecode(s)

	// Normalize remaining unicode characters (decompose accents)
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ = transform.String(t, result)

	// Convert to lowercase
	result = strings.ToLower(result)

	// Replace spaces with hyphens
	result = strings.ReplaceAll(result, " ", "-")

	// Remove all non-alphanumeric characters except hyphens
	result = slugRegex.ReplaceAllString(result, "")

	// Replace multiple hyphens with single hyphen
	result = multipleHyphens.ReplaceAllString(result, "-")

	// Trim hyphens from start and end
	result = strings.Trim(result, "-")

	return result
}

// IsValidSlug checks if a string is a valid slug format.
func IsValidSlug(s string) bool {
	if s == "" {
		return false
	}

	// Check if it only contains lowercase letters, numbers, and hyphens
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}

	// Check that it doesn't start or end with a hyphen
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}

	// Check for consecutive hyphens
	if strings.Contains(s, "--") {
		return false
	}

	return true
}

// IsValidLangCode checks if a string is a valid language code format. Codes
// contain 2-10 lowercase letters, digits, or hyphens, with no leading,
// trailing, or consecutive hyphens.
func IsValidLangCode(s string) bool {
	if len(s) < 2 || len(s) > 10 {
		return false
	}

	// Must start and end with alphanumeric
	if !isAlphanumeric(rune(s[0])) || !isAlphanumeric(rune(s[len(s)-1])) {
		return false
	}

	// Check all characters are lowercase letters, digits, or hyphens
	for _, r := range s {
		if !isAlphanumeric(r) && r != '-' {
			return false
		}
	}

	// No consecutive hyphens
	if strings.Contains(s, "--") {
		return false
	}

	return true
}

// IsReservedRouteSegment reports whether s is the first path segment of a
// route that the application or a built-in module registers, at that URL or
// below it. A language URL prefix equal to one of them would collide with the
// route. TestTopLevelRoutesAreReserved (cmd/ocms) keeps the list in step with
// the router.
func IsReservedRouteSegment(s string) bool {
	switch s {
	case "admin", "api", "blog", "uploads", "static", "themes", "health",
		"login", "logout", "language", "forms", "search", "tag", "category",
		"page", "session", "account", "csp-report", "analytics", "embed", "example":
		return true
	default:
		return false
	}
}

// reservedPageSlugs are the single path segments whose exact URL an
// application or built-in module route answers on GET. chi matches those
// routes before the page catch-all, so a page saved at one is never served.
// Segments that own only deeper paths (forms/{slug}, uploads/...) or other
// methods (POST /logout) stay free for pages. TestExactRoutesReservePageSlugs
// (cmd/ocms) keeps the list equal to the router's exact routes.
var reservedPageSlugs = []string{"account", "admin", "blog", "example", "health", "login", "search"}

// IsReservedPageSlug reports whether a page slug or single-segment alias equals
// the exact URL of an application route, which answers it instead of the page.
func IsReservedPageSlug(s string) bool {
	return slices.Contains(reservedPageSlugs, s)
}

// ReservedPageSlugs returns the reserved page slugs in sorted order.
func ReservedPageSlugs() []string {
	return slices.Clone(reservedPageSlugs)
}

// reservedRoutePaths are the multi-segment paths that application and
// built-in module routes answer on GET without falling back to the page alias
// lookup. A "*" segment stands for one path segment and a trailing "/**" for
// zero or more further segments: "admin/**" covers /admin and everything below
// it (a mounted router), while "uploads/*/**" covers only paths below
// /uploads, because chi's /uploads/* does not answer /uploads itself.
// Parameter routes whose handlers resolve page aliases on a miss (/page/{id},
// /category/{slug}, /tag/{slug}, /forms/{slug}) are left out: an alias there
// works while no such page, term or form exists. A page alias matching an
// entry is answered by the route and never reaches the alias lookup.
// TestRoutePathsReserveAliases (cmd/ocms) keeps the list equal to the
// router's registrations.
var reservedRoutePaths = []string{
	"account/security",
	"account/security/totp",
	"admin/**",
	"api/informer/demo-credentials",
	"api/mcp",
	"api/v2/**",
	"blog/tag/*",
	"embed/dify/messages/*/suggested",
	"embed/dify/token",
	"health/live",
	"health/ready",
	"login/mfa",
	"login/mfa/setup",
	"session/get",
	"session/set",
	"static/dist/*/**",
	"themes/*/static/*/**",
	"uploads/*/**",
}

// ReservedRoutePath returns the reserved route path that a slash-separated
// path such as a page alias matches, or "" when no route answers it.
func ReservedRoutePath(path string) string {
	segments := strings.Split(path, "/")
	for _, pattern := range reservedRoutePaths {
		parts := strings.Split(pattern, "/")
		subtree := parts[len(parts)-1] == "**"
		if subtree {
			parts = parts[:len(parts)-1]
		}
		if len(segments) < len(parts) || (!subtree && len(segments) != len(parts)) {
			continue
		}
		matched := true
		for i, part := range parts {
			if part != "*" && part != segments[i] {
				matched = false
				break
			}
		}
		if matched {
			return pattern
		}
	}
	return ""
}

// ReservedRoutePaths returns the reserved route paths in sorted order.
func ReservedRoutePaths() []string {
	return slices.Clone(reservedRoutePaths)
}

// IsReservedLanguageCode reports whether a language code conflicts with a
// top-level route owned by the application. Reserved codes must not be used as
// language URL prefixes, even when legacy data marks them as active.
func IsReservedLanguageCode(s string) bool {
	return IsReservedRouteSegment(s)
}

// IsPrivateRoutePath reports whether path belongs to the admin UI, the API or
// the signed-in account area. Content redirects and first-party analytics
// leave these paths alone. TestAuthenticatedRoutesArePrivate (cmd/ocms) fails
// when a session-authenticated top-level route is missing here.
func IsPrivateRoutePath(path string) bool {
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	switch first {
	case "admin", "api", "account":
		return true
	default:
		return false
	}
}

// IsRoutableLanguageCode reports whether a code may serve as a public URL
// prefix. A code that is malformed or owned by an application route stays
// editable in the admin UI but never routes, so callers deciding what a
// language claims in the URL namespace ask this rather than re-deriving it.
func IsRoutableLanguageCode(s string) bool {
	return IsValidLangCode(s) && !IsReservedLanguageCode(s)
}

// isAlphanumeric checks if a rune is a lowercase letter or digit.
func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// IsValidAlias checks if a string is a valid alias format.
// Aliases can contain path segments separated by forward slashes (e.g., "blog/post/55").
// Each segment must be a valid slug: lowercase letters, numbers, and hyphens.
func IsValidAlias(s string) bool {
	if s == "" {
		return false
	}

	// Check for leading or trailing slashes
	if s[0] == '/' || s[len(s)-1] == '/' {
		return false
	}

	// Check for consecutive slashes
	if strings.Contains(s, "//") {
		return false
	}

	// Split into segments and validate each
	segments := strings.Split(s, "/")
	for _, segment := range segments {
		if !IsValidSlug(segment) {
			return false
		}
	}

	return true
}
