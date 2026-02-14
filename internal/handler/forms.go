// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/olegiv/ocms-go/internal/cache"
	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/module"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/theme"
	"github.com/olegiv/ocms-go/internal/util"
	adminviews "github.com/olegiv/ocms-go/internal/views/admin"
	"github.com/olegiv/ocms-go/internal/webhook"
	"github.com/olegiv/ocms-go/modules/hcaptcha"
)

// FormsHandler handles form management routes.
type FormsHandler struct {
	db              *sql.DB
	queries         *store.Queries
	renderer        *render.Renderer
	sessionManager  *scs.SessionManager
	dispatcher      *webhook.Dispatcher
	hookRegistry    *module.HookRegistry
	themeManager    *theme.Manager
	cacheManager    *cache.Manager
	menuService     *service.MenuService
	frontendHandler *FrontendHandler
}

// NewFormsHandler creates a new FormsHandler.
func NewFormsHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager, hr *module.HookRegistry, tm *theme.Manager, cm *cache.Manager, ms *service.MenuService, fh *FrontendHandler) *FormsHandler {
	return &FormsHandler{
		db:              db,
		queries:         store.New(db),
		renderer:        renderer,
		sessionManager:  sm,
		hookRegistry:    hr,
		themeManager:    tm,
		cacheManager:    cm,
		menuService:     ms,
		frontendHandler: fh,
	}
}

// SetDispatcher sets the webhook dispatcher for event dispatching.
func (h *FormsHandler) SetDispatcher(d *webhook.Dispatcher) {
	h.dispatcher = d
}

// dispatchFormEvent dispatches a form submission webhook event.
func (h *FormsHandler) dispatchFormEvent(ctx context.Context, form store.Form, submissionID int64, data map[string]string) {
	if h.dispatcher == nil {
		return
	}

	eventData := webhook.FormEventData{
		FormID:       form.ID,
		FormName:     form.Name,
		FormSlug:     form.Slug,
		SubmissionID: submissionID,
		Data:         data,
		SubmittedAt:  time.Now(),
	}

	if err := h.dispatcher.DispatchEvent(ctx, model.EventFormSubmitted, eventData); err != nil {
		slog.Error("failed to dispatch webhook event",
			"error", err,
			"event_type", model.EventFormSubmitted,
			"form_id", form.ID)
	}
}

// FormListItem represents a form with submission counts.
type FormListItem struct {
	Form            store.Form
	SubmissionCount int64
	UnreadCount     int64
}

// formInput holds parsed form input values.
type formInput struct {
	Name           string
	Slug           string
	Title          string
	Description    string
	SuccessMessage string
	EmailTo        string
	IsActive       bool
	FormValues     map[string]string
}

// parseFormInput extracts and returns form field values from the request.
func parseFormInput(r *http.Request) formInput {
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))
	successMessage := strings.TrimSpace(r.FormValue("success_message"))
	emailTo := strings.TrimSpace(r.FormValue("email_to"))
	isActive := r.FormValue("is_active") == "true" || r.FormValue("is_active") == "on"

	formValues := map[string]string{
		"name":            name,
		"slug":            slug,
		"title":           title,
		"description":     description,
		"success_message": successMessage,
		"email_to":        emailTo,
	}
	if isActive {
		formValues["is_active"] = "true"
	}

	return formInput{
		Name:           name,
		Slug:           slug,
		Title:          title,
		Description:    description,
		SuccessMessage: successMessage,
		EmailTo:        emailTo,
		IsActive:       isActive,
		FormValues:     formValues,
	}
}

// validateFormInput validates form input fields and returns validation errors.
// It also auto-generates the slug from name if empty.
func validateFormInput(input *formInput) map[string]string {
	errs := make(map[string]string)

	if input.Name == "" {
		errs["name"] = "Name is required"
	} else if len(input.Name) < 2 {
		errs["name"] = "Name must be at least 2 characters"
	}

	if input.Title == "" {
		errs["title"] = "Title is required"
	}

	// Auto-generate slug if empty
	if input.Slug == "" {
		input.Slug = util.Slugify(input.Name)
		input.FormValues["slug"] = input.Slug
	}

	if errMsg := ValidateSlugFormat(input.Slug); errMsg != "" {
		errs["slug"] = errMsg
	}

	// Set default success message
	if input.SuccessMessage == "" {
		input.SuccessMessage = "Thank you for your submission."
		input.FormValues["success_message"] = input.SuccessMessage
	}

	return errs
}

// parseFormIDParam parses the form ID from URL and redirects on error.
// Returns 0 if there was an error (redirect already sent).
func (h *FormsHandler) parseFormIDParam(w http.ResponseWriter, r *http.Request) int64 {
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminForms, "Invalid form ID")
		return 0
	}
	return id
}

// fetchFormByID retrieves a form by ID and handles errors with redirect.
// Returns nil if there was an error (redirect already sent).
func (h *FormsHandler) fetchFormByID(w http.ResponseWriter, r *http.Request, id int64) *store.Form {
	form, err := h.queries.GetFormByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			flashError(w, r, h.renderer, redirectAdminForms, "Form not found")
		} else {
			slog.Error("failed to get form", "error", err, "form_id", id)
			flashError(w, r, h.renderer, redirectAdminForms, "Error loading form")
		}
		return nil
	}
	return &form
}

// parseFieldIDParam parses field ID from URL and returns error response if invalid.
// Returns 0 if there was an error (response already sent).
func parseFieldIDParam(w http.ResponseWriter, r *http.Request, paramName string) int64 {
	idStr := chi.URLParam(r, paramName)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid %s", paramName), http.StatusBadRequest)
		return 0
	}
	return id
}

// validateFieldRequest validates a field request and normalizes values.
// Returns error message if validation fails, empty string on success.
func validateFieldRequest(req *AddFieldRequest) string {
	if req.Label == "" {
		return "Label is required"
	}
	if !model.IsValidFieldType(req.Type) {
		return "Invalid field type"
	}

	// Generate name from label if not provided
	if req.Name == "" {
		req.Name = util.Slugify(req.Label)
		req.Name = strings.ReplaceAll(req.Name, "-", "_")
	}

	// Set default options
	if req.Options == "" {
		req.Options = "[]"
	}
	if req.Validation == "" {
		req.Validation = "{}"
	}
	return ""
}

// validateCaptchaFieldLimit checks that only one captcha field exists per form.
// Returns an error message if validation fails, empty string on success.
func (h *FormsHandler) validateCaptchaFieldLimit(ctx context.Context, formID int64, fieldType string, excludeFieldID int64) string {
	if fieldType != model.FieldTypeCaptcha {
		return ""
	}

	fields, err := h.queries.GetFormFields(ctx, formID)
	if err != nil {
		return ""
	}

	for _, field := range fields {
		if field.Type == model.FieldTypeCaptcha && field.ID != excludeFieldID {
			return "Only one captcha field is allowed per form"
		}
	}
	return ""
}

// hasCaptchaField checks if the form has a captcha field.
func hasCaptchaField(fields []store.FormField) bool {
	for _, field := range fields {
		if field.Type == model.FieldTypeCaptcha {
			return true
		}
	}
	return false
}

// verifyCaptcha verifies the captcha response using hooks.
// Returns error message if verification fails, empty string on success.
func (h *FormsHandler) verifyCaptcha(ctx context.Context, r *http.Request, lang string) string {
	if h.hookRegistry == nil {
		return "" // No hook registry, skip verification
	}

	// Check if captcha hooks are registered (hCaptcha module is active)
	if !h.hookRegistry.HasHandlers(hcaptcha.HookFormCaptchaVerify) {
		slog.Warn("captcha field present but hCaptcha module not active")
		return "" // Module not active, skip verification
	}

	// Build verification request
	verifyReq := &hcaptcha.VerifyRequest{
		Response: hcaptcha.GetResponseFromForm(r),
		RemoteIP: hcaptcha.GetRemoteIP(r),
	}

	// Call verification hook
	result, err := h.hookRegistry.Call(ctx, hcaptcha.HookFormCaptchaVerify, verifyReq)
	if err != nil {
		slog.Error("captcha verification hook error", "error", err)
		return i18n.T(lang, "hcaptcha.error_verification")
	}

	// Check result
	if verifiedReq, ok := result.(*hcaptcha.VerifyRequest); ok {
		if !verifiedReq.Verified {
			// Use i18n message from ErrorCode if available
			if verifiedReq.ErrorCode != "" {
				return i18n.T(lang, verifiedReq.ErrorCode)
			}
			return verifiedReq.Error
		}
	}

	return "" // Success
}

// getActiveFormBySlug retrieves a form by slug and checks if it's active.
// Returns nil if form not found or inactive (response already sent).
func (h *FormsHandler) getActiveFormBySlug(w http.ResponseWriter, r *http.Request, slug string) *store.Form {
	form, err := h.queries.GetFormBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.frontendHandler.NotFound(w, r)
		} else {
			slog.Error("failed to get form", "error", err, "slug", slug)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return nil
	}

	if !form.IsActive {
		h.frontendHandler.NotFound(w, r)
		return nil
	}
	return &form
}


// updateLanguageContext updates the request context with the form's language.
// This ensures UI translations match the form's language, consistent with page handler behavior.
func (h *FormsHandler) updateLanguageContext(w http.ResponseWriter, r *http.Request, languageCode string) *http.Request {
	if languageCode == "" {
		return r
	}

	formLang, err := h.queries.GetLanguageByCode(r.Context(), languageCode)
	if err != nil {
		return r
	}

	langInfo := middleware.LanguageInfo{
		ID:         formLang.ID,
		Code:       formLang.Code,
		Name:       formLang.Name,
		NativeName: formLang.NativeName,
		Direction:  formLang.Direction,
		IsDefault:  formLang.IsDefault,
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyLanguage, langInfo)
	ctx = context.WithValue(ctx, middleware.ContextKeyLanguageCode, formLang.Code)

	middleware.SetLanguageCookie(w, formLang.Code)

	return r.WithContext(ctx)
}

// renderFormFormPage renders the form create/edit page with data and breadcrumbs.
func (h *FormsHandler) renderFormFormPage(w http.ResponseWriter, r *http.Request, data FormFormData, title string, breadcrumbs []render.Breadcrumb) {
	pc := buildPageContext(r, h.sessionManager, h.renderer, title, breadcrumbs)
	viewData := convertFormFormViewData(data, h.renderer)
	renderTempl(w, r, adminviews.FormsFormPage(pc, viewData))
}

// FormsListData holds data for the forms list template.
type FormsListData struct {
	Forms []FormListItem
}

// List handles GET /admin/forms - displays a list of forms.
func (h *FormsHandler) List(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	forms, err := h.queries.ListForms(r.Context(), store.ListFormsParams{
		Limit:  1000,
		Offset: 0,
	})
	if err != nil {
		slog.Error("failed to list forms", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get submission counts for each form
	formItems := make([]FormListItem, len(forms))
	for i, form := range forms {
		submissionCount, _ := h.queries.CountFormSubmissions(r.Context(), form.ID)
		unreadCount, _ := h.queries.CountUnreadSubmissions(r.Context(), form.ID)
		formItems[i] = FormListItem{
			Form:            form,
			SubmissionCount: submissionCount,
			UnreadCount:     unreadCount,
		}
	}

	data := FormsListData{
		Forms: formItems,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer, i18n.T(lang, "nav.forms"), formsListBreadcrumbs(lang))
	viewData := convertFormsListViewData(data)
	renderTempl(w, r, adminviews.FormsListPage(pc, viewData))
}

// FormTranslationInfo holds information about a form translation.
type FormTranslationInfo struct {
	Language store.Language
	Form     store.Form
}

// FormFormData holds data for the form create/edit template.
type FormFormData struct {
	Form             *store.Form
	Fields           []store.FormField
	FieldTypes       []string
	Errors           map[string]string
	FormValues       map[string]string
	IsEdit           bool
	Language         *store.Language       // Current form language
	AllLanguages     []store.Language      // All active languages for selection
	Translations     []FormTranslationInfo // Existing translations
	MissingLanguages []store.Language      // Languages without translations
}

// NewForm handles GET /admin/forms/new - displays the new form form.
func (h *FormsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	// Load all active languages
	allLanguages := ListActiveLanguagesWithFallback(r.Context(), h.queries)
	defaultLanguage := FindDefaultLanguage(allLanguages)

	data := FormFormData{
		FieldTypes: model.ValidFieldTypes(),
		Errors:     make(map[string]string),
		FormValues: map[string]string{
			"success_message": "Thank you for your submission.",
			"is_active":       "true",
		},
		IsEdit:       false,
		AllLanguages: allLanguages,
		Language:     defaultLanguage,
	}

	h.renderFormFormPage(w, r, data, i18n.T(lang, "forms.new"), formsNewBreadcrumbs(lang))
}

// Create handles POST /admin/forms - creates a new form.
func (h *FormsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionContentReadOnly, redirectAdminFormsNew) {
		return
	}

	lang := h.renderer.GetAdminLang(r)

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminFormsNew) {
		return
	}

	input := parseFormInput(r)
	validationErrors := validateFormInput(&input)

	// Get default language for form creation
	defaultLang, err := h.queries.GetDefaultLanguage(r.Context())
	if err != nil {
		slog.Error("failed to get default language", "error", err)
		flashError(w, r, h.renderer, redirectAdminFormsNew, "Error creating form")
		return
	}

	// Check slug uniqueness for this language
	if validationErrors["slug"] == "" && input.Slug != "" {
		exists, err := h.queries.FormSlugExistsForLanguage(r.Context(), store.FormSlugExistsForLanguageParams{
			Slug:         input.Slug,
			LanguageCode: defaultLang.Code,
		})
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			validationErrors["slug"] = "Error checking slug"
		} else if exists != 0 {
			validationErrors["slug"] = "Slug already exists"
		}
	}

	if len(validationErrors) > 0 {
		data := FormFormData{
			FieldTypes: model.ValidFieldTypes(),
			Errors:     validationErrors,
			FormValues: input.FormValues,
			IsEdit:     false,
		}

		h.renderFormFormPage(w, r, data, i18n.T(lang, "forms.new"), formsNewBreadcrumbs(lang))
		return
	}

	now := time.Now()
	form, err := h.queries.CreateForm(r.Context(), store.CreateFormParams{
		Name:           input.Name,
		Slug:           input.Slug,
		Title:          input.Title,
		Description:    util.NullStringFromValue(input.Description),
		SuccessMessage: sql.NullString{String: input.SuccessMessage, Valid: true}, // Always valid - has default
		EmailTo:        util.NullStringFromValue(input.EmailTo),
		IsActive:       input.IsActive,
		LanguageCode:   defaultLang.Code,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		slog.Error("failed to create form", "error", err)
		flashError(w, r, h.renderer, redirectAdminFormsNew, "Error creating form")
		return
	}

	slog.Info("form created", "form_id", form.ID, "slug", form.Slug)
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsID, form.ID), "Form created successfully")
}

// EditForm handles GET /admin/forms/{id} - displays the form builder.
func (h *FormsHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	id := h.parseFormIDParam(w, r)
	if id == 0 {
		return
	}

	form := h.fetchFormByID(w, r, id)
	if form == nil {
		return
	}

	fields, err := h.queries.GetFormFields(r.Context(), id)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", id)
		fields = []store.FormField{}
	}

	// Load language and translation info
	langInfo := h.loadFormLanguageInfo(r.Context(), *form)

	data := FormFormData{
		Form:             form,
		Fields:           fields,
		FieldTypes:       model.ValidFieldTypes(),
		Errors:           make(map[string]string),
		FormValues:       make(map[string]string),
		IsEdit:           true,
		Language:         langInfo.EntityLanguage,
		AllLanguages:     langInfo.AllLanguages,
		Translations:     langInfo.Translations,
		MissingLanguages: langInfo.MissingLanguages,
	}

	h.renderFormFormPage(w, r, data, fmt.Sprintf("Edit Form - %s", form.Name), formsEditBreadcrumbs(lang, form.Name, form.ID))
}

// Update handles PUT /admin/forms/{id} - updates a form.
func (h *FormsHandler) Update(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionContentReadOnly, redirectAdminForms) {
		return
	}

	lang := h.renderer.GetAdminLang(r)

	id := h.parseFormIDParam(w, r)
	if id == 0 {
		return
	}

	form := h.fetchFormByID(w, r, id)
	if form == nil {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsID, id)) {
		return
	}

	input := parseFormInput(r)
	validationErrors := validateFormInput(&input)

	// Check slug uniqueness for this language (Update: only if slug changed)
	if validationErrors["slug"] == "" && input.Slug != "" && input.Slug != form.Slug {
		exists, err := h.queries.FormSlugExistsExcludingForLanguage(r.Context(), store.FormSlugExistsExcludingForLanguageParams{
			Slug:         input.Slug,
			LanguageCode: form.LanguageCode,
			ID:           id,
		})
		if err != nil {
			slog.Error("database error checking slug", "error", err)
			validationErrors["slug"] = "Error checking slug"
		} else if exists != 0 {
			validationErrors["slug"] = "Slug already exists"
		}
	}

	if len(validationErrors) > 0 {
		fields, _ := h.queries.GetFormFields(r.Context(), id)

		// Load language and translation info for re-rendering
		langInfo := h.loadFormLanguageInfo(r.Context(), *form)

		data := FormFormData{
			Form:             form,
			Fields:           fields,
			FieldTypes:       model.ValidFieldTypes(),
			Errors:           validationErrors,
			FormValues:       input.FormValues,
			IsEdit:           true,
			Language:         langInfo.EntityLanguage,
			AllLanguages:     langInfo.AllLanguages,
			Translations:     langInfo.Translations,
			MissingLanguages: langInfo.MissingLanguages,
		}

		h.renderFormFormPage(w, r, data, fmt.Sprintf("Edit Form - %s", form.Name), formsEditBreadcrumbs(lang, form.Name, form.ID))
		return
	}

	now := time.Now()
	_, err := h.queries.UpdateForm(r.Context(), store.UpdateFormParams{
		ID:             id,
		Name:           input.Name,
		Slug:           input.Slug,
		Title:          input.Title,
		Description:    util.NullStringFromValue(input.Description),
		SuccessMessage: sql.NullString{String: input.SuccessMessage, Valid: true}, // Always valid - has default
		EmailTo:        util.NullStringFromValue(input.EmailTo),
		IsActive:       input.IsActive,
		LanguageCode:   form.LanguageCode,
		UpdatedAt:      now,
	})
	if err != nil {
		slog.Error("failed to update form", "error", err, "form_id", id)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsID, id), "Error updating form")
		return
	}

	slog.Info("form updated", "form_id", id, "updated_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsID, id), "Form updated successfully")
}

// Delete handles DELETE /admin/forms/{id} - deletes a form.
func (h *FormsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		http.Error(w, middleware.DemoModeMessageDetailed(middleware.RestrictionDeleteForm), http.StatusForbidden)
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		http.Error(w, "Invalid form ID", http.StatusBadRequest)
		return
	}

	if _, ok := h.requireFormWithHTTPError(w, r, id); !ok {
		return
	}

	err = h.queries.DeleteForm(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete form", "error", err, "form_id", id)
		http.Error(w, "Error deleting form", http.StatusInternalServerError)
		return
	}

	slog.Info("form deleted", "form_id", id, "deleted_by", middleware.GetUserID(r))

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminForms, "Form deleted successfully")
}

// AddFieldRequest represents the JSON request for adding a form field.
type AddFieldRequest struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	HelpText    string `json:"help_text"`
	Options     string `json:"options"`
	Validation  string `json:"validation"`
	IsRequired  bool   `json:"is_required"`
}

// AddField handles POST /admin/forms/{id}/fields - adds a form field.
func (h *FormsHandler) AddField(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w) {
		return
	}

	formID, ok := parseFieldIDParamJSON(w, r, "id")
	if !ok {
		return
	}

	// Get the form to inherit its language_id
	form, err := h.queries.GetFormByID(r.Context(), formID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "Form not found")
		} else {
			slog.Error("failed to get form", "error", err, "form_id", formID)
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		}
		return
	}

	var req AddFieldRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if errMsg := validateFieldRequest(&req); errMsg != "" {
		writeJSONError(w, http.StatusBadRequest, errMsg)
		return
	}

	// Check captcha field limit (only one per form)
	if errMsg := h.validateCaptchaFieldLimit(r.Context(), formID, req.Type, 0); errMsg != "" {
		writeJSONError(w, http.StatusBadRequest, errMsg)
		return
	}

	maxPos := h.getMaxFieldPosition(r, formID)

	now := time.Now()
	field, err := h.queries.CreateFormField(r.Context(), store.CreateFormFieldParams{
		FormID:       formID,
		Type:         req.Type,
		Name:         req.Name,
		Label:        req.Label,
		Placeholder:  util.NullStringFromValue(req.Placeholder),
		HelpText:     util.NullStringFromValue(req.HelpText),
		Options:      util.NullStringFromValue(req.Options),
		Validation:   util.NullStringFromValue(req.Validation),
		IsRequired:   req.IsRequired,
		Position:     maxPos + 1,
		LanguageCode: form.LanguageCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to create form field", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Error creating field")
		return
	}

	writeJSONSuccess(w, map[string]any{"field": field})
}

// UpdateFieldRequest represents the JSON request for updating a form field.
type UpdateFieldRequest struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	HelpText    string `json:"help_text"`
	Options     string `json:"options"`
	Validation  string `json:"validation"`
	IsRequired  bool   `json:"is_required"`
}

// UpdateField handles PUT /admin/forms/{id}/fields/{fieldId} - updates a form field.
func (h *FormsHandler) UpdateField(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w) {
		return
	}

	formID, ok := parseFieldIDParamJSON(w, r, "id")
	if !ok {
		return
	}

	fieldID, ok := parseFieldIDParamJSON(w, r, "fieldId")
	if !ok {
		return
	}

	field := h.fetchFieldByIDJSON(w, r, fieldID, formID)
	if field == nil {
		return
	}

	var req AddFieldRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if errMsg := validateFieldRequest(&req); errMsg != "" {
		writeJSONError(w, http.StatusBadRequest, errMsg)
		return
	}

	// Check captcha field limit (only one per form, exclude current field)
	if errMsg := h.validateCaptchaFieldLimit(r.Context(), formID, req.Type, fieldID); errMsg != "" {
		writeJSONError(w, http.StatusBadRequest, errMsg)
		return
	}

	now := time.Now()
	updatedField, err := h.queries.UpdateFormField(r.Context(), store.UpdateFormFieldParams{
		ID:           fieldID,
		Type:         req.Type,
		Name:         req.Name,
		Label:        req.Label,
		Placeholder:  util.NullStringFromValue(req.Placeholder),
		HelpText:     util.NullStringFromValue(req.HelpText),
		Options:      util.NullStringFromValue(req.Options),
		Validation:   util.NullStringFromValue(req.Validation),
		IsRequired:   req.IsRequired,
		Position:     field.Position,
		LanguageCode: field.LanguageCode,
		UpdatedAt:    now,
	})
	if err != nil {
		slog.Error("failed to update form field", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Error updating field")
		return
	}

	writeJSONSuccess(w, map[string]any{"field": updatedField})
}

// DeleteField handles DELETE /admin/forms/{id}/fields/{fieldId} - deletes a form field.
func (h *FormsHandler) DeleteField(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w) {
		return
	}

	formID, ok := parseFieldIDParamJSON(w, r, "id")
	if !ok {
		return
	}

	fieldID, ok := parseFieldIDParamJSON(w, r, "fieldId")
	if !ok {
		return
	}

	if h.fetchFieldByIDJSON(w, r, fieldID, formID) == nil {
		return
	}

	if err := h.queries.DeleteFormField(r.Context(), fieldID); err != nil {
		slog.Error("failed to delete form field", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "Error deleting field")
		return
	}

	writeJSONSuccess(w, nil)
}

// ReorderFieldsRequest represents the JSON request for reordering form fields.
type ReorderFieldsRequest struct {
	FieldIDs []int64 `json:"field_ids"`
}

// ReorderFields handles POST /admin/forms/{id}/fields/reorder - reorders form fields.
func (h *FormsHandler) ReorderFields(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w) {
		return
	}

	formID, ok := parseFieldIDParamJSON(w, r, "id")
	if !ok {
		return
	}

	if !h.checkFormExistsJSON(w, r, formID) {
		return
	}

	var req ReorderFieldsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	now := time.Now()

	for i, fieldID := range req.FieldIDs {
		field, err := h.queries.GetFormFieldByID(r.Context(), fieldID)
		if err != nil {
			continue
		}
		if field.FormID != formID {
			continue
		}

		if _, err = h.queries.UpdateFormField(r.Context(), store.UpdateFormFieldParams{
			ID:           fieldID,
			Type:         field.Type,
			Name:         field.Name,
			Label:        field.Label,
			Placeholder:  field.Placeholder,
			HelpText:     field.HelpText,
			Options:      field.Options,
			Validation:   field.Validation,
			IsRequired:   field.IsRequired,
			Position:     int64(i),
			LanguageCode: field.LanguageCode,
			UpdatedAt:    now,
		}); err != nil {
			slog.Error("failed to update field position", "error", err, "field_id", fieldID)
		}
	}

	writeJSONSuccess(w, nil)
}

// ===== Public Form Handlers =====

// PublicFormData holds data for the public form template.
type PublicFormData struct {
	Form      store.Form
	Fields    []store.FormField
	Errors    map[string]string
	Values    map[string]string
	Success   bool
	CSRFToken string
	SiteName  string
}

// Show handles GET /forms/{slug} - displays a public form.
func (h *FormsHandler) Show(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	form := h.getActiveFormBySlug(w, r, slug)
	if form == nil {
		return
	}

	// Update language context to match form's language
	r = h.updateLanguageContext(w, r, form.LanguageCode)

	fields, err := h.queries.GetFormFields(r.Context(), form.ID)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", form.ID)
		fields = []store.FormField{}
	}

	// Build template data with theme support
	base := h.getBaseTemplateData(r, form.Title)
	data := FormTemplateData{
		BaseTemplateData: base,
		Form:             *form,
		Fields:           fields,
		Errors:           make(map[string]string),
		Values:           make(map[string]string),
		Success:          false,
		CSRFToken:        template.HTML(h.sessionManager.Token(r.Context())),
	}

	h.render(w, r, data)
}

// Submit handles POST /forms/{slug} - processes form submission.
func (h *FormsHandler) Submit(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	form := h.getActiveFormBySlug(w, r, slug)
	if form == nil {
		return
	}

	// Update language context to match form's language
	r = h.updateLanguageContext(w, r, form.LanguageCode)

	fields, err := h.queries.GetFormFields(r.Context(), form.ID)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", form.ID)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Parse the form data
	if err := r.ParseForm(); err != nil {
		slog.Error("failed to parse form", "error", err)
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// Check honeypot field (spam protection)
	honeypot := r.FormValue("_website")
	if honeypot != "" {
		// Bot detected, silently pretend success
		slog.Info("honeypot triggered", "form_slug", slug, "ip", r.RemoteAddr)
		h.renderFormSuccess(w, r, *form, fields)
		return
	}

	// Note: CSRF protection is handled by the middleware (filippo.io/csrf/gorilla)
	// which uses Fetch metadata headers - no form token validation needed here.

	// Verify captcha if form has captcha field
	if hasCaptchaField(fields) {
		if errMsg := h.verifyCaptcha(r.Context(), r, form.LanguageCode); errMsg != "" {
			validationErrors := map[string]string{"_captcha": errMsg}
			h.renderFormWithErrors(w, r, *form, fields, validationErrors, r.Form)
			return
		}
	}

	// Collect values and validate
	values := make(map[string]string)
	validationErrors := make(map[string]string)

	for _, field := range fields {
		// Skip captcha fields - don't store their values
		if field.Type == model.FieldTypeCaptcha {
			continue
		}

		value := strings.TrimSpace(r.FormValue(field.Name))
		values[field.Name] = value

		// Required validation
		if field.IsRequired && value == "" {
			validationErrors[field.Name] = fmt.Sprintf("%s is required", field.Label)
			continue
		}

		// Skip further validation if empty and not required
		if value == "" {
			continue
		}

		// Type-specific validation
		switch field.Type {
		case model.FieldTypeEmail:
			if !isValidEmail(value) {
				validationErrors[field.Name] = "Please enter a valid email address"
			}
		case model.FieldTypeNumber:
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				validationErrors[field.Name] = "Please enter a valid number"
			}
		case model.FieldTypeDate:
			if !isValidDate(value) {
				validationErrors[field.Name] = "Please enter a valid date"
			}
		}

		// Custom validation from field's validation JSON
		if field.Validation.Valid && field.Validation.String != "" && field.Validation.String != "{}" {
			var validation map[string]interface{}
			if err := json.Unmarshal([]byte(field.Validation.String), &validation); err == nil {
				if minLen, ok := validation["minLength"].(float64); ok && len(value) < int(minLen) {
					validationErrors[field.Name] = fmt.Sprintf("%s must be at least %d characters", field.Label, int(minLen))
				}
				if maxLen, ok := validation["maxLength"].(float64); ok && len(value) > int(maxLen) {
					validationErrors[field.Name] = fmt.Sprintf("%s must be no more than %d characters", field.Label, int(maxLen))
				}
				if pattern, ok := validation["pattern"].(string); ok && pattern != "" {
					if matched, _ := regexp.MatchString(pattern, value); !matched {
						validationErrors[field.Name] = fmt.Sprintf("%s is not in the correct format", field.Label)
					}
				}
			}
		}
	}

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		h.renderFormWithErrors(w, r, *form, fields, validationErrors, r.Form)
		return
	}

	// Serialize form data as JSON
	dataJSON, err := json.Marshal(values)
	if err != nil {
		slog.Error("failed to marshal form data", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Save submission
	submission, err := h.queries.CreateFormSubmission(r.Context(), store.CreateFormSubmissionParams{
		FormID:       form.ID,
		Data:         string(dataJSON),
		IpAddress:    sql.NullString{String: middleware.GetClientIP(r), Valid: true},
		UserAgent:    sql.NullString{String: r.UserAgent(), Valid: true},
		IsRead:       false,
		LanguageCode: form.LanguageCode,
		CreatedAt:    time.Now(),
	})
	if err != nil {
		slog.Error("failed to save form submission", "error", err, "form_id", form.ID)
		h.renderFormWithErrors(w, r, *form, fields, map[string]string{"_form": "Failed to save submission. Please try again."}, r.Form)
		return
	}

	slog.Info("form submission saved", "form_id", form.ID, "form_slug", slug)

	// Dispatch form.submitted webhook event
	h.dispatchFormEvent(r.Context(), *form, submission.ID, values)

	// TODO: Send notification email if configured
	// if form.EmailTo.Valid && form.EmailTo.String != "" {
	//     sendNotificationEmail(form, values)
	// }

	// Render success
	h.renderFormSuccess(w, r, *form, fields)
}

// renderFormWithErrors renders the form with validation errors.
func (h *FormsHandler) renderFormWithErrors(w http.ResponseWriter, r *http.Request, form store.Form, fields []store.FormField, fieldErrors map[string]string, formData map[string][]string) {
	values := make(map[string]string)
	for key, vals := range formData {
		if len(vals) > 0 {
			values[key] = vals[0]
		}
	}

	// Build template data with theme support
	base := h.getBaseTemplateData(r, form.Title)
	data := FormTemplateData{
		BaseTemplateData: base,
		Form:             form,
		Fields:           fields,
		Errors:           fieldErrors,
		Values:           values,
		Success:          false,
		CSRFToken:        template.HTML(h.sessionManager.Token(r.Context())),
	}

	h.render(w, r, data)
}

// renderFormSuccess renders the form success page.
func (h *FormsHandler) renderFormSuccess(w http.ResponseWriter, r *http.Request, form store.Form, fields []store.FormField) {
	// Build template data with theme support
	base := h.getBaseTemplateData(r, form.Title)
	data := FormTemplateData{
		BaseTemplateData: base,
		Form:             form,
		Fields:           fields,
		Success:          true,
		Errors:           make(map[string]string),
		Values:           make(map[string]string),
	}

	h.render(w, r, data)
}

// isValidEmail checks if the email is valid.
func isValidEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// isValidDate checks if the date is valid (YYYY-MM-DD format).
func isValidDate(date string) bool {
	matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}$`, date)
	if !matched {
		return false
	}
	_, err := time.Parse("2006-01-02", date)
	return err == nil
}

// ===== Submission Management Handlers =====

// SubmissionListItem represents a submission with parsed data for display.
type SubmissionListItem struct {
	Submission store.FormSubmission
	Data       map[string]interface{}
	Preview    string
}

// SubmissionsListData holds data for the submissions list template.
type SubmissionsListData struct {
	Form        store.Form
	Fields      []store.FormField
	Submissions []SubmissionListItem
	TotalCount  int64
	UnreadCount int64
	Pagination  AdminPagination
}

// Submissions handles GET /admin/forms/{id}/submissions - lists form submissions.
func (h *FormsHandler) Submissions(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	formID := h.parseFormIDParam(w, r)
	if formID == 0 {
		return
	}

	form := h.fetchFormByID(w, r, formID)
	if form == nil {
		return
	}

	// Pagination
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if pInt, err := strconv.Atoi(p); err == nil && pInt > 0 {
			page = pInt
		}
	}
	perPage := 20
	offset := (page - 1) * perPage

	// Get fields for display
	fields, err := h.queries.GetFormFields(r.Context(), formID)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", formID)
		fields = []store.FormField{}
	}

	// Get submissions
	submissions, err := h.queries.GetFormSubmissions(r.Context(), store.GetFormSubmissionsParams{
		FormID: formID,
		Limit:  int64(perPage),
		Offset: int64(offset),
	})
	if err != nil {
		slog.Error("failed to get submissions", "error", err, "form_id", formID)
		submissions = []store.FormSubmission{}
	}

	// Parse submission data and create preview
	submissionItems := make([]SubmissionListItem, len(submissions))
	for i, sub := range submissions {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(sub.Data), &data); err != nil {
			data = make(map[string]interface{})
		}

		// Create preview from first few fields
		preview := ""
		for j, field := range fields {
			if j >= 2 {
				break
			}
			if val, ok := data[field.Name]; ok {
				if preview != "" {
					preview += " | "
				}
				valStr := fmt.Sprintf("%v", val)
				if len(valStr) > 30 {
					valStr = valStr[:30] + "..."
				}
				preview += valStr
			}
		}

		submissionItems[i] = SubmissionListItem{
			Submission: sub,
			Data:       data,
			Preview:    preview,
		}
	}

	// Get counts
	totalCount, _ := h.queries.CountFormSubmissions(r.Context(), formID)
	unreadCount, _ := h.queries.CountUnreadSubmissions(r.Context(), formID)

	data := SubmissionsListData{
		Form:        *form,
		Fields:      fields,
		Submissions: submissionItems,
		TotalCount:  totalCount,
		UnreadCount: unreadCount,
		Pagination:  BuildAdminPagination(page, int(totalCount), perPage, fmt.Sprintf(redirectAdminFormsIDSubmissions, formID), r.URL.Query()),
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer,
		fmt.Sprintf("Submissions - %s", form.Name),
		formsSubmissionsBreadcrumbs(lang, form.Name, form.ID))
	viewData := convertSubmissionsListViewData(data, h.renderer, lang)
	renderTempl(w, r, adminviews.FormsSubmissionsPage(pc, viewData))
}

// SubmissionViewData holds data for viewing a single submission.
type SubmissionViewData struct {
	Form       store.Form
	Fields     []store.FormField
	Submission store.FormSubmission
	Data       map[string]interface{}
}

// ViewSubmission handles GET /admin/forms/{id}/submissions/{subId} - views a submission.
func (h *FormsHandler) ViewSubmission(w http.ResponseWriter, r *http.Request) {
	lang := h.renderer.GetAdminLang(r)

	formID := h.parseFormIDParam(w, r)
	if formID == 0 {
		return
	}

	subIDStr := chi.URLParam(r, "subId")
	subID, err := strconv.ParseInt(subIDStr, 10, 64)
	if err != nil {
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsIDSubmissions, formID), "Invalid submission ID")
		return
	}

	form := h.fetchFormByID(w, r, formID)
	if form == nil {
		return
	}

	submission, err := h.queries.GetFormSubmissionByID(r.Context(), subID)
	if err != nil {
		redirectURL := fmt.Sprintf(redirectAdminFormsIDSubmissions, formID)
		if errors.Is(err, sql.ErrNoRows) {
			flashError(w, r, h.renderer, redirectURL, "Submission not found")
		} else {
			slog.Error("failed to get submission", "error", err, "sub_id", subID)
			flashError(w, r, h.renderer, redirectURL, "Error loading submission")
		}
		return
	}

	// Verify submission belongs to this form
	if submission.FormID != formID {
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsIDSubmissions, formID), "Submission not found")
		return
	}

	// Mark as read if not already
	if !submission.IsRead {
		if err := h.queries.MarkSubmissionRead(r.Context(), subID); err != nil {
			slog.Error("failed to mark submission as read", "error", err, "sub_id", subID)
		}
		submission.IsRead = true
	}

	// Get fields for display
	fields, err := h.queries.GetFormFields(r.Context(), formID)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", formID)
		fields = []store.FormField{}
	}

	// Parse submission data
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(submission.Data), &data); err != nil {
		data = make(map[string]interface{})
	}

	handlerData := SubmissionViewData{
		Form:       *form,
		Fields:     fields,
		Submission: submission,
		Data:       data,
	}

	pc := buildPageContext(r, h.sessionManager, h.renderer,
		fmt.Sprintf("Submission #%d - %s", submission.ID, form.Name),
		formsSubmissionViewBreadcrumbs(lang, form.Name, form.ID, submission.ID))
	viewData := convertSubmissionViewViewData(handlerData, h.renderer, lang)
	renderTempl(w, r, adminviews.FormsSubmissionViewPage(pc, viewData))
}

// DeleteSubmission handles DELETE /admin/forms/{id}/submissions/{subId} - deletes a submission.
func (h *FormsHandler) DeleteSubmission(w http.ResponseWriter, r *http.Request) {
	if demoGuardAPI(w) {
		return
	}

	formID := parseFieldIDParam(w, r, "id")
	if formID == 0 {
		return
	}

	subID := parseFieldIDParam(w, r, "subId")
	if subID == 0 {
		return
	}

	submission, err := h.queries.GetFormSubmissionByID(r.Context(), subID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Submission not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get submission", "error", err, "sub_id", subID)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Verify submission belongs to this form
	if submission.FormID != formID {
		http.Error(w, "Submission not found", http.StatusNotFound)
		return
	}

	err = h.queries.DeleteFormSubmission(r.Context(), subID)
	if err != nil {
		slog.Error("failed to delete submission", "error", err, "sub_id", subID)
		http.Error(w, "Error deleting submission", http.StatusInternalServerError)
		return
	}

	slog.Info("submission deleted", "sub_id", subID, "form_id", formID, "deleted_by", middleware.GetUserID(r))

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsIDSubmissions, formID), "Submission deleted successfully")
}

// ExportSubmissions handles POST /admin/forms/{id}/submissions/export - exports submissions as CSV.
func (h *FormsHandler) ExportSubmissions(w http.ResponseWriter, r *http.Request) {
	formID := parseFieldIDParam(w, r, "id")
	if formID == 0 {
		return
	}

	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionExportData, fmt.Sprintf(redirectAdminFormsIDSubmissions, formID)) {
		return
	}

	form, ok := h.requireFormWithHTTPError(w, r, formID)
	if !ok {
		return
	}

	// Get all fields
	fields, err := h.queries.GetFormFields(r.Context(), formID)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", formID)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Get all submissions (no pagination for export)
	submissions, err := h.queries.GetFormSubmissions(r.Context(), store.GetFormSubmissionsParams{
		FormID: formID,
		Limit:  100000, // Large limit to get all
		Offset: 0,
	})
	if err != nil {
		slog.Error("failed to get submissions", "error", err, "form_id", formID)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Build CSV
	var csvBuilder strings.Builder

	// Header row
	headers := []string{"ID", "Submitted At", "IP Address", "Read"}
	for _, field := range fields {
		headers = append(headers, field.Label)
	}
	csvBuilder.WriteString(escapeCSVRow(headers))
	csvBuilder.WriteString("\n")

	// Data rows
	for _, sub := range submissions {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(sub.Data), &data); err != nil {
			data = make(map[string]interface{})
		}

		row := []string{
			fmt.Sprintf("%d", sub.ID),
			sub.CreatedAt.Format("2006-01-02 15:04:05"),
			sub.IpAddress.String,
			fmt.Sprintf("%t", sub.IsRead),
		}

		for _, field := range fields {
			val := ""
			if v, ok := data[field.Name]; ok {
				val = fmt.Sprintf("%v", v)
			}
			row = append(row, val)
		}

		csvBuilder.WriteString(escapeCSVRow(row))
		csvBuilder.WriteString("\n")
	}

	// Set headers for CSV download
	filename := fmt.Sprintf("%s-submissions-%s.csv", form.Slug, time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	_, _ = w.Write([]byte(csvBuilder.String()))

	slog.Info("submissions exported", "form_id", formID, "count", len(submissions))
}

// escapeCSVRow escapes a row of CSV values.
func escapeCSVRow(values []string) string {
	escaped := make([]string, len(values))
	for i, v := range values {
		// Escape double quotes by doubling them
		v = strings.ReplaceAll(v, "\"", "\"\"")
		// Wrap in quotes if contains comma, newline, or quotes
		if strings.ContainsAny(v, ",\"\n\r") {
			v = "\"" + v + "\""
		}
		escaped[i] = v
	}
	return strings.Join(escaped, ",")
}

// parseFieldIDParamJSON parses field ID from URL and returns JSON error response if invalid.
// Returns the ID and true on success, or 0 and false on error (response already sent).
func parseFieldIDParamJSON(w http.ResponseWriter, r *http.Request, paramName string) (int64, bool) {
	idStr := chi.URLParam(r, paramName)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid %s", paramName))
		return 0, false
	}
	return id, true
}

// fetchFieldByIDJSON retrieves a field by ID and verifies it belongs to the form.
// Returns nil if there was an error (JSON response already sent).
func (h *FormsHandler) fetchFieldByIDJSON(w http.ResponseWriter, r *http.Request, fieldID, formID int64) *store.FormField {
	field, err := h.queries.GetFormFieldByID(r.Context(), fieldID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "Field not found")
		} else {
			slog.Error("failed to get form field", "error", err, "field_id", fieldID)
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		}
		return nil
	}

	if field.FormID != formID {
		writeJSONError(w, http.StatusBadRequest, "Field does not belong to this form")
		return nil
	}
	return &field
}

// checkFormExistsJSON verifies a form exists by ID for JSON API handlers.
// Returns false if the form doesn't exist (JSON response already sent).
func (h *FormsHandler) checkFormExistsJSON(w http.ResponseWriter, r *http.Request, formID int64) bool {
	_, err := h.queries.GetFormByID(r.Context(), formID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "Form not found")
		} else {
			slog.Error("failed to get form", "error", err, "form_id", formID)
			writeJSONError(w, http.StatusInternalServerError, "Internal Server Error")
		}
		return false
	}
	return true
}

// requireFormWithHTTPError fetches a form by ID and sends HTTP error on failure.
// Returns the form and true if successful, or zero value and false if an error occurred.
func (h *FormsHandler) requireFormWithHTTPError(w http.ResponseWriter, r *http.Request, formID int64) (store.Form, bool) {
	form, err := h.queries.GetFormByID(r.Context(), formID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Form not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get form", "error", err, "form_id", formID)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return store.Form{}, false
	}
	return form, true
}

// getMaxFieldPosition returns the max position for fields in a form.
func (h *FormsHandler) getMaxFieldPosition(r *http.Request, formID int64) int64 {
	fields, err := h.queries.GetFormFields(r.Context(), formID)
	if err != nil {
		return -1
	}
	maxPos := int64(-1)
	for _, f := range fields {
		if f.Position > maxPos {
			maxPos = f.Position
		}
	}
	return maxPos
}

// =============================================================================
// FORM TRANSLATION HELPERS
// =============================================================================

// formLanguageInfo is an alias for entityLanguageInfo with FormTranslationInfo.
type formLanguageInfo = entityLanguageInfo[FormTranslationInfo]

// loadFormLanguageInfo loads language and translation info for a form.
func (h *FormsHandler) loadFormLanguageInfo(ctx context.Context, form store.Form) formLanguageInfo {
	return loadLanguageInfo(
		ctx, h.queries, model.EntityTypeForm, form.ID, form.LanguageCode,
		func(id int64) (store.Form, error) { return h.queries.GetFormByID(ctx, id) },
		func(lang store.Language, f store.Form) FormTranslationInfo {
			return FormTranslationInfo{Language: lang, Form: f}
		},
	)
}

// setupFormTranslation validates langCode, gets target language, and generates unique slug.
// Returns nil, false if validation failed (flash message sent).
func (h *FormsHandler) setupFormTranslation(
	w http.ResponseWriter,
	r *http.Request,
	entityID int64,
	sourceSlug string,
	redirectURL string,
) (*translationSetupResult, bool) {
	langCode := chi.URLParam(r, "langCode")
	if langCode == "" {
		flashError(w, r, h.renderer, redirectURL, "Language code is required")
		return nil, false
	}

	// Validate language and check for existing translation
	tc, ok := getTargetLanguageForTranslation(w, r, h.queries, h.renderer, langCode, redirectURL, model.EntityTypeForm, entityID)
	if !ok {
		return nil, false
	}

	// Generate a unique slug using FormSlugExists
	translatedSlug, err := generateUniqueSlug(sourceSlug, langCode, func(slug string) (int64, error) {
		return h.queries.FormSlugExists(r.Context(), slug)
	})
	if err != nil {
		slog.Error("database error checking slug", "error", err)
		flashError(w, r, h.renderer, redirectURL, "Error creating translation")
		return nil, false
	}

	return &translationSetupResult{
		TargetContext:  tc,
		TranslatedSlug: translatedSlug,
		Now:            time.Now(),
	}, true
}

// TranslateForm handles POST /admin/forms/{id}/translate/{langCode} - creates a translation.
func (h *FormsHandler) TranslateForm(w http.ResponseWriter, r *http.Request) {
	if demoGuard(w, r, h.renderer, middleware.RestrictionContentReadOnly, redirectAdminForms) {
		return
	}

	id := h.parseFormIDParam(w, r)
	if id == 0 {
		return
	}

	redirectURL := fmt.Sprintf(redirectAdminFormsID, id)
	sourceForm := h.fetchFormByID(w, r, id)
	if sourceForm == nil {
		return
	}

	// Validate language, check for existing translation, and generate unique slug
	setup, ok := h.setupFormTranslation(w, r, id, sourceForm.Slug, redirectURL)
	if !ok {
		return
	}

	// Create the translated form with same properties
	translatedForm, err := h.queries.CreateForm(r.Context(), store.CreateFormParams{
		Name:           sourceForm.Name, // Keep same name (user will translate)
		Slug:           setup.TranslatedSlug,
		Title:          sourceForm.Title,
		Description:    sourceForm.Description,
		SuccessMessage: sourceForm.SuccessMessage,
		EmailTo:        sourceForm.EmailTo,
		IsActive:       false, // Start as inactive until translated
		LanguageCode:   setup.TargetContext.TargetLang.Code,
		CreatedAt:      setup.Now,
		UpdatedAt:      setup.Now,
	})
	if err != nil {
		slog.Error("failed to create translated form", "error", err)
		flashError(w, r, h.renderer, redirectURL, "Error creating translation")
		return
	}

	// Copy all form fields to the translated form
	sourceFields, err := h.queries.GetFormFields(r.Context(), id)
	if err != nil {
		slog.Error("failed to get source form fields", "error", err, "form_id", id)
	} else {
		for _, field := range sourceFields {
			_, err := h.queries.CreateFormField(r.Context(), store.CreateFormFieldParams{
				FormID:       translatedForm.ID,
				Type:         field.Type,
				Name:         field.Name,
				Label:        field.Label,
				Placeholder:  field.Placeholder,
				HelpText:     field.HelpText,
				Options:      field.Options,
				Validation:   field.Validation,
				IsRequired:   field.IsRequired,
				Position:     field.Position,
				LanguageCode: setup.TargetContext.TargetLang.Code,
				CreatedAt:    setup.Now,
				UpdatedAt:    setup.Now,
			})
			if err != nil {
				slog.Error("failed to copy form field", "error", err, "field_id", field.ID)
			}
		}
	}

	// Create translation link from source to translated form
	_, err = h.queries.CreateTranslation(r.Context(), store.CreateTranslationParams{
		EntityType:    model.EntityTypeForm,
		EntityID:      id,
		LanguageID:    setup.TargetContext.TargetLang.ID,
		TranslationID: translatedForm.ID,
		CreatedAt:     setup.Now,
	})
	if err != nil {
		slog.Error("failed to create translation link", "error", err)
		// Form was created, so we should still redirect to it
	}

	slog.Info("form translation created",
		"source_form_id", id,
		"translated_form_id", translatedForm.ID,
		"language", setup.TargetContext.TargetLang.Code,
		"created_by", middleware.GetUserID(r))

	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminFormsID, translatedForm.ID),
		fmt.Sprintf("Translation created for %s. Please translate the form content.", setup.TargetContext.TargetLang.Name))
}

// FormTemplateData holds data for form template rendering.
type FormTemplateData struct {
	BaseTemplateData
	Form      store.Form
	Fields    []store.FormField
	Errors    map[string]string
	Values    map[string]string
	Success   bool
	CSRFToken template.HTML
}

// render renders a form page using the active theme's render engine.
// For templ engine: renders via templ component.
// For html engine: renders via html/template through activeTheme.RenderPage.
func (h *FormsHandler) render(w http.ResponseWriter, r *http.Request, data FormTemplateData) {
	activeTheme := h.themeManager.GetActiveTheme()

	engine := theme.ThemeEngineTempl
	if activeTheme != nil {
		engine = activeTheme.RenderEngine()
	}

	if engine == theme.ThemeEngineHTML {
		if activeTheme == nil {
			slog.Error("html engine selected but no active theme for form")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		var buf bytes.Buffer
		if err := activeTheme.RenderPage(&buf, "form", data); err != nil {
			slog.Error("html theme form render failed", "theme", activeTheme.Name, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
		return
	}

	viewData := h.buildPublicFormViewData(r, data)
	renderTempl(w, r, FrontendFormPage(viewData))
}

// buildPublicFormViewData converts FormTemplateData to the templ-friendly PublicFormViewData.
func (h *FormsHandler) buildPublicFormViewData(r *http.Request, data FormTemplateData) PublicFormViewData {
	fields := make([]PublicFormField, 0, len(data.Fields))
	for _, f := range data.Fields {
		pf := PublicFormField{
			ID:         f.ID,
			Type:       f.Type,
			Name:       f.Name,
			Label:      f.Label,
			IsRequired: f.IsRequired,
		}
		if f.Placeholder.Valid {
			pf.Placeholder = f.Placeholder.String
		}
		if f.HelpText.Valid {
			pf.HelpText = f.HelpText.String
		}
		if f.Options.Valid && f.Options.String != "" && f.Options.String != "[]" {
			pf.Options = parseFieldOptions(f.Options.String)
		}
		fields = append(fields, pf)
	}

	successMsg := ""
	if data.Form.SuccessMessage.Valid {
		successMsg = data.Form.SuccessMessage.String
	}

	description := ""
	if data.Form.Description.Valid {
		description = data.Form.Description.String
	}

	captchaWidget := h.getCaptchaWidget(r.Context())

	return PublicFormViewData{
		Base:           data.BaseTemplateData,
		FormTitle:      data.Form.Title,
		FormSlug:       data.Form.Slug,
		Description:    description,
		SuccessMessage: successMsg,
		Fields:         fields,
		Errors:         data.Errors,
		Values:         data.Values,
		Success:        data.Success,
		CaptchaWidget:  captchaWidget,
		Lang:           data.BaseTemplateData.LangCode,
	}
}

// getCaptchaWidget safely fetches the captcha widget HTML via hooks.
func (h *FormsHandler) getCaptchaWidget(ctx context.Context) string {
	if h.hookRegistry == nil {
		return ""
	}
	if !h.hookRegistry.HasHandlers(hcaptcha.HookFormCaptchaWidget) {
		return ""
	}
	result, err := h.hookRegistry.Call(ctx, hcaptcha.HookFormCaptchaWidget, nil)
	if err != nil {
		slog.Error("failed to get captcha widget", "error", err)
		return ""
	}
	if htmlStr, ok := result.(template.HTML); ok {
		return string(htmlStr)
	}
	return ""
}

// parseFieldOptions parses a JSON string array into a Go string slice.
func parseFieldOptions(jsonStr string) []string {
	var opts []string
	if err := json.Unmarshal([]byte(jsonStr), &opts); err != nil {
		return nil
	}
	return opts
}

// formT is a helper for i18n translation in public form templates.
func formT(lang, key string) string {
	return i18n.T(lang, key)
}

// requiredAttr returns templ.Attributes with "required" if isRequired is true.
func requiredAttr(isRequired bool) templ.Attributes {
	if isRequired {
		return templ.Attributes{"required": true}
	}
	return nil
}

// fieldValueContains checks if the comma-separated value string contains the given option.
func fieldValueContains(value, option string) bool {
	if value == "" {
		return false
	}
	return strings.Contains(value, option)
}

// getBaseTemplateData builds base template data for form rendering.
func (h *FormsHandler) getBaseTemplateData(r *http.Request, title string) BaseTemplateData {
	ctx := r.Context()
	var langCode string
	langPrefix := ""

	// Get language from middleware
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		langCode = langInfo.Code
		if !langInfo.IsDefault {
			langPrefix = "/" + langInfo.Code
		}
	}

	// Build base data
	data := BaseTemplateData{
		Title:       title,
		SiteName:    "oCMS",
		Year:        time.Now().Year(),
		ShowSidebar: false,
		ShowSearch:  true,
		CurrentPath: r.URL.Path,
		RequestURI:  r.URL.RequestURI(),
		LangCode:    langCode,
		LangPrefix:  langPrefix,
		Site: SiteData{
			SiteName:    "oCMS",
			Description: "A simple content management system",
			CurrentYear: time.Now().Year(),
			Settings:    make(map[string]string),
		},
	}

	// Get site configuration
	if h.cacheManager != nil {
		if name, err := h.cacheManager.GetConfig(ctx, "site_name"); err == nil && name != "" {
			data.SiteName = name
			data.Site.SiteName = name
		}
		if desc, err := h.cacheManager.GetConfig(ctx, "site_description"); err == nil && desc != "" {
			data.SiteTagline = desc
			data.Site.Description = desc
		}
		if url, err := h.cacheManager.GetConfig(ctx, "site_url"); err == nil && url != "" {
			data.SiteURL = strings.TrimSuffix(url, "/")
			data.Site.URL = strings.TrimSuffix(url, "/")
		}
		if logo, err := h.cacheManager.GetConfig(ctx, "site_logo"); err == nil && logo != "" {
			data.SiteLogo = logo
		}
		if ogImage, err := h.cacheManager.GetConfig(ctx, "default_og_image"); err == nil && ogImage != "" {
			data.OGImage = ogImage
			data.Site.DefaultOGImage = ogImage
		}
		if css, err := h.cacheManager.GetConfig(ctx, "custom_css"); err == nil && css != "" {
			data.CustomCSS = css
		}
	}

	// Get theme settings
	if activeTheme := h.themeManager.GetActiveTheme(); activeTheme != nil {
		data.Site.Theme = &activeTheme.Config
		data.ThemeSettings = make(map[string]string)

		configKey := "theme_settings_" + activeTheme.Name
		var settingsJSON string
		if h.cacheManager != nil {
			settingsJSON, _ = h.cacheManager.GetConfig(ctx, configKey)
		}
		if settingsJSON != "" {
			var settings map[string]string
			if err := json.Unmarshal([]byte(settingsJSON), &settings); err == nil {
				data.ThemeSettings = settings
			}
		}
	}

	// Apply translated config values for current language
	if langInfo := middleware.GetLanguage(r); langInfo != nil {
		if translatedName, err := h.queries.GetConfigTranslationByKeyAndLangCode(ctx, store.GetConfigTranslationByKeyAndLangCodeParams{
			ConfigKey: "site_name",
			Code:      langInfo.Code,
		}); err == nil && translatedName.Value != "" {
			data.SiteName = translatedName.Value
			data.Site.SiteName = translatedName.Value
		}
		if translatedDesc, err := h.queries.GetConfigTranslationByKeyAndLangCode(ctx, store.GetConfigTranslationByKeyAndLangCodeParams{
			ConfigKey: "site_description",
			Code:      langInfo.Code,
		}); err == nil && translatedDesc.Value != "" {
			data.SiteTagline = translatedDesc.Value
			data.Site.Description = translatedDesc.Value
		}
	}

	// Load menus
	if h.menuService != nil {
		data.MainMenu = h.loadMenu("main", r.URL.Path, langCode)
		data.FooterMenu = h.loadMenu("footer", r.URL.Path, langCode)
		data.Navigation = data.MainMenu
		data.FooterNav = data.FooterMenu
	}

	return data
}

// loadMenu loads a menu by slug and language.
func (h *FormsHandler) loadMenu(slug, currentPath, langCode string) []MenuItem {
	var items []service.MenuItem
	if langCode != "" {
		items = h.menuService.GetMenuForLanguage(slug, langCode)
	} else {
		items = h.menuService.GetMenu(slug)
	}
	if items == nil {
		return nil
	}

	return h.menuItemsToView(items, currentPath)
}

// menuItemsToView converts service menu items to view items.
func (h *FormsHandler) menuItemsToView(items []service.MenuItem, currentPath string) []MenuItem {
	result := make([]MenuItem, 0, len(items))
	for _, item := range items {
		mi := MenuItem{
			Title:    item.Title,
			URL:      item.URL,
			Target:   item.Target,
			IsActive: item.URL == currentPath,
			Children: h.menuItemsToView(item.Children, currentPath),
		}
		result = append(result, mi)
	}
	return result
}
