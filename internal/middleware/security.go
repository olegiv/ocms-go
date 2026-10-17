// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package middleware provides HTTP middleware for the OCMS application.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const cspNoncePlaceholder = "{{nonce}}"

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

	// CSPSources, when set, is called for every response and returns
	// third-party sources to add to ContentSecurityPolicy, keyed by directive.
	// It lets an active module allow the hosts it loads while the base policy
	// keeps scripts, connections and (apart from Google Fonts and YouTube)
	// styles and frames first-party. addCSPSources
	// accepts only host sources for directives the base policy already has.
	CSPSources func() map[string][]string

	// CrossOriginOpenerPolicy sets the Cross-Origin-Opener-Policy header.
	CrossOriginOpenerPolicy string

	// CrossOriginResourcePolicy sets the Cross-Origin-Resource-Policy header
	// on every response except those under PublicResourcePrefixes.
	CrossOriginResourcePolicy string

	// PublicResourcePrefixes are path prefixes whose responses other sites
	// may embed, such as uploaded images shown by feed readers. They are
	// sent with Cross-Origin-Resource-Policy: cross-origin.
	PublicResourcePrefixes []string
}

// DefaultSecurityHeadersConfig returns a SecurityHeadersConfig with sensible defaults.
func DefaultSecurityHeadersConfig(isDev bool) SecurityHeadersConfig {
	cfg := SecurityHeadersConfig{
		IsDevelopment:             isDev,
		HSTSMaxAge:                31536000, // 1 year
		FrameOptions:              "SAMEORIGIN",
		ReferrerPolicy:            "strict-origin-when-cross-origin",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		PublicResourcePrefixes:    []string{"/uploads/", "/static/", "/themes/"},
	}

	// Default CSP - allow self, inline styles (for CMS features), Google Fonts
	// for themes, YouTube frames for page content and images from any https:
	// host. Browsers report violations to /csp-report through report-uri,
	// where they are logged. report-to is deliberately absent: Chrome ignores
	// report-uri whenever report-to is present, and in testing it delivered
	// report-uri reports at once but sent nothing through report-to
	// (TestCSPReporting keeps report-to out). Hosts that a module loads
	// (analytics, hCaptcha) are added per request through CSPSources only
	// while that module is active (TestBaseCSPHostsAreFirstPartyOrContent).
	// unpkg.com and esm.sh were removed from both dev and prod CSPs: no script or
	// fetch in this repo or its embedded assets loads from them, so keeping them
	// in the allowlist only widened the supply-chain trust surface (FIND-007).
	if isDev {
		// More permissive in development for easier debugging
		cfg.ContentSecurityPolicy = buildCSP(map[string]string{
			"default-src":     "'self'",
			"script-src":      "'self' 'nonce-{{nonce}}' 'unsafe-inline' 'unsafe-eval'",
			"style-src":       "'self' 'unsafe-inline' https://fonts.googleapis.com",
			"img-src":         "'self' data: blob: https:",
			"font-src":        "'self' data: https://fonts.gstatic.com",
			"connect-src":     "'self'",
			"frame-src":       "'self' https://www.youtube.com https://www.youtube-nocookie.com",
			"object-src":      "'none'",
			"base-uri":        "'self'",
			"form-action":     "'self'",
			"frame-ancestors": "'self'",
			"report-uri":      "/csp-report",
		})
	} else {
		// Strict CSP for production.
		// upgrade-insecure-requests forces any http:// subresource in admin-
		// authored HTML up to https:// at request time (FIND-008).
		cfg.ContentSecurityPolicy = buildCSP(map[string]string{
			"default-src":               "'self'",
			"script-src":                "'self' 'nonce-{{nonce}}' 'unsafe-eval'",
			"style-src":                 "'self' 'unsafe-inline' https://fonts.googleapis.com",
			"img-src":                   "'self' data: blob: https:",
			"font-src":                  "'self' data: https://fonts.gstatic.com",
			"connect-src":               "'self'",
			"frame-src":                 "'self' https://www.youtube.com https://www.youtube-nocookie.com",
			"object-src":                "'none'",
			"base-uri":                  "'self'",
			"form-action":               "'self'",
			"frame-ancestors":           "'self'",
			"upgrade-insecure-requests": "",
			"report-uri":                "/csp-report",
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
// Directives with an empty value (e.g. upgrade-insecure-requests) are emitted
// as the bare directive name with no trailing space.
func buildCSP(directives map[string]string) string {
	var parts []string
	// Define order for consistent output
	order := []string{
		"default-src", "script-src", "style-src", "img-src", "font-src",
		"connect-src", "frame-src", "object-src", "base-uri", "form-action",
		"frame-ancestors", "upgrade-insecure-requests", "report-uri",
	}

	for _, key := range order {
		if value, ok := directives[key]; ok {
			parts = append(parts, cspPart(key, value))
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
			parts = append(parts, cspPart(key, value))
		}
	}

	return strings.Join(parts, "; ")
}

// cspPart renders a single CSP directive: `name value` when value is set,
// or just `name` for value-less directives like upgrade-insecure-requests.
func cspPart(name, value string) string {
	if value == "" {
		return name
	}
	return name + " " + value
}

// cspExtendableDirectives are the directives a module may add sources to.
// Other directives must never be widened (object-src 'none', base-uri,
// form-action, frame-ancestors), and a directive the policy lacks is not
// created, because it would lose its fallback to default-src 'self'.
var cspExtendableDirectives = map[string]bool{
	"script-src": true, "style-src": true, "img-src": true,
	"font-src": true, "connect-src": true, "frame-src": true,
}

// cspHostSource matches a scheme-qualified host source such as
// https://example.com, https://*.example.com or http://host:8080. It rejects
// anything that could widen a directive to every host ("https://*", "https:",
// keywords) or end it (";" or "," in a host). CSP host sources cannot name an
// IPv6 address, so neither does this.
var cspHostSource = regexp.MustCompile(`^https?://(\*\.)?[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:([0-9]{1,5}))?$`)

// IsCSPHostSource reports whether source is a host source that a module may
// add to the Content-Security-Policy (see cspHostSource), with a port, if
// any, between 1 and 65535. Settings that become CSP sources, such as a
// tracker URL, should be validated with it so the value an administrator
// saves is the value the policy keeps.
func IsCSPHostSource(source string) bool {
	match := cspHostSource.FindStringSubmatch(source)
	if match == nil {
		return false
	}
	if port := match[len(match)-1]; port != "" {
		n, err := strconv.Atoi(port)
		return err == nil && n >= 1 && n <= 65535
	}
	return true
}

// rejectedCSPSources remembers rejected sources so each is logged once, not on
// every response.
var rejectedCSPSources sync.Map

// addCSPSources appends module sources to the matching directives of policy.
// Only host sources for directives in cspExtendableDirectives that the policy
// already has are added; anything else is dropped and logged once, so a bad
// value from a module or its settings cannot widen or break the header.
func addCSPSources(policy string, sources map[string][]string) string {
	if len(sources) == 0 {
		return policy
	}
	parts := strings.Split(policy, "; ")
	present := make(map[string]int, len(parts))
	for i, part := range parts {
		name, _, _ := strings.Cut(part, " ")
		present[name] = i
	}
	for name, values := range sources {
		i, ok := present[name]
		for _, value := range values {
			switch {
			case !cspExtendableDirectives[name] || !ok:
				rejectCSPSource(name, value, "directive cannot be extended")
			case !IsCSPHostSource(value):
				rejectCSPSource(name, value, "not a host source")
			default:
				parts[i] += " " + value
			}
		}
	}
	return strings.Join(parts, "; ")
}

// rejectCSPSource logs a dropped CSP source the first time it is seen.
func rejectCSPSource(directive, source, reason string) {
	if _, seen := rejectedCSPSources.LoadOrStore(directive+" "+source, true); !seen {
		slog.Warn("ignoring CSP source", "directive", directive, "source", source, "reason", reason)
	}
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
			nonce := generateCSPNonce()
			r = r.WithContext(context.WithValue(r.Context(), ContextKeyCSPNonce, nonce))

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
				policy := cfg.ContentSecurityPolicy
				if cfg.CSPSources != nil {
					policy = addCSPSources(policy, cfg.CSPSources())
				}
				w.Header().Set("Content-Security-Policy", applyCSPNonce(policy, nonce))
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

			// X-XSS-Protection: 0 turns off the legacy XSS auditor of older
			// browsers, whose blocking mode could be abused to leak page
			// state; CSP is the protection that modern browsers enforce.
			w.Header().Set("X-XSS-Protection", "0")

			// Referrer-Policy
			if cfg.ReferrerPolicy != "" {
				w.Header().Set("Referrer-Policy", cfg.ReferrerPolicy)
			}

			// Permissions-Policy (formerly Feature-Policy)
			if cfg.PermissionsPolicy != "" {
				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
			}

			// Cross-Origin-Opener-Policy isolates the browsing context from
			// cross-origin windows that open it or that it opens.
			if cfg.CrossOriginOpenerPolicy != "" {
				w.Header().Set("Cross-Origin-Opener-Policy", cfg.CrossOriginOpenerPolicy)
			}

			// Cross-Origin-Resource-Policy keeps other sites from embedding
			// pages and API responses, except public media and assets.
			if cfg.CrossOriginResourcePolicy != "" {
				resourcePolicy := cfg.CrossOriginResourcePolicy
				for _, prefix := range cfg.PublicResourcePrefixes {
					if strings.HasPrefix(r.URL.Path, prefix) {
						resourcePolicy = "cross-origin"
						break
					}
				}
				w.Header().Set("Cross-Origin-Resource-Policy", resourcePolicy)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetCSPNonce retrieves the request CSP nonce from context.
// Returns empty string if not present.
func GetCSPNonce(r *http.Request) string {
	nonce, ok := r.Context().Value(ContextKeyCSPNonce).(string)
	if !ok {
		return ""
	}
	return nonce
}

func applyCSPNonce(policy, nonce string) string {
	if policy == "" || nonce == "" || !strings.Contains(policy, cspNoncePlaceholder) {
		return policy
	}
	return strings.ReplaceAll(policy, cspNoncePlaceholder, nonce)
}

func generateCSPNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		slog.Error("failed to generate CSP nonce", "error", err)
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(b[:])
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
