// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
)

const (
	eventBatchSize  = 1000
	eventRunLimit   = 10000
	eventCountLimit = 10001
	eventRunTimeout = 10 * time.Second
)

type eventProducerGroup struct {
	category string
	messages []string // nil matches the whole category
}

// System and cache include both diagnostics and audit records. Match known
// producers there, including legacy messages, and preserve other messages.
var diagnosticEventProducers = []eventProducerGroup{
	{model.EventCategorySystem, []string{
		"Page not found", "Server started", "Server stopped", "request timed out",
	}},
	{model.EventCategoryCache, []string{
		"failed to preload caches", "failed to create distributed cache",
		"failed to close distributed cache", "failed to clear distributed cache",
		"failed to preload menus cache", "failed to preload language cache",
		"failed to preload sitemap cache", "failed to connect to Redis cache",
	}},
}

var auditEventProducers = []eventProducerGroup{
	{category: model.EventCategoryAuth},
	{category: model.EventCategorySecurity},
	{category: model.EventCategoryUser},
	{category: model.EventCategoryAPIKey},
	{category: model.EventCategoryConfig},
	{category: model.EventCategoryPage},
	{category: model.EventCategoryMedia},
	{category: model.EventCategoryTag},
	{category: model.EventCategoryCategory},
	{category: model.EventCategoryMenu},
	{category: model.EventCategoryWebhook},
	{category: model.EventCategoryMigrator},
	{category: model.EventCategoryScheduler},
	{model.EventCategorySystem, []string{
		"access denied", "CSRF validation failed", "public rate limit exceeded",
		"MCP request rejected: cross-origin browser request",
		"MCP request rejected: API key lacks mcp:access",
	}},
	{model.EventCategoryCache, []string{
		"All caches cleared", "Configuration cache cleared", "Sitemap cache cleared",
		"Pages cache cleared", "Menus cache cleared", "Languages cache cleared",
	}},
}

type eventRetentionRule struct {
	category string
	messages string
	days     int
}

// AddEventCleanup registers retention when enabled and at least one window is
// finite. Skipping registration disables both cron and Run Now.
func (s *Scheduler) AddEventCleanup(enabled bool, diagnosticDays, auditDays int) error {
	if !enabled || (diagnosticDays == 0 && auditDays == 0) {
		return nil
	}
	var rules []eventRetentionRule
	for _, class := range []struct {
		groups []eventProducerGroup
		days   int
	}{{diagnosticEventProducers, diagnosticDays}, {auditEventProducers, auditDays}} {
		if class.days == 0 {
			continue
		}
		for _, group := range class.groups {
			messages, err := json.Marshal(group.messages)
			if err != nil {
				return err
			}
			rules = append(rules, eventRetentionRule{group.category, string(messages), class.days})
		}
	}

	const source, name, defaultSchedule = "core", "event_cleanup", "*/15 * * * *"
	schedule := defaultSchedule
	if s.registry != nil {
		schedule = s.registry.GetEffectiveSchedule(source, name, defaultSchedule)
	}
	queries := store.New(s.db)
	var running sync.Mutex
	run := func() error {
		if !running.TryLock() {
			return errors.New("event retention is already running")
		}
		defer running.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), eventRunTimeout)
		defer cancel()
		deleted, remaining, err := cleanupEvents(ctx, queries, rules, time.Now().UTC())
		if err != nil {
			s.logger.ErrorContext(service.WithoutEventLog(context.Background()), "event retention failed",
				"deleted", deleted, "remaining_known", false, "error", err)
			return err
		}
		s.logger.Info("event retention completed",
			"deleted", deleted, "remaining", remaining, "remaining_capped", remaining == eventCountLimit)
		return nil
	}
	job := func() { _ = run() }
	entry, err := s.cron.AddFunc(schedule, job)
	if err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.Register(source, name, "Remove expired diagnostics and audit events in bounded batches",
			defaultSchedule, s.cron, entry, job, run)
	}
	return nil
}

func cleanupEvents(ctx context.Context, queries *store.Queries, rules []eventRetentionRule, now time.Time) (deleted, remaining int64, err error) {
	pending := make([]store.DeleteExpiredEventsParams, 0, len(rules))
	for _, rule := range rules {
		// Use the UTC whole second without an offset so a legacy SQLite
		// CURRENT_TIMESTAMP exactly at the cutoff is kept too.
		cutoff := now.UTC().AddDate(0, 0, -rule.days).Format(time.DateTime)
		pending = append(pending, store.DeleteExpiredEventsParams{
			Category: rule.category, Cutoff: cutoff, Messages: rule.messages,
		})
	}
	// Give each group a batch per round while sharing the overall row budget.
	for len(pending) > 0 {
		limit := min(eventBatchSize, (eventRunLimit-deleted)/int64(len(pending)))
		if limit == 0 {
			break
		}
		next := pending[:0]
		for _, params := range pending {
			params.BatchLimit = limit
			count, err := queries.DeleteExpiredEvents(ctx, params)
			if err != nil {
				return deleted, 0, err
			}
			deleted += count
			if count == limit {
				next = append(next, params)
			}
		}
		pending = next
	}
	for _, params := range pending {
		count, err := queries.CountExpiredEvents(ctx, store.CountExpiredEventsParams{
			Category: params.Category, Cutoff: params.Cutoff, Messages: params.Messages,
			CountLimit: eventCountLimit - remaining,
		})
		if err != nil {
			return deleted, 0, err
		}
		remaining += count
		if remaining == eventCountLimit {
			break
		}
	}
	return deleted, remaining, nil
}
