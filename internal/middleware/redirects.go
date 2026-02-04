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
			matched, captures := rm.matchPathWithCaptures(path, rd.SourcePath, rd.IsWildcard)
			if matched {
				targetURL := rm.buildTargetURL(rd, captures)

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

// matchPathWithCaptures matches a request path against a source pattern and returns captured wildcard segments.
// For non-wildcard patterns, it returns a simple equality check with no captures.
// For wildcard patterns:
//   - "*" captures exactly one path segment
//   - "**" captures zero or more path segments (joined with "/")
func (rm *RedirectsMiddleware) matchPathWithCaptures(requestPath, sourcePath string, isWildcard bool) (bool, []string) {
	if !isWildcard {
		return requestPath == sourcePath, nil
	}

	sourceParts := strings.Split(strings.Trim(sourcePath, "/"), "/")
	requestParts := strings.Split(strings.Trim(requestPath, "/"), "/")

	var captures []string
	matched := matchPathPartsWithCaptures(sourceParts, requestParts, 0, 0, &captures)
	return matched, captures
}

// matchPathPartsWithCaptures recursively matches path parts with wildcard support and captures matched segments.
func matchPathPartsWithCaptures(sourceParts, requestParts []string, si, ri int, captures *[]string) bool {
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
			// ** matching zero segments captures empty string
			*captures = append(*captures, "")
		}
		return true
	}

	part := sourceParts[si]

	switch part {
	case "*":
		// Single wildcard - matches exactly one segment, capture it
		*captures = append(*captures, requestParts[ri])
		return matchPathPartsWithCaptures(sourceParts, requestParts, si+1, ri+1, captures)

	case "**":
		// Double wildcard - matches zero or more segments
		// Try matching zero segments first (move source forward)
		capturesBefore := len(*captures)
		*captures = append(*captures, "") // Capture empty for zero match
		if matchPathPartsWithCaptures(sourceParts, requestParts, si+1, ri, captures) {
			return true
		}
		// Backtrack: remove captures added during failed attempt
		*captures = (*captures)[:capturesBefore]

		// Try matching one or more segments
		// Collect all segments that ** should match
		for endIdx := ri; endIdx <= len(requestParts); endIdx++ {
			capturesBefore := len(*captures)
			captured := strings.Join(requestParts[ri:endIdx], "/")
			*captures = append(*captures, captured)
			if matchPathPartsWithCaptures(sourceParts, requestParts, si+1, endIdx, captures) {
				return true
			}
			// Backtrack
			*captures = (*captures)[:capturesBefore]
		}
		return false

	default:
		// Literal match
		if part == requestParts[ri] {
			return matchPathPartsWithCaptures(sourceParts, requestParts, si+1, ri+1, captures)
		}
		return false
	}
}

// buildTargetURL builds the final target URL by substituting captured wildcard segments.
// Each "*" or "**" in the target URL is replaced with the corresponding captured segment.
func (rm *RedirectsMiddleware) buildTargetURL(rd store.Redirect, captures []string) string {
	if !rd.IsWildcard || len(captures) == 0 {
		return rd.TargetUrl
	}

	targetURL := rd.TargetUrl

	// Replace each * in the target with the corresponding captured segment
	for _, capture := range captures {
		// Find and replace the first * (which could be part of ** too)
		if idx := strings.Index(targetURL, "*"); idx != -1 {
			// Check if it's ** (double wildcard)
			if idx+1 < len(targetURL) && targetURL[idx+1] == '*' {
				// Replace ** with the captured segment
				targetURL = targetURL[:idx] + capture + targetURL[idx+2:]
			} else {
				// Replace single * with the captured segment
				targetURL = targetURL[:idx] + capture + targetURL[idx+1:]
			}
		}
	}

	return targetURL
}
