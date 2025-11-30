package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
	"ocms-go/internal/util"
)

// FormsHandler handles form management routes.
type FormsHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewFormsHandler creates a new FormsHandler.
func NewFormsHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *FormsHandler {
	return &FormsHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// FormListItem represents a form with submission counts.
type FormListItem struct {
	Form            store.Form
	SubmissionCount int64
	UnreadCount     int64
}

// FormsListData holds data for the forms list template.
type FormsListData struct {
	Forms []FormListItem
}

// List handles GET /admin/forms - displays a list of forms.
func (h *FormsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

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

	if err := h.renderer.Render(w, r, "admin/forms_list", render.TemplateData{
		Title: "Forms",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Forms", URL: "/admin/forms", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// FormFormData holds data for the form create/edit template.
type FormFormData struct {
	Form       *store.Form
	Fields     []store.FormField
	FieldTypes []string
	Errors     map[string]string
	FormValues map[string]string
	IsEdit     bool
}

// NewForm handles GET /admin/forms/new - displays the new form form.
func (h *FormsHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	data := FormFormData{
		FieldTypes: model.ValidFieldTypes(),
		Errors:     make(map[string]string),
		FormValues: map[string]string{
			"success_message": "Thank you for your submission.",
			"is_active":       "true",
		},
		IsEdit: false,
	}

	if err := h.renderer.Render(w, r, "admin/forms_form", render.TemplateData{
		Title: "New Form",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Forms", URL: "/admin/forms"},
			{Label: "New Form", URL: "/admin/forms/new", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Create handles POST /admin/forms - creates a new form.
func (h *FormsHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/forms/new", http.StatusSeeOther)
		return
	}

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

	errors := make(map[string]string)

	// Validate name
	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) < 2 {
		errors["name"] = "Name must be at least 2 characters"
	}

	// Validate title
	if title == "" {
		errors["title"] = "Title is required"
	}

	// Validate slug
	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format"
	} else {
		existing, err := h.queries.GetFormBySlug(r.Context(), slug)
		if err == nil && existing.ID > 0 {
			errors["slug"] = "Slug already exists"
		} else if err != nil && err != sql.ErrNoRows {
			slog.Error("database error checking slug", "error", err)
			errors["slug"] = "Error checking slug"
		}
	}

	// Set default success message
	if successMessage == "" {
		successMessage = "Thank you for your submission."
		formValues["success_message"] = successMessage
	}

	if len(errors) > 0 {
		data := FormFormData{
			FieldTypes: model.ValidFieldTypes(),
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     false,
		}

		if err := h.renderer.Render(w, r, "admin/forms_form", render.TemplateData{
			Title: "New Form",
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Forms", URL: "/admin/forms"},
				{Label: "New Form", URL: "/admin/forms/new", Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	now := time.Now()
	form, err := h.queries.CreateForm(r.Context(), store.CreateFormParams{
		Name:           name,
		Slug:           slug,
		Title:          title,
		Description:    sql.NullString{String: description, Valid: description != ""},
		SuccessMessage: sql.NullString{String: successMessage, Valid: successMessage != ""},
		EmailTo:        sql.NullString{String: emailTo, Valid: emailTo != ""},
		IsActive:       isActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		slog.Error("failed to create form", "error", err)
		h.renderer.SetFlash(r, "Error creating form", "error")
		http.Redirect(w, r, "/admin/forms/new", http.StatusSeeOther)
		return
	}

	slog.Info("form created", "form_id", form.ID, "slug", form.Slug)
	h.renderer.SetFlash(r, "Form created successfully", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/forms/%d", form.ID), http.StatusSeeOther)
}

// EditForm handles GET /admin/forms/{id} - displays the form builder.
func (h *FormsHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid form ID", "error")
		http.Redirect(w, r, "/admin/forms", http.StatusSeeOther)
		return
	}

	form, err := h.queries.GetFormByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Form not found", "error")
		} else {
			slog.Error("failed to get form", "error", err, "form_id", id)
			h.renderer.SetFlash(r, "Error loading form", "error")
		}
		http.Redirect(w, r, "/admin/forms", http.StatusSeeOther)
		return
	}

	fields, err := h.queries.GetFormFields(r.Context(), id)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", id)
		fields = []store.FormField{}
	}

	data := FormFormData{
		Form:       &form,
		Fields:     fields,
		FieldTypes: model.ValidFieldTypes(),
		Errors:     make(map[string]string),
		FormValues: make(map[string]string),
		IsEdit:     true,
	}

	if err := h.renderer.Render(w, r, "admin/forms_form", render.TemplateData{
		Title: fmt.Sprintf("Edit Form - %s", form.Name),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Forms", URL: "/admin/forms"},
			{Label: form.Name, URL: fmt.Sprintf("/admin/forms/%d", form.ID), Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles PUT /admin/forms/{id} - updates a form.
func (h *FormsHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid form ID", "error")
		http.Redirect(w, r, "/admin/forms", http.StatusSeeOther)
		return
	}

	form, err := h.queries.GetFormByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Form not found", "error")
		} else {
			slog.Error("failed to get form", "error", err, "form_id", id)
			h.renderer.SetFlash(r, "Error loading form", "error")
		}
		http.Redirect(w, r, "/admin/forms", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/forms/%d", id), http.StatusSeeOther)
		return
	}

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

	errors := make(map[string]string)

	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) < 2 {
		errors["name"] = "Name must be at least 2 characters"
	}

	if title == "" {
		errors["title"] = "Title is required"
	}

	if slug == "" {
		slug = util.Slugify(name)
		formValues["slug"] = slug
	}

	if slug == "" {
		errors["slug"] = "Slug is required"
	} else if !util.IsValidSlug(slug) {
		errors["slug"] = "Invalid slug format"
	} else if slug != form.Slug {
		existing, err := h.queries.GetFormBySlug(r.Context(), slug)
		if err == nil && existing.ID > 0 && existing.ID != id {
			errors["slug"] = "Slug already exists"
		} else if err != nil && err != sql.ErrNoRows {
			slog.Error("database error checking slug", "error", err)
			errors["slug"] = "Error checking slug"
		}
	}

	if successMessage == "" {
		successMessage = "Thank you for your submission."
		formValues["success_message"] = successMessage
	}

	if len(errors) > 0 {
		fields, _ := h.queries.GetFormFields(r.Context(), id)

		data := FormFormData{
			Form:       &form,
			Fields:     fields,
			FieldTypes: model.ValidFieldTypes(),
			Errors:     errors,
			FormValues: formValues,
			IsEdit:     true,
		}

		if err := h.renderer.Render(w, r, "admin/forms_form", render.TemplateData{
			Title: fmt.Sprintf("Edit Form - %s", form.Name),
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Forms", URL: "/admin/forms"},
				{Label: form.Name, URL: fmt.Sprintf("/admin/forms/%d", form.ID), Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	now := time.Now()
	_, err = h.queries.UpdateForm(r.Context(), store.UpdateFormParams{
		ID:             id,
		Name:           name,
		Slug:           slug,
		Title:          title,
		Description:    sql.NullString{String: description, Valid: description != ""},
		SuccessMessage: sql.NullString{String: successMessage, Valid: successMessage != ""},
		EmailTo:        sql.NullString{String: emailTo, Valid: emailTo != ""},
		IsActive:       isActive,
		UpdatedAt:      now,
	})
	if err != nil {
		slog.Error("failed to update form", "error", err, "form_id", id)
		h.renderer.SetFlash(r, "Error updating form", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/forms/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("form updated", "form_id", id, "updated_by", user.ID)
	h.renderer.SetFlash(r, "Form updated successfully", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/forms/%d", id), http.StatusSeeOther)
}

// Delete handles DELETE /admin/forms/{id} - deletes a form.
func (h *FormsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid form ID", http.StatusBadRequest)
		return
	}

	_, err = h.queries.GetFormByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Form not found", http.StatusNotFound)
		} else {
			slog.Error("failed to get form", "error", err, "form_id", id)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	err = h.queries.DeleteForm(r.Context(), id)
	if err != nil {
		slog.Error("failed to delete form", "error", err, "form_id", id)
		http.Error(w, "Error deleting form", http.StatusInternalServerError)
		return
	}

	user := middleware.GetUser(r)
	slog.Info("form deleted", "form_id", id, "deleted_by", user.ID)

	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.renderer.SetFlash(r, "Form deleted successfully", "success")
	http.Redirect(w, r, "/admin/forms", http.StatusSeeOther)
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
	idStr := chi.URLParam(r, "id")
	formID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid form ID", http.StatusBadRequest)
		return
	}

	_, err = h.queries.GetFormByID(r.Context(), formID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Form not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	var req AddFieldRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate
	if req.Label == "" {
		http.Error(w, "Label is required", http.StatusBadRequest)
		return
	}

	if !model.IsValidFieldType(req.Type) {
		http.Error(w, "Invalid field type", http.StatusBadRequest)
		return
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

	// Get max position
	fields, err := h.queries.GetFormFields(r.Context(), formID)
	maxPos := int64(-1)
	if err == nil {
		for _, f := range fields {
			if f.Position > maxPos {
				maxPos = f.Position
			}
		}
	}

	now := time.Now()
	field, err := h.queries.CreateFormField(r.Context(), store.CreateFormFieldParams{
		FormID:      formID,
		Type:        req.Type,
		Name:        req.Name,
		Label:       req.Label,
		Placeholder: sql.NullString{String: req.Placeholder, Valid: req.Placeholder != ""},
		HelpText:    sql.NullString{String: req.HelpText, Valid: req.HelpText != ""},
		Options:     sql.NullString{String: req.Options, Valid: req.Options != ""},
		Validation:  sql.NullString{String: req.Validation, Valid: req.Validation != ""},
		IsRequired:  req.IsRequired,
		Position:    maxPos + 1,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		slog.Error("failed to create form field", "error", err)
		http.Error(w, "Error creating field", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"field":   field,
	})
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
	formIDStr := chi.URLParam(r, "id")
	formID, err := strconv.ParseInt(formIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid form ID", http.StatusBadRequest)
		return
	}

	fieldIDStr := chi.URLParam(r, "fieldId")
	fieldID, err := strconv.ParseInt(fieldIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid field ID", http.StatusBadRequest)
		return
	}

	field, err := h.queries.GetFormFieldByID(r.Context(), fieldID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Field not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if field.FormID != formID {
		http.Error(w, "Field does not belong to this form", http.StatusBadRequest)
		return
	}

	var req UpdateFieldRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Label == "" {
		http.Error(w, "Label is required", http.StatusBadRequest)
		return
	}

	if !model.IsValidFieldType(req.Type) {
		http.Error(w, "Invalid field type", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = util.Slugify(req.Label)
		req.Name = strings.ReplaceAll(req.Name, "-", "_")
	}

	if req.Options == "" {
		req.Options = "[]"
	}
	if req.Validation == "" {
		req.Validation = "{}"
	}

	now := time.Now()
	updatedField, err := h.queries.UpdateFormField(r.Context(), store.UpdateFormFieldParams{
		ID:          fieldID,
		Type:        req.Type,
		Name:        req.Name,
		Label:       req.Label,
		Placeholder: sql.NullString{String: req.Placeholder, Valid: req.Placeholder != ""},
		HelpText:    sql.NullString{String: req.HelpText, Valid: req.HelpText != ""},
		Options:     sql.NullString{String: req.Options, Valid: req.Options != ""},
		Validation:  sql.NullString{String: req.Validation, Valid: req.Validation != ""},
		IsRequired:  req.IsRequired,
		Position:    field.Position,
		UpdatedAt:   now,
	})
	if err != nil {
		slog.Error("failed to update form field", "error", err)
		http.Error(w, "Error updating field", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"field":   updatedField,
	})
}

// DeleteField handles DELETE /admin/forms/{id}/fields/{fieldId} - deletes a form field.
func (h *FormsHandler) DeleteField(w http.ResponseWriter, r *http.Request) {
	formIDStr := chi.URLParam(r, "id")
	formID, err := strconv.ParseInt(formIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid form ID", http.StatusBadRequest)
		return
	}

	fieldIDStr := chi.URLParam(r, "fieldId")
	fieldID, err := strconv.ParseInt(fieldIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid field ID", http.StatusBadRequest)
		return
	}

	field, err := h.queries.GetFormFieldByID(r.Context(), fieldID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Field not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if field.FormID != formID {
		http.Error(w, "Field does not belong to this form", http.StatusBadRequest)
		return
	}

	err = h.queries.DeleteFormField(r.Context(), fieldID)
	if err != nil {
		slog.Error("failed to delete form field", "error", err)
		http.Error(w, "Error deleting field", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// ReorderFieldsRequest represents the JSON request for reordering form fields.
type ReorderFieldsRequest struct {
	FieldIDs []int64 `json:"field_ids"`
}

// ReorderFields handles POST /admin/forms/{id}/fields/reorder - reorders form fields.
func (h *FormsHandler) ReorderFields(w http.ResponseWriter, r *http.Request) {
	formIDStr := chi.URLParam(r, "id")
	formID, err := strconv.ParseInt(formIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid form ID", http.StatusBadRequest)
		return
	}

	_, err = h.queries.GetFormByID(r.Context(), formID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Form not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	var req ReorderFieldsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
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

		_, err = h.queries.UpdateFormField(r.Context(), store.UpdateFormFieldParams{
			ID:          fieldID,
			Type:        field.Type,
			Name:        field.Name,
			Label:       field.Label,
			Placeholder: field.Placeholder,
			HelpText:    field.HelpText,
			Options:     field.Options,
			Validation:  field.Validation,
			IsRequired:  field.IsRequired,
			Position:    int64(i),
			UpdatedAt:   now,
		})
		if err != nil {
			slog.Error("failed to update field position", "error", err, "field_id", fieldID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
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

	form, err := h.queries.GetFormBySlug(r.Context(), slug)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.RenderNotFound(w, r)
		} else {
			slog.Error("failed to get form", "error", err, "slug", slug)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Check if form is active
	if !form.IsActive {
		h.renderer.RenderNotFound(w, r)
		return
	}

	fields, err := h.queries.GetFormFields(r.Context(), form.ID)
	if err != nil {
		slog.Error("failed to get form fields", "error", err, "form_id", form.ID)
		fields = []store.FormField{}
	}

	// Generate CSRF token
	csrfToken := h.sessionManager.Token(r.Context())

	// Get site name from config
	siteName := "oCMS"
	if siteConfig, err := h.queries.GetConfig(r.Context(), "site_name"); err == nil {
		if siteConfig.Value != "" {
			siteName = siteConfig.Value
		}
	}

	data := PublicFormData{
		Form:      form,
		Fields:    fields,
		Errors:    make(map[string]string),
		Values:    make(map[string]string),
		Success:   false,
		CSRFToken: csrfToken,
		SiteName:  siteName,
	}

	if err := h.renderer.Render(w, r, "public/form", render.TemplateData{
		Title: form.Title,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Submit handles POST /forms/{slug} - processes form submission.
func (h *FormsHandler) Submit(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	form, err := h.queries.GetFormBySlug(r.Context(), slug)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.RenderNotFound(w, r)
		} else {
			slog.Error("failed to get form", "error", err, "slug", slug)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Check if form is active
	if !form.IsActive {
		h.renderer.RenderNotFound(w, r)
		return
	}

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
		h.renderFormSuccess(w, r, form, fields)
		return
	}

	// Validate CSRF token
	csrfToken := r.FormValue("_csrf")
	if csrfToken == "" || csrfToken != h.sessionManager.Token(r.Context()) {
		slog.Warn("invalid CSRF token", "form_slug", slug, "ip", r.RemoteAddr)
		// Don't reveal it's a CSRF issue, just show generic error
		h.renderFormWithErrors(w, r, form, fields, map[string]string{"_form": "Session expired. Please try again."}, r.Form)
		return
	}

	// Collect values and validate
	values := make(map[string]string)
	errors := make(map[string]string)

	for _, field := range fields {
		value := strings.TrimSpace(r.FormValue(field.Name))
		values[field.Name] = value

		// Required validation
		if field.IsRequired && value == "" {
			errors[field.Name] = fmt.Sprintf("%s is required", field.Label)
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
				errors[field.Name] = "Please enter a valid email address"
			}
		case model.FieldTypeNumber:
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				errors[field.Name] = "Please enter a valid number"
			}
		case model.FieldTypeDate:
			if !isValidDate(value) {
				errors[field.Name] = "Please enter a valid date"
			}
		}

		// Custom validation from field's validation JSON
		if field.Validation.Valid && field.Validation.String != "" && field.Validation.String != "{}" {
			var validation map[string]interface{}
			if err := json.Unmarshal([]byte(field.Validation.String), &validation); err == nil {
				if minLen, ok := validation["minLength"].(float64); ok && len(value) < int(minLen) {
					errors[field.Name] = fmt.Sprintf("%s must be at least %d characters", field.Label, int(minLen))
				}
				if maxLen, ok := validation["maxLength"].(float64); ok && len(value) > int(maxLen) {
					errors[field.Name] = fmt.Sprintf("%s must be no more than %d characters", field.Label, int(maxLen))
				}
				if pattern, ok := validation["pattern"].(string); ok && pattern != "" {
					if matched, _ := regexp.MatchString(pattern, value); !matched {
						errors[field.Name] = fmt.Sprintf("%s is not in the correct format", field.Label)
					}
				}
			}
		}
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		h.renderFormWithErrors(w, r, form, fields, errors, r.Form)
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
	_, err = h.queries.CreateFormSubmission(r.Context(), store.CreateFormSubmissionParams{
		FormID:    form.ID,
		Data:      string(dataJSON),
		IpAddress: sql.NullString{String: getClientIP(r), Valid: true},
		UserAgent: sql.NullString{String: r.UserAgent(), Valid: true},
		IsRead:    false,
		CreatedAt: time.Now(),
	})
	if err != nil {
		slog.Error("failed to save form submission", "error", err, "form_id", form.ID)
		h.renderFormWithErrors(w, r, form, fields, map[string]string{"_form": "Failed to save submission. Please try again."}, r.Form)
		return
	}

	slog.Info("form submission saved", "form_id", form.ID, "form_slug", slug)

	// TODO: Send notification email if configured
	// if form.EmailTo.Valid && form.EmailTo.String != "" {
	//     sendNotificationEmail(form, values)
	// }

	// Render success
	h.renderFormSuccess(w, r, form, fields)
}

// renderFormWithErrors renders the form with validation errors.
func (h *FormsHandler) renderFormWithErrors(w http.ResponseWriter, r *http.Request, form store.Form, fields []store.FormField, errors map[string]string, formData map[string][]string) {
	values := make(map[string]string)
	for key, vals := range formData {
		if len(vals) > 0 {
			values[key] = vals[0]
		}
	}

	csrfToken := h.sessionManager.Token(r.Context())

	siteName := "oCMS"
	if siteConfig, err := h.queries.GetConfig(r.Context(), "site_name"); err == nil {
		if siteConfig.Value != "" {
			siteName = siteConfig.Value
		}
	}

	data := PublicFormData{
		Form:      form,
		Fields:    fields,
		Errors:    errors,
		Values:    values,
		Success:   false,
		CSRFToken: csrfToken,
		SiteName:  siteName,
	}

	if err := h.renderer.Render(w, r, "public/form", render.TemplateData{
		Title: form.Title,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// renderFormSuccess renders the form success page.
func (h *FormsHandler) renderFormSuccess(w http.ResponseWriter, r *http.Request, form store.Form, fields []store.FormField) {
	siteName := "oCMS"
	if siteConfig, err := h.queries.GetConfig(r.Context(), "site_name"); err == nil {
		if siteConfig.Value != "" {
			siteName = siteConfig.Value
		}
	}

	data := PublicFormData{
		Form:     form,
		Fields:   fields,
		Success:  true,
		SiteName: siteName,
	}

	if err := h.renderer.Render(w, r, "public/form", render.TemplateData{
		Title: form.Title,
		Data:  data,
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
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

// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// Take the first IP in the list
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	// Check X-Real-IP header
	xrip := r.Header.Get("X-Real-IP")
	if xrip != "" {
		return xrip
	}

	// Fall back to RemoteAddr
	host := r.RemoteAddr
	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}
