package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"ocms-go/internal/middleware"
	"ocms-go/internal/model"
	"ocms-go/internal/render"
	"ocms-go/internal/store"
)

// WebhooksPerPage is the number of webhooks to display per page.
const WebhooksPerPage = 20

// DeliveriesPerPage is the number of deliveries to display per page.
const DeliveriesPerPage = 25

// WebhooksHandler handles webhook management routes.
type WebhooksHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
}

// NewWebhooksHandler creates a new WebhooksHandler.
func NewWebhooksHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *WebhooksHandler {
	return &WebhooksHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
	}
}

// WebhooksListData holds data for the webhooks list template.
type WebhooksListData struct {
	Webhooks      []WebhookWithStats
	TotalWebhooks int64
}

// WebhookWithStats includes webhook data and delivery stats.
type WebhookWithStats struct {
	store.Webhook
	Events         []string
	TotalDelivered int64
	TotalPending   int64
	TotalDead      int64
	SuccessRate    float64
}

// WebhookFormData holds data for the webhook form template.
type WebhookFormData struct {
	Webhook     *store.Webhook
	Events      []model.WebhookEventInfo
	Errors      map[string]string
	FormValues  map[string]string
	FormEvents  []string
	FormHeaders map[string]string
	IsEdit      bool
}

// WebhookDeliveriesData holds data for the deliveries template.
type WebhookDeliveriesData struct {
	Webhook     store.Webhook
	Deliveries  []store.WebhookDelivery
	CurrentPage int
	TotalPages  int
	TotalCount  int64
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
}

// List handles GET /admin/webhooks - displays all webhooks.
func (h *WebhooksHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	webhooks, err := h.queries.ListWebhooks(r.Context())
	if err != nil {
		slog.Error("failed to list webhooks", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	totalWebhooks, err := h.queries.CountWebhooks(r.Context())
	if err != nil {
		slog.Error("failed to count webhooks", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Build webhooks with stats
	var webhooksWithStats []WebhookWithStats
	for _, wh := range webhooks {
		stats, _ := h.queries.GetDeliveryStats(r.Context(), wh.ID)

		// Parse events
		var events []string
		if wh.Events != "" && wh.Events != "[]" {
			_ = json.Unmarshal([]byte(wh.Events), &events)
		}

		// Calculate success rate
		var successRate float64
		if stats.Total > 0 {
			delivered := int64(0)
			if stats.Delivered.Valid {
				delivered = int64(stats.Delivered.Float64)
			}
			successRate = float64(delivered) / float64(stats.Total) * 100
		}

		pending := int64(0)
		if stats.Pending.Valid {
			pending = int64(stats.Pending.Float64)
		}
		dead := int64(0)
		if stats.Dead.Valid {
			dead = int64(stats.Dead.Float64)
		}
		delivered := int64(0)
		if stats.Delivered.Valid {
			delivered = int64(stats.Delivered.Float64)
		}

		webhooksWithStats = append(webhooksWithStats, WebhookWithStats{
			Webhook:        wh,
			Events:         events,
			TotalDelivered: delivered,
			TotalPending:   pending,
			TotalDead:      dead,
			SuccessRate:    successRate,
		})
	}

	data := WebhooksListData{
		Webhooks:      webhooksWithStats,
		TotalWebhooks: totalWebhooks,
	}

	if err := h.renderer.Render(w, r, "admin/webhooks_list", render.TemplateData{
		Title: "Webhooks",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Webhooks", URL: "/admin/webhooks", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// NewForm handles GET /admin/webhooks/new - displays the new webhook form.
func (h *WebhooksHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	data := WebhookFormData{
		Events:      model.AllWebhookEvents(),
		Errors:      make(map[string]string),
		FormValues:  make(map[string]string),
		FormEvents:  []string{},
		FormHeaders: make(map[string]string),
		IsEdit:      false,
	}

	if err := h.renderer.Render(w, r, "admin/webhooks_form", render.TemplateData{
		Title: "New Webhook",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Webhooks", URL: "/admin/webhooks"},
			{Label: "New Webhook", URL: "/admin/webhooks/new", Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Create handles POST /admin/webhooks - creates a new webhook.
func (h *WebhooksHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, "/admin/webhooks/new", http.StatusSeeOther)
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	url := strings.TrimSpace(r.FormValue("url"))
	secret := strings.TrimSpace(r.FormValue("secret"))
	events := r.Form["events"]
	isActive := r.FormValue("is_active") == "on" || r.FormValue("is_active") == "true" || r.FormValue("is_active") == "1"

	// Parse custom headers
	headerKeys := r.Form["header_key"]
	headerValues := r.Form["header_value"]
	headers := make(map[string]string)
	for i, key := range headerKeys {
		key = strings.TrimSpace(key)
		if key != "" && i < len(headerValues) {
			headers[key] = strings.TrimSpace(headerValues[i])
		}
	}

	// Auto-generate secret if empty
	if secret == "" {
		generatedSecret, err := model.GenerateWebhookSecret()
		if err != nil {
			slog.Error("failed to generate webhook secret", "error", err)
			h.renderer.SetFlash(r, "Error generating secret", "error")
			http.Redirect(w, r, "/admin/webhooks/new", http.StatusSeeOther)
			return
		}
		secret = generatedSecret
	}

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":   name,
		"url":    url,
		"secret": secret,
	}
	if isActive {
		formValues["is_active"] = "1"
	}

	// Validate
	errors := make(map[string]string)

	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) > 100 {
		errors["name"] = "Name must be less than 100 characters"
	}

	if url == "" {
		errors["url"] = "URL is required"
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		errors["url"] = "URL must start with http:// or https://"
	}

	if len(events) == 0 {
		errors["events"] = "At least one event is required"
	} else {
		// Validate event types
		validEvents := model.AllWebhookEvents()
		for _, e := range events {
			valid := false
			for _, ve := range validEvents {
				if e == ve.Type {
					valid = true
					break
				}
			}
			if !valid {
				errors["events"] = "Invalid event type: " + e
				break
			}
		}
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		data := WebhookFormData{
			Events:      model.AllWebhookEvents(),
			Errors:      errors,
			FormValues:  formValues,
			FormEvents:  events,
			FormHeaders: headers,
			IsEdit:      false,
		}

		if err := h.renderer.Render(w, r, "admin/webhooks_form", render.TemplateData{
			Title: "New Webhook",
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Webhooks", URL: "/admin/webhooks"},
				{Label: "New Webhook", URL: "/admin/webhooks/new", Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Create webhook
	now := time.Now()
	webhook, err := h.queries.CreateWebhook(r.Context(), store.CreateWebhookParams{
		Name:      name,
		Url:       url,
		Secret:    secret,
		Events:    model.EventsToJSON(events),
		IsActive:  isActive,
		Headers:   model.HeadersToJSON(headers),
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create webhook", "error", err)
		h.renderer.SetFlash(r, "Error creating webhook", "error")
		http.Redirect(w, r, "/admin/webhooks/new", http.StatusSeeOther)
		return
	}

	slog.Info("webhook created", "webhook_id", webhook.ID, "name", webhook.Name, "created_by", user.ID)
	h.renderer.SetFlash(r, "Webhook created successfully", "success")
	http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
}

// EditForm handles GET /admin/webhooks/{id} - displays the edit webhook form.
func (h *WebhooksHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid webhook ID", "error")
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	webhook, err := h.queries.GetWebhookByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Webhook not found", "error")
		} else {
			slog.Error("failed to get webhook", "error", err)
			h.renderer.SetFlash(r, "Error loading webhook", "error")
		}
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	// Parse events
	var events []string
	if webhook.Events != "" && webhook.Events != "[]" {
		_ = json.Unmarshal([]byte(webhook.Events), &events)
	}

	// Parse headers
	headers := make(map[string]string)
	if webhook.Headers != "" && webhook.Headers != "{}" {
		_ = json.Unmarshal([]byte(webhook.Headers), &headers)
	}

	formValues := map[string]string{
		"name":   webhook.Name,
		"url":    webhook.Url,
		"secret": webhook.Secret,
	}
	if webhook.IsActive {
		formValues["is_active"] = "1"
	}

	data := WebhookFormData{
		Webhook:     &webhook,
		Events:      model.AllWebhookEvents(),
		Errors:      make(map[string]string),
		FormValues:  formValues,
		FormEvents:  events,
		FormHeaders: headers,
		IsEdit:      true,
	}

	if err := h.renderer.Render(w, r, "admin/webhooks_form", render.TemplateData{
		Title: "Edit Webhook",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Webhooks", URL: "/admin/webhooks"},
			{Label: webhook.Name, URL: fmt.Sprintf("/admin/webhooks/%d", id), Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Update handles PUT /admin/webhooks/{id} - updates an existing webhook.
func (h *WebhooksHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid webhook ID", "error")
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	webhook, err := h.queries.GetWebhookByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Webhook not found", "error")
		} else {
			slog.Error("failed to get webhook", "error", err)
			h.renderer.SetFlash(r, "Error loading webhook", "error")
		}
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderer.SetFlash(r, "Invalid form data", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/webhooks/%d", id), http.StatusSeeOther)
		return
	}

	// Get form values
	name := strings.TrimSpace(r.FormValue("name"))
	url := strings.TrimSpace(r.FormValue("url"))
	secret := strings.TrimSpace(r.FormValue("secret"))
	events := r.Form["events"]
	isActive := r.FormValue("is_active") == "on" || r.FormValue("is_active") == "true" || r.FormValue("is_active") == "1"

	// Parse custom headers
	headerKeys := r.Form["header_key"]
	headerValues := r.Form["header_value"]
	headers := make(map[string]string)
	for i, key := range headerKeys {
		key = strings.TrimSpace(key)
		if key != "" && i < len(headerValues) {
			headers[key] = strings.TrimSpace(headerValues[i])
		}
	}

	// Keep existing secret if not changed
	if secret == "" {
		secret = webhook.Secret
	}

	// Store form values for re-rendering on error
	formValues := map[string]string{
		"name":   name,
		"url":    url,
		"secret": secret,
	}
	if isActive {
		formValues["is_active"] = "1"
	}

	// Validate
	errors := make(map[string]string)

	if name == "" {
		errors["name"] = "Name is required"
	} else if len(name) > 100 {
		errors["name"] = "Name must be less than 100 characters"
	}

	if url == "" {
		errors["url"] = "URL is required"
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		errors["url"] = "URL must start with http:// or https://"
	}

	if len(events) == 0 {
		errors["events"] = "At least one event is required"
	}

	// If there are validation errors, re-render the form
	if len(errors) > 0 {
		data := WebhookFormData{
			Webhook:     &webhook,
			Events:      model.AllWebhookEvents(),
			Errors:      errors,
			FormValues:  formValues,
			FormEvents:  events,
			FormHeaders: headers,
			IsEdit:      true,
		}

		if err := h.renderer.Render(w, r, "admin/webhooks_form", render.TemplateData{
			Title: "Edit Webhook",
			User:  user,
			Data:  data,
			Breadcrumbs: []render.Breadcrumb{
				{Label: "Dashboard", URL: "/admin"},
				{Label: "Webhooks", URL: "/admin/webhooks"},
				{Label: webhook.Name, URL: fmt.Sprintf("/admin/webhooks/%d", id), Active: true},
			},
		}); err != nil {
			slog.Error("render error", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Update webhook
	now := time.Now()
	_, err = h.queries.UpdateWebhook(r.Context(), store.UpdateWebhookParams{
		ID:        id,
		Name:      name,
		Url:       url,
		Secret:    secret,
		Events:    model.EventsToJSON(events),
		IsActive:  isActive,
		Headers:   model.HeadersToJSON(headers),
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update webhook", "error", err)
		h.renderer.SetFlash(r, "Error updating webhook", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/webhooks/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("webhook updated", "webhook_id", id, "updated_by", user.ID)
	h.renderer.SetFlash(r, "Webhook updated successfully", "success")
	http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
}

// Delete handles DELETE /admin/webhooks/{id} - deletes a webhook.
func (h *WebhooksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.sendDeleteError(w, "Invalid webhook ID")
		return
	}

	webhook, err := h.queries.GetWebhookByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.sendDeleteError(w, "Webhook not found")
		} else {
			slog.Error("failed to get webhook", "error", err)
			h.sendDeleteError(w, "Error loading webhook")
		}
		return
	}

	if err := h.queries.DeleteWebhook(r.Context(), id); err != nil {
		slog.Error("failed to delete webhook", "error", err)
		h.sendDeleteError(w, "Error deleting webhook")
		return
	}

	slog.Info("webhook deleted", "webhook_id", id, "name", webhook.Name, "deleted_by", user.ID)

	// Check if this is an HTMX request
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", `{"showToast": "Webhook deleted successfully"}`)
		w.WriteHeader(http.StatusOK)
		return
	}

	h.renderer.SetFlash(r, "Webhook deleted successfully", "success")
	http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
}

// Deliveries handles GET /admin/webhooks/{id}/deliveries - displays delivery history.
func (h *WebhooksHandler) Deliveries(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid webhook ID", "error")
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	webhook, err := h.queries.GetWebhookByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Webhook not found", "error")
		} else {
			slog.Error("failed to get webhook", "error", err)
			h.renderer.SetFlash(r, "Error loading webhook", "error")
		}
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	// Get page number
	pageStr := r.URL.Query().Get("page")
	page := 1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Get total count
	totalCount, err := h.queries.CountWebhookDeliveries(r.Context(), id)
	if err != nil {
		slog.Error("failed to count deliveries", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Calculate pagination
	totalPages := int((totalCount + DeliveriesPerPage - 1) / DeliveriesPerPage)
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := int64((page - 1) * DeliveriesPerPage)

	// Get deliveries
	deliveries, err := h.queries.ListWebhookDeliveries(r.Context(), store.ListWebhookDeliveriesParams{
		WebhookID: id,
		Limit:     DeliveriesPerPage,
		Offset:    offset,
	})
	if err != nil {
		slog.Error("failed to list deliveries", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := WebhookDeliveriesData{
		Webhook:     webhook,
		Deliveries:  deliveries,
		CurrentPage: page,
		TotalPages:  totalPages,
		TotalCount:  totalCount,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
	}

	if err := h.renderer.Render(w, r, "admin/webhooks_deliveries", render.TemplateData{
		Title: "Webhook Deliveries",
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: "Dashboard", URL: "/admin"},
			{Label: "Webhooks", URL: "/admin/webhooks"},
			{Label: webhook.Name, URL: fmt.Sprintf("/admin/webhooks/%d", id)},
			{Label: "Deliveries", URL: fmt.Sprintf("/admin/webhooks/%d/deliveries", id), Active: true},
		},
	}); err != nil {
		slog.Error("render error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Test handles POST /admin/webhooks/{id}/test - sends a test event.
func (h *WebhooksHandler) Test(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid webhook ID", "error")
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	webhook, err := h.queries.GetWebhookByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			h.renderer.SetFlash(r, "Webhook not found", "error")
		} else {
			slog.Error("failed to get webhook", "error", err)
			h.renderer.SetFlash(r, "Error loading webhook", "error")
		}
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	// Create a test payload
	testPayload := map[string]interface{}{
		"type":      "test",
		"timestamp": time.Now().Format(time.RFC3339),
		"data": map[string]interface{}{
			"message":    "This is a test webhook delivery",
			"webhook_id": webhook.ID,
			"triggered_by": map[string]interface{}{
				"user_id": user.ID,
				"email":   user.Email,
			},
		},
	}

	payloadBytes, _ := json.Marshal(testPayload)

	// Create delivery record
	now := time.Now()
	_, err = h.queries.CreateWebhookDelivery(r.Context(), store.CreateWebhookDeliveryParams{
		WebhookID: id,
		Event:     "test",
		Payload:   string(payloadBytes),
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create test delivery", "error", err)
		h.renderer.SetFlash(r, "Error creating test delivery", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/webhooks/%d", id), http.StatusSeeOther)
		return
	}

	slog.Info("test webhook created", "webhook_id", id, "triggered_by", user.ID)
	h.renderer.SetFlash(r, "Test event queued for delivery", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/webhooks/%d/deliveries", id), http.StatusSeeOther)
}

// RetryDelivery handles POST /admin/webhooks/{id}/deliveries/{did}/retry - retries a delivery.
func (h *WebhooksHandler) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)

	webhookIDStr := chi.URLParam(r, "id")
	webhookID, err := strconv.ParseInt(webhookIDStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid webhook ID", "error")
		http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
		return
	}

	deliveryIDStr := chi.URLParam(r, "did")
	deliveryID, err := strconv.ParseInt(deliveryIDStr, 10, 64)
	if err != nil {
		h.renderer.SetFlash(r, "Invalid delivery ID", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/webhooks/%d/deliveries", webhookID), http.StatusSeeOther)
		return
	}

	// Reset delivery for retry
	now := time.Now()
	err = h.queries.ResetDeliveryForRetry(r.Context(), store.ResetDeliveryForRetryParams{
		ID:        deliveryID,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to reset delivery", "error", err)
		h.renderer.SetFlash(r, "Error resetting delivery", "error")
		http.Redirect(w, r, fmt.Sprintf("/admin/webhooks/%d/deliveries", webhookID), http.StatusSeeOther)
		return
	}

	slog.Info("delivery reset for retry", "delivery_id", deliveryID, "webhook_id", webhookID, "reset_by", user.ID)
	h.renderer.SetFlash(r, "Delivery queued for retry", "success")
	http.Redirect(w, r, fmt.Sprintf("/admin/webhooks/%d/deliveries", webhookID), http.StatusSeeOther)
}

// sendDeleteError sends an error response for delete operations.
func (h *WebhooksHandler) sendDeleteError(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast": "`+message+`", "toastType": "error"}`)
	w.WriteHeader(http.StatusBadRequest)
}
