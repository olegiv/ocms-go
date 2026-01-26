// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/store"
)

// Scheduler handles scheduled tasks like publishing pages.
type Scheduler struct {
	db     *sql.DB
	cron   *cron.Cron
	logger *slog.Logger
}

// New creates a new scheduler instance.
func New(db *sql.DB, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		db:     db,
		cron:   cron.New(),
		logger: logger,
	}
}

// Start begins the scheduler with a job to check for scheduled pages every minute.
func (s *Scheduler) Start() error {
	// Run every minute
	_, err := s.cron.AddFunc("* * * * *", func() {
		if err := s.processScheduledPages(); err != nil {
			s.logger.Error("failed to process scheduled pages", "error", err)
		}
	})
	if err != nil {
		return err
	}

	s.cron.Start()
	s.logger.Info("scheduler started", "jobs", len(s.cron.Entries()))
	return nil
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.logger.Info("scheduler stopped")
}

// processScheduledPages checks for pages due for publishing and publishes them.
func (s *Scheduler) processScheduledPages() error {
	ctx := context.Background()
	queries := store.New(s.db)

	// Get pages scheduled to be published up to now
	now := time.Now()
	pages, err := queries.GetScheduledPagesForPublishing(ctx, sql.NullTime{
		Time:  now,
		Valid: true,
	})
	if err != nil {
		return err
	}

	if len(pages) == 0 {
		return nil
	}

	s.logger.Info("processing scheduled pages", "count", len(pages))

	for _, page := range pages {
		if err := s.publishPage(ctx, queries, page, now); err != nil {
			s.logger.Error("failed to publish scheduled page",
				"page_id", page.ID,
				"page_title", page.Title,
				"error", err,
			)
			continue
		}

		s.logger.Info("published scheduled page",
			"page_id", page.ID,
			"page_title", page.Title,
			"scheduled_at", page.ScheduledAt.Time,
		)
	}

	return nil
}

// publishPage publishes a single scheduled page and logs the event.
func (s *Scheduler) publishPage(ctx context.Context, queries *store.Queries, page store.Page, now time.Time) error {
	// Publish the page
	_, err := queries.PublishScheduledPage(ctx, store.PublishScheduledPageParams{
		PublishedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt:   now,
		ID:          page.ID,
	})
	if err != nil {
		return err
	}

	// Log the event
	metadata := map[string]interface{}{
		"page_id":      page.ID,
		"page_title":   page.Title,
		"page_slug":    page.Slug,
		"scheduled_at": page.ScheduledAt.Time.Format(time.RFC3339),
		"published_at": now.Format(time.RFC3339),
	}
	metadataJSON, _ := json.Marshal(metadata)

	_, err = queries.CreateEvent(ctx, store.CreateEventParams{
		Level:     "info",
		Category:  "page",
		Message:   "Page published automatically by scheduler: " + page.Title,
		UserID:    sql.NullInt64{}, // System action, no user
		Metadata:  string(metadataJSON),
		CreatedAt: now,
	})
	if err != nil {
		s.logger.Warn("failed to log scheduled publish event", "error", err)
	}

	return nil
}
