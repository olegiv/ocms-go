// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/contentpath"
	"github.com/olegiv/ocms-go/internal/preview"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"
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

// Handler returns the shared content redirect middleware.
func (rm *RedirectsMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A scrubbed path is a placeholder, never a redirect rule source.
		_, redacted := preview.RedactedShareAliasPath(r.Context())
		if redacted || util.IsPrivateRoutePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		target, status, matched, err := contentpath.FirstRedirect(rm.getRedirects(), r.URL.Path, r.URL.RawQuery)
		if err != nil {
			attrs := []any{"source", util.BoundedLogField(r.URL.Path), "error", contentpath.ErrUnsafeRedirect}
			var unsafe *contentpath.UnsafeRedirectError
			if errors.As(err, &unsafe) {
				targetPath, _, _ := strings.Cut(unsafe.Target, "?")
				attrs = append(attrs, "target", util.BoundedLogField(targetPath))
			}
			slog.Warn("blocked unsafe redirect target", attrs...)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if matched {
			http.Redirect(w, r, target, status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rm *RedirectsMiddleware) matchPathWithCaptures(path, pattern string, wildcard bool) (bool, []string) {
	return contentpath.MatchRedirect(path, pattern, wildcard)
}
func (rm *RedirectsMiddleware) buildTargetURL(rd store.Redirect, captures []string) string {
	return contentpath.BuildRedirectTarget(rd, captures)
}
