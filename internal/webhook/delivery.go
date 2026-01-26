// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/olegiv/ocms-go/internal/store"
)

// Delivery configuration constants
const (
	MaxAttempts    = 5                // Maximum number of delivery attempts
	InitialBackoff = 1 * time.Minute  // Initial backoff delay
	MaxBackoff     = 24 * time.Hour   // Maximum backoff delay
	RequestTimeout = 30 * time.Second // HTTP request timeout
	MaxResponseLen = 10 * 1024        // Maximum response body to store (10KB)
	UserAgent      = "oCMS/1.0"       // User-Agent header value
)

// DeliveryResult represents the result of a delivery attempt.
type DeliveryResult struct {
	Success      bool
	StatusCode   int
	ResponseBody string
	Error        error
	ShouldRetry  bool
}

// httpClient is the shared HTTP client with appropriate timeouts.
var httpClient = &http.Client{
	Timeout: RequestTimeout,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

// processDelivery attempts to deliver a webhook payload via HTTP POST.
func (d *Dispatcher) processDelivery(ctx context.Context, delivery *QueuedDelivery) {
	// Get the current delivery record to check attempts
	record, err := d.queries.GetWebhookDelivery(ctx, delivery.DeliveryID)
	if err != nil {
		d.logger.Error("failed to get delivery record",
			"error", err,
			"delivery_id", delivery.DeliveryID)
		return
	}

	// Check if already delivered or dead
	if record.Status == "delivered" || record.Status == "dead" {
		d.logger.Debug("delivery already processed",
			"delivery_id", delivery.DeliveryID,
			"status", record.Status)
		return
	}

	// Attempt the HTTP delivery
	result := d.attemptDelivery(ctx, delivery)
	now := time.Now()

	if result.Success {
		// Delivery succeeded
		err = d.queries.UpdateDeliverySuccess(ctx, store.UpdateDeliverySuccessParams{
			ResponseCode: sql.NullInt64{Int64: int64(result.StatusCode), Valid: true},
			ResponseBody: sql.NullString{String: result.ResponseBody, Valid: true},
			DeliveredAt:  sql.NullTime{Time: now, Valid: true},
			UpdatedAt:    now,
			ID:           delivery.DeliveryID,
		})
		if err != nil {
			d.logger.Error("failed to update delivery success",
				"error", err,
				"delivery_id", delivery.DeliveryID)
		} else {
			d.logger.Info("webhook delivered successfully",
				"delivery_id", delivery.DeliveryID,
				"webhook_id", delivery.WebhookID,
				"status_code", result.StatusCode)
		}
		return
	}

	// Delivery failed
	newAttempts := record.Attempts + 1

	if !result.ShouldRetry || newAttempts >= MaxAttempts {
		// Mark as dead - no more retries
		errMsg := ""
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		err = d.queries.UpdateDeliveryDead(ctx, store.UpdateDeliveryDeadParams{
			ErrorMessage: sql.NullString{String: errMsg, Valid: errMsg != ""},
			UpdatedAt:    now,
			ID:           delivery.DeliveryID,
		})
		if err != nil {
			d.logger.Error("failed to update delivery as dead",
				"error", err,
				"delivery_id", delivery.DeliveryID)
		} else {
			d.logger.Warn("webhook delivery marked as dead",
				"delivery_id", delivery.DeliveryID,
				"webhook_id", delivery.WebhookID,
				"attempts", newAttempts,
				"reason", errMsg)
		}
		return
	}

	// Schedule retry with exponential backoff
	backoff := calculateBackoff(newAttempts)
	nextRetry := now.Add(backoff)
	errMsg := ""
	if result.Error != nil {
		errMsg = result.Error.Error()
	}

	err = d.queries.UpdateDeliveryRetry(ctx, store.UpdateDeliveryRetryParams{
		ResponseCode: sql.NullInt64{Int64: int64(result.StatusCode), Valid: result.StatusCode > 0},
		ResponseBody: sql.NullString{String: result.ResponseBody, Valid: result.ResponseBody != ""},
		ErrorMessage: sql.NullString{String: errMsg, Valid: errMsg != ""},
		NextRetryAt:  sql.NullTime{Time: nextRetry, Valid: true},
		UpdatedAt:    now,
		ID:           delivery.DeliveryID,
	})
	if err != nil {
		d.logger.Error("failed to schedule delivery retry",
			"error", err,
			"delivery_id", delivery.DeliveryID)
	} else {
		d.logger.Info("webhook delivery scheduled for retry",
			"delivery_id", delivery.DeliveryID,
			"webhook_id", delivery.WebhookID,
			"attempt", newAttempts,
			"next_retry_at", nextRetry.Format(time.RFC3339),
			"backoff", backoff.String())
	}
}

// attemptDelivery performs the actual HTTP POST request.
func (d *Dispatcher) attemptDelivery(ctx context.Context, delivery *QueuedDelivery) DeliveryResult {
	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URL, bytes.NewReader(delivery.Payload))
	if err != nil {
		return DeliveryResult{
			Success:     false,
			Error:       fmt.Errorf("failed to create request: %w", err),
			ShouldRetry: false, // Bad URL, don't retry
		}
	}

	// Set standard headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)

	// Generate and set signature header
	signature := GenerateSignature(delivery.Payload, delivery.Secret)
	req.Header.Set("X-Webhook-Signature", signature)
	req.Header.Set("X-Webhook-Event", delivery.Event)
	req.Header.Set("X-Webhook-Delivery-ID", fmt.Sprintf("%d", delivery.DeliveryID))

	// Set custom headers from webhook configuration
	for key, value := range delivery.Headers {
		req.Header.Set(key, value)
	}

	// Send request
	resp, err := httpClient.Do(req)
	if err != nil {
		return DeliveryResult{
			Success:     false,
			Error:       fmt.Errorf("request failed: %w", err),
			ShouldRetry: true, // Network error, retry
		}
	}
	if resp == nil {
		return DeliveryResult{
			Success:     false,
			Error:       fmt.Errorf("nil response from server"),
			ShouldRetry: true,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body (limited to MaxResponseLen)
	bodyReader := io.LimitReader(resp.Body, MaxResponseLen)
	body, _ := io.ReadAll(bodyReader)
	responseBody := string(body)

	// Check response status
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Success
		return DeliveryResult{
			Success:      true,
			StatusCode:   resp.StatusCode,
			ResponseBody: responseBody,
		}
	}

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// Client error - don't retry (except for 408 Request Timeout and 429 Too Many Requests)
		shouldRetry := resp.StatusCode == 408 || resp.StatusCode == 429
		return DeliveryResult{
			Success:      false,
			StatusCode:   resp.StatusCode,
			ResponseBody: responseBody,
			Error:        fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
			ShouldRetry:  shouldRetry,
		}
	}

	// Server error (5xx) - retry
	return DeliveryResult{
		Success:      false,
		StatusCode:   resp.StatusCode,
		ResponseBody: responseBody,
		Error:        fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		ShouldRetry:  true,
	}
}

// calculateBackoff calculates the exponential backoff duration for a given attempt.
// Attempt 1 = 1 min, Attempt 2 = 2 min, Attempt 3 = 4 min, Attempt 4 = 8 min, etc.
func calculateBackoff(attempt int64) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}

	// Calculate backoff: InitialBackoff * 2^(attempt-1)
	backoff := time.Duration(float64(InitialBackoff) * math.Pow(2, float64(attempt-1)))

	// Cap at MaxBackoff
	if backoff > MaxBackoff {
		backoff = MaxBackoff
	}

	return backoff
}
