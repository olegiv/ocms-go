// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package v2

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// StatusBody is the API availability payload returned by GET /api/v2/status.
type StatusBody struct {
	Status  string `json:"status" example:"ok" doc:"'ok' when the API is reachable."`
	Version string `json:"version" example:"v2" doc:"API major version."`
}

// AuthBody describes the authenticated API key for GET /api/v2/auth.
type AuthBody struct {
	KeyPrefix   string   `json:"key_prefix" doc:"Public prefix of the API key used for display and correlation."`
	Name        string   `json:"name" doc:"Human-readable name assigned to the key."`
	Permissions []string `json:"permissions" doc:"Permission scopes granted to the key."`
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
		Security:    []map[string][]string{}, // no auth
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
		actor := ActorFromContext(ctx)
		if actor.APIKey == nil {
			return nil, ToHuma(NewError(ErrUnauthorized, "API key required"))
		}
		return &struct{ Body AuthBody }{Body: AuthBody{
			KeyPrefix:   actor.APIKey.KeyPrefix,
			Name:        actor.APIKey.Name,
			Permissions: actor.Permissions,
		}}, nil
	})
}
