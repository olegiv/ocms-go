// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAuthLoggerRedactsQueriesAndPreservesHandlerInput(t *testing.T) {
	type loggerContextKey struct{}
	for _, path := range []string{"/oauth/authorize", "/oauth/token", "/oauth/revoke", "/oauth/register"} {
		t.Run(path, func(t *testing.T) {
			var logged, received string
			logger := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					logged = r.RequestURI + " " + r.URL.String()
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loggerContextKey{}, true)))
				})
			}
			h := RedactedRequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received = r.URL.RawQuery
				if r.Context().Value(loggerContextKey{}) != true {
					t.Fatal("logger context lost")
				}
				w.WriteHeader(204)
			}))
			r := httptest.NewRequest(http.MethodGet, path+"?code=secret-code&state=secret-state&code_verifier=secret-verifier", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if strings.Contains(logged, "secret") || !strings.Contains(logged, path) {
				t.Fatal("OAuth credentials reached access log")
			}
			if !strings.Contains(received, "secret-code") || w.Code != 204 {
				t.Fatal("redaction changed the authorization request")
			}
		})
	}
}
