// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLinkHeadersHomepage(t *testing.T) {
	var called bool
	handler := LinkHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("downstream handler was not invoked")
	}

	link := rec.Header().Get("Link")
	if link == "" {
		t.Fatal("Link header not set on homepage request")
	}
	for _, want := range []string{
		`rel="api-catalog"`,
		`rel="service-desc"`,
		`rel="service-doc"`,
		`</.well-known/api-catalog>`,
		`</api/v2/openapi.json>`,
		`</api/v2/docs>`,
	} {
		if !strings.Contains(link, want) {
			t.Errorf("Link header missing %q; full value:\n%s", want, link)
		}
	}
}

func TestLinkHeadersNotSetOnOtherPaths(t *testing.T) {
	handler := LinkHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/about", "/blog/post-slug", "/admin", "/api/v2/pages", "/robots.txt"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Header().Get("Link"); got != "" {
				t.Errorf("Link header should be empty on %s; got %q", path, got)
			}
		})
	}
}
