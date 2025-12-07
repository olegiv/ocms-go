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
		{"/api/v1/pages", false},
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
