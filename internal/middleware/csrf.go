// Package middleware provides HTTP middleware for the OCMS application.
package middleware

import (
	"log/slog"
	"net/http"

	"filippo.io/csrf/gorilla"
)

// CSRFConfig holds configuration for CSRF protection.
// Note: filippo.io/csrf/gorilla uses Fetch metadata headers instead of cookies,
// so cookie-related options (Secure, Domain, Path, MaxAge, SameSite) are no longer used.
type CSRFConfig struct {
	// AuthKey is a 32-byte key used to authenticate the CSRF token.
	// This should be the same as the session secret for simplicity.
	AuthKey []byte

	// ErrorHandler is called when CSRF validation fails.
	ErrorHandler http.Handler

	// TrustedOrigins is a list of origins that are allowed to make
	// cross-origin requests. This is useful for AJAX requests.
	TrustedOrigins []string
}

// DefaultCSRFConfig returns a CSRFConfig with sensible defaults.
func DefaultCSRFConfig(authKey []byte, isDev bool) CSRFConfig {
	cfg := CSRFConfig{
		AuthKey: authKey,
	}

	// In development, trust localhost origins for easier testing
	// Note: csrf library expects host-only values, not full URLs
	if isDev {
		cfg.TrustedOrigins = []string{
			"localhost:8080",
			"127.0.0.1:8080",
		}
	}

	return cfg
}

// CSRF returns a middleware that provides CSRF protection.
// It uses filippo.io/csrf/gorilla under the hood, which uses Fetch metadata
// headers instead of cookies for CSRF protection.
func CSRF(cfg CSRFConfig) func(http.Handler) http.Handler {
	var opts []csrf.Option

	if cfg.ErrorHandler != nil {
		opts = append(opts, csrf.ErrorHandler(cfg.ErrorHandler))
	} else {
		// Default error handler returns a simple 403 response
		opts = append(opts, csrf.ErrorHandler(http.HandlerFunc(csrfErrorHandler)))
	}

	if len(cfg.TrustedOrigins) > 0 {
		opts = append(opts, csrf.TrustedOrigins(cfg.TrustedOrigins))
	}

	return csrf.Protect(cfg.AuthKey, opts...)
}

// csrfErrorHandler handles CSRF validation failures.
func csrfErrorHandler(w http.ResponseWriter, r *http.Request) {
	// Get the failure reason from the csrf library
	reason := csrf.FailureReason(r)
	reasonStr := "unknown"
	if reason != nil {
		reasonStr = reason.Error()
	}
	slog.Error("CSRF validation failed",
		"reason", reasonStr,
		"method", r.Method,
		"path", r.URL.Path,
		"origin", r.Header.Get("Origin"),
		"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"),
	)
	http.Error(w, "Forbidden - CSRF validation failed", http.StatusForbidden)
}

// SkipCSRF returns a middleware that skips CSRF protection for specific paths.
// This is useful for API endpoints that use token-based authentication.
func SkipCSRF(paths ...string) func(http.Handler) http.Handler {
	skipPaths := make(map[string]bool)
	for _, p := range paths {
		skipPaths[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipPaths[r.URL.Path] {
				// Set a flag to skip CSRF for this request
				r = csrf.UnsafeSkipCheck(r)
			}
			next.ServeHTTP(w, r)
		})
	}
}
