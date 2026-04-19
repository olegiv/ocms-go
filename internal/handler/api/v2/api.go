// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package v2 is the oCMS REST API v2, served from /api/v2.
//
// Each HTTP operation is a huma.Register call driven by typed input and
// output structs. The OpenAPI 3.1 document served at /api/v2/openapi.{json,yaml}
// is derived from those types — there is no hand-written spec.
package v2

import (
	"database/sql"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// Deps bundles everything the v2 operation handlers need to call into the
// store / service layer. Kept as a plain struct so individual op files can
// accept it without knowing about chi / huma details.
type Deps struct {
	DB           *sql.DB
	Queries      *store.Queries
	CacheManager *cache.Manager
	EventService *service.EventService
}

// Handler carries the v2 huma API plus the deps bundle. It's returned by
// Register so main.go can hand it to DocsHandler for spec serving.
type Handler struct {
	API  huma.API
	Deps Deps
}

// Register attaches the v2 huma API to the given chi router and registers all
// v2 operations on it. The caller still owns the chi router for middleware.
func Register(r chi.Router, deps Deps) *Handler {
	cfg := huma.DefaultConfig("oCMS REST API", "2.0.0")
	cfg.OpenAPI.Info.Description = "Content management REST API for pages, media, tags, and categories. " +
		"Spec is generated from Go types via huma v2 — requests and responses match the handler implementation exactly."
	cfg.OpenAPI.Info.License = &huma.License{
		Name:       "GPL-3.0-or-later",
		Identifier: "GPL-3.0-or-later",
	}
	cfg.OpenAPI.Servers = []*huma.Server{{URL: "/api/v2"}}

	api := humachi.New(r, cfg)

	// Replace huma's default error with our Error envelope so clients see the
	// same {error:{code,message,details}} shape the v1 surface used.
	huma.NewError = newStatusError
	huma.NewErrorWithContext = func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return newStatusError(status, msg, errs...)
	}

	h := &Handler{API: api, Deps: deps}
	registerMeta(h)
	registerPages(h)
	return h
}

// OpenAPI returns the current OpenAPI 3.1 document built from registered operations.
func (h *Handler) OpenAPI() *huma.OpenAPI {
	return h.API.OpenAPI()
}
