// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecureUploads_BlocksActiveExtensions(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/uploads/originals/u/payload.svg", nil)
	rr := httptest.NewRecorder()

	SecureUploads(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
	if called {
		t.Fatal("next handler should not be called for blocked extension")
	}
}

func TestSecureUploads_AllowsInlineImage(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/uploads/originals/u/photo.JPG", nil)
	rr := httptest.NewRecorder()

	SecureUploads(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want empty", got)
	}
}

func TestSecureUploads_ForcesAttachmentForNonInline(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/uploads/originals/u/report.pdf", nil)
	rr := httptest.NewRecorder()

	SecureUploads(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename="report.pdf"` {
		t.Fatalf("Content-Disposition = %q, want %q", got, `attachment; filename="report.pdf"`)
	}
	if got := rr.Header().Get("X-Download-Options"); got != "noopen" {
		t.Fatalf("X-Download-Options = %q, want %q", got, "noopen")
	}
}

func TestSanitizeContentDispositionFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "download"},
		{name: "control chars stripped", in: "bad\r\nname.txt", want: "badname.txt"},
		{name: "dangerous chars replaced", in: `bad"name;v.txt`, want: "bad_name_v.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeContentDispositionFilename(tt.in)
			if got != tt.want {
				t.Fatalf("sanitizeContentDispositionFilename(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
