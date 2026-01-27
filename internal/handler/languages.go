// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/util"

	"github.com/alexedwards/scs/v2"
)

// LanguagesHandler handles language management in admin.
type LanguagesHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewLanguagesHandler creates a new LanguagesHandler.
func NewLanguagesHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *LanguagesHandler {
	return &LanguagesHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// LanguagesListData holds data for the languages list template.
type LanguagesListData struct {
	Languages      []store.Language
	TotalLanguages int64
}

// LanguageFormData holds data for the language form template.
type LanguageFormData struct {
	Language        *store.Language
	CommonLanguages []struct {
		Code       string
		Name       string
		NativeName string
		Direction  string
	}
	Errors     map[string]string
	FormValues map[string]string
	IsEdit     bool
}

// languageFormInput holds parsed form values for language create/update.
type languageFormInput struct {
	Code       string
	Name       string
	NativeName string
	Direction  string
	IsActive   bool
	Position   string
}

// parseLanguageFormInput extracts language form field values from the request.
func parseLanguageFormInput(r *http.Request) languageFormInput {
	isActiveStr := r.FormValue("is_active")
	return languageFormInput{
		Code:       strings.TrimSpace(r.FormValue("code")),
		Name:       strings.TrimSpace(r.FormValue("name")),
		NativeName: strings.TrimSpace(r.FormValue("native_name")),
		Direction:  strings.TrimSpace(r.FormValue("direction")),
		IsActive:   isActiveStr == "1" || isActiveStr == "on" || isActiveStr == "true",
		Position:   strings.TrimSpace(r.FormValue("position")),
	}
}

// toFormValues converts the input to a map for template re-rendering.
func (input languageFormInput) toFormValues() map[string]string {
	fv := map[string]string{
		"code":        input.Code,
		"name":        input.Name,
		"native_name": input.NativeName,
		"direction":   input.Direction,
		"position":    input.Position,
	}
	if input.IsActive {
		fv["is_active"] = "1"
	}
	return fv
}

// getLanguageByIDParam parses the language ID from URL and fetches the language.
// Returns nil and sends an error response if the language is not found.
func (h *LanguagesHandler) getLanguageByIDParam(w http.ResponseWriter, r *http.Request) *store.Language {
	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return nil
	}

	lang, err := h.queries.GetLanguageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			logAndInternalError(w, "failed to get language", "error", err)
		}
		return nil
	}
	return &lang
}

// renderLanguageForm renders the language form template.
func (h *LanguagesHandler) renderLanguageForm(w http.ResponseWriter, r *http.Request, user interface{}, data LanguageFormData) {
	lang := middleware.GetAdminLang(r)

	var title, lastLabel, lastURL string
	if data.IsEdit && data.Language != nil {
		title = i18n.T(lang, "languages.edit")
		lastLabel = i18n.T(lang, "languages.edit")
		lastURL = fmt.Sprintf(redirectAdminLanguagesID, data.Language.ID)
	} else {
		title = i18n.T(lang, "languages.new")
		lastLabel = i18n.T(lang, "languages.new")
		lastURL = redirectAdminLanguagesNew
	}

	h.renderer.RenderPage(w, r, "admin/languages_form", render.TemplateData{
		Title: title,
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.languages"), URL: redirectAdminLanguages},
			{Label: lastLabel, URL: lastURL, Active: true},
		},
	})
}

// List displays all languages.
func (h *LanguagesHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	languages, err := h.queries.ListLanguages(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to list languages", "error", err)
		return
	}

	totalLanguages, err := h.queries.CountLanguages(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to count languages", "error", err)
		return
	}

	data := LanguagesListData{
		Languages:      languages,
		TotalLanguages: totalLanguages,
	}

	h.renderer.RenderPage(w, r, "admin/languages_list", render.TemplateData{
		Title: i18n.T(lang, "nav.languages"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.languages"), URL: redirectAdminLanguages, Active: true},
		},
	})
}

// NewForm displays the form to create a new language.
func (h *LanguagesHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	h.renderLanguageForm(w, r, user, LanguageFormData{
		CommonLanguages: model.CommonLanguages,
		Errors:          make(map[string]string),
		FormValues:      make(map[string]string),
		IsEdit:          false,
	})
}

// Create handles creating a new language.
func (h *LanguagesHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminLanguagesNew) {
		return
	}

	input := parseLanguageFormInput(r)

	position := int64(0)
	if input.Position != "" {
		if p, err := strconv.ParseInt(input.Position, 10, 64); err == nil {
			position = p
		}
	} else {
		// Get max position and add 1
		maxPos, err := h.queries.GetMaxLanguagePosition(r.Context())
		if err == nil && maxPos != nil {
			switch v := maxPos.(type) {
			case int64:
				position = v + 1
			case int:
				position = int64(v) + 1
			case float64:
				position = int64(v) + 1
			}
		}
	}

	// Default direction to ltr
	direction := input.Direction
	if direction == "" {
		direction = model.DirectionLTR
	}

	// Update input with computed values for form re-rendering
	input.Direction = direction
	input.Position = strconv.FormatInt(position, 10)

	validationErrors := make(map[string]string)

	// Validate code
	switch {
	case input.Code == "":
		validationErrors["code"] = "Language code is required"
	case len(input.Code) < 2 || len(input.Code) > 5:
		validationErrors["code"] = "Language code must be 2-5 characters"
	default:
		exists, err := h.queries.LanguageCodeExists(r.Context(), input.Code)
		if err != nil {
			slog.Error("database error checking language code", "error", err)
		} else if exists != 0 {
			validationErrors["code"] = "Language code already exists"
		}
	}

	// Validate name
	if input.Name == "" {
		validationErrors["name"] = "Name is required"
	}

	// Validate native name
	if input.NativeName == "" {
		validationErrors["native_name"] = "Native name is required"
	}

	// Validate direction
	if direction != model.DirectionLTR && direction != model.DirectionRTL {
		validationErrors["direction"] = "Direction must be ltr or rtl"
	}

	if len(validationErrors) > 0 {
		h.renderLanguageForm(w, r, user, LanguageFormData{
			CommonLanguages: model.CommonLanguages,
			Errors:          validationErrors,
			FormValues:      input.toFormValues(),
			IsEdit:          false,
		})
		return
	}

	now := time.Now()
	newLang, err := h.queries.CreateLanguage(r.Context(), store.CreateLanguageParams{
		Code:       input.Code,
		Name:       input.Name,
		NativeName: input.NativeName,
		IsDefault:  false,
		IsActive:   input.IsActive,
		Direction:  direction,
		Position:   position,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to create language", "error", err)
		flashError(w, r, h.renderer, redirectAdminLanguagesNew, "Error creating language")
		return
	}

	slog.Info("language created", "language_id", newLang.ID, "code", newLang.Code)
	flashSuccess(w, r, h.renderer, redirectAdminLanguages, "Language created successfully")
}

// languageToFormValues converts a store.Language to form values map.
func languageToFormValues(lang *store.Language) map[string]string {
	fv := map[string]string{
		"code":        lang.Code,
		"name":        lang.Name,
		"native_name": lang.NativeName,
		"direction":   lang.Direction,
		"position":    strconv.FormatInt(lang.Position, 10),
	}
	if lang.IsActive {
		fv["is_active"] = "1"
	}
	return fv
}

// EditForm displays the form to edit an existing language.
func (h *LanguagesHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	lang := h.getLanguageByIDParam(w, r)
	if lang == nil {
		return
	}

	h.renderLanguageForm(w, r, user, LanguageFormData{
		Language:        lang,
		CommonLanguages: model.CommonLanguages,
		Errors:          make(map[string]string),
		FormValues:      languageToFormValues(lang),
		IsEdit:          true,
	})
}

// Update handles updating an existing language.
func (h *LanguagesHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	existingLang := h.getLanguageByIDParam(w, r)
	if existingLang == nil {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminLanguagesID, existingLang.ID)) {
		return
	}

	input := parseLanguageFormInput(r)

	position := existingLang.Position
	if input.Position != "" {
		if p, err := strconv.ParseInt(input.Position, 10, 64); err == nil {
			position = p
		}
	}

	// Default direction to ltr
	direction := input.Direction
	if direction == "" {
		direction = model.DirectionLTR
	}

	// Update input with computed values for form re-rendering
	input.Direction = direction
	input.Position = strconv.FormatInt(position, 10)

	validationErrors := make(map[string]string)

	// Validate code
	switch {
	case input.Code == "":
		validationErrors["code"] = "Language code is required"
	case len(input.Code) < 2 || len(input.Code) > 5:
		validationErrors["code"] = "Language code must be 2-5 characters"
	default:
		exists, err := h.queries.LanguageCodeExistsExcluding(r.Context(), store.LanguageCodeExistsExcludingParams{
			Code: input.Code,
			ID:   existingLang.ID,
		})
		if err != nil {
			slog.Error("database error checking language code", "error", err)
		} else if exists != 0 {
			validationErrors["code"] = "Language code already exists"
		}
	}

	// Validate name
	if input.Name == "" {
		validationErrors["name"] = "Name is required"
	}

	// Validate native name
	if input.NativeName == "" {
		validationErrors["native_name"] = "Native name is required"
	}

	// Validate direction
	if direction != model.DirectionLTR && direction != model.DirectionRTL {
		validationErrors["direction"] = "Direction must be ltr or rtl"
	}

	// Cannot deactivate default language
	if existingLang.IsDefault && !input.IsActive {
		validationErrors["is_active"] = "Cannot deactivate the default language"
	}

	if len(validationErrors) > 0 {
		h.renderLanguageForm(w, r, user, LanguageFormData{
			Language:        existingLang,
			CommonLanguages: model.CommonLanguages,
			Errors:          validationErrors,
			FormValues:      input.toFormValues(),
			IsEdit:          true,
		})
		return
	}

	now := time.Now()
	_, err := h.queries.UpdateLanguage(r.Context(), store.UpdateLanguageParams{
		ID:         existingLang.ID,
		Code:       input.Code,
		Name:       input.Name,
		NativeName: input.NativeName,
		IsDefault:  existingLang.IsDefault, // Keep existing default status
		IsActive:   input.IsActive,
		Direction:  direction,
		Position:   position,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to update language", "error", err)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminLanguagesID, existingLang.ID), "Error updating language")
		return
	}

	slog.Info("language updated", "language_id", existingLang.ID, "code", input.Code)
	flashSuccess(w, r, h.renderer, redirectAdminLanguages, "Language updated successfully")
}

// Delete handles deleting a language.
func (h *LanguagesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	lang, err := h.queries.GetLanguageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Reswap", "none")
				http.Error(w, "Language not found", http.StatusNotFound)
				return
			}
			http.NotFound(w, r)
		} else {
			slog.Error("failed to get language", "error", err, "language_id", id)
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Reswap", "none")
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Cannot delete default language
	if lang.IsDefault {
		errMsg := "Cannot delete the default language"
		slog.Warn("attempted to delete default language", "language_id", id, "code", lang.Code)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, errMsg, http.StatusBadRequest)
			return
		}
		flashError(w, r, h.renderer, redirectAdminLanguages, errMsg)
		return
	}

	// Check if there are pages linked to this language
	pageCount, err := h.queries.CountPagesByLanguageID(r.Context(), util.NullInt64FromValue(id))
	if err != nil {
		slog.Error("failed to count pages for language", "error", err, "language_id", id)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, "Failed to check language usage", http.StatusInternalServerError)
			return
		}
		flashError(w, r, h.renderer, redirectAdminLanguages, "Failed to check language usage")
		return
	}

	if pageCount > 0 {
		errMsg := fmt.Sprintf("Cannot delete language: %d page(s) are using this language", pageCount)
		slog.Warn("attempted to delete language with linked pages",
			"language_id", id, "code", lang.Code, "page_count", pageCount)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, errMsg, http.StatusConflict)
			return
		}
		flashError(w, r, h.renderer, redirectAdminLanguages, errMsg)
		return
	}

	if err := h.queries.DeleteLanguage(r.Context(), id); err != nil {
		slog.Error("failed to delete language", "error", err, "language_id", id, "code", lang.Code)
		errMsg := "Error deleting language"
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, errMsg, http.StatusInternalServerError)
			return
		}
		flashError(w, r, h.renderer, redirectAdminLanguages, errMsg)
		return
	}

	slog.Info("language deleted", "language_id", id, "code", lang.Code)

	// For HTMX requests, return empty response to remove the row
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminLanguages, "Language deleted successfully")
}

// SetDefault handles setting a language as the default.
func (h *LanguagesHandler) SetDefault(w http.ResponseWriter, r *http.Request) {
	lang := h.getLanguageByIDParam(w, r)
	if lang == nil {
		return
	}

	// Cannot set inactive language as default
	if !lang.IsActive {
		flashError(w, r, h.renderer, redirectAdminLanguages, "Cannot set an inactive language as default")
		return
	}

	// Clear current default
	if err := h.queries.ClearDefaultLanguage(r.Context()); err != nil {
		slog.Error("failed to clear default language", "error", err)
		flashError(w, r, h.renderer, redirectAdminLanguages, "Error setting default language")
		return
	}

	// Set new default
	now := time.Now()
	if err := h.queries.SetDefaultLanguage(r.Context(), store.SetDefaultLanguageParams{
		ID:        lang.ID,
		UpdatedAt: now,
	}); err != nil {
		slog.Error("failed to set default language", "error", err)
		flashError(w, r, h.renderer, redirectAdminLanguages, "Error setting default language")
		return
	}

	slog.Info("default language set", "language_id", lang.ID, "code", lang.Code)
	flashSuccess(w, r, h.renderer, redirectAdminLanguages, fmt.Sprintf("%s set as default language", lang.Name))
}

// FindDefaultLanguage returns a pointer to the default language from a slice.
// Returns nil if no default language is found or the slice is empty.
func FindDefaultLanguage(languages []store.Language) *store.Language {
	for i := range languages {
		if languages[i].IsDefault {
			return &languages[i]
		}
	}
	return nil
}

// ListActiveLanguagesWithFallback returns all active languages, or an empty slice on error.
// This is useful when the languages list is needed for display but not critical for the operation.
func ListActiveLanguagesWithFallback(ctx context.Context, queries *store.Queries) []store.Language {
	languages, err := queries.ListActiveLanguages(ctx)
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		return []store.Language{}
	}
	return languages
}
