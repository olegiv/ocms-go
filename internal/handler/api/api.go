// Package api provides REST API handlers for the CMS.
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"ocms-go/internal/middleware"
	"ocms-go/internal/store"
)

// Handler holds shared dependencies for all API handlers.
type Handler struct {
	db      *sql.DB
	queries *store.Queries
}

// NewHandler creates a new API handler.
func NewHandler(db *sql.DB) *Handler {
	return &Handler{
		db:      db,
		queries: store.New(db),
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
