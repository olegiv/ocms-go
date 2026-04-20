// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package middleware

import (
	"net/http"

	"github.com/olegiv/ocms-go/internal/seo"
)

// LinkHeaders adds an RFC 8288 Link response header on the homepage
// pointing agents at the oCMS discovery surface — the RFC 9727 API
// catalog, the OpenAPI service description, and the Swagger UI.
//
// The header is set only on requests targeting "/" and is not
// path-sensitive beyond that. The header value is a constant built by
// the seo package (see seo.LinkHeaderHomepage) so there is nothing
// per-request to compute.
//
// This middleware exists so AI agents that fetch the root URL can
// discover programmable surfaces without a separate crawl step. See
// https://isitagentready.com for the scanner that exercises it.
func LinkHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			// Set before calling the next handler so it is emitted even
			// if downstream handlers flush eagerly.
			w.Header().Set("Link", seo.LinkHeaderHomepage)
		}
		next.ServeHTTP(w, r)
	})
}
