// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaticCache(t *testing.T) {
	tests := []struct {
		name   string
		maxAge int
		want   string
	}{
		{
			name:   "one hour",
			maxAge: 3600,
			want:   "public, max-age=3600",
		},
		{
			name:   "one day",
			maxAge: 86400,
			want:   "public, max-age=86400",
		},
		{
			name:   "one week",
			maxAge: 604800,
			want:   "public, max-age=604800",
		},
		{
			name:   "zero",
			maxAge: 0,
			want:   "public, max-age=0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := StaticCache(tt.maxAge)
			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/static/file.js", nil)
			rr := httptest.NewRecorder()

			wrapped.ServeHTTP(rr, req)

			got := rr.Header().Get("Cache-Control")
			if got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStaticCachePreservesResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console.log('test')"))
	})

	middleware := StaticCache(3600)
	wrapped := middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/static/file.js", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	// Check status code preserved
	if rr.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Check content type preserved
	if ct := rr.Header().Get("Content-Type"); ct != "application/javascript" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/javascript")
	}

	// Check body preserved
	if body := rr.Body.String(); body != "console.log('test')" {
		t.Errorf("Body = %q, want %q", body, "console.log('test')")
	}

	// Check cache header added
	if cc := rr.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=3600")
	}
}
