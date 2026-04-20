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

// APIKeyAuthSecurity is the base Security block for any operation that just
// needs a valid API key regardless of permission (e.g., /auth meta endpoint).
// Write operations should use the permission-scoped variants below so the
// runtime middleware can short-circuit a wrong-permission call BEFORE huma
// parses the request body — a read-only key sending `POST /media` is rejected
// with 403 without any multipart work. The scope string must match one of
// `model.Permission*` constants so services and the OpenAPI spec stay aligned.
var (
	APIKeyAuthSecurity    = []map[string][]string{{"ApiKeyAuth": {}}}
	PagesWriteSecurity    = []map[string][]string{{"ApiKeyAuth": {"pages:write"}}}
	MediaWriteSecurity    = []map[string][]string{{"ApiKeyAuth": {"media:write"}}}
	TaxonomyWriteSecurity = []map[string][]string{{"ApiKeyAuth": {"taxonomy:write"}}}
)

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
// with 401/403 when the current operation declares `ApiKeyAuth` Security:
//   - 401 if no API key is in the request context
//   - 403 if the API key is present but lacks any of the scopes declared
//     alongside ApiKeyAuth in the operation's Security block
//
// Both checks run BEFORE huma's input binding, so a wrong-permission caller
// never causes multipart parsing, tempfile writes, or any body work. Ties
// runtime enforcement to the spec declaration so the two can never drift.
func requireAPIKeyWhenDeclared(api huma.API) func(ctx huma.Context, next func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		requiredScopes, needsKey := apiKeyScopesFor(op)
		if !needsKey {
			next(ctx)
			return
		}
		key, ok := ctx.Context().Value(middleware.ContextKeyAPIKey).(store.ApiKey)
		if !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "API key required")
			return
		}
		if len(requiredScopes) > 0 {
			perms := middleware.ParseAPIKeyPermissions(&key)
			for _, scope := range requiredScopes {
				if !permissionsContain(perms, scope) {
					_ = huma.WriteErr(api, ctx, http.StatusForbidden,
						scope+" permission required")
					return
				}
			}
		}
		next(ctx)
	}
}

// apiKeyScopesFor returns the scopes required by the ApiKeyAuth Security
// alternative on an operation, and a flag indicating whether authentication is
// required at all. OpenAPI Security semantics: the top-level list is OR; an
// empty block `{}` means "no auth is a valid alternative" so the operation is
// public. If ApiKeyAuth is required, its scope array names the permissions
// the caller's key must hold.
func apiKeyScopesFor(op *huma.Operation) (scopes []string, needsKey bool) {
	if op == nil || len(op.Security) == 0 {
		return nil, false
	}
	for _, block := range op.Security {
		if len(block) == 0 {
			// Public alternative exists — do not enforce auth.
			return nil, false
		}
		if s, ok := block["ApiKeyAuth"]; ok {
			scopes = s
			needsKey = true
		}
	}
	return scopes, needsKey
}

func permissionsContain(perms []string, want string) bool {
	for _, p := range perms {
		if p == want {
			return true
		}
	}
	return false
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
