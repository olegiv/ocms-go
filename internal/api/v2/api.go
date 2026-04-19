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

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
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

	api := humachi.New(r, cfg)

	// Route huma's own framework errors (input parse, validation failures it
	// detects) through the ErrorBody envelope so every error response looks
	// identical on the wire, whether raised by huma or a domain service.
	huma.NewError = newStatusError
	huma.NewErrorWithContext = func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return newStatusError(status, msg, errs...)
	}

	h := &Handler{API: api, Deps: deps}
	registerMeta(h)
	return h
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
