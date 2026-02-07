// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package react_headless

import (
	"net/http"
	"strconv"
	"strings"
)

// GetCORSMiddleware returns an HTTP middleware that adds CORS headers to API responses.
// The middleware checks the module's active status at runtime via the provided function.
func (m *Module) GetCORSMiddleware(isActive func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip CORS if module is inactive
			if !isActive(m.Name()) {
				next.ServeHTTP(w, r)
				return
			}

			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Check if origin is allowed
			if m.isOriginAllowed(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")

				if m.settings != nil && m.settings.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}

				// Handle preflight requests
				if r.Method == http.MethodOptions {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")

					maxAge := 3600
					if m.settings != nil && m.settings.MaxAge > 0 {
						maxAge = m.settings.MaxAge
					}
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(maxAge))

					w.WriteHeader(http.StatusNoContent)
					return
				}

				// Expose common response headers
				w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count, X-Page, X-Per-Page")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isOriginAllowed checks if an origin matches the allowed origins list.
func (m *Module) isOriginAllowed(origin string) bool {
	allowed := m.GetAllowedOrigins()
	for _, a := range allowed {
		if a == "*" {
			return true
		}
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}
