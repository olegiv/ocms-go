// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/preview"
)

func TestRegisteredShareRoutesNeverAcceptURLCredentials(t *testing.T) {
	router := chi.NewRouter()
	router.Use(preview.RedactRequest, middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(true)), preview.Middleware, middleware.StripTrailingSlash)
	registerPreviewRoutes(router, &handler.FrontendHandler{})
	secret := strings.Repeat("A", 43)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, preview.SharePath, 200},
		{http.MethodPost, preview.SharePath, 405},
		{http.MethodPost, preview.ShareOpenPath, 404},
		{http.MethodGet, preview.ShareOpenPath, 405},
		{http.MethodGet, preview.SharePath + "/" + secret, 404},
		{http.MethodPost, preview.SharePath + "/" + secret, 404},
		{http.MethodGet, preview.SharePath + "/" + secret + "/", 404},
		{http.MethodGet, preview.SharePath + "?token=" + secret, 200}, // Static shell only; query never authenticates.
		{http.MethodGet, preview.SharePath + "?utm_source=chatgpt.com", 200},
		{http.MethodGet, "/preview/%73hare", 404},
		{http.MethodGet, "/ru/preview/share", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), secret) {
				t.Fatalf("status=%d want=%d, unsafe URL was accepted, reflected or redirected", w.Code, tc.status)
			}
			if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("registered preview route lost its privacy headers")
			}
		})
	}
}

// TestShareRateLimitAnswersBrowsersInPlainText fails when the share landing or
// its POST answer a rate-limited visitor with the API's JSON error. Both are
// pages people open in a browser; a person behind a shared NAT address saw a
// JSON object instead of a message (security audit 2026-11-03, C-7).
//
// Bug state: use limiter.Middleware() instead of limiter.HTMLMiddleware() in
// registerPreviewRoutes and this reports application/json for both routes.
func TestShareRateLimitAnswersBrowsersInPlainText(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, preview.SharePath},
		{http.MethodPost, preview.ShareOpenPath},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			router := chi.NewRouter() // a fresh router has a fresh limiter
			router.Use(preview.RedactRequest, middleware.SecurityHeaders(middleware.DefaultSecurityHeadersConfig(true)), preview.Middleware)
			registerPreviewRoutes(router, &handler.FrontendHandler{})
			var limited *httptest.ResponseRecorder
			for range 100 { // the limiter's burst is 20
				request := httptest.NewRequest(tc.method, tc.path, nil)
				request.RemoteAddr = "192.0.2.77:4321"
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code == http.StatusTooManyRequests {
					limited = response
					break
				}
			}
			if limited == nil {
				t.Fatal("the share route was never rate limited, so this test no longer exercises the limiter")
			}
			contentType := limited.Header().Get("Content-Type")
			body := limited.Body.String()
			if strings.Contains(contentType, "json") || strings.HasPrefix(strings.TrimSpace(body), "{") || !strings.HasPrefix(contentType, "text/plain") || !strings.Contains(body, "Too many requests") {
				t.Fatalf("rate-limited share request got %q %q, want the plain-text page answer", contentType, body)
			}
			if limited.Header().Get("Cache-Control") != "private, no-store" || limited.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("rate-limited share response lost its privacy headers")
			}
		})
	}
}
