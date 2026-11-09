// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

func TestAccessLogBoundsOnlyItsRequestCopy(t *testing.T) {
	path := "/" + strings.Repeat("x", 2000)
	host := strings.Repeat("h", 500)
	requestID := "\u202e\u2028\u2029" + strings.Repeat("i", 500)
	h := RedactedRequestLogger(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.RequestURI != path[:256] || r.Host != host[:256] ||
				chimw.GetReqID(r.Context()) != strings.Repeat("i", 256) {
				t.Fatal("logger received unbounded request fields")
			}
			next.ServeHTTP(w, r)
		})
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != path || r.URL.Path != path || r.Host != host ||
			chimw.GetReqID(r.Context()) != requestID {
			t.Fatal("handler request fields changed")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = host
	r = r.WithContext(context.WithValue(r.Context(), chimw.RequestIDKey, requestID))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("handler returned %d", w.Code)
	}
}

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
