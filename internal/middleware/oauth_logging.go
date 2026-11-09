// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"context"
	"net/http"
	"strings"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/olegiv/ocms-go/internal/util"
)

// RedactedRequestLogger bounds the access logger's request fields and hides
// OAuth queries. Handlers receive the original values and the logger context.
func RedactedRequestLogger(logger func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		ordinary := logger(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			oauth := strings.HasPrefix(r.URL.Path, "/oauth/")
			uri := r.RequestURI
			if oauth {
				uri = r.URL.EscapedPath()
			}
			uri = util.BoundedLogField(uri)
			method, host := util.BoundedLogField(r.Method), util.BoundedLogField(r.Host)
			remote, proto := util.BoundedLogField(r.RemoteAddr), util.BoundedLogField(r.Proto)
			requestID := chimw.GetReqID(r.Context())
			logID := util.BoundedLogField(requestID)
			if !oauth && uri == r.RequestURI && method == r.Method && host == r.Host &&
				remote == r.RemoteAddr && proto == r.Proto && requestID == logID {
				ordinary.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			if logID != requestID {
				ctx = context.WithValue(ctx, chimw.RequestIDKey, logID)
			}
			clean := r.WithContext(ctx)
			clean.Method, clean.Host, clean.RemoteAddr, clean.Proto = method, host, remote, proto
			clean.RequestURI = uri
			if oauth {
				cleanURL := *r.URL
				cleanURL.RawQuery = ""
				cleanURL.ForceQuery = false
				clean.URL = &cleanURL
			}
			logger(http.HandlerFunc(func(w http.ResponseWriter, logged *http.Request) {
				ctx := logged.Context()
				if logID != requestID {
					ctx = context.WithValue(ctx, chimw.RequestIDKey, requestID)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
			})).ServeHTTP(w, clean)
		})
	}
}
