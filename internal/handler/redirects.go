// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// RedirectsHandler handles redirect management routes.
type RedirectsHandler struct {
	queries             *store.Queries
	renderer            *render.Renderer
	sessionManager      *scs.SessionManager
	eventService        *service.EventService
	redirectsMiddleware *middleware.RedirectsMiddleware
}

// NewRedirectsHandler creates a new RedirectsHandler.
func NewRedirectsHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, rm *middleware.RedirectsMiddleware) *RedirectsHandler {
	return &RedirectsHandler{
		queries:             store.New(db),
		renderer:            renderer,
		sessionManager:      sm,
		eventService:        service.NewEventService(db),
		redirectsMiddleware: rm,
	}
}

// RedirectsListData holds data for the redirects list template.
type RedirectsListData struct {
	Redirects  []store.Redirect
	TotalCount int64
}

// List handles GET /admin/redirects - displays a list of redirects.
func (h *RedirectsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	redirects, err := h.queries.ListRedirects(r.Context())
	if err != nil {
		slog.Error("failed to list redirects", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	count, err := h.queries.CountRedirects(r.Context())
	if err != nil {
		slog.Error("failed to count redirects", "error", err)
		count = 0
	}

	data := RedirectsListData{
		Redirects:  redirects,
		TotalCount: count,
	}

	h.renderer.RenderPage(w, r, "admin/redirects_list", render.TemplateData{
		Title: i18n.T(lang, "nav.redirects"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects, Active: true},
		},
	})
}

// RedirectFormData holds data for the redirect form template.
type RedirectFormData struct {
	Redirect    *store.Redirect
	StatusCodes []StatusCodeOption
	TargetTypes []string
	Errors      map[string]string
	FormValues  map[string]string
	IsEdit      bool
}

// StatusCodeOption represents a status code option for the form select.
type StatusCodeOption struct {
	Code  int
	Label string
}

// ValidStatusCodes contains valid HTTP redirect status codes.
var ValidStatusCodes = []StatusCodeOption{
	{Code: 301, Label: "301 - Permanent Redirect"},
	{Code: 302, Label: "302 - Temporary Redirect"},
	{Code: 307, Label: "307 - Temporary Redirect (preserve method)"},
	{Code: 308, Label: "308 - Permanent Redirect (preserve method)"},
}

// NewForm handles GET /admin/redirects/new - displays the new redirect form.
func (h *RedirectsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	data := RedirectFormData{
		StatusCodes: ValidStatusCodes,
		TargetTypes: model.ValidTargets,
		Errors:      make(map[string]string),
		FormValues: map[string]string{
			"status_code": "301",
			"target_type": model.TargetSelf,
			"enabled":     "true",
		},
		IsEdit: false,
	}

	h.renderer.RenderPage(w, r, "admin/redirects_form", render.TemplateData{
		Title: i18n.T(lang, "redirects.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects},
			{Label: i18n.T(lang, "redirects.new"), URL: redirectAdminRedirectsNew, Active: true},
		},
	})
}

// Create handles POST /admin/redirects - creates a new redirect.
func (h *RedirectsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionContentReadOnly, redirectAdminRedirectsNew) {
		return
	}

	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminRedirectsNew) {
		return
	}

	input := parseRedirectFormInput(r)

	// Validate source path uniqueness
	if errMsg := h.validateSourcePathCreate(r.Context(), input.SourcePath); errMsg != "" {
		input.Errors["source_path"] = errMsg
	}

	if len(input.Errors) > 0 {
		data := RedirectFormData{
			StatusCodes: ValidStatusCodes,
			TargetTypes: model.ValidTargets,
			Errors:      input.Errors,
			FormValues:  input.FormValues,
			IsEdit:      false,
		}

		h.renderer.RenderPage(w, r, "admin/redirects_form", render.TemplateData{
			Title: i18n.T(lang, "redirects.new"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects},
				{Label: i18n.T(lang, "redirects.new"), URL: redirectAdminRedirectsNew, Active: true},
			},
		})
		return
	}

	now := time.Now()
	redirect, err := h.queries.CreateRedirect(r.Context(), store.CreateRedirectParams{
		SourcePath: input.SourcePath,
		TargetUrl:  input.TargetURL,
		StatusCode: input.StatusCode,
		IsWildcard: input.IsWildcard,
		TargetType: input.TargetType,
		Enabled:    input.Enabled,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to create redirect", "error", err)
		flashError(w, r, h.renderer, redirectAdminRedirectsNew, "Error creating redirect")
		return
	}

	slog.Info("redirect created", "redirect_id", redirect.ID, "source_path", redirect.SourcePath)
	_ = h.eventService.LogConfigEvent(r.Context(), model.EventLevelInfo, "Redirect created", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"redirect_id": redirect.ID, "source_path": redirect.SourcePath, "target_url": redirect.TargetUrl})
	h.redirectsMiddleware.InvalidateCache()
	flashSuccess(w, r, h.renderer, redirectAdminRedirects, "Redirect created successfully")
}

// EditForm handles GET /admin/redirects/{id} - displays the redirect edit form.
func (h *RedirectsHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminRedirects, "Invalid redirect ID")
		return
	}

	redirect, ok := h.requireRedirectWithRedirect(w, r, id)
	if !ok {
		return
	}

	data := RedirectFormData{
		Redirect:    &redirect,
		StatusCodes: ValidStatusCodes,
		TargetTypes: model.ValidTargets,
		Errors:      make(map[string]string),
		FormValues:  make(map[string]string),
		IsEdit:      true,
	}

	h.renderer.RenderPage(w, r, "admin/redirects_form", render.TemplateData{
		Title: i18n.T(lang, "redirects.edit"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects},
			{Label: redirect.SourcePath, URL: "", Active: true},
		},
	})
}

// Update handles PUT /admin/redirects/{id} - updates a redirect.
func (h *RedirectsHandler) Update(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionContentReadOnly, redirectAdminRedirects) {
		return
	}

	user := middleware.GetUser(r)
	lang := h.renderer.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminRedirects, "Invalid redirect ID")
		return
	}

	redirect, ok := h.requireRedirectWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminRedirects) {
		return
	}

	input := parseRedirectFormInput(r)

	// Validate source path uniqueness (only if changed)
	if input.SourcePath != redirect.SourcePath {
		if errMsg := h.validateSourcePathUpdate(r.Context(), input.SourcePath, id); errMsg != "" {
			input.Errors["source_path"] = errMsg
		}
	}

	if len(input.Errors) > 0 {
		data := RedirectFormData{
			Redirect:    &redirect,
			StatusCodes: ValidStatusCodes,
			TargetTypes: model.ValidTargets,
			Errors:      input.Errors,
			FormValues:  input.FormValues,
			IsEdit:      true,
		}

		h.renderer.RenderPage(w, r, "admin/redirects_form", render.TemplateData{
			Title: i18n.T(lang, "redirects.edit"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
				{Label: i18n.T(lang, "nav.redirects"), URL: redirectAdminRedirects},
				{Label: redirect.SourcePath, URL: "", Active: true},
			},
		})
		return
	}

	now := time.Now()
	_, err = h.queries.UpdateRedirect(r.Context(), store.UpdateRedirectParams{
		ID:         id,
		SourcePath: input.SourcePath,
		TargetUrl:  input.TargetURL,
		StatusCode: input.StatusCode,
		IsWildcard: input.IsWildcard,
		TargetType: input.TargetType,
		Enabled:    input.Enabled,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to update redirect", "error", err, "redirect_id", id)
		flashError(w, r, h.renderer, redirectAdminRedirects, "Error updating redirect")
		return
	}

	slog.Info("redirect updated", "redirect_id", id, "updated_by", middleware.GetUserID(r))
	_ = h.eventService.LogConfigEvent(r.Context(), model.EventLevelInfo, "Redirect updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"redirect_id": id, "source_path": input.SourcePath})
	h.redirectsMiddleware.InvalidateCache()
	flashSuccess(w, r, h.renderer, redirectAdminRedirects, "Redirect updated successfully")
}

// Delete handles DELETE /admin/redirects/{id} - deletes a redirect.
func (h *RedirectsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w, middleware.RestrictionContentReadOnly) {
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid redirect ID", http.StatusBadRequest)
		return
	}

	redirect, ok := h.requireRedirectWithError(w, r, id)
	if !ok {
		return
	}

	err = h.queries.DeleteRedirect(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete redirect", "error", err, "redirect_id", id)
		http.Error(w, "Error deleting redirect", http.StatusInternalServerError)
		return
	}

	slog.Info("redirect deleted", "redirect_id", id, "source_path", redirect.SourcePath, "deleted_by", middleware.GetUserID(r))
	_ = h.eventService.LogConfigEvent(r.Context(), model.EventLevelInfo, "Redirect deleted", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"redirect_id": id, "source_path": redirect.SourcePath})
	h.redirectsMiddleware.InvalidateCache()

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminRedirects, "Redirect deleted successfully")
}

// Toggle handles POST /admin/redirects/{id}/toggle - toggles a redirect's enabled status.
func (h *RedirectsHandler) Toggle(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w, middleware.RestrictionContentReadOnly) {
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid redirect ID", http.StatusBadRequest)
		return
	}

	redirect, ok := h.requireRedirectWithError(w, r, id)
	if !ok {
		return
	}

	now := time.Now()
	err = h.queries.ToggleRedirectEnabled(r.Context(), store.ToggleRedirectEnabledParams{
		ID:        id,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to toggle redirect", "error", err, "redirect_id", id)
		http.Error(w, "Error toggling redirect", http.StatusInternalServerError)
		return
	}

	newStatus := !redirect.Enabled
	slog.Info("redirect toggled", "redirect_id", id, "enabled", newStatus, "toggled_by", middleware.GetUserID(r))
	_ = h.eventService.LogConfigEvent(r.Context(), model.EventLevelInfo, "Redirect toggled", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"redirect_id": id, "enabled": newStatus})
	h.redirectsMiddleware.InvalidateCache()

	if r.Header.Get("HX-Request") == "true" {
		// Return 204 No Content so htmx doesn't swap (row stays visible)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminRedirects, "Redirect status updated")
}

// Helper functions

// requireRedirectWithRedirect fetches redirect by ID and handles errors with flash messages and redirect.
func (h *RedirectsHandler) requireRedirectWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Redirect, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminRedirects, "Redirect", id,
		func(id int64) (store.Redirect, error) { return h.queries.GetRedirectByID(r.Context(), id) })
}

// requireRedirectWithError fetches redirect by ID and handles errors with http.Error.
func (h *RedirectsHandler) requireRedirectWithError(w http.ResponseWriter, r *http.Request, id int64) (store.Redirect, bool) {
	return requireEntityWithError(w, "Redirect", id,
		func(id int64) (store.Redirect, error) { return h.queries.GetRedirectByID(r.Context(), id) })
}

// redirectFormInput holds parsed and validated redirect form input.
type redirectFormInput struct {
	SourcePath string
	TargetURL  string
	StatusCode int64
	IsWildcard bool
	TargetType string
	Enabled    bool
	FormValues map[string]string
	Errors     map[string]string
}

// parseRedirectFormInput parses and validates redirect form input.
func parseRedirectFormInput(r *http.Request) redirectFormInput {
	sourcePath := strings.TrimSpace(r.FormValue("source_path"))
	targetURL := strings.TrimSpace(r.FormValue("target_url"))
	statusCodeStr := r.FormValue("status_code")
	// Auto-detect wildcards: if source path contains * or **, set isWildcard automatically
	isWildcard := r.FormValue("is_wildcard") == "true" ||
		r.FormValue("is_wildcard") == "on" ||
		strings.Contains(sourcePath, "*")
	targetType := r.FormValue("target_type")
	enabled := r.FormValue("enabled") == "true" || r.FormValue("enabled") == "on"

	// Parse status code
	statusCode, err := strconv.ParseInt(statusCodeStr, 10, 64)
	if err != nil {
		statusCode = 301 // Default to permanent redirect
	}

	// Default target type
	if targetType == "" {
		targetType = model.TargetSelf
	}

	formValues := map[string]string{
		"source_path": sourcePath,
		"target_url":  targetURL,
		"status_code": statusCodeStr,
		"target_type": targetType,
	}
	if isWildcard {
		formValues["is_wildcard"] = "true"
	}
	if enabled {
		formValues["enabled"] = "true"
	}

	validationErrors := make(map[string]string)

	// Validate source path
	if sourcePath == "" {
		validationErrors["source_path"] = "Source path is required"
	} else if !strings.HasPrefix(sourcePath, "/") {
		validationErrors["source_path"] = "Source path must start with /"
	}

	// Validate target URL
	if targetURL == "" {
		validationErrors["target_url"] = "Target URL is required"
	}

	// Validate status code
	validStatusCode := false
	for _, sc := range ValidStatusCodes {
		if int64(sc.Code) == statusCode {
			validStatusCode = true
			break
		}
	}
	if !validStatusCode {
		validationErrors["status_code"] = "Invalid status code"
		statusCode = 301
	}

	// Validate target type
	if !model.IsValidTarget(targetType) {
		validationErrors["target_type"] = "Invalid target type"
		targetType = model.TargetSelf
	}

	return redirectFormInput{
		SourcePath: sourcePath,
		TargetURL:  targetURL,
		StatusCode: statusCode,
		IsWildcard: isWildcard,
		TargetType: targetType,
		Enabled:    enabled,
		FormValues: formValues,
		Errors:     validationErrors,
	}
}

// validateSourcePathCreate validates that a source path doesn't exist.
func (h *RedirectsHandler) validateSourcePathCreate(ctx context.Context, sourcePath string) string {
	exists, err := h.queries.RedirectSourcePathExists(ctx, sourcePath)
	if err != nil {
		slog.Error("failed to check source path existence", "error", err)
		return "Error validating source path"
	}
	if exists == 1 {
		return "A redirect with this source path already exists"
	}
	return ""
}

// validateSourcePathUpdate validates that a source path doesn't exist for other redirects.
func (h *RedirectsHandler) validateSourcePathUpdate(ctx context.Context, sourcePath string, excludeID int64) string {
	exists, err := h.queries.RedirectSourcePathExistsExcluding(ctx, store.RedirectSourcePathExistsExcludingParams{
		SourcePath: sourcePath,
		ID:         excludeID,
	})
	if err != nil {
		slog.Error("failed to check source path existence", "error", err)
		return "Error validating source path"
	}
	if exists == 1 {
		return "A redirect with this source path already exists"
	}
	return ""
}
