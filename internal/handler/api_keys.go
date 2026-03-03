// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
)

// APIKeysPerPage is the number of API keys to display per page.
const APIKeysPerPage = 10

const defaultAPIKeyLifetime = 90 * 24 * time.Hour
const maxAPIKeyLifetime = 365 * 24 * time.Hour
const maxAPIKeySourceCIDRs = 64

// APIKeysHandler handles API key management routes.
type APIKeysHandler struct {
	queries            *store.Queries
	renderer           *render.Renderer
	sessionManager     *scs.SessionManager
	eventService       *service.EventService
	requireSourceCIDRs bool
	requireExpiry      bool
	maxTTLDays         int
}

// NewAPIKeysHandler creates a new APIKeysHandler.
func NewAPIKeysHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *APIKeysHandler {
	return &APIKeysHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		eventService:   service.NewEventService(db),
	}
}

// SetRequireSourceCIDRs configures whether API key create/update requires
// at least one per-key source CIDR/IP entry.
func (h *APIKeysHandler) SetRequireSourceCIDRs(required bool) {
	h.requireSourceCIDRs = required
}

// SetRequireExpiry configures whether API key updates must keep an explicit
// expiration date.
func (h *APIKeysHandler) SetRequireExpiry(required bool) {
	h.requireExpiry = required
}

// SetMaxTTLDays configures the optional maximum lifetime policy for API keys.
// Values <= 0 disable policy enforcement in the admin create/update forms.
func (h *APIKeysHandler) SetMaxTTLDays(days int) {
	if days <= 0 {
		h.maxTTLDays = 0
		return
	}
	h.maxTTLDays = days
}

// List handles GET /admin/api-keys - displays a paginated list of API keys.
func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	adminLang := h.renderer.GetAdminLang(r)

	page := ParsePageParam(r)
	perPage := ParsePerPageParam(r, APIKeysPerPage, maxPerPageSelectionValue)

	// Get total count
	totalKeys, err := h.queries.CountAPIKeys(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to count API keys", "error", err)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalKeys), perPage)
	offset := int64((page - 1) * perPage)

	// Fetch API keys for current page
	apiKeys, err := h.queries.ListAPIKeys(r.Context(), store.ListAPIKeysParams{
		Limit:  int64(perPage),
		Offset: offset,
	})
	if err != nil {
		logAndInternalError(w, "failed to list API keys", "error", err)
		return
	}

	pagination := convertPagination(BuildAdminPagination(page, int(totalKeys), perPage, redirectAdminAPIKeys, r.URL.Query()))
	pagination.BulkAction = bulkPaginationAction(bulkScopeAPIKeys, redirectAdminAPIKeys+RouteSuffixBulkDelete)
	pagination.PerPageSelector = perPageSelector(perPage, perPageOptionsStandard)

	viewData := adminviews.APIKeysListData{
		APIKeys:    convertAPIKeyListItems(apiKeys),
		TotalKeys:  totalKeys,
		Pagination: pagination,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(adminLang, "api_keys.title"), apiKeysBreadcrumbs(adminLang))
	renderTempl(w, r, adminviews.APIKeysListPage(pc, viewData))
}

// NewForm handles GET /admin/api-keys/new - displays the new API key form.
func (h *APIKeysHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionAPIKeys, redirectAdminAPIKeys) {
		return
	}

	h.renderAPIKeyForm(w, r, nil, make(map[string]string), make(map[string]string), false)
}

// Create handles POST /admin/api-keys - creates a new API key.
func (h *APIKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionAPIKeys, redirectAdminAPIKeys) {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminAPIKeysNew) {
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	permissions := r.Form["permissions"]
	expiresAtStr := r.FormValue("expires_at")
	sourceCIDRsRaw := r.FormValue("source_cidrs")

	// Validate form input
	input, validationErrors := validateAPIKeyForm(name, permissions, expiresAtStr, sourceCIDRsRaw, true, h.requireSourceCIDRs)

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		formValues := map[string]string{
			"name":         name,
			"expires_at":   expiresAtStr,
			"source_cidrs": sourceCIDRsRaw,
		}
		h.renderAPIKeyForm(w, r, nil, validationErrors, formValues, false)
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
	keyHash, err := model.HashAPIKey(rawKey)
	if err != nil {
		slog.Error("failed to hash API key", "error", err)
		flashError(w, r, h.renderer, redirectAdminAPIKeysNew, "Error creating API key")
		return
	}

	// Convert permissions to JSON
	permissionsJSON := model.PermissionsToJSON(input.Permissions)

	// Create API key
	now := time.Now()
	expiresAt := applyDefaultAPIKeyExpiry(input.ExpiresAt, now, h.maxTTLDays)
	if lifetimeErr := validateAPIKeyLifetimePolicy(expiresAt, now, h.maxTTLDays); lifetimeErr != "" {
		formValues := map[string]string{
			"name":         name,
			"expires_at":   expiresAtStr,
			"source_cidrs": sourceCIDRsRaw,
		}
		h.renderAPIKeyForm(w, r, nil, map[string]string{"expires_at": lifetimeErr}, formValues, false)
		return
	}
	apiKey, err := h.queries.CreateAPIKey(r.Context(), store.CreateAPIKeyParams{
		Name:        input.Name,
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

	if err := h.queries.ReplaceAPIKeySourceCIDRs(r.Context(), apiKey.ID, input.SourceCIDRs); err != nil {
		if isMissingAPIKeySourceCIDRTable(err) {
			slog.Warn("api key source CIDR table missing; skipping per-key source allowlist", "key_id", apiKey.ID)
		} else {
			slog.Error("failed to save API key source CIDRs", "error", err, "key_id", apiKey.ID)
			_ = h.queries.DeleteAPIKey(r.Context(), apiKey.ID)
			flashError(w, r, h.renderer, redirectAdminAPIKeysNew, "Error creating API key")
			return
		}
	}

	slog.Info("API key created", "key_id", apiKey.ID, "name", apiKey.Name, "created_by", middleware.GetUserID(r))
	_ = h.eventService.LogAPIKeyEvent(r.Context(), model.EventLevelInfo, "API key created", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{
		"key_id":                 apiKey.ID,
		"name":                   apiKey.Name,
		"source_cidr_count":      len(input.SourceCIDRs),
		"source_cidr_restricted": len(input.SourceCIDRs) > 0,
	})

	// Render success page showing the generated key once
	adminLang := h.renderer.GetAdminLang(r)
	viewData := adminviews.APIKeyFormData{
		APIKey:           convertAPIKeyInfo(&apiKey),
		PermissionGroups: buildPermissionGroups(apiKey.Permissions),
		Errors:           make(map[string]string),
		FormValues:       make(map[string]string),
		IsEdit:           false,
		GeneratedKey:     rawKey,
		GeneratedPerms:   parsePermissionsJSON(apiKey.Permissions),
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(adminLang, "api_keys.key_created"), apiKeyFormBreadcrumbs(adminLang, false))
	renderTempl(w, r, adminviews.APIKeyFormPage(pc, viewData))
}

// EditForm handles GET /admin/api-keys/{id} - displays the edit API key form.
func (h *APIKeysHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	id, _, ok := parseAPIKeyID(r)
	if !ok {
		flashError(w, r, h.renderer, redirectAdminAPIKeys, "Invalid API key ID")
		return
	}

	apiKey, ok := h.fetchAPIKey(w, r, id)
	if !ok {
		return
	}
	formValues := make(map[string]string)
	cidrs, err := h.queries.ListAPIKeySourceCIDRs(r.Context(), id)
	if err != nil {
		if !isMissingAPIKeySourceCIDRTable(err) {
			slog.Warn("failed to load API key source CIDRs", "error", err, "key_id", id)
		}
	} else if len(cidrs) > 0 {
		formValues["source_cidrs"] = strings.Join(cidrs, ", ")
	}

	h.renderAPIKeyForm(w, r, &apiKey, make(map[string]string), formValues, true)
}

// Update handles PUT /admin/api-keys/{id} - updates an existing API key.
func (h *APIKeysHandler) Update(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionAPIKeys, redirectAdminAPIKeys) {
		return
	}

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
	sourceCIDRsRaw := r.FormValue("source_cidrs")
	isActive := r.FormValue("is_active") == "on" || r.FormValue("is_active") == "true"

	// Validate form input (don't require future expiry for edits)
	input, validationErrors := validateAPIKeyForm(name, permissions, expiresAtStr, sourceCIDRsRaw, false, h.requireSourceCIDRs)

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		formValues := map[string]string{
			"name":         name,
			"expires_at":   expiresAtStr,
			"source_cidrs": sourceCIDRsRaw,
		}
		h.renderAPIKeyForm(w, r, &apiKey, validationErrors, formValues, true)
		return
	}
	if expiryErr := validateAPIKeyExpiryPolicy(input.ExpiresAt, h.requireExpiry); expiryErr != "" {
		formValues := map[string]string{
			"name":         name,
			"expires_at":   expiresAtStr,
			"source_cidrs": sourceCIDRsRaw,
		}
		h.renderAPIKeyForm(w, r, &apiKey, map[string]string{"expires_at": expiryErr}, formValues, true)
		return
	}
	if lifetimeErr := validateAPIKeyLifetimePolicy(input.ExpiresAt, apiKey.CreatedAt, h.maxTTLDays); lifetimeErr != "" {
		formValues := map[string]string{
			"name":         name,
			"expires_at":   expiresAtStr,
			"source_cidrs": sourceCIDRsRaw,
		}
		h.renderAPIKeyForm(w, r, &apiKey, map[string]string{"expires_at": lifetimeErr}, formValues, true)
		return
	}

	// Convert permissions to JSON
	permissionsJSON := model.PermissionsToJSON(input.Permissions)

	// Update API key
	now := time.Now()
	_, err := h.queries.UpdateAPIKey(r.Context(), store.UpdateAPIKeyParams{
		Name:        input.Name,
		Permissions: permissionsJSON,
		ExpiresAt:   input.ExpiresAt,
		IsActive:    isActive,
		UpdatedAt:   now,
		ID:          id,
	})
	if err != nil {
		slog.Error("failed to update API key", "error", err)
		flashError(w, r, h.renderer, redirectAdminAPIKeysSlash+idStr, "Error updating API key")
		return
	}

	if err := h.queries.ReplaceAPIKeySourceCIDRs(r.Context(), id, input.SourceCIDRs); err != nil {
		if isMissingAPIKeySourceCIDRTable(err) {
			slog.Warn("api key source CIDR table missing; skipping per-key source allowlist", "key_id", id)
		} else {
			slog.Error("failed to update API key source CIDRs", "error", err, "key_id", id)
			flashError(w, r, h.renderer, redirectAdminAPIKeysSlash+idStr, "Error updating API key")
			return
		}
	}

	slog.Info("API key updated", "key_id", id, "updated_by", middleware.GetUserID(r))
	_ = h.eventService.LogAPIKeyEvent(r.Context(), model.EventLevelInfo, "API key updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{
		"key_id":                 id,
		"source_cidr_count":      len(input.SourceCIDRs),
		"source_cidr_restricted": len(input.SourceCIDRs) > 0,
	})
	flashSuccess(w, r, h.renderer, redirectAdminAPIKeys, "API key updated successfully")
}

// Delete handles DELETE /admin/api-keys/{id} - deletes (deactivates) an API key.
func (h *APIKeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		h.sendDeleteError(w, middleware.DemoModeMessageDetailed(middleware.RestrictionAPIKeys))
		return
	}

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
	_ = h.eventService.LogAPIKeyEvent(r.Context(), model.EventLevelInfo, "API key revoked", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"key_id": id, "name": apiKey.Name})

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

// BulkDelete handles POST /admin/api-keys/bulk-delete - revokes multiple API keys.
func (h *APIKeysHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	if middleware.IsDemoMode() {
		writeJSONError(w, http.StatusForbidden, middleware.DemoModeMessageDetailed(middleware.RestrictionAPIKeys))
		return
	}

	ids, err := parseBulkActionIDs(w, r, defaultBulkActionMaxBatch)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	failed := make([]bulkActionFailedItem, 0)
	deleted := 0

	for _, id := range ids {
		apiKey, err := h.queries.GetAPIKeyByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				failed = append(failed, bulkActionFailedItem{ID: id, Reason: "API key not found"})
				continue
			}
			slog.Error("failed to load API key for bulk revoke", "error", err, "key_id", id)
			failed = append(failed, bulkActionFailedItem{ID: id, Reason: "Error loading API key"})
			continue
		}

		if err := h.queries.DeactivateAPIKey(r.Context(), store.DeactivateAPIKeyParams{
			UpdatedAt: time.Now(),
			ID:        id,
		}); err != nil {
			slog.Error("failed to bulk revoke API key", "error", err, "key_id", id)
			failed = append(failed, bulkActionFailedItem{ID: id, Reason: "Error revoking API key"})
			continue
		}

		slog.Info("API key revoked", "key_id", id, "name", apiKey.Name, "revoked_by", middleware.GetUserID(r))
		_ = h.eventService.LogAPIKeyEvent(r.Context(), model.EventLevelInfo, "API key revoked", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"key_id": id, "name": apiKey.Name})
		deleted++
	}

	writeBulkActionSuccess(w, deleted, failed)
}

// sendDeleteError sends an error response for delete operations.
func (h *APIKeysHandler) sendDeleteError(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast": "`+message+`", "toastType": "error"}`)
	w.WriteHeader(http.StatusBadRequest)
}

// apiKeyFormInput holds parsed form input for API key creation/update.
type apiKeyFormInput struct {
	Name        string
	Permissions []string
	ExpiresAt   sql.NullTime
	SourceCIDRs []string
}

func applyDefaultAPIKeyExpiry(expiresAt sql.NullTime, now time.Time, maxTTLDays int) sql.NullTime {
	if expiresAt.Valid {
		return expiresAt
	}
	lifetime := defaultAPIKeyLifetime
	if maxTTLDays > 0 {
		policyLifetime := time.Duration(maxTTLDays) * 24 * time.Hour
		if policyLifetime < lifetime {
			lifetime = policyLifetime
		}
	}
	return sql.NullTime{
		Time:  now.Add(lifetime),
		Valid: true,
	}
}

func validateAPIKeyLifetimePolicy(expiresAt sql.NullTime, createdAt time.Time, maxTTLDays int) string {
	if maxTTLDays <= 0 {
		return ""
	}
	if !expiresAt.Valid {
		return "Expiration date is required when max API key lifetime policy is enabled"
	}
	maxAllowed := createdAt.Add(time.Duration(maxTTLDays) * 24 * time.Hour)
	if expiresAt.Time.After(maxAllowed) {
		return fmt.Sprintf("Expiration date must be within %d days of key creation", maxTTLDays)
	}
	return ""
}

func validateAPIKeyExpiryPolicy(expiresAt sql.NullTime, requireExpiry bool) string {
	if requireExpiry && !expiresAt.Valid {
		return "Expiration date is required by policy"
	}
	return ""
}

// validateAPIKeyForm validates the API key form and returns validation errors.
// requireFutureExpiry controls whether expiration date must be in the future.
func validateAPIKeyForm(name string, permissions []string, expiresAtStr, sourceCIDRsRaw string, requireFutureExpiry, requireSourceCIDRs bool) (apiKeyFormInput, map[string]string) {
	errors := make(map[string]string)

	if err := validateAPIKeyName(name); err != "" {
		errors["name"] = err
	}
	if err := validateAPIKeyPermissions(permissions); err != "" {
		errors["permissions"] = err
	}
	expiresAt, expiresErr := parseAPIKeyExpiration(expiresAtStr, requireFutureExpiry)
	if expiresErr != "" {
		errors["expires_at"] = expiresErr
	}
	sourceCIDRs, sourceCIDRsErr := parseAPIKeySourceCIDRs(sourceCIDRsRaw)
	if sourceCIDRsErr != "" {
		errors["source_cidrs"] = sourceCIDRsErr
	}
	if requireSourceCIDRs && len(sourceCIDRs) == 0 {
		errors["source_cidrs"] = "At least one source CIDR/IP is required"
	}

	return apiKeyFormInput{
		Name:        name,
		Permissions: permissions,
		ExpiresAt:   expiresAt,
		SourceCIDRs: sourceCIDRs,
	}, errors
}

// validateAPIKeyName validates the name field and returns an error message if invalid.
func validateAPIKeyName(name string) string {
	if name == "" {
		return "Name is required"
	}
	if len(name) < 3 {
		return "Name must be at least 3 characters"
	}
	if len(name) > 255 {
		return "Name must be less than 255 characters"
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
		if !slices.Contains(validPerms, p) {
			return "Invalid permission: " + p
		}
	}
	return ""
}

// parseAPIKeyExpiration parses expiration date string and returns NullTime and error message.
// If requireFuture is true, the date must be in the future.
func parseAPIKeyExpiration(expiresAtStr string, requireFuture bool) (sql.NullTime, string) {
	return parseAPIKeyExpirationAt(expiresAtStr, requireFuture, time.Now())
}

func parseAPIKeyExpirationAt(expiresAtStr string, requireFuture bool, now time.Time) (sql.NullTime, string) {
	if expiresAtStr == "" {
		return sql.NullTime{}, ""
	}
	t, err := time.Parse("2006-01-02", expiresAtStr)
	if err != nil {
		return sql.NullTime{}, "Invalid date format"
	}
	if requireFuture && t.Before(now) {
		return sql.NullTime{}, "Expiration date must be in the future"
	}
	expiresAt := t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	if expiresAt.After(now.Add(maxAPIKeyLifetime)) {
		return sql.NullTime{}, fmt.Sprintf("Expiration date must be within %d days", int(maxAPIKeyLifetime.Hours()/24))
	}
	// Set to end of day.
	return sql.NullTime{
		Time:  expiresAt,
		Valid: true,
	}, ""
}

func parseAPIKeySourceCIDRs(raw string) ([]string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ""
	}

	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})

	if len(parts) > maxAPIKeySourceCIDRs {
		return nil, fmt.Sprintf("Too many source CIDR/IP entries (max %d)", maxAPIKeySourceCIDRs)
	}

	seen := make(map[string]struct{}, len(parts))
	normalized := make([]string, 0, len(parts))

	for _, part := range parts {
		prefix, err := parseCIDROrIPForStorage(part)
		if err != nil {
			return nil, fmt.Sprintf("Invalid source CIDR/IP: %s", part)
		}

		value := prefix.String()
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	return normalized, ""
}

func parseCIDROrIPForStorage(value string) (netip.Prefix, error) {
	entry := strings.TrimSpace(value)
	if entry == "" {
		return netip.Prefix{}, fmt.Errorf("empty entry")
	}

	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix.Masked(), nil
	}

	ip, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, err
	}
	if ip.Is4() {
		return netip.PrefixFrom(ip.Unmap(), 32), nil
	}
	return netip.PrefixFrom(ip, 128), nil
}

func isMissingAPIKeySourceCIDRTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") && strings.Contains(msg, "api_key_source_cidrs")
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

// renderAPIKeyForm renders the API key form using templ.
func (h *APIKeysHandler) renderAPIKeyForm(w http.ResponseWriter, r *http.Request, apiKey *store.ApiKey, errs map[string]string, formValues map[string]string, isEdit bool) {
	adminLang := h.renderer.GetAdminLang(r)

	// Build permission groups with checked state
	existingPerms := ""
	if isEdit && apiKey != nil {
		existingPerms = apiKey.Permissions
	}

	viewData := adminviews.APIKeyFormData{
		IsEdit:           isEdit,
		APIKey:           convertAPIKeyInfo(apiKey),
		PermissionGroups: buildPermissionGroups(existingPerms),
		Errors:           errs,
		FormValues:       formValues,
		GeneratedKey:     "",
	}

	var title string
	var breadcrumbs []render.Breadcrumb
	if isEdit && apiKey != nil {
		title = i18n.T(adminLang, "api_keys.edit_key")
		breadcrumbs = apiKeyEditBreadcrumbs(adminLang, apiKey.Name, apiKey.ID)
	} else {
		title = i18n.T(adminLang, "api_keys.new_key")
		breadcrumbs = apiKeyFormBreadcrumbs(adminLang, false)
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, title, breadcrumbs)
	renderTempl(w, r, adminviews.APIKeyFormPage(pc, viewData))
}
