// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	tests := []struct {
		name        string
		isDev       bool
		wantHSTS    bool
		wantCSP     bool
		wantFrame   bool
		wantNosniff bool
	}{
		{
			name:        "production mode enables all headers",
			isDev:       false,
			wantHSTS:    true,
			wantCSP:     true,
			wantFrame:   true,
			wantNosniff: true,
		},
		{
			name:        "development mode disables HSTS",
			isDev:       true,
			wantHSTS:    false,
			wantCSP:     true,
			wantFrame:   true,
			wantNosniff: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultSecurityHeadersConfig(tt.isDev)
			handler := SecurityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			// Check HSTS header
			hsts := rec.Header().Get("Strict-Transport-Security")
			if tt.wantHSTS && hsts == "" {
				t.Error("expected HSTS header but got none")
			}
			if !tt.wantHSTS && hsts != "" {
				t.Errorf("expected no HSTS header but got: %s", hsts)
			}

			// Check CSP header
			csp := rec.Header().Get("Content-Security-Policy")
			if tt.wantCSP && csp == "" {
				t.Error("expected CSP header but got none")
			}
			if tt.wantCSP && !strings.Contains(csp, "default-src") {
				t.Error("CSP should contain default-src directive")
			}
			if tt.wantCSP && !strings.Contains(csp, "nonce-") {
				t.Error("CSP should contain nonce in script-src")
			}

			// Check X-Frame-Options header
			frame := rec.Header().Get("X-Frame-Options")
			if tt.wantFrame && frame != "SAMEORIGIN" {
				t.Errorf("expected X-Frame-Options: SAMEORIGIN, got: %s", frame)
			}

			// Check X-Content-Type-Options header
			nosniff := rec.Header().Get("X-Content-Type-Options")
			if tt.wantNosniff && nosniff != "nosniff" {
				t.Errorf("expected X-Content-Type-Options: nosniff, got: %s", nosniff)
			}
		})
	}
}

func TestSecurityHeadersExcludePaths(t *testing.T) {
	cfg := DefaultSecurityHeadersConfig(false)
	cfg.ExcludePaths = []string{"/api/"}

	handler := SecurityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		path        string
		wantHeaders bool
	}{
		{"/", true},
		{"/admin", true},
		{"/api/v2/pages", false},
		{"/api/health", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			csp := rec.Header().Get("Content-Security-Policy")
			if tt.wantHeaders && csp == "" {
				t.Errorf("expected CSP header for path %s", tt.path)
			}
			if !tt.wantHeaders && csp != "" {
				t.Errorf("expected no CSP header for path %s, got: %s", tt.path, csp)
			}
		})
	}
}

func TestSecurityHeadersHSTSOptions(t *testing.T) {
	cfg := SecurityHeadersConfig{
		IsDevelopment:         false,
		HSTSMaxAge:            63072000, // 2 years
		HSTSIncludeSubDomains: true,
		HSTSPreload:           true,
	}

	handler := SecurityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	hsts := rec.Header().Get("Strict-Transport-Security")
	if !strings.Contains(hsts, "max-age=63072000") {
		t.Errorf("HSTS should contain max-age=63072000, got: %s", hsts)
	}
	if !strings.Contains(hsts, "includeSubDomains") {
		t.Errorf("HSTS should contain includeSubDomains, got: %s", hsts)
	}
	if !strings.Contains(hsts, "preload") {
		t.Errorf("HSTS should contain preload, got: %s", hsts)
	}
}

func TestSecurityHeadersAllHeadersPresent(t *testing.T) {
	cfg := DefaultSecurityHeadersConfig(false)
	handler := SecurityHeaders(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	requiredHeaders := []string{
		"Content-Security-Policy",
		"Strict-Transport-Security",
		"X-Frame-Options",
		"X-Content-Type-Options",
		"X-XSS-Protection",
		"Referrer-Policy",
		"Permissions-Policy",
	}

	for _, header := range requiredHeaders {
		if rec.Header().Get(header) == "" {
			t.Errorf("missing required header: %s", header)
		}
	}
}

func TestBuildCSP(t *testing.T) {
	directives := map[string]string{
		"default-src": "'self'",
		"script-src":  "'self' 'unsafe-inline'",
		"img-src":     "'self' data:",
	}

	csp := buildCSP(directives)

	if !strings.Contains(csp, "default-src 'self'") {
		t.Error("CSP should contain default-src directive")
	}
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("CSP should contain script-src directive")
	}
	if !strings.Contains(csp, "img-src 'self' data:") {
		t.Error("CSP should contain img-src directive")
	}
	if !strings.Contains(csp, "; ") {
		t.Error("CSP directives should be separated by semicolons")
	}
}

func TestDefaultSecurityHeadersConfig_ProductionCSPIsStrict(t *testing.T) {
	cfg := DefaultSecurityHeadersConfig(false)
	if strings.Contains(cfg.ContentSecurityPolicy, "script-src") &&
		strings.Contains(cfg.ContentSecurityPolicy, "script-src 'self' 'unsafe-inline'") {
		t.Error("production CSP must not allow 'unsafe-inline' for scripts")
	}
}

func TestDefaultSecurityHeadersConfig_DevelopmentCSPAllowsUnsafeDirectives(t *testing.T) {
	cfg := DefaultSecurityHeadersConfig(true)
	if !strings.Contains(cfg.ContentSecurityPolicy, "'unsafe-inline'") {
		t.Error("development CSP should allow 'unsafe-inline' for DX")
	}
	if !strings.Contains(cfg.ContentSecurityPolicy, "'unsafe-eval'") {
		t.Error("development CSP should allow 'unsafe-eval' for DX")
	}
}

func TestSecurityHeadersSetsCSPNonceInContext(t *testing.T) {
	cfg := DefaultSecurityHeadersConfig(false)
	var capturedNonce string

	handler := SecurityHeaders(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedNonce = GetCSPNonce(r)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if capturedNonce == "" {
		t.Fatal("expected nonce in request context")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-"+capturedNonce+"'") {
		t.Fatalf("expected CSP header to include request nonce, got: %s", csp)
	}
}

func TestCSPAllowsYouTubeNoCookieFrames(t *testing.T) {
	for _, isDev := range []bool{true, false} {
		cfg := DefaultSecurityHeadersConfig(isDev)
		if !strings.Contains(cfg.ContentSecurityPolicy, "https://www.youtube-nocookie.com") {
			t.Errorf("CSP (dev=%t) should allow youtube-nocookie.com in frame-src", isDev)
		}
	}
}

func TestIntToStr(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{12, "12"},
		{123, "123"},
		{31536000, "31536000"},
		{-1, "-1"},
		{-123, "-123"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := intToStr(tt.input)
			if result != tt.expected {
				t.Errorf("intToStr(%d) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

// TestCSPStyleSrcDoesNotAllowUnpkg locks in the "self-hosted Swagger UI"
// guarantee: style-src stays on 'self' + the existing trusted origins, never
// widened to unpkg. Prevents regressing to a CDN-hosted docs page.
func TestCSPStyleSrcDoesNotAllowUnpkg(t *testing.T) {
	for _, isDev := range []bool{false, true} {
		cfg := DefaultSecurityHeadersConfig(isDev)
		csp := cfg.ContentSecurityPolicy
		styleSrc := ""
		for _, dir := range strings.Split(csp, ";") {
			d := strings.TrimSpace(dir)
			if strings.HasPrefix(d, "style-src ") {
				styleSrc = d
				break
			}
		}
		if styleSrc == "" {
			t.Fatalf("isDev=%v: no style-src directive in CSP: %s", isDev, csp)
		}
		if strings.Contains(styleSrc, "https://unpkg.com") {
			t.Errorf("isDev=%v: style-src must not allow unpkg (Swagger UI is self-hosted), got: %s", isDev, styleSrc)
		}
	}
}

// TestCSPDoesNotAllowUnusedCDNs is the drift test for audit finding FIND-007:
// no script, fetch, or asset in this repo loads from unpkg.com or esm.sh, so
// the CSP must not list them. If a future change reintroduces a CDN origin
// without the matching asset, this test fails.
func TestCSPDoesNotAllowUnusedCDNs(t *testing.T) {
	bannedOrigins := []string{"https://unpkg.com", "https://esm.sh"}
	for _, isDev := range []bool{false, true} {
		csp := DefaultSecurityHeadersConfig(isDev).ContentSecurityPolicy
		for _, origin := range bannedOrigins {
			if strings.Contains(csp, origin) {
				t.Errorf("isDev=%v: CSP must not allow %s (unused CDN); got: %s", isDev, origin, csp)
			}
		}
	}
}

// TestCSPProductionIncludesUpgradeInsecureRequests is the drift test for
// audit finding FIND-008: the production CSP must tell browsers to auto-
// upgrade http:// subresources in admin-authored HTML. Dev is intentionally
// unconstrained so http://localhost fetches still work.
func TestCSPProductionIncludesUpgradeInsecureRequests(t *testing.T) {
	prodCSP := DefaultSecurityHeadersConfig(false).ContentSecurityPolicy

	found := false
	for _, dir := range strings.Split(prodCSP, ";") {
		if strings.TrimSpace(dir) == "upgrade-insecure-requests" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("production CSP must include upgrade-insecure-requests; got: %s", prodCSP)
	}

	devCSP := DefaultSecurityHeadersConfig(true).ContentSecurityPolicy
	if strings.Contains(devCSP, "upgrade-insecure-requests") {
		t.Errorf("development CSP should NOT include upgrade-insecure-requests (breaks http://localhost); got: %s", devCSP)
	}
}

// TestCSPBuildEmitsBareDirectiveForEmptyValue pins the serialization shape of
// a value-less directive: `upgrade-insecure-requests` must appear as a bare
// token, not `upgrade-insecure-requests ` with a trailing space. A permissive
// CSP serializer that emits the trailing-space form is accepted by modern
// browsers but breaks hash-based CSP verifiers and audit tools.
func TestCSPBuildEmitsBareDirectiveForEmptyValue(t *testing.T) {
	csp := buildCSP(map[string]string{
		"default-src":               "'self'",
		"upgrade-insecure-requests": "",
	})
	if !strings.Contains(csp, "upgrade-insecure-requests") {
		t.Fatalf("buildCSP dropped the value-less directive: %q", csp)
	}
	if strings.Contains(csp, "upgrade-insecure-requests ;") || strings.HasSuffix(csp, "upgrade-insecure-requests ") {
		t.Errorf("buildCSP emitted trailing space for value-less directive: %q", csp)
	}
}
