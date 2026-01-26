// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package handler

import (
	"context"
	"testing"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

func TestNewWebhooksHandler(t *testing.T) {
	db, sm := testHandlerSetup(t)

	h := NewWebhooksHandler(db, nil, sm)
	if h == nil {
		t.Fatal("NewWebhooksHandler returned nil")
	}
	if h.queries == nil {
		t.Error("queries should not be nil")
	}
}

func TestWebhookCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	webhook, err := queries.CreateWebhook(context.Background(), store.CreateWebhookParams{
		Name:      "Test Webhook",
		Url:       "https://example.com/webhook",
		Secret:    "secret123",
		Events:    `["page.created", "page.updated"]`,
		IsActive:  true,
		Headers:   "{}",
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}

	if webhook.Name != "Test Webhook" {
		t.Errorf("Name = %q, want %q", webhook.Name, "Test Webhook")
	}
	if webhook.Url != "https://example.com/webhook" {
		t.Errorf("Url = %q, want %q", webhook.Url, "https://example.com/webhook")
	}
	if !webhook.IsActive {
		t.Error("IsActive should be true")
	}
}

func TestWebhookList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	// Create test webhooks
	for i := 1; i <= 3; i++ {
		_, err := queries.CreateWebhook(context.Background(), store.CreateWebhookParams{
			Name:      "Webhook " + string(rune('A'+i-1)),
			Url:       "https://example.com/hook" + string(rune('0'+i)),
			Secret:    "secret",
			Events:    `[]`,
			IsActive:  true,
			Headers:   "{}",
			CreatedBy: user.ID,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("CreateWebhook failed: %v", err)
		}
	}

	t.Run("list all", func(t *testing.T) {
		webhooks, err := queries.ListWebhooks(context.Background())
		if err != nil {
			t.Fatalf("ListWebhooks failed: %v", err)
		}
		if len(webhooks) != 3 {
			t.Errorf("got %d webhooks, want 3", len(webhooks))
		}
	})

	t.Run("count", func(t *testing.T) {
		count, err := queries.CountWebhooks(context.Background())
		if err != nil {
			t.Fatalf("CountWebhooks failed: %v", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3", count)
		}
	})
}

func TestWebhookUpdate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	webhook, err := queries.CreateWebhook(context.Background(), store.CreateWebhookParams{
		Name:      "Original Webhook",
		Url:       "https://example.com/original",
		Secret:    "secret",
		Events:    `[]`,
		IsActive:  true,
		Headers:   "{}",
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}

	_, err = queries.UpdateWebhook(context.Background(), store.UpdateWebhookParams{
		ID:        webhook.ID,
		Name:      "Updated Webhook",
		Url:       "https://example.com/updated",
		Secret:    "newsecret",
		Events:    `["page.deleted"]`,
		IsActive:  false,
		Headers:   "{}",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateWebhook failed: %v", err)
	}

	updated, err := queries.GetWebhookByID(context.Background(), webhook.ID)
	if err != nil {
		t.Fatalf("GetWebhookByID failed: %v", err)
	}

	if updated.Name != "Updated Webhook" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated Webhook")
	}
	if updated.IsActive {
		t.Error("IsActive should be false")
	}
}

func TestWebhookDelete(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	webhook, err := queries.CreateWebhook(context.Background(), store.CreateWebhookParams{
		Name:      "To Delete Webhook",
		Url:       "https://example.com/delete",
		Secret:    "secret",
		Events:    `[]`,
		IsActive:  true,
		Headers:   "{}",
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}

	if err := queries.DeleteWebhook(context.Background(), webhook.ID); err != nil {
		t.Fatalf("DeleteWebhook failed: %v", err)
	}

	_, err = queries.GetWebhookByID(context.Background(), webhook.ID)
	if err == nil {
		t.Error("expected error when getting deleted webhook")
	}
}

func TestWebhookDeliveryCreate(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	webhook, err := queries.CreateWebhook(context.Background(), store.CreateWebhookParams{
		Name:      "Delivery Test Webhook",
		Url:       "https://example.com/delivery",
		Secret:    "secret",
		Events:    `[]`,
		IsActive:  true,
		Headers:   "{}",
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}

	now2 := time.Now()
	delivery, err := queries.CreateWebhookDelivery(context.Background(), store.CreateWebhookDeliveryParams{
		WebhookID: webhook.ID,
		Event:     "page.created",
		Payload:   `{"id": 1, "title": "Test"}`,
		CreatedAt: now2,
		UpdatedAt: now2,
	})
	if err != nil {
		t.Fatalf("CreateWebhookDelivery failed: %v", err)
	}

	if delivery.WebhookID != webhook.ID {
		t.Errorf("WebhookID = %d, want %d", delivery.WebhookID, webhook.ID)
	}
	if delivery.Event != "page.created" {
		t.Errorf("Event = %q, want %q", delivery.Event, "page.created")
	}
}

func TestWebhookDeliveryList(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	webhook, err := queries.CreateWebhook(context.Background(), store.CreateWebhookParams{
		Name:      "List Deliveries Webhook",
		Url:       "https://example.com/list",
		Secret:    "secret",
		Events:    `[]`,
		IsActive:  true,
		Headers:   "{}",
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}

	// Create deliveries
	for i := 0; i < 5; i++ {
		deliveryTime := time.Now()
		_, err := queries.CreateWebhookDelivery(context.Background(), store.CreateWebhookDeliveryParams{
			WebhookID: webhook.ID,
			Event:     "page.created",
			Payload:   `{}`,
			CreatedAt: deliveryTime,
			UpdatedAt: deliveryTime,
		})
		if err != nil {
			t.Fatalf("CreateWebhookDelivery failed: %v", err)
		}
	}

	deliveries, err := queries.ListWebhookDeliveries(context.Background(), store.ListWebhookDeliveriesParams{
		WebhookID: webhook.ID,
		Limit:     100,
		Offset:    0,
	})
	if err != nil {
		t.Fatalf("ListWebhookDeliveries failed: %v", err)
	}

	if len(deliveries) != 5 {
		t.Errorf("got %d deliveries, want 5", len(deliveries))
	}
}

func TestWebhookToggleActive(t *testing.T) {
	db, _ := testHandlerSetup(t)

	user := createTestAdminUser(t, db)

	queries := store.New(db)
	now := time.Now()

	webhook, err := queries.CreateWebhook(context.Background(), store.CreateWebhookParams{
		Name:      "Toggle Webhook",
		Url:       "https://example.com/toggle",
		Secret:    "secret",
		Events:    `[]`,
		IsActive:  true,
		Headers:   "{}",
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateWebhook failed: %v", err)
	}

	// Toggle to inactive using UpdateWebhook
	_, err = queries.UpdateWebhook(context.Background(), store.UpdateWebhookParams{
		ID:        webhook.ID,
		Name:      webhook.Name,
		Url:       webhook.Url,
		Secret:    webhook.Secret,
		Events:    webhook.Events,
		IsActive:  false,
		Headers:   webhook.Headers,
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateWebhook failed: %v", err)
	}

	updated, err := queries.GetWebhookByID(context.Background(), webhook.ID)
	if err != nil {
		t.Fatalf("GetWebhookByID failed: %v", err)
	}

	if updated.IsActive {
		t.Error("IsActive should be false after toggle")
	}
}
