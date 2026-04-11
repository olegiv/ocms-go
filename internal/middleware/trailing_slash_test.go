// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripTrailingSlash(t *testing.T) {
	handler := StripTrailingSlash(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name           string
		target         string
		wantStatus     int
		wantLocation   string
		wantRedirected bool
	}{
		{
			name:           "redirects path with trailing slash",
			target:         "/admin/",
			wantStatus:     http.StatusMovedPermanently,
			wantLocation:   "/admin",
			wantRedirected: true,
		},
		{
			name:           "preserves query string in redirect",
			target:         "/admin/?tab=users",
			wantStatus:     http.StatusMovedPermanently,
			wantLocation:   "/admin?tab=users",
			wantRedirected: true,
		},
		{
			name:           "normalizes scheme-relative path to same-origin",
			target:         "//evil.com/",
			wantStatus:     http.StatusMovedPermanently,
			wantLocation:   "/evil.com",
			wantRedirected: true,
		},
		{
			name:           "does not redirect root path",
			target:         "/",
			wantStatus:     http.StatusOK,
			wantLocation:   "",
			wantRedirected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			location := rec.Header().Get("Location")
			if location != tt.wantLocation {
				t.Fatalf("expected Location %q, got %q", tt.wantLocation, location)
			}
		})
	}
}
