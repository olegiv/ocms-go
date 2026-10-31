// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"strings"
)

// RedactedRequestLogger hides OAuth query strings from the access logger without changing handlers.
func RedactedRequestLogger(logger func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		ordinary := logger(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/oauth/") {
				ordinary.ServeHTTP(w, r)
				return
			}
			clean := r.Clone(r.Context())
			cleanURL := *r.URL
			cleanURL.RawQuery = ""
			clean.URL = &cleanURL
			clean.RequestURI = cleanURL.EscapedPath()
			logger(http.HandlerFunc(func(w http.ResponseWriter, logged *http.Request) {
				next.ServeHTTP(w, r.WithContext(logged.Context()))
			})).ServeHTTP(w, clean)
		})
	}
}
