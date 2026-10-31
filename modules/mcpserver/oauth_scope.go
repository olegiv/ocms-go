// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/oauth"
)

// withOAuthToolScopes runs after authentication and batch/body limits. The
// transport challenge lets clients request fresh consent before invoking a
// tool; the tool wrapper still enforces scopes for every invocation path.
func (m *Module) withOAuthToolScopes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil || m.ctx.OAuth == nil {
			next.ServeHTTP(w, r)
			return
		}
		principal, ok := info.Extra[identityKey].(*identity)
		if !ok || principal == nil || principal.grantID == "" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes+1))
		if err != nil {
			middleware.WriteAPIError(w, http.StatusBadRequest, "invalid_request", "Failed to read request body", nil)
			return
		}
		if len(body) > maxRequestBodyBytes {
			middleware.WriteAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body too large", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var message struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &message) == nil && message.JSONRPC == "2.0" &&
			len(message.ID) > 0 && !bytes.Equal(bytes.TrimSpace(message.ID), []byte("null")) && message.Method == "tools/call" {
			for _, spec := range m.catalog() {
				if spec.Name != message.Params.Name || spec.scope == "" || slices.Contains(info.Scopes, spec.scope) {
					continue
				}
				// Basic endpoint access remains required during scope upgrades.
				required := model.PermissionMCPAccess + " " + spec.scope
				w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer resource_metadata=%q, error=\"insufficient_scope\", scope=%q", m.ctx.OAuth.Issuer()+oauth.MetadataPath, required))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "insufficient_scope"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
