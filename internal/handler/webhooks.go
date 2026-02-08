// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

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

	"github.com/olegiv/ocms-go/internal/i18n"
	"github.com/olegiv/ocms-go/internal/middleware"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/render"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

// DeliveriesPerPage is the number of deliveries to display per page.
const DeliveriesPerPage = 25

// WebhooksHandler handles webhook management routes.
type WebhooksHandler struct {
	queries        *store.Queries
	renderer       *render.Renderer
	sessionManager *scs.SessionManager
	eventService   *service.EventService
}

// NewWebhooksHandler creates a new WebhooksHandler.
func NewWebhooksHandler(db *sql.DB, renderer *render.Renderer, sm *scs.SessionManager) *WebhooksHandler {
	return &WebhooksHandler{
		queries:        store.New(db),
		renderer:       renderer,
		sessionManager: sm,
		eventService:   service.NewEventService(db),
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
	Events              []string
	TotalDelivered      int64
	TotalPending        int64
	TotalDead           int64
	SuccessRate         float64
	HealthStatus        string // "green", "yellow", "red", "unknown"
	Last24hDelivered    int64
	Last24hTotal        int64
	LastSuccessfulAt    *time.Time // nil if never delivered
	LastSuccessfulEvent string     // event type of last successful delivery
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
	Webhook    store.Webhook
	Deliveries []store.WebhookDelivery
	TotalCount int64
	Pagination AdminPagination
}

// List handles GET /admin/webhooks - displays all webhooks.
func (h *WebhooksHandler) List(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	webhooks, err := h.queries.ListWebhooks(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to list webhooks", "error", err)
		return
	}

	totalWebhooks, err := h.queries.CountWebhooks(r.Context())
	if err != nil {
		logAndInternalError(w, "failed to count webhooks", "error", err)
		return
	}

	// Build webhooks with stats
	var webhooksWithStats []WebhookWithStats
	last24h := time.Now().Add(-24 * time.Hour)

	for _, wh := range webhooks {
		stats, _ := h.queries.GetDeliveryStats(r.Context(), wh.ID)
		stats24h, _ := h.queries.GetDeliveryStatsLast24h(r.Context(), store.GetDeliveryStatsLast24hParams{
			WebhookID: wh.ID,
			CreatedAt: last24h,
		})

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

		// Get last 24h stats
		last24hDelivered := int64(0)
		if stats24h.Delivered.Valid {
			last24hDelivered = int64(stats24h.Delivered.Float64)
		}

		// Calculate health status based on success rate
		healthStatus := calculateHealthStatus(successRate, stats.Total)

		// Get last successful delivery
		var lastSuccessfulAt *time.Time
		var lastSuccessfulEvent string
		if lastDelivery, err := h.queries.GetLastSuccessfulDelivery(r.Context(), wh.ID); err == nil {
			if lastDelivery.DeliveredAt.Valid {
				lastSuccessfulAt = &lastDelivery.DeliveredAt.Time
			}
			lastSuccessfulEvent = lastDelivery.Event
		}

		webhooksWithStats = append(webhooksWithStats, WebhookWithStats{
			Webhook:             wh,
			Events:              events,
			TotalDelivered:      delivered,
			TotalPending:        pending,
			TotalDead:           dead,
			SuccessRate:         successRate,
			HealthStatus:        healthStatus,
			Last24hDelivered:    last24hDelivered,
			Last24hTotal:        stats24h.Total,
			LastSuccessfulAt:    lastSuccessfulAt,
			LastSuccessfulEvent: lastSuccessfulEvent,
		})
	}

	data := WebhooksListData{
		Webhooks:      webhooksWithStats,
		TotalWebhooks: totalWebhooks,
	}

	h.renderer.RenderPage(w, r, "admin/webhooks_list", render.TemplateData{
		Title: i18n.T(lang, "nav.webhooks"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.webhooks"), URL: redirectAdminWebhooks, Active: true},
		},
	})
}

// NewForm handles GET /admin/webhooks/new - displays the new webhook form.
func (h *WebhooksHandler) NewForm(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionWebhooks, redirectAdminWebhooks) {
		return
	}

	h.renderNewWebhookForm(w, r, WebhookFormData{
		Events:      model.AllWebhookEvents(),
		Errors:      make(map[string]string),
		FormValues:  make(map[string]string),
		FormEvents:  []string{},
		FormHeaders: make(map[string]string),
		IsEdit:      false,
	})
}

// Create handles POST /admin/webhooks - creates a new webhook.
func (h *WebhooksHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionWebhooks, redirectAdminWebhooks) {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, redirectAdminWebhooksNew) {
		return
	}

	input := parseWebhookFormInput(r)

	// Auto-generate secret if empty
	if input.Secret == "" {
		generatedSecret, err := model.GenerateWebhookSecret()
		if err != nil {
			slog.Error("failed to generate webhook secret", "error", err)
			flashError(w, r, h.renderer, redirectAdminWebhooksNew, "Error generating secret")
			return
		}
		input.Secret = generatedSecret
	}

	// Validate with full event type checking for create
	validationErrors := validateWebhookForm(input, true)

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		h.renderNewWebhookForm(w, r, WebhookFormData{
			Events:      model.AllWebhookEvents(),
			Errors:      validationErrors,
			FormValues:  input.toFormValues(),
			FormEvents:  input.Events,
			FormHeaders: input.Headers,
			IsEdit:      false,
		})
		return
	}

	// Create webhook
	now := time.Now()
	webhook, err := h.queries.CreateWebhook(r.Context(), store.CreateWebhookParams{
		Name:      input.Name,
		Url:       input.URL,
		Secret:    input.Secret,
		Events:    model.EventsToJSON(input.Events),
		IsActive:  input.IsActive,
		Headers:   model.HeadersToJSON(input.Headers),
		CreatedBy: middleware.GetUserID(r),
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to create webhook", "error", err)
		flashError(w, r, h.renderer, redirectAdminWebhooksNew, "Error creating webhook")
		return
	}

	slog.Info("webhook created", "webhook_id", webhook.ID, "name", webhook.Name, "created_by", middleware.GetUserID(r))
	_ = h.eventService.LogWebhookEvent(r.Context(), model.EventLevelInfo, "Webhook created", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"webhook_id": webhook.ID, "name": webhook.Name, "url": webhook.Url})
	flashSuccess(w, r, h.renderer, redirectAdminWebhooks, "Webhook created successfully")
}

// EditForm handles GET /admin/webhooks/{id} - displays the edit webhook form.
func (h *WebhooksHandler) EditForm(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminWebhooks, "Invalid webhook ID")
		return
	}

	webhook, ok := h.requireWebhookWithRedirect(w, r, id)
	if !ok {
		return
	}

	// Parse events and headers from stored JSON
	events, headers := parseWebhookStoredData(webhook)

	formValues := map[string]string{
		"name":   webhook.Name,
		"url":    webhook.Url,
		"secret": webhook.Secret,
	}
	if webhook.IsActive {
		formValues["is_active"] = "1"
	}

	h.renderEditWebhookForm(w, r, webhook, WebhookFormData{
		Webhook:     &webhook,
		Events:      model.AllWebhookEvents(),
		Errors:      make(map[string]string),
		FormValues:  formValues,
		FormEvents:  events,
		FormHeaders: headers,
		IsEdit:      true,
	})
}

// Update handles PUT /admin/webhooks/{id} - updates an existing webhook.
func (h *WebhooksHandler) Update(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if demoGuard(w, r, h.renderer, middleware.RestrictionWebhooks, redirectAdminWebhooks) {
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminWebhooks, "Invalid webhook ID")
		return
	}

	webhook, ok := h.requireWebhookWithRedirect(w, r, id)
	if !ok {
		return
	}

	if !parseFormOrRedirect(w, r, h.renderer, fmt.Sprintf(redirectAdminWebhooksID, id)) {
		return
	}

	input := parseWebhookFormInput(r)

	// Keep existing secret if not changed
	if input.Secret == "" {
		input.Secret = webhook.Secret
	}

	// Validate without full event type checking for update
	validationErrors := validateWebhookForm(input, false)

	// If there are validation errors, re-render the form
	if len(validationErrors) > 0 {
		h.renderEditWebhookForm(w, r, webhook, WebhookFormData{
			Webhook:     &webhook,
			Events:      model.AllWebhookEvents(),
			Errors:      validationErrors,
			FormValues:  input.toFormValues(),
			FormEvents:  input.Events,
			FormHeaders: input.Headers,
			IsEdit:      true,
		})
		return
	}

	// Update webhook
	now := time.Now()
	_, err = h.queries.UpdateWebhook(r.Context(), store.UpdateWebhookParams{
		ID:        id,
		Name:      input.Name,
		Url:       input.URL,
		Secret:    input.Secret,
		Events:    model.EventsToJSON(input.Events),
		IsActive:  input.IsActive,
		Headers:   model.HeadersToJSON(input.Headers),
		UpdatedAt: now,
	})
	if err != nil {
		slog.Error("failed to update webhook", "error", err)
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminWebhooksID, id), "Error updating webhook")
		return
	}

	slog.Info("webhook updated", "webhook_id", id, "updated_by", middleware.GetUserID(r))
	_ = h.eventService.LogWebhookEvent(r.Context(), model.EventLevelInfo, "Webhook updated", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"webhook_id": id})
	flashSuccess(w, r, h.renderer, redirectAdminWebhooks, "Webhook updated successfully")
}

// Delete handles DELETE /admin/webhooks/{id} - deletes a webhook.
func (h *WebhooksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	// Block in demo mode
	if middleware.IsDemoMode() {
		h.sendDeleteError(w, middleware.DemoModeMessageDetailed(middleware.RestrictionWebhooks))
		return
	}

	id, err := ParseIDParam(r)
	if err != nil {
		h.sendDeleteError(w, "Invalid webhook ID")
		return
	}

	webhook, ok := h.requireWebhookWithDeleteError(w, r, id)
	if !ok {
		return
	}

	if err := h.queries.DeleteWebhook(r.Context(), id); err != nil {
		slog.Error("failed to delete webhook", "error", err)
		h.sendDeleteError(w, "Error deleting webhook")
		return
	}

	slog.Info("webhook deleted", "webhook_id", id, "name", webhook.Name, "deleted_by", middleware.GetUserID(r))
	_ = h.eventService.LogWebhookEvent(r.Context(), model.EventLevelInfo, "Webhook deleted", middleware.GetUserIDPtr(r), middleware.GetClientIP(r), middleware.GetRequestURL(r), map[string]any{"webhook_id": id, "name": webhook.Name})

	// Check if this is an HTMX request
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", `{"showToast": "Webhook deleted successfully"}`)
		w.WriteHeader(http.StatusOK)
		return
	}

	flashSuccess(w, r, h.renderer, redirectAdminWebhooks, "Webhook deleted successfully")
}

// Deliveries handles GET /admin/webhooks/{id}/deliveries - displays delivery history.
func (h *WebhooksHandler) Deliveries(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminWebhooks, "Invalid webhook ID")
		return
	}

	webhook, ok := h.requireWebhookWithRedirect(w, r, id)
	if !ok {
		return
	}

	page := ParsePageParam(r)

	// Get total count
	totalCount, err := h.queries.CountWebhookDeliveries(r.Context(), id)
	if err != nil {
		logAndInternalError(w, "failed to count deliveries", "error", err)
		return
	}

	// Normalize page to valid range
	page, _ = NormalizePagination(page, int(totalCount), DeliveriesPerPage)
	offset := int64((page - 1) * DeliveriesPerPage)

	// Get deliveries
	deliveries, err := h.queries.ListWebhookDeliveries(r.Context(), store.ListWebhookDeliveriesParams{
		WebhookID: id,
		Limit:     DeliveriesPerPage,
		Offset:    offset,
	})
	if err != nil {
		logAndInternalError(w, "failed to list deliveries", "error", err)
		return
	}

	data := WebhookDeliveriesData{
		Webhook:    webhook,
		Deliveries: deliveries,
		TotalCount: totalCount,
		Pagination: BuildAdminPagination(page, int(totalCount), DeliveriesPerPage, fmt.Sprintf(redirectAdminWebhooksIDDeliveries, id), r.URL.Query()),
	}

	h.renderer.RenderPage(w, r, "admin/webhooks_deliveries", render.TemplateData{
		Title: i18n.T(lang, "webhooks.deliveries_title"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.webhooks"), URL: redirectAdminWebhooks},
			{Label: webhook.Name, URL: fmt.Sprintf(redirectAdminWebhooksID, id)},
			{Label: i18n.T(lang, "webhooks.deliveries_title"), URL: fmt.Sprintf(redirectAdminWebhooksIDDeliveries, id), Active: true},
		},
	})
}

// Test handles POST /admin/webhooks/{id}/test - sends a test event.
func (h *WebhooksHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, err := ParseIDParam(r)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminWebhooks, "Invalid webhook ID")
		return
	}

	webhook, ok := h.requireWebhookWithRedirect(w, r, id)
	if !ok {
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
				"user_id": middleware.GetUserID(r),
				"email":   middleware.GetUserEmail(r),
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
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminWebhooksID, id), "Error creating test delivery")
		return
	}

	slog.Info("test webhook created", "webhook_id", id, "triggered_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminWebhooksIDDeliveries, id), "Test event queued for delivery")
}

// RetryDelivery handles POST /admin/webhooks/{id}/deliveries/{did}/retry - retries a delivery.
func (h *WebhooksHandler) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	webhookIDStr := chi.URLParam(r, "id")
	webhookID, err := strconv.ParseInt(webhookIDStr, 10, 64)
	if err != nil {
		flashError(w, r, h.renderer, redirectAdminWebhooks, "Invalid webhook ID")
		return
	}

	deliveryIDStr := chi.URLParam(r, "did")
	deliveryID, err := strconv.ParseInt(deliveryIDStr, 10, 64)
	if err != nil {
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminWebhooksIDDeliveries, webhookID), "Invalid delivery ID")
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
		flashError(w, r, h.renderer, fmt.Sprintf(redirectAdminWebhooksIDDeliveries, webhookID), "Error resetting delivery")
		return
	}

	slog.Info("delivery reset for retry", "delivery_id", deliveryID, "webhook_id", webhookID, "reset_by", middleware.GetUserID(r))
	flashSuccess(w, r, h.renderer, fmt.Sprintf(redirectAdminWebhooksIDDeliveries, webhookID), "Delivery queued for retry")
}

// sendDeleteError sends an error response for delete operations.
func (h *WebhooksHandler) sendDeleteError(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("HX-Trigger", `{"showToast": "`+message+`", "toastType": "error"}`)
	w.WriteHeader(http.StatusBadRequest)
}

// requireWebhookWithRedirect fetches a webhook by ID and redirects with flash on error.
func (h *WebhooksHandler) requireWebhookWithRedirect(w http.ResponseWriter, r *http.Request, id int64) (store.Webhook, bool) {
	return requireEntityWithRedirect(w, r, h.renderer, redirectAdminWebhooks, "Webhook", id,
		func(id int64) (store.Webhook, error) { return h.queries.GetWebhookByID(r.Context(), id) })
}

// requireWebhookWithDeleteError fetches a webhook by ID and sends delete error on failure.
func (h *WebhooksHandler) requireWebhookWithDeleteError(w http.ResponseWriter, r *http.Request, id int64) (store.Webhook, bool) {
	return requireEntityWithCustomError(w, "Webhook", id,
		func(id int64) (store.Webhook, error) { return h.queries.GetWebhookByID(r.Context(), id) },
		h.sendDeleteError)
}

// webhookFormInput holds parsed form values for webhook create/update.
type webhookFormInput struct {
	Name     string
	URL      string
	Secret   string
	Events   []string
	IsActive bool
	Headers  map[string]string
}

// parseWebhookFormInput extracts and parses webhook form values.
func parseWebhookFormInput(r *http.Request) webhookFormInput {
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

	return webhookFormInput{
		Name:     name,
		URL:      url,
		Secret:   secret,
		Events:   events,
		IsActive: isActive,
		Headers:  headers,
	}
}

// toFormValues converts webhookFormInput to a map for form re-rendering.
func (input webhookFormInput) toFormValues() map[string]string {
	formValues := map[string]string{
		"name":   input.Name,
		"url":    input.URL,
		"secret": input.Secret,
	}
	if input.IsActive {
		formValues["is_active"] = "1"
	}
	return formValues
}

// parseWebhookStoredData parses events and headers from a stored webhook's JSON fields.
func parseWebhookStoredData(webhook store.Webhook) ([]string, map[string]string) {
	var events []string
	if webhook.Events != "" && webhook.Events != "[]" {
		_ = json.Unmarshal([]byte(webhook.Events), &events)
	}

	headers := make(map[string]string)
	if webhook.Headers != "" && webhook.Headers != "{}" {
		_ = json.Unmarshal([]byte(webhook.Headers), &headers)
	}

	return events, headers
}

// validateWebhookForm validates webhook form input and returns validation errors.
func validateWebhookForm(input webhookFormInput, validateEvents bool) map[string]string {
	validationErrors := make(map[string]string)

	if input.Name == "" {
		validationErrors["name"] = "Name is required"
	} else if len(input.Name) > 100 {
		validationErrors["name"] = "Name must be less than 100 characters"
	}

	if input.URL == "" {
		validationErrors["url"] = "URL is required"
	} else if !strings.HasPrefix(input.URL, "http://") && !strings.HasPrefix(input.URL, "https://") {
		validationErrors["url"] = "URL must start with http:// or https://"
	}

	if len(input.Events) == 0 {
		validationErrors["events"] = "At least one event is required"
	} else if validateEvents {
		// Validate event types
		validEvents := model.AllWebhookEvents()
		for _, e := range input.Events {
			valid := false
			for _, ve := range validEvents {
				if e == ve.Type {
					valid = true
					break
				}
			}
			if !valid {
				validationErrors["events"] = "Invalid event type: " + e
				break
			}
		}
	}

	return validationErrors
}

// renderNewWebhookForm renders the new webhook form with the given data.
func (h *WebhooksHandler) renderNewWebhookForm(w http.ResponseWriter, r *http.Request, data WebhookFormData) {
	user := middleware.GetUser(r)
	lang := middleware.GetAdminLang(r)

	h.renderer.RenderPage(w, r, "admin/webhooks_form", render.TemplateData{
		Title: i18n.T(lang, "webhooks.new"),
		User:  user,
		Data:  data,
		Breadcrumbs: []render.Breadcrumb{
			{Label: i18n.T(lang, "nav.dashboard"), URL: redirectAdmin},
			{Label: i18n.T(lang, "nav.webhooks"), URL: redirectAdminWebhooks},
			{Label: i18n.T(lang, "webhooks.new"), URL: redirectAdminWebhooksNew, Active: true},
		},
	})
}

// renderEditWebhookForm renders the edit webhook form with the given data.
func (h *WebhooksHandler) renderEditWebhookForm(w http.ResponseWriter, r *http.Request, webhook store.Webhook, data WebhookFormData) {
	lang := middleware.GetAdminLang(r)
	renderEntityEditPage(w, r, h.renderer, "admin/webhooks_form",
		webhook.Name, data, lang,
		"nav.webhooks", redirectAdminWebhooks,
		webhook.Name, fmt.Sprintf(redirectAdminWebhooksID, webhook.ID))
}

// calculateHealthStatus determines the health status based on success rate.
// Green: > 95% success rate
// Yellow: 80-95% success rate
// Red: < 80% success rate
// Unknown: No deliveries yet
func calculateHealthStatus(successRate float64, totalDeliveries int64) string {
	if totalDeliveries == 0 {
		return "unknown"
	}
	if successRate >= 95 {
		return "green"
	}
	if successRate >= 80 {
		return "yellow"
	}
	return "red"
}
