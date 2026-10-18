// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"
	"sync"
)

// WebhookEvent is a webhook event a WebhookRecorder recorded.
type WebhookEvent struct {
	Type string
	Data any
}

// WebhookRecorder records the webhook events sent through it; it satisfies
// webhook.EventDispatcher.
type WebhookRecorder struct {
	mu     sync.Mutex
	events []WebhookEvent
}

// DispatchEvent records the event.
func (r *WebhookRecorder) DispatchEvent(_ context.Context, eventType string, data any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, WebhookEvent{Type: eventType, Data: data})
	return nil
}

// Events returns the events recorded so far.
func (r *WebhookRecorder) Events() []WebhookEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]WebhookEvent(nil), r.events...)
}
