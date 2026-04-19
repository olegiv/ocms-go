// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package v2 is the oCMS REST API v2, served at /api/v2.
//
// Every operation is registered via huma.Register with typed input and output
// structs. The OpenAPI 3.1 document served at /api/v2/openapi.json is derived
// from those types — by construction the spec and the handlers cannot drift.
//
// This package MUST NOT import internal/handler/api (the v1 surface). v2 is a
// fresh rewrite; the old package will be deleted as each domain migrates.
package v2

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// APIKeyAuthSecurity is the huma Security block applied to every write
// operation: it pairs the route with the `ApiKeyAuth` scheme declared in
// Register so the OpenAPI spec and Swagger UI show the padlock.
var APIKeyAuthSecurity = []map[string][]string{{"ApiKeyAuth": {}}}

// Deps bundles dependencies that domain services may pick from.
type Deps struct {
	DB      *sql.DB
	Queries *store.Queries
	Cache   *cache.Manager
	Events  *service.EventService
}

// Handler holds the live huma.API plus shared deps.
type Handler struct {
	API  huma.API
	Deps Deps
}

// Register wires huma onto the given chi router and installs every v2
// operation. Chi middleware already attached to the router (rate limiting,
// optional API key auth, …) still applies to huma-routed requests.
func Register(r chi.Router, deps Deps) *Handler {
	cfg := huma.DefaultConfig("oCMS REST API", "2.0.0")
	cfg.OpenAPI.Info.Description = "REST API v2 for oCMS. Spec generated from Go types via huma v2 — requests and responses match the handler implementation exactly."
	cfg.OpenAPI.Info.License = &huma.License{
		Name:       "GPL-3.0-or-later",
		Identifier: "GPL-3.0-or-later",
	}
	cfg.OpenAPI.Servers = []*huma.Server{{URL: "/api/v2"}}

	// Advertise the API-key bearer scheme in the generated OpenAPI spec so Swagger
	// UI renders an authorize prompt and write operations show the padlock icon.
	if cfg.OpenAPI.Components == nil {
		cfg.OpenAPI.Components = &huma.Components{}
	}
	if cfg.OpenAPI.Components.SecuritySchemes == nil {
		cfg.OpenAPI.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	cfg.OpenAPI.Components.SecuritySchemes["ApiKeyAuth"] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "API key",
		Description:  "API key issued from /admin/api-keys. Send as `Authorization: Bearer <key>`.",
	}

	api := humachi.New(r, cfg)

	// Route huma's own framework errors (input parse, validation failures it
	// detects) through the ErrorBody envelope so every error response looks
	// identical on the wire, whether raised by huma or a domain service.
	huma.NewError = newStatusError
	huma.NewErrorWithContext = func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return newStatusError(status, msg, errs...)
	}

	// Reject unauthenticated requests to Security-declared operations BEFORE
	// huma parses the request body. Prevents DoS vectors where an attacker
	// forces multipart/temp-file parsing on POST /media and only then hits the
	// service-layer auth check. See Codex P1 review on commit 024b94f.
	api.UseMiddleware(requireAPIKeyWhenDeclared(api))

	h := &Handler{API: api, Deps: deps}
	registerMeta(h)
	return h
}

// requireAPIKeyWhenDeclared returns a huma API middleware that short-circuits
// with 401 when the current operation declares `ApiKeyAuth` Security but the
// request has no valid API key in its context. Ties runtime enforcement to the
// spec declaration so the two can never drift.
func requireAPIKeyWhenDeclared(api huma.API) func(ctx huma.Context, next func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if op == nil || !operationRequiresAPIKey(op) {
			next(ctx)
			return
		}
		if _, ok := ctx.Context().Value(middleware.ContextKeyAPIKey).(store.ApiKey); !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "API key required")
			return
		}
		next(ctx)
	}
}

// operationRequiresAPIKey reports whether every Security alternative requires
// a non-empty scheme AND at least one alternative references `ApiKeyAuth`.
// OpenAPI Security semantics: the top-level list is OR; an empty block `{}`
// means "no auth is a valid alternative", so the operation is public.
func operationRequiresAPIKey(op *huma.Operation) bool {
	if len(op.Security) == 0 {
		return false
	}
	hasAPIKey := false
	for _, block := range op.Security {
		if len(block) == 0 {
			// Public alternative exists — do not enforce auth.
			return false
		}
		if _, ok := block["ApiKeyAuth"]; ok {
			hasAPIKey = true
		}
	}
	return hasAPIKey
}

// OpenAPI returns the live OpenAPI 3.1 document built from registered operations.
func (h *Handler) OpenAPI() *huma.OpenAPI { return h.API.OpenAPI() }

// Actor describes the principal making a v2 request. APIKey is nil for
// unauthenticated callers; Permissions is parsed once so services can make
// authorization decisions without re-querying the key.
type Actor struct {
	APIKey      *store.ApiKey
	Permissions []string
}

// HasPermission reports whether the actor's API key grants a permission.
func (a Actor) HasPermission(perm string) bool {
	for _, p := range a.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// ActorFromContext extracts the authenticated principal (if any) from a huma
// handler's request context. Returns a zero Actor for public callers.
func ActorFromContext(ctx context.Context) Actor {
	key, ok := ctx.Value(middleware.ContextKeyAPIKey).(store.ApiKey)
	if !ok {
		return Actor{}
	}
	return Actor{
		APIKey:      &key,
		Permissions: middleware.ParseAPIKeyPermissions(&key),
	}
}
