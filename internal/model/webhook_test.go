// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"testing"
)

func TestGenerateWebhookSecret(t *testing.T) {
	secret1, err := GenerateWebhookSecret()
	if err != nil {
		t.Fatalf("GenerateWebhookSecret() error = %v", err)
	}

	// Secret should be 64 hex characters (32 bytes)
	if len(secret1) != 64 {
		t.Errorf("GenerateWebhookSecret() length = %d, want 64", len(secret1))
	}

	// Should be unique each time
	secret2, err := GenerateWebhookSecret()
	if err != nil {
		t.Fatalf("GenerateWebhookSecret() second call error = %v", err)
	}
	if secret1 == secret2 {
		t.Error("GenerateWebhookSecret() generated identical secrets")
	}
}

func TestWebhookGetEvents(t *testing.T) {
	tests := standardJSONArrayParseTests("page.created", "page.created", "page.updated", "media.uploaded")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Webhook{Events: tt.input}
			assertStringSliceEqual(t, "GetEvents()", w.GetEvents(), tt.want)
		})
	}
}

func TestWebhookSetEvents(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		want   string
	}{
		{
			name:   "empty",
			events: []string{},
			want:   "[]",
		},
		{
			name:   "nil",
			events: nil,
			want:   "[]",
		},
		{
			name:   "single event",
			events: []string{"page.created"},
			want:   `["page.created"]`,
		},
		{
			name:   "multiple events",
			events: []string{"page.created", "page.updated"},
			want:   `["page.created","page.updated"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Webhook{}
			w.SetEvents(tt.events)
			if w.Events != tt.want {
				t.Errorf("SetEvents() resulted in %v, want %v", w.Events, tt.want)
			}
		})
	}
}

func TestWebhookHasEvent(t *testing.T) {
	w := &Webhook{Events: `["page.created","page.updated"]`}
	runHasItemTests(t, []hasItemTest{
		{"page.created", true},
		{"page.updated", true},
		{"page.deleted", false},
		{"media.uploaded", false},
	}, w.HasEvent)
}

func TestWebhookGetHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers string
		want    map[string]string
	}{
		{
			name:    "empty string",
			headers: "",
			want:    map[string]string{},
		},
		{
			name:    "empty object",
			headers: "{}",
			want:    map[string]string{},
		},
		{
			name:    "single header",
			headers: `{"X-Custom":"value"}`,
			want:    map[string]string{"X-Custom": "value"},
		},
		{
			name:    "multiple headers",
			headers: `{"X-Custom":"value","Authorization":"Bearer token"}`,
			want:    map[string]string{"X-Custom": "value", "Authorization": "Bearer token"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Webhook{Headers: tt.headers}
			got := w.GetHeaders()
			if len(got) != len(tt.want) {
				t.Errorf("GetHeaders() = %v, want %v", got, tt.want)
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("GetHeaders()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestWebhookSetHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "empty",
			headers: map[string]string{},
			want:    "{}",
		},
		{
			name:    "nil",
			headers: nil,
			want:    "{}",
		},
		{
			name:    "single header",
			headers: map[string]string{"X-Custom": "value"},
			want:    `{"X-Custom":"value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Webhook{}
			w.SetHeaders(tt.headers)
			// For single header case, we can check exact match
			if len(tt.headers) <= 1 && w.Headers != tt.want {
				t.Errorf("SetHeaders() resulted in %v, want %v", w.Headers, tt.want)
			}
		})
	}
}

func TestEventsToJSON(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		want   string
	}{
		{
			name:   "empty",
			events: []string{},
			want:   "[]",
		},
		{
			name:   "nil",
			events: nil,
			want:   "[]",
		},
		{
			name:   "single",
			events: []string{"page.created"},
			want:   `["page.created"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EventsToJSON(tt.events); got != tt.want {
				t.Errorf("EventsToJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHeadersToJSON(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{
			name:    "empty",
			headers: map[string]string{},
			want:    "{}",
		},
		{
			name:    "nil",
			headers: nil,
			want:    "{}",
		},
		{
			name:    "single",
			headers: map[string]string{"X-Custom": "value"},
			want:    `{"X-Custom":"value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HeadersToJSON(tt.headers)
			// For single-key maps we can check exact match
			if len(tt.headers) <= 1 && got != tt.want {
				t.Errorf("HeadersToJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebhookDeliveryStatus(t *testing.T) {
	tests := []struct {
		status      string
		isPending   bool
		isDelivered bool
		isFailed    bool
		isDead      bool
	}{
		{DeliveryStatusPending, true, false, false, false},
		{DeliveryStatusDelivered, false, true, false, false},
		{DeliveryStatusFailed, false, false, true, false},
		{DeliveryStatusDead, false, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			d := &WebhookDelivery{Status: tt.status}
			if got := d.IsPending(); got != tt.isPending {
				t.Errorf("IsPending() = %v, want %v", got, tt.isPending)
			}
			if got := d.IsDelivered(); got != tt.isDelivered {
				t.Errorf("IsDelivered() = %v, want %v", got, tt.isDelivered)
			}
			if got := d.IsFailed(); got != tt.isFailed {
				t.Errorf("IsFailed() = %v, want %v", got, tt.isFailed)
			}
			if got := d.IsDead(); got != tt.isDead {
				t.Errorf("IsDead() = %v, want %v", got, tt.isDead)
			}
		})
	}
}

func TestWebhookDeliveryGetPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantLen int
	}{
		{
			name:    "empty",
			payload: "",
			wantLen: 0,
		},
		{
			name:    "valid JSON",
			payload: `{"event":"page.created","data":{"id":1}}`,
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &WebhookDelivery{Payload: tt.payload}
			got := d.GetPayload()
			if len(got) != tt.wantLen {
				t.Errorf("GetPayload() returned %d fields, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestAllWebhookEvents(t *testing.T) {
	events := AllWebhookEvents()

	// Check that we have all expected events
	expectedTypes := []string{
		EventPageCreated,
		EventPageUpdated,
		EventPageDeleted,
		EventPagePublished,
		EventPageUnpublished,
		EventMediaUploaded,
		EventMediaDeleted,
		EventFormSubmitted,
		EventUserCreated,
		EventUserDeleted,
	}

	if len(events) != len(expectedTypes) {
		t.Errorf("AllWebhookEvents() returned %d events, want %d", len(events), len(expectedTypes))
	}

	for i, evt := range events {
		if evt.Type != expectedTypes[i] {
			t.Errorf("AllWebhookEvents()[%d].Type = %q, want %q", i, evt.Type, expectedTypes[i])
		}
		if evt.Description == "" {
			t.Errorf("AllWebhookEvents()[%d].Description is empty", i)
		}
	}
}
