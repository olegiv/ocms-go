// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package middleware provides HTTP middleware for the OCMS application.
package middleware

import (
	"net/http"
	"strings"
)

// SecurityHeadersConfig holds configuration for security headers.
type SecurityHeadersConfig struct {
	// IsDevelopment indicates if the application is running in development mode.
	// When true, HSTS is disabled and CSP is more permissive.
	IsDevelopment bool

	// ContentSecurityPolicy is the CSP header value.
	// If empty, a default policy is used.
	ContentSecurityPolicy string

	// HSTSMaxAge is the max-age for Strict-Transport-Security header in seconds.
	// Default is 31536000 (1 year). Set to 0 to disable HSTS.
	HSTSMaxAge int

	// HSTSIncludeSubDomains includes subdomains in HSTS policy.
	HSTSIncludeSubDomains bool

	// HSTSPreload enables HSTS preload list eligibility.
	HSTSPreload bool

	// FrameOptions controls the X-Frame-Options header.
	// Valid values: "DENY", "SAMEORIGIN", or empty to disable.
	FrameOptions string

	// ReferrerPolicy controls the Referrer-Policy header.
	// Default is "strict-origin-when-cross-origin".
	ReferrerPolicy string

	// PermissionsPolicy controls the Permissions-Policy header.
	// If empty, a restrictive default policy is used.
	PermissionsPolicy string

	// ExcludePaths are paths that should skip security headers.
	// Useful for API endpoints that need different policies.
	ExcludePaths []string
}

// DefaultSecurityHeadersConfig returns a SecurityHeadersConfig with sensible defaults.
func DefaultSecurityHeadersConfig(isDev bool) SecurityHeadersConfig {
	cfg := SecurityHeadersConfig{
		IsDevelopment:  isDev,
		HSTSMaxAge:     31536000, // 1 year
		FrameOptions:   "SAMEORIGIN",
		ReferrerPolicy: "strict-origin-when-cross-origin",
	}

	// Default CSP - allow self, inline styles (for CMS features), and common analytics
	if isDev {
		// More permissive in development for easier debugging
		cfg.ContentSecurityPolicy = buildCSP(map[string]string{
			"default-src": "'self'",
			"script-src":  "'self' 'unsafe-inline' 'unsafe-eval' https://unpkg.com https://esm.sh https://hcaptcha.com https://*.hcaptcha.com https://*.dify.ai https://udify.app",
			"style-src":   "'self' 'unsafe-inline' https://hcaptcha.com https://*.hcaptcha.com",
			"img-src":     "'self' data: blob: https:",
			"font-src":    "'self' data:",
			"connect-src": "'self' https://esm.sh https://hcaptcha.com https://*.hcaptcha.com https://*.dify.ai https://udify.app",
			"frame-src":   "'self' https://hcaptcha.com https://*.hcaptcha.com https://*.dify.ai https://udify.app",
			"object-src":  "'none'",
			"base-uri":    "'self'",
			"form-action": "'self'",
		})
	} else {
		// Strict CSP for production
		cfg.ContentSecurityPolicy = buildCSP(map[string]string{
			"default-src": "'self'",
			"script-src":  "'self' 'unsafe-inline' https://unpkg.com https://esm.sh https://www.googletagmanager.com https://www.google-analytics.com https://hcaptcha.com https://*.hcaptcha.com https://*.dify.ai https://udify.app",
			"style-src":   "'self' 'unsafe-inline' https://hcaptcha.com https://*.hcaptcha.com",
			"img-src":     "'self' data: blob: https:",
			"font-src":    "'self' data:",
			"connect-src": "'self' https://esm.sh https://www.google-analytics.com https://hcaptcha.com https://*.hcaptcha.com https://*.dify.ai https://udify.app",
			"frame-src":   "'self' https://hcaptcha.com https://*.hcaptcha.com https://*.dify.ai https://udify.app",
			"object-src":  "'none'",
			"base-uri":    "'self'",
			"form-action": "'self'",
		})
		cfg.HSTSIncludeSubDomains = true
	}

	// Default permissions policy - restrict sensitive features
	cfg.PermissionsPolicy = buildPermissionsPolicy(map[string]string{
		"accelerometer":   "()",
		"camera":          "()",
		"geolocation":     "()",
		"gyroscope":       "()",
		"magnetometer":    "()",
		"microphone":      "()",
		"payment":         "()",
		"usb":             "()",
		"interest-cohort": "()", // Block FLoC
		"browsing-topics": "()", // Block Topics API
	})

	return cfg
}

// buildCSP builds a Content-Security-Policy string from a map of directives.
func buildCSP(directives map[string]string) string {
	var parts []string
	// Define order for consistent output
	order := []string{
		"default-src", "script-src", "style-src", "img-src", "font-src",
		"connect-src", "frame-src", "object-src", "base-uri", "form-action",
		"frame-ancestors", "upgrade-insecure-requests",
	}

	for _, key := range order {
		if value, ok := directives[key]; ok {
			parts = append(parts, key+" "+value)
		}
	}

	// Add any remaining directives not in the order list
	for key, value := range directives {
		found := false
		for _, ordered := range order {
			if key == ordered {
				found = true
				break
			}
		}
		if !found {
			parts = append(parts, key+" "+value)
		}
	}

	return strings.Join(parts, "; ")
}

// buildPermissionsPolicy builds a Permissions-Policy string from a map.
func buildPermissionsPolicy(policies map[string]string) string {
	var parts []string
	for key, value := range policies {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ", ")
}

// SecurityHeaders returns a middleware that adds security headers to responses.
func SecurityHeaders(cfg SecurityHeadersConfig) func(http.Handler) http.Handler {
	// Pre-build exclude paths map for faster lookup
	excludeMap := make(map[string]bool)
	for _, path := range cfg.ExcludePaths {
		excludeMap[path] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if path should be excluded
			if len(excludeMap) > 0 {
				for path := range excludeMap {
					if strings.HasPrefix(r.URL.Path, path) {
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// Content-Security-Policy
			if cfg.ContentSecurityPolicy != "" {
				w.Header().Set("Content-Security-Policy", cfg.ContentSecurityPolicy)
			}

			// Strict-Transport-Security (only in production over HTTPS)
			if !cfg.IsDevelopment && cfg.HSTSMaxAge > 0 {
				hsts := "max-age=" + intToStr(cfg.HSTSMaxAge)
				if cfg.HSTSIncludeSubDomains {
					hsts += "; includeSubDomains"
				}
				if cfg.HSTSPreload {
					hsts += "; preload"
				}
				w.Header().Set("Strict-Transport-Security", hsts)
			}

			// X-Frame-Options
			if cfg.FrameOptions != "" {
				w.Header().Set("X-Frame-Options", cfg.FrameOptions)
			}

			// X-Content-Type-Options - prevent MIME sniffing
			w.Header().Set("X-Content-Type-Options", "nosniff")

			// X-XSS-Protection - legacy but still useful for older browsers
			// Note: Modern browsers ignore this in favor of CSP
			w.Header().Set("X-XSS-Protection", "1; mode=block")

			// Referrer-Policy
			if cfg.ReferrerPolicy != "" {
				w.Header().Set("Referrer-Policy", cfg.ReferrerPolicy)
			}

			// Permissions-Policy (formerly Feature-Policy)
			if cfg.PermissionsPolicy != "" {
				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// intToStr is a simple integer to string conversion without importing strconv.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + intToStr(-n)
	}

	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
