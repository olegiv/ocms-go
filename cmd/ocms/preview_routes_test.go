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
