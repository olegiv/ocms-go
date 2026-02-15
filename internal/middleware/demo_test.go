// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// resetDemoMode resets the demo mode state for testing.
func resetDemoMode() {
	demoMode = false
	demoModeOnce = sync.Once{}
}

func TestIsDemoMode(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "demo mode enabled",
			envValue: "true",
			want:     true,
		},
		{
			name:     "demo mode disabled",
			envValue: "false",
			want:     false,
		},
		{
			name:     "demo mode not set",
			envValue: "",
			want:     false,
		},
		{
			name:     "demo mode invalid value",
			envValue: "yes",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDemoMode()
			if tt.envValue != "" {
				t.Setenv("OCMS_DEMO_MODE", tt.envValue)
			}

			got := IsDemoMode()
			if got != tt.want {
				t.Errorf("IsDemoMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDemoModeMessageDetailed(t *testing.T) {
	tests := []struct {
		restriction DemoRestriction
		wantContains string
	}{
		{RestrictionDeletePage, "Deleting pages"},
		{RestrictionDeleteMedia, "Deleting media"},
		{RestrictionAPIKeys, "API key"},
		{RestrictionWebhooks, "Webhook"},
		{RestrictionThemeSettings, "theme settings"},
		{DemoRestriction("unknown"), "disabled in demo mode"},
	}

	for _, tt := range tests {
		t.Run(string(tt.restriction), func(t *testing.T) {
			got := DemoModeMessageDetailed(tt.restriction)
			if got == "" {
				t.Error("DemoModeMessageDetailed() returned empty string")
			}
			if !contains(got, tt.wantContains) {
				t.Errorf("DemoModeMessageDetailed() = %q, want to contain %q", got, tt.wantContains)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || s != "" && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestBlockInDemoMode(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	tests := []struct {
		name       string
		demoMode   bool
		path       string
		wantStatus int
	}{
		{
			name:       "demo mode off - request allowed",
			demoMode:   false,
			path:       "/admin/users/new",
			wantStatus: http.StatusOK,
		},
		{
			name:       "demo mode on - web request redirected",
			demoMode:   true,
			path:       "/admin/users/new",
			wantStatus: http.StatusSeeOther,
		},
		{
			name:       "demo mode on - API request blocked",
			demoMode:   true,
			path:       "/api/v1/users",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDemoMode()
			if tt.demoMode {
				t.Setenv("OCMS_DEMO_MODE", "true")
			}

			middleware := BlockInDemoMode(RestrictionCreateUser)
			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			req.Header.Set("Referer", "/admin/users")
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", rec.Code, tt.wantStatus)
			}

			// Check for demo_blocked cookie on redirect
			if tt.demoMode && !isAPIPath(tt.path) {
				cookies := rec.Result().Cookies()
				found := false
				for _, c := range cookies {
					if c.Name == "demo_blocked" {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected demo_blocked cookie to be set")
				}
			}
		})
	}
}

func isAPIPath(path string) bool {
	return len(path) >= 5 && path[:5] == "/api/"
}

// demoMiddlewareTestCase defines a test case for demo mode middleware.
type demoMiddlewareTestCase struct {
	name       string
	demoMode   bool
	method     string
	path       string
	wantStatus int
}

// runDemoMiddlewareTests runs a set of test cases against a demo mode middleware factory.
func runDemoMiddlewareTests(t *testing.T, makeMiddleware func() func(http.Handler) http.Handler, referer string, tests []demoMiddlewareTestCase) {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetDemoMode()
			if tt.demoMode {
				t.Setenv("OCMS_DEMO_MODE", "true")
			}

			mw := makeMiddleware()
			wrapped := mw(handler)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Referer", referer)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestBlockDeleteInDemoMode(t *testing.T) {
	runDemoMiddlewareTests(t, func() func(http.Handler) http.Handler {
		return BlockDeleteInDemoMode(RestrictionDeletePage)
	}, "/admin/pages", []demoMiddlewareTestCase{
		{"demo mode off - DELETE allowed", false, http.MethodDelete, "/admin/pages/1", http.StatusOK},
		{"demo mode on - GET allowed", true, http.MethodGet, "/admin/pages/1", http.StatusOK},
		{"demo mode on - POST to edit allowed", true, http.MethodPost, "/admin/pages/1/edit", http.StatusOK},
		{"demo mode on - DELETE blocked", true, http.MethodDelete, "/admin/pages/1", http.StatusSeeOther},
		{"demo mode on - POST to delete blocked", true, http.MethodPost, "/admin/pages/1/delete", http.StatusSeeOther},
		{"demo mode on - API DELETE blocked", true, http.MethodDelete, "/api/v1/pages/1", http.StatusForbidden},
	})
}

func TestBlockWriteInDemoMode(t *testing.T) {
	runDemoMiddlewareTests(t, func() func(http.Handler) http.Handler {
		return BlockWriteInDemoMode(RestrictionEditConfig)
	}, "/admin/config", []demoMiddlewareTestCase{
		{"demo mode off - POST allowed", false, http.MethodPost, "/admin/config", http.StatusOK},
		{"demo mode on - GET allowed", true, http.MethodGet, "/admin/config", http.StatusOK},
		{"demo mode on - HEAD allowed", true, http.MethodHead, "/admin/config", http.StatusOK},
		{"demo mode on - POST blocked", true, http.MethodPost, "/admin/config", http.StatusSeeOther},
		{"demo mode on - PUT blocked", true, http.MethodPut, "/admin/config", http.StatusSeeOther},
		{"demo mode on - API POST blocked", true, http.MethodPost, "/api/v1/config", http.StatusForbidden},
	})
}

func TestGetDemoBlockedMessage(t *testing.T) {
	t.Run("no cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
		rec := httptest.NewRecorder()

		msg := GetDemoBlockedMessage(rec, req)
		if msg != "" {
			t.Errorf("expected empty message, got %q", msg)
		}
	})

	t.Run("with cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
		req.AddCookie(&http.Cookie{
			Name:  "demo_blocked",
			Value: string(RestrictionDeletePage),
		})
		rec := httptest.NewRecorder()

		msg := GetDemoBlockedMessage(rec, req)
		if msg == "" {
			t.Error("expected message, got empty")
		}
		if !contains(msg, "Deleting pages") {
			t.Errorf("expected message about deleting pages, got %q", msg)
		}

		// Check cookie was cleared
		cookies := rec.Result().Cookies()
		for _, c := range cookies {
			if c.Name == "demo_blocked" && c.MaxAge != -1 {
				t.Error("expected cookie to be cleared")
			}
		}
	})
}
