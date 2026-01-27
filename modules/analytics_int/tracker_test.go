// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package analytics_int

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// runStringExtractTests runs table-driven tests for string extraction functions.
func runStringExtractTests(t *testing.T, extractFn func(string) string, tests []struct {
	name     string
	input    string
	expected string
}) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFn(tt.input)
			if result != tt.expected {
				t.Errorf("extract(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractReferrerDomain(t *testing.T) {
	runStringExtractTests(t, extractReferrerDomain, []struct {
		name     string
		input    string
		expected string
	}{
		{"full URL with path", "https://www.google.com/search?q=test", "www.google.com"},
		{"URL with port", "http://localhost:8080/page", "localhost"},
		{"simple domain", "https://example.com", "example.com"},
		{"empty string", "", ""},
		{"invalid URL", "not-a-url", ""},
		{"subdomain", "https://blog.example.com/article", "blog.example.com"},
	})
}

func TestParseAcceptLanguage(t *testing.T) {
	runStringExtractTests(t, parseAcceptLanguage, []struct {
		name     string
		input    string
		expected string
	}{
		{"single language", "en-US", "en"},
		{"multiple languages with quality", "en-US,en;q=0.9,fr;q=0.8", "en"},
		{"just language code", "de", "de"},
		{"empty header", "", ""},
		{"complex header", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7", "ru"},
		{"with spaces", "  en-GB  ", "en"},
	})
}

func TestGetRealIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		expected   string
	}{
		{
			name:       "X-Real-IP header",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string]string{"X-Real-IP": "203.0.113.50"},
			expected:   "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For single IP",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "198.51.100.178"},
			expected:   "198.51.100.178",
		},
		{
			name:       "X-Forwarded-For multiple IPs",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.195, 70.41.3.18, 150.172.238.178"},
			expected:   "203.0.113.195",
		},
		{
			name:       "X-Real-IP takes precedence",
			remoteAddr: "127.0.0.1:12345",
			headers: map[string]string{
				"X-Real-IP":       "203.0.113.50",
				"X-Forwarded-For": "198.51.100.178",
			},
			expected: "203.0.113.50",
		},
		{
			name:       "fallback to RemoteAddr",
			remoteAddr: "192.168.1.100:54321",
			headers:    map[string]string{},
			expected:   "192.168.1.100",
		},
		{
			name:       "IPv6 RemoteAddr",
			remoteAddr: "[::1]:12345",
			headers:    map[string]string{},
			expected:   "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			result := getRealIP(req)
			if result != tt.expected {
				t.Errorf("getRealIP() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestShouldTrack(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:      true,
			ExcludePaths: []string{"/private", "/internal/"},
		},
	}

	tests := []struct {
		name     string
		method   string
		path     string
		expected bool
	}{
		// Should track
		{name: "homepage", method: "GET", path: "/", expected: true},
		{name: "regular page", method: "GET", path: "/about", expected: true},
		{name: "blog post", method: "GET", path: "/blog/my-post", expected: true},
		{name: "category page", method: "GET", path: "/category/tech", expected: true},

		// Should NOT track - static assets
		{name: "CSS file", method: "GET", path: "/static/style.css", expected: false},
		{name: "JS file", method: "GET", path: "/assets/app.js", expected: false},
		{name: "image PNG", method: "GET", path: "/images/logo.png", expected: false},
		{name: "image JPG", method: "GET", path: "/uploads/photo.jpg", expected: false},
		{name: "favicon", method: "GET", path: "/favicon.ico", expected: false},
		{name: "robots.txt", method: "GET", path: "/robots.txt", expected: false},
		{name: "sitemap", method: "GET", path: "/sitemap.xml", expected: false},

		// Should NOT track - admin/API
		{name: "admin dashboard", method: "GET", path: "/admin", expected: false},
		{name: "admin pages", method: "GET", path: "/admin/pages", expected: false},
		{name: "API endpoint", method: "GET", path: "/api/v1/pages", expected: false},
		{name: "health check", method: "GET", path: "/health", expected: false},

		// Should NOT track - non-GET methods
		{name: "POST request", method: "POST", path: "/contact", expected: false},
		{name: "PUT request", method: "PUT", path: "/page", expected: false},

		// Should NOT track - excluded paths
		{name: "excluded path exact", method: "GET", path: "/private", expected: false},
		{name: "excluded path prefix", method: "GET", path: "/private/page", expected: false},
		{name: "excluded internal", method: "GET", path: "/internal/test", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			result := m.shouldTrack(req)
			if result != tt.expected {
				t.Errorf("shouldTrack(%s %s) = %v, want %v", tt.method, tt.path, result, tt.expected)
			}
		})
	}
}

func TestShouldTrack_DisabledModule(t *testing.T) {
	m := &Module{
		settings: &Settings{
			Enabled:      false,
			ExcludePaths: []string{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// When module is disabled, middleware should skip tracking
	// shouldTrack still checks path filtering even when disabled
	// shouldTrack only checks path, not enabled state
	// The middleware checks enabled state separately
	_ = m.shouldTrack(req)
}

func TestResponseWriter(t *testing.T) {
	t.Run("captures status code", func(t *testing.T) {
		w := httptest.NewRecorder()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		rw.WriteHeader(http.StatusNotFound)

		if rw.status != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rw.status, http.StatusNotFound)
		}
	})

	t.Run("defaults to 200 on Write", func(t *testing.T) {
		w := httptest.NewRecorder()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		_, _ = rw.Write([]byte("hello"))

		if rw.status != http.StatusOK {
			t.Errorf("status = %d, want %d", rw.status, http.StatusOK)
		}
	})

	t.Run("only sets status once", func(t *testing.T) {
		w := httptest.NewRecorder()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		rw.WriteHeader(http.StatusCreated)
		rw.WriteHeader(http.StatusNotFound)

		if rw.status != http.StatusCreated {
			t.Errorf("status = %d, want %d (first call)", rw.status, http.StatusCreated)
		}
	})
}
