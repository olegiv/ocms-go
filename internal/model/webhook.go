// Package model defines domain models and types used throughout the application.
package model

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Webhook event types
const (
	EventPageCreated     = "page.created"
	EventPageUpdated     = "page.updated"
	EventPageDeleted     = "page.deleted"
	EventPagePublished   = "page.published"
	EventPageUnpublished = "page.unpublished"
	EventMediaUploaded   = "media.uploaded"
	EventMediaDeleted    = "media.deleted"
	EventFormSubmitted   = "form.submitted"
	EventUserCreated     = "user.created"
	EventUserDeleted     = "user.deleted"
)

// Webhook delivery statuses
const (
	DeliveryStatusPending   = "pending"
	DeliveryStatusDelivered = "delivered"
	DeliveryStatusFailed    = "failed"
	DeliveryStatusDead      = "dead"
)

// WebhookEventInfo contains event type and description.
type WebhookEventInfo struct {
	Type        string
	Description string
}

// AllWebhookEvents returns all available webhook event types with descriptions.
func AllWebhookEvents() []WebhookEventInfo {
	return []WebhookEventInfo{
		{EventPageCreated, "When a new page is created"},
		{EventPageUpdated, "When a page is updated"},
		{EventPageDeleted, "When a page is deleted"},
		{EventPagePublished, "When a page is published"},
		{EventPageUnpublished, "When a page is unpublished"},
		{EventMediaUploaded, "When media is uploaded"},
		{EventMediaDeleted, "When media is deleted"},
		{EventFormSubmitted, "When a form is submitted"},
		{EventUserCreated, "When a user is created"},
		{EventUserDeleted, "When a user is deleted"},
	}
}

// Webhook represents a webhook configuration.
type Webhook struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Secret    string    `json:"-"` // Never expose in JSON
	Events    string    `json:"-"` // JSON array stored as string
	IsActive  bool      `json:"is_active"`
	Headers   string    `json:"-"` // JSON object stored as string
	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WebhookDelivery represents a webhook delivery attempt.
type WebhookDelivery struct {
	ID           int64         `json:"id"`
	WebhookID    int64         `json:"webhook_id"`
	Event        string        `json:"event"`
	Payload      string        `json:"payload"`
	ResponseCode sql.NullInt64 `json:"response_code,omitempty"`
	ResponseBody string        `json:"response_body,omitempty"`
	Attempts     int64         `json:"attempts"`
	NextRetryAt  sql.NullTime  `json:"next_retry_at,omitempty"`
	DeliveredAt  sql.NullTime  `json:"delivered_at,omitempty"`
	Status       string        `json:"status"`
	ErrorMessage string        `json:"error_message,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// GenerateWebhookSecret generates a random secret for webhook signing.
func GenerateWebhookSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GetEvents parses the JSON events string into a slice.
func (w *Webhook) GetEvents() []string {
	var events []string
	if w.Events == "" || w.Events == "[]" {
		return events
	}
	_ = json.Unmarshal([]byte(w.Events), &events)
	return events
}

// SetEvents sets the events from a slice to JSON string.
func (w *Webhook) SetEvents(events []string) {
	if len(events) == 0 {
		w.Events = "[]"
		return
	}
	data, _ := json.Marshal(events)
	w.Events = string(data)
}

// HasEvent checks if the webhook is subscribed to a specific event.
func (w *Webhook) HasEvent(event string) bool {
	for _, e := range w.GetEvents() {
		if e == event {
			return true
		}
	}
	return false
}

// GetHeaders parses the JSON headers string into a map.
func (w *Webhook) GetHeaders() map[string]string {
	headers := make(map[string]string)
	if w.Headers == "" || w.Headers == "{}" {
		return headers
	}
	_ = json.Unmarshal([]byte(w.Headers), &headers)
	return headers
}

// SetHeaders sets the headers from a map to JSON string.
func (w *Webhook) SetHeaders(headers map[string]string) {
	if len(headers) == 0 {
		w.Headers = "{}"
		return
	}
	data, _ := json.Marshal(headers)
	w.Headers = string(data)
}

// EventsToJSON converts a slice of events to a JSON string.
func EventsToJSON(events []string) string {
	if len(events) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(events)
	return string(data)
}

// HeadersToJSON converts a map of headers to a JSON string.
func HeadersToJSON(headers map[string]string) string {
	if len(headers) == 0 {
		return "{}"
	}
	data, _ := json.Marshal(headers)
	return string(data)
}

// IsPending returns true if the delivery is pending.
func (d *WebhookDelivery) IsPending() bool {
	return d.Status == DeliveryStatusPending
}

// IsDelivered returns true if the delivery was successful.
func (d *WebhookDelivery) IsDelivered() bool {
	return d.Status == DeliveryStatusDelivered
}

// IsFailed returns true if the delivery failed but may retry.
func (d *WebhookDelivery) IsFailed() bool {
	return d.Status == DeliveryStatusFailed
}

// IsDead returns true if the delivery has exhausted all retries.
func (d *WebhookDelivery) IsDead() bool {
	return d.Status == DeliveryStatusDead
}

// GetPayload parses the JSON payload string into a map.
func (d *WebhookDelivery) GetPayload() map[string]interface{} {
	var payload map[string]interface{}
	if d.Payload == "" {
		return payload
	}
	_ = json.Unmarshal([]byte(d.Payload), &payload)
	return payload
}
