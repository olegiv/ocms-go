package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ocms-go/internal/i18n"
	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
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

// List displays all languages.
func (h *LanguagesHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	languages, err := h.queries.ListLanguages(r.Context())
	if err != nil {
		slog.Error("failed to list languages", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	totalLanguages, err := h.queries.CountLanguages(r.Context())
	if err != nil {
		slog.Error("failed to count languages", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := LanguagesListData{
		Languages:      languages,
		TotalLanguages: totalLanguages,
	}

	if err := h.renderer.Render(w, r, "admin/languages_list", render.TemplateData{
		Title: i18n.T(lang, "nav.languages"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.languages"), URL: "/admin/languages", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// NewForm displays the form to create a new language.
func (h *LanguagesHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	data := LanguageFormData{
		CommonLanguages: model.CommonLanguages,
		Errors:          make(map[string]string),
		FormValues:      make(map[string]string),
		IsEdit:          false,
	}

	if err := h.renderer.Render(w, r, "admin/languages_form", render.TemplateData{
		Title: i18n.T(lang, "languages.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(lang, "nav.languages"), URL: "/admin/languages"},
			{Label: i18n.T(lang, "languages.new"), URL: "/admin/languages/new", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Create handles creating a new language.
func (h *LanguagesHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/languages/new", http.StatusSeeOther)
		return
	}

	code := strings.TrimSpace(r.FormValue("code"))
	name := strings.TrimSpace(r.FormValue("name"))
	nativeName := strings.TrimSpace(r.FormValue("native_name"))
	direction := strings.TrimSpace(r.FormValue("direction"))
	isActiveStr := r.FormValue("is_active")
	positionStr := strings.TrimSpace(r.FormValue("position"))

	isActive := isActiveStr == "1" || isActiveStr == "on" || isActiveStr == "true"

	position := int64(0)
	if positionStr != "" {
		if p, err := strconv.ParseInt(positionStr, 10, 64); err == nil {
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
	if direction == "" {
		direction = model.DirectionLTR
	}

	formValues := map[string]string{
		"code":        code,
		"name":        name,
		"native_name": nativeName,
		"direction":   direction,
		"position":    strconv.FormatInt(position, 10),
	}
	if isActive {
		formValues["is_active"] = "1"
	}

	validationErrors := make(map[string]string)

	// Validate code
	if code == "" {
		validationErrors["code"] = "Language code is required"
	} else if len(code) < 2 || len(code) > 5 {
		validationErrors["code"] = "Language code must be 2-5 characters"
	} else {
		exists, err := h.queries.LanguageCodeExists(r.Context(), code)
		if err != nil {
			slog.Error("database error checking language code", "error", err)
		} else if exists != 0 {
			validationErrors["code"] = "Language code already exists"
		}
	}

	// Validate name
	if name == "" {
		validationErrors["name"] = "Name is required"
	}

	// Validate native name
	if nativeName == "" {
		validationErrors["native_name"] = "Native name is required"
	}

	// Validate direction
	if direction != model.DirectionLTR && direction != model.DirectionRTL {
		validationErrors["direction"] = "Direction must be ltr or rtl"
	}

	if len(validationErrors) > 0 {
		data := LanguageFormData{
			CommonLanguages: model.CommonLanguages,
			Errors:          validationErrors,
			FormValues:      formValues,
			IsEdit:          false,
		}

		if err := h.renderer.Render(w, r, "admin/languages_form", render.TemplateData{
			Title: i18n.T(lang, "languages.new"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(lang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(lang, "nav.languages"), URL: "/admin/languages"},
				{Label: i18n.T(lang, "languages.new"), URL: "/admin/languages/new", Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	now := time.Now()
	newLang, err := h.queries.CreateLanguage(r.Context(), store.CreateLanguageParams{
		Code:       code,
		Name:       name,
		NativeName: nativeName,
		IsDefault:  false,
		IsActive:   isActive,
		Direction:  direction,
		Position:   position,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to create language", "error", err)
		h.renderer.SetFlash(r, "Error creating language", "error")
		http.Redirect(w, r, "/admin/languages/new", http.StatusSeeOther)
		return
	}

	slog.Info("language created", "language_id", newLang.ID, "code", newLang.Code)
	h.renderer.SetFlash(r, "Language created successfully", "success")
	http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
}

// EditForm displays the form to edit an existing language.
func (h *LanguagesHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	adminLang := middleware.GetAdminLang(r)
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	lang, err := h.queries.GetLanguageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			slog.Error("failed to get language", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	formValues := map[string]string{
		"code":        lang.Code,
		"name":        lang.Name,
		"native_name": lang.NativeName,
		"direction":   lang.Direction,
		"position":    strconv.FormatInt(lang.Position, 10),
	}
	if lang.IsActive {
		formValues["is_active"] = "1"
	}

	data := LanguageFormData{
		Language:        &lang,
		CommonLanguages: model.CommonLanguages,
		Errors:          make(map[string]string),
		FormValues:      formValues,
		IsEdit:          true,
	}

	if err := h.renderer.Render(w, r, "admin/languages_form", render.TemplateData{
		Title: i18n.T(adminLang, "languages.edit"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(adminLang, "nav.dashboard"), URL: "/admin"},
			{Label: i18n.T(adminLang, "nav.languages"), URL: "/admin/languages"},
			{Label: i18n.T(adminLang, "languages.edit"), URL: fmt.Sprintf("/admin/languages/%d", id), Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles updating an existing language.
func (h *LanguagesHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	adminLang := middleware.GetAdminLang(r)
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	existingLang, err := h.queries.GetLanguageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			slog.Error("failed to get language", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/languages/%d", id), http.StatusSeeOther)
		return
	}

	code := strings.TrimSpace(r.FormValue("code"))
	name := strings.TrimSpace(r.FormValue("name"))
	nativeName := strings.TrimSpace(r.FormValue("native_name"))
	direction := strings.TrimSpace(r.FormValue("direction"))
	isActiveStr := r.FormValue("is_active")
	positionStr := strings.TrimSpace(r.FormValue("position"))

	isActive := isActiveStr == "1" || isActiveStr == "on" || isActiveStr == "true"

	position := existingLang.Position
	if positionStr != "" {
		if p, err := strconv.ParseInt(positionStr, 10, 64); err == nil {
			position = p
		}
	}

	// Default direction to ltr
	if direction == "" {
		direction = model.DirectionLTR
	}

	formValues := map[string]string{
		"code":        code,
		"name":        name,
		"native_name": nativeName,
		"direction":   direction,
		"position":    strconv.FormatInt(position, 10),
	}
	if isActive {
		formValues["is_active"] = "1"
	}

	validationErrors := make(map[string]string)

	// Validate code
	if code == "" {
		validationErrors["code"] = "Language code is required"
	} else if len(code) < 2 || len(code) > 5 {
		validationErrors["code"] = "Language code must be 2-5 characters"
	} else {
		exists, err := h.queries.LanguageCodeExistsExcluding(r.Context(), store.LanguageCodeExistsExcludingParams{
			Code: code,
			ID:   id,
		})
		if err != nil {
			slog.Error("database error checking language code", "error", err)
		} else if exists != 0 {
			validationErrors["code"] = "Language code already exists"
		}
	}

	// Validate name
	if name == "" {
		validationErrors["name"] = "Name is required"
	}

	// Validate native name
	if nativeName == "" {
		validationErrors["native_name"] = "Native name is required"
	}

	// Validate direction
	if direction != model.DirectionLTR && direction != model.DirectionRTL {
		validationErrors["direction"] = "Direction must be ltr or rtl"
	}

	// Cannot deactivate default language
	if existingLang.IsDefault && !isActive {
		validationErrors["is_active"] = "Cannot deactivate the default language"
	}

	if len(validationErrors) > 0 {
		data := LanguageFormData{
			Language:        &existingLang,
			CommonLanguages: model.CommonLanguages,
			Errors:          validationErrors,
			FormValues:      formValues,
			IsEdit:          true,
		}

		if err := h.renderer.Render(w, r, "admin/languages_form", render.TemplateData{
			Title: i18n.T(adminLang, "languages.edit"),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: i18n.T(adminLang, "nav.dashboard"), URL: "/admin"},
				{Label: i18n.T(adminLang, "nav.languages"), URL: "/admin/languages"},
				{Label: i18n.T(adminLang, "languages.edit"), URL: fmt.Sprintf("/admin/languages/%d", id), Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	now := time.Now()
	_, err = h.queries.UpdateLanguage(r.Context(), store.UpdateLanguageParams{
		ID:         id,
		Code:       code,
		Name:       name,
		NativeName: nativeName,
		IsDefault:  existingLang.IsDefault, // Keep existing default status
		IsActive:   isActive,
		Direction:  direction,
		Position:   position,
		UpdatedAt:  now,
	})
	if err != nil {
		slog.Error("failed to update language", "error", err)
		h.renderer.SetFlash(r, "Error updating language", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/languages/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("language updated", "language_id", id, "code", code)
	h.renderer.SetFlash(r, "Language updated successfully", "success")
	http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
}

// Delete handles deleting a language.
func (h *LanguagesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
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
		h.renderer.SetFlash(r, errMsg, "error")
		http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
		return
	}

	// Check if there are pages linked to this language
	pageCount, err := h.queries.CountPagesByLanguageID(r.Context(), sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		slog.Error("failed to count pages for language", "error", err, "language_id", id)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			http.Error(w, "Failed to check language usage", http.StatusInternalServerError)
			return
		}
		h.renderer.SetFlash(r, "Failed to check language usage", "error")
		http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
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
		h.renderer.SetFlash(r, errMsg, "error")
		http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
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
		h.renderer.SetFlash(r, errMsg, "error")
		http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
		return
	}

	slog.Info("language deleted", "language_id", id, "code", lang.Code)

	// For HTMX requests, return empty response to remove the row
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.renderer.SetFlash(r, "Language deleted successfully", "success")
	http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
}

// SetDefault handles setting a language as the default.
func (h *LanguagesHandler) SetDefault(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	lang, err := h.queries.GetLanguageByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			slog.Error("failed to get language", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Cannot set inactive language as default
	if !lang.IsActive {
		h.renderer.SetFlash(r, "Cannot set an inactive language as default", "error")
		http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
		return
	}

	// Clear current default
	if err := h.queries.ClearDefaultLanguage(r.Context()); err != nil {
		slog.Error("failed to clear default language", "error", err)
		h.renderer.SetFlash(r, "Error setting default language", "error")
		http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
		return
	}

	// Set new default
	now := time.Now()
	if err := h.queries.SetDefaultLanguage(r.Context(), store.SetDefaultLanguageParams{
		ID:        id,
		UpdatedAt: now,
	}); err != nil {
		slog.Error("failed to set default language", "error", err)
		h.renderer.SetFlash(r, "Error setting default language", "error")
		http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
		return
	}

	slog.Info("default language set", "language_id", id, "code", lang.Code)
	h.renderer.SetFlash(r, fmt.Sprintf("%s set as default language", lang.Name), "success")
	http.Redirect(w, r, "/admin/languages", http.StatusSeeOther)
}
