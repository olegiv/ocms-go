// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// RedirectsMiddleware handles URL redirects based on database rules.
type RedirectsMiddleware struct {
	db        *sql.DB
	redirects []store.Redirect
	mu        sync.RWMutex
	lastLoad  time.Time
	cacheTTL  time.Duration
}

// NewRedirectsMiddleware creates a new redirects middleware.
func NewRedirectsMiddleware(db *sql.DB) *RedirectsMiddleware {
	rm := &RedirectsMiddleware{
		db:       db,
		cacheTTL: 5 * time.Minute, // Cache redirects for 5 minutes
	}
	// Initial load
	rm.loadRedirects()
	return rm
}

// loadRedirects loads enabled redirects from the database.
func (rm *RedirectsMiddleware) loadRedirects() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	queries := store.New(rm.db)
	redirects, err := queries.ListEnabledRedirects(ctx)
	if err != nil {
		slog.Error("failed to load redirects", "error", err)
		return
	}

	rm.mu.Lock()
	rm.redirects = redirects
	rm.lastLoad = time.Now()
	rm.mu.Unlock()

	slog.Debug("redirects loaded", "count", len(redirects))
}

// getRedirects returns the cached redirects, reloading if necessary.
func (rm *RedirectsMiddleware) getRedirects() []store.Redirect {
	rm.mu.RLock()
	if time.Since(rm.lastLoad) < rm.cacheTTL {
		redirects := rm.redirects
		rm.mu.RUnlock()
		return redirects
	}
	rm.mu.RUnlock()

	// Reload
	rm.loadRedirects()

	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.redirects
}

// InvalidateCache forces a reload of redirects on next request.
func (rm *RedirectsMiddleware) InvalidateCache() {
	rm.mu.Lock()
	rm.lastLoad = time.Time{} // Reset to zero time
	rm.mu.Unlock()
}

// Handler returns the middleware handler function.
func (rm *RedirectsMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip admin and API routes
		path := r.URL.Path
		if strings.HasPrefix(path, "/admin") || strings.HasPrefix(path, "/api") {
			next.ServeHTTP(w, r)
			return
		}

		// Check for matching redirect
		redirects := rm.getRedirects()
		for _, rd := range redirects {
			if rm.matchPath(path, rd.SourcePath, rd.IsWildcard) {
				targetURL := rm.buildTargetURL(path, rd)

				// For _blank target, we can't do a server redirect to open a new window
				// The browser will follow the redirect in the same window
				// _blank is mainly useful for links, but we still process it
				// as a regular redirect (the target_type is informational for HTML links)

				// Preserve query string
				if r.URL.RawQuery != "" {
					if strings.Contains(targetURL, "?") {
						targetURL += "&" + r.URL.RawQuery
					} else {
						targetURL += "?" + r.URL.RawQuery
					}
				}

				slog.Debug("redirect matched",
					"source", path,
					"target", targetURL,
					"status", rd.StatusCode,
					"wildcard", rd.IsWildcard,
				)

				http.Redirect(w, r, targetURL, int(rd.StatusCode))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// matchPath checks if a request path matches a redirect source path.
func (rm *RedirectsMiddleware) matchPath(requestPath, sourcePath string, isWildcard bool) bool {
	if !isWildcard {
		return requestPath == sourcePath
	}

	// Wildcard matching
	// * matches any single path segment
	// ** matches any number of path segments (including zero)

	sourceParts := strings.Split(strings.Trim(sourcePath, "/"), "/")
	requestParts := strings.Split(strings.Trim(requestPath, "/"), "/")

	return matchPathParts(sourceParts, requestParts, 0, 0)
}

// matchPathParts recursively matches path parts with wildcard support.
func matchPathParts(sourceParts, requestParts []string, si, ri int) bool {
	// Both exhausted - match
	if si >= len(sourceParts) && ri >= len(requestParts) {
		return true
	}

	// Source exhausted but request has more - no match
	if si >= len(sourceParts) {
		return false
	}

	// Request exhausted but source has more - check for ** only
	if ri >= len(requestParts) {
		// Only match if remaining source parts are all **
		for i := si; i < len(sourceParts); i++ {
			if sourceParts[i] != "**" {
				return false
			}
		}
		return true
	}

	part := sourceParts[si]

	switch part {
	case "*":
		// Single wildcard - matches exactly one segment
		return matchPathParts(sourceParts, requestParts, si+1, ri+1)

	case "**":
		// Double wildcard - matches zero or more segments
		// Try matching zero segments (move source forward)
		if matchPathParts(sourceParts, requestParts, si+1, ri) {
			return true
		}
		// Try matching one segment (move request forward, keep source at **)
		return matchPathParts(sourceParts, requestParts, si, ri+1)

	default:
		// Literal match
		if part == requestParts[ri] {
			return matchPathParts(sourceParts, requestParts, si+1, ri+1)
		}
		return false
	}
}

// buildTargetURL builds the final target URL, handling wildcard substitutions.
func (rm *RedirectsMiddleware) buildTargetURL(requestPath string, rd store.Redirect) string {
	// For non-wildcard redirects, just return the target
	if !rd.IsWildcard {
		return rd.TargetUrl
	}

	// For wildcard redirects, check if target contains $1, $2, etc. for captured segments
	// If target contains *, replace it with the matched wildcard portion
	targetURL := rd.TargetUrl

	// Simple case: if target ends with * and source ends with *, append the matched portion
	if strings.Contains(rd.SourcePath, "*") && strings.Contains(targetURL, "*") {
		// Extract the prefix before the wildcard in source
		sourcePrefix := strings.Split(rd.SourcePath, "*")[0]
		// Get the matched portion from the request
		if strings.HasPrefix(requestPath, sourcePrefix) {
			matchedPortion := strings.TrimPrefix(requestPath, sourcePrefix)
			// Replace * in target with matched portion
			targetURL = strings.Replace(targetURL, "*", matchedPortion, 1)
		}
	}

	return targetURL
}
