// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

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
