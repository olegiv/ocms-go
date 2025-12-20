package handler

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
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

	// Get page number from query string
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get total count
	totalKeys, err := h.queries.CountAPIKeys(r.Context())
	if err != nil {
		slog.Error("failed to count API keys", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalKeys + APIKeysPerPage - 1) / APIKeysPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := int64((page - 1) * APIKeysPerPage)

	// Fetch API keys for current page
	apiKeys, err := h.queries.ListAPIKeys(r.Context(), store.ListAPIKeysParams{
		Limit:  APIKeysPerPage,
		Offset: offset,
	})
	if err != nil {
		slog.Error("failed to list API keys", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := APIKeysListData{
		APIKeys:    apiKeys,
		TotalKeys:  totalKeys,
		Pagination: BuildAdminPagination(page, int(totalKeys), APIKeysPerPage, "/admin/api-keys", r.URL.Query()),
	}

	if err := h.renderer.Render(w, r, "admin/api_keys_list", render.TemplateData{
		Title: i18n.T(lang, "api_keys.title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.api_keys"), URL: "/admin/api-keys", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
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

	if err := h.renderer.Render(w, r, "admin/api_keys_form", render.TemplateData{
		Title: i18n.T(lang, "api_keys.new_key"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.api_keys"), URL: "/admin/api-keys"},
			{Label: i18n.T(lang, "api_keys.new_key"), URL: "/admin/api-keys/new", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Create handles POST /admin/api-keys - creates a new API key.
func (h *APIKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/api-keys/new", http.StatusSeeOther)
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

	// Name validation
	if name == "" {
		validationErrors["name"] = "Name is required"
	} else if len(name) < 3 {
		validationErrors["name"] = "Name must be at least 3 characters"
	} else if len(name) > 100 {
		validationErrors["name"] = "Name must be less than 100 characters"
	}

	// Permissions validation
	if len(permissions) == 0 {
		validationErrors["permissions"] = "At least one permission is required"
	} else {
		// Validate each permission
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
				validationErrors["permissions"] = "Invalid permission: " + p
				break
			}
		}
	}

	// Optional expiration date validation
	var expiresAt sql.NullTime
	if expiresAtStr != "" {
		t, err := time.Parse("2006-01-02", expiresAtStr)
		if err != nil {
			validationErrors["expires_at"] = "Invalid date format"
		} else if t.Before(time.Now()) {
			validationErrors["expires_at"] = "Expiration date must be in the future"
		} else {
			// Set to end of day
			expiresAt = sql.NullTime{
				Time:  t.Add(23*time.Hour + 59*time.Minute + 59*time.Second),
				Valid: true,
			}
		}
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		data := APIKeyFormData{
			Permissions: model.AllPermissions(),
			Errors:      validationErrors,
			FormValues:  formValues,
			IsEdit:      false,
		}

		if err := h.renderer.Render(w, r, "admin/api_keys_form", render.TemplateData{
			Title: i18n.T(lang, "api_keys.new_key"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.api_keys"), URL: "/admin/api-keys"},
				{Label: i18n.T(lang, "api_keys.new_key"), URL: "/admin/api-keys/new", Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Generate API key
	rawKey, prefix, err := model.GenerateAPIKey()
	if err != nil {
		slog.Error("failed to generate API key", "error", err)
		h.renderer.SetFlash(r, "Error generating API key", "error")
		http.Redirect(w, r, "/admin/api-keys/new", http.StatusSeeOther)
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
		h.renderer.SetFlash(r, "Error creating API key", "error")
		http.Redirect(w, r, "/admin/api-keys/new", http.StatusSeeOther)
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

	if err := h.renderer.Render(w, r, "admin/api_keys_form", render.TemplateData{
		Title: i18n.T(lang, "api_keys.key_created"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.api_keys"), URL: "/admin/api-keys"},
			{Label: i18n.T(lang, "api_keys.new_key"), URL: "/admin/api-keys/new", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// EditForm handles GET /admin/api-keys/{id} - displays the edit API key form.
func (h *APIKeysHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	// Get API key ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid API key ID", "error")
		http.Redirect(w, r, "/admin/api-keys", http.StatusSeeOther)
		return
	}

	// Fetch API key
	apiKey, err := h.queries.GetAPIKeyByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "API key not found", "error")
		} else {
			slog.Error("failed to get API key", "error", err)
			h.renderer.SetFlash(r, "Error loading API key", "error")
		}
		http.Redirect(w, r, "/admin/api-keys", http.StatusSeeOther)
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

	if err := h.renderer.Render(w, r, "admin/api_keys_form", render.TemplateData{
		Title: i18n.T(lang, "api_keys.edit_key"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.api_keys"), URL: "/admin/api-keys"},
			{Label: apiKey.Name, URL: "/admin/api-keys/" + idStr, Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles PUT /admin/api-keys/{id} - updates an existing API key.
func (h *APIKeysHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	// Get API key ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid API key ID", "error")
		http.Redirect(w, r, "/admin/api-keys", http.StatusSeeOther)
		return
	}

	// Fetch the API key being edited
	apiKey, err := h.queries.GetAPIKeyByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderer.SetFlash(r, "API key not found", "error")
		} else {
			slog.Error("failed to get API key", "error", err)
			h.renderer.SetFlash(r, "Error loading API key", "error")
		}
		http.Redirect(w, r, "/admin/api-keys", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/api-keys/"+idStr, http.StatusSeeOther)
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

	// Name validation
	if name == "" {
		validationErrors["name"] = "Name is required"
	} else if len(name) < 3 {
		validationErrors["name"] = "Name must be at least 3 characters"
	} else if len(name) > 100 {
		validationErrors["name"] = "Name must be less than 100 characters"
	}

	// Permissions validation
	if len(permissions) == 0 {
		validationErrors["permissions"] = "At least one permission is required"
	} else {
		// Validate each permission
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
				validationErrors["permissions"] = "Invalid permission: " + p
				break
			}
		}
	}

	// Optional expiration date validation
	var expiresAt sql.NullTime
	if expiresAtStr != "" {
		t, err := time.Parse("2006-01-02", expiresAtStr)
		if err != nil {
			validationErrors["expires_at"] = "Invalid date format"
		} else {
			// Set to end of day
			expiresAt = sql.NullTime{
				Time:  t.Add(23*time.Hour + 59*time.Minute + 59*time.Second),
				Valid: true,
			}
		}
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

		if err := h.renderer.Render(w, r, "admin/api_keys_form", render.TemplateData{
			Title: i18n.T(lang, "api_keys.edit_key"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.api_keys"), URL: "/admin/api-keys"},
				{Label: apiKey.Name, URL: "/admin/api-keys/" + idStr, Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Convert permissions to JSON
	permissionsJSON := model.PermissionsToJSON(permissions)

	// Update API key
	now := time.Now()
	_, err = h.queries.UpdateAPIKey(r.Context(), store.UpdateAPIKeyParams{
		Name:        name,
		Permissions: permissionsJSON,
		ExpiresAt:   expiresAt,
		IsActive:    isActive,
		UpdatedAt:   now,
		ID:          id,
	})
	if err != nil {
		slog.Error("failed to update API key", "error", err)
		h.renderer.SetFlash(r, "Error updating API key", "error")
		http.Redirect(w, r, "/admin/api-keys/"+idStr, http.StatusSeeOther)
		return
	}

	slog.Info("API key updated", "key_id", id, "updated_by", middleware.GetUserID(r))
	h.renderer.SetFlash(r, "API key updated successfully", "success")
	http.Redirect(w, r, "/admin/api-keys", http.StatusSeeOther)
}

// Delete handles DELETE /admin/api-keys/{id} - deletes (deactivates) an API key.
func (h *APIKeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Get API key ID from URL
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.sendDeleteError(w, "Invalid API key ID")
		return
	}

	// Fetch the API key being deleted
	apiKey, err := h.queries.GetAPIKeyByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.sendDeleteError(w, "API key not found")
		} else {
			slog.Error("failed to get API key", "error", err)
			h.sendDeleteError(w, "Error loading API key")
		}
		return
	}

	// Deactivate API key (soft delete)
	now := time.Now()
	err = h.queries.DeactivateAPIKey(r.Context(), store.DeactivateAPIKeyParams{
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
	h.renderer.SetFlash(r, "API key revoked successfully", "success")
	http.Redirect(w, r, "/admin/api-keys", http.StatusSeeOther)
}

// sendDeleteError sends an error response for delete operations.
func (h *APIKeysHandler) sendDeleteError(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast": "`+message+`", "toastType": "error"}`)
	w.WriteHeader(http.StatusBadRequest)
}
