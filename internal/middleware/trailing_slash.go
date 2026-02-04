// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"strings"
)

// StripTrailingSlash redirects URLs with trailing slashes to their
// non-trailing equivalents (HTTP 301). Excludes root path "/".
func StripTrailingSlash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" && strings.HasSuffix(path, "/") {
			// Build new URL without trailing slash
			newPath := strings.TrimSuffix(path, "/")
			newURL := newPath
			if r.URL.RawQuery != "" {
				newURL += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, newURL, http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}
