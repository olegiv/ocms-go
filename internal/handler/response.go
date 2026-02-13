// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/render"
)

// flashAndRedirect sets a flash message and redirects to the given URL.
// Uses http.StatusSeeOther (303) for POST/PUT/DELETE redirects.
func flashAndRedirect(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, url, message, messageType string) {
	renderer.SetFlash(r, message, messageType)
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// flashError sets an error flash message and redirects to the given URL.
func flashError(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, url, message string) {
	flashAndRedirect(w, r, renderer, url, message, "error")
}

// flashSuccess sets a success flash message and redirects to the given URL.
func flashSuccess(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, url, message string) {
	flashAndRedirect(w, r, renderer, url, message, "success")
}

// parseFormOrRedirect parses the request form and redirects with an error message on failure.
// Returns true if parsing succeeded, false if it failed (and redirect was performed).
func parseFormOrRedirect(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, redirectURL string) bool {
	if err := r.ParseForm(); err != nil {
		flashError(w, r, renderer, redirectURL, "Invalid form data")
		return false
	}
	return true
}

// logAndHTTPError logs an error and writes an HTTP error response.
func logAndHTTPError(w http.ResponseWriter, message string, statusCode int, logMsg string, args ...any) {
	slog.Error(logMsg, args...)
	http.Error(w, message, statusCode)
}

// logAndInternalError logs an error and writes a 500 Internal Server Error response.
func logAndInternalError(w http.ResponseWriter, logMsg string, args ...any) {
	logAndHTTPError(w, "Internal Server Error", http.StatusInternalServerError, logMsg, args...)
}

// =============================================================================
// GENERIC ENTITY FETCHING HELPERS
// =============================================================================

// requireEntityWithRedirect fetches an entity by ID using the provided query function.
// On error, it sets a flash message and redirects. Returns the entity and true if successful,
// or zero value and false if an error occurred (redirect already performed).
//
// Example usage:
//
//	page, ok := requireEntityWithRedirect(w, r, h.renderer, "/admin/pages", "page", id,
//	    func(id int64) (store.Page, error) { return h.queries.GetPageByID(r.Context(), id) })
func requireEntityWithRedirect[T any](
	w http.ResponseWriter,
	r *http.Request,
	renderer *render.Renderer,
	redirectURL string,
	entityName string,
	id int64,
	queryFn func(id int64) (T, error),
) (T, bool) {
	var zero T
	entity, err := queryFn(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			flashError(w, r, renderer, redirectURL, entityName+" not found")
		} else {
			slog.Error("failed to get "+entityName, "error", err, entityName+"_id", id)
			flashError(w, r, renderer, redirectURL, "Error loading "+entityName)
		}
		return zero, false
	}
	return entity, true
}

// requireEntityWithError fetches an entity by ID using the provided query function.
// On error, it writes an HTTP error response. Returns the entity and true if successful,
// or zero value and false if an error occurred (response already written).
//
// Example usage:
//
//	page, ok := requireEntityWithError(w, "page", id,
//	    func(id int64) (store.Page, error) { return h.queries.GetPageByID(r.Context(), id) })
func requireEntityWithError[T any](
	w http.ResponseWriter,
	entityName string,
	id int64,
	queryFn func(id int64) (T, error),
) (T, bool) {
	var zero T
	entity, err := queryFn(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, entityName+" not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get "+entityName, "error", err, entityName+"_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return zero, false
	}
	return entity, true
}

// requireEntityWithJSONError fetches an entity by ID using the provided query function.
// On error, it writes a JSON error response. Returns the entity and true if successful,
// or zero value and false if an error occurred (response already written).
//
// Example usage:
//
//	menu, ok := requireEntityWithJSONError(w, "Menu", id,
//	    func(id int64) (store.Menu, error) { return h.queries.GetMenuByID(r.Context(), id) })
func requireEntityWithJSONError[T any](
	w http.ResponseWriter,
	entityName string,
	id int64,
	queryFn func(id int64) (T, error),
) (T, bool) {
	var zero T
	entity, err := queryFn(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, entityName+" not found")
		} else {
			slog.Error("failed to get "+entityName, "error", err, entityName+"_id", id)
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		}
		return zero, false
	}
	return entity, true
}

// requireEntityWithCustomError fetches an entity by ID using the provided query function.
// On error, it calls the provided error handler. Returns the entity and true if successful,
// or zero value and false if an error occurred (error handler already called).
//
// Example usage:
//
//	apiKey, ok := requireEntityWithCustomError(w, "API key", id,
//	    func(id int64) (store.ApiKey, error) { return h.queries.GetAPIKeyByID(r.Context(), id) },
//	    h.sendDeleteError)
func requireEntityWithCustomError[T any](
	w http.ResponseWriter,
	entityName string,
	id int64,
	queryFn func(id int64) (T, error),
	errorHandler func(http.ResponseWriter, string),
) (T, bool) {
	var zero T
	entity, err := queryFn(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			errorHandler(w, entityName+" not found")
		} else {
			slog.Error("failed to get "+entityName, "error", err, entityName+"_id", id)
			errorHandler(w, "Error loading "+entityName)
		}
		return zero, false
	}
	return entity, true
}

// =============================================================================
// DELETE OPERATION HELPERS
// =============================================================================

// deleteEntityParams holds parameters for a generic delete operation.
type deleteEntityParams[T any] struct {
	EntityName     string                              // e.g., "tag", "category"
	IDField        string                              // e.g., "tag_id", "category_id"
	RedirectURL    string                              // URL to redirect after success
	SuccessMessage string                              // Flash message on success
	RequireFn      func(int64) (T, bool)               // function to fetch and validate entity
	DeleteFn       func(context.Context, int64) error  // function to delete entity
	GetSlug        func(T) string                      // function to get slug for logging
}

// handleDeleteEntity performs a generic delete operation with HTMX support.
func handleDeleteEntity[T any](w http.ResponseWriter, r *http.Request, renderer *render.Renderer, p deleteEntityParams[T]) {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid "+p.EntityName+" ID", http.StatusBadRequest)
		return
	}

	entity, ok := p.RequireFn(id)
	if !ok {
		return
	}

	if err = p.DeleteFn(r.Context(), id); err != nil {
		slog.Error("failed to delete "+p.EntityName, "error", err, p.IDField, id)
		http.Error(w, "Error deleting "+p.EntityName, http.StatusInternalServerError)
		return
	}

	slog.Info(p.EntityName+" deleted", p.IDField, id, "slug", p.GetSlug(entity), "deleted_by", middleware.GetUserID(r))

	// For HTMX requests, return empty response (row removed)
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For regular requests, redirect
	flashSuccess(w, r, renderer, p.RedirectURL, p.SuccessMessage)
}

// =============================================================================
// LANGUAGE PREFERENCE HELPERS
// =============================================================================

// setLanguagePreference sets the admin UI language preference from a form value.
// It validates the language code and redirects to the referer or the provided fallback URL.
func setLanguagePreference(w http.ResponseWriter, r *http.Request, renderer *render.Renderer, fallbackURL string) {
	lang := r.FormValue("lang")
	if lang == "" {
		lang = "en"
	}

	// Validate the language code
	if !i18n.IsSupported(lang) {
		lang = "en"
	}

	// Set the language preference in session
	renderer.SetAdminLang(r, lang)

	// Redirect back to the referring page, or fallback URL if not available
	referer := r.Header.Get("Referer")
	if referer == "" {
		referer = fallbackURL
	}

	http.Redirect(w, r, referer, http.StatusSeeOther)
}
