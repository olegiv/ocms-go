// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package api provides REST API handlers for the CMS.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/handler"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// Handler holds shared dependencies for all API handlers.
type Handler struct {
	db                        *sql.DB
	queries                   *store.Queries
	cacheManager              *cache.Manager
	eventService              *service.EventService
	blockSuspiciousPageMarkup bool
	sanitizePageHTML          bool
}

const maxAPIJSONBodyBytes int64 = 1 << 20 // 1 MiB

// NewHandler creates a new API handler.
func NewHandler(db *sql.DB) *Handler {
	return &Handler{
		db:      db,
		queries: store.New(db),
	}
}

// SetCacheManager sets the cache manager for cache invalidation.
func (h *Handler) SetCacheManager(cm *cache.Manager) {
	h.cacheManager = cm
}

// SetBlockSuspiciousPageMarkup configures whether API page write operations
// reject suspicious HTML body content.
func (h *Handler) SetBlockSuspiciousPageMarkup(block bool) {
	h.blockSuspiciousPageMarkup = block
}

// SetSanitizePageHTML configures whether API page write operations sanitize
// HTML body content before persistence.
func (h *Handler) SetSanitizePageHTML(enabled bool) {
	h.sanitizePageHTML = enabled
}

// SetEventService sets the event service for audit logging.
func (h *Handler) SetEventService(es *service.EventService) {
	h.eventService = es
}

// apiLogger provides category-scoped event logging for API handlers.
// Create one per handler method via h.newAPILogger() to avoid repeating
// the request and category on every logging call.
type apiLogger struct {
	h        *Handler
	r        *http.Request
	category string
}

// newAPILogger creates a logger scoped to a request and event category.
func (h *Handler) newAPILogger(r *http.Request, category string) *apiLogger {
	return &apiLogger{h: h, r: r, category: category}
}

// Info logs a success event to the database event log.
func (l *apiLogger) Info(message string, meta map[string]any) {
	l.h.logEvent(l.r, l.category, model.EventLevelInfo, message, meta)
}

// Error logs an error event to the database event log (without writing an HTTP response).
func (l *apiLogger) Error(message string, meta map[string]any) {
	l.h.logEvent(l.r, l.category, model.EventLevelError, message, meta)
}

// Error500 logs an error via slog and the event service, then writes a 500 response.
func (l *apiLogger) Error500(w http.ResponseWriter, message string, args ...any) {
	l.h.logAndRespondError(w, l.r, l.category, message, args...)
}

// apiUserIDPtr returns the API key creator's user ID pointer.
func (h *Handler) apiUserIDPtr(r *http.Request) *int64 {
	if key := middleware.GetAPIKey(r); key != nil {
		id := key.CreatedBy
		return &id
	}
	return nil
}

// apiEventMeta returns metadata with API source info merged with extra fields.
func (h *Handler) apiEventMeta(r *http.Request, extra map[string]any) map[string]any {
	m := map[string]any{"source": "api"}
	if key := middleware.GetAPIKey(r); key != nil {
		m["api_key_id"] = key.ID
		m["api_key_name"] = key.Name
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// logEvent logs an event via the event service if available.
func (h *Handler) logEvent(r *http.Request, category, level, message string, extra map[string]any) {
	if h.eventService == nil {
		return
	}
	_ = h.eventService.LogEvent(
		r.Context(), level, category, message,
		h.apiUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r),
		h.apiEventMeta(r, extra),
	)
}

// logAndRespondError logs the error via slog and the event service, then writes a 500 response.
func (h *Handler) logAndRespondError(w http.ResponseWriter, r *http.Request, category, message string, args ...any) {
	slog.Error(message, args...)
	if h.eventService != nil {
		_ = h.eventService.LogEvent(
			r.Context(), model.EventLevelError, category, "API error: "+message,
			h.apiUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r),
			h.apiEventMeta(r, map[string]any{"error": message}),
		)
	}
	WriteInternalError(w, message)
}

// invalidatePageCache invalidates the page cache after a page is modified.
func (h *Handler) invalidatePageCache(pageID int64) {
	if h.cacheManager != nil {
		h.cacheManager.InvalidatePage(pageID)
	}
}

// Response is the standard API response wrapper.
type Response struct {
	Data any   `json:"data,omitempty"`
	Meta *Meta `json:"meta,omitempty"`
}

// Meta contains pagination and other metadata.
type Meta struct {
	Total   int64 `json:"total,omitempty"`
	Page    int   `json:"page,omitempty"`
	PerPage int   `json:"per_page,omitempty"`
	Pages   int   `json:"pages,omitempty"`
}

// ErrorResponse is the standard API error response.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error information.
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// WriteJSON writes a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

// WriteSuccess writes a successful JSON response.
func WriteSuccess(w http.ResponseWriter, data any, meta *Meta) {
	resp := Response{
		Data: data,
		Meta: meta,
	}
	WriteJSON(w, http.StatusOK, resp)
}

// WriteCreated writes a 201 Created JSON response.
func WriteCreated(w http.ResponseWriter, data any) {
	resp := Response{
		Data: data,
	}
	WriteJSON(w, http.StatusCreated, resp)
}

// WriteError writes an error JSON response.
func WriteError(w http.ResponseWriter, statusCode int, code, message string, details map[string]string) {
	resp := ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
	WriteJSON(w, statusCode, resp)
}

// WriteBadRequest writes a 400 Bad Request response.
func WriteBadRequest(w http.ResponseWriter, message string, details map[string]string) {
	WriteError(w, http.StatusBadRequest, "bad_request", message, details)
}

// WriteNotFound writes a 404 Not Found response.
func WriteNotFound(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusNotFound, "not_found", message, nil)
}

// WriteUnauthorized writes a 401 Unauthorized response.
func WriteUnauthorized(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusUnauthorized, "unauthorized", message, nil)
}

// WriteForbidden writes a 403 Forbidden response.
func WriteForbidden(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusForbidden, "forbidden", message, nil)
}

// WriteInternalError writes a 500 Internal Server Error response.
func WriteInternalError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusInternalServerError, "internal_error", message, nil)
}

// LogAndWriteInternalError logs the error via slog.Error and writes a 500 JSON response.
// This ensures the error reaches the EventLogHandler for database event logging.
// IMPORTANT: message is sent to the HTTP client verbatim — use only static string literals.
func LogAndWriteInternalError(w http.ResponseWriter, message string, args ...any) {
	slog.Error(message, args...)
	WriteInternalError(w, message)
}

// decodeJSON decodes a JSON body with strict validation.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = maxAPIJSONBodyBytes
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}

	// Require exactly one JSON object.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain only one JSON object")
	}

	return nil
}

// WriteValidationError writes a 422 Unprocessable Entity response with field errors.
func WriteValidationError(w http.ResponseWriter, fieldErrors map[string]string) {
	WriteError(w, http.StatusUnprocessableEntity, "validation_error", "Validation failed", fieldErrors)
}

// StatusResponse contains API status information.
type StatusResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Status returns the API status.
func (h *Handler) Status(w http.ResponseWriter, _ *http.Request) {
	WriteSuccess(w, StatusResponse{
		Status:  "ok",
		Version: "v1",
	}, nil)
}

// AuthInfo returns information about the authenticated API key.
func (h *Handler) AuthInfo(w http.ResponseWriter, r *http.Request) {
	apiKey := middleware.GetAPIKey(r)
	if apiKey == nil {
		WriteUnauthorized(w, "Not authenticated")
		return
	}

	type AuthInfoResponse struct {
		KeyPrefix   string   `json:"key_prefix"`
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}

	WriteSuccess(w, AuthInfoResponse{
		KeyPrefix:   apiKey.KeyPrefix,
		Name:        apiKey.Name,
		Permissions: middleware.ParseAPIKeyPermissions(apiKey),
	}, nil)
}

// SlugExistsChecker is a function that checks if a slug exists (returns count and error).
type SlugExistsChecker func() (int64, error)

// error500Func is a callback for logging 500 errors and writing the response.
type error500Func func(w http.ResponseWriter, message string, args ...any)

// checkSlugUnique checks if a slug is unique using the provided checker function.
// Returns true if unique, false if duplicate or error (response already written).
func checkSlugUnique(w http.ResponseWriter, slugExists SlugExistsChecker, logErr error500Func) bool {
	exists, err := slugExists()
	if err != nil {
		if logErr != nil {
			logErr(w, "Failed to check slug", "error", err)
		} else {
			LogAndWriteInternalError(w, "Failed to check slug", "error", err)
		}
		return false
	}
	if exists != 0 {
		WriteValidationError(w, map[string]string{"slug": "Slug already exists"})
		return false
	}
	return true
}

// EntityFetcher is a function that fetches an entity by ID.
type EntityFetcher[T any] func(id int64) (T, error)

// requireEntityByID parses an ID from the URL and fetches the entity.
// Returns the entity and true if successful, or zero value and false if error (response written).
// The entityName is used for error messages (e.g., "page", "tag", "category", "media").
func requireEntityByID[T any](w http.ResponseWriter, r *http.Request, entityName string, fetch EntityFetcher[T], logErr error500Func) (T, bool) {
	var zero T

	id, err := handler.ParseIDParam(r)
	if err != nil {
		WriteBadRequest(w, "Invalid "+entityName+" ID", nil)
		return zero, false
	}

	entity, err := fetch(id)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			WriteNotFound(w, capitalizeFirst(entityName)+" not found")
		case logErr != nil:
			logErr(w, "Failed to retrieve "+entityName, "error", err)
		default:
			LogAndWriteInternalError(w, "Failed to retrieve "+entityName, "error", err)
		}
		return zero, false
	}

	return entity, true
}

// capitalizeFirst returns s with the first letter capitalized.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// resolveLanguageCode returns the provided language code or defaults to the system default language.
func (h *Handler) resolveLanguageCode(ctx context.Context, langCode *string) (string, error) {
	if langCode != nil && *langCode != "" {
		return *langCode, nil
	}
	defaultLang, err := h.queries.GetDefaultLanguage(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get default language: %w", err)
	}
	return defaultLang.Code, nil
}
