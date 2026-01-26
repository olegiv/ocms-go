// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// Dispatcher handles webhook event dispatching and queuing.
type Dispatcher struct {
	db        *sql.DB
	queries   *store.Queries
	logger    *slog.Logger
	queue     chan *QueuedDelivery
	workers   int
	wg        sync.WaitGroup
	done      chan struct{}
	mu        sync.RWMutex
	running   bool
	debouncer *Debouncer // Optional event debouncer for batching rapid-fire events
	config    Config
}

// QueuedDelivery represents a delivery queued for processing.
type QueuedDelivery struct {
	DeliveryID int64
	WebhookID  int64
	Event      string
	Payload    []byte
	URL        string
	Secret     string
	Headers    map[string]string
}

// Config holds dispatcher configuration.
type Config struct {
	Workers        int            // Number of concurrent delivery workers
	EnableDebounce bool           // Enable event debouncing
	Debounce       DebounceConfig // Debounce configuration
}

// DefaultConfig returns default dispatcher configuration.
func DefaultConfig() Config {
	return Config{
		Workers:        3,
		EnableDebounce: true,
		Debounce:       DefaultDebounceConfig(),
	}
}

// NewDispatcher creates a new webhook dispatcher.
func NewDispatcher(db *sql.DB, logger *slog.Logger, cfg Config) *Dispatcher {
	if cfg.Workers <= 0 {
		cfg.Workers = 3
	}
	if logger == nil {
		logger = slog.Default()
	}

	d := &Dispatcher{
		db:      db,
		queries: store.New(db),
		logger:  logger,
		queue:   make(chan *QueuedDelivery, 100),
		workers: cfg.Workers,
		done:    make(chan struct{}),
		config:  cfg,
	}

	// Initialize debouncer if enabled
	if cfg.EnableDebounce {
		d.debouncer = NewDebouncer(d, cfg.Debounce)
	}

	return d
}

// Retry worker configuration
const (
	RetryInterval     = 30 * time.Second    // How often to check for pending deliveries
	RetryBatchSize    = 50                  // Max number of pending deliveries to process per batch
	CleanupInterval   = 24 * time.Hour      // How often to clean up old deliveries
	DeliveryRetention = 30 * 24 * time.Hour // How long to keep delivered/dead entries (30 days)
)

// Start starts the dispatcher workers.
func (d *Dispatcher) Start(ctx context.Context) {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()

	d.logger.Info("starting webhook dispatcher", "workers", d.workers)

	// Start worker goroutines
	for i := 0; i < d.workers; i++ {
		d.wg.Add(1)
		go d.worker(ctx, i)
	}

	// Start retry worker
	d.wg.Add(1)
	go d.retryWorker(ctx)

	// Start cleanup worker
	d.wg.Add(1)
	go d.cleanupWorker(ctx)
}

// Stop stops the dispatcher and waits for workers to finish.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	d.mu.Unlock()

	d.logger.Info("stopping webhook dispatcher")

	// Stop debouncer first to flush pending events
	if d.debouncer != nil {
		d.debouncer.Stop()
	}

	close(d.done)
	d.wg.Wait()
	d.logger.Info("webhook dispatcher stopped")
}

// worker processes queued deliveries.
func (d *Dispatcher) worker(ctx context.Context, id int) {
	defer d.wg.Done()
	d.logger.Debug("webhook worker started", "worker_id", id)

	for {
		select {
		case <-d.done:
			d.logger.Debug("webhook worker stopping", "worker_id", id)
			return
		case <-ctx.Done():
			d.logger.Debug("webhook worker context cancelled", "worker_id", id)
			return
		case delivery := <-d.queue:
			d.logger.Debug("webhook worker processing delivery",
				"worker_id", id,
				"delivery_id", delivery.DeliveryID,
				"event", delivery.Event)
			// Process the delivery (HTTP POST with retry logic)
			d.processDelivery(ctx, delivery)
		}
	}
}

// runTickerWorker runs a periodic task with the given interval.
// It handles shutdown signals and context cancellation.
func (d *Dispatcher) runTickerWorker(ctx context.Context, name string, interval time.Duration, runOnStart bool, task func(context.Context)) {
	defer d.wg.Done()
	d.logger.Debug("webhook worker started", "worker", name)

	if runOnStart {
		task(ctx)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			d.logger.Debug("webhook worker stopping", "worker", name)
			return
		case <-ctx.Done():
			d.logger.Debug("webhook worker context cancelled", "worker", name)
			return
		case <-ticker.C:
			task(ctx)
		}
	}
}

// retryWorker periodically checks for pending deliveries that are ready for retry.
func (d *Dispatcher) retryWorker(ctx context.Context) {
	d.runTickerWorker(ctx, "retry", RetryInterval, false, d.processRetries)
}

// processRetries fetches and processes pending deliveries that are ready for retry.
func (d *Dispatcher) processRetries(ctx context.Context) {
	now := time.Now()

	// Fetch pending deliveries that are ready for retry
	deliveries, err := d.queries.GetPendingDeliveries(ctx, store.GetPendingDeliveriesParams{
		NextRetryAt: sql.NullTime{Time: now, Valid: true},
		Limit:       RetryBatchSize,
	})
	if err != nil {
		d.logger.Error("failed to get pending deliveries", "error", err)
		return
	}

	if len(deliveries) == 0 {
		return
	}

	d.logger.Debug("processing pending deliveries", "count", len(deliveries))

	for _, delivery := range deliveries {
		// Get the webhook to get URL, secret, and headers
		webhook, err := d.queries.GetWebhookByID(ctx, delivery.WebhookID)
		if err != nil {
			d.logger.Error("failed to get webhook for retry",
				"error", err,
				"webhook_id", delivery.WebhookID,
				"delivery_id", delivery.ID)
			continue
		}

		// Check if webhook is still active
		if !webhook.IsActive {
			d.logger.Debug("skipping retry for inactive webhook",
				"webhook_id", webhook.ID,
				"delivery_id", delivery.ID)
			// Mark as dead since webhook is disabled
			_ = d.queries.UpdateDeliveryDead(ctx, store.UpdateDeliveryDeadParams{
				ErrorMessage: sql.NullString{String: "webhook disabled", Valid: true},
				UpdatedAt:    now,
				ID:           delivery.ID,
			})
			continue
		}

		whModel := webhookToModel(webhook)

		// Queue for processing
		qd := &QueuedDelivery{
			DeliveryID: delivery.ID,
			WebhookID:  delivery.WebhookID,
			Event:      delivery.Event,
			Payload:    []byte(delivery.Payload),
			URL:        webhook.Url,
			Secret:     webhook.Secret,
			Headers:    whModel.GetHeaders(),
		}

		select {
		case d.queue <- qd:
			d.logger.Debug("pending delivery queued for retry", "delivery_id", delivery.ID)
		default:
			d.logger.Warn("delivery queue full, skipping retry", "delivery_id", delivery.ID)
		}
	}
}

// cleanupWorker periodically cleans up old delivered and dead deliveries.
func (d *Dispatcher) cleanupWorker(ctx context.Context) {
	d.runTickerWorker(ctx, "cleanup", CleanupInterval, true, d.cleanupOldDeliveries)
}

// cleanupOldDeliveries removes old delivered and dead delivery records.
func (d *Dispatcher) cleanupOldDeliveries(ctx context.Context) {
	cutoff := time.Now().Add(-DeliveryRetention)

	err := d.queries.DeleteOldDeliveries(ctx, cutoff)
	if err != nil {
		d.logger.Error("failed to cleanup old deliveries", "error", err)
		return
	}

	d.logger.Debug("old deliveries cleaned up", "older_than", cutoff.Format(time.RFC3339))
}

// Dispatch dispatches an event to all subscribed webhooks.
func (d *Dispatcher) Dispatch(ctx context.Context, event *Event) error {
	d.mu.RLock()
	running := d.running
	d.mu.RUnlock()

	if !running {
		d.logger.Warn("dispatcher not running, cannot dispatch event", "event_type", event.Type)
		return nil
	}

	// Find all active webhooks subscribed to this event
	webhooks, err := d.queries.ListWebhooksForEvent(ctx, sql.NullString{String: event.Type, Valid: true})
	if err != nil {
		d.logger.Error("failed to list webhooks for event", "error", err, "event_type", event.Type)
		return err
	}

	if len(webhooks) == 0 {
		d.logger.Debug("no webhooks subscribed to event", "event_type", event.Type)
		return nil
	}

	// Serialize the event payload
	payload, err := json.Marshal(event)
	if err != nil {
		d.logger.Error("failed to marshal event payload", "error", err, "event_type", event.Type)
		return err
	}

	now := time.Now()

	// Create delivery records for each webhook
	for _, wh := range webhooks {
		// Verify the webhook is subscribed to this event (double-check since SQL uses LIKE)
		whModel := webhookToModel(wh)
		if !whModel.HasEvent(event.Type) {
			continue
		}

		// Create delivery record
		delivery, err := d.queries.CreateWebhookDelivery(ctx, store.CreateWebhookDeliveryParams{
			WebhookID: wh.ID,
			Event:     event.Type,
			Payload:   string(payload),
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			d.logger.Error("failed to create delivery record",
				"error", err,
				"webhook_id", wh.ID,
				"event_type", event.Type)
			continue
		}

		d.logger.Info("webhook delivery created",
			"delivery_id", delivery.ID,
			"webhook_id", wh.ID,
			"webhook_name", wh.Name,
			"event_type", event.Type)

		// Queue for async processing
		qd := &QueuedDelivery{
			DeliveryID: delivery.ID,
			WebhookID:  wh.ID,
			Event:      event.Type,
			Payload:    payload,
			URL:        wh.Url,
			Secret:     wh.Secret,
			Headers:    whModel.GetHeaders(),
		}

		select {
		case d.queue <- qd:
			d.logger.Debug("delivery queued", "delivery_id", delivery.ID)
		default:
			d.logger.Warn("delivery queue full, delivery will be retried later", "delivery_id", delivery.ID)
		}
	}

	return nil
}

// DispatchEvent is a convenience method to dispatch an event with the given type and data.
// If debouncing is enabled, events will be debounced; otherwise, they are dispatched immediately.
func (d *Dispatcher) DispatchEvent(ctx context.Context, eventType string, data any) error {
	event := NewEvent(eventType, data)
	if d.debouncer != nil {
		return d.debouncer.Dispatch(ctx, event)
	}
	return d.Dispatch(ctx, event)
}

// DispatchImmediate dispatches an event immediately, bypassing debouncing.
// Use this for events that should never be debounced (e.g., delete events).
func (d *Dispatcher) DispatchImmediate(ctx context.Context, eventType string, data any) error {
	return d.Dispatch(ctx, NewEvent(eventType, data))
}

// DebounceStats returns debounce statistics if debouncing is enabled.
func (d *Dispatcher) DebounceStats() (pending int, enabled bool) {
	if d.debouncer != nil {
		return d.debouncer.PendingCount(), true
	}
	return 0, false
}

// GenerateSignature generates an HMAC-SHA256 signature for the payload.
func GenerateSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature verifies an HMAC-SHA256 signature.
func VerifySignature(payload []byte, signature, secret string) bool {
	expectedSig := GenerateSignature(payload, secret)
	return hmac.Equal([]byte(signature), []byte(expectedSig))
}

// webhookToModel converts a store.Webhook to model.Webhook for helper methods.
func webhookToModel(wh store.Webhook) *webhookHelper {
	return &webhookHelper{
		Events:  wh.Events,
		Headers: wh.Headers,
	}
}

// webhookHelper provides helper methods for webhook data.
type webhookHelper struct {
	Events  string
	Headers string
}

// GetEvents parses the JSON events string into a slice.
func (w *webhookHelper) GetEvents() []string {
	var events []string
	if w.Events == "" || w.Events == "[]" {
		return events
	}
	_ = json.Unmarshal([]byte(w.Events), &events)
	return events
}

// HasEvent checks if the webhook is subscribed to a specific event.
func (w *webhookHelper) HasEvent(event string) bool {
	for _, e := range w.GetEvents() {
		if e == event {
			return true
		}
	}
	return false
}

// GetHeaders parses the JSON headers string into a map.
func (w *webhookHelper) GetHeaders() map[string]string {
	headers := make(map[string]string)
	if w.Headers == "" || w.Headers == "{}" {
		return headers
	}
	_ = json.Unmarshal([]byte(w.Headers), &headers)
	return headers
}
