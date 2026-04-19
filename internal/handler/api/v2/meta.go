// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/store"
)

// apiKeyFromContext extracts the authenticated API key from a v2 huma handler's
// context. Returns nil if the request was unauthenticated.
func apiKeyFromContext(ctx context.Context) *store.ApiKey {
	apiKey, ok := ctx.Value(middleware.ContextKeyAPIKey).(store.ApiKey)
	if !ok {
		return nil
	}
	return &apiKey
}

// StatusBody is the API availability payload returned by GET /api/v2/status.
type StatusBody struct {
	Status  string `json:"status" example:"ok" doc:"'ok' when the API is reachable."`
	Version string `json:"version" example:"v2"`
}

// AuthBody describes the authenticated API key. Returned from GET /api/v2/auth.
type AuthBody struct {
	KeyPrefix   string   `json:"key_prefix" doc:"Public prefix of the API key used for display and correlation."`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

// registerMeta wires /status and /auth onto the huma API.
func registerMeta(h *Handler) {
	huma.Register(h.API, huma.Operation{
		OperationID: "status",
		Method:      http.MethodGet,
		Path:        "/status",
		Summary:     "API status",
		Description: "Lightweight liveness check. Public, no authentication required.",
		Tags:        []string{"Meta"},
		Security:    []map[string][]string{},
	}, func(_ context.Context, _ *struct{}) (*struct{ Body StatusBody }, error) {
		return &struct{ Body StatusBody }{Body: StatusBody{Status: "ok", Version: "v2"}}, nil
	})

	huma.Register(h.API, huma.Operation{
		OperationID: "auth",
		Method:      http.MethodGet,
		Path:        "/auth",
		Summary:     "Authenticated API key info",
		Description: "Returns the prefix, name, and permissions of the API key that authenticated the request.",
		Tags:        []string{"Meta"},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body AuthBody }, error) {
		apiKey := apiKeyFromContext(ctx)
		if apiKey == nil {
			return nil, huma.Error401Unauthorized("Not authenticated")
		}
		return &struct{ Body AuthBody }{Body: AuthBody{
			KeyPrefix:   apiKey.KeyPrefix,
			Name:        apiKey.Name,
			Permissions: middleware.ParseAPIKeyPermissions(apiKey),
		}}, nil
	})
}
