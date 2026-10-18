// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/olegiv/ocms-go/internal/demo"
	"github.com/olegiv/ocms-go/internal/model"
	"github.com/olegiv/ocms-go/internal/service"
	"github.com/olegiv/ocms-go/internal/store"
	"github.com/olegiv/ocms-go/internal/webhook"
)

// errNoLongerDue reports a page published, unscheduled or rescheduled to a
// later time after processScheduledPages listed it.
var errNoLongerDue = errors.New("page no longer due for publishing")

// Scheduler handles scheduled tasks like publishing pages.
type Scheduler struct {
	db       *sql.DB
	cron     *cron.Cron
	logger   *slog.Logger
	registry *Registry
	webhooks webhook.EventDispatcher
}

// New creates a new scheduler instance.
func New(db *sql.DB, logger *slog.Logger, registry *Registry) *Scheduler {
	return &Scheduler{
		db:       db,
		cron:     cron.New(),
		logger:   logger,
		registry: registry,
	}
}

// SetWebhookDispatcher makes scheduled publishing send the page.published
// webhook event the admin UI's publish toggle sends. Call it before Start.
func (s *Scheduler) SetWebhookDispatcher(d webhook.EventDispatcher) {
	s.webhooks = d
}

// Cron returns the cron instance.
func (s *Scheduler) Cron() *cron.Cron {
	return s.cron
}

// Start begins the scheduler with a job to check for scheduled pages every minute.
func (s *Scheduler) Start() error {
	const (
		defaultSchedule = "* * * * *"
		jobSource       = "core"
		jobName         = "scheduled_pages"
	)

	schedule := defaultSchedule
	if s.registry != nil {
		schedule = s.registry.GetEffectiveSchedule(jobSource, jobName, defaultSchedule)
	}

	jobFunc := func() {
		if err := s.processScheduledPages(); err != nil {
			s.logger.Error("failed to process scheduled pages", "error", err)
		}
	}

	entryID, err := s.cron.AddFunc(schedule, jobFunc)
	if err != nil {
		return err
	}

	if s.registry != nil {
		s.registry.Register(
			jobSource, jobName,
			"Publish pages scheduled for publishing",
			defaultSchedule,
			s.cron, entryID, jobFunc,
			func() error { return s.processScheduledPages() },
		)
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
			if errors.Is(err, errNoLongerDue) {
				s.logger.Info("skipped scheduled page changed since it was listed", "page_id", page.ID)
				continue
			}
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

// publishPage publishes a single scheduled page, logs the event and sends the
// page.published webhook event. It returns errNoLongerDue, and does nothing
// else, when the page is no longer a draft due by now.
func (s *Scheduler) publishPage(ctx context.Context, queries *store.Queries, page store.Page, now time.Time) error {
	published, err := queries.PublishScheduledPage(ctx, store.PublishScheduledPageParams{
		PublishedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt:   now,
		ID:          page.ID,
		ScheduledAt: sql.NullTime{Time: now, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return errNoLongerDue
	}
	if err != nil {
		return err
	}

	// Log the event
	metadata := map[string]any{
		"page_id":      page.ID,
		"page_title":   page.Title,
		"page_slug":    page.Slug,
		"scheduled_at": page.ScheduledAt.Time.Format(time.RFC3339),
		"published_at": now.Format(time.RFC3339),
	}
	metadataJSON, _ := json.Marshal(metadata)

	message := "Page published automatically by scheduler: " + page.Title
	_, err = queries.CreateEvent(ctx, store.CreateEventParams{
		Level:     "info",
		Category:  "page",
		Message:   message,
		UserID:    sql.NullInt64{}, // System action, no user
		Metadata:  string(metadataJSON),
		CreatedAt: now,
	})
	if err != nil {
		// Kept out of the Event Log it failed to write (service.WithoutEventLog).
		s.logger.WarnContext(service.WithoutEventLog(ctx), "failed to log scheduled publish event", "event", message, "error", err)
	}
	if s.webhooks != nil {
		webhook.DispatchPageEvent(ctx, s.webhooks, model.EventPagePublished, published, "")
	}

	return nil
}

// AddDemoReset registers a daily job at 01:00 UTC that resets the demo
// database and uploads, then sends SIGTERM for a clean restart.
func (s *Scheduler) AddDemoReset(dbPath, uploadsDir, dataDir string) error {
	const (
		defaultSchedule = "0 1 * * *"
		jobSource       = "core"
		jobName         = "demo_reset"
	)

	schedule := defaultSchedule
	if s.registry != nil {
		schedule = s.registry.GetEffectiveSchedule(jobSource, jobName, defaultSchedule)
	}

	jobFunc := func() {
		s.logger.Info("starting scheduled demo reset")
		if err := demo.Reset(dbPath, uploadsDir, dataDir); err != nil {
			s.logger.Error("scheduled demo reset failed", "error", err)
			return
		}
		s.logger.Info("demo reset complete, sending SIGTERM for restart")
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGTERM)
	}

	entryID, err := s.cron.AddFunc(schedule, jobFunc)
	if err != nil {
		return err
	}

	if s.registry != nil {
		// No manual trigger for demo reset — it sends SIGTERM
		s.registry.Register(
			jobSource, jobName,
			"Reset demo data daily at 01:00 UTC",
			defaultSchedule,
			s.cron, entryID, jobFunc, nil,
		)
	}

	return nil
}
