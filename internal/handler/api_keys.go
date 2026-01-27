// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
)

// APIKeysPerPage is the number of API keys to display per page.
const APIKeysPerPage = 10

// APIKeysHandler handles API key management routes.
type APIKeysHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewAPIKeysHandler creates a new APIKeysHandler.
func NewAPIKeysHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *APIKeysHandler {
	return &APIKeysHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// APIKeysListData holds data for the API keys list template.
type APIKeysListData struct {
	APIKeys    []store.ApiKey
	TotalKeys  int64
	Pagination AdminPagination
}

// List handles GET /admin/api-keys - displays a paginated list of API keys.
func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	page := ParsePageParam(r)

	// Get total count
	totalKeys, err := h.queries.CountAPIKeys(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to count API keys", "error", err)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalKeys), APIKeysPerPage)
	offset := int64((page - 1) * APIKeysPerPage)

	// Fetch API keys for current page
	apiKeys, err := h.queries.ListAPIKeys(r.Context(), store.ListAPIKeysParams{
		Limit:  APIKeysPerPage,
		Offset: offset,
	})
	if err != nil {
		logAndInternalError(w, "failed to list API keys", "error", err)
		return
	}

	data := APIKeysListData{
		APIKeys:    apiKeys,
		TotalKeys:  totalKeys,
		Pagination: BuildAdminPagination(page, int(totalKeys), APIKeysPerPage, redirectAdminAPIKeys, r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/api_keys_list", render.TemplateData{
		Title: i18n.T(lang, "api_keys.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.api_keys"), URL: redirectAdminAPIKeys, Active: true},
		},
	})
}

// APIKeyFormData holds data for the API key form template.
type APIKeyFormData struct {
	APIKey       *store.ApiKey
	Permissions  []string
	Errors       map[string]string
	FormValues   map[string]string
	IsEdit       bool
	GeneratedKey string // Only set after creation to show the key once
}

// NewForm handles GET /admin/api-keys/new - displays the new API key form.
func (h *APIKeysHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	data := APIKeyFormData{
		Permissions: model.AllPermissions(),
		Errors:      make(map[string]string),
		FormValues:  make(map[string]string),
		IsEdit:      false,
	}

	h.renderAPIKeyForm(w, r, user, lang, i18n.T(lang, "api_keys.new_key"), data,
		i18n.T(lang, "api_keys.new_key"), redirectAdminAPIKeysNew)
}

// Create handles POST /admin/api-keys - creates a new API key.
func (h *APIKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminAPIKeysNew) {
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	permissions := r.Form["permissions"]
	expiresAtStr := r.FormValue("expires_at")

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":       name,
		"expires_at": expiresAtStr,
	}

	// Validate
	validationErrors := make(map[string]string)

	if err := validateAPIKeyName(name); err != "" {
		validationErrors["name"] = err
	}
	if err := validateAPIKeyPermissions(permissions); err != "" {
		validationErrors["permissions"] = err
	}
	expiresAt, expiresErr := parseAPIKeyExpiration(expiresAtStr, true)
	if expiresErr != "" {
		validationErrors["expires_at"] = expiresErr
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		data := APIKeyFormData{
			Permissions: model.AllPermissions(),
			Errors:      validationErrors,
			FormValues:  formValues,
			IsEdit:      false,
		}
		h.renderAPIKeyForm(w, r, user, lang, i18n.T(lang, "api_keys.new_key"), data,
			i18n.T(lang, "api_keys.new_key"), redirectAdminAPIKeysNew)
		return
	}

	// Generate API key
	rawKey, prefix, err := model.GenerateAPIKey()
	if err != nil {
		slog.Error("failed to generate API key", "error", err)
		flashError(w, r, h.renderer, redirectAdminAPIKeysNew, "Error generating API key")
		return
	}

	// Hash the key for storage
	keyHash := model.HashAPIKey(rawKey)

	// Convert permissions to JSON
	permissionsJSON := model.PermissionsToJSON(permissions)

	// Create API key
	now := time.Now()
	apiKey, err := h.queries.CreateAPIKey(r.Context(), store.CreateAPIKeyParams{
		Name:        name,
		KeyHash:     keyHash,
		KeyPrefix:   prefix,
		Permissions: permissionsJSON,
		ExpiresAt:   expiresAt,
		IsActive:    true,
		CreatedBy:   middleware.GetUserID(r),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		slog.Error("failed to create API key", "error", err)
		flashError(w, r, h.renderer, redirectAdminAPIKeysNew, "Error creating API key")
		return
	}

	slog.Info("API key created", "key_id", apiKey.ID, "name", apiKey.Name, "created_by", middleware.GetUserID(r))

	// Render success page showing the generated key once
	data := APIKeyFormData{
		APIKey:       &apiKey,
		Permissions:  model.AllPermissions(),
		Errors:       make(map[string]string),
		FormValues:   make(map[string]string),
		IsEdit:       false,
		GeneratedKey: rawKey,
	}
	h.renderAPIKeyForm(w, r, user, lang, i18n.T(lang, "api_keys.key_created"), data,
		i18n.T(lang, "api_keys.new_key"), redirectAdminAPIKeysNew)
}

// EditForm handles GET /admin/api-keys/{id} - displays the edit API key form.
func (h *APIKeysHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, idStr, ok := parseAPIKeyID(r)
	if !ok {
		flashError(w, r, h.renderer, redirectAdminAPIKeys, "Invalid API key ID")
		return
	}

	apiKey, ok := h.fetchAPIKey(w, r, id)
	if !ok {
		return
	}

	// Format expiration date for form
	expiresAtStr := ""
	if apiKey.ExpiresAt.Valid {
		expiresAtStr = apiKey.ExpiresAt.Time.Format("2006-01-02")
	}

	data := APIKeyFormData{
		APIKey:      &apiKey,
		Permissions: model.AllPermissions(),
		Errors:      make(map[string]string),
		FormValues: map[string]string{
			"name":       apiKey.Name,
			"expires_at": expiresAtStr,
		},
		IsEdit: true,
	}
	h.renderAPIKeyForm(w, r, user, lang, i18n.T(lang, "api_keys.edit_key"), data,
		apiKey.Name, redirectAdminAPIKeysSlash+idStr)
}

// Update handles PUT /admin/api-keys/{id} - updates an existing API key.
func (h *APIKeysHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, idStr, ok := parseAPIKeyID(r)
	if !ok {
		flashError(w, r, h.renderer, redirectAdminAPIKeys, "Invalid API key ID")
		return
	}

	apiKey, ok := h.fetchAPIKey(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminAPIKeysSlash+idStr) {
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	permissions := r.Form["permissions"]
	expiresAtStr := r.FormValue("expires_at")
	isActive := r.FormValue("is_active") == "on" || r.FormValue("is_active") == "true"

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":       name,
		"expires_at": expiresAtStr,
	}

	// Validate
	validationErrors := make(map[string]string)

	if err := validateAPIKeyName(name); err != "" {
		validationErrors["name"] = err
	}
	if err := validateAPIKeyPermissions(permissions); err != "" {
		validationErrors["permissions"] = err
	}
	expiresAt, expiresErr := parseAPIKeyExpiration(expiresAtStr, false) // Don't require future for edits
	if expiresErr != "" {
		validationErrors["expires_at"] = expiresErr
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		data := APIKeyFormData{
			APIKey:      &apiKey,
			Permissions: model.AllPermissions(),
			Errors:      validationErrors,
			FormValues:  formValues,
			IsEdit:      true,
		}
		h.renderAPIKeyForm(w, r, user, lang, i18n.T(lang, "api_keys.edit_key"), data,
			apiKey.Name, redirectAdminAPIKeysSlash+idStr)
		return
	}

	// Convert permissions to JSON
	permissionsJSON := model.PermissionsToJSON(permissions)

	// Update API key
	now := time.Now()
	_, err := h.queries.UpdateAPIKey(r.Context(), store.UpdateAPIKeyParams{
		Name:        name,
		Permissions: permissionsJSON,
		ExpiresAt:   expiresAt,
		IsActive:    isActive,
		UpdatedAt:   now,
		ID:          id,
	})
	if err != nil {
		slog.Error("failed to update API key", "error", err)
		flashError(w, r, h.renderer, redirectAdminAPIKeysSlash+idStr, "Error updating API key")
		return
	}

	slog.Info("API key updated", "key_id", id, "updated_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, redirectAdminAPIKeys, "API key updated successfully")
}

// Delete handles DELETE /admin/api-keys/{id} - deletes (deactivates) an API key.
func (h *APIKeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _, ok := parseAPIKeyID(r)
	if !ok {
		h.sendDeleteError(w, "Invalid API key ID")
		return
	}

	// Fetch the API key being deleted
	apiKey, ok := h.fetchAPIKeyForDelete(w, r, id)
	if !ok {
		return
	}

	// Deactivate API key (soft delete)
	now := time.Now()
	err := h.queries.DeactivateAPIKey(r.Context(), store.DeactivateAPIKeyParams{
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		slog.Error("failed to deactivate API key", "error", err)
		h.sendDeleteError(w, "Error revoking API key")
		return
	}

	slog.Info("API key revoked", "key_id", id, "name", apiKey.Name, "revoked_by", middleware.GetUserID(r))

	// Check if this is an HTMX request
	if r.Header.Get("HX-Request") == "true" {
		// Return empty response for HTMX to remove the row
		w.Header().Set("HX-Trigger", `{"showToast": "API key revoked successfully"}`)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Regular request - redirect with flash message
	flashSuccess(w, r, h.renderer, redirectAdminAPIKeys, "API key revoked successfully")
}

// sendDeleteError sends an error response for delete operations.
func (h *APIKeysHandler) sendDeleteError(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast": "`+message+`", "toastType": "error"}`)
	w.WriteHeader(http.StatusBadRequest)
}

// validateAPIKeyName validates the name field and returns an error message if invalid.
func validateAPIKeyName(name string) string {
	if name == "" {
		return "Name is required"
	}
	if len(name) < 3 {
		return "Name must be at least 3 characters"
	}
	if len(name) > 100 {
		return "Name must be less than 100 characters"
	}
	return ""
}

// validateAPIKeyPermissions validates permissions and returns an error message if invalid.
func validateAPIKeyPermissions(permissions []string) string {
	if len(permissions) == 0 {
		return "At least one permission is required"
	}
	validPerms := model.AllPermissions()
	for _, p := range permissions {
		valid := false
		for _, vp := range validPerms {
			if p == vp {
				valid = true
				break
			}
		}
		if !valid {
			return "Invalid permission: " + p
		}
	}
	return ""
}

// parseAPIKeyExpiration parses expiration date string and returns NullTime and error message.
// If requireFuture is true, the date must be in the future.
func parseAPIKeyExpiration(expiresAtStr string, requireFuture bool) (sql.NullTime, string) {
	if expiresAtStr == "" {
		return sql.NullTime{}, ""
	}
	t, err := time.Parse("2006-01-02", expiresAtStr)
	if err != nil {
		return sql.NullTime{}, "Invalid date format"
	}
	if requireFuture && t.Before(time.Now()) {
		return sql.NullTime{}, "Expiration date must be in the future"
	}
	// Set to end of day
	return sql.NullTime{
		Time:  t.Add(23*time.Hour + 59*time.Minute + 59*time.Second),
		Valid: true,
	}, ""
}

// parseAPIKeyID parses API key ID from URL parameter.
// Returns id, idStr, and whether parsing succeeded.
func parseAPIKeyID(r *http.Request) (int64, string, bool) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, idStr, false
	}
	return id, idStr, true
}

// fetchAPIKey fetches an API key by ID and handles common error cases.
// Returns the API key and whether the fetch succeeded.
func (h *APIKeysHandler) fetchAPIKey(w http.ResponseWriter, r *http.Request, id int64) (store.ApiKey, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminAPIKeys, "API key", id,
		func(id int64) (store.ApiKey, error) { return h.queries.GetAPIKeyByID(r.Context(), id) })
}

// fetchAPIKeyForDelete fetches an API key by ID for delete operations.
// Uses sendDeleteError for error responses (HTMX-compatible).
// Returns the API key and true if successful, or zero value and false if an error occurred.
func (h *APIKeysHandler) fetchAPIKeyForDelete(w http.ResponseWriter, r *http.Request, id int64) (store.ApiKey, bool) {
	return requireEntityWithCustomError(w, "API key", id,
		func(id int64) (store.ApiKey, error) { return h.queries.GetAPIKeyByID(r.Context(), id) },
		h.sendDeleteError)
}

// renderAPIKeyForm renders the API key form with appropriate breadcrumbs.
func (h *APIKeysHandler) renderAPIKeyForm(w http.ResponseWriter, r *http.Request, user any, lang string, title string, data APIKeyFormData, breadcrumbLabel string, breadcrumbURL string) {
	h.renderer.RenderPage(w, r, "admin/api_keys_form", render.TemplateData{
		Title: title,
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.api_keys"), URL: redirectAdminAPIKeys},
			{Label: breadcrumbLabel, URL: breadcrumbURL, Active: true},
		},
	})
}
