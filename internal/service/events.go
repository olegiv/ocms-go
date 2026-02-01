// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

// Package service provides business logic and service layer functionality
// including event logging for audit trails.
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/store"
)

// EventService provides event logging functionality.
type EventService struct {
	queries *store.Queries
}

// NewEventService creates a new EventService.
func NewEventService(db *sql.DB) *EventService {
	return &EventService{
		queries: store.New(db),
	}
}

// LogEvent creates a new event log entry.
func (s *EventService) LogEvent(ctx context.Context, level, category, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	var nullUserID sql.NullInt64
	if userID != nil {
		nullUserID = sql.NullInt64{Int64: *userID, Valid: true}
	}

	metadataJSON := "{}"
	if metadata != nil {
		jsonBytes, err := json.Marshal(metadata)
		if err == nil {
			metadataJSON = string(jsonBytes)
		}
	}

	_, err := s.queries.CreateEvent(ctx, store.CreateEventParams{
		Level:     level,
		Category:  category,
		Message:   message,
		UserID:    nullUserID,
		Metadata:  metadataJSON,
		IpAddress: ipAddress,
		CreatedAt: time.Now(),
	})
	if err != nil {
		log.Printf("Failed to log event: %v", err)
		return err
	}

	return nil
}

// LogInfo logs an info-level event.
func (s *EventService) LogInfo(ctx context.Context, category, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, model.EventLevelInfo, category, message, userID, ipAddress, metadata)
}

// LogWarning logs a warning-level event.
func (s *EventService) LogWarning(ctx context.Context, category, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, model.EventLevelWarning, category, message, userID, ipAddress, metadata)
}

// LogError logs an error-level event.
func (s *EventService) LogError(ctx context.Context, category, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, model.EventLevelError, category, message, userID, ipAddress, metadata)
}

// LogAuthEvent logs an authentication-related event.
func (s *EventService) LogAuthEvent(ctx context.Context, level, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, level, model.EventCategoryAuth, message, userID, ipAddress, metadata)
}

// LogPageEvent logs a page-related event.
func (s *EventService) LogPageEvent(ctx context.Context, level, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, level, model.EventCategoryPage, message, userID, ipAddress, metadata)
}

// LogUserEvent logs a user-related event.
func (s *EventService) LogUserEvent(ctx context.Context, level, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, level, model.EventCategoryUser, message, userID, ipAddress, metadata)
}

// LogConfigEvent logs a config-related event.
func (s *EventService) LogConfigEvent(ctx context.Context, level, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, level, model.EventCategoryConfig, message, userID, ipAddress, metadata)
}

// LogSystemEvent logs a system-related event.
func (s *EventService) LogSystemEvent(ctx context.Context, level, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, level, model.EventCategorySystem, message, userID, ipAddress, metadata)
}

// LogCacheEvent logs a cache-related event.
func (s *EventService) LogCacheEvent(ctx context.Context, level, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, level, model.EventCategoryCache, message, userID, ipAddress, metadata)
}

// LogMigratorEvent logs a migrator-related event.
func (s *EventService) LogMigratorEvent(ctx context.Context, level, message string, userID *int64, ipAddress string, metadata map[string]any) error {
	return s.LogEvent(ctx, level, model.EventCategoryMigrator, message, userID, ipAddress, metadata)
}

// DeleteOldEvents removes events older than the specified duration.
func (s *EventService) DeleteOldEvents(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	return s.queries.DeleteOldEvents(ctx, cutoff)
}
